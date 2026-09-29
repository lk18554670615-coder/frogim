// Offline platform-directory backup and QUARANTINED restore. This tool never
// enables recovered auth sessions, deployment jobs or tenant credentials.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/linli/im/server/internal/directorybackup"
	"github.com/linli/im/server/internal/privatefile"
)

type configuration struct {
	DatabaseURL      string `json:"databaseUrl"`
	DumpBinary       string `json:"dumpBinary"`
	RestoreBinary    string `json:"restoreBinary"`
	ArchiveDirectory string `json:"archiveDirectory"`
	ComposeFile      string `json:"composeFile"`
	ReleaseFile      string `json:"releaseFile"`
	directorybackup.Request
}

func readRegular(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, directorybackup.ErrInvalid
	}
	i, e := os.Lstat(path)
	if e != nil || !i.Mode().IsRegular() || i.Size() > limit {
		return nil, directorybackup.ErrInvalid
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, directorybackup.ErrInvalid
	}
	defer f.Close()
	a, e := f.Stat()
	if e != nil || !os.SameFile(i, a) {
		return nil, directorybackup.ErrInvalid
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, directorybackup.ErrInvalid
	}
	return b, nil
}
func externalKey(archive, key string) bool {
	if !filepath.IsAbs(archive) || !filepath.IsAbs(key) {
		return false
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(archive))
	if e != nil {
		return false
	}
	archive = filepath.Join(parent, filepath.Base(archive))
	if i, e := os.Lstat(archive); e == nil && (!i.IsDir() || i.Mode()&os.ModeSymlink != 0) {
		return false
	}
	parent, e = filepath.EvalSymlinks(filepath.Dir(key))
	if e != nil {
		return false
	}
	key = filepath.Join(parent, filepath.Base(key))
	if !strings.EqualFold(filepath.VolumeName(archive), filepath.VolumeName(key)) {
		return true
	}
	rel, e := filepath.Rel(archive, key)
	return e == nil && (rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
func execute(ctx context.Context, mode, configPath, keyPath string, confirmed bool) (string, error) {
	if mode == "create-key" && confirmed && configPath == "" && filepath.IsAbs(keyPath) {
		var key [32]byte
		if _, e := rand.Read(key[:]); e != nil {
			return "", directorybackup.ErrInvalid
		}
		defer clear(key[:])
		f, e := privatefile.Create(keyPath)
		if e != nil {
			return "", directorybackup.ErrInvalid
		}
		defer f.Close()
		if n, e := f.Write(key[:]); e != nil || n != 32 || f.Sync() != nil {
			return "", directorybackup.ErrUnconfirmed
		}
		return "Independent platform backup key created; store it separately from archives.", nil
	}
	if mode != "inspect" && mode != "verify" && mode != "backup" && mode != "stage-restore" {
		return "", directorybackup.ErrInvalid
	}
	if (mode == "backup" || mode == "stage-restore") && !confirmed {
		return "", directorybackup.ErrInvalid
	}
	raw, e := readRegular(configPath, 1<<20)
	if e != nil {
		return "", e
	}
	defer clear(raw)
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var c configuration
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return "", directorybackup.ErrInvalid
	}
	db := directorybackup.Database{DSN: c.DatabaseURL, Tools: directorybackup.Tools{Dump: c.DumpBinary, Restore: c.RestoreBinary}}
	if mode == "inspect" {
		if keyPath != "" {
			return "", directorybackup.ErrInvalid
		}
		identity, e := db.Inspect(ctx)
		if e != nil {
			return "", e
		}
		result, _ := json.Marshal(identity)
		return string(result), nil
	}
	if !externalKey(c.ArchiveDirectory, keyPath) {
		return "", directorybackup.ErrInvalid
	}
	key, e := readRegular(keyPath, 32)
	if e != nil || len(key) != 32 {
		return "", directorybackup.ErrInvalid
	}
	defer clear(key)
	if mode == "verify" {
		proof, e := directorybackup.Verify(c.ArchiveDirectory, c.Request, key)
		if e != nil {
			return "", e
		}
		result, _ := json.Marshal(proof)
		return string(result), nil
	}
	if mode == "stage-restore" {
		if e := db.Stage(ctx, c.Request, c.ArchiveDirectory, key); e != nil {
			return "", e
		}
		return "Platform database restored and credentials revoked. QUARANTINED: authentication and workers remain disabled pending enterprise reconciliation. No original database was overwritten.", nil
	}
	compose, e := readRegular(c.ComposeFile, 1<<20)
	if e != nil {
		return "", e
	}
	defer clear(compose)
	release, e := readRegular(c.ReleaseFile, 1<<16)
	if e != nil {
		return "", e
	}
	proof, e := db.Capture(ctx, c.Request, directorybackup.Bundle{Compose: compose, Release: release}, c.ArchiveDirectory, key)
	if e != nil {
		return "", e
	}
	result, _ := json.Marshal(proof)
	return "Encrypted platform backup complete; manifest proof: " + string(result), nil
}
func main() {
	mode := flag.String("mode", "", "inspect, create-key, backup, verify, stage-restore; schedule-configure/status/once/run/retry; recovery-review, recovery-review-status, recovery-prepare, recovery-activate, recovery-status")
	config := flag.String("config", "", "absolute private operator configuration")
	key := flag.String("key-file", "", "absolute independent 32-byte key file outside the archive")
	confirmed := flag.Bool("confirmed", false, "operator confirms backup or restore into an empty isolated database")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *mode != "schedule-run" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Minute)
		defer cancel()
	}
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "platform backup request rejected")
		os.Exit(2)
	}
	var message string
	var e error
	if strings.HasPrefix(*mode, "schedule-") {
		message, e = executeSchedule(ctx, *mode, *config, *key, *confirmed)
	} else if *mode == "recovery-prepare" || *mode == "recovery-activate" || *mode == "recovery-status" {
		message, e = executeActivation(ctx, *mode, *config, *key, *confirmed)
	} else if strings.HasPrefix(*mode, "recovery-") {
		message, e = executeReview(ctx, *mode, *config, *key, *confirmed)
	} else {
		message, e = execute(ctx, *mode, *config, *key, *confirmed)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, "Platform backup/recovery unconfirmed or rejected. Retain incomplete archives and recovery quarantine; do not activate or overwrite original data.")
		os.Exit(1)
	}
	fmt.Println(message)
}
