package project

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lumesky-me/veyra/internal/backend"
)

func TestChild(t *testing.T) {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return
	}
	args = args[1:]
	switch args[0] {
	case "write":
		if err := os.WriteFile(args[1], []byte("modified by wrapped command"), 0600); err != nil {
			os.Exit(90)
		}
	case "sleep":
		time.Sleep(time.Minute)
	}
	code, _ := strconv.Atoi(args[len(args)-1])
	os.Exit(code)
}
func child(mode, path string, code int) []string {
	return []string{os.Args[0], "-test.run=^TestChild$", "--", mode, path, strconv.Itoa(code)}
}

func TestCommandProtection(t *testing.T) {
	if _, e := exec.LookPath("restic"); e != nil {
		t.Skip("install restic for integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	src := filepath.Join(root, "project")
	if e := os.Mkdir(src, 0700); e != nil {
		t.Fatal(e)
	}
	password := filepath.Join(root, "password")
	if e := os.WriteFile(password, []byte("disposable-password"), 0600); e != nil {
		t.Fatal(e)
	}
	r := backend.Repository{Path: filepath.Join(root, "repo"), PasswordFile: password, Out: io.Discard, Err: io.Discard}
	if e := r.Execute(ctx, "init"); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(src, "file")
	opts := RunOptions{Project: src, Command: child("write", file, 7), Timeout: 10 * time.Second, Post: true, Stdout: io.Discard, Stderr: io.Discard}
	result, e := Protect(ctx, r, opts)
	if e != nil {
		t.Fatal(e)
	}
	if result.ExitCode != 7 || result.Before == "" || result.After == "" || len(result.Changes) != 1 || result.Changes[0].Kind != "added" {
		t.Fatalf("unexpected result: %+v", result)
	}
	opts.Command = child("noop", file, 0)
	opts.Test = child("noop", file, 9)
	opts.Post = false
	result, e = Protect(ctx, r, opts)
	if e != nil {
		t.Fatal(e)
	}
	if result.TestExitCode == nil || *result.TestExitCode != 9 {
		t.Fatalf("test exit lost: %+v", result)
	}
	opts.Command = child("sleep", file, 0)
	opts.Test = nil
	opts.Timeout = 100 * time.Millisecond
	result, e = Protect(ctx, r, opts)
	if e != nil {
		t.Fatal(e)
	}
	if result.ExitCode != 124 {
		t.Fatalf("timeout exit %d", result.ExitCode)
	}
	marker := filepath.Join(src, "must-not-exist")
	opts.Command = child("write", marker, 0)
	opts.Timeout = time.Second
	bad := r
	bad.PasswordFile = filepath.Join(root, "missing-password")
	if _, e = Protect(ctx, bad, opts); e == nil {
		t.Fatal("command allowed without checkpoint")
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("command executed after checkpoint failure")
	}
	// Corrupt a stored pack: backup can still append a snapshot but full verification
	// must refuse to launch the command.
	var pack string
	_ = filepath.WalkDir(filepath.Join(r.Path, "data"), func(p string, d os.DirEntry, e error) error {
		if e == nil && d.Type().IsRegular() {
			pack = p
		}
		return e
	})
	if pack == "" {
		t.Fatal("missing test pack")
	}
	if e = os.Chmod(pack, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(pack, []byte("corruption"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Protect(ctx, r, opts); e == nil {
		t.Fatal("command allowed after integrity failure")
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("command executed after verification failure")
	}
}
