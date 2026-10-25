package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/lumesky-me/veyra/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestProfileTransferRequiresRemapping(t *testing.T) {
	root := t.TempDir()
	oldCfg := filepath.Join(root, "old.json")
	newCfg := filepath.Join(root, "new.json")
	invoke := func(code int, args ...string) string {
		t.Helper()
		var out, stderr bytes.Buffer
		got := (App{Out: &out, Err: &stderr}).Run(context.Background(), args)
		if got != code {
			t.Fatalf("%v exit %d expected %d: %s %s", args, got, code, out.String(), stderr.String())
		}
		return out.String()
	}
	invoke(0, "profile", "add", "--config", oldCfg, "--repo", filepath.Join(root, "repo"), "--source", filepath.Join(root, "old-source"), "app")
	exported := invoke(0, "profile", "export", "--config", oldCfg, "app")
	file := filepath.Join(root, "export.json")
	if e := os.WriteFile(file, []byte(exported), 0600); e != nil {
		t.Fatal(e)
	}
	invoke(1, "profile", "import", "--config", newCfg, "--from", file, "replacement")
	args := []string{"profile", "import", "--config", newCfg, "--from", file, "--repo", filepath.Join(root, "repo"), "--source", filepath.Join(root, "new-source"), "replacement"}
	invoke(0, args...)
	invoke(1, args...)
	var original struct {
		Profile config.Profile `json:"profile"`
	}
	if e := json.Unmarshal([]byte(exported), &original); e != nil {
		t.Fatal(e)
	}
	settings, e := (config.Store{Path: newCfg}).Read()
	if e != nil {
		t.Fatal(e)
	}
	p, e := settings.Select("replacement")
	if e != nil {
		t.Fatal(e)
	}
	if p.ID == original.Profile.ID || !p.Disabled || p.Sources[0] == original.Profile.Sources[0] {
		t.Fatal("import reused identity, enabled scheduling, or failed to remap sources")
	}
}

func TestNestedSchedulingPreview(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "profiles.json")
	var out, stderr bytes.Buffer
	a := App{Out: &out, Err: &stderr}
	if code := a.Run(context.Background(), []string{"profile", "add", "--config", cfg, "--repo", filepath.Join(root, "repo"), "--source", filepath.Join(root, "src"), "app"}); code != 0 {
		t.Fatal(stderr.String())
	}
	out.Reset()
	stderr.Reset()
	if code := a.Run(context.Background(), []string{"schedule", "preview", "--config", cfg, "--profile", "app", "--password-file", filepath.Join(root, "password"), "--json"}); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var defs map[string]string
	if e := json.Unmarshal(out.Bytes(), &defs); e != nil || len(defs) == 0 {
		t.Fatal("nested scheduler syntax not parsed", e)
	}
}
