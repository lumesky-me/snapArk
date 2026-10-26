package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/lumesky-me/snapArk/internal/backend"
)

// Version can be set when preparing a later authorized release. Development
// builds retain their explicit suffix; no date or guessed tag is substituted.
var Version = "0.1.0-dev"

func (a App) version(args []string) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	var structured bool
	fs.BoolVar(&structured, "json", false, "machine-readable build and backend information")
	if e := fs.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Err, "version accepts options only")
		return 2
	}
	revision := "unknown"
	modified := false
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	if structured {
		return encodeResult(a.Out, map[string]any{"version": Version, "revision": revision, "modified": modified, "go_version": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "restic_version": backend.Version}, a.Err)
	}
	name := "snapark"
	if exe, e := os.Executable(); e == nil {
		base := filepath.Base(exe)
		if strings.EqualFold(base, "veyra") || strings.EqualFold(base, "veyra.exe") {
			name = "veyra"
		}
	}
	if _, e := fmt.Fprintf(a.Out, "%s %s (requires restic %s)\n%s/%s %s revision=%s modified=%t\n", name, Version, backend.Version, runtime.GOOS, runtime.GOARCH, runtime.Version(), revision, modified); e != nil {
		return 1
	}
	return 0
}
