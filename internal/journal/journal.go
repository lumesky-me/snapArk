// Package journal stores local operational evidence, not tamper-proof attestations.
package journal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const Limit = 1000

var ErrRunBusy = errors.New("a snapArk run is active or its lock needs manual review; no lock was stolen")

type Record struct {
	MetadataVerified  bool       `json:"metadata_verified"`
	DataAuthenticated bool       `json:"all_repository_data_authenticated"`
	Evidence          string     `json:"recovery_evidence,omitempty"`
	Version           int        `json:"version"`
	ID                string     `json:"id"`
	Kind              string     `json:"kind"`
	Repository        string     `json:"repository_id,omitempty"`
	Profile           string     `json:"profile_id,omitempty"`
	Started           time.Time  `json:"started"`
	Finished          *time.Time `json:"finished,omitempty"`
	Depth             string     `json:"depth"`
	Outcome           string     `json:"outcome"`
	Category          string     `json:"error_category,omitempty"`
	Scope             string     `json:"snapshot_scope,omitempty"`
	Snapshot          string     `json:"snapshot,omitempty"`
	Files             []string   `json:"tested_files,omitempty"`
	Veyra             string     `json:"veyra_version"` // Keep the version-one key readable by both commands.
	Restic            string     `json:"restic_version"`
}
type Store struct{ Dir string }

func (s Store) Read() ([]Record, error) {
	entries, e := os.ReadDir(s.Dir)
	if errors.Is(e, os.ErrNotExist) {
		return []Record{}, nil
	}
	if e != nil {
		return nil, e
	}
	records := []Record{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		p := filepath.Join(s.Dir, entry.Name())
		info, e := os.Lstat(p)
		if e != nil {
			return nil, e
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return nil, errors.New("invalid journal record file")
		}
		f, e := os.Open(p)
		if e != nil {
			return nil, e
		}
		var r Record
		d := json.NewDecoder(io.LimitReader(f, 1<<20))
		d.DisallowUnknownFields()
		e = d.Decode(&r)
		if e == nil {
			var extra any
			if d.Decode(&extra) != io.EOF {
				e = errors.New("trailing journal data")
			}
		}
		_ = f.Close()
		if e != nil || r.Version != 1 || r.ID+".json" != entry.Name() || r.Started.IsZero() || !valid(r) {
			return nil, fmt.Errorf("corrupt or unsupported history record %s", entry.Name())
		}
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Started.Before(records[j].Started) })
	return records, nil
}
func (s Store) write(r Record) error {
	f, e := os.CreateTemp(s.Dir, ".event-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	e = json.NewEncoder(f).Encode(r)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e := os.Rename(f.Name(), filepath.Join(s.Dir, r.ID+".json")); e != nil {
		return e
	}
	return syncDirectory(s.Dir)
}
func (s Store) mutate(edit func([]Record) error) error {
	if e := s.directory(); e != nil {
		return e
	}
	lock := filepath.Join(s.Dir, ".lock")
	if e := os.Mkdir(lock, 0700); e != nil {
		return fmt.Errorf("history busy; inspect stale lock without stealing it: %w", e)
	}
	defer os.Remove(lock)
	records, e := s.Read()
	if e != nil {
		return e
	}
	return edit(records)
}
func (s Store) Begin(r Record) (Record, error) {
	var id [16]byte
	if _, e := rand.Read(id[:]); e != nil {
		return r, e
	}
	r.ID = hex.EncodeToString(id[:])
	r.Version = 1
	r.Started = time.Now().UTC()
	r.Finished = nil
	r.Outcome = "started"
	e := s.mutate(func(records []Record) error {
		if len(records) >= Limit {
			return errors.New("history limit reached; archive or remove reviewed records explicitly before continuing")
		}
		return s.write(r)
	})
	return r, e
}
func (s Store) Finish(r Record, outcome, category string) error {
	return s.mutate(func(records []Record) error {
		found := false
		for _, old := range records {
			if old.ID == r.ID {
				if old.Outcome != "started" {
					return errors.New("history record is already finalized")
				}
				found = true
			}
		}
		if !found {
			return errors.New("history start record is missing")
		}
		now := time.Now().UTC()
		if now.Before(r.Started) {
			outcome = "inconclusive"
			category = "clock_changed"
		}
		r.Finished = &now
		r.Outcome = outcome
		r.Category = category
		r.MetadataVerified = r.Kind == "verification" && outcome == "passed"
		r.DataAuthenticated = r.MetadataVerified && r.Depth == "full-data"
		return s.write(r)
	})
}

// Acquire is fail-fast and deliberately does not steal a lock after a crash.
func (s Store) Acquire(key string) (func(), error) {
	if strings.ContainsAny(key, "/\\") || key == "" || key == "." || key == ".." {
		return nil, errors.New("invalid operation lock key")
	}
	if e := s.directory(); e != nil {
		return nil, e
	}
	p := filepath.Join(s.Dir, "run-"+key+".lock")
	if e := os.Mkdir(p, 0700); e != nil {
		if errors.Is(e, os.ErrExist) {
			return nil, ErrRunBusy
		}
		return nil, fmt.Errorf("cannot create profile run lock: %w", e)
	}
	return func() { _ = os.Remove(p) }, nil
}

func valid(r Record) bool {
	expectedMetadata := r.Kind == "verification" && r.Outcome == "passed"
	if r.MetadataVerified != expectedMetadata || r.DataAuthenticated != (expectedMetadata && r.Depth == "full-data") {
		return false
	}
	if len(r.ID) != 32 {
		return false
	}
	if _, e := hex.DecodeString(r.ID); e != nil {
		return false
	}
	if r.Kind != "verification" && r.Kind != "backup" && r.Kind != "scheduled-run" && r.Kind != "recovery-test" {
		return false
	}
	switch r.Outcome {
	case "started":
		return r.Finished == nil
	case "passed", "failed", "interrupted", "inconclusive":
		return r.Finished != nil && !r.Finished.IsZero() && (!r.Finished.Before(r.Started) || r.Category == "clock_changed")
	default:
		return false
	}
}

func (s Store) directory() error {
	if e := os.MkdirAll(filepath.Dir(s.Dir), 0700); e != nil {
		return e
	}
	if e := privateDirectory(s.Dir); e != nil {
		if !errors.Is(e, os.ErrExist) {
			return e
		}
		info, err := os.Lstat(s.Dir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("history must be a directory, not a symbolic link")
		}
	}
	return nil
}
