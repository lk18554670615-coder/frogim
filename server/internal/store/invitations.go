package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/linli/im/server/internal/model"
)

const inviteAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

type InviteCode struct {
	ID                   string      `json:"id"`
	UserID               string      `json:"userId"`
	Code                 string      `json:"code"`
	Status               string      `json:"status"`
	Source               string      `json:"source"`
	SelfChangesUsed      int         `json:"selfChangesUsed"`
	SelfChangesRemaining int         `json:"selfChangesRemaining"`
	CreatedAt            time.Time   `json:"createdAt"`
	User                 *model.User `json:"user,omitempty"`
}

type InviteRelation struct {
	Invitee            *model.User `json:"invitee"`
	Inviter            *model.User `json:"inviter"`
	InviteCodeID       string      `json:"inviteCodeId"`
	InviteCode         string      `json:"inviteCode"`
	RegistrationMethod string      `json:"registrationMethod"`
	CreatedAt          time.Time   `json:"createdAt"`
}

type InviteCodePage struct {
	Items      []InviteCode `json:"items"`
	Total      int64        `json:"total"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

type InviteRelationPage struct {
	Items      []InviteRelation `json:"items"`
	Total      int64            `json:"total"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

func randomInviteCode() (string, error) {
	code := make([]byte, 10)
	// Rejection sampling avoids modulo bias while retaining a compact,
	// human-readable alphabet without 0/O/1/I/L.
	limit := byte(256 - (256 % len(inviteAlphabet)))
	for index := 0; index < len(code); {
		var sample [1]byte
		if _, err := rand.Read(sample[:]); err != nil {
			return "", err
		}
		if sample[0] >= limit {
			continue
		}
		code[index] = inviteAlphabet[int(sample[0])%len(inviteAlphabet)]
		index++
	}
	return string(code), nil
}

func inviteCodeID() (string, error) { return secureOpaqueToken("ic_") }

func scanUser(row pgx.Row) (*model.User, error) {
	u := &model.User{}
	err := row.Scan(&u.ID, &u.Phone, &u.Name, &u.Handle, &u.HandleChangeCount, &u.Signature,
		&u.AvatarMediaID, &u.AvatarURL, &u.AllowSearchByHandle, &u.AllowSearchByPhone,
		&u.Gender, &u.Banned, &u.CreatedAt)
	return u, err
}

const inviteUserColumns = `id,phone,name,COALESCE(handle,''),handle_change_count,signature,COALESCE(avatar_media_id,''),avatar_url,allow_search_by_handle,allow_search_by_phone,gender,banned,created_at`

func ensureInviteCodeTx(ctx context.Context, tx pgx.Tx, userID, source, actor string, at time.Time) (*InviteCode, error) {
	current := &InviteCode{}
	err := tx.QueryRow(ctx, `SELECT id,user_id,code,status,source,created_at FROM im_user_invite_codes WHERE user_id=$1 AND status IN ('active','disabled') FOR UPDATE`, userID).
		Scan(&current.ID, &current.UserID, &current.Code, &current.Status, &current.Source, &current.CreatedAt)
	if err == nil {
		return current, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	for attempt := 0; attempt < 32; attempt++ {
		code, codeErr := randomInviteCode()
		if codeErr != nil {
			return nil, codeErr
		}
		id, idErr := inviteCodeID()
		if idErr != nil {
			return nil, idErr
		}
		item := &InviteCode{ID: id, UserID: userID, Code: code, Status: "active", Source: source, CreatedAt: at}
		tag, insertErr := tx.Exec(ctx, `INSERT INTO im_user_invite_codes(id,user_id,code,status,source,created_by,created_at) VALUES($1,$2,$3,'active',$4,$5,$6) ON CONFLICT DO NOTHING`, id, userID, code, source, actor, at)
		if insertErr != nil {
			return nil, insertErr
		}
		if tag.RowsAffected() == 1 {
			return item, nil
		}
		// A concurrent request may have created the current row; return it.
		if lookupErr := tx.QueryRow(ctx, `SELECT id,user_id,code,status,source,created_at FROM im_user_invite_codes WHERE user_id=$1 AND status IN ('active','disabled')`, userID).
			Scan(&current.ID, &current.UserID, &current.Code, &current.Status, &current.Source, &current.CreatedAt); lookupErr == nil {
			return current, nil
		}
	}
	return nil, fmt.Errorf("generate unique invitation code: %w", ErrConflict)
}

func (p *Postgres) backfillInviteCodes(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT u.id FROM im_users u WHERE u.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM im_user_invite_codes c WHERE c.user_id=u.id AND c.status IN ('active','disabled')) ORDER BY u.id`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	at := time.Now().UTC()
	for _, id := range ids {
		if _, err = ensureInviteCodeTx(ctx, tx, id, "system", "system:migration", at); err != nil {
			return err
		}
	}
	var missing int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_users u WHERE u.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM im_user_invite_codes c WHERE c.user_id=u.id AND c.status IN ('active','disabled'))`).Scan(&missing); err != nil {
		return err
	}
	if missing != 0 {
		return fmt.Errorf("invitation code backfill incomplete: %d users", missing)
	}
	return nil
}

func resolveInviteCodeTx(ctx context.Context, tx pgx.Tx, code string) (*InviteCode, error) {
	item := &InviteCode{}
	err := tx.QueryRow(ctx, `SELECT c.id,c.user_id,c.code,c.status,c.source,c.created_at FROM im_user_invite_codes c JOIN im_users u ON u.id=c.user_id
		WHERE upper(c.code)=upper($1) AND c.status='active' AND u.deleted_at IS NULL AND NOT (u.banned AND (u.banned_until IS NULL OR u.banned_until>now())) FOR UPDATE OF c`, code).
		Scan(&item.ID, &item.UserID, &item.Code, &item.Status, &item.Source, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInviteInvalid
	}
	return item, err
}

func validateInviteMode(mode, code string) error {
	if mode != "disabled" && mode != "optional" && mode != "required" {
		return ErrInviteInvalid
	}
	if mode == "required" && code == "" {
		return ErrInviteRequired
	}
	return nil
}

func (p *Postgres) RegisterPasswordUserWithInvite(ctx context.Context, phone, name, id, hash string, created time.Time, mode, code string) (*model.User, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	code = strings.ToUpper(strings.TrimSpace(code))
	if err = validateInviteMode(mode, code); err != nil {
		return nil, err
	}
	var inviter *InviteCode
	if mode != "disabled" && code != "" {
		if inviter, err = resolveInviteCodeTx(ctx, tx, code); err != nil {
			return nil, err
		}
	}
	u, err := scanUser(tx.QueryRow(ctx, `INSERT INTO im_users(id,phone,name,handle,password_hash,password_updated_at,created_at) VALUES($1,$2,$3,'gg_'||left(md5($1),20),$4,$5,$5) RETURNING `+inviteUserColumns, id, phone, name, hash, created))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if _, err = ensureInviteCodeTx(ctx, tx, u.ID, "system", u.ID, created); err != nil {
		return nil, err
	}
	if inviter != nil {
		if _, err = tx.Exec(ctx, `INSERT INTO im_user_invite_relations(invitee_user_id,inviter_user_id,invite_code_id,registration_method,created_at) VALUES($1,$2,$3,'password',$4)`, u.ID, inviter.UserID, inviter.ID, created); err != nil {
			return nil, err
		}
	}
	metadata, _ := json.Marshal(map[string]any{"method": "phone_password", "inviteCodeId": func() string {
		if inviter != nil {
			return inviter.ID
		}
		return ""
	}(), "inviterId": func() string {
		if inviter != nil {
			return inviter.UserID
		}
		return ""
	}()})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'auth.register','user',$2,$3,$4)`, "aud_register_"+id, id, metadata, created); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return u, nil
}

func (p *Postgres) LoginOrCreateUserWithInvite(ctx context.Context, phone, name, id string, created time.Time, allowCreate bool, mode, code string) (*model.User, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	if existing, lookupErr := scanUser(tx.QueryRow(ctx, `SELECT `+inviteUserColumns+` FROM im_users WHERE phone=$1 FOR UPDATE`, phone)); lookupErr == nil {
		if existing.Banned {
			return nil, false, ErrForbidden
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return existing, false, nil
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return nil, false, lookupErr
	}
	if !allowCreate {
		return nil, false, ErrForbidden
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if err = validateInviteMode(mode, code); err != nil {
		return nil, false, err
	}
	var inviter *InviteCode
	if mode != "disabled" && code != "" {
		if inviter, err = resolveInviteCodeTx(ctx, tx, code); err != nil {
			return nil, false, err
		}
	}
	u, err := scanUser(tx.QueryRow(ctx, `INSERT INTO im_users(id,phone,name,handle,created_at) VALUES($1,$2,$3,'gg_'||left(md5($1),20),$4) ON CONFLICT(phone) DO NOTHING RETURNING `+inviteUserColumns, id, phone, name, created))
	if errors.Is(err, pgx.ErrNoRows) {
		u, err = scanUser(tx.QueryRow(ctx, `SELECT `+inviteUserColumns+` FROM im_users WHERE phone=$1`, phone))
		if err != nil {
			return nil, false, err
		}
		if u.Banned {
			return nil, false, ErrForbidden
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return u, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if _, err = ensureInviteCodeTx(ctx, tx, u.ID, "system", u.ID, created); err != nil {
		return nil, false, err
	}
	if inviter != nil {
		if _, err = tx.Exec(ctx, `INSERT INTO im_user_invite_relations(invitee_user_id,inviter_user_id,invite_code_id,registration_method,created_at) VALUES($1,$2,$3,'otp',$4)`, u.ID, inviter.UserID, inviter.ID, created); err != nil {
			return nil, false, err
		}
	}
	metadata, _ := json.Marshal(map[string]any{"method": "otp", "inviteCodeId": func() string {
		if inviter != nil {
			return inviter.ID
		}
		return ""
	}(), "inviterId": func() string {
		if inviter != nil {
			return inviter.UserID
		}
		return ""
	}()})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'auth.register','user',$2,$3,$4)`, "aud_register_"+u.ID, u.ID, metadata, created); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return u, true, nil
}

func (p *Postgres) ValidateInviteCode(ctx context.Context, code string) (bool, error) {
	var valid bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_user_invite_codes c JOIN im_users u ON u.id=c.user_id WHERE upper(c.code)=upper($1) AND c.status='active' AND u.deleted_at IS NULL AND NOT (u.banned AND (u.banned_until IS NULL OR u.banned_until>now())))`, strings.TrimSpace(code)).Scan(&valid)
	return valid, err
}

func (p *Postgres) UserInviteCode(ctx context.Context, userID string) (*InviteCode, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	item, err := ensureInviteCodeTx(ctx, tx, userID, "system", userID, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err = tx.QueryRow(ctx, `SELECT invite_code_change_count FROM im_users WHERE id=$1 AND deleted_at IS NULL`, userID).Scan(&item.SelfChangesUsed); err != nil {
		return nil, err
	}
	item.SelfChangesRemaining = max(0, 1-item.SelfChangesUsed)
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return item, nil
}

func (p *Postgres) ChangeUserInviteCode(ctx context.Context, userID, code string, at time.Time) (*InviteCode, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var changes int
	var banned bool
	var deleted *time.Time
	if err = tx.QueryRow(ctx, `SELECT invite_code_change_count,(banned AND (banned_until IS NULL OR banned_until>now())),deleted_at FROM im_users WHERE id=$1 FOR UPDATE`, userID).Scan(&changes, &banned, &deleted); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if banned || deleted != nil {
		return nil, ErrForbidden
	}
	if changes >= 1 {
		return nil, ErrInviteChangeUsed
	}
	var currentID, currentStatus string
	if err = tx.QueryRow(ctx, `SELECT id,status FROM im_user_invite_codes WHERE user_id=$1 AND status IN ('active','disabled') FOR UPDATE`, userID).Scan(&currentID, &currentStatus); err != nil {
		return nil, err
	}
	if currentStatus == "disabled" {
		return nil, ErrInviteDisabled
	}
	newID, err := inviteCodeID()
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_user_invite_codes SET status='retired',retired_at=$2,retired_by=$1,reason='user changed personal invitation code' WHERE id=$3`, userID, at, currentID); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO im_user_invite_codes(id,user_id,code,status,source,created_by,created_at) VALUES($1,$2,$3,'active','user',$2,$4)`, newID, userID, code, at)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_users SET invite_code_change_count=1,updated_at=$2 WHERE id=$1`, userID, at); err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(map[string]any{"oldCodeId": currentID, "newCodeId": newID})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,result,created_at) VALUES($1,$2,'invite_code.changed','user',$2,$3,'success',$4)`, `aud_invite_change_`+newID, userID, metadata, at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &InviteCode{ID: newID, UserID: userID, Code: code, Status: "active", Source: "user", SelfChangesUsed: 1, SelfChangesRemaining: 0, CreatedAt: at}, nil
}

func (p *Postgres) ListAdminInviteCodes(ctx context.Context, query, status, cursor string, limit int) (InviteCodePage, error) {
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + query + "%"
	where := `($1='' OR c.code ILIKE $2 OR u.id ILIKE $2 OR u.phone ILIKE $2 OR u.name ILIKE $2 OR COALESCE(u.handle,'') ILIKE $2) AND ($3='' OR c.status=$3)`
	var page InviteCodePage
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_user_invite_codes c JOIN im_users u ON u.id=c.user_id WHERE `+where, query, pattern, status).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := p.pool.Query(ctx, `SELECT c.id,c.user_id,c.code,c.status,c.source,c.created_at,u.invite_code_change_count,u.id,u.phone,u.name,COALESCE(u.handle,''),u.avatar_url,u.banned,u.created_at FROM im_user_invite_codes c JOIN im_users u ON u.id=c.user_id WHERE `+where+` ORDER BY c.created_at DESC,c.id LIMIT $4 OFFSET $5`, query, pattern, status, limit, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item InviteCode
		item.User = &model.User{}
		if err = rows.Scan(&item.ID, &item.UserID, &item.Code, &item.Status, &item.Source, &item.CreatedAt, &item.SelfChangesUsed, &item.User.ID, &item.User.Phone, &item.User.Name, &item.User.Handle, &item.User.AvatarURL, &item.User.Banned, &item.User.CreatedAt); err != nil {
			return page, err
		}
		item.SelfChangesRemaining = max(0, 1-item.SelfChangesUsed)
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	page.NextCursor = nextPageCursor(offset, len(page.Items), page.Total)
	return page, nil
}

func (p *Postgres) ListAdminInviteRelations(ctx context.Context, query, method, from, to, cursor string, limit int) (InviteRelationPage, error) {
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + query + "%"
	where := `($1='' OR c.code ILIKE $2 OR inviter.id ILIKE $2 OR inviter.phone ILIKE $2 OR inviter.name ILIKE $2 OR invitee.id ILIKE $2 OR invitee.phone ILIKE $2 OR invitee.name ILIKE $2) AND ($3='' OR r.registration_method=$3) AND (NULLIF($4,'')::timestamptz IS NULL OR r.created_at>=NULLIF($4,'')::timestamptz) AND (NULLIF($5,'')::timestamptz IS NULL OR r.created_at<NULLIF($5,'')::timestamptz)`
	var page InviteRelationPage
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_user_invite_relations r JOIN im_user_invite_codes c ON c.id=r.invite_code_id JOIN im_users inviter ON inviter.id=r.inviter_user_id JOIN im_users invitee ON invitee.id=r.invitee_user_id WHERE `+where, query, pattern, method, from, to).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := p.pool.Query(ctx, `SELECT c.id,c.code,r.registration_method,r.created_at,inviter.id,inviter.phone,inviter.name,COALESCE(inviter.handle,''),inviter.avatar_url,invitee.id,invitee.phone,invitee.name,COALESCE(invitee.handle,''),invitee.avatar_url FROM im_user_invite_relations r JOIN im_user_invite_codes c ON c.id=r.invite_code_id JOIN im_users inviter ON inviter.id=r.inviter_user_id JOIN im_users invitee ON invitee.id=r.invitee_user_id WHERE `+where+` ORDER BY r.created_at DESC,r.invitee_user_id DESC LIMIT $6 OFFSET $7`, query, pattern, method, from, to, limit, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		item := InviteRelation{Inviter: &model.User{}, Invitee: &model.User{}}
		if err = rows.Scan(&item.InviteCodeID, &item.InviteCode, &item.RegistrationMethod, &item.CreatedAt, &item.Inviter.ID, &item.Inviter.Phone, &item.Inviter.Name, &item.Inviter.Handle, &item.Inviter.AvatarURL, &item.Invitee.ID, &item.Invitee.Phone, &item.Invitee.Name, &item.Invitee.Handle, &item.Invitee.AvatarURL); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	page.NextCursor = nextPageCursor(offset, len(page.Items), page.Total)
	return page, nil
}

func (p *Postgres) SetAdminInviteCodeStatus(ctx context.Context, actor, id, status, reason string, at time.Time) (*InviteCode, error) {
	if status != "active" && status != "disabled" {
		return nil, ErrInviteInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	item := &InviteCode{}
	if err = tx.QueryRow(ctx, `SELECT id,user_id,code,status,source,created_at FROM im_user_invite_codes WHERE id=$1 AND status IN ('active','disabled') FOR UPDATE`, id).Scan(&item.ID, &item.UserID, &item.Code, &item.Status, &item.Source, &item.CreatedAt); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	before := item.Status
	if _, err = tx.Exec(ctx, `UPDATE im_user_invite_codes SET status=$2,reason=$3,
		disabled_at=CASE WHEN $2='disabled' THEN $4 ELSE disabled_at END,
		disabled_by=CASE WHEN $2='disabled' THEN $5 ELSE disabled_by END
		WHERE id=$1`, id, status, reason, at, actor); err != nil {
		return nil, err
	}
	item.Status = status
	metadata, _ := json.Marshal(map[string]any{"before": before, "after": status, "reason": reason})
	auditID, _ := secureOpaqueToken("aud_")
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,result,created_at) VALUES($1,$2,'invite_code.status_changed','invite_code',$3,$4,'success',$5)`, auditID, actor, id, metadata, at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return item, nil
}

func (p *Postgres) ResetAdminInviteCode(ctx context.Context, actor, id, reason string, at time.Time) (*InviteCode, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var userID string
	if err = tx.QueryRow(ctx, `SELECT user_id FROM im_user_invite_codes WHERE id=$1 AND status IN ('active','disabled') FOR UPDATE`, id).Scan(&userID); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_user_invite_codes SET status='retired',retired_at=$2,retired_by=$1,reason=$3 WHERE id=$4`, actor, at, reason, id); err != nil {
		return nil, err
	}
	var item *InviteCode
	for attempt := 0; attempt < 32; attempt++ {
		code, e := randomInviteCode()
		if e != nil {
			return nil, e
		}
		newID, e := inviteCodeID()
		if e != nil {
			return nil, e
		}
		tag, e := tx.Exec(ctx, `INSERT INTO im_user_invite_codes(id,user_id,code,status,source,created_by,created_at,reason) VALUES($1,$2,$3,'active','admin',$4,$5,$6) ON CONFLICT DO NOTHING`, newID, userID, code, actor, at, reason)
		if e != nil {
			return nil, e
		}
		if tag.RowsAffected() == 1 {
			item = &InviteCode{ID: newID, UserID: userID, Code: code, Status: "active", Source: "admin", CreatedAt: at}
			break
		}
	}
	if item == nil {
		return nil, ErrConflict
	}
	metadata, _ := json.Marshal(map[string]any{"oldCodeId": id, "newCodeId": item.ID, "reason": reason})
	auditID, _ := secureOpaqueToken("aud_")
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,result,created_at) VALUES($1,$2,'invite_code.reset','invite_code',$3,$4,'success',$5)`, auditID, actor, item.ID, metadata, at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return item, nil
}
