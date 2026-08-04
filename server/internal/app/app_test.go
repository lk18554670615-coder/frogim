package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/store"
)

type failingEphemeralStore struct{ store.Memory }

func (failingEphemeralStore) PublishEphemeral(context.Context, []string, string, map[string]any) error {
	return errors.New("redis unavailable")
}
func (failingEphemeralStore) RunEphemeral(context.Context, func([]string, string, map[string]any)) error {
	return nil
}

func TestMessageIdempotencySequenceAndSync(t *testing.T) {
	a, err := New(context.Background(), store.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	c, err := a.DirectConversation("usr_alice", "usr_bob")
	if err != nil {
		t.Fatal(err)
	}
	first, duplicate, err := a.SendMessage("usr_alice", c.ID, "client-1", "text", map[string]any{"text": "hello"}, "")
	if err != nil || duplicate {
		t.Fatalf("first send: duplicate=%v err=%v", duplicate, err)
	}
	again, duplicate, err := a.SendMessage("usr_alice", c.ID, "client-1", "text", map[string]any{"text": "hello"}, "")
	if err != nil || !duplicate {
		t.Fatalf("duplicate send: duplicate=%v err=%v", duplicate, err)
	}
	if again.ID != first.ID || first.Seq != 1 {
		t.Fatalf("idempotency/seq mismatch: first=%+v again=%+v", first, again)
	}
	second, _, err := a.SendMessage("usr_bob", c.ID, "client-2", "text", map[string]any{"text": "world"}, "")
	if err != nil || second.Seq != 2 {
		t.Fatalf("second sequence=%d err=%v", second.Seq, err)
	}
	events, cursor, more := a.Sync("usr_bob", 0, 100)
	if len(events) != 3 || cursor != 3 || more {
		t.Fatalf("sync len=%d cursor=%d more=%v", len(events), cursor, more)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Seq != events[i-1].Seq+1 {
			t.Fatalf("non-contiguous sync seq: %+v", events)
		}
	}
}

func TestSyncPaginationCursorNeverSkipsEvents(t *testing.T) {
	a, err := New(context.Background(), store.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	c, err := a.DirectConversation("usr_alice", "usr_bob")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, _, err = a.SendMessage("usr_alice", c.ID, fmt.Sprintf("page-%d", i), "text", map[string]any{"text": "x"}, ""); err != nil {
			t.Fatal(err)
		}
	}

	after := int64(0)
	var seen []int64
	for {
		events, cursor, more := a.Sync("usr_bob", after, 2)
		if len(events) > 0 && cursor != events[len(events)-1].Seq {
			t.Fatalf("cursor=%d last=%d", cursor, events[len(events)-1].Seq)
		}
		for _, event := range events {
			seen = append(seen, event.Seq)
		}
		after = cursor
		if !more {
			break
		}
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] != seen[i-1]+1 {
			t.Fatalf("sync skipped events: %v", seen)
		}
	}
}

func TestGroupMentionUnreadCountIsExactAndClearsOnRead(t *testing.T) {
	a, err := New(context.Background(), store.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	group, err := a.CreateGroup("usr_alice", "mention count", []string{"usr_bob"})
	if err != nil {
		t.Fatal(err)
	}
	message, _, err := a.SendMessage("usr_alice", group.ID, "mention-one", "text", map[string]any{"text": "@Bob", "mentions": []string{"usr_bob"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.SendMessage("usr_alice", group.ID, "ordinary-one", "text", map[string]any{"text": "ordinary"}, ""); err != nil {
		t.Fatal(err)
	}
	items := a.Conversations("usr_bob")
	var mentionCount int64
	for _, item := range items {
		if conversation := item["conversation"]; conversation != nil && item["mentionUnreadCount"] != nil {
			if candidate, ok := item["mentionUnreadCount"].(int64); ok && candidate > mentionCount {
				mentionCount = candidate
			}
		}
	}
	if mentionCount != 1 {
		t.Fatalf("mentionUnreadCount=%d", mentionCount)
	}
	if err = a.Read("usr_bob", group.ID, message.Seq); err != nil {
		t.Fatal(err)
	}
	for _, item := range a.Conversations("usr_bob") {
		if count, _ := item["mentionUnreadCount"].(int64); count != 0 {
			t.Fatalf("mention count after read=%d", count)
		}
	}
}

func TestUserCannotSendSystemMessage(t *testing.T) {
	a, err := New(context.Background(), store.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	c, err := a.DirectConversation("usr_alice", "usr_bob")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.SendMessage("usr_alice", c.ID, "forged-system", "system", map[string]any{"text": "管理员已封禁对方"}, ""); err != ErrInvalid {
		t.Fatalf("forged system message err=%v", err)
	}
}

func TestCallSignalFailsClosedWhenCrossNodePublishFails(t *testing.T) {
	a, err := New(context.Background(), failingEphemeralStore{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	conversation, err := a.DirectConversation("usr_alice", "usr_bob")
	if err != nil {
		t.Fatal(err)
	}
	call, _, err := a.InviteCall("usr_alice", conversation.ID, "usr_bob", "call-fail-closed", "audio")
	if err != nil {
		t.Fatal(err)
	}
	err = a.SignalCall("usr_alice", conversation.ID, "call.ice", map[string]any{
		"callId":    call.ID,
		"candidate": map[string]any{"candidate": "candidate:1 1 UDP 1 127.0.0.1 9 typ host"},
	})
	if err == nil || !strings.Contains(err.Error(), "publish realtime call signal") {
		t.Fatalf("signal must fail closed, err=%v", err)
	}
}

func TestGroupRoleMuteAndRecallRules(t *testing.T) {
	a, _ := New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	g, err := a.CreateGroup("usr_alice", "Project", []string{"usr_bob"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetGroupRole("usr_alice", g.ID, "usr_bob", "admin"); err != nil {
		t.Fatal(err)
	}
	m, _, err := a.SendMessage("usr_bob", g.ID, "group-1", "text", map[string]any{"text": "status"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Recall("usr_alice", m.ID); err != nil {
		t.Fatalf("owner recall: %v", err)
	}
	if m.RecalledAt == nil || len(m.Body) != 0 {
		t.Fatalf("message was not redacted: %+v", m)
	}
}
