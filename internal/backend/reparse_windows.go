package backend

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Check every named existing component without following the final reparse point.
// Known directory aliases are resolved by Canonical's subsequent handle query.
// Other tags (including cloud placeholders) require manual filesystem preparation.
func checkPathComponents(path string) error {
	volume := filepath.VolumeName(path)
	current := volume + `\`
	for _, part := range strings.Split(strings.TrimLeft(path[len(volume):], `\`), `\`) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		if e := checkReparse(current); e != nil {
			if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
				return nil
			}
			return e
		}
	}
	return nil
}
func checkReparse(path string) error {
	name, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	h, e := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if e = windows.GetFileInformationByHandle(h, &info); e != nil {
		return e
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return nil
	}
	var tag struct{ Attributes, Tag uint32 }
	if e = windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&tag)), uint32(unsafe.Sizeof(tag))); e != nil {
		return e
	}
	if tag.Tag != windows.IO_REPARSE_TAG_SYMLINK && tag.Tag != windows.IO_REPARSE_TAG_MOUNT_POINT {
		return fmt.Errorf("unsupported Windows reparse tag 0x%x at %q; materialize or move the data before retrying", tag.Tag, path)
	}
	return nil
}
func checkSourceTree(ctx context.Context, root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		data, ok := info.Sys().(*syscall.Win32FileAttributeData)
		if !ok {
			return errors.New("Windows file attributes unavailable")
		}
		if data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			if e = checkReparse(p); e != nil {
				return e
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
}
