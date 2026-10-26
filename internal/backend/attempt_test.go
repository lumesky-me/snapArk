package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectIncompleteRestore(t *testing.T) {
	root := t.TempDir()
	target, e := Canonical(filepath.Join(root, "partial"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(target, 0700); e != nil {
		t.Fatal(e)
	}
	marker := target + ".veyra-incomplete.json"
	b, _ := json.Marshal(RestoreAttempt{Version: 1, State: "incomplete", Snapshot: strings.Repeat("a", 64), Repository: strings.Repeat("b", 64), Target: target, Root: "/original"})
	if e = os.WriteFile(marker, b, 0600); e != nil {
		t.Fatal(e)
	}
	report, e := InspectRestore(target)
	if e != nil || report.Output != "partial directory present; contents not certified" {
		t.Fatal(report, e)
	}
	after, e := os.ReadFile(marker)
	if e != nil || string(after) != string(b) {
		t.Fatal("inspection changed marker")
	}
	if e = os.Remove(target); e != nil {
		t.Fatal(e)
	}
	if report, e = InspectRestore(target); e != nil || report.Output != "missing" {
		t.Fatal(report, e)
	}
	if e = os.WriteFile(marker, []byte(`{"state":"incomplete","snapshot":"invalid","target":"/wrong"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = InspectRestore(target); e == nil {
		t.Fatal("invalid marker accepted")
	}
}
