package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (p *Postgres) SaveWukongPluginRelease(ctx context.Context, input WukongPluginRelease) (*WukongPluginRelease, error) {
	manifest, err := json.Marshal(input.Manifest)
	if err != nil {
		return nil, err
	}
	row := p.pool.QueryRow(ctx, `
		INSERT INTO im_wukong_plugin_releases(plugin_no,node_id,name,file_name,version,methods,sha256,size_bytes,key_id,status,manifest,last_actor,last_reason,installed_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,now())
		ON CONFLICT(plugin_no) DO UPDATE SET node_id=EXCLUDED.node_id,name=EXCLUDED.name,file_name=EXCLUDED.file_name,version=EXCLUDED.version,methods=EXCLUDED.methods,sha256=EXCLUDED.sha256,size_bytes=EXCLUDED.size_bytes,key_id=EXCLUDED.key_id,status=EXCLUDED.status,manifest=EXCLUDED.manifest,last_actor=EXCLUDED.last_actor,last_reason=EXCLUDED.last_reason,installed_at=COALESCE(EXCLUDED.installed_at,im_wukong_plugin_releases.installed_at),updated_at=now()
		RETURNING plugin_no,node_id,name,file_name,version,methods,sha256,size_bytes,key_id,status,manifest,last_actor,last_reason,installed_at,updated_at`,
		input.PluginNo, input.NodeID, input.Name, input.FileName, input.Version, input.Methods, input.SHA256, input.SizeBytes, input.KeyID, input.Status, manifest, input.LastActor, input.LastReason, input.InstalledAt)
	return scanWukongPluginRelease(row)
}

func (p *Postgres) GetWukongPluginRelease(ctx context.Context, pluginNo string) (*WukongPluginRelease, error) {
	item, err := scanWukongPluginRelease(p.pool.QueryRow(ctx, `SELECT plugin_no,node_id,name,file_name,version,methods,sha256,size_bytes,key_id,status,manifest,last_actor,last_reason,installed_at,updated_at FROM im_wukong_plugin_releases WHERE plugin_no=$1`, pluginNo))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

func (p *Postgres) ListWukongPluginReleases(ctx context.Context) ([]*WukongPluginRelease, error) {
	rows, err := p.pool.Query(ctx, `SELECT plugin_no,node_id,name,file_name,version,methods,sha256,size_bytes,key_id,status,manifest,last_actor,last_reason,installed_at,updated_at FROM im_wukong_plugin_releases ORDER BY updated_at DESC,plugin_no`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*WukongPluginRelease{}
	for rows.Next() {
		item, scanErr := scanWukongPluginRelease(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanWukongPluginRelease(row rowScanner) (*WukongPluginRelease, error) {
	item := &WukongPluginRelease{}
	var manifest []byte
	if err := row.Scan(&item.PluginNo, &item.NodeID, &item.Name, &item.FileName, &item.Version, &item.Methods, &item.SHA256, &item.SizeBytes, &item.KeyID, &item.Status, &manifest, &item.LastActor, &item.LastReason, &item.InstalledAt, &item.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(manifest, &item.Manifest); err != nil {
		return nil, err
	}
	return item, nil
}

func (p *Postgres) RecordWukongPluginEvent(ctx context.Context, input WukongPluginEvent) error {
	details, err := json.Marshal(input.Details)
	if err != nil {
		return err
	}
	var createdAt any
	if !input.CreatedAt.IsZero() {
		createdAt = input.CreatedAt
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO im_wukong_plugin_events(plugin_no,action,status,actor_id,reason,details,created_at) VALUES($1,$2,$3,$4,$5,$6,COALESCE($7,now()))`, input.PluginNo, input.Action, input.Status, input.Actor, input.Reason, details, createdAt)
	return err
}

func (p *Postgres) ListWukongPluginEvents(ctx context.Context, pluginNo string, limit int) ([]*WukongPluginEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := p.pool.Query(ctx, `SELECT id,plugin_no,action,status,actor_id,reason,details,created_at FROM im_wukong_plugin_events WHERE ($1='' OR plugin_no=$1) ORDER BY id DESC LIMIT $2`, pluginNo, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*WukongPluginEvent{}
	for rows.Next() {
		item := &WukongPluginEvent{}
		var details []byte
		if err = rows.Scan(&item.ID, &item.PluginNo, &item.Action, &item.Status, &item.Actor, &item.Reason, &details, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(details, &item.Details); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *WithRedis) SaveWukongPluginRelease(ctx context.Context, input WukongPluginRelease) (*WukongPluginRelease, error) {
	if lifecycle, ok := p.base.(WukongPluginLifecycleStore); ok {
		return lifecycle.SaveWukongPluginRelease(ctx, input)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) GetWukongPluginRelease(ctx context.Context, pluginNo string) (*WukongPluginRelease, error) {
	if lifecycle, ok := p.base.(WukongPluginLifecycleStore); ok {
		return lifecycle.GetWukongPluginRelease(ctx, pluginNo)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListWukongPluginReleases(ctx context.Context) ([]*WukongPluginRelease, error) {
	if lifecycle, ok := p.base.(WukongPluginLifecycleStore); ok {
		return lifecycle.ListWukongPluginReleases(ctx)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) RecordWukongPluginEvent(ctx context.Context, input WukongPluginEvent) error {
	if lifecycle, ok := p.base.(WukongPluginLifecycleStore); ok {
		return lifecycle.RecordWukongPluginEvent(ctx, input)
	}
	return ErrUnsupported
}

func (p *WithRedis) ListWukongPluginEvents(ctx context.Context, pluginNo string, limit int) ([]*WukongPluginEvent, error) {
	if lifecycle, ok := p.base.(WukongPluginLifecycleStore); ok {
		return lifecycle.ListWukongPluginEvents(ctx, pluginNo, limit)
	}
	return nil, ErrUnsupported
}
