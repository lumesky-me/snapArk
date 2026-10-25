// Package config stores non-secret backup profiles outside repositories.
package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type Retention struct {
	Last    int `json:"last,omitempty"`
	Daily   int `json:"daily,omitempty"`
	Weekly  int `json:"weekly,omitempty"`
	Monthly int `json:"monthly,omitempty"`
	Yearly  int `json:"yearly,omitempty"`
}

func (r Retention) Validate() error {
	for _, n := range []int{r.Last, r.Daily, r.Weekly, r.Monthly, r.Yearly} {
		if n < 0 {
			return errors.New("retention counts cannot be negative")
		}
	}
	return nil
}
func (r Retention) Empty() bool { return r == Retention{} }

type Profile struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Repository    string    `json:"repository"`
	Sources       []string  `json:"sources"`
	Include       []string  `json:"include,omitempty"`
	Exclude       []string  `json:"exclude,omitempty"`
	Label         string    `json:"label,omitempty"`
	Retention     Retention `json:"retention"`
	Verify        string    `json:"verify"`
	Disabled      bool      `json:"disabled,omitempty"`
	ExpectedHours int       `json:"expected_hours,omitempty"`
}
type File struct {
	Version  int                `json:"version"`
	Default  string             `json:"default,omitempty"`
	Profiles map[string]Profile `json:"profiles"`
}
type Store struct{ Path string }

func DefaultStore() (Store, error) {
	root, e := os.UserConfigDir()
	return Store{Path: filepath.Join(root, "veyra", "profiles.json")}, e
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func NewID() (string, error) {
	var b [16]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}
func (p Profile) Validate() error {
	if !namePattern.MatchString(p.Name) || !idPattern.MatchString(p.ID) {
		return errors.New("profile name or ID is invalid (names use letters, numbers, underscore and hyphen)")
	}
	if !filepath.IsAbs(p.Repository) || len(p.Sources) == 0 {
		return errors.New("profile needs an absolute repository path and at least one source")
	}
	for _, s := range p.Sources {
		if !filepath.IsAbs(s) {
			return errors.New("profile source paths must be absolute")
		}
	}
	for _, s := range p.Include {
		if s == "" || !filepath.IsLocal(s) {
			return errors.New("includes must be relative file or directory paths within sources")
		}
	}
	if p.Verify != "metadata" && p.Verify != "full" {
		return errors.New("verification must be metadata or full")
	}
	if p.ExpectedHours < 0 || p.ExpectedHours > 87600 {
		return errors.New("expected backup interval cannot be negative")
	}
	return p.Retention.Validate()
}

// Scope changes with source/filter configuration; retention cannot reach an old
// profile revision or another host merely because its name happens to match.
func (p Profile) Tag() string {
	b, _ := json.Marshal(struct{ Sources, Include, Exclude []string }{p.Sources, p.Include, p.Exclude})
	h := sha256.Sum256(b)
	return "veyra:profile:" + p.ID + ":" + hex.EncodeToString(h[:8])
}
func (s Store) Read() (File, error) {
	f := File{Version: 1, Profiles: map[string]Profile{}}
	info, e := os.Lstat(s.Path)
	if errors.Is(e, os.ErrNotExist) {
		return f, nil
	}
	if e != nil {
		return f, e
	}
	if info.Size() > 4<<20 {
		return f, errors.New("configuration exceeds 4 MiB limit")
	}
	if !info.Mode().IsRegular() {
		return f, errors.New("configuration must be a regular file, not a symbolic link")
	}
	file, e := os.Open(s.Path)
	if e != nil {
		return f, e
	}
	defer file.Close()
	d := json.NewDecoder(io.LimitReader(file, 4<<20))
	d.DisallowUnknownFields()
	if e = d.Decode(&f); e != nil {
		return f, fmt.Errorf("invalid profiles configuration: %w", e)
	}
	var extra any
	if e = d.Decode(&extra); e != io.EOF {
		return f, errors.New("configuration contains trailing data")
	}
	if f.Version != 1 {
		return f, fmt.Errorf("unsupported configuration version %d; file left unchanged", f.Version)
	}
	if f.Profiles == nil {
		return f, errors.New("configuration profiles map is missing")
	}
	ids := map[string]bool{}
	for name, p := range f.Profiles {
		if name != p.Name {
			return f, errors.New("profile key does not match its name")
		}
		if e = p.Validate(); e != nil {
			return f, e
		}
		if ids[p.ID] {
			return f, errors.New("duplicate profile identity")
		}
		ids[p.ID] = true
	}
	if f.Default != "" {
		if _, ok := f.Profiles[f.Default]; !ok {
			return f, errors.New("default profile does not exist")
		}
	}
	return f, nil
}
func (s Store) Update(edit func(*File) error) error {
	dir := filepath.Dir(s.Path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	lock := s.Path + ".lock"
	if e := os.Mkdir(lock, 0700); e != nil {
		return fmt.Errorf("configuration is busy or a previous writer was interrupted; inspect %s before removing the lock: %w", lock, e)
	}
	defer os.Remove(lock)
	f, e := s.Read()
	if e != nil {
		return e
	}
	if e = edit(&f); e != nil {
		return e
	}
	// Validate the complete edited document before replacing the last good copy.
	for name, p := range f.Profiles {
		if p.Name != name {
			return errors.New("invalid profile name")
		}
		if e = p.Validate(); e != nil {
			return e
		}
	}
	if f.Default != "" {
		if _, ok := f.Profiles[f.Default]; !ok {
			return errors.New("default profile must exist")
		}
	}
	b, e := json.MarshalIndent(f, "", "  ")
	if e != nil {
		return e
	}
	tmp, e := os.CreateTemp(dir, ".profiles-*")
	if e != nil {
		return e
	}
	defer os.Remove(tmp.Name())
	if _, e = tmp.Write(append(b, '\n')); e == nil {
		e = tmp.Sync()
	}
	closeErr := tmp.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Rename(tmp.Name(), s.Path); e != nil {
		return e
	}
	// Directory sync is unavailable on some platforms/filesystems; rename remains
	// atomic where supported. No claim is made about surviving hardware write loss.
	if d, e := os.Open(dir); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
func (f File) Select(name string) (Profile, error) {
	if name == "" {
		name = f.Default
	}
	p, ok := f.Profiles[name]
	if !ok {
		return p, fmt.Errorf("profile %q not found; use veyra profile list", name)
	}
	return p, p.Validate()
}
func (p Profile) Interval() time.Duration { return time.Duration(p.ExpectedHours) * time.Hour }
