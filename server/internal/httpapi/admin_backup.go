package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const backupMetricQuery = `max by (__name__) ({__name__=~"nexachat_backup_(last_attempt_timestamp_seconds|last_success_timestamp_seconds|last_duration_seconds|last_status|running|incomplete_generations|offsite_enabled)"})`

type prometheusVectorResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  []json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func (x *API) adminBackups(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(strings.TrimSpace(x.cfg.PrometheusURL), "/")
	if base == "" {
		write(w, http.StatusOK, map[string]any{"configured": false, "available": false, "status": "unconfigured"})
		return
	}
	values := url.Values{"query": []string{backupMetricQuery}}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, base+"/api/v1/query?"+values.Encode(), nil)
	if err != nil {
		x.writeBackupMetricsUnavailable(w, err)
		return
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		x.writeBackupMetricsUnavailable(w, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		x.writeBackupMetricsUnavailable(w, &backupMetricsStatusError{status: response.StatusCode})
		return
	}
	var payload prometheusVectorResponse
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil || payload.Status != "success" {
		if err == nil {
			err = &backupMetricsStatusError{}
		}
		x.writeBackupMetricsUnavailable(w, err)
		return
	}
	metrics := map[string]float64{}
	for _, item := range payload.Data.Result {
		name := item.Metric["__name__"]
		if name == "" || len(item.Value) != 2 {
			continue
		}
		var raw string
		if json.Unmarshal(item.Value[1], &raw) != nil {
			continue
		}
		value, parseErr := strconv.ParseFloat(raw, 64)
		if parseErr != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		metrics[name] = value
	}
	lastAttempt := metrics["nexachat_backup_last_attempt_timestamp_seconds"]
	lastSuccess := metrics["nexachat_backup_last_success_timestamp_seconds"]
	lastStatus := metrics["nexachat_backup_last_status"] >= 0.5
	running := metrics["nexachat_backup_running"] >= 0.5
	incomplete := max(0, int(math.Round(metrics["nexachat_backup_incomplete_generations"])))
	status := "healthy"
	switch {
	case running:
		status = "running"
	case lastAttempt <= 0:
		status = "never"
	case !lastStatus:
		status = "failed"
	case incomplete > 0:
		status = "warning"
	case lastSuccess <= 0 || time.Since(time.Unix(int64(lastSuccess), 0)) > 26*time.Hour:
		status = "stale"
	}
	result := map[string]any{
		"configured": true, "available": true, "status": status,
		"lastStatus": lastStatus, "running": running,
		"lastDurationSeconds":   max(0, int(math.Round(metrics["nexachat_backup_last_duration_seconds"]))),
		"incompleteGenerations": incomplete,
		"offsiteEnabled":        metrics["nexachat_backup_offsite_enabled"] >= 0.5,
	}
	if lastAttempt > 0 {
		result["lastAttemptAt"] = time.Unix(int64(lastAttempt), 0).UTC()
	}
	if lastSuccess > 0 {
		result["lastSuccessAt"] = time.Unix(int64(lastSuccess), 0).UTC()
	}
	write(w, http.StatusOK, result)
}

type backupMetricsStatusError struct{ status int }

func (e *backupMetricsStatusError) Error() string {
	if e.status > 0 {
		return "Prometheus returned status " + strconv.Itoa(e.status)
	}
	return "Prometheus returned an invalid backup metrics response"
}

func (x *API) writeBackupMetricsUnavailable(w http.ResponseWriter, err error) {
	slog.Warn("backup metrics unavailable", "error", err)
	write(w, http.StatusOK, map[string]any{"configured": true, "available": false, "status": "unavailable"})
}
