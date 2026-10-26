package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lumesky-me/veyra/internal/backend"
	"github.com/lumesky-me/veyra/internal/config"
)

func (a App) recover(ctx context.Context, args []string) int {
	fail := func(e error) int { fmt.Fprintln(a.Err, e); return 1 }
	fs := flag.NewFlagSet("recover", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	var repo, password, cfg, profile, binary string
	fs.StringVar(&binary, "restic", "", "explicit restic executable")
	fs.StringVar(&repo, "repo", "", "local or mounted backup repository")
	fs.StringVar(&password, "password-file", "", "existing private password file (otherwise prompt securely)")
	fs.StringVar(&cfg, "config", "", "profile configuration path")
	fs.StringVar(&profile, "profile", "", "profile whose repository to browse (all hosts and historical revisions)")
	if e := fs.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		return fail(errors.New("recover accepts options only"))
	}
	p, e := a.prompts(ctx)
	if e != nil {
		return fail(e)
	}
	fmt.Fprintln(a.Out, "Recover your backup. Type q at any prompt to cancel. Original files are left alone.")
	var sources []string
	if repo != "" && profile != "" {
		return fail(errors.New("choose --repo or --profile"))
	}
	if repo == "" {
		store, e := configStore(cfg)
		if e != nil {
			return fail(e)
		}
		f, e := store.Read()
		if e != nil {
			return fail(e)
		}
		var selected config.Profile
		if profile != "" {
			selected, e = f.Select(profile)
			if e != nil {
				return fail(e)
			}
		} else if len(f.Profiles) > 0 {
			names := []string{}
			for name := range f.Profiles {
				names = append(names, name)
			}
			sort.Strings(names)
			for i, name := range names {
				fmt.Fprintf(a.Out, "%d. %s\n", i+1, name)
			}
			s, e := p.line("Profile number, or Enter to enter another repository:")
			if e != nil {
				return fail(e)
			}
			if s != "" {
				n, e := strconv.Atoi(s)
				if e != nil || n < 1 || n > len(names) {
					return fail(errors.New("invalid profile number"))
				}
				selected = f.Profiles[names[n-1]]
			}
		}
		if selected.Name != "" {
			repo = selected.Repository
			if binary == "" {
				binary = selected.Backend
			}
			sources = selected.Sources
		} else {
			repo, e = p.line("Backup repository path:")
			if e != nil {
				return fail(e)
			}
		}
	}
	if repo == "" {
		return fail(errors.New("repository path cannot be empty"))
	}
	if info, e := os.Stat(repo); e != nil || !info.IsDir() {
		return fail(errors.New("repository directory is unavailable; connect the backup drive and check its path"))
	}
	r := backend.Repository{Path: repo, Binary: binary, PasswordFile: password, Out: a.Err, Err: a.Err, ReadOnly: true}
	if password == "" {
		r.PasswordInput, e = p.password()
		if e != nil {
			return fail(e)
		}
		defer wipe(r.PasswordInput)
	}
	if e = r.Validate(ctx); e != nil {
		return fail(e)
	}
	identity, e := r.Identity(ctx)
	if e != nil {
		return fail(fmt.Errorf("cannot open repository; check password and backup drive: %w", e))
	}
	snapshots, e := r.Snapshots(ctx)
	if e != nil {
		return fail(e)
	}
	snapshot, e := chooseSnapshot(p, snapshots)
	if e != nil {
		return fail(e)
	}
	// Use the same default root as scripted restore, including foreign drive paths.
	scopeRoot, e := r.DefaultRoot(ctx, snapshot)
	if e != nil {
		return fail(e)
	}
	includes, e := chooseFiles(p, r, snapshot.ID, scopeRoot)
	if e != nil {
		return fail(e)
	}
	target, e := p.line("New destination directory (its parent must exist):")
	if e != nil {
		return fail(e)
	}
	if target == "" {
		return fail(errors.New("destination cannot be empty"))
	}
	actual, e := backend.Canonical(target)
	if e != nil {
		return fail(e)
	}
	sources = append(sources, snapshot.Paths...)
	for _, source := range sources {
		if !filepath.IsAbs(source) {
			continue
		}
		root, e := backend.Canonical(source)
		if e == nil && backend.Within(root, actual) {
			return fail(errors.New("choose a destination outside original source directories"))
		}
	}
	fmt.Fprintf(a.Out, "\nRepository: %q\nRepository identity: %s\nSnapshot: %s (%s, host %q)\nSnapshot root: %q\nDestination: %q\n", repo, identity, snapshot.ID, snapshot.Time, snapshot.Hostname, scopeRoot, actual)
	labels, kind := snapshotLabels(snapshot.Tags)
	if labels != "" {
		fmt.Fprintf(a.Out, "Labels: %q\n", labels)
	}
	if kind == "unfinished backup" {
		fmt.Fprintln(a.Out, "This backup has no confirmed completion. Recovery will still authenticate and verify the restored contents.")
	}
	if len(includes) == 0 {
		fmt.Fprintln(a.Out, "Selection: entire selected root")
	} else {
		for _, v := range includes {
			fmt.Fprintf(a.Out, "Include: %q\n", v)
		}
	}
	fmt.Fprintln(a.Out, "Checking portable paths, links and authenticated content before extraction. Large backups may take time. Metadata support depends on your operating system and permissions.")
	options := backend.RestoreOptions{Snapshot: snapshot.ID, Root: scopeRoot, Target: actual, Include: includes, DryRun: true}
	if e = r.Restore(ctx, options); e != nil {
		return fail(e)
	}
	approval, e := p.line("Type restore to recover and verify into this new directory:")
	if e != nil {
		return fail(e)
	}
	if approval != "restore" {
		fmt.Fprintln(a.Out, "Cancelled; destination was not created.")
		return 0
	}
	after, e := r.Identity(ctx)
	if e != nil || after != identity {
		return fail(errors.New("repository identity changed; reopen recovery before proceeding"))
	}
	r.ReadOnly = false
	options.DryRun = false
	if e = r.Restore(ctx, options); e != nil {
		return fail(e)
	}
	fmt.Fprintf(a.Out, "\nRecovery completed and file contents verified.\nSnapshot: %s\nDestination: %q\n", snapshot.ID, actual)
	return 0
}
func chooseSnapshot(p *prompts, snapshots []backend.Snapshot) (backend.Snapshot, error) {
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Time > snapshots[j].Time })
	query := ""
	page := 0
	for {
		matches := []backend.Snapshot{}
		for _, s := range snapshots {
			if strings.Contains(strings.ToLower(s.Time+" "+snapshotDate(s.Time)+" "+s.Hostname+" "+s.ID+" "+strings.Join(s.Tags, " ")), strings.ToLower(query)) {
				matches = append(matches, s)
			}
		}
		if len(matches) == 0 && query == "" {
			return backend.Snapshot{}, errors.New("no snapshots found in this repository")
		}
		start := page * 10
		if start >= len(matches) {
			start = 0
			page = 0
		}
		end := start + 10
		if end > len(matches) {
			end = len(matches)
		}
		fmt.Fprintln(p.out, "\nSnapshots (all hosts and profile revisions):")
		for i := start; i < end; i++ {
			s := matches[i]
			labels, kind := snapshotLabels(s.Tags)
			fmt.Fprintf(p.out, "%d. %s  ID=%s\n   host=%s  %s", i-start+1, snapshotDate(s.Time), abbreviate(s.ID, 12), abbreviate(strconv.Quote(s.Hostname), 30), kind)
			if labels != "" {
				fmt.Fprint(p.out, "  "+abbreviate(strconv.Quote(labels), 45))
			}
			fmt.Fprintln(p.out)
		}
		s, e := p.line("Snapshot number, n/p for pages, or /text to search dates, labels, hosts and IDs:")
		if e != nil {
			return backend.Snapshot{}, e
		}
		switch {
		case s == "n":
			if end < len(matches) {
				page++
			}
		case s == "p":
			if page > 0 {
				page--
			}
		case strings.HasPrefix(s, "/"):
			query = strings.TrimPrefix(s, "/")
			page = 0
		default:
			n, e := strconv.Atoi(s)
			if e == nil && n > 0 && start+n <= end {
				return matches[start+n-1], nil
			}
			fmt.Fprintln(p.out, "Choose a displayed number.")
		}
	}
}
func literalInclude(p string) string {
	return strings.NewReplacer("[", "[[]", "*", "[*]", "?", "[?]").Replace(p)
}
func chooseFiles(p *prompts, r backend.Repository, id, scopeRoot string) ([]string, error) {
	dir, query, after := scopeRoot, "", ""
	var pages []string
	selected := map[string]bool{}
	for {
		nodes, more, e := r.Browse(p.context, id, dir, after, query, 12)
		if e != nil {
			return nil, e
		}
		fmt.Fprintf(p.out, "\nFolder %q (%d selected)\n", dir, len(selected))
		for i, n := range nodes {
			fmt.Fprintf(p.out, "%d. %-7s %q\n", i+1, n.Type, path.Base(n.Path))
		}
		s, e := p.line("Number opens a folder or selects a file; add N selects an entry. up, n, p, /search, all (whole root), clear, done:")
		if e != nil {
			return nil, e
		}
		switch {
		case s == "all":
			return nil, nil
		case s == "done":
			if len(selected) == 0 {
				fmt.Fprintln(p.out, "Select at least one entry, or choose all.")
				continue
			}
			result := []string{}
			for v := range selected {
				result = append(result, literalInclude("/"+strings.TrimPrefix(strings.TrimPrefix(v, strings.TrimSuffix(scopeRoot, "/")), "/")))
			}
			sort.Strings(result)
			return result, nil
		case s == "clear":
			selected = map[string]bool{}
		case s == "up":
			if dir != scopeRoot {
				dir = path.Dir(dir)
			}
			query = ""
			after = ""
			pages = nil
		case s == "n":
			if more && len(nodes) > 0 {
				pages = append(pages, after)
				after = nodes[len(nodes)-1].Path
			}
		case s == "p":
			if len(pages) > 0 {
				after = pages[len(pages)-1]
				pages = pages[:len(pages)-1]
			}
		case strings.HasPrefix(s, "/"):
			query = strings.TrimPrefix(s, "/")
			after = ""
			pages = nil
		default:
			add := strings.HasPrefix(s, "add ")
			n, e := strconv.Atoi(strings.TrimPrefix(s, "add "))
			if e != nil || n < 1 || n > len(nodes) {
				fmt.Fprintln(p.out, "Choose a displayed number or command.")
				continue
			}
			node := nodes[n-1]
			if node.Type == "dir" && !add {
				dir = node.Path
				after = ""
				query = ""
				pages = nil
			} else {
				if node.Type != "file" && node.Type != "dir" && node.Type != "symlink" {
					fmt.Fprintln(p.out, "This entry requires manual recovery.")
					continue
				}
				selected[node.Path] = true
			}
		}
	}
}

func snapshotDate(value string) string {
	if instant, e := time.Parse(time.RFC3339Nano, value); e == nil {
		return instant.Local().Format("2006-01-02 15:04 Z07:00")
	}
	return strconv.Quote(value)
}
func snapshotLabels(tags []string) (string, string) {
	kind := "snapshot"
	for _, tag := range tags {
		if strings.HasPrefix(tag, "veyra:") {
			if strings.HasSuffix(tag, ":pending") {
				return userLabels(tags), "unfinished backup"
			}
			if strings.HasSuffix(tag, ":checkpoint") {
				kind = "checkpoint"
			} else if strings.HasSuffix(tag, ":complete") {
				kind = "backup"
			}
		}
	}
	return userLabels(tags), kind
}

func userLabels(tags []string) string {
	var labels []string
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "veyra:") {
			labels = append(labels, tag)
		}
	}
	return strings.Join(labels, ", ")
}
func abbreviate(value string, limit int) string {
	r := []rune(value)
	if len(r) <= limit {
		return value
	}
	return string(r[:limit]) + "…"
}
