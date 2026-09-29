package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

func TestLocalAgentInitializationPreservesExistingTrust(t *testing.T) {
	root := t.TempDir()
	if err := initialize(root); err != nil {
		t.Fatal(err)
	}
	original := map[string][]byte{}
	for _, path := range []string{"credentials.json", "platform/pki/control.key", "enterprise/pki/control.key", "browser-ca.pem"} {
		b, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if e != nil {
			t.Fatal(e)
		}
		original[path] = b
	}
	if e := initializeAgent(root); e != nil {
		t.Fatal(e)
	}
	host, e := os.ReadFile(filepath.Join(root, "ops/agent/host-id"))
	if e != nil || len(host) != 43 {
		t.Fatal("host identifier", e)
	}
	if e = initializeAgent(root); e != nil {
		t.Fatal(e)
	}
	next, _ := os.ReadFile(filepath.Join(root, "ops/agent/host-id"))
	if !bytes.Equal(host, next) {
		t.Fatal("host identity changed")
	}
	for path, before := range original {
		after, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if e != nil || !bytes.Equal(before, after) {
			t.Fatal("existing credentials changed", path)
		}
	}
	for dir, id := range map[string]string{"agent": deployment.AgentIdentity("default-local"), "platform": tenancy.PlatformIdentity} {
		base := filepath.Join(root, "ops", dir, "pki")
		cfg, e := tenancy.TLSConfig(filepath.Join(base, "ca.pem"), filepath.Join(base, "control.pem"), filepath.Join(base, "control.key"))
		if e != nil || !deployment.LocalIdentity(cfg, id) {
			t.Fatal("local agent certificate", dir, e)
		}
	}
	oldCA, _ := os.ReadFile(filepath.Join(root, "platform/pki/ca.pem"))
	newCA, _ := os.ReadFile(filepath.Join(root, "ops/platform/pki/ca.pem"))
	if bytes.Equal(oldCA, newCA) {
		t.Fatal("agent trust domain reused old control CA")
	}
}
func TestLocalAgentRefusesPartialConfiguration(t *testing.T) {
	root := t.TempDir()
	if initializeAgent(root) == nil {
		t.Fatal("uninitialized root")
	}
	if e := initialize(root); e != nil {
		t.Fatal(e)
	}
	if e := save(root, "ops/sentinel", []byte("keep")); e != nil {
		t.Fatal(e)
	}
	if initializeAgent(root) == nil {
		t.Fatal("partial configuration accepted")
	}
	b, e := os.ReadFile(filepath.Join(root, "ops/sentinel"))
	if e != nil || string(b) != "keep" {
		t.Fatal("partial files changed")
	}
}
