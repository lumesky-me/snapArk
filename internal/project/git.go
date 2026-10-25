// Package project provides read-only project inspection and command protection.
package project

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/lumesky-me/veyra/internal/backend"
)

type GitEntry struct {
	Status   string `json:"status"`
	Path     string `json:"path"`
	Original string `json:"original,omitempty"`
}

type GitStatus struct {
	Root    string     `json:"root"`
	Entries []GitEntry `json:"entries"`
	Tracked []string   `json:"tracked"`
}

func Git(ctx context.Context, dir string) (GitStatus, error) {
	run := func(args ...string) ([]byte, error) {
		c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		c.Env = append(backend.Environment(), "GIT_OPTIONAL_LOCKS=0")
		return c.Output()
	}
	root, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return GitStatus{}, fmt.Errorf("not an accessible Git working tree: %w", err)
	}
	data, err := run("status", "--porcelain=v1", "-z", "--ignored", "--untracked-files=all")
	if err != nil {
		return GitStatus{}, err
	}
	s := GitStatus{Root: strings.TrimSpace(string(root)), Entries: []GitEntry{}}
	parts := strings.Split(string(data), "\x00")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if p == "" {
			continue
		}
		if len(p) < 4 {
			return s, fmt.Errorf("invalid Git status record")
		}
		e := GitEntry{Status: p[:2], Path: p[3:]}
		if strings.ContainsAny(e.Status, "RC") {
			i++
			if i >= len(parts) {
				return s, fmt.Errorf("incomplete rename record")
			}
			e.Original = parts[i]
		}
		s.Entries = append(s.Entries, e)
	}
	tracked, err := run("ls-files", "--cached", "-z")
	if err != nil {
		return s, err
	}
	s.Tracked = []string{}
	for _, p := range strings.Split(string(tracked), "\x00") {
		if p != "" {
			s.Tracked = append(s.Tracked, p)
		}
	}
	return s, nil
}
