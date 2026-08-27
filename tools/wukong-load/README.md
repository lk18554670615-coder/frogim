# WuKongIM performance gate

This tool uses the official TCP client from the fixed WuKongIM server commit. It provisions unique test users through the internal API, opens the requested number of real encrypted TCP sessions, and sends paced direct messages while measuring successful server ACK latency and peer delivery.

For message runs, every sender/receiver pair is first created through the business API together with its friendship and direct conversation. This is mandatory by default so the production PolicyPlugin evaluates real PostgreSQL facts; the JSON report must say `businessPolicyProvisioned: true`. The preferred multi-pair path uses the development-only internal fixture and requires `IM_WUKONG_POLICY_SECRET`; that route is not registered when `IM_DEV_MODE=false`. The public OTP fallback is limited to five pairs so the generator cannot weaken or evade login controls. Never point the generator at production business data.

The release target is:

- 10,000 connected clients;
- 1,000 messages/second;
- ACK P95 no more than 300 ms and P99 no more than 800 ms;
- no rejected, missing, or acknowledged-but-undelivered messages.

Run the full gate only in an approved disposable load environment. It creates test identities and intentionally consumes substantial CPU, memory, file descriptors, network and disk. The target command is:

```bash
wukong-load \
  -api http://wukongim:5001 \
  -tcp tcp://wukongim:5100 \
  -manager-token "$IM_WUKONG_MANAGER_TOKEN" \
  -business-api http://server:8080 \
  -otp "$WUKONG_LOAD_OTP" \
  -connections 10000 \
  -connection-workers 200 \
  -connection-attempts 3 \
  -connection-retry-delay 100ms \
  -message-pairs 20 \
  -messages-per-second 1000 \
  -duration 1m \
  -max-p95 300ms \
  -max-p99 800ms
```

The message rate is an aggregate system target. By default it is distributed
round-robin over 20 independent sender/receiver pairs, all provisioned through
the business API with real friendship and direct-channel facts. This avoids
mistaking a single-user/channel hot-spot limit for aggregate server capacity;
set `-message-pairs` explicitly in preserved release evidence.

The report distinguishes final `connectionFailures` from `connectionRetries`. A
client is counted as connected only after an authenticated CONNACK; bounded
retries model the official clients' recovery from a transient handshake timeout
without hiding the number of extra attempts. Release evidence still requires
all 10,000 clients to be connected.

The process prints one JSON report and exits non-zero when any threshold fails. A small smoke run validates the tool but is not release evidence.
