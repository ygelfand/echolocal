package update

import (
	"fmt"
	"syscall"
)

// room refuses an install that would not fit. Both copies exist at once, and filling /system is a
// worse outcome than not updating.
func room(need int64) error {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(mount, &fs); err != nil {
		return nil
	}

	free := int64(fs.Bavail) * int64(fs.Bsize)
	if free < need {
		return fmt.Errorf("update: %d bytes free on %s, need %d", free, mount, need)
	}
	return nil
}

// remount makes the filesystem echod lives on writable, or read-only again. Everything here is under
// /system, which is mounted read-only for the good reason that nothing should be writing to it —
// including this, for as short a time as it can manage.
func remount(rw bool) error {
	flags := uintptr(syscall.MS_REMOUNT)
	if !rw {
		flags |= syscall.MS_RDONLY
	}
	return syscall.Mount("", mount, "", flags, "")
}
