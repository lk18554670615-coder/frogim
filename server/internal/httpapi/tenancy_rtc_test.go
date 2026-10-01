package httpapi

import (
	livekitcontrol "github.com/linli/im/server/internal/livekit"
	"testing"
	"time"
)

func TestEnterpriseCallTokenFence(t *testing.T) {
	secret := "local-call-secret-for-enterprise-a"
	c, err := livekitcontrol.NewControl(livekitcontrol.Config{URL: "ws://127.0.0.1:18701/livekit", APIURL: "http://127.0.0.1:7880", APIKey: "devkey", APISecret: secret, TokenTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.IssueEnterpriseParticipant("call1", "user1", "conversation1", "audio", "enterprise-a", 4)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := verifyEnterpriseCallToken(s.Token, "devkey", secret, "enterprise-a")
	if err != nil || scope.User != "user1" || scope.Call != "call1" || scope.Epoch != 4 {
		t.Fatalf("scope=%+v err=%v", scope, err)
	}
	if _, err = verifyEnterpriseCallToken(s.Token, "devkey", secret, "enterprise-b"); err == nil {
		t.Fatal("another enterprise accepted call token")
	}
	if _, err = verifyEnterpriseCallToken(s.Token, "devkey", secret+"other", "enterprise-a"); err == nil {
		t.Fatal("invalid signature accepted")
	}
	legacy, _ := c.IssueParticipant("call1", "user1", "conversation1", "audio")
	if _, err = verifyEnterpriseCallToken(legacy.Token, "devkey", secret, "enterprise-a"); err == nil {
		t.Fatal("unscoped legacy call token accepted")
	}
}
