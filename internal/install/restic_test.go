package install

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func TestArchiveVerification(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, e := z.Create("restic_0.18.1_windows_amd64.exe")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.Write([]byte("binary"))
	_ = z.Close()
	if _, e = decode("restic.zip", b.Bytes(), "wrong"); e == nil {
		t.Fatal("checksum mismatch accepted")
	}
	data, e := decode("restic.zip", b.Bytes(), digest(b.Bytes()))
	if e != nil || string(data) != "binary" {
		t.Fatal(string(data), e)
	}
	b.Reset()
	z = zip.NewWriter(&b)
	_, _ = z.Create("../restic.exe")
	_ = z.Close()
	if _, e = decode("restic.zip", b.Bytes(), digest(b.Bytes())); e == nil {
		t.Fatal("archive traversal accepted")
	}
	if _, _, e = Artifact(); e != nil {
		t.Fatal(e)
	}
}
func TestInstallExistingPinnedBinary(t *testing.T) {
	binary, e := exec.LookPath("restic")
	if e != nil {
		t.Skip("install restic")
	}
	payload, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(t.TempDir(), "backend with spaces ü")
	installed, e := place(context.Background(), dir, payload)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(installed); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(dir, "RESTIC-"+Version+"-LICENSE")); e != nil {
		t.Fatal(e)
	}
	if _, e = place(context.Background(), dir, payload); e == nil {
		t.Fatal("existing executable overwritten")
	}
	if _, e = place(context.Background(), filepath.Join(t.TempDir(), "invalid"), []byte("not executable")); e == nil {
		t.Fatal("invalid binary installed")
	}
}

func TestPublicationNeverOverwrites(t *testing.T) {
	for _, kind := range []string{"file", "directory", "link"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			staged := filepath.Join(root, "staged")
			target := filepath.Join(root, "restic")
			if e := os.WriteFile(staged, []byte("verified payload"), 0700); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "file":
				if e := os.WriteFile(target, []byte("keep existing"), 0600); e != nil {
					t.Fatal(e)
				}
			case "directory":
				if e := os.Mkdir(target, 0700); e != nil {
					t.Fatal(e)
				}
			case "link":
				other := filepath.Join(root, "other")
				if e := os.WriteFile(other, []byte("keep existing"), 0600); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(other, target); e != nil {
					t.Skipf("symlinks unavailable: %v", e)
				}
			}
			if e := publish(staged, target); e == nil {
				t.Fatal("existing target replaced")
			}
			if kind != "directory" {
				b, e := os.ReadFile(target)
				if e != nil || string(b) != "keep existing" {
					t.Fatal(string(b), e)
				}
			}
			b, e := os.ReadFile(staged)
			if e != nil || string(b) != "verified payload" {
				t.Fatal("staging file lost on conflict", e)
			}
		})
	}
	root := t.TempDir()
	staged := filepath.Join(root, "stage")
	target := filepath.Join(root, "restic")
	if e := os.WriteFile(staged, []byte("complete executable"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := publish(staged, target); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(target)
	if e != nil || string(b) != "complete executable" {
		t.Fatal(string(b), e)
	}
}
func TestCancelledInstallDoesNotPublish(t *testing.T) {
	binary, e := exec.LookPath("restic")
	if e != nil {
		t.Skip("install restic")
	}
	payload, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	if _, e = place(ctx, dir, payload); e == nil {
		t.Fatal("cancelled installation succeeded")
	}
	name := "restic"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if _, e = os.Stat(filepath.Join(dir, name)); !os.IsNotExist(e) {
		t.Fatal("cancelled install published executable")
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".restic-") {
			t.Fatal("staging file leaked")
		}
	}
}
