package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/netutil"
	"time"
)

func (p *Postgres) RecordUserAccess(ctx context.Context, e UserAccessLog) error {
	e.IP = netutil.NormalizeIP(e.IP)
	if e.Event == "register" && e.Method == "admin" {
		e.IP = ""
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if e.UserID == "" && e.LookupPhone != "" {
		err = tx.QueryRow(ctx, `SELECT id FROM im_users WHERE phone=$1 AND created_at<=$2`, e.LookupPhone, e.OccurredAt).Scan(&e.UserID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	// Idempotency and the summary are committed together. Retry cannot move a
	// last-login marker backwards, or turn a legacy login into registration.
	tag, err := tx.Exec(ctx, `INSERT INTO im_user_access_logs(id,user_id,event,method,result,failure_code,ip,platform,occurred_at)
 VALUES($1,NULLIF($2,''),$3,$4,$5,$6,NULLIF($7,'')::inet,$8,$9) ON CONFLICT(id) DO NOTHING`, e.ID, e.UserID, e.Event, e.Method, e.Result, e.FailureCode, e.IP, e.Platform, e.OccurredAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if e.UserID != "" && e.Result == "success" {
		if e.Event == "register" {
			source := "app"
			if e.Method == "admin" {
				source = "admin"
				e.IP = ""
			}
			_, err = tx.Exec(ctx, `INSERT INTO im_user_access_profiles(user_id,registration_source,registration_ip)
    VALUES($1,$2,NULLIF($3,'')::inet) ON CONFLICT(user_id) DO UPDATE SET
    registration_source=excluded.registration_source,registration_ip=excluded.registration_ip
    WHERE im_user_access_profiles.registration_source='unknown'`, e.UserID, source, e.IP)
		} else if e.Event == "login" {
			_, err = tx.Exec(ctx, `INSERT INTO im_user_access_profiles(user_id,last_login_ip,last_login_at,last_login_event_id)
    VALUES($1,NULLIF($2,'')::inet,$3,$4) ON CONFLICT(user_id) DO UPDATE SET
    last_login_ip=excluded.last_login_ip,last_login_at=excluded.last_login_at,last_login_event_id=excluded.last_login_event_id
    WHERE im_user_access_profiles.last_login_at IS NULL OR
    (im_user_access_profiles.last_login_at,im_user_access_profiles.last_login_event_id)<(excluded.last_login_at,excluded.last_login_event_id)`, e.UserID, e.IP, e.OccurredAt, e.ID)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) UserAccessProfiles(ctx context.Context, ids []string, ip string) (map[string]UserAccessProfile, error) {
	result := map[string]UserAccessProfile{}
	rows, err := p.pool.Query(ctx, `SELECT u.id,COALESCE(p.registration_source,'unknown'),COALESCE(host(p.registration_ip),''),
 COALESCE(host(p.last_login_ip),''),p.last_login_at,
 ($2<>'' AND p.registration_ip=NULLIF($2,'')::inet) IS TRUE,
 ($2<>'' AND p.last_login_ip=NULLIF($2,'')::inet) IS TRUE,
 ($2<>'' AND EXISTS(SELECT 1 FROM im_user_access_logs l WHERE l.user_id=u.id AND l.ip=NULLIF($2,'')::inet AND l.result='success' AND l.occurred_at >= $3))
 FROM im_users u LEFT JOIN im_user_access_profiles p ON p.user_id=u.id WHERE u.id=ANY($1)`, ids, ip, time.Now().UTC().Add(-UserAccessRetention))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var v UserAccessProfile
		var reg, last, hist bool
		if err = rows.Scan(&id, &v.RegistrationSource, &v.RegistrationIP, &v.LastLoginIP, &v.LastLoginAt, &reg, &last, &hist); err != nil {
			return nil, err
		}
		v.MatchedSources = []string{}
		if reg {
			v.MatchedSources = append(v.MatchedSources, "registration")
		}
		if last {
			v.MatchedSources = append(v.MatchedSources, "last_login")
		}
		if hist {
			v.MatchedSources = append(v.MatchedSources, "history")
		}
		result[id] = v
	}
	return result, rows.Err()
}

type userAccessCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func ValidateUserAccessCursor(raw string) bool {
	_, err := decodeUserAccessCursor(raw)
	return err == nil
}
func decodeUserAccessCursor(raw string) (userAccessCursor, error) {
	var c userAccessCursor
	if raw == "" {
		return c, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	if err != nil {
		return c, err
	}
	if c.At.IsZero() || c.ID == "" {
		return c, errors.New("invalid access cursor")
	}
	return c, nil
}
func (p *Postgres) ListUserAccessLogs(ctx context.Context, q UserAccessQuery) (UserAccessPage, error) {
	page := UserAccessPage{Items: []UserAccessLog{}}
	c, err := decodeUserAccessCursor(q.Cursor)
	if err != nil {
		return page, err
	}
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 20
	}
	cutoff := time.Now().UTC().Add(-UserAccessRetention)
	if q.From.Before(cutoff) {
		q.From = cutoff
	}
	var cursorAt *time.Time
	if !c.At.IsZero() {
		cursorAt = &c.At
	}
	rows, err := p.pool.Query(ctx, `SELECT l.id,COALESCE(l.user_id,''),l.event,l.method,l.result,l.failure_code,COALESCE(host(l.ip),''),l.platform,l.occurred_at,
 COALESCE(u.name,''),COALESCE(u.phone,''),COALESCE(u.handle,''),COALESCE(u.avatar_url,'')
 FROM im_user_access_logs l LEFT JOIN im_users u ON u.id=l.user_id
 WHERE ($1='' OR l.user_id=$1) AND ($2='' OR l.ip=NULLIF($2,'')::inet) AND ($3='' OR l.event=$3)
 AND ($4='' OR l.result=$4) AND ($5='' OR l.method=$5) AND l.occurred_at >= $6 AND l.occurred_at <= $7
 AND ($8::timestamptz IS NULL OR (l.occurred_at,l.id)<($8,$9)) ORDER BY l.occurred_at DESC,l.id DESC LIMIT $10`, q.UserID, q.IP, q.Event, q.Result, q.Method, q.From, q.To, cursorAt, c.ID, q.Limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var e UserAccessLog
		u := &model.User{}
		if err = rows.Scan(&e.ID, &e.UserID, &e.Event, &e.Method, &e.Result, &e.FailureCode, &e.IP, &e.Platform, &e.OccurredAt, &u.Name, &u.Phone, &u.Handle, &u.AvatarURL); err != nil {
			return page, err
		}
		if e.UserID != "" {
			u.ID = e.UserID
			e.User = u
		}
		page.Items = append(page.Items, e)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		last := page.Items[len(page.Items)-1]
		b, _ := json.Marshal(userAccessCursor{last.OccurredAt, last.ID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return page, nil
}

// EXISTS avoids duplicate accounts even when thousands of successful logins
// share an IP. Failed attempts never establish an account/IP association.
const userAccessIPFilter = `($4='' OR EXISTS(SELECT 1 FROM im_user_access_profiles ap WHERE ap.user_id=u.id AND
 (($5 IN ('any','registration') AND ap.registration_ip=NULLIF($4,'')::inet) OR ($5 IN ('any','last_login') AND ap.last_login_ip=NULLIF($4,'')::inet)))
 OR ($5 IN ('any','history') AND EXISTS(SELECT 1 FROM im_user_access_logs al WHERE al.user_id=u.id AND al.ip=NULLIF($4,'')::inet AND al.result='success' AND al.occurred_at >= now()-interval '180 days')))`

func (p *Postgres) ListAdminUsersByIP(ctx context.Context, q, status, cursor string, limit int, ip, source string) ([]*model.User, int64, string, error) {
	return p.listAdminUsers(ctx, q, status, cursor, limit, ip, source)
}
