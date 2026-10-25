package backend

import (
	"errors"
	"os"
	"path/filepath"
)

// AvailableSpace observes free bytes for the filesystem containing a destination,
// including a not-yet-created repository. It is not a backup-size prediction.
func AvailableSpace(destination string) (uint64, error) {
	p, e := Canonical(destination)
	if e != nil {
		return 0, e
	}
	for {
		info, e := os.Stat(p)
		if e == nil {
			if !info.IsDir() {
				p = filepath.Dir(p)
			}
			return availableSpace(p)
		}
		if !errors.Is(e, os.ErrNotExist) {
			return 0, e
		}
		parent := filepath.Dir(p)
		if parent == p {
			return 0, e
		}
		p = parent
	}
}
