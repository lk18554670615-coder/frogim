package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/linli/im/server/internal/store"
)

func (x *API) clientDiagnostic(w http.ResponseWriter, r *http.Request) {
	if !x.limits.allow("client-diagnostic:"+uid(r), 30, time.Hour) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many diagnostic reports")
		return
	}
	var payload struct {
		Kind        string `json:"kind"`
		Name        string `json:"name"`
		Fingerprint string `json:"fingerprint"`
		Platform    string `json:"platform"`
		AppVersion  string `json:"appVersion"`
		DurationMS  *int64 `json:"durationMs"`
	}
	if decode(r, &payload) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.RecordClientDiagnostic(uid(r), store.ClientDiagnostic{Kind: payload.Kind, Name: payload.Name, Fingerprint: payload.Fingerprint, Platform: payload.Platform, AppVersion: payload.AppVersion, DurationMS: payload.DurationMS}); err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (x *API) adminClientDiagnostics(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, summary, err := x.app.AdminClientDiagnostics(r.URL.Query().Get("kind"), r.URL.Query().Get("platform"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "summary": summary})
}
