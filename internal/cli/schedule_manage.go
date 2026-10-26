package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/lumesky-me/veyra/internal/backend"
	"github.com/lumesky-me/veyra/internal/config"
	"github.com/lumesky-me/veyra/internal/scheduler"
)

func nativeCommand(ctx context.Context) scheduler.Command {
	return func(program string, args []string, input []byte) ([]byte, error) {
		c := exec.CommandContext(ctx, program, args...)
		c.Env = backend.Environment()
		c.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		c.Stderr = &stderr
		out, e := c.Output()
		if e != nil {
			return out, fmt.Errorf("%s: %w: %s", program, e, strings.TrimSpace(stderr.String()))
		}
		return out, nil
	}
}
func (a App) manageSchedule(ctx context.Context, p config.Profile, path, password, action string, confirm, structured bool) int {
	fail := func(e error) int { fmt.Fprintln(a.Err, e); return 1 }
	store, e := configStore(path)
	if e != nil {
		return fail(e)
	}
	owner := scheduleOwner(p, store.Path)
	name := "veyra-" + p.Name
	command := nativeCommand(ctx)
	var apply func(string, string) error
	var status func() (scheduler.State, error)
	var definition string
	switch runtime.GOOS {
	case "windows":
		m := scheduler.TaskManager{Dir: store.Path + ".schedules", Name: name, Owner: owner, Command: command}
		apply = m.Apply
		status = m.Status
	case "darwin":
		home, e := os.UserHomeDir()
		if e != nil {
			return fail(e)
		}
		m := scheduler.LaunchManager{Dir: filepath.Join(home, "Library", "LaunchAgents"), Name: "me.lumesky.veyra." + p.Name, Owner: owner, Domain: "gui/" + strconv.Itoa(os.Getuid()), Records: store.Path + ".schedules", Command: command}
		apply = m.Apply
		status = m.Status
	case "linux":
		root, e := os.UserConfigDir()
		if e != nil {
			return fail(e)
		}
		m := scheduler.Manager{Dir: filepath.Join(root, "systemd", "user"), Name: name, Owner: owner, Run: func(args ...string) error {
			out, e := command("systemctl", append([]string{"--user"}, args...), nil)
			if e != nil {
				return fmt.Errorf("%s: %w", out, e)
			}
			return nil
		}}
		apply = func(action, unused string) error {
			switch action {
			case "install":
				files, e := schedule(p, path, password)
				if e != nil {
					return e
				}
				return m.Install(files)
			case "enable":
				return m.Enable()
			case "disable":
				return m.Disable()
			case "remove":
				return m.Remove()
			default:
				return errors.New("systemd replacement requires explicit removal and installation")
			}
		}
		status = func() (scheduler.State, error) { return systemdState(m, command) }
	default:
		return fail(errors.New("native scheduling is unsupported on this platform"))
	}
	if action == "status" {
		state, e := status()
		if structured {
			if code := encodeResult(a.Out, state, a.Err); code != 0 {
				return code
			}
		} else {
			fmt.Fprintf(a.Out, "%s: %s\n", state.State, state.Detail)
		}
		if e != nil {
			return fail(e)
		}
		return 0
	}
	if action != "install" && action != "replace" && action != "enable" && action != "disable" && action != "remove" {
		return fail(errors.New("use schedule preview, install, replace (Windows), status, enable, disable or remove"))
	}
	if !confirm {
		return fail(fmt.Errorf("%s changes the current-user scheduler; inspect schedule preview first, then repeat with --confirm", action))
	}
	if action == "install" || action == "replace" {
		if password == "" || !filepath.IsAbs(password) {
			return fail(errors.New("installation requires an absolute --password-file"))
		}
		release, e := backend.ProtectPassword(password)
		if e != nil {
			return fail(e)
		}
		defer release()
		r := backend.Repository{Path: p.Repository, PasswordFile: password}
		if e = r.Validate(ctx); e != nil {
			return fail(e)
		}
		for _, src := range p.Sources {
			root, e := backend.Canonical(src)
			if e != nil {
				return fail(e)
			}
			pw, e := backend.Canonical(password)
			if e != nil {
				return fail(e)
			}
			if backend.Within(root, pw) {
				return fail(errors.New("password file must be outside every source"))
			}
		}
		files, e := schedule(p, path, password)
		if e != nil {
			return fail(e)
		}
		if runtime.GOOS == "windows" {
			definition = files[name+".xml"]
		} else if runtime.GOOS == "darwin" {
			definition = files[name+".plist"]
		}
	}
	if e = apply(action, definition); e != nil {
		return fail(e)
	}
	if structured {
		return encodeResult(a.Out, map[string]string{"result": "applied", "operation": action}, a.Err)
	}
	fmt.Fprintln(a.Out, "Current-user schedule", action+" completed.")
	return 0
}
func systemdState(m scheduler.Manager, command scheduler.Command) (scheduler.State, error) {
	data, e := command("systemctl", []string{"--user", "show", m.Name + ".timer", "--property=LoadState,ActiveState,UnitFileState,FragmentPath,DropInPaths"}, nil)
	if e != nil {
		return scheduler.State{State: "inaccessible", Detail: e.Error()}, e
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			props[k] = v
		}
	}
	if props["LoadState"] == "not-found" {
		if e := m.Owned(); errors.Is(e, os.ErrNotExist) {
			return scheduler.State{State: "missing"}, nil
		}
		return scheduler.State{State: "unknown", Detail: "unit files exist but user manager has not loaded them"}, errors.New("systemd unit state unknown")
	}
	if e = m.Owned(); e != nil {
		return scheduler.State{State: "conflicting", Detail: e.Error()}, e
	}
	if props["FragmentPath"] != filepath.Join(m.Dir, m.Name+".timer") || props["DropInPaths"] != "" {
		return scheduler.State{State: "conflicting", Installed: true, Detail: "systemd loaded a different definition or override"}, errors.New("systemd definition conflict")
	}
	enabled := props["UnitFileState"] == "enabled"
	loaded := props["ActiveState"] == "active"
	state := "installed"
	if enabled {
		state = "enabled"
	}
	if props["UnitFileState"] == "disabled" {
		state = "disabled"
	}
	return scheduler.State{State: state, Installed: true, Enabled: &enabled, Loaded: &loaded, Detail: "user timer; active timer does not establish backup success"}, nil
}
