package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
	"golang.org/x/crypto/bcrypt"
)

func TestAdminUserBatchCreatesValidRowsAndReturnsFailuresInOrder(t *testing.T) {
	application, err := app.New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	existingFixture, err := application.CreateAdminUser(context.Background(), "test-admin", "13822220000", "已有用户", "StrongPass123!", "unspecified", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("b", 32), DevMode: true, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	api := New(cfg, application)
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()

	body := `{"items":[
		{"clientRow":2,"phone":"13822220001","name":"批量一","password":"StrongPass123!","gender":"female"},
		{"clientRow":3,"phone":"13822220000","name":"不能覆盖","password":"StrongPass123!","gender":"male"},
		{"clientRow":4,"phone":"12822220002","name":"错误号码","password":"StrongPass123!","gender":"male"},
		{"clientRow":5,"phone":"13922220003","name":"批量二","password":"AnotherPass123!","gender":"unspecified"}
	],"reason":"运营工单 BATCH-HTTP","confirmed":true}`
	response := adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/users/batch", adminTestToken(t, cfg.JWTSecret), body)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var payload struct {
		BatchID                  string `json:"batchId"`
		Total, Succeeded, Failed int
		Items                    []app.AdminUserBatchItemResult `json:"items"`
	}
	if err = json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.BatchID == "" || payload.Total != 4 || payload.Succeeded != 2 || payload.Failed != 2 || len(payload.Items) != 4 {
		t.Fatalf("payload=%+v", payload)
	}
	wantStatus := []string{"created", "failed", "failed", "created"}
	wantCode := []string{"", "PHONE_ALREADY_EXISTS", "INVALID_PHONE", ""}
	for index, item := range payload.Items {
		if item.ClientRow != index+2 || item.Status != wantStatus[index] || item.Code != wantCode[index] {
			t.Fatalf("item[%d]=%+v", index, item)
		}
	}
	encoded, _ := json.Marshal(payload)
	if strings.Contains(string(encoded), "StrongPass") || strings.Contains(string(encoded), "AnotherPass") {
		t.Fatalf("response leaked a password: %s", encoded)
	}
	existing, err := application.User(payload.Items[0].User.ID)
	if err != nil || existing.Name != "批量一" {
		t.Fatalf("created user=%+v err=%v", existing, err)
	}
	existing, err = application.User(existingFixture.ID)
	if err != nil || existing.Name != "已有用户" {
		t.Fatalf("existing user was overwritten: user=%+v err=%v", existing, err)
	}
}

func TestAdminUserBatchRequiresWritePermissionConfirmationAndBoundedItems(t *testing.T) {
	application, err := app.New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("p", 32), DevMode: true, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	api := New(cfg, application)
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()
	platformToken := adminTestToken(t, cfg.JWTSecret)
	supportToken, err := api.auth.IssueAdmin("support-1", "support", time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	validItem := `{"clientRow":2,"phone":"13833330001","name":"批量用户","password":"StrongPass123!","gender":"female"}`
	checks := []struct {
		name, token, body string
		status            int
	}{
		{name: "missing confirmation", token: platformToken, body: `{"items":[` + validItem + `],"reason":"reason"}`, status: http.StatusBadRequest},
		{name: "empty items", token: platformToken, body: `{"items":[],"reason":"reason","confirmed":true}`, status: http.StatusBadRequest},
		{name: "read only administrator", token: supportToken, body: `{"items":[` + validItem + `],"reason":"reason","confirmed":true}`, status: http.StatusForbidden},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			response := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/admin/users/batch", check.token, check.body)
			defer response.Body.Close()
			if response.StatusCode != check.status {
				t.Fatalf("status=%d want=%d", response.StatusCode, check.status)
			}
		})
	}
	items := make([]app.AdminUserBatchInput, 101)
	for index := range items {
		items[index] = app.AdminUserBatchInput{ClientRow: index + 2, Phone: "13833330001", Name: "用户", Password: "StrongPass123!", Gender: "female"}
	}
	oversized, _ := json.Marshal(map[string]any{"items": items, "reason": "reason", "confirmed": true})
	response := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/admin/users/batch", platformToken, string(oversized))
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized status=%d", response.StatusCode)
	}
}

func TestAdminUserBatchPostgresPersistsIndependentHashesAndSummaryAudit(t *testing.T) {
	databaseURL := os.Getenv("IM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repository, err := store.NewPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	application, err := app.New(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("i", 32), DevMode: true, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	token := postgresAdminTestToken(t, repository, cfg.JWTSecret)
	api := New(cfg, application)
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()

	seed := time.Now().UnixNano() % 100_000_000
	phone1 := fmt.Sprintf("136%08d", seed)
	phone2 := fmt.Sprintf("137%08d", (seed+1)%100_000_000)
	body := fmt.Sprintf(`{"items":[
		{"clientRow":2,"phone":%q,"name":"数据库批量一","password":"StrongPass123!","gender":"female"},
		{"clientRow":3,"phone":%q,"name":"数据库批量二","password":"StrongPass123!","gender":"male"}
	],"reason":"PostgreSQL batch integration","confirmed":true}`, phone1, phone2)
	response := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/admin/users/batch", token, body)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var payload struct {
		BatchID string                         `json:"batchId"`
		Items   []app.AdminUserBatchItemResult `json:"items"`
	}
	if err = json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.BatchID == "" || len(payload.Items) != 2 || payload.Items[0].User == nil || payload.Items[1].User == nil {
		t.Fatalf("payload=%+v", payload)
	}

	cleanup, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup.Close()
	defer func() {
		userIDs := []string{payload.Items[0].User.ID, payload.Items[1].User.ID}
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM im_audits WHERE target_id=$1 OR target_id=ANY($2::text[])`, payload.BatchID, userIDs)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM im_users WHERE id=ANY($1::text[])`, userIDs)
	}()

	_, hash1, err := repository.PasswordCredentials(ctx, phone1)
	if err != nil {
		t.Fatal(err)
	}
	_, hash2, err := repository.PasswordCredentials(ctx, phone2)
	if err != nil {
		t.Fatal(err)
	}
	if hash1 == hash2 || bcrypt.CompareHashAndPassword([]byte(hash1), []byte("StrongPass123!")) != nil || bcrypt.CompareHashAndPassword([]byte(hash2), []byte("StrongPass123!")) != nil {
		t.Fatal("batch users must have independently salted valid bcrypt hashes")
	}
	audits, _, _, err := repository.ListAdminAudits(ctx, payload.BatchID, "", "", 10)
	if err != nil || len(audits) != 1 || audits[0].Action != "user.batch_created" || audits[0].Result != "success" {
		t.Fatalf("batch audits=%+v err=%v", audits, err)
	}
	metadata, _ := json.Marshal(audits[0].Metadata)
	if strings.Contains(strings.ToLower(string(metadata)), "password") || strings.Contains(string(metadata), "StrongPass123!") {
		t.Fatalf("batch audit leaked password data: %s", metadata)
	}
}
