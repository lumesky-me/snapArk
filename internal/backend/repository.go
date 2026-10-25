// Package backend integrates the encrypted restic repository format.
package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const Version = "0.18.1"

type Repository struct {
	Path         string
	PasswordFile string
	Binary       string
	Out          io.Writer
	Err          io.Writer
	JSON         bool
	Quiet        bool
	ReadOnly     bool
	SnapshotTag  string
	SnapshotHost string
}

// Environment removes inherited backend configuration and credential bindings.
// Wrapped commands must not receive repository passwords or API tokens by default.
func Environment() []string {
	var env []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		u := strings.ToUpper(k)
		if strings.HasPrefix(u, "RESTIC_") || strings.HasPrefix(u, "VEYRA_") || strings.HasSuffix(u, "_TOKEN") || strings.HasSuffix(u, "_PASSWORD") || strings.HasSuffix(u, "_SECRET") || strings.HasSuffix(u, "_API_KEY") {
			continue
		}
		env = append(env, e)
	}
	return env
}

func (r Repository) Validate(ctx context.Context) error {
	if r.Path == "" {
		return errors.New("specify --repo with a local repository directory")
	}
	if strings.Contains(r.Path, "://") || strings.HasPrefix(r.Path, "sftp:") {
		return errors.New("only local and mounted external-drive repositories are supported")
	}
	if r.PasswordFile != "" {
		f, err := os.Lstat(r.PasswordFile)
		if err != nil {
			return fmt.Errorf("password file: %w", err)
		}
		if !f.Mode().IsRegular() {
			return errors.New("password file must be a regular file, not a symbolic link")
		}
		if runtime.GOOS != "windows" && f.Mode().Perm()&0077 != 0 {
			return errors.New("password file must be private (chmod 600)")
		}
		if f.Size() == 0 {
			return errors.New("password file is empty")
		}
	}
	binary := r.Binary
	if binary == "" {
		binary = "restic"
	}
	cmd := exec.CommandContext(ctx, binary, "version")
	cmd.Env = Environment()
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("restic is required: %w", err)
	}
	if !strings.HasPrefix(string(data), "restic "+Version+" ") {
		return fmt.Errorf("restic %s is required; found %s", Version, strings.TrimSpace(string(data)))
	}
	return nil
}

func (r Repository) command(ctx context.Context, args ...string) *exec.Cmd {
	binary := r.Binary
	if binary == "" {
		binary = "restic"
	}
	repoPath, _ := filepath.Abs(r.Path)
	base := []string{"--repo", repoPath, "--no-cache"}
	if r.ReadOnly {
		base = append(base, "--no-lock")
	}
	if r.PasswordFile != "" {
		base = append(base, "--password-file", r.PasswordFile)
	}
	if r.JSON {
		base = append(base, "--json")
	}
	if r.Quiet {
		base = append(base, "--quiet")
	}
	c := exec.CommandContext(ctx, binary, append(base, args...)...)
	c.Env = Environment()
	c.Stdin = os.Stdin
	c.Stdout = r.Out
	c.Stderr = r.Err
	return c
}

func (r Repository) Execute(ctx context.Context, args ...string) error {
	err := r.command(ctx, args...).Run()
	if err != nil {
		return fmt.Errorf("restic %s failed (operation may be incomplete; inspect output): %w", args[0], err)
	}
	return nil
}

func (r Repository) Decode(ctx context.Context, v any, args ...string) error {
	r.JSON = true
	r.Quiet = false
	c := r.command(ctx, args...)
	c.Stdout = nil
	p, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	if err = c.Start(); err != nil {
		return err
	}
	decodeErr := json.NewDecoder(p).Decode(v)
	// Drain before waiting so large responses cannot block the process.
	_, _ = io.Copy(io.Discard, p)
	runErr := c.Wait()
	if runErr != nil {
		return fmt.Errorf("restic %s: %w", args[0], runErr)
	}
	return decodeErr
}

type Snapshot struct {
	ID       string   `json:"id"`
	Hostname string   `json:"hostname"`
	Paths    []string `json:"paths"`
	Tags     []string `json:"tags"`
	Time     string   `json:"time"`
}

func (r Repository) Snapshots(ctx context.Context) ([]Snapshot, error) {
	var s []Snapshot
	args := []string{"snapshots"}
	if r.SnapshotTag != "" {
		args = append(args, "--tag", r.SnapshotTag)
	}
	if r.SnapshotHost != "" {
		args = append(args, "--host", r.SnapshotHost)
	}
	err := r.Decode(ctx, &s, args...)
	return s, err
}

var snapshotID = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Resolve binds mutable aliases to one immutable snapshot ID before verification.
func (r Repository) Resolve(ctx context.Context, ref string) (Snapshot, error) {
	s, err := r.Snapshots(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	var matches []Snapshot
	for _, v := range s {
		if ref == "latest" || (len(ref) >= 8 && strings.HasPrefix(v.ID, ref)) {
			matches = append(matches, v)
		}
	}
	if ref == "latest" && len(matches) > 0 {
		best := matches[0]
		for _, v := range matches[1:] {
			vTime, e := time.Parse(time.RFC3339Nano, v.Time)
			if e != nil {
				return Snapshot{}, fmt.Errorf("invalid snapshot timestamp: %w", e)
			}
			bestTime, e := time.Parse(time.RFC3339Nano, best.Time)
			if e != nil {
				return Snapshot{}, fmt.Errorf("invalid snapshot timestamp: %w", e)
			}
			if vTime.After(bestTime) {
				best = v
			}
		}
		return best, nil
	}
	if len(matches) != 1 || !snapshotID.MatchString(matches[0].ID) {
		return Snapshot{}, fmt.Errorf("snapshot %q is missing or ambiguous (use at least 8 ID characters)", ref)
	}
	return matches[0], nil
}

// Canonical resolves symlink ancestors even when the leaf does not yet exist.
func Canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var tail []string
	for {
		resolved, e := filepath.EvalSymlinks(abs)
		if e == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", e
		}
		tail = append(tail, filepath.Base(abs))
		abs = parent
	}
}

func Within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
