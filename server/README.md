# 邻里通讯服务端（linli-im）

基于 Go 1.26 的模块化单体服务，为邻里通讯 Flutter 客户端和运营后台提供 REST、WebSocket、PostgreSQL 持久化、Redis 跨实例实时路由、对象存储和推送能力。服务健康标识与新技术资源名统一使用 `linli-im`。

## Run

The service now starts fail-closed. Generate explicit local-only secrets and a
development OTP; development mode is accepted only on a loopback bind:

```bash
export IM_ADDR=127.0.0.1:8080
export IM_ENV=development
export IM_MODE=memory
export IM_JWT_SECRET="$(openssl rand -hex 32)"
export IM_ADMIN_KEY="$(openssl rand -hex 24)"
export IM_DEV_MODE=true
export IM_DEV_OTP_CODE="$(printf '%06d' $((RANDOM % 1000000)))"
go run ./cmd/server
```

Docker-only development may bind the service to the container interface by
setting all of `IM_MODE=full`, `IM_DEV_MODE=true`, `IM_ENV=development`, and
`IM_DEV_ALLOW_CONTAINER_BIND=true`. The escape hatch defaults to false and is
always rejected when either `IM_ENV` or `APP_ENV` resolves to `production`.

Demo phones are `13800000001` (Alice), `13800000002` (Bob), and `13800000000` (Admin). Request a code with `POST /v1/auth/code`; the server never returns the configured development code.

Full mode uses normalized PostgreSQL tables, automatically applies its idempotent schema, and checks Redis when configured:

```bash
export IM_MODE=full
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

`internal/store/schema.sql` is embedded into the binary and is the full-mode source of truth. Message insertion, sender-scoped `clientMsgId` idempotency, conversation sequence allocation, every recipient's sync cursor, durable sync events, push outbox, and event outbox commit in one PostgreSQL transaction. Conversation rows serialize sequence allocation; sender/client advisory locks serialize simultaneous retries across instances. The integration test opens two independent connection pools and verifies concurrent gap-free sequences and duplicate ACK behavior.

To inspect or apply the same schema manually with `psql`:

```bash
psql "$IM_DATABASE_URL" -f migrations/000003_normalized_runtime.up.sql
```

## HTTP contract

All user routes except login require `Authorization: Bearer <accessToken>`. REST access tokens are accepted only from this header; query-string, body and cookie token fallbacks are intentionally unsupported. JSON errors have the stable form `{"error":{"code":"...","message":"..."}}`.

| Area | Method and path |
|---|---|
| Runtime | `GET /health`, `GET /ready`, `GET /metrics` |
| Auth/profile | OTP login, password registration/login/reset under `/v1/auth/*`; refresh/logout; `GET/PATCH /v1/users/me`; phone change code and confirmation routes |
| Devices/push | `POST /v1/devices`, `GET/DELETE /v1/users/me/devices[/{id}]`; transactional `im_push_outbox` |
| Media | `POST /v1/media/presign`, direct S3/MinIO `PUT`, `POST /v1/media/{id}/complete`, authenticated `GET /v1/media/{id}` |
| Users | `GET /v1/users/search?q=&by=handle|phone`, `GET /v1/users/search/capabilities`, `GET /v1/friends`, `PUT /v1/users/{id}/block` |
| Friends | `GET/POST /v1/friend-requests`, accept/reject/cancel routes, `DELETE/PATCH /v1/friends/{id}` |
| Conversations | `GET /v1/conversations`, `POST /v1/conversations/direct`, `PATCH /v1/conversations/{id}/preferences`, `DELETE /v1/conversations/{id}` |
| Groups | profile, announcement/read, invite confirmation, QR join, member/role/mute/nickname, owner transfer, leave and disband under `/v1/groups/*` |
| Messages | `GET/POST /v1/conversations/{id}/messages`, `POST /v1/conversations/{targetId}/forward`, `POST /v1/messages/{id}/recall`, `PUT /v1/conversations/{id}/read` |
| Realtime state | `POST /v1/conversations/{id}/typing`, `GET /v1/sync?after=&limit=`, `POST /v1/ws/ticket`, `GET /v1/ws?ticket=` |
| Trust | `POST /v1/reports` |
| Personal | `GET /v1/users/me/favorites`, `POST /v1/feedback` |
| Admin | `/v1/admin/{dashboard,users,groups,reports,sensitive-words,health,audit-logs,settings}` |

Administrators authenticate at `POST /v1/admin/auth/login` with `email`, `password`, and `totp`, then use the returned short-lived admin JWT. Roles are `platform_admin`, `moderator`, and read-only `support`. The shared emergency key is disabled by default and is accepted only when `IM_ADMIN_SHARED_KEY_ENABLED=true`. `/api/v1/admin/*` is an alias for admin frontends deployed behind an `/api` prefix.

Example login and message flow:

```bash
curl -s localhost:8080/v1/auth/code -H 'content-type: application/json' -d '{"phone":"13800000001"}'
curl -s localhost:8080/v1/auth/login -H 'content-type: application/json' -d '{"phone":"13800000001","code":"<one-time-code>"}'
curl -s localhost:8080/v1/conversations/direct -H "authorization: Bearer $TOKEN" -H 'content-type: application/json' -d '{"userId":"usr_bob"}'
curl -s localhost:8080/v1/conversations/$CONVERSATION/messages -H "authorization: Bearer $TOKEN" -H 'content-type: application/json' -d '{"clientMsgId":"device-a-0001","type":"text","body":{"text":"hello"}}'
```

Conversation preferences are per-user and independent from group moderation. `PATCH /v1/conversations/{id}/preferences` accepts any non-empty subset of `pinned`, `notificationsMuted`, and `manualUnread`. `PUT /v1/conversations/{id}/read` always clears `manualUnread`. `DELETE /v1/conversations/{id}` does not delete messages or remove membership: it hides the conversation only for the requesting user up to its current `lastMessageSeq`, clears pin/manual-unread, and the next message makes the conversation visible again. `GET /v1/conversations` returns these fields under `membership` and sorts pinned conversations first.

Every registration receives a random globally unique neighbor handle beginning with `ll_`; it is independent of the phone number. `PATCH /v1/users/me` accepts a non-empty subset of `name`, unique lowercase `handle` (`[a-z0-9_]`, 6–24 characters), `signature` (up to 160 characters), and `avatarMediaId`. Reserved or impersonation-prone handles are rejected. A user may change the generated handle at most twice; `GET` and `PATCH /v1/users/me` return `handleChangeCount`, `handleChangesRemaining`, `allowSearchByHandle`, and `allowSearchByPhone`. An avatar media object must belong to the caller and be in `ready` state; the response includes both `avatarMediaId` and the authenticated relative `avatarUrl`. Phone changes are two-step: request a code with `POST /v1/users/me/phone/code`, then send `phone` and `code` to `PATCH /v1/users/me/phone`. Device list responses never expose push tokens.

Discovery is exact-match only. `GET /v1/users/search?q=<value>&by=handle|phone` obeys the separately audited `allowSearchByHandle` and `allowSearchByPhone` runtime settings and returns a privacy-safe projection with an empty/omitted phone. `GET /v1/users/search/capabilities` lets authenticated clients hide disabled search modes. There is no partial phone search.

Friend requests use a persisted `pending -> accepted|rejected|cancelled|expired` state machine. A request with `source=group` must also provide `sourceId=<conversationId>`; the server verifies that both users remain members and that the group still allows member-to-member friend requests. Push payloads contain routing identifiers and state only, never the verification message.

Group management is transactional and role-checked. `GET/PATCH /v1/groups/{id}` handles name, avatar, join policy, QR rotation, all-member mute, and the member-add-friend switch. Announcement update/read, invite creation and accept/reject/cancel, short-lived QR-token join, owner transfer, self nickname, leave, remove, admin role, member mute, and owner-only disband have dedicated routes. QR payloads contain only a 192-bit cryptographically random opaque token with a 24-hour expiry; no phone number or user identifier is encoded. `GET /v1/groups/{id}/members` returns `userId`, `name`, `handle`, `avatarUrl`, role, group nickname and mute/read metadata, and never returns phone numbers. Every privileged operation writes an audit entry, a durable `group.system` sync event, and a persistent `system` message visible in normal group history.

Forwarding is server-generated: `POST /v1/conversations/{targetId}/forward` accepts `sourceMessageIds` (1–100 unique IDs), `mode` (`separate` or `merged`), and a required `clientBatchId`. The caller must be a member of every source conversation and the target. Separate forwarding preserves the trusted source type/body and annotates it as forwarded; merged forwarding emits one `chat_history` message containing only server-read sender IDs, timestamps, source IDs, types, and bounded summaries. Retries with the same batch ID return the same messages, and normal message sync/outbox delivery is used.

`POST /v1/conversations/{id}/messages` also supports `contact` and `location`. A contact body may contain only `userId`, `name`, `handle`, and `avatarUrl`; `userId` is required and the server replaces the body with the canonical current public profile, so phone, remark, tags, and forged display fields cannot be persisted. A location body must contain only numeric `latitude` (-90…90), numeric `longitude` (-180…180), non-empty `name` (up to 80 characters), and `address` (up to 240 characters). Push-outbox payloads contain only message ID, conversation ID, and type; contact fields, coordinates, addresses, captions, text, and other message bodies remain available only through authenticated sync/history. These types use the existing JSONB message schema, so no database migration is required.

Password accounts use `POST /v1/auth/register` (`phone`, `code`, `password`, `name`), `POST /v1/auth/password-login`, `POST /v1/auth/password/reset-code`, and `POST /v1/auth/password/reset`. Passwords are bcrypt cost 12, login failures use the same `INVALID_CREDENTIALS` response for unknown phones and wrong passwords, and all public auth routes are rate-limited. Reset-code responses do not reveal whether an account exists; a successful reset revokes existing refresh sessions. Registration obeys `registrationEnabled`/`allowRegistration`. The fixed OTP is available only when `IM_DEV_MODE=true`; production validation requires a configured external OTP webhook.

## Voice and video call sessions

Calls are limited to two-member direct conversations. `POST /v1/calls/invite` accepts `callId` (optional idempotency key), `conversationId`, optional `calleeUserId`, and `mediaType` (`audio` or `video`). Participants transition the session with `POST /v1/calls/{id}/accept`, `/reject`, `/cancel`, or `/hangup`; `GET /v1/calls/{id}` returns the current metadata. Invites expire after `IM_CALL_INVITE_TTL` and become `missed`. Caller/callee authorization and the `invited -> accepted -> ended` state machine are enforced transactionally.

`GET /v1/calls/config` returns authenticated ICE configuration. Production requires `IM_RTC_STUN_URLS`, `IM_RTC_TURN_URLS`, `IM_RTC_TURN_USERNAME`, and `IM_RTC_TURN_CREDENTIAL`. SDP and ICE candidates continue to use `call.offer`, `call.answer`, and `call.ice` WebSocket frames and are never written to PostgreSQL. Cross-node call-signal publication through Redis fails closed: a publish failure is returned to the sender instead of being acknowledged as delivered. These frames remain ephemeral and have no server replay log; the Flutter client assigns `signalId`, retries until the peer acknowledgement, and deduplicates received signal IDs. In contrast, `accepted`, `rejected`, `cancelled`, `ended`, and `timeout` state changes are transactionally appended to both participants' durable `userSyncSeq` streams, so reconnecting clients converge without persisting SDP/ICE. `GET /v1/admin/calls?q=&status=&cursor=&limit=` returns call IDs, participants, media type, status, timestamps, duration, and end reason only—never SDP or ICE candidates.

## Announcements and runtime policy

`GET /v1/announcements` returns published announcements for the authenticated user, with pinned items first. `POST /v1/announcements/{id}/read` records an idempotent read receipt. Administrators use `/v1/admin/announcements` to create drafts or scheduled items, update, publish, withdraw, delete, and target all users or an explicit user-ID list. A replica-safe scheduler promotes due announcements every 15 seconds. Optional publication push is written to the normal retrying push outbox and obeys the global `announcementPushEnabled` policy.

`GET/PUT /v1/admin/settings` is the audited runtime-policy endpoint. It validates registration/password, message text/recall/retention, group size, friend requests, announcement push, audio/video availability, sensitive-word enforcement, report SLA, and maintenance fields. The response also contains boolean `configurationStatus` values for database, Redis, object storage, OTP, push, TURN, and admin TOTP. Credentials and endpoints are never returned. Read-only infrastructure limits are grouped under `infrastructure` and listed in `restartRequiredKeys`; change those through deployment secrets/environment and roll the service rather than sending them to `PUT`.

The Getui provider sends a privacy-safe online `transmission` plus Android UPS and iOS APNs `push_channel` notifications. Routing payloads contain event type, unread/badge information, and bounded identifiers only. User message text, friend verification text, file names, credentials, and push tokens are excluded; the client performs sync after opening the notification.

## WebSocket protocol

First call authenticated `POST /v1/ws/ticket`, then connect to `ws://localhost:8080/v1/ws?ticket=<shortLivedTicket>`. The ticket expires after 30 seconds and is consumed once; it is the only accepted WebSocket admission credential. Access tokens in the WebSocket URL or upgrade `Authorization` header are rejected. Every frame is one JSON envelope:

```json
{
  "version": 1,
  "requestId": "local-request-id",
  "type": "message.send",
  "payload": {
    "conversationId": "conv_...",
    "clientMsgId": "device-a-0001",
    "messageType": "text",
    "body": {"text": "hello"}
  }
}
```

Client request types are `ping`, `message.send`, `message.read`, `typing`, `sync`, `call.offer`, `call.answer`, `call.ice`, and `call.end`. Call frames only carry WebRTC signaling; media never traverses this server. Server types include `session.ready`, `pong`, `message.ack`, `message.created`, `message.recalled`, `message.read`, `typing`, `sync.result`, call signals, group/friend events, and `error`. `clientMsgId` is unique per sender and makes retries idempotent. `conversationSeq` orders messages inside a conversation. `userSyncSeq` provides a gap-free per-user offline event cursor. The equivalent transport-neutral envelope is documented at `../packages/protocol/im/v1/envelope.proto`.

When a live event is missed because an app was suspended or a client was slow, call `/v1/sync?after=<lastUserSyncSeq>` until `hasMore` is false. WebSocket delivery is an accelerator; the sync cursor is the recovery source of truth.

## Verification

```bash
find cmd internal -name '*.go' -type f -print0 | xargs -0 gofmt -w
go test ./...
go test -race ./...
IM_TEST_DATABASE_URL="$IM_DATABASE_URL" go test -run TestPostgresConcurrent -count=1 -v ./internal/store
docker build -t linli-im-server .
```

The tests cover memory and PostgreSQL message idempotency, multi-pool concurrent conversation sequencing, sync cursor continuity, group role/recall rules, real HTTP login, WebSocket connection, ACK, and duplicate WebSocket sends.

## Production checklist

- `IM_DEV_MODE` defaults to false and development mode can bind only to loopback. Startup rejects missing/weak JWT and admin secrets.
- Refresh tokens are one-time rotating sessions persisted in PostgreSQL; reuse, logout, and account bans revoke them.
- Redis Pub/Sub routes typing and call signaling across online instances. Call-signal publication fails closed, but SDP/ICE remains ephemeral with no replay; clients retry/deduplicate using `signalId`. Durable message sync is the recovery source only for persisted IM events, not WebRTC signaling.
- Production full mode requires HTTPS OTP and push webhooks with high-entropy bearer tokens. OTP gateway endpoints are `POST <base>/request` and `POST <base>/verify`; the push gateway receives a bounded outbox item with up to 20 registered devices.
- Production full mode also requires real STUN/TURN endpoints and a TURN credential. The example hostname is a placeholder, not a relay service.
- Terminate TLS at a trusted reverse proxy and only expose HTTPS/WSS.
- Restrict CORS and admin ingress at the edge; rotate `IM_ADMIN_KEY`.
- Run PostgreSQL backups and restore drills. Use a shared event bus for cross-instance WebSocket fan-out; durable sync remains the recovery path.
- `IM_PUSH_PROVIDER=noop|log` are development-only. The production webhook gateway owns APNs/FCM credentials, provider feedback, and token invalidation; the server owns durable leasing, retry, and dead-letter state.
- Production MinIO bootstraps a separate application credential with bucket-scoped least-privilege object access; the root credential is reserved for initialization, backup and administration. Upload completion verifies expected size, client SHA-256 and magic-byte MIME before marking an object ready. General-file malware scanning, archive inspection and media safety scanning remain external production dependencies and must be connected before allowing arbitrary files.
- Backups are created under a private `.incomplete-<timestamp>` directory with `umask 077`, checksummed, permission-tightened, and atomically renamed to the published timestamp only after all steps succeed. Incomplete directories are never valid restore points.
- Export `/metrics`, alert on readiness, errors, latency, queue pressure, and database/Redis availability.
