package backend

import "context"

type Plan struct {
	Repository      string   `json:"repository"`
	Sources         []string `json:"sources"`
	ResolvedSources []string `json:"resolved_sources"`
	Include         []string `json:"include_paths,omitempty"`
	Exclude         []string `json:"exclusions,omitempty"`
	Accessible      bool     `json:"repository_access_confirmed"`
	EstimatedFiles  uint64   `json:"estimated_files"`
	EstimatedBytes  uint64   `json:"estimated_source_bytes"`
	Note            string   `json:"note"`
}

func (r Repository) Plan(ctx context.Context, o BackupOptions) (Plan, error) {
	result := Plan{Repository: r.Path, Sources: o.Sources, Include: o.Include, Exclude: o.Exclude, Note: "Counts are a point-in-time dry-run estimate. Deduplicated storage growth and full repository health are not determined."}
	for _, source := range o.Sources {
		resolved, e := Canonical(source)
		if e != nil {
			return result, e
		}
		result.ResolvedSources = append(result.ResolvedSources, resolved)
	}
	r.ReadOnly = true
	if e := r.Validate(ctx); e != nil {
		return result, e
	}
	if _, e := r.Snapshots(ctx); e != nil {
		return result, e
	}
	result.Accessible = true
	writer := &summaryWriter{}
	r.JSON = true
	r.Quiet = false
	r.Out = writer
	o.DryRun = true
	if e := r.Backup(ctx, o); e != nil {
		return result, e
	}
	result.EstimatedFiles = writer.Files
	result.EstimatedBytes = writer.Bytes
	return result, nil
}
