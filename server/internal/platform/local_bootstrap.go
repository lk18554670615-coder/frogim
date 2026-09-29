package platform

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

// BootstrapLocalDefault is used by the explicit local-only CLI. Existing
// definitions must match exactly; it cannot change routing or reactivate a
// suspended enterprise. This does not adopt an existing business database.
func (s *Store) BootstrapLocalDefault(ctx context.Context, peer EnterpriseRPC) error {
	var address, state string
	var isDefault bool
	var version int64
	err := s.pool.QueryRow(ctx, `SELECT http_base_url,status,is_default,config_version FROM platform_tenants WHERE id='default'`).Scan(&address, &state, &isDefault, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = s.PutTenant(ctx, "default", "默认企业（本机开发）", "https://127.0.0.1:18444", "local-bootstrap", "初始化本机默认企业", true); err != nil {
			return err
		}
		version = 1
	} else if err != nil {
		return err
	} else if address != "https://127.0.0.1:18444" || !isDefault || state == "suspended" {
		return ErrConflict
	}
	return s.ActivateTenant(ctx, "default", "local-bootstrap", "本机运行依赖检查通过", true, version, peer.CheckReadiness)
}

// A disposable test identity is explicit in the local-only bootstrap command.
// It uses the real registration saga, and cannot take over an existing account.
func (s *Store) BootstrapLocalUser(ctx context.Context, password string) error {
	var tenant, state string
	err := s.pool.QueryRow(ctx, `SELECT tenant_id,state FROM platform_accounts WHERE phone='19900000001'`).Scan(&tenant, &state)
	if err == nil {
		if tenant != "default" || (state != "provisioning" && state != "active") {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = s.Reserve(ctx, Registration{Phone: "19900000001", Password: password, Name: "本机验收账号", Method: "admin", ForcedTenantID: "default", Actor: "local-bootstrap"})
	return err
}
