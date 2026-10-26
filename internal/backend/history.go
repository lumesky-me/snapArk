package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/lumesky-me/snapArk/internal/journal"
)

func (r Repository) Identity(ctx context.Context) (string, error) {
	var config struct {
		ID string `json:"id"`
	}
	e := r.Decode(ctx, &config, "cat", "config")
	if e != nil {
		return "", e
	}
	if !snapshotID.MatchString(config.ID) {
		return "", errors.New("repository identity is invalid")
	}
	return config.ID, nil
}
func (r Repository) Scope(ctx context.Context) (string, error) {
	r.SnapshotTag = ""
	r.SnapshotHost = ""
	snapshots, e := r.Snapshots(ctx)
	if e != nil {
		return "", e
	}
	ids := []string{}
	for _, s := range snapshots {
		ids = append(ids, s.ID)
	}
	sort.Strings(ids)
	b, _ := json.Marshal(ids)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func (r Repository) RecordedCheck(ctx context.Context, full bool) error {
	identity, e := r.Identity(ctx)
	if e != nil {
		depth := "metadata"
		if full {
			depth = "full-data"
		}
		event, saveErr := r.History.Begin(journal.Record{Kind: "verification", Profile: r.ProfileID, Depth: depth, Veyra: r.AppVersion, Restic: Version})
		if saveErr != nil {
			return errors.Join(e, saveErr)
		}
		outcome := "failed"
		if ctx.Err() != nil {
			outcome = "interrupted"
		}
		return errors.Join(e, r.History.Finish(event, outcome, "repository_access"))
	}
	scope, e := r.Scope(ctx)
	if e != nil {
		return e
	}
	depth := "metadata"
	if full {
		depth = "full-data"
	}
	event, e := r.History.Begin(journal.Record{Kind: "verification", Repository: identity, Profile: r.ProfileID, Scope: scope, Depth: depth, Veyra: r.AppVersion, Restic: Version})
	if e != nil {
		return e
	}
	args := []string{"check"}
	if full {
		args = append(args, "--read-data")
	}
	e = r.Execute(ctx, args...)
	outcome, category := "passed", ""
	if e != nil {
		outcome = "failed"
		category = "backend"
		if ctx.Err() != nil {
			outcome = "interrupted"
			category = "cancellation"
		}
	}
	// Do not attach a check to a replaced repository or a changed snapshot set.
	if e == nil {
		after, err := r.Identity(ctx)
		if err != nil || after != identity {
			e = errors.New("repository identity changed during verification")
		}
		afterScope, err := r.Scope(ctx)
		if err != nil || afterScope != scope {
			e = errors.New("snapshot scope changed during verification")
		}
		if e != nil {
			outcome = "inconclusive"
			category = "scope_changed"
		}
	}
	if saveErr := r.History.Finish(event, outcome, category); saveErr != nil {
		return errors.Join(e, saveErr)
	}
	return e
}
