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
	"sync"
	"time"

	"github.com/lumesky-me/veyra/internal/backend"
	"github.com/lumesky-me/veyra/internal/config"
	"github.com/lumesky-me/veyra/internal/project"
)

const help = `VEYRA — encrypted backups and developer recovery (experimental)

Usage: veyra COMMAND [OPTIONS] [ARGUMENTS]

Commands:
  recover       Guided recovery into a new directory
  setup         Create a backup profile with guided installation and first backup
  test-restore  Rehearse sampled or full recovery in a private temporary directory
  history       Inspect local verification and scheduled-run records
  profile       Manage named backup profiles
  plan          Preview a backup without creating snapshots
  doctor        Diagnose configuration and repository access
  credentials   Check password-file permissions for unattended use
  status        Show authoritative profile backup freshness
  retention     Preview scoped retention (--confirm applies)
  schedule      Preview or manage current-user backup schedules
  init          Initialize an encrypted local repository
  backup        Back up one or more directories
  snapshots     List snapshots
  checkpoint    Save a project without changing Git history
  checkpoints   List developer checkpoints
  find          Search snapshot paths by literal substring
  files         List snapshot contents
  diff          Compare two snapshots
  inspect-restore  Explain an incomplete restore without changing it
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
	Out, Err   io.Writer
	In         io.Reader
	isTerminal func() bool
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
	var outputLock sync.Mutex
	a.Out = &outputWriter{a.Out, &outputLock}
	a.Err = &outputWriter{a.Err, &outputLock}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprint(a.Out, help)
		return 0
	}
	if args[0] == "version" {
		return a.version(args[1:])
	}
	if args[0] == "history" && len(args) > 1 && args[1] == "archive" {
		return a.archiveHistory(ctx, args[2:])
	}
	if args[0] == "recover" {
		return a.recover(ctx, args[1:])
	}
	if args[0] == "setup" {
		return a.setup(ctx, args[1:])
	}
	if args[0] == "profile" {
		return a.profile(ctx, args[1:])
	}
	if args[0] == "schedule" && len(args) > 1 {
		switch args[1] {
		case "preview", "install", "replace", "status", "enable", "disable", "remove":
			operation := args[1]
			args = append(append([]string{"schedule"}, args[2:]...), operation)
		}
	}
	cmd := args[0]
	switch cmd {
	case "init", "backup", "snapshots", "checkpoint", "checkpoints", "files", "diff", "restore", "verify", "stats", "forget", "prune", "git", "run", "plan", "doctor", "status", "retention", "schedule", "find", "history", "test-restore", "credentials", "inspect-restore":
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
	fs.StringVar(&r.Binary, "restic", "", "explicit pinned restic executable")
	fs.BoolVar(&r.JSON, "json", false, "structured JSON output")
	fs.BoolVar(&r.Quiet, "quiet", false, "suppress normal output")
	var profileName, configPath string
	fs.StringVar(&profileName, "profile", "", "named profile (default applies when --repo is omitted)")
	fs.StringVar(&configPath, "config", "", "alternative profiles configuration file")
	var selected *config.Profile
	var exclude, include, test stringsFlag
	var label, target, dir, restoreRoot, query, tempDir, archiveDir, eventID string
	var full, dry, confirm, fullGit, post, tracked, scheduled bool
	var sampleFiles int
	var sampleBytes int64
	var timeout time.Duration
	switch cmd {
	case "backup", "checkpoint", "plan":
		fs.BoolVar(&scheduled, "scheduled", false, "noninteractive coordinated profile backup with run history")
		fs.Var(&exclude, "exclude", "exclude pattern (repeatable; ignored files are included by default)")
		fs.StringVar(&label, "label", "", "snapshot label")
		fs.BoolVar(&dry, "dry-run", false, "scan without storing a snapshot")
		if cmd == "checkpoint" {
			fs.BoolVar(&fullGit, "full-git", false, "include Git internals")
		}
	case "history":
		fs.StringVar(&archiveDir, "archive", "", "inspect a committed history archive")
		fs.StringVar(&eventID, "id", "", "inspect one exact history record ID")
	case "test-restore":
		fs.BoolVar(&full, "full", false, "authorize complete recovery rather than bounded file-content sampling")
		fs.IntVar(&sampleFiles, "sample-files", 5, "maximum sampled files (1..32)")
		fs.Int64Var(&sampleBytes, "max-bytes", 64<<20, "maximum sampled content bytes")
		fs.StringVar(&tempDir, "temp-dir", "", "existing parent for a new private rehearsal directory")
	case "find":
		fs.StringVar(&query, "query", "", "literal substring to search in snapshot paths")
	case "inspect-restore":
		fs.StringVar(&target, "target", "", "destination of an incomplete restore")
	case "restore":
		fs.StringVar(&restoreRoot, "root", "", "snapshot subfolder (single-source snapshots default to their source root)")
		fs.StringVar(&target, "target", "", "new restore directory (parent must exist)")
		fs.Var(&include, "include", "snapshot path pattern to recover (repeatable)")
		fs.BoolVar(&dry, "dry-run", false, "preview restoration without writing files")
	case "doctor":
		fs.BoolVar(&full, "full", false, "explicitly read and authenticate all repository data")
	case "verify":
		fs.BoolVar(&full, "full", false, "authenticate and read all stored data")
	case "schedule", "retention", "forget", "prune":
		fs.BoolVar(&confirm, "confirm", false, "apply destructive operation (default previews only)")
	case "git":
		fs.BoolVar(&tracked, "tracked", false, "also list tracked files")
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
			if tracked {
				for _, p := range s.Tracked {
					fmt.Fprintf(out, "tracked %q\n", p)
				}
			}
			for _, e := range s.Entries {
				fmt.Fprintf(out, "%s %q\n", e.Status, e.Path)
			}
		}
		return 0
	}
	if cmd == "inspect-restore" {
		if e := require(0); e != nil {
			return fail(e)
		}
		if target == "" {
			return fail(errors.New("provide --target for the incomplete restore"))
		}
		report, e := backend.InspectRestore(target)
		if e != nil {
			return fail(e)
		}
		if r.JSON {
			return encodeResult(out, report, a.Err)
		}
		fmt.Fprintf(out, "Snapshot: %s\nRepository identity: %s\nOutput: %s\n%s\n", report.Attempt.Snapshot, report.Attempt.Repository, report.Output, report.Advice)
		return 0
	}
	if cmd == "credentials" {
		if e := require(0); e != nil {
			return fail(e)
		}
		if r.PasswordFile == "" {
			return fail(errors.New("provide --password-file; this check never changes permissions"))
		}
		release, e := backend.ProtectPassword(r.PasswordFile)
		if e != nil {
			return fail(e)
		}
		release()
		if r.JSON {
			return encodeResult(out, map[string]string{"state": "accepted", "detail": "permissions checked for the current account; no permissions changed"}, a.Err)
		}
		fmt.Fprintln(out, "Password-file permissions accepted for the current account; no permissions changed.")
		return 0
	}
	if profileName != "" || (r.Path == "" && cmd != "history") {
		store, e := configStore(configPath)
		if e != nil {
			return fail(e)
		}
		f, e := store.Read()
		if e != nil {
			return fail(e)
		}
		if profileName != "" || (f.Default != "" && cmd != "status" && cmd != "history") {
			p, e := f.Select(profileName)
			if e != nil {
				return fail(e)
			}
			selected = &p
			if hasFlag(fs, "repo") {
				return fail(errors.New("--repo cannot override a named profile"))
			}
			r.Path = p.Repository
			if r.Binary == "" {
				r.Binary = p.Backend
			}
			r.SnapshotTag = p.Tag() + ":complete"
			if cmd == "checkpoint" || cmd == "checkpoints" {
				r.SnapshotTag = p.Tag() + ":checkpoint"
			}
			r.SnapshotHost, _ = os.Hostname()
		}
	}
	if cmd == "history" {
		if e := require(0); e != nil {
			return fail(e)
		}
		return a.history(configPath, selected, r.JSON, archiveDir, eventID)
	}
	if cmd == "verify" || cmd == "test-restore" || cmd == "run" || cmd == "doctor" || (selected != nil && (cmd == "backup" || cmd == "checkpoint")) {
		if e := attachHistory(&r, configPath, selected); e != nil {
			return fail(e)
		}
	}
	if scheduled && (selected == nil || cmd != "backup" || dry) {
		return fail(errors.New("--scheduled requires a real profile backup"))
	}
	if cmd == "test-restore" {

		if len(positional) > 1 {
			return fail(errors.New("provide at most one snapshot reference"))
		}
		ref := "latest"
		if len(positional) == 1 {
			ref = positional[0]
		}
		if e := r.Validate(ctx); e != nil {
			return fail(e)
		}
		if e := a.repositoryPassword(ctx, &r, false); e != nil {
			return fail(e)
		}
		defer wipe(r.PasswordInput)
		return a.rehearsal(ctx, r, ref, tempDir, full, sampleFiles, sampleBytes)
	}
	if cmd == "plan" || cmd == "doctor" || cmd == "status" || cmd == "retention" || cmd == "schedule" {
		if cmd == "plan" && len(exclude) > 0 {
			if selected != nil && len(positional) > 0 {
				return fail(errors.New("sources cannot override a named profile"))
			}
			if selected == nil {
				selected = &config.Profile{Sources: positional}
			} else {
				copy := *selected
				selected = &copy
			}
			selected.Exclude = append(append([]string{}, selected.Exclude...), exclude...)
			if len(positional) > 0 {
				selected.Sources = positional
				positional = nil
			}
		}
		if cmd != "status" && cmd != "schedule" {
			if e := r.Validate(ctx); e != nil {
				if cmd != "doctor" {
					return fail(e)
				}
			} else if e := a.repositoryPassword(ctx, &r, false); e != nil {
				return fail(e)
			}
			defer wipe(r.PasswordInput)
		}
		return a.workflow(ctx, cmd, r, selected, configPath, full, confirm, positional)
	}
	if !scheduled {
		if err := r.Validate(ctx); err != nil {
			return fail(err)
		}
		if e := a.repositoryPassword(ctx, &r, cmd == "init"); e != nil {
			return fail(e)
		}
		defer wipe(r.PasswordInput)
	}

	var err error
	switch cmd {
	case "init":
		if err = require(0); err == nil {
			err = r.Execute(ctx, "init", "--repository-version", "2")
		}
	case "backup", "checkpoint":
		options := backend.BackupOptions{Sources: positional, Exclude: exclude, Label: label, Checkpoint: cmd == "checkpoint", FullGit: fullGit, DryRun: dry}
		if selected != nil {
			if len(exclude) > 0 {
				return fail(errors.New("profile exclusions cannot be overridden during backup; use profile replace so snapshot scope remains accurate"))
			}
			if selected.Disabled {
				return fail(errors.New("profile is disabled"))
			}
			if len(positional) > 0 {
				return fail(errors.New("sources cannot override a named profile"))
			}
			options.Sources = selected.Sources
			options.Include = selected.Include
			options.Exclude = append(append([]string{}, selected.Exclude...), exclude...)
			options.Tags = []string{r.SnapshotTag}
			if label == "" {
				options.Label = selected.Label
			}
		}
		if selected != nil && !dry {
			var id string
			operation := func() (string, error) {
				if e := r.Validate(ctx); e != nil {
					return "", e
				}
				return r.VerifiedBackup(ctx, options, r.SnapshotTag, selected.Verify == "full")
			}
			actualOperation := operation
			operation = func() (string, error) { return recordedBackup(ctx, r, actualOperation) }
			if scheduled {
				id, err = a.scheduled(ctx, r, selected, operation)
			} else {
				id, err = operation()
			}
			if err == nil {
				if r.JSON {
					err = json.NewEncoder(out).Encode(map[string]string{"message_type": "completed", "snapshot_id": id, "verification": selected.Verify})
				} else {
					fmt.Fprintf(out, "Snapshot %s completed; %s verification passed.\n", id, selected.Verify)
				}
			}
		} else {
			err = r.Backup(ctx, options)
		}

	case "snapshots", "checkpoints":
		if err = require(0); err != nil {
			break
		}
		listing := r
		if cmd == "checkpoints" && selected == nil {
			listing.SnapshotTag = "veyra:checkpoint"
		}
		if r.JSON {
			opts := []string{"snapshots"}
			if listing.SnapshotTag != "" {
				opts = append(opts, "--tag", listing.SnapshotTag)
			}
			if listing.SnapshotHost != "" {
				opts = append(opts, "--host", listing.SnapshotHost)
			}
			err = r.Execute(ctx, opts...)
		} else {
			var snapshots []backend.Snapshot
			snapshots, err = listing.Snapshots(ctx)
			if err == nil {
				if len(snapshots) == 0 {
					fmt.Fprintln(out, "No matching snapshots.")
				}
				for _, snapshot := range snapshots {
					labels, kind := snapshotLabels(snapshot.Tags)
					fmt.Fprintf(out, "%s  %s  host=%q  %s", abbreviate(snapshot.ID, 12), snapshotDate(snapshot.Time), snapshot.Hostname, kind)
					if labels != "" {
						fmt.Fprintf(out, "  %q", labels)
					}
					fmt.Fprintln(out)
				}
			}
		}
	case "find":
		ref := "latest"
		if len(positional) > 1 {
			return fail(errors.New("find accepts at most one snapshot reference"))
		}
		if len(positional) == 1 {
			ref = positional[0]
		}
		var nodes []backend.Node
		nodes, err = r.Search(ctx, ref, query)
		if err == nil {
			if r.JSON {
				err = json.NewEncoder(out).Encode(nodes)
			} else {
				for _, n := range nodes {
					fmt.Fprintf(out, "%s %q\n", n.Type, n.Path)
				}
			}
		}
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
		var runInclude, runTags []string
		if selected != nil {
			if selected.Disabled {
				return fail(errors.New("profile is disabled"))
			}
			if len(selected.Sources) != 1 {
				return fail(errors.New("protected commands require a single-source profile"))
			}
			if len(exclude) > 0 {
				return fail(errors.New("profile exclusions cannot be overridden; use profile replace"))
			}
			if hasFlag(fs, "project") {
				configured, e := backend.Canonical(selected.Sources[0])
				if e != nil {
					return fail(e)
				}
				actual, e := backend.Canonical(dir)
				if e != nil {
					return fail(e)
				}
				if configured != actual {
					return fail(errors.New("--project must match the profile source"))
				}
			}
			dir = selected.Sources[0]
			exclude = append(stringsFlag{}, selected.Exclude...)
			runInclude = selected.Include
			runTags = []string{selected.Tag() + ":checkpoint"}
		}
		protectedRepo := r
		if selected != nil {
			protectedRepo.SnapshotTag = selected.Tag() + ":checkpoint"
		}
		protectedRepo.Out = a.Err
		var result project.RunResult
		result, err = project.Protect(ctx, protectedRepo, project.RunOptions{Project: dir, Command: positional, Test: test, Timeout: timeout, Post: post, Exclude: exclude, Include: runInclude, Tags: runTags, FullGit: fullGit, Stdout: childOut, Stderr: a.Err, Stdin: a.In})
		if err != nil && result.Error == "" {
			result.Error = err.Error()
		}
		if r.JSON {
			_ = json.NewEncoder(out).Encode(result)
		} else {
			fmt.Fprintf(out, "checkpoint: %s\ncommand exit: %d\n", result.Before, result.ExitCode)
			for _, c := range result.Changes {
				fmt.Fprintf(out, "%s %q\n", c.Kind, c.Path)
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
