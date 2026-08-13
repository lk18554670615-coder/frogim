package store

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/wukong"
)

//go:embed schema.sql
var normalizedSchema string

type Postgres struct{ pool *pgxpool.Pool }

const schemaVersion = 48

type PostgresOptions struct {
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	StatementTimeout  time.Duration
}

func secureOpaqueToken(prefix string) (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func NewPostgres(ctx context.Context, url string) (*Postgres, error) {
	return NewPostgresWithOptions(ctx, url, PostgresOptions{})
}

func NewPostgresWithOptions(ctx context.Context, url string, options PostgresOptions) (*Postgres, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if options.MaxConns <= 0 {
		options.MaxConns = 20
	}
	if options.MinConns < 0 {
		options.MinConns = 0
	}
	if options.MinConns == 0 && options.MaxConns >= 4 {
		options.MinConns = 2
	}
	if options.MaxConnLifetime <= 0 {
		options.MaxConnLifetime = time.Hour
	}
	if options.MaxConnIdleTime <= 0 {
		options.MaxConnIdleTime = 15 * time.Minute
	}
	if options.HealthCheckPeriod <= 0 {
		options.HealthCheckPeriod = time.Minute
	}
	config.MaxConns = options.MaxConns
	config.MinConns = options.MinConns
	config.MaxConnLifetime = options.MaxConnLifetime
	config.MaxConnLifetimeJitter = min(10*time.Minute, options.MaxConnLifetime/4)
	config.MaxConnIdleTime = options.MaxConnIdleTime
	config.HealthCheckPeriod = options.HealthCheckPeriod
	if options.StatementTimeout <= 0 {
		options.StatementTimeout = 15 * time.Second
	}
	config.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(options.StatementTimeout.Milliseconds(), 10)
	config.ConnConfig.RuntimeParams["lock_timeout"] = "5000"
	config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "15000"
	config.ConnConfig.RuntimeParams["application_name"] = "nexachat-server"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	p := &Postgres{pool: pool}
	if err = p.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err = p.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err = p.recoverWukongWebhookEvents(ctx, 20000); err != nil {
		pool.Close()
		return nil, err
	}
	return p, nil
}

func (p *Postgres) migrate(ctx context.Context) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(490739125)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS im_schema_migrations(version integer PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	var current int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0) FROM im_schema_migrations`).Scan(&current); err != nil {
		return err
	}
	if current < schemaVersion {
		if _, err = tx.Exec(ctx, normalizedSchema); err != nil {
			return err
		}
		if current < 44 {
			// Versions before 44 acknowledged these events without consuming all
			// side effects and retained completed message bodies. This is a
			// one-time upgrade cleanup; keeping it out of normalizedSchema avoids
			// discarding a legitimate delayed event on a future schema upgrade.
			if _, err = tx.Exec(ctx, `
				UPDATE im_wukong_webhook_events
				SET status='completed',payload='{}'::jsonb,completed_at=now(),locked_at=NULL,last_error='superseded before schema 44'
				WHERE event_type IN ('msg.offline','user.onlinestatus','msg.stream')
				  AND status IN ('pending','processing') AND received_at<now()-interval '5 minutes';
				UPDATE im_wukong_webhook_events
				SET payload='{}'::jsonb
				WHERE status IN ('completed','failed') AND payload<>'{}'::jsonb;
			`); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO im_schema_migrations(version) VALUES($1) ON CONFLICT DO NOTHING`, schemaVersion); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
func (p *Postgres) Close()                         { p.pool.Close() }
func (p *Postgres) IsNormalized() bool             { return true }

func (p *Postgres) Load(ctx context.Context) (*model.State, error) {
	s := model.NewState()
	if err := p.pool.QueryRow(ctx, `SELECT revision FROM im_state_meta WHERE singleton=true`).Scan(&s.Revision); err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `SELECT id,phone,name,COALESCE(handle,''),handle_change_count,signature,COALESCE(avatar_media_id,''),avatar_url,banned,created_at FROM im_users`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		u := &model.User{}
		if err = rows.Scan(&u.ID, &u.Phone, &u.Name, &u.Handle, &u.HandleChangeCount, &u.Signature, &u.AvatarMediaID, &u.AvatarURL, &u.Banned, &u.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		s.Users[u.ID] = u
		s.PhoneToUser[u.Phone] = u.ID
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows, err = p.pool.Query(ctx, `SELECT id,from_user_id,to_user_id,message,source,source_id,status,created_at,expires_at,updated_at,resolved_at FROM im_friend_requests`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		r := &model.FriendRequest{}
		if err = rows.Scan(&r.ID, &r.FromUserID, &r.ToUserID, &r.Message, &r.Source, &r.SourceID, &r.Status, &r.CreatedAt, &r.ExpiresAt, &r.UpdatedAt, &r.ResolvedAt); err != nil {
			rows.Close()
			return nil, err
		}
		s.FriendRequests[r.ID] = r
	}
	rows.Close()
	rows, err = p.pool.Query(ctx, `SELECT user_id,friend_user_id FROM im_friendships`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a, b string
		if err = rows.Scan(&a, &b); err != nil {
			rows.Close()
			return nil, err
		}
		if s.Friends[a] == nil {
			s.Friends[a] = map[string]bool{}
		}
		s.Friends[a][b] = true
	}
	rows.Close()
	rows, err = p.pool.Query(ctx, `SELECT user_id,blocked_user_id FROM im_blocks`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a, b string
		if err = rows.Scan(&a, &b); err != nil {
			rows.Close()
			return nil, err
		}
		if s.Blocks[a] == nil {
			s.Blocks[a] = map[string]bool{}
		}
		s.Blocks[a][b] = true
	}
	rows.Close()
	rows, err = p.pool.Query(ctx, `SELECT id,kind,title,avatar_url,current_seq,last_message_seq,created_at,updated_at FROM im_conversations`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c := &model.Conversation{}
		if err = rows.Scan(&c.ID, &c.Type, &c.Title, &c.AvatarURL, &c.Seq, &c.LastMessageSeq, &c.CreatedAt, &c.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		s.Conversations[c.ID] = c
	}
	rows.Close()
	rows, err = p.pool.Query(ctx, `SELECT pair_key,conversation_id FROM im_direct_index`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, v string
		if err = rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, err
		}
		s.DirectIndex[k] = v
	}
	rows.Close()
	rows, err = p.pool.Query(ctx, `SELECT conversation_id,user_id,role,last_read_seq,last_delivered_seq,muted_until,pinned,archived,notifications_muted,manual_unread,hidden_until_seq,group_nickname,joined_at FROM im_members`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		m := &model.ConversationMember{}
		if err = rows.Scan(&m.ConversationID, &m.UserID, &m.Role, &m.LastReadSeq, &m.LastDeliveredSeq, &m.MutedUntil, &m.Pinned, &m.Archived, &m.NotificationsMuted, &m.ManualUnread, &m.HiddenUntilSeq, &m.GroupNickname, &m.JoinedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if s.Members[m.ConversationID] == nil {
			s.Members[m.ConversationID] = map[string]*model.ConversationMember{}
		}
		s.Members[m.ConversationID][m.UserID] = m
	}
	rows.Close()
	rows, err = p.pool.Query(ctx, `SELECT id,reporter_id,target_type,target_id,reason,details,status,resolution,created_at,updated_at FROM im_reports`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		r := &model.Report{}
		if err = rows.Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID, &r.Reason, &r.Details, &r.Status, &r.Resolution, &r.CreatedAt, &r.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		s.Reports[r.ID] = r
	}
	rows.Close()
	rows, err = p.pool.Query(ctx, `SELECT id,actor_id,action,target_type,target_id,metadata,created_at FROM im_audits ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		a := &model.AuditEntry{}
		var metadata []byte
		if err = rows.Scan(&a.ID, &a.ActorID, &a.Action, &a.TargetType, &a.TargetID, &metadata, &a.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal(metadata, &a.Metadata)
		s.Audits = append(s.Audits, a)
	}
	rows.Close()
	rows, err = p.pool.Query(ctx, `SELECT id,value FROM im_sensitive_words`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, v string
		_ = rows.Scan(&k, &v)
		s.SensitiveWords[k] = v
	}
	rows.Close()
	rows, err = p.pool.Query(ctx, `SELECT key,value FROM im_settings`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		var v []byte
		_ = rows.Scan(&k, &v)
		var x any
		_ = json.Unmarshal(v, &x)
		s.Settings[k] = x
	}
	rows.Close()
	rows, err = p.pool.Query(ctx, `SELECT id,user_id,platform,provider,push_token,notifications_enabled,preview_enabled,sound_enabled,vibration_enabled,updated_at FROM im_devices`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		d := &model.Device{}
		if err = rows.Scan(&d.ID, &d.UserID, &d.Platform, &d.Provider, &d.PushToken, &d.NotificationsEnabled, &d.PreviewEnabled, &d.SoundEnabled, &d.VibrationEnabled, &d.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		s.Devices[d.ID] = d
	}
	rows.Close()
	rows, err = p.pool.Query(ctx, `SELECT id,owner_id,object_key,mime,size,status,checksum FROM im_media`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		m := &model.Media{}
		if err = rows.Scan(&m.ID, &m.OwnerID, &m.ObjectKey, &m.MIME, &m.Size, &m.Status, &m.Checksum); err != nil {
			rows.Close()
			return nil, err
		}
		s.Media[m.ID] = m
	}
	rows.Close()
	return s, nil
}

func (p *Postgres) Save(ctx context.Context, s *model.State) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(741852963)`); err != nil {
		return err
	}
	var revision int64
	if err = tx.QueryRow(ctx, `SELECT revision FROM im_state_meta WHERE singleton=true FOR UPDATE`).Scan(&revision); err != nil {
		return err
	}
	if revision != s.Revision {
		return ErrConflict
	}
	for _, u := range s.Users {
		_, err = tx.Exec(ctx, `INSERT INTO im_users(id,phone,name,handle,handle_change_count,signature,avatar_media_id,avatar_url,banned,created_at,updated_at) VALUES($1,$2,$3,COALESCE(NULLIF($4,''),'ll_'||right(md5($1),20)),$5,$6,NULLIF($7,''),$8,$9,$10,now()) ON CONFLICT(id) DO UPDATE SET phone=excluded.phone,name=excluded.name,handle=excluded.handle,handle_change_count=excluded.handle_change_count,signature=excluded.signature,avatar_media_id=excluded.avatar_media_id,avatar_url=excluded.avatar_url,banned=excluded.banned,updated_at=now()`, u.ID, u.Phone, u.Name, u.Handle, u.HandleChangeCount, u.Signature, u.AvatarMediaID, u.AvatarURL, u.Banned, u.CreatedAt)
		if err != nil {
			return err
		}
	}
	for _, r := range s.FriendRequests {
		_, err = tx.Exec(ctx, `INSERT INTO im_friend_requests(id,from_user_id,to_user_id,message,source,source_id,status,created_at,expires_at,updated_at,resolved_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(id) DO UPDATE SET status=excluded.status,message=excluded.message,source=excluded.source,source_id=excluded.source_id,expires_at=excluded.expires_at,updated_at=excluded.updated_at,resolved_at=excluded.resolved_at`, r.ID, r.FromUserID, r.ToUserID, r.Message, r.Source, r.SourceID, r.Status, r.CreatedAt, r.ExpiresAt, r.UpdatedAt, r.ResolvedAt)
		if err != nil {
			return err
		}
	}
	for uid, xs := range s.Friends {
		for other := range xs {
			_, err = tx.Exec(ctx, `INSERT INTO im_friendships(user_id,friend_user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, uid, other)
			if err != nil {
				return err
			}
		}
	}
	for uid, xs := range s.Blocks {
		for other := range xs {
			_, err = tx.Exec(ctx, `INSERT INTO im_blocks(user_id,blocked_user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, uid, other)
			if err != nil {
				return err
			}
		}
	}
	for _, c := range s.Conversations {
		_, err = tx.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,avatar_url,current_seq,last_message_seq,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO UPDATE SET title=excluded.title,avatar_url=excluded.avatar_url,current_seq=GREATEST(im_conversations.current_seq,excluded.current_seq),last_message_seq=GREATEST(im_conversations.last_message_seq,excluded.last_message_seq),updated_at=GREATEST(im_conversations.updated_at,excluded.updated_at)`, c.ID, c.Type, c.Title, c.AvatarURL, c.Seq, c.LastMessageSeq, c.CreatedAt, c.UpdatedAt)
		if err != nil {
			return err
		}
	}
	for k, cid := range s.DirectIndex {
		_, err = tx.Exec(ctx, `INSERT INTO im_direct_index(pair_key,conversation_id) VALUES($1,$2) ON CONFLICT(pair_key) DO NOTHING`, k, cid)
		if err != nil {
			return err
		}
	}
	for cid, xs := range s.Members {
		for _, m := range xs {
			_, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,last_read_seq,last_delivered_seq,muted_until,pinned,archived,notifications_muted,manual_unread,hidden_until_seq,group_nickname,joined_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(conversation_id,user_id) DO UPDATE SET role=excluded.role,last_read_seq=GREATEST(im_members.last_read_seq,excluded.last_read_seq),last_delivered_seq=GREATEST(im_members.last_delivered_seq,excluded.last_delivered_seq),muted_until=excluded.muted_until,pinned=excluded.pinned,archived=excluded.archived,notifications_muted=excluded.notifications_muted,manual_unread=excluded.manual_unread,hidden_until_seq=excluded.hidden_until_seq,group_nickname=excluded.group_nickname`, cid, m.UserID, m.Role, m.LastReadSeq, m.LastDeliveredSeq, m.MutedUntil, m.Pinned, m.Archived, m.NotificationsMuted, m.ManualUnread, m.HiddenUntilSeq, m.GroupNickname, m.JoinedAt)
			if err != nil {
				return err
			}
		}
	}
	for _, r := range s.Reports {
		_, err = tx.Exec(ctx, `INSERT INTO im_reports(id,reporter_id,target_type,target_id,reason,details,status,resolution,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(id) DO UPDATE SET status=excluded.status,resolution=excluded.resolution,updated_at=excluded.updated_at`, r.ID, r.ReporterID, r.TargetType, r.TargetID, r.Reason, r.Details, r.Status, r.Resolution, r.CreatedAt, r.UpdatedAt)
		if err != nil {
			return err
		}
	}
	for _, a := range s.Audits {
		raw, _ := json.Marshal(a.Metadata)
		_, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, a.ID, a.ActorID, a.Action, a.TargetType, a.TargetID, raw, a.CreatedAt)
		if err != nil {
			return err
		}
	}
	for k, v := range s.SensitiveWords {
		_, err = tx.Exec(ctx, `INSERT INTO im_sensitive_words(id,value) VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET value=excluded.value`, k, v)
		if err != nil {
			return err
		}
	}
	for k, v := range s.Settings {
		raw, _ := json.Marshal(v)
		_, err = tx.Exec(ctx, `INSERT INTO im_settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, raw)
		if err != nil {
			return err
		}
	}
	for _, d := range s.Devices {
		_, err = tx.Exec(ctx, `INSERT INTO im_devices(id,user_id,platform,provider,push_token,notifications_enabled,preview_enabled,sound_enabled,vibration_enabled,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(id) DO UPDATE SET platform=excluded.platform,provider=excluded.provider,push_token=excluded.push_token,notifications_enabled=excluded.notifications_enabled,preview_enabled=excluded.preview_enabled,sound_enabled=excluded.sound_enabled,vibration_enabled=excluded.vibration_enabled,updated_at=excluded.updated_at`, d.ID, d.UserID, d.Platform, d.Provider, d.PushToken, d.NotificationsEnabled, d.PreviewEnabled, d.SoundEnabled, d.VibrationEnabled, d.UpdatedAt)
		if err != nil {
			return err
		}
	}
	for _, m := range s.Media {
		_, err = tx.Exec(ctx, `INSERT INTO im_media(id,owner_id,object_key,mime,size,status,checksum) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO UPDATE SET size=excluded.size,status=excluded.status,checksum=excluded.checksum`, m.ID, m.OwnerID, m.ObjectKey, m.MIME, m.Size, m.Status, m.Checksum)
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE im_state_meta SET revision=revision+1 WHERE singleton=true`); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.Revision++
	return nil
}

func (p *Postgres) GetUser(ctx context.Context, id string) (*model.User, error) {
	u := &model.User{}
	err := p.pool.QueryRow(ctx, `SELECT id,phone,name,COALESCE(handle,''),handle_change_count,signature,COALESCE(avatar_media_id,''),avatar_url,banned,created_at FROM im_users WHERE id=$1`, id).Scan(&u.ID, &u.Phone, &u.Name, &u.Handle, &u.HandleChangeCount, &u.Signature, &u.AvatarMediaID, &u.AvatarURL, &u.Banned, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (p *Postgres) LoginOrCreateUser(ctx context.Context, phone, name, id string, created time.Time) (*model.User, error) {
	u := &model.User{}
	err := p.pool.QueryRow(ctx, `INSERT INTO im_users(id,phone,name,handle,created_at) VALUES($1,$2,$3,'ll_'||right($1,20),$4) ON CONFLICT(phone) DO UPDATE SET phone=excluded.phone RETURNING id,phone,name,COALESCE(handle,''),handle_change_count,signature,COALESCE(avatar_media_id,''),avatar_url,banned,created_at`, id, phone, name, created).Scan(&u.ID, &u.Phone, &u.Name, &u.Handle, &u.HandleChangeCount, &u.Signature, &u.AvatarMediaID, &u.AvatarURL, &u.Banned, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	if u.Banned {
		return nil, ErrForbidden
	}
	return u, nil
}
func (p *Postgres) RegisterPasswordUser(ctx context.Context, phone, name, id, hash string, created time.Time) (*model.User, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	u := &model.User{}
	err = tx.QueryRow(ctx, `INSERT INTO im_users(id,phone,name,handle,password_hash,password_updated_at,created_at) VALUES($1,$2,$3,'ll_'||right($1,20),$4,$5,$5)
		RETURNING id,phone,name,COALESCE(handle,''),handle_change_count,signature,COALESCE(avatar_media_id,''),avatar_url,banned,created_at`, id, phone, name, hash, created).Scan(&u.ID, &u.Phone, &u.Name, &u.Handle, &u.HandleChangeCount, &u.Signature, &u.AvatarMediaID, &u.AvatarURL, &u.Banned, &u.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(map[string]any{"method": "phone_password"})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'auth.register','user',$2,$3,$4)`, "aud_register_"+id, id, metadata, created); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return u, nil
}

func (p *Postgres) PasswordCredentials(ctx context.Context, phone string) (*model.User, string, error) {
	u := &model.User{}
	var hash string
	err := p.pool.QueryRow(ctx, `SELECT id,phone,name,COALESCE(handle,''),handle_change_count,signature,COALESCE(avatar_media_id,''),avatar_url,banned,created_at,password_hash FROM im_users WHERE phone=$1`, phone).Scan(&u.ID, &u.Phone, &u.Name, &u.Handle, &u.HandleChangeCount, &u.Signature, &u.AvatarMediaID, &u.AvatarURL, &u.Banned, &u.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	return u, hash, err
}

func (p *Postgres) UpdatePassword(ctx context.Context, phone, hash string, updated time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var uid string
	if err = tx.QueryRow(ctx, `UPDATE im_users SET password_hash=$2,password_updated_at=$3,updated_at=$3 WHERE phone=$1 RETURNING id`, phone, hash, updated).Scan(&uid); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{"method": "otp"})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'auth.password_reset','user',$2,$3,$4)`, "aud_reset_"+strconv.FormatInt(updated.UnixNano(), 36), uid, metadata, updated); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) CreateRefreshSession(ctx context.Context, id, uid string, hash []byte, exp time.Time) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO im_refresh_sessions(id,user_id,token_hash,expires_at) VALUES($1,$2,$3,$4)`, id, uid, hash, exp)
	return err
}
func (p *Postgres) RotateRefreshSession(ctx context.Context, oldID, newID string, hash []byte, exp time.Time, uid string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE im_refresh_sessions SET revoked_at=now(),replaced_by=$2 WHERE id=$1 AND user_id=$3 AND revoked_at IS NULL AND expires_at>now()`, oldID, newID, uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrForbidden
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_refresh_sessions(id,user_id,token_hash,expires_at) VALUES($1,$2,$3,$4)`, newID, uid, hash, exp); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) RevokeRefreshSession(ctx context.Context, id, uid string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE im_refresh_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1 AND user_id=$2`, id, uid)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
func (p *Postgres) RevokeUserRefreshSessions(ctx context.Context, uid string) error {
	_, err := p.pool.Exec(ctx, `UPDATE im_refresh_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1 AND revoked_at IS NULL`, uid)
	return err
}

func (p *Postgres) AccountDeleted(ctx context.Context, uid string) (bool, error) {
	var deleted bool
	err := p.pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM im_users WHERE id=$1`, uid).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return deleted, err
}

func (p *Postgres) DeleteAccount(ctx context.Context, uid string, at time.Time) (bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var deletedAt *time.Time
	if err = tx.QueryRow(ctx, `SELECT deleted_at FROM im_users WHERE id=$1 FOR UPDATE`, uid).Scan(&deletedAt); errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	} else if err != nil {
		return false, err
	}
	if deletedAt != nil {
		return true, tx.Commit(ctx)
	}
	var ownsActiveGroup bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_groups WHERE owner_id=$1 AND dissolved_at IS NULL)`, uid).Scan(&ownsActiveGroup); err != nil {
		return false, err
	}
	if ownsActiveGroup {
		return false, ErrConflict
	}
	friendRows, err := tx.Query(ctx, `SELECT friend_user_id FROM im_friendships WHERE user_id=$1 ORDER BY friend_user_id`, uid)
	if err != nil {
		return false, err
	}
	friendIDs := []string{}
	for friendRows.Next() {
		var friendID string
		if err = friendRows.Scan(&friendID); err != nil {
			friendRows.Close()
			return false, err
		}
		friendIDs = append(friendIDs, friendID)
	}
	if err = friendRows.Err(); err != nil {
		friendRows.Close()
		return false, err
	}
	friendRows.Close()
	if _, err = tx.Exec(ctx, `UPDATE im_refresh_sessions SET revoked_at=COALESCE(revoked_at,$2) WHERE user_id=$1 AND revoked_at IS NULL`, uid, at); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM im_devices WHERE user_id=$1`, uid); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_friend_requests SET status='cancelled',resolved_at=COALESCE(resolved_at,$2),updated_at=$2 WHERE (from_user_id=$1 OR to_user_id=$1) AND status='pending'`, uid, at); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_group_invites SET status='cancelled',resolved_at=COALESCE(resolved_at,$2),updated_at=$2 WHERE (inviter_id=$1 OR invitee_id=$1) AND status='pending'`, uid, at); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM im_friendships WHERE user_id=$1 OR friend_user_id=$1`, uid); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM im_blocks WHERE user_id=$1 OR blocked_user_id=$1`, uid); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM im_members WHERE user_id=$1`, uid); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM im_favorites WHERE user_id=$1`, uid); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_feedback SET contact='' WHERE user_id=$1`, uid); err != nil {
		return false, err
	}
	var wasSystemUser bool
	if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT enabled FROM im_wukong_system_users WHERE user_id=$1),false)`, uid).Scan(&wasSystemUser); err != nil {
		return false, err
	}
	if wasSystemUser {
		if _, err = tx.Exec(ctx, `UPDATE im_wukong_system_users SET enabled=false,updated_by=$1,reason='account deleted',updated_at=$2 WHERE user_id=$1`, uid, at); err != nil {
			return false, err
		}
		if err = enqueueWukongOutbox(ctx, tx, fmt.Sprintf("system-user-delete:%s:%d", uid, at.UnixNano()), wukong.OperationSystemUIDRemove, "system_user", uid, map[string]any{"uids": []string{uid}}); err != nil {
			return false, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE im_users SET
		phone='deleted_'||md5(id),handle='deleted_'||left(md5(id),16),name='已注销用户',signature='',avatar_media_id=NULL,avatar_url='',
		password_hash='',password_updated_at=$2,handle_change_count=2,banned=true,deleted_at=$2,updated_at=$2
		WHERE id=$1`, uid, at); err != nil {
		return false, err
	}
	for _, friendID := range friendIDs {
		payload, _ := json.Marshal(map[string]any{"userId": uid})
		if err = appendUserBusinessEvent(ctx, tx, friendID, "friend.account_deleted", payload, at); err != nil {
			return false, err
		}
	}
	metadata, _ := json.Marshal(map[string]any{"mode": "immediate_anonymization", "friendCount": len(friendIDs)})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'account.deleted','user',$2,$3,$4)`, "aud_account_delete_"+strconv.FormatInt(at.UnixNano(), 36), uid, metadata, at); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}

func (p *Postgres) SearchUsers(ctx context.Context, query string, limit int) ([]*model.User, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	q := "%" + query + "%"
	rows, err := p.pool.Query(ctx, `SELECT id,'',name,avatar_url,banned,created_at FROM im_users WHERE $1='' OR name ILIKE $2 OR phone ILIKE $2 ORDER BY lower(name),id LIMIT $3`, query, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*model.User, 0, limit)
	for rows.Next() {
		u := &model.User{}
		if err = rows.Scan(&u.ID, &u.Phone, &u.Name, &u.AvatarURL, &u.Banned, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (p *Postgres) SearchUsersByIdentifier(ctx context.Context, query, by string, limit int) ([]*model.User, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return []*model.User{}, nil
	}
	column := "lower(handle)"
	if by == "phone" {
		column = "phone"
	} else if by != "handle" {
		return nil, ErrForbidden
	}
	rows, err := p.pool.Query(ctx, `SELECT id,'',name,COALESCE(handle,''),avatar_url,banned,created_at FROM im_users WHERE `+column+`=$1 ORDER BY id LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*model.User, 0, limit)
	for rows.Next() {
		u := &model.User{}
		if err = rows.Scan(&u.ID, &u.Phone, &u.Name, &u.Handle, &u.AvatarURL, &u.Banned, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (p *Postgres) ListFriends(ctx context.Context, uid string) ([]*model.User, error) {
	rows, err := p.pool.Query(ctx, `SELECT u.id,'',u.name,u.avatar_url,u.banned,f.remark,f.tags,u.created_at FROM im_friendships f JOIN im_users u ON u.id=f.friend_user_id WHERE f.user_id=$1 ORDER BY lower(COALESCE(NULLIF(f.remark,''),u.name)),u.id LIMIT 500`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u := &model.User{}
		if err = rows.Scan(&u.ID, &u.Phone, &u.Name, &u.AvatarURL, &u.Banned, &u.Remark, &u.Tags, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (p *Postgres) ListBlockedUsers(ctx context.Context, uid string) ([]*model.User, error) {
	rows, err := p.pool.Query(ctx, `SELECT u.id,u.phone,u.name,u.handle,u.handle_changes,u.signature,u.avatar_url,u.banned,u.created_at
		FROM im_blocks b JOIN im_users u ON u.id=b.blocked_user_id
		WHERE b.user_id=$1 ORDER BY b.created_at DESC,u.id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*model.User
	for rows.Next() {
		u := &model.User{}
		if err = rows.Scan(&u.ID, &u.Phone, &u.Name, &u.Handle, &u.HandleChangeCount, &u.Signature, &u.AvatarURL, &u.Banned, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.Phone = ""
		users = append(users, u)
	}
	return users, rows.Err()
}

func (p *Postgres) ListFriendRequests(ctx context.Context, uid string) ([]*model.FriendRequest, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,from_user_id,to_user_id,message,source,source_id,status,created_at,expires_at,updated_at,resolved_at FROM im_friend_requests WHERE from_user_id=$1 OR to_user_id=$1 ORDER BY created_at DESC LIMIT 500`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.FriendRequest
	for rows.Next() {
		r := &model.FriendRequest{}
		if err = rows.Scan(&r.ID, &r.FromUserID, &r.ToUserID, &r.Message, &r.Source, &r.SourceID, &r.Status, &r.CreatedAt, &r.ExpiresAt, &r.UpdatedAt, &r.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) ListGroupInvites(ctx context.Context, uid, status string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := p.pool.Query(ctx, `SELECT `+groupInviteColumns("gi.")+`,c.title,u.id,u.name,COALESCE(u.handle,''),u.avatar_url
		FROM im_group_invites gi
		JOIN im_conversations c ON c.id=gi.conversation_id
		JOIN im_users u ON u.id=gi.inviter_id
		WHERE (gi.invitee_id=$1 OR gi.inviter_id=$1) AND ($2='' OR gi.status=$2)
		ORDER BY CASE WHEN gi.status='pending' THEN 0 ELSE 1 END,gi.updated_at DESC,gi.id
		LIMIT $3`, uid, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit)
	for rows.Next() {
		invite := &model.GroupInvite{}
		inviter := &model.User{}
		var groupName string
		if err = rows.Scan(&invite.ID, &invite.ConversationID, &invite.InviterID, &invite.InviteeID, &invite.Source, &invite.Status, &invite.CreatedAt, &invite.ExpiresAt, &invite.UpdatedAt, &invite.ResolvedAt, &groupName, &inviter.ID, &inviter.Name, &inviter.Handle, &inviter.AvatarURL); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"invite": invite, "groupName": groupName, "inviter": inviter, "outgoing": invite.InviterID == uid})
	}
	return items, rows.Err()
}

func pageOffset(cursor string, limit int) (int, int) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		offset = 0
	}
	return offset, limit
}

func nextPageCursor(offset, count int, total int64) string {
	if int64(offset+count) >= total {
		return ""
	}
	return strconv.Itoa(offset + count)
}

func (p *Postgres) ListAdminUsers(ctx context.Context, q, status, cursor string, limit int) ([]*model.User, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + q + "%"
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_users WHERE ($1='' OR name ILIKE $2 OR phone ILIKE $2 OR id ILIKE $2 OR COALESCE(handle,'') ILIKE $2)
		AND ($3='' OR ($3='active' AND NOT (banned AND (banned_until IS NULL OR banned_until>now()))) OR ($3='banned' AND banned AND (banned_until IS NULL OR banned_until>now())))`, q, pattern, status).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, `SELECT id,phone,name,COALESCE(handle,''),handle_change_count,avatar_url,(banned AND (banned_until IS NULL OR banned_until>now())),banned_until,created_at FROM im_users
		WHERE ($1='' OR name ILIKE $2 OR phone ILIKE $2 OR id ILIKE $2 OR COALESCE(handle,'') ILIKE $2)
		AND ($3='' OR ($3='active' AND NOT (banned AND (banned_until IS NULL OR banned_until>now()))) OR ($3='banned' AND banned AND (banned_until IS NULL OR banned_until>now())))
		ORDER BY created_at DESC,id LIMIT $4 OFFSET $5`, q, pattern, status, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	var items []*model.User
	for rows.Next() {
		u := &model.User{}
		if err = rows.Scan(&u.ID, &u.Phone, &u.Name, &u.Handle, &u.HandleChangeCount, &u.AvatarURL, &u.Banned, &u.BannedUntil, &u.CreatedAt); err != nil {
			return nil, 0, "", err
		}
		items = append(items, u)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func (p *Postgres) ListAdminReports(ctx context.Context, q, status, cursor string, limit int) ([]*model.Report, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + q + "%"
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_reports WHERE ($1='' OR status=$1) AND
		($2='' OR id ILIKE $3 OR reporter_id ILIKE $3 OR target_id ILIKE $3 OR reason ILIKE $3 OR details ILIKE $3)`, status, q, pattern).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, `SELECT id,reporter_id,target_type,target_id,reason,details,status,resolution,created_at,updated_at
		FROM im_reports WHERE ($1='' OR status=$1) AND
		($2='' OR id ILIKE $3 OR reporter_id ILIKE $3 OR target_id ILIKE $3 OR reason ILIKE $3 OR details ILIKE $3)
		ORDER BY created_at DESC,id LIMIT $4 OFFSET $5`, status, q, pattern, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	var items []*model.Report
	for rows.Next() {
		r := &model.Report{}
		if err = rows.Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID, &r.Reason, &r.Details, &r.Status, &r.Resolution, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, 0, "", err
		}
		items = append(items, r)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func (p *Postgres) ListAdminAudits(ctx context.Context, q, status, cursor string, limit int) ([]*model.AuditEntry, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + q + "%"
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE
		($1='' OR actor_id ILIKE $2 OR action ILIKE $2 OR target_type ILIKE $2 OR target_id ILIKE $2)
		AND ($3='' OR result=$3)`, q, pattern, status).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, `SELECT id,actor_id,action,target_type,target_id,metadata,result,ip,created_at FROM im_audits WHERE
		($1='' OR actor_id ILIKE $2 OR action ILIKE $2 OR target_type ILIKE $2 OR target_id ILIKE $2)
		AND ($3='' OR result=$3) ORDER BY created_at DESC,id LIMIT $4 OFFSET $5`, q, pattern, status, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	var items []*model.AuditEntry
	for rows.Next() {
		a := &model.AuditEntry{}
		var raw []byte
		if err = rows.Scan(&a.ID, &a.ActorID, &a.Action, &a.TargetType, &a.TargetID, &raw, &a.Result, &a.IP, &a.CreatedAt); err != nil {
			return nil, 0, "", err
		}
		if err = json.Unmarshal(raw, &a.Metadata); err != nil {
			return nil, 0, "", err
		}
		items = append(items, a)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

const adminMessageUnion = `
	SELECT message_index.message_id::text AS id,message_index.conversation_id,message_index.sender_id,
		message_index.client_msg_no AS client_msg_id,message_index.message_seq,
		CASE message_index.content_type
			WHEN 1 THEN 'text' WHEN 2 THEN 'image' WHEN 3 THEN 'image' WHEN 4 THEN 'audio'
			WHEN 5 THEN 'video' WHEN 6 THEN 'location' WHEN 7 THEN 'contact' WHEN 8 THEN 'file'
			WHEN 1001 THEN 'chat_history' WHEN 1002 THEN 'system' WHEN 1003 THEN 'sticker'
			WHEN 1004 THEN 'moment' WHEN 1005 THEN 'call' WHEN 1006 THEN 'live'
			WHEN 1007 THEN 'support' WHEN 1008 THEN 'screenshot' ELSE 'custom'
		END AS message_type,
		'{}'::jsonb AS body,NULL::text AS reply_to_id,
		NULLIF(message_extension.payload->>'recalledAt','')::timestamptz AS recalled_at,
		message_index.expires_at,
		COALESCE(message_index.expired_at,CASE WHEN message_index.expires_at<=now() THEN message_index.expires_at END) AS expired_at,
		NULLIF(message_extension.payload->>'editedAt','')::timestamptz AS edited_at,
		COALESCE(NULLIF(message_extension.payload->>'editVersion','')::integer,0) AS edit_version,
		message_index.message_timestamp AS created_at
	FROM im_wukong_message_index message_index
	LEFT JOIN im_wukong_message_extensions message_extension ON message_extension.message_id=message_index.message_id
		AND message_extension.channel_id=message_index.channel_id AND message_extension.channel_type=message_index.channel_type
`

func (p *Postgres) ListAdminMessages(ctx context.Context, q, messageType, cursor string, limit int) ([]*model.Message, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + q + "%"
	var total int64
	if err := p.pool.QueryRow(ctx, `WITH all_messages AS (`+adminMessageUnion+`) SELECT count(*) FROM all_messages WHERE
		($1='' OR id ILIKE $2 OR conversation_id ILIKE $2 OR sender_id ILIKE $2 OR client_msg_id ILIKE $2)
		AND ($3='' OR message_type=$3)`, q, pattern, messageType).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, `WITH all_messages AS (`+adminMessageUnion+`) SELECT id,conversation_id,sender_id,client_msg_id,message_seq,message_type,body,reply_to_id,recalled_at,expires_at,expired_at,edited_at,edit_version,created_at
		FROM all_messages WHERE
		($1='' OR id ILIKE $2 OR conversation_id ILIKE $2 OR sender_id ILIKE $2 OR client_msg_id ILIKE $2)
		AND ($3='' OR message_type=$3) ORDER BY created_at DESC,id LIMIT $4 OFFSET $5`, q, pattern, messageType, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]*model.Message, 0, limit)
	for rows.Next() {
		m := &model.Message{}
		var raw []byte
		var reply *string
		if err = rows.Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.ClientMsgID, &m.Seq, &m.Type, &raw, &reply, &m.RecalledAt, &m.ExpiresAt, &m.ExpiredAt, &m.EditedAt, &m.EditVersion, &m.CreatedAt); err != nil {
			return nil, 0, "", err
		}
		if reply != nil {
			m.ReplyToID = *reply
		}
		if err = json.Unmarshal(raw, &m.Body); err != nil {
			return nil, 0, "", err
		}
		items = append(items, m)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func (p *Postgres) ListAdminMedia(ctx context.Context, q, status, cursor string, limit int) ([]*model.Media, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + q + "%"
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_media WHERE
		($1='' OR id ILIKE $2 OR owner_id ILIKE $2 OR object_key ILIKE $2 OR mime ILIKE $2 OR checksum ILIKE $2)
		AND ($3='' OR status=$3)`, q, pattern, status).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, `SELECT id,owner_id,object_key,mime,status,checksum,size FROM im_media WHERE
		($1='' OR id ILIKE $2 OR owner_id ILIKE $2 OR object_key ILIKE $2 OR mime ILIKE $2 OR checksum ILIKE $2)
		AND ($3='' OR status=$3) ORDER BY created_at DESC,id LIMIT $4 OFFSET $5`, q, pattern, status, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]*model.Media, 0, limit)
	for rows.Next() {
		m := &model.Media{}
		if err = rows.Scan(&m.ID, &m.OwnerID, &m.ObjectKey, &m.MIME, &m.Status, &m.Checksum, &m.Size); err != nil {
			return nil, 0, "", err
		}
		items = append(items, m)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func (p *Postgres) ListConversations(ctx context.Context, uid string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := p.pool.Query(ctx, `SELECT c.id,c.kind,c.title,c.avatar_url,
		GREATEST(c.current_seq,COALESCE(wk.last_message_seq,0)),
		GREATEST(c.last_message_seq,COALESCE(wk.last_message_seq,0)),c.created_at,
		GREATEST(c.updated_at,COALESCE(wk.last_message_at,c.updated_at)),
		m.role,m.muted_until,m.last_read_seq,m.last_delivered_seq,m.pinned,m.archived,m.notifications_muted,m.manual_unread,m.hidden_until_seq,m.joined_at,
		COALESCE(mention_stats.unread_count,0),c.member_count
		FROM im_members m
		JOIN im_conversations c ON c.id=m.conversation_id
		LEFT JOIN LATERAL (
			SELECT max(message_seq) AS last_message_seq,max(message_timestamp) AS last_message_at
			FROM im_wukong_message_index WHERE conversation_id=c.id
		) wk ON true
		LEFT JOIN LATERAL (
			SELECT count(*)::bigint AS unread_count FROM im_wukong_reminders reminder
			WHERE reminder.user_id=m.user_id AND reminder.conversation_id=c.id
			  AND reminder.message_seq>m.last_read_seq AND reminder.done=false
		) mention_stats ON true
		WHERE m.user_id=$1 AND (m.hidden_until_seq IS NULL OR GREATEST(c.last_message_seq,COALESCE(wk.last_message_seq,0))>m.hidden_until_seq)
		ORDER BY m.pinned DESC,GREATEST(c.updated_at,COALESCE(wk.last_message_at,c.updated_at)) DESC,c.id LIMIT $2`, uid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0, limit)
	conversationIDs := make([]string, 0, limit)
	for rows.Next() {
		c := &model.Conversation{}
		m := &model.ConversationMember{UserID: uid}
		var mentionUnreadCount int64
		var memberCount int64
		if err = rows.Scan(&c.ID, &c.Type, &c.Title, &c.AvatarURL, &c.Seq, &c.LastMessageSeq, &c.CreatedAt, &c.UpdatedAt,
			&m.Role, &m.MutedUntil, &m.LastReadSeq, &m.LastDeliveredSeq, &m.Pinned, &m.Archived, &m.NotificationsMuted, &m.ManualUnread, &m.HiddenUntilSeq, &m.JoinedAt,
			&mentionUnreadCount, &memberCount); err != nil {
			return nil, err
		}
		m.ConversationID = c.ID
		out = append(out, map[string]any{"conversation": c, "membership": m, "lastMessage": nil, "unreadCount": max(int64(0), c.LastMessageSeq-m.LastReadSeq), "mentionUnreadCount": mentionUnreadCount, "memberCount": memberCount})
		conversationIDs = append(conversationIDs, c.ID)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if len(conversationIDs) == 0 {
		return out, nil
	}
	membersByConversation := make(map[string][]*model.ConversationMember, len(conversationIDs))
	memberRows, err := p.pool.Query(ctx, `SELECT preview.conversation_id,preview.user_id,preview.name,preview.handle,preview.avatar_url,preview.role,preview.muted_until,preview.last_read_seq,preview.last_delivered_seq,preview.group_nickname,preview.joined_at
		FROM unnest($1::text[]) WITH ORDINALITY requested(conversation_id,position)
		JOIN LATERAL (
			SELECT m.conversation_id,m.user_id,u.name,COALESCE(u.handle,'') AS handle,u.avatar_url,m.role,m.muted_until,m.last_read_seq,m.last_delivered_seq,m.group_nickname,m.joined_at
			FROM im_members m JOIN im_users u ON u.id=m.user_id
			WHERE m.conversation_id=requested.conversation_id
			ORDER BY CASE WHEN m.user_id=$2 THEN 0 WHEN m.role='owner' THEN 1 ELSE 2 END,m.joined_at,m.user_id LIMIT 8
		) preview ON true
		ORDER BY requested.position,CASE WHEN preview.user_id=$2 THEN 0 WHEN preview.role='owner' THEN 1 ELSE 2 END,preview.joined_at,preview.user_id`, conversationIDs, uid)
	if err != nil {
		return nil, err
	}
	defer memberRows.Close()
	for memberRows.Next() {
		member := &model.ConversationMember{}
		if err = memberRows.Scan(&member.ConversationID, &member.UserID, &member.Name, &member.Handle, &member.AvatarURL, &member.Role, &member.MutedUntil, &member.LastReadSeq, &member.LastDeliveredSeq, &member.GroupNickname, &member.JoinedAt); err != nil {
			return nil, err
		}
		member.ID = member.UserID
		membersByConversation[member.ConversationID] = append(membersByConversation[member.ConversationID], member)
	}
	if err = memberRows.Err(); err != nil {
		return nil, err
	}
	for _, item := range out {
		conversation := item["conversation"].(*model.Conversation)
		item["members"] = membersByConversation[conversation.ID]
	}
	return out, nil
}

func (p *Postgres) CanAccessConversation(ctx context.Context, uid, cid string) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_members WHERE conversation_id=$1 AND user_id=$2)`, cid, uid).Scan(&ok)
	return ok, err
}

func (p *Postgres) ConversationMemberIDs(ctx context.Context, cid string) ([]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT user_id FROM im_members WHERE conversation_id=$1 ORDER BY user_id LIMIT 501`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var uid string
		if err = rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

func (p *Postgres) ConversationKind(ctx context.Context, cid string) (string, error) {
	var kind string
	err := p.pool.QueryRow(ctx, `SELECT kind FROM im_conversations WHERE id=$1`, cid).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return kind, err
}

func (p *Postgres) ListConversationMembers(ctx context.Context, uid, cid string) ([]*model.ConversationMember, error) {
	var out []*model.ConversationMember
	cursor := ""
	for {
		page, next, err := p.ListConversationMembersPage(ctx, uid, cid, cursor, 200)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if next == "" || len(out) >= 5000 {
			return out, nil
		}
		cursor = next
	}
}

func (p *Postgres) ListConversationMembersPage(ctx context.Context, uid, cid, cursor string, limit int) ([]*model.ConversationMember, string, error) {
	ok, err := p.CanAccessConversation(ctx, uid, cid)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", ErrForbidden
	}
	offset, limit := pageOffset(cursor, limit)
	rows, err := p.pool.Query(ctx, `SELECT m.conversation_id,m.user_id,u.name,COALESCE(u.handle,''),u.avatar_url,m.role,m.muted_until,m.last_read_seq,m.last_delivered_seq,m.group_nickname,m.joined_at
		FROM im_members m JOIN im_users u ON u.id=m.user_id WHERE m.conversation_id=$1 ORDER BY m.joined_at,m.user_id LIMIT $2 OFFSET $3`, cid, limit+1, offset)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*model.ConversationMember
	for rows.Next() {
		m := &model.ConversationMember{}
		if err = rows.Scan(&m.ConversationID, &m.UserID, &m.Name, &m.Handle, &m.AvatarURL, &m.Role, &m.MutedUntil, &m.LastReadSeq, &m.LastDeliveredSeq, &m.GroupNickname, &m.JoinedAt); err != nil {
			return nil, "", err
		}
		m.ID = m.UserID
		out = append(out, m)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = strconv.Itoa(offset + limit)
	}
	return out, next, nil
}

func (p *Postgres) CleanupRuntimeData(ctx context.Context, policy RetentionPolicy, batch int) (int64, error) {
	if batch <= 0 || batch > 10000 {
		batch = 1000
	}
	if policy.Outbox <= 0 {
		policy.Outbox = 7 * 24 * time.Hour
	}
	type cleanup struct {
		query  string
		cutoff time.Time
	}
	now := time.Now()
	jobs := []cleanup{
		{`WITH expired AS (SELECT id FROM im_push_outbox WHERE status IN ('sent','failed') AND COALESCE(sent_at,available_at)<$1 ORDER BY id LIMIT $2) DELETE FROM im_push_outbox o USING expired x WHERE o.id=x.id`, now.Add(-policy.Outbox)},
		{`WITH expired AS (SELECT id FROM im_wukong_outbox WHERE status IN ('completed','failed') AND completed_at<$1 ORDER BY id LIMIT $2) DELETE FROM im_wukong_outbox o USING expired x WHERE o.id=x.id`, now.Add(-policy.Outbox)},
		{`WITH expired AS (SELECT id FROM im_wukong_webhook_events WHERE status IN ('completed','failed') AND completed_at<$1 ORDER BY id LIMIT $2) DELETE FROM im_wukong_webhook_events e USING expired x WHERE e.id=x.id`, now.Add(-policy.Outbox)},
	}
	var removed int64
	for _, job := range jobs {
		result, err := p.pool.Exec(ctx, job.query, job.cutoff, batch)
		if err != nil {
			return removed, err
		}
		removed += result.RowsAffected()
	}
	return removed, nil
}

func (p *Postgres) RuntimeStats(ctx context.Context) (RuntimeStats, error) {
	poolStats := p.pool.Stat()
	stats := RuntimeStats{
		DBMaxConnections: poolStats.MaxConns(), DBTotalConnections: poolStats.TotalConns(),
		DBIdleConnections: poolStats.IdleConns(), DBAcquiredConnections: poolStats.AcquiredConns(),
		DBAcquireCount: poolStats.AcquireCount(), DBEmptyAcquireCount: poolStats.EmptyAcquireCount(),
		DBCanceledAcquireCount: poolStats.CanceledAcquireCount(), DBAcquireDurationSeconds: poolStats.AcquireDuration().Seconds(),
	}
	err := p.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM im_push_outbox WHERE status IN ('pending','processing')),
		COALESCE((SELECT EXTRACT(EPOCH FROM now()-min(created_at)) FROM im_push_outbox WHERE status IN ('pending','processing')),0),
		(SELECT count(*) FROM im_wukong_outbox WHERE status IN ('pending','processing')),
		COALESCE((SELECT EXTRACT(EPOCH FROM now()-min(created_at)) FROM im_wukong_outbox WHERE status IN ('pending','processing')),0),
		(SELECT count(*) FROM im_wukong_outbox WHERE status='failed'),
		(SELECT count(*) FROM im_wukong_webhook_events WHERE status IN ('pending','processing')),
		COALESCE((SELECT EXTRACT(EPOCH FROM now()-min(received_at)) FROM im_wukong_webhook_events WHERE status IN ('pending','processing')),0),
		(SELECT count(*) FROM im_wukong_webhook_events WHERE status='failed')`).Scan(
		&stats.PushPending, &stats.OldestPushSeconds,
		&stats.WukongOutboxPending, &stats.OldestWukongOutboxSeconds, &stats.WukongOutboxFailed,
		&stats.WukongWebhookPending, &stats.OldestWukongWebhookSeconds, &stats.WukongWebhookFailed)
	return stats, err
}

func (p *Postgres) RegisterDevice(ctx context.Context, uid string, d Device) error {
	tag, err := p.pool.Exec(ctx, `INSERT INTO im_devices(id,user_id,platform,provider,push_token,notifications_enabled,preview_enabled,sound_enabled,vibration_enabled,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,now()) ON CONFLICT(id) DO UPDATE SET platform=excluded.platform,provider=excluded.provider,push_token=excluded.push_token,notifications_enabled=excluded.notifications_enabled,preview_enabled=excluded.preview_enabled,sound_enabled=excluded.sound_enabled,vibration_enabled=excluded.vibration_enabled,updated_at=now() WHERE im_devices.user_id=excluded.user_id`, d.ID, uid, d.Platform, d.Provider, d.PushToken, d.NotificationsEnabled, d.PreviewEnabled, d.SoundEnabled, d.VibrationEnabled)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrForbidden
	}
	return err
}
func (p *Postgres) UnregisterDevice(ctx context.Context, uid, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM im_devices WHERE id=$1 AND user_id=$2`, id, uid)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
func (p *Postgres) CreateMedia(ctx context.Context, m Media) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO im_media(id,owner_id,object_key,mime,size,status) VALUES($1,$2,$3,$4,$5,$6)`, m.ID, m.OwnerID, m.ObjectKey, m.MIME, m.Size, m.Status)
	return err
}
func (p *Postgres) CompleteMedia(ctx context.Context, id, uid string, size int64, sum string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE im_media SET status='ready',size=$3,checksum=$4,completed_at=now() WHERE id=$1 AND owner_id=$2 AND status='pending'`, id, uid, size, sum)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
func (p *Postgres) GetMedia(ctx context.Context, id string) (Media, error) {
	var m Media
	err := p.pool.QueryRow(ctx, `SELECT id,owner_id,object_key,mime,size,status,checksum FROM im_media WHERE id=$1`, id).Scan(&m.ID, &m.OwnerID, &m.ObjectKey, &m.MIME, &m.Size, &m.Status, &m.Checksum)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func (p *Postgres) CanAccessMedia(ctx context.Context, uid, id string) (bool, error) {
	var allowed bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM im_media media WHERE media.id=$2 AND (
			media.owner_id=$1 OR EXISTS(
				SELECT 1 FROM im_wukong_media_channels binding
				WHERE binding.media_id=$2 AND (
					(binding.channel_type=1 AND (binding.sender_id=$1 OR binding.channel_id=$1)) OR
					(binding.channel_type=2 AND EXISTS(
						SELECT 1 FROM im_members member
						WHERE member.conversation_id=binding.channel_id AND member.user_id=$1
					))
				)
			) OR EXISTS(
				SELECT 1 FROM im_moments moment WHERE $2=ANY(moment.media_ids) AND moment.status<>'deleted' AND (
					moment.author_id=$1 OR (moment.status='published' AND (
						moment.visibility='public' OR
						(moment.visibility IN ('friends','excluded') AND EXISTS(
							SELECT 1 FROM im_friendships friendship
							WHERE friendship.user_id=$1 AND friendship.friend_user_id=moment.author_id)
							AND (moment.visibility<>'excluded' OR NOT ($1=ANY(moment.visible_user_ids)))) OR
						(moment.visibility='selected' AND $1=ANY(moment.visible_user_ids))
					))
				)
			) OR EXISTS(
				SELECT 1 FROM im_sticker_packs pack WHERE pack.status='published' AND (
					pack.cover_media_id=$2 OR EXISTS(
						SELECT 1 FROM im_sticker_items item
						WHERE item.pack_id=pack.id AND item.media_id=$2 AND item.status='published'
					)
				)
			)
		)
	)`, uid, id).Scan(&allowed)
	return allowed, err
}

func (p *Postgres) BindMediaChannel(ctx context.Context, binding MediaChannelBinding) error {
	result, err := p.pool.Exec(ctx, `
		INSERT INTO im_wukong_media_channels(media_id,channel_id,channel_type,sender_id)
		SELECT media.id,$2,$3,$4 FROM im_media media
		WHERE media.id=$1 AND media.owner_id=$4 AND media.status='ready'
		ON CONFLICT(media_id,channel_id,channel_type) DO NOTHING
	`, binding.MediaID, binding.ChannelID, binding.ChannelType, binding.SenderID)
	if err == nil && result.RowsAffected() == 0 {
		var exists bool
		err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_wukong_media_channels WHERE media_id=$1 AND channel_id=$2 AND channel_type=$3 AND sender_id=$4)`, binding.MediaID, binding.ChannelID, binding.ChannelType, binding.SenderID).Scan(&exists)
		if err == nil && !exists {
			return ErrForbidden
		}
	}
	return err
}

func (p *Postgres) UpdateUserProfile(ctx context.Context, uid string, update UserProfileUpdate) (*model.User, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if update.Handle != nil {
		var current string
		var changes int
		if err = tx.QueryRow(ctx, `SELECT COALESCE(handle,''),handle_change_count FROM im_users WHERE id=$1 FOR UPDATE`, uid).Scan(&current, &changes); errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		} else if err != nil {
			return nil, err
		}
		if current != *update.Handle && changes >= 2 {
			return nil, ErrForbidden
		}
	}
	var avatarURL *string
	if update.AvatarMediaID != nil {
		url := ""
		if *update.AvatarMediaID != "" {
			var owner, status string
			if err = tx.QueryRow(ctx, `SELECT owner_id,status FROM im_media WHERE id=$1`, *update.AvatarMediaID).Scan(&owner, &status); errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			} else if err != nil {
				return nil, err
			}
			if owner != uid || status != "ready" {
				return nil, ErrForbidden
			}
			url = "/v2/media/" + *update.AvatarMediaID
		}
		avatarURL = &url
	}
	var u model.User
	err = tx.QueryRow(ctx, `UPDATE im_users SET
		name=COALESCE($2,name),handle=COALESCE($3,handle),signature=COALESCE($4,signature),
		handle_change_count=handle_change_count+CASE WHEN $3::text IS NOT NULL AND handle IS DISTINCT FROM $3 THEN 1 ELSE 0 END,
		avatar_media_id=CASE WHEN $5::text IS NULL THEN avatar_media_id ELSE NULLIF($5,'') END,
		avatar_url=COALESCE($6,avatar_url),updated_at=now()
		WHERE id=$1 RETURNING id,phone,name,COALESCE(handle,''),handle_change_count,signature,COALESCE(avatar_media_id,''),avatar_url,banned,created_at`, uid, update.Name, update.Handle, update.Signature, update.AvatarMediaID, avatarURL).Scan(&u.ID, &u.Phone, &u.Name, &u.Handle, &u.HandleChangeCount, &u.Signature, &u.AvatarMediaID, &u.AvatarURL, &u.Banned, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &u, nil
}

func (p *Postgres) UpdateUserPhone(ctx context.Context, uid, phone string) (*model.User, error) {
	var u model.User
	err := p.pool.QueryRow(ctx, `UPDATE im_users SET phone=$2,updated_at=now() WHERE id=$1
		RETURNING id,phone,name,COALESCE(handle,''),handle_change_count,signature,COALESCE(avatar_media_id,''),avatar_url,banned,created_at`, uid, phone).Scan(&u.ID, &u.Phone, &u.Name, &u.Handle, &u.HandleChangeCount, &u.Signature, &u.AvatarMediaID, &u.AvatarURL, &u.Banned, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrConflict
	}
	return &u, err
}

func (p *Postgres) ListUserDevices(ctx context.Context, uid string) ([]*model.Device, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,user_id,platform,provider,notifications_enabled,preview_enabled,sound_enabled,vibration_enabled,updated_at FROM im_devices WHERE user_id=$1 ORDER BY updated_at DESC,id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*model.Device
	for rows.Next() {
		d := &model.Device{}
		if err = rows.Scan(&d.ID, &d.UserID, &d.Platform, &d.Provider, &d.NotificationsEnabled, &d.PreviewEnabled, &d.SoundEnabled, &d.VibrationEnabled, &d.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, d)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *Postgres) ListFavorites(ctx context.Context, uid string, limit int) ([]*model.Message, error) {
	_, items, err := p.listWukongFavorites(ctx, uid, limit)
	return items, err
}

func (p *Postgres) SetFavorite(ctx context.Context, uid, messageID string, enabled bool) error {
	if handled, err := p.setWukongFavorite(ctx, uid, messageID, enabled); handled {
		return err
	}
	return ErrNotFound
}

func (p *Postgres) CreateFeedback(ctx context.Context, id, uid, category, content, contact string, created time.Time) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO im_feedback(id,user_id,category,content,contact,created_at) VALUES($1,$2,$3,$4,$5,$6)`, id, uid, category, content, contact, created)
	return err
}
func (p *Postgres) ClaimPush(ctx context.Context, n int) ([]OutboxItem, error) {
	if n <= 0 || n > 1000 {
		n = 200
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,user_id,event_type,payload,attempts FROM im_push_outbox WHERE (status='pending' AND available_at<=now()) OR (status='processing' AND COALESCE(locked_at,created_at)<now()-interval '5 minutes') ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	var out []OutboxItem
	for rows.Next() {
		var x OutboxItem
		var raw []byte
		if err = rows.Scan(&x.ID, &x.UserID, &x.EventType, &raw, &x.Attempts); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal(raw, &x.Payload)
		out = append(out, x)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	userIndexes := make(map[string][]int, len(out))
	userIDs := make([]string, 0, len(out))
	itemIDs := make([]int64, 0, len(out))
	for i := range out {
		if len(userIndexes[out[i].UserID]) == 0 {
			userIDs = append(userIDs, out[i].UserID)
		}
		userIndexes[out[i].UserID] = append(userIndexes[out[i].UserID], i)
		itemIDs = append(itemIDs, out[i].ID)
	}
	if len(userIDs) > 0 {
		deviceRows, queryErr := tx.Query(ctx, `SELECT user_id,id,platform,push_token,provider,notifications_enabled,preview_enabled,sound_enabled,vibration_enabled FROM (
			SELECT d.*,row_number() OVER(PARTITION BY user_id ORDER BY id) AS device_rank FROM im_devices d
			WHERE user_id=ANY($1::text[]) AND notifications_enabled=true
		) ranked WHERE device_rank<=20 ORDER BY user_id,id`, userIDs)
		if queryErr != nil {
			return nil, queryErr
		}
		for deviceRows.Next() {
			var userID string
			var device Device
			if queryErr = deviceRows.Scan(&userID, &device.ID, &device.Platform, &device.PushToken, &device.Provider, &device.NotificationsEnabled, &device.PreviewEnabled, &device.SoundEnabled, &device.VibrationEnabled); queryErr != nil {
				deviceRows.Close()
				return nil, queryErr
			}
			for _, index := range userIndexes[userID] {
				out[index].Devices = append(out[index].Devices, device)
			}
		}
		queryErr = deviceRows.Err()
		deviceRows.Close()
		if queryErr != nil {
			return nil, queryErr
		}
	}
	if len(itemIDs) > 0 {
		if _, err = tx.Exec(ctx, `UPDATE im_push_outbox SET status='processing',locked_at=now(),attempts=attempts+1 WHERE id=ANY($1::bigint[])`, itemIDs); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
func (p *Postgres) CompletePush(ctx context.Context, id int64, sendErr error) error {
	if sendErr == nil {
		_, err := p.pool.Exec(ctx, `UPDATE im_push_outbox SET status='sent',sent_at=now(),locked_at=NULL,last_error='' WHERE id=$1 AND status='processing'`, id)
		return err
	}
	type permanentError interface{ Permanent() bool }
	var classified permanentError
	permanent := errors.As(sendErr, &classified) && classified.Permanent()
	_, err := p.pool.Exec(ctx, `UPDATE im_push_outbox SET status=CASE WHEN $3 OR attempts>=10 THEN 'failed' ELSE 'pending' END,available_at=now()+LEAST(attempts,10)*interval '30 seconds',locked_at=NULL,last_error=left($2,500) WHERE id=$1 AND status='processing'`, id, sendErr.Error(), permanent)
	return err
}
func (p *Postgres) InvalidatePushDevices(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := p.pool.Exec(ctx, `UPDATE im_devices SET notifications_enabled=false,push_token='',updated_at=now() WHERE id=ANY($1::text[]) AND provider IN ('getui','apns_voip','webpush')`, ids)
	return err
}
func (p *Postgres) SetBlock(ctx context.Context, uid, target string, blocked bool) error {
	if blocked {
		_, err := p.pool.Exec(ctx, `INSERT INTO im_blocks(user_id,blocked_user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, uid, target)
		return err
	}
	_, err := p.pool.Exec(ctx, `DELETE FROM im_blocks WHERE user_id=$1 AND blocked_user_id=$2`, uid, target)
	return err
}
func (p *Postgres) RemoveMember(ctx context.Context, cid, uid string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM im_members WHERE conversation_id=$1 AND user_id=$2`, cid, uid)
	return err
}
func (p *Postgres) DeleteConversation(ctx context.Context, cid string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM im_conversations WHERE id=$1`, cid)
	return err
}
func (p *Postgres) DeleteSensitiveWord(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM im_sensitive_words WHERE id=$1`, id)
	return err
}

func (p *Postgres) ListSensitiveWords(ctx context.Context) (map[string]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,value FROM im_sensitive_words ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := map[string]string{}
	for rows.Next() {
		var id, value string
		if err = rows.Scan(&id, &value); err != nil {
			return nil, err
		}
		items[id] = value
	}
	return items, rows.Err()
}

func (p *Postgres) CreateSensitiveWord(ctx context.Context, id, value, actor string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO im_sensitive_words(id,value) VALUES($1,$2)`, id, value); err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{"value": value})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'sensitive_word.created','sensitive_word',$3,$4,$5)`, "aud_sensitive_"+strconv.FormatInt(at.UnixNano(), 36), actor, id, metadata, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) DeleteSensitiveWordRecord(ctx context.Context, id, actor string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM im_sensitive_words WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'sensitive_word.deleted','sensitive_word',$3,'{}'::jsonb,$4)`, "aud_sensitive_"+strconv.FormatInt(at.UnixNano(), 36), actor, id, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) RuntimeSettings(ctx context.Context) (map[string]any, error) {
	rows, err := p.pool.Query(ctx, `SELECT key,value FROM im_settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := map[string]any{}
	for rows.Next() {
		var key string
		var raw []byte
		if err = rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		var value any
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		items[key] = value
	}
	return items, rows.Err()
}

func (p *Postgres) UpdateRuntimeSettings(ctx context.Context, actor string, settings map[string]any, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for key, value := range settings {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO im_settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, raw); err != nil {
			return err
		}
	}
	metadata, _ := json.Marshal(settings)
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'settings.updated','settings','global',$3,$4)`, "aud_settings_"+strconv.FormatInt(at.UnixNano(), 36), actor, metadata, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) RecallMessage(ctx context.Context, id string, at time.Time) error {
	return ErrUnsupported
}

func appendMemberBusinessEvent(ctx context.Context, tx pgx.Tx, cid, typ string, payload []byte, _ time.Time) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT user_id FROM im_members WHERE conversation_id=$1 ORDER BY user_id`, cid)
	if err != nil {
		return nil, err
	}
	var ids []string
	var recipients []wukongCommandRecipient
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		recipients = append(recipients, wukongCommandRecipient{UserID: id})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(ids)
	if err = enqueueWukongBusinessEvent(ctx, tx, "conversation", cid, typ, payload, recipients); err != nil {
		return nil, err
	}
	return ids, nil
}

func (p *Postgres) MarkRead(ctx context.Context, uid, cid string, seq int64, at time.Time) (int64, []string, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx)
	var actual int64
	err = tx.QueryRow(ctx, `UPDATE im_members m SET last_read_seq=GREATEST(m.last_read_seq,LEAST($3,GREATEST(c.last_message_seq,COALESCE((SELECT max(message_seq) FROM im_wukong_message_index WHERE conversation_id=c.id),0)))),manual_unread=false FROM im_conversations c
		WHERE m.conversation_id=$1 AND m.user_id=$2 AND c.id=m.conversation_id
		AND (m.last_read_seq<LEAST($3,GREATEST(c.last_message_seq,COALESCE((SELECT max(message_seq) FROM im_wukong_message_index WHERE conversation_id=c.id),0))) OR m.manual_unread)
		RETURNING m.last_read_seq`, cid, uid, seq).Scan(&actual)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT last_read_seq FROM im_members WHERE conversation_id=$1 AND user_id=$2`, cid, uid).Scan(&actual)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, ErrForbidden
		}
		if err != nil {
			return 0, nil, err
		}
		return actual, nil, tx.Commit(ctx)
	}
	if err != nil {
		return 0, nil, err
	}
	payload, _ := json.Marshal(map[string]any{"conversationId": cid, "userId": uid, "seq": actual})
	ids, err := appendMemberBusinessEvent(ctx, tx, cid, "message.read", payload, at)
	if err != nil {
		return 0, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	return actual, ids, nil
}

func (p *Postgres) MarkWukongRead(ctx context.Context, uid, cid string, verifiedSeq int64, at time.Time) (int64, []string, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx)
	var actual int64
	err = tx.QueryRow(ctx, `
		UPDATE im_members SET last_read_seq=GREATEST(last_read_seq,$3),manual_unread=false
		WHERE conversation_id=$1 AND user_id=$2
			AND (last_read_seq<$3 OR manual_unread)
		RETURNING last_read_seq
	`, cid, uid, verifiedSeq).Scan(&actual)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT last_read_seq FROM im_members WHERE conversation_id=$1 AND user_id=$2`, cid, uid).Scan(&actual)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, ErrForbidden
		}
		if err != nil {
			return 0, nil, err
		}
		return actual, nil, tx.Commit(ctx)
	}
	if err != nil {
		return 0, nil, err
	}
	payload, _ := json.Marshal(map[string]any{"conversationId": cid, "userId": uid, "seq": actual})
	ids, err := appendMemberBusinessEvent(ctx, tx, cid, "message.read", payload, at)
	if err != nil {
		return 0, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	return actual, ids, nil
}

func (p *Postgres) MarkDelivered(ctx context.Context, uid, cid string, seq int64, at time.Time) (int64, []string, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx)
	var actual int64
	err = tx.QueryRow(ctx, `UPDATE im_members m SET last_delivered_seq=GREATEST(m.last_delivered_seq,LEAST($3,GREATEST(c.last_message_seq,COALESCE((SELECT max(message_seq) FROM im_wukong_message_index WHERE conversation_id=c.id),0)))) FROM im_conversations c
		WHERE m.conversation_id=$1 AND m.user_id=$2 AND c.id=m.conversation_id
		AND m.last_delivered_seq<LEAST($3,GREATEST(c.last_message_seq,COALESCE((SELECT max(message_seq) FROM im_wukong_message_index WHERE conversation_id=c.id),0)))
		RETURNING m.last_delivered_seq`, cid, uid, seq).Scan(&actual)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT last_delivered_seq FROM im_members WHERE conversation_id=$1 AND user_id=$2`, cid, uid).Scan(&actual)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, ErrForbidden
		}
		if err != nil {
			return 0, nil, err
		}
		return actual, nil, tx.Commit(ctx)
	}
	if err != nil {
		return 0, nil, err
	}
	payload, _ := json.Marshal(map[string]any{"conversationId": cid, "userId": uid, "seq": actual})
	ids, err := appendMemberBusinessEvent(ctx, tx, cid, "message.delivered", payload, at)
	if err != nil {
		return 0, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	return actual, ids, nil
}

func (p *Postgres) UpdateConversationPreferences(ctx context.Context, uid, cid string, preferences ConversationPreferences) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var pinned, archived, muted, unread bool
	err = tx.QueryRow(ctx, `UPDATE im_members SET
		pinned=COALESCE($3,pinned), archived=COALESCE($4,archived),
		notifications_muted=COALESCE($5,notifications_muted), manual_unread=COALESCE($6,manual_unread)
		WHERE conversation_id=$1 AND user_id=$2 AND (
			($3::boolean IS NOT NULL AND pinned IS DISTINCT FROM $3) OR
			($4::boolean IS NOT NULL AND archived IS DISTINCT FROM $4) OR
			($5::boolean IS NOT NULL AND notifications_muted IS DISTINCT FROM $5) OR
			($6::boolean IS NOT NULL AND manual_unread IS DISTINCT FROM $6))
		RETURNING pinned,archived,notifications_muted,manual_unread`, cid, uid, preferences.Pinned, preferences.Archived, preferences.NotificationsMuted, preferences.ManualUnread).Scan(&pinned, &archived, &muted, &unread)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT pinned,archived,notifications_muted,manual_unread FROM im_members WHERE conversation_id=$1 AND user_id=$2`, cid, uid).Scan(&pinned, &archived, &muted, &unread)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"conversationId": cid, "pinned": pinned, "archived": archived, "notificationsMuted": muted, "manualUnread": unread})
	if err = appendUserBusinessEvent(ctx, tx, uid, "conversation.preferences.updated", payload, time.Now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) HideConversation(ctx context.Context, uid, cid string) error {
	result, err := p.pool.Exec(ctx, `UPDATE im_members m SET hidden_until_seq=GREATEST(c.last_message_seq,COALESCE((
		SELECT max(message_seq) FROM im_wukong_message_index WHERE conversation_id=c.id
	),0)),pinned=false,manual_unread=false
		FROM im_conversations c WHERE m.conversation_id=$1 AND m.user_id=$2 AND c.id=m.conversation_id`, cid, uid)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrForbidden
	}
	return nil
}
func (p *Postgres) RecallAuthorized(ctx context.Context, uid, mid string, at time.Time, window time.Duration) (string, int64, []string, error) {
	if handled, cid, seq, ids, err := p.recallWukongAuthorized(ctx, uid, mid, at, window); handled {
		return cid, seq, ids, err
	}
	return "", 0, nil, ErrNotFound
}

func messageMentionFields(body map[string]any) ([]string, bool) {
	mentions := []string{}
	switch values := body["mentions"].(type) {
	case []string:
		mentions = append(mentions, values...)
	case []any:
		for _, value := range values {
			if userID, ok := value.(string); ok {
				mentions = append(mentions, userID)
			}
		}
	}
	mentionAll, _ := body["mentionAll"].(bool)
	return mentions, mentionAll
}

func validateEditMentions(ctx context.Context, tx pgx.Tx, cid, uid, kind, role string, body map[string]any) error {
	mentions, mentionAll := messageMentionFields(body)
	if len(mentions) == 0 && !mentionAll {
		return nil
	}
	if kind != "group" || (mentionAll && role != "owner" && role != "admin") {
		return ErrForbidden
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM im_members WHERE conversation_id=$1 AND user_id=ANY($2::text[])`, cid, mentions).Scan(&count); err != nil {
		return err
	}
	if count != len(mentions) {
		return ErrForbidden
	}
	return nil
}

func (p *Postgres) EditMessage(ctx context.Context, uid, mid, editID string, body, originalBody map[string]any, at time.Time, window time.Duration) (*model.Message, bool, error) {
	if handled, message, duplicate, err := p.editWukongMessage(ctx, uid, mid, editID, body, originalBody, at, window); handled {
		return message, duplicate, err
	}
	return nil, false, ErrNotFound
}

func (p *Postgres) ListMessageEdits(ctx context.Context, uid, mid string) ([]*model.MessageEdit, error) {
	if handled, items, err := p.listWukongMessageEdits(ctx, uid, mid); handled {
		return items, err
	}
	return nil, ErrNotFound
}

func (p *Postgres) SetMessageReaction(ctx context.Context, uid, mid, emoji string, add bool, at time.Time) (model.MessageReactionSummary, bool, error) {
	if handled, summary, duplicate, err := p.reactWukongMessage(ctx, uid, mid, emoji, add, at); handled {
		return summary, duplicate, err
	}
	return model.MessageReactionSummary{}, false, ErrNotFound
}

func (p *Postgres) SetGroupMessagePin(ctx context.Context, uid, cid, mid string, pin bool, at time.Time) (*model.MessagePin, bool, error) {
	if handled, item, duplicate, err := p.setWukongGroupMessagePin(ctx, uid, cid, mid, pin, at); handled {
		return item, duplicate, err
	}
	return nil, false, ErrNotFound
}

func (p *Postgres) ListGroupMessagePins(ctx context.Context, uid, cid string, before int64, limit int) ([]*model.MessagePin, error) {
	if handled, items, err := p.listWukongGroupMessagePins(ctx, uid, cid, before, limit); handled {
		return items, err
	}
	return []*model.MessagePin{}, nil
}

func (p *Postgres) CreateReportRecord(ctx context.Context, r *model.Report, a *model.AuditEntry) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var targetExists bool
	switch r.TargetType {
	case "user":
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1)`, r.TargetID).Scan(&targetExists)
	case "message":
		err = tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM im_wukong_message_index message_index
			JOIN im_members member ON member.conversation_id=message_index.conversation_id AND member.user_id=$2
			WHERE message_index.message_id::text=$1
		)`, r.TargetID, r.ReporterID).Scan(&targetExists)
	case "group":
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_conversations c JOIN im_members member ON member.conversation_id=c.id AND member.user_id=$2 WHERE c.id=$1 AND c.kind='group')`, r.TargetID, r.ReporterID).Scan(&targetExists)
	case "moment":
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_moments moment WHERE moment.id=$1 AND moment.status<>'deleted' AND (
			moment.author_id=$2 OR (moment.status='published' AND (
				moment.visibility='public' OR
				(moment.visibility IN ('friends','excluded') AND EXISTS(
					SELECT 1 FROM im_friendships friendship WHERE friendship.user_id=$2 AND friendship.friend_user_id=moment.author_id)
					AND (moment.visibility<>'excluded' OR NOT ($2=ANY(moment.visible_user_ids)))) OR
				(moment.visibility='selected' AND $2=ANY(moment.visible_user_ids))
			))
		))`, r.TargetID, r.ReporterID).Scan(&targetExists)
	default:
		return ErrUnsupported
	}
	if err != nil {
		return err
	}
	if !targetExists {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_reports(id,reporter_id,target_type,target_id,reason,details,status,resolution,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, r.ID, r.ReporterID, r.TargetType, r.TargetID, r.Reason, r.Details, r.Status, r.Resolution, r.CreatedAt, r.UpdatedAt); err != nil {
		return err
	}
	raw, _ := json.Marshal(a.Metadata)
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, a.ID, a.ActorID, a.Action, a.TargetType, a.TargetID, raw, a.CreatedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) SetUserBanRecord(ctx context.Context, actor, uid string, banned bool, until *time.Time, reason, auditID string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE im_users SET banned=$2,banned_until=CASE WHEN $2 THEN $3::timestamptz ELSE NULL END,updated_at=$4 WHERE id=$1`, uid, banned, until, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	action := "user.unbanned"
	if banned {
		action = "user.banned"
	}
	meta, _ := json.Marshal(map[string]any{"reason": reason, "bannedUntil": until})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,$3,'user',$4,$5,$6)`, auditID, actor, action, uid, meta, at); err != nil {
		return err
	}
	if banned {
		if _, err = tx.Exec(ctx, `UPDATE im_refresh_sessions SET revoked_at=COALESCE(revoked_at,$2) WHERE user_id=$1 AND revoked_at IS NULL`, uid, at); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (p *Postgres) ExpireUserBans(ctx context.Context, at time.Time, limit int) ([]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `WITH due AS (SELECT id FROM im_users WHERE banned AND banned_until<=$1 ORDER BY banned_until,id FOR UPDATE SKIP LOCKED LIMIT $2)
		UPDATE im_users u SET banned=false,banned_until=NULL,updated_at=$1 FROM due WHERE u.id=due.id RETURNING u.id`, at, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		metadata, _ := json.Marshal(map[string]any{"reason": "scheduled ban expired"})
		if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,'system','user.ban_expired','user',$2,$3,$4)`, "aud_ban_expired_"+id+"_"+strconv.FormatInt(at.UnixNano(), 36), id, metadata, at); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ids, nil
}
func (p *Postgres) ResolveReportRecord(ctx context.Context, actor, rid, action, note, auditID string, at time.Time) (string, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var targetType, targetID, status string
	err = tx.QueryRow(ctx, `SELECT target_type,target_id,status FROM im_reports WHERE id=$1 FOR UPDATE`, rid).Scan(&targetType, &targetID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if status != "pending" {
		return "", ErrConflict
	}
	result := "resolved"
	switch action {
	case "dismiss", "no_violation":
		result = "rejected"
	case "delete_message":
		if targetType != "message" {
			return "", ErrConflict
		}
		meta := wukongMutationMeta{MessageID: targetID, Extension: map[string]any{}}
		var extension []byte
		err = tx.QueryRow(ctx, `
			SELECT message_index.conversation_id,message_index.sender_id,message_index.channel_id,message_index.channel_type,
				message_index.message_seq,message_index.content_type,message_index.message_timestamp,
				COALESCE(message_extension.payload,'{}'::jsonb)
			FROM im_wukong_message_index message_index
			LEFT JOIN im_wukong_message_extensions message_extension ON message_extension.message_id=message_index.message_id
				AND message_extension.channel_id=message_index.channel_id AND message_extension.channel_type=message_index.channel_type
			WHERE message_index.message_id::text=$1 FOR UPDATE OF message_index
		`, targetID).Scan(&meta.ConversationID, &meta.SenderID, &meta.ChannelID, &meta.ChannelType, &meta.MessageSeq, &meta.ContentType, &meta.MessageAt, &extension)
		if err == nil {
			if err = json.Unmarshal(extension, &meta.Extension); err != nil {
				return "", err
			}
			if meta.Extension["recalledAt"] == nil {
				meta.Extension["recalledAt"] = at.UTC().Format(time.RFC3339Nano)
				meta.Extension["moderatedBy"] = actor
				if err = saveWukongMessageExtension(ctx, tx, meta, actor, meta.Extension, at); err != nil {
					return "", err
				}
			}
		} else if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		} else {
			return "", err
		}
		payload, _ := json.Marshal(map[string]any{"messageId": targetID, "conversationId": meta.ConversationID, "conversationSeq": meta.MessageSeq})
		if _, err = appendMemberBusinessEvent(ctx, tx, meta.ConversationID, "message.recalled", payload, at); err != nil {
			return "", err
		}
	case "ban_user":
		userID := targetID
		if targetType == "message" {
			if err = tx.QueryRow(ctx, `SELECT sender_id FROM im_wukong_message_index WHERE message_id::text=$1`, targetID).Scan(&userID); err != nil {
				return "", ErrNotFound
			}
		} else if targetType != "user" {
			return "", ErrConflict
		}
		tag, e := tx.Exec(ctx, `UPDATE im_users SET banned=true,updated_at=$2 WHERE id=$1`, userID, at)
		if e != nil {
			return "", e
		}
		if tag.RowsAffected() == 0 {
			return "", ErrNotFound
		}
		if _, e = tx.Exec(ctx, `UPDATE im_refresh_sessions SET revoked_at=COALESCE(revoked_at,$2) WHERE user_id=$1 AND revoked_at IS NULL`, userID, at); e != nil {
			return "", e
		}
	default:
		return "", ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE im_reports SET status=$2,resolution=$3,updated_at=$4 WHERE id=$1`, rid, result, note, at); err != nil {
		return "", err
	}
	meta, _ := json.Marshal(map[string]any{"action": action, "note": note})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'report.resolved','report',$3,$4,$5)`, auditID, actor, rid, meta, at); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return result, nil
}

const callSelectColumns = `id,conversation_id,call_kind,caller_id,callee_id,participant_ids,joined_user_ids,declined_user_ids,left_user_ids,media_type,status,end_reason,ended_by,invited_at,expires_at,accepted_at,ended_at,
	CASE WHEN accepted_at IS NULL THEN 0 ELSE GREATEST(0,EXTRACT(EPOCH FROM (COALESCE(ended_at,now())-accepted_at)))::bigint END,updated_at`

type callRow interface{ Scan(...any) error }

func scanCall(row callRow) (*model.CallSession, error) {
	c := &model.CallSession{}
	var calleeID *string
	err := row.Scan(&c.ID, &c.ConversationID, &c.Kind, &c.CallerID, &calleeID, &c.ParticipantIDs, &c.JoinedUserIDs, &c.DeclinedUserIDs, &c.LeftUserIDs, &c.MediaType, &c.Status, &c.EndReason, &c.EndedBy,
		&c.InvitedAt, &c.ExpiresAt, &c.AcceptedAt, &c.EndedAt, &c.DurationSeconds, &c.UpdatedAt)
	if calleeID != nil {
		c.CalleeID = *calleeID
	}
	if c.ParticipantIDs == nil {
		c.ParticipantIDs = []string{}
	}
	if c.JoinedUserIDs == nil {
		c.JoinedUserIDs = []string{}
	}
	if c.DeclinedUserIDs == nil {
		c.DeclinedUserIDs = []string{}
	}
	if c.LeftUserIDs == nil {
		c.LeftUserIDs = []string{}
	}
	return c, err
}

func callHasUser(users []string, userID string) bool {
	for _, candidate := range users {
		if candidate == userID {
			return true
		}
	}
	return false
}

func callWithoutUser(users []string, userID string) []string {
	result := make([]string, 0, len(users))
	for _, candidate := range users {
		if candidate != userID {
			result = append(result, candidate)
		}
	}
	return result
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func appendCallUser(users []string, userID string) []string {
	if callHasUser(users, userID) {
		return users
	}
	result := append(append([]string(nil), users...), userID)
	sort.Strings(result)
	return result
}

func allCallInviteesDeclined(c *model.CallSession, declined []string) bool {
	for _, participantID := range c.ParticipantIDs {
		if participantID != c.CallerID && !callHasUser(declined, participantID) {
			return false
		}
	}
	return true
}

func callStateEvent(status string) string {
	switch status {
	case "accepted":
		return "call.accepted"
	case "rejected":
		return "call.rejected"
	case "cancelled":
		return "call.cancelled"
	case "ended":
		return "call.ended"
	case "missed":
		return "call.timeout"
	default:
		return ""
	}
}

func appendCallStateEvent(ctx context.Context, tx pgx.Tx, c *model.CallSession, at time.Time) error {
	event := callStateEvent(c.Status)
	if event == "" {
		return nil
	}
	return appendCallCommandEvent(ctx, tx, c, event, at)
}

func appendCallCommandEvent(ctx context.Context, tx pgx.Tx, c *model.CallSession, event string, at time.Time) error {
	payload, err := json.Marshal(map[string]any{
		"call": c, "callId": c.ID, "conversationId": c.ConversationID,
		"status": c.Status, "endReason": c.EndReason, "event": event,
	})
	if err != nil {
		return err
	}
	participants := append([]string(nil), c.ParticipantIDs...)
	sort.Strings(participants)
	for _, userID := range participants {
		if err = appendUserBusinessEvent(ctx, tx, userID, event, payload, at); err != nil {
			return err
		}
	}
	return enqueueWukongCallEvent(ctx, tx, c, event, participants)
}

func (p *Postgres) InviteCall(ctx context.Context, in CallInvite) (*model.CallSession, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "call:"+in.ConversationID); err != nil {
		return nil, false, err
	}
	var kind string
	var members []string
	if err = tx.QueryRow(ctx, `SELECT c.kind,array_agg(m.user_id ORDER BY m.user_id) FROM im_conversations c JOIN im_members m ON m.conversation_id=c.id WHERE c.id=$1 GROUP BY c.kind`, in.ConversationID).Scan(&kind, &members); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrNotFound
		}
		return nil, false, err
	}
	if (kind != "direct" && kind != "group") || (in.Kind != "" && in.Kind != kind) || len(members) < 2 || len(members) > 9 {
		return nil, false, ErrConflict
	}
	seenCaller, seenCallee := false, false
	for _, member := range members {
		seenCaller = seenCaller || member == in.CallerID
		seenCallee = seenCallee || member == in.CalleeID
	}
	if !seenCaller {
		return nil, false, ErrForbidden
	}
	if kind == "direct" && (len(members) != 2 || in.CallerID == in.CalleeID || !seenCallee) {
		return nil, false, ErrForbidden
	}
	if kind == "group" && in.CalleeID != "" {
		return nil, false, ErrConflict
	}
	in.Kind = kind
	in.ParticipantIDs = append([]string(nil), members...)
	existing, scanErr := scanCall(tx.QueryRow(ctx, `SELECT `+callSelectColumns+` FROM im_call_sessions WHERE id=$1`, in.ID))
	if scanErr == nil {
		if existing.ConversationID != in.ConversationID || existing.Kind != in.Kind || existing.CallerID != in.CallerID || existing.CalleeID != in.CalleeID || existing.MediaType != in.MediaType || !equalStringSlices(existing.ParticipantIDs, in.ParticipantIDs) {
			return nil, false, ErrConflict
		}
		return existing, true, tx.Commit(ctx)
	}
	if !errors.Is(scanErr, pgx.ErrNoRows) {
		return nil, false, scanErr
	}
	expiredRows, err := tx.Query(ctx, `UPDATE im_call_sessions SET status='missed',ended_at=expires_at,end_reason='timeout',updated_at=$2 WHERE conversation_id=$1 AND status='invited' AND expires_at<=$2 RETURNING `+callSelectColumns, in.ConversationID, in.InvitedAt)
	if err != nil {
		return nil, false, err
	}
	var expiredCalls []*model.CallSession
	for expiredRows.Next() {
		expired, scanExpiredErr := scanCall(expiredRows)
		if scanExpiredErr != nil {
			expiredRows.Close()
			return nil, false, scanExpiredErr
		}
		expiredCalls = append(expiredCalls, expired)
	}
	if err = expiredRows.Err(); err != nil {
		expiredRows.Close()
		return nil, false, err
	}
	expiredRows.Close()
	for _, expired := range expiredCalls {
		if err = appendCallStateEvent(ctx, tx, expired, in.InvitedAt); err != nil {
			return nil, false, err
		}
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_call_sessions WHERE conversation_id=$1 AND status IN ('invited','accepted'))`, in.ConversationID).Scan(&active); err != nil {
		return nil, false, err
	}
	if active {
		return nil, false, ErrConflict
	}
	var calleeID any
	if in.CalleeID != "" {
		calleeID = in.CalleeID
	}
	c, err := scanCall(tx.QueryRow(ctx, `INSERT INTO im_call_sessions(id,conversation_id,call_kind,caller_id,callee_id,participant_ids,joined_user_ids,media_type,status,invited_at,expires_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,ARRAY[$4]::text[],$7,'invited',$8,$9,$8) RETURNING `+callSelectColumns, in.ID, in.ConversationID, in.Kind, in.CallerID, calleeID, in.ParticipantIDs, in.MediaType, in.InvitedAt, in.ExpiresAt))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, false, ErrConflict
		}
		return nil, false, err
	}
	invitees := callWithoutUser(c.ParticipantIDs, c.CallerID)
	payload, _ := json.Marshal(map[string]any{"call": c, "callId": c.ID, "conversationId": c.ConversationID, "mediaType": c.MediaType})
	pushPayload, _ := json.Marshal(map[string]any{"callId": c.ID, "conversationId": c.ConversationID, "mediaType": c.MediaType})
	for _, inviteeID := range invitees {
		if err = appendUserBusinessEvent(ctx, tx, inviteeID, "call.invited", payload, c.InvitedAt); err != nil {
			return nil, false, err
		}
		if err = insertPrivatePush(ctx, tx, inviteeID, "call.invited", pushPayload); err != nil {
			return nil, false, err
		}
	}
	if err = enqueueWukongCallEvent(ctx, tx, c, "call.invited", invitees); err != nil {
		return nil, false, err
	}
	return c, false, tx.Commit(ctx)
}

func (p *Postgres) TransitionCall(ctx context.Context, id, uid, action, reason string, at time.Time) (*model.CallSession, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	c, err := scanCall(tx.QueryRow(ctx, `SELECT `+callSelectColumns+` FROM im_call_sessions WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	if !callHasUser(c.ParticipantIDs, uid) {
		return nil, false, ErrForbidden
	}
	if c.Status == "invited" && !at.Before(c.ExpiresAt) {
		c, err = scanCall(tx.QueryRow(ctx, `UPDATE im_call_sessions SET status='missed',ended_at=expires_at,end_reason='timeout',updated_at=$2 WHERE id=$1 RETURNING `+callSelectColumns, id, at))
		if err != nil {
			return nil, false, err
		}
		if err = appendCallStateEvent(ctx, tx, c, at); err != nil {
			return nil, false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return c, false, ErrConflict
	}
	target, event, endedBy := c.Status, "", ""
	joined := append([]string{}, c.JoinedUserIDs...)
	declined := append([]string{}, c.DeclinedUserIDs...)
	left := append([]string{}, c.LeftUserIDs...)
	acceptedAt, endedAt := c.AcceptedAt, c.EndedAt
	terminal := false

	if c.Kind == "group" {
		switch action {
		case "accept":
			if callHasUser(joined, uid) && uid != c.CallerID {
				return c, true, tx.Commit(ctx)
			}
			if uid == c.CallerID || (c.Status != "invited" && c.Status != "accepted") || callHasUser(declined, uid) || callHasUser(left, uid) {
				return nil, false, ErrConflict
			}
			joined = appendCallUser(joined, uid)
			target, event = "accepted", "call.accepted"
			if acceptedAt == nil {
				acceptedAt = &at
			}
		case "reject":
			if callHasUser(declined, uid) {
				return c, true, tx.Commit(ctx)
			}
			if uid == c.CallerID || (c.Status != "invited" && c.Status != "accepted") || callHasUser(joined, uid) {
				return nil, false, ErrConflict
			}
			declined = appendCallUser(declined, uid)
			event = "call.participant_declined"
			if c.Status == "invited" && allCallInviteesDeclined(c, declined) {
				target, event, endedBy, terminal = "rejected", "call.rejected", uid, true
			}
		case "cancel":
			if c.Status == "cancelled" && uid == c.CallerID {
				return c, true, tx.Commit(ctx)
			}
			if c.Status != "invited" || uid != c.CallerID {
				return nil, false, ErrConflict
			}
			target, event, endedBy, terminal = "cancelled", "call.cancelled", uid, true
		case "hangup", "end":
			if callHasUser(left, uid) || c.Status == "ended" {
				return c, true, tx.Commit(ctx)
			}
			if c.Status == "invited" && action == "end" {
				if uid == c.CallerID {
					target, event, endedBy, terminal = "cancelled", "call.cancelled", uid, true
				} else {
					declined = appendCallUser(declined, uid)
					event = "call.participant_declined"
					if allCallInviteesDeclined(c, declined) {
						target, event, endedBy, terminal = "rejected", "call.rejected", uid, true
					}
				}
			} else {
				if c.Status != "accepted" || !callHasUser(joined, uid) {
					return nil, false, ErrConflict
				}
				left = appendCallUser(left, uid)
				event = "call.participant_left"
				if uid == c.CallerID {
					target, event, endedBy, terminal = "ended", "call.ended", uid, true
				}
			}
		default:
			return nil, false, ErrUnsupported
		}
	} else {
		switch action {
		case "accept":
			if c.Status == "accepted" && uid == c.CalleeID {
				return c, true, tx.Commit(ctx)
			}
			if c.Status != "invited" || uid != c.CalleeID {
				return nil, false, ErrConflict
			}
			joined = appendCallUser(joined, uid)
			target, event, acceptedAt = "accepted", "call.accepted", &at
		case "reject":
			if c.Status == "rejected" && uid == c.CalleeID {
				return c, true, tx.Commit(ctx)
			}
			if c.Status != "invited" || uid != c.CalleeID {
				return nil, false, ErrConflict
			}
			declined = appendCallUser(declined, uid)
			target, event, endedBy, terminal = "rejected", "call.rejected", uid, true
		case "cancel":
			if c.Status == "cancelled" && uid == c.CallerID {
				return c, true, tx.Commit(ctx)
			}
			if c.Status != "invited" || uid != c.CallerID {
				return nil, false, ErrConflict
			}
			target, event, endedBy, terminal = "cancelled", "call.cancelled", uid, true
		case "hangup", "end":
			if c.Status == "ended" || c.Status == "cancelled" || c.Status == "rejected" {
				return c, true, tx.Commit(ctx)
			}
			if c.Status == "accepted" {
				left = appendCallUser(left, uid)
				target, event, endedBy, terminal = "ended", "call.ended", uid, true
			} else if action == "end" && c.Status == "invited" && uid == c.CallerID {
				target, event, endedBy, terminal = "cancelled", "call.cancelled", uid, true
			} else if action == "end" && c.Status == "invited" && uid == c.CalleeID {
				declined = appendCallUser(declined, uid)
				target, event, endedBy, terminal = "rejected", "call.rejected", uid, true
			} else {
				return nil, false, ErrConflict
			}
		default:
			return nil, false, ErrUnsupported
		}
	}
	if terminal {
		endedAt = &at
	}
	c, err = scanCall(tx.QueryRow(ctx, `UPDATE im_call_sessions SET status=$2,joined_user_ids=$3,declined_user_ids=$4,left_user_ids=$5,accepted_at=$6,ended_at=$7,ended_by=$8,end_reason=$9,updated_at=$10 WHERE id=$1 RETURNING `+callSelectColumns,
		id, target, joined, declined, left, acceptedAt, endedAt, endedBy, reason, at))
	if err != nil {
		return nil, false, err
	}
	if err = appendCallCommandEvent(ctx, tx, c, event, at); err != nil {
		return nil, false, err
	}
	return c, false, tx.Commit(ctx)
}

func (p *Postgres) GetCall(ctx context.Context, id, uid string, at time.Time) (*model.CallSession, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	c, err := scanCall(tx.QueryRow(ctx, `SELECT `+callSelectColumns+` FROM im_call_sessions WHERE id=$1 AND ($2='' OR $2=ANY(participant_ids)) FOR UPDATE`, id, uid))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if c.Status == "invited" && !at.Before(c.ExpiresAt) {
		c, err = scanCall(tx.QueryRow(ctx, `UPDATE im_call_sessions SET status='missed',ended_at=expires_at,end_reason='timeout',updated_at=$2 WHERE id=$1 RETURNING `+callSelectColumns, id, at))
		if err != nil {
			return nil, err
		}
		if err = appendCallStateEvent(ctx, tx, c, at); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (p *Postgres) ExpireCalls(ctx context.Context, at time.Time, limit int) ([]*model.CallSession, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `WITH expired AS (
		SELECT id FROM im_call_sessions WHERE status='invited' AND expires_at<=$1 ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT $2
	) UPDATE im_call_sessions c SET status='missed',ended_at=c.expires_at,end_reason='timeout',updated_at=$1 FROM expired WHERE c.id=expired.id
	RETURNING c.id,c.conversation_id,c.call_kind,c.caller_id,c.callee_id,c.participant_ids,c.joined_user_ids,c.declined_user_ids,c.left_user_ids,c.media_type,c.status,c.end_reason,c.ended_by,c.invited_at,c.expires_at,c.accepted_at,c.ended_at,0::bigint,c.updated_at`, at, limit)
	if err != nil {
		return nil, err
	}
	var out []*model.CallSession
	for rows.Next() {
		c, scanErr := scanCall(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		out = append(out, c)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, call := range out {
		if err = appendCallStateEvent(ctx, tx, call, at); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Postgres) ListAdminCalls(ctx context.Context, q, status, cursor string, limit int) ([]*model.CallSession, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + q + "%"
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_call_sessions WHERE ($1='' OR status=$1) AND
		($2='' OR id ILIKE $3 OR conversation_id ILIKE $3 OR caller_id ILIKE $3 OR COALESCE(callee_id,'') ILIKE $3 OR array_to_string(participant_ids,',') ILIKE $3)`, status, q, pattern).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, `SELECT `+callSelectColumns+` FROM im_call_sessions WHERE ($1='' OR status=$1) AND
		($2='' OR id ILIKE $3 OR conversation_id ILIKE $3 OR caller_id ILIKE $3 OR COALESCE(callee_id,'') ILIKE $3 OR array_to_string(participant_ids,',') ILIKE $3)
		ORDER BY invited_at DESC,id LIMIT $4 OFFSET $5`, status, q, pattern, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	var items []*model.CallSession
	for rows.Next() {
		c, scanErr := scanCall(rows)
		if scanErr != nil {
			return nil, 0, "", scanErr
		}
		items = append(items, c)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func appendUserBusinessEvent(ctx context.Context, tx pgx.Tx, uid, typ string, payload []byte, _ time.Time) error {
	return enqueueWukongBusinessEvent(ctx, tx, "user", uid, typ, payload, []wukongCommandRecipient{{UserID: uid}})
}

func insertPrivatePush(ctx context.Context, tx pgx.Tx, uid, typ string, payload []byte) error {
	_, err := tx.Exec(ctx, `INSERT INTO im_push_outbox(user_id,event_type,payload) VALUES($1,$2,$3)`, uid, typ, payload)
	return err
}

func friendRequestColumns(prefix string) string {
	return prefix + `id,` + prefix + `from_user_id,` + prefix + `to_user_id,` + prefix + `message,` + prefix + `source,` + prefix + `source_id,` + prefix + `status,` + prefix + `created_at,` + prefix + `expires_at,` + prefix + `updated_at,` + prefix + `resolved_at`
}

func scanFriendRequest(row callRow) (*model.FriendRequest, error) {
	r := &model.FriendRequest{}
	err := row.Scan(&r.ID, &r.FromUserID, &r.ToUserID, &r.Message, &r.Source, &r.SourceID, &r.Status, &r.CreatedAt, &r.ExpiresAt, &r.UpdatedAt, &r.ResolvedAt)
	return r, err
}

func friendPairKey(a, b string) string {
	if a < b {
		return a + ":" + b
	}
	return b + ":" + a
}

func (p *Postgres) CreateFriendRequest(ctx context.Context, request *model.FriendRequest) (*model.FriendRequest, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "friend:"+friendPairKey(request.FromUserID, request.ToUserID)); err != nil {
		return nil, false, err
	}
	var targetOK, blocked, friends bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1 AND NOT banned)`, request.ToUserID).Scan(&targetOK); err != nil {
		return nil, false, err
	}
	if !targetOK {
		return nil, false, ErrNotFound
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_blocks WHERE (user_id=$1 AND blocked_user_id=$2) OR (user_id=$2 AND blocked_user_id=$1))`, request.FromUserID, request.ToUserID).Scan(&blocked); err != nil {
		return nil, false, err
	}
	if blocked {
		return nil, false, ErrForbidden
	}
	if request.Source == "group" {
		if request.SourceID == "" {
			return nil, false, ErrForbidden
		}
		var allowed bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_groups g JOIN im_members a ON a.conversation_id=g.conversation_id AND a.user_id=$2 JOIN im_members b ON b.conversation_id=g.conversation_id AND b.user_id=$3 WHERE g.conversation_id=$1 AND g.allow_member_add_friend AND g.dissolved_at IS NULL)`, request.SourceID, request.FromUserID, request.ToUserID).Scan(&allowed); err != nil {
			return nil, false, err
		}
		if !allowed {
			return nil, false, ErrForbidden
		}
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_friendships WHERE user_id=$1 AND friend_user_id=$2)`, request.FromUserID, request.ToUserID).Scan(&friends); err != nil {
		return nil, false, err
	}
	if friends {
		return nil, false, ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE im_friend_requests SET status='expired',resolved_at=expires_at,updated_at=$3 WHERE status='pending' AND expires_at<=$3 AND ((from_user_id=$1 AND to_user_id=$2) OR (from_user_id=$2 AND to_user_id=$1))`, request.FromUserID, request.ToUserID, request.CreatedAt); err != nil {
		return nil, false, err
	}
	existing, scanErr := scanFriendRequest(tx.QueryRow(ctx, `SELECT `+friendRequestColumns("")+` FROM im_friend_requests WHERE from_user_id=$1 AND to_user_id=$2 AND status='pending'`, request.FromUserID, request.ToUserID))
	if scanErr == nil {
		return existing, true, tx.Commit(ctx)
	}
	if !errors.Is(scanErr, pgx.ErrNoRows) {
		return nil, false, scanErr
	}
	var reverse bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_friend_requests WHERE from_user_id=$2 AND to_user_id=$1 AND status='pending')`, request.FromUserID, request.ToUserID).Scan(&reverse); err != nil {
		return nil, false, err
	}
	if reverse {
		return nil, false, ErrConflict
	}
	created, err := scanFriendRequest(tx.QueryRow(ctx, `INSERT INTO im_friend_requests(id,from_user_id,to_user_id,message,source,source_id,status,created_at,expires_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$8,$7) RETURNING `+friendRequestColumns(""), request.ID, request.FromUserID, request.ToUserID, request.Message, request.Source, request.SourceID, request.CreatedAt, request.ExpiresAt))
	if err != nil {
		return nil, false, err
	}
	senderPayload, _ := json.Marshal(map[string]any{"requestId": created.ID, "userId": created.ToUserID, "status": "pending"})
	receiverPayload, _ := json.Marshal(map[string]any{"request": created})
	if err = appendUserBusinessEvent(ctx, tx, created.FromUserID, "friend.request.sent", senderPayload, created.CreatedAt); err != nil {
		return nil, false, err
	}
	if err = appendUserBusinessEvent(ctx, tx, created.ToUserID, "friend.request", receiverPayload, created.CreatedAt); err != nil {
		return nil, false, err
	}
	pushPayload, _ := json.Marshal(map[string]any{"requestId": created.ID, "fromUserId": created.FromUserID})
	if err = insertPrivatePush(ctx, tx, created.ToUserID, "friend.request", pushPayload); err != nil {
		return nil, false, err
	}
	return created, false, tx.Commit(ctx)
}

func (p *Postgres) TransitionFriendRequest(ctx context.Context, id, uid, action string, at time.Time) (*model.FriendRequest, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	r, err := scanFriendRequest(tx.QueryRow(ctx, `SELECT `+friendRequestColumns("")+` FROM im_friend_requests WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	if r.Status == "pending" && !at.Before(r.ExpiresAt) {
		r, err = scanFriendRequest(tx.QueryRow(ctx, `UPDATE im_friend_requests SET status='expired',resolved_at=expires_at,updated_at=$2 WHERE id=$1 RETURNING `+friendRequestColumns(""), id, at))
		if err != nil {
			return nil, false, err
		}
		fromPayload, _ := json.Marshal(map[string]any{"requestId": r.ID, "userId": r.ToUserID, "status": "expired"})
		toPayload, _ := json.Marshal(map[string]any{"requestId": r.ID, "userId": r.FromUserID, "status": "expired"})
		if err = appendUserBusinessEvent(ctx, tx, r.FromUserID, "friend.request.updated", fromPayload, at); err != nil {
			return nil, false, err
		}
		if err = appendUserBusinessEvent(ctx, tx, r.ToUserID, "friend.request.updated", toPayload, at); err != nil {
			return nil, false, err
		}
		pushPayload, _ := json.Marshal(map[string]any{"requestId": r.ID, "status": "expired"})
		if err = insertPrivatePush(ctx, tx, r.FromUserID, "friend.request.updated", pushPayload); err != nil {
			return nil, false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return r, false, ErrConflict
	}
	target := map[string]string{"accept": "accepted", "reject": "rejected", "cancel": "cancelled"}[action]
	if target == "" {
		return nil, false, ErrUnsupported
	}
	authorized := (action == "cancel" && uid == r.FromUserID) || (action != "cancel" && uid == r.ToUserID)
	if !authorized {
		return nil, false, ErrForbidden
	}
	if r.Status == target {
		return r, true, tx.Commit(ctx)
	}
	if r.Status != "pending" {
		return nil, false, ErrConflict
	}
	if action == "accept" {
		var blocked bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_blocks WHERE (user_id=$1 AND blocked_user_id=$2) OR (user_id=$2 AND blocked_user_id=$1))`, r.FromUserID, r.ToUserID).Scan(&blocked); err != nil {
			return nil, false, err
		}
		if blocked {
			return nil, false, ErrForbidden
		}
		if _, err = tx.Exec(ctx, `INSERT INTO im_friendships(user_id,friend_user_id) VALUES($1,$2),($2,$1) ON CONFLICT DO NOTHING`, r.FromUserID, r.ToUserID); err != nil {
			return nil, false, err
		}
		if err = enqueueWukongOutbox(ctx, tx, "friend-allow:"+r.ID, wukong.OperationFriendAllow, "friendship", friendPairKey(r.FromUserID, r.ToUserID), map[string]string{"user_id": r.FromUserID, "friend_id": r.ToUserID}); err != nil {
			return nil, false, err
		}
	}
	r, err = scanFriendRequest(tx.QueryRow(ctx, `UPDATE im_friend_requests SET status=$2,resolved_at=$3,updated_at=$3 WHERE id=$1 RETURNING `+friendRequestColumns(""), id, target, at))
	if err != nil {
		return nil, false, err
	}
	typ := "friend.request.updated"
	if target == "accepted" {
		typ = "friend.accepted"
	}
	fromPayload, _ := json.Marshal(map[string]any{"requestId": id, "userId": r.ToUserID, "status": target})
	toPayload, _ := json.Marshal(map[string]any{"requestId": id, "userId": r.FromUserID, "status": target})
	if err = appendUserBusinessEvent(ctx, tx, r.FromUserID, typ, fromPayload, at); err != nil {
		return nil, false, err
	}
	if err = appendUserBusinessEvent(ctx, tx, r.ToUserID, typ, toPayload, at); err != nil {
		return nil, false, err
	}
	notifyUser := r.FromUserID
	if target == "cancelled" {
		notifyUser = r.ToUserID
	}
	pushPayload, _ := json.Marshal(map[string]any{"requestId": id, "status": target})
	if err = insertPrivatePush(ctx, tx, notifyUser, "friend.request.updated", pushPayload); err != nil {
		return nil, false, err
	}
	return r, false, tx.Commit(ctx)
}

func (p *Postgres) DeleteFriend(ctx context.Context, uid, target string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "friend:"+friendPairKey(uid, target)); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM im_friendships WHERE (user_id=$1 AND friend_user_id=$2) OR (user_id=$2 AND friend_user_id=$1)`, uid, target)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	aPayload, _ := json.Marshal(map[string]any{"userId": target})
	bPayload, _ := json.Marshal(map[string]any{"userId": uid})
	if err = appendUserBusinessEvent(ctx, tx, uid, "friend.removed", aPayload, at); err != nil {
		return err
	}
	if err = appendUserBusinessEvent(ctx, tx, target, "friend.removed", bPayload, at); err != nil {
		return err
	}
	if err = enqueueWukongOutbox(ctx, tx, fmt.Sprintf("friend-remove:%s:%d", friendPairKey(uid, target), at.UnixNano()), wukong.OperationFriendRemove, "friendship", friendPairKey(uid, target), map[string]string{"user_id": uid, "friend_id": target}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) UpdateFriendMetadata(ctx context.Context, uid, target string, metadata FriendMetadata, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE im_friendships SET remark=$3,tags=$4,updated_at=$5 WHERE user_id=$1 AND friend_user_id=$2`, uid, target, metadata.Remark, metadata.Tags, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	payload, _ := json.Marshal(map[string]any{"userId": target, "remark": metadata.Remark, "tags": metadata.Tags})
	if err = appendUserBusinessEvent(ctx, tx, uid, "friend.metadata.updated", payload, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) SetFriendBlock(ctx context.Context, uid, target string, blocked bool, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1)`, target).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if blocked {
		if _, err = tx.Exec(ctx, `INSERT INTO im_blocks(user_id,blocked_user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, uid, target); err != nil {
			return err
		}
		tag, e := tx.Exec(ctx, `DELETE FROM im_friendships WHERE (user_id=$1 AND friend_user_id=$2) OR (user_id=$2 AND friend_user_id=$1)`, uid, target)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE im_friend_requests SET status='cancelled',resolved_at=$3,updated_at=$3 WHERE status='pending' AND ((from_user_id=$1 AND to_user_id=$2) OR (from_user_id=$2 AND to_user_id=$1))`, uid, target, at); e != nil {
			return e
		}
		if tag.RowsAffected() > 0 {
			payload, _ := json.Marshal(map[string]any{"userId": uid})
			if e = appendUserBusinessEvent(ctx, tx, target, "friend.removed", payload, at); e != nil {
				return e
			}
		}
	} else if _, err = tx.Exec(ctx, `DELETE FROM im_blocks WHERE user_id=$1 AND blocked_user_id=$2`, uid, target); err != nil {
		return err
	}
	operation := wukong.OperationFriendUnblock
	if blocked {
		operation = wukong.OperationFriendBlock
	}
	if err = enqueueWukongOutbox(ctx, tx, fmt.Sprintf("%s:%s:%d", operation, friendPairKey(uid, target), at.UnixNano()), operation, "friendship", friendPairKey(uid, target), map[string]string{"user_id": uid, "friend_id": target}); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"userId": target, "blocked": blocked})
	if err = appendUserBusinessEvent(ctx, tx, uid, "block.updated", payload, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ExpireFriendRequests(ctx context.Context, at time.Time, limit int) ([]*model.FriendRequest, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `WITH due AS (SELECT id FROM im_friend_requests WHERE status='pending' AND expires_at<=$1 ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT $2)
		UPDATE im_friend_requests r SET status='expired',resolved_at=r.expires_at,updated_at=$1 FROM due WHERE r.id=due.id RETURNING `+friendRequestColumns("r."), at, limit)
	if err != nil {
		return nil, err
	}
	var items []*model.FriendRequest
	for rows.Next() {
		r, scanErr := scanFriendRequest(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		items = append(items, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, r := range items {
		fromPayload, _ := json.Marshal(map[string]any{"requestId": r.ID, "userId": r.ToUserID, "status": "expired"})
		toPayload, _ := json.Marshal(map[string]any{"requestId": r.ID, "userId": r.FromUserID, "status": "expired"})
		if err = appendUserBusinessEvent(ctx, tx, r.FromUserID, "friend.request.updated", fromPayload, at); err != nil {
			return nil, err
		}
		if err = appendUserBusinessEvent(ctx, tx, r.ToUserID, "friend.request.updated", toPayload, at); err != nil {
			return nil, err
		}
		pushPayload, _ := json.Marshal(map[string]any{"requestId": r.ID, "status": "expired"})
		if err = insertPrivatePush(ctx, tx, r.FromUserID, "friend.request.updated", pushPayload); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

const groupProfileColumns = `g.conversation_id,g.owner_id,c.title,c.avatar_url,g.announcement,g.announcement_version,r.read_at,g.join_policy,g.allow_member_add_friend,g.all_muted_until,COALESCE(g.qr_token,''),g.qr_expires_at,g.dissolved_at,g.updated_at`

func scanGroupProfile(row callRow) (*model.GroupProfile, error) {
	g := &model.GroupProfile{}
	err := row.Scan(&g.ConversationID, &g.OwnerID, &g.Name, &g.AvatarURL, &g.Announcement, &g.AnnouncementVersion, &g.AnnouncementReadAt, &g.JoinPolicy, &g.AllowMemberAddFriend, &g.AllMutedUntil, &g.QRToken, &g.QRExpiresAt, &g.DissolvedAt, &g.UpdatedAt)
	return g, err
}
func groupInviteColumns(prefix string) string {
	return prefix + `id,` + prefix + `conversation_id,` + prefix + `inviter_id,` + prefix + `invitee_id,` + prefix + `source,` + prefix + `status,` + prefix + `created_at,` + prefix + `expires_at,` + prefix + `updated_at,` + prefix + `resolved_at`
}
func scanGroupInvite(row callRow) (*model.GroupInvite, error) {
	i := &model.GroupInvite{}
	err := row.Scan(&i.ID, &i.ConversationID, &i.InviterID, &i.InviteeID, &i.Source, &i.Status, &i.CreatedAt, &i.ExpiresAt, &i.UpdatedAt, &i.ResolvedAt)
	return i, err
}
func groupActor(ctx context.Context, tx pgx.Tx, cid, uid string) (string, string, *time.Time, error) {
	var role, owner string
	var dissolved *time.Time
	err := tx.QueryRow(ctx, `SELECT COALESCE(m.role,''),g.owner_id,g.dissolved_at FROM im_groups g LEFT JOIN im_members m ON m.conversation_id=g.conversation_id AND m.user_id=$2 WHERE g.conversation_id=$1`, cid, uid).Scan(&role, &owner, &dissolved)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil, ErrNotFound
	}
	return role, owner, dissolved, err
}
func emitGroupSystem(ctx context.Context, tx pgx.Tx, cid, actor, event string, data map[string]any, at time.Time) error {
	payload, _ := json.Marshal(map[string]any{"conversationId": cid, "event": event, "actorId": actor, "data": data})
	var actorExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1)`, actor).Scan(&actorExists); err != nil {
		return err
	}
	if actorExists {
		clientMsgNo, err := secureOpaqueToken("group-system-")
		if err != nil {
			return err
		}
		if err = enqueueWukongOutbox(ctx, tx, "stored-message:"+clientMsgNo, wukong.OperationStoredMessage, "group_message", clientMsgNo, wukong.StoredMessageRequest{
			ClientMsgNo: clientMsgNo, FromUID: actor, ChannelID: cid, ChannelType: wukong.ChannelGroup,
			Payload: map[string]any{
				"type": wukong.ContentTypeSystemEvent, "schemaVersion": 1,
				"event": event, "actorId": actor, "data": data, "digest": "[群系统消息]",
			},
		}); err != nil {
			return err
		}
	}
	if _, err := appendMemberBusinessEvent(ctx, tx, cid, "group.system", payload, at); err != nil {
		return err
	}
	meta, _ := json.Marshal(data)
	_, err := tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,$3,'group',$4,$5,$6)`, "aud_group_"+strconv.FormatInt(at.UnixNano(), 36), actor, event, cid, meta, at)
	return err
}

func (p *Postgres) GetOrCreateDirectConversation(ctx context.Context, uid, other, conversationID string, at time.Time) (*model.Conversation, bool, error) {
	uid, other = strings.TrimSpace(uid), strings.TrimSpace(other)
	if uid == "" || other == "" || uid == other || strings.TrimSpace(conversationID) == "" {
		return nil, false, ErrConflict
	}
	left, right := uid, other
	if left > right {
		left, right = right, left
	}
	pairKey := left + ":" + right
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "direct:"+pairKey); err != nil {
		return nil, false, err
	}
	conversation := &model.Conversation{}
	err = tx.QueryRow(ctx, `SELECT conversation.id,conversation.kind,conversation.title,conversation.avatar_url,
		conversation.current_seq,conversation.last_message_seq,conversation.created_at,conversation.updated_at
		FROM im_direct_index direct_index JOIN im_conversations conversation ON conversation.id=direct_index.conversation_id
		WHERE direct_index.pair_key=$1`, pairKey).Scan(
		&conversation.ID, &conversation.Type, &conversation.Title, &conversation.AvatarURL,
		&conversation.Seq, &conversation.LastMessageSeq, &conversation.CreatedAt, &conversation.UpdatedAt,
	)
	if err == nil {
		return conversation, false, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	var validUsers int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_users WHERE id=ANY($1::text[]) AND deleted_at IS NULL AND NOT banned`, []string{uid, other}).Scan(&validUsers); err != nil {
		return nil, false, err
	}
	if validUsers != 2 {
		return nil, false, ErrNotFound
	}
	var blocked bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_blocks WHERE (user_id=$1 AND blocked_user_id=$2) OR (user_id=$2 AND blocked_user_id=$1))`, uid, other).Scan(&blocked); err != nil {
		return nil, false, err
	}
	if blocked {
		return nil, false, ErrForbidden
	}
	conversation = &model.Conversation{ID: conversationID, Type: "direct", CreatedAt: at, UpdatedAt: at}
	if _, err = tx.Exec(ctx, `INSERT INTO im_conversations(id,kind,created_at,updated_at) VALUES($1,'direct',$2,$2)`, conversationID, at); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_direct_index(pair_key,conversation_id) VALUES($1,$2)`, pairKey, conversationID); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'member',$4),($1,$3,'member',$4)`, conversationID, uid, other, at); err != nil {
		return nil, false, err
	}
	payload, _ := json.Marshal(map[string]any{"conversation": conversation})
	for _, userID := range []string{uid, other} {
		if err = appendUserBusinessEvent(ctx, tx, userID, "conversation.created", payload, at); err != nil {
			return nil, false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return conversation, true, nil
}

func (p *Postgres) CreateGroupRecord(ctx context.Context, cid, owner, name string, members []string, at time.Time) (*model.Conversation, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	c := &model.Conversation{ID: cid, Type: "group", Title: name, CreatedAt: at, UpdatedAt: at}
	if _, err = tx.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,created_at,updated_at)VALUES($1,'group',$2,$3,$3)`, cid, name, at); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_groups(conversation_id,owner_id,updated_at)VALUES($1,$2,$3)`, cid, owner, at); err != nil {
		return nil, err
	}
	all := append([]string{owner}, members...)
	if _, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) SELECT $1,id,CASE WHEN id=$2 THEN 'owner' ELSE 'member' END,$3 FROM im_users WHERE id=ANY($4::text[]) ON CONFLICT DO NOTHING`, cid, owner, at, all); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(map[string]any{"conversation": c})
	if _, err = appendMemberBusinessEvent(ctx, tx, cid, "group.created", raw, at); err != nil {
		return nil, err
	}
	if err = enqueueWukongChannelReconcile(ctx, tx, cid, "created", at); err != nil {
		return nil, err
	}
	return c, tx.Commit(ctx)
}
func (p *Postgres) GetGroupProfile(ctx context.Context, uid, cid string) (*model.GroupProfile, error) {
	g, err := scanGroupProfile(p.pool.QueryRow(ctx, `SELECT `+groupProfileColumns+` FROM im_groups g JOIN im_conversations c ON c.id=g.conversation_id JOIN im_members m ON m.conversation_id=g.conversation_id AND m.user_id=$2 LEFT JOIN im_group_announcement_reads r ON r.conversation_id=g.conversation_id AND r.user_id=$2 AND r.announcement_version=g.announcement_version WHERE g.conversation_id=$1`, cid, uid))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return g, err
}
func (p *Postgres) UpdateGroupProfile(ctx context.Context, actor, cid string, u GroupProfileUpdate, at time.Time) (*model.GroupProfile, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	role, _, dissolved, err := groupActor(ctx, tx, cid, actor)
	if err != nil {
		return nil, err
	}
	if dissolved != nil {
		return nil, ErrConflict
	}
	if role != "owner" && role != "admin" {
		return nil, ErrForbidden
	}
	if role != "owner" && (u.JoinPolicy != nil || u.AllowMemberAddFriend != nil || u.AllMutedUntil != nil || u.RotateQR) {
		return nil, ErrForbidden
	}
	var avatarURL *string
	if u.AvatarMediaID != nil {
		value := ""
		if *u.AvatarMediaID != "" {
			var owner, status, mime string
			if err = tx.QueryRow(ctx, `SELECT owner_id,status,mime FROM im_media WHERE id=$1`, *u.AvatarMediaID).Scan(&owner, &status, &mime); errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			} else if err != nil {
				return nil, err
			}
			if owner != actor || status != "ready" {
				return nil, ErrForbidden
			}
			if !strings.HasPrefix(strings.ToLower(mime), "image/") {
				return nil, ErrUnsupported
			}
			value = "/v2/media/" + *u.AvatarMediaID
		}
		avatarURL = &value
	}
	if _, err = tx.Exec(ctx, `UPDATE im_conversations SET title=COALESCE($2,title),avatar_url=COALESCE($3,avatar_url),updated_at=$4 WHERE id=$1`, cid, u.Name, avatarURL, at); err != nil {
		return nil, err
	}
	var token any
	if u.RotateQR {
		value, tokenErr := secureOpaqueToken("gqr_")
		if tokenErr != nil {
			return nil, tokenErr
		}
		token = value
	}
	if _, err = tx.Exec(ctx, `UPDATE im_groups SET join_policy=COALESCE($2,join_policy),allow_member_add_friend=COALESCE($3,allow_member_add_friend),all_muted_until=CASE WHEN $4::boolean THEN $5 ELSE all_muted_until END,qr_token=COALESCE($6,qr_token),qr_expires_at=CASE WHEN $6::text IS NULL THEN qr_expires_at ELSE $7 END,updated_at=$8 WHERE conversation_id=$1`, cid, u.JoinPolicy, u.AllowMemberAddFriend, u.AllMutedUntil != nil, u.AllMutedUntil, token, at.Add(24*time.Hour), at); err != nil {
		return nil, err
	}
	if err = emitGroupSystem(ctx, tx, cid, actor, "group.profile.updated", map[string]any{}, at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.GetGroupProfile(ctx, actor, cid)
}
func (p *Postgres) SetGroupAnnouncement(ctx context.Context, actor, cid, content string, at time.Time) (*model.GroupProfile, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	role, _, dissolved, err := groupActor(ctx, tx, cid, actor)
	if err != nil {
		return nil, err
	}
	if dissolved != nil {
		return nil, ErrConflict
	}
	if role != "owner" && role != "admin" {
		return nil, ErrForbidden
	}
	if _, err = tx.Exec(ctx, `UPDATE im_groups SET announcement=$2,announcement_version=announcement_version+1,updated_at=$3 WHERE conversation_id=$1`, cid, content, at); err != nil {
		return nil, err
	}
	if err = emitGroupSystem(ctx, tx, cid, actor, "group.announcement.updated", map[string]any{}, at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.GetGroupProfile(ctx, actor, cid)
}
func (p *Postgres) MarkGroupAnnouncementRead(ctx context.Context, uid, cid string, at time.Time) error {
	tag, err := p.pool.Exec(ctx, `INSERT INTO im_group_announcement_reads(conversation_id,user_id,announcement_version,read_at) SELECT g.conversation_id,$2,g.announcement_version,$3 FROM im_groups g JOIN im_members m ON m.conversation_id=g.conversation_id AND m.user_id=$2 WHERE g.conversation_id=$1 ON CONFLICT DO NOTHING`, cid, uid, at)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
func (p *Postgres) CreateGroupInvite(ctx context.Context, i *model.GroupInvite) (*model.GroupInvite, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	role, _, dissolved, err := groupActor(ctx, tx, i.ConversationID, i.InviterID)
	if err != nil {
		return nil, false, err
	}
	if dissolved != nil {
		return nil, false, ErrConflict
	}
	if role != "owner" && role != "admin" {
		return nil, false, ErrForbidden
	}
	var member bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_members WHERE conversation_id=$1 AND user_id=$2)`, i.ConversationID, i.InviteeID).Scan(&member); err != nil {
		return nil, false, err
	}
	if member {
		return nil, false, ErrConflict
	}
	existing, e := scanGroupInvite(tx.QueryRow(ctx, `SELECT `+groupInviteColumns("")+` FROM im_group_invites WHERE conversation_id=$1 AND invitee_id=$2 AND status='pending'`, i.ConversationID, i.InviteeID))
	if e == nil {
		return existing, true, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return nil, false, e
	}
	created, err := scanGroupInvite(tx.QueryRow(ctx, `INSERT INTO im_group_invites(id,conversation_id,inviter_id,invitee_id,source,status,created_at,expires_at,updated_at)VALUES($1,$2,$3,$4,$5,'pending',$6,$7,$6)RETURNING `+groupInviteColumns(""), i.ID, i.ConversationID, i.InviterID, i.InviteeID, i.Source, i.CreatedAt, i.ExpiresAt))
	if err != nil {
		return nil, false, err
	}
	payload, _ := json.Marshal(map[string]any{"inviteId": i.ID, "conversationId": i.ConversationID, "inviterId": i.InviterID})
	if err = appendUserBusinessEvent(ctx, tx, i.InviteeID, "group.invite", payload, i.CreatedAt); err != nil {
		return nil, false, err
	}
	if err = insertPrivatePush(ctx, tx, i.InviteeID, "group.invite", payload); err != nil {
		return nil, false, err
	}
	return created, false, tx.Commit(ctx)
}
func (p *Postgres) TransitionGroupInvite(ctx context.Context, id, uid, action string, at time.Time) (*model.GroupInvite, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	i, err := scanGroupInvite(tx.QueryRow(ctx, `SELECT `+groupInviteColumns("")+` FROM im_group_invites WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	target := map[string]string{"accept": "accepted", "reject": "rejected", "cancel": "cancelled"}[action]
	if target == "" {
		return nil, false, ErrUnsupported
	}
	if (action == "cancel" && uid != i.InviterID) || (action != "cancel" && uid != i.InviteeID) {
		return nil, false, ErrForbidden
	}
	if i.Status == target {
		return i, true, tx.Commit(ctx)
	}
	if i.Status != "pending" || !at.Before(i.ExpiresAt) {
		return nil, false, ErrConflict
	}
	if target == "accepted" {
		if _, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at)VALUES($1,$2,'member',$3)ON CONFLICT DO NOTHING`, i.ConversationID, i.InviteeID, at); err != nil {
			return nil, false, err
		}
		if err = enqueueWukongChannelReconcile(ctx, tx, i.ConversationID, "invite-accepted", at); err != nil {
			return nil, false, err
		}
	}
	i, err = scanGroupInvite(tx.QueryRow(ctx, `UPDATE im_group_invites SET status=$2,resolved_at=$3,updated_at=$3 WHERE id=$1 RETURNING `+groupInviteColumns(""), id, target, at))
	if err != nil {
		return nil, false, err
	}
	if err = emitGroupSystem(ctx, tx, i.ConversationID, uid, "group.invite."+target, map[string]any{"userId": i.InviteeID}, at); err != nil {
		return nil, false, err
	}
	return i, false, tx.Commit(ctx)
}
func (p *Postgres) JoinGroupByQR(ctx context.Context, uid, token string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var cid, policy string
	var dissolved *time.Time
	err = tx.QueryRow(ctx, `SELECT conversation_id,join_policy,dissolved_at FROM im_groups WHERE qr_token=$1 AND qr_expires_at>$2 FOR UPDATE`, token, at).Scan(&cid, &policy, &dissolved)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if dissolved != nil || policy != "qr" {
		return ErrForbidden
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at)VALUES($1,$2,'member',$3)ON CONFLICT DO NOTHING`, cid, uid, at); err != nil {
		return err
	}
	if err = emitGroupSystem(ctx, tx, cid, uid, "group.member.joined", map[string]any{"userId": uid, "source": "qr"}, at); err != nil {
		return err
	}
	if err = enqueueWukongChannelReconcile(ctx, tx, cid, "qr-joined", at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) AddGroupMembers(ctx context.Context, actor, cid string, ids []string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	role, _, dissolved, err := groupActor(ctx, tx, cid, actor)
	if err != nil {
		return err
	}
	if dissolved != nil {
		return ErrConflict
	}
	if role != "owner" && role != "admin" {
		return ErrForbidden
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at)SELECT $1,id,'member',$3 FROM im_users WHERE id=ANY($2::text[])ON CONFLICT DO NOTHING`, cid, ids, at); err != nil {
		return err
	}
	if err = emitGroupSystem(ctx, tx, cid, actor, "group.members.added", map[string]any{"userIds": ids}, at); err != nil {
		return err
	}
	if err = enqueueWukongChannelReconcile(ctx, tx, cid, "members-added", at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) ApplyGroupMemberAction(ctx context.Context, a GroupMemberAction) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	role, owner, dissolved, err := groupActor(ctx, tx, a.ConversationID, a.ActorID)
	if err != nil {
		return err
	}
	if dissolved != nil {
		return ErrConflict
	}
	var targetRole string
	if a.TargetID != "" {
		_ = tx.QueryRow(ctx, `SELECT role FROM im_members WHERE conversation_id=$1 AND user_id=$2`, a.ConversationID, a.TargetID).Scan(&targetRole)
	}
	switch a.Action {
	case "leave":
		if a.ActorID == owner {
			return ErrForbidden
		}
		_, err = tx.Exec(ctx, `DELETE FROM im_members WHERE conversation_id=$1 AND user_id=$2`, a.ConversationID, a.ActorID)
		a.TargetID = a.ActorID
	case "remove":
		if role != "owner" && role != "admin" {
			return ErrForbidden
		}
		if targetRole == "owner" || (role == "admin" && targetRole == "admin") {
			return ErrForbidden
		}
		_, err = tx.Exec(ctx, `DELETE FROM im_members WHERE conversation_id=$1 AND user_id=$2`, a.ConversationID, a.TargetID)
	case "role":
		if role != "owner" || a.TargetID == owner || (a.Role != "member" && a.Role != "admin") {
			return ErrForbidden
		}
		_, err = tx.Exec(ctx, `UPDATE im_members SET role=$3 WHERE conversation_id=$1 AND user_id=$2`, a.ConversationID, a.TargetID, a.Role)
	case "transfer":
		if role != "owner" || targetRole == "" || a.TargetID == a.ActorID {
			return ErrForbidden
		}
		_, err = tx.Exec(ctx, `UPDATE im_members SET role=CASE WHEN user_id=$2 THEN 'member' WHEN user_id=$3 THEN 'owner' ELSE role END WHERE conversation_id=$1 AND user_id=ANY($4::text[])`, a.ConversationID, a.ActorID, a.TargetID, []string{a.ActorID, a.TargetID})
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE im_groups SET owner_id=$2,updated_at=$3 WHERE conversation_id=$1`, a.ConversationID, a.TargetID, a.At)
		}
	case "mute":
		if role != "owner" && role != "admin" {
			return ErrForbidden
		}
		if targetRole == "owner" || (role == "admin" && targetRole == "admin") {
			return ErrForbidden
		}
		_, err = tx.Exec(ctx, `UPDATE im_members SET muted_until=$3 WHERE conversation_id=$1 AND user_id=$2`, a.ConversationID, a.TargetID, a.MutedUntil)
	case "nickname":
		if a.ActorID != a.TargetID {
			return ErrForbidden
		}
		_, err = tx.Exec(ctx, `UPDATE im_members SET group_nickname=$3 WHERE conversation_id=$1 AND user_id=$2`, a.ConversationID, a.TargetID, a.Nickname)
	default:
		return ErrUnsupported
	}
	if err != nil {
		return err
	}
	if err = emitGroupSystem(ctx, tx, a.ConversationID, a.ActorID, "group.member."+a.Action, map[string]any{"userId": a.TargetID, "role": a.Role}, a.At); err != nil {
		return err
	}
	if a.Action == "leave" || a.Action == "remove" {
		if err = enqueueWukongChannelReconcile(ctx, tx, a.ConversationID, "member-"+a.Action, a.At); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (p *Postgres) DisbandGroupRecord(ctx context.Context, actor, cid, reason string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	role, _, dissolved, err := groupActor(ctx, tx, cid, actor)
	if err != nil {
		return err
	}
	if dissolved != nil {
		return tx.Commit(ctx)
	}
	var actorExists bool
	_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1)`, actor).Scan(&actorExists)
	if actorExists && role != "owner" {
		return ErrForbidden
	}
	if err = emitGroupSystem(ctx, tx, cid, actor, "group.disbanded", map[string]any{"reason": reason}, at); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_groups SET dissolved_at=$2,updated_at=$2 WHERE conversation_id=$1`, cid, at); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM im_members WHERE conversation_id=$1`, cid); err != nil {
		return err
	}
	if err = enqueueWukongChannelReconcile(ctx, tx, cid, "disbanded", at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const announcementColumns = `id,title,content,status,pinned,target_type,target_user_ids,scheduled_at,published_at,withdrawn_at,push_on_publish,created_by,created_at,updated_at`

func scanAnnouncement(row interface{ Scan(...any) error }) (*model.Announcement, error) {
	a := &model.Announcement{}
	var targets []byte
	if err := row.Scan(&a.ID, &a.Title, &a.Content, &a.Status, &a.Pinned, &a.TargetType, &targets, &a.ScheduledAt, &a.PublishedAt, &a.WithdrawnAt, &a.PushOnPublish, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(targets, &a.TargetUserIDs); err != nil {
		return nil, err
	}
	return a, nil
}

func enqueueAnnouncementPush(ctx context.Context, tx pgx.Tx, a *model.Announcement, at time.Time) error {
	payload, _ := json.Marshal(map[string]any{"announcementId": a.ID, "title": a.Title, "content": a.Content})
	if a.TargetType == "all" {
		_, err := tx.Exec(ctx, `INSERT INTO im_push_outbox(user_id,event_type,payload) SELECT id,'announcement.published',$1 FROM im_users WHERE NOT banned`, payload)
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO im_push_outbox(user_id,event_type,payload) SELECT id,'announcement.published',$1 FROM im_users WHERE NOT banned AND id=ANY($2::text[])`, payload, a.TargetUserIDs)
	return err
}

func insertAnnouncementAudit(ctx context.Context, tx pgx.Tx, actor, action, id string, metadata any, at time.Time) error {
	meta, _ := json.Marshal(metadata)
	auditID := "aud_announcement_" + strconv.FormatInt(at.UnixNano(), 36) + "_" + strings.ReplaceAll(action, ".", "_") + "_" + id
	_, err := tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,$3,'announcement',$4,$5,$6)`, auditID, actor, action, id, meta, at)
	return err
}

func promoteScheduledAnnouncements(ctx context.Context, tx pgx.Tx, at time.Time) (int, error) {
	rows, err := tx.Query(ctx, `UPDATE im_announcements SET status='published',published_at=scheduled_at,updated_at=$1 WHERE status='scheduled' AND scheduled_at<=$1 RETURNING `+announcementColumns, at)
	if err != nil {
		return 0, err
	}
	var due []*model.Announcement
	for rows.Next() {
		a, scanErr := scanAnnouncement(rows)
		if scanErr != nil {
			rows.Close()
			return 0, scanErr
		}
		due = append(due, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for index, a := range due {
		if a.PushOnPublish {
			if err = enqueueAnnouncementPush(ctx, tx, a, at); err != nil {
				return 0, err
			}
		}
		if err = insertAnnouncementAudit(ctx, tx, "system", "announcement.published", a.ID, map[string]any{"scheduled": true, "push": a.PushOnPublish, "sequence": index}, at.Add(time.Duration(index)*time.Nanosecond)); err != nil {
			return 0, err
		}
		if err = appendAnnouncementAudienceEvent(ctx, tx, a, "announcement.published", at); err != nil {
			return 0, err
		}
	}
	return len(due), nil
}

func (p *Postgres) PromoteDueAnnouncements(ctx context.Context, at time.Time) (int, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	count, err := promoteScheduledAnnouncements(ctx, tx, at)
	if err != nil {
		return 0, err
	}
	return count, tx.Commit(ctx)
}

func (p *Postgres) ListAnnouncements(ctx context.Context, uid string, at time.Time) ([]*model.Announcement, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = promoteScheduledAnnouncements(ctx, tx, at); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+announcementColumns+`,r.read_at FROM im_announcements a LEFT JOIN im_announcement_reads r ON r.announcement_id=a.id AND r.user_id=$1
		WHERE a.status='published' AND (a.target_type='all' OR a.target_user_ids ? $1) ORDER BY a.pinned DESC,a.published_at DESC,a.id LIMIT 100`, uid)
	if err != nil {
		return nil, err
	}
	var items []*model.Announcement
	for rows.Next() {
		a := &model.Announcement{}
		var targets []byte
		if err = rows.Scan(&a.ID, &a.Title, &a.Content, &a.Status, &a.Pinned, &a.TargetType, &targets, &a.ScheduledAt, &a.PublishedAt, &a.WithdrawnAt, &a.PushOnPublish, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt, &a.ReadAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(targets, &a.TargetUserIDs); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *Postgres) MarkAnnouncementRead(ctx context.Context, uid, id string, at time.Time) error {
	result, err := p.pool.Exec(ctx, `INSERT INTO im_announcement_reads(announcement_id,user_id,read_at)
		SELECT id,$2,$3 FROM im_announcements WHERE id=$1 AND status='published' AND (target_type='all' OR target_user_ids ? $2)
		ON CONFLICT(announcement_id,user_id) DO UPDATE SET read_at=LEAST(im_announcement_reads.read_at,excluded.read_at)`, id, uid, at)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) ListAdminAnnouncements(ctx context.Context, q, status, cursor string, limit int, at time.Time) ([]*model.Announcement, int64, string, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, 0, "", err
	}
	defer tx.Rollback(ctx)
	if _, err = promoteScheduledAnnouncements(ctx, tx, at); err != nil {
		return nil, 0, "", err
	}
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + q + "%"
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_announcements WHERE ($1='' OR status=$1) AND ($2='' OR id ILIKE $3 OR title ILIKE $3 OR content ILIKE $3)`, status, q, pattern).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := tx.Query(ctx, `SELECT `+announcementColumns+` FROM im_announcements WHERE ($1='' OR status=$1) AND ($2='' OR id ILIKE $3 OR title ILIKE $3 OR content ILIKE $3) ORDER BY pinned DESC,created_at DESC,id LIMIT $4 OFFSET $5`, status, q, pattern, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	var items []*model.Announcement
	for rows.Next() {
		a, scanErr := scanAnnouncement(rows)
		if scanErr != nil {
			rows.Close()
			return nil, 0, "", scanErr
		}
		items = append(items, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func (p *Postgres) CreateAnnouncement(ctx context.Context, in AnnouncementInput, at time.Time) (*model.Announcement, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	targets, _ := json.Marshal(in.TargetUserIDs)
	a, err := scanAnnouncement(tx.QueryRow(ctx, `INSERT INTO im_announcements(id,title,content,status,pinned,target_type,target_user_ids,scheduled_at,push_on_publish,created_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11) RETURNING `+announcementColumns, in.ID, in.Title, in.Content, in.Status, in.Pinned, in.TargetType, targets, in.ScheduledAt, in.PushOnPublish, in.ActorID, at))
	if err != nil {
		return nil, err
	}
	if err = insertAnnouncementAudit(ctx, tx, in.ActorID, "announcement.created", in.ID, map[string]any{"status": in.Status, "targetType": in.TargetType}, at); err != nil {
		return nil, err
	}
	return a, tx.Commit(ctx)
}
func (p *Postgres) UpdateAnnouncement(ctx context.Context, id string, in AnnouncementInput, at time.Time) (*model.Announcement, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	targets, _ := json.Marshal(in.TargetUserIDs)
	a, err := scanAnnouncement(tx.QueryRow(ctx, `UPDATE im_announcements SET title=$2,content=$3,status=$4,pinned=$5,target_type=$6,target_user_ids=$7,scheduled_at=$8,push_on_publish=$9,updated_at=$10 WHERE id=$1 AND status IN ('draft','scheduled') RETURNING `+announcementColumns, id, in.Title, in.Content, in.Status, in.Pinned, in.TargetType, targets, in.ScheduledAt, in.PushOnPublish, at))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if err = insertAnnouncementAudit(ctx, tx, in.ActorID, "announcement.updated", id, map[string]any{"status": in.Status, "targetType": in.TargetType}, at); err != nil {
		return nil, err
	}
	return a, tx.Commit(ctx)
}
func (p *Postgres) PublishAnnouncement(ctx context.Context, id, actor string, push bool, at time.Time) (*model.Announcement, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	a, err := scanAnnouncement(tx.QueryRow(ctx, `UPDATE im_announcements SET status='published',published_at=COALESCE(published_at,$2),scheduled_at=NULL,push_on_publish=$3,updated_at=$2 WHERE id=$1 AND status IN ('draft','scheduled') RETURNING `+announcementColumns, id, at, push))
	if errors.Is(err, pgx.ErrNoRows) {
		a, err = scanAnnouncement(tx.QueryRow(ctx, `SELECT `+announcementColumns+` FROM im_announcements WHERE id=$1 AND status='published'`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrConflict
		}
		if err != nil {
			return nil, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	if push {
		if err = enqueueAnnouncementPush(ctx, tx, a, at); err != nil {
			return nil, err
		}
	}
	if err = insertAnnouncementAudit(ctx, tx, actor, "announcement.published", id, map[string]any{"push": push}, at); err != nil {
		return nil, err
	}
	if err = appendAnnouncementAudienceEvent(ctx, tx, a, "announcement.published", at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return a, nil
}
func (p *Postgres) WithdrawAnnouncement(ctx context.Context, id, actor string, at time.Time) (*model.Announcement, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	a, err := scanAnnouncement(tx.QueryRow(ctx, `UPDATE im_announcements SET status='withdrawn',withdrawn_at=$2,updated_at=$2 WHERE id=$1 AND status IN ('published','scheduled') RETURNING `+announcementColumns, id, at))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if err = insertAnnouncementAudit(ctx, tx, actor, "announcement.withdrawn", id, map[string]any{}, at); err != nil {
		return nil, err
	}
	if err = appendAnnouncementAudienceEvent(ctx, tx, a, "announcement.withdrawn", at); err != nil {
		return nil, err
	}
	return a, tx.Commit(ctx)
}
func (p *Postgres) DeleteAnnouncement(ctx context.Context, id, actor string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM im_announcements WHERE id=$1 AND status IN ('draft','withdrawn')`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if err = insertAnnouncementAudit(ctx, tx, actor, "announcement.deleted", id, map[string]any{}, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
