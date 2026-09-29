package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOfflineBackupRequiresConfirmationAndExistingJournal(t *testing.T) {
	for _, mode := range []string{"backup", "stage-restore", "repair-media", "restore", "shell"} {
		if _, e := execute(context.Background(), mode, "", "", false); e == nil {
			t.Fatal("confirmation bypass")
		}
	}
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if os.Mkdir(state, 0700) != nil {
		t.Fatal("fixture")
	}
	configPath := filepath.Join(dir, "config.json")
	key := filepath.Join(dir, "key")
	c := configuration{StateDirectory: state, BundleDirectory: dir, DockerBinary: filepath.Join(dir, "docker"), ArchiveDirectory: filepath.Join(dir, "archive")}
	data, _ := json.Marshal(c)
	if os.WriteFile(configPath, data, 0600) != nil || os.WriteFile(key, make([]byte, 32), 0600) != nil {
		t.Fatal("fixture")
	}
	if _, e := execute(context.Background(), "backup", configPath, key, true); e == nil {
		t.Fatal("adopted absent journal")
	}
	if _, e := os.Stat(filepath.Join(state, "state.json")); !os.IsNotExist(e) {
		t.Fatal("created a new journal")
	}
}
func TestOfflineBackupStrictConfiguration(t *testing.T) {
	dir := t.TempDir()
	for _, value := range []string{`{"unknown":true}`, `{} {}`, `[]`} {
		p := filepath.Join(dir, "config.json")
		if os.WriteFile(p, []byte(value), 0600) != nil {
			t.Fatal("fixture")
		}
		if _, e := readConfiguration(p); e == nil {
			t.Fatal("ambiguous configuration")
		}
	}
	if _, e := readRegular(dir, 100); e == nil {
		t.Fatal("directory as key")
	}
}

func TestIndependentKeyExclusiveCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.key")
	if _, e := execute(context.Background(), "create-key", "", path, true); e != nil {
		t.Fatal(e)
	}
	original, e := os.ReadFile(path)
	if e != nil || len(original) != 32 {
		t.Fatal("key missing")
	}
	if _, e = execute(context.Background(), "create-key", "", path, true); e == nil {
		t.Fatal("overwrote backup key")
	}
	next, _ := os.ReadFile(path)
	if string(original) != string(next) {
		t.Fatal("backup key changed")
	}
}
