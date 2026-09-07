package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

type inviteBindingSpy struct {
	teststore.Memory
	store.InvitationStore
	calls        int
	code, reason string
	result       error
}

func (s *inviteBindingSpy) SetAdminInviteRelation(_ context.Context, actor, user, code, reason string, version int64, _ time.Time) (*store.InviteRelationBinding, error) {
	s.calls++
	s.code = code
	s.reason = reason
	return &store.InviteRelationBinding{InviteCode: code, Version: version + 1}, s.result
}
func TestAdminInviteBindingValidation(t *testing.T) {
	spy := &inviteBindingSpy{}
	a, err := New(t.Context(), spy)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		code, reason string
		version      int64
		want         error
	}{
		{"ABCDEF88", "", 0, ErrInvalid}, {"ABCDEF88", strings.Repeat("字", 501), 0, ErrInvalid},
		{"ABCDEF88", "reason", -1, ErrInvalid}, {"abc", "reason", 0, ErrInviteInvalid},
	} {
		if _, err = a.AdminSetInviteRelation(t.Context(), "admin", "user", tt.code, tt.reason, tt.version); !errors.Is(err, tt.want) {
			t.Fatalf("%+v: %v", tt, err)
		}
	}
	if spy.calls != 0 {
		t.Fatal("invalid input reached persistence")
	}
	for _, tt := range []struct{ storeErr, appErr error }{{nil, nil}, {store.ErrInviteInvalid, ErrInviteInvalid}, {store.ErrInviteRelationCycle, ErrInviteRelationCycle}, {store.ErrInviteRelationStale, ErrInviteRelationStale}, {store.ErrNotFound, ErrNotFound}} {
		spy.result = tt.storeErr
		_, err = a.AdminSetInviteRelation(t.Context(), "admin", "user", " abcdef88 ", " reason ", 0)
		if !errors.Is(err, tt.appErr) || spy.code != "ABCDEF88" || spy.reason != "reason" {
			t.Fatalf("mapping/normalization: %v %+v", err, spy)
		}
	}
}
