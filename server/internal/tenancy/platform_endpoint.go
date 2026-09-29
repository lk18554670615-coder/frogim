package tenancy

import (
	"net/url"
	"strings"
)

// PublicPlatformURL permits only the explicit shared-edge prefix. It does not
// relax enterprise discovery, CORS origins or privileged control addresses.
func PublicPlatformURL(raw string) error {
	if strings.HasSuffix(raw, "/platform") {
		return PublicOrigin(strings.TrimSuffix(raw, "/platform"))
	}
	return PublicOrigin(raw)
}

func PlatformOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
