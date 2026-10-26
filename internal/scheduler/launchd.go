package scheduler

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	disabled, err = launchDisabled(string(out), m.Name)
	return
}
func launchDisabled(output, name string) (bool, error) {
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(name) + `"\s*=>\s*([^\s,;}]+)`)
	match := pattern.FindStringSubmatch(output)
	if len(match) == 0 {
		return false, nil
	} // No override: launchd's default is enabled.
	switch match[1] {
	case "true", "disabled":
		return true, nil
	case "false", "enabled":
		return false, nil
	default:
		return false, errors.New("launchd reported an unknown disabled-state value")
	}
}
func (m LaunchManager) loadedOwner(detail string) error {
	b, e := os.ReadFile(m.file())
	if e != nil {
		return e
	}
	args, seconds, e := launchParameters(string(b))
	if e != nil {
		return e
	}
	origin, program, interval := "", "", ""
	observed := []string{}
	inArguments := false
	for _, line := range strings.Split(detail, "\n") {
		text := strings.TrimSpace(line)
		if inArguments {
			if text == "}" {
				inArguments = false
			} else {
				observed = append(observed, text)
			}
			continue
		}
		switch {
		case strings.HasPrefix(text, "path = "):
			origin = strings.TrimPrefix(text, "path = ")
		case strings.HasPrefix(text, "program = "):
			program = strings.TrimPrefix(text, "program = ")
		case text == "arguments = {":
			inArguments = true
		case strings.HasPrefix(text, "run interval = "):
			interval = strings.TrimPrefix(text, "run interval = ")
		}
	}
	if origin != m.file() || len(args) == 0 || program != args[0] || len(observed) != len(args) || interval != strconv.Itoa(seconds)+" seconds" {
		return errors.New("loaded job origin, program, arguments or interval differ from the owned plist, or cannot be observed; nothing modified")
	}
	for i, arg := range args {
		if observed[i] != arg {
			return errors.New("loaded job arguments differ from the owned plist; nothing modified")
		}
	}
	return nil
}
func launchParameters(definition string) ([]string, int, error) {
	d := xml.NewDecoder(strings.NewReader(definition))
	key := ""
	args := []string{}
	seconds := 0
	for {
		t, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, 0, e
		}
		s, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		switch s.Name.Local {
		case "key":
			if e = d.DecodeElement(&key, &s); e != nil {
				return nil, 0, e
			}
		case "array":
			if key == "ProgramArguments" {
				var a struct {
					Strings []string `xml:"string"`
				}
				if e = d.DecodeElement(&a, &s); e != nil {
					return nil, 0, e
				}
				args = a.Strings
			}
		case "integer":
			if key == "StartInterval" {
				if e = d.DecodeElement(&seconds, &s); e != nil {
					return nil, 0, e
				}
			}
		}
	}
	if len(args) == 0 || seconds < 1 {
		return nil, 0, errors.New("invalid owned LaunchAgent parameters")
	}
	return args, seconds, nil
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
