package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/lumesky-me/veyra/internal/backend"
	"github.com/lumesky-me/veyra/internal/config"
	"github.com/lumesky-me/veyra/internal/journal"
)

func historyStore(path string) (journal.Store, error) {
	store, e := configStore(path)
	return journal.Store{Dir: store.Path + ".history"}, e
}
func attachHistory(r *backend.Repository, path string, p *config.Profile) error {
	store, e := historyStore(path)
	if e != nil {
		return e
	}
	root, e := backend.Canonical(store.Dir)
	if e != nil {
		return e
	}
	repo, e := backend.Canonical(r.Path)
	if e != nil {
		return e
	}
	if backend.Within(repo, root) {
		return errors.New("history must be outside the repository; choose another --config")
	}
	if p != nil {
		r.ProfileID = p.ID
		for _, source := range p.Sources {
			physical, e := backend.Canonical(source)
			if e != nil {
				return e
			}
			if backend.Within(physical, root) {
				return errors.New("history must be outside backup sources")
			}
		}
	}
	r.History = &store
	r.AppVersion = Version
	return nil
}
func (a App) history(path string, p *config.Profile, structured bool) int {
	store, e := historyStore(path)
	if e != nil {
		fmt.Fprintln(a.Err, e)
		return 1
	}
	records, e := store.Read()
	if e != nil {
		fmt.Fprintln(a.Err, e)
		return 1
	}
	selected := []journal.Record{}
	for _, event := range records {
		if p == nil || event.Profile == p.ID {
			selected = append(selected, event)
		}
	}
	if structured {
		return encodeResult(a.Out, selected, a.Err)
	}
	for _, event := range selected {
		fmt.Fprintf(a.Out, "%s %s %s %s repo=%s profile=%s\n", event.Started.Format("2006-01-02T15:04:05Z07:00"), event.Kind, event.Depth, event.Outcome, event.Repository, event.Profile)
	}
	return 0
}
func encodeResult(out io.Writer, v any, stderr io.Writer) int {
	if e := json.NewEncoder(out).Encode(v); e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	return 0
}
func (a App) scheduled(ctx context.Context, r backend.Repository, p *config.Profile, operation func() (string, error)) (string, error) {
	if r.History == nil {
		return "", errors.New("history is required for scheduled execution")
	}
	if p == nil || r.PasswordFile == "" {
		return "", errors.New("scheduled backup requires a profile and an absolute protected --password-file")
	}
	if !filepath.IsAbs(r.PasswordFile) {
		return "", errors.New("scheduled password-file path must be absolute")
	}
	event, e := r.History.Begin(journal.Record{Kind: "scheduled-run", Profile: p.ID, Depth: p.Verify, Veyra: Version, Restic: backend.Version})
	if e != nil {
		return "", e
	}
	var id string
	release, e := backend.ProtectPassword(r.PasswordFile)
	if e == nil {
		defer release()
		id, e = operation()
		if identity, err := r.Identity(ctx); err == nil {
			event.Repository = identity
		}
	}
	event.Snapshot = id
	outcome, category := "passed", ""
	if e != nil {
		outcome = "failed"
		category = "backup"
		if ctx.Err() != nil {
			outcome = "interrupted"
			category = "cancellation"
		}
	}
	return id, errors.Join(e, r.History.Finish(event, outcome, category))
}
func (r *overview) withHistory(ctx context.Context, repo backend.Repository, path string) {
	store, e := historyStore(path)
	if e != nil {
		r.HistoryState = e.Error()
		return
	}
	records, e := store.Read()
	if e != nil {
		r.HistoryState = "unreadable: " + e.Error()
		return
	}
	if len(records) >= journal.Limit {
		r.HistoryWarning = "history capacity reached; archive reviewed records before the next operation"
	}
	identity, e := repo.Identity(ctx)
	if e != nil {
		r.HistoryState = "repository identity unavailable"
		return
	}
	scope, e := repo.Scope(ctx)
	if e != nil {
		r.HistoryState = "repository scope unavailable"
		return
	}
	r.HistoryState = "no verification history for this repository"
	r.Verification = "unknown"
	for _, event := range records {
		if event.Kind == "scheduled-run" && event.Profile == repo.ProfileID && (event.Repository == identity || event.Repository == "") {
			copy := event
			r.LastRun = &copy
			if event.Outcome == "passed" {
				r.ScheduledFailures = 0
			} else if event.Outcome == "failed" || event.Outcome == "interrupted" {
				r.ScheduledFailures++
			}
		}
		if event.Kind == "recovery-test" && event.Repository == identity {
			copy := event
			r.LastRecovery = &copy
		}
		if event.Repository != identity || event.Kind != "verification" {
			continue
		}
		copy := event
		if event.Outcome == "passed" {
			if laterResult(event, r.LastCheck) {
				r.LastCheck = &copy
			}
			if event.Depth == "full-data" && laterResult(event, r.LastFull) {
				r.LastFull = &copy
			}

		}
		if event.Outcome == "failed" || event.Outcome == "interrupted" || event.Outcome == "inconclusive" {
			if laterResult(event, r.LastFailure) {
				r.LastFailure = &copy
			}
		}
	}
	if r.LastCheck != nil {
		r.HistoryState = "historical verification; current snapshot scope differs"
		if r.LastCheck.Scope == scope {
			r.HistoryState = "verification matches observed snapshot scope; not a current health guarantee"
			r.Verification = r.LastCheck.Depth + " passed at " + r.LastCheck.Finished.Format("2006-01-02T15:04:05Z07:00")
		}
	}
}
func laterResult(candidate journal.Record, current *journal.Record) bool {
	return candidate.Finished != nil && (current == nil || current.Finished == nil || candidate.Finished.After(*current.Finished))
}

func recordedBackup(ctx context.Context, r backend.Repository, operation func() (string, error)) (string, error) {
	release, e := r.History.Acquire(r.ProfileID)
	if e != nil {
		return "", e
	}
	defer release()
	event, e := r.History.Begin(journal.Record{Kind: "backup", Profile: r.ProfileID, Depth: "completion", Veyra: Version, Restic: backend.Version})
	if e != nil {
		return "", e
	}
	id, e := operation()
	event.Snapshot = id
	if identity, err := r.Identity(ctx); err == nil {
		event.Repository = identity
	}
	if scope, err := r.Scope(ctx); err == nil {
		event.Scope = scope
	}
	outcome, category := "passed", ""
	if e != nil {
		outcome = "failed"
		category = "backup"
		if ctx.Err() != nil {
			outcome = "interrupted"
			category = "cancellation"
		}
	}
	return id, errors.Join(e, r.History.Finish(event, outcome, category))
}
