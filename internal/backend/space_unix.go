//go:build linux || darwin

package backend

import "syscall"

func availableSpace(p string) (uint64, error) {
	var stat syscall.Statfs_t
	if e := syscall.Statfs(p, &stat); e != nil {
		return 0, e
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
