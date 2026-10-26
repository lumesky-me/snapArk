package install

import "golang.org/x/sys/windows"

// MoveFileEx without REPLACE_EXISTING publishes the complete same-directory
// staging file without replacing files, directories or links. No hard link or
// cross-volume copy is involved, including on Windows filesystems without links.
func publish(staged, target string) error {
	from, e := windows.UTF16PtrFromString(staged)
	if e != nil {
		return e
	}
	to, e := windows.UTF16PtrFromString(target)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}
