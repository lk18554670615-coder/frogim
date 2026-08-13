package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/linli/im/server/internal/config"
)

func TestAdminBackupsReportsHealthyMetrics(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("query"); got != backupMetricQuery {
			t.Fatalf("unexpected query %q", got)
		}
		write(w, http.StatusOK, map[string]any{
			"status": "success",
			"data": map[string]any{"result": []any{
				prometheusTestMetric("nexachat_backup_last_attempt_timestamp_seconds", now.Unix()),
				prometheusTestMetric("nexachat_backup_last_success_timestamp_seconds", now.Unix()),
				prometheusTestMetric("nexachat_backup_last_duration_seconds", 37),
				prometheusTestMetric("nexachat_backup_last_status", 1),
				prometheusTestMetric("nexachat_backup_running", 0),
				prometheusTestMetric("nexachat_backup_incomplete_generations", 0),
				prometheusTestMetric("nexachat_backup_offsite_enabled", 1),
			}},
		})
	}))
	defer upstream.Close()

	api := &API{cfg: config.Config{PrometheusURL: upstream.URL}}
	recorder := httptest.NewRecorder()
	api.adminBackups(recorder, httptest.NewRequest(http.MethodGet, "/v2/admin/backups", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Configured            bool       `json:"configured"`
		Available             bool       `json:"available"`
		Status                string     `json:"status"`
		LastStatus            bool       `json:"lastStatus"`
		LastDurationSeconds   int        `json:"lastDurationSeconds"`
		IncompleteGenerations int        `json:"incompleteGenerations"`
		OffsiteEnabled        bool       `json:"offsiteEnabled"`
		LastSuccessAt         *time.Time `json:"lastSuccessAt"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Configured || !payload.Available || payload.Status != "healthy" || !payload.LastStatus {
		t.Fatalf("unexpected status payload: %+v", payload)
	}
	if payload.LastDurationSeconds != 37 || payload.IncompleteGenerations != 0 || !payload.OffsiteEnabled {
		t.Fatalf("unexpected metric values: %+v", payload)
	}
	if payload.LastSuccessAt == nil || !payload.LastSuccessAt.Equal(now) {
		t.Fatalf("lastSuccessAt=%v want=%v", payload.LastSuccessAt, now)
	}
}

func TestAdminBackupsDegradesWithoutMonitoring(t *testing.T) {
	t.Run("unconfigured", func(t *testing.T) {
		api := &API{}
		recorder := httptest.NewRecorder()
		api.adminBackups(recorder, httptest.NewRequest(http.MethodGet, "/v2/admin/backups", nil))
		assertBackupAvailability(t, recorder, false, "unconfigured")
	})

	t.Run("upstream failure", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		defer upstream.Close()
		api := &API{cfg: config.Config{PrometheusURL: upstream.URL}}
		recorder := httptest.NewRecorder()
		api.adminBackups(recorder, httptest.NewRequest(http.MethodGet, "/v2/admin/backups", nil))
		assertBackupAvailability(t, recorder, true, "unavailable")
	})
}

func prometheusTestMetric(name string, value int64) map[string]any {
	return map[string]any{"metric": map[string]string{"__name__": name}, "value": []any{time.Now().Unix(), strconv.FormatInt(value, 10)}}
}

func assertBackupAvailability(t *testing.T, recorder *httptest.ResponseRecorder, configured bool, status string) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Configured bool   `json:"configured"`
		Available  bool   `json:"available"`
		Status     string `json:"status"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Configured != configured || payload.Available || payload.Status != status {
		t.Fatalf("unexpected availability: %+v", payload)
	}
}
