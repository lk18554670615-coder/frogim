package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
	"golang.org/x/crypto/bcrypt"
)

type mutableAdminStore struct {
	teststore.Memory
	mu        sync.Mutex
	account   store.AdminAccount
	lookupErr error
	lastLogin time.Time
	extra     map[string]store.AdminAccount
}

func newMutableAdminStore(t *testing.T, roleID string, permissions []string) *mutableAdminStore {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("InitialPassword123!"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return &mutableAdminStore{extra: map[string]store.AdminAccount{}, account: store.AdminAccount{
		ID: "admin_db", Username: "admin", Email: "admin@example.com", DisplayName: "Database Admin", PasswordHash: string(hash),
		RoleID: roleID, RoleName: "Database Role", Status: "active", AuthVersion: 1,
		Permissions: append([]string(nil), permissions...), PasswordUpdatedAt: time.Now().UTC(),
	}}
}

func (s *mutableAdminStore) snapshot() *store.AdminAccount {
	copy := s.account
	copy.Permissions = append([]string(nil), s.account.Permissions...)
	return &copy
}

func (s *mutableAdminStore) AdminAccountByUsername(_ context.Context, username string) (*store.AdminAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	if !strings.EqualFold(strings.TrimSpace(username), s.account.Username) {
		for _, account := range s.extra {
			if strings.EqualFold(strings.TrimSpace(username), account.Username) {
				copy := account
				copy.Permissions = append([]string(nil), account.Permissions...)
				return &copy, nil
			}
		}
		return nil, store.ErrNotFound
	}
	return s.snapshot(), nil
}

func (s *mutableAdminStore) AdminAccountByID(_ context.Context, id string) (*store.AdminAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	if id != s.account.ID {
		account, ok := s.extra[id]
		if !ok {
			return nil, store.ErrNotFound
		}
		copy := account
		copy.Permissions = append([]string(nil), account.Permissions...)
		return &copy, nil
	}
	return s.snapshot(), nil
}

func (s *mutableAdminStore) CreateAdminAccount(_ context.Context, input store.AdminAccountCreate) (*store.AdminAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.EqualFold(input.Username, s.account.Username) {
		return nil, store.ErrConflict
	}
	for _, existing := range s.extra {
		if strings.EqualFold(input.Username, existing.Username) || (input.Email != "" && strings.EqualFold(input.Email, existing.Email)) {
			return nil, store.ErrConflict
		}
	}
	account := store.AdminAccount{ID: input.ID, Username: input.Username, Email: input.Email, DisplayName: input.DisplayName, PasswordHash: input.PasswordHash, RoleID: input.RoleID, RoleName: input.RoleID, Status: "active", AuthVersion: 1, PasswordUpdatedAt: input.At, CreatedBy: input.CreatedBy, CreatedAt: input.At, UpdatedAt: input.At}
	s.extra[account.ID] = account
	copy := account
	return &copy, nil
}

func (s *mutableAdminStore) UpdateAdminAccount(_ context.Context, input store.AdminAccountUpdate) (*store.AdminAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.ID != s.account.ID {
		return nil, store.ErrNotFound
	}
	if input.Username != nil {
		s.account.Username = *input.Username
	}
	if input.Email != nil {
		s.account.Email = *input.Email
	}
	if input.DisplayName != nil {
		s.account.DisplayName = *input.DisplayName
	}
	if input.RoleID != nil {
		s.account.RoleID = *input.RoleID
	}
	if input.Status != nil {
		s.account.Status = *input.Status
	}
	s.account.AuthVersion++
	s.account.UpdatedAt = input.At
	return s.snapshot(), nil
}

func (s *mutableAdminStore) UpdateAdminAccountPassword(_ context.Context, id, hash string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.account.ID {
		return store.ErrNotFound
	}
	s.account.PasswordHash = hash
	s.account.PasswordUpdatedAt = at
	s.account.AuthVersion++
	return nil
}

func (s *mutableAdminStore) RecordAdminAccountLogin(_ context.Context, id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.account.ID {
		account, ok := s.extra[id]
		if !ok {
			return store.ErrNotFound
		}
		account.LastLoginAt = &at
		s.extra[id] = account
		return nil
	}
	s.lastLogin = at
	return nil
}

func adminJSONRequest(t *testing.T, method, url, token, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func loginAdminToken(t *testing.T, base, password string) (*http.Response, string) {
	t.Helper()
	response := adminJSONRequest(t, http.MethodPost, base+"/v2/admin/auth/login", "", `{"username":" AdMiN ","password":"`+password+`"}`)
	var payload struct {
		AccessToken string `json:"accessToken"`
	}
	_ = json.NewDecoder(response.Body).Decode(&payload)
	_ = response.Body.Close()
	return response, payload.AccessToken
}

func TestDatabaseAdminLoginPasswordChangeAndImmediateInvalidation(t *testing.T) {
	persistence := newMutableAdminStore(t, "platform_admin", []string{"users.write"})
	a, err := app.New(context.Background(), persistence)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	server := httptest.NewServer(New(cfg, a).Handler())
	defer server.Close()

	wrong, token := loginAdminToken(t, server.URL, "WrongPassword123!")
	if wrong.StatusCode != http.StatusUnauthorized || token != "" {
		t.Fatalf("wrong login status=%d token=%q", wrong.StatusCode, token)
	}
	login, token := loginAdminToken(t, server.URL, "InitialPassword123!")
	if login.StatusCode != http.StatusOK || token == "" || persistence.lastLogin.IsZero() {
		t.Fatalf("login status=%d token=%q lastLogin=%v", login.StatusCode, token, persistence.lastLogin)
	}
	legacyEmail := adminJSONRequest(t, http.MethodPost, server.URL+"/v2/admin/auth/login", "", `{"email":"admin@example.com","password":"InitialPassword123!"}`)
	if legacyEmail.StatusCode != http.StatusUnauthorized {
		t.Fatalf("legacy email login status=%d", legacyEmail.StatusCode)
	}
	_ = legacyEmail.Body.Close()
	me := adminJSONRequest(t, http.MethodGet, server.URL+"/v2/admin/auth/me", token, "")
	if me.StatusCode != http.StatusOK {
		t.Fatalf("me status=%d", me.StatusCode)
	}
	_ = me.Body.Close()
	wrongChange := adminJSONRequest(t, http.MethodPost, server.URL+"/v2/admin/auth/change-password", token, `{"currentPassword":"wrong password","newPassword":"NewPassword456!"}`)
	if wrongChange.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong current password status=%d", wrongChange.StatusCode)
	}
	_ = wrongChange.Body.Close()
	changed := adminJSONRequest(t, http.MethodPost, server.URL+"/v2/admin/auth/change-password", token, `{"currentPassword":"InitialPassword123!","newPassword":"NewPassword456!"}`)
	if changed.StatusCode != http.StatusNoContent {
		t.Fatalf("change password status=%d", changed.StatusCode)
	}
	_ = changed.Body.Close()
	stale := adminJSONRequest(t, http.MethodGet, server.URL+"/v2/admin/auth/me", token, "")
	if stale.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale token status=%d", stale.StatusCode)
	}
	_ = stale.Body.Close()
	oldLogin, _ := loginAdminToken(t, server.URL, "InitialPassword123!")
	if oldLogin.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password status=%d", oldLogin.StatusCode)
	}
	newLogin, newToken := loginAdminToken(t, server.URL, "NewPassword456!")
	if newLogin.StatusCode != http.StatusOK || newToken == "" {
		t.Fatalf("new password status=%d token=%q", newLogin.StatusCode, newToken)
	}
	persistence.mu.Lock()
	persistence.account.Status = "disabled"
	persistence.account.AuthVersion++
	persistence.mu.Unlock()
	disabled := adminJSONRequest(t, http.MethodGet, server.URL+"/v2/admin/auth/me", newToken, "")
	if disabled.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disabled account token status=%d", disabled.StatusCode)
	}
	_ = disabled.Body.Close()
}

func TestDatabaseAdminUsernameChangeAndOptionalEmailAccount(t *testing.T) {
	persistence := newMutableAdminStore(t, "platform_admin", []string{"settings.write"})
	a, err := app.New(context.Background(), persistence)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(config.Config{JWTSecret: strings.Repeat("n", 32)}, a).Handler())
	defer server.Close()

	login, token := loginAdminToken(t, server.URL, "InitialPassword123!")
	if login.StatusCode != http.StatusOK || token == "" {
		t.Fatalf("initial login status=%d token=%q", login.StatusCode, token)
	}
	renamed := adminJSONRequest(t, http.MethodPatch, server.URL+"/v2/admin/administrators/admin_db", token, `{"username":"new_admin","email":null,"confirmed":true,"reason":"rename test"}`)
	if renamed.StatusCode != http.StatusOK {
		t.Fatalf("rename status=%d", renamed.StatusCode)
	}
	_ = renamed.Body.Close()
	persistence.mu.Lock()
	clearedEmail := persistence.account.Email
	persistence.mu.Unlock()
	if clearedEmail != "" {
		t.Fatalf("contact email was not cleared: %q", clearedEmail)
	}
	stale := adminJSONRequest(t, http.MethodGet, server.URL+"/v2/admin/auth/me", token, "")
	if stale.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale token after username change status=%d", stale.StatusCode)
	}
	_ = stale.Body.Close()
	oldLogin, _ := loginAdminToken(t, server.URL, "InitialPassword123!")
	if oldLogin.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old username login status=%d", oldLogin.StatusCode)
	}
	newLogin := adminJSONRequest(t, http.MethodPost, server.URL+"/v2/admin/auth/login", "", `{"username":" NEW_ADMIN ","password":"InitialPassword123!"}`)
	if newLogin.StatusCode != http.StatusOK {
		t.Fatalf("new username login status=%d", newLogin.StatusCode)
	}
	var payload struct {
		AccessToken string `json:"accessToken"`
	}
	_ = json.NewDecoder(newLogin.Body).Decode(&payload)
	_ = newLogin.Body.Close()

	created := adminJSONRequest(t, http.MethodPost, server.URL+"/v2/admin/administrators", payload.AccessToken, `{"username":"no_email","displayName":"No Email","roleId":"support","password":"Password123!","confirmed":true,"reason":"optional email test"}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create without email status=%d", created.StatusCode)
	}
	var createdAccount store.AdminAccount
	_ = json.NewDecoder(created.Body).Decode(&createdAccount)
	_ = created.Body.Close()
	if createdAccount.Username != "no_email" || createdAccount.Email != "" {
		t.Fatalf("created account=%+v", createdAccount)
	}
	noEmailLogin := adminJSONRequest(t, http.MethodPost, server.URL+"/v2/admin/auth/login", "", `{"username":"no_email","password":"Password123!"}`)
	if noEmailLogin.StatusCode != http.StatusOK {
		t.Fatalf("no-email account login status=%d", noEmailLogin.StatusCode)
	}
	_ = noEmailLogin.Body.Close()
	duplicate := adminJSONRequest(t, http.MethodPost, server.URL+"/v2/admin/administrators", payload.AccessToken, `{"username":"NO_EMAIL","displayName":"Duplicate","roleId":"support","password":"Password123!","confirmed":true,"reason":"duplicate test"}`)
	if duplicate.StatusCode != http.StatusConflict {
		t.Fatalf("case-insensitive duplicate username status=%d", duplicate.StatusCode)
	}
	_ = duplicate.Body.Close()
}

func TestDatabaseAdminRateLimitUnavailableStoreAndRealtimePermissions(t *testing.T) {
	t.Run("login rate limit", func(t *testing.T) {
		persistence := newMutableAdminStore(t, "platform_admin", nil)
		a, _ := app.New(context.Background(), persistence)
		server := httptest.NewServer(New(config.Config{JWTSecret: strings.Repeat("r", 32)}, a).Handler())
		defer server.Close()
		for attempt := 1; attempt <= 6; attempt++ {
			response, _ := loginAdminToken(t, server.URL, "WrongPassword123!")
			want := http.StatusUnauthorized
			if attempt == 6 {
				want = http.StatusTooManyRequests
			}
			if response.StatusCode != want {
				t.Fatalf("attempt=%d status=%d want=%d", attempt, response.StatusCode, want)
			}
		}
	})
	t.Run("database unavailable does not fall back", func(t *testing.T) {
		persistence := newMutableAdminStore(t, "platform_admin", nil)
		persistence.lookupErr = errors.New("database connection unavailable")
		a, _ := app.New(context.Background(), persistence)
		api := New(config.Config{JWTSecret: strings.Repeat("u", 32)}, a)
		server := httptest.NewServer(api.Handler())
		defer server.Close()
		response, _ := loginAdminToken(t, server.URL, "InitialPassword123!")
		if response.StatusCode != http.StatusInternalServerError {
			t.Fatalf("database failure status=%d", response.StatusCode)
		}
		token, err := api.auth.IssueAdmin(persistence.account.ID, persistence.account.RoleID, time.Hour, persistence.account.AuthVersion)
		if err != nil {
			t.Fatal(err)
		}
		me := adminJSONRequest(t, http.MethodGet, server.URL+"/v2/admin/auth/me", token, "")
		if me.StatusCode != http.StatusInternalServerError {
			t.Fatalf("database failure during token validation status=%d", me.StatusCode)
		}
		_ = me.Body.Close()
	})
	t.Run("permissions and auth version are realtime", func(t *testing.T) {
		persistence := newMutableAdminStore(t, "custom_operator", []string{"users.write"})
		a, _ := app.New(context.Background(), persistence)
		api := New(config.Config{JWTSecret: strings.Repeat("p", 32)}, a)
		token, err := api.auth.IssueAdmin(persistence.account.ID, persistence.account.RoleID, time.Hour, persistence.account.AuthVersion)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(api.Handler())
		defer server.Close()
		denied := adminJSONRequest(t, http.MethodPost, server.URL+"/v2/admin/groups/missing/disband", token, `{"confirmed":true,"reason":"permission test"}`)
		if denied.StatusCode != http.StatusForbidden {
			t.Fatalf("group write before grant status=%d", denied.StatusCode)
		}
		_ = denied.Body.Close()
		persistence.mu.Lock()
		persistence.account.Permissions = append(persistence.account.Permissions, "groups.write")
		persistence.mu.Unlock()
		allowed := adminJSONRequest(t, http.MethodPost, server.URL+"/v2/admin/groups/missing/disband", token, `{"confirmed":true,"reason":"permission test"}`)
		if allowed.StatusCode == http.StatusForbidden || allowed.StatusCode == http.StatusUnauthorized {
			t.Fatalf("group write after grant status=%d", allowed.StatusCode)
		}
		_ = allowed.Body.Close()
		persistence.mu.Lock()
		persistence.account.AuthVersion++
		persistence.mu.Unlock()
		invalidated := adminJSONRequest(t, http.MethodGet, server.URL+"/v2/admin/auth/me", token, "")
		if invalidated.StatusCode != http.StatusUnauthorized {
			t.Fatalf("auth version invalidation status=%d", invalidated.StatusCode)
		}
		_ = invalidated.Body.Close()
	})
}
