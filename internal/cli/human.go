package cli

import (
	"fmt"
	"io"
	"sort"

	"github.com/lumesky-me/veyra/internal/backend"
)

func humanWorkflow(out io.Writer, value any) error {
	switch v := value.(type) {
	case []overview:
		if len(v) == 0 {
			_, e := fmt.Fprintln(out, "No backup profiles. Run veyra setup or profile add.")
			return e
		}
		for _, row := range v {
			fmt.Fprintf(out, "%s: %s\n", row.Profile, row.State)
			fmt.Fprintf(out, "  Repository: %q\n", row.Repository)
			if row.Latest != "" {
				fmt.Fprintf(out, "  Latest: %s at %s\n", abbreviate(row.Latest, 12), snapshotDate(row.Time))
			}
			fmt.Fprintf(out, "  Verification: %s\n", row.Verification)
			if row.HistoryState != "" {
				fmt.Fprintf(out, "  History: %s\n", row.HistoryState)
			}
			if row.Error != "" {
				fmt.Fprintf(out, "  Error: %s\n", row.Error)
			}
			if row.HistoryWarning != "" {
				fmt.Fprintf(out, "  Warning: %s\n", row.HistoryWarning)
			}
			if row.LastBackup != nil && row.LastBackup.Outcome != "passed" {
				fmt.Fprintf(out, "  Backup attempt: %s (%s); inspect veyra history and check for an active process before retrying.\n", row.LastBackup.ID, row.LastBackup.Outcome)
			}
		}
	case backend.Plan:
		fmt.Fprintf(out, "Repository: %q\n", v.Repository)
		for _, source := range v.ResolvedSources {
			fmt.Fprintf(out, "Source: %q\n", source)
		}
		if len(v.Include) > 0 {
			fmt.Fprintf(out, "Includes: %q\n", v.Include)
		}
		if len(v.Exclude) > 0 {
			fmt.Fprintf(out, "Exclusions: %q\n", v.Exclude)
		}
		if v.Accessible {
			fmt.Fprintf(out, "Preview: %d files, %d source bytes before deduplication.\n", v.EstimatedFiles, v.EstimatedBytes)
		} else {
			fmt.Fprintln(out, "Repository access was not confirmed.")
		}
		fmt.Fprintln(out, v.Note)
	case []finding:
		for _, f := range v {
			fmt.Fprintf(out, "%s: %s — %s\n", f.Check, f.State, f.Detail)
		}
	case map[string]string:
		keys := make([]string, 0, len(v))
		for key := range v {
			if key != "notice" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		if notice := v["notice"]; notice != "" {
			fmt.Fprintln(out, notice)
		}
		for _, key := range keys {
			fmt.Fprintf(out, "\n%s\n%s\n", key, v[key])
		}
	default:
		return fmt.Errorf("unsupported workflow result %T", value)
	}
	return nil
}
