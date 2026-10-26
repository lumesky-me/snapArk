package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestTaskSchedulerNative(t *testing.T) {
	if os.Getenv("VEYRA_NATIVE_SCHEDULER_TESTS") != "1" {
		t.Skip("set VEYRA_NATIVE_SCHEDULER_TESTS=1 for disposable Task Scheduler lifecycle test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	owner := fmt.Sprintf("native-test-%d", time.Now().UnixNano())
	m := TaskManager{Dir: t.TempDir(), Name: "veyra-test-" + owner, Owner: owner, Command: func(program string, args []string, input []byte) ([]byte, error) {
		c := exec.CommandContext(ctx, program, args...)
		c.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		c.Stderr = &stderr
		out, e := c.Output()
		if e != nil {
			return out, fmt.Errorf("%w: %s", e, stderr.String())
		}
		return out, nil
	}}
	definition := fmt.Sprintf(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><RegistrationInfo><Source>VEYRA owner=%s</Source></RegistrationInfo><Triggers><TimeTrigger><StartBoundary>2099-01-01T00:00:00</StartBoundary><Enabled>true</Enabled></TimeTrigger></Triggers><Principals><Principal id="Author"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><Enabled>true</Enabled></Settings><Actions Context="Author"><Exec><Command>%s</Command><Arguments>/c exit 0</Arguments></Exec></Actions></Task>`, owner, user.User.Sid.String(), filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"))
	if e = m.Apply("install", definition); e != nil {
		t.Fatal(e)
	}
	defer func() {
		o, e := m.call("inspect", "", "")
		if e != nil {
			t.Error(e)
			return
		}
		if o.Exists && strings.Contains(o.XML, "VEYRA owner="+owner) {
			if _, e = m.call("remove", "", o.XML); e != nil {
				t.Error("cleanup", e)
			}
		}
	}()
	if s, e := m.Status(); e != nil || s.State != "enabled" {
		t.Fatal(s, e)
	}
	if e = m.Apply("disable", ""); e != nil {
		t.Fatal(e)
	}
	if s, e := m.Status(); e != nil || s.State != "disabled" {
		t.Fatal(s, e)
	}
	if e = m.Apply("replace", strings.ReplaceAll(definition, "/c exit 0", "/c exit 1")); e != nil {
		t.Fatal(e)
	}
	if s, e := m.Status(); e != nil || s.State != "disabled" {
		t.Fatal("replacement enabled a disabled task", s, e)
	}
	if e = m.Apply("enable", ""); e != nil {
		t.Fatal(e)
	}
	// A manager for a different config cannot claim this task.
	foreign := m
	foreign.Owner = "other-profile"
	if e = foreign.Apply("remove", ""); e == nil {
		t.Fatal("foreign manager deleted task")
	}
	if e = m.Apply("remove", ""); e != nil {
		t.Fatal(e)
	}
	if s, e := m.Status(); e != nil || s.State != "missing" {
		t.Fatal(s, e)
	}
}
