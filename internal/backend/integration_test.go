package backend

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func fixture(t *testing.T) (Repository, string, context.Context) {
	t.Helper()
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("install restic 0.18.1 to run integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(root, "password")
	if err := os.WriteFile(password, []byte("disposable-test-password\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r := Repository{Path: filepath.Join(root, "repo"), PasswordFile: password, Out: io.Discard, Err: os.Stderr}
	if err := r.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Execute(ctx, "init", "--repository-version", "2"); err != nil {
		t.Fatal(err)
	}
	return r, source, ctx
}
func mustWrite(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func hashFile(t *testing.T, p string) [32]byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return sha256.Sum256(b)
}

func TestBackupRecovery(t *testing.T) {
	r, src, ctx := fixture(t)
	data := make([]byte, 8<<20)
	for i := range data {
		data[i] = byte(i % 251)
	}
	for _, name := range []string{"large.bin", "duplicate.bin"} {
		mustWrite(t, filepath.Join(src, name), data)
	}
	mustWrite(t, filepath.Join(src, "empty"), nil)
	mustWrite(t, filepath.Join(src, "日本語.txt"), []byte("recover me"))
	if e := os.Mkdir(filepath.Join(src, "empty-dir"), 0700); e != nil {
		t.Fatal(e)
	}
	if runtime.GOOS != "windows" {
		if e := os.Mkdir(filepath.Join(src, "sub"), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink("../日本語.txt", filepath.Join(src, "sub", "link")); e != nil {
			t.Fatal(e)
		}
	}
	if e := r.Backup(ctx, BackupOptions{Sources: []string{src}}); e != nil {
		t.Fatal(e)
	}
	first, e := r.Resolve(ctx, "latest")
	if e != nil {
		t.Fatal(e)
	}
	recorder := &summaryWriter{}
	repeatRepo := r
	repeatRepo.JSON = true
	repeatRepo.Out = recorder
	if e = repeatRepo.Backup(ctx, BackupOptions{Sources: []string{src}}); e != nil {
		t.Fatal(e)
	}
	if recorder.ID == "" || recorder.NewData != 0 {
		t.Fatalf("repeated backup must confirm snapshot and upload no new file chunks: %+v", recorder)
	}
	mustWrite(t, filepath.Join(src, "日本語.txt"), []byte("changed"))
	if e = os.Remove(filepath.Join(src, "empty")); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(filepath.Join(src, "duplicate.bin"), filepath.Join(src, "renamed.bin")); e != nil {
		t.Fatal(e)
	}
	if e = r.Backup(ctx, BackupOptions{Sources: []string{src}}); e != nil {
		t.Fatal(e)
	}
	if e = r.Check(ctx, true); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(filepath.Dir(src), "restore")
	if e = r.Restore(ctx, RestoreOptions{Snapshot: first.ID, Target: target}); e != nil {
		t.Fatal(e)
	}
	// Restic preserves the source's absolute hierarchy beneath the restore root.
	var large, unicode, link string
	e = filepath.WalkDir(target, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		switch d.Name() {
		case "large.bin":
			large = p
		case "日本語.txt":
			unicode = p
		case "link":
			link = p
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if hashFile(t, large) != sha256.Sum256(data) {
		t.Fatal("restored large-file hash mismatch")
	}
	if hashFile(t, unicode) != sha256.Sum256([]byte("recover me")) {
		t.Fatal("restored old content differs")
	}
	if runtime.GOOS != "windows" {
		v, e := os.ReadFile(link)
		if e != nil || string(v) != "recover me" {
			t.Fatalf("relative symlink restore: %q, %v", v, e)
		}
	}
	if e = r.Restore(ctx, RestoreOptions{Snapshot: first.ID, Target: target}); e == nil {
		t.Fatal("existing restore target accepted")
	}
	selectTarget := filepath.Join(filepath.Dir(src), "selective")
	if e = r.Restore(ctx, RestoreOptions{Snapshot: first.ID, Target: selectTarget, Include: []string{"**/large.bin"}}); e != nil {
		t.Fatal(e)
	}
	count := 0
	_ = filepath.WalkDir(selectTarget, func(p string, d os.DirEntry, e error) error {
		if e == nil && d.Type().IsRegular() {
			count++
			if d.Name() != "large.bin" {
				t.Errorf("unexpected recovered file %s", p)
			}
		}
		return e
	})
	if count != 1 {
		t.Fatalf("selective restore file count=%d", count)
	}
	wrong := filepath.Join(filepath.Dir(src), "wrong")
	mustWrite(t, wrong, []byte("wrong-password"))
	bad := r
	bad.PasswordFile = wrong
	if e = bad.Check(ctx, false); e == nil {
		t.Fatal("wrong password accepted")
	}
	if e = r.Forget(ctx, []string{first.ID}, false); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Resolve(ctx, first.ID); e != nil {
		t.Fatal("deletion preview deleted snapshot")
	}
	if e = r.Forget(ctx, []string{first.ID}, true); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Resolve(ctx, first.ID); e == nil {
		t.Fatal("forgotten snapshot remains")
	}
	if e = r.Execute(ctx, "prune"); e != nil {
		t.Fatal(e)
	}
	if e = r.Check(ctx, true); e != nil {
		t.Fatal(e)
	}
}

func TestBackupGuards(t *testing.T) {
	r, src, ctx := fixture(t)
	nested := r
	nested.Path = filepath.Join(src, "repo")
	if e := nested.Backup(ctx, BackupOptions{Sources: []string{src}}); e == nil {
		t.Fatal("recursive backup allowed")
	}
	inside := filepath.Join(src, "password")
	mustWrite(t, inside, []byte("secret"))
	r.PasswordFile = inside
	if e := r.Backup(ctx, BackupOptions{Sources: []string{src}}); e == nil {
		t.Fatal("password source inclusion allowed")
	}
	if runtime.GOOS != "windows" {
		r.PasswordFile = filepath.Join(filepath.Dir(src), "public-password")
		if e := os.WriteFile(r.PasswordFile, []byte("secret"), 0644); e != nil {
			t.Fatal(e)
		}
		if e := os.Chmod(r.PasswordFile, 0644); e != nil {
			t.Fatal(e)
		}
		if e := r.Validate(ctx); e == nil {
			t.Fatal("public password file accepted")
		}
	}
}

func TestCorruptionDetected(t *testing.T) {
	r, src, ctx := fixture(t)
	mustWrite(t, filepath.Join(src, "file"), []byte("important contents"))
	if e := r.Backup(ctx, BackupOptions{Sources: []string{src}}); e != nil {
		t.Fatal(e)
	}
	var pack string
	e := filepath.WalkDir(filepath.Join(r.Path, "data"), func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type().IsRegular() {
			pack = p
		}
		return nil
	})
	if e != nil || pack == "" {
		t.Fatalf("pack missing: %v", e)
	}
	b, e := os.ReadFile(pack)
	if e != nil {
		t.Fatal(e)
	}
	b[len(b)/2] ^= 0xff
	if e = os.Chmod(pack, 0600); e != nil {
		t.Fatal(e)
	}
	mustWrite(t, pack, b)
	if e = r.Check(ctx, true); e == nil {
		t.Fatal("corrupted pack passed full check")
	}
	if e = os.Remove(pack); e != nil {
		t.Fatal(e)
	}
	if e = r.Check(ctx, true); e == nil {
		t.Fatal("missing pack passed full check")
	}
}
