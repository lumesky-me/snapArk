package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type BackupOptions struct {
	Sources     []string
	Exclude     []string
	ExcludeFile string
	Label       string
	Checkpoint  bool
	FullGit     bool
	DryRun      bool
}

func (r Repository) Backup(ctx context.Context, o BackupOptions) error {
	if len(o.Sources) == 0 {
		return errors.New("provide at least one source directory")
	}
	repo, err := Canonical(r.Path)
	if err != nil {
		return err
	}
	args := []string{"backup"}
	for _, p := range o.Sources {
		source, e := Canonical(p)
		if e != nil {
			return e
		}
		info, e := os.Stat(source)
		if e != nil {
			return e
		}
		if !info.IsDir() {
			return fmt.Errorf("source must be a directory: %s", p)
		}
		if Within(source, repo) || Within(repo, source) {
			return errors.New("source and repository directories must not overlap, including through symbolic links")
		}
		if r.PasswordFile != "" {
			password, e := Canonical(r.PasswordFile)
			if e != nil {
				return e
			}
			if Within(source, password) {
				return errors.New("keep the password file outside every backup source")
			}
		}
	}
	if o.Label != "" {
		args = append(args, "--tag", o.Label)
	}
	if o.Checkpoint {
		args = append(args, "--tag", "veyra:checkpoint")
		if !o.FullGit {
			args = append(args, "--exclude", ".git")
		}
	}
	for _, p := range o.Exclude {
		args = append(args, "--exclude", p)
	}
	if o.ExcludeFile != "" {
		args = append(args, "--exclude-file", o.ExcludeFile)
	}
	if o.DryRun {
		args = append(args, "--dry-run")
	}
	args = append(args, "--")
	for _, p := range o.Sources {
		abs, e := filepath.Abs(p)
		if e != nil {
			return e
		}
		args = append(args, abs)
	}
	return r.Execute(ctx, args...)
}

func (r Repository) Check(ctx context.Context, full bool) error {
	args := []string{"check"}
	if full {
		args = append(args, "--read-data")
	}
	return r.Execute(ctx, args...)
}

func (r Repository) Forget(ctx context.Context, refs []string, confirm bool) error {
	if len(refs) == 0 {
		return errors.New("provide explicit snapshot IDs to forget")
	}
	args := []string{"forget"}
	if !confirm {
		args = append(args, "--dry-run")
	}
	for _, ref := range refs {
		if ref == "latest" {
			return errors.New("deletion requires explicit snapshot IDs, not latest")
		}
		s, err := r.Resolve(ctx, ref)
		if err != nil {
			return err
		}
		args = append(args, s.ID)
	}
	return r.Execute(ctx, args...)
}

func validSelector(s string) bool {
	return s != "" && !strings.HasPrefix(s, "-") && !strings.ContainsAny(s, "\x00\r\n")
}
