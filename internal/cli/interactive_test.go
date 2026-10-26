package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lumesky-me/snapArk/internal/backend"
	"github.com/lumesky-me/snapArk/internal/config"
)

func TestSetupAndGuidedRecovery(t *testing.T) {
	if _, e := exec.LookPath("restic"); e != nil {
		t.Skip("install restic")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source ü")
	repo := filepath.Join(root, "repository")
	cfg := filepath.Join(root, "config.json")
	pw := filepath.Join(root, "password")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	for name, text := range map[string]string{"alpha[a].txt": "recover alpha", "beta.txt": "leave beta"} {
		if e := os.WriteFile(filepath.Join(source, name), []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(pw, []byte("disposable-wizard-password"), 0600); e != nil {
		t.Fatal(e)
	}
	run := func(input string, want int, args ...string) string {
		t.Helper()
		var out, stderr bytes.Buffer
		app := App{Out: &out, Err: &stderr, In: strings.NewReader(input), isTerminal: func() bool { return true }}
		code := app.Run(context.Background(), args)
		if code != want {
			t.Fatalf("%v: exit %d want %d\n%s\n%s", args, code, want, out.String(), stderr.String())
		}
		if strings.Contains(out.String()+stderr.String(), "disposable-wizard-password") {
			t.Fatal("password leaked")
		}
		return out.String()
	}
	answers := strings.Join([]string{"app", repo, source, "", "", "y", "y", "y", ""}, "\n")
	output := run(answers, 0, "setup", "--config", cfg, "--password-file", pw)
	if !strings.Contains(output, "Verified backup completed") {
		t.Fatal(output)
	}
	file, e := (config.Store{Path: cfg}).Read()
	if e != nil {
		t.Fatal(e)
	}
	if file.Profiles["app"].Backend == "" {
		t.Fatal("backend path not saved")
	}
	r := backend.Repository{Path: repo, PasswordFile: pw}
	snapshots, e := r.Snapshots(context.Background())
	if e != nil || len(snapshots) != 1 {
		t.Fatal(snapshots, e)
	}
	// Navigate the one-source hierarchy from snapshot '/' and select a filename
	// containing brackets. It must be treated literally on every host OS.
	target := filepath.Join(root, "restored ü")
	answers = "1\n1\ndone\n" + target + "\nrestore\n"
	output = run(answers, 0, "recover", "--repo", repo, "--password-file", pw, "--config", cfg)
	if !strings.Contains(output, "Recovery completed and file contents verified") {
		t.Fatal(output)
	}
	found := false
	if e = filepath.WalkDir(target, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == "beta.txt" {
			t.Error("unselected file recovered")
		}
		if d.Name() == "alpha[a].txt" {
			b, e := os.ReadFile(path)
			if e != nil || string(b) != "recover alpha" {
				t.Errorf("content mismatch: %s %v", b, e)
			}
			found = true
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if !found {
		t.Fatal("selected file missing")
	}
	before, e := os.ReadFile(cfg)
	if e != nil {
		t.Fatal(e)
	}
	run("app\n", 1, "setup", "--config", cfg, "--password-file", pw)
	after, _ := os.ReadFile(cfg)
	if !bytes.Equal(before, after) {
		t.Fatal("existing configuration replaced")
	}
	// Dry-run preview and declined final approval must create no destination.
	cancelled := filepath.Join(root, "cancelled")
	run("1\nall\n"+cancelled+"\nno\n", 0, "recover", "--repo", repo, "--password-file", pw, "--config", cfg)
	if _, e = os.Lstat(cancelled); !os.IsNotExist(e) {
		t.Fatal("declined recovery wrote files", e)
	}
	run("1\nall\n"+source+"\n", 1, "recover", "--repo", repo, "--password-file", pw, "--config", cfg)
	run("1\nall\n"+filepath.Join(source, "new-folder")+"\n", 1, "recover", "--repo", repo, "--password-file", pw, "--config", cfg)
	run("1\nall\n"+target+"\n", 1, "recover", "--repo", repo, "--password-file", pw, "--config", cfg)
	// Noninteractive recovery remains usable and emits ordinary JSON.
	var out, stderr bytes.Buffer
	if code := (App{Out: &out, Err: &stderr}).Run(context.Background(), []string{"snapshots", "--repo", repo, "--password-file", pw, "--json"}); code != 0 {
		t.Fatal(stderr.String())
	}
	var listed []backend.Snapshot
	if e = json.Unmarshal(out.Bytes(), &listed); e != nil || len(listed) != 1 {
		t.Fatal(out.String(), e)
	}
}
func TestWizardRefusesBackgroundInput(t *testing.T) {
	for _, cmd := range []string{"setup", "recover"} {
		var out, stderr bytes.Buffer
		app := App{Out: &out, Err: &stderr, In: strings.NewReader(""), isTerminal: func() bool { return false }}
		if code := app.Run(context.Background(), []string{cmd}); code != 1 || !strings.Contains(stderr.String(), "interactive terminal") {
			t.Fatalf("%s: %d %s", cmd, code, stderr.String())
		}
	}
}
func TestSetupRejectsOverlapAndUnrelatedDirectory(t *testing.T) {
	if _, e := exec.LookPath("restic"); e != nil {
		t.Skip("install restic")
	}
	for _, overlap := range []bool{false, true} {
		t.Run(map[bool]string{true: "overlap", false: "unrelated"}[overlap], func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			repo := filepath.Join(root, "repo")
			cfg := filepath.Join(root, "config.json")
			if e := os.Mkdir(source, 0700); e != nil {
				t.Fatal(e)
			}
			if overlap {
				repo = filepath.Join(source, "backup")
			} else {
				if e := os.Mkdir(repo, 0700); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(filepath.Join(repo, "personal"), []byte("keep"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			var out, stderr bytes.Buffer
			input := strings.Join([]string{"app", repo, source, "", "", "y", ""}, "\n")
			app := App{Out: &out, Err: &stderr, In: strings.NewReader(input), isTerminal: func() bool { return true }}
			if code := app.Run(context.Background(), []string{"setup", "--config", cfg}); code == 0 {
				t.Fatal("unsafe setup accepted")
			}
			if _, e := os.Stat(cfg); !os.IsNotExist(e) {
				t.Fatal("configuration written before validation")
			}
			if _, e := os.Stat(filepath.Join(repo, "config")); !os.IsNotExist(e) {
				t.Fatal("unsafe repository initialized")
			}
		})
	}
}

func TestRecoveryWizardCannotBypassPreflight(t *testing.T) {
	if _, e := exec.LookPath("restic"); e != nil {
		t.Skip("install restic")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(root, filepath.Join(source, "escape")); e != nil {
		t.Skipf("symlinks unavailable: %v", e)
	}
	pw := filepath.Join(root, "password")
	if e := os.WriteFile(pw, []byte("test"), 0600); e != nil {
		t.Fatal(e)
	}
	repo := filepath.Join(root, "repository")
	r := backend.Repository{Path: repo, PasswordFile: pw}
	ctx := context.Background()
	if e := r.Execute(ctx, "init", "--repository-version", "2"); e != nil {
		t.Fatal(e)
	}
	if e := r.Backup(ctx, backend.BackupOptions{Sources: []string{source}}); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(root, "restore")
	var out, stderr bytes.Buffer
	app := App{Out: &out, Err: &stderr, In: strings.NewReader("1\nall\n" + target + "\nrestore\n"), isTerminal: func() bool { return true }}
	if code := app.Run(ctx, []string{"recover", "--repo", repo, "--password-file", pw}); code != 1 || !strings.Contains(stderr.String(), "preflight") {
		t.Fatalf("%d %s %s", code, out.String(), stderr.String())
	}
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("unsafe wizard extraction wrote a target")
	}
	if _, e := os.Stat(target + ".veyra-incomplete.json"); !os.IsNotExist(e) {
		t.Fatal("unsafe preflight wrote a marker")
	}
}
