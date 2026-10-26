package scheduler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type LaunchManager struct {
	Dir, Name, Owner, Domain, Records string
	Command                           Command
}

func (m LaunchManager) file() string      { return filepath.Join(m.Dir, m.Name+".plist") }
func (m LaunchManager) records() receipts { return receipts{m.Records, m.Name, m.Owner} }
func (m LaunchManager) call(args ...string) ([]byte, error) {
	return m.Command("/bin/launchctl", args, nil)
}
func (m LaunchManager) owned() error {
	r, e := m.records().read()
	if e != nil {
		return e
	}
	info, e := os.Lstat(m.file())
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return errors.New("LaunchAgent is not a supported regular file")
	}
	b, e := os.ReadFile(m.file())
	if e != nil {
		return e
	}
	if fingerprint(b) != r.Definition || !strings.Contains(string(b), "VEYRA owner="+m.Owner) {
		return errors.New("LaunchAgent is unrelated or externally modified")
	}
	return nil
}
func (m LaunchManager) observed() (loaded, disabled bool, detail string, err error) {
	if _, e := m.call("print", m.Domain); e != nil {
		return false, false, "", fmt.Errorf("launchd GUI domain unavailable (log in to a desktop session): %w", e)
	}
	out, e := m.call("print", m.Domain+"/"+m.Name)
	if e == nil {
		loaded = true
		detail = string(out)
	} else if !strings.Contains(string(out)+" "+e.Error(), "Could not find service") {
		return false, false, "", fmt.Errorf("launchd job state unknown: %w", e)
	}
	out, e = m.call("print-disabled", m.Domain)
	if e != nil {
		return loaded, false, detail, fmt.Errorf("launchd disabled state unknown: %w", e)
	}
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(m.Name) + `"\s*=>\s*(true|false)`)
	match := pattern.FindSubmatch(out)
	disabled = len(match) > 1 && string(match[1]) == "true"
	return
}
func (m LaunchManager) loadedOwner(detail string) error {
	// launchctl reports the origin plist. Do not boot out a job loaded from a
	// different path merely because it has the same label.
	for _, line := range strings.Split(detail, "\n") {
		if strings.TrimSpace(line) == "path = "+m.file() {
			return nil
		}
	}
	return errors.New("loaded job origin cannot be matched to the owned LaunchAgent; nothing modified")
}
func (m LaunchManager) Status() (State, error) {
	loaded, disabled, detail, e := m.observed()
	if e != nil {
		return State{State: "inaccessible", Detail: e.Error()}, e
	}
	_, e = os.Lstat(m.file())
	if errors.Is(e, os.ErrNotExist) {
		if loaded {
			return State{State: "conflicting", Loaded: &loaded, Detail: "label is occupied without an owned plist"}, errors.New("launchd label conflict")
		}
		return State{State: "missing", Loaded: &loaded}, nil
	}
	if e != nil {
		return State{State: "inaccessible", Detail: e.Error()}, e
	}
	if e = m.owned(); e != nil {
		return State{State: "conflicting", Installed: true, Loaded: &loaded, Detail: e.Error()}, e
	}
	if loaded {
		if e = m.loadedOwner(detail); e != nil {
			return State{State: "conflicting", Installed: true, Loaded: &loaded, Detail: e.Error()}, e
		}
	}
	enabled := !disabled
	state := "installed"
	if loaded {
		state = "enabled"
	}
	if disabled {
		state = "disabled"
	}
	return State{State: state, Installed: true, Loaded: &loaded, Enabled: &enabled, Detail: "user LaunchAgent; loading requires a GUI login session; loaded does not mean a backup is running"}, nil
}
func (m LaunchManager) Apply(action, definition string) error {
	release, e := m.records().lock()
	if e != nil {
		return e
	}
	defer release()
	loaded, _, detail, e := m.observed()
	if e != nil {
		return e
	}
	if action == "install" {
		if loaded {
			return errors.New("launchd label already loaded; nothing overwritten")
		}
		if _, e = os.Lstat(m.records().path()); !errors.Is(e, os.ErrNotExist) {
			return errors.New("ownership record exists; inspect interrupted installation")
		}
		if !strings.Contains(definition, "VEYRA owner="+m.Owner) {
			return errors.New("definition has no matching owner")
		}
		if e = os.MkdirAll(m.Dir, 0700); e != nil {
			return e
		}
		f, e := os.OpenFile(m.file(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return fmt.Errorf("LaunchAgent conflict; nothing overwritten: %w", e)
		}
		_, e = f.WriteString(definition)
		if e == nil {
			e = f.Sync()
		}
		e = errors.Join(e, f.Close())
		if e != nil {
			_ = os.Remove(m.file())
			return e
		}
		if e = m.records().save(fingerprint([]byte(definition))); e != nil {
			return fmt.Errorf("plist retained but ownership save failed; inspect %s: %w", m.file(), e)
		}
		if _, e = m.call("enable", m.Domain+"/"+m.Name); e != nil {
			return fmt.Errorf("owned plist installed but enabling failed; inspect schedule status: %w", e)
		}
		if _, e = m.call("bootstrap", m.Domain, m.file()); e != nil {
			return fmt.Errorf("owned plist retained after bootstrap failure; inspect schedule status before retrying enable: %w", e)
		}
		return nil
	}
	if e = m.owned(); e != nil {
		return e
	}
	if loaded {
		if e = m.loadedOwner(detail); e != nil {
			return e
		}
	}
	switch action {
	case "enable":
		if _, e = m.call("enable", m.Domain+"/"+m.Name); e != nil {
			return e
		}
		if !loaded {
			_, e = m.call("bootstrap", m.Domain, m.file())
		}
		return e
	case "disable", "remove":
		if loaded {
			if _, e = m.call("bootout", m.Domain+"/"+m.Name); e != nil {
				return e
			}
		}
		if _, e = m.call("disable", m.Domain+"/"+m.Name); e != nil {
			return e
		}
		if action == "remove" {
			if e = m.owned(); e != nil {
				return e
			}
			if e = os.Remove(m.file()); e != nil {
				return e
			}
			return os.Remove(m.records().path())
		}
		return nil
	default:
		return errors.New("launchd supports install, status, enable, disable and remove; replace uses explicit removal and installation")
	}
}
