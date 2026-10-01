package tenancy

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type versionPublication struct {
	ID         string          `json:"id"`
	Platform   string          `json:"platform"`
	Version    int64           `json:"version"`
	Policy     json.RawMessage `json:"policy"`
	Actor      string          `json:"actor"`
	Reason     string          `json:"reason"`
	Source     string          `json:"source"`
	RecordedAt time.Time       `json:"recordedAt"`
}

func (p *Platform) versionHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	page := 1
	var err error
	if value := r.URL.Query().Get("page"); value != "" {
		page, err = strconv.Atoi(value)
	}
	if (id != "android" && id != "ios" && id != "web") || err != nil || page < 1 || page > 100000 {
		fail(w, 400, "INVALID_ARGUMENT")
		return
	}
	const size = 20
	rows, err := p.DB.Query(r.Context(), `SELECT id::text,platform,version,policy,actor,reason,source,recorded_at FROM lp_version_history WHERE platform=$1 ORDER BY version DESC LIMIT $2 OFFSET $3`, id, size+1, (page-1)*size)
	if err != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	defer rows.Close()
	items := []versionPublication{}
	for rows.Next() {
		var item versionPublication
		if err = rows.Scan(&item.ID, &item.Platform, &item.Version, &item.Policy, &item.Actor, &item.Reason, &item.Source, &item.RecordedAt); err != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	hasMore := len(items) > size
	if hasMore {
		items = items[:size]
	}
	jsonResponse(w, 200, map[string]any{"items": items, "page": page, "hasMore": hasMore})
}
