package deployment

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockExecutor(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func syncExecutorDir(root *os.Root) error {
	d, e := root.Open(".")
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
