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
