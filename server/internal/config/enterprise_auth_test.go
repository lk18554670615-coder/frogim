package config

import (
	"strings"
	"testing"
)

func TestEnterpriseProductionDelegatesOnlySMS(t *testing.T) {
	c := validConfig()
	c.DevMode = false
	c.Environment = "production"
	c.AdminUsername = "ops"
	c.AdminPasswordHash = "$2a$12$example"
	c.PushProvider = "getui"
	c.GetuiAppID = "app-id"
	c.GetuiAppKey = "app-key"
	c.GetuiMasterSecret = strings.Repeat("m", 16)
	configureProductionRealtime(&c)
	if err := c.Validate(); err == nil {
		t.Fatal("standalone must require SMS")
	}
	c.PlatformAuthentication = true
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.DevIPTestOnly = true
	if err := c.Validate(); err == nil {
		t.Fatal("delegated auth bypassed production address checks")
	}
	c.DevIPTestOnly = false
	c.GetuiMasterSecret = "short"
	if err := c.Validate(); err == nil {
		t.Fatal("delegated auth bypassed push checks")
	}
}
