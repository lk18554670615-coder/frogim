package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/legacyimport"
)

func TestPreflightCommandDoesNotExposeConfiguration(t *testing.T) {
	for _, args := range [][]string{{"-source", "postgres://someone:private-secret@remote.example/db"}, {"extra-secret"}, {}} {
		var out, diagnostics bytes.Buffer
		code := run(context.Background(), args, func(string) string { return "secret-sentinel" }, &out, &diagnostics)
		if code != 2 || strings.Contains(diagnostics.String(), "secret") || out.Len() != 0 {
			t.Fatal("configuration/argument error was not sanitized")
		}
	}
	var out, diagnostics bytes.Buffer
	code := run(context.Background(), nil, func(k string) string {
		if k == "TENANCY_PREFLIGHT_ENV" {
			return "development"
		}
		return "postgres://private-user:private-secret@remote.example/private-db"
	}, &out, &diagnostics)
	if code != 2 || diagnostics.String() != "PREFLIGHT_SOURCE_UNAVAILABLE\n" {
		t.Fatal("remote connection was not rejected safely")
	}
}
func TestPreflightReportNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	r := legacyimport.Report{Version: 1, ReadOnly: true, RequiresCutoverValidation: true, Issues: []legacyimport.Issue{{Code: "SOURCE_PHONE_INVALID", SourceUserIDs: []string{"fixture"}}}}
	if e := saveReport(path, r); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if saveReport(path, legacyimport.Report{}) == nil {
		t.Fatal("overwrote report")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("report changed")
	}
	var result legacyimport.Report
	if json.Unmarshal(after, &result) != nil || !result.ReadOnly {
		t.Fatal("report invalid")
	}
	if saveReport(filepath.Join(t.TempDir(), "missing", "report.json"), r) == nil {
		t.Fatal("created unapproved directory")
	}
	if saveReport(filepath.Join(t.TempDir(), "report.txt"), r) == nil {
		t.Fatal("unexpected extension accepted")
	}
}

func TestPreflightHelpDoesNotReadConfigurationOrConnect(t *testing.T) {
	var out, diagnostics bytes.Buffer
	code := run(context.Background(), []string{"-h"}, func(string) string { t.Fatal("help read environment"); return "" }, &out, &diagnostics)
	if code != 0 || diagnostics.Len() != 0 || !strings.Contains(out.String(), "No import") {
		t.Fatal("help unavailable")
	}
}
