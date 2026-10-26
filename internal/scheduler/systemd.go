// Package scheduler manages explicitly authorized current-user scheduler entries.
package scheduler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Manager struct {
	Dir, Name, Owner string
	Run              func(...string) error
}

func (m Manager) paths() []string {
	return []string{filepath.Join(m.Dir, m.Name+".service"), filepath.Join(m.Dir, m.Name+".timer")}
}
func (m Manager) header() string { return "# VEYRA owner=" + m.Owner + "\n" }
func (m Manager) lock() (func(), error) {
	if e := os.MkdirAll(m.Dir, 0700); e != nil {
		return nil, e
	}
	p := filepath.Join(m.Dir, "."+m.Name+".veyra-lock")
	if e := os.Mkdir(p, 0700); e != nil {
		return nil, fmt.Errorf("scheduler busy or interrupted; inspect %s without stealing its lock: %w", p, e)
	}
	return func() { _ = os.Remove(p) }, nil
}
func (m Manager) Owned() error {
	for _, p := range m.paths() {
		info, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("scheduler entry is not a regular file")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if !strings.HasPrefix(string(b), m.header()) {
			return errors.New("scheduler entry is unrelated or ownership cannot be established")
		}
		definition := strings.TrimPrefix(string(b), m.header())
		line, body, ok := strings.Cut(definition, "\n")
		if !ok || line != "# VEYRA sha256="+fingerprint([]byte(body)) {
			return errors.New("scheduler definition changed or uses a legacy ownership marker; inspect it manually before replacing")
		}
	}
	return nil
}
func (m Manager) Install(files map[string]string) error {
	release, e := m.lock()
	if e != nil {
		return e
	}
	defer release()
	// No overwrites: an update is an explicit remove followed by installation.
	created := []string{}
	rollback := func() {
		for _, p := range created {
			_ = os.Remove(p)
		}
	}
	for _, p := range m.paths() {
		body, ok := files[filepath.Base(p)]
		if !ok {
			rollback()
			return errors.New("missing scheduler definition")
		}
		f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			rollback()
			return fmt.Errorf("scheduler conflict; no existing entry overwritten: %w", e)
		}
		created = append(created, p)
		_, e = f.WriteString(m.header() + "# VEYRA sha256=" + fingerprint([]byte(body)) + "\n" + body)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			rollback()
			return errors.Join(e, closeErr)
		}
	}
	if e := m.Run("daemon-reload"); e != nil {
		rollback()
		return e
	}
	if e := m.Run("enable", "--now", m.Name+".timer"); e != nil {
		if cleanup := m.Run("disable", "--now", m.Name+".timer"); cleanup != nil {
			return fmt.Errorf("partial scheduler installation retained for inspection: %w", errors.Join(e, cleanup))
		}
		rollback()
		reload := m.Run("daemon-reload")
		return errors.Join(e, reload)
	}
	return nil
}
func (m Manager) Disable() error {
	release, e := m.lock()
	if e != nil {
		return e
	}
	defer release()
	return m.disable()
}
func (m Manager) disable() error {
	if e := m.Owned(); e != nil {
		return e
	}
	return m.Run("disable", "--now", m.Name+".timer")
}
func (m Manager) Enable() error {
	release, e := m.lock()
	if e != nil {
		return e
	}
	defer release()
	if e := m.Owned(); e != nil {
		return e
	}
	return m.Run("enable", "--now", m.Name+".timer")
}
func (m Manager) Remove() error {
	release, e := m.lock()
	if e != nil {
		return e
	}
	defer release()
	if e := m.disable(); e != nil {
		return e
	}
	for _, p := range m.paths() {
		if e := os.Remove(p); e != nil {
			return fmt.Errorf("partial removal; inspect owned files: %w", e)
		}
	}
	return m.Run("daemon-reload")
}
