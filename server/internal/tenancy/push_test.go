package tenancy

import (
	"testing"
	"time"
)

func TestPushRequestContract(t *testing.T) {
	now := time.Now().UTC()
	base := PushRequest{Identity: Identity{AccountID: "account", TenantID: "a", LocalUserID: "local", AssignmentVersion: 1}, RequestID: "request", AuthVersion: 1, RealmVersion: 1, EventType: "message.created", ConversationID: "conversation", MessageID: "message", MessageType: "text", ExpiresAt: now.Add(time.Hour)}
	if err := base.Validate(now); err != nil {
		t.Fatal(err)
	}
	for name, modify := range map[string]func(*PushRequest){
		"foreign url":   func(p *PushRequest) { p.ConversationID = "https://other.example" },
		"event":         func(p *PushRequest) { p.EventType = "messages.deleted" },
		"system":        func(p *PushRequest) { p.MessageType = "system" },
		"unbounded ttl": func(p *PushRequest) { p.ExpiresAt = now.Add(25 * time.Hour) },
		"zero expiry":   func(p *PushRequest) { p.ExpiresAt = time.Time{} },
		"zero version":  func(p *PushRequest) { p.AssignmentVersion = 0 },
		"mixed type":    func(p *PushRequest) { p.CallID = "call" },
	} {
		t.Run(name, func(t *testing.T) {
			p := base
			modify(&p)
			if p.Validate(now) == nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
	call := base
	call.EventType = "call.invited"
	call.MessageID = ""
	call.MessageType = ""
	call.CallID = "call"
	call.MediaType = "audio"
	if call.Validate(now) == nil {
		t.Fatal("hour-old call TTL accepted")
	}
	call.ExpiresAt = now.Add(45 * time.Second)
	if e := call.Validate(now); e != nil {
		t.Fatal(e)
	}
}
