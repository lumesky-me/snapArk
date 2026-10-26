package backend

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestSourceChangesDuringBackup(t *testing.T) {
	r, src, ctx := fixture(t)
	mustWrite(t, filepath.Join(src, "stable"), []byte("unchanged project data"))
	complete := "veyra:test-live-source:complete"
	previous, e := r.VerifiedBackup(ctx, BackupOptions{Sources: []string{src}}, complete, false)
	if e != nil {
		t.Fatal(e)
	}
	live := filepath.Join(src, "live binary")
	f, e := os.Create(live)
	if e != nil {
		t.Fatal(e)
	}
	_, e = io.CopyN(f, rand.Reader, 32<<20)
	e = errors.Join(e, f.Close())
	if e != nil {
		t.Fatal(e)
	}
	stop := make(chan struct{})
	changed := make(chan error, 1)
	go func() {
		file, err := os.OpenFile(live, os.O_WRONLY, 0)
		if err != nil {
			changed <- err
			return
		}
		defer file.Close()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for n := 0; ; n++ {
			if _, err = file.WriteAt(bytes.Repeat([]byte{byte('A' + n%2)}, 4096), (32<<20)-4096); err != nil {
				changed <- err
				return
			}
			select {
			case <-stop:
				changed <- nil
				return
			case <-ticker.C:
			}
		}
	}()
	id, backupErr := r.VerifiedBackup(ctx, BackupOptions{Sources: []string{src}}, complete, false)
	close(stop)
	if e = <-changed; e != nil {
		t.Fatal(e)
	}
	if backupErr != nil {
		if id != "" {
			t.Fatal("failed live-source backup reported completion", id)
		}
		snapshots, err := r.Snapshots(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, snapshot := range snapshots {
			for _, tag := range snapshot.Tags {
				if tag == complete && snapshot.ID != previous {
					t.Fatal("failed live-source backup was certified complete")
				}
			}
		}
	} else {
		target := filepath.Join(filepath.Dir(src), "live recovery")
		if e = r.Restore(ctx, RestoreOptions{Snapshot: id, Target: target}); e != nil {
			t.Fatal(e)
		}
		if hashFile(t, filepath.Join(target, "stable")) != hashFile(t, filepath.Join(src, "stable")) {
			t.Fatal("live-source backup damaged unrelated stable contents")
		}
	}
	// Live application consistency is not promised. Once the writer stops, the
	// next incremental snapshot must recover the exact final file contents.
	expected := hashFile(t, live)
	id, e = r.VerifiedBackup(ctx, BackupOptions{Sources: []string{src}}, complete, false)
	if e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(filepath.Dir(src), "quiescent recovery")
	if e = r.Restore(ctx, RestoreOptions{Snapshot: id, Target: target}); e != nil {
		t.Fatal(e)
	}
	if hashFile(t, filepath.Join(target, "live binary")) != expected {
		t.Fatal("incremental backup did not capture final source contents")
	}
	if _, e = r.Resolve(ctx, previous); e != nil {
		t.Fatal("live source changes lost the previous backup", e)
	}
}

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
	if _, markerErr := os.Stat(target + ".veyra-incomplete.json"); markerErr != nil {
		t.Fatal("interrupted restore has no incomplete marker", markerErr)
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

func TestFailedProfileBackupHasNoCompletionTag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission denial; backend exit failures are tested portably")
	}
	r, src, ctx := fixture(t)
	denied := filepath.Join(src, "unreadable")
	mustWrite(t, denied, []byte("must not silently omit"))
	if e := os.Chmod(denied, 0000); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(denied, 0600)
	complete := "veyra:test-profile:complete"
	id, e := r.VerifiedBackup(ctx, BackupOptions{Sources: []string{src}}, complete, true)
	if e == nil || id != "" {
		t.Fatal("partial backup was certified complete", id, e)
	}
	snapshots, e := r.Snapshots(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range snapshots {
		for _, tag := range s.Tags {
			if tag == complete {
				t.Fatal("failed snapshot received completion tag")
			}
		}
	}
}
