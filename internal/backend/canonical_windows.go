package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// EvalSymlinks does not resolve every Windows junction on supported Go versions.
// Opening the object and querying its final name follows actual reparse targets.
func canonicalExisting(path string) (string, error) {
	file, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer file.Close()
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")
	buffer := make([]uint16, 256)
	for {
		n, _, callErr := proc.Call(file.Fd(), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
		runtime.KeepAlive(file)
		runtime.KeepAlive(buffer)
		if n == 0 {
			return "", fmt.Errorf("resolve final Windows path: %w", callErr)
		}
		if n >= uintptr(len(buffer)) {
			if n > 32768 {
				return "", fmt.Errorf("resolved Windows path exceeds supported length")
			}
			buffer = make([]uint16, int(n)+1)
			continue
		}
		final := syscall.UTF16ToString(buffer[:int(n)])
		if strings.HasPrefix(final, `\\?\UNC\`) {
			return `\\` + strings.TrimPrefix(final, `\\?\UNC\`), nil
		}
		normalized := strings.TrimPrefix(final, `\\?\`)
		if !filepath.IsAbs(normalized) || strings.HasPrefix(final, `\\?\Volume{`) {
			return "", fmt.Errorf("Windows volume has no supported DOS/UNC path; assign a drive letter before using it")
		}
		return normalized, nil
	}
}
