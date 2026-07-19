//go:build darwin

package indexer

import "golang.org/x/sys/unix"

func filesystemReadOnly(path string) (bool, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return false, err
	}
	return stat.Flags&unix.MNT_RDONLY != 0, nil
}
