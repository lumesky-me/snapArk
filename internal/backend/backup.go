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
	Include     []string
	Tags        []string
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
	sources := make([]string, 0, len(o.Sources))
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
		sources = append(sources, source)
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
		if e := checkSourceTree(ctx, source); e != nil {
			return e
		}
	}
	for _, tag := range o.Tags {
		args = append(args, "--tag", tag)
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
		args = append(args, "--dry-run", "--no-lock")
	}
	if len(o.Include) > 0 {
		list, e := os.CreateTemp("", "veyra-includes-*")
		if e != nil {
			return e
		}
		defer os.Remove(list.Name())
		listPath, e := Canonical(list.Name())
		if e != nil {
			_ = list.Close()
			return e
		}
		for _, source := range sources {
			base, e := Canonical(source)
			if e != nil {
				_ = list.Close()
				return e
			}
			if Within(base, listPath) {
				_ = list.Close()
				return errors.New("include-list temporary directory is inside a source; set the OS temporary directory outside sources before retrying")
			}
		}
		count := 0
		for _, src := range sources {
			for _, rel := range o.Include {
				if !filepath.IsLocal(rel) {
					_ = list.Close()
					return errors.New("include paths must stay within sources")
				}
				selected := filepath.Join(src, rel)
				resolved, e := Canonical(selected)
				if e != nil {
					_ = list.Close()
					return e
				}
				base, e := Canonical(src)
				if e != nil {
					_ = list.Close()
					return e
				}
				if !Within(base, resolved) {
					_ = list.Close()
					return errors.New("include resolves outside its source")
				}
				if _, e = os.Lstat(selected); errors.Is(e, os.ErrNotExist) {
					continue
				} else if e != nil {
					_ = list.Close()
					return e
				}
				abs, e := filepath.Abs(selected)
				if e != nil {
					_ = list.Close()
					return e
				}
				if _, e = list.WriteString(abs + "\x00"); e != nil {
					_ = list.Close()
					return e
				}
				count++
			}
		}
		if e = list.Close(); e != nil {
			return e
		}
		if count == 0 {
			return errors.New("no include paths exist in the sources")
		}
		return r.Execute(ctx, append(args, "--files-from-raw", list.Name())...)
	}
	args = append(args, "--")
	for _, p := range sources {
		abs, e := filepath.Abs(p)
		if e != nil {
			return e
		}
		args = append(args, abs)
	}
	return r.Execute(ctx, args...)
}

func (r Repository) Check(ctx context.Context, full bool) error {
	if r.History != nil {
		return r.RecordedCheck(ctx, full)
	}
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
		args = append(args, "--dry-run", "--no-lock")
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
