package journal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func finalized(t *testing.T, s Store, outcome string) Record {
	t.Helper()
	r, e := s.Begin(Record{Kind: "verification", Depth: "metadata", Repository: "repo-identity"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Finish(r, outcome, ""); e != nil {
		t.Fatal(e)
	}
	return r
}
func TestArchivePreservesUnfinishedAndFailedRecords(t *testing.T) {
	root := t.TempDir()
	s := Store{Dir: filepath.Join(root, "history")}
	pass := finalized(t, s, "passed")
	failed := finalized(t, s, "failed")
	started, e := s.Begin(Record{Kind: "backup"})
	if e != nil {
		t.Fatal(e)
	}
	destination := filepath.Join(root, "archive")
	n, e := s.Archive(destination, time.Now().Add(time.Hour), false)
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	archived, e := ReadArchive(destination)
	if e != nil || len(archived) != 1 || archived[0].ID != pass.ID || archived[0].Repository != "repo-identity" {
		t.Fatal(archived, e)
	}
	active, e := s.Read()
	if e != nil || len(active) != 2 {
		t.Fatal(active, e)
	}
	n, e = s.Archive(filepath.Join(root, "reviewed-failures"), time.Now().Add(time.Hour), true)
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	remaining, e := s.Read()
	if e != nil || len(remaining) != 1 || remaining[0].ID != started.ID {
		t.Fatal(remaining, e)
	}
	if _, e = os.Stat(filepath.Join(root, "reviewed-failures", failed.ID+".json")); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(destination, pass.ID+".json"), []byte(`{}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadArchive(destination); e == nil {
		t.Fatal("corrupt archive accepted")
	}
}
func TestArchiveWriteFailureAndLock(t *testing.T) {
	root := t.TempDir()
	s := Store{Dir: filepath.Join(root, "history")}
	finalized(t, s, "passed")
	finalized(t, s, "passed")
	for _, failAt := range []int{1, 2, 3} {
		calls := 0
		destination := filepath.Join(root, fmt.Sprintf("failed-%d", failAt))
		_, e := s.archive(destination, time.Now().Add(time.Hour), false, func(p string, b []byte) error {
			calls++
			if calls == failAt {
				return errors.New("injected disk-full write failure")
			}
			return exclusiveSynced(p, b)
		})
		if e == nil {
			t.Fatal("failure reported success")
		}
		records, e := s.Read()
		if e != nil || len(records) != 2 {
			t.Fatal("originals lost", records, e)
		}
		if _, e = ReadArchive(destination); e == nil {
			t.Fatal("uncommitted archive accepted")
		}
	}
	if e := os.Mkdir(filepath.Join(s.Dir, ".lock"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Archive(filepath.Join(root, "busy"), time.Now().Add(time.Hour), false); e == nil {
		t.Fatal("concurrent archive stole lock")
	}
	_ = os.Remove(filepath.Join(s.Dir, ".lock"))
	if _, e := s.Archive(filepath.Join(s.Dir, "nested"), time.Now().Add(time.Hour), false); e == nil {
		t.Fatal("nested archive accepted")
	}
}
func TestArchiveFreesCapacity(t *testing.T) {
	root := t.TempDir()
	s := Store{Dir: filepath.Join(root, "history")}
	if e := os.Mkdir(s.Dir, 0700); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < Limit; i++ {
		r := Record{Version: 1, ID: fmt.Sprintf("%032x", i), Kind: "backup", Started: now, Finished: &now, Outcome: "passed"}
		if e := s.write(r); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.Begin(Record{Kind: "backup"}); e == nil {
		t.Fatal("history exceeded cap")
	}
	if n, e := s.Archive(filepath.Join(root, "archive"), time.Now(), false); e != nil || n != Limit {
		t.Fatal(n, e)
	}
	if _, e := s.Begin(Record{Kind: "backup"}); e != nil {
		t.Fatal("archival did not free capacity", e)
	}
}
func TestBackwardClockDoesNotCorruptJournal(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	r, e := s.Begin(Record{Kind: "verification", Depth: "full-data"})
	if e != nil {
		t.Fatal(e)
	}
	r.Started = time.Now().Add(time.Hour)
	if e = s.write(r); e != nil {
		t.Fatal(e)
	}
	if e = s.Finish(r, "passed", ""); e != nil {
		t.Fatal(e)
	}
	records, e := s.Read()
	if e != nil || len(records) != 1 || records[0].Outcome != "inconclusive" || records[0].Category != "clock_changed" || records[0].DataAuthenticated {
		t.Fatal(records, e)
	}
}
func TestArchiveDetectsChangedRecord(t *testing.T) {
	root := t.TempDir()
	s := Store{Dir: filepath.Join(root, "history")}
	r := finalized(t, s, "passed")
	destination := filepath.Join(root, "archive")
	if _, e := s.Archive(destination, time.Now().Add(time.Hour), false); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(destination, r.ID+".json")
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	b = []byte(strings.ReplaceAll(string(b), "repo-identity", "other-identity"))
	if e = os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadArchive(destination); e == nil || !strings.Contains(e.Error(), "checksum") {
		t.Fatal("modified archive record accepted", e)
	}
}
