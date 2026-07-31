package update

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const blkroset = 0x125d

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

func remount(rw bool) error {
	if rw {
		if err := allowWrites(); err != nil {
			return err
		}
	}

	flags := uintptr(syscall.MS_REMOUNT)
	if !rw {
		flags |= syscall.MS_RDONLY
	}
	return syscall.Mount("", mount, "", flags, "")
}

// allowWrites clears the block device's read-only flag, which the kernel checks before any ro->rw
// remount and answers with EACCES. Some devices carry it on /system; `mount` clears it and a bare
// mount(2) does not. It does not survive a reboot.
func allowWrites() error {
	dev, err := backing(mount)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(dev, os.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("update: opening %s: %w", dev, err)
	}
	defer f.Close()

	off := int32(0)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), blkroset, uintptr(unsafe.Pointer(&off))); errno != 0 {
		return fmt.Errorf("update: clearing the read-only flag on %s: %w", dev, errno)
	}
	return nil
}

func backing(target string) (string, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return "", err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		if fields := strings.Fields(s.Text()); len(fields) >= 2 && fields[1] == target {
			return fields[0], nil
		}
	}
	if err := s.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("update: %s is not mounted", target)
}
