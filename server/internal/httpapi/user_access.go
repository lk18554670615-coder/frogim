package httpapi

import (
	"context"
	"encoding/json"
	"github.com/linli/im/server/internal/ipregion"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/netutil"
	"github.com/linli/im/server/internal/store"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type accessAttempt struct {
	x     *API
	entry store.UserAccessLog
	out   *accessOutcomeWriter
}
type accessOutcomeWriter struct {
	http.ResponseWriter
	status int
	code   string
}

func (w *accessOutcomeWriter) WriteHeader(s int) {
	if w.status == 0 {
		w.status = s
	}
	w.ResponseWriter.WriteHeader(s)
}
func (w *accessOutcomeWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.status >= 400 {
		var v struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &v)
		switch v.Error.Code {
		case "INVALID_CODE", "INVALID_CREDENTIALS", "FORBIDDEN", "ACCOUNT_EXISTS", "RATE_LIMITED", "SMS_NOT_CONFIGURED", "SMS_UNAVAILABLE", "IM_UNAVAILABLE", "QR_LOGIN_NOT_FOUND", "QR_LOGIN_EXPIRED", "QR_LOGIN_USED", "QR_LOGIN_ACCOUNT_UNAVAILABLE", "INVALID_ARGUMENT":
			w.code = v.Error.Code
		default:
			w.code = "AUTH_UNAVAILABLE"
		}
	}
	return w.ResponseWriter.Write(b)
}
func (x *API) beginUserAccess(w *http.ResponseWriter, r *http.Request, event, method, phone string) *accessAttempt {
	out := &accessOutcomeWriter{ResponseWriter: *w}
	*w = out
	return &accessAttempt{x: x, out: out, entry: x.newUserAccess(r, event, method, "", phone)}
}
func (x *API) newUserAccess(r *http.Request, event, method, userID, phone string) store.UserAccessLog {
	platform := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Client-Platform")))
	switch platform {
	case "android", "ios", "web", "macos":
	default:
		platform = "unknown"
	}
	return store.UserAccessLog{ID: "access_" + newRequestID(), UserID: userID, LookupPhone: phone, Event: event, Method: method, IP: netutil.NormalizeIP(x.clientIP(r)), Platform: platform, OccurredAt: time.Now().UTC()}
}
func (a *accessAttempt) finish() {
	if a.out.status == http.StatusAccepted {
		return
	} // QR polling is not a login.
	a.entry.OccurredAt = time.Now().UTC()
	a.entry.Result = "failed"
	a.entry.FailureCode = a.out.code
	if a.out.status == 200 {
		a.entry.Result = "success"
		a.entry.FailureCode = ""
	} else if a.entry.FailureCode == "" {
		a.entry.FailureCode = "AUTH_UNAVAILABLE"
	}
	a.x.accessRecorder.Enqueue(a.entry)
}
func (x *API) recordRegistration(r *http.Request, userID, method string) {
	e := x.newUserAccess(r, "register", method, userID, "")
	e.Result = "success"
	if method == "admin" {
		e.IP = ""
		e.Platform = "admin"
	}
	x.accessRecorder.Enqueue(e)
}
func (x *API) RunUserAccess(ctx context.Context) { x.accessRecorder.Run(ctx) }
func (x *API) Close() {
	if x.ipRegion != nil {
		x.ipRegion.Close()
	}
}

type adminAccessProfile struct {
	store.UserAccessProfile
	RegistrationRegion ipregion.Region `json:"registrationRegion"`
	LastLoginRegion    ipregion.Region `json:"lastLoginRegion"`
}
type adminAccessUser struct {
	*model.User
	Access adminAccessProfile `json:"access"`
}

func (x *API) adminAccessUsers(ctx context.Context, users []*model.User, ip string) ([]adminAccessUser, error) {
	ids := make([]string, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	profiles, err := x.app.UserAccessProfiles(ctx, ids, ip)
	if err != nil {
		return nil, err
	}
	result := make([]adminAccessUser, 0, len(users))
	for _, u := range users {
		p, ok := profiles[u.ID]
		if !ok {
			p = store.UserAccessProfile{RegistrationSource: "unknown", MatchedSources: []string{}}
		}
		result = append(result, adminAccessUser{u, adminAccessProfile{p, x.ipRegion.Lookup(p.RegistrationIP), x.ipRegion.Lookup(p.LastLoginIP)}})
	}
	return result, nil
}
func parseAccessIP(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return "", true
	}
	ip := netutil.NormalizeIP(raw)
	return ip, ip != ""
}
func memberOf(raw string, values ...string) bool {
	for _, v := range values {
		if raw == v {
			return true
		}
	}
	return false
}

func (x *API) adminUserAccessLogs(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	now := time.Now().UTC()
	q := store.UserAccessQuery{UserID: strings.TrimSpace(v.Get("userId")), Event: v.Get("event"), Method: v.Get("method"), Result: v.Get("result"), Cursor: v.Get("cursor"), From: now.Add(-30 * 24 * time.Hour), To: now, Limit: 20}
	ip, ok := parseAccessIP(v.Get("ip"))
	q.IP = ip
	if !ok || !memberOf(q.Event, "", "register", "login") || !memberOf(q.Method, "", "otp", "password", "qr", "admin") || !memberOf(q.Result, "", "success", "failed") || !store.ValidateUserAccessCursor(q.Cursor) {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid access filters")
		return
	}
	for key, target := range map[string]*time.Time{"from": &q.From, "to": &q.To} {
		if raw := v.Get(key); raw != "" {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				writeError(w, 400, "INVALID_ARGUMENT", "invalid date filter")
				return
			}
			*target = t.UTC()
		}
	}
	cutoff := now.Add(-store.UserAccessRetention)
	if q.From.Before(cutoff) {
		q.From = cutoff
	}
	if q.To.After(now) {
		q.To = now
	}
	if q.From.After(q.To) {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid date range")
		return
	}
	if raw := v.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeError(w, 400, "INVALID_ARGUMENT", "limit must be 1-100")
			return
		}
		q.Limit = n
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := x.app.ListUserAccessLogs(ctx, q)
	if err != nil {
		handleErr(w, err)
		return
	}
	type entry struct {
		store.UserAccessLog
		Region ipregion.Region `json:"region"`
	}
	items := make([]entry, 0, len(page.Items))
	for _, e := range page.Items {
		if e.User != nil {
			x.signAvatarURL(e.User)
		}
		items = append(items, entry{e, x.ipRegion.Lookup(e.IP)})
	}
	x.app.RecordAdminAudit(uid(r), "user.access_logs.viewed", "user_access", q.UserID, "success", x.clientIP(r), map[string]any{"ip": q.IP, "event": q.Event, "result": q.Result, "method": q.Method, "from": q.From, "to": q.To, "returned": len(items)})
	write(w, 200, map[string]any{"items": items, "nextCursor": page.NextCursor, "from": q.From, "to": q.To, "retentionDays": 180, "geoVersion": ipregion.Version})
}
