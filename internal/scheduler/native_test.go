package scheduler

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskLifecycle(t *testing.T) {
	var task taskObservation
	m := TaskManager{Dir: t.TempDir(), Name: "veyra-test", Owner: "profile:config"}
	m.Command = func(program string, args []string, input []byte) ([]byte, error) {
		if program != "powershell.exe" || args[len(args)-2] != "-EncodedCommand" {
			t.Fatal(program, args)
		}
		var r map[string]string
		if e := json.Unmarshal(input, &r); e != nil {
			t.Fatal(e)
		}
		switch r["action"] {
		case "inspect":
		case "create":
			if task.Exists {
				return nil, errors.New("conflict")
			}
			task = taskObservation{Exists: true, XML: r["xml"], Enabled: true}
		case "replace":
			if r["expected"] != task.XML {
				t.Fatal("expected definition not bound")
			}
			task.XML = r["xml"]
		case "enable":
			task.Enabled = true
		case "disable":
			task.Enabled = false
		case "remove":
			task = taskObservation{}
		default:
			t.Fatal(r)
		}
		return json.Marshal(task)
	}
	xml := `<Task><RegistrationInfo><Source>VEYRA owner=profile:config</Source></RegistrationInfo><Settings><Enabled>true</Enabled></Settings><Actions><Exec><Command>C:\hello ü\veyra.exe</Command></Exec></Actions></Task>`
	if s, e := m.Status(); e != nil || s.State != "missing" {
		t.Fatal(s, e)
	}
	if e := m.Apply("install", xml); e != nil {
		t.Fatal(e)
	}
	if e := m.Apply("install", xml); e == nil {
		t.Fatal("overwrote existing task")
	}
	if e := m.Apply("disable", ""); e != nil {
		t.Fatal(e)
	}
	if s, e := m.Status(); e != nil || s.State != "disabled" {
		t.Fatal(s, e)
	}
	if e := m.Apply("enable", ""); e != nil {
		t.Fatal(e)
	}
	task.XML = strings.ReplaceAll(task.XML, "<Enabled>true", "<Enabled>false")
	if _, e := m.Status(); e != nil {
		t.Fatal("native enable change broke ownership", e)
	}
	task.XML = strings.ReplaceAll(task.XML, "veyra.exe", "edited.exe")
	if e := m.Apply("remove", ""); e == nil {
		t.Fatal("deleted externally modified task")
	}
	if s, _ := m.Status(); s.State != "conflicting" {
		t.Fatal(s)
	}
	task.XML = xml
	next := strings.ReplaceAll(xml, "veyra.exe", "new.exe")
	if e := m.Apply("replace", next); e != nil {
		t.Fatal(e)
	}
	if e := m.Apply("remove", ""); e != nil {
		t.Fatal(e)
	}
	if task.Exists {
		t.Fatal("task retained")
	}
}
func TestLaunchLifecycle(t *testing.T) {
	root := t.TempDir()
	loaded, disabled := false, false
	m := LaunchManager{Dir: filepath.Join(root, "LaunchAgents"), Records: filepath.Join(root, "records"), Name: "me.test.veyra", Owner: "profile:config", Domain: "gui/123"}
	m.Command = func(program string, args []string, input []byte) ([]byte, error) {
		if program != "/bin/launchctl" {
			t.Fatal(program)
		}
		switch args[0] {
		case "print":
			if args[1] == m.Domain {
				return []byte("domain"), nil
			}
			if !loaded {
				return []byte("Could not find service"), errors.New("missing")
			}
			return []byte("job = {\n path = " + m.file() + "\n program = /a & ü/veyra\n arguments = {\n/a & ü/veyra\nbackup\nquote\"\n}\nrun interval = 3600 seconds\n}"), nil
		case "print-disabled":
			return []byte(`{"` + m.Name + `" => ` + map[bool]string{true: "true", false: "false"}[disabled] + `}`), nil
		case "bootstrap":
			loaded = true
		case "bootout":
			loaded = false
		case "enable":
			disabled = false
		case "disable":
			disabled = true
		default:
			t.Fatal(args)
		}
		return nil, nil
	}
	xml, e := LaunchDefinition(m.Name, m.Owner, []string{"/a & ü/veyra", "backup", "quote\""}, 3600)
	if e != nil {
		t.Fatal(e)
	}
	if s, e := m.Status(); e != nil || s.State != "missing" {
		t.Fatal(s, e)
	}
	if e = m.Apply("install", xml); e != nil {
		t.Fatal(e)
	}
	if s, e := m.Status(); e != nil || s.State != "enabled" || !*s.Loaded {
		t.Fatal(s, e)
	}
	if e = m.Apply("install", xml); e == nil {
		t.Fatal("overwrote existing job")
	}
	if e = m.Apply("disable", ""); e != nil {
		t.Fatal(e)
	}
	if s, e := m.Status(); e != nil || s.State != "disabled" || *s.Loaded {
		t.Fatal(s, e)
	}
	if e = m.Apply("enable", ""); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(m.file(), []byte(xml+"<!-- edited -->"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = m.Apply("remove", ""); e == nil {
		t.Fatal("removed edited plist")
	}
	if !loaded {
		t.Fatal("unloaded edited job")
	}
	if e = os.WriteFile(m.file(), []byte(xml), 0600); e != nil {
		t.Fatal(e)
	}
	if e = m.Apply("remove", ""); e != nil {
		t.Fatal(e)
	}
	if loaded {
		t.Fatal("job retained")
	}
}
func TestSystemdEditedDefinitions(t *testing.T) {
	m := Manager{Dir: t.TempDir(), Name: "app", Owner: "test", Run: func(...string) error { return nil }}
	if e := m.Install(map[string]string{"app.service": "[Service]\n", "app.timer": "[Timer]\n"}); e != nil {
		t.Fatal(e)
	}
	p := m.paths()[0]
	f, e := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.WriteString("# user edit\n")
	_ = f.Close()
	if e = m.Remove(); e == nil {
		t.Fatal("removed edited units")
	}
}
