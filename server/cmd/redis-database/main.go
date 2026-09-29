// redis-database streams one logical database; credentials are environment-only.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/linli/im/server/internal/privatefile"
	"github.com/linli/im/server/internal/redisbackup"
)

func main() {
	mode := flag.String("mode", "", "export, validate, or import")
	file := flag.String("file", "", "snapshot file for import/validation")
	flag.Parse()
	err := run(*mode, *file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Redis database operation failed; inspect the selected database and private configuration.")
		os.Exit(1)
	}
}
func run(mode, path string) error {
	if mode == "validate" {
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		return redisbackup.Read(f, nil)
	}
	c, e := redisbackup.Open(os.Getenv("REDIS_DATABASE_URL"))
	if e != nil {
		return e
	}
	defer c.Close()
	if mode == "export" {
		if path != "" {
			f, e := privatefile.Create(path)
			if e != nil {
				return e
			}
			defer f.Close()
			return redisbackup.Export(context.Background(), c, f)
		}
		return redisbackup.Export(context.Background(), c, os.Stdout)
	}
	if mode == "import" {
		if path == "-" {
			f, e := os.CreateTemp("", "redis-database-")
			if e != nil {
				return e
			}
			defer os.Remove(f.Name())
			defer f.Close()
			if _, e = io.Copy(f, os.Stdin); e != nil {
				return e
			}
			if _, e = f.Seek(0, io.SeekStart); e != nil {
				return e
			}
			return redisbackup.Import(context.Background(), c, f)
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		return redisbackup.Import(context.Background(), c, f)
	}
	return redisbackup.ErrSnapshot
}
