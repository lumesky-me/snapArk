package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Junctions do not require symbolic-link privileges. Exercise Windows' actual
// reparse-point resolution rather than substituting a Unix symlink fixture.
func TestJunctionRepositoryOverlap(t *testing.T) {
	r, src, ctx := fixture(t)
	junction := filepath.Join(src, "repository junction")
	command := exec.Command("cmd", "/c", "mklink", "/J", junction, r.Path)
	command.Env = Environment()
	if out, e := command.CombinedOutput(); e != nil {
		t.Fatalf("create disposable junction: %v: %s", e, out)
	}
	t.Cleanup(func() {
		if e := os.Remove(junction); e != nil && !os.IsNotExist(e) {
			t.Error(e)
		}
	})
	resolved, e := Canonical(junction)
	if e != nil {
		t.Fatal(e)
	}
	repository, e := Canonical(r.Path)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.EqualFold(resolved, repository) {
		t.Fatalf("junction resolved to %q instead of %q", resolved, repository)
	}
	e = r.Backup(ctx, BackupOptions{Sources: []string{junction}})
	if e == nil || !strings.Contains(e.Error(), "overlap") {
		t.Fatalf("junction source must fail the repository-overlap guard: %v", e)
	}
	if e = r.Check(ctx, true); e != nil {
		t.Fatal("overlap refusal damaged repository", e)
	}
}

func TestSourceJunctionContents(t *testing.T) {
	r, src, ctx := fixture(t)
	mustWrite(t, filepath.Join(src, "important"), []byte("junction contents"))
	alias := filepath.Join(filepath.Dir(src), "source junction")
	command := exec.Command("cmd", "/c", "mklink", "/J", alias, src)
	command.Env = Environment()
	if out, e := command.CombinedOutput(); e != nil {
		t.Fatalf("create junction: %v %s", e, out)
	}
	t.Cleanup(func() {
		if e := os.Remove(alias); e != nil && !os.IsNotExist(e) {
			t.Error(e)
		}
	})
	if e := r.Backup(ctx, BackupOptions{Sources: []string{alias}}); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(filepath.Dir(src), "junction-recovered")
	if e := r.Restore(ctx, RestoreOptions{Snapshot: "latest", Target: target}); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(target, "important"))
	if e != nil || string(data) != "junction contents" {
		t.Fatalf("junction source data not recovered: %q %v", data, e)
	}
}

func TestInternalJunctionIsNotTraversed(t *testing.T) {
	r, src, ctx := fixture(t)
	outside := filepath.Join(filepath.Dir(src), "unrelated ü")
	if e := os.Mkdir(outside, 0700); e != nil {
		t.Fatal(e)
	}
	mustWrite(t, filepath.Join(outside, "outside-secret"), []byte("must not be followed"))
	junction := filepath.Join(src, "internal junction")
	if out, e := exec.Command("cmd", "/c", "mklink", "/J", junction, outside).CombinedOutput(); e != nil {
		t.Fatalf("%s %v", out, e)
	}
	defer os.Remove(junction)
	mustWrite(t, filepath.Join(src, "inside"), []byte("protect this"))
	if e := r.Backup(ctx, BackupOptions{Sources: []string{src}}); e != nil {
		t.Fatal(e)
	}
	nodes, e := r.Search(ctx, "latest", "outside-secret")
	if e != nil || len(nodes) != 0 {
		t.Fatal("internal junction was traversed", nodes, e)
	}
}
func TestLongUnicodePaths(t *testing.T) {
	r, src, ctx := fixture(t)
	nested := src
	for i := 0; i < 7; i++ {
		nested = filepath.Join(nested, "directory with spaces and 日本語")
	}
	if e := os.MkdirAll(nested, 0700); e != nil {
		t.Fatal(e)
	}
	mustWrite(t, filepath.Join(nested, "café.txt"), []byte("long Unicode path data"))
	if e := r.Backup(ctx, BackupOptions{Sources: []string{src}}); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(filepath.Dir(src), "long-path-recovery")
	if e := r.Restore(ctx, RestoreOptions{Snapshot: "latest", Target: target}); e != nil {
		t.Fatal(e)
	}
	rel, e := filepath.Rel(src, nested)
	if e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(target, rel, "café.txt"))
	if e != nil || string(data) != "long Unicode path data" {
		t.Fatal(string(data), e)
	}
}
