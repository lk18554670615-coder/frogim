package deployment

import (
	"golang.org/x/sys/windows"
	"os"
)

func lockExecutor(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
}

// Windows is used by local tests, not the production agent executable. File
// Sync and atomic replacement are exercised; directory fsync is Linux-only.
func syncExecutorDir(root *os.Root) error { return nil }
