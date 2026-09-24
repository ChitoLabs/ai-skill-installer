package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var errInstallerLockContended = errors.New("installer lock is already held")

// installerLock holds the same advisory flock used by install-skills.sh.
type installerLock struct {
	file *os.File
}

// acquireInstallerLock creates the configured lock directory and takes a
// nonblocking exclusive flock without truncating an existing lock file.
func acquireInstallerLock(lockPath string) (*installerLock, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create installer lock directory: %w", err)
	}

	info, err := os.Lstat(lockPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("installer lock path cannot be a symbolic link: %s", lockPath)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("installer lock path is not a regular file: %s", lockPath)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect installer lock path: %w", err)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o666)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("installer lock path became a symbolic link: %s", lockPath)
		}
		return nil, fmt.Errorf("open installer lock: %w", err)
	}
	info, err = file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect opened installer lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("installer lock path is not a regular file: %s", lockPath)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w (lock: %s)", errInstallerLockContended, lockPath)
		}
		return nil, fmt.Errorf("acquire installer lock: %w", err)
	}
	return &installerLock{file: file}, nil
}

// Close releases the advisory lock and closes its file descriptor.
func (lock *installerLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}
