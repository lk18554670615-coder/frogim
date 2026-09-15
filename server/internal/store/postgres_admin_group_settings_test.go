package store

import "testing"

func TestAdminGroupAvatarMediaMustBelongToTargetGroup(t *testing.T) {
	tests := []struct {
		name, owner, expectedOwner, status, mime, key, groupID string
		allowed                                                bool
	}{
		{"valid", "owner-1", "owner-1", "ready", "image/png", "groups/group-1/2026/09/med.png", "group-1", true},
		{"other group", "owner-1", "owner-1", "ready", "image/png", "groups/group-2/2026/09/med.png", "group-1", false},
		{"ordinary user upload", "owner-1", "owner-1", "ready", "image/png", "users/owner-1/2026/09/med.png", "group-1", false},
		{"other owner", "owner-2", "owner-1", "ready", "image/png", "groups/group-1/2026/09/med.png", "group-1", false},
		{"pending", "owner-1", "owner-1", "pending", "image/png", "groups/group-1/2026/09/med.png", "group-1", false},
		{"not image", "owner-1", "owner-1", "ready", "video/mp4", "groups/group-1/2026/09/med.mp4", "group-1", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := adminGroupAvatarMediaAllowed(test.owner, test.expectedOwner, test.status, test.mime, test.key, test.groupID); got != test.allowed {
				t.Fatalf("allowed=%v want=%v", got, test.allowed)
			}
		})
	}
}

func TestAdminAnnouncementAuditValueDoesNotCopyBody(t *testing.T) {
	value := adminAnnouncementAuditValue("confidential announcement body", 7)
	if value["version"] != int64(7) || value["length"] != 30 || value["digest"] == "" {
		t.Fatalf("unexpected audit summary: %#v", value)
	}
	for _, field := range value {
		if field == "confidential announcement body" {
			t.Fatal("announcement body leaked into audit metadata")
		}
	}
}
