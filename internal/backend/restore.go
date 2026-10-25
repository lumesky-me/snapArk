package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type Node struct {
	Type       string `json:"type"`
	Path       string `json:"path"`
	LinkTarget string `json:"linktarget"`
}

// SafeNode treats snapshot paths as portable paths, independent of host OS.
func SafeNode(n Node) error {
	p := n.Path
	if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\:\x00") || path.Clean(p) != p {
		return fmt.Errorf("unsafe snapshot path %q", p)
	}
	if n.Type != "file" && n.Type != "dir" && n.Type != "symlink" {
		return fmt.Errorf("unsupported restore node %q: %s", p, n.Type)
	}
	if n.Type == "symlink" {
		target := n.LinkTarget
		if target == "" || strings.HasPrefix(target, "/") || strings.ContainsAny(target, "\\:\x00") {
			return fmt.Errorf("unsafe symbolic link at %q", p)
		}
		// Normalize relative to the snapshot root, never the staging directory.
		resolved := path.Join(path.Dir(strings.TrimPrefix(p, "/")), target)
		if resolved == ".." || strings.HasPrefix(resolved, "../") {
			return fmt.Errorf("symbolic link escapes restore root: %q", p)
		}
	}
	// Windows also treats these names as devices or alternate data streams.
	for _, part := range strings.Split(p, "/") {
		upper := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" || (len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9') {
			return fmt.Errorf("nonportable restore path %q", p)
		}
	}
	return nil
}

func (r Repository) validateTree(ctx context.Context, id string) error {
	r.JSON = true
	r.Quiet = false
	c := r.command(ctx, "ls", id)
	pipe, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	if err = c.Start(); err != nil {
		return err
	}
	d := json.NewDecoder(pipe)
	var firstErr error
	for {
		var n Node
		err = d.Decode(&n)
		if err == io.EOF {
			break
		}
		if err != nil {
			firstErr = err
			break
		}
		if n.Path == "" {
			continue
		} // snapshot metadata record
		if err = SafeNode(n); err != nil {
			firstErr = err
			break
		}
	}
	if firstErr != nil {
		_ = c.Process.Kill()
		_, _ = io.Copy(io.Discard, pipe)
	}
	runErr := c.Wait()
	if firstErr != nil {
		return firstErr
	}
	return runErr
}

type RestoreOptions struct {
	Snapshot string
	Target   string
	Include  []string
	DryRun   bool
}

func (r Repository) Restore(ctx context.Context, o RestoreOptions) error {
	if o.Target == "" {
		return errors.New("restore requires --target pointing to a new directory")
	}
	s, err := r.Resolve(ctx, o.Snapshot)
	if err != nil {
		return err
	}
	target, err := Canonical(o.Target)
	if err != nil {
		return err
	}
	repo, err := Canonical(r.Path)
	if err != nil {
		return err
	}
	if Within(repo, target) || Within(target, repo) {
		return errors.New("restore target must not overlap the backup repository")
	}
	if _, e := os.Lstat(o.Target); !errors.Is(e, os.ErrNotExist) {
		return errors.New("restore target must not exist; existing files are never overwritten")
	}
	if err = r.validateTree(ctx, s.ID); err != nil {
		return fmt.Errorf("restore preflight: %w", err)
	}
	args := []string{"restore", s.ID, "--target", target, "--verify"}
	for _, p := range o.Include {
		if !validSelector(p) {
			return errors.New("invalid include pattern")
		}
		args = append(args, "--include", p)
	}
	if o.DryRun {
		args = append(args, "--dry-run")
		return r.Execute(ctx, args...)
	}
	// Reserve an empty private root. Do not remove failed restores: data can be recovered.
	if err = os.Mkdir(target, 0700); err != nil {
		return fmt.Errorf("create target (parent must exist): %w", err)
	}
	if err = r.Execute(ctx, args...); err != nil {
		return fmt.Errorf("partial restore retained at %s: %w", target, err)
	}
	return filepath.WalkDir(target, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		link, e := os.Readlink(p)
		if e != nil {
			return e
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(p), link))
		if filepath.IsAbs(link) || !Within(target, resolved) {
			return fmt.Errorf("unsafe restored link at %s", p)
		}
		return nil
	})
}
