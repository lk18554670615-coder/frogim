//go:build !linux

package main

import "errors"

func execute(string, []string) error { return errors.New("Linux container runtime required") }
