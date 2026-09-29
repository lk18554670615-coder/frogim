package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformBackupKeyAndConfirmation(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, "key")
	if _, e := execute(t.Context(), "create-key", "", key, false); e == nil {
		t.Fatal("unconfirmed key")
	}
	if _, e := execute(t.Context(), "create-key", "", key, true); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(key)
	if e != nil || len(data) != 32 {
		t.Fatal("key")
	}
	if _, e := execute(t.Context(), "create-key", "", key, true); e == nil {
		t.Fatal("overwrote key")
	}
	for _, mode := range []string{"backup", "stage-restore"} {
		if _, e := execute(t.Context(), mode, "", key, false); e == nil {
			t.Fatal("unconfirmed mutation")
		}
	}
	for _, mode := range []string{"schedule-configure", "schedule-retry"} {
		if _, e := executeSchedule(t.Context(), mode, "", key, false); e == nil {
			t.Fatal("unconfirmed schedule mutation")
		}
	}
	if !externalKey(filepath.Join(root, "archive"), key) {
		t.Fatal("independent key rejected")
	}
	archive := filepath.Join(root, "archive")
	if e := os.Mkdir(archive, 0700); e != nil {
		t.Fatal(e)
	}
	if externalKey(archive, filepath.Join(archive, "key")) || externalKey("relative", key) {
		t.Fatal("key in archive accepted")
	}
}
