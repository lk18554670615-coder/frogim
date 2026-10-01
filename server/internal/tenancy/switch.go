package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/golang-jwt/jwt/v5"

	"net/http"
	"regexp"
	"time"
)

var regexpCode = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type Operation struct {
	ID, UserID, Actor, Kind, Source, Target, Phase, Reason string
	Revision                                               int64
}

func (p *Platform) switchUser(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Confirmation
		TenantID string `json:"tenantId"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !b.valid() {
		fail(w, 400, "CONFIRMATION_REQUIRED")
		return
	}
	o, e := p.startOperation(r.Context(), r.PathValue("id"), b.TenantID, "switch", actor(r), b.Reason, b.Version)
	if e != nil {
		fail(w, 409, "SWITCH_REJECTED")
		return
	}
	p.runAndRespond(w, r, o)
}
func (p *Platform) banUser(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Confirmation
		Banned bool `json:"banned"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !b.valid() {
		fail(w, 400, "CONFIRMATION_REQUIRED")
		return
	}
	u, e := p.user(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, 404, "NOT_FOUND")
		return
	}
	kind := "ban"
	if !b.Banned {
		kind = "unban"
	}
	o, e := p.startOperation(r.Context(), u.ID, u.TenantID, kind, actor(r), b.Reason, b.Version)
	if e != nil {
		fail(w, 409, "CHANGE_REJECTED")
		return
	}
	p.runAndRespond(w, r, o)
}

type OperationValues struct{ Phone, PasswordHash string }

func (p *Platform) startOperation(ctx context.Context, uid, target, kind, a, reason string, revision int64, values ...OperationValues) (Operation, error) {
	u, e := p.user(ctx, uid)
	if e != nil {
		return Operation{}, e
	}
	t, e := p.tenant(ctx, target)
	if e != nil || !t.Enabled {
		return Operation{}, errors.New("target unavailable")
	}
	if u.Revision != revision || u.Pending != "" || (kind == "switch" && u.TenantID == target) {
		return Operation{}, errors.New("version conflict")
	}
	old, e := p.tenant(ctx, u.TenantID)
	if e != nil {
		return Operation{}, e
	}
	if e = p.checkDeployment(ctx, old); e != nil {
		return Operation{}, e
	}
	if kind == "switch" {
		if e = p.checkDeployment(ctx, t); e != nil {
			return Operation{}, e
		}
	}
	o := Operation{ID: ID(), UserID: uid, Actor: a, Kind: kind, Source: u.TenantID, Target: target, Phase: "prepare", Reason: reason, Revision: revision}
	tx, e := p.DB.Begin(ctx)
	if e != nil {
		return o, e
	}
	defer tx.Rollback(ctx)
	v := OperationValues{}
	if len(values) > 0 {
		v = values[0]
	}
	phone := v.Phone
	if kind == "password" && v.PasswordHash == "" {
		return o, errors.New("password hash required")
	}
	if phone != "" {
		var exists bool
		if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "phone:"+phone); e != nil {
			return o, e
		}
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lp_users WHERE phone=$1) OR EXISTS(SELECT 1 FROM lp_phone_reservations WHERE phone=$1)`, phone).Scan(&exists)
		if e != nil || exists {
			return o, errors.New("phone conflict")
		}
	}
	res, e := tx.Exec(ctx, `UPDATE lp_users SET pending=$2 WHERE id=$1 AND revision=$3 AND pending=''`, uid, o.ID, revision)
	if e != nil || res.RowsAffected() != 1 {
		return o, errors.New("version conflict")
	}
	_, e = tx.Exec(ctx, `INSERT INTO lp_operations(id,user_id,actor,kind,source,target,revision,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, o.ID, uid, a, kind, o.Source, target, revision, reason)
	if e == nil {
		_, e = tx.Exec(ctx, `UPDATE lp_operations SET desired_phone=$2,password_hash=$3 WHERE id=$1`, o.ID, phone, v.PasswordHash)
	}
	if e == nil && phone != "" {
		_, e = tx.Exec(ctx, `INSERT INTO lp_phone_reservations(phone,user_id,operation_id) VALUES($1,$2,$3)`, phone, uid, o.ID)
	}
	if e == nil {
		e = auditTx(ctx, tx, a, kind, uid, reason, "started")
	}
	if e == nil {
		e = tx.Commit(ctx)
	}
	return o, e
}
func (p *Platform) retry(w http.ResponseWriter, r *http.Request) {
	var b Confirmation
	if !decode(w, r, &b) {
		return
	}
	if !b.valid() {
		fail(w, 400, "CONFIRMATION_REQUIRED")
		return
	}
	var o Operation
	e := p.DB.QueryRow(r.Context(), `SELECT id,user_id,actor,kind,source,target,revision,phase,reason FROM lp_operations WHERE id=$1`, r.PathValue("id")).Scan(&o.ID, &o.UserID, &o.Actor, &o.Kind, &o.Source, &o.Target, &o.Revision, &o.Phase, &o.Reason)
	if e != nil {
		fail(w, 404, "NOT_FOUND")
		return
	}
	if b.Version != o.Revision {
		fail(w, 409, "VERSION_CONFLICT")
		return
	}
	if o.Phase == "done" {
		jsonResponse(w, 200, map[string]any{"operationId": o.ID, "phase": "done"})
		return
	}
	_, e = p.DB.Exec(r.Context(), `INSERT INTO lp_audit(actor,action,object_id,reason,result) VALUES($1,'operation.retry',$2,$3,'started')`, actor(r), o.ID, b.Reason)
	if e != nil {
		fail(w, 503, "AUDIT_FAILED")
		return
	}
	p.runAndRespond(w, r, o)
}
func (p *Platform) runAndRespond(w http.ResponseWriter, r *http.Request, o Operation) {
	e := p.runOperation(r.Context(), o)
	if e != nil {
		_, _ = p.DB.Exec(context.Background(), `UPDATE lp_operations SET error=$2,updated_at=now() WHERE id=$1`, o.ID, e.Error())
		jsonResponse(w, 503, map[string]any{"operationId": o.ID, "phase": "incomplete", "error": map[string]string{"code": "OPERATION_INCOMPLETE", "message": "操作未完成，请查看记录并重试原操作"}})
		return
	}
	jsonResponse(w, 200, map[string]any{"operationId": o.ID, "phase": "done"})
}
func (p *Platform) runOperation(ctx context.Context, o Operation) error {
	conn, e := p.DB.Acquire(ctx)
	if e != nil {
		return e
	}
	defer conn.Release()
	var locked bool
	e = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, o.UserID).Scan(&locked)
	if e != nil || !locked {
		return errors.New("operation busy")
	}
	defer func() {
		if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, o.UserID); err != nil {
			_ = conn.Conn().Close(context.Background())
		}
	}()
	// A retry may have read its receipt before another request advanced it.
	// Re-read only after owning the user lock; never replay a stale phase.
	if e = conn.QueryRow(ctx, `SELECT phase FROM lp_operations WHERE id=$1 AND user_id=$2 AND revision=$3`, o.ID, o.UserID, o.Revision).Scan(&o.Phase); e != nil {
		return e
	}
	if o.Phase == "done" {
		return nil
	}
	u, e := p.user(ctx, o.UserID)
	if e != nil {
		return e
	}
	if u.Pending != o.ID || u.Revision != o.Revision {
		return errors.New("operation state changed")
	}
	if o.Kind == "switch" {
		var pr []byte
		if err := p.DB.QueryRow(ctx, `SELECT profile FROM lp_memberships WHERE user_id=$1 AND tenant_id=$2`, u.ID, o.Source).Scan(&pr); err == nil {
			_ = json.Unmarshal(pr, &u.Profile)
		}
	}
	old, e := p.tenant(ctx, o.Source)
	if e != nil {
		return e
	}
	target, e := p.tenant(ctx, o.Target)
	if e != nil || !target.Enabled {
		return errors.New("target unavailable")
	}
	if o.Phase == "prepare" {
		if o.Kind == "switch" {
			var latest struct {
				Exists   bool     `json:"exists"`
				Snapshot Snapshot `json:"snapshot"`
			}
			if e = p.control(ctx, old, "/internal/directory/profile-read", map[string]string{"userId": u.ID}, &latest); e != nil {
				return e
			}
			if latest.Exists {
				if latest.Snapshot.Revision > u.Revision {
					return errors.New("source assignment changed")
				}
				u.Profile = latest.Snapshot.Profile
			}
			pr := Prepare{Operation: o.ID, User: u}
			if u.Profile.AvatarMediaID != "" {
				pr.AvatarSource = old.ControlURL + "/internal/directory/avatar/" + u.ID
				pr.AvatarGrant, _ = jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"typ": "avatar-copy", "sub": u.ID, "source": old.ID, "aud": target.ID, "media": u.Profile.AvatarMediaID, "operation": o.ID, "exp": time.Now().Add(5 * time.Minute).Unix()}).SignedString(p.signingKey())
			}
			var snapshot Snapshot
			if e = p.control(ctx, target, "/internal/directory/prepare", pr, &snapshot); e != nil {
				return e
			}
			b, _ := json.Marshal(snapshot.Profile)
			_, e = p.DB.Exec(ctx, `INSERT INTO lp_memberships(user_id,tenant_id,profile,profile_version,assignment_version,synced_at) VALUES($1,$2,$3,$4,$5,now()) ON CONFLICT(user_id,tenant_id) DO NOTHING`, u.ID, target.ID, b, snapshot.Version, u.Revision+1)
			if e != nil {
				return e
			}
		}
		if _, e = conn.Exec(ctx, `UPDATE lp_operations SET phase='offline',error='',updated_at=now() WHERE id=$1`, o.ID); e != nil {
			return e
		}
		o.Phase = "offline"
	}
	if o.Phase == "offline" {
		if e = p.control(ctx, old, "/internal/directory/offline", map[string]any{"userId": u.ID, "revision": u.Revision, "operationId": o.ID}, nil); e != nil {
			return e
		}
		if _, e = conn.Exec(ctx, `UPDATE lp_operations SET phase='commit',error='',updated_at=now() WHERE id=$1`, o.ID); e != nil {
			return e
		}
	}
	if o.Kind == "phone" {
		var phone string
		if e = p.DB.QueryRow(ctx, `SELECT desired_phone FROM lp_operations WHERE id=$1`, o.ID).Scan(&phone); e != nil {
			return e
		}
		rows, err := p.DB.Query(ctx, `SELECT tenant_id FROM lp_memberships WHERE user_id=$1`, u.ID)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			t, err := p.tenant(ctx, id)
			if err != nil {
				return err
			}
			if e = p.control(ctx, t, "/internal/directory/phone", map[string]any{"userId": u.ID, "phone": phone, "revision": u.Revision}, nil); e != nil {
				return e
			}
		}
	}

	tx, e := conn.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	// The row lock fences ticket redemption against the assignment commit.
	var pending string
	e = tx.QueryRow(ctx, `SELECT pending FROM lp_users WHERE id=$1 AND revision=$2 FOR UPDATE`, u.ID, u.Revision).Scan(&pending)
	if e != nil || pending != o.ID {
		return errors.New("operation state changed")
	}
	var pr []byte
	if o.Kind == "switch" {
		e = tx.QueryRow(ctx, `SELECT profile FROM lp_memberships WHERE user_id=$1 AND tenant_id=$2`, u.ID, target.ID).Scan(&pr)
	} else {
		pr, _ = json.Marshal(u.Profile)
	}
	if e != nil {
		return e
	}
	ban := u.Banned
	if o.Kind == "ban" {
		ban = true
	}
	if o.Kind == "unban" {
		ban = false
	}
	_, e = tx.Exec(ctx, `UPDATE lp_users SET tenant_id=$2,revision=revision+1,pending='',banned=$3,profile=$4 WHERE id=$1`, u.ID, target.ID, ban, pr)
	if e == nil {
		_, e = tx.Exec(ctx, `UPDATE lp_memberships SET assignment_version=$3 WHERE user_id=$1 AND tenant_id=$2`, u.ID, target.ID, u.Revision+1)
	}
	if e == nil && o.Kind == "password" {
		var hash string
		e = tx.QueryRow(ctx, `SELECT password_hash FROM lp_operations WHERE id=$1`, o.ID).Scan(&hash)
		if e == nil && hash == "" {
			e = errors.New("password preparation incomplete")
		}
		if e == nil {
			_, e = tx.Exec(ctx, `UPDATE lp_users SET password_hash=$2 WHERE id=$1`, u.ID, hash)
		}
	}
	if e == nil && o.Kind == "phone" {
		_, e = tx.Exec(ctx, `UPDATE lp_users SET phone=(SELECT desired_phone FROM lp_operations WHERE id=$2) WHERE id=$1`, u.ID, o.ID)
		if e == nil {
			_, e = tx.Exec(ctx, `DELETE FROM lp_phone_reservations WHERE operation_id=$1`, o.ID)
		}
	}
	if e == nil && (o.Kind == "ban" || o.Kind == "password") {
		_, e = tx.Exec(ctx, `UPDATE lp_sessions SET revoked=true WHERE user_id=$1`, u.ID)
	}
	if e == nil {
		e = auditTx(ctx, tx, o.Actor, o.Kind, u.ID, o.Reason, "success")
	}
	if e == nil {
		_, e = tx.Exec(ctx, `UPDATE lp_operations SET phase='done',error='',password_hash='',updated_at=now() WHERE id=$1`, o.ID)
	}
	if e == nil {
		e = tx.Commit(ctx)
	}
	return e
}
func (p *Platform) phase(ctx context.Context, id, phase string) error {
	_, e := p.DB.Exec(ctx, `UPDATE lp_operations SET phase=$2,error='',updated_at=now() WHERE id=$1`, id, phase)
	return e
}
func (p *Platform) ControlHandler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /internal/directory/key", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]string{"key": publicKey(p)})
	})
	m.HandleFunc("POST /internal/directory/consume", p.consume)
	m.HandleFunc("POST /internal/directory/profile", p.snapshot)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := peer(r)
		if id == "" || id == "platform" {
			fail(w, 403, "ENTERPRISE_IDENTITY_REQUIRED")
			return
		}
		if _, e := p.tenant(r.Context(), id); e != nil {
			fail(w, 403, "UNKNOWN_ENTERPRISE")
			return
		}
		m.ServeHTTP(w, r)
	})
}
func (p *Platform) consume(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Ticket string `json:"ticket"`
	}
	if !decode(w, r, &b) {
		return
	}
	ctx := r.Context()
	tx, e := p.DB.Begin(ctx)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(ctx)
	var uid, tid string
	var rev int64
	e = tx.QueryRow(ctx, `SELECT user_id,tenant_id,revision FROM lp_tickets WHERE token_hash=$1 AND NOT used AND expires_at>now()`, digest(b.Ticket)).Scan(&uid, &tid, &rev)
	if e != nil || tid != peer(r) {
		fail(w, 401, "INVALID_GRANT")
		return
	}
	var current, pending string
	var revision int64
	var banned, enabled bool
	e = tx.QueryRow(ctx, `SELECT u.tenant_id,u.revision,u.pending,u.banned,t.enabled FROM lp_users u JOIN lp_tenants t ON t.id=u.tenant_id WHERE u.id=$1 FOR UPDATE OF u`, uid).Scan(&current, &revision, &pending, &banned, &enabled)
	if e != nil || current != tid || revision != rev || pending != "" || banned || !enabled {
		fail(w, 403, "ASSIGNMENT_CHANGED")
		return
	}
	res, e := tx.Exec(ctx, `UPDATE lp_tickets SET used=true WHERE token_hash=$1 AND NOT used`, digest(b.Ticket))
	if e != nil || res.RowsAffected() != 1 {
		fail(w, 401, "GRANT_ALREADY_USED")
		return
	}
	if e = tx.Commit(ctx); e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	u, e := p.user(ctx, uid)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	jsonResponse(w, 200, u)
}
func (p *Platform) snapshot(w http.ResponseWriter, r *http.Request) {
	var b Snapshot
	if !decode(w, r, &b) {
		return
	}
	ctx := r.Context()
	tx, e := p.DB.Begin(ctx)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(ctx)
	var tid string
	var revision int64
	e = tx.QueryRow(ctx, `SELECT tenant_id,revision FROM lp_users WHERE id=$1 FOR UPDATE`, b.UserID).Scan(&tid, &revision)
	if e != nil || tid != peer(r) || revision != b.Revision {
		fail(w, 409, "STALE_ENTERPRISE")
		return
	}
	pr, _ := json.Marshal(b.Profile)
	res, e := tx.Exec(ctx, `UPDATE lp_memberships SET profile=$3,profile_version=$4,synced_at=now(),sync_error='',assignment_version=$5 WHERE user_id=$1 AND tenant_id=$2 AND profile_version<$4`, b.UserID, tid, pr, b.Version, b.Revision)
	if e == nil && res.RowsAffected() == 1 {
		_, e = tx.Exec(ctx, `UPDATE lp_users SET profile=$2 WHERE id=$1`, b.UserID, pr)
	}
	if e == nil {
		_, e = tx.Exec(ctx, `UPDATE lp_memberships SET synced_at=now(),sync_error='' WHERE user_id=$1 AND tenant_id=$2 AND profile_version=$3`, b.UserID, tid, b.Version)
	}
	if e == nil {
		e = tx.Commit(ctx)
	}
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	jsonResponse(w, 200, map[string]any{"ok": true, "receivedAt": time.Now().UTC()})
}
