package main

import (
	"os"
	"syscall"
)

func execute(command string, args []string) error {
	return syscall.Exec(command, append([]string{command}, args...), os.Environ())
}
