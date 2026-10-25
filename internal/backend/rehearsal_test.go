package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBoundedRecoverySample(t *testing.T) {
	r, src, ctx := fixture(t)
	mustWrite(t, filepath.Join(src, "a-empty"), nil)
	mustWrite(t, filepath.Join(src, "b-small"), []byte("small"))
	mustWrite(t, filepath.Join(src, "c-large"), make([]byte, 128))
	if e := r.Execute(ctx, "backup", "--host", "replacement-origin", src); e != nil {
		t.Fatal(e)
	}
	snapshot, e := r.Resolve(ctx, "latest")
	if e != nil {
		t.Fatal(e)
	}
	if snapshot.Hostname != "replacement-origin" {
		t.Fatal("foreign-host fixture not created")
	}
	files, e := r.SampleFiles(ctx, snapshot.ID, 2, 16)
	if e != nil || len(files) != 2 {
		t.Fatal(files, e)
	}
	target := filepath.Join(t.TempDir(), "sample")
	if e = r.RecoverSample(ctx, snapshot.ID, files[1], target); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(target)
	if e != nil || string(data) != "small" {
		t.Fatal("foreign-host content not recovered", data, e)
	}
	if e = r.RecoverSample(ctx, snapshot.ID, files[1], target); e == nil {
		t.Fatal("sample overwrote an existing file")
	}
	if _, e = r.SampleFiles(ctx, snapshot.ID, 33, 16); e == nil {
		t.Fatal("unbounded sample accepted")
	}
}
