//go:build !windows

package install

import "os"

// A hard link is an atomic, no-replace publication on supported Unix filesystems.
func publish(staged, target string) error { return os.Link(staged, target) }
