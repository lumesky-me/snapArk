package backend

import (
	"path/filepath"
	"testing"
)

func TestAvailableSpace(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{root, filepath.Join(root, "missing", "repo")} {
		if _, e := AvailableSpace(p); e != nil {
			t.Fatalf("capacity query for %s: %v", p, e)
		}
	}
}
