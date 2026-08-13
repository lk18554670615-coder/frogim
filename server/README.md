# 邻里通讯服务端（linli-im）

基于 Go 1.26 的模块化单体业务服务，为邻里通讯 Flutter 客户端和运营后台提供 REST、WuKongIM DataSource/策略/Webhook、PostgreSQL 持久化、Redis 任务协调、对象存储、推送和 LiveKit 房间控制。消息长连接、ACK、离线消息与最近会话由固定版本 WuKongIM 负责。

## Run

The service starts fail-closed and has no standalone memory mode. Start the
complete PostgreSQL, Redis, MinIO, WuKongIM, LiveKit and Go development stack
from the repository root:

```bash
make infra-up
```

Use `make infra-up-android-emulator` when an Android Studio emulator must reach
the host through `10.0.2.2`. `go run ./cmd/server` is supported only after all
runtime dependencies and WuKongIM/LiveKit settings are explicitly provided;
it never falls back to a second message runtime.

Docker development may bind the service to the container interface by
setting all of `IM_DEV_MODE=true`, `IM_ENV=development`, and
`IM_DEV_ALLOW_CONTAINER_BIND=true`. The escape hatch defaults to false and is
always rejected when either `IM_ENV` or `APP_ENV` resolves to `production`.

Demo accounts are opt-in. Set `IM_SEED_DEMO=true` together with
`IM_DEV_MODE=true` to create `13800000001` (Alice), `13800000002` (Bob), and
`13800000000` (Admin). Public development deployments keep this disabled.
Request a code with `POST /v2/auth/code`; the server never returns the
configured development code.

The service uses normalized PostgreSQL tables, automatically applies its idempotent schema, and checks Redis when configured:

```bash
export IM_DATABASE_URL='postgres://nexachat:nexachat@localhost:5432/nexachat?sslmode=disable'
export IM_REDIS_URL='redis://localhost:6379/0'
export IM_JWT_SECRET='replace-with-a-long-random-secret'
export IM_ADMIN_KEY='replace-with-a-random-admin-key'
export IM_ADMIN_SHARED_KEY_ENABLED=false
export IM_ADMIN_ID='platform-admin'
export IM_ADMIN_EMAIL='admin@example.com'
export IM_ADMIN_PASSWORD_HASH='$2b$12$...'
export IM_ADMIN_TOTP_SECRET='BASE32SECRET'
export IM_ADMIN_ROLE='platform_admin'
export IM_DEV_MODE=false
export IM_OTP_WEBHOOK_URL='https://otp-gateway.example/v1/otp'
export IM_OTP_WEBHOOK_TOKEN='replace-with-a-random-gateway-token'
export IM_PUSH_PROVIDER=webhook
export IM_PUSH_WEBHOOK_URL='https://push-gateway.example/v1/deliver'
export IM_PUSH_WEBHOOK_TOKEN='replace-with-a-random-gateway-token'
export IM_S3_ENDPOINT='localhost:9000'
export IM_S3_PUBLIC_ENDPOINT='localhost:9000'
export IM_S3_ACCESS_KEY='nexachat'
export IM_S3_SECRET_KEY='change-this-before-production'
export IM_S3_BUCKET='nexachat-media'
go run ./cmd/server
```

The server can also deliver directly through Getui RestAPI V2. Set `IM_PUSH_PROVIDER=getui` and provide `IM_GETUI_APP_ID`, `IM_GETUI_APP_KEY`, and `IM_GETUI_MASTER_SECRET` only in the server secret environment. Devices registered with `provider=getui` store their Getui CID in `pushToken`; other provider devices are ignored by this sender. The provider caches the one-day API token, refreshes proactively and once on Getui code `10001`, uses bounded HTTP timeouts, and returns safe errors to the durable outbox retry loop. Never embed the master secret in Flutter, a checked-in file, or an admin API response.

For production iOS killed-process calls, use `IM_PUSH_PROVIDER=getui_apns_voip`. Configure `IM_APNS_VOIP_KEY_ID`, `IM_APNS_VOIP_TEAM_ID`, `IM_APNS_VOIP_BUNDLE_ID=com.linlitong.imapp`, `IM_APNS_VOIP_PRIVATE_KEY_FILE`, and the correct sandbox flag. The `.p8` key is parsed at startup and the process fails closed when it is absent or invalid. The host key must be owned by container uid or gid `10001` and have mode `0400` or `0440`. `infra/scripts/deploy.sh` automatically adds `infra/compose.apns-voip.yaml` for `apns_voip` and `getui_apns_voip`; the overlay mounts the key read-only without copying it into the image. The APNs sender uses a cached ES256 provider JWT, HTTP/2, the `.voip` topic, a zero expiry, privacy-safe call routing fields, retry classification, and automatic invalid-token disabling. See `../docs/CONFIGURATION.md` for the Chinese deployment procedure.

`internal/store/schema.sql` is embedded into the binary and is the business schema. WuKongIM owns message body, message ID, channel sequence, ACK, recent conversations and offline history. PostgreSQL transactions own accounts, relationships, channel policy, extensions, message indexes, push rows and WuKong Outbox; sender/client and aggregate idempotency keys serialize retries across instances.

The server applies the embedded, versioned `internal/store/schema.sql` at
startup. There is no second hand-applied migration chain; startup either
advances the complete business schema atomically or fails before serving.

## HTTP contract

All user routes except login require `Authorization: Bearer <accessToken>`. REST access tokens are accepted only from this header; query-string, body and cookie token fallbacks are intentionally unsupported. JSON errors have the stable form `{"error":{"code":"...","message":"..."}}`.

| Area | Method and path |
|---|---|
| Runtime | `GET /health`, `GET /ready`, `GET /metrics` |
| Auth/profile | OTP login, password registration/login/reset under `/v2/auth/*`; refresh/logout; `GET/PATCH /v2/users/me`; phone change code and confirmation routes |
| Devices/push | `POST /v2/users/me/devices`, `GET/DELETE /v2/users/me/devices[/{id}]`; transactional `im_push_outbox` |
| Media | `POST /v2/media/presign`, direct S3/MinIO `PUT`, `POST /v2/media/{id}/complete`, authenticated `GET /v2/media/{id}` |
| Users | `GET /v2/contacts/search?q=&by=handle|phone`, `GET /v2/contacts/search/capabilities`, `GET /v2/contacts/friends`, `PUT /v2/contacts/blocks/{id}` |
| Friends | `GET/POST /v2/contacts/requests`, accept/reject/cancel routes, `DELETE/PATCH /v2/contacts/friends/{id}` |
| Conversations | `GET /v2/channels/conversations`, `POST /v2/channels/direct`, `PATCH /v2/channels/conversations/{id}/preferences`, `DELETE /v2/channels/conversations/{id}` |
| Groups | profile, announcement/read, invite confirmation, QR join, member/role/mute/nickname, owner transfer, leave and disband under `/v2/channels/groups/*` |
| Messages | WuKongIM SDK send/history, `/v2/messages/{id}` edit/recall/reactions, business DataSource and CMD |
| IM session/data source | `POST /v2/auth/im-session`; `/v2/im/datasource/{conversations,messages,extensions,message-extras,reminders,channel,members}` |
| Channels/modules | `/v2/channels/*`, `/v2/moments/*`, `/v2/stickers/*`, `/v2/support/*`, `/v2/calls/*` |
| Trust | `POST /v2/reports` |
| Personal | `GET /v2/messages/favorites`, `POST /v2/feedback` |
| Admin | `/v2/admin/{dashboard,users,groups,reports,sensitive-words,health,audit-logs,settings}` |

Administrators authenticate at `POST /v2/admin/auth/login` with `email`, `password`, and `totp`, then use the returned short-lived admin JWT. Roles are `platform_admin`, `system_operator`, `moderator`, `content_operator`, `support_agent`, and read-only `support`. The shared emergency key is disabled by default and is accepted only when `IM_ADMIN_SHARED_KEY_ENABLED=true`. `/api/v2/admin/*` is an alias for admin frontends deployed behind an `/api` prefix; WuKongIM/LiveKit secrets are never returned to the browser.

Example login and IM-session flow:

```bash
curl -s localhost:8080/v2/auth/code -H 'content-type: application/json' -H 'x-client-platform: android' -d '{"phone":"13800000001"}'
curl -s localhost:8080/v2/auth/login -H 'content-type: application/json' -H 'x-client-platform: android' -d '{"phone":"13800000001","code":"<one-time-code>"}'
curl -s localhost:8080/v2/auth/im-session -H "authorization: Bearer $TOKEN" -H 'content-type: application/json' -d '{"deviceId":"device-a","platform":"android"}'
```

Flutter 使用返回的 `ImSession` 初始化对应平台的 WuKongIM Gateway；消息通过 WuKongIM SDK 发送，不通过业务服务自建 WebSocket。业务资料、权限、扩展、提醒和模块数据继续经已鉴权 REST/DataSource 同步。

Conversation preferences are per-user and independent from group moderation. `PATCH /v2/channels/conversations/{id}/preferences` accepts any non-empty subset of `pinned`, `notificationsMuted`, and `manualUnread`. `PUT /v2/channels/conversations/{id}/read` always clears `manualUnread`. `DELETE /v2/channels/conversations/{id}` does not delete messages or remove membership: it hides the conversation only for the requesting user up to its current `lastMessageSeq`, clears pin/manual-unread, and the next message makes the conversation visible again. `GET /v2/channels/conversations` returns these fields under `membership` and sorts pinned conversations first.

Every registration receives a random globally unique neighbor handle beginning with `ll_`; it is independent of the phone number. `PATCH /v2/users/me` accepts a non-empty subset of `name`, unique lowercase `handle` (`[a-z0-9_]`, 6–24 characters), `signature` (up to 160 characters), and `avatarMediaId`. Reserved or impersonation-prone handles are rejected. A user may change the generated handle at most twice; `GET` and `PATCH /v2/users/me` return `handleChangeCount`, `handleChangesRemaining`, `allowSearchByHandle`, and `allowSearchByPhone`. An avatar media object must belong to the caller and be in `ready` state; the response includes both `avatarMediaId` and the authenticated relative `avatarUrl`. Phone changes are two-step: request a code with `POST /v2/users/me/phone/code`, then send `phone` and `code` to `PATCH /v2/users/me/phone`. Device list responses never expose push tokens.

Discovery is exact-match only. `GET /v2/contacts/search?q=<value>&by=handle|phone` obeys the separately audited `allowSearchByHandle` and `allowSearchByPhone` runtime settings and returns a privacy-safe projection with an empty/omitted phone. `GET /v2/contacts/search/capabilities` lets authenticated clients hide disabled search modes. There is no partial phone search.

Friend requests use a persisted `pending -> accepted|rejected|cancelled|expired` state machine. A request with `source=group` must also provide `sourceId=<conversationId>`; the server verifies that both users remain members and that the group still allows member-to-member friend requests. Push payloads contain routing identifiers and state only, never the verification message.

Group management is transactional and role-checked. `GET/PATCH /v2/channels/groups/{id}` handles name, avatar, join policy, QR rotation, all-member mute, and the member-add-friend switch. Announcement update/read, invite creation and accept/reject/cancel, short-lived QR-token join, owner transfer, self nickname, leave, remove, admin role, member mute, and owner-only disband have dedicated routes. QR payloads contain only a 192-bit cryptographically random opaque token with a 24-hour expiry; no phone number or user identifier is encoded. `GET /v2/channels/groups/{id}/members` returns `userId`, `name`, `handle`, `avatarUrl`, role, group nickname and mute/read metadata, and never returns phone numbers. Every privileged operation writes an audit entry and durable business/outbox state; WuKongIM provisioning and CMD delivery are retried and reconciled.

Forwarding is server-generated: `POST /v2/messages/forward` accepts `targetConversationId`, `sourceMessageIds` (1–100 unique IDs), `mode` (`separate` or `merged`), and a required `clientBatchId`. The caller must be a member of every source conversation and the target. Separate forwarding preserves the trusted source type/body and annotates it as forwarded; merged forwarding emits one `chat_history` message containing only server-read sender IDs, timestamps, source IDs, types, and bounded summaries. Retries with the same batch ID return the same messages, and normal message sync/outbox delivery is used.

`POST /v2/messages/conversations/{id}/send` is the server-side sending endpoint used by protocol probes and trusted business flows; Flutter clients send ordinary messages through the WuKongIM SDK. It also supports `contact` and `location`. A contact body may contain only `userId`, `name`, `handle`, and `avatarUrl`; `userId` is required and the server replaces the body with the canonical current public profile, so phone, remark, tags, and forged display fields cannot be persisted. A location body must contain only numeric `latitude` (-90…90), numeric `longitude` (-180…180), non-empty `name` (up to 80 characters), and `address` (up to 240 characters). Push-outbox payloads contain only message ID, conversation ID, and type; contact fields, coordinates, addresses, captions, text, and other message bodies remain available only through authenticated sync/history.

Password accounts use `POST /v2/auth/register` (`phone`, `code`, `password`, `name`), `POST /v2/auth/password-login`, `POST /v2/auth/password/reset-code`, and `POST /v2/auth/password/reset`. Login, registration, and refresh requests include `X-Client-Platform: android|ios|web|macos` so the response can issue the correct WuKongIM SDK session. Passwords are bcrypt cost 12, login failures use the same `INVALID_CREDENTIALS` response for unknown phones and wrong passwords, and all public auth routes are rate-limited. Reset-code responses do not reveal whether an account exists; a successful reset revokes existing refresh sessions. Registration obeys `registrationEnabled`/`allowRegistration`. The fixed OTP is available only when `IM_DEV_MODE=true`; production validation requires a configured external OTP webhook.

## Voice and video call sessions

`POST /v2/calls/invite` creates a direct or group call for 2–9 canonical conversation members. Per-member accept/reject/leave transitions, caller-only termination, invitation timeout, membership checks and idempotency are enforced transactionally. `POST /v2/calls/{id}/token` issues a short-lived, participant-scoped LiveKit token only after authorization; the API secret never leaves the server.

LiveKit handles media negotiation, active-speaker updates, reconnect, audio/video and screen sharing. The business service does not receive or persist SDP/ICE; PostgreSQL stores only call/member state, timestamps and end reason. State changes are delivered through durable business Outbox plus WuKongIM CMD and clients re-read canonical call state after reconnect. Admin APIs expose room/participant controls without returning LiveKit credentials.

## Announcements and runtime policy

`GET /v2/announcements` returns published announcements for the authenticated user, with pinned items first. `POST /v2/announcements/{id}/read` records an idempotent read receipt. Administrators use `/v2/admin/announcements` to create drafts or scheduled items, update, publish, withdraw, delete, and target all users or an explicit user-ID list. A replica-safe scheduler promotes due announcements every 15 seconds. Optional publication push is written to the normal retrying push outbox and obeys the global `announcementPushEnabled` policy.

`GET/PUT /v2/admin/settings` is the audited runtime-policy endpoint. It validates registration/password, message text/recall/retention, group size, friend requests, announcement push, audio/video availability, sensitive-word enforcement, report SLA, and maintenance fields. The response also contains boolean `configurationStatus` values for database, Redis, object storage, OTP, push, LiveKit, and admin TOTP. Credentials and endpoints are never returned. Read-only infrastructure limits are grouped under `infrastructure` and listed in `restartRequiredKeys`; change those through deployment secrets/environment and roll the service rather than sending them to `PUT`.

The Getui provider sends a privacy-safe online `transmission` plus Android UPS and iOS APNs `push_channel` notifications. Routing payloads contain event type, unread/badge information, and bounded identifiers only. User message text, friend verification text, file names, credentials, and push tokens are excluded; the client performs sync after opening the notification.

## WuKongIM transport

The client obtains an `ImSession` from the authenticated business API, then connects directly to WuKongIM over TCP on Android/iOS or WSS on Web/macOS. WuKongIM owns handshake, message ACK, channel sequence, deduplication, reconnect, offline messages and recent conversations. The business service supplies channel/member/data-source responses and enforces friend, membership, mute, ban, sensitive-word and message-type policies before delivery.

Business message extensions (edit, recall, reactions, pins, reminders and read state) remain canonical in PostgreSQL. Their Outbox rows publish WuKongIM CMD notifications; clients treat CMD as an invalidation signal and re-sync the authoritative extension state. `/v1/sync`, `/v1/ws/ticket` and `/v1/ws` are not client recovery or transport interfaces.

## Verification

```bash
find cmd internal -name '*.go' -type f -print0 | xargs -0 gofmt -w
go test ./...
go test -race ./...
IM_TEST_DATABASE_URL="$IM_DATABASE_URL" go test -run TestPostgresConcurrent -count=1 -v ./internal/store
docker build -t linli-im-server .
```

The tests cover memory/PostgreSQL business invariants, Outbox/reconciliation, group roles, message extensions, real HTTP login, WuKongIM DataSource/policy contracts, LiveKit authorization and call membership. `tools/wukong-probe` validates a real fixed-version WuKongIM handshake, ACK, receive, offline/history/group sync, CMD, policy and message-extension flow.

## Production checklist

- `IM_DEV_MODE` defaults to false and development mode can bind only to loopback. Startup rejects missing/weak JWT and admin secrets.
- Refresh tokens are one-time rotating sessions persisted in PostgreSQL; reuse, logout, and account bans revoke them.
- WuKongIM provides message long connections and CMD delivery; LiveKit provides media signaling and transport. Redis remains a business cache/task coordination dependency, never the unique copy of a message.
- Production requires HTTPS OTP and push webhooks with high-entropy bearer tokens. OTP gateway endpoints are `POST <base>/request` and `POST <base>/verify`; the push gateway receives a bounded outbox item with up to 20 registered devices.
- Production requires valid WuKongIM TCP/WSS endpoints and a LiveKit deployment with externally reachable media ports. The example hostnames are placeholders.
- Terminate business HTTP and WSS at a trusted reverse proxy; expose only the explicitly documented HTTPS/TCP/WSS/UDP ports.
- Restrict CORS and admin ingress at the edge; rotate `IM_ADMIN_KEY`.
- Run PostgreSQL, WuKongIM and MinIO backups and restore drills. Do not claim high availability while the documented single-node topology is in use.
- `IM_PUSH_PROVIDER=noop|log` are development-only. The production webhook gateway owns APNs/FCM credentials, provider feedback, and token invalidation; the server owns durable leasing, retry, and dead-letter state.

Browser Web Push is an optional channel alongside the selected mobile provider. Set `IM_WEB_PUSH_PUBLIC_KEY`, `IM_WEB_PUSH_PRIVATE_KEY`, and `IM_WEB_PUSH_SUBJECT` together; generate them with `go run ./cmd/webpush-keygen https://chat.example.com`. Subscriptions are encrypted with VAPID, 404/410 endpoints are disabled, and the private key is never returned by an API.
- Production MinIO bootstraps a separate application credential with bucket-scoped least-privilege object access; the root credential is reserved for initialization, backup and administration. Upload completion verifies expected size, client SHA-256 and magic-byte MIME before marking an object ready. General-file malware scanning, archive inspection and media safety scanning remain external production dependencies and must be connected before allowing arbitrary files.
- Backups are created under a private `.incomplete-<timestamp>` directory with `umask 077`, checksummed, permission-tightened, and atomically renamed to the published timestamp only after all steps succeed. Incomplete directories are never valid restore points.
- Export `/metrics`, alert on readiness, errors, latency, queue pressure, and database/Redis availability.
