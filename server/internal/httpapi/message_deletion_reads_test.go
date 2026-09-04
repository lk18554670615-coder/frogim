package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
	"github.com/linli/im/server/internal/wukong"
	"net/http"
	"net/http/httptest"
	"testing"
)

type deletionReadStore struct {
	teststore.Memory
	store.WukongMessageExtensionStore
	fail bool
}

func (s *deletionReadStore) LoadWukongMessageExtensions(ctx context.Context, uid string, ids []string) (map[string]map[string]any, error) {
	if s.fail {
		return nil, errors.New("database unavailable")
	}
	out := map[string]map[string]any{}
	for _, id := range ids {
		if id == "2" || id == "3" {
			out[id] = map[string]any{"deletedForEveryoneAt": "2026-09-04T00:00:00Z", "editedBody": map[string]any{"text": "private edit"}, "version": 4}
		}
	}
	return out, nil
}

func TestDeletedHistoryPaginationAndReadFailure(t *testing.T) {
	s := &deletionReadStore{}
	a, err := app.New(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	var starts []uint64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Start uint64 `json:"start_message_seq"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		starts = append(starts, in.Start)
		seq := in.Start
		if seq == 0 {
			seq = 3
		}
		more := 1
		if seq == 1 {
			more = 0
		}
		payload := base64.StdEncoding.EncodeToString([]byte(`{"type":1,"content":"visible","reply":{"message_id":"2","content":"hidden quote"}}`))
		json.NewEncoder(w).Encode(map[string]any{"more": more, "messages": []any{map[string]any{"message_idstr": fmt.Sprint(seq), "message_seq": seq, "channel_id": "peer", "channel_type": 1, "from_uid": "peer", "client_msg_no": fmt.Sprint(seq), "timestamp": 1700000000, "payload": payload}}})
	}))
	defer upstream.Close()
	client, err := wukong.NewClient(wukong.Config{APIURL: upstream.URL, ManagerURL: upstream.URL, ManagerToken: "test-manager"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := syncVisibleGroupMessages(t.Context(), client, a, wukong.MessageSyncRequest{LoginUID: "me", ChannelID: "peer", ChannelType: 1, Limit: 1, PullMode: 0})
	if err != nil || len(result.Messages) != 1 || wukongMessageID(result.Messages[0]) != "1" || result.More != 0 || len(starts) != 3 {
		t.Fatal(result, starts, err)
	}
	payload, _ := wukongStreamProjectedPayload(result.Messages[0])
	if payload["reply"] != nil {
		t.Fatal("deleted quote returned", payload)
	}
	extra := clientDeletionExtra(map[string]any{"deletedForEveryoneAt": "now", "editedBody": "secret", "version": 4})
	if extra["editedBody"] != nil {
		t.Fatal("edited body leaked")
	}
	s.fail = true
	if _, err = syncVisibleGroupMessages(t.Context(), client, a, wukong.MessageSyncRequest{LoginUID: "me", ChannelID: "peer", ChannelType: 1, Limit: 1}); err == nil {
		t.Fatal("database failure returned raw text")
	}
}
