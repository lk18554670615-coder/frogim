package tenancy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/config"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"io"
	"net/http"
	"strings"
	"time"
)

const enterpriseSchema = `
CREATE TABLE IF NOT EXISTS lp_config(tenant_id text PRIMARY KEY);

CREATE TABLE IF NOT EXISTS lp_identity(user_id text PRIMARY KEY REFERENCES im_users(id),revision bigint NOT NULL DEFAULT 0,epoch bigint NOT NULL DEFAULT 1,active boolean NOT NULL DEFAULT false,profile_version bigint NOT NULL DEFAULT 1,synced_version bigint NOT NULL DEFAULT 0,synced_at timestamptz,sync_error text NOT NULL DEFAULT '',prepared_by text NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS lp_offline(operation_id text PRIMARY KEY,user_id text NOT NULL,revision bigint NOT NULL,completed boolean NOT NULL DEFAULT false);
ALTER TABLE lp_identity ADD COLUMN IF NOT EXISTS synced_at timestamptz;
CREATE OR REPLACE FUNCTION lp_mark_profile() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
UPDATE lp_identity SET profile_version=profile_version+1 WHERE user_id=NEW.id; RETURN NEW; END $$;
DROP TRIGGER IF EXISTS lp_profile_change ON im_users;
CREATE TRIGGER lp_profile_change AFTER UPDATE OF phone,name,handle,gender,signature,avatar_media_id,avatar_url,allow_search_by_handle,allow_search_by_phone,banned,deleted_at ON im_users FOR EACH ROW EXECUTE FUNCTION lp_mark_profile();`

type Enterprise struct {
	DB      *pgxpool.Pool
	cfg     config.Config
	o       Options
	client  *http.Client
	key     ed25519.PublicKey
	media   *minio.Client
	Offline func(context.Context, string) error
	Session func(http.ResponseWriter, *http.Request, string)
}

func (e *Enterprise) PublicAPIBase() string { return e.o.PublicAPI }

func NewEnterprise(ctx context.Context, c config.Config, o Options) (*Enterprise, error) {
	if e := o.Validate(); e != nil {
		return nil, e
	}
	if c.LiveKitEnabled {
		signal := strings.Replace(strings.Replace(c.LiveKitURL, "wss://", "https://", 1), "ws://", "http://", 1)
		if strings.TrimRight(signal, "/") != strings.TrimRight(o.PublicAPI, "/")+"/livekit" {
			return nil, errors.New("enterprise LiveKit signaling must use the existing API /livekit entry; keep the native signaling port private")
		}
	}
	db, e := openPool(ctx, c.DatabaseURL)
	if e != nil {
		return nil, e
	}
	if _, e = db.Exec(ctx, enterpriseSchema); e != nil {
		db.Close()
		return nil, e
	}
	if _, e = db.Exec(ctx, `INSERT INTO lp_config(tenant_id) VALUES($1) ON CONFLICT DO NOTHING`, o.TenantID); e != nil {
		db.Close()
		return nil, e
	}
	var tenantCount int
	var tenantID string
	if e = db.QueryRow(ctx, `SELECT count(*),min(tenant_id) FROM lp_config`).Scan(&tenantCount, &tenantID); e != nil || tenantCount != 1 || tenantID != o.TenantID {
		db.Close()
		return nil, errors.New("enterprise database belongs to another tenant")
	}
	if _, e = db.Exec(ctx, `CREATE OR REPLACE FUNCTION lp_scope_push() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE n bigint;a boolean;t text; BEGIN
 SELECT active,epoch INTO a,n FROM lp_identity WHERE user_id=NEW.user_id;
 SELECT tenant_id INTO t FROM lp_config LIMIT 1;
 NEW.payload=NEW.payload||jsonb_build_object('tenantId',t,'recipientId',NEW.user_id,'enterpriseEpoch',n);
 IF a IS DISTINCT FROM true THEN NEW.status='failed';NEW.last_error='enterprise offline'; END IF;RETURN NEW; END $$;
 DROP TRIGGER IF EXISTS lp_push_scope ON im_push_outbox;
 CREATE TRIGGER lp_push_scope BEFORE INSERT ON im_push_outbox FOR EACH ROW EXECUTE FUNCTION lp_scope_push();`); e != nil {
		db.Close()
		return nil, e
	}
	client, e := ControlClient(o)
	if e != nil {
		db.Close()
		return nil, e
	}
	mc, e := minio.New(c.S3Endpoint, &minio.Options{Creds: credentials.NewStaticV4(c.S3AccessKey, c.S3SecretKey, ""), Secure: c.S3Secure})
	if e != nil {
		db.Close()
		return nil, e
	}
	en := &Enterprise{DB: db, cfg: c, o: o, client: client, media: mc}
	var k struct {
		Key string `json:"key"`
	}
	if e = en.platform(ctx, "/internal/directory/key", nil, &k); e != nil {
		db.Close()
		return nil, e
	}
	en.key, e = base64.RawURLEncoding.DecodeString(k.Key)
	if e != nil || len(en.key) != ed25519.PublicKeySize {
		db.Close()
		return nil, errors.New("invalid platform signing key")
	}
	return en, nil
}
func (e *Enterprise) Close() { e.DB.Close() }
func (e *Enterprise) platform(ctx context.Context, path string, in, out any) error {
	b, _ := json.Marshal(in)
	r, err := http.NewRequestWithContext(ctx, "POST", e.o.PlatformURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(r)
	if err != nil {
		return errors.New("platform control unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return errors.New("platform rejected grant or profile")
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	return nil
}
func (e *Enterprise) ticket(raw, typ, aud string) (jwt.MapClaims, error) {
	t, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) { return e.key, nil }, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithAudience(aud), jwt.WithExpirationRequired())
	if err != nil || !t.Valid {
		return nil, errors.New("invalid grant")
	}
	c := t.Claims.(jwt.MapClaims)
	if c["typ"] != typ {
		return nil, errors.New("invalid grant purpose")
	}
	return c, nil
}
func (e *Enterprise) lock(ctx context.Context, uid string) (*pgxpool.Conn, error) {
	c, err := e.DB.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	err = c.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, "enterprise:"+uid).Scan(&ok)
	if err != nil || !ok {
		c.Release()
		return nil, errors.New("identity busy")
	}
	return c, nil
}
func unlock(c *pgxpool.Conn, uid string) {
	_, err := c.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, "enterprise:"+uid)
	if err != nil {
		_ = c.Conn().Close(context.Background())
	}
	c.Release()
}
func (e *Enterprise) Exchange(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Ticket string `json:"ticket"`
	}
	if !decode(w, r, &b) {
		return
	}
	claims, err := e.ticket(b.Ticket, "enterprise-grant", e.o.TenantID)
	if err != nil {
		fail(w, 401, "INVALID_GRANT")
		return
	}
	uid, _ := claims.GetSubject()
	c, err := e.lock(r.Context(), uid)
	if err != nil {
		fail(w, 409, "IDENTITY_BUSY")
		return
	}
	defer unlock(c, uid)
	var u User
	if err = e.platform(r.Context(), "/internal/directory/consume", b, &u); err != nil {
		fail(w, 401, "GRANT_REJECTED")
		return
	}
	if _, err = e.prepare(r.Context(), Prepare{Operation: "register:" + uid, User: u}); err != nil {
		fail(w, 503, "ACCOUNT_PREPARATION_FAILED")
		return
	}
	var banned bool
	err = e.DB.QueryRow(r.Context(), `SELECT banned OR deleted_at IS NOT NULL FROM im_users WHERE id=$1`, uid).Scan(&banned)
	if err != nil || banned {
		fail(w, 403, "ACCOUNT_UNAVAILABLE")
		return
	}
	if _, err = c.Exec(r.Context(), `UPDATE im_users SET phone=$2 WHERE id=$1`, uid, u.Phone); err != nil {
		fail(w, 409, "PHONE_CONFLICT")
		return
	}
	res, err := c.Exec(r.Context(), `UPDATE lp_identity SET active=true,profile_version=profile_version+CASE WHEN revision<>$2 THEN 1 ELSE 0 END,revision=$2 WHERE user_id=$1 AND revision<=$2`, uid, u.Revision)
	if err != nil || res.RowsAffected() != 1 {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	if e.Session == nil {
		fail(w, 503, "SESSION_UNAVAILABLE")
		return
	}
	e.Session(w, r, uid)
}
func (e *Enterprise) Active(ctx context.Context, uid string, epoch int64) (bool, error) {
	var active bool
	var v int64
	err := e.DB.QueryRow(ctx, `SELECT active,epoch FROM lp_identity WHERE user_id=$1`, uid).Scan(&active, &v)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return active && (epoch < 0 || epoch == v), err
}
func (e *Enterprise) Epoch(ctx context.Context, uid string) (int64, error) {
	var v int64
	err := e.DB.QueryRow(ctx, `SELECT epoch FROM lp_identity WHERE user_id=$1 AND active`, uid).Scan(&v)
	return v, err
}
func (e *Enterprise) ControlHandler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /internal/directory/ready", func(w http.ResponseWriter, r *http.Request) {
		if e.DB.Ping(r.Context()) != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		jsonResponse(w, 200, map[string]any{"status": "ready", "tenantId": e.o.TenantID, "services": Services{API: e.o.PublicAPI, IMWS: e.cfg.WukongWSURL, IMTCP: e.cfg.WukongTCPURL, RTC: e.cfg.LiveKitURL, Media: e.o.PublicMedia}})
	})
	m.HandleFunc("POST /internal/directory/profile-read", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			UserID string `json:"userId"`
		}
		if !decode(w, r, &b) {
			return
		}
		s, err := e.profile(r.Context(), b.UserID)
		if err == pgx.ErrNoRows {
			jsonResponse(w, 200, map[string]bool{"exists": false})
			return
		}
		if err != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		jsonResponse(w, 200, map[string]any{"exists": true, "snapshot": s})
	})
	m.HandleFunc("POST /internal/directory/profile-status", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			UserID string `json:"userId"`
		}
		if !decode(w, r, &b) {
			return
		}
		var failed bool
		err := e.DB.QueryRow(r.Context(), `SELECT sync_error<>'' FROM lp_identity WHERE user_id=$1`, b.UserID).Scan(&failed)
		if err != nil && err != pgx.ErrNoRows {
			fail(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		jsonResponse(w, 200, map[string]bool{"failed": failed})
	})
	m.HandleFunc("POST /internal/directory/prepare", func(w http.ResponseWriter, r *http.Request) {
		var b Prepare
		if !decode(w, r, &b) {
			return
		}
		c, err := e.lock(r.Context(), b.User.ID)
		if err != nil {
			fail(w, 409, "IDENTITY_BUSY")
			return
		}
		defer unlock(c, b.User.ID)
		s, err := e.prepare(r.Context(), b)
		if err != nil {
			fail(w, 503, "PREPARATION_FAILED")
			return
		}
		jsonResponse(w, 200, s)
	})
	m.HandleFunc("POST /internal/directory/offline", e.offline)
	m.HandleFunc("POST /internal/directory/phone", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			UserID   string `json:"userId"`
			Phone    string `json:"phone"`
			Revision int64  `json:"revision"`
		}
		if !decode(w, r, &b) {
			return
		}
		if !phonePattern.MatchString(b.Phone) {
			fail(w, 400, "INVALID_PHONE")
			return
		}
		var exists bool
		if err := e.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1)`, b.UserID).Scan(&exists); err != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		if !exists {
			jsonResponse(w, 200, map[string]bool{"ok": true})
			return
		}
		res, err := e.DB.Exec(r.Context(), `UPDATE im_users SET phone=$2 WHERE id=$1 AND EXISTS(SELECT 1 FROM lp_identity WHERE user_id=$1 AND NOT active AND revision<=$3)`, b.UserID, b.Phone, b.Revision)
		if err != nil || res.RowsAffected() != 1 {
			fail(w, 409, "PHONE_CONFLICT")
			return
		}
		jsonResponse(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("GET /internal/directory/avatar/{id}", e.avatar)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who := peer(r)
		if who != "platform" && !(r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/internal/directory/avatar/") && who != "") {
			fail(w, 403, "PLATFORM_IDENTITY_REQUIRED")
			return
		}
		m.ServeHTTP(w, r)
	})
}
func (e *Enterprise) profile(ctx context.Context, uid string) (Snapshot, error) {
	s := Snapshot{UserID: uid}
	err := e.DB.QueryRow(ctx, `SELECT i.revision,i.profile_version,u.name,COALESCE(u.handle,''),u.gender,u.signature,COALESCE(u.avatar_media_id,''),u.avatar_url,u.allow_search_by_handle,u.allow_search_by_phone,u.banned,u.created_at,u.deleted_at FROM lp_identity i JOIN im_users u ON u.id=i.user_id WHERE u.id=$1`, uid).Scan(&s.Revision, &s.Version, &s.Profile.Name, &s.Profile.Handle, &s.Profile.Gender, &s.Profile.Signature, &s.Profile.AvatarMediaID, &s.Profile.AvatarURL, &s.Profile.SearchHandle, &s.Profile.SearchPhone, &s.Profile.Banned, &s.Profile.CreatedAt, &s.Profile.DeletedAt)
	return s, err
}
func (e *Enterprise) prepare(ctx context.Context, b Prepare) (Snapshot, error) {
	var prepared string
	err := e.DB.QueryRow(ctx, `SELECT prepared_by FROM lp_identity WHERE user_id=$1`, b.User.ID).Scan(&prepared)
	if err == nil && prepared != b.Operation {
		return e.profile(ctx, b.User.ID)
	}
	if err != nil && err != pgx.ErrNoRows {
		return Snapshot{}, err
	}
	if err == pgx.ErrNoRows {
		tx, err := e.DB.Begin(ctx)
		if err != nil {
			return Snapshot{}, err
		}
		defer tx.Rollback(ctx)
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1 OR phone=$2)`, b.User.ID, b.User.Phone).Scan(&exists)
		if err != nil || exists {
			return Snapshot{}, errors.New("identity collision; explicit import required")
		}
		name := b.User.Profile.Name
		if name == "" {
			name = "新用户"
		}
		gender := b.User.Profile.Gender
		if gender == "" {
			gender = "unspecified"
		}
		_, err = tx.Exec(ctx, `INSERT INTO im_users(id,phone,name,handle,gender,signature,allow_search_by_handle,allow_search_by_phone,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now())`, b.User.ID, b.User.Phone, name, "u_"+digest(b.User.ID)[:12], gender, b.User.Profile.Signature, b.User.Profile.SearchHandle, b.User.Profile.SearchPhone)
		if err != nil {
			return Snapshot{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO lp_identity(user_id,prepared_by) VALUES($1,$2)`, b.User.ID, b.Operation)
		if err != nil {
			return Snapshot{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return Snapshot{}, err
		}
	}
	if b.AvatarSource != "" {
		if err = e.copyAvatar(ctx, b); err != nil {
			return Snapshot{}, err
		}
	}
	return e.profile(ctx, b.User.ID)
}
func (e *Enterprise) offline(w http.ResponseWriter, r *http.Request) {
	var b struct {
		UserID      string `json:"userId"`
		Revision    int64  `json:"revision"`
		OperationID string `json:"operationId"`
	}
	if !decode(w, r, &b) {
		return
	}
	ctx := r.Context()
	c, err := e.lock(ctx, b.UserID)
	if err != nil {
		fail(w, 409, "IDENTITY_BUSY")
		return
	}
	defer unlock(c, b.UserID)
	var done bool
	err = c.QueryRow(ctx, `SELECT completed FROM lp_offline WHERE operation_id=$1 AND user_id=$2 AND revision=$3`, b.OperationID, b.UserID, b.Revision).Scan(&done)
	if err == nil && done {
		jsonResponse(w, 200, map[string]bool{"offline": true})
		return
	}
	var rev int64
	err = c.QueryRow(ctx, `SELECT revision FROM lp_identity WHERE user_id=$1`, b.UserID).Scan(&rev)
	if err == pgx.ErrNoRows {
		jsonResponse(w, 200, map[string]bool{"offline": true})
		return
	}
	if err != nil || rev > b.Revision {
		fail(w, 409, "STALE_OPERATION")
		return
	}
	tx, err := c.Begin(ctx)
	if err != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO lp_offline(operation_id,user_id,revision) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, b.OperationID, b.UserID, b.Revision)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE lp_identity SET active=false,epoch=epoch+1 WHERE user_id=$1;`, b.UserID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE im_refresh_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, b.UserID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE im_devices SET notifications_enabled=false,push_token='' WHERE user_id=$1`, b.UserID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE im_push_outbox SET status='failed',last_error='enterprise session offline' WHERE user_id=$1 AND status IN ('pending','sending')`, b.UserID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	if e.Offline == nil || e.Offline(ctx, b.UserID) != nil {
		fail(w, 503, "OFFLINE_CONFIRMATION_FAILED")
		return
	}
	_, err = c.Exec(ctx, `UPDATE lp_offline SET completed=true WHERE operation_id=$1`, b.OperationID)
	if err != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	jsonResponse(w, 200, map[string]bool{"offline": true})
}
func (e *Enterprise) RunSync(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		e.syncOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
func (e *Enterprise) syncOnce(ctx context.Context) {
	rows, err := e.DB.Query(ctx, `SELECT user_id FROM lp_identity WHERE active AND (profile_version>synced_version OR synced_at IS NULL OR synced_at<now()-interval '30 seconds') ORDER BY synced_at NULLS FIRST,user_id LIMIT 50`)
	if err != nil {
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		s, err := e.profile(ctx, id)
		if err != nil {
			continue
		}
		err = e.platform(ctx, "/internal/directory/profile", s, nil)
		if err != nil {
			_, _ = e.DB.Exec(ctx, `UPDATE lp_identity SET sync_error=$2 WHERE user_id=$1`, id, err.Error())
			continue
		}
		_, _ = e.DB.Exec(ctx, `UPDATE lp_identity SET synced_version=GREATEST(synced_version,$2),synced_at=now(),sync_error='' WHERE user_id=$1 AND revision=$3`, id, s.Version, s.Revision)
	}
}
func (e *Enterprise) avatar(w http.ResponseWriter, r *http.Request) {
	claims, err := e.ticket(bearer(r), "avatar-copy", peer(r))
	if err != nil || claims["source"] != e.o.TenantID || claims["sub"] != r.PathValue("id") {
		fail(w, 403, "AVATAR_GRANT_REJECTED")
		return
	}
	s, err := e.profile(r.Context(), r.PathValue("id"))
	if err != nil || claims["media"] != s.Profile.AvatarMediaID {
		fail(w, 409, "AVATAR_CHANGED")
		return
	}
	var key, mime string
	var size int64
	err = e.DB.QueryRow(r.Context(), `SELECT object_key,mime,size FROM im_media WHERE id=$1 AND owner_id=$2 AND status='ready'`, s.Profile.AvatarMediaID, s.UserID).Scan(&key, &mime, &size)
	if err != nil || size > 10<<20 {
		fail(w, 404, "AVATAR_UNAVAILABLE")
		return
	}
	object, err := e.media.GetObject(r.Context(), e.cfg.S3Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		fail(w, 503, "MEDIA_UNAVAILABLE")
		return
	}
	defer object.Close()
	if _, err = object.Stat(); err != nil {
		fail(w, 503, "MEDIA_UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, io.LimitReader(object, 10<<20))
}
func (e *Enterprise) copyAvatar(ctx context.Context, b Prepare) error {
	if !strings.HasPrefix(b.AvatarSource, "https://") {
		return errors.New("invalid avatar source")
	}
	r, err := http.NewRequestWithContext(ctx, "GET", b.AvatarSource, nil)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+b.AvatarGrant)
	resp, err := e.client.Do(r)
	if err != nil {
		return errors.New("avatar source unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("avatar copy refused")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (10<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 10<<20 {
		return errors.New("invalid avatar size")
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return errors.New("avatar is not an image")
	}
	id := "avatar_" + digest(b.Operation)[:24]
	key := "avatars/" + b.User.ID + "/" + id
	_, err = e.media.PutObject(ctx, e.cfg.S3Bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: mime})
	if err != nil {
		return errors.New("avatar storage unavailable")
	}
	tx, err := e.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO im_media(id,owner_id,object_key,mime,size,status,completed_at) VALUES($1,$2,$3,$4,$5,'ready',now()) ON CONFLICT(id) DO NOTHING`, id, b.User.ID, key, mime, len(data))
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE im_users SET avatar_media_id=$2,avatar_url='' WHERE id=$1`, b.User.ID, id)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return err
}
func (e *Enterprise) String() string { return fmt.Sprintf("enterprise %s", e.o.TenantID) }
