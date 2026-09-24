package main

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// publishSnapshotNoReplace uses Linux renameat2(RENAME_NOREPLACE): unlike
// rename(2), it fails atomically if another creator has already published the
// destination. Unsupported kernels or architectures fail closed.
func publishSnapshotNoReplace(source, destination string) error {
	sourcePath, err := syscall.BytePtrFromString(source)
	if err != nil {
		return err
	}
	destinationPath, err := syscall.BytePtrFromString(destination)
	if err != nil {
		return err
	}
	const renameNoReplace = 1
	atFDCWD := ^uintptr(99)
	syscallNumber, ok := renameat2SyscallNumber(runtime.GOARCH)
	if !ok {
		return fmt.Errorf("atomic no-replace snapshot publication is unsupported on %s", runtime.GOARCH)
	}
	_, _, errno := syscall.Syscall6(
		syscallNumber,
		atFDCWD, uintptr(unsafe.Pointer(sourcePath)),
		atFDCWD, uintptr(unsafe.Pointer(destinationPath)),
		renameNoReplace, 0,
	)
	runtime.KeepAlive(sourcePath)
	runtime.KeepAlive(destinationPath)
	if errno != 0 {
		return errno
	}
	return nil
}

func renameat2SyscallNumber(architecture string) (uintptr, bool) {
	switch architecture {
	case "amd64":
		return 316, true
	case "386":
		return 353, true
	case "arm":
		return 382, true
	case "arm64", "riscv64", "loong64":
		return 276, true
	case "ppc64", "ppc64le":
		return 357, true
	case "s390x":
		return 347, true
	default:
		return 0, false
	}
}
