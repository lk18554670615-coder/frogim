package platform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const legacyExpiryActor = "system-legacy-expiry"
const legacyExpiryReason = "Imported temporary ban reached its original expiry"

// LegacyBanExpiryWorker requests the normal durable unblock; it never writes
// account access directly and cannot lift a later operator-imposed ban.
type LegacyBanExpiryWorker struct{ Store *Store }

func (w LegacyBanExpiryWorker) Once(ctx context.Context) (bool, error) {
	var account, request, state, job string
	var expected int64
	e := w.Store.pool.QueryRow(ctx, `SELECT account_id,request_id,state,COALESCE(access_job_id,''),expected_auth_version FROM platform_legacy_ban_expiries WHERE NOT EXISTS(SELECT 1 FROM platform_recovery_holds h WHERE h.kind='platform_legacy_ban_expiries' AND h.object_id=platform_legacy_ban_expiries.account_id) AND state IN ('pending','queued') AND expires_at<=clock_timestamp() AND retry_at<=clock_timestamp() ORDER BY (state='queued'),expires_at,account_id LIMIT 1`).Scan(&account, &request, &state, &job, &expected)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if state == "pending" {
		j, err := w.Store.RequestAccess(ctx, account, request, legacyExpiryActor, legacyExpiryReason, expected, false, true)
		if err == nil {
			_, e = w.Store.pool.Exec(ctx, `UPDATE platform_legacy_ban_expiries SET state='queued',access_job_id=$2,retry_at=clock_timestamp()+interval '1 second',updated_at=now() WHERE account_id=$1 AND state='pending'`, account, j.ID)
			return true, e
		}
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrDenied) {
			// This condition is checked by SQL again, so another worker's accepted
			// expiration task cannot be classified as a superseding human change.
			_, e = w.Store.pool.Exec(ctx, `UPDATE platform_legacy_ban_expiries b SET state='superseded',updated_at=now() FROM platform_accounts a WHERE b.account_id=$1 AND a.id=b.account_id AND b.state='pending'
AND (a.auth_version<>b.expected_auth_version OR a.state='deleted' OR (a.state='active' AND NOT a.globally_blocked))
AND NOT EXISTS(SELECT 1 FROM platform_access_jobs j WHERE j.actor_id=$2 AND j.request_id=b.request_id)`, account, legacyExpiryActor)
			if e != nil {
				return true, e
			}
		}
	} else {
		_, e = w.Store.pool.Exec(ctx, `UPDATE platform_legacy_ban_expiries b SET state='completed',updated_at=now() WHERE account_id=$1 AND state='queued' AND EXISTS(SELECT 1 FROM platform_access_jobs j WHERE j.id=b.access_job_id AND j.id=$2 AND j.actor_id=$3 AND j.request_id=b.request_id AND j.account_id=b.account_id AND NOT j.blocked AND j.state='completed')`, account, job, legacyExpiryActor)
		if e != nil {
			return true, e
		}
	}
	_, e = w.Store.pool.Exec(ctx, `UPDATE platform_legacy_ban_expiries SET retry_at=clock_timestamp()+interval '5 seconds' WHERE account_id=$1 AND state IN ('pending','queued')`, account)
	return true, e
}
func (w LegacyBanExpiryWorker) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			call, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, _ = w.Once(call)
			cancel()
		}
	}
}
