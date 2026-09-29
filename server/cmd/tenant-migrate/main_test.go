package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/legacyimport"
)

func TestMigrationRequiresCompleteUnchangedBackupProof(t *testing.T) {
	dir := t.TempDir()
	c := configuration{Request: legacyimport.CutoverRequest{OriginalDatabase: "old/original", AdoptedDatabase: "new/copy"}, Step: legacyimport.CutoverStep{Phase: "backed_up"}, EvidenceFile: filepath.Join(dir, "evidence.json")}
	e := evidence{Phase: c.Step.Phase, OriginalDatabase: c.Request.OriginalDatabase, AdoptedDatabase: c.Request.AdoptedDatabase, OldStackStopped: true, OldAuthIsolated: true, NewStackPrivate: true, VerificationSHA256: strings.Repeat("a", 64)}
	for _, kind := range []string{"database", "media", "im", "redis", "deployment"} {
		raw := []byte("fixture-" + kind)
		path := filepath.Join(dir, kind)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		e.Files = append(e.Files, proofFile{Kind: kind, Path: path, SHA256: digest(raw), Size: int64(len(raw))})
	}
	save := func() {
		t.Helper()
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(c.EvidenceFile, raw, 0600); err != nil {
			t.Fatal(err)
		}
		c.Step.EvidenceSHA256 = digest(raw)
	}
	save()
	if err := verifyEvidence(c); err != nil {
		t.Fatal(err)
	}
	e.OldAuthIsolated = false
	save()
	if verifyEvidence(c) == nil {
		t.Fatal("unfenced authority accepted")
	}
	e.OldAuthIsolated = true
	e.NewStackPrivate = false
	save()
	if verifyEvidence(c) == nil {
		t.Fatal("public candidate accepted before write intent")
	}
	e.NewStackPrivate = true
	last := e.Files[len(e.Files)-1]
	e.Files = e.Files[:len(e.Files)-1]
	save()
	if verifyEvidence(c) == nil {
		t.Fatal("incomplete full backup accepted")
	}
	e.Files = append(e.Files, last)
	save()
	if err := os.WriteFile(last.Path, []byte(strings.Repeat("x", int(last.Size))), 0600); err != nil {
		t.Fatal(err)
	}
	if verifyEvidence(c) == nil {
		t.Fatal("same size modified backup accepted")
	}
}

func TestMigrationRejectsUnknownConfigurationAndRelativeFiles(t *testing.T) {
	var c configuration
	if _, e := readJSON("relative.json", &c); e == nil {
		t.Fatal("relative private configuration")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	for _, raw := range []string{`{"allowOldLogin":true}`, `{} {}`} {
		if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := readJSON(path, &c); e == nil {
			t.Fatal("ambiguous config accepted")
		}
	}
}
