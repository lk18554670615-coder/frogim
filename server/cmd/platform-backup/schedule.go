package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/directorybackup"
)

type scheduleConfiguration struct {
	DatabaseURL string                          `json:"databaseUrl"`
	DumpBinary  string                          `json:"dumpBinary"`
	ArchiveRoot string                          `json:"archiveRoot"`
	ComposeFile string                          `json:"composeFile"`
	ReleaseFile string                          `json:"releaseFile"`
	Expected    backup.Binding                  `json:"expected"`
	Destination backup.OffsiteConfig            `json:"destination"`
	Change      *directorybackup.ScheduleChange `json:"change,omitempty"`
	Retry       *directorybackup.RetryChange    `json:"retry,omitempty"`
}

func executeSchedule(ctx context.Context, mode, configPath, keyPath string, confirmed bool) (string, error) {
	if mode != "schedule-run" && mode != "schedule-once" && mode != "schedule-status" && mode != "schedule-configure" && mode != "schedule-retry" {
		return "", directorybackup.ErrInvalid
	}
	if (mode == "schedule-configure" || mode == "schedule-retry") && !confirmed {
		return "", directorybackup.ErrInvalid
	}
	raw, e := readRegular(configPath, 1<<20)
	if e != nil {
		return "", e
	}
	defer clear(raw)
	var c scheduleConfiguration
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || !externalKey(c.ArchiveRoot, keyPath) || !externalKey(c.ArchiveRoot, configPath) || !externalKey(c.ArchiveRoot, c.ComposeFile) || !externalKey(c.ArchiveRoot, c.ReleaseFile) {
		return "", directorybackup.ErrInvalid
	}
	key, e := readRegular(keyPath, 32)
	if e != nil || len(key) != 32 {
		return "", directorybackup.ErrInvalid
	}
	defer clear(key)
	compose, e := readRegular(c.ComposeFile, 1<<20)
	if e != nil {
		return "", e
	}
	defer clear(compose)
	release, e := readRegular(c.ReleaseFile, 1<<16)
	if e != nil {
		return "", e
	}
	remote, e := backup.NewOffsite(c.Destination)
	if e != nil {
		return "", directorybackup.ErrInvalid
	}
	defer remote.Close()
	s, e := directorybackup.NewScheduler(directorybackup.Database{DSN: c.DatabaseURL, Tools: directorybackup.Tools{Dump: c.DumpBinary}}, c.Expected, directorybackup.Bundle{Compose: compose, Release: release}, c.ArchiveRoot, key, remote)
	if e != nil {
		return "", e
	}
	defer s.Close()
	if mode == "schedule-configure" {
		if c.Change == nil || c.Retry != nil {
			return "", directorybackup.ErrInvalid
		}
		out, e := s.Configure(ctx, *c.Change)
		if e != nil {
			return "", e
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
	if mode == "schedule-retry" {
		if c.Retry == nil || c.Change != nil {
			return "", directorybackup.ErrInvalid
		}
		out, e := s.Retry(ctx, *c.Retry)
		if e != nil {
			return "", e
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
	if c.Change != nil || c.Retry != nil {
		return "", directorybackup.ErrInvalid
	}
	if mode == "schedule-once" {
		if e = s.CaptureOnce(ctx); e != nil {
			return "", e
		}
		if e = s.DeliverOnce(ctx); e != nil {
			return "", e
		}
	}
	if mode == "schedule-status" || mode == "schedule-once" {
		schedule, runs, e := s.Status(ctx)
		if e != nil {
			return "", e
		}
		b, _ := json.Marshal(struct {
			Schedule directorybackup.Schedule   `json:"schedule"`
			Runs     []directorybackup.DailyRun `json:"runs"`
		}{schedule, runs})
		return string(b), nil
	}
	// Validate identity and quarantine before starting any worker. There are
	// no HTTP listeners, shell commands, plaintext dumps or Docker privileges.
	if _, _, e = s.Status(ctx); e != nil {
		return "", e
	}
	var wg sync.WaitGroup
	for _, work := range []func(context.Context) error{s.CaptureOnce, s.DeliverOnce} {
		wg.Add(1)
		go func(work func(context.Context) error) {
			defer wg.Done()
			timer := time.NewTicker(30 * time.Second)
			defer timer.Stop()
			for {
				job, end := context.WithTimeout(ctx, 60*time.Minute)
				e := work(job)
				end()
				if e != nil && ctx.Err() == nil {
					fmt.Fprintln(os.Stderr, "Platform daily backup unconfirmed; inspect schedule-status. No partial archive is overwritten.")
				}
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
			}
		}(work)
	}
	wg.Wait()
	return "Platform daily backup worker stopped; durable tasks retained.", nil
}
