package journal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJournalLifecycleAndLock(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "history")}
	r, e := s.Begin(Record{Kind: "verification", Depth: "full-data"})
	if e != nil {
		t.Fatal(e)
	}
	records, e := s.Read()
	if e != nil || len(records) != 1 || records[0].Outcome != "started" {
		t.Fatal(records, e)
	}
	if e = s.Finish(r, "passed", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.Finish(r, "passed", ""); e == nil {
		t.Fatal("event rewritten after finalization")
	}
	records, e = s.Read()
	if e != nil || records[0].Finished == nil {
		t.Fatal(records, e)
	}
	release, e := s.Acquire("profile")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Acquire("profile"); e == nil {
		t.Fatal("overlap lock stolen")
	}
	release()
	if e = os.WriteFile(filepath.Join(s.Dir, "broken.json"), []byte(`{"version":1}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Read(); e == nil {
		t.Fatal("corruption ignored")
	}
	if _, e = s.Begin(Record{Kind: "backup"}); e == nil {
		t.Fatal("corruption overwritten")
	}
}
func TestStartedDoesNotBecomeSuccessful(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	_, e := s.Begin(Record{Kind: "scheduled-run"})
	if e != nil {
		t.Fatal(e)
	}
	records, e := s.Read()
	if e != nil || records[0].Outcome != "started" || records[0].Finished != nil {
		t.Fatal(records, e)
	}
}
