package backend

import "testing"

func TestSafeNode(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    Node
		ok   bool
	}{
		{"file", Node{Type: "file", Path: "/project/file"}, true},
		{"relative sibling", Node{Type: "symlink", Path: "/project/link", LinkTarget: "file"}, true},
		{"relative parent", Node{Type: "symlink", Path: "/project/sub/link", LinkTarget: "../file"}, true},
		{"root escape", Node{Type: "symlink", Path: "/link", LinkTarget: "../secret"}, false},
		{"absolute link", Node{Type: "symlink", Path: "/project/link", LinkTarget: "/etc/passwd"}, false},
		{"traversal", Node{Type: "file", Path: "/../file"}, false},
		{"backslash", Node{Type: "file", Path: "/x\\..\\secret"}, false},
		{"drive", Node{Type: "file", Path: "/C:/file"}, false},
		{"device", Node{Type: "file", Path: "/NUL.txt"}, false},
		{"superscript device", Node{Type: "file", Path: "/COM¹.txt"}, false},
		{"spaced device", Node{Type: "file", Path: "/CON .txt"}, false},
		{"device link", Node{Type: "symlink", Path: "/link", LinkTarget: "NUL"}, false},
		{"console device", Node{Type: "file", Path: "/CONOUT$"}, false},
		{"wildcard", Node{Type: "file", Path: "/wild*card"}, false},
		{"control", Node{Type: "file", Path: "/line\nfeed"}, false},
		{"unicode", Node{Type: "file", Path: "/日本語.txt"}, true},
		{"socket", Node{Type: "socket", Path: "/socket"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := SafeNode(tc.n)
			if (err == nil) != tc.ok {
				t.Fatalf("SafeNode(%+v)=%v", tc.n, err)
			}
		})
	}
}

func TestSymlinkChains(t *testing.T) {
	for _, tc := range []struct {
		name  string
		links map[string]string
		ok    bool
	}{
		{"internal chain", map[string]string{"/dir/a": "../b", "/b": "file"}, true},
		{"indirect traversal", map[string]string{"/dir/sub": "..", "/link": "dir/sub/../../outside"}, false},
		{"cycle", map[string]string{"/a": "b", "/b": "a"}, false},
		{"Unicode alias traversal", map[string]string{"/dir/Σ": "..", "/link": "dir/ς/../../outside"}, false},
		{"case alias traversal", map[string]string{"/dir/SUB": "..", "/link": "dir/sub/../../outside"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLinks(tc.links)
			if (err == nil) != tc.ok {
				t.Fatalf("links: %v", err)
			}
		})
	}
}

func TestSnapshotRootAcrossHosts(t *testing.T) {
	for source, want := range map[string]string{`C:\Users\dev\app`: "/C/Users/dev/app", "D:/app": "/D/app", "/home/dev/app": "/home/dev/app"} {
		if got := snapshotRoot(source); got != want {
			t.Errorf("snapshotRoot(%q)=%q, want %q", source, got, want)
		}
	}
}

func TestUnicodePathKeys(t *testing.T) {
	for _, pair := range [][2]string{{"/Σ", "/ς"}, {"/S", "/ſ"}, {"/K", "/K"}, {"/DIR/File", "/dir/file"}} {
		if pathKey(pair[0]) != pathKey(pair[1]) {
			t.Errorf("missed case aliases %q and %q", pair[0], pair[1])
		}
	}
	if pathKey("/日本語") == pathKey("/file") {
		t.Fatal("unrelated Unicode names collide")
	}
}
