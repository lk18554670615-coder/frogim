package main

import (
	"path/filepath"
	"testing"
)

func TestAgentProductionHostAndNetworkGuard(t *testing.T) {
	dir := t.TempDir()
	values := map[string]string{"AGENT_ENV": "production", "AGENT_ISOLATION_MODE": "dedicated_host", "AGENT_HOST_ID_FILE": "/etc/machine-id", "AGENT_ADDR": "10.40.0.12:8450", "AGENT_TENANT_HTTP_URL": "https://tenant.example.test"}
	for _, key := range []string{"AGENT_CA_FILE", "AGENT_CERT_FILE", "AGENT_KEY_FILE"} {
		values[key] = filepath.Join(dir, key)
	}
	get := func(k string) string { return values[k] }
	if validateAgentRuntime(get, "linux") != nil {
		t.Fatal("valid profile")
	}
	if validateAgentRuntime(get, "windows") == nil {
		t.Fatal("production agent accepted another runtime")
	}
	for key, bad := range map[string]string{"AGENT_ISOLATION_MODE": "local_preview", "AGENT_HOST_ID_FILE": "/tmp/chosen-id", "AGENT_ADDR": ":8450", "AGENT_TENANT_HTTP_URL": "https://127.0.0.1:18444", "AGENT_KEY_FILE": "relative", "AGENT_EXECUTOR_FILE": "relative"} {
		t.Run(key, func(t *testing.T) {
			before := values[key]
			values[key] = bad
			defer func() { values[key] = before }()
			if validateAgentRuntime(get, "linux") == nil {
				t.Fatal("unsafe agent profile")
			}
		})
	}
	values["AGENT_ENV"] = "development"
	if validateAgentRuntime(get, "linux") == nil {
		t.Fatal("preview agent can act as dedicated production")
	}
	values["AGENT_ISOLATION_MODE"] = "local_preview"
	if validateAgentRuntime(get, "linux") != nil {
		t.Fatal("local agent blocked")
	}
}
