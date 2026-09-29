package main

import (
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

// Validate before opening/migrating the directory DB or starting workers.
// Same-host datastores and unpublished container listeners are enforced by
// the corresponding deployment artifact; this cannot verify a cloud firewall.
func validatePlatformRuntime(get func(string) string) error {
	invalid := errors.New("platform runtime profile invalid; inspect private operator configuration")
	env, mode := get("PLATFORM_ENV"), get("PLATFORM_DEPLOYMENT_MODE")
	if env == "development" {
		if mode != "" && mode != "local_preview" {
			return invalid
		}
		return nil
	}
	if env != "production" || mode != "dedicated_host" || tenancy.PublicOrigin(get("PLATFORM_PUBLIC_URL")) != nil {
		return invalid
	}
	if tenancy.DeploymentDatastores(get("PLATFORM_DATABASE_URL"), get("PLATFORM_REDIS_URL"), "platform", "", get("PLATFORM_DATASTORE_MODE")) != nil {
		return invalid
	}
	database, _ := url.Parse(get("PLATFORM_DATABASE_URL"))
	cache, _ := url.Parse(get("PLATFORM_REDIS_URL"))
	dbSecret, _ := database.User.Password()
	cacheSecret, _ := cache.User.Password()
	gateway := get("PLATFORM_GATEWAY_SECRET")
	decoded, err := base64.RawURLEncoding.DecodeString(gateway)
	if err != nil || len(decoded) != 32 || gateway == dbSecret || gateway == cacheSecret {
		return invalid
	}
	for _, key := range []string{"PLATFORM_CA_FILE", "PLATFORM_CERT_FILE", "PLATFORM_KEY_FILE", "PLATFORM_PEERS_FILE"} {
		if !filepath.IsAbs(get(key)) {
			return invalid
		}
	}
	for _, key := range []string{"PLATFORM_AGENTS_FILE", "PLATFORM_DEPLOYMENT_CATALOG_FILE"} {
		if get(key) != "" && !filepath.IsAbs(get(key)) {
			return invalid
		}
	}
	for _, key := range []string{"PLATFORM_DEV_OTP_CODE", "PLATFORM_DEV_MODE", "IM_DEV_OTP_CODE", "IM_DEV_MODE"} {
		if get(key) != "" && get(key) != "false" {
			return invalid
		}
	}
	origins := strings.Split(get("PLATFORM_WEB_ORIGIN"), ",")
	for _, origin := range origins {
		if tenancy.PublicOrigin(origin) != nil {
			return invalid
		}
	}
	if !tenancy.ValidID(get("PLATFORM_ADMIN_USERNAME")) {
		return invalid
	}
	cost, err := bcrypt.Cost([]byte(get("PLATFORM_ADMIN_PASSWORD_HASH")))
	if err != nil || cost < 12 {
		return invalid
	}
	ports := map[int]bool{}
	for _, key := range []string{"PLATFORM_ADDR", "PLATFORM_CONTROL_ADDR"} {
		host, port, err := net.SplitHostPort(get(key))
		p, e := strconv.Atoi(port)
		if err != nil || e != nil || p < 1024 || p > 65535 || ports[p] || (host != "" && host != "127.0.0.1" && host != "::1") {
			return invalid
		}
		ports[p] = true
	}
	return nil
}
