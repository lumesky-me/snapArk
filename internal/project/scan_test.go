package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestScanChanges(t *testing.T) {
	root := t.TempDir()
	write := func(name, value string) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("modified", "before")
	write("deleted", "gone")
	write("unchanged", "same")
	before, e := Scan(context.Background(), root)
	if e != nil {
		t.Fatal(e)
	}
	write("modified", "after")
	write("added", "new")
	if e = os.Remove(filepath.Join(root, "deleted")); e != nil {
		t.Fatal(e)
	}
	after, e := Scan(context.Background(), root)
	if e != nil {
		t.Fatal(e)
	}
	got := Compare(before, after)
	want := []Change{{Path: "added", Kind: "added"}, {Path: "deleted", Kind: "deleted"}, {Path: "modified", Kind: "modified"}}
	if len(got) != len(want) {
		t.Fatalf("changes: %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("change %d: %+v", i, got[i])
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = Scan(canceled, root); e == nil {
		t.Fatal("cancellation ignored")
	}
}

func BenchmarkScan(b *testing.B) {
	root := b.TempDir()
	data := make([]byte, 1<<20)
	for i := 0; i < 8; i++ {
		if e := os.WriteFile(filepath.Join(root, string(rune('a'+i))), data, 0600); e != nil {
			b.Fatal(e)
		}
	}
	b.SetBytes(int64(8 * len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, e := Scan(context.Background(), root); e != nil {
			b.Fatal(e)
		}
	}
}

func TestGitStatus(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init")
	for name, value := range map[string]string{".gitignore": "ignored\n", "tracked": "before", "untracked": "new", "ignored": "important local data"} {
		if e := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	gitRun(t, root, "add", "tracked")
	if e := os.WriteFile(filepath.Join(root, "tracked"), []byte("after"), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := Git(context.Background(), root)
	if e != nil {
		t.Fatal(e)
	}
	statuses := map[string]string{}
	for _, v := range s.Entries {
		statuses[v.Path] = v.Status
	}
	if statuses["tracked"] != "AM" || statuses["untracked"] != "??" || statuses["ignored"] != "!!" {
		t.Fatalf("status: %+v", statuses)
	}
}

func gitRun(t *testing.T, root string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, e := c.CombinedOutput(); e != nil {
		t.Fatalf("git: %v: %s", e, out)
	}
}
