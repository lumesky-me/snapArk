package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryPasswordAndBrowse(t *testing.T) {
	if _, e := exec.LookPath("restic"); e != nil {
		t.Skip("install restic")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"a", "b", "c"} {
		if e := os.WriteFile(filepath.Join(source, name), []byte(name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	r := Repository{Path: filepath.Join(root, "repo"), PasswordInput: []byte("memory-only-secret")}
	ctx := context.Background()
	if e := r.Validate(ctx); e != nil {
		t.Fatal(e)
	}
	c := r.command(ctx, "snapshots")
	if strings.Contains(strings.Join(c.Args, " "), string(r.PasswordInput)) || strings.Contains(strings.Join(c.Env, " "), string(r.PasswordInput)) {
		t.Fatal("password leaked outside stdin")
	}
	if e := r.Execute(ctx, "init", "--repository-version", "2"); e != nil {
		t.Fatal(e)
	}
	if e := r.Backup(ctx, BackupOptions{Sources: []string{source}}); e != nil {
		t.Fatal(e)
	}
	s, e := r.Resolve(ctx, "latest")
	if e != nil {
		t.Fatal(e)
	}
	dir := snapshotRoot(s.Paths[0])
	nodes, more, e := r.Browse(ctx, s.ID, dir, "", "", 2)
	if e != nil || len(nodes) != 2 || !more {
		t.Fatal(nodes, more, e)
	}
	next, more, e := r.Browse(ctx, s.ID, dir, nodes[1].Path, "", 2)
	if e != nil || len(next) != 1 || more {
		t.Fatal(next, more, e)
	}
	if path := filepath.Base(next[0].Path); path != "c" {
		t.Fatal(path)
	}
}
