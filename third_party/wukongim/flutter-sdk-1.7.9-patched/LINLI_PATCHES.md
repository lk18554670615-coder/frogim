# WuKongIM Flutter SDK 1.7.9 local patch

This directory is an auditable source copy of the official
`wukongimfluttersdk` 1.7.9 package.

- Upstream archive: `https://pub.dev/api/archives/wukongimfluttersdk-1.7.9.tar.gz`
- Upstream archive SHA-256: `b6191a86cd1e4caacaa4652e95709310eb1493f159fee65e1dd53c2a3ff9e80a`
- License: Apache-2.0; the upstream `LICENSE` is retained in this directory.
- Public version/API/protocol/database schema: unchanged.

## Patch 1: immediate transport termination handling

File: `lib/manager/connect_manager.dart`

The official 1.7.9 `_WKSocket.listen` logs `onError`, ignores the supplied
termination callback, and comments the same callback out of `onDone`. A clean
server restart therefore leaves the client in a false connected state until
the 60-second heartbeat notices a missed pong.

The local patch calls the SDK's existing termination callback exactly once for
both `onError` and `onDone`, resets the listener flag, and enables
`cancelOnError`. The existing connection manager then performs its official
1.5-second reconnect path. No frame, token, ACK, retry, device, database, or
DataSource behavior is changed.

The manager also assigns every connection attempt a monotonically increasing
generation and keeps at most one delayed reconnect timer. Callbacks from a
Socket that was intentionally replaced, a superseded asynchronous
`Socket.connect`, or a completed disconnect are ignored. This prevents a stale
`onDone` callback from repeatedly closing a newer master-device connection.

The upstream singleton socket factory cleared the prior wrapper's inner
socket, but then returned that same wrapper instead of storing the newly
connected socket. Any reconnect therefore opened TCP successfully but sent no
CONNECT frame; the server reset it at its five-second handshake timeout. The
patch now destroys the old wrapper and replaces it with a wrapper around the
new socket before listening or writing the first frame.

Validation requires an Android/iOS target-device test that stops and restarts
the pinned WuKongIM server, observes a new `ConnackPacket` within 10 seconds,
obtains a successful message ACK after recovery, and then holds one stable
connection without recurring remote-close/re-CONNACK cycles.

## Patch 2: pinned v2.2.5 message event packets

Files: `lib/proto/proto.dart`, `lib/proto/packet.dart`,
`lib/manager/connect_manager.dart`, `lib/manager/event_manager.dart`, and
`lib/wkim.dart`.

WuKongIM Server v2.2.5 emits its message-event protocol as frame type `12`;
types `10` and `11` are `SUB` and `SUBACK`.
The official Flutter SDK 1.7.9 rejects that packet index before decoding it,
while the pinned JS SDK already exposes the same event mechanism. This patch
implements only the wire layout defined by the pinned server source: event ID,
event type, unsigned 64-bit timestamp, and the remaining JSON object. It then
dispatches the decoded packet through `WKIM.shared.eventManager`.

No AI Agent behavior is implemented. The application accepts only the public
general-text stream lifecycle (`stream.delta`, `stream.snapshot`,
`stream.close`, `stream.error`, `stream.cancel`, and `stream.finish`) and
validates those payloads in the Go business service.

## Patch 3: pinned stream setting and legacy metadata

Files: `lib/proto/proto.dart`, `lib/entity/msg.dart`, and
`lib/manager/connect_manager.dart`.

The upstream Flutter decoder uses stream bit 2 and a `no/uint32-seq/flag`
receive layout. The pinned Go protocol defines `SettingStream = 1 << 1` and,
for negotiated protocol versions 2–4, encodes `flag/string no/uint64 stream
id`; version 5 removes those legacy receive fields. The patch matches that
wire layout, retaining the upstream Dart `streamSeq` property name as a
lossless holder for the uint64 stream ID, and exposes the values on
`WKMsg`/`WKSyncMsg`.

Every patched file and its upstream counterpart is SHA-256 locked in
`third_party/wukongim/versions.lock.json`. Run
`infra/scripts/verify-wukong-flutter-patch.sh` after dependency changes.
