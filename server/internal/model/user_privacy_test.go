package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFriendLoginIPCapabilityIsNotPartOfSharedUserJSON(t *testing.T) {
	raw, err := json.Marshal(User{ID: "friend", Name: "Friend", CanViewFriendLoginIP: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "canViewFriendLoginIp") {
		t.Fatalf("shared friend/group user JSON leaked a private capability: %s", raw)
	}
}
