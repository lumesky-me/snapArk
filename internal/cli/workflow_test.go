package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lumesky-me/veyra/internal/backend"
	"github.com/lumesky-me/veyra/internal/config"
	"github.com/lumesky-me/veyra/internal/scheduler"
)

func TestProfileWorkflow(t *testing.T) {
	if _, e := exec.LookPath("restic"); e != nil {
		t.Skip("install pinned restic for integration testing")
	}
	root := t.TempDir()
	cfg := filepath.Join(root, "settings", "profiles.json")
	src := filepath.Join(root, "project 日本語")
	repo := filepath.Join(root, "repo")
	pw := filepath.Join(root, "password")
	if e := os.Mkdir(src, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(pw, []byte("disposable-password"), 0600); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(src, "important.txt")
	if e := os.WriteFile(file, []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	invoke := func(code int, args ...string) string {
		t.Helper()
		var out, err bytes.Buffer
		a := App{Out: &out, Err: &err}
		if got := a.Run(context.Background(), args); got != code {
			t.Fatalf("%v: exit %d expected %d\nout=%s\nerr=%s", args, got, code, out.String(), err.String())
		}
		return out.String()
	}
	invoke(0, "profile", "add", "--config", cfg, "--repo", repo, "--source", src, "--keep-last", "1", "app")
	invoke(1, "profile", "add", "--config", cfg, "--repo", repo, "--source", src, "app")
	invoke(0, "profile", "default", "--config", cfg, "app")
	invoke(0, "profile", "validate", "--config", cfg, "app")
	invoke(0, "init", "--repo", repo, "--password-file", pw)
	options := []string{"--config", cfg, "--profile", "app", "--password-file", pw, "--json"}
	run := func(code int, cmd string, extra ...string) string {
		return invoke(code, append(append([]string{cmd}, options...), extra...)...)
	}
	// Dry-run leaves the encrypted repository byte-for-byte unchanged.
	before := repoFiles(t, repo)
	plan := run(0, "plan")
	if !strings.Contains(plan, `"estimated_files":1`) {
		t.Fatal("incorrect plan", plan)
	}
	after := repoFiles(t, repo)
	if string(before) != string(after) {
		t.Fatal("plan changed repository contents")
	}
	run(1, "status")
	stream := run(0, "backup")
	decoder := json.NewDecoder(strings.NewReader(stream))
	completed := false
	for {
		var event map[string]any
		e := decoder.Decode(&event)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal("profile backup mixed non-JSON progress into stdout", e, stream)
		}
		if event["message_type"] == "completed" {
			completed = true
		}
	}
	if !completed {
		t.Fatal("backup did not confirm completion")
	}
	if !strings.Contains(run(0, "find", "--query", "important"), "important.txt") {
		t.Fatal("search missed backed-up file")
	}
	run(0, "status")
	run(0, "doctor")
	invoke(0, "backup", "--repo", repo, "--password-file", pw, src) // unrelated snapshot
	if e := os.WriteFile(file, []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	run(0, "backup")
	listing := run(0, "snapshots")
	var snaps []struct {
		ID string `json:"id"`
	}
	if e := json.Unmarshal([]byte(listing), &snaps); e != nil || len(snaps) != 2 {
		t.Fatalf("scope snapshots: %s %v", listing, e)
	}
	run(0, "retention")
	var preview []map[string]any
	if e := json.Unmarshal([]byte(run(0, "snapshots")), &preview); e != nil || len(preview) != 2 {
		t.Fatal("retention preview deleted snapshots")
	}
	run(0, "retention", "--confirm")
	if e := json.Unmarshal([]byte(run(0, "snapshots")), &preview); e != nil || len(preview) != 1 {
		t.Fatal("retention did not keep exactly one profile snapshot")
	}
	var all []map[string]any
	text := invoke(0, "snapshots", "--repo", repo, "--password-file", pw, "--json")
	if e := json.Unmarshal([]byte(text), &all); e != nil || len(all) != 2 {
		t.Fatal("retention touched unrelated snapshot", text)
	}
	run(0, "run", "--", os.Args[0], "-test.run=^TestHelpAndInvalidInput$")
	var checkpoints []map[string]any
	if e := json.Unmarshal([]byte(run(0, "checkpoints")), &checkpoints); e != nil || len(checkpoints) != 1 {
		t.Fatal("protected profile checkpoint not browsable", e)
	}
	run(0, "status")
	run(0, "schedule")
	store := config.Store{Path: cfg}
	settings, e := store.Read()
	if e != nil {
		t.Fatal(e)
	}
	profile, e := settings.Select("app")
	if e != nil {
		t.Fatal(e)
	}
	command := exec.Command("restic", "--repo", repo, "--password-file", pw, "--no-cache", "backup", "--tag", profile.Tag()+":complete:pending", src)
	if output, e := command.CombinedOutput(); e != nil {
		t.Fatalf("create pending fixture: %v %s", e, output)
	}
	if !strings.Contains(run(1, "status"), "unfinished") {
		t.Fatal("pending backup was reported healthy")
	}
	beforePending := invoke(0, "snapshots", "--repo", repo, "--password-file", pw, "--json")
	run(0, "retention", "--confirm")
	afterPending := invoke(0, "snapshots", "--repo", repo, "--password-file", pw, "--json")
	if beforePending != afterPending {
		t.Fatal("retention deleted pending or protected checkpoint snapshots")
	}

	invoke(0, "profile", "delete", "--config", cfg, "app")
	if _, e := os.Stat(filepath.Join(repo, "config")); e != nil {
		t.Fatal("deleting profile removed repository")
	}
}
func repoFiles(t *testing.T, root string) []byte {
	t.Helper()
	files := map[string]string{}
	if e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type().IsRegular() {
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			files[p] = string(b)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(files)
	return b
}
func TestScheduleEscaping(t *testing.T) {
	p := config.Profile{Name: "app", ExpectedHours: 24}
	result, e := schedule(p, filepath.Join(t.TempDir(), "settings with space.json"), filepath.Join(t.TempDir(), "pw & 日本語.txt"))
	if e != nil {
		t.Fatal(e)
	}
	if (runtime.GOOS == "darwin" && !strings.Contains(result["veyra-app.plist"], "&amp;")) || (runtime.GOOS == "windows" && !strings.Contains(result["veyra-app.xml"], "&amp;")) {
		t.Fatal("XML arguments were not escaped")
	}
	for key, value := range result {
		if strings.HasSuffix(key, ".xml") || strings.HasSuffix(key, ".plist") {
			d := xml.NewDecoder(strings.NewReader(value))
			for {
				_, err := d.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal("invalid scheduler XML", err)
				}
			}
		}
	}
	if _, e = schedule(p, "", ""); e == nil {
		t.Fatal("noninteractive schedule accepted no password file")
	}
	if systemdArg("%h/$X") != `"%%h/$$X"` {
		t.Fatal("systemd expansion was not escaped")
	}
}

func TestStatusRejectsRedirectedSourceAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows junction resolution is tested natively in backend")
	}
	root := t.TempDir()
	old := filepath.Join(root, "old")
	next := filepath.Join(root, "new")
	alias := filepath.Join(root, "alias")
	for _, path := range []string{old, next} {
		if e := os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.Symlink(old, alias); e != nil {
		t.Fatal(e)
	}
	p := config.Profile{Sources: []string{alias}}
	physical, e := backend.Canonical(old)
	if e != nil {
		t.Fatal(e)
	}
	snapshot := backend.Snapshot{Paths: []string{physical}}
	if !profileRootsMatch(snapshot, p) {
		t.Fatal("current source rejected")
	}
	if e := os.Remove(alias); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(next, alias); e != nil {
		t.Fatal(e)
	}
	if profileRootsMatch(snapshot, p) {
		t.Fatal("old target snapshot accepted as backup of redirected source")
	}
}

func TestSystemdStatusRejectsServiceOverrides(t *testing.T) {
	m := scheduler.Manager{Dir: t.TempDir(), Name: "veyra-app", Owner: "profile:config", Run: func(...string) error { return nil }}
	if e := m.Install(map[string]string{"veyra-app.service": "[Service]\n", "veyra-app.timer": "[Timer]\n"}); e != nil {
		t.Fatal(e)
	}
	edited := false
	command := func(program string, args []string, input []byte) ([]byte, error) {
		unit := args[2]
		drop := ""
		if edited && strings.HasSuffix(unit, ".service") {
			drop = "/private/user-override.conf"
		}
		return []byte("LoadState=loaded\nActiveState=active\nUnitFileState=enabled\nFragmentPath=" + filepath.Join(m.Dir, unit) + "\nDropInPaths=" + drop + "\n"), nil
	}
	if state, e := systemdState(m, command); e != nil || state.State != "enabled" {
		t.Fatal(state, e)
	}
	edited = true
	if state, e := systemdState(m, command); e == nil || state.State != "conflicting" {
		t.Fatal("service override not observed", state, e)
	}
}
