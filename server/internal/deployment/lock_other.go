//go:build !linux && !windows

package deployment

import "os"

func lockExecutor(*os.File) error    { return ErrState }
func syncExecutorDir(*os.Root) error { return ErrState }
