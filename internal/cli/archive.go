package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/lumesky-me/veyra/internal/backend"
)

func (a App) archiveHistory(ctx context.Context, args []string) int {
	fail := func(e error) int { fmt.Fprintln(a.Err, e); return 1 }
	fs := flag.NewFlagSet("history archive", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	var cfg, destination, cutoff string
	var confirm, failures, structured bool
	fs.StringVar(&cfg, "config", "", "configuration whose history to maintain")
	fs.StringVar(&destination, "to", "", "new archive directory (existing parent, outside sources and repositories)")
	fs.StringVar(&cutoff, "before", "", "archive completed results older than this RFC3339 timestamp")
	fs.BoolVar(&failures, "include-failures", false, "also archive reviewed finalized failures, interruptions and inconclusive results; started records always stay")
	fs.BoolVar(&confirm, "confirm", false, "commit the archive and remove verified copies from active history")
	fs.BoolVar(&structured, "json", false, "JSON report")
	if e := fs.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || cutoff == "" || destination == "" {
		return fail(errors.New("use history archive --before RFC3339 --to NEW_DIRECTORY [--confirm]"))
	}
	before, e := time.Parse(time.RFC3339, cutoff)
	if e != nil {
		return fail(e)
	}
	store, e := historyStore(cfg)
	if e != nil {
		return fail(e)
	}
	records, e := store.Read()
	if e != nil {
		return fail(e)
	}
	ids := []string{}
	for _, r := range records {
		if r.Finished != nil && r.Finished.Before(before) && (r.Outcome == "passed" || failures) {
			ids = append(ids, r.ID)
		}
	}
	if !confirm {
		if structured {
			return encodeResult(a.Out, map[string]any{"mode": "preview", "records": ids, "destination": destination, "note": "started records stay; archive is local evidence, not cryptographic proof"}, a.Err)
		}
		fmt.Fprintf(a.Out, "Would archive %d finalized records to %q. Started records stay. Repeat with --confirm after review.\n", len(ids), destination)
		return 0
	}
	if e = ctx.Err(); e != nil {
		return fail(e)
	}
	configuration, e := configStore(cfg)
	if e != nil {
		return fail(e)
	}
	f, e := configuration.Read()
	if e != nil {
		return fail(e)
	}
	dest, e := backend.Canonical(destination)
	if e != nil {
		return fail(e)
	}
	for _, p := range f.Profiles {
		for _, root := range append(append([]string{}, p.Sources...), p.Repository) {
			actual, e := backend.Canonical(root)
			if e != nil {
				return fail(e)
			}
			if backend.Within(actual, dest) {
				return fail(errors.New("history archive must be outside configured sources and repositories"))
			}
		}
	}
	count, e := store.Archive(dest, before, failures)
	if e != nil {
		return fail(e)
	}
	if structured {
		return encodeResult(a.Out, map[string]any{"result": "archived", "records": count, "destination": dest}, a.Err)
	}
	fmt.Fprintf(a.Out, "Archived %d records to %q. Inspect with veyra history --archive %q.\n", count, dest, dest)
	return 0
}
