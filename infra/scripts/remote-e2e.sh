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
    curl --fail --silent --show-error "$url" -H 'content-type: application/json' -H 'x-client-platform: android' -H "authorization: Bearer $token" -d "$body"
  else
    curl --fail --silent --show-error "$url" -H 'content-type: application/json' -H 'x-client-platform: android' -d "$body"
  fi
}

curl --fail --silent --show-error "$base/health" | jq -e '.status == "ok"' >/dev/null
echo "PASS health"

suffix="$(date +%H%M%S)"
phone_a="13910${suffix}1"
phone_b="13910${suffix}2"
if [[ "${E2E_REQUEST_CODES:-0}" == "1" ]]; then
  json_post "$base/v2/auth/code" "" "{\"phone\":\"$phone_a\"}" >/dev/null
  json_post "$base/v2/auth/code" "" "{\"phone\":\"$phone_b\"}" >/dev/null
fi
login_a="$(json_post "$base/v2/auth/login" "" "{\"phone\":\"$phone_a\",\"code\":\"$IM_DEV_OTP_CODE\",\"name\":\"验收甲\"}")"
login_b="$(json_post "$base/v2/auth/login" "" "{\"phone\":\"$phone_b\",\"code\":\"$IM_DEV_OTP_CODE\",\"name\":\"验收乙\"}")"
token_a="$(jq -er '.accessToken' <<<"$login_a")"
token_b="$(jq -er '.accessToken' <<<"$login_b")"
user_a="$(jq -er '.user.id' <<<"$login_a")"
user_b="$(jq -er '.user.id' <<<"$login_b")"
jq -e '.imSession.uid != "" and .imSession.token != "" and .imSession.tcpUrl != "" and .imSession.wsUrl != "" and .imSession.sdk == "wukongimfluttersdk"' <<<"$login_a" >/dev/null
jq -e '.imSession.uid != "" and .imSession.token != ""' <<<"$login_b" >/dev/null
echo "PASS two-user OTP login and WuKongIM sessions"

friend="$(json_post "$base/v2/contacts/requests" "$token_a" "{\"userId\":\"$user_b\",\"message\":\"端到端验收\"}")"
friend_id="$(jq -er '.id' <<<"$friend")"
json_post "$base/v2/contacts/requests/$friend_id/accept" "$token_b" '{}' | jq -e '.accepted == true' >/dev/null
curl --fail --silent --show-error "$base/v2/contacts/friends" -H "authorization: Bearer $token_a" | jq -e --arg id "$user_b" '.items | any(.id == $id)' >/dev/null
echo "PASS friend request and accept"

conversation="$(json_post "$base/v2/channels/direct" "$token_a" "{\"userId\":\"$user_b\"}")"
conversation_id="$(jq -er '.id' <<<"$conversation")"
channel="$(json_post "$base/v2/im/datasource/channel" "$token_a" "{\"channelId\":\"$conversation_id\",\"channelType\":1}")"
jq -e --arg id "$conversation_id" '.item.channel_id == $id' <<<"$channel" >/dev/null
echo "PASS direct channel provisioning and datasource"

group="$(json_post "$base/v2/channels/groups" "$token_a" "{\"name\":\"真实验收群\",\"memberIds\":[\"$user_b\"]}")"
group_id="$(jq -er '.id' <<<"$group")"
curl --fail --silent --show-error "$base/v2/channels/groups/$group_id/members" -H "authorization: Bearer $token_a" | jq -e '.items | length == 2' >/dev/null
members="$(json_post "$base/v2/im/datasource/members" "$token_a" "{\"channelId\":\"$group_id\",\"channelType\":2,\"version\":0,\"limit\":100}")"
jq -e --arg a "$user_a" --arg b "$user_b" '(.items | any(.member_uid == $a)) and (.items | any(.member_uid == $b))' <<<"$members" >/dev/null
echo "PASS group create, members and datasource"

head -c 256 /dev/urandom > "$work/upload.bin"
presign="$(json_post "$base/v2/media/presign" "$token_a" '{"mime":"application/octet-stream","fileName":"e2e.bin","size":256}')"
media_id="$(jq -er '.mediaId' <<<"$presign")"
upload_url="$(jq -er '.uploadUrl' <<<"$presign")"
[[ "$upload_url" == "https://$SERVER_IP/nexachat-media/"* ]]
curl --fail --silent --show-error -X PUT -H 'content-type: application/octet-stream' --data-binary @"$work/upload.bin" "$upload_url" >/dev/null
if command -v sha256sum >/dev/null 2>&1; then
  upload_checksum="$(sha256sum "$work/upload.bin" | awk '{print $1}')"
else
  upload_checksum="$(shasum -a 256 "$work/upload.bin" | awk '{print $1}')"
fi
json_post "$base/v2/media/$media_id/complete" "$token_a" "{\"checksum\":\"$upload_checksum\"}" | jq -e '.status == "ready" and .size == 256' >/dev/null
echo "PASS HTTPS media presign, upload and completion"

compose=(docker compose --env-file "$CONFIG_FILE")
if [[ "${WUKONG_DEV_PUBLIC_REPLACEMENT:-false}" == "true" ]]; then
  compose+=(-f "$APP_ROOT/infra/compose.ip.yaml" -f "$APP_ROOT/infra/compose.wukong.production.yaml" -f "$APP_ROOT/infra/compose.ip.wukong-dev.yaml")
else
  compose+=(-f "$APP_ROOT/infra/compose.ip.yaml" -f "$APP_ROOT/infra/compose.ip.production.yaml" -f "$APP_ROOT/infra/compose.wukong.production.yaml")
fi
"${compose[@]}" \
  --profile probe run --rm --no-deps wukong-probe \
  -api http://wukongim:5001 -manager-api http://wukongim:5300 -tcp tcp://wukongim:5100 \
  -manager-token "$IM_WUKONG_MANAGER_TOKEN" \
  -business-api http://server:8080 -otp "$IM_DEV_OTP_CODE" -timeout 90s \
  | jq -e '.managerApi and .handshake and .sendAck and .receive and .offlineSync and .historySync and .groupSync and .businessAuth and .policyPluginRegistered and .policyAllowedMember and .policyDeniedOutsider' >/dev/null
echo "PASS fixed WuKongIM handshake, ACK, sync, CMD, datasource and policy probe"

admin_password="$(sed -n 's/^ADMIN_PASSWORD=//p' "$CREDENTIAL_FILE")"
if [[ -z "$admin_password" ]]; then
  admin_password="$(sed -n 's/^管理员密码：//p' "$CREDENTIAL_FILE")"
fi
totp=""
if [[ -n "${IM_ADMIN_TOTP_SECRET:-}" ]]; then
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
fi
if [[ -n "$totp" ]]; then
  admin_body="{\"email\":\"$IM_ADMIN_EMAIL\",\"password\":\"$admin_password\",\"totp\":\"$totp\"}"
else
  admin_body="{\"email\":\"$IM_ADMIN_EMAIL\",\"password\":\"$admin_password\"}"
fi
admin="$(json_post "$base/v2/admin/auth/login" "" "$admin_body")"
admin_token="$(jq -er '.accessToken' <<<"$admin")"
curl --fail --silent --show-error "$base/api/v2/admin/dashboard" -H "authorization: Bearer $admin_token" | jq -e 'type == "object"' >/dev/null
echo "PASS admin password, TOTP and dashboard"

echo "REMOTE_E2E_PASSED"
