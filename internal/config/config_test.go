package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func testProfile(t *testing.T) Profile {
	t.Helper()
	id, e := NewID()
	if e != nil {
		t.Fatal(e)
	}
	return Profile{ID: id, Name: "app", Repository: filepath.Join(t.TempDir(), "repo"), Sources: []string{t.TempDir()}, Verify: "metadata"}
}
func TestStoreLifecycle(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "settings", "profiles.json")}
	p := testProfile(t)
	if e := s.Update(func(f *File) error { f.Profiles[p.Name] = p; f.Default = p.Name; return nil }); e != nil {
		t.Fatal(e)
	}
	f, e := s.Read()
	if e != nil {
		t.Fatal(e)
	}
	selected, e := f.Select("")
	if e != nil || selected.ID != p.ID {
		t.Fatalf("default: %+v %v", selected, e)
	}
	tag := selected.Tag()
	p.Retention.Last = 3
	if tag != p.Tag() {
		t.Fatal("retention changed snapshot scope")
	}
	p.Exclude = []string{"build"}
	if tag == p.Tag() {
		t.Fatal("source filter change reused old scope")
	}
	before, e := os.ReadFile(s.Path)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Update(func(f *File) error { v := f.Profiles["app"]; v.Verify = "invalid"; f.Profiles["app"] = v; return nil }); e == nil {
		t.Fatal("invalid edit saved")
	}
	after, _ := os.ReadFile(s.Path)
	if string(before) != string(after) {
		t.Fatal("failed edit changed last good configuration")
	}
	if e = s.Update(func(f *File) error { delete(f.Profiles, "app"); f.Default = ""; return nil }); e != nil {
		t.Fatal(e)
	}
	f, e = s.Read()
	if e != nil || len(f.Profiles) != 0 {
		t.Fatalf("delete: %v %v", f, e)
	}
}
func TestCorruptionAndVersions(t *testing.T) {
	for _, data := range []string{`{"version":99,"profiles":{}}`, `{"version":1,"profiles":{},"password":"secret"}`, `{"version":1`, `{"version":1,"profiles":{}} {}`} {
		s := Store{Path: filepath.Join(t.TempDir(), "profiles.json")}
		if e := os.WriteFile(s.Path, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Read(); e == nil {
			t.Fatal("invalid configuration accepted", data)
		}
		if e := s.Update(func(f *File) error { return nil }); e == nil {
			t.Fatal("corruption overwritten")
		}
		b, _ := os.ReadFile(s.Path)
		if string(b) != data {
			t.Fatal("corrupt file was modified")
		}
	}
}
func TestConcurrentUpdates(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "profiles.json")}
	p := testProfile(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if e := s.Update(func(f *File) error { close(entered); <-release; f.Profiles[p.Name] = p; return nil }); e != nil {
			t.Error(e)
		}
	}()
	<-entered
	if e := s.Update(func(f *File) error { t.Error("second writer acquired lock"); return nil }); e == nil {
		t.Error("concurrent writer was allowed")
	}
	close(release)
	wg.Wait()
	if _, e := s.Read(); e != nil {
		t.Fatal(e)
	}
	// Orphan temp files are never interpreted as a committed configuration.
	if e := os.WriteFile(filepath.Join(filepath.Dir(s.Path), ".profiles-orphan"), []byte("interrupted write"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Read(); e != nil {
		t.Fatal(e)
	}
}
