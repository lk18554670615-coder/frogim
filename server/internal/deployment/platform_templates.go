package deployment

// No enterprise proxy, media proxy or internal control routes. CSP permits
// HTTPS/WSS for platform-authenticated runtime tenant contexts, not arbitrary
// server entry by a user. APIs independently validate each enterprise identity.
const platformCaddyConfig = `{
  auto_https off
  admin off
}
http://:8080 {
  respond /health "platform-gateway-ready"
}
https://:8443 {
  tls /config/public.pem /config/public.key
  header {
    -Server
    X-Content-Type-Options nosniff
    X-Frame-Options DENY
    Referrer-Policy no-referrer
    Strict-Transport-Security "max-age=15552000"
    Content-Security-Policy "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data: blob:; connect-src 'self' https: wss: blob:; worker-src 'self' blob:; font-src 'self' data:; media-src 'self' https: blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"
  }
  @private path /internal/* /metrics /debug/* /twirp/*
  handle @private {
    respond 404
  }
  @platformAPI path /platform/admin/* /v2/* /health
  handle @platformAPI {
    reverse_proxy platform-api:8090 {
      header_up X-Forwarded-For {remote_host}
      header_up X-Real-IP {remote_host}
      header_up X-Frogim-Gateway {$FROGIM_GATEWAY_SECRET}
      header_up -Forwarded
    }
  }
  redir /app /app/ 308
  handle_path /app/* {
    root * /srv/web
    header Cache-Control "no-cache"
    @fonts path *.woff *.woff2 *.ttf *.otf
    header @fonts Cache-Control "public, max-age=2592000, immutable"
    try_files {path} /index.html
    file_server
  }
  handle {
    root * /srv/platform
    header Cache-Control "no-cache"
    try_files {path} /platform.html
    file_server
  }
}
`
