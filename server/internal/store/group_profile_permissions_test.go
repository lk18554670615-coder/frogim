package store

import (
	"testing"
	"time"
)

func TestCanUpdateGroupProfileAllowsAdminMuteAllOnlyOutsideOwnerControls(t *testing.T) {
	name, joinPolicy, allow, muted, rateLimit := "群名", "invite", true, time.Now(), 10
	tests := []struct {
		name   string
		role   string
		update GroupProfileUpdate
		want   bool
	}{
		{name: "owner", role: "owner", update: GroupProfileUpdate{JoinPolicy: &joinPolicy, AllowMemberAddFriend: &allow, RotateQR: true}, want: true},
		{name: "admin mute all", role: "admin", update: GroupProfileUpdate{AllMutedUntil: &muted}, want: true},
		{name: "admin regular profile", role: "admin", update: GroupProfileUpdate{Name: &name}, want: true},
		{name: "admin message rate", role: "admin", update: GroupProfileUpdate{MemberMessageRateLimitPerMinute: &rateLimit}, want: true},
		{name: "admin join policy", role: "admin", update: GroupProfileUpdate{JoinPolicy: &joinPolicy}},
		{name: "admin member discovery", role: "admin", update: GroupProfileUpdate{AllowMemberAddFriend: &allow}},
		{name: "admin rotate qr", role: "admin", update: GroupProfileUpdate{RotateQR: true}},
		{name: "member mute all", role: "member", update: GroupProfileUpdate{AllMutedUntil: &muted}},
		{name: "member message rate", role: "member", update: GroupProfileUpdate{MemberMessageRateLimitPerMinute: &rateLimit}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canUpdateGroupProfile(test.role, test.update); got != test.want {
				t.Fatalf("canUpdateGroupProfile(%q)=%v want %v", test.role, got, test.want)
			}
		})
	}
}
