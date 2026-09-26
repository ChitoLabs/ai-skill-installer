package main

import (
	"io/fs"
	"syscall"
)

// hardlinkIdentity returns the device/inode pair backing info and whether the
// underlying file currently has more than one hard link. copyTree uses it to
// recreate hard links between files copied from the same source tree, the
// same thing cp -a's --preserve=links does; ok is false when the platform
// stat structure is unavailable.
func hardlinkIdentity(info fs.FileInfo) (key [2]uint64, hasMultipleLinks bool, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return key, false, false
	}
	return [2]uint64{uint64(stat.Dev), stat.Ino}, stat.Nlink > 1, true
}
