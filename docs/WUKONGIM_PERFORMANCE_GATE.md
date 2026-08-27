# WuKongIM performance gate

## Release thresholds

The migration release gate is 10,000 concurrent long connections and 1,000 acknowledged messages/second, with ACK P95 no more than 300 ms, ACK P99 no more than 800 ms, zero rejected/missing acknowledged messages, CPU continuously below 75%, memory below 80%, disk below 70%, and no continuously growing Outbox/Webhook queue.

The Go metrics endpoint exposes `im_wukong_outbox_pending`, `im_wukong_outbox_oldest_seconds`, `im_wukong_outbox_failed`, `im_wukong_webhook_pending`, `im_wukong_webhook_oldest_seconds`, and `im_wukong_webhook_failed`. Prometheus rules alert on sustained synchronization/webhook backlog and surface both permanently failed business mutations and quarantined webhook events; release evidence must retain these series throughout the ramp and requires both failed gauges to remain zero.

`tools/wukong-load` uses the official encrypted TCP client from fixed server commit `a888f89533d0e7d1b2030e06504ca97f1ad891d4`. It provisions unique load identities, opens real sessions, sends paced direct messages, verifies nonzero WuKong message IDs/sequences and peer delivery, prints one JSON report, and exits nonzero when a threshold fails. Its container build uses the locked Go and Alpine image digests.

Message runs also provision every sender and receiver through the business service, create their friendship and direct conversation, and require the report fields `businessPolicyProvisioned=true` and `businessProvisionMode=internal-dev-fixture`. This makes the real `wk.plugin.im-policy` query PostgreSQL on every send. Multi-pair release runs require `WUKONG_BUSINESS_API_URL` and `IM_WUKONG_POLICY_SECRET`; the fixture route is registered only under `IM_DEV_MODE=true`, accepts only private/loopback peers, and uses the independent WuKong secret. Production has no such route. The real friendship transaction still emits its durable Outbox record; before returning, the fixture idempotently reapplies the pair allowlists so measurement starts from a settled WuKong projection rather than racing asynchronous setup. The OTP path remains a maximum-five-pair smoke fallback; `-require-business-policy=false` is only for an isolated upstream server where the policy plugin is absent and cannot be used as release evidence.

Run it only in a disposable environment whose PostgreSQL, WuKong and MinIO data will be rebuilt afterward. The Compose service is behind the explicit `loadtest` profile and defaults to help output so enabling the profile cannot accidentally start a 10k run.

```bash
docker compose --env-file .env.load \
  -f infra/compose.production.yaml \
  -f infra/compose.wukong.production.yaml \
  --profile loadtest run --rm wukong-load \
  -connections 10000 \
  -connection-workers 200 \
  -message-pairs 20 \
  -messages-per-second 1000 \
  -duration 1m \
  -warmup 30s \
  -ack-wait 30s \
  -max-p95 300ms \
  -max-p99 800ms \
  -minimum-ack-ratio 1
```

`.env.load` belongs only to the disposable load environment and must provide the environment's WuKong policy secret. Never enable the fixture or run this generator against production business data.

The 1,000 msg/s target is measured as aggregate system throughput. The load tool distributes messages over independently provisioned business pairs (20 by default), records the pair count in JSON, and still routes every message through the production PolicyPlugin. A separate one-pair hot-spot run may be retained as an overload characterization, but it is not substituted for the aggregate release gate.

Before the full run, verify at least 1 TB usable data capacity, public/internal bandwidth, host and container file-descriptor limits, and a separate load-generator host. Ramp through 1k/100, 5k/500 and 10k/1k stages. Stop when CPU remains above 85%, memory exceeds 90%, disk exceeds 80%, errors appear, or any queue grows for five consecutive minutes. Preserve the JSON report, Prometheus snapshot, host/container resource series and exact image/config digests.

## Local tool smoke evidence

On 2026-08-11 the fixed WuKong image was started in an isolated temporary container with no business data. The locked load image opened 20/20 connections and sent 200 messages at 100 messages/second over two seconds:

| Metric | Result |
|---|---:|
| Connection failures | 0 |
| Client send errors | 0 |
| Successful ACKs | 200/200 |
| Rejected/missing ACKs | 0/0 |
| Receiver messages | 200/200 |
| ACK P95 | 7.13 ms |
| ACK P99 | 21.54 ms |
| Observed ACK rate | 100/s |

This proves the generator, ACK correlation, percentile calculation and failure gate work. It is not evidence that the release capacity target has been met.

After enabling the patched server and mandatory PolicyPlugin, the original internal-only provisioning was correctly rejected 200/200 times because no PostgreSQL friendship/conversation existed. The generator was then corrected to provision the active pair through the business API and to fail closed when that step is absent. On 2026-08-12 the corrected local smoke passed with `businessPolicyProvisioned=true`: 20/20 connections, 200/200 successful ACKs, 200/200 receiver messages, no rejection or missing ACK, exactly 100 ACK/s, P95 15.40 ms and P99 24.70 ms. This verifies the small smoke through the real policy path, not the 10k/1k capacity target.

After rebuilding the final `linli.3` candidate and schema 44 business service on 2026-08-12, the same policy-backed smoke was rerun: 20/20 connections, 200/200 ACKs and receiver messages, zero rejection/send error/unacknowledged message, exactly 100 ACK/s, P95 27.50 ms and P99 38.18 ms. Webhook pending, failed, and retained completed payload counts all remained zero afterward. This supersedes the earlier patched-image smoke for candidate verification, while still not representing the 10k/1k release-capacity gate.

On 2026-08-12 the local aggregate ramp exposed and corrected two measurement defects before producing a passing result. First, the public 300/minute HTTP limiter was incorrectly applied to the private PolicyPlugin endpoint, causing reason 15 (`ReasonSystemError`) under load; WuKong internal traffic now has an independent configurable 120,000/minute budget while retaining private-peer and secret checks. Second, the original generator sent every message over one user/channel and measured a hot spot rather than aggregate system throughput. The corrected generator provisions 20 independent business pairs through a development-only internal fixture, leaves the durable friendship Outbox path active, stabilizes the idempotent WuKong allowlists, and distributes the aggregate rate round-robin.

The corrected local ramp produced these results:

| Stage | Result | ACK and delivery | ACK latency | Sampled service resources |
|---|---|---|---|---|
| 1,000 connections / 1,000 msg/s / 20s | pass | 20,000/20,000 ACK, 20,000/20,000 delivered, zero rejected/unacknowledged | P95 63.60 ms, P99 76.89 ms | after-run WuKongIM 396 MiB, Go service 31 MiB; queues zero |
| 5,000 connections / 500 msg/s / 20s | pass | 10,000/10,000 ACK, 10,000/10,000 delivered, zero rejected/unacknowledged | P95 33.72 ms, P99 51.15 ms | in-run WuKongIM 477 MiB, Go service 31 MiB; sampled CPU below 20%; queues zero |

On 2026-08-12 the same policy-backed generator completed the full numerical
target on the local development host: 10,000/10,000 authenticated connections,
30,000/30,000 successful ACKs and peer deliveries over 30 seconds at exactly
1,000 ACK/s, with zero rejected or unacknowledged messages. Connection P95/P99
were 48.51/67.93 ms and ACK P95/P99 were 62.70/73.42 ms. Outbox and Webhook
pending gauges remained zero and the run did not create a new permanent Outbox
failure. This is strong engineering-capacity evidence, but it remains a
same-host run on a previously used database and therefore does not replace the
fresh-environment, independent-generator production release gate.

Future runs should use `make wukong-message-load`. The wrapper preserves the
load JSON, complete log, before/after business metrics, sampled container
resources, Compose state, Docker capacity and exact container/image inspection
under `build/qa/wukong-load-<UTC timestamp>/`. Set
`WUKONG_LOAD_EVIDENCE_KIND=release-candidate` only on a fresh disposable
database; that mode additionally requires the permanent-failure gauge to be
zero.

These are same-host development results, not the formal 10,000/1,000 release proof. The local database also retains intentionally generated historical permanent-failure rows from negative Outbox tests, now visible through `im_wukong_outbox_failed`; a formal run must use a fresh disposable database and retain zero failed gauges throughout. The target production server currently has only a 200 GB root disk, below the explicit 1 TB gate, and no independent load-generator host has been assigned.

The target was replaced on 2026-08-12 after explicit development-stage cutover
authorization. Its `nexachat-ip` Compose project now runs only the WuKongIM
stack: WuKongIM, LiveKit, the Go business service, Flutter Web, React admin,
PostgreSQL, Redis, MinIO, Prometheus and Caddy. Legacy Coturn, containers,
images, releases and mounted runtime data were removed. Public TCP (`5100`),
WSS, HTTPS API/Web/admin and LiveKit TCP/UDP passed reachability and protocol
probes. A post-cutover backup restored in isolation with 63 tables, 7 critical
tables, schema 45, 342 constraints and 155 WuKong files. The host still has a
200 GB root disk and there is no independent load-generator host, so this is a
development acceptance deployment, not the deferred formal 1 TB production
capacity gate.

Later on 2026-08-12 the numerical target was also exercised on the target
server without writing load identities into the public development database.
An isolated `linli-load-20260812` Compose project used separate PostgreSQL,
Redis, MinIO and WuKongIM storage, fixed production images and the real policy
plugin. It passed the 1,000/100, 5,000/500 and 10,000/1,000 ramps. The final
stage established 10,000/10,000 authenticated connections (three transient
handshake retries), then produced 30,000/30,000 ACKs and peer deliveries over
30 seconds at exactly 1,000 ACK/s, with zero rejected or unacknowledged
messages. ACK P95/P99 were 5.18/7.67 ms. Outbox and Webhook pending/failed
gauges were all zero. On the eight-core host the peak Docker CPU samples
normalize to 18.91% for WuKongIM, 10.71% for the Go service and 16.91% for
PostgreSQL; WuKongIM used 305.8 MiB at its CPU peak. The public stack remained
healthy and sampled only 3.76% WuKongIM and 0.71% Go CPU. The isolated
containers, networks and data were deleted after the run; evidence and a full
SHA-256 manifest remain at
`/data/linli-im/load-evidence/20260812-server-ramp`.

This closes the target-server engineering-capacity number. It still does not
claim the deferred formal gate because the user explicitly deferred the 1 TiB
disk and independent load-generator requirements.

## LiveKit 9-person media gate

`infra/scripts/livekit-load-test.sh` uses the official `livekit-cli` image v2.18.2 at source commit `6eec7324a8f7b24f62569a758878e762fed9f886` and the locked Linux/amd64 image digest. It first creates rooms explicitly because the fixed server intentionally has `auto_create: false`, then starts synthetic WebRTC publishers/subscribers concurrently. The gate requires every active room snapshot to report `maxParticipants=9`, all participants present, all expected video tracks subscribed, zero subscriber errors and zero packet loss. It always deletes its rooms and stores the room snapshot, per-room reports, Prometheus snapshot and container resource sample under `build/qa/`.

The fixed CLI source defines the `medium` publisher as 640×360 at 20 fps. The test disables simulcast and uses the official `3x3` layout; the default speaker layout intentionally subscribes to only six remote tracks and therefore cannot prove the nine-person product layout.

Run against the local fixed stack:

```bash
make livekit-media-load
```

On 2026-08-12 the local fixed LiveKit v1.13.5 container passed 10 concurrent rooms for 30 seconds. Each room had eight 360p/20fps video publishers and one subscriber (90 real WebRTC participants total), and each subscriber received 8/8 tracks with zero reported packet loss and zero subscriber errors. At the captured instant the LiveKit container reported 26.42% CPU, 228.7 MiB memory, 244 MB received and 171 MB sent; Prometheus reported 90 active participants and zero accumulated incoming/outgoing video packet-loss percentage. Evidence is in `build/qa/livekit-load-20260811T201615Z/`.

The gate was repeated after the final Windows/Git Bash portability fix on 2026-08-12 with the same 10 rooms, 90 participants, 30-second duration and 8/8 subscribed tracks per room. It again reported zero packet loss and zero subscriber errors; the captured container sample was 54.50% CPU and 466 MiB memory, both below the release thresholds. Evidence is in `build/qa/livekit-load-20260812T021839Z/`.

This exceeds the requested 360p/15fps frame rate in a local same-host test, but it does not replace cross-network Android/iOS media, screen-share, background/lock-screen and production-host capacity validation.
