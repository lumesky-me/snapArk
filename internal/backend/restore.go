package backend

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type Node struct {
	Type       string `json:"type"`
	Size       uint64 `json:"size,omitempty"`
	Path       string `json:"path"`
	LinkTarget string `json:"linktarget"`
}

// SafeNode treats snapshot paths as portable paths, independent of host OS.
func SafeNode(n Node) error {
	p := n.Path
	if !utf8.ValidString(p) || !utf8.ValidString(n.LinkTarget) || !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\:\x00") || path.Clean(p) != p {
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
	names := strings.Split(p, "/")
	if n.Type == "symlink" {
		for _, part := range strings.Split(n.LinkTarget, "/") {
			if part != "." && part != ".." {
				names = append(names, part)
			}
		}
	}
	for _, part := range names {
		for _, r := range part {
			if r < 32 || strings.ContainsRune(`<>"|?*`, r) {
				return fmt.Errorf("nonportable restore path %q", p)
			}
		}
		upper := strings.ToUpper(strings.TrimRight(strings.SplitN(part, ".", 2)[0], " "))
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" || upper == "CONIN$" || upper == "CONOUT$" || reservedPort(upper) || (len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9') {
			return fmt.Errorf("nonportable restore path %q", p)
		}
	}
	return nil
}

// Preflight reads an authenticated archive stream because restic ls JSON omits
// symbolic-link targets. Nothing is extracted or stored in plaintext on disk.
func (r Repository) validateTree(ctx context.Context, id string) error {
	r.JSON = false
	r.Quiet = true
	c := r.command(ctx, "dump", id, "/", "--archive", "tar")
	c.Stdout = nil
	pipe, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	if err = c.Start(); err != nil {
		return err
	}
	reader := tar.NewReader(pipe)
	var firstErr error
	seen := make(map[string]string)
	links := make(map[string]string)
	for {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			firstErr = e
			break
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == "." || name == "" {
			continue
		}
		if strings.HasPrefix(name, "/") {
			firstErr = errors.New("absolute archive member")
			break
		}
		n := Node{Path: "/" + name, LinkTarget: h.Linkname}
		switch h.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			n.Type = "file"
		case tar.TypeDir:
			n.Type = "dir"
		case tar.TypeSymlink:
			n.Type = "symlink"
			links[pathKey(n.Path)] = n.LinkTarget
		case tar.TypeLink:
			n.Type = "file"
			if h.Linkname == "" || h.Linkname == ".." || strings.HasPrefix(h.Linkname, "/") || strings.ContainsAny(h.Linkname, "\\:\x00") || path.Clean(h.Linkname) != h.Linkname || strings.HasPrefix(h.Linkname, "../") {
				firstErr = errors.New("unsafe hard-link target")
			}
		default:
			firstErr = fmt.Errorf("unsupported archive member type %d", h.Typeflag)
		}
		if firstErr != nil {
			break
		}
		if e = SafeNode(n); e != nil {
			firstErr = e
			break
		}
		folded := pathKey(n.Path)
		if prev, ok := seen[folded]; ok {
			firstErr = fmt.Errorf("duplicate or case-colliding paths: %q and %q", prev, n.Path)
			break
		}
		seen[folded] = n.Path
	}
	if firstErr == nil {
		for _, p := range seen {
			for parent := path.Dir(p); parent != "/" && parent != "."; parent = path.Dir(parent) {
				if _, ok := links[pathKey(parent)]; ok {
					firstErr = fmt.Errorf("archive entry below symbolic link: %s", p)
					break
				}
			}
			if firstErr != nil {
				break
			}
		}
	}
	if firstErr == nil {
		firstErr = validateLinks(links)
	}
	if firstErr != nil {
		_ = c.Process.Kill()
	}
	// Consume any remaining output before Wait, including tar trailer padding.
	_, _ = io.Copy(io.Discard, pipe)
	runErr := c.Wait()
	if firstErr != nil {
		return firstErr
	}
	return runErr
}

type RestoreOptions struct {
	Snapshot string
	Root     string
	Target   string
	Include  []string
	DryRun   bool
}

func (r Repository) Restore(ctx context.Context, o RestoreOptions) error {
	if o.Target == "" {
		return errors.New("restore requires --target pointing to a new directory")
	}
	if o.DryRun {
		r.ReadOnly = true
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
	ref := s.ID
	root := o.Root
	if root == "" {
		root, err = r.DefaultRoot(ctx, s)
		if err != nil {
			return err
		}
	}

	if root != "" {
		if !strings.HasPrefix(root, "/") || path.Clean(root) != root || strings.ContainsAny(root, "\\:\x00") {
			return errors.New("invalid snapshot root")
		}
		ref += ":" + root
	}
	if err = r.validateTree(ctx, ref); err != nil {
		return fmt.Errorf("restore preflight: %w", err)
	}
	args := []string{"restore", ref, "--target", target}
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
	args = append(args, "--verify")
	// Reserve an empty private root. Do not remove failed restores: data can be recovered.
	marker := target + ".veyra-incomplete.json"
	status, e := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return fmt.Errorf("reserve restore status file (inspect any previous attempt): %w", e)
	}
	identity, identityErr := r.Identity(ctx)
	if identityErr != nil {
		_ = status.Close()
		_ = os.Remove(marker)
		return identityErr
	}
	e = json.NewEncoder(status).Encode(RestoreAttempt{Version: 1, Started: time.Now().UTC(), State: "incomplete", Repository: identity, Snapshot: s.ID, Target: target, Root: root, Include: o.Include})
	if e == nil {
		e = status.Sync()
	}
	closeErr := status.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		_ = os.Remove(marker)
		return e
	}
	if err = os.Mkdir(target, 0700); err != nil {
		_ = os.Remove(marker)
		return fmt.Errorf("create target (parent must exist): %w", err)
	}
	if err = r.Execute(ctx, args...); err != nil {
		return fmt.Errorf("partial restore retained at %s: %w", target, err)
	}
	err = filepath.WalkDir(target, func(p string, d os.DirEntry, e error) error {
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
	if err != nil {
		return fmt.Errorf("restore validation failed; incomplete marker retained at %s: %w", marker, err)
	}
	if err = os.Remove(marker); err != nil {
		return fmt.Errorf("data verified but status marker could not be removed: %w", err)
	}
	return nil
}

// Snapshot source paths belong to the backup host, not necessarily this host.
func snapshotRoot(source string) string {
	if len(source) >= 3 && source[1] == ':' && (source[2] == '\\' || source[2] == '/') {
		return "/" + source[:1] + strings.ReplaceAll(source[2:], "\\", "/")
	}

	return source
}

func reservedPort(name string) bool {
	if !strings.HasPrefix(name, "COM") && !strings.HasPrefix(name, "LPT") {
		return false
	}
	suffix := name[3:]
	return suffix == "¹" || suffix == "²" || suffix == "³"
}

// DefaultRoot preserves a single source's contents without extracting synthetic
// ancestor directories. Multiple sources remain rooted at the snapshot root.
func (r Repository) DefaultRoot(ctx context.Context, s Snapshot) (string, error) {
	if len(s.Paths) != 1 {
		return "/", nil
	}
	root := snapshotRoot(s.Paths[0])
	if root == "/" {
		return root, nil
	}
	if !strings.HasPrefix(root, "/") || strings.ContainsAny(root, "\\:\x00") {
		return "", errors.New("snapshot source root needs an explicit portable --root; inspect files first")
	}
	after := ""
	for {
		nodes, more, e := r.Browse(ctx, s.ID, path.Dir(root), after, path.Base(root), 100)
		if e != nil {
			return "", e
		}
		for _, n := range nodes {
			if n.Path == root {
				if n.Type != "dir" {
					return path.Dir(root), nil
				}
				return root, nil
			}
		}
		if !more || len(nodes) == 0 {
			return "", errors.New("snapshot source root was not found; choose an explicit --root")
		}
		after = nodes[len(nodes)-1].Path
	}
}
