package main

import (
	"errors"
	"path/filepath"

	"github.com/linli/im/server/internal/tenancy"
)

func validateAgentRuntime(get func(string) string, goos string) error {
	invalid := errors.New("agent runtime profile invalid; inspect private dedicated-host configuration")
	if goos != "linux" {
		return invalid
	}
	switch get("AGENT_ENV") {
	case "development":
		if get("AGENT_ISOLATION_MODE") != "local_preview" {
			return invalid
		}
		return nil
	case "production":
		if get("AGENT_ISOLATION_MODE") != "dedicated_host" || get("AGENT_HOST_ID_FILE") != "/etc/machine-id" || tenancy.PrivateListen(get("AGENT_ADDR")) != nil || tenancy.PublicOrigin(get("AGENT_TENANT_HTTP_URL")) != nil {
			return invalid
		}
		for _, key := range []string{"AGENT_CA_FILE", "AGENT_CERT_FILE", "AGENT_KEY_FILE"} {
			if !filepath.IsAbs(get(key)) {
				return invalid
			}
		}
		if get("AGENT_EXECUTOR_FILE") != "" && !filepath.IsAbs(get("AGENT_EXECUTOR_FILE")) {
			return invalid
		}
		return nil
	}
	return invalid
}
