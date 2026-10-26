package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func TestWindowsArguments(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	args := []string{`C:\backup with spaces\`, `quote"and\\slashes\`, "日本語 & %PATH% |", "", `C:\config\profiles.json`}
	argv := []string{windowsArg(exe), "-test.run=^TestWindowsArgumentHelper$", "--"}
	for _, a := range args {
		argv = append(argv, windowsArg(a))
	}
	c := exec.Command(exe)
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: strings.Join(argv, " ")}
	c.Env = append(os.Environ(), "VEYRA_ARGUMENT_TEST=1")
	out, e := c.Output()
	if e != nil {
		t.Fatal(e)
	}
	var got []string
	if e = json.Unmarshal(out, &got); e != nil {
		t.Fatalf("%s: %v", out, e)
	}
	if !reflect.DeepEqual(args, got) {
		t.Fatalf("want %q got %q", args, got)
	}
}
func TestWindowsArgumentHelper(t *testing.T) {
	if os.Getenv("VEYRA_ARGUMENT_TEST") != "1" {
		return
	}
	for i, a := range os.Args {
		if a == "--" {
			_ = json.NewEncoder(os.Stdout).Encode(os.Args[i+1:])
			os.Exit(0)
		}
	}
	os.Exit(2)
}
