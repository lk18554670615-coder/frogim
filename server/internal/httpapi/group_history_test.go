package httpapi

import (
	"context"
	"encoding/json"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
	"github.com/linli/im/server/internal/wukong"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type historyPolicyFixture struct {
	teststore.Memory
	access model.HistoryAccess
	denied bool
}

func (f *historyPolicyFixture) SetGroupHistoryBoundaryReader(store.GroupHistoryBoundaryReader) {}
func (f *historyPolicyFixture) GroupHistoryAccess(context.Context, string, string) (*model.HistoryAccess, error) {
	if f.denied {
		return nil, store.ErrForbidden
	}
	v := f.access
	return &v, nil
}
func (f *historyPolicyFixture) SetAdminGroupHistoryVisibility(context.Context, string, string, bool, string, time.Time) error {
	return nil
}

func TestVisibleHistorySyncContract(t *testing.T) {
	cutoff := int64(10)
	f := &historyPolicyFixture{access: model.HistoryAccess{Version: 1, AfterSeq: &cutoff}}
	a, err := app.New(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	var request wukong.MessageSyncRequest
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/channel/messagesync" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		// Include forbidden rows deliberately, to verify defensive post-filtering.
		json.NewEncoder(w).Encode(map[string]any{"more": 1, "messages": []map[string]any{{"message_seq": 9, "timestamp": 100}, {"message_seq": 10, "timestamp": 100}, {"message_seq": 11, "timestamp": 101}}})
	}))
	defer server.Close()
	client, err := wukong.NewClient(wukong.Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "test-manager-token"})
	if err != nil {
		t.Fatal(err)
	}
	for _, pull := range []int{0, 1} {
		out, err := syncVisibleGroupMessages(t.Context(), client, a, wukong.MessageSyncRequest{LoginUID: "u", ChannelID: "g", ChannelType: 2, Limit: 50, PullMode: pull})
		if err != nil || len(out.Messages) != 1 || wukongInt64(out.Messages[0]["message_seq"]) != 11 || out.More != 0 {
			t.Fatalf("latest pull=%d out=%+v err=%v", pull, out, err)
		}
		if request.StartMessageSeq != 0 || request.EndMessageSeq != 0 {
			t.Fatalf("latest must preserve pinned server zero/zero semantics: %+v", request)
		}
	}
	before := calls
	out, err := syncVisibleGroupMessages(t.Context(), client, a, wukong.MessageSyncRequest{LoginUID: "u", ChannelID: "g", ChannelType: 2, StartMessageSeq: 10, Limit: 50})
	if err != nil || len(out.Messages) != 0 || out.More != 0 || calls != before {
		t.Fatalf("boundary did not terminate: %+v %v", out, err)
	}
	_, err = syncVisibleGroupMessages(t.Context(), client, a, wukong.MessageSyncRequest{LoginUID: "u", ChannelID: "g", ChannelType: 2, StartMessageSeq: 20, Limit: 50})
	if err != nil || request.EndMessageSeq != 10 {
		t.Fatalf("exclusive lower bound=%+v err=%v", request, err)
	}
	f.access.VisibleAll = true
	out, err = syncVisibleGroupMessages(t.Context(), client, a, wukong.MessageSyncRequest{LoginUID: "u", ChannelID: "g", ChannelType: 2, Limit: 50})
	if err != nil || len(out.Messages) != 3 {
		t.Fatalf("open=%+v err=%v", out, err)
	}
	f.denied = true
	if _, err = syncVisibleGroupMessages(t.Context(), client, a, wukong.MessageSyncRequest{LoginUID: "u", ChannelID: "g", ChannelType: 2, Limit: 50}); err == nil {
		t.Fatal("missing membership allowed")
	}
}

func TestAdminHistoryVisibilityConfirmation(t *testing.T) {
	f := &historyPolicyFixture{}
	a, err := app.New(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	api := &API{app: a}
	for _, body := range []string{`{}`, `{"confirmed":true,"reason":"test"}`, `{"historyVisibleToNewMembers":false,"reason":"test"}`, `{"historyVisibleToNewMembers":true,"confirmed":true,"reason":"  "}`} {
		r := httptest.NewRequest("PUT", "/v2/admin/groups/g/history-visibility", strings.NewReader(body))
		r.SetPathValue("id", "g")
		w := httptest.NewRecorder()
		api.adminGroupHistoryVisibility(w, r)
		if w.Code != 400 {
			t.Fatalf("body=%s code=%d", body, w.Code)
		}
	}
	for _, value := range []string{"true", "false"} {
		r := httptest.NewRequest("PUT", "/v2/admin/groups/g/history-visibility", strings.NewReader(`{"historyVisibleToNewMembers":`+value+`,"confirmed":true,"reason":"test"}`))
		r.SetPathValue("id", "g")
		w := httptest.NewRecorder()
		api.adminGroupHistoryVisibility(w, r)
		if w.Code != 200 {
			t.Fatalf("value=%s code=%d %s", value, w.Code, w.Body.String())
		}
	}
}

func TestHistoryClosesDuringWuKongRequest(t *testing.T) {
	seq := int64(10)
	f := &historyPolicyFixture{access: model.HistoryAccess{Version: 1, VisibleAll: true, AfterSeq: &seq}}
	a, _ := app.New(t.Context(), f)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.access.VisibleAll = false
		f.access.Version++
		json.NewEncoder(w).Encode(map[string]any{"more": 0, "messages": []map[string]any{{"message_seq": 1, "timestamp": 100}}})
	}))
	defer server.Close()
	client, err := wukong.NewClient(wukong.Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "test-manager-token"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := syncVisibleGroupMessages(t.Context(), client, a, wukong.MessageSyncRequest{LoginUID: "u", ChannelID: "g", ChannelType: 2, Limit: 50})
	if err != nil || len(out.Messages) != 0 {
		t.Fatalf("concurrent closure leaked: %+v %v", out, err)
	}
}

func TestHistoryAdminPermission(t *testing.T) {
	if adminPermission("PUT", "/v2/admin/groups/g/history-visibility") != "groups.write" {
		t.Fatal("history write permission missing")
	}
}
