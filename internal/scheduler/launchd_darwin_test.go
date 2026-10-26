package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLaunchPlistNative(t *testing.T) {
	definition, e := LaunchDefinition("me.test.veyra", "test", []string{"/Applications/hello ü & space/veyra", "arg\"quote"}, 3600)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(t.TempDir(), "agent.plist")
	if e = os.WriteFile(p, []byte(definition), 0600); e != nil {
		t.Fatal(e)
	}
	if out, e := exec.Command("/usr/bin/plutil", "-lint", p).CombinedOutput(); e != nil {
		t.Fatalf("%s: %v", out, e)
	}
}
func TestLaunchdNative(t *testing.T) {
	if os.Getenv("SNAPARK_NATIVE_SCHEDULER_TESTS") != "1" && os.Getenv("VEYRA_NATIVE_SCHEDULER_TESTS") != "1" {
		t.Skip("set SNAPARK_NATIVE_SCHEDULER_TESTS=1 for disposable launchd lifecycle test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := func(program string, args []string, input []byte) ([]byte, error) {
		c := exec.CommandContext(ctx, program, args...)
		c.Stdin = bytes.NewReader(input)
		out, e := c.CombinedOutput()
		if e != nil {
			return out, fmt.Errorf("%w: %s", e, out)
		}
		return out, nil
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	if _, e := command("/bin/launchctl", []string{"print", domain}, nil); e != nil {
		if os.Getenv("CI") == "true" {
			t.Fatalf("native scheduler validation requires an accessible GUI domain: %v", e)
		}
		t.Skipf("no desktop user launchd domain on this runner: %v", e)
	}
	root := t.TempDir()
	home, e := os.UserHomeDir()
	if e != nil {
		t.Fatal(e)
	}
	owner := fmt.Sprintf("test-%d", time.Now().UnixNano())
	m := LaunchManager{Dir: filepath.Join(home, "Library", "LaunchAgents"), Records: filepath.Join(root, "records"), Name: "me.lumesky.veyra." + owner, Owner: owner, Domain: domain, Command: command}
	definition, e := LaunchDefinition(m.Name, owner, []string{"/usr/bin/true"}, 3600)
	if e != nil {
		t.Fatal(e)
	}
	// Cleanup is scoped to this randomly named, self-owned disposable test job.
	defer func() {
		if e := m.owned(); e == nil {
			_, _ = m.call("bootout", m.Domain+"/"+m.Name)
			_ = os.Remove(m.file())
		}
	}()
	if e = m.Apply("install", definition); e != nil {
		if out, err := exec.Command("/usr/bin/plutil", "-p", m.file()).CombinedOutput(); err == nil {
			t.Logf("native plist: %s", out)
		}

		if out, err := exec.Command("/usr/bin/log", "show", "--last", "1m", "--style", "compact", "--predicate", `process == "launchd" AND eventMessage CONTAINS "`+m.Name+`"`).CombinedOutput(); len(out) > 0 {
			t.Logf("launchd diagnostics (%v): %s", err, out)
		}
		t.Fatalf("uid=%d domain=%s plist=%s: %v", os.Getuid(), m.Domain, m.file(), e)
	}
	if s, e := m.Status(); e != nil || s.State != "enabled" {
		t.Fatal(s, e)
	}
	if e = m.Apply("disable", ""); e != nil {
		t.Fatal(e)
	}
	if s, e := m.Status(); e != nil || s.State != "disabled" {
		if out, err := m.call("print-disabled", m.Domain); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if strings.Contains(line, m.Name) {
					t.Log("native disabled state:", line)
				}
			}
		}
		t.Fatal(s, e)
	}
	if e = m.Apply("enable", ""); e != nil {
		t.Fatal(e)
	}
	foreign := m
	foreign.Owner = "unrelated"
	if e = foreign.Apply("remove", ""); e == nil {
		t.Fatal("foreign owner removed agent")
	}
	if e = m.Apply("remove", ""); e != nil {
		t.Fatal(e)
	}
}
