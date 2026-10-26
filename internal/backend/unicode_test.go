package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnicodePathAliases(t *testing.T) {
	for _, pair := range [][2]string{{"/café/file", "/cafe\u0301/file"}, {"/CAFÉ", "/cafe\u0301"}, {"/Ångström", "/A\u030angstro\u0308m"}, {"/Σ/é", "/ς/e\u0301"}} {
		if pathKey(pair[0]) != pathKey(pair[1]) {
			t.Errorf("normalization/case alias missed: %q", pair)
		}
	}
	if pathKey("/a") == pathKey("/á") {
		t.Fatal("distinct names merged")
	}
}
func TestUnicodeFilesystem(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "café")
	b := filepath.Join(root, "cafe\u0301")
	if e := os.WriteFile(a, []byte("first"), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := os.OpenFile(b, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(e) {
		content, e := os.ReadFile(b)
		if e != nil || string(content) != "first" {
			t.Fatal("filesystem alias changed contents", e)
		}
		t.Log("filesystem aliases canonical Unicode forms; portable collision guard also treats them as aliases")
		return
	}
	if e != nil {
		t.Fatal(e)
	}
	_ = f.Close()
	ai, e := os.Stat(a)
	if e != nil {
		t.Fatal(e)
	}
	bi, e := os.Stat(b)
	if e != nil {
		t.Fatal(e)
	}
	if os.SameFile(ai, bi) {
		t.Fatal("exclusive creation aliased existing file")
	}
	t.Log("filesystem permits distinct canonical Unicode forms; restore guard conservatively rejects their joint extraction")
}
