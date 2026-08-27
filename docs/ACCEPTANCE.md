# Production acceptance gates

Software is considered launch-ready only when the applicable gates below have evidence. A successful build alone is not production approval.

## Functional journeys

- New user requests a code, signs in, edits profile and signs out.
- Two users find each other, complete a friend request and create a direct conversation.
- Messages survive duplicate send, process restart, network loss and reconnect.
- Read receipts, recall and reply remain ordered across both users.
- A group owner creates a group, adds/removes members, assigns roles and mutes a member.
- Blocked users cannot deliver new content to the blocker.
- A user reports content; an administrator reviews and resolves it with an audit record.
- Account deletion has a user-visible entry point and documented server workflow.

## Correctness

- Reusing `client_msg_id` never creates a second logical message.
- WuKong channel sequences are monotonically increasing without reuse.
- Offline clients resume from WuKong recent-conversation/channel sequence state and receive every missing message.
- The UI deduplicates live, offline and retry copies by WuKong message ID/client message number.
- Acknowledged messages remain available after service restart.
- Stream anchors, event IDs and event cursors remain idempotent; an event that arrives before its anchor is applied once after the anchor is stored.
- Stream history restores the authoritative WuKong event snapshot without storing a second message body in PostgreSQL.

## Reliability

- 24-hour load run has no unbounded memory or goroutine growth.
- Reconnect storms use jitter and bounded retry rates.
- PostgreSQL restore and point-in-time recovery are exercised.
- Loss of Redis does not lose acknowledged history.
- Slow or broken WuKong clients do not block healthy recipients; reconnect uses bounded jitter.

## Security and abuse

- Authentication, authorization, rate limiting and payload limits have negative tests.
- User and administrator credentials use separate authorization paths.
- Logs and metrics avoid tokens, verification codes and message bodies.
- Media upload validates declared type, actual type, size and ownership.
- Ban, block, report and appeal paths are tested as product journeys.

## Client quality

- iPhone small/large, Android phone and tablet layouts have no overflow.
- Light/dark modes meet contrast and 44-point tap target requirements.
- Empty, loading, offline, error and retry states are present.
- Text scaling and screen-reader labels cover primary journeys.

## Admin and deployment

- `/health` and `/ready` return their expected JSON contracts, and anonymous `/v2/config/auth` returns a boolean registration switch, an 8-16 character password minimum and the bcrypt-safe 72-byte maximum. A healthy process with a missing or incompatible public client contract is not release-ready.
- Production admin build contains no demo selector or embedded administrator credential.
- The local admin development proxy returns the server's `401` for an unauthenticated `/api/v2/admin/health` request. A proxy `502` is a failed real-data connection, not an acceptable empty state.
- Current Go dashboard/list/error envelopes pass contract tests without a blank render.
- User, report and audit lists consume server `q/status/cursor/limit` pagination and `{items,total,nextCursor}` without local re-pagination.
- Report actions map to `delete_message`, `ban_user`, `no_violation` or `dismiss`; a failed server action leaves the dialog open and never reports success.
- A 401 expires the admin session; destructive actions require confirmation and report failures.
- Operations can create, schedule, target, publish and withdraw announcements; scheduled publication is idempotent and optional offline push uses the outbox.
- Runtime business policy is grouped in system settings and audited. Secret values are never returned; restart-only infrastructure limits are visibly read-only.
- Dialog focus is trapped, Escape closes when safe, and focus returns to the trigger.
- Production Compose validates with real secret inputs and publishes only the TLS gateway publicly.
- HTTPS smoke verifies health, security headers and unauthenticated admin rejection.
- PostgreSQL, WuKong and MinIO backup completes off-host; an isolated structural drill and full product restore journey have recorded evidence.
- Prometheus targets, alert delivery, bounded logs and on-call ownership are verified.
