package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

// A separate CA adds the inspection agent to an existing local preview without
// replacing any authentication/control certificate, password or data volume.
func initializeAgent(root string) error {
	b, err := os.ReadFile(filepath.Join(root, "initialized.json"))
	if err != nil {
		return errors.New("initialize the default-only local preview first")
	}
	var base struct {
		Version  int
		TenantID string
	}
	if json.Unmarshal(b, &base) != nil || base.Version != 1 || base.TenantID != "default" {
		return errors.New("not the expected default-only local preview")
	}
	ops := filepath.Join(root, "ops")
	if b, err = os.ReadFile(filepath.Join(ops, "initialized.json")); err == nil {
		var marker struct {
			Version  int
			ServerID string
		}
		if json.Unmarshal(b, &marker) != nil || marker.Version != 1 || marker.ServerID != "default-local" {
			return errors.New("unknown local agent marker; existing files preserved")
		}
		for _, name := range []string{"agent/api.env", "agent/host-id", "agent/pki/ca.pem", "agent/pki/control.pem", "agent/pki/control.key", "platform/peers.json", "platform/pki/ca.pem", "platform/pki/control.pem", "platform/pki/control.key"} {
			info, e := os.Stat(filepath.Join(ops, filepath.FromSlash(name)))
			if e != nil || !info.Mode().IsRegular() {
				return errors.New("local agent configuration incomplete; existing files preserved")
			}
		}
		return nil
	}
	if entries, e := os.ReadDir(ops); e == nil && len(entries) > 0 {
		return errors.New("partial agent configuration; inspect before recovery, no files replaced")
	}
	authority, err := ca("FrogIM local inspection agent CA")
	if err != nil {
		return err
	}
	if err = leaf(ops, "platform/pki", authority, tenancy.PlatformIdentity, []string{"platform-api"}, false); err != nil {
		return err
	}
	if err = leaf(ops, "agent/pki", authority, deployment.AgentIdentity("default-local"), []string{"enterprise-agent"}, false); err != nil {
		return err
	}
	if err = save(ops, "agent/host-id", []byte(secret())); err != nil {
		return err
	}
	if err = envFile(ops, "agent/api.env", map[string]string{"AGENT_ENV": "development", "AGENT_SERVER_ID": "default-local", "AGENT_TENANT_ID": "default", "AGENT_TENANT_HTTP_URL": "https://127.0.0.1:18444", "AGENT_ISOLATION_MODE": "local_preview", "AGENT_HOST_ID_FILE": "/config/host-id", "AGENT_CA_FILE": "/config/pki/ca.pem", "AGENT_CERT_FILE": "/config/pki/control.pem", "AGENT_KEY_FILE": "/config/pki/control.key"}); err != nil {
		return err
	}
	if err = jsonFile(ops, "platform/peers.json", []deployment.PeerConfig{{ServerID: "default-local", ControlURL: "https://enterprise-agent:8450", CAFile: "/agentplane/pki/ca.pem", CertFile: "/agentplane/pki/control.pem", KeyFile: "/agentplane/pki/control.key"}}); err != nil {
		return err
	}
	return jsonFile(ops, "initialized.json", map[string]any{"version": 1, "serverId": "default-local"})
}
