package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/lumesky-me/veyra/internal/journal"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDurableVerificationAndRecovery(t *testing.T) {
	if _, e := exec.LookPath("restic"); e != nil {
		t.Skip("install restic")
	}
	root := t.TempDir()
	src := filepath.Join(root, "project")
	repo := filepath.Join(root, "repo")
	cfg := filepath.Join(root, "settings.json")
	pw := filepath.Join(root, "password")
	if e := os.Mkdir(src, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(src, "file"), []byte("rehearse this"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(pw, []byte("disposable-password"), 0600); e != nil {
		t.Fatal(e)
	}
	privateTestCredential(t, pw)
	invoke := func(code int, args ...string) string {
		t.Helper()
		var out, stderr bytes.Buffer
		got := (App{Out: &out, Err: &stderr}).Run(context.Background(), args)
		if got != code {
			t.Fatalf("%v: %d expected %d: %s %s", args, got, code, out.String(), stderr.String())
		}
		return out.String()
	}
	invoke(0, "profile", "add", "--config", cfg, "--source", src, "--repo", repo, "app")
	invoke(0, "init", "--repo", repo, "--password-file", pw)
	options := []string{"--profile", "app", "--config", cfg, "--password-file", pw, "--json"}
	run := func(code int, cmd string, more ...string) string {
		return invoke(code, append(append([]string{cmd}, options...), more...)...)
	}
	run(0, "backup")
	run(0, "verify", "--full")
	var rows []overview
	if e := json.Unmarshal([]byte(run(0, "status")), &rows); e != nil || rows[0].LastFull == nil {
		t.Fatal("full check not reported", rows, e)
	}
	run(0, "test-restore", "--full", "--temp-dir", root)
	sample := run(0, "test-restore", "--sample-files", "1", "--max-bytes", "64", "--temp-dir", root)
	var sampleReport struct {
		Files []string `json:"tested_files"`
		Depth string   `json:"depth"`
	}
	if e := json.Unmarshal([]byte(sample), &sampleReport); e != nil || len(sampleReport.Files) != 1 || sampleReport.Depth != "sample-content" {
		t.Fatal("sample recovery was not accurately reported", sample, e)
	}
	var records []journal.Record
	if e := json.Unmarshal([]byte(run(0, "history")), &records); e != nil {
		t.Fatal(e)
	}
	recovery := false
	for _, r := range records {
		if r.Kind == "recovery-test" && r.Outcome == "passed" {
			recovery = true
		}
	}
	if !recovery {
		t.Fatal("actual rehearsal not recorded")
	}
	offline := repo + "-offline"
	if e := os.Rename(repo, offline); e != nil {
		t.Fatal(e)
	}
	run(1, "backup", "--scheduled")
	var unavailable []journal.Record
	if e := json.Unmarshal([]byte(run(0, "history")), &unavailable); e != nil {
		t.Fatal(e)
	}
	foundFailure := false
	for _, event := range unavailable {
		if event.Kind == "scheduled-run" && event.Category == "repository_unavailable" && event.Outcome == "failed" {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Fatal("disconnected destination failure was not classified")
	}

	if e := os.Rename(offline, repo); e != nil {
		t.Fatal(e)
	}
	run(0, "backup", "--scheduled")
	run(0, "status")
	// A process can disappear before publishing any snapshot or final journal
	// record. The older good snapshot must not hide this unobserved attempt.
	store := journal.Store{Dir: cfg + ".history"}
	records, e := store.Read()
	if e != nil {
		t.Fatal(e)
	}
	var profile string
	for _, record := range records {
		if record.Kind == "backup" {
			profile = record.Profile
		}
	}
	unobserved, e := store.Begin(journal.Record{Kind: "backup", Profile: profile, Depth: "completion"})
	if e != nil {
		t.Fatal(e)
	}
	recordPath := filepath.Join(store.Dir, unobserved.ID+".json")
	before, e := os.ReadFile(recordPath)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal([]byte(run(1, "status")), &rows); e != nil || rows[0].LastBackup == nil || rows[0].LastBackup.Outcome != "started" {
		t.Fatal("unobserved backup was hidden by an older good snapshot", rows, e)
	}
	after, e := os.ReadFile(recordPath)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("status changed an unobserved record", e)
	}
	// This fixture owns the interrupted operation; the CLI itself never
	// finalizes somebody else's record or removes a stale run lock.
	if e = store.Finish(unobserved, "interrupted", "cancellation"); e != nil {
		t.Fatal(e)
	}
	run(1, "status")
	run(0, "backup", "--scheduled")
	run(0, "status")
	oldRepo := repo + "-original"
	if e := os.Rename(repo, oldRepo); e != nil {
		t.Fatal(e)
	}
	invoke(0, "init", "--repo", repo, "--password-file", pw)
	text := run(1, "status")
	var replaced []overview
	if e := json.Unmarshal([]byte(text), &replaced); e != nil || replaced[0].LastFull != nil {
		t.Fatal("replaced repository inherited former verification", text, e)
	}

}

func TestVerificationResultsUseCompletionTime(t *testing.T) {
	now := time.Now()
	early := now.Add(-time.Hour)
	late := now
	first := journal.Record{Started: now.Add(-2 * time.Hour), Finished: &late}
	second := journal.Record{Started: now.Add(-90 * time.Minute), Finished: &early}
	if !laterResult(first, &second) || laterResult(second, &first) {
		t.Fatal("result ordering ignored completion time of a long-running check")
	}
}
