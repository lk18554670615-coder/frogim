#!/usr/bin/env bash
set -euo pipefail

APP_ROOT="${NEXACHAT_ROOT:-/opt/nexachat/current}"
DEFAULT_CONFIG="/data/linli-im/shared/config.env"
if [[ ! -f "$DEFAULT_CONFIG" && -f /opt/nexachat/shared/config.env ]]; then
  DEFAULT_CONFIG="/opt/nexachat/shared/config.env"
fi
CONFIG_FILE="${NEXACHAT_CONFIG:-$DEFAULT_CONFIG}"
CREDENTIAL_FILE="$(dirname "$CONFIG_FILE")/initial-credentials.txt"

# shellcheck disable=SC1090
source "$APP_ROOT/infra/scripts/load-env.sh"
load_env_file "$CONFIG_FILE"

base="https://$SERVER_IP"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

json_post() {
  local url="$1" token="$2" body="$3"
  if [[ -n "$token" ]]; then
    curl --fail --silent --show-error "$url" -H 'content-type: application/json' -H "authorization: Bearer $token" -d "$body"
  else
    curl --fail --silent --show-error "$url" -H 'content-type: application/json' -d "$body"
  fi
}

curl --fail --silent --show-error "$base/health" | jq -e '.status == "ok"' >/dev/null
echo "PASS health"

suffix="$(date +%H%M%S)"
phone_a="13910${suffix}1"
phone_b="13910${suffix}2"
if [[ "${E2E_REQUEST_CODES:-0}" == "1" ]]; then
  json_post "$base/v1/auth/code" "" "{\"phone\":\"$phone_a\"}" >/dev/null
  json_post "$base/v1/auth/code" "" "{\"phone\":\"$phone_b\"}" >/dev/null
fi
login_a="$(json_post "$base/v1/auth/login" "" "{\"phone\":\"$phone_a\",\"code\":\"$IM_DEV_OTP_CODE\",\"name\":\"验收甲\"}")"
login_b="$(json_post "$base/v1/auth/login" "" "{\"phone\":\"$phone_b\",\"code\":\"$IM_DEV_OTP_CODE\",\"name\":\"验收乙\"}")"
token_a="$(jq -er '.accessToken' <<<"$login_a")"
token_b="$(jq -er '.accessToken' <<<"$login_b")"
user_a="$(jq -er '.user.id' <<<"$login_a")"
user_b="$(jq -er '.user.id' <<<"$login_b")"
echo "PASS two-user OTP login"

friend="$(json_post "$base/v1/friend-requests" "$token_a" "{\"userId\":\"$user_b\",\"message\":\"端到端验收\"}")"
friend_id="$(jq -er '.id' <<<"$friend")"
json_post "$base/v1/friend-requests/$friend_id/accept" "$token_b" '{}' | jq -e '.accepted == true' >/dev/null
curl --fail --silent --show-error "$base/v1/friends" -H "authorization: Bearer $token_a" | jq -e --arg id "$user_b" '.items | any(.id == $id)' >/dev/null
echo "PASS friend request and accept"

conversation="$(json_post "$base/v1/conversations/direct" "$token_a" "{\"userId\":\"$user_b\"}")"
conversation_id="$(jq -er '.id' <<<"$conversation")"
client_id="e2e-$suffix"
message="$(json_post "$base/v1/conversations/$conversation_id/messages" "$token_a" "{\"clientMsgId\":\"$client_id\",\"type\":\"text\",\"body\":{\"text\":\"真实环境端到端消息\"}}")"
message_id="$(jq -er '.message.id' <<<"$message")"
message_seq="$(jq -er '.message.conversationSeq' <<<"$message")"
duplicate="$(json_post "$base/v1/conversations/$conversation_id/messages" "$token_a" "{\"clientMsgId\":\"$client_id\",\"type\":\"text\",\"body\":{\"text\":\"真实环境端到端消息\"}}")"
jq -e '.duplicate == true' <<<"$duplicate" >/dev/null
curl --fail --silent --show-error "$base/v1/conversations/$conversation_id/messages" -H "authorization: Bearer $token_b" | jq -e --arg id "$message_id" '.items | any(.id == $id)' >/dev/null
curl --fail --silent --show-error -X PUT "$base/v1/conversations/$conversation_id/read" \
  -H 'content-type: application/json' -H "authorization: Bearer $token_b" \
  -d "{\"seq\":$message_seq}" | jq -e --argjson seq "$message_seq" '.Seq == $seq or .seq == $seq' >/dev/null
json_post "$base/v1/messages/$message_id/recall" "$token_a" '{}' | jq -e '.recalled == true' >/dev/null
echo "PASS direct chat, idempotency, history, read and recall"

group="$(json_post "$base/v1/groups" "$token_a" "{\"name\":\"真实验收群\",\"memberIds\":[\"$user_b\"]}")"
group_id="$(jq -er '.id' <<<"$group")"
json_post "$base/v1/conversations/$group_id/messages" "$token_b" "{\"clientMsgId\":\"group-$suffix\",\"type\":\"text\",\"body\":{\"text\":\"群聊真实消息\"}}" | jq -e '.message.id != ""' >/dev/null
curl --fail --silent --show-error "$base/v1/groups/$group_id/members" -H "authorization: Bearer $token_a" | jq -e '.items | length == 2' >/dev/null
echo "PASS group create, members and message"

head -c 256 /dev/urandom > "$work/upload.bin"
presign="$(json_post "$base/v1/media/presign" "$token_a" '{"mime":"application/octet-stream","fileName":"e2e.bin","size":256}')"
media_id="$(jq -er '.mediaId' <<<"$presign")"
upload_url="$(jq -er '.uploadUrl' <<<"$presign")"
[[ "$upload_url" == "https://$SERVER_IP/nexachat-media/"* ]]
curl --fail --silent --show-error -X PUT -H 'content-type: application/octet-stream' --data-binary @"$work/upload.bin" "$upload_url" >/dev/null
if command -v sha256sum >/dev/null 2>&1; then
  upload_checksum="$(sha256sum "$work/upload.bin" | awk '{print $1}')"
else
  upload_checksum="$(shasum -a 256 "$work/upload.bin" | awk '{print $1}')"
fi
json_post "$base/v1/media/$media_id/complete" "$token_a" "{\"checksum\":\"$upload_checksum\"}" | jq -e '.status == "ready" and .size == 256' >/dev/null
echo "PASS HTTPS media presign, upload and completion"

curl --fail --silent --show-error "$base/v1/sync?after=0&limit=100" -H "authorization: Bearer $token_b" | jq -e '.cursor > 0 and (.events | length > 0)' >/dev/null
echo "PASS offline sync cursor"

ws_headers="$work/ws.headers"
ws_ticket="$(json_post "$base/v1/ws/ticket" "$token_a" '{}' | jq -er '.ticket')"
curl --silent --show-error --http1.1 --max-time 2 -D "$ws_headers" -o /dev/null \
  -H 'Connection: Upgrade' -H 'Upgrade: websocket' -H 'Sec-WebSocket-Version: 13' \
  -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' "$base/v1/ws?ticket=$ws_ticket" || true
grep -q '^HTTP/1.1 101' "$ws_headers"
echo "PASS one-time-ticket WSS upgrade"

admin_password="$(sed -n 's/^管理员密码：//p' "$CREDENTIAL_FILE")"
totp="$(python3 - "$IM_ADMIN_TOTP_SECRET" <<'PY'
import base64, hashlib, hmac, struct, sys, time
secret = sys.argv[1]
secret += '=' * ((8 - len(secret) % 8) % 8)
key = base64.b32decode(secret)
counter = struct.pack('>Q', int(time.time()) // 30)
digest = hmac.new(key, counter, hashlib.sha1).digest()
offset = digest[-1] & 15
code = (struct.unpack('>I', digest[offset:offset+4])[0] & 0x7fffffff) % 1000000
print(f'{code:06d}')
PY
)"
admin="$(json_post "$base/v1/admin/auth/login" "" "{\"email\":\"$IM_ADMIN_EMAIL\",\"password\":\"$admin_password\",\"totp\":\"$totp\"}")"
admin_token="$(jq -er '.accessToken' <<<"$admin")"
curl --fail --silent --show-error "$base/api/v1/admin/dashboard" -H "authorization: Bearer $admin_token" | jq -e 'type == "object"' >/dev/null
echo "PASS admin password, TOTP and dashboard"

echo "REMOTE_E2E_PASSED"
