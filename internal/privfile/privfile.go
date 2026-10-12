// Package privfile reads small files that must be private to their owner: a bearer token,
// a stored fingerprint. Nothing else may be able to write them, so a symlink or a loose
// mode is refused.
package privfile

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// Read returns at most limit bytes of path. It refuses anything but a regular file (a
// symlink is not followed) that only its owner can access, and a file swapped between the
// check and the open.
func Read(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("mode %04o; run: chmod 600 %s", info.Mode().Perm(), path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if now, err := f.Stat(); err != nil || !os.SameFile(info, now) {
		return nil, errors.New("changed while being read")
	}
	return io.ReadAll(io.LimitReader(f, limit))
}
