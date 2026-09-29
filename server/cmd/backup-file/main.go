// backup-file seals an operator-selected legacy backup stream using the same
// authenticated format as tenant-backup. It never connects to a service.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/privatefile"
)

func transform(mode, input, output, keyPath string) (err error) {
	if (mode != "encrypt" && mode != "decrypt") || !filepath.IsAbs(output) || !filepath.IsAbs(keyPath) || (input != "-" && !filepath.IsAbs(input)) {
		return backup.ErrInvalid
	}
	info, err := os.Lstat(keyPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != 32 {
		return backup.ErrInvalid
	}
	key, err := os.ReadFile(keyPath)
	if err != nil || len(key) != 32 {
		return backup.ErrInvalid
	}
	defer clear(key)
	var src io.Reader = os.Stdin
	if input != "-" {
		f, e := os.Open(input)
		if e != nil {
			return backup.ErrInvalid
		}
		defer f.Close()
		src = f
	}
	f, err := privatefile.Create(output)
	if err != nil {
		return backup.ErrInvalid
	}
	defer func() {
		if e := f.Close(); err == nil {
			err = e
		}
		if err != nil {
			_ = os.Remove(output)
		}
	}()
	if mode == "encrypt" {
		err = backup.Encrypt(f, src, key)
	} else {
		err = backup.Decrypt(f, src, key)
	}
	if err == nil {
		err = f.Sync()
	}
	return err
}

func main() {
	mode := flag.String("mode", "", "encrypt or decrypt")
	input := flag.String("input", "-", "absolute input path, or - for stdin")
	output := flag.String("output", "", "new absolute private output path; never overwritten")
	key := flag.String("key", "", "absolute path to independent 32-byte key")
	flag.Parse()
	if transform(*mode, *input, *output, *key) != nil {
		fmt.Fprintln(os.Stderr, "Backup file operation failed; no completed output was retained.")
		os.Exit(1)
	}
}
