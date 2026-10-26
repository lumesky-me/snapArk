package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/lumesky-me/veyra/internal/backend"
	"github.com/lumesky-me/veyra/internal/config"
	"github.com/lumesky-me/veyra/internal/install"
)

func (a App) setup(ctx context.Context, args []string) int {
	fail := func(e error) int { fmt.Fprintln(a.Err, e); return 1 }
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	var cfg, password, binary, dir string
	var installOnly, confirm, structured bool
	fs.StringVar(&cfg, "config", "", "profile configuration path")
	fs.StringVar(&password, "password-file", "", "existing private password file (otherwise prompt securely)")
	fs.StringVar(&binary, "restic", "", "existing pinned restic executable")
	fs.BoolVar(&installOnly, "install-restic", false, "install only the checksum-pinned backend; requires --dir and --confirm")
	fs.StringVar(&dir, "dir", "", "user-controlled backend installation directory")
	fs.BoolVar(&confirm, "confirm", false, "approve the requested backend download and installation")
	fs.BoolVar(&structured, "json", false, "JSON result for --install-restic")
	if e := fs.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		return fail(errors.New("setup accepts options only"))
	}
	if installOnly {
		if !confirm || dir == "" {
			return fail(errors.New("download and installation require --dir PATH --confirm; existing files and PATH are never changed"))
		}
		installed, e := install.Install(ctx, dir)
		if e != nil {
			return fail(e)
		}
		if structured {
			return encodeResult(a.Out, map[string]string{"installed": installed, "version": install.Version}, a.Err)
		}
		fmt.Fprintf(a.Out, "Installed verified restic %s at %q. PATH unchanged.\nRun veyra setup --restic %q to create a profile.\n", install.Version, installed, installed)
		return 0
	}
	if structured {
		return fail(errors.New("interactive setup does not emit JSON; use profile add, init and backup for scripts"))
	}
	p, e := a.prompts(ctx)
	if e != nil {
		return fail(e)
	}
	store, e := configStore(cfg)
	if e != nil {
		return fail(e)
	}
	f, e := store.Read()
	if e != nil {
		return fail(e)
	} // Never replace an unreadable config.
	exe, e := os.Executable()
	if e != nil {
		return fail(e)
	}
	fmt.Fprintf(a.Out, "Set up VEYRA on %s/%s. Type q to cancel.\nExecutable: %q\nProfiles: %q\n", runtime.GOOS, runtime.GOARCH, exe, store.Path)
	found, version, backendErr := backend.Backend(ctx, binary)
	if backendErr != nil {
		fmt.Fprintln(a.Out, backendErr)
		name, digest, e := install.Artifact()
		if e != nil {
			return fail(e)
		}
		if dir == "" {
			dir, e = p.line("Install restic in which directory? (no PATH changes):")
			if e != nil {
				return fail(e)
			}
		}
		if dir == "" {
			return fail(errors.New("installation directory cannot be empty"))
		}
		fmt.Fprintf(a.Out, "Download %s from the official restic GitHub release.\nSHA-256: %s\nDestination: %q\n", name, digest, dir)
		yes, e := p.yes("Download, verify and install this executable?")
		if e != nil {
			return fail(e)
		}
		if !yes {
			return fail(errCancelled)
		}
		found, e = install.Install(ctx, dir)
		if e != nil {
			return fail(e)
		}
		version = "restic " + install.Version
	}
	fmt.Fprintf(a.Out, "Backend: %s at %q\n", version, found)
	name, e := p.line("Name this backup profile (letters, numbers, _ and -):")
	if e != nil {
		return fail(e)
	}
	if _, ok := f.Profiles[name]; ok {
		return fail(errors.New("profile already exists; setup will not replace it"))
	}
	repo, e := p.line("Backup repository path (use a separate disk for disk-loss protection):")
	if e != nil {
		return fail(e)
	}
	if repo == "" {
		return fail(errors.New("repository path cannot be empty"))
	}
	repo, e = filepath.Abs(repo)
	if e != nil {
		return fail(e)
	}
	profile := config.Profile{Name: name, Repository: repo, Backend: found, Verify: "full", ExpectedHours: 24}
	profile.ID, e = config.NewID()
	if e != nil {
		return fail(e)
	}
	for {
		label := "Source directory:"
		if len(profile.Sources) > 0 {
			label = "Another source directory (Enter to finish):"
		}
		source, e := p.line(label)
		if e != nil {
			return fail(e)
		}
		if source == "" {
			if len(profile.Sources) == 0 {
				return fail(errors.New("at least one source is required"))
			}
			break
		}
		source, e = backend.Canonical(source)
		if e != nil {
			return fail(e)
		}
		info, e := os.Stat(source)
		if e != nil || !info.IsDir() {
			return fail(errors.New("source must be an accessible directory"))
		}
		profile.Sources = append(profile.Sources, source)
	}
	for {
		exclude, e := p.line("Exclude pattern, for example node_modules (Enter to finish):")
		if e != nil {
			return fail(e)
		}
		if exclude == "" {
			break
		}
		profile.Exclude = append(profile.Exclude, exclude)
	}
	if e = profile.Validate(); e != nil {
		return fail(e)
	}
	if e = profileLocation(store, profile); e != nil {
		return fail(e)
	}
	r := backend.Repository{Path: repo, Binary: found, PasswordFile: password, Out: a.Err, Err: a.Err}
	if e = attachHistory(&r, cfg, &profile); e != nil {
		return fail(e)
	}
	// Decide whether initialization is safe before requesting a password.
	initialize := false
	info, e := os.Lstat(repo)
	if errors.Is(e, os.ErrNotExist) {
		initialize = true
	} else if e != nil {
		return fail(e)
	} else {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fail(errors.New("repository must be a directory, not a symbolic link"))
		}
		file, e := os.Open(repo)
		if e != nil {
			return fail(e)
		}
		_, e = file.Readdirnames(1)
		_ = file.Close()
		initialize = errors.Is(e, io.EOF)
		if !initialize {
			if _, e = os.Stat(filepath.Join(repo, "config")); e != nil {
				return fail(errors.New("directory is nonempty and is not an existing restic repository; nothing initialized"))
			}
		}
	}
	fmt.Fprintln(a.Out, "Encryption needs a password. Keep it in a password manager or offline safe place. A lost password cannot be recovered. VEYRA will not save a password entered here.")
	if password == "" {
		r.PasswordInput, e = p.password()
		if e != nil {
			return fail(e)
		}
		defer wipe(r.PasswordInput)
		if initialize {
			fmt.Fprintln(a.Out, "Confirm the new repository password.")
			again, e := p.password()
			if e != nil {
				return fail(e)
			}
			match := bytes.Equal(r.PasswordInput, again)
			wipe(again)
			if !match {
				return fail(errors.New("passwords do not match; nothing initialized"))
			}
		}
	}
	if e = r.Validate(ctx); e != nil {
		return fail(e)
	}
	if password != "" {
		pw, e := backend.Canonical(password)
		if e != nil {
			return fail(e)
		}
		for _, source := range profile.Sources {
			if backend.Within(source, pw) {
				return fail(errors.New("keep the password file outside every source"))
			}
		}
	}
	fmt.Fprintf(a.Out, "\nProfile: %s\nRepository: %q\nSources: %q\nExclusions: %q\nVerification: full repository data after backup\n", profile.Name, repo, profile.Sources, profile.Exclude)
	if initialize {
		fmt.Fprintln(a.Out, "Next: initialize a new encrypted repository, then save this profile.")
		yes, e := p.yes("Initialize and save?")
		if e != nil {
			return fail(e)
		}
		if !yes {
			return fail(errCancelled)
		}
		// Recheck emptiness after approval; never initialize over unrelated content.
		if entries, e := os.ReadDir(repo); e == nil && len(entries) > 0 {
			return fail(errors.New("repository directory is no longer empty"))
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return fail(e)
		}
		if e = r.Execute(ctx, "init", "--repository-version", "2"); e != nil {
			return fail(fmt.Errorf("initialization failed; inspect the repository before retrying: %w", e))
		}
	} else {
		if _, e = r.Identity(ctx); e != nil {
			return fail(e)
		}
		yes, e := p.yes("Use this existing encrypted repository and save the profile?")
		if e != nil {
			return fail(e)
		}
		if !yes {
			return fail(errCancelled)
		}
	}
	e = store.Update(func(f *config.File) error {
		if _, ok := f.Profiles[name]; ok {
			return errors.New("profile was created concurrently; existing configuration preserved")
		}
		f.Profiles[name] = profile
		if f.Default == "" {
			f.Default = name
		}
		return nil
	})
	if e != nil {
		return fail(fmt.Errorf("repository retained; profile could not be saved: %w", e))
	}
	fmt.Fprintln(a.Out, "Profile saved. No schedule installed.")
	options := backend.BackupOptions{Sources: profile.Sources, Exclude: profile.Exclude, Tags: []string{profile.Tag() + ":complete"}}
	preview, e := p.yes("Preview the initial backup without storing data?")
	if e != nil {
		return fail(e)
	}
	if preview {
		plan, e := r.Plan(ctx, options)
		if e != nil {
			return fail(e)
		}
		fmt.Fprintf(a.Out, "Preview: %d files, %d bytes before deduplication.\n", plan.EstimatedFiles, plan.EstimatedBytes)
	}
	backup, e := p.yes("Create the initial backup and authenticate all repository data?")
	if e != nil {
		return fail(e)
	}
	if backup {
		id, e := recordedBackup(ctx, r, func() (string, error) { return r.VerifiedBackup(ctx, options, profile.Tag()+":complete", true) })
		if e != nil {
			return fail(fmt.Errorf("profile is saved; backup did not complete successfully: %w", e))
		}
		fmt.Fprintln(a.Out, "Verified backup completed. Snapshot:", id)
	} else {
		fmt.Fprintln(a.Out, "No initial backup created.")
	}
	fmt.Fprintf(a.Out, "\nNext: veyra backup --config %q --profile %s\nRecover: veyra recover --config %q --profile %s\nScheduling is separate: inspect veyra schedule preview --config %q --profile %s --password-file PATH\n", store.Path, name, store.Path, name, store.Path, name)
	return 0
}
