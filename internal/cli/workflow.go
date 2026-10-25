package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/lumesky-me/veyra/internal/backend"
	"github.com/lumesky-me/veyra/internal/config"
)

type finding struct {
	Check  string `json:"check"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}
type overview struct {
	Profile      string   `json:"profile"`
	Repository   string   `json:"repository"`
	Sources      []string `json:"sources"`
	State        string   `json:"state"`
	Latest       string   `json:"latest,omitempty"`
	Time         string   `json:"time,omitempty"`
	AgeHours     float64  `json:"age_hours,omitempty"`
	Verification string   `json:"verification"`
	Error        string   `json:"error,omitempty"`
}

func scoped(s backend.Snapshot, p config.Profile, host string) bool {
	if s.Hostname != host {
		return false
	}
	for _, t := range s.Tags {
		if t == p.Tag()+":complete" {
			return true
		}
	}
	return false
}
func (a App) workflow(ctx context.Context, cmd string, r backend.Repository, p *config.Profile, path string, full, confirm bool, args []string) int {
	emit := func(v any) int {
		if r.Quiet {
			return 0
		}
		if r.JSON {
			if e := json.NewEncoder(a.Out).Encode(v); e != nil {
				fmt.Fprintln(a.Err, e)
				return 1
			}
		} else {
			b, e := json.MarshalIndent(v, "", "  ")
			if e != nil {
				return 1
			}
			fmt.Fprintln(a.Out, string(b))
		}
		return 0
	}
	fail := func(e error) int {
		if r.JSON {
			_ = json.NewEncoder(a.Err).Encode(map[string]string{"error": e.Error()})
		} else {
			fmt.Fprintln(a.Err, e)
		}
		return 1
	}
	if len(args) > 0 && cmd != "plan" {
		return fail(errors.New("this command accepts options only"))
	}
	if cmd == "schedule" {
		if p == nil {
			return fail(errors.New("schedule requires --profile"))
		}
		if p.Disabled {
			return fail(errors.New("profile is disabled"))
		}
		text, e := schedule(*p, path, r.PasswordFile)
		if e != nil {
			return fail(e)
		}
		return emit(text)
	}
	if cmd == "retention" {
		if p == nil {
			return fail(errors.New("retention requires --profile"))
		}
		if p.Retention.Empty() {
			return fail(errors.New("profile has no retention policy; no snapshots were changed"))
		}
		if e := r.Validate(ctx); e != nil {
			return fail(e)
		}
		host, e := os.Hostname()
		if e != nil {
			return fail(e)
		}
		opts := []string{"forget", "--tag", p.Tag() + ":complete", "--host", host, "--group-by", "host,paths"}
		if !confirm {
			opts = append(opts, "--dry-run")
		}
		for _, v := range []struct {
			flag string
			n    int
		}{{"--keep-last", p.Retention.Last}, {"--keep-daily", p.Retention.Daily}, {"--keep-weekly", p.Retention.Weekly}, {"--keep-monthly", p.Retention.Monthly}, {"--keep-yearly", p.Retention.Yearly}} {
			if v.n > 0 {
				opts = append(opts, v.flag, strconv.Itoa(v.n))
			}
		}
		if e = r.Execute(ctx, opts...); e != nil {
			return fail(fmt.Errorf("retention incomplete; inspect snapshot history before retry; pruning was not run: %w", e))
		}
		return 0
	}
	if cmd == "status" {
		profiles := []config.Profile{}
		if p != nil {
			profiles = append(profiles, *p)
		} else {
			store, e := configStore(path)
			if e != nil {
				return fail(e)
			}
			f, e := store.Read()
			if e != nil {
				return fail(e)
			}
			for _, v := range f.Profiles {
				profiles = append(profiles, v)
			}
		}
		host, e := os.Hostname()
		if e != nil {
			return fail(e)
		}
		sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
		result := []overview{}
		code := 0
		for _, v := range profiles {
			var latest, pending time.Time
			row := overview{Profile: v.Name, Repository: v.Repository, Sources: v.Sources, State: "unknown", Verification: "not checked; snapshot recency is not an integrity check"}
			repo := r
			repo.Path = v.Repository
			repo.SnapshotTag = ""
			repo.SnapshotHost = host
			repo.ReadOnly = true
			repo.Out = a.Err
			if e := repo.Validate(ctx); e != nil {
				row.State = "unavailable"
				row.Error = e.Error()
				code = 1
			} else if snaps, e := repo.Snapshots(ctx); e != nil {
				row.State = "unavailable"
				row.Error = e.Error()
				code = 1
			} else {
				for _, s := range snaps {
					for _, tag := range s.Tags {
						if tag == v.Tag()+":complete:pending" {
							if instant, e := time.Parse(time.RFC3339Nano, s.Time); e == nil && instant.After(pending) {
								pending = instant
							}
						}
					}
					if !scoped(s, v, host) || !profileRootsMatch(s, v) {
						continue
					}
					instant, e := time.Parse(time.RFC3339Nano, s.Time)
					if e != nil {
						continue
					}
					if latest.IsZero() || instant.After(latest) {
						latest = instant
						row.Latest = s.ID
						row.Time = s.Time
					}
				}
				if latest.IsZero() {
					row.State = "no matching snapshot"
					code = 1
				} else {
					row.State = "working"
					row.AgeHours = time.Since(latest).Hours()
					if v.ExpectedHours > 0 && row.AgeHours > float64(v.ExpectedHours) {
						row.State = "warning: overdue"
						code = 1
					}
					if row.AgeHours < 0 {
						row.State = "warning: snapshot timestamp is in the future"
						code = 1
					}
				}
			}
			if !pending.IsZero() && !pending.Before(latest) {
				row.State = "warning: unfinished or in-progress backup"
				code = 1
			}
			if v.Disabled {
				row.State += " (profile disabled)"
			}
			result = append(result, row)
		}
		if e := emit(result); e != 0 {
			return e
		}
		return code
	}
	if cmd == "plan" {
		options := backend.BackupOptions{Sources: args}
		if p != nil {
			if len(args) > 0 {
				return fail(errors.New("profile sources cannot be overridden"))
			}
			if p.Disabled {
				return fail(errors.New("profile is disabled"))
			}
			options.Sources = p.Sources
			options.Include = p.Include
			options.Exclude = p.Exclude
		}
		r.Out = a.Err
		result, e := r.Plan(ctx, options)
		if e != nil {
			_ = emit(result)
			return fail(e)
		}
		return emit(result)
	}
	// Doctor reports what was actually observed; it does not infer disk capacity,
	// successful verification history, or lock health from repository existence.
	findings := []finding{{Check: "platform", State: "working", Detail: Version + " " + runtime.GOOS + "/" + runtime.GOARCH}, {Check: "locks", State: "unknown", Detail: "read-only probes do not establish whether another writer is active"}}
	r.ReadOnly = true
	r.Out = a.Err
	code := 0
	if r.Path != "" {
		bytes, e := backend.AvailableSpace(r.Path)
		if e != nil {
			findings = append(findings, finding{"free disk space", "unknown", e.Error()})
		} else {
			state := "working"
			if bytes == 0 {
				state = "warning"
				code = 1
			}
			findings = append(findings, finding{"free disk space", state, fmt.Sprintf("%d bytes available when checked; future backup growth is not determined", bytes)})
		}
	}

	if e := r.Validate(ctx); e != nil {
		findings = append(findings, finding{"backend/configuration", "failed", e.Error()})
		code = 1
	} else {
		findings = append(findings, finding{"backend", "working", "restic " + backend.Version})
		if _, e := r.Snapshots(ctx); e != nil {
			findings = append(findings, finding{"repository/credentials", "failed", e.Error()})
			code = 1
		} else {
			findings = append(findings, finding{"repository/credentials", "working", "repository metadata was opened successfully; this is not a full integrity check"})
		}
		if full {
			if e := r.Check(ctx, true); e != nil {
				findings = append(findings, finding{"full integrity", "failed", e.Error()})
				code = 1
			} else {
				findings = append(findings, finding{"full integrity", "working", "all data authenticated in this run"})
			}
		} else {
			findings = append(findings, finding{"full integrity", "not checked", "use doctor --full or verify --full"})
		}
	}
	if p != nil {
		store, e := configStore(path)
		if e == nil {
			e = profileLocation(store, *p)
		}
		if e != nil {
			findings = append(findings, finding{"path boundaries", "failed", e.Error()})
			code = 1
		}
		for _, source := range p.Sources {
			file, e := os.Open(source)
			if e == nil {
				_, e = file.Readdirnames(1)
				_ = file.Close()
				if errors.Is(e, io.EOF) {
					e = nil
				}
			}
			state, detail := "working", "directory opened for reading (individual files not fully scanned)"
			if e != nil {
				state = "failed"
				detail = e.Error()
				code = 1
			}
			findings = append(findings, finding{"source " + source, state, detail})
		}
	} else {
		findings = append(findings, finding{"profile", "not configured", "select --profile to inspect configured sources"})
	}
	if e := emit(findings); e != 0 {
		return e
	}
	return code
}

// A profile may use a directory alias that has since been redirected. Snapshot
// metadata must refer to the current physical source roots, not just its tag.
func profileRootsMatch(snapshot backend.Snapshot, p config.Profile) bool {
	roots := []string{}
	for _, source := range p.Sources {
		root, e := backend.Canonical(source)
		if e != nil {
			return false
		}
		roots = append(roots, root)
	}
	if len(snapshot.Paths) == 0 {
		return false
	}
	for _, source := range snapshot.Paths {
		path, e := filepath.Abs(source)
		if e != nil {
			return false
		}
		matched := false
		for _, root := range roots {
			if backend.Within(root, path) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
