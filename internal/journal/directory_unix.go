//go:build !windows

package journal

import "os"

func privateDirectory(path string) error { return os.Mkdir(path, 0700) }
