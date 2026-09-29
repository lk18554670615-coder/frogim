package deployment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// EdgeConfig belongs to the host operator, outside enterprise agent execution.
// Paths are existing Linux directories. This builder never creates, mounts or
// changes them; a release preflight must verify the certificate and contents.
type EdgeConfig struct {
	Platform             PlatformConfig   `json:"platform"`
	Enterprise           EnterpriseConfig `json:"enterprise"`
	CertificateDirectory string           `json:"certificateDirectory"`
	DownloadsDirectory   string           `json:"downloadsDirectory"`
	LegalDirectory       string           `json:"legalDirectory"`
}

func BuildEdgeBundle(c EdgeConfig) ([]byte, error) {
	p, e := c.Platform, c.Enterprise
	if p.Validate() != nil || e.Validate() != nil || p.SharedIngress == nil || !e.sharedIngress() || p.PublicURL != e.PublicURL() || p.SharedIngress.Secret == e.Production.SharedIngress.Secret {
		return nil, ErrBundle
	}
	matched := false
	for _, peer := range p.Peers {
		if peer.TenantID == e.TenantID && peer.ServerID == e.ServerID && peer.ControlURL == e.Production.ControlURL {
			matched = true
		}
	}
	if !matched || e.PlatformControlURL != p.ControlURL || p.RedisSecret != e.Secrets.Redis {
		return nil, ErrBundle
	}
	for _, dir := range []string{c.CertificateDirectory, c.DownloadsDirectory, c.LegalDirectory} {
		if !path.IsAbs(dir) || path.Clean(dir) != dir || dir == "/" || strings.ContainsAny(dir, "\r\n\x00$") {
			return nil, ErrBundle
		}
	}
	u, _ := url.Parse(p.PublicURL)
	if u.Port() != "" {
		return nil, ErrBundle
	}
	upstream := func(ip string, port int) string { return "https://" + net.JoinHostPort(ip, strconv.Itoa(port)) }
	platform := upstream(p.ControlBindIP, p.SharedIngress.HTTPSPort)
	enterprise := upstream(e.Production.ControlBindIP, e.Ports.HTTP)
	media := upstream(e.Production.ControlBindIP, e.Ports.Media)
	if platform == enterprise || platform == media {
		return nil, ErrBundle
	}
	proxy := func(target, role string, signedMedia bool) string {
		extra := ""
		// S3 signatures include the original host and path. Never strip the
		// bucket prefix or redirect an upload to the internal origin.
		if signedMedia {
			extra = "\n      header_up Host {http.request.host}"
		}
		return fmt.Sprintf(`reverse_proxy %s {
      header_up -Forwarded
      header_up -X-Real-IP
      header_up -X-Frogim-Gateway
      header_up X-Forwarded-For {remote_host}
      header_up X-Frogim-Client-IP {remote_host}
      header_up X-Frogim-Edge {$%s_EDGE_SECRET}%s
      transport http {
        tls_server_name %s
        tls_trust_pool file /config/%s-ca.pem
      }
    }`, target, strings.ToUpper(role), extra, u.Hostname(), role)
	}
	pc, ec, mc := proxy(platform, "platform", false), proxy(enterprise, "enterprise", false), proxy(media, "enterprise", true)
	caddy := fmt.Sprintf(`{
  admin 127.0.0.1:2019
  auto_https off
}
http://%s {
  handle /.well-known/acme-challenge/* {
    root * /var/www/certbot
    file_server
  }
  handle {
    redir https://%s{uri} 308
  }
}
https://%s {
  tls /etc/letsencrypt/live/%s/fullchain.pem /etc/letsencrypt/live/%s/privkey.pem
  header {
    -Server
    X-Content-Type-Options nosniff
    Strict-Transport-Security "max-age=31536000"
    Referrer-Policy no-referrer
  }
  route {
    @private path /internal /internal/* /metrics /debug /debug/* /twirp /twirp/* /rtc /rtc/* /v1 /v1/* /platform/internal/* /platform/metrics /platform/debug/* /platform/twirp/*
    respond @private 404
    handle /platform/v2/* {
      uri strip_prefix /platform
      %s
    }
    handle /platform/admin/* {
      %s
    }
    redir /platform /platform/ 308
    handle /platform/* {
      %s
    }
    redir /app /app/ 308
    handle /app/* {
      %s
    }
    redir /web /app/ 308
    handle /web/* {
      redir /app/ 308
    }
    handle_path /downloads/* {
      root * /srv/downloads
      header Content-Disposition attachment
      file_server
    }
    handle_path /legal/* {
      root * /srv/legal
      file_server
    }
    handle /%s/* {
      %s
    }
    handle {
      %s
    }
  }
}
`, u.Hostname(), u.Host, u.Host, u.Hostname(), u.Hostname(), pc, pc, pc, pc, e.mediaBucket(), mc, ec)
	// Host networking is confined to this independently operated ingress. All
	// application bundles retain the strict no-host-namespace policy.
	compose := map[string]any{"name": "frogim-edge", "services": map[string]any{"gateway": map[string]any{
		"image": "caddy@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d", "network_mode": "host", "restart": "unless-stopped", "read_only": true,
		"security_opt": []string{"no-new-privileges:true"}, "cap_drop": []string{"ALL"}, "cap_add": []string{"NET_BIND_SERVICE"}, "tmpfs": []string{"/config:mode=0700", "/data:mode=0700"},
		"environment": map[string]string{"EDGE_CADDYFILE": caddy, "PLATFORM_CA": p.PublicTLS.CA, "ENTERPRISE_CA": e.PublicTLS.CA, "PLATFORM_EDGE_SECRET": p.SharedIngress.Secret, "ENTERPRISE_EDGE_SECRET": e.Production.SharedIngress.Secret},
		"healthcheck": map[string]any{"test": []string{"CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:2019/config/"}, "interval": "10s", "timeout": "3s", "retries": 6},
		"entrypoint":  []string{"sh", "-ec", `umask 077; printf '%s' "$EDGE_CADDYFILE" > /config/Caddyfile; printf '%s' "$PLATFORM_CA" > /config/platform-ca.pem; printf '%s' "$ENTERPRISE_CA" > /config/enterprise-ca.pem; caddy validate --config /config/Caddyfile --adapter caddyfile; exec caddy run --config /config/Caddyfile --adapter caddyfile`},
		"volumes": []map[string]any{
			{"type": "bind", "source": c.CertificateDirectory, "target": "/etc/letsencrypt", "read_only": true, "bind": map[string]any{"create_host_path": false}},
			{"type": "bind", "source": path.Join(path.Dir(c.CertificateDirectory), "certbot-webroot"), "target": "/var/www/certbot", "read_only": true, "bind": map[string]any{"create_host_path": false}},
			{"type": "bind", "source": c.DownloadsDirectory, "target": "/srv/downloads", "read_only": true, "bind": map[string]any{"create_host_path": false}},
			{"type": "bind", "source": c.LegalDirectory, "target": "/srv/legal", "read_only": true, "bind": map[string]any{"create_host_path": false}},
		},
	}}}
	raw, err := json.MarshalIndent(compose, "", "  ")
	return bytes.ReplaceAll(raw, []byte("$"), []byte("$$")), err
}
