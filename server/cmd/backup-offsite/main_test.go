package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/backup"
)

func TestOffsiteCommandRequiresConfirmationAndExactJournal(t *testing.T) {
	for _, mode := range []string{"upload", "download"} {
		if _, e := execute(t.Context(), mode, "", "", false); e == nil {
			t.Fatal("unconfirmed transfer")
		}
	}
	root := t.TempDir()
	path := filepath.Join(root, "intent.json")
	record := intent{Version: 1, Mode: "upload", Actor: "operator", Reason: "fixture transfer"}
	if saveOnce(path, record) != nil || saveOnce(path, record) != nil {
		t.Fatal("idempotent record")
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	record.Actor = "someone-else"
	if saveOnce(path, record) == nil {
		t.Fatal("audit overwritten")
	}
	after, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("existing audit changed")
	}
	if outside(root, filepath.Join(root, "key")) || !outside(filepath.Join(root, "archive"), filepath.Join(root, "key")) {
		t.Fatal("key separation")
	}
	if _, e = resolved("relative"); e == nil {
		t.Fatal("relative path accepted")
	}
	stamp := filepath.Join(root, "stamped.json")
	if saveStamped(stamp, record) != nil {
		t.Fatal("timestamped audit")
	}
	first, e := os.ReadFile(stamp)
	if e != nil {
		t.Fatal(e)
	}
	if saveStamped(stamp, record) != nil {
		t.Fatal("timestamped repeat")
	}
	second, _ := os.ReadFile(stamp)
	if !bytes.Equal(first, second) {
		t.Fatal("retry changed audit timestamp")
	}
}
func TestOffsiteCommandPolicyNeverIncludesCredentials(t *testing.T) {
	c := configuration{Destination: backup.OffsiteConfig{ID: "alpha-offsite", Scope: "enterprise", TenantID: "alpha", ServerID: "host-alpha", Endpoint: "https://backup.example.test", Bucket: "backups", Prefix: "frogim", Region: "us-east-1", AccessKey: "fixture-access-key", SecretKey: strings.Repeat("s", 43)}}
	file := filepath.Join(t.TempDir(), "config.json")
	raw, _ := json.Marshal(c)
	if e := os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	policy, e := execute(t.Context(), "policy", file, "", false)
	if e != nil || strings.Contains(policy, c.Destination.SecretKey) || strings.Contains(policy, c.Destination.AccessKey) || !strings.Contains(policy, "frogim/enterprise/alpha/host-alpha/*") {
		t.Fatal("policy leaked credentials or scope")
	}
}
