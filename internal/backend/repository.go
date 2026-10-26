// Package backend integrates the encrypted restic repository format.
package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lumesky-me/snapArk/internal/journal"
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
	History    *journal.Store
	ProfileID  string
	AppVersion string

	Path         string
	PasswordFile string
	// PasswordInput is supplied only by an interactive terminal prompt. It travels
	// through a child stdin pipe, never arguments, environment or a disk file.
	PasswordInput []byte `json:"-"`
	Binary        string
	Out           io.Writer
	Err           io.Writer
	JSON          bool
	Quiet         bool
	ReadOnly      bool
	SnapshotTag   string
	SnapshotHost  string
}

// Environment removes inherited backend configuration and credential bindings.
// Wrapped commands must not receive repository passwords or API tokens by default.
func Environment() []string {
	var env []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		u := strings.ToUpper(k)
		if strings.HasPrefix(u, "RESTIC_") || strings.HasPrefix(u, "VEYRA_") || strings.HasPrefix(u, "SNAPARK_") || strings.HasSuffix(u, "_TOKEN") || strings.HasSuffix(u, "_PASSWORD") || strings.HasSuffix(u, "_SECRET") || strings.HasSuffix(u, "_API_KEY") {
			continue
		}
		env = append(env, e)
	}
	return env
}

func (r Repository) Validate(ctx context.Context) error {
	if len(r.PasswordInput) > 0 && (r.PasswordFile != "" || bytes.ContainsAny(r.PasswordInput, "\r\n\x00")) {
		return errors.New("invalid in-memory password source")
	}
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
	_, _, err := Backend(ctx, r.Binary)
	return err
}

// Backend reports the selected executable and its observed pinned version.
func Backend(ctx context.Context, binary string) (string, string, error) {
	if binary == "" {
		binary = resticBinary()
	}
	located, e := exec.LookPath(binary)
	if e != nil {
		return "", "", fmt.Errorf("restic %s is required; run snapark setup: %w", Version, e)
	}
	binary, e = filepath.Abs(located)
	if e != nil {
		return "", "", e
	}
	cmd := exec.CommandContext(ctx, binary, "version")
	cmd.Env = Environment()
	data, err := cmd.Output()
	if err != nil {
		return binary, "", fmt.Errorf("restic is required: %w", err)
	}
	if !strings.HasPrefix(string(data), "restic "+Version+" ") {
		return binary, strings.TrimSpace(string(data)), fmt.Errorf("restic %s is required; found %s", Version, strings.TrimSpace(string(data)))
	}
	return binary, strings.TrimSpace(string(data)), nil
}

func (r Repository) command(ctx context.Context, args ...string) *exec.Cmd {
	binary := r.Binary
	if binary == "" {
		binary = resticBinary()
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
	// A supplied password file is the only credential source. Restic can fall
	// back to a terminal prompt when its trimmed contents are empty; unattended
	// execution must fail instead of waiting for someone to type a password.
	if r.PasswordFile != "" {
		c.Stdin = nil
	}
	if len(r.PasswordInput) > 0 {
		c.Stdin = io.MultiReader(bytes.NewReader(r.PasswordInput), strings.NewReader("\n"))
	}
	c.Stdout = r.Out
	c.Stderr = r.Err
	return c
}

func (r Repository) Execute(ctx context.Context, args ...string) error {
	err := r.command(ctx, args...).Run()
	if err != nil {
		return operationError(ctx, args[0], err)
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
		return operationError(ctx, args[0], runErr)
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
	// Exact IDs can be authenticated directly. Listing every historical snapshot
	// for each immutable lookup caused repeated unbounded metadata scans.
	if snapshotID.MatchString(ref) {
		var s Snapshot
		if e := r.Decode(ctx, &s, "cat", "snapshot", ref); e != nil {
			return s, e
		}
		s.ID = ref
		if r.SnapshotHost != "" && s.Hostname != r.SnapshotHost {
			return Snapshot{}, errors.New("snapshot is outside the selected host scope")
		}
		if r.SnapshotTag != "" {
			found := false
			for _, tag := range s.Tags {
				if tag == r.SnapshotTag {
					found = true
					break
				}
			}
			if !found {
				return Snapshot{}, errors.New("snapshot is outside the selected profile scope")
			}
		}
		if _, e := time.Parse(time.RFC3339Nano, s.Time); e != nil {
			return Snapshot{}, fmt.Errorf("invalid snapshot timestamp: %w", e)
		}
		return s, nil
	}
	s, err := r.Snapshots(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	var matches []Snapshot
	for _, v := range s {
		if !snapshotID.MatchString(v.ID) {
			return Snapshot{}, errors.New("invalid snapshot ID in repository metadata")
		}
		if _, e := time.Parse(time.RFC3339Nano, v.Time); e != nil {
			return Snapshot{}, fmt.Errorf("invalid snapshot timestamp: %w", e)
		}
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
	if e := checkPathComponents(abs); e != nil {
		return "", e
	}
	var tail []string
	for {
		resolved, e := filepath.EvalSymlinks(abs)
		if e == nil {
			resolved, e = canonicalExisting(resolved)
			if e != nil {
				return "", e
			}
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

func operationError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("restic %s interrupted; inspect snapshot history or the restore status before retrying: %w", operation, ctx.Err())
	}
	detail := "operation failed; inspect backend diagnostics (data may be incomplete)"
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		switch exit.ExitCode() {
		case 3:
			detail = "not all source data could be read; any saved snapshot may be incomplete"
		case 10:
			detail = "repository unavailable; check the destination, mounted drive, and initialization"
		case 11:
			detail = "repository locked; wait for other operations and inspect lock ownership"
		case 12:
			detail = "incorrect password; check the protected password file or interactive password"
		}
	}
	return fmt.Errorf("restic %s: %s: %w", operation, detail, err)
}

func resticBinary() string {
	exe, e := os.Executable()
	if e == nil {
		candidate := filepath.Join(filepath.Dir(exe), "restic")
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if info, e := os.Stat(candidate); e == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return "restic"
}
