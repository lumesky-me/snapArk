package backend

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The test executable also acts as a failing backend to inject errors that are
// impractical to reproduce with portable filesystem quotas in unit tests.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "--repo" {
		mode := filepath.Base(os.Args[2])
		if strings.HasPrefix(mode, "archive-") {
			for _, arg := range os.Args[3:] {
				if arg == "snapshots" {
					fmt.Printf(`[{"id":"%s","time":"2026-01-01T00:00:00Z","paths":["/project"]}]`, strings.Repeat("a", 64))
					os.Exit(0)
				}
			}
			archives := map[string][]tar.Header{
				"archive-parent":        {{Name: "hard", Typeflag: tar.TypeLink, Linkname: ".."}},
				"archive-traversal":     {{Name: "../outside", Typeflag: tar.TypeReg}},
				"archive-absolute":      {{Name: "/outside", Typeflag: tar.TypeReg}},
				"archive-chain":         {{Name: "dir", Typeflag: tar.TypeDir}, {Name: "dir/sub", Typeflag: tar.TypeSymlink, Linkname: ".."}, {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "dir/sub/../../outside"}},
				"archive-case":          {{Name: "file", Typeflag: tar.TypeReg}, {Name: "FILE", Typeflag: tar.TypeReg}},
				"archive-unicode-case":  {{Name: "Σ", Typeflag: tar.TypeReg}, {Name: "ς", Typeflag: tar.TypeReg}},
				"archive-unicode-child": {{Name: "Σ", Typeflag: tar.TypeSymlink, Linkname: "dir"}, {Name: "ς/file", Typeflag: tar.TypeReg}},
				"archive-alias-child":   {{Name: "LINK", Typeflag: tar.TypeSymlink, Linkname: "dir"}, {Name: "link/file", Typeflag: tar.TypeReg}},
				"archive-link-child":    {{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "dir"}, {Name: "link/file", Typeflag: tar.TypeReg}},
			}
			writer := tar.NewWriter(os.Stdout)
			for _, header := range archives[mode] {
				if e := writer.WriteHeader(&header); e != nil {
					os.Exit(2)
				}
			}
			if e := writer.Close(); e != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}

		if mode == "timestamps" {
			fmt.Printf(`[{"id":"%s","time":"2026-01-01T01:00:00+02:00"},{"id":"%s","time":"2026-01-01T00:00:00Z"}]`, strings.Repeat("a", 64), strings.Repeat("b", 64))
			os.Exit(0)
		}
		if mode == "partial" {
			fmt.Printf("{\"message_type\":\"summary\",\"snapshot_id\":\"%s\"}\n", strings.Repeat("a", 64))
			fmt.Fprintln(os.Stderr, "source read failure")
			os.Exit(3)
		}
		messages := map[string]string{"disk-full": "no space left on device", "unplugged": "input/output error: device disconnected", "locked": "repository is already locked"}
		if msg, ok := messages[mode]; ok {
			fmt.Fprintln(os.Stderr, msg)
			if mode == "locked" {
				os.Exit(11)
			}
			os.Exit(1)
		}
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func TestBackendFailuresPropagate(t *testing.T) {
	for _, mode := range []string{"disk-full", "unplugged", "locked", "partial"} {
		t.Run(mode, func(t *testing.T) {
			var out, stderr bytes.Buffer
			root := t.TempDir()
			src := filepath.Join(root, "source")
			if e := os.Mkdir(src, 0700); e != nil {
				t.Fatal(e)
			}
			r := Repository{Path: filepath.Join(root, mode), Binary: os.Args[0], Out: &out, Err: &stderr}
			id, err := r.CheckpointID(context.Background(), BackupOptions{Sources: []string{src}})
			if err == nil || id != "" {
				t.Fatalf("failed backend produced a valid checkpoint: %q %v", id, err)
			}
			if stderr.Len() == 0 {
				t.Fatal("backend diagnostics lost")
			}
		})
	}
}

func TestEnvironmentFiltersCredentials(t *testing.T) {
	t.Setenv("RESTIC_PASSWORD", "must-not-leak")
	t.Setenv("GH_TOKEN", "must-not-leak")
	t.Setenv("SERVICE_API_KEY", "must-not-leak")
	t.Setenv("VEYRA_PRIVATE", "must-not-leak")
	t.Setenv("NORMAL_BUILD_SETTING", "kept")
	env := strings.Join(Environment(), "\n")
	if strings.Contains(env, "must-not-leak") {
		t.Fatal("credential binding was inherited")
	}
	if !strings.Contains(env, "NORMAL_BUILD_SETTING=kept") {
		t.Fatal("normal build environment was removed")
	}
}

func TestLatestUsesInstantNotTimestampText(t *testing.T) {
	r := Repository{Path: filepath.Join(t.TempDir(), "timestamps"), Binary: os.Args[0]}
	s, e := r.Resolve(context.Background(), "latest")
	if e != nil {
		t.Fatal(e)
	}
	if s.ID != strings.Repeat("b", 64) {
		t.Fatalf("selected earlier instant: %+v", s)
	}
}

func TestUnsafeArchiveNeverCreatesTarget(t *testing.T) {
	for _, mode := range []string{"archive-parent", "archive-traversal", "archive-absolute", "archive-chain", "archive-case", "archive-link-child", "archive-alias-child", "archive-unicode-case", "archive-unicode-child"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "recovery")
			r := Repository{Path: filepath.Join(root, mode), Binary: os.Args[0]}
			if e := r.Restore(context.Background(), RestoreOptions{Snapshot: "latest", Target: target}); e == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, e := os.Stat(target); !os.IsNotExist(e) {
				t.Fatal("unsafe archive created restore target")
			}
		})
	}
}
