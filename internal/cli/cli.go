// Package cli implements VEYRA's command-line interface.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lumesky-me/veyra/internal/backend"
	"github.com/lumesky-me/veyra/internal/project"
)

const Version = "0.1.0-dev"

const help = `VEYRA — encrypted backups and developer recovery (experimental)

Usage: veyra COMMAND [OPTIONS] [ARGUMENTS]

Commands:
  init          Initialize an encrypted local repository
  backup        Back up one or more directories
  snapshots     List snapshots
  checkpoint    Save a project without changing Git history
  checkpoints   List developer checkpoints
  files         List snapshot contents
  diff          Compare two snapshots
  restore       Restore into a new directory and verify file contents
  verify        Check repository metadata (--full reads all stored data)
  stats         Inspect repository storage usage
  forget        Preview snapshot deletion (--confirm applies it)
  prune         Preview unreferenced-data removal (--confirm applies it)
  git           Read-only Git status, including ignored files
  run           Verify a checkpoint before running an explicit command
  version       Show version and required backend
  help          Show help

Repository commands accept --repo PATH, --password-file PATH, --json, --quiet.
Flags precede positional arguments. Without a password file, restic securely
prompts on the terminal. Passwords are never accepted as command arguments.
Use COMMAND --help for command options.
`

type stringsFlag []string

func (s *stringsFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }

type App struct {
	Out, Err io.Writer
	In       io.Reader
}

func (a App) Run(ctx context.Context, args []string) int {
	if a.Out == nil {
		a.Out = os.Stdout
	}
	if a.Err == nil {
		a.Err = os.Stderr
	}
	if a.In == nil {
		a.In = os.Stdin
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprint(a.Out, help)
		return 0
	}
	if args[0] == "version" {
		fmt.Fprintf(a.Out, "veyra %s (requires restic %s)\n", Version, backend.Version)
		return 0
	}
	cmd := args[0]
	switch cmd {
	case "init", "backup", "snapshots", "checkpoint", "checkpoints", "files", "diff", "restore", "verify", "stats", "forget", "prune", "git", "run":
	default:
		fmt.Fprintf(a.Err, "unknown command %q; use veyra help\n", cmd)
		return 2
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	var r backend.Repository
	r.Out = a.Out
	r.Err = a.Err
	fs.StringVar(&r.Path, "repo", "", "local or mounted-drive repository directory")
	fs.StringVar(&r.PasswordFile, "password-file", "", "private password file outside backup sources")
	fs.BoolVar(&r.JSON, "json", false, "structured JSON output")
	fs.BoolVar(&r.Quiet, "quiet", false, "suppress normal output")
	var exclude, include, test stringsFlag
	var label, target, dir, restoreRoot string
	var full, dry, confirm, fullGit, post bool
	var timeout time.Duration
	switch cmd {
	case "backup", "checkpoint":
		fs.Var(&exclude, "exclude", "exclude pattern (repeatable; ignored files are included by default)")
		fs.StringVar(&label, "label", "", "snapshot label")
		fs.BoolVar(&dry, "dry-run", false, "scan without storing a snapshot")
		if cmd == "checkpoint" {
			fs.BoolVar(&fullGit, "full-git", false, "include Git internals")
		}
	case "restore":
		fs.StringVar(&restoreRoot, "root", "", "snapshot subfolder (single-source snapshots default to their source root)")
		fs.StringVar(&target, "target", "", "new restore directory (parent must exist)")
		fs.Var(&include, "include", "snapshot path pattern to recover (repeatable)")
		fs.BoolVar(&dry, "dry-run", false, "preview restoration without writing files")
	case "verify":
		fs.BoolVar(&full, "full", false, "authenticate and read all stored data")
	case "forget", "prune":
		fs.BoolVar(&confirm, "confirm", false, "apply destructive operation (default previews only)")
	case "git":
		fs.StringVar(&dir, "project", ".", "project directory")
	case "run":
		fs.StringVar(&dir, "project", ".", "project directory")
		fs.DurationVar(&timeout, "timeout", 30*time.Minute, "timeout for command and separately for tests")
		fs.BoolVar(&post, "post", false, "also save a post-operation checkpoint")
		fs.BoolVar(&fullGit, "full-git", false, "include Git internals in checkpoints")
		fs.Var(&exclude, "exclude", "checkpoint exclusion pattern (repeatable)")
		fs.Var(&test, "test-arg", "explicit test argv token, starting with executable (repeatable)")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	positional := fs.Args()
	if r.Quiet {
		r.Out = io.Discard
	}
	out := a.Out
	if r.Quiet {
		out = io.Discard
	}
	fail := func(err error) int {
		if r.JSON {
			_ = json.NewEncoder(a.Err).Encode(map[string]string{"error": err.Error()})
		} else {
			fmt.Fprintln(a.Err, err)
		}
		return 1
	}
	require := func(n int) error {
		if len(positional) != n {
			return fmt.Errorf("%s expects %d positional arguments; got %d", cmd, n, len(positional))
		}
		return nil
	}
	if cmd == "git" {
		if err := require(0); err != nil {
			return fail(err)
		}
		s, err := project.Git(ctx, dir)
		if err != nil {
			return fail(err)
		}
		if r.JSON {
			_ = json.NewEncoder(out).Encode(s)
		} else {
			fmt.Fprintln(out, s.Root)
			for _, e := range s.Entries {
				fmt.Fprintf(out, "%s %s\n", e.Status, e.Path)
			}
		}
		return 0
	}
	if err := r.Validate(ctx); err != nil {
		return fail(err)
	}
	var err error
	switch cmd {
	case "init":
		if err = require(0); err == nil {
			err = r.Execute(ctx, "init", "--repository-version", "2")
		}
	case "backup", "checkpoint":
		err = r.Backup(ctx, backend.BackupOptions{Sources: positional, Exclude: exclude, Label: label, Checkpoint: cmd == "checkpoint", FullGit: fullGit, DryRun: dry})
	case "snapshots", "checkpoints":
		if err = require(0); err != nil {
			break
		}
		opts := []string{"snapshots"}
		if cmd == "checkpoints" {
			opts = append(opts, "--tag", "veyra:checkpoint")
		}
		err = r.Execute(ctx, opts...)
	case "files":
		if err = require(1); err != nil {
			break
		}
		var s backend.Snapshot
		s, err = r.Resolve(ctx, positional[0])
		if err == nil {
			err = r.Execute(ctx, "ls", s.ID)
		}
	case "diff":
		if err = require(2); err != nil {
			break
		}
		var s, t backend.Snapshot
		s, err = r.Resolve(ctx, positional[0])
		if err != nil {
			break
		}
		t, err = r.Resolve(ctx, positional[1])
		if err == nil {
			err = r.Execute(ctx, "diff", s.ID, t.ID)
		}
	case "restore":
		if err = require(1); err == nil {
			err = r.Restore(ctx, backend.RestoreOptions{Snapshot: positional[0], Root: restoreRoot, Target: target, Include: include, DryRun: dry})
		}
	case "verify":
		if err = require(0); err == nil {
			err = r.Check(ctx, full)
		}
	case "stats":
		if err = require(0); err == nil {
			err = r.Execute(ctx, "stats", "--mode", "raw-data")
		}
	case "forget":
		err = r.Forget(ctx, positional, confirm)
	case "prune":
		if err = require(0); err != nil {
			break
		}
		opts := []string{"prune"}
		if !confirm {
			opts = append(opts, "--dry-run")
		}
		err = r.Execute(ctx, opts...)
	case "run":
		childOut := out
		if r.JSON {
			childOut = a.Err
		}
		protectedRepo := r
		protectedRepo.Out = a.Err
		var result project.RunResult
		result, err = project.Protect(ctx, protectedRepo, project.RunOptions{Project: dir, Command: positional, Test: test, Timeout: timeout, Post: post, Exclude: exclude, FullGit: fullGit, Stdout: childOut, Stderr: a.Err, Stdin: a.In})
		if r.JSON {
			_ = json.NewEncoder(out).Encode(result)
		} else {
			fmt.Fprintf(out, "checkpoint: %s\ncommand exit: %d\n", result.Before, result.ExitCode)
			for _, c := range result.Changes {
				fmt.Fprintf(out, "%s %s\n", c.Kind, c.Path)
			}
			if result.TestExitCode != nil {
				fmt.Fprintf(out, "test exit: %d\n", *result.TestExitCode)
			}
			if result.After != "" {
				fmt.Fprintf(out, "post-checkpoint: %s\n", result.After)
			}
		}
		if err != nil {
			if result.Before != "" && result.ExitCode != 0 {
				fmt.Fprintln(a.Err, err)
				return result.ExitCode
			}
			return fail(err)
		}
		if result.ExitCode != 0 {
			return result.ExitCode
		}
		if result.TestExitCode != nil && *result.TestExitCode != 0 {
			return *result.TestExitCode
		}
		return 0
	}
	if err != nil {
		return fail(err)
	}
	return 0
}
