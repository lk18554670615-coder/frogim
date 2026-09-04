package store

import "testing"

func TestGroupSystemDigestDescribesOperation(t *testing.T) {
	tests := []struct {
		name  string
		event string
		data  map[string]any
		want  string
	}{
		{name: "announcement", event: "group.announcement.updated", data: map[string]any{}, want: "群公告已更新，点击查看"},
		{name: "mute all", event: "group.mute_all.updated", data: map[string]any{"muted": true}, want: "已开启全员禁言"},
		{name: "unmute member", event: "group.member.mute", data: map[string]any{"muted": false}, want: "已解除一名群成员的禁言"},
		{name: "pin", event: "group.message.pinned", data: map[string]any{}, want: "已置顶一条群消息"},
		{name: "admin", event: "group.member.role", data: map[string]any{"role": "admin"}, want: "已设置一名群管理员"},
		{name: "members", event: "group.members.added", data: map[string]any{"userIds": []string{"u1", "u2"}}, want: "2 位成员已加入群聊"},
		{name: "unknown", event: "group.future.event", data: map[string]any{}, want: "[群系统消息]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := groupSystemDigest(tt.event, tt.data); got != tt.want {
				t.Fatalf("groupSystemDigest() = %q, want %q", got, tt.want)
			}
		})
	}
}
