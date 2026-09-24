package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

func typingTestApp(t *testing.T) *App {
	t.Helper()
	a, err := New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	a.state.Conversations["group"] = &model.Conversation{ID: "group", Type: "group"}
	a.state.Members["group"] = map[string]*model.ConversationMember{}
	for _, id := range []string{"sender", "owner", "admin", "internal", "ordinary", "banned", "deleted"} {
		a.state.Users[id] = &model.User{ID: id}
		a.state.Members["group"][id] = &model.ConversationMember{UserID: id, Role: "member"}
	}
	a.state.Members["group"]["owner"].Role = "owner"
	a.state.Members["group"]["admin"].Role = "admin"
	a.state.Users["internal"].IsInternalUser = true
	a.state.Users["banned"].IsInternalUser = true
	a.state.Users["banned"].Banned = true
	a.state.Members["group"]["deleted"].Role = "admin"
	now := time.Now()
	a.state.Users["deleted"].DeletedAt = &now
	return a
}

func TestTypingRecipientsMemory(t *testing.T) {
	a := typingTestApp(t)
	var recipients []string
	var value bool
	a.SetEventSink(func(ids []string, event string, payload any) {
		if event != "typing" {
			t.Fatalf("unexpected event %s", event)
		}
		recipients = slices.Clone(ids)
		slices.Sort(recipients)
		p := payload.(map[string]any)
		value = p["typing"].(bool)
		if p["expiresAt"].(time.Time).Sub(time.Now()) > 6*time.Second {
			t.Fatal("typing lifetime changed")
		}
	})
	check := func(sender string, typing bool, want ...string) {
		t.Helper()
		recipients = nil
		if err := a.SetTypingContext(t.Context(), sender, "group", typing); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(recipients, want) || (len(want) != 0 && value != typing) {
			t.Fatalf("recipients=%v typing=%v, want=%v typing=%v", recipients, value, want, typing)
		}
	}
	check("sender", true, "admin", "internal", "owner")
	check("sender", false, "admin", "internal", "owner")
	check("owner", true, "admin", "internal")
	a.state.Users["internal"].IsInternalUser = false
	check("sender", true, "admin", "owner")
	a.state.Members["group"]["admin"].Role = "member"
	check("sender", true, "owner")
	a.state.Members["group"]["owner"].Role = "member"
	a.state.Members["group"]["ordinary"].Role = "owner"
	check("sender", true, "ordinary")
	a.state.Users["ordinary"].IsInternalUser = true
	a.state.Members["group"]["ordinary"].Role = "member"
	check("sender", true, "ordinary")
	delete(a.state.Members["group"], "ordinary")
	check("sender", true)
	for _, sender := range []string{"ordinary", "banned", "deleted", "unknown"} {
		if err := a.SetTypingContext(t.Context(), sender, "group", true); !errors.Is(err, ErrForbidden) {
			t.Fatalf("sender=%s err=%v", sender, err)
		}
	}
	// Direct chats retain their existing audience, regardless of internal status.
	a.state.Conversations["direct"] = &model.Conversation{ID: "direct", Type: "direct"}
	a.state.Members["direct"] = map[string]*model.ConversationMember{
		"sender": {UserID: "sender"}, "ordinary": {UserID: "ordinary"},
	}
	if err := a.SetTypingContext(t.Context(), "sender", "direct", true); err != nil || !slices.Equal(recipients, []string{"ordinary"}) {
		t.Fatalf("direct recipients=%v err=%v", recipients, err)
	}
}

func TestTypingLargeGroupSuppressedBeforeAudienceFiltering(t *testing.T) {
	a := typingTestApp(t)
	for len(a.state.Members["group"]) < 500 {
		id := fmt.Sprintf("member-%d", len(a.state.Members["group"]))
		a.state.Members["group"][id] = &model.ConversationMember{UserID: id, Role: "member"}
	}
	calls := 0
	a.SetEventSink(func([]string, string, any) { calls++ })
	if err := a.SetTypingContext(t.Context(), "sender", "group", true); err != nil || calls != 1 {
		t.Fatalf("500-member group calls=%d err=%v", calls, err)
	}
	a.state.Members["group"]["extra"] = &model.ConversationMember{UserID: "extra"}
	if err := a.SetTypingContext(t.Context(), "sender", "group", true); err != nil || calls != 1 {
		t.Fatalf("501-member group calls=%d err=%v", calls, err)
	}
}

type typingStoreFixture struct {
	teststore.Memory
	err error
}

func (s typingStoreFixture) TypingRecipients(context.Context, string, string) ([]string, error) {
	return []string{"authorized-only"}, s.err
}

func TestTypingPersistentRecipientsAndFailures(t *testing.T) {
	for _, failure := range []error{nil, errors.New("database unavailable"), store.ErrUnsupported, store.ErrForbidden} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			// Exercise the production Redis decorator too: permissions must never
			// require Redis or use its profile cache.
			p, err := store.NewWithRedis(typingStoreFixture{err: failure}, "redis://127.0.0.1:1")
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			a, err := New(t.Context(), p)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			a.SetEventSink(func(ids []string, _ string, _ any) {
				calls++
				if !slices.Equal(ids, []string{"authorized-only"}) {
					t.Fatalf("unfiltered recipients: %v", ids)
				}
			})
			err = a.SetTypingContext(t.Context(), "sender", "group", true)
			if failure == nil {
				if err != nil || calls != 1 {
					t.Fatalf("calls=%d err=%v", calls, err)
				}
			} else if err == nil || calls != 0 {
				t.Fatalf("failure must not publish: calls=%d err=%v", calls, err)
			}
		})
	}
}
