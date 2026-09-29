package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedLocalPreparationPreservesSourceAndCredentials(t *testing.T) {
	root := t.TempDir()
	if initialize(root) != nil {
		t.Fatal("initialize")
	}
	paths := []string{"credentials.json", "platform/api.env", "enterprise/api.env"}
	before := map[string][]byte{}
	for _, p := range paths {
		before[p], _ = os.ReadFile(filepath.Join(root, p))
	}
	if prepareShared(root) != nil {
		t.Fatal("prepare")
	}
	raw, _ := os.ReadFile(filepath.Join(root, "shared/config.json"))
	if prepareShared(root) != nil {
		t.Fatal("repeat prepare")
	}
	again, _ := os.ReadFile(filepath.Join(root, "shared/config.json"))
	if !bytes.Equal(raw, again) {
		t.Fatal("shared secret rotated")
	}
	for _, p := range paths {
		after, _ := os.ReadFile(filepath.Join(root, p))
		if !bytes.Equal(before[p], after) {
			t.Fatal("source changed", p)
		}
	}
	if os.WriteFile(filepath.Join(root, "shared/platform.env"), []byte("changed\n"), 0600) != nil {
		t.Fatal("fixture mutation")
	}
	if prepareShared(root) == nil {
		t.Fatal("changed shared config overwritten")
	}
}
