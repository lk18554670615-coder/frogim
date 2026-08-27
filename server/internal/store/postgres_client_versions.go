package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const clientVersionPolicyColumns = `platform,minimum_version,latest_version,force_update,rollout_percentage,release_notes,download_url,updated_by,updated_at`

func scanClientVersionPolicy(row pgx.Row) (*ClientVersionPolicy, error) {
	policy := &ClientVersionPolicy{}
	if err := row.Scan(
		&policy.Platform,
		&policy.MinimumVersion,
		&policy.LatestVersion,
		&policy.ForceUpdate,
		&policy.RolloutPercentage,
		&policy.ReleaseNotes,
		&policy.DownloadURL,
		&policy.UpdatedBy,
		&policy.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return policy, nil
}

func (p *Postgres) ListClientVersionPolicies(ctx context.Context) ([]ClientVersionPolicy, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+clientVersionPolicyColumns+` FROM im_client_version_policies ORDER BY platform`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ClientVersionPolicy, 0, 4)
	for rows.Next() {
		policy, scanErr := scanClientVersionPolicy(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, *policy)
	}
	return items, rows.Err()
}

func (p *Postgres) GetClientVersionPolicy(ctx context.Context, platform string) (*ClientVersionPolicy, error) {
	policy, err := scanClientVersionPolicy(p.pool.QueryRow(ctx, `SELECT `+clientVersionPolicyColumns+` FROM im_client_version_policies WHERE platform=$1`, platform))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return policy, err
}

func (p *Postgres) ListClientVersionHistory(ctx context.Context, platform, cursor string, limit int) ([]ClientVersionReleaseRecord, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE action='client_version_policy.updated' AND target_type='client_version_policy' AND target_id=$1`, platform).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id,target_id,
		 COALESCE(metadata->>'minimumVersion',''),COALESCE(metadata->>'latestVersion',''),
		 COALESCE((metadata->>'forceUpdate')::boolean,false),COALESCE(NULLIF(metadata->>'rolloutPercentage','')::int,100),
		 COALESCE(metadata->>'releaseNotes',''),COALESCE(metadata->>'downloadUrl',''),COALESCE(metadata->>'reason',''),
		 actor_id,created_at
		FROM im_audits
		WHERE action='client_version_policy.updated' AND target_type='client_version_policy' AND target_id=$1
		ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3
	`, platform, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]ClientVersionReleaseRecord, 0, limit)
	for rows.Next() {
		item := ClientVersionReleaseRecord{}
		if err = rows.Scan(&item.ID, &item.Platform, &item.MinimumVersion, &item.LatestVersion, &item.ForceUpdate, &item.RolloutPercentage, &item.ReleaseNotes, &item.DownloadURL, &item.Reason, &item.UpdatedBy, &item.UpdatedAt); err != nil {
			return nil, 0, "", err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func (p *Postgres) UpsertClientVersionPolicy(ctx context.Context, policy ClientVersionPolicy, actor, reason string, at time.Time) (*ClientVersionPolicy, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	updated, err := scanClientVersionPolicy(tx.QueryRow(ctx, `
		INSERT INTO im_client_version_policies(
		 platform,minimum_version,latest_version,force_update,rollout_percentage,
		 release_notes,download_url,updated_by,updated_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT(platform) DO UPDATE SET
		 minimum_version=excluded.minimum_version,
		 latest_version=excluded.latest_version,
		 force_update=excluded.force_update,
		 rollout_percentage=excluded.rollout_percentage,
		 release_notes=excluded.release_notes,
		 download_url=excluded.download_url,
		 updated_by=excluded.updated_by,
		 updated_at=excluded.updated_at
		RETURNING `+clientVersionPolicyColumns,
		policy.Platform, policy.MinimumVersion, policy.LatestVersion,
		policy.ForceUpdate, policy.RolloutPercentage, policy.ReleaseNotes,
		policy.DownloadURL, actor, at,
	))
	if err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(map[string]any{
		"reason":            reason,
		"minimumVersion":    policy.MinimumVersion,
		"latestVersion":     policy.LatestVersion,
		"forceUpdate":       policy.ForceUpdate,
		"rolloutPercentage": policy.RolloutPercentage,
		"releaseNotes":      policy.ReleaseNotes,
		"downloadUrl":       policy.DownloadURL,
	})
	if _, err = tx.Exec(ctx, `
		INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,result,created_at)
		VALUES($1,$2,'client_version_policy.updated','client_version_policy',$3,$4,'success',$5)
	`, "aud_client_version_"+policy.Platform+"_"+at.UTC().Format("20060102150405.000000000"), actor, policy.Platform, metadata, at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}
