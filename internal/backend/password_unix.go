//go:build !windows

package backend

import (
	"errors"
	"os"
)

// ProtectPassword validates the unattended credential. Unix permissions remain
// the authority; callers must keep this user-owned file outside backup sources.
func ProtectPassword(path string) (func(), error) {
	f, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !f.Mode().IsRegular() || f.Size() == 0 {
		return nil, errors.New("password must be a nonempty regular file, not a symbolic link")
	}
	if f.Mode().Perm()&0077 != 0 {
		return nil, errors.New("password file must be private (chmod 600)")
	}
	return func() {}, nil
}
