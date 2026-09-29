package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/linli/im/server/internal/backup"
)

func discoverDaily(ctx context.Context, c configuration, keyPath string) (string, error) {
	day, e := time.Parse("2006-01-02", c.DailyDate)
	if e != nil || day.Year() < 2020 || c.Expected != (backup.Binding{}) || c.ArchiveDirectory != "" || c.DeliveryFile != "" || c.Destination.Scope != "platform" {
		return "", backup.ErrInvalid
	}
	journal, e := resolved(c.JournalDirectory)
	if e != nil {
		return "", e
	}
	keyPath, e = resolved(keyPath)
	if e != nil || !outside(journal, keyPath) {
		return "", backup.ErrInvalid
	}
	key, e := readPrivate(keyPath, 32)
	if e != nil || len(key) != 32 {
		return "", backup.ErrInvalid
	}
	defer clear(key)
	o, e := backup.NewOffsite(c.Destination)
	if e != nil {
		return "", e
	}
	defer o.Close()
	if e = os.Mkdir(journal, 0700); e != nil && !os.IsExist(e) {
		return "", backup.ErrInvalid
	}
	request := struct{ Mode, DirectoryID, Date, TargetHash, Actor, Reason string }{"discover-daily", c.Destination.DirectoryID, c.DailyDate, o.DestinationFingerprint(), c.Actor, c.Reason}
	if e = saveStamped(filepath.Join(journal, "intent.json"), request); e != nil {
		return "", e
	}
	receipt, e := o.ReadDailyReceipt(ctx, c.DailyDate, key)
	if e != nil {
		return "", e
	}
	if e = saveOnce(filepath.Join(journal, "daily-receipt.json"), receipt); e != nil {
		return "", e
	}
	if e = saveOnce(filepath.Join(journal, "delivery.json"), receipt.Delivery); e != nil {
		return "", e
	}
	if e = saveStamped(filepath.Join(journal, "completed.json"), request); e != nil {
		return "", e
	}
	return "Authenticated daily receipt saved. Inspect its binding before a separate confirmed download and quarantined restore; no database was activated.", nil
}
