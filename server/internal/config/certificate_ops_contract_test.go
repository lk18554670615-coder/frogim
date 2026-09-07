package config

import (
	"os"
	"strings"
	"testing"
)

func TestIPCertificateRenewalTargetsActiveLineageAndReloads(t *testing.T) {
	raw, err := os.ReadFile("../../../infra/scripts/nexachat-ops.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	start := strings.Index(script, "  renew-cert)")
	if start < 0 {
		t.Fatal("certificate renewal action not found")
	}
	end := strings.Index(script[start:], "  issue-cert)")
	if end < 0 {
		t.Fatal("certificate renewal action not found")
	}
	action := script[start : start+end]
	if !strings.Contains(action, `renew --quiet --cert-name "$SERVER_IP"`) {
		t.Fatal("retired IP certificates can block active renewal")
	}
	if !strings.Contains(action, `caddy reload --config /etc/caddy/Caddyfile --force`) {
		t.Fatal("unchanged Caddy config must reload renewed certificates")
	}
}
