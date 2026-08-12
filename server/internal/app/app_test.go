package app

import (
	"context"
	"testing"

	"github.com/linli/im/server/internal/store"
)

func TestMessageIdempotencyAndConversationSequence(t *testing.T) {
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
}

func TestMessageTransportOwnsPersistentMessageWhenInstalled(t *testing.T) {
	a, err := New(context.Background(), store.Memory{})
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
	var received MessageTransportRequest
	a.SetMessageTransport(func(_ context.Context, request MessageTransportRequest) (MessageTransportResult, error) {
		received = request
		return MessageTransportResult{MessageID: "987654321", ClientMsgID: request.ClientMsgID, MessageSeq: 7}, nil
	})
	message, duplicate, err := a.SendMessage("usr_alice", conversation.ID, "wk-client-1", "text", map[string]any{"text": "hello", "mentions": []string{"usr_bob"}}, "")
	if err != nil || duplicate {
		t.Fatalf("send duplicate=%v err=%v", duplicate, err)
	}
	if message.ID != "987654321" || message.Seq != 7 || received.Body["text"] != "hello" || len(received.Mentions) != 1 {
		t.Fatalf("unexpected transport result=%+v request=%+v", message, received)
	}
	history, err := a.History("usr_alice", conversation.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("transport message leaked into legacy message store: %+v", history)
	}
}

func TestBindMediaChannelAllowsAuthorizedForwardButRejectsOutsider(t *testing.T) {
	a, err := New(context.Background(), store.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := a.Login("13800009001", "Owner")
	recipient, _ := a.Login("13800009002", "Recipient")
	forwardTarget, _ := a.Login("13800009003", "Forward target")
	outsider, _ := a.Login("13800009004", "Outsider")
	if err = a.CreateMedia(store.Media{ID: "media-forward", OwnerID: owner.ID, ObjectKey: "objects/media-forward", MIME: "image/png", Size: 10, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if err = a.BindMediaChannel(store.MediaChannelBinding{MediaID: "media-forward", ChannelID: recipient.ID, ChannelType: 1, SenderID: owner.ID}); err != nil {
		t.Fatal(err)
	}
	if err = a.BindMediaChannel(store.MediaChannelBinding{MediaID: "media-forward", ChannelID: forwardTarget.ID, ChannelType: 1, SenderID: recipient.ID}); err != nil {
		t.Fatalf("authorized forward binding failed: %v", err)
	}
	if allowed, _ := a.CanAccessMedia(forwardTarget.ID, "media-forward"); !allowed {
		t.Fatal("forward target did not receive media access")
	}
	if err = a.BindMediaChannel(store.MediaChannelBinding{MediaID: "media-forward", ChannelID: forwardTarget.ID, ChannelType: 1, SenderID: outsider.ID}); err != ErrForbidden {
		t.Fatalf("outsider binding error=%v", err)
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
