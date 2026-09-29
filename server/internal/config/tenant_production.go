package config

import (
	"errors"
	"net/url"
	"strings"

	"github.com/linli/im/server/internal/tenancy"
)

func (c Config) validateTenantProduction() error {
	invalid := errors.New("production enterprise configuration invalid; inspect the private dedicated-host profile")
	if c.DevMode || c.SeedDemo || c.DevAllowContainerBind || c.DevIPTestOnly || c.DevOTPCode != "" || c.OTPWebhookURL != "" || c.OTPWebhookToken != "" || c.PushProvider != "platform" || c.TrustProxy {
		return invalid
	}
	if tenancy.PublicOrigin(c.TenantPublicURL) != nil || len(c.AllowedOrigins) == 0 || tenancy.DeploymentDatastores(c.DatabaseURL, c.RedisURL, "enterprise", c.TenantID, c.DatastoreMode) != nil {
		return invalid
	}
	for _, origin := range c.AllowedOrigins {
		if tenancy.PublicOrigin(origin) != nil {
			return invalid
		}
	}
	db, _ := url.Parse(c.DatabaseURL)
	dbSecret, _ := db.User.Password()
	cache, _ := url.Parse(c.RedisURL)
	cacheSecret, _ := cache.User.Password()
	seen := map[string]bool{}
	for _, secret := range []string{c.JWTSecret, c.MediaSigningSecret, c.S3SecretKey, c.WukongManagerToken, c.WukongTokenSecret, c.WukongPolicySecret, c.LiveKitAPISecret, dbSecret, cacheSecret, c.GatewaySecret} {
		if len(secret) < 32 || seen[secret] {
			return invalid
		}
		seen[secret] = true
	}
	if c.LegacyMediaSigningSecret != "" && c.LegacyMediaSigningSecret == c.JWTSecret {
		return invalid
	}
	if c.S3Endpoint != "enterprise-minio:9000" || !c.S3PublicSecure || tenancy.PublicOrigin("https://"+c.S3PublicEndpoint) != nil || c.S3AndroidPublicEndpoint != "" {
		return invalid
	}
	if !c.WukongEnabled || c.WukongAPIURL != "http://enterprise-im:5001" || c.WukongManagerURL != "http://enterprise-im:5300" || c.WukongWSURL != "wss://"+strings.TrimPrefix(c.TenantPublicURL, "https://")+"/im" || !c.LiveKitEnabled || c.LiveKitAPIURL != "http://enterprise-livekit:7880" {
		return invalid
	}
	im, err := url.Parse(c.WukongTCPURL)
	if err != nil || im.Scheme != "tcp" || im.Port() == "" || im.User != nil || im.RawQuery != "" || im.Fragment != "" || im.Path != "" || tenancy.PublicOrigin("https://"+im.Host) != nil {
		return invalid
	}
	tlsConfig, err := tenancy.TLSConfig(c.TenantCAFile, c.TenantCertFile, c.TenantKeyFile)
	if err != nil || tenancy.ValidateLocalIdentity(tlsConfig, tenancy.EnterpriseIdentity(c.TenantID)) != nil {
		return invalid
	}
	return nil
}
