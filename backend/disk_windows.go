package main

import (
	"syscall"
	"unsafe"
)

func freeDisk(path string) (uint64, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	r, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW").Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&available)), 0, 0)
	if r == 0 {
		return 0, err
	}
	return available, nil
}
