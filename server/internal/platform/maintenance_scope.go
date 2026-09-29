package platform

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrMaintenanceChanged = errors.New("maintenance schedule or run changed")
var errMaintenanceWindow = errors.New("maintenance window elapsed")

// Only the in-process scheduler can construct this authority. HTTP requests
// cannot specify a maintenance ID, impersonate an operator, or obtain a token.
// Authority is the original audited run, not a retained browser credential.
type maintenancePermit struct{ ID, Lease, Phase string }

func noPendingMaintenance(ctx context.Context, tx pgx.Tx, tenant, except string) error {
	var pending bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_maintenance_runs WHERE tenant_id=$1 AND state='pending' AND id<>$2)`, tenant, except).Scan(&pending); e != nil {
		return e
	}
	if pending {
		return ErrDeploymentMaintenance
	}
	return nil
}

func (p *maintenancePermit) id() string {
	if p == nil {
		return ""
	}
	return p.ID
}

// Caller holds the tenant row. New starts obey the window; recovery and replay
// must continue after it, without ever releasing an unconfirmed suspension.
func (p *maintenancePermit) check(ctx context.Context, tx pgx.Tx, tenant, actor, phase string, start bool) error {
	if p == nil {
		return nil
	}
	if p.Phase != phase {
		return ErrMaintenanceChanged
	}
	var valid, within bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_maintenance_runs WHERE id=$1 AND tenant_id=$2 AND actor_id=$3 AND phase=$4 AND state='pending' AND lease_id=$5 AND lease_until>clock_timestamp()),
 EXISTS(SELECT 1 FROM platform_maintenance_runs WHERE id=$1 AND window_end>clock_timestamp())`, p.ID, tenant, actor, phase, p.Lease).Scan(&valid, &within); e != nil {
		return e
	}
	if !valid {
		return ErrMaintenanceChanged
	}
	if start && !within {
		return errMaintenanceWindow
	}
	return nil
}
