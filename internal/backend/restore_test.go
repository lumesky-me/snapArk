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
