package store

import "fmt"

// groupSystemDigest keeps stored messages and older clients useful while the
// structured event remains the authoritative source for newer clients.
func groupSystemDigest(event string, data map[string]any) string {
	switch event {
	case "group.created":
		return "群聊已创建"
	case "group.profile.updated":
		return "群资料已更新"
	case "group.announcement.updated":
		return "群公告已更新，点击查看"
	case "group.history.updated":
		if visible, ok := groupSystemBool(data["historyVisibleToNewMembers"]); ok {
			if visible {
				return "已允许新成员查看入群前消息"
			}
			return "已关闭新成员查看入群前消息"
		}
		return "群历史消息可见范围已更新"
	case "group.invite.accepted":
		return "有成员接受邀请并加入群聊"
	case "group.invite.rejected":
		return "有成员拒绝了群聊邀请"
	case "group.invite.cancelled":
		return "群聊邀请已取消"
	case "group.member.joined":
		if data["source"] == "qr" {
			return "有成员通过二维码加入群聊"
		}
		return "有成员加入群聊"
	case "group.members.added", "group.member_added":
		if count := groupSystemListLength(data["userIds"]); count > 1 {
			return fmt.Sprintf("%d 位成员已加入群聊", count)
		}
		return "有成员加入群聊"
	case "group.member.leave":
		return "有成员退出群聊"
	case "group.member.remove":
		return "有成员被移出群聊"
	case "group.member.role":
		switch data["role"] {
		case "admin":
			return "已设置一名群管理员"
		case "member":
			return "已取消一名群管理员"
		default:
			return "群成员角色已更新"
		}
	case "group.member.transfer":
		return "群主已转让"
	case "group.member.mute":
		if muted, ok := groupSystemBool(data["muted"]); ok {
			if muted {
				return "已禁言一名群成员"
			}
			return "已解除一名群成员的禁言"
		}
		if until, exists := data["mutedUntil"]; exists {
			if until == nil || fmt.Sprint(until) == "" {
				return "已解除一名群成员的禁言"
			}
			return "已禁言一名群成员"
		}
		return "群成员禁言设置已更新"
	case "group.member.nickname":
		return "群昵称已更新"
	case "group.blacklist.added":
		return "有成员被加入群黑名单"
	case "group.blacklist.removed":
		return "有成员已移出群黑名单"
	case "group.mute_all.updated":
		if muted, ok := groupSystemBool(data["muted"]); ok {
			if muted {
				return "已开启全员禁言"
			}
			return "已解除全员禁言"
		}
		return "全员禁言设置已更新"
	case "group.ban.updated":
		if banned, ok := groupSystemBool(data["banned"]); ok {
			if banned {
				return "群聊已封禁"
			}
			return "群聊已解除封禁"
		}
		return "群聊封禁状态已更新"
	case "group.message.pinned":
		return "已置顶一条群消息"
	case "group.message.unpinned":
		return "已取消一条群消息置顶"
	case "group.disbanded":
		return "群聊已解散"
	default:
		return "[群系统消息]"
	}
}

func groupSystemBool(value any) (bool, bool) {
	switch value := value.(type) {
	case bool:
		return value, true
	case int:
		return value != 0, true
	case int64:
		return value != 0, true
	case float64:
		return value != 0, true
	case string:
		switch value {
		case "true", "1":
			return true, true
		case "false", "0":
			return false, true
		}
	}
	return false, false
}

func groupSystemListLength(value any) int {
	switch value := value.(type) {
	case []string:
		return len(value)
	case []any:
		return len(value)
	default:
		return 0
	}
}
