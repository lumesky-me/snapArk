package journal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ArchiveEntry struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}
type ArchiveManifest struct {
	Version int            `json:"version"`
	Created time.Time      `json:"created"`
	Records []ArchiveEntry `json:"records"`
}

// Archive copies finalized records into a new private directory, verifies and
// syncs every copy, then commits a manifest before removing any original.
// Interruption can leave duplicate records, never an uncommitted sole copy.
// Started records stay in the active store, even if they look old.
func (s Store) Archive(destination string, before time.Time, includeFailures bool) (int, error) {
	return s.archive(destination, before, includeFailures, exclusiveSynced)
}
func (s Store) archive(destination string, before time.Time, includeFailures bool, write func(string, []byte) error) (int, error) {
	count := 0
	e := s.mutate(func(records []Record) error {
		source, e := filepath.EvalSymlinks(s.Dir)
		if e != nil {
			return e
		}
		source, e = filepath.Abs(source)
		if e != nil {
			return e
		}
		destination, e = filepath.Abs(destination)
		if e != nil {
			return e
		}
		parent, e := filepath.EvalSymlinks(filepath.Dir(destination))
		if e != nil {
			return e
		}
		destination = filepath.Join(parent, filepath.Base(destination))
		rel, e := filepath.Rel(source, destination)
		if e != nil {
			return e
		}
		if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("archive must be outside active history")
		}
		selected := []Record{}
		for _, r := range records {
			if r.Finished != nil && r.Finished.Before(before) && (r.Outcome == "passed" || includeFailures) {
				selected = append(selected, r)
			}
		}
		if len(selected) == 0 {
			return errors.New("no eligible finalized records; active or unexplained started records are never archived")
		}
		if e = os.Mkdir(destination, 0700); e != nil {
			return fmt.Errorf("archive must be a new directory: %w", e)
		}
		manifest := ArchiveManifest{Version: 1, Created: time.Now().UTC(), Records: []ArchiveEntry{}}
		for _, r := range selected {
			name := r.ID + ".json"
			b, e := os.ReadFile(filepath.Join(source, name))
			if e != nil {
				return e
			}
			if e = write(filepath.Join(destination, name), b); e != nil {
				return fmt.Errorf("archive incomplete; originals retained: %w", e)
			}
			copied, e := os.ReadFile(filepath.Join(destination, name))
			if e != nil {
				return e
			}
			if hashBytes(copied) != hashBytes(b) {
				return errors.New("archive copy differs; originals retained")
			}
			manifest.Records = append(manifest.Records, ArchiveEntry{r.ID, hashBytes(b)})
		}
		b, e := json.MarshalIndent(manifest, "", "  ")
		if e != nil {
			return e
		}
		if e = write(filepath.Join(destination, ".veyra-archive"), b); e != nil {
			return e
		}
		if e = syncDirectory(destination); e != nil {
			return fmt.Errorf("archive directory sync failed; originals retained: %w", e)
		}
		// The manifest and every destination file exist before any original is removed.
		for _, entry := range manifest.Records {
			original := filepath.Join(source, entry.ID+".json")
			b, e := os.ReadFile(original)
			if e != nil {
				return e
			}
			if hashBytes(b) != entry.SHA256 {
				return errors.New("original changed; archive retained and removal stopped")
			}
			if e = os.Remove(original); e != nil {
				return fmt.Errorf("archive committed, partial originals retained; inspect both directories: %w", e)
			}
			count++
		}
		return syncDirectory(source)
	})
	return count, e
}
func exclusiveSynced(path string, b []byte) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	return errors.Join(e, f.Close())
}
func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// ReadArchive verifies the committed manifest as well as normal record validation.
// Hashes detect accidental changes, not tampering by someone who can rewrite both.
func ReadArchive(dir string) ([]Record, error) {
	info, e := os.Lstat(filepath.Join(dir, ".veyra-archive"))
	if e != nil {
		return nil, fmt.Errorf("archive is incomplete or missing its manifest; retain originals: %w", e)
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("invalid archive manifest")
	}
	b, e := os.ReadFile(filepath.Join(dir, ".veyra-archive"))
	if e != nil {
		return nil, e
	}
	var m ArchiveManifest
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&m); e != nil || m.Version != 1 || m.Created.IsZero() || len(m.Records) > Limit {
		return nil, errors.New("corrupt or unsupported archive manifest")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, errors.New("trailing archive manifest data")
	}
	records, e := (Store{Dir: dir}).Read()
	if e != nil {
		return nil, e
	}
	if len(records) != len(m.Records) {
		return nil, errors.New("archive record count differs from committed manifest")
	}
	byID := map[string]Record{}
	for _, r := range records {
		byID[r.ID] = r
	}
	seen := map[string]bool{}
	for _, entry := range m.Records {
		r, ok := byID[entry.ID]
		if !ok || seen[entry.ID] || r.Finished == nil {
			return nil, errors.New("archive contains missing, duplicate or active records")
		}
		seen[entry.ID] = true
		b, e := os.ReadFile(filepath.Join(dir, r.ID+".json"))
		if e != nil {
			return nil, e
		}
		if hashBytes(b) != entry.SHA256 {
			return nil, errors.New("archive record checksum mismatch")
		}
	}
	return records, nil
}
