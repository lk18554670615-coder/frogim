package deployment

// Keep policy/webhook behavior aligned with infra/wukongim/wk.yaml. Paths are
// non-root named volumes rather than legacy /root/wukongim host mounts.
const enterpriseWKConfig = `mode: release
addr: "tcp://0.0.0.0:5100"
httpAddr: "0.0.0.0:5001"
wsAddr: "ws://0.0.0.0:5200"
rootDir: "/data"
tokenAuthOn: true
whitelistOffOfPerson: false
pprofOn: false
manager:
  on: true
  addr: "0.0.0.0:5300"
demo:
  on: false
logger:
  level: 2
  dir: "/logs"
  lineNum: false
channel:
  createIfNoExist: false
  cacheCount: 10000
  subscriberCompressOfCount: 1000
conversation:
  on: true
  cacheExpire: 24h
  syncInterval: 30s
  syncOnce: 100
  userMaxCount: 1000
messageRetry:
  interval: 60s
  scanInterval: 5s
  maxCount: 5
webhook:
  grpcAddr: "enterprise-api:6970"
  msgNotifyEventPushInterval: 200ms
  msgNotifyEventRetryMaxCount: 3600
  msgNotifyEventCountPerPush: 200
  focusEvents: ["msg.offline", "msg.notify", "user.onlinestatus", "msg.stream"]
datasource:
  addr: "http://enterprise-api:8080/internal/wukong/datasource"
  channelInfoOn: false
plugin:
  timeout: 1s
  install: []
customerService:
  visitorsOn: false
  visitorsSuffix: "____vstr"
`

const enterpriseLiveKitConfig = `port: 7880
rtc:
  node_ip: 127.0.0.1
  tcp_port: %d
  udp_port: %d-%d
  use_external_ip: false
  allow_tcp_fallback: true
room:
  auto_create: false
  empty_timeout: 60
  departure_timeout: 20
  max_participants: 9
logging:
  level: warn
  json: true
`

const enterpriseCaddyConfig = `{
  auto_https off
  admin off
}
https://:8444 {
  tls /config/public.pem /config/public.key
  @private path /internal/* /platform/* /metrics /twirp/*
  handle @private {
    respond 404
  }
  handle /im {
    reverse_proxy enterprise-im:5200
  }
  handle /livekit/* {
    reverse_proxy enterprise-api:8080
  }
  @api path /v2/* /health /ready
  handle @api {
    reverse_proxy enterprise-api:8080
  }
  handle {
    root * /srv/admin
    try_files {path} /index.html
    file_server
  }
}
http://:8080 {
  respond /health "gateway-ready"
}
https://:8445 {
  tls /config/public.pem /config/public.key
  reverse_proxy enterprise-minio:9000
}
`
