package store

import "context"

func (p *Postgres) RecordClientDiagnostic(ctx context.Context, item ClientDiagnostic) error {
	_, err := p.pool.Exec(ctx, `
WITH expired AS (
 SELECT id FROM im_client_diagnostics
 WHERE occurred_at < now()-interval '30 days'
 ORDER BY occurred_at LIMIT 1000
), cleanup AS (
 DELETE FROM im_client_diagnostics diagnostic USING expired
 WHERE diagnostic.id=expired.id RETURNING diagnostic.id
), recent AS (
 SELECT count(*) AS total FROM im_client_diagnostics
 WHERE user_id=$2 AND occurred_at > now()-interval '1 hour'
)
INSERT INTO im_client_diagnostics(id,user_id,kind,name,fingerprint,platform,app_version,duration_ms,occurred_at)
SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9 FROM recent WHERE total < 60
ON CONFLICT(id) DO NOTHING`, item.ID, item.UserID, item.Kind, item.Name, item.Fingerprint, item.Platform, item.AppVersion, item.DurationMS, item.OccurredAt)
	return err
}

func (p *Postgres) ListAdminClientDiagnostics(ctx context.Context, kind, platform string, limit int) ([]ClientDiagnostic, map[string]any, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx, `SELECT id,user_id,kind,name,fingerprint,platform,app_version,duration_ms,occurred_at
FROM im_client_diagnostics
WHERE ($1='' OR kind=$1) AND ($2='' OR platform=$2)
ORDER BY occurred_at DESC,id DESC LIMIT $3`, kind, platform, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := make([]ClientDiagnostic, 0, limit)
	for rows.Next() {
		var item ClientDiagnostic
		if err = rows.Scan(&item.ID, &item.UserID, &item.Kind, &item.Name, &item.Fingerprint, &item.Platform, &item.AppVersion, &item.DurationMS, &item.OccurredAt); err != nil {
			return nil, nil, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	var crashes, connections, calls, performanceSamples int64
	var performanceP95 *float64
	err = p.pool.QueryRow(ctx, `SELECT
 count(*) FILTER (WHERE kind='crash'),
 count(*) FILTER (WHERE kind='connection'),
 count(*) FILTER (WHERE kind='call'),
 count(*) FILTER (WHERE kind='performance' AND duration_ms IS NOT NULL),
 percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE kind='performance' AND duration_ms IS NOT NULL)
FROM im_client_diagnostics WHERE occurred_at > now()-interval '24 hours'`).Scan(&crashes, &connections, &calls, &performanceSamples, &performanceP95)
	if err != nil {
		return nil, nil, err
	}
	summary := map[string]any{"windowHours": 24, "crashes": crashes, "connectionFailures": connections, "callFailures": calls, "performanceSamples": performanceSamples}
	if performanceP95 != nil {
		summary["performanceP95Ms"] = int64(*performanceP95 + 0.5)
	}
	return items, summary, nil
}
