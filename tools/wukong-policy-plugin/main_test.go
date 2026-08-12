package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPolicyPluginAllowsExactSuccessfulDecision(t *testing.T) {
	var received policyRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(policySecretHeader) != strings.Repeat("s", 32) {
			t.Fatal("missing policy secret")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allowed":true,"reasonCode":1,"code":"ALLOW"}`))
	}))
	defer server.Close()
	plugin := testPolicyPlugin(server.URL)
	reason := plugin.decide(policyRequest{FromUID: "u1", ChannelID: "c1", ChannelType: 9, Payload: []byte(`{"type":1006}`)})
	if reason != reasonSuccess || received.FromUID != "u1" || received.ChannelID != "c1" || string(received.Payload) != `{"type":1006}` {
		t.Fatalf("reason=%d request=%+v payload=%s", reason, received, received.Payload)
	}
}

func TestPolicyPluginPropagatesDenialAndFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		handler  http.Handler
		expected uint32
	}{
		{"denial", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"allowed":false,"reasonCode":11,"code":"NOT_ALLOWED"}`))
		}), 11},
		{"invalid allow", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"allowed":true,"reasonCode":11,"code":"BROKEN"}`))
		}), reasonSystemError},
		{"invalid json", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{`)) }), reasonSystemError},
		{"bad status", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", http.StatusBadGateway) }), reasonSystemError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			if reason := testPolicyPlugin(server.URL).decide(policyRequest{}); reason != test.expected {
				t.Fatalf("reason=%d", reason)
			}
		})
	}
	if reason := (&policyPlugin{}).decide(policyRequest{}); reason != reasonSystemError {
		t.Fatalf("missing config reason=%d", reason)
	}
}

func TestPolicyPluginTimesOutClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"allowed":true,"reasonCode":1,"code":"ALLOW"}`))
	}))
	defer server.Close()
	plugin := testPolicyPlugin(server.URL)
	plugin.client.Timeout = 20 * time.Millisecond
	if reason := plugin.decide(policyRequest{}); reason != reasonSystemError {
		t.Fatalf("timeout reason=%d", reason)
	}
}

func testPolicyPlugin(endpoint string) *policyPlugin {
	return &policyPlugin{endpoint: endpoint, secret: strings.Repeat("s", 32), client: &http.Client{Timeout: time.Second}}
}
