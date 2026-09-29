package config

import (
	"errors"
	"github.com/linli/im/server/internal/tenancy"
	"strings"
)

func (c Config) validateTenant() error {
	if c.MediaSigningSecret != "" && len(c.MediaSigningSecret) < 32 {
		return errors.New("IM_MEDIA_SIGNING_SECRET must contain at least 32 bytes")
	}
	if c.LegacyMediaSigningSecret != "" && len(c.LegacyMediaSigningSecret) < 32 {
		return errors.New("IM_LEGACY_MEDIA_SIGNING_SECRET must contain at least 32 bytes")
	}
	if c.TenantID == "" {
		if c.PushProvider == "platform" {
			return errors.New("platform push requires a managed enterprise")
		}
		if c.PlatformControlURL != "" || c.BindExistingTenant || c.TenantDeploymentMode != "" {
			return errors.New("enterprise configuration requires IM_TENANT_ID")
		}
		return nil
	}
	if !((c.TenancyPreview && c.Environment == "development" && c.TenantDeploymentMode == "") || (!c.TenancyPreview && c.Environment == "production" && c.TenantDeploymentMode == "dedicated_host")) {
		return errors.New("enterprise runtime requires an explicit preview or dedicated production profile")
	}
	if !tenancy.ValidID(c.TenantID) || tenancy.ValidateBaseURL(c.PlatformControlURL, false) != nil {
		return errors.New("invalid enterprise identity or platform control address")
	}
	if c.TenantPublicURL != "" && tenancy.ValidateBaseURL(c.TenantPublicURL, false) != nil {
		return errors.New("enterprise public address must be an HTTPS root URL")
	}
	if c.TenantCAFile == "" || c.TenantCertFile == "" || c.TenantKeyFile == "" || c.TenantControlAddr == "" {
		return errors.New("enterprise control requires mutual TLS configuration")
	}
	if len(c.MediaSigningSecret) < 32 || c.MediaSigningSecret == c.JWTSecret {
		return errors.New("enterprise media signing key must be independent of authentication")
	}
	if c.DevMode {
		return errors.New("platform-managed enterprises cannot use development authentication")
	}
	if c.PushProvider != "" && c.PushProvider != "noop" && c.PushProvider != "platform" {
		return errors.New("managed enterprises must delegate push to the platform or keep it disabled")
	}
	if c.GetuiAppID != "" || c.GetuiAppKey != "" || c.GetuiMasterSecret != "" || c.APNSVoIPKeyFile != "" || c.APNSVoIPKeyID != "" || c.APNSVoIPTeamID != "" || c.PushWebhookURL != "" || c.PushWebhookToken != "" || c.WebPushPublicKey != "" || c.WebPushPrivateKey != "" {
		return errors.New("shared App push credentials belong to the platform, not an enterprise")
	}
	if c.LiveKitEnabled && (c.TenantPublicURL == "" || c.LiveKitURL != "wss://"+strings.TrimPrefix(c.TenantPublicURL, "https://")+"/livekit") {
		return errors.New("managed LiveKit must use this enterprise gateway /livekit endpoint")
	}
	if c.DBMaxConns > 0 && c.DBMaxConns < 2 {
		return errors.New("enterprise session fencing requires at least two database connections")
	}
	if c.BindExistingTenant && c.LegacyMediaSigningSecret == "" {
		return errors.New("adopting an existing enterprise requires its historical media signing key")
	}
	for _, origin := range c.AllowedOrigins {
		if origin == "*" || tenancy.ValidateBaseURL(origin, false) != nil {
			return errors.New("enterprise CORS requires exact HTTPS origins")
		}
	}
	if c.Environment == "production" {
		return c.validateTenantProduction()
	}
	return nil
}
