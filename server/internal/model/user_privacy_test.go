package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInternalUserClassificationIsNotPartOfSharedUserJSON(t *testing.T) {
	raw, err := json.Marshal(User{ID: "friend", Name: "Friend", IsInternalUser: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "isInternalUser") {
		t.Fatalf("shared friend/group user JSON leaked the internal-user classification: %s", raw)
	}
}
