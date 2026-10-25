package project

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/lumesky-me/veyra/internal/backend"
)

type RunOptions struct {
	Project string
	Command []string
	Test    []string
	Timeout time.Duration
	Post    bool
	Exclude []string
	Include []string
	Tags    []string
	FullGit bool
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
}

type RunResult struct {
	Before       string   `json:"before"`
	After        string   `json:"after,omitempty"`
	ExitCode     int      `json:"exit_code"`
	TestExitCode *int     `json:"test_exit_code,omitempty"`
	Changes      []Change `json:"changes"`
	Error        string   `json:"error,omitempty"`
}

func execute(ctx context.Context, dir string, args []string, o RunOptions) int {
	if len(args) == 0 {
		return 0
	}
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = dir
	c.Env = backend.Environment()
	c.Stdin = o.Stdin
	c.Stdout = o.Stdout
	c.Stderr = o.Stderr
	configureProcess(c)
	err := c.Run()
	if ctx.Err() != nil {
		return 124
	}
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitCode()
		if code < 0 {
			return 128
		}
		return code
	}
	fmt.Fprintln(o.Stderr, err)
	return 127
}

func Protect(ctx context.Context, r backend.Repository, o RunOptions) (RunResult, error) {
	result := RunResult{ExitCode: 1, Changes: []Change{}}
	if len(o.Command) == 0 {
		return result, errors.New("provide a command after --")
	}
	dir, err := backend.Canonical(o.Project)
	if err != nil {
		return result, err
	}
	f, err := os.Stat(dir)
	if err != nil {
		return result, err
	}
	if !f.IsDir() {
		return result, errors.New("project must be a directory")
	}
	if o.Timeout <= 0 {
		return result, errors.New("timeout must be positive")
	}
	before, err := Scan(ctx, dir)
	if err != nil {
		return result, err
	}
	backup := backend.BackupOptions{Sources: []string{dir}, Label: "veyra:pre-operation", Exclude: o.Exclude, Include: o.Include, Tags: o.Tags, FullGit: o.FullGit}
	id, err := r.CheckpointID(ctx, backup)
	if err != nil {
		return result, fmt.Errorf("command not started: checkpoint failed: %w", err)
	}
	result.Before = id
	// Full repository content verification is deliberately fail-closed before execution.
	if err = r.Check(ctx, true); err != nil {
		return result, fmt.Errorf("command not started: verification failed: %w", err)
	}
	commandCtx, cancel := context.WithTimeout(ctx, o.Timeout)
	result.ExitCode = execute(commandCtx, dir, o.Command, o)
	cancel()
	after, scanErr := Scan(ctx, dir)
	if scanErr != nil {
		result.Error = scanErr.Error()
	} else {
		result.Changes = Compare(before, after)
	}
	if len(o.Test) > 0 && ctx.Err() == nil {
		testCtx, stop := context.WithTimeout(ctx, o.Timeout)
		code := execute(testCtx, dir, o.Test, o)
		stop()
		result.TestExitCode = &code
	}
	if o.Post && ctx.Err() == nil {
		backup.Label = "veyra:post-operation"
		result.After, err = r.CheckpointID(ctx, backup)
		if err != nil {
			result.Error = fmt.Sprintf("%s post-operation checkpoint: %v", result.Error, err)
		}
	}
	if result.Error != "" {
		return result, errors.New(result.Error)
	}
	return result, nil
}
