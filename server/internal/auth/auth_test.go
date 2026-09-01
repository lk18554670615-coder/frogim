package auth

import (
	"strings"
	"testing"
	"time"
)

func TestDeviceSessionClaimsShareLogicalSessionAcrossRotation(t *testing.T) {
	manager := Manager{Secret: []byte(strings.Repeat("s", 32)), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	access, refresh, sessionID, err := manager.IssueDeviceSession("user-1", "app")
	if err != nil {
		t.Fatal(err)
	}
	accessClaims, err := manager.ParseClaims(access, "access")
	if err != nil {
		t.Fatal(err)
	}
	refreshClaims, err := manager.ParseClaims(refresh, "refresh")
	if err != nil {
		t.Fatal(err)
	}
	if sessionID == "" || accessClaims.SessionID != sessionID || refreshClaims.SessionID != sessionID || accessClaims.DeviceKind != "app" || refreshClaims.DeviceKind != "app" {
		t.Fatalf("unexpected device session claims: access=%+v refresh=%+v", accessClaims, refreshClaims)
	}
	rotatedAccess, rotatedRefresh, err := manager.RotateDeviceSession("user-1", sessionID, "app")
	if err != nil {
		t.Fatal(err)
	}
	rotatedAccessClaims, _ := manager.ParseClaims(rotatedAccess, "access")
	rotatedRefreshClaims, _ := manager.ParseClaims(rotatedRefresh, "refresh")
	if rotatedAccessClaims.SessionID != sessionID || rotatedRefreshClaims.SessionID != sessionID {
		t.Fatal("rotation changed the logical device session")
	}
	if rotatedAccessClaims.ID == accessClaims.ID || rotatedRefreshClaims.ID == refreshClaims.ID {
		t.Fatal("rotation must issue new token JTIs")
	}
}
