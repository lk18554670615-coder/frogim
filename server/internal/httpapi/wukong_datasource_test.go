package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

type datasourceSystemUserStore struct {
	teststore.Memory
	uids []string
}

func (s *datasourceSystemUserStore) WukongSystemUIDs(context.Context) ([]string, error) {
	return append([]string{}, s.uids...), nil
}
func (s *datasourceSystemUserStore) IsWukongSystemUser(_ context.Context, uid string) (bool, error) {
	for _, item := range s.uids {
		if item == uid {
			return true, nil
		}
	}
	return false, nil
}
func (s *datasourceSystemUserStore) ListWukongSystemUsers(context.Context) ([]*store.WukongSystemUser, error) {
	return nil, nil
}
func (s *datasourceSystemUserStore) SetWukongSystemUser(context.Context, string, bool, string, string, time.Time) (*store.WukongSystemUser, error) {
	return nil, nil
}

func TestWukongServerDataSourceReturnsAuthoritativeSystemUIDs(t *testing.T) {
	repository := &datasourceSystemUserStore{uids: []string{"usr_notice", "usr_support"}}
	a, err := app.New(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(config.Config{JWTSecret: "test-secret"}, a).Handler())
	defer server.Close()
	response, err := http.Post(server.URL+"/internal/wukong/datasource", "application/json", strings.NewReader(`{"cmd":"getSystemUIDs","data":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var ids []string
	if err = json.NewDecoder(response.Body).Decode(&ids); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(ids) != 2 || ids[0] != "usr_notice" || ids[1] != "usr_support" {
		t.Fatalf("status=%d ids=%v", response.StatusCode, ids)
	}
}

func TestWukongPolicyMatchesNativeSystemUserBypass(t *testing.T) {
	repository := &datasourceSystemUserStore{uids: []string{"usr_notice"}}
	a, err := app.New(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "test-policy-secret"
	api := New(config.Config{JWTSecret: "test-secret", WukongPolicySecret: secret}, a)
	request := httptest.NewRequest(http.MethodPost, "/internal/wukong/policy/send", strings.NewReader(`{"fromUid":"usr_notice","channelId":"usr_any","channelType":1,"payload":"eyJ0eXBlIjoxLCJjb250ZW50Ijoic3lzdGVtIG5vdGljZSJ9"}`))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(wukongPolicySecretHeader, secret)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	var output wukongPolicySendResponse
	if err = json.NewDecoder(response.Body).Decode(&output); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !output.Allowed || output.ReasonCode != 1 {
		t.Fatalf("status=%d output=%+v", response.Code, output)
	}

	request = httptest.NewRequest(http.MethodPost, "/internal/wukong/policy/send", strings.NewReader(`{"fromUid":"usr_customer","channelId":"usr_notice","channelType":1,"payload":"eyJ0eXBlIjoxLCJjb250ZW50IjoiaGVscCJ9"}`))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(wukongPolicySecretHeader, secret)
	response = httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if err = json.NewDecoder(response.Body).Decode(&output); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !output.Allowed || output.ReasonCode != 1 {
		t.Fatalf("ordinary sender to system recipient: status=%d output=%+v", response.Code, output)
	}
}

func TestWukongServerDataSourceExactCommandEnvelope(t *testing.T) {
	a, err := app.New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := a.Login("+8613800001001", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	member, err := a.Login("+8613800001002", "Member")
	if err != nil {
		t.Fatal(err)
	}
	group, err := a.CreateGroup(owner.ID, "Datasource", []string{member.ID})
	if err != nil {
		t.Fatal(err)
	}

	api := New(config.Config{JWTSecret: "test-secret"}, a)
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	response, err := http.Post(server.URL+"/internal/wukong/datasource", "application/json", strings.NewReader(`{"cmd":"getSubscribers","data":{"channel_id":"`+group.ID+`","channel_type":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var ids []string
	if err = json.NewDecoder(response.Body).Decode(&ids); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("subscribers=%v", ids)
	}

	response, err = http.Post(server.URL+"/internal/wukong/datasource", "application/json", strings.NewReader(`{"cmd":"getSystemUIDs","data":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("system uid status=%d", response.StatusCode)
	}
}

func TestWukongServerDataSourceRejectsPublicPeer(t *testing.T) {
	a, err := app.New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	api := New(config.Config{JWTSecret: "test-secret"}, a)
	request := httptest.NewRequest(http.MethodPost, "/internal/wukong/datasource", strings.NewReader(`{"cmd":"getSystemUIDs","data":{}}`))
	request.RemoteAddr = "203.0.113.10:1234"
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
}
