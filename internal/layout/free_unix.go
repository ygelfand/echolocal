//go:build !windows

package layout

import "syscall"

// Free is the space left on the filesystem holding path, in bytes.
func Free(path string) (int64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, err
	}
	return int64(fs.Bavail) * int64(fs.Bsize), nil
}
