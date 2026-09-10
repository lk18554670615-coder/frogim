package app

import (
	"testing"

	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

func TestUpdateGroupProfileRejectsUnsupportedMessageRate(t *testing.T) {
	application, err := New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	invalid := 7
	if _, err = application.UpdateGroupProfile("actor", "group", store.GroupProfileUpdate{
		MemberMessageRateLimitPerMinute: &invalid,
	}); err != ErrInvalid {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}
