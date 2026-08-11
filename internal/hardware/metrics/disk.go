package metrics

import "syscall"

// Free is the space left on the filesystem holding dir, in bytes. Not a Reader method: there is no
// file to point at a fixture, and a statfs of a temporary directory is a real answer already.
func Free(dir string) (int64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(dir, &fs); err != nil {
		return 0, err
	}
	return int64(fs.Bavail) * int64(fs.Bsize), nil
}
