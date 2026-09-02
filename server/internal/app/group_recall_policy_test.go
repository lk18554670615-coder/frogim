package app

import (
	"context"
	"github.com/linli/im/server/internal/teststore"
	"testing"
)

func TestGroupRecallSettingsIndependentAndBounded(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if a.AuthPolicy().GroupRecallMinutes != 1440 {
		t.Fatal("default must be 24 hours")
	}
	for _, minutes := range []float64{1, 60, 1440, 10080} {
		if err = a.UpdateSettings("admin", map[string]any{"groupRecallMinutes": minutes}); err != nil {
			t.Fatal(err)
		}
		policy := a.AuthPolicy()
		if policy.GroupRecallMinutes != int(minutes) || policy.MessageRecallMinutes != 2 {
			t.Fatalf("policy=%+v", policy)
		}
	}
	for _, value := range []any{float64(0), float64(10081), float64(1.5), "1440", nil} {
		if err = a.UpdateSettings("admin", map[string]any{"groupRecallMinutes": value}); err == nil {
			t.Fatalf("invalid value accepted: %v", value)
		}
	}
}
