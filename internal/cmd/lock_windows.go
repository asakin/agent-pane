//go:build windows

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock on path, creating it, and returns its
// release. The lock goes with the handle, so a crashed holder frees it.
// It gives up when ctx ends.
//
// As lock_unix.go's flock, the lock is per open file: a second lockFile
// on the same path waits even in the same process.
func lockFile(ctx context.Context, path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	for {
		// The first byte stands for the file. FAIL_IMMEDIATELY makes this a
		// try, so the wait below can honor ctx.
		ol := new(windows.Overlapped)
		err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
		if err == nil {
			return func() {
				// Closing the handle would release the lock too, but
				// only once the system gets to it.
				windows.UnlockFileEx(h, 0, 1, 0, new(windows.Overlapped))
				f.Close()
			}, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
