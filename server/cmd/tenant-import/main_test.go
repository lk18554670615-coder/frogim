package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportCommandRequiresExplicitBoundariesAndSanitizesErrors(t *testing.T) {
	for _, args := range [][]string{{"-bad", "private-password"}, {"-mode", "start", "-batch", "sample"}, {"-mode", "resume", "-batch", "sample"}, {"-mode", "quarantine", "-batch", "sample"}} {
		var out, diagnostics bytes.Buffer
		code := run(context.Background(), args, func(k string) string {
			if k == "TENANCY_IMPORT_ENV" {
				return "development"
			}
			return "secret-sentinel"
		}, &out, &diagnostics)
		if code != 2 || out.Len() != 0 || strings.Contains(diagnostics.String(), "private-password") || strings.Contains(diagnostics.String(), "secret-sentinel") {
			t.Fatal("unsafe command error")
		}
	}
	var out, diagnostics bytes.Buffer
	if run(context.Background(), []string{"-h"}, func(string) string { t.Fatal("help read env"); return "" }, &out, &diagnostics) != 0 || !strings.Contains(out.String(), "obsolete-service") {
		t.Fatal("help missing cutover boundaries")
	}
	for _, path := range []string{"relative.json", ""} {
		if _, e := controlPeer(path, "default"); e == nil {
			t.Fatal("relative control configuration accepted")
		}
	}
}

func TestQuarantineInventoryRequiresOneExplicitJSONList(t *testing.T) {
	if _, e := readQuarantineUsers("relative.json"); e == nil {
		t.Fatal("relative inventory accepted")
	}
	for _, body := range []string{`[]`, `["id"] {}`, `{"id":"value"}`} {
		path := filepath.Join(t.TempDir(), "ids.json")
		if e := os.WriteFile(path, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := readQuarantineUsers(path); e == nil {
			t.Fatal("invalid inventory accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "ids.json")
	if e := os.WriteFile(path, []byte(`["reviewed-user"]`), 0600); e != nil {
		t.Fatal(e)
	}
	ids, e := readQuarantineUsers(path)
	if e != nil || len(ids) != 1 || ids[0] != "reviewed-user" {
		t.Fatal("valid inventory rejected")
	}
}
