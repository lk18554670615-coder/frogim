package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

type recallPolicyTestStore struct {
	teststore.Memory
	store.RuntimeMutationStore
	store.MessageCollaborationStore
	recallWindow, editWindow time.Duration
	recallErr                error
}

func (s *recallPolicyTestStore) RecallAuthorized(_ context.Context, _, _ string, _ time.Time, window time.Duration) (string, int64, []string, error) {
	s.recallWindow = window
	return "direct", 1, nil, s.recallErr
}

func (s *recallPolicyTestStore) EditMessage(_ context.Context, _, _, _ string, _, _ map[string]any, _ time.Time, window time.Duration) (*model.Message, bool, error) {
	s.editWindow = window
	return &model.Message{}, false, nil
}

func TestDirectRecallPolicyDefaultsIndependentAndApplied(t *testing.T) {
	s := &recallPolicyTestStore{}
	a, err := New(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	check := func(direct, group, edit int) {
		t.Helper()
		policy := a.AuthPolicy()
		if policy.DirectRecallMinutes != direct || policy.GroupRecallMinutes != group || policy.MessageRecallMinutes != edit {
			t.Fatalf("policy=%+v", policy)
		}
		if err := a.Recall("sender", "message"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.EditMessage("sender", "message", "edit-test", map[string]any{"text": "edited"}); err != nil {
			t.Fatal(err)
		}
		if s.recallWindow != time.Duration(direct)*time.Minute || s.editWindow != time.Duration(edit)*time.Minute {
			t.Fatalf("recall=%v edit=%v", s.recallWindow, s.editWindow)
		}
	}
	check(1440, 1440, 2)
	// Existing installations need not persist a new key before the default works.
	delete(a.state.Settings, "directRecallMinutes")
	if err := a.UpdateSettings("admin", map[string]any{"messageRecallMinutes": float64(7), "groupRecallMinutes": float64(60)}); err != nil {
		t.Fatal(err)
	}
	check(1440, 60, 7)
	for _, minutes := range []float64{1, 60, 1440, 10080} {
		if err := a.UpdateSettings("admin", map[string]any{"directRecallMinutes": minutes}); err != nil {
			t.Fatal(err)
		}
		check(int(minutes), 60, 7)
	}
	for _, value := range []any{float64(0), float64(-1), float64(10081), float64(1.5), "1440", nil, true} {
		if err := a.UpdateSettings("admin", map[string]any{"directRecallMinutes": value}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid value %v: %v", value, err)
		}
		check(10080, 60, 7)
	}
	for _, pair := range []struct{ stored, public error }{{store.ErrForbidden, ErrForbidden}, {store.ErrNotFound, ErrNotFound}} {
		s.recallErr = pair.stored
		if err := a.Recall("sender", "message"); !errors.Is(err, pair.public) {
			t.Fatalf("recall error=%v want %v", err, pair.public)
		}
	}
}
