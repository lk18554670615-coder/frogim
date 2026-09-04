package httpapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionVideoBlobCSP(t *testing.T) {
	for _, file := range []string{"Caddyfile", "Caddyfile.ip"} {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "..", "infra", file))
			if err != nil {
				t.Fatal(err)
			}
			policy := string(data)
			if !strings.Contains(policy, "connect-src 'self' blob: https: wss:;") {
				t.Fatal("XFile Web blob reads require connect-src blob: without broadening other sources")
			}
			if !strings.Contains(policy, "media-src 'self' blob: https:;") {
				t.Fatal("local video preview requires media-src blob:")
			}
		})
	}
}
