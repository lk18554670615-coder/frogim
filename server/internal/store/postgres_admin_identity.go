package store

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type adminScanner interface{ Scan(...any) error }

const adminAccountSelect = `
SELECT a.id,a.email,a.display_name,a.password_hash,a.role_id,r.name,a.status,a.auth_version,
       COALESCE((SELECT array_agg(p.permission ORDER BY p.permission) FROM im_admin_role_permissions p WHERE p.role_id=a.role_id),ARRAY[]::text[]),
       a.last_login_at,a.password_updated_at,a.created_by,a.created_at,a.updated_at,a.disabled_at
FROM im_admin_accounts a JOIN im_admin_roles r ON r.id=a.role_id`

func scanAdminAccount(row adminScanner) (*AdminAccount, error) {
	item := &AdminAccount{}
	err := row.Scan(&item.ID, &item.Email, &item.DisplayName, &item.PasswordHash, &item.RoleID, &item.RoleName, &item.Status, &item.AuthVersion,
		&item.Permissions, &item.LastLoginAt, &item.PasswordUpdatedAt, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt, &item.DisabledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

func (p *Postgres) BootstrapAdmin(ctx context.Context, input AdminAccountCreate) (bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(490739126)`); err != nil {
		return false, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_admin_accounts`).Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return false, tx.Commit(ctx)
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	address, emailErr := mail.ParseAddress(email)
	if strings.TrimSpace(input.ID) == "" || emailErr != nil || address.Address != email || len(email) > 254 || !strings.HasPrefix(input.PasswordHash, "$2") || strings.TrimSpace(input.RoleID) == "" {
		return false, fmt.Errorf("administrator database is empty and bootstrap credentials are incomplete")
	}
	var roleExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_admin_roles WHERE id=$1)`, input.RoleID).Scan(&roleExists); err != nil {
		return false, err
	}
	if !roleExists {
		return false, fmt.Errorf("administrator bootstrap role %q does not exist", input.RoleID)
	}
	at := input.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = input.ID
	}
	_, err = tx.Exec(ctx, `INSERT INTO im_admin_accounts(id,email,display_name,password_hash,role_id,status,auth_version,password_updated_at,created_by,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,'active',1,$6,'bootstrap',$6,$6)`, input.ID, email, displayName, input.PasswordHash, input.RoleID, at)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (p *Postgres) AdminAccountByEmail(ctx context.Context, email string) (*AdminAccount, error) {
	return scanAdminAccount(p.pool.QueryRow(ctx, adminAccountSelect+` WHERE lower(a.email)=lower($1)`, strings.TrimSpace(email)))
}

func (p *Postgres) AdminAccountByID(ctx context.Context, id string) (*AdminAccount, error) {
	return scanAdminAccount(p.pool.QueryRow(ctx, adminAccountSelect+` WHERE a.id=$1`, id))
}

func (p *Postgres) ListAdminAccounts(ctx context.Context, query, status, cursor string, limit int) ([]*AdminAccount, int64, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query = strings.TrimSpace(query)
	status = strings.TrimSpace(status)
	if status != "" && status != "active" && status != "disabled" {
		return nil, 0, "", ErrConflict
	}
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_admin_accounts a JOIN im_admin_roles r ON r.id=a.role_id
		WHERE ($1='' OR a.email ILIKE '%'||$1||'%' OR a.display_name ILIKE '%'||$1||'%' OR a.id ILIKE '%'||$1||'%' OR r.name ILIKE '%'||$1||'%')
		AND ($2='' OR a.status=$2)`, query, status).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, adminAccountSelect+`
		WHERE ($1='' OR a.email ILIKE '%'||$1||'%' OR a.display_name ILIKE '%'||$1||'%' OR a.id ILIKE '%'||$1||'%' OR r.name ILIKE '%'||$1||'%')
		AND ($2='' OR a.status=$2) AND ($3='' OR a.id>$3) ORDER BY a.id LIMIT $4`, query, status, cursor, limit+1)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]*AdminAccount, 0, limit)
	for rows.Next() {
		item, scanErr := scanAdminAccount(rows)
		if scanErr != nil {
			return nil, 0, "", scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, total, next, nil
}

func adminConstraintError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			return ErrNotFound
		case "23505", "23514":
			return ErrConflict
		}
	}
	return err
}

func (p *Postgres) CreateAdminAccount(ctx context.Context, input AdminAccountCreate) (*AdminAccount, error) {
	at := input.At.UTC()
	_, err := p.pool.Exec(ctx, `INSERT INTO im_admin_accounts(id,email,display_name,password_hash,role_id,status,auth_version,password_updated_at,created_by,created_at,updated_at)
		VALUES($1,lower($2),$3,$4,$5,'active',1,$6,$7,$6,$6)`, input.ID, input.Email, input.DisplayName, input.PasswordHash, input.RoleID, at, input.CreatedBy)
	if err != nil {
		return nil, adminConstraintError(err)
	}
	return p.AdminAccountByID(ctx, input.ID)
}

func (p *Postgres) UpdateAdminAccount(ctx context.Context, input AdminAccountUpdate) (*AdminAccount, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var currentRole, currentStatus string
	if err = tx.QueryRow(ctx, `SELECT role_id,status FROM im_admin_accounts WHERE id=$1 FOR UPDATE`, input.ID).Scan(&currentRole, &currentStatus); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	nextRole, nextStatus := currentRole, currentStatus
	if input.RoleID != nil {
		nextRole = *input.RoleID
	}
	if input.Status != nil {
		nextStatus = *input.Status
	}
	if input.ID == input.ActorID && nextStatus == "disabled" {
		return nil, fmt.Errorf("%w: cannot disable the current administrator", ErrForbidden)
	}
	if currentRole == "platform_admin" && currentStatus == "active" && (nextRole != "platform_admin" || nextStatus != "active") {
		rows, queryErr := tx.Query(ctx, `SELECT id FROM im_admin_accounts WHERE role_id='platform_admin' AND status='active' FOR UPDATE`)
		if queryErr != nil {
			return nil, queryErr
		}
		active := 0
		for rows.Next() {
			active++
		}
		rows.Close()
		if active <= 1 {
			return nil, fmt.Errorf("%w: at least one active platform administrator is required", ErrConflict)
		}
	}
	if nextStatus != "active" && nextStatus != "disabled" {
		return nil, ErrConflict
	}
	var roleExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_admin_roles WHERE id=$1)`, nextRole).Scan(&roleExists); err != nil {
		return nil, err
	}
	if !roleExists {
		return nil, ErrNotFound
	}
	_, err = tx.Exec(ctx, `UPDATE im_admin_accounts SET
		email=COALESCE($2,email),display_name=COALESCE($3,display_name),role_id=$4,status=$5,
		auth_version=auth_version+1,updated_at=$6,disabled_at=CASE WHEN $5='disabled' THEN COALESCE(disabled_at,$6) ELSE NULL END
		WHERE id=$1`, input.ID, input.Email, input.DisplayName, nextRole, nextStatus, input.At.UTC())
	if err != nil {
		return nil, adminConstraintError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.AdminAccountByID(ctx, input.ID)
}

func (p *Postgres) UpdateAdminAccountPassword(ctx context.Context, id, hash string, at time.Time) error {
	tag, err := p.pool.Exec(ctx, `UPDATE im_admin_accounts SET password_hash=$2,password_updated_at=$3,updated_at=$3,auth_version=auth_version+1 WHERE id=$1`, id, hash, at.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) RecordAdminAccountLogin(ctx context.Context, id string, at time.Time) error {
	tag, err := p.pool.Exec(ctx, `UPDATE im_admin_accounts SET last_login_at=$2 WHERE id=$1`, id, at.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanAdminRole(row adminScanner) (*AdminRole, error) {
	item := &AdminRole{}
	err := row.Scan(&item.ID, &item.Name, &item.Description, &item.BuiltIn, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt, &item.Permissions, &item.AccountCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

const adminRoleSelect = `SELECT r.id,r.name,r.description,r.built_in,r.created_by,r.created_at,r.updated_at,
	COALESCE((SELECT array_agg(p.permission ORDER BY p.permission) FROM im_admin_role_permissions p WHERE p.role_id=r.id),ARRAY[]::text[]),
	(SELECT count(*) FROM im_admin_accounts a WHERE a.role_id=r.id) FROM im_admin_roles r`

func (p *Postgres) ListAdminRoles(ctx context.Context) ([]*AdminRole, error) {
	rows, err := p.pool.Query(ctx, adminRoleSelect+` ORDER BY r.built_in DESC,r.created_at,r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*AdminRole{}
	for rows.Next() {
		item, scanErr := scanAdminRole(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) adminRoleByID(ctx context.Context, id string) (*AdminRole, error) {
	return scanAdminRole(p.pool.QueryRow(ctx, adminRoleSelect+` WHERE r.id=$1`, id))
}

func replaceAdminRolePermissions(ctx context.Context, tx pgx.Tx, roleID string, permissions []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM im_admin_role_permissions WHERE role_id=$1`, roleID); err != nil {
		return err
	}
	for _, permission := range permissions {
		if _, err := tx.Exec(ctx, `INSERT INTO im_admin_role_permissions(role_id,permission) VALUES($1,$2)`, roleID, permission); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) CreateAdminRole(ctx context.Context, input AdminRoleCreate) (*AdminRole, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO im_admin_roles(id,name,description,built_in,created_by,created_at,updated_at) VALUES($1,$2,$3,false,$4,$5,$5)`, input.ID, input.Name, input.Description, input.CreatedBy, input.At.UTC())
	if err != nil {
		return nil, adminConstraintError(err)
	}
	if err = replaceAdminRolePermissions(ctx, tx, input.ID, input.Permissions); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.adminRoleByID(ctx, input.ID)
}

func (p *Postgres) UpdateAdminRole(ctx context.Context, input AdminRoleUpdate) (*AdminRole, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var builtIn bool
	if err = tx.QueryRow(ctx, `SELECT built_in FROM im_admin_roles WHERE id=$1 FOR UPDATE`, input.ID).Scan(&builtIn); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if builtIn {
		return nil, fmt.Errorf("%w: built-in roles are immutable", ErrForbidden)
	}
	if _, err = tx.Exec(ctx, `UPDATE im_admin_roles SET name=$2,description=$3,updated_at=$4 WHERE id=$1`, input.ID, input.Name, input.Description, input.At.UTC()); err != nil {
		return nil, adminConstraintError(err)
	}
	if err = replaceAdminRolePermissions(ctx, tx, input.ID, input.Permissions); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.adminRoleByID(ctx, input.ID)
}

func (p *Postgres) DeleteAdminRole(ctx context.Context, id string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var builtIn bool
	if err = tx.QueryRow(ctx, `SELECT built_in FROM im_admin_roles WHERE id=$1 FOR UPDATE`, id).Scan(&builtIn); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if builtIn {
		return fmt.Errorf("%w: built-in roles are immutable", ErrForbidden)
	}
	var accounts int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_admin_accounts WHERE role_id=$1`, id).Scan(&accounts); err != nil {
		return err
	}
	if accounts > 0 {
		return fmt.Errorf("%w: role is assigned to administrators", ErrConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM im_admin_roles WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
