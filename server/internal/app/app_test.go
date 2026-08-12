package app

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

type testWukongRuntime struct {
	mu       sync.Mutex
	seq      map[string]int64
	byClient map[string]*model.Message
	byID     map[string]*model.Message
}

func installTestWukongRuntime(a *App) *testWukongRuntime {
	runtime := &testWukongRuntime{seq: map[string]int64{}, byClient: map[string]*model.Message{}, byID: map[string]*model.Message{}}
	a.SetMessageTransport(func(_ context.Context, request MessageTransportRequest) (MessageTransportResult, error) {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		key := request.UserID + ":" + request.ClientMsgID
		if existing := runtime.byClient[key]; existing != nil {
			return MessageTransportResult{MessageID: existing.ID, ClientMsgID: existing.ClientMsgID, MessageSeq: existing.Seq, CreatedAt: existing.CreatedAt, Duplicate: true}, nil
		}
		runtime.seq[request.ConversationID]++
		created := time.Now().UTC()
		message := &model.Message{ID: fmt.Sprintf("%d", len(runtime.byID)+1001), ClientMsgID: request.ClientMsgID, ConversationID: request.ConversationID, SenderID: request.UserID, Seq: runtime.seq[request.ConversationID], Type: request.Type, Body: request.Body, ReplyToID: request.ReplyToID, CreatedAt: created}
		runtime.byClient[key], runtime.byID[message.ID] = message, message
		return MessageTransportResult{MessageID: message.ID, ClientMsgID: message.ClientMsgID, MessageSeq: message.Seq, CreatedAt: created}, nil
	})
	a.SetMessageSourceLoader(func(_ context.Context, _ string, ids []string) ([]*model.Message, error) {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		items := make([]*model.Message, 0, len(ids))
		for _, id := range ids {
			message := runtime.byID[id]
			if message == nil {
				return nil, store.ErrForbidden
			}
			copy := *message
			items = append(items, &copy)
		}
		return items, nil
	})
	return runtime
}

func TestMessageIdempotencyAndConversationSequence(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	installTestWukongRuntime(a)
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
	a, err := New(context.Background(), teststore.Memory{})
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
	if _, err := a.History("usr_alice", conversation.ID, 0, 10); err != ErrUnavailable {
		t.Fatalf("history without canonical WuKong loader error=%v", err)
	}
}

func TestBindMediaChannelAllowsAuthorizedForwardButRejectsOutsider(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
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

func TestGroupMentionMetadataIsPassedToWukongTransport(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	var received MessageTransportRequest
	a.SetMessageTransport(func(_ context.Context, request MessageTransportRequest) (MessageTransportResult, error) {
		received = request
		return MessageTransportResult{MessageID: "2001", ClientMsgID: request.ClientMsgID, MessageSeq: 1}, nil
	})
	group, err := a.CreateGroup("usr_alice", "mention count", []string{"usr_bob"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = a.SendMessage("usr_alice", group.ID, "mention-one", "text", map[string]any{"text": "@Bob", "mentions": []string{"usr_bob"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(received.Mentions) != 1 || received.Mentions[0] != "usr_bob" || received.MentionAll {
		t.Fatalf("transport mention metadata=%+v", received)
	}
}

func TestUserCannotSendSystemMessage(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
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

func TestMessageMutationDoesNotFallBackToMemory(t *testing.T) {
	a, _ := New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	g, err := a.CreateGroup("usr_alice", "Project", []string{"usr_bob"})
	if err != nil {
		t.Fatal(err)
	}
	installTestWukongRuntime(a)
	m, _, err := a.SendMessage("usr_bob", g.ID, "group-1", "text", map[string]any{"text": "status"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Recall("usr_alice", m.ID); err != ErrUnavailable {
		t.Fatalf("memory mutation fallback error=%v", err)
	}
}
