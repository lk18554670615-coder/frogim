# 邻里通讯运营后台

The operations console covers service overview, users, groups, reports, sensitive-word rules, health, audit and global settings. It supports the current Go response shapes and a future paginated `{items,total}` response without rendering failures.

## Security and environments

- Production builds always use the live API and do not render the demo selector.
- Administrators sign in with a named email, password and optional TOTP code. Only the short-lived JWT returned by the server is kept in `sessionStorage`; passwords and TOTP values are never persisted.
- A 401 expires the current session; nested API errors and request IDs are shown to the operator.
- UI permissions improve clarity, but the service remains the authorization boundary.
- Destructive actions require confirmation and show explicit success or failure feedback.

Demo data is available only while running Vite in development or tests. `VITE_ALLOW_DEMO=true` must never be used for a production build.

## Local development

```bash
npm ci
npm run dev
```

The Vite server listens on `http://127.0.0.1:4173` and proxies `/api` to `ADMIN_PROXY_TARGET` (default `http://127.0.0.1:8080`).

Supported build variables:

```dotenv
VITE_ADMIN_API_URL=/api/v1/admin
VITE_ALLOW_DEMO=false
```

Never set an administrator password, hash, TOTP seed or token in a `VITE_*` variable. Vite values are public build assets.

## Live API behavior

The adapter accepts direct data, `{data: ...}` and list `{items,total,nextCursor}` envelopes. Current server fields such as `name`, `banned`, `targetId`, `reporterId`, `lastMessageSeq` and flat dashboard counters are normalized into the UI domain types. User, report and audit lists send `q/status/cursor/limit` and render exactly the server page; the production console never slices a full response into fake local pages.

The console sends only the current server report actions: `delete_message`, `ban_user`, `no_violation`, and `dismiss`. Message deletion is offered only for message reports; user banning is offered only when the target can resolve to a user. Failed actions remain in the dialog and never emit a success notification. Every destructive action requires an operator reason. Timed bans are persisted by the service and expire automatically.

The message-governance index exposes metadata and lifecycle state only. It does not return or search private message bodies. Friendships, feedback, push queues, background tasks and administrator role boundaries are read-only operational views. See `../../docs/ADMIN_CONSOLE_ZH.md` for the complete route and permission matrix.

## Quality gates

```bash
npm run lint
npm test
npm run build
npm audit --audit-level=high
```

Tests cover demo journeys, live dashboard compatibility, response envelopes, pagination fallback, nested errors, authentication, accessible dialogs and destructive confirmations.

## Container

The image builds with Node.js 22 and runs as an unprivileged Nginx user on port 8080. It includes CSP, clickjacking protection, referrer/permissions policies, bounded proxy timeouts and hashed-asset caching.

```bash
docker build -t linli-im-admin .
docker run --rm -p 127.0.0.1:8088:8080 linli-im-admin
```
