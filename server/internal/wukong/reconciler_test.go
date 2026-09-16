package wukong

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type reconcileTestStore struct{}

func (reconcileTestStore) ListWukongUserAccess(context.Context, string, int) ([]UserAccessSnapshot, error) {
	return []UserAccessSnapshot{{UID: "usr_a", Allowlist: []string{"usr_b"}, Denylist: []string{}}}, nil
}

func (reconcileTestStore) LoadWukongChannelSnapshot(context.Context, string, uint8) (ChannelSnapshot, error) {
	return ChannelSnapshot{Cursor: "02:grp_1", ChannelID: "grp_1", ChannelType: ChannelGroup, Subscribers: []string{"usr_a", "usr_b"}, Allowlist: []string{"usr_a"}, Denylist: []string{}}, nil
}

type permanentChannelFailureStore struct{}

func (permanentChannelFailureStore) ListWukongUserAccess(context.Context, string, int) ([]UserAccessSnapshot, error) {
	return nil, nil
}

func (permanentChannelFailureStore) LoadWukongChannelSnapshot(_ context.Context, channelID string, channelType uint8) (ChannelSnapshot, error) {
	return ChannelSnapshot{Cursor: channelID, ChannelID: channelID, ChannelType: channelType}, nil
}

func (permanentChannelFailureStore) ListWukongChannels(context.Context, string, int) ([]ChannelSnapshot, error) {
	return []ChannelSnapshot{
		{Cursor: "05:community@topic", ChannelID: "community@topic", ChannelType: ChannelCommunityTopic},
		{Cursor: "06:info_1", ChannelID: "info_1", ChannelType: ChannelInfo},
	}, nil
}
func (reconcileTestStore) ListWukongChannels(context.Context, string, int) ([]ChannelSnapshot, error) {
	return []ChannelSnapshot{{Cursor: "02:grp_1", ChannelID: "grp_1", ChannelType: ChannelGroup, Subscribers: []string{"usr_a", "usr_b"}, Allowlist: []string{"usr_a"}, Denylist: []string{}}}, nil
}

func TestReconcilerOverwritesAuthoritativeAccessAndChannels(t *testing.T) {
	var mu sync.Mutex
	paths := []string{}
	bodies := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		paths, bodies = append(paths, r.URL.Path), append(bodies, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewReconciler(reconcileTestStore{}, client)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := reconciler.runPage(t.Context())
	if err != nil || !complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
	if len(paths) != 5 || paths[0] != "/channel/whitelist_set" || paths[1] != "/channel/blacklist_set" || paths[2] != "/channel" || paths[3] != "/channel/whitelist_set" || paths[4] != "/channel/blacklist_set" {
		t.Fatalf("paths=%v", paths)
	}
	if bodies[1]["uids"] == nil || bodies[2]["reset"] != float64(1) || bodies[3]["uids"].([]any)[0] != "usr_a" || bodies[4]["uids"] == nil {
		t.Fatalf("bodies=%v", bodies)
	}
}

type changingChannelSnapshotStore struct {
	mu        sync.Mutex
	allowlist []string
	loads     chan []string
}

func (s *changingChannelSnapshotStore) LoadWukongChannelSnapshot(_ context.Context, channelID string, channelType uint8) (ChannelSnapshot, error) {
	s.mu.Lock()
	allowlist := append([]string(nil), s.allowlist...)
	s.mu.Unlock()
	s.loads <- allowlist
	return ChannelSnapshot{ChannelID: channelID, ChannelType: channelType, Subscribers: []string{"usr_owner", "usr_member"}, Allowlist: allowlist}, nil
}

func (s *changingChannelSnapshotStore) setAllowlist(values []string) {
	s.mu.Lock()
	s.allowlist = append([]string(nil), values...)
	s.mu.Unlock()
}

func TestChannelSnapshotSynchronizationReloadsAfterWaitingForSameChannel(t *testing.T) {
	firstChannelRequest := make(chan struct{})
	releaseFirstRequest := make(chan struct{})
	var channelRequests atomic.Int32
	var mu sync.Mutex
	whitelists := [][]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			UIDs []string `json:"uids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/channel" && channelRequests.Add(1) == 1 {
			close(firstChannelRequest)
			<-releaseFirstRequest
		}
		if r.URL.Path == "/channel/whitelist_set" {
			mu.Lock()
			whitelists = append(whitelists, append([]string(nil), body.UIDs...))
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	store := &changingChannelSnapshotStore{allowlist: []string{"usr_owner"}, loads: make(chan []string, 2)}
	errs := make(chan error, 2)
	go func() { errs <- syncLatestChannelSnapshot(t.Context(), store, client, "grp_1", ChannelGroup) }()
	select {
	case loaded := <-store.loads:
		if len(loaded) != 1 || loaded[0] != "usr_owner" {
			t.Fatalf("first snapshot=%v", loaded)
		}
	case <-time.After(time.Second):
		t.Fatal("first snapshot was not loaded")
	}
	select {
	case <-firstChannelRequest:
	case <-time.After(time.Second):
		t.Fatal("first channel request did not start")
	}

	// Simulate disabling all-member mute while an older periodic repair is in
	// flight. The outbox-driven repair must wait and then reload the empty list.
	store.setAllowlist(nil)
	go func() { errs <- syncLatestChannelSnapshot(t.Context(), store, client, "grp_1", ChannelGroup) }()
	select {
	case loaded := <-store.loads:
		t.Fatalf("second snapshot loaded before the channel lock was released: %v", loaded)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirstRequest)
	for range 2 {
		if err = <-errs; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case loaded := <-store.loads:
		if len(loaded) != 0 {
			t.Fatalf("latest snapshot=%v", loaded)
		}
	default:
		t.Fatal("latest snapshot was not reloaded")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(whitelists) != 2 || len(whitelists[0]) != 1 || len(whitelists[1]) != 0 {
		t.Fatalf("whitelist applications=%v", whitelists)
	}
}

func TestReconcilerRetriesTheIncompleteSnapshotWithoutSkippingIt(t *testing.T) {
	var denyFailures atomic.Int32
	var mu sync.Mutex
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/channel/blacklist_set" && body["channel_id"] == "grp_1" && denyFailures.Add(1) == 1 {
			http.Error(w, "injected outage", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret", MaxRetries: 0})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewReconciler(reconcileTestStore{}, client)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := reconciler.runPage(t.Context())
	if err == nil || complete {
		t.Fatalf("first page complete=%v err=%v", complete, err)
	}
	if reconciler.channelCursor != "" || !reconciler.usersDone {
		t.Fatalf("failed snapshot was advanced: channelCursor=%q usersDone=%v", reconciler.channelCursor, reconciler.usersDone)
	}
	complete, err = reconciler.runPage(t.Context())
	if err != nil || !complete {
		t.Fatalf("retry complete=%v err=%v", complete, err)
	}
	mu.Lock()
	defer mu.Unlock()
	counts := map[string]int{}
	for _, path := range paths {
		counts[path]++
	}
	if counts["/channel"] != 2 || counts["/channel/whitelist_set"] != 3 || counts["/channel/blacklist_set"] != 3 {
		t.Fatalf("snapshot retry paths=%v", paths)
	}
}

func TestReconcilerDefersPermanentChannelFailureWithoutBlockingLaterChannels(t *testing.T) {
	var mu sync.Mutex
	channelAttempts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		channelID, _ := body["channel_id"].(string)
		if r.URL.Path == "/channel" {
			mu.Lock()
			channelAttempts[channelID]++
			mu.Unlock()
		}
		if channelID == "community@topic" {
			http.Error(w, "invalid channel id", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret", MaxRetries: 0})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewReconciler(permanentChannelFailureStore{}, client)
	if err != nil {
		t.Fatal(err)
	}

	complete, runErr := reconciler.runPage(t.Context())
	if runErr == nil || !complete {
		t.Fatalf("deferred cycle complete=%v err=%v", complete, runErr)
	}
	mu.Lock()
	if channelAttempts["community@topic"] != 1 || channelAttempts["info_1"] != 1 {
		t.Fatalf("channel attempts=%v", channelAttempts)
	}
	mu.Unlock()

	// Completion starts a new cycle, so the deferred snapshot remains eligible
	// for repair rather than being silently marked successful.
	complete, runErr = reconciler.runPage(t.Context())
	if runErr == nil || !complete {
		t.Fatalf("next cycle complete=%v err=%v", complete, runErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if channelAttempts["community@topic"] != 2 || channelAttempts["info_1"] != 2 {
		t.Fatalf("next cycle channel attempts=%v", channelAttempts)
	}
}
