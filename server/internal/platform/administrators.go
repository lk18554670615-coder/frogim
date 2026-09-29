package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrAdminChanged  = errors.New("administrator changed")
	ErrAdminSelf     = errors.New("cannot change own administrator access")
	ErrLastOperator  = errors.New("last enabled operator must remain")
	ErrAdminPassword = errors.New("current administrator password incorrect")
)

type Administrator struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	Role        string    `json:"role"`
	Enabled     bool      `json:"enabled"`
	AuthVersion int64     `json:"authVersion"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
type AdminOperationInput struct {
	Action          string `json:"action"`
	TargetID        string `json:"targetId"`
	Username        string `json:"username"`
	Role            string `json:"role"`
	Enabled         *bool  `json:"enabled"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Reason          string `json:"reason"`
}
type AdminOperation struct {
	AdminOperationInput
	RequestID       string `json:"requestId"`
	Password        string `json:"password"`
	CurrentPassword string `json:"currentPassword"`
	Confirmed       bool   `json:"confirmed"`
}

// One serialized lifecycle transaction rechecks the active session, writes the
// account, revokes every old session and records an immutable replay response.
// Passwords are never serialized into input, snapshot or audit data.
func (s *Store) ManageAdministrator(ctx context.Context, actor, token string, in AdminOperation) (Administrator, error) {
	var result Administrator
	in.Username, in.Reason = strings.TrimSpace(in.Username), strings.TrimSpace(in.Reason)
	if !tenancy.ValidID(actor) || !tenancy.ValidID(in.RequestID) || len(token) != 43 || !adminReason(in.Reason, in.Confirmed) || in.ExpectedVersion < 0 {
		return result, tenancy.ErrInvalid
	}
	validRole := in.Role == "operator" || in.Role == "reader"
	switch in.Action {
	case "create":
		if in.TargetID != "" || !tenancy.ValidID(in.Username) || len(in.Username) < 3 || !validRole || in.Enabled == nil || !*in.Enabled || in.ExpectedVersion != 0 || in.CurrentPassword != "" {
			return result, tenancy.ErrInvalid
		}
	case "access":
		if !tenancy.ValidID(in.TargetID) || in.Username != "" || !validRole || in.Enabled == nil || in.ExpectedVersion < 1 || in.Password != "" || in.CurrentPassword != "" {
			return result, tenancy.ErrInvalid
		}
		if in.TargetID == actor {
			return result, ErrAdminSelf
		}
	case "password":
		if !tenancy.ValidID(in.TargetID) || in.Username != "" || in.Role != "" || in.Enabled != nil || in.ExpectedVersion < 1 || len(in.CurrentPassword) > 72 || (in.TargetID == actor && in.CurrentPassword == "") || (in.TargetID != actor && in.CurrentPassword != "") {
			return result, tenancy.ErrInvalid
		}
	default:
		return result, tenancy.ErrInvalid
	}
	var passwordHash []byte
	var err error
	if in.Action != "access" {
		if len([]rune(in.Password)) < 12 || len(in.Password) > 72 {
			return result, tenancy.ErrInvalid
		}
		passwordHash, err = bcrypt.GenerateFromPassword([]byte(in.Password), 12)
		if err != nil {
			return result, err
		}
	}
	input, err := json.Marshal(in.AdminOperationInput)
	if err != nil {
		return result, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	// Also shared with bootstrap, preventing concurrent first-admin creation.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(490739175)`); err != nil {
		return result, err
	}
	var role string
	err = tx.QueryRow(ctx, `SELECT a.role FROM platform_admin_accounts a JOIN platform_admin_sessions s ON s.admin_id=a.id WHERE a.id=$1 AND s.token_hash=$2 AND a.enabled AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND s.auth_version=a.auth_version FOR SHARE OF a,s`, actor, tenancy.Hash(token)).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrDenied
	}
	if err != nil {
		return result, err
	}
	if role != "operator" && !(in.Action == "password" && in.TargetID == actor) {
		return result, ErrDenied
	}
	var same bool
	var snapshot []byte
	var oldRequestHash string
	err = tx.QueryRow(ctx, `SELECT input=$3::jsonb,snapshot,password_hash FROM platform_admin_operations WHERE actor_id=$1 AND request_id=$2`, actor, in.RequestID, input).Scan(&same, &snapshot, &oldRequestHash)
	if err == nil {
		if !same || (in.Action != "access" && bcrypt.CompareHashAndPassword([]byte(oldRequestHash), []byte(in.Password)) != nil) {
			return result, ErrRequestChanged
		}
		if err = json.Unmarshal(snapshot, &result); err != nil {
			return result, err
		}
		return result, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	var before Administrator
	if in.Action == "create" {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_admin_accounts WHERE lower(username)=lower($1))`, in.Username).Scan(&exists); err != nil {
			return result, err
		}
		if exists {
			return result, ErrConflict
		}
		result.ID, err = newID("admin")
		if err != nil {
			return result, err
		}
		err = tx.QueryRow(ctx, `INSERT INTO platform_admin_accounts(id,username,password_hash,role) VALUES($1,$2,$3,$4) RETURNING id,username,role,enabled,auth_version,created_at,updated_at`, result.ID, in.Username, string(passwordHash), in.Role).Scan(&result.ID, &result.Username, &result.Role, &result.Enabled, &result.AuthVersion, &result.CreatedAt, &result.UpdatedAt)
		if err != nil {
			return result, err
		}
	} else {
		var existingHash string
		err = tx.QueryRow(ctx, `SELECT id,username,role,enabled,auth_version,created_at,updated_at,password_hash FROM platform_admin_accounts WHERE id=$1 FOR UPDATE`, in.TargetID).Scan(&before.ID, &before.Username, &before.Role, &before.Enabled, &before.AuthVersion, &before.CreatedAt, &before.UpdatedAt, &existingHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return result, ErrDenied
		}
		if err != nil {
			return result, err
		}
		if before.AuthVersion != in.ExpectedVersion {
			return result, ErrAdminChanged
		}
		if in.Action == "password" && in.TargetID == actor && bcrypt.CompareHashAndPassword([]byte(existingHash), []byte(in.CurrentPassword)) != nil {
			return result, ErrAdminPassword
		}
		result = before
		changed := in.Action == "password" || before.Role != in.Role || before.Enabled != *in.Enabled
		if in.Action == "access" {
			if before.Enabled && before.Role == "operator" && (!*in.Enabled || in.Role != "operator") {
				var count int
				if err = tx.QueryRow(ctx, `SELECT count(*) FROM platform_admin_accounts WHERE enabled AND role='operator'`).Scan(&count); err != nil {
					return result, err
				}
				if count <= 1 {
					return result, ErrLastOperator
				}
			}
			result.Role, result.Enabled = in.Role, *in.Enabled
		}
		if changed {
			result.AuthVersion++
			if in.Action == "password" {
				existingHash = string(passwordHash)
			}
			err = tx.QueryRow(ctx, `UPDATE platform_admin_accounts SET role=$2,enabled=$3,password_hash=$4,auth_version=$5,updated_at=clock_timestamp() WHERE id=$1 RETURNING updated_at`, result.ID, result.Role, result.Enabled, existingHash, result.AuthVersion).Scan(&result.UpdatedAt)
			if err != nil {
				return result, err
			}
			if _, err = tx.Exec(ctx, `UPDATE platform_admin_sessions SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE admin_id=$1`, result.ID); err != nil {
				return result, err
			}
		}
	}
	snapshot, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_admin_operations(actor_id,request_id,target_id,input,password_hash,snapshot) VALUES($1,$2,$3,$4,$5,$6)`, actor, in.RequestID, result.ID, input, string(passwordHash), snapshot); err != nil {
		return result, err
	}
	metadata, err := json.Marshal(map[string]any{"requestId": in.RequestID, "targetId": result.ID, "action": in.Action, "beforeRole": before.Role, "role": result.Role, "beforeEnabled": before.Enabled, "enabled": result.Enabled, "beforeVersion": before.AuthVersion, "authVersion": result.AuthVersion})
	if err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,reason,metadata) VALUES($1,'administrator.updated',$2,$3)`, actor, in.Reason, metadata); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (a *API) adminAdministrators(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state != "" && state != "enabled" && state != "disabled" {
		failure(w, tenancy.ErrInvalid)
		return
	}
	a.adminPage(w, r, `SELECT lower(username) AS ordering,jsonb_build_object('id',id,'username',username,'role',role,'enabled',enabled,'authVersion',auth_version,'createdAt',created_at,'updatedAt',updated_at) AS data FROM platform_admin_accounts WHERE ($3='' OR strpos(lower(username),lower($3))>0) AND ($4='' OR enabled=($4='enabled'))`, strings.TrimSpace(r.URL.Query().Get("q")), state)
}
func (a *API) adminManageAdministrator(w http.ResponseWriter, r *http.Request) {
	var in AdminOperation
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	result, err := a.Store.ManageAdministrator(r.Context(), actorID(r), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), in)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, result)
}
func (a *API) adminOperationResult(w http.ResponseWriter, r *http.Request) {
	var result json.RawMessage
	err := a.Store.pool.QueryRow(r.Context(), `SELECT snapshot FROM platform_admin_operations WHERE actor_id=$1 AND request_id=$2`, actorID(r), r.PathValue("requestId")).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		respond(w, 404, map[string]any{"error": map[string]string{"code": "ADMIN_OPERATION_NOT_FOUND", "message": "尚无该请求的已提交结果"}})
		return
	}
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, result)
}
