package backend

import (
	"runtime"
	"syscall"
	"unsafe"
)

func availableSpace(p string) (uint64, error) {
	name, e := syscall.UTF16PtrFromString(p)
	if e != nil {
		return 0, e
	}
	var available uint64
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	ok, _, err := proc.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&available)), 0, 0)
	runtime.KeepAlive(name)
	if ok == 0 {
		return 0, err
	}
	return available, nil
}
