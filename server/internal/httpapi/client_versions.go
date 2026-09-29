package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/clientversion"
	"github.com/linli/im/server/internal/store"
)

func (x *API) clientVersion(w http.ResponseWriter, r *http.Request) {
	if x.cfg.TenantID != "" {
		q := r.URL.Query()
		if _, e := clientversion.Evaluate(q.Get("platform"), q.Get("version"), q.Get("installId"), nil); e != nil {
			writeError(w, 400, "INVALID_ARGUMENT", "版本查询参数无效")
			return
		}
		var decision clientversion.Decision
		if x.platformControl == nil || x.platformControl.Call(r.Context(), "/internal/tenancy/client-version", map[string]string{"platform": q.Get("platform"), "version": q.Get("version"), "installId": q.Get("installId")}, &decision) != nil {
			writeError(w, 503, "PLATFORM_VERSION_UNAVAILABLE", "平台升级信息暂不可用，请稍后重试")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, map[string]any{"data": decision})
		return
	}
	decision, err := x.app.EvaluateClientVersion(
		r.Context(),
		r.URL.Query().Get("platform"),
		r.URL.Query().Get("version"),
		r.URL.Query().Get("installId"),
	)
	if err != nil {
		handleErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, http.StatusOK, map[string]any{"data": decision})
}

func (x *API) adminClientVersions(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.ListClientVersionPolicies(r.Context())
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"data": map[string]any{"items": items}})
}

func (x *API) adminClientVersionHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, total, next, err := x.app.ListClientVersionHistory(r.Context(), r.PathValue("platform"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "total": total, "nextCursor": next})
}

func (x *API) updateAdminClientVersion(w http.ResponseWriter, r *http.Request) {
	var request struct {
		MinimumVersion    string `json:"minimumVersion"`
		LatestVersion     string `json:"latestVersion"`
		ForceUpdate       bool   `json:"forceUpdate"`
		RolloutPercentage int    `json:"rolloutPercentage"`
		ReleaseNotes      string `json:"releaseNotes"`
		DownloadURL       string `json:"downloadUrl"`
		Reason            string `json:"reason"`
		Confirmed         bool   `json:"confirmed"`
	}
	if decode(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if !request.Confirmed || strings.TrimSpace(request.Reason) == "" {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	policy, err := x.app.UpdateClientVersionPolicy(
		r.Context(),
		store.ClientVersionPolicy{
			Platform:          r.PathValue("platform"),
			MinimumVersion:    request.MinimumVersion,
			LatestVersion:     request.LatestVersion,
			ForceUpdate:       request.ForceUpdate,
			RolloutPercentage: request.RolloutPercentage,
			ReleaseNotes:      request.ReleaseNotes,
			DownloadURL:       request.DownloadURL,
		},
		uid(r),
		request.Reason,
		time.Now(),
	)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"data": policy})
}
