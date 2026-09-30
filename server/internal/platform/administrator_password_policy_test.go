package platform

import (
	"strings"
	"testing"
)

func TestAdministratorPasswordPolicy(t *testing.T) {
	for _, password := range []string{"123456", strings.Repeat("a", 32), strings.Repeat("密", 24)} {
		if !validAdminPassword(password) {
			t.Fatalf("valid administrator password rejected: %d characters", len([]rune(password)))
		}
	}
	for _, password := range []string{"12345", strings.Repeat("a", 33), strings.Repeat("密", 25)} {
		if validAdminPassword(password) {
			t.Fatalf("invalid administrator password accepted: %d characters", len([]rune(password)))
		}
	}
}
