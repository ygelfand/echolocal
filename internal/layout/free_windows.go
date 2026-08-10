package layout

import "golang.org/x/sys/windows"

// Free is the space left on the filesystem holding path, in bytes.
func Free(path string) (int64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	var available uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &available, nil, nil); err != nil {
		return 0, err
	}
	return int64(available), nil
}
