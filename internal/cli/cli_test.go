package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHelpAndInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{{nil, 0}, {[]string{"help"}, 0}, {[]string{"version"}, 0}, {[]string{"bogus"}, 2}, {[]string{"backup", "--unknown"}, 2}, {[]string{"run", "--help"}, 0}} {
		var out bytes.Buffer
		a := App{Out: &out, Err: &out}
		if code := a.Run(context.Background(), tc.args); code != tc.code {
			t.Fatalf("%v: exit %d: %s", tc.args, code, out.String())
		}
	}
}

func TestCLIBackupRestore(t *testing.T) {
	if _, e := exec.LookPath("restic"); e != nil {
		t.Skip("install restic for end-to-end tests")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	src := filepath.Join(root, "source")
	password := filepath.Join(root, "password")
	if e := os.Mkdir(src, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(src, "file"), []byte("restore this"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(password, []byte("disposable-password"), 0600); e != nil {
		t.Fatal(e)
	}
	run := func(command string, args ...string) string {
		t.Helper()
		var out, err bytes.Buffer
		a := App{Out: &out, Err: &err}
		argv := append([]string{command, "--repo", repo, "--password-file", password, "--json"}, args...)
		if code := a.Run(context.Background(), argv); code != 0 {
			t.Fatalf("%s exit %d: %s %s", command, code, out.String(), err.String())
		}
		return out.String()
	}
	run("init")
	run("checkpoint", src)
	var snapshots []struct {
		ID string `json:"id"`
	}
	if e := json.Unmarshal([]byte(run("checkpoints")), &snapshots); e != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots: %+v %v", snapshots, e)
	}
	run("files", snapshots[0].ID)
	run("verify", "--full")
	run("stats")
	target := filepath.Join(root, "recovery")
	run("restore", "--target", target, snapshots[0].ID)
	data, e := os.ReadFile(filepath.Join(target, "file"))
	if e != nil || string(data) != "restore this" {
		t.Fatalf("restored: %q %v", data, e)
	}
	run("forget", snapshots[0].ID)
	run("prune")
}
