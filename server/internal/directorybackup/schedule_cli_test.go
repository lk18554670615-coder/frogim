package directorybackup

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/backup"
)

func TestPlatformDailyPostgresRealOperatorCLI(t *testing.T) {
	db := integrationDatabase(t)
	seedDirectory(t, db)
	binary := os.Getenv("TENANCY_PLATFORM_BACKUP_CLI")
	if !filepath.IsAbs(binary) {
		t.Skip("explicit compiled operator helper required")
	}
	identity, e := db.Inspect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r, b, key := fixture()
	r.Expected.DirectoryID = identity.DirectoryID
	root := t.TempDir()
	archives := filepath.Join(root, "archives")
	if e = os.Mkdir(archives, 0700); e != nil {
		t.Fatal(e)
	}
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(root, name)
		if os.WriteFile(path, data, 0600) != nil {
			t.Fatal("fixture write")
		}
		return path
	}
	keyPath := write("key.bin", key)
	config := map[string]any{"databaseUrl": db.DSN, "dumpBinary": db.Tools.Dump, "archiveRoot": archives, "composeFile": write("compose.json", b.Compose), "releaseFile": write("release.json", b.Release), "expected": r.Expected,
		"destination": backup.OffsiteConfig{ID: "platform-offsite", Scope: "platform", DirectoryID: identity.DirectoryID, Endpoint: "https://127.0.0.1:1", Bucket: "isolated-backups", Prefix: "frogim", Region: "us-east-1", AccessKey: "fixture-access", SecretKey: strings.Repeat("s", 43)}}
	raw, _ := json.Marshal(config)
	worker := write("worker.json", raw)
	now := time.Now().UTC()
	config["change"] = ScheduleChange{RequestID: "cli-config", Enabled: true, StartMinuteUTC: now.Hour()*60 + now.Minute(), WindowMinutes: 30, Actor: "cli-operator", Reason: "compiled helper integration", Confirmed: true}
	raw, _ = json.Marshal(config)
	change := write("configure.json", raw)
	run := func(mode, file string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-mode", mode, "-config", file, "-key-file", keyPath, "-confirmed")
		cmd.Stderr = io.Discard
		out, e := cmd.Output()
		if e != nil {
			t.Fatal("operator command failed", mode)
		}
		if bytes.Contains(out, []byte(db.DSN)) || bytes.Contains(out, []byte(root)) || bytes.Contains(out, key) {
			t.Fatal("operator output contains secrets or private path")
		}
		return out
	}
	for range 2 {
		var configured Schedule
		if json.Unmarshal(run("schedule-configure", change), &configured) != nil || configured.Version != 1 {
			t.Fatal("CLI configure idempotency")
		}
	}
	var result struct {
		Schedule Schedule   `json:"schedule"`
		Runs     []DailyRun `json:"runs"`
	}
	if json.Unmarshal(run("schedule-once", worker), &result) != nil || len(result.Runs) != 1 {
		t.Fatal("CLI run status")
	}
	job := result.Runs[0]
	if job.State != "captured" || job.ErrorCode != "OFFSITE_UNCONFIRMED" || job.Proof.Size == 0 {
		t.Fatal("CLI confused local completion with remote success", job.State)
	}
	if _, e = Verify(filepath.Join(archives, job.Request.Expected.BackupID), job.Request, key); e != nil {
		t.Fatal("CLI archive", e)
	}
	if json.Unmarshal(run("schedule-status", worker), &result) != nil || len(result.Runs) != 1 {
		t.Fatal("CLI persisted status")
	}
}
