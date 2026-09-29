package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestImportCommandRequiresExplicitBoundariesAndSanitizesErrors(t *testing.T) {
	for _, args := range [][]string{{"-bad", "private-password"}, {"-mode", "start", "-batch", "sample"}, {"-mode", "resume", "-batch", "sample"}} {
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
