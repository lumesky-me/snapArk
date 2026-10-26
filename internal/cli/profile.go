package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/lumesky-me/veyra/internal/backend"
	"github.com/lumesky-me/veyra/internal/config"
)

func configStore(path string) (config.Store, error) {
	if path != "" {
		abs, e := filepath.Abs(path)
		return config.Store{Path: abs}, e
	}
	return config.DefaultStore()
}
func (a App) profile(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Out, "Usage: veyra profile add|list|show|export|import|replace|validate|delete|default [options] NAME\nProfiles contain no passwords. add/replace accept --repo, repeated --source, --include (relative paths), --exclude, --label, --verify metadata|full, --expected-hours and --keep-* retention counts. Options precede NAME. --config selects an alternative configuration file.")
		return 0
	}
	action := args[0]
	fs := flag.NewFlagSet("profile "+action, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	var path, from string
	var structured bool
	var sources, include, exclude stringsFlag
	p := config.Profile{Verify: "metadata"}
	fs.StringVar(&from, "from", "", "exported profile JSON to import (requires deliberate --repo and --source remapping)")
	fs.StringVar(&path, "config", "", "configuration file (default: per-user config directory)")
	fs.BoolVar(&structured, "json", false, "JSON output")
	fs.StringVar(&p.Repository, "repo", "", "repository path")
	fs.StringVar(&p.Backend, "restic", "", "optional pinned restic executable path")
	fs.Var(&sources, "source", "source directory (repeatable)")
	fs.Var(&include, "include", "relative file or directory to include (repeatable, no glob expansion)")
	fs.Var(&exclude, "exclude", "restic exclusion pattern (repeatable)")
	fs.StringVar(&p.Label, "label", "", "snapshot label")
	fs.StringVar(&p.Verify, "verify", "metadata", "post-backup verification: metadata or full")
	fs.BoolVar(&p.Disabled, "disabled", false, "disable profile backups")
	fs.IntVar(&p.ExpectedHours, "expected-hours", 24, "expected backup age in hours (0 disables freshness warning)")
	fs.IntVar(&p.FullVerifyHours, "full-verify-hours", 0, "full-data verification freshness expectation (0 disables)")
	retentionFlags(fs, &p.Retention)
	if e := fs.Parse(args[1:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	fail := func(e error) int {
		if structured {
			_ = json.NewEncoder(a.Err).Encode(map[string]string{"error": e.Error()})
		} else {
			fmt.Fprintln(a.Err, e)
		}
		return 1
	}
	store, e := configStore(path)
	if e != nil {
		return fail(e)
	}
	if action == "list" {
		if fs.NArg() != 0 {
			return fail(errors.New("list takes no profile name"))
		}
		f, e := store.Read()
		if e != nil {
			return fail(e)
		}
		if structured {
			_ = json.NewEncoder(a.Out).Encode(f)
		} else {
			names := []string{}
			for n := range f.Profiles {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				fmt.Fprintf(a.Out, "%s default=%t disabled=%t\n", n, n == f.Default, f.Profiles[n].Disabled)
			}
		}
		return 0
	}
	if fs.NArg() != 1 {
		return fail(errors.New("provide one profile name after options"))
	}
	name := fs.Arg(0)
	switch action {
	case "show", "validate", "export":
		f, e := store.Read()
		if e != nil {
			return fail(e)
		}
		p, e := f.Select(name)
		if e != nil {
			return fail(e)
		}
		if action == "validate" {
			if e = profileLocation(store, p); e != nil {
				return fail(e)
			}
		}
		var output any = p
		if action == "export" {
			output = struct {
				Version int            `json:"version"`
				Profile config.Profile `json:"profile"`
			}{1, p}
		}
		if e = json.NewEncoder(a.Out).Encode(output); e != nil {
			return fail(e)
		}
		return 0
	case "add", "replace", "import":
		if action == "import" {
			if from == "" || !hasFlag(fs, "repo") || !hasFlag(fs, "source") {
				return fail(errors.New("import requires --from, --repo and --source; no original machine paths are assumed"))
			}
			file, e := os.Open(from)
			if e != nil {
				return fail(e)
			}
			info, statErr := file.Stat()
			if statErr != nil || info.Size() > 4<<20 {
				_ = file.Close()
				return fail(errors.New("profile export cannot be read or exceeds 4 MiB"))
			}
			var exported struct {
				Version int            `json:"version"`
				Profile config.Profile `json:"profile"`
			}
			decoder := json.NewDecoder(io.LimitReader(file, 4<<20))
			decoder.DisallowUnknownFields()
			e = decoder.Decode(&exported)
			var extra any
			if e == nil && decoder.Decode(&extra) != io.EOF {
				e = errors.New("trailing export data")
			}
			_ = file.Close()
			if e != nil {
				return fail(e)
			}
			if exported.Version != 1 {
				return fail(errors.New("unsupported profile export version"))
			}
			imported := exported.Profile
			imported.Backend = p.Backend // Never import an executable path from another machine.
			imported.Repository = p.Repository
			imported.Sources = nil
			if hasFlag(fs, "include") {
				imported.Include = include
			}
			if hasFlag(fs, "exclude") {
				imported.Exclude = exclude
			}
			if hasFlag(fs, "label") {
				imported.Label = p.Label
			}
			if hasFlag(fs, "verify") {
				imported.Verify = p.Verify
			}
			if hasFlag(fs, "expected-hours") {
				imported.ExpectedHours = p.ExpectedHours
			}
			if hasFlag(fs, "full-verify-hours") {
				imported.FullVerifyHours = p.FullVerifyHours
			}
			imported.Disabled = true // Imported scheduling is deliberately disabled until reviewed.
			p = imported
			include = append(stringsFlag{}, p.Include...)
			exclude = append(stringsFlag{}, p.Exclude...)
		}
		if p.Backend != "" {
			p.Backend, e = filepath.Abs(p.Backend)
			if e != nil {
				return fail(e)
			}
		}
		p.Name = name
		p.Include = include
		p.Exclude = exclude
		p.Repository, e = filepath.Abs(p.Repository)
		if e != nil {
			return fail(e)
		}
		if !hasFlag(fs, "repo") {
			return fail(errors.New("provide --repo"))
		}
		for _, s := range sources {
			abs, e := filepath.Abs(s)
			if e != nil {
				return fail(e)
			}
			p.Sources = append(p.Sources, abs)
		}
		if e = profileLocation(store, p); e != nil {
			return fail(e)
		}
		e = store.Update(func(f *config.File) error {
			old, exists := f.Profiles[name]
			if (action == "add" || action == "import") && exists {
				return errors.New("profile already exists; use profile replace")
			}
			if action == "replace" && !exists {
				return errors.New("profile does not exist; use profile add")
			}
			if exists {
				p.ID = old.ID
			} else {
				var e error
				p.ID, e = config.NewID()
				if e != nil {
					return e
				}
			}
			if e := p.Validate(); e != nil {
				return e
			}
			f.Profiles[name] = p
			return nil
		})
	case "delete":
		e = store.Update(func(f *config.File) error {
			if _, ok := f.Profiles[name]; !ok {
				return errors.New("profile does not exist")
			}
			delete(f.Profiles, name)
			if f.Default == name {
				f.Default = ""
			}
			return nil
		})
	case "default":
		e = store.Update(func(f *config.File) error {
			if _, ok := f.Profiles[name]; !ok {
				return errors.New("profile does not exist")
			}
			f.Default = name
			return nil
		})
	default:
		return fail(errors.New("unknown profile operation; use veyra profile"))
	}
	if e != nil {
		return fail(e)
	}
	if structured {
		_ = json.NewEncoder(a.Out).Encode(map[string]string{"result": "saved", "profile": name})
	} else {
		fmt.Fprintln(a.Out, "Profile configuration saved; backup repository unchanged.")
	}
	return 0
}
func hasFlag(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
func retentionFlags(fs *flag.FlagSet, r *config.Retention) {
	fs.IntVar(&r.Last, "keep-last", 0, "keep last N snapshots")
	fs.IntVar(&r.Daily, "keep-daily", 0, "keep N daily snapshots")
	fs.IntVar(&r.Weekly, "keep-weekly", 0, "keep N weekly snapshots")
	fs.IntVar(&r.Monthly, "keep-monthly", 0, "keep N monthly snapshots")
	fs.IntVar(&r.Yearly, "keep-yearly", 0, "keep N yearly snapshots")
}
func profileLocation(s config.Store, p config.Profile) error {
	cfg, e := backend.Canonical(s.Path)
	if e != nil {
		return e
	}
	repo, e := backend.Canonical(p.Repository)
	if e != nil {
		return e
	}
	if backend.Within(repo, cfg) {
		return errors.New("configuration cannot be inside a repository; choose --config outside it")
	}
	for _, source := range p.Sources {
		root, e := backend.Canonical(source)
		if e != nil {
			return e
		}
		if backend.Within(root, cfg) {
			return errors.New("configuration is inside a source; choose --config outside all sources")
		}
		if backend.Within(root, repo) || backend.Within(repo, root) {
			return errors.New("repository and sources must not overlap")
		}
	}
	return nil
}
