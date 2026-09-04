package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/linli/im/server/internal/teststore"
)

func TestInvitationPolicyDefaultsAndValidation(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.AuthPolicy().InviteRegistrationMode; got != "optional" {
		t.Fatalf("default invitation mode=%q", got)
	}
	for _, mode := range []string{"disabled", "optional", "required"} {
		if err = a.UpdateSettings("admin", map[string]any{"inviteRegistrationMode": mode}); err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
		if got := a.AuthPolicy().InviteRegistrationMode; got != mode {
			t.Fatalf("mode=%q, want %q", got, mode)
		}
	}
	for _, invalid := range []any{"", "OPTION", "required ", true, float64(1)} {
		if err = a.UpdateSettings("admin", map[string]any{"inviteRegistrationMode": invalid}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid mode %#v err=%v", invalid, err)
		}
	}
}

func TestInvitationRelationTimeFilters(t *testing.T) {
	for _, value := range []string{"2026-09-04", "2026-09-05T00:00:00Z", "2026-09-05T08:00:00.123456+08:00"} {
		if _, err := parseInviteFilterTime(value); err != nil {
			t.Fatalf("valid time %q rejected: %v", value, err)
		}
	}
	if _, err := parseInviteFilterTime("2026/09/04"); err == nil {
		t.Fatal("invalid date accepted")
	}
	from, _ := parseInviteFilterTime("2026-09-05")
	to, _ := parseInviteFilterTime("2026-09-04")
	if from.Before(to) || from.Equal(to) || from.Sub(to) != 24*time.Hour {
		t.Fatalf("unexpected date ordering: from=%v to=%v", from, to)
	}
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.AdminInviteRelations(context.Background(), "", "", "2026-09-05", "2026-09-04", "", 20); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reversed relation range error=%v", err)
	}
}

func TestCustomInvitationCodeRules(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"ABCDEF", "HELLO-WORLD", "USER_2026", "A234567890123456789Z"} {
		if !a.validCustomInviteCode(code) {
			t.Fatalf("valid custom code rejected: %s", code)
		}
	}
	for _, code := range []string{"ABCDE", "_ABCDEF", "ABCDEF_", "12345678901", "ADMIN", "HAS SPACE", "A2345678901234567890Z"} {
		if a.validCustomInviteCode(code) {
			t.Fatalf("invalid custom code accepted: %s", code)
		}
	}
}
