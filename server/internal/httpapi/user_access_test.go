package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/linli/im/server/internal/accesslog"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type accessSink struct {
	mu    sync.Mutex
	items []store.UserAccessLog
	fail  bool
}

func (s *accessSink) RecordUserAccess(_ context.Context, e store.UserAccessLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, e)
	if s.fail {
		return errors.New("database unavailable")
	}
	return nil
}
func (s *accessSink) snapshot() []store.UserAccessLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]store.UserAccessLog(nil), s.items...)
}
func accessAPI(t *testing.T) (*API, *accessSink) {
	t.Helper()
	a, err := app.New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	x := New(config.Config{JWTSecret: strings.Repeat("t", 32), DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}, a)
	sink := &accessSink{}
	x.accessRecorder = accesslog.New(sink)
	x.accessRecorder.RetryDelay = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go x.RunUserAccess(ctx)
	return x, sink
}
func waitAccess(t *testing.T, x *API) {
	t.Helper()
	until := time.Now().Add(4 * time.Second)
	for x.accessRecorder.Pending() != 0 {
		if time.Now().After(until) {
			t.Fatal("queue not drained")
		}
		time.Sleep(time.Millisecond)
	}
}
func accessRequest(x *API, path, body, platform, ip, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Client-Platform", platform)
	r.Header.Set("X-Forwarded-For", "8.8.8.8")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.RemoteAddr = ip + ":30000"
	w := httptest.NewRecorder()
	x.Handler().ServeHTTP(w, r)
	return w
}
func TestUserAccessAuthEventsAndNonBlockingFailure(t *testing.T) {
	x, sink := accessAPI(t)
	w := accessRequest(x, "/v2/auth/register", `{"phone":"13900001001","code":"654321","password":"StrongPass123!","name":"IP Test"}`, "android", "203.0.113.1", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var session struct {
		AccessToken, RefreshToken string
		User                      model.User
	}
	json.Unmarshal(w.Body.Bytes(), &session)
	waitAccess(t, x)
	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("events %+v", events)
	}
	for _, e := range events {
		if e.IP != "203.0.113.1" || e.UserID != session.User.ID || e.Result != "success" {
			t.Fatalf("%+v", e)
		}
	}
	if strings.Contains(w.Body.String(), "registrationIp") || strings.Contains(w.Body.String(), "203.0.113.1") {
		t.Fatal("public response leaked IP")
	}
	for _, phone := range []string{"13900001001", "13900001002"} {
		w = accessRequest(x, "/v2/auth/password-login", `{"phone":"`+phone+`","password":"WrongSecret!"}`, "android", "203.0.113.2", "")
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	w = accessRequest(x, "/v2/auth/login", `{"phone":"13900001001","code":"wrong"}`, "android", "203.0.113.2", "")
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	waitAccess(t, x)
	for _, e := range sink.snapshot()[2:] {
		if e.Result != "failed" || e.FailureCode == "" {
			t.Fatalf("%+v", e)
		}
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), "WrongSecret") || strings.Contains(string(raw), "1390000") {
			t.Fatal("sensitive data serialized")
		}
	}
	before := len(sink.snapshot())
	w = accessRequest(x, "/v2/auth/refresh", `{"refreshToken":"`+session.RefreshToken+`"}`, "android", "203.0.113.3", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	waitAccess(t, x)
	if len(sink.snapshot()) != before {
		t.Fatal("refresh counted as login")
	}
	sink.mu.Lock()
	sink.fail = true
	sink.mu.Unlock()
	w = accessRequest(x, "/v2/auth/password-login", `{"phone":"13900001001","password":"StrongPass123!"}`, "android", "203.0.113.4", "")
	if w.Code != 200 {
		t.Fatal("logging failure blocked login", w.Code)
	}
	waitAccess(t, x)
	if x.accessRecorder.Dropped.Load() != 1 {
		t.Fatal("missing failure metric")
	}
}
func TestUserAccessOTPNewAccountAndQRClaimIP(t *testing.T) {
	x, sink := accessAPI(t)
	phoneBody := `{"phone":"13900001005","code":"654321"}`
	w := accessRequest(x, "/v2/auth/login", phoneBody, "android", "203.0.113.5", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var login struct{ AccessToken string }
	json.Unmarshal(w.Body.Bytes(), &login)
	waitAccess(t, x)
	w = accessRequest(x, "/v2/auth/login", phoneBody, "android", "203.0.113.5", "")
	json.Unmarshal(w.Body.Bytes(), &login)
	waitAccess(t, x)
	regCount := 0
	for _, e := range sink.snapshot() {
		if e.Event == "register" {
			regCount++
		}
	}
	if regCount != 1 {
		t.Fatal("repeat registration", regCount)
	}
	w = accessRequest(x, "/v2/auth/qr/create", `{}`, "web", "203.0.113.20", "")
	var ticket struct{ ID, QRPayload, PollToken string }
	json.Unmarshal(w.Body.Bytes(), &ticket)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	poll := `{"id":"` + ticket.ID + `","pollToken":"` + ticket.PollToken + `"}`
	before := len(sink.snapshot())
	w = accessRequest(x, "/v2/auth/qr/poll", poll, "web", "203.0.113.20", "")
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	waitAccess(t, x)
	if len(sink.snapshot()) != before {
		t.Fatal("waiting poll logged")
	}
	w = accessRequest(x, "/v2/auth/qr/confirm", `{"token":"`+strings.TrimPrefix(ticket.QRPayload, "qingwaguagua://login/")+`"}`, "android", "203.0.113.5", login.AccessToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = accessRequest(x, "/v2/auth/qr/poll", poll, "web", "203.0.113.20", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	waitAccess(t, x)
	events := sink.snapshot()
	last := events[len(events)-1]
	if last.Method != "qr" || last.IP != "203.0.113.20" || last.Platform != "web" || last.Result != "success" {
		t.Fatalf("%+v", last)
	}
}

type accessAdminStore struct {
	*mutableAdminStore
	store.AdminOperationsStore
	query  store.UserAccessQuery
	audits []*model.AuditEntry
}

func (s *accessAdminStore) RecordAdminAudit(_ context.Context, entry *model.AuditEntry) error {
	s.audits = append(s.audits, entry)
	return nil
}

func (s *accessAdminStore) RecordUserAccess(context.Context, store.UserAccessLog) error { return nil }
func (s *accessAdminStore) UserAccessProfiles(context.Context, []string, string) (map[string]store.UserAccessProfile, error) {
	return map[string]store.UserAccessProfile{}, nil
}
func (s *accessAdminStore) ListUserAccessLogs(_ context.Context, q store.UserAccessQuery) (store.UserAccessPage, error) {
	s.query = q
	return store.UserAccessPage{Items: []store.UserAccessLog{{ID: "e", Event: "login", Result: "failed", Method: "password", FailureCode: "INVALID_CREDENTIALS", IP: "192.168.1.1", OccurredAt: time.Now().UTC()}}}, nil
}
func (s *accessAdminStore) ListAdminUsersByIP(context.Context, string, string, string, int, string, string) ([]*model.User, int64, string, error) {
	return []*model.User{}, 0, "", nil
}
func TestUserAccessAdminReadPermissionsValidationAndAudit(t *testing.T) {
	s := &accessAdminStore{mutableAdminStore: newMutableAdminStore(t, "support", nil)}
	a, _ := app.New(context.Background(), s)
	x := New(config.Config{JWTSecret: strings.Repeat("s", 32)}, a)
	token, _ := x.auth.IssueAdmin(s.account.ID, "support", time.Hour, 1)
	get := func(path, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+auth)
		w := httptest.NewRecorder()
		x.Handler().ServeHTTP(w, r)
		return w
	}
	w := get("/v2/admin/user-access-logs", token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String())
	}
	if time.Since(s.query.From) < 29*24*time.Hour {
		t.Fatal("default range")
	}
	for _, filter := range []string{"ip=bad", "ip=1.1.1.0/24", "limit=101", "result=other", "from=bad", "cursor=broken"} {
		if w = get("/v2/admin/user-access-logs?"+filter, token); w.Code != 400 {
			t.Fatal(filter, w.Code)
		}
	}
	w = get("/v2/admin/user-access-logs?ip=::ffff:192.0.2.1", token)
	if w.Code != 200 || s.query.IP != "192.0.2.1" {
		t.Fatal("normalization")
	}
	if get("/v2/admin/user-access-logs", "").Code != 401 {
		t.Fatal("anonymous allowed")
	}
	s.mu.Lock()
	s.account.Status = "disabled"
	s.mu.Unlock()
	if get("/v2/admin/user-access-logs", token).Code != 401 {
		t.Fatal("disabled allowed")
	}
	audits := s.audits
	found := false
	for _, entry := range audits {
		if entry.Action == "user.access_logs.viewed" {
			found = true
			if _, ok := entry.Metadata["returned"]; !ok {
				t.Fatal("missing query count")
			}
			if _, ok := entry.Metadata["items"]; ok {
				t.Fatal("audit must not copy results")
			}
		}
	}
	if !found {
		t.Fatal("missing audit")
	}
}
