package livekit

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/tenancy"
	lkauth "github.com/livekit/protocol/auth"
)

func TestTenantParticipantTokens(t *testing.T) {
	c, err := NewControl(Config{URL: "wss://enterprise.example/livekit", APIURL: "http://127.0.0.1:7880", APIKey: "tenant-a", APISecret: testSecret, TokenTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	identity := tenancy.Identity{AccountID: "account", TenantID: "a", LocalUserID: "local", AssignmentVersion: 3}
	session, err := c.IssueTenantParticipant("call-id", "conversation-id", "audio", identity, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.VerifyTenantParticipant(session.Token)
	if err != nil || p.Identity != identity || p.AuthVersion != 8 || p.RealmVersion != 1 || p.CallID != "call-id" {
		t.Fatal("scoped participant lost", err)
	}
	legacy, err := c.IssueParticipant("call-id", "local", "conversation-id", "audio")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.VerifyTenantParticipant(legacy.Token); !errors.Is(err, ErrTenantParticipant) {
		t.Fatal("legacy credential accepted")
	}
	if _, err = c.IssueTenantParticipant("call-id", "conversation-id", "audio", identity, 0, 1); err == nil {
		t.Fatal("missing auth generation accepted")
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*TenantParticipant, *lkauth.VideoGrant, *string, *string, *string)
		invalid bool
	}{
		{name: "server refreshed token", mutate: func(*TenantParticipant, *lkauth.VideoGrant, *string, *string, *string) {}},
		{name: "wrong signing key", invalid: true, mutate: func(_ *TenantParticipant, _ *lkauth.VideoGrant, _ *string, _ *string, s *string) {
			*s = strings.Repeat("x", 40)
		}},
		{name: "wrong issuer", invalid: true, mutate: func(_ *TenantParticipant, _ *lkauth.VideoGrant, _ *string, k *string, _ *string) { *k = "tenant-b" }},
		{name: "wrong subject", invalid: true, mutate: func(_ *TenantParticipant, _ *lkauth.VideoGrant, s *string, _ *string, _ *string) { *s = "other" }},
		{name: "wrong room", invalid: true, mutate: func(_ *TenantParticipant, v *lkauth.VideoGrant, _ *string, _ *string, _ *string) { v.Room = "other" }},
		{name: "old schema", invalid: true, mutate: func(p *TenantParticipant, _ *lkauth.VideoGrant, _ *string, _ *string, _ *string) {
			p.SchemaVersion = "1"
		}},
		{name: "editable metadata", invalid: true, mutate: func(_ *TenantParticipant, v *lkauth.VideoGrant, _ *string, _ *string, _ *string) {
			v.SetCanUpdateOwnMetadata(true)
		}},
		{name: "admin", invalid: true, mutate: func(_ *TenantParticipant, v *lkauth.VideoGrant, _ *string, _ *string, _ *string) { v.RoomAdmin = true }},
		{name: "no generation", invalid: true, mutate: func(p *TenantParticipant, _ *lkauth.VideoGrant, _ *string, _ *string, _ *string) {
			p.Identity.AssignmentVersion = 0
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := p
			grant := &lkauth.VideoGrant{RoomJoin: true, Room: CallRoomName(p.CallID)}
			grant.SetCanUpdateOwnMetadata(false)
			subject, key, secret := identity.LocalUserID, c.apiKey, testSecret
			tc.mutate(&copy, grant, &subject, &key, &secret)
			metadata, _ := json.Marshal(copy)
			token, err := lkauth.NewAccessToken(key, secret).SetIdentity(subject).SetVideoGrant(grant).SetMetadata(string(metadata)).SetValidFor(10 * time.Minute).ToJWT()
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.VerifyTenantParticipant(token)
			if (err != nil) != tc.invalid {
				t.Fatal("unexpected verification result", err)
			}
		})
	}
	for _, value := range []string{"", "not-a-jwt", strings.Repeat("x", 16385)} {
		if _, e := c.VerifyTenantParticipant(value); !errors.Is(e, ErrTenantParticipant) {
			t.Fatal("invalid token accepted")
		}
	}
}
