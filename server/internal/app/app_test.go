package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

func TestPhoneNumberValidationMatchesPublicAuthContract(t *testing.T) {
	tests := []struct {
		name  string
		phone string
		valid bool
	}{
		{name: "mainland mobile", phone: "13800138000", valid: true},
		{name: "international prefix", phone: "+8613800138000", valid: true},
		{name: "minimum length", phone: "123456", valid: true},
		{name: "outer whitespace", phone: " 13800138000 ", valid: true},
		{name: "too short", phone: "12345"},
		{name: "letters", phone: "abc123456"},
		{name: "internal whitespace", phone: "138 00138000"},
		{name: "double plus", phone: "++8613800138000"},
		{name: "plus only", phone: "+"},
		{name: "too long", phone: strings.Repeat("1", 33)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ValidPhoneNumber(test.phone); got != test.valid {
				t.Fatalf("ValidPhoneNumber(%q)=%v, want %v", test.phone, got, test.valid)
			}
		})
	}

	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Login("abc123456", "非法号码"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Login malformed phone err=%v", err)
	}
	if _, err = a.RegisterWithPassword("abc123456", "非法号码", "password123"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("RegisterWithPassword malformed phone err=%v", err)
	}
	if _, err = a.PasswordLogin("abc123456", "password123"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("PasswordLogin malformed phone err=%v", err)
	}
	if err = a.ResetPassword("abc123456", "password123"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ResetPassword malformed phone err=%v", err)
	}
	if _, err = a.UpdateUserPhone("missing-user", "abc123456"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("UpdateUserPhone malformed phone err=%v", err)
	}
}

func TestAuthPolicyUsesCharacterMinimumAndBcryptByteMaximum(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.UpdateSettings("admin", map[string]any{
		"registrationEnabled": false,
		"passwordMinLength":   float64(12),
	}); err != nil {
		t.Fatal(err)
	}
	policy := a.AuthPolicy()
	if policy.RegistrationEnabled || policy.PasswordMinLength != 12 || policy.PasswordMaxBytes != 72 || policy.MessageRecallMinutes != 2 {
		t.Fatalf("policy=%+v", policy)
	}
	if a.validPassword("十一位密码abc123") {
		t.Fatal("password below the configured character minimum was accepted")
	}
	if !a.validPassword("十二位密码规则abc1234") {
		t.Fatal("password matching the configured character minimum was rejected")
	}
	if a.validPassword(strings.Repeat("界", 25)) {
		t.Fatal("password exceeding bcrypt's 72-byte limit was accepted")
	}
}

func TestQRLoginTicketsRequireConfirmationAndAreConsumedOnce(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	user, err := a.RegisterWithPassword("13900009999", "扫码用户", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	qrHash := sha256.Sum256([]byte("qr-secret"))
	pollHash := sha256.Sum256([]byte("poll-secret"))
	ticket := store.QRLoginTicket{
		ID:             "qrlogin_test",
		QRTokenHash:    qrHash[:],
		PollTokenHash:  pollHash[:],
		ClientPlatform: "web",
		ClientName:     "青蛙呱呱网页版",
		CreatedAt:      now,
		ExpiresAt:      now.Add(2 * time.Minute),
	}
	if err = a.CreateQRLoginTicket(ticket); err != nil {
		t.Fatal(err)
	}
	if pending, consumed, err := a.ConsumeQRLoginTicket(ticket.ID, pollHash[:], now.Add(time.Second)); err != nil || consumed || pending.State(now.Add(time.Second)) != "pending" {
		t.Fatalf("pending poll ticket=%+v consumed=%v err=%v", pending, consumed, err)
	}
	confirmed, err := a.ConfirmQRLoginTicket(qrHash[:], user.ID, now.Add(2*time.Second))
	if err != nil || confirmed.UserID != user.ID || confirmed.State(now.Add(2*time.Second)) != "confirmed" {
		t.Fatalf("confirmation ticket=%+v err=%v", confirmed, err)
	}
	claimed, consumed, err := a.ConsumeQRLoginTicket(ticket.ID, pollHash[:], now.Add(3*time.Second))
	if err != nil || !consumed || claimed.UserID != user.ID || claimed.State(now.Add(3*time.Second)) != "consumed" {
		t.Fatalf("first consume ticket=%+v consumed=%v err=%v", claimed, consumed, err)
	}
	used, consumed, err := a.ConsumeQRLoginTicket(ticket.ID, pollHash[:], now.Add(4*time.Second))
	if err != nil || consumed || used.State(now.Add(4*time.Second)) != "consumed" {
		t.Fatalf("second consume ticket=%+v consumed=%v err=%v", used, consumed, err)
	}

	expiredQR := sha256.Sum256([]byte("expired-qr"))
	expiredPoll := sha256.Sum256([]byte("expired-poll"))
	expired := store.QRLoginTicket{ID: "qrlogin_expired", QRTokenHash: expiredQR[:], PollTokenHash: expiredPoll[:], ClientPlatform: "web", ClientName: "网页版", CreatedAt: now.Add(-3 * time.Minute), ExpiresAt: now.Add(-time.Minute)}
	if err = a.CreateQRLoginTicket(expired); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ConfirmQRLoginTicket(expiredQR[:], user.ID, now); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expired confirmation err=%v", err)
	}
}

type failingNormalizedPersistence struct {
	err error
}

func (p failingNormalizedPersistence) Load(context.Context) (*model.State, error) {
	return nil, p.err
}

func (failingNormalizedPersistence) Save(context.Context, *model.State) error { return nil }
func (failingNormalizedPersistence) Ping(context.Context) error               { return nil }
func (failingNormalizedPersistence) Close()                                   {}
func (failingNormalizedPersistence) IsNormalized() bool                       { return true }

func TestAdminStatsContextDoesNotMaskStoreFailure(t *testing.T) {
	sentinel := errors.New("database unavailable")
	a, err := New(context.Background(), failingNormalizedPersistence{err: sentinel})
	if err != nil {
		t.Fatal(err)
	}

	stats, err := a.AdminStatsContext(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected database error, got stats=%v err=%v", stats, err)
	}
	if stats != nil {
		t.Fatalf("expected no fallback stats, got %v", stats)
	}
}

func TestGeneratedHandleIsPublicAndTwoChangesAreEnforced(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	u, err := a.RegisterWithPassword("13900000001", "呱呱用户", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.Handle, "gg_") || len(u.Handle) != 23 || u.HandleChangeCount != 0 {
		t.Fatalf("generated handle=%q changes=%d", u.Handle, u.HandleChangeCount)
	}

	first := "frog_1"
	u, err = a.UpdateUserProfile(u.ID, store.UserProfileUpdate{Handle: &first})
	if err != nil || u.HandleChangeCount != 1 {
		t.Fatalf("first change user=%+v err=%v", u, err)
	}
	second := "frog_2"
	u, err = a.UpdateUserProfile(u.ID, store.UserProfileUpdate{Handle: &second})
	if err != nil || u.HandleChangeCount != 2 {
		t.Fatalf("second change user=%+v err=%v", u, err)
	}
	third := "frog_3"
	if _, err = a.UpdateUserProfile(u.ID, store.UserProfileUpdate{Handle: &third}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("third change err=%v", err)
	}
}

func TestFourCharacterHandleMatchesPublishedClientRule(t *testing.T) {
	if !validHandle("frog") {
		t.Fatal("four-character handle should be valid")
	}
	if validHandle("abc") {
		t.Fatal("three-character handle should be rejected")
	}
}

func TestUserGenderAcceptsOnlyPublishedValues(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	u, err := a.Login("13900000021", "资料测试")
	if err != nil {
		t.Fatal(err)
	}
	female := "female"
	u, err = a.UpdateUserProfile(u.ID, store.UserProfileUpdate{Gender: &female})
	if err != nil || u.Gender != female {
		t.Fatalf("gender update user=%+v err=%v", u, err)
	}
	invalid := "private"
	if _, err = a.UpdateUserProfile(u.ID, store.UserProfileUpdate{Gender: &invalid}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid gender err=%v", err)
	}
}

func TestAcceptFriendCreatesOneDirectConversationForBothUsers(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	requester, err := a.Login("13900000011", "申请人")
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := a.Login("13900000012", "接收人")
	if err != nil {
		t.Fatal(err)
	}
	request, err := a.RequestFriend(requester.ID, receiver.ID, "你好")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.AcceptFriend(receiver.ID, request.ID); err != nil {
		t.Fatal(err)
	}

	requesterConversations := a.Conversations(requester.ID)
	receiverConversations := a.Conversations(receiver.ID)
	if len(requesterConversations) != 1 || len(receiverConversations) != 1 {
		t.Fatalf("requester conversations=%v receiver conversations=%v", requesterConversations, receiverConversations)
	}
	requesterConversation := requesterConversations[0]["conversation"].(*model.Conversation)
	receiverConversation := receiverConversations[0]["conversation"].(*model.Conversation)
	if requesterConversation.Type != "direct" || receiverConversation.ID != requesterConversation.ID {
		t.Fatalf("requester conversation=%+v receiver conversation=%+v", requesterConversation, receiverConversation)
	}

	if err = a.AcceptFriend(receiver.ID, request.ID); err != nil {
		t.Fatal(err)
	}
	if conversations := a.Conversations(receiver.ID); len(conversations) != 1 {
		t.Fatalf("accept retry created duplicate conversations: %v", conversations)
	}
}

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

func TestAdminModerateGroupMemberAppliesRoleMuteAndRemovalWithoutChangingOwner(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	group, err := a.CreateGroup("usr_alice", "运营治理群", []string{"usr_bob"})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.AdminModerateGroupMember("admin-test", group.ID, "usr_bob", "role", "admin", "运营工单 GROUP-1", nil); err != nil {
		t.Fatal(err)
	}
	if got := a.state.Members[group.ID]["usr_bob"].Role; got != "admin" {
		t.Fatalf("role=%q", got)
	}
	mutedUntil := time.Now().Add(time.Hour)
	if err = a.AdminModerateGroupMember("admin-test", group.ID, "usr_bob", "mute", "", "群内违规发言", &mutedUntil); err != nil {
		t.Fatal(err)
	}
	if got := a.state.Members[group.ID]["usr_bob"].MutedUntil; got == nil || got.Before(time.Now()) {
		t.Fatalf("mutedUntil=%v", got)
	}
	if err = a.AdminModerateGroupMember("admin-test", group.ID, "usr_alice", "remove", "", "不得移除群主", nil); err != ErrForbidden {
		t.Fatalf("owner removal err=%v", err)
	}
	if err = a.AdminModerateGroupMember("admin-test", group.ID, "usr_bob", "remove", "", "确认移出违规成员", nil); err != nil {
		t.Fatal(err)
	}
	if a.state.Members[group.ID]["usr_bob"] != nil {
		t.Fatal("member still present after admin removal")
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
