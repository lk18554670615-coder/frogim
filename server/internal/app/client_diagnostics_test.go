package app

import (
	"context"
	"testing"

	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

type diagnosticTestStore struct {
	teststore.Memory
	items []store.ClientDiagnostic
}

func (s *diagnosticTestStore) RecordClientDiagnostic(_ context.Context, item store.ClientDiagnostic) error {
	s.items = append(s.items, item)
	return nil
}

func (s *diagnosticTestStore) ListAdminClientDiagnostics(_ context.Context, _, _ string, _ int) ([]store.ClientDiagnostic, map[string]any, error) {
	return append([]store.ClientDiagnostic(nil), s.items...), map[string]any{"windowHours": 24}, nil
}

func TestClientDiagnosticsAcceptOnlyBoundedMetadata(t *testing.T) {
	persistence := &diagnosticTestStore{}
	application, err := New(context.Background(), persistence)
	if err != nil {
		t.Fatal(err)
	}
	duration := int64(820)
	err = application.RecordClientDiagnostic("user-1", store.ClientDiagnostic{
		Kind: "performance", Name: "app_start", Fingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Platform: "android", AppVersion: "1.0.0+1", DurationMS: &duration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(persistence.items) != 1 {
		t.Fatalf("items=%d want=1", len(persistence.items))
	}
	stored := persistence.items[0]
	if stored.ID == "" || stored.UserID != "user-1" || stored.OccurredAt.IsZero() || stored.DurationMS == nil || *stored.DurationMS != duration {
		t.Fatalf("unexpected stored diagnostic: %+v", stored)
	}

	invalid := []store.ClientDiagnostic{
		{Kind: "crash", Name: "flutter_error", Fingerprint: "raw-stack", Platform: "android", AppVersion: "1.0.0"},
		{Kind: "performance", Name: "app_start", Fingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Platform: "android", AppVersion: "1.0.0"},
		{Kind: "message", Name: "chat_body", Fingerprint: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Platform: "android", AppVersion: "1.0.0"},
	}
	for index, item := range invalid {
		if err = application.RecordClientDiagnostic("user-1", item); err != ErrInvalid {
			t.Fatalf("invalid[%d] error=%v want=%v", index, err, ErrInvalid)
		}
	}
	if len(persistence.items) != 1 {
		t.Fatalf("invalid diagnostics were stored: %d", len(persistence.items))
	}
}
