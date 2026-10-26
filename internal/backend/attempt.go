package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type RestoreAttempt struct {
	Version    int       `json:"version,omitempty"`
	Started    time.Time `json:"started,omitempty"`
	State      string    `json:"state"`
	Repository string    `json:"repository_id,omitempty"`
	Snapshot   string    `json:"snapshot"`
	Target     string    `json:"target"`
	Root       string    `json:"root,omitempty"`
	Include    []string  `json:"include,omitempty"`
}
type RestoreInspection struct {
	Attempt RestoreAttempt `json:"attempt"`
	Output  string         `json:"output"`
	Advice  string         `json:"advice"`
}

// InspectRestore is read-only. A local marker describes an attempt, never proves
// ownership for deletion or successful content verification.
func InspectRestore(target string) (RestoreInspection, error) {
	var report RestoreInspection
	target, e := Canonical(target)
	if e != nil {
		return report, e
	}
	marker := target + ".veyra-incomplete.json"
	info, e := os.Lstat(marker)
	if e != nil {
		return report, e
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return report, errors.New("invalid incomplete-restore marker")
	}
	f, e := os.Open(marker)
	if e != nil {
		return report, e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if e = d.Decode(&report.Attempt); e != nil {
		return report, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return report, errors.New("trailing restore marker data")
	}
	a := report.Attempt
	if a.Version != 0 && a.Version != 1 || a.State != "incomplete" || !snapshotID.MatchString(a.Snapshot) || !filepath.IsAbs(a.Target) || filepath.Clean(a.Target) != target {
		return report, errors.New("unsupported or mismatched restore marker; inspect it manually")
	}
	report.Output = "missing"
	if info, e = os.Lstat(target); e == nil {
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			report.Output = "partial directory present; contents not certified"
		} else {
			report.Output = "target is no longer a regular directory; inspect manually"
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return report, e
	}
	report.Advice = fmt.Sprintf("Completion was not observed. Inspect %q and any active process. Retry into a different new directory; no automatic resume, deletion or lock removal is available.", target)
	return report, nil
}
