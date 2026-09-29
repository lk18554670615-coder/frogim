package directorybackup

import (
	"context"
	"github.com/jackc/pgx/v5"
)

func directoryRecoveryFilter(ctx context.Context, conn *pgx.Conn) (string, error) {
	var exists bool
	if conn.QueryRow(ctx, `SELECT to_regclass('public.platform_recovery_holds') IS NOT NULL`).Scan(&exists) != nil {
		return "", ErrUnconfirmed
	}
	if !exists {
		return "", nil
	}
	return `NOT EXISTS(SELECT 1 FROM public.platform_recovery_holds h WHERE h.kind='platform_directory_daily_runs' AND h.object_id=platform_directory_daily_runs.id) AND `, nil
}
