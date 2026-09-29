package tenancy

import (
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// PublicOrigin is a syntactic deployment check, not a DNS ownership or public
// certificate-trust check. Those require the actual operator's infrastructure.
func PublicOrigin(raw string) error {
	if ValidateBaseURL(raw, false) != nil {
		return ErrInvalid
	}
	u, _ := url.Parse(raw)
	// Browser Origin excludes paths (even '/'). Keeping one canonical spelling
	// prevents a rendered CORS allowlist that can never match the real browser.
	if u.Path != "" || u.Host != strings.ToLower(u.Host) || strings.HasSuffix(u.Hostname(), ".") {
		return ErrInvalid
	}
	if u.Port() != "" {
		p, err := strconv.Atoi(u.Port())
		if err != nil || p < 1 || p > 65535 || p == 443 {
			return ErrInvalid
		}
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return ErrInvalid
		}
	} else if !strings.Contains(host, ".") || strings.EqualFold(host, "host.docker.internal") || strings.HasSuffix(strings.ToLower(host), ".localhost") || strings.HasSuffix(strings.ToLower(host), ".local") {
		return ErrInvalid
	}
	return nil
}

// Control listeners on a dedicated host may bind only a concrete private or
// loopback IP. A wildcard would also publish the privileged API on every NIC.
func PrivateListen(raw string) error {
	host, port, err := net.SplitHostPort(raw)
	p, e := strconv.Atoi(port)
	ip, parseErr := netip.ParseAddr(host)
	if err != nil || e != nil || p < 1024 || p > 65535 || parseErr != nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return ErrInvalid
	}
	return nil
}

// Each datastore is on the same private Compose network. External/shared DB
// hosts are deliberately unavailable in the first dedicated-host topology.
func ProductionDatastores(database, redis, databaseService, redisService string) error {
	db, err := url.Parse(database)
	if err != nil || (db.Scheme != "postgres" && db.Scheme != "postgresql") || db.Hostname() != databaseService || db.Port() != "5432" || db.User == nil || db.User.Username() == "" || db.Fragment != "" {
		return ErrInvalid
	}
	dbPassword, ok := db.User.Password()
	query, err := url.ParseQuery(db.RawQuery)
	if err != nil || len(query) != 1 || len(query["sslmode"]) != 1 || db.Path != "/"+strings.TrimSuffix(databaseService, "-db") || !ok || len(dbPassword) < 32 || (query.Get("sslmode") != "disable" && query.Get("sslmode") != "verify-full") {
		return ErrInvalid
	}
	cache, err := url.Parse(redis)
	if err != nil || cache.Scheme != "redis" || cache.Hostname() != redisService || cache.Port() != "6379" || cache.User == nil || cache.RawQuery != "" || cache.Fragment != "" || cache.Path != "/0" {
		return ErrInvalid
	}
	password, ok := cache.User.Password()
	if !ok || len(password) < 32 || password == dbPassword {
		return ErrInvalid
	}
	return nil
}
