package scheduler

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOwnedLifecycleAndConflict(t *testing.T) {
	dir := t.TempDir()
	calls := [][]string{}
	m := Manager{Dir: dir, Name: "veyra-app", Owner: "identity", Run: func(args ...string) error { calls = append(calls, args); return nil }}
	files := map[string]string{"veyra-app.service": "[Service]\n", "veyra-app.timer": "[Timer]\n"}
	if e := m.Install(files); e != nil {
		t.Fatal(e)
	}
	if e := m.Owned(); e != nil {
		t.Fatal(e)
	}
	if e := m.Install(files); e == nil {
		t.Fatal("existing schedule overwritten")
	}
	if e := m.Remove(); e != nil {
		t.Fatal(e)
	}
	if len(calls) != 4 {
		t.Fatal(calls)
	}
	unrelated := filepath.Join(dir, "veyra-app.service")
	if e := os.WriteFile(unrelated, []byte("unrelated"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := m.Remove(); e == nil {
		t.Fatal("unrelated entry removed")
	}
	b, _ := os.ReadFile(unrelated)
	if string(b) != "unrelated" {
		t.Fatal("unrelated data modified")
	}
}
func TestInstallRollback(t *testing.T) {
	m := Manager{Dir: t.TempDir(), Name: "veyra-app", Owner: "id", Run: func(args ...string) error {
		if args[0] == "enable" {
			return errors.New("injected failure")
		}
		return nil
	}}
	if e := m.Install(map[string]string{"veyra-app.service": "[Service]", "veyra-app.timer": "[Timer]"}); e == nil {
		t.Fatal("failed install reported success")
	}
	for _, p := range m.paths() {
		if _, e := os.Stat(p); !os.IsNotExist(e) {
			t.Fatal("failed install left definition", p, e)
		}
	}
}

func TestConcurrentSystemdChangeRefused(t *testing.T) {
	m := Manager{Dir: t.TempDir(), Name: "veyra-app", Owner: "profile:config"}
	checked := false
	m.Run = func(args ...string) error {
		if !checked && args[0] == "daemon-reload" {
			checked = true
			if e := m.Remove(); e == nil {
				t.Fatal("removal raced an unfinished installation")
			}
		}
		return nil
	}
	if e := m.Install(map[string]string{"veyra-app.service": "[Service]\n", "veyra-app.timer": "[Timer]\n"}); e != nil {
		t.Fatal(e)
	}
	if !checked {
		t.Fatal("concurrent operation was not exercised")
	}
	if e := m.Owned(); e != nil {
		t.Fatal("concurrent operation damaged definitions", e)
	}
	if e := m.Remove(); e != nil {
		t.Fatal("lock was not released after installation", e)
	}
}
