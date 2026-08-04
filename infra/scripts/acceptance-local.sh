#!/usr/bin/env bash
set -euo pipefail

# 本脚本只用于本机 Docker 开发环境，会创建并在结束时注销临时账号。
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
SERVER_URL="${SERVER_URL:-http://127.0.0.1:8080}"
OTP_CODE="${IM_DEV_OTP_CODE:-123456}"
case "$SERVER_URL" in
  http://127.0.0.1:* | http://localhost:* | http://\[::1\]:*) ;;
  *)
    echo "拒绝执行：acceptance-local 只能连接本机 HTTP 开发环境。" >&2
    exit 2
    ;;
esac
TMP_DIR="$(mktemp -d)"
RESPONSE_FILE="$TMP_DIR/response.json"
token_a=""
token_b=""

step="初始化"
request() {
  local method="$1"
  local path="$2"
  local token="${3:-}"
  local body="${4:-}"
  local args=(--silent --show-error --output "$RESPONSE_FILE" --write-out '%{http_code}' --request "$method" "$SERVER_URL$path")
  if [[ -n "$token" ]]; then
    args+=(--header "Authorization: Bearer $token")
  fi
  if [[ -n "$body" ]]; then
    args+=(--header 'Content-Type: application/json' --data "$body")
  fi
  curl "${args[@]}"
}

cleanup() {
  local exit_status=$?
  local token
  for token in "$token_a" "$token_b"; do
    if [[ -n "$token" ]]; then
      curl --silent --show-error --output /dev/null --request POST \
        --header "Authorization: Bearer $token" \
        --header 'Content-Type: application/json' --data '{}' \
        "$SERVER_URL/v1/users/me/deletion/code" || true
      curl --silent --show-error --output /dev/null --request DELETE \
        --header "Authorization: Bearer $token" \
        --header 'Content-Type: application/json' \
        --data "$(jq -nc --arg code "$OTP_CODE" '{code:$code}')" \
        "$SERVER_URL/v1/users/me" || true
    fi
  done
  rm -rf "$TMP_DIR"
  return "$exit_status"
}
trap cleanup EXIT

expect_status() {
  local expected="$1"
  local actual="$2"
  if [[ "$actual" != "$expected" ]]; then
    echo "验收失败：${step}，期望 HTTP ${expected}，实际 ${actual}" >&2
    exit 1
  fi
}

json_value() {
  jq -er "$1" "$RESPONSE_FILE"
}

assert_json_equal() {
  local filter="$1"
  local expected="$2"
  local actual
  if ! actual="$(jq -r "$filter" "$RESPONSE_FILE")"; then
    echo "验收失败：${step}，响应缺少可验证字段 ${filter}" >&2
    exit 1
  fi
  if [[ "$actual" != "$expected" ]]; then
    echo "验收失败：${step}，字段 ${filter} 期望 ${expected}，实际 ${actual}" >&2
    exit 1
  fi
}

assert_jq_equal() {
  local expected="$1"
  local filter="$2"
  shift 2
  local actual
  if ! actual="$(jq -r "$@" "$filter" "$RESPONSE_FILE")"; then
    echo "验收失败：${step}，响应无法执行断言 ${filter}" >&2
    exit 1
  fi
  if [[ "$actual" != "$expected" ]]; then
    echo "验收失败：${step}，断言 ${filter} 期望 ${expected}，实际 ${actual}" >&2
    exit 1
  fi
}

curl --fail --silent --show-error --retry 10 --retry-delay 1 "$SERVER_URL/ready" >/dev/null
# 重复执行验收时，只清理本机 Docker 中的短时限流计数；不触碰会话、消息或其他 Redis 数据。
if command -v docker >/dev/null 2>&1 && docker compose -f "$ROOT_DIR/infra/compose.yaml" ps --status running redis >/dev/null 2>&1; then
  docker compose -f "$ROOT_DIR/infra/compose.yaml" exec -T redis sh -c '
    keys="$(redis-cli --no-auth-warning -a "$REDIS_PASSWORD" --scan --pattern "im:rate:*")"
    [ -z "$keys" ] || printf "%s\n" "$keys" | xargs redis-cli --no-auth-warning -a "$REDIS_PASSWORD" del >/dev/null
  '
fi

suffix="$(date +%s | tail -c 9)"
phone_a="191${suffix}"
phone_b="192${suffix}"
handle_a="qa_a_${suffix}"
handle_b="qa_b_${suffix}"
password='LocalQaPass_2026!'

step="注册用户 A"
status="$(request POST /v1/auth/register '' "$(jq -nc --arg phone "$phone_a" --arg code "$OTP_CODE" --arg password "$password" '{phone:$phone,code:$code,password:$password,name:"验收用户甲"}')")"
expect_status 200 "$status"
token_a="$(json_value '.accessToken')"
user_a="$(json_value '.user.id')"

step="注册用户 B"
status="$(request POST /v1/auth/register '' "$(jq -nc --arg phone "$phone_b" --arg code "$OTP_CODE" --arg password "$password" '{phone:$phone,code:$code,password:$password,name:"验收用户乙"}')")"
expect_status 200 "$status"
token_b="$(json_value '.accessToken')"
user_b="$(json_value '.user.id')"

step="验证码登录、密码登录、重置密码与退出"
status="$(request POST /v1/auth/code '' "$(jq -nc --arg phone "$phone_a" '{phone:$phone}')")"
expect_status 202 "$status"
status="$(request POST /v1/auth/login '' "$(jq -nc --arg phone "$phone_a" --arg code "$OTP_CODE" '{phone:$phone,code:$code}')")"
expect_status 200 "$status"
otp_token_a="$(json_value '.accessToken')"
otp_refresh_a="$(json_value '.refreshToken')"
status="$(request POST /v1/auth/logout "$otp_token_a" "$(jq -nc --arg refresh "$otp_refresh_a" '{refreshToken:$refresh}')")"
expect_status 204 "$status"
status="$(request POST /v1/auth/password-login '' "$(jq -nc --arg phone "$phone_a" --arg password "$password" '{phone:$phone,password:$password}')")"
expect_status 200 "$status"
status="$(request POST /v1/auth/password/reset-code '' "$(jq -nc --arg phone "$phone_a" '{phone:$phone}')")"
expect_status 202 "$status"
new_password='LocalQaPass_2026_Changed!'
status="$(request POST /v1/auth/password/reset '' "$(jq -nc --arg phone "$phone_a" --arg code "$OTP_CODE" --arg password "$new_password" '{phone:$phone,code:$code,password:$password}')")"
expect_status 204 "$status"
status="$(request POST /v1/auth/password-login '' "$(jq -nc --arg phone "$phone_a" --arg password "$password" '{phone:$phone,password:$password}')")"
expect_status 401 "$status"
status="$(request POST /v1/auth/password-login '' "$(jq -nc --arg phone "$phone_a" --arg password "$new_password" '{phone:$phone,password:$password}')")"
expect_status 200 "$status"
token_a="$(json_value '.accessToken')"

step="更新双方公开资料"
status="$(request PATCH /v1/users/me "$token_a" "$(jq -nc --arg handle "$handle_a" '{handle:$handle,signature:"端到端验收账号"}')")"
expect_status 200 "$status"
status="$(request PATCH /v1/users/me "$token_b" "$(jq -nc --arg handle "$handle_b" '{handle:$handle,signature:"端到端验收账号"}')")"
expect_status 200 "$status"

step="按邻里号搜索"
status="$(request GET "/v1/users/search?q=$handle_b&by=handle" "$token_a")"
expect_status 200 "$status"
assert_json_equal '.items[0].id' "$user_b"

step="好友申请与接受"
status="$(request POST /v1/friend-requests "$token_a" "$(jq -nc --arg user "$user_b" '{userId:$user,message:"本地自动化验收",source:"search"}')")"
expect_status 201 "$status"
friend_request_id="$(json_value '.id')"
status="$(request POST "/v1/friend-requests/$friend_request_id/accept" "$token_b" '{}')"
expect_status 200 "$status"

step="创建单聊"
status="$(request POST /v1/conversations/direct "$token_a" "$(jq -nc --arg user "$user_b" '{userId:$user}')")"
expect_status 201 "$status"
direct_id="$(json_value '.id')"

step="会话左滑置顶、免打扰、未读与删除后恢复"
status="$(request PATCH "/v1/conversations/$direct_id/preferences" "$token_a" '{"pinned":true,"notificationsMuted":true,"manualUnread":true}')"
expect_status 204 "$status"
status="$(request GET /v1/conversations "$token_a")"
expect_status 200 "$status"
assert_jq_equal true '.items[] | select(.conversation.id==$conversation) | .membership.pinned' --arg conversation "$direct_id"
assert_jq_equal true '.items[] | select(.conversation.id==$conversation) | .membership.notificationsMuted' --arg conversation "$direct_id"
assert_jq_equal true '.items[] | select(.conversation.id==$conversation) | .membership.manualUnread' --arg conversation "$direct_id"
status="$(request DELETE "/v1/conversations/$direct_id" "$token_a")"
expect_status 204 "$status"
status="$(request GET /v1/conversations "$token_a")"
expect_status 200 "$status"
assert_jq_equal 0 '[.items[] | select(.conversation.id==$conversation)] | length' --arg conversation "$direct_id"
status="$(request POST "/v1/conversations/$direct_id/messages" "$token_b" "$(jq -nc --arg id "qa-restore-$suffix" '{clientMsgId:$id,type:"text",body:{text:"新消息恢复已删除会话"}}')")"
expect_status 201 "$status"
status="$(request GET /v1/conversations "$token_a")"
expect_status 200 "$status"
assert_jq_equal 1 '[.items[] | select(.conversation.id==$conversation)] | length' --arg conversation "$direct_id"

step="消息发送幂等"
client_msg_id="qa-message-$suffix"
send_body="$(jq -nc --arg id "$client_msg_id" '{clientMsgId:$id,type:"text",body:{text:"邻里通讯本地验收消息"}}')"
status="$(request POST "/v1/conversations/$direct_id/messages" "$token_a" "$send_body")"
expect_status 201 "$status"
message_id="$(json_value '.message.id')"
assert_json_equal '.duplicate' false
status="$(request POST "/v1/conversations/$direct_id/messages" "$token_a" "$send_body")"
expect_status 200 "$status"
assert_json_equal '.duplicate' true
assert_json_equal '.message.id' "$message_id"

step="编辑、版本历史、回应与服务端搜索"
edited_text="qa-edited-$suffix"
edit_body="$(jq -nc --arg id "qa-edit-$suffix" --arg text "$edited_text" '{editId:$id,text:$text}')"
status="$(request PATCH "/v1/messages/$message_id" "$token_a" "$edit_body")"
expect_status 200 "$status"
assert_json_equal '.duplicate' false
assert_json_equal '.message.editVersion' 1
status="$(request PATCH "/v1/messages/$message_id" "$token_a" "$edit_body")"
expect_status 200 "$status"
assert_json_equal '.duplicate' true
status="$(request GET "/v1/messages/$message_id/edits" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.items | length' 2
status="$(request PUT "/v1/messages/$message_id/reactions/%F0%9F%91%8D" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.reaction.count' 1
assert_json_equal '.reaction.reactedByMe' true
status="$(request PUT "/v1/messages/$message_id/reactions/%F0%9F%91%8D" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.duplicate' true
status="$(request GET "/v1/conversations/$direct_id/messages/search?q=$edited_text&limit=10" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.items[0].id' "$message_id"
status="$(request DELETE "/v1/messages/$message_id/reactions/%F0%9F%91%8D" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.reaction.count' 0

step="对象存储直传与图片消息"
printf '%s' 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=' | base64 --decode > "$TMP_DIR/acceptance.png"
media_size="$(wc -c < "$TMP_DIR/acceptance.png" | tr -d ' ')"
if command -v sha256sum >/dev/null 2>&1; then
  media_checksum="$(sha256sum "$TMP_DIR/acceptance.png" | awk '{print $1}')"
else
  media_checksum="$(shasum -a 256 "$TMP_DIR/acceptance.png" | awk '{print $1}')"
fi
status="$(request POST /v1/media/presign "$token_a" "$(jq -nc --argjson size "$media_size" '{mime:"image/png",fileName:"acceptance.png",size:$size}')")"
expect_status 201 "$status"
media_id="$(json_value '.mediaId')"
upload_url="$(json_value '.uploadUrl')"
curl --fail --silent --show-error --request PUT --header 'Content-Type: image/png' --data-binary @"$TMP_DIR/acceptance.png" "$upload_url" >/dev/null
status="$(request POST "/v1/media/$media_id/complete" "$token_a" '{"checksum":"0000000000000000000000000000000000000000000000000000000000000000"}')"
expect_status 400 "$status"
status="$(request POST "/v1/media/$media_id/complete" "$token_a" "$(jq -nc --arg checksum "$media_checksum" '{checksum:$checksum}')")"
expect_status 200 "$status"
assert_json_equal '.status' ready

step="更新并读取头像与昵称"
status="$(request PATCH /v1/users/me "$token_a" "$(jq -nc --arg media "$media_id" '{name:"验收用户甲已更新",avatarMediaId:$media}')")"
expect_status 200 "$status"
assert_json_equal '.name' 验收用户甲已更新
assert_json_equal '.avatarMediaId' "$media_id"
assert_json_equal '.avatarUrl | startswith("/v1/avatars/")' true
status="$(request GET /v1/users/me "$token_a")"
expect_status 200 "$status"
assert_json_equal '.name' 验收用户甲已更新
assert_json_equal '.avatarMediaId' "$media_id"

step="发送图片消息"
status="$(request POST "/v1/conversations/$direct_id/messages" "$token_a" "$(jq -nc --arg id "qa-image-$suffix" --arg media "$media_id" '{clientMsgId:$id,type:"image",body:{mediaId:$media}}')")"
expect_status 201 "$status"
image_message_id="$(json_value '.message.id')"
assert_json_equal '.message.body.mediaId' "$media_id"
assert_json_equal '.message.body.downloadUrl | startswith("http://127.0.0.1:9000/")' true
status="$(request PUT "/v1/users/me/favorites/$image_message_id" "$token_b")"
expect_status 204 "$status"
status="$(request GET /v1/users/me/favorites?limit=20 "$token_b")"
expect_status 200 "$status"
assert_jq_equal 1 '[.items[] | select(.id==$message)] | length' --arg message "$image_message_id"
status="$(request DELETE "/v1/users/me/favorites/$image_message_id" "$token_b")"
expect_status 204 "$status"

step="回复、已读与撤回"
status="$(request POST "/v1/conversations/$direct_id/messages" "$token_b" "$(jq -nc --arg reply "$message_id" --arg id "qa-reply-$suffix" '{clientMsgId:$id,type:"text",replyToId:$reply,body:{text:"收到"}}')")"
expect_status 201 "$status"
reply_message_id="$(json_value '.message.id')"
reply_seq="$(json_value '.message.conversationSeq')"
status="$(request PUT "/v1/conversations/$direct_id/read" "$token_a" "$(jq -nc --argjson seq "$reply_seq" '{seq:$seq}')")"
expect_status 200 "$status"
status="$(request POST "/v1/messages/$message_id/recall" "$token_a" '{}')"
expect_status 200 "$status"

step="一对一语音与视频通话状态机"
status="$(request GET /v1/calls/config "$token_a")"
expect_status 200 "$status"
ice_server_count="$(json_value '.iceServers | length')"
if (( ice_server_count <= 0 )); then
  echo "验收失败：${step}，服务端未返回可用 ICE 配置" >&2
  exit 1
fi
audio_call_id="qa_audio_$suffix"
audio_invite="$(jq -nc --arg call "$audio_call_id" --arg conversation "$direct_id" --arg callee "$user_b" '{callId:$call,conversationId:$conversation,calleeUserId:$callee,mediaType:"audio"}')"
status="$(request POST /v1/calls/invite "$token_a" "$audio_invite")"
expect_status 201 "$status"
assert_json_equal '.duplicate' false
status="$(request POST /v1/calls/invite "$token_a" "$audio_invite")"
expect_status 201 "$status"
assert_json_equal '.duplicate' true
status="$(request POST "/v1/calls/$audio_call_id/accept" "$token_b" '{}')"
expect_status 200 "$status"
assert_json_equal '.call.status' accepted
status="$(request POST "/v1/calls/$audio_call_id/hangup" "$token_a" '{"reason":"qa_completed"}')"
expect_status 200 "$status"
assert_json_equal '.call.status' ended
status="$(request GET "/v1/calls/$audio_call_id" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.call.status' ended
video_call_id="qa_video_$suffix"
status="$(request POST /v1/calls/invite "$token_b" "$(jq -nc --arg call "$video_call_id" --arg conversation "$direct_id" --arg callee "$user_a" '{callId:$call,conversationId:$conversation,calleeUserId:$callee,mediaType:"video"}')")"
expect_status 201 "$status"
status="$(request POST "/v1/calls/$video_call_id/reject" "$token_a" '{"reason":"qa_declined"}')"
expect_status 200 "$status"
assert_json_equal '.call.status' rejected

step="群聊、公告与成员权限"
status="$(request POST /v1/groups "$token_a" "$(jq -nc --arg user "$user_b" '{name:"本地验收群",memberIds:[$user]}')")"
expect_status 201 "$status"
group_id="$(json_value '.id')"
status="$(request PATCH "/v1/groups/$group_id" "$token_a" "$(jq -nc --arg media "$media_id" '{name:"本地验收群已更新",avatarMediaId:$media,joinPolicy:"qr",allowMemberAddFriend:false,rotateQR:true}')")"
expect_status 200 "$status"
assert_json_equal '.name' 本地验收群已更新
assert_json_equal '.allowMemberAddFriend' false
group_qr_token="$(json_value '.qrToken')"
status="$(request PUT "/v1/groups/$group_id/announcement" "$token_a" '{"content":"本地验收公告"}')"
expect_status 200 "$status"
status="$(request POST "/v1/groups/$group_id/announcement/read" "$token_b" '{}')"
expect_status 204 "$status"
status="$(request GET "/v1/groups/$group_id/members" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.items | length' 2
status="$(request DELETE "/v1/groups/$group_id/members/$user_b" "$token_a")"
expect_status 204 "$status"
status="$(request POST /v1/groups/join/qr "$token_b" "$(jq -nc --arg token "$group_qr_token" '{token:$token}')")"
expect_status 200 "$status"
status="$(request PATCH "/v1/groups/$group_id/nickname" "$token_b" '{"nickname":"群内昵称乙"}')"
expect_status 200 "$status"
status="$(request PUT "/v1/groups/$group_id/members/$user_b/mute" "$token_a" '{"until":"2099-01-01T00:00:00Z"}')"
expect_status 200 "$status"
status="$(request POST "/v1/conversations/$group_id/messages" "$token_b" "$(jq -nc --arg id "qa-muted-denied-$suffix" '{clientMsgId:$id,type:"text",body:{text:"禁言期间不得发出"}}')")"
expect_status 403 "$status"
status="$(request PUT "/v1/groups/$group_id/members/$user_b/mute" "$token_a" '{"until":null}')"
expect_status 200 "$status"

step="多选逐条与合并转发"
separate_forward="$(jq -nc --arg image "$image_message_id" --arg reply "$reply_message_id" --arg batch "qa-separate-$suffix" '{sourceMessageIds:[$image,$reply],mode:"separate",clientBatchId:$batch}')"
status="$(request POST "/v1/conversations/$group_id/forward" "$token_a" "$separate_forward")"
expect_status 200 "$status"
assert_json_equal '.messages | length' 2
assert_json_equal '.duplicate' false
status="$(request POST "/v1/conversations/$group_id/forward" "$token_a" "$separate_forward")"
expect_status 200 "$status"
assert_json_equal '.duplicate' true
status="$(request POST "/v1/conversations/$group_id/forward" "$token_a" "$(jq -nc --arg image "$image_message_id" --arg reply "$reply_message_id" --arg batch "qa-merged-$suffix" '{sourceMessageIds:[$image,$reply],mode:"merged",clientBatchId:$batch}')")"
expect_status 200 "$status"
assert_json_equal '.messages | length' 1
assert_json_equal '.messages[0].type' chat_history

step="群 @成员、@所有人和消息置顶"
status="$(request POST "/v1/conversations/$group_id/messages" "$token_a" "$(jq -nc --arg id "qa-mention-$suffix" --arg user "$user_b" '{clientMsgId:$id,type:"text",body:{text:"@验收用户乙 请确认",mentions:[$user]}}')")"
expect_status 201 "$status"
group_message_id="$(json_value '.message.id')"
status="$(request POST "/v1/conversations/$group_id/messages" "$token_b" "$(jq -nc --arg id "qa-mention-all-denied-$suffix" '{clientMsgId:$id,type:"text",body:{text:"无权限的群发提醒",mentionAll:true}}')")"
expect_status 403 "$status"
status="$(request POST "/v1/conversations/$group_id/messages" "$token_a" "$(jq -nc --arg id "qa-mention-all-$suffix" '{clientMsgId:$id,type:"text",body:{text:"@所有人 本地验收",mentionAll:true}}')")"
expect_status 201 "$status"
status="$(request GET /v1/conversations "$token_b")"
expect_status 200 "$status"
assert_jq_equal 2 '.items[] | select(.conversation.id==$group) | .mentionUnreadCount' --arg group "$group_id"
status="$(request PUT "/v1/conversations/$group_id/pinned-messages/$group_message_id" "$token_b")"
expect_status 403 "$status"
status="$(request PUT "/v1/conversations/$group_id/pinned-messages/$group_message_id" "$token_a")"
expect_status 200 "$status"
assert_json_equal '.duplicate' false
status="$(request GET "/v1/conversations/$group_id/pinned-messages?limit=10" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.items[0].message.id' "$group_message_id"
status="$(request DELETE "/v1/conversations/$group_id/pinned-messages/$group_message_id" "$token_a")"
expect_status 200 "$status"
status="$(request PUT "/v1/groups/$group_id/members/$user_b/role" "$token_a" '{"role":"admin"}')"
expect_status 200 "$status"
status="$(request PUT "/v1/groups/$group_id/announcement" "$token_b" '{"content":"管理员更新的验收公告"}')"
expect_status 200 "$status"
status="$(request PUT "/v1/groups/$group_id/members/$user_b/role" "$token_a" '{"role":"member"}')"
expect_status 200 "$status"

step="黑名单阻断消息"
status="$(request PUT "/v1/users/$user_a/block" "$token_b" '{"blocked":true}')"
expect_status 200 "$status"
status="$(request POST "/v1/conversations/$direct_id/messages" "$token_a" "$(jq -nc --arg id "qa-blocked-$suffix" '{clientMsgId:$id,type:"text",body:{text:"此消息必须被拒绝"}}')")"
expect_status 403 "$status"
status="$(request PUT "/v1/users/$user_a/block" "$token_b" '{"blocked":false}')"
expect_status 200 "$status"

step="举报与反馈"
status="$(request POST /v1/reports "$token_b" "$(jq -nc --arg target "$message_id" '{targetType:"message",targetId:$target,reason:"自动化验收",details:"仅本地测试"}')")"
expect_status 201 "$status"
report_id="$(json_value '.id')"
status="$(request POST /v1/feedback "$token_b" '{"category":"product","content":"本地自动化验收反馈"}')"
expect_status 201 "$status"

step="管理端登录、数据列表、处置、公告与审计"
admin_totp="$(python3 - "${IM_ADMIN_TOTP_SECRET:-JBSWY3DPEHPK3PXP}" <<'PY'
import base64, hashlib, hmac, struct, sys, time
secret = sys.argv[1] + '=' * ((8 - len(sys.argv[1]) % 8) % 8)
key = base64.b32decode(secret)
counter = struct.pack('>Q', int(time.time()) // 30)
digest = hmac.new(key, counter, hashlib.sha1).digest()
offset = digest[-1] & 15
print(f'{(struct.unpack(">I", digest[offset:offset+4])[0] & 0x7fffffff) % 1000000:06d}')
PY
)"
status="$(request POST /v1/admin/auth/login '' "$(jq -nc --arg email "${IM_ADMIN_EMAIL:-admin@nexachat.local}" --arg password "${IM_ADMIN_PASSWORD:-local-development-admin-password}" --arg totp "$admin_totp" '{email:$email,password:$password,totp:$totp}')")"
expect_status 200 "$status"
admin_token="$(json_value '.accessToken')"
for admin_path in \
  /v1/admin/dashboard \
  "/v1/admin/users?q=$handle_b&limit=20" \
  /v1/admin/groups?limit=20 \
  /v1/admin/messages?limit=20 \
  /v1/admin/media?limit=20 \
  /v1/admin/calls?limit=20 \
  /v1/admin/online \
  /v1/admin/settings \
  /v1/admin/audit-logs?limit=20; do
  status="$(request GET "$admin_path" "$admin_token")"
  expect_status 200 "$status"
done
status="$(request POST "/v1/admin/users/$user_b/ban" "$admin_token" '{"reason":"本地权限验收"}')"
expect_status 200 "$status"
assert_json_equal '.banned' true
status="$(request GET /v1/users/me "$token_b")"
expect_status 403 "$status"
status="$(request POST "/v1/admin/users/$user_b/unban" "$admin_token" '{"reason":"本地验收恢复账号"}')"
expect_status 200 "$status"
assert_json_equal '.banned' false
status="$(request POST "/v1/admin/reports/$report_id/resolve" "$admin_token" '{"action":"no_violation","note":"本地自动验收"}')"
expect_status 200 "$status"
assert_json_equal '.action' no_violation
status="$(request POST /v1/admin/announcements "$admin_token" '{"title":"自动验收公告","content":"后台公告完整链路","status":"draft","pinned":true,"targetType":"all","targetUserIds":[],"pushOnPublish":false}')"
expect_status 201 "$status"
admin_announcement_id="$(json_value '.id')"
status="$(request POST "/v1/admin/announcements/$admin_announcement_id/publish" "$admin_token" '{"enqueuePush":false}')"
expect_status 200 "$status"
assert_json_equal '.status' published
status="$(request GET /v1/announcements "$token_a")"
expect_status 200 "$status"
assert_jq_equal 1 '[.items[] | select(.id==$announcement)] | length' --arg announcement "$admin_announcement_id"
status="$(request POST "/v1/announcements/$admin_announcement_id/read" "$token_a" '{}')"
expect_status 204 "$status"
status="$(request POST "/v1/admin/announcements/$admin_announcement_id/withdraw" "$admin_token" '{}')"
expect_status 200 "$status"
assert_json_equal '.status' withdrawn
status="$(request DELETE "/v1/admin/announcements/$admin_announcement_id" "$admin_token" '{"reason":"本地验收清理草稿"}')"
expect_status 204 "$status"

step="转让群主并解散临时群"
status="$(request POST "/v1/groups/$group_id/owner/transfer" "$token_a" "$(jq -nc --arg user "$user_b" '{userId:$user}')")"
expect_status 200 "$status"
status="$(request POST "/v1/groups/$group_id/disband" "$token_b" '{"reason":"本地验收完成"}')"
expect_status 200 "$status"

step="注销临时账号"
for token in "$token_a" "$token_b"; do
  status="$(request POST /v1/users/me/deletion/code "$token" '{}')"
  expect_status 202 "$status"
  status="$(request DELETE /v1/users/me "$token" "$(jq -nc --arg code "$OTP_CODE" '{code:$code}')")"
  expect_status 204 "$status"
done

echo "本地全链路产品验收通过：注册、验证码登录、密码登录、重置密码、退出、资料、昵称头像、搜索、好友、单聊、会话置顶/免打扰/未读/删除恢复、幂等、编辑历史、回应、收藏、逐条/合并转发、服务端消息搜索、对象存储直传与校验、图片消息、回复、已读、撤回、语音/视频通话状态机、群聊资料/头像/二维码/成员/角色/禁言/群昵称/群主转让/解散、@成员、@所有人、群消息置顶、公告已读、黑名单、举报、反馈、管理端登录/RBAC列表/封禁恢复/举报处置/公告发布撤回/审计、注销。"
