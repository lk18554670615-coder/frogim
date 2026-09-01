#!/usr/bin/env bash
set -euo pipefail

# 本脚本只用于本机 Docker 开发环境，会创建并在结束时注销临时账号。
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
SERVER_URL="${SERVER_URL:-http://127.0.0.1:8080}"
OTP_CODE="${IM_DEV_OTP_CODE:-123456}"
CLIENT_PLATFORM="${CLIENT_PLATFORM:-android}"
case "$CLIENT_PLATFORM" in
  android | ios | web | macos) ;;
  *)
    echo "CLIENT_PLATFORM must be android, ios, web, or macos" >&2
    exit 2
    ;;
esac
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
token_c=""
admin_token=""
user_a=""
user_b=""
user_c=""
group_id=""
business_community_id=""
business_topic_id=""
business_info_id=""
business_live_id=""

if ! command -v jq >/dev/null 2>&1; then
  jq() {
    python3 "$SCRIPT_DIR/acceptance-json.py" "$@"
  }
fi

step="初始化"
request() {
  local method="$1"
  local path="$2"
  local token="${3:-}"
  local body="${4:-}"
  local args=(--silent --show-error --output "$RESPONSE_FILE" --write-out '%{http_code}' --request "$method" --header "X-Client-Platform: $CLIENT_PLATFORM" "$SERVER_URL$path")
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
  if [[ -n "$admin_token" && -n "$user_a" ]]; then
    curl --silent --show-error --output /dev/null --request PUT \
      --header "Authorization: Bearer $admin_token" \
      --header 'Content-Type: application/json' \
      --data '{"enabled":false,"confirmed":true,"reason":"本地验收异常清理"}' \
      "$SERVER_URL/v2/admin/wukong/system-users/$user_a" || true
  fi
  if [[ -n "$token_a" ]]; then
    local channel_spec channel_id channel_type
    for channel_spec in "$business_topic_id:5" "$business_info_id:6" "$business_live_id:9" "$business_community_id:4"; do
      channel_id="${channel_spec%%:*}"
      channel_type="${channel_spec##*:}"
      if [[ -n "$channel_id" ]]; then
        curl --silent --show-error --output /dev/null --request PATCH \
          --header "Authorization: Bearer $token_a" \
          --header 'Content-Type: application/json' --data '{"disband":true}' \
          "$SERVER_URL/v2/channels/business/$channel_id?channelType=$channel_type" || true
      fi
    done
  fi
  if [[ -n "$group_id" ]]; then
    local group_token
    for group_token in "$token_a" "$token_b"; do
      if [[ -n "$group_token" ]]; then
        curl --silent --show-error --output /dev/null --request POST \
          --header "Authorization: Bearer $group_token" \
          --header 'Content-Type: application/json' --data '{"reason":"本地验收异常清理"}' \
          "$SERVER_URL/v2/channels/groups/$group_id/disband" || true
      fi
    done
  fi
  local token
  for token in "$token_a" "$token_b" "$token_c"; do
    if [[ -n "$token" ]]; then
      curl --silent --show-error --output /dev/null --request POST \
        --header "Authorization: Bearer $token" \
        --header 'Content-Type: application/json' --data '{}' \
        "$SERVER_URL/v2/users/me/deletion/code" || true
      curl --silent --show-error --output /dev/null --request DELETE \
        --header "Authorization: Bearer $token" \
        --header 'Content-Type: application/json' \
        --data "$(jq -nc --arg code "$OTP_CODE" '{code:$code}')" \
        "$SERVER_URL/v2/users/me" || true
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
phone_c="193${suffix}"
handle_a="qa_a_${suffix}"
handle_b="qa_b_${suffix}"
handle_c="qa_c_${suffix}"
password='LocalQaPass_2026!'

step="注册用户 A"
status="$(request POST /v2/auth/register '' "$(jq -nc --arg phone "$phone_a" --arg code "$OTP_CODE" --arg password "$password" '{phone:$phone,code:$code,password:$password,name:"验收用户甲"}')")"
expect_status 200 "$status"
token_a="$(json_value '.accessToken')"
user_a="$(json_value '.user.id')"

step="注册用户 B"
status="$(request POST /v2/auth/register '' "$(jq -nc --arg phone "$phone_b" --arg code "$OTP_CODE" --arg password "$password" '{phone:$phone,code:$code,password:$password,name:"验收用户乙"}')")"
expect_status 200 "$status"
token_b="$(json_value '.accessToken')"
user_b="$(json_value '.user.id')"

step="注册用户 C"
status="$(request POST /v2/auth/register '' "$(jq -nc --arg phone "$phone_c" --arg code "$OTP_CODE" --arg password "$password" '{phone:$phone,code:$code,password:$password,name:"验收用户丙"}')")"
expect_status 200 "$status"
token_c="$(json_value '.accessToken')"
user_c="$(json_value '.user.id')"

step="验证码登录、密码登录、重置密码与退出"
status="$(request POST /v2/auth/code '' "$(jq -nc --arg phone "$phone_a" '{phone:$phone}')")"
expect_status 202 "$status"
status="$(request POST /v2/auth/login '' "$(jq -nc --arg phone "$phone_a" --arg code "$OTP_CODE" '{phone:$phone,code:$code}')")"
expect_status 200 "$status"
otp_token_a="$(json_value '.accessToken')"
otp_refresh_a="$(json_value '.refreshToken')"
status="$(request POST /v2/auth/logout "$otp_token_a" "$(jq -nc --arg refresh "$otp_refresh_a" '{refreshToken:$refresh}')")"
expect_status 204 "$status"
status="$(request POST /v2/auth/password-login '' "$(jq -nc --arg phone "$phone_a" --arg password "$password" '{phone:$phone,password:$password}')")"
expect_status 200 "$status"
status="$(request POST /v2/auth/password/reset-code '' "$(jq -nc --arg phone "$phone_a" '{phone:$phone}')")"
expect_status 202 "$status"
new_password='LocalQaPass_2026_Changed!'
status="$(request POST /v2/auth/password/reset '' "$(jq -nc --arg phone "$phone_a" --arg code "$OTP_CODE" --arg password "$new_password" '{phone:$phone,code:$code,password:$password}')")"
expect_status 204 "$status"
status="$(request POST /v2/auth/password-login '' "$(jq -nc --arg phone "$phone_a" --arg password "$password" '{phone:$phone,password:$password}')")"
expect_status 401 "$status"
status="$(request POST /v2/auth/password-login '' "$(jq -nc --arg phone "$phone_a" --arg password "$new_password" '{phone:$phone,password:$password}')")"
expect_status 200 "$status"
token_a="$(json_value '.accessToken')"

step="更新双方公开资料"
status="$(request PATCH /v2/users/me "$token_a" "$(jq -nc --arg handle "$handle_a" '{handle:$handle,signature:"端到端验收账号"}')")"
expect_status 200 "$status"
status="$(request PATCH /v2/users/me "$token_b" "$(jq -nc --arg handle "$handle_b" '{handle:$handle,signature:"端到端验收账号"}')")"
expect_status 200 "$status"
status="$(request PATCH /v2/users/me "$token_c" "$(jq -nc --arg handle "$handle_c" '{handle:$handle,signature:"端到端验收账号"}')")"
expect_status 200 "$status"

step="按邻里号搜索"
status="$(request GET "/v2/contacts/search?q=$handle_b&by=handle" "$token_a")"
expect_status 200 "$status"
assert_json_equal '.items[0].id' "$user_b"

step="好友申请与接受"
status="$(request POST /v2/contacts/requests "$token_a" "$(jq -nc --arg user "$user_b" '{userId:$user,message:"本地自动化验收",source:"search"}')")"
expect_status 201 "$status"
friend_request_id="$(json_value '.id')"
status="$(request POST "/v2/contacts/requests/$friend_request_id/accept" "$token_b" '{}')"
expect_status 200 "$status"

step="创建单聊"
status="$(request POST /v2/channels/direct "$token_a" "$(jq -nc --arg user "$user_b" '{userId:$user}')")"
expect_status 201 "$status"
direct_id="$(json_value '.id')"

step="会话左滑置顶、免打扰、未读与删除后恢复"
status="$(request PATCH "/v2/channels/conversations/$direct_id/preferences" "$token_a" '{"pinned":true,"notificationsMuted":true,"manualUnread":true}')"
expect_status 204 "$status"
status="$(request GET /v2/channels/conversations "$token_a")"
expect_status 200 "$status"
assert_jq_equal true '.items[] | select(.conversation.id==$conversation) | .membership.pinned' --arg conversation "$direct_id"
assert_jq_equal true '.items[] | select(.conversation.id==$conversation) | .membership.notificationsMuted' --arg conversation "$direct_id"
assert_jq_equal true '.items[] | select(.conversation.id==$conversation) | .membership.manualUnread' --arg conversation "$direct_id"
status="$(request DELETE "/v2/channels/conversations/$direct_id" "$token_a")"
expect_status 204 "$status"
status="$(request GET /v2/channels/conversations "$token_a")"
expect_status 200 "$status"
assert_jq_equal 0 '[.items[] | select(.conversation.id==$conversation)] | length' --arg conversation "$direct_id"
status="$(request POST "/v2/messages/conversations/$direct_id/send" "$token_b" "$(jq -nc --arg id "qa-restore-$suffix" '{clientMsgId:$id,type:"text",body:{text:"新消息恢复已删除会话"}}')")"
expect_status 201 "$status"
conversation_restored=false
for _ in $(seq 1 20); do
  status="$(request GET /v2/channels/conversations "$token_a")"
  expect_status 200 "$status"
  if [[ "$(jq -r --arg conversation "$direct_id" '[.items[] | select(.conversation.id==$conversation)] | length' "$RESPONSE_FILE")" == "1" ]]; then
    conversation_restored=true
    break
  fi
  sleep 0.25
done
if [[ "$conversation_restored" != "true" ]]; then
  echo "验收失败：${step}，新消息在5秒内未恢复已删除会话" >&2
  exit 1
fi

step="消息发送幂等"
client_msg_id="qa-message-$suffix"
send_body="$(jq -nc --arg id "$client_msg_id" '{clientMsgId:$id,type:"text",body:{text:"青蛙呱呱本地验收消息"}}')"
status="$(request POST "/v2/messages/conversations/$direct_id/send" "$token_a" "$send_body")"
expect_status 201 "$status"
message_id="$(json_value '.message.id')"
assert_json_equal '.duplicate' false
status="$(request POST "/v2/messages/conversations/$direct_id/send" "$token_a" "$send_body")"
expect_status 200 "$status"
assert_json_equal '.duplicate' true
assert_json_equal '.message.id' "$message_id"

step="编辑、版本历史、回应与服务端搜索"
edited_text="qa-edited-$suffix"
edit_body="$(jq -nc --arg id "qa-edit-$suffix" --arg text "$edited_text" '{editId:$id,text:$text}')"
status="$(request PATCH "/v2/messages/$message_id" "$token_a" "$edit_body")"
expect_status 200 "$status"
assert_json_equal '.duplicate' false
assert_json_equal '.message.editVersion' 1
status="$(request PATCH "/v2/messages/$message_id" "$token_a" "$edit_body")"
expect_status 200 "$status"
assert_json_equal '.duplicate' true
status="$(request GET "/v2/messages/$message_id/edits" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.items | length' 2
status="$(request PUT "/v2/messages/$message_id/reactions/%F0%9F%91%8D" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.reaction.count' 1
assert_json_equal '.reaction.reactedByMe' true
status="$(request PUT "/v2/messages/$message_id/reactions/%F0%9F%91%8D" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.duplicate' true
status="$(request GET "/v2/messages/search?conversationId=$direct_id&q=$edited_text&limit=10" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.items[0].id' "$message_id"
status="$(request DELETE "/v2/messages/$message_id/reactions/%F0%9F%91%8D" "$token_b")"
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
status="$(request POST /v2/media/presign "$token_a" "$(jq -nc --argjson size "$media_size" '{mime:"image/png",fileName:"acceptance.png",size:$size}')")"
expect_status 201 "$status"
media_id="$(json_value '.mediaId')"
upload_url="$(json_value '.uploadUrl')"
curl --fail --silent --show-error --request PUT --header 'Content-Type: image/png' --data-binary @"$TMP_DIR/acceptance.png" "$upload_url" >/dev/null
status="$(request POST "/v2/media/$media_id/complete" "$token_a" '{"checksum":"0000000000000000000000000000000000000000000000000000000000000000"}')"
expect_status 400 "$status"
status="$(request POST "/v2/media/$media_id/complete" "$token_a" "$(jq -nc --arg checksum "$media_checksum" '{checksum:$checksum}')")"
expect_status 200 "$status"
assert_json_equal '.status' ready

step="更新并读取头像与昵称"
status="$(request PATCH /v2/users/me "$token_a" "$(jq -nc --arg media "$media_id" '{name:"验收用户甲已更新",avatarMediaId:$media}')")"
expect_status 200 "$status"
assert_json_equal '.name' 验收用户甲已更新
assert_json_equal '.avatarMediaId' "$media_id"
assert_json_equal '.avatarUrl | startswith("/v2/avatars/")' true
status="$(request GET /v2/users/me "$token_a")"
expect_status 200 "$status"
assert_json_equal '.name' 验收用户甲已更新
assert_json_equal '.avatarMediaId' "$media_id"

step="朋友圈发布、可见性、点赞、评论、提醒与删除"
status="$(request POST /v2/moments "$token_a" "$(jq -nc --arg media "$media_id" '{content:"本地验收朋友圈",mediaKind:"images",mediaIds:[$media],visibility:"friends",visibleUserIds:[],location:{name:"本地测试"}}')")"
expect_status 201 "$status"
moment_id="$(json_value '.item.id')"
status="$(request GET /v2/moments "$token_b")"
expect_status 200 "$status"
assert_jq_equal 1 '[.items[] | select(.id==$moment)] | length' --arg moment "$moment_id"
status="$(request PUT "/v2/moments/$moment_id/like" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.item.likeCount' 1
status="$(request POST "/v2/moments/$moment_id/comments" "$token_b" '{"content":"朋友圈验收评论"}')"
expect_status 201 "$status"
moment_comment_id="$(json_value '.item.id')"
status="$(request GET /v2/moments/reminders "$token_a")"
expect_status 200 "$status"
assert_jq_equal 2 '[.items[] | select(.momentId==$moment)] | length' --arg moment "$moment_id"
reminder_id="$(json_value '.items[0].id')"
reminder_body="$(jq -nc --argjson reminder "$reminder_id" '{reminderIds:[$reminder]}')"
status="$(request POST /v2/moments/reminders/read "$token_a" "$reminder_body")"
expect_status 200 "$status"
status="$(request DELETE "/v2/moments/$moment_id/comments/$moment_comment_id" "$token_b")"
expect_status 204 "$status"
status="$(request DELETE "/v2/moments/$moment_id/like" "$token_b")"
expect_status 200 "$status"

step="发送图片消息"
status="$(request POST "/v2/messages/conversations/$direct_id/send" "$token_a" "$(jq -nc --arg id "qa-image-$suffix" --arg media "$media_id" '{clientMsgId:$id,type:"image",body:{mediaId:$media}}')")"
expect_status 201 "$status"
image_message_id="$(json_value '.message.id')"
assert_json_equal '.message.body.mediaId' "$media_id"
assert_json_equal '.message.body.downloadUrl | startswith("http://127.0.0.1:9000/")' true
status="$(request PUT "/v2/messages/favorites/$image_message_id" "$token_b")"
expect_status 204 "$status"
status="$(request GET /v2/messages/favorites?limit=20 "$token_b")"
expect_status 200 "$status"
assert_jq_equal 1 '[.items[] | select(.id==$message)] | length' --arg message "$image_message_id"
status="$(request DELETE "/v2/messages/favorites/$image_message_id" "$token_b")"
expect_status 204 "$status"

step="定时消息创建、修改、取消与真实派发"
scheduled_at="$(python3 - <<'PY'
from datetime import datetime, timedelta, timezone
print((datetime.now(timezone.utc) + timedelta(seconds=30)).isoformat().replace('+00:00', 'Z'))
PY
)"
scheduled_client_id="qa-scheduled-$suffix"
status="$(request POST /v2/messages/scheduled "$token_a" "$(jq -nc --arg conversation "$direct_id" --arg id "$scheduled_client_id" --arg at "$scheduled_at" '{conversationId:$conversation,clientMsgId:$id,type:"text",body:{text:"定时消息真实派发"},scheduledAt:$at}')")"
expect_status 201 "$status"
scheduled_message_id="$(json_value '.scheduledMessage.id')"
dispatch_at="$(python3 - <<'PY'
from datetime import datetime, timedelta, timezone
print((datetime.now(timezone.utc) + timedelta(seconds=7)).isoformat().replace('+00:00', 'Z'))
PY
)"
status="$(request PATCH "/v2/messages/scheduled/$scheduled_message_id" "$token_a" "$(jq -nc --arg at "$dispatch_at" '{body:{text:"定时消息真实派发（已修改）"},scheduledAt:$at}')")"
expect_status 200 "$status"
assert_json_equal '.scheduledMessage.status' pending

cancel_at="$(python3 - <<'PY'
from datetime import datetime, timedelta, timezone
print((datetime.now(timezone.utc) + timedelta(seconds=30)).isoformat().replace('+00:00', 'Z'))
PY
)"
status="$(request POST /v2/messages/scheduled "$token_a" "$(jq -nc --arg conversation "$direct_id" --arg id "qa-scheduled-cancel-$suffix" --arg at "$cancel_at" '{conversationId:$conversation,clientMsgId:$id,type:"text",body:{text:"必须取消的定时消息"},scheduledAt:$at}')")"
expect_status 201 "$status"
cancel_scheduled_id="$(json_value '.scheduledMessage.id')"
status="$(request DELETE "/v2/messages/scheduled/$cancel_scheduled_id" "$token_a")"
expect_status 200 "$status"
assert_json_equal '.scheduledMessage.status' cancelled

scheduled_sent=false
for _ in $(seq 1 40); do
  status="$(request GET "/v2/messages/scheduled?status=sent&limit=50" "$token_a")"
  expect_status 200 "$status"
  if [[ "$(jq -r --arg id "$scheduled_message_id" '[.items[] | select(.id==$id)] | length' "$RESPONSE_FILE")" == "1" ]]; then
    sent_status="$(jq -r --arg id "$scheduled_message_id" '.items[] | select(.id==$id) | .status' "$RESPONSE_FILE")"
    sent_message_id="$(jq -r --arg id "$scheduled_message_id" '.items[] | select(.id==$id) | .sentMessageId' "$RESPONSE_FILE")"
    if [[ "$sent_status" == "sent" && -n "$sent_message_id" && "$sent_message_id" != "null" ]]; then
      scheduled_sent=true
      break
    fi
  fi
  sleep 0.25
done
if [[ "$scheduled_sent" != "true" ]]; then
  echo "验收失败：${step}，定时消息在10秒内未完成派发" >&2
  exit 1
fi
status="$(request GET "/v2/messages/conversations/$direct_id/history?limit=100" "$token_b")"
expect_status 200 "$status"
assert_jq_equal 1 '[.items[] | select(.clientMsgId==$client)] | length' --arg client "$scheduled_client_id"
assert_jq_equal '定时消息真实派发（已修改）' '.items[] | select(.clientMsgId==$client) | .body.text' --arg client "$scheduled_client_id"

step="回复、已读与撤回"
status="$(request POST "/v2/messages/conversations/$direct_id/send" "$token_b" "$(jq -nc --arg reply "$message_id" --arg id "qa-reply-$suffix" '{clientMsgId:$id,type:"text",replyToId:$reply,body:{text:"收到"}}')")"
expect_status 201 "$status"
reply_message_id="$(json_value '.message.id')"
reply_seq="$(json_value '.message.conversationSeq')"
status="$(request PUT "/v2/channels/conversations/$direct_id/read" "$token_a" "$(jq -nc --argjson seq "$reply_seq" '{seq:$seq}')")"
expect_status 200 "$status"
status="$(request POST "/v2/messages/$message_id/recall" "$token_a" '{}')"
expect_status 200 "$status"

step="一对一语音与视频通话状态机"
status="$(request GET /v2/calls/config "$token_a")"
expect_status 200 "$status"
assert_json_equal '.provider' livekit
assert_json_equal '.maxParticipants' 9
assert_json_equal '.supportsScreenShare' true
livekit_url="$(json_value '.url')"
if [[ "$livekit_url" != ws://* && "$livekit_url" != wss://* ]]; then
  echo "验收失败：${step}，服务端未返回可用 LiveKit WebSocket 地址" >&2
  exit 1
fi
audio_call_id="qa_audio_$suffix"
audio_invite="$(jq -nc --arg call "$audio_call_id" --arg conversation "$direct_id" --arg callee "$user_b" '{callId:$call,conversationId:$conversation,calleeUserId:$callee,mediaType:"audio"}')"
status="$(request POST /v2/calls/invite "$token_a" "$audio_invite")"
expect_status 201 "$status"
assert_json_equal '.duplicate' false
status="$(request POST /v2/calls/invite "$token_a" "$audio_invite")"
expect_status 201 "$status"
assert_json_equal '.duplicate' true
status="$(request POST "/v2/calls/$audio_call_id/accept" "$token_b" '{}')"
expect_status 200 "$status"
assert_json_equal '.call.status' accepted
status="$(request POST "/v2/calls/$audio_call_id/hangup" "$token_a" '{"reason":"qa_completed"}')"
expect_status 200 "$status"
assert_json_equal '.call.status' ended
status="$(request GET "/v2/calls/$audio_call_id" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.call.status' ended
video_call_id="qa_video_$suffix"
status="$(request POST /v2/calls/invite "$token_b" "$(jq -nc --arg call "$video_call_id" --arg conversation "$direct_id" --arg callee "$user_a" '{callId:$call,conversationId:$conversation,calleeUserId:$callee,mediaType:"video"}')")"
expect_status 201 "$status"
status="$(request POST "/v2/calls/$video_call_id/reject" "$token_a" '{"reason":"qa_declined"}')"
expect_status 200 "$status"
assert_json_equal '.call.status' rejected

step="群聊、公告与成员权限"
status="$(request POST /v2/channels/groups "$token_a" "$(jq -nc --arg user "$user_b" '{name:"本地验收群",memberIds:[$user]}')")"
expect_status 201 "$status"
group_id="$(json_value '.id')"
status="$(request PATCH "/v2/channels/groups/$group_id" "$token_a" "$(jq -nc --arg media "$media_id" '{name:"本地验收群已更新",avatarMediaId:$media,joinPolicy:"qr",allowMemberAddFriend:false,rotateQR:true}')")"
expect_status 200 "$status"
assert_json_equal '.name' 本地验收群已更新
assert_json_equal '.allowMemberAddFriend' false
group_qr_token="$(json_value '.qrToken')"
status="$(request PUT "/v2/channels/groups/$group_id/announcement" "$token_a" '{"content":"本地验收公告"}')"
expect_status 200 "$status"
status="$(request POST "/v2/channels/groups/$group_id/announcement/read" "$token_b" '{}')"
expect_status 204 "$status"
status="$(request GET "/v2/channels/groups/$group_id/members" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.items | length' 2
status="$(request DELETE "/v2/channels/groups/$group_id/members/$user_b" "$token_a")"
expect_status 204 "$status"
status="$(request POST /v2/channels/groups/join/qr "$token_b" "$(jq -nc --arg token "$group_qr_token" '{token:$token}')")"
expect_status 200 "$status"
status="$(request PATCH "/v2/channels/groups/$group_id/nickname" "$token_b" '{"nickname":"群内昵称乙"}')"
expect_status 200 "$status"
status="$(request PUT "/v2/channels/groups/$group_id/members/$user_b/mute" "$token_a" '{"until":"2099-01-01T00:00:00Z"}')"
expect_status 200 "$status"
status="$(request POST "/v2/messages/conversations/$group_id/send" "$token_b" "$(jq -nc --arg id "qa-muted-denied-$suffix" '{clientMsgId:$id,type:"text",body:{text:"禁言期间不得发出"}}')")"
expect_status 403 "$status"
status="$(request PUT "/v2/channels/groups/$group_id/members/$user_b/mute" "$token_a" '{"until":null}')"
expect_status 200 "$status"

status="$(request POST "/v2/channels/groups/$group_id/members" "$token_a" "$(jq -nc --arg user "$user_c" '{userIds:[$user]}')")"
expect_status 200 "$status"
group_call_id="qa_group_video_$suffix"
status="$(request POST /v2/calls/invite "$token_a" "$(jq -nc --arg call "$group_call_id" --arg conversation "$group_id" '{callId:$call,conversationId:$conversation,mediaType:"video"}')")"
expect_status 201 "$status"
assert_json_equal '.call.kind' group
assert_json_equal '.call.participantIds | length' 3
status="$(request POST "/v2/calls/$group_call_id/accept" "$token_b" '{}')"
expect_status 200 "$status"
assert_json_equal '.call.status' accepted
status="$(request POST "/v2/calls/$group_call_id/reject" "$token_c" '{"reason":"qa_group_declined"}')"
expect_status 200 "$status"
assert_json_equal '.call.status' accepted
for call_token in "$token_a" "$token_b"; do
  status="$(request POST "/v2/calls/$group_call_id/token" "$call_token" '{}')"
  expect_status 200 "$status"
  assert_json_equal '.session.roomName' "call_$group_call_id"
  call_media_token="$(json_value '.session.token')"
  [[ -n "$call_media_token" ]] || { echo "验收失败：${step}，群视频未签发媒体令牌" >&2; exit 1; }
done
status="$(request POST "/v2/calls/$group_call_id/token" "$token_c" '{}')"
expect_status 403 "$status"

step="多选逐条与合并转发"
separate_forward="$(jq -nc --arg target "$group_id" --arg image "$image_message_id" --arg reply "$reply_message_id" --arg batch "qa-separate-$suffix" '{targetConversationId:$target,sourceMessageIds:[$image,$reply],mode:"separate",clientBatchId:$batch}')"
status="$(request POST "/v2/messages/forward" "$token_a" "$separate_forward")"
expect_status 200 "$status"
assert_json_equal '.messages | length' 2
assert_json_equal '.duplicate' false
status="$(request POST "/v2/messages/forward" "$token_a" "$separate_forward")"
expect_status 200 "$status"
assert_json_equal '.duplicate' true
status="$(request POST "/v2/messages/forward" "$token_a" "$(jq -nc --arg target "$group_id" --arg image "$image_message_id" --arg reply "$reply_message_id" --arg batch "qa-merged-$suffix" '{targetConversationId:$target,sourceMessageIds:[$image,$reply],mode:"merged",clientBatchId:$batch}')")"
expect_status 200 "$status"
assert_json_equal '.messages | length' 1
assert_json_equal '.messages[0].type' chat_history

step="群 @成员、@所有人和消息置顶"
status="$(request POST "/v2/messages/conversations/$group_id/send" "$token_a" "$(jq -nc --arg id "qa-mention-$suffix" --arg user "$user_b" '{clientMsgId:$id,type:"text",body:{text:"@验收用户乙 请确认",mentions:[$user]}}')")"
expect_status 201 "$status"
group_message_id="$(json_value '.message.id')"
status="$(request POST "/v2/messages/conversations/$group_id/send" "$token_b" "$(jq -nc --arg id "qa-mention-all-denied-$suffix" '{clientMsgId:$id,type:"text",body:{text:"无权限的群发提醒",mentionAll:true}}')")"
expect_status 403 "$status"
status="$(request POST "/v2/messages/conversations/$group_id/send" "$token_a" "$(jq -nc --arg id "qa-mention-all-$suffix" '{clientMsgId:$id,type:"text",body:{text:"@所有人 本地验收",mentionAll:true}}')")"
expect_status 201 "$status"
mention_indexed=false
for _ in $(seq 1 20); do
  status="$(request GET /v2/channels/conversations "$token_b")"
  expect_status 200 "$status"
  if [[ "$(jq -r --arg group "$group_id" '.items[] | select(.conversation.id==$group) | .mentionUnreadCount' "$RESPONSE_FILE")" == "2" ]]; then
    mention_indexed=true
    break
  fi
  sleep 0.25
done
if [[ "$mention_indexed" != "true" ]]; then
  echo "验收失败：${step}，群提醒在5秒内未完成 webhook 索引" >&2
  exit 1
fi
status="$(request PUT "/v2/messages/pins/$group_message_id?conversationId=$group_id" "$token_b")"
expect_status 403 "$status"
status="$(request PUT "/v2/messages/pins/$group_message_id?conversationId=$group_id" "$token_a")"
expect_status 200 "$status"
assert_json_equal '.duplicate' false
status="$(request GET "/v2/messages/pins?conversationId=$group_id&limit=10" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.items[0].message.id' "$group_message_id"
status="$(request DELETE "/v2/messages/pins/$group_message_id?conversationId=$group_id" "$token_a")"
expect_status 200 "$status"
status="$(request PUT "/v2/channels/groups/$group_id/members/$user_b/role" "$token_a" '{"role":"admin"}')"
expect_status 200 "$status"
status="$(request PUT "/v2/channels/groups/$group_id/announcement" "$token_b" '{"content":"管理员更新的验收公告"}')"
expect_status 200 "$status"
status="$(request PUT "/v2/channels/groups/$group_id/members/$user_b/role" "$token_a" '{"role":"member"}')"
expect_status 200 "$status"

step="社区、话题、资讯、直播频道与临时订阅"
status="$(request POST /v2/channels/business "$token_a" '{"channelType":4,"name":"本地验收社区","description":"社区频道验收","visibility":"public","joinPolicy":"open","postingPolicy":"members","slowModeSeconds":0}')"
expect_status 201 "$status"
business_community_id="$(json_value '.item.id')"
status="$(request POST /v2/channels/business "$token_a" "$(jq -nc --arg parent "$business_community_id" '{channelType:5,name:"本地验收话题",parentId:$parent,description:"社区话题验收",visibility:"public",joinPolicy:"open",postingPolicy:"members",slowModeSeconds:0}')")"
expect_status 201 "$status"
business_topic_id="$(json_value '.item.id')"
status="$(request POST /v2/channels/business "$token_a" '{"channelType":6,"name":"本地验收资讯","description":"资讯频道验收","visibility":"public","joinPolicy":"open","postingPolicy":"operators","slowModeSeconds":0}')"
expect_status 201 "$status"
business_info_id="$(json_value '.item.id')"
status="$(request POST /v2/channels/business "$token_a" '{"channelType":9,"name":"本地验收直播","description":"直播互动验收","visibility":"public","joinPolicy":"open","postingPolicy":"members","slowModeSeconds":1}')"
expect_status 201 "$status"
business_live_id="$(json_value '.item.id')"
status="$(request GET "/v2/channels/business?limit=50" "$token_b")"
expect_status 200 "$status"
for channel_id in "$business_community_id" "$business_topic_id" "$business_info_id" "$business_live_id"; do
  assert_jq_equal 1 '[.items[] | select(.id==$channel)] | length' --arg channel "$channel_id"
done
expires_at="$(date -u -d '+1 hour' '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -v+1H '+%Y-%m-%dT%H:%M:%SZ')"
status="$(request POST "/v2/channels/business/$business_info_id/subscribe?channelType=6" "$token_b" "$(jq -nc --arg expires "$expires_at" '{expiresAt:$expires}')")"
expect_status 200 "$status"
status="$(request GET "/v2/channels/business/$business_info_id/members?channelType=6&limit=20" "$token_a")"
expect_status 200 "$status"
assert_jq_equal member '.items[] | select(.userId==$user) | .role' --arg user "$user_b"
assert_jq_equal "$expires_at" '.items[] | select(.userId==$user) | .expiresAt' --arg user "$user_b"
status="$(request PATCH "/v2/channels/business/$business_info_id/members/$user_b?channelType=6" "$token_a" '{"clearExpiry":true}')"
expect_status 200 "$status"
status="$(request GET "/v2/channels/business/$business_info_id/members?channelType=6&limit=20" "$token_a")"
expect_status 200 "$status"
assert_jq_equal null '.items[] | select(.userId==$user) | .expiresAt' --arg user "$user_b"
status="$(request PATCH "/v2/channels/business/$business_info_id/members/$user_b?channelType=6" "$token_a" "$(jq -nc --arg expires "$expires_at" '{expiresAt:$expires}')")"
expect_status 200 "$status"
status="$(request PUT "/v2/channels/business/$business_info_id/access/deny/$user_b?channelType=6" "$token_a" '{"reason":"本地访问名单验收"}')"
expect_status 200 "$status"
status="$(request GET "/v2/channels/business/$business_info_id/access?channelType=6&accessType=deny" "$token_a")"
expect_status 200 "$status"
assert_jq_equal deny '.items[] | select(.userId==$user) | .accessType' --arg user "$user_b"
status="$(request DELETE "/v2/channels/business/$business_info_id/access/deny/$user_b?channelType=6" "$token_a")"
expect_status 200 "$status"
status="$(request DELETE "/v2/channels/business/$business_info_id/subscription?channelType=6" "$token_b")"
expect_status 200 "$status"

status="$(request POST "/v2/channels/business/$business_live_id/subscribe?channelType=9" "$token_b" '{}')"
expect_status 200 "$status"
live_policy_payload="$(printf '%s' '{"type":1006,"schemaVersion":1,"event":"live.like"}' | base64 | tr -d '\r\n')"
status="$(curl --silent --show-error --output "$RESPONSE_FILE" --write-out '%{http_code}' \
  --request POST --header 'Content-Type: application/json' \
  --header "X-IM-Wukong-Policy-Secret: ${IM_WUKONG_POLICY_SECRET:-local-wukong-policy-secret-change-me-123456}" \
  --data "$(jq -nc --arg from "$user_b" --arg channel "$business_live_id" --arg payload "$live_policy_payload" '{fromUid:$from,channelId:$channel,channelType:9,payload:$payload}')" \
  "$SERVER_URL/internal/wukong/policy/send")"
expect_status 200 "$status"
assert_json_equal '.allowed' true
assert_json_equal '.reasonCode' 1
invalid_live_policy_payload="$(printf '%s' '{"type":1006,"schemaVersion":1,"event":"live.unknown"}' | base64 | tr -d '\r\n')"
status="$(curl --silent --show-error --output "$RESPONSE_FILE" --write-out '%{http_code}' \
  --request POST --header 'Content-Type: application/json' \
  --header "X-IM-Wukong-Policy-Secret: ${IM_WUKONG_POLICY_SECRET:-local-wukong-policy-secret-change-me-123456}" \
  --data "$(jq -nc --arg from "$user_b" --arg channel "$business_live_id" --arg payload "$invalid_live_policy_payload" '{fromUid:$from,channelId:$channel,channelType:9,payload:$payload}')" \
  "$SERVER_URL/internal/wukong/policy/send")"
expect_status 200 "$status"
assert_json_equal '.allowed' false
assert_json_equal '.code' INVALID_LIVE_EVENT

step="黑名单阻断消息"
status="$(request PUT "/v2/contacts/blocks/$user_a" "$token_b" '{"blocked":true}')"
expect_status 200 "$status"
status="$(request POST "/v2/messages/conversations/$direct_id/send" "$token_a" "$(jq -nc --arg id "qa-blocked-$suffix" '{clientMsgId:$id,type:"text",body:{text:"此消息必须被拒绝"}}')")"
expect_status 403 "$status"
status="$(request PUT "/v2/contacts/blocks/$user_a" "$token_b" '{"blocked":false}')"
expect_status 200 "$status"

step="举报与反馈"
status="$(request POST /v2/reports "$token_b" "$(jq -nc --arg target "$message_id" '{targetType:"message",targetId:$target,reason:"自动化验收",details:"仅本地测试"}')")"
expect_status 201 "$status"
report_id="$(json_value '.id')"
status="$(request POST /v2/feedback "$token_b" '{"category":"product","content":"本地自动化验收反馈"}')"
expect_status 201 "$status"

step="管理端登录、数据列表、处置、公告与审计"
status="$(request POST /v2/admin/auth/login '' "$(jq -nc --arg username "${IM_ADMIN_USERNAME:-admin}" --arg password "${IM_ADMIN_PASSWORD:-local-development-admin-password}" '{username:$username,password:$password}')")"
expect_status 200 "$status"
admin_token="$(json_value '.accessToken')"
for admin_path in \
  /v2/admin/dashboard \
  "/v2/admin/users?q=$handle_b&limit=20" \
  /v2/admin/groups?limit=20 \
  /v2/admin/messages?limit=20 \
  /v2/admin/media?limit=20 \
  /v2/admin/calls?limit=20 \
  /v2/admin/online \
  /v2/admin/settings \
  /v2/admin/wukong/overview \
  /v2/admin/wukong/settings \
  /v2/admin/wukong/nodes \
  /v2/admin/audit-logs?limit=20; do
  status="$(request GET "$admin_path" "$admin_token")"
  expect_status 200 "$status"
done

status="$(request GET /v2/admin/livekit/rooms "$admin_token")"
expect_status 200 "$status"
assert_jq_equal 1 '[.items[] | select(.name==$room)] | length' --arg room "call_$group_call_id"
status="$(request GET "/v2/admin/livekit/rooms/call_$group_call_id/participants" "$admin_token")"
expect_status 200 "$status"
status="$(request POST "/v2/calls/$group_call_id/hangup" "$token_a" '{"reason":"qa_group_completed"}')"
expect_status 200 "$status"
assert_json_equal '.call.status' ended
group_room_deleted=false
for _ in $(seq 1 20); do
  status="$(request GET /v2/admin/livekit/rooms "$admin_token")"
  expect_status 200 "$status"
  if [[ "$(jq -r --arg room "call_$group_call_id" '[.items[] | select(.name==$room)] | length' "$RESPONSE_FILE")" == "0" ]]; then
    group_room_deleted=true
    break
  fi
  sleep 0.1
done
[[ "$group_room_deleted" == "true" ]] || { echo "验收失败：${step}，群视频结束后房间未清理" >&2; exit 1; }

step="四端客户端版本策略、强制升级与回退"
for version_platform in android ios web macos; do
  status="$(request PUT "/v2/admin/client-versions/$version_platform" "$admin_token" '{"minimumVersion":"2.0.0","latestVersion":"2.1.0","forceUpdate":false,"rolloutPercentage":100,"releaseNotes":"本地强制升级验收","downloadUrl":"https://downloads.example.test/app","confirmed":true,"reason":"本地验收发布版本策略"}')"
  expect_status 200 "$status"
  assert_json_equal '.data.platform' "$version_platform"
  status="$(request GET "/v2/config/version?platform=$version_platform&version=1.0.0&installId=acceptance-install-$suffix" '')"
  expect_status 200 "$status"
  assert_json_equal '.data.updateAvailable' true
  assert_json_equal '.data.forceUpdate' true
  assert_json_equal '.data.minimumVersion' 2.0.0
  status="$(request PUT "/v2/admin/client-versions/$version_platform" "$admin_token" '{"minimumVersion":"1.0.0","latestVersion":"1.0.0","forceUpdate":false,"rolloutPercentage":100,"releaseNotes":"本地验收恢复当前版本","downloadUrl":"","confirmed":true,"reason":"本地验收恢复版本策略"}')"
  expect_status 200 "$status"
done

step="朋友圈后台隐藏、客户端隔离、恢复与删除"
# 前面的黑名单链路会按产品规则解除好友关系；恢复好友后再验证
# “仅好友可见”动态的后台隐藏/恢复，避免把解除好友误判为审核问题。
status="$(request POST /v2/contacts/requests "$token_a" "$(jq -nc --arg user "$user_b" '{userId:$user,message:"恢复好友后验收朋友圈审核",source:"search"}')")"
expect_status 201 "$status"
moderation_friend_request_id="$(json_value '.id')"
status="$(request POST "/v2/contacts/requests/$moderation_friend_request_id/accept" "$token_b" '{}')"
expect_status 200 "$status"
status="$(request GET "/v2/admin/moments?q=$moment_id&limit=20" "$admin_token")"
expect_status 200 "$status"
assert_jq_equal published '.items[] | select(.id==$moment) | .status' --arg moment "$moment_id"
status="$(request POST "/v2/admin/moments/$moment_id/moderate" "$admin_token" '{"status":"hidden","reason":"本地验收隐藏动态","confirmed":true}')"
expect_status 200 "$status"
assert_json_equal '.status' hidden
status="$(request GET /v2/moments "$token_b")"
expect_status 200 "$status"
assert_jq_equal 0 '[.items[] | select(.id==$moment)] | length' --arg moment "$moment_id"
status="$(request POST "/v2/admin/moments/$moment_id/moderate" "$admin_token" '{"status":"published","reason":"本地验收恢复动态","confirmed":true}')"
expect_status 200 "$status"
assert_json_equal '.status' published
status="$(request GET /v2/moments "$token_b")"
expect_status 200 "$status"
assert_jq_equal 1 '[.items[] | select(.id==$moment)] | length' --arg moment "$moment_id"
status="$(request DELETE "/v2/moments/$moment_id" "$token_a")"
expect_status 204 "$status"

status="$(request GET /v2/admin/wukong/settings "$admin_token")"
expect_status 200 "$status"
assert_json_equal '.prometheus_on' 1
assert_json_equal '.stress_on' 0

step="WuKongIM 系统账号 DataSource、运行缓存与后台操作"
status="$(request GET /v2/admin/wukong/system-users "$admin_token")"
expect_status 200 "$status"
status="$(request PUT "/v2/admin/wukong/system-users/$user_a" "$admin_token" '{"enabled":true,"confirmed":true,"reason":"本地验收临时系统账号"}')"
expect_status 202 "$status"
assert_json_equal '.item.syncStatus' pending
for _ in $(seq 1 50); do
  status="$(request GET /v2/admin/wukong/system-users "$admin_token")"
  expect_status 200 "$status"
  system_sync="$(jq -r --arg uid "$user_a" '.items[] | select(.userId==$uid) | .syncStatus' "$RESPONSE_FILE")"
  [[ "$system_sync" == "synced" ]] && break
  sleep 0.1
done
[[ "${system_sync:-}" == "synced" ]] || { echo "验收失败：系统账号新增未同步完成" >&2; exit 1; }
status="$(request POST /internal/wukong/datasource '' '{"cmd":"getSystemUIDs","data":{}}')"
expect_status 200 "$status"
assert_jq_equal 1 '[.[] | select(.==$uid)] | length' --arg uid "$user_a"
system_uids="$(curl --silent --show-error --fail --header "token: ${IM_WUKONG_MANAGER_TOKEN:-local-wukong-manager-token-change-me}" http://127.0.0.1:${WUKONG_API_PORT:-5001}/user/systemuids)"
printf '%s' "$system_uids" >"$RESPONSE_FILE"
assert_jq_equal 1 '[.[] | select(.==$uid)] | length' --arg uid "$user_a"
status="$(request PUT "/v2/contacts/blocks/$user_a" "$token_b" '{"blocked":true}')"
expect_status 200 "$status"
policy_payload="$(printf '%s' '{"type":1,"content":"system account acceptance"}' | base64 | tr -d '\r\n')"
for policy_tuple in "$user_a:$user_b" "$user_b:$user_a"; do
  policy_from="${policy_tuple%%:*}"
  policy_channel="${policy_tuple##*:}"
  status="$(curl --silent --show-error --output "$RESPONSE_FILE" --write-out '%{http_code}' \
    --request POST --header 'Content-Type: application/json' \
    --header "X-IM-Wukong-Policy-Secret: ${IM_WUKONG_POLICY_SECRET:-local-wukong-policy-secret-change-me-123456}" \
    --data "$(jq -nc --arg from "$policy_from" --arg channel "$policy_channel" --arg payload "$policy_payload" '{fromUid:$from,channelId:$channel,channelType:1,payload:$payload}')" \
    "$SERVER_URL/internal/wukong/policy/send")"
  expect_status 200 "$status"
  assert_json_equal '.allowed' true
  assert_json_equal '.reasonCode' 1
done
status="$(request PUT "/v2/contacts/blocks/$user_a" "$token_b" '{"blocked":false}')"
expect_status 200 "$status"
status="$(request PUT "/v2/admin/wukong/system-users/$user_a" "$admin_token" '{"enabled":false,"confirmed":true,"reason":"本地验收恢复普通账号"}')"
expect_status 202 "$status"
for _ in $(seq 1 50); do
  status="$(request GET /v2/admin/wukong/system-users "$admin_token")"
  expect_status 200 "$status"
  if [[ "$(jq -r --arg uid "$user_a" '[.items[] | select(.userId==$uid)] | length' "$RESPONSE_FILE")" == "0" ]]; then break; fi
  sleep 0.1
done
[[ "$(jq -r --arg uid "$user_a" '[.items[] | select(.userId==$uid)] | length' "$RESPONSE_FILE")" == "0" ]] || { echo "验收失败：系统账号撤销未同步完成" >&2; exit 1; }
system_uids="$(curl --silent --show-error --fail --header "token: ${IM_WUKONG_MANAGER_TOKEN:-local-wukong-manager-token-change-me}" http://127.0.0.1:${WUKONG_API_PORT:-5001}/user/systemuids)"
printf '%s' "$system_uids" >"$RESPONSE_FILE"
assert_jq_equal 0 '[.[] | select(.==$uid)] | length' --arg uid "$user_a"

status="$(request POST "/v2/admin/users/$user_b/ban" "$admin_token" '{"confirmed":true,"reason":"本地权限验收"}')"
expect_status 200 "$status"
assert_json_equal '.banned' true
status="$(request GET /v2/users/me "$token_b")"
expect_status 403 "$status"
status="$(request POST "/v2/admin/users/$user_b/unban" "$admin_token" '{"confirmed":true,"reason":"本地验收恢复账号"}')"
expect_status 200 "$status"
assert_json_equal '.banned' false
status="$(request POST "/v2/admin/reports/$report_id/resolve" "$admin_token" '{"action":"no_violation","confirmed":true,"reason":"本地自动验收"}')"
expect_status 200 "$status"
assert_json_equal '.action' no_violation
status="$(request POST /v2/admin/announcements "$admin_token" '{"title":"自动验收公告","content":"后台公告完整链路","status":"draft","pinned":true,"targetType":"all","targetUserIds":[],"pushOnPublish":false,"confirmed":true,"reason":"本地验收创建公告"}')"
expect_status 201 "$status"
admin_announcement_id="$(json_value '.id')"
status="$(request POST "/v2/admin/announcements/$admin_announcement_id/publish" "$admin_token" '{"enqueuePush":false,"confirmed":true,"reason":"本地验收发布公告"}')"
expect_status 200 "$status"
assert_json_equal '.status' published
status="$(request GET /v2/announcements "$token_a")"
expect_status 200 "$status"
assert_jq_equal 1 '[.items[] | select(.id==$announcement)] | length' --arg announcement "$admin_announcement_id"
status="$(request POST "/v2/announcements/$admin_announcement_id/read" "$token_a" '{}')"
expect_status 204 "$status"
status="$(request POST "/v2/admin/announcements/$admin_announcement_id/withdraw" "$admin_token" '{"confirmed":true,"reason":"本地验收撤回公告"}')"
expect_status 200 "$status"
assert_json_equal '.status' withdrawn
status="$(request DELETE "/v2/admin/announcements/$admin_announcement_id" "$admin_token" '{"confirmed":true,"reason":"本地验收清理草稿"}')"
expect_status 204 "$status"

step="表情商店分类、表情包、表情项、审核、收藏与最近使用"
status="$(request POST /v2/admin/sticker-categories "$admin_token" "$(jq -nc --arg name "验收分类$suffix" '{name:$name,sortOrder:1000,enabled:true,reason:"本地验收创建分类",confirmed:true}')")"
expect_status 200 "$status"
sticker_category_id="$(json_value '.item.id')"
status="$(request POST /v2/admin/sticker-packs "$admin_token" "$(jq -nc --arg category "$sticker_category_id" --arg media "$media_id" --arg name "验收表情包$suffix" '{categoryId:$category,name:$name,description:"本地表情商店验收",coverMediaId:$media,status:"reviewing",sortOrder:1000,reason:"本地验收创建表情包",confirmed:true}')")"
expect_status 200 "$status"
sticker_pack_id="$(json_value '.item.id')"
status="$(request POST "/v2/admin/sticker-packs/$sticker_pack_id/items" "$admin_token" "$(jq -nc --arg media "$media_id" '{name:"验收表情",mediaId:$media,emoji:"🙂",status:"published",sortOrder:1000,reason:"本地验收创建表情项",confirmed:true}')")"
expect_status 200 "$status"
sticker_item_id="$(json_value '.item.id')"
status="$(request POST "/v2/admin/sticker-packs/$sticker_pack_id/review" "$admin_token" '{"status":"published","reason":"本地验收审核通过","confirmed":true}')"
expect_status 200 "$status"
assert_json_equal '.status' published
status="$(request GET "/v2/stickers/packs?categoryId=$sticker_category_id" "$token_a")"
expect_status 200 "$status"
assert_json_equal '.items[0].id' "$sticker_pack_id"
assert_json_equal '.items[0].items[0].id' "$sticker_item_id"
status="$(request PUT "/v2/stickers/packs/$sticker_pack_id/favorite" "$token_a")"
expect_status 204 "$status"
status="$(request PUT "/v2/stickers/$sticker_item_id/favorite" "$token_a")"
expect_status 204 "$status"
status="$(request POST "/v2/stickers/$sticker_item_id/used" "$token_a")"
expect_status 204 "$status"
status="$(request GET /v2/stickers/favorites?limit=20 "$token_a")"
expect_status 200 "$status"
assert_jq_equal 1 '[.items[] | select(.id==$sticker)] | length' --arg sticker "$sticker_item_id"
status="$(request GET /v2/stickers/recent?limit=20 "$token_a")"
expect_status 200 "$status"
assert_jq_equal 1 '[.items[] | select(.id==$sticker)] | length' --arg sticker "$sticker_item_id"
status="$(request POST "/v2/admin/sticker-packs/$sticker_pack_id/review" "$admin_token" '{"status":"disabled","reason":"本地验收完成后下架","confirmed":true}')"
expect_status 200 "$status"
status="$(request PUT "/v2/admin/sticker-categories/$sticker_category_id" "$admin_token" "$(jq -nc --arg name "验收分类$suffix" '{name:$name,sortOrder:1000,enabled:false,reason:"本地验收完成后停用",confirmed:true}')")"
expect_status 200 "$status"

step="客服技能组、自动分配、排队、后台认领、转接、结束与评价"
status="$(request POST /v2/admin/support/skills "$admin_token" "$(jq -nc --arg name "验收客服$suffix" '{name:$name,description:"本地客服链路验收",routingStrategy:"least_active",maxConcurrentPerAgent:5,enabled:true,reason:"本地验收创建技能组",confirmed:true}')")"
expect_status 200 "$status"
support_skill_id="$(json_value '.item.id')"
status="$(request PUT "/v2/admin/support/agents/$user_b" "$admin_token" "$(jq -nc --arg skill "$support_skill_id" '{status:"available",maxConcurrent:5,skillGroupIds:[$skill],reason:"本地验收配置客服",confirmed:true}')")"
expect_status 200 "$status"
status="$(request POST /v2/support/sessions "$token_a" "$(jq -nc --arg skill "$support_skill_id" '{skillGroupId:$skill,subject:"本地客服验收",channelType:10,metadata:{source:"acceptance-local"}}')")"
expect_status 201 "$status"
support_session_id="$(json_value '.item.id')"
assert_json_equal '.item.assignedAgentId' "$user_b"
status="$(request GET "/v2/support/sessions/$support_session_id" "$token_b")"
expect_status 200 "$status"
assert_json_equal '.item.status' active
status="$(request POST "/v2/support/sessions/$support_session_id/end" "$token_b" '{}')"
expect_status 200 "$status"
assert_json_equal '.item.status' ended
status="$(request POST "/v2/support/sessions/$support_session_id/rating" "$token_a" '{"rating":5,"comment":"本地验收满意"}')"
expect_status 200 "$status"
assert_json_equal '.item.rating' 5

status="$(request PUT "/v2/admin/support/agents/$user_b" "$admin_token" "$(jq -nc --arg skill "$support_skill_id" '{status:"busy",maxConcurrent:5,skillGroupIds:[$skill],reason:"本地验收切换为手工认领",confirmed:true}')")"
expect_status 200 "$status"
status="$(request PUT "/v2/admin/support/agents/$user_c" "$admin_token" "$(jq -nc --arg skill "$support_skill_id" '{status:"busy",maxConcurrent:5,skillGroupIds:[$skill],reason:"本地验收配置转接坐席",confirmed:true}')")"
expect_status 200 "$status"
status="$(request POST /v2/support/sessions "$token_a" "$(jq -nc --arg skill "$support_skill_id" '{skillGroupId:$skill,subject:"本地客服排队与转接验收",channelType:10,metadata:{source:"acceptance-local-queue"}}')")"
expect_status 201 "$status"
queued_support_session_id="$(json_value '.item.id')"
assert_json_equal '.item.status' queued
assert_json_equal '.item.assignedAgentId' ''
status="$(request GET "/v2/admin/support/sessions?status=queued&q=$queued_support_session_id&limit=20" "$admin_token")"
expect_status 200 "$status"
assert_jq_equal 1 '.items[] | select(.id==$session) | .queuePosition' --arg session "$queued_support_session_id"
status="$(request POST "/v2/admin/support/sessions/$queued_support_session_id/claim" "$admin_token" "$(jq -nc --arg agent "$user_b" '{agentId:$agent,reason:"本地验收后台认领",confirmed:true}')")"
expect_status 200 "$status"
assert_json_equal '.item.status' active
assert_json_equal '.item.assignedAgentId' "$user_b"
status="$(request POST "/v2/admin/support/sessions/$queued_support_session_id/transfer" "$admin_token" "$(jq -nc --arg agent "$user_c" '{targetAgentId:$agent,reason:"本地验收后台转接",confirmed:true}')")"
expect_status 200 "$status"
assert_json_equal '.item.assignedAgentId' "$user_c"
assert_json_equal '.item.transferCount' 1
status="$(request GET "/v2/support/sessions/$queued_support_session_id" "$token_c")"
expect_status 200 "$status"
assert_json_equal '.item.status' active
status="$(request POST "/v2/support/sessions/$queued_support_session_id/end" "$token_c" '{}')"
expect_status 200 "$status"
assert_json_equal '.item.status' ended
status="$(request POST "/v2/support/sessions/$queued_support_session_id/rating" "$token_a" '{"rating":4,"comment":"排队转接链路验收完成"}')"
expect_status 200 "$status"
assert_json_equal '.item.rating' 4

step="解散扩展业务频道"
for channel_spec in "$business_topic_id:5" "$business_info_id:6" "$business_live_id:9" "$business_community_id:4"; do
  channel_id="${channel_spec%%:*}"
  channel_type="${channel_spec##*:}"
  status="$(request PATCH "/v2/channels/business/$channel_id?channelType=$channel_type" "$token_a" '{"disband":true}')"
  expect_status 200 "$status"
done
business_community_id=""; business_topic_id=""; business_info_id=""; business_live_id=""

step="转让群主并解散临时群"
status="$(request POST "/v2/channels/groups/$group_id/owner/transfer" "$token_a" "$(jq -nc --arg user "$user_b" '{userId:$user}')")"
expect_status 200 "$status"
status="$(request POST "/v2/channels/groups/$group_id/disband" "$token_b" '{"reason":"本地验收完成"}')"
expect_status 200 "$status"

step="注销临时账号"
for token in "$token_a" "$token_b" "$token_c"; do
  status="$(request POST /v2/users/me/deletion/code "$token" '{}')"
  expect_status 202 "$status"
  status="$(request DELETE /v2/users/me "$token" "$(jq -nc --arg code "$OTP_CODE" '{code:$code}')")"
  expect_status 204 "$status"
done

echo "本地全链路产品验收通过：账号与好友、单聊群聊、消息协作、定时消息真实派发/取消、媒体、朋友圈及后台隐藏/恢复、社区/话题/资讯/直播频道及精确互动事件、临时订阅与访问名单、WuKong 系统账号运行同步、表情商店运营/审核/收藏/最近使用、客服自动分配及排队/认领/转接/结束/评价、四端版本升级策略、LiveKit 单聊/三人群视频状态与令牌/后台房间/清理、管理后台与注销。"
