package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lumesky-me/snapArk/internal/backend"
	"github.com/lumesky-me/snapArk/internal/journal"
)

func (a App) rehearsal(ctx context.Context, r backend.Repository, ref, parent string, full bool, sampleFiles int, sampleBytes int64) int {
	fail := func(e error) int { fmt.Fprintln(a.Err, e); return 1 }
	if e := r.Validate(ctx); e != nil {
		return fail(e)
	}
	snapshot, e := r.Resolve(ctx, ref)
	if e != nil {
		return fail(e)
	}
	identity, e := r.Identity(ctx)
	if e != nil {
		return fail(e)
	}
	temporaryParent := parent
	if temporaryParent == "" {
		temporaryParent = os.TempDir()
	}
	actualParent, e := backend.Canonical(temporaryParent)
	if e != nil {
		return fail(e)
	}
	repository, e := backend.Canonical(r.Path)
	if e != nil {
		return fail(e)
	}
	if backend.Within(repository, actualParent) {
		return fail(errors.New("rehearsal temporary parent must be outside the repository"))
	}
	depth := "sample-content"
	if full {
		depth = "full-restore"
	}
	event, e := r.History.Begin(journal.Record{Kind: "recovery-test", Repository: identity, Profile: r.ProfileID, Snapshot: snapshot.ID, Scope: snapshot.ID, Depth: depth, Veyra: Version, Restic: backend.Version})
	if e != nil {
		return fail(e)
	}
	root, e := os.MkdirTemp(actualParent, "snapark-rehearsal-"+event.ID+"-*")
	if e == nil {
		target := filepath.Join(root, "recovered")
		runner := r
		runner.Out = a.Err
		if full {
			e = runner.Restore(ctx, backend.RestoreOptions{Snapshot: snapshot.ID, Target: target})
		} else {
			var files []backend.Node
			files, e = runner.SampleFiles(ctx, snapshot.ID, sampleFiles, sampleBytes)
			if e == nil {
				for i, node := range files {
					if e = runner.RecoverSample(ctx, snapshot.ID, node, filepath.Join(root, fmt.Sprintf("sample-%02d", i))); e != nil {
						break
					}
					event.Files = append(event.Files, node.Path)
				}
			}
		}
	}
	outcome, category := "passed", ""
	if e != nil {
		outcome = "failed"
		category = "recovery"
		if ctx.Err() != nil {
			outcome = "interrupted"
			category = "cancellation"
		}
	}
	if e != nil {
		event.Evidence = root
	}
	recordErr := r.History.Finish(event, outcome, category)
	if e == nil && recordErr == nil {
		if cleanupErr := os.RemoveAll(root); cleanupErr != nil {
			return fail(fmt.Errorf("rehearsal passed; private directory cleanup failed at %s: %w", root, cleanupErr))
		}
	} else {
		e = errors.Join(e, recordErr)
		return fail(fmt.Errorf("rehearsal failed; private recovery evidence retained at %s: %w", root, e))
	}
	report := map[string]any{"tested_files": event.Files, "result": "passed", "snapshot": snapshot.ID, "depth": depth, "verification": "actual file content recovered and verified; a sample does not prove full recoverability or metadata support"}
	if r.JSON {
		return encodeResult(a.Out, report, a.Err)
	}
	fmt.Fprintln(a.Out, "Recovery rehearsal passed for snapshot", snapshot.ID, "mode", depth, "tested files", event.Files)
	return 0
}
