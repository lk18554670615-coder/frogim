package store

import (
	"regexp"
	"testing"
)

func TestRandomInviteCodesUseUnambiguousUniqueAlphabet(t *testing.T) {
	pattern := regexp.MustCompile(`^[23456789ABCDEFGHJKMNPQRSTUVWXYZ]{10}$`)
	seen := make(map[string]struct{}, 512)
	for range 512 {
		code, err := randomInviteCode()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(code) {
			t.Fatalf("invalid generated code %q", code)
		}
		if _, exists := seen[code]; exists {
			t.Fatalf("duplicate generated code %q", code)
		}
		seen[code] = struct{}{}
	}
}

func TestInviteModeRules(t *testing.T) {
	for _, test := range []struct {
		mode, code string
		wantErr    error
	}{
		{mode: "disabled"},
		{mode: "disabled", code: "IGNORED"},
		{mode: "optional"},
		{mode: "optional", code: "CODE"},
		{mode: "required", code: "CODE"},
		{mode: "required", wantErr: ErrInviteRequired},
		{mode: "invalid", wantErr: ErrInviteInvalid},
	} {
		if err := validateInviteMode(test.mode, test.code); err != test.wantErr {
			t.Fatalf("validateInviteMode(%q,%q)=%v, want %v", test.mode, test.code, err, test.wantErr)
		}
	}
}
