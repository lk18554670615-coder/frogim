package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/linli/im/server/internal/model"
)

func (p *Postgres) CreateAdminPasswordUser(ctx context.Context, actor, phone, name, userID, hash, gender, reason string, at time.Time) (*model.User, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	user := &model.User{}
	err = tx.QueryRow(ctx, `INSERT INTO im_users(id,phone,name,handle,password_hash,password_updated_at,gender,created_at)
		VALUES($1,$2,$3,'gg_'||left(md5($1),20),$4,$6,$5,$6)
		RETURNING id,phone,name,COALESCE(handle,''),handle_change_count,signature,COALESCE(avatar_media_id,''),avatar_url,
			allow_search_by_handle,allow_search_by_phone,gender,banned,created_at`,
		userID, phone, name, hash, gender, at,
	).Scan(&user.ID, &user.Phone, &user.Name, &user.Handle, &user.HandleChangeCount, &user.Signature,
		&user.AvatarMediaID, &user.AvatarURL, &user.AllowSearchByHandle, &user.AllowSearchByPhone,
		&user.Gender, &user.Banned, &user.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(map[string]any{"method": "admin_phone_password", "reason": reason, "gender": gender})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,result,created_at)
		VALUES($1,$2,'user.created','user',$3,$4,'success',$5)`, "aud_admin_user_"+userID, actor, userID, metadata, at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return user, nil
}

func (p *Postgres) ListAdminUserFriends(ctx context.Context, userID string) ([]AdminUserRelation, error) {
	rows, err := p.pool.Query(ctx, `SELECT u.id,u.phone,u.name,COALESCE(u.handle,''),u.handle_change_count,u.signature,u.avatar_url,u.gender,
		(u.banned AND (u.banned_until IS NULL OR u.banned_until>now())),u.banned_until,u.created_at,
		COALESCE(presence.online,false),COALESCE(presence.total_online_count,0),presence.last_offline_at,
		f.remark,f.tags,f.created_at,f.updated_at
		FROM im_friendships f JOIN im_users u ON u.id=f.friend_user_id
		LEFT JOIN im_wukong_presence presence ON presence.user_id=u.id
		WHERE f.user_id=$1 ORDER BY lower(COALESCE(NULLIF(f.remark,''),u.name)),u.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AdminUserRelation{}
	for rows.Next() {
		item := AdminUserRelation{User: &model.User{}}
		if err = rows.Scan(&item.User.ID, &item.User.Phone, &item.User.Name, &item.User.Handle, &item.User.HandleChangeCount,
			&item.User.Signature, &item.User.AvatarURL, &item.User.Gender, &item.User.Banned, &item.User.BannedUntil,
			&item.User.CreatedAt, &item.User.Online, &item.User.OnlineConnections, &item.User.LastOfflineAt,
			&item.Remark, &item.Tags, &item.RelationshipCreatedAt, &item.RelationshipUpdatedAt); err != nil {
			return nil, err
		}
		item.User.Remark = item.Remark
		item.User.Tags = item.Tags
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) ListAdminUserBlocks(ctx context.Context, userID string) ([]AdminUserBlock, error) {
	rows, err := p.pool.Query(ctx, `SELECT u.id,u.phone,u.name,COALESCE(u.handle,''),u.handle_change_count,u.signature,u.avatar_url,u.gender,
		(u.banned AND (u.banned_until IS NULL OR u.banned_until>now())),u.banned_until,u.created_at,
		COALESCE(presence.online,false),COALESCE(presence.total_online_count,0),presence.last_offline_at,
		b.remark,b.created_at
		FROM im_blocks b JOIN im_users u ON u.id=b.blocked_user_id
		LEFT JOIN im_wukong_presence presence ON presence.user_id=u.id
		WHERE b.user_id=$1 ORDER BY b.created_at DESC,u.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AdminUserBlock{}
	for rows.Next() {
		item := AdminUserBlock{User: &model.User{}}
		if err = rows.Scan(&item.User.ID, &item.User.Phone, &item.User.Name, &item.User.Handle, &item.User.HandleChangeCount,
			&item.User.Signature, &item.User.AvatarURL, &item.User.Gender, &item.User.Banned, &item.User.BannedUntil,
			&item.User.CreatedAt, &item.User.Online, &item.User.OnlineConnections, &item.User.LastOfflineAt,
			&item.Remark, &item.BlockedAt); err != nil {
			return nil, err
		}
		item.User.Remark = item.Remark
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) FindDirectConversation(ctx context.Context, userID, otherID string) (*model.Conversation, error) {
	conversation := &model.Conversation{}
	err := p.pool.QueryRow(ctx, `SELECT c.id,c.kind,c.title,c.avatar_url,c.current_seq,c.last_message_seq,c.created_at,c.updated_at
		FROM im_direct_index d JOIN im_conversations c ON c.id=d.conversation_id WHERE d.pair_key=$1`,
		friendPairKey(userID, otherID)).Scan(&conversation.ID, &conversation.Type, &conversation.Title, &conversation.AvatarURL,
		&conversation.Seq, &conversation.LastMessageSeq, &conversation.CreatedAt, &conversation.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return conversation, err
}

func (p *Postgres) UpsertClientDevice(ctx context.Context, userID string, device ClientDevice) (*ClientDevice, error) {
	item := &ClientDevice{}
	err := p.pool.QueryRow(ctx, `INSERT INTO im_client_devices(user_id,installation_id,platform,device_name,device_model,os_version,app_version,first_seen_at,last_seen_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)
		ON CONFLICT(user_id,installation_id) DO UPDATE SET platform=excluded.platform,device_name=excluded.device_name,
			device_model=excluded.device_model,os_version=excluded.os_version,app_version=excluded.app_version,last_seen_at=excluded.last_seen_at
		RETURNING user_id,installation_id,platform,device_name,device_model,os_version,app_version,first_seen_at,last_seen_at`,
		userID, device.InstallationID, device.Platform, device.DeviceName, device.DeviceModel, device.OSVersion, device.AppVersion, device.LastSeenAt,
	).Scan(&item.UserID, &item.InstallationID, &item.Platform, &item.DeviceName, &item.DeviceModel, &item.OSVersion, &item.AppVersion, &item.FirstSeenAt, &item.LastSeenAt)
	return item, err
}

func (p *Postgres) ListClientDevices(ctx context.Context, userID string) ([]ClientDevice, error) {
	rows, err := p.pool.Query(ctx, `SELECT user_id,installation_id,platform,device_name,device_model,os_version,app_version,first_seen_at,last_seen_at
		FROM im_client_devices WHERE user_id=$1 ORDER BY last_seen_at DESC,installation_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClientDevice{}
	for rows.Next() {
		var item ClientDevice
		if err = rows.Scan(&item.UserID, &item.InstallationID, &item.Platform, &item.DeviceName, &item.DeviceModel,
			&item.OSVersion, &item.AppVersion, &item.FirstSeenAt, &item.LastSeenAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
