package backend

import (
	"context"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestStorageFailuresAndConcurrency(t *testing.T) {
	r, src, ctx := fixture(t)
	mustWrite(t, filepath.Join(src, "safe"), []byte("preserve this"))
	if e := r.Backup(ctx, BackupOptions{Sources: []string{src}}); e != nil {
		t.Fatal(e)
	}
	original, e := r.Resolve(ctx, "latest")
	if e != nil {
		t.Fatal(e)
	}
	if runtime.GOOS != "windows" {
		denied := filepath.Join(src, "denied")
		mustWrite(t, denied, []byte("not readable"))
		if e = os.Chmod(denied, 0000); e != nil {
			t.Fatal(e)
		}
		e = r.Backup(ctx, BackupOptions{Sources: []string{src}})
		if chmodErr := os.Chmod(denied, 0600); chmodErr != nil {
			t.Fatal(chmodErr)
		}
		if e == nil {
			t.Fatal("unreadable file was silently accepted")
		}
	}
	// Renaming a disposable destination simulates an unavailable external mount.
	moved := r.Path + "-disconnected"
	if e = os.Rename(r.Path, moved); e != nil {
		t.Fatal(e)
	}
	e = r.Backup(ctx, BackupOptions{Sources: []string{src}})
	if restoreErr := os.Rename(moved, r.Path); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if e == nil {
		t.Fatal("disconnected repository accepted")
	}
	if _, e = r.Resolve(ctx, original.ID); e != nil {
		t.Fatal("previous snapshot lost after destination failure")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- r.Backup(ctx, BackupOptions{Sources: []string{src}}) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = r.Check(ctx, true); e != nil {
		t.Fatal(e)
	}
}

func TestInterruptedBackup(t *testing.T) {
	r, src, ctx := fixture(t)
	mustWrite(t, filepath.Join(src, "safe"), []byte("previous snapshot"))
	if e := r.Backup(ctx, BackupOptions{Sources: []string{src}}); e != nil {
		t.Fatal(e)
	}
	original, e := r.Resolve(ctx, "latest")
	if e != nil {
		t.Fatal(e)
	}
	packs := func() map[string]bool {
		result := map[string]bool{}
		_ = filepath.WalkDir(filepath.Join(r.Path, "data"), func(p string, d os.DirEntry, e error) error {
			if e == nil && d.Type().IsRegular() {
				result[p] = true
			}
			return e
		})
		return result
	}
	before := packs()
	f, e := os.Create(filepath.Join(src, "large-random"))
	if e != nil {
		t.Fatal(e)
	}
	_, copyErr := io.CopyN(f, rand.Reader, 64<<20)
	closeErr := f.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	interrupted, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Backup(interrupted, BackupOptions{Sources: []string{src}}) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	canceled := false
	for !canceled {
		select {
		case e := <-done:
			t.Fatalf("backup completed before interruption could be injected: %v", e)
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("backup did not begin writing data")
		case <-ticker.C:
			for p := range packs() {
				if !before[p] {
					cancel()
					canceled = true
					break
				}
			}
		}
	}
	if e = <-done; e == nil {
		t.Fatal("interrupted backup reported success")
	}
	// Killing the backend can retain a stale lock. This test owns every process
	// using the disposable repository, so explicit backend stale-lock cleanup is safe.
	if e = r.Execute(ctx, "unlock"); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Resolve(ctx, original.ID); e != nil {
		t.Fatal("interruption lost previous snapshot")
	}
	if e = r.Check(ctx, true); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(src, "large-random")); e != nil {
		t.Fatal(e)
	}
	if e = r.Backup(ctx, BackupOptions{Sources: []string{src}}); e != nil {
		t.Fatal(e)
	}
}

func TestInterruptedRestore(t *testing.T) {
	r, src, ctx := fixture(t)
	f, e := os.Create(filepath.Join(src, "large"))
	if e != nil {
		t.Fatal(e)
	}
	_, copyErr := io.CopyN(f, rand.Reader, 64<<20)
	closeErr := f.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	originalHash := hashFile(t, filepath.Join(src, "large"))
	if e = r.Backup(ctx, BackupOptions{Sources: []string{src}}); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(filepath.Dir(src), "partial")
	interrupted, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Restore(interrupted, RestoreOptions{Snapshot: "latest", Target: target}) }()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	canceled := false
	for !canceled {
		select {
		case e := <-done:
			t.Fatalf("restore completed before interruption could be injected: %v", e)
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("restore did not begin extracting")
		case <-ticker.C:
			if _, e := os.Stat(filepath.Join(target, "large")); e == nil {
				cancel()
				canceled = true
			}
		}
	}
	if e = <-done; e == nil {
		t.Fatal("interrupted restore reported success")
	}
	if _, e = os.Stat(target); e != nil {
		t.Fatal("partial restore was discarded")
	}
	if hashFile(t, filepath.Join(src, "large")) != originalHash {
		t.Fatal("restore modified source data")
	}
	if e = r.Execute(ctx, "unlock"); e != nil {
		t.Fatal(e)
	}
	if e = r.Check(ctx, true); e != nil {
		t.Fatal(e)
	}
}
