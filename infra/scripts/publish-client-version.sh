#!/usr/bin/env bash
set -euo pipefail
umask 077

APP_ROOT="${NEXACHAT_ROOT:-/opt/nexachat/current}"
DEFAULT_CONFIG="/data/linli-im/shared/config.env"
if [[ ! -f "$DEFAULT_CONFIG" && -f /opt/nexachat/shared/config.env ]]; then
  DEFAULT_CONFIG="/opt/nexachat/shared/config.env"
fi
CONFIG_FILE="${NEXACHAT_CONFIG:-$DEFAULT_CONFIG}"
CREDENTIAL_FILE="$(dirname "$CONFIG_FILE")/initial-credentials.txt"

platform="${1:?usage: publish-client-version.sh PLATFORM MINIMUM LATEST DOWNLOAD_URL RELEASE_NOTES}"
minimum="${2:?missing minimum version}"
latest="${3:?missing latest version}"
download_url="${4:?missing download URL}"
release_notes="${5:?missing release notes}"

case "$platform" in
  android|ios|web|macos) ;;
  *) echo "unsupported platform: $platform" >&2; exit 2 ;;
esac
[[ "$minimum" =~ ^[0-9]+(\.[0-9]+){1,3}$ ]] || { echo "invalid minimum version" >&2; exit 2; }
[[ "$latest" =~ ^[0-9]+(\.[0-9]+){1,3}$ ]] || { echo "invalid latest version" >&2; exit 2; }
[[ "$download_url" == https://* ]] || { echo "download URL must use HTTPS" >&2; exit 2; }
[[ ${#release_notes} -le 2000 ]] || { echo "release notes are too long" >&2; exit 2; }
[[ -s "$CONFIG_FILE" && -s "$CREDENTIAL_FILE" ]] || { echo "server configuration or initial credentials are missing" >&2; exit 1; }
command -v curl >/dev/null
command -v jq >/dev/null
command -v python3 >/dev/null

# shellcheck disable=SC1090
source "$APP_ROOT/infra/scripts/load-env.sh"
load_env_file "$CONFIG_FILE"
base="https://$SERVER_IP"

admin_password="$(sed -n 's/^ADMIN_PASSWORD=//p' "$CREDENTIAL_FILE")"
if [[ -z "$admin_password" ]]; then
  admin_password="$(sed -n 's/^管理员密码：//p' "$CREDENTIAL_FILE")"
fi
[[ -n "$admin_password" ]] || { echo "administrator password is missing" >&2; exit 1; }

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

login_body="$(jq -nc \
  --arg email "$IM_ADMIN_EMAIL" \
  --arg password "$admin_password" \
  --arg totp "$totp" \
  'if $totp == "" then {email:$email,password:$password} else {email:$email,password:$password,totp:$totp} end')"
admin="$(curl --fail --silent --show-error "$base/v2/admin/auth/login" \
  -H 'content-type: application/json' -d "$login_body")"
admin_token="$(jq -er '.accessToken' <<<"$admin")"

reason="发布${platform}客户端${latest}"
payload="$(jq -nc \
  --arg minimum "$minimum" \
  --arg latest "$latest" \
  --arg notes "$release_notes" \
  --arg url "$download_url" \
  --arg reason "$reason" \
  '{minimumVersion:$minimum,latestVersion:$latest,forceUpdate:false,rolloutPercentage:100,releaseNotes:$notes,downloadUrl:$url,confirmed:true,reason:$reason}')"

updated="$(curl --fail --silent --show-error -X PUT "$base/v2/admin/client-versions/$platform" \
  -H 'content-type: application/json' \
  -H "authorization: Bearer $admin_token" \
  -d "$payload")"
jq -e --arg platform "$platform" --arg minimum "$minimum" --arg latest "$latest" --arg url "$download_url" \
  '.data.platform == $platform and .data.minimumVersion == $minimum and .data.latestVersion == $latest and .data.downloadUrl == $url' \
  <<<"$updated" >/dev/null

decision="$(curl --fail --silent --show-error \
  "$base/v2/config/version?platform=$platform&version=0.0.1&installId=release-verification")"
jq -e --arg minimum "$minimum" --arg latest "$latest" --arg url "$download_url" \
  '.data.updateAvailable == true and .data.forceUpdate == true and .data.minimumVersion == $minimum and .data.latestVersion == $latest and .data.downloadUrl == $url' \
  <<<"$decision" >/dev/null

echo "client version published and verified: platform=$platform minimum=$minimum latest=$latest download=$download_url"
