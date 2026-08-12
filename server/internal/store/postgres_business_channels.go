package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/wukong"
)

func businessChannelCategory(channelType int) (string, bool) {
	switch channelType {
	case int(wukong.ChannelCustomer):
		return "customer_service", true
	case int(wukong.ChannelCommunity):
		return "community", true
	case int(wukong.ChannelCommunityTopic):
		return "community_topic", true
	case int(wukong.ChannelInfo):
		return "info", true
	case int(wukong.ChannelLive):
		return "live", true
	case int(wukong.ChannelVisitor):
		return "visitor", true
	default:
		return "", false
	}
}

func publicBusinessChannelType(channelType int) bool {
	return channelType == int(wukong.ChannelCommunity) ||
		channelType == int(wukong.ChannelCommunityTopic) ||
		channelType == int(wukong.ChannelInfo) ||
		channelType == int(wukong.ChannelLive)
}

func validBusinessChannelID(id, parentID string, channelType int) bool {
	id, parentID = strings.TrimSpace(id), strings.TrimSpace(parentID)
	if id == "" || len(id) > 160 || strings.ContainsAny(id, "|#&") {
		return false
	}
	if channelType == int(wukong.ChannelCommunityTopic) {
		parts := strings.Split(id, "@")
		return parentID != "" && len(parts) == 2 && parts[0] == parentID && parts[1] != ""
	}
	return parentID == "" && !strings.Contains(id, "@")
}

func validBusinessChannelSettings(visibility, joinPolicy, postingPolicy string, slowMode int) bool {
	return (visibility == "public" || visibility == "private") &&
		(joinPolicy == "open" || joinPolicy == "approval" || joinPolicy == "invite" || joinPolicy == "closed") &&
		(postingPolicy == "members" || postingPolicy == "operators") &&
		slowMode >= 0 && slowMode <= 86400
}

func (p *Postgres) CreateBusinessChannel(ctx context.Context, input BusinessChannelCreate, at time.Time) (*BusinessChannel, error) {
	input.ID, input.ActorID, input.Name = strings.TrimSpace(input.ID), strings.TrimSpace(input.ActorID), strings.TrimSpace(input.Name)
	input.AvatarURL, input.ParentID, input.Description = strings.TrimSpace(input.AvatarURL), strings.TrimSpace(input.ParentID), strings.TrimSpace(input.Description)
	input.Visibility, input.JoinPolicy, input.PostingPolicy = strings.TrimSpace(input.Visibility), strings.TrimSpace(input.JoinPolicy), strings.TrimSpace(input.PostingPolicy)
	category, supported := businessChannelCategory(input.ChannelType)
	// Types 3 and 10 have session-specific IDs in the pinned WuKongIM server;
	// only the dedicated support workflow may create them.
	if !supported || !publicBusinessChannelType(input.ChannelType) || input.ActorID == "" || input.Name == "" || len(input.Name) > 100 ||
		len(input.Description) > 2000 || !validBusinessChannelID(input.ID, input.ParentID, input.ChannelType) ||
		!validBusinessChannelSettings(input.Visibility, input.JoinPolicy, input.PostingPolicy, input.SlowModeSeconds) {
		return nil, ErrConflict
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	metadata, err := json.Marshal(input.Metadata)
	if err != nil || len(metadata) > 64<<10 {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var actorAvailable bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1 AND NOT banned AND deleted_at IS NULL)`, input.ActorID).Scan(&actorAvailable); err != nil {
		return nil, err
	}
	if !actorAvailable {
		return nil, ErrForbidden
	}
	if input.ChannelType == int(wukong.ChannelCommunityTopic) {
		var role string
		err = tx.QueryRow(ctx, `SELECT member.role FROM im_business_channels channel
			JOIN im_members member ON member.conversation_id=channel.conversation_id AND member.user_id=$2
				AND (member.expires_at IS NULL OR member.expires_at>$3)
			WHERE channel.conversation_id=$1 AND channel.channel_type=4 AND NOT channel.disband`, input.ParentID, input.ActorID, at).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && role != "owner" && role != "admin" && role != "moderator") {
			return nil, ErrForbidden
		}
		if err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,avatar_url,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$5)`, input.ID, category, input.Name, input.AvatarURL, at); err != nil {
		return nil, mapBusinessChannelConstraint(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_business_channels(
		conversation_id,channel_type,category,owner_id,parent_id,description,visibility,join_policy,
		posting_policy,slow_mode_seconds,metadata,created_at,updated_at
	) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9,$10,$11,$12,$12)`,
		input.ID, input.ChannelType, category, input.ActorID, input.ParentID, input.Description,
		input.Visibility, input.JoinPolicy, input.PostingPolicy, input.SlowModeSeconds, metadata, at); err != nil {
		return nil, mapBusinessChannelConstraint(err)
	}
	if input.ChannelType == int(wukong.ChannelCommunityTopic) {
		// Topic membership is independent in WuKongIM. Seed it from the parent
		// community so every current community member is an actual subscriber.
		if _, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,expires_at,joined_at)
			SELECT $1,parent_member.user_id,
				CASE WHEN parent_member.user_id=$2 THEN 'owner' WHEN parent_member.role='owner' THEN 'admin' ELSE parent_member.role END,
				parent_member.expires_at,$3
			FROM im_members parent_member WHERE parent_member.conversation_id=$4
			AND (parent_member.expires_at IS NULL OR parent_member.expires_at>$3)`,
			input.ID, input.ActorID, at, input.ParentID); err != nil {
			return nil, err
		}
	} else if _, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'owner',$3)`, input.ID, input.ActorID, at); err != nil {
		return nil, err
	}
	if err = enqueueWukongChannelReconcileTyped(ctx, tx, input.ID, uint8(input.ChannelType), "created", at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.GetBusinessChannel(ctx, input.ActorID, input.ID, input.ChannelType)
}

func mapBusinessChannelConstraint(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	if strings.Contains(text, "duplicate key") || strings.Contains(text, "violates check constraint") || strings.Contains(text, "violates foreign key") {
		return ErrConflict
	}
	return err
}

func scanBusinessChannel(row pgx.Row) (*BusinessChannel, error) {
	item := &BusinessChannel{}
	var metadata []byte
	var subscribed bool
	err := row.Scan(&item.ID, &item.ChannelType, &item.Category, &item.Name, &item.AvatarURL,
		&item.OwnerID, &item.ParentID, &item.Description, &item.Visibility, &item.JoinPolicy,
		&item.PostingPolicy, &item.SlowModeSeconds, &item.Ban, &item.Disband, &item.SendBan,
		&item.AllowStranger, &metadata, &item.CreatedAt, &item.UpdatedAt, &item.MemberCount,
		&subscribed, &item.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	item.Subscribed = subscribed
	if err = json.Unmarshal(metadata, &item.Metadata); err != nil {
		return nil, err
	}
	return item, nil
}

const businessChannelSelect = `SELECT channel.conversation_id,channel.channel_type,channel.category,
	conversation.title,conversation.avatar_url,channel.owner_id,COALESCE(channel.parent_id,''),channel.description,
	channel.visibility,channel.join_policy,channel.posting_policy,channel.slow_mode_seconds,
	channel.ban,channel.disband,channel.send_ban,channel.allow_stranger,channel.metadata,
	channel.created_at,channel.updated_at,conversation.member_count,(own.user_id IS NOT NULL),COALESCE(own.role,'')
	FROM im_business_channels channel JOIN im_conversations conversation ON conversation.id=channel.conversation_id
	LEFT JOIN im_members own ON own.conversation_id=channel.conversation_id AND own.user_id=$1
		AND (own.expires_at IS NULL OR own.expires_at>now())`

func (p *Postgres) GetBusinessChannel(ctx context.Context, userID, channelID string, channelType int) (*BusinessChannel, error) {
	userID, channelID = strings.TrimSpace(userID), strings.TrimSpace(channelID)
	if userID == "" || channelID == "" {
		return nil, ErrForbidden
	}
	item, err := scanBusinessChannel(p.pool.QueryRow(ctx, businessChannelSelect+`
		WHERE channel.conversation_id=$2 AND channel.channel_type=$3
		AND (channel.visibility='public' OR own.user_id IS NOT NULL)
		AND NOT EXISTS(SELECT 1 FROM im_business_channel_access denied
			WHERE denied.conversation_id=channel.conversation_id AND denied.user_id=$1 AND denied.access_type='deny')`, userID, channelID, channelType))
	if err == ErrNotFound {
		return nil, ErrForbidden
	}
	return item, err
}

func (p *Postgres) ListBusinessChannels(ctx context.Context, userID, category, parentID string, channelType int, after string, limit int) ([]*BusinessChannel, string, error) {
	userID, category, parentID, after = strings.TrimSpace(userID), strings.TrimSpace(category), strings.TrimSpace(parentID), strings.TrimSpace(after)
	if userID == "" {
		return nil, "", ErrForbidden
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return p.listBusinessChannelsPage(ctx, userID, category, parentID, channelType, after, limit)
}

func (p *Postgres) listBusinessChannelsPage(ctx context.Context, userID, category, parentID string, channelType int, after string, limit int) ([]*BusinessChannel, string, error) {
	rows, err := p.pool.Query(ctx, businessChannelSelect+`
		WHERE channel.conversation_id>$2
		AND channel.channel_type IN (4,5,6,9)
		AND ($3='' OR channel.category=$3)
		AND ($4='' OR COALESCE(channel.parent_id,'')=$4)
		AND ($5=0 OR channel.channel_type=$5)
		AND (channel.visibility='public' OR own.user_id IS NOT NULL)
		AND NOT channel.disband
		AND NOT EXISTS(SELECT 1 FROM im_business_channel_access denied
			WHERE denied.conversation_id=channel.conversation_id AND denied.user_id=$1 AND denied.access_type='deny')
		ORDER BY channel.conversation_id LIMIT $6`, userID, after, category, parentID, channelType, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]*BusinessChannel, 0, limit)
	for rows.Next() {
		if len(items) == limit {
			return items, items[len(items)-1].ID, nil
		}
		item, scanErr := scanBusinessChannel(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		items = append(items, item)
	}
	return items, "", rows.Err()
}

func (p *Postgres) UpdateBusinessChannel(ctx context.Context, channelID string, channelType int, update BusinessChannelUpdate) (*BusinessChannel, error) {
	channelID, update.ActorID = strings.TrimSpace(channelID), strings.TrimSpace(update.ActorID)
	if channelID == "" || update.ActorID == "" {
		return nil, ErrForbidden
	}
	if update.At.IsZero() {
		update.At = time.Now()
	}
	if update.Name != nil {
		trimmed := strings.TrimSpace(*update.Name)
		if trimmed == "" || len(trimmed) > 100 {
			return nil, ErrConflict
		}
		update.Name = &trimmed
	}
	if update.Description != nil {
		trimmed := strings.TrimSpace(*update.Description)
		if len(trimmed) > 2000 {
			return nil, ErrConflict
		}
		update.Description = &trimmed
	}
	if update.Visibility != nil && *update.Visibility != "public" && *update.Visibility != "private" {
		return nil, ErrConflict
	}
	if update.JoinPolicy != nil && *update.JoinPolicy != "open" && *update.JoinPolicy != "approval" && *update.JoinPolicy != "invite" && *update.JoinPolicy != "closed" {
		return nil, ErrConflict
	}
	if update.PostingPolicy != nil && *update.PostingPolicy != "members" && *update.PostingPolicy != "operators" {
		return nil, ErrConflict
	}
	if update.SlowModeSeconds != nil && (*update.SlowModeSeconds < 0 || *update.SlowModeSeconds > 86400) {
		return nil, ErrConflict
	}
	metadata := []byte(nil)
	var err error
	if update.MetadataSet {
		if update.Metadata == nil {
			update.Metadata = map[string]any{}
		}
		metadata, err = json.Marshal(update.Metadata)
		if err != nil || len(metadata) > 64<<10 {
			return nil, ErrConflict
		}
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var role string
	if err = tx.QueryRow(ctx, `SELECT member.role FROM im_business_channels channel
		JOIN im_members member ON member.conversation_id=channel.conversation_id AND member.user_id=$3
			AND (member.expires_at IS NULL OR member.expires_at>$4)
		WHERE channel.conversation_id=$1 AND channel.channel_type=$2 FOR UPDATE OF channel`, channelID, channelType, update.ActorID, update.At).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrForbidden
	} else if err != nil {
		return nil, err
	}
	if role != "owner" && role != "admin" && role != "moderator" {
		return nil, ErrForbidden
	}
	if update.Disband != nil && *update.Disband && role != "owner" {
		return nil, ErrForbidden
	}
	if _, err = tx.Exec(ctx, `UPDATE im_business_channels SET
		description=COALESCE($3,description),visibility=COALESCE($4,visibility),join_policy=COALESCE($5,join_policy),
		posting_policy=COALESCE($6,posting_policy),slow_mode_seconds=COALESCE($7,slow_mode_seconds),
		ban=COALESCE($8,ban),disband=COALESCE($9,disband),send_ban=COALESCE($10,send_ban),
		allow_stranger=COALESCE($11,allow_stranger),metadata=CASE WHEN $12::boolean THEN $13::jsonb ELSE metadata END,
		updated_at=$14 WHERE conversation_id=$1 AND channel_type=$2`, channelID, channelType,
		update.Description, update.Visibility, update.JoinPolicy, update.PostingPolicy, update.SlowModeSeconds,
		update.Ban, update.Disband, update.SendBan, update.AllowStranger, update.MetadataSet, metadata, update.At); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_conversations SET title=COALESCE($2,title),avatar_url=COALESCE($3,avatar_url),updated_at=$4 WHERE id=$1`,
		channelID, update.Name, update.AvatarURL, update.At); err != nil {
		return nil, err
	}
	if err = enqueueWukongChannelReconcileTyped(ctx, tx, channelID, uint8(channelType), "updated", update.At); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.GetBusinessChannel(ctx, update.ActorID, channelID, channelType)
}

func businessChannelOperator(role string) bool {
	return role == "owner" || role == "admin" || role == "moderator"
}

func (p *Postgres) ApplyBusinessChannelMemberAction(ctx context.Context, action BusinessChannelMemberAction) error {
	action.ActorID, action.ChannelID, action.TargetID = strings.TrimSpace(action.ActorID), strings.TrimSpace(action.ChannelID), strings.TrimSpace(action.TargetID)
	action.Action, action.Role = strings.TrimSpace(action.Action), strings.TrimSpace(action.Role)
	if action.ActorID == "" || action.ChannelID == "" || action.TargetID == "" || action.At.IsZero() {
		return ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var actorRole, joinPolicy string
	var currentRole *string
	err = tx.QueryRow(ctx, `SELECT COALESCE(actor.role,''),channel.join_policy,
		(SELECT target.role FROM im_members target WHERE target.conversation_id=channel.conversation_id AND target.user_id=$4
		 AND (target.expires_at IS NULL OR target.expires_at>$5))
		FROM im_business_channels channel LEFT JOIN im_members actor
		ON actor.conversation_id=channel.conversation_id AND actor.user_id=$3 AND (actor.expires_at IS NULL OR actor.expires_at>$5)
		WHERE channel.conversation_id=$1 AND channel.channel_type=$2 AND NOT channel.disband FOR UPDATE OF channel`,
		action.ChannelID, action.ChannelType, action.ActorID, action.TargetID, action.At).Scan(&actorRole, &joinPolicy, &currentRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	operator := businessChannelOperator(actorRole)
	switch action.Action {
	case "subscribe", "add":
		selfSubscribe := action.ActorID == action.TargetID && action.Action == "subscribe"
		if !operator && (!selfSubscribe || joinPolicy != "open") {
			return ErrForbidden
		}
		if currentRole == nil {
			if action.ExpiresAt != nil && !action.ExpiresAt.After(action.At) {
				return ErrConflict
			}
			if _, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,muted_until,expires_at,joined_at)
				SELECT $1,$2,'member',$3,$4,$5 FROM im_users WHERE id=$2 AND NOT banned AND deleted_at IS NULL
				ON CONFLICT(conversation_id,user_id) DO UPDATE SET role='member',muted_until=excluded.muted_until,
				expires_at=excluded.expires_at,joined_at=excluded.joined_at`,
				action.ChannelID, action.TargetID, action.MutedUntil, action.ExpiresAt, action.At); err != nil {
				return err
			}
		}
	case "unsubscribe", "remove":
		selfRemove := action.ActorID == action.TargetID
		if currentRole == nil {
			return tx.Commit(ctx)
		}
		if *currentRole == "owner" || (!selfRemove && !operator) {
			return ErrForbidden
		}
		if _, err = tx.Exec(ctx, `DELETE FROM im_members WHERE conversation_id=$1 AND user_id=$2`, action.ChannelID, action.TargetID); err != nil {
			return err
		}
	case "role":
		if actorRole != "owner" || currentRole == nil || *currentRole == "owner" ||
			(action.Role != "member" && action.Role != "moderator" && action.Role != "admin") {
			return ErrForbidden
		}
		if _, err = tx.Exec(ctx, `UPDATE im_members SET role=$3 WHERE conversation_id=$1 AND user_id=$2`, action.ChannelID, action.TargetID, action.Role); err != nil {
			return err
		}
	case "mute":
		if !operator || currentRole == nil || *currentRole == "owner" || (*currentRole == "admin" && actorRole != "owner") {
			return ErrForbidden
		}
		if _, err = tx.Exec(ctx, `UPDATE im_members SET muted_until=$3 WHERE conversation_id=$1 AND user_id=$2`, action.ChannelID, action.TargetID, action.MutedUntil); err != nil {
			return err
		}
	case "expiry":
		if !operator || currentRole == nil || *currentRole == "owner" || (action.ExpiresAt != nil && !action.ExpiresAt.After(action.At)) {
			return ErrForbidden
		}
		if _, err = tx.Exec(ctx, `UPDATE im_members SET expires_at=$3 WHERE conversation_id=$1 AND user_id=$2`, action.ChannelID, action.TargetID, action.ExpiresAt); err != nil {
			return err
		}
	default:
		return ErrConflict
	}
	// Community membership is inherited by every topic. This mirrors the
	// client SDK's parent-channel grouping while retaining real WuKong
	// subscribers on each topic channel.
	if action.ChannelType == int(wukong.ChannelCommunity) {
		if action.Action == "subscribe" || action.Action == "add" {
			if _, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,muted_until,expires_at,joined_at)
				SELECT topic.conversation_id,$2,'member',$3,$4,$5 FROM im_business_channels topic
				WHERE topic.parent_id=$1 AND topic.channel_type=5 AND NOT topic.disband
				ON CONFLICT(conversation_id,user_id) DO UPDATE SET expires_at=excluded.expires_at`, action.ChannelID, action.TargetID, action.MutedUntil, action.ExpiresAt, action.At); err != nil {
				return err
			}
		} else if action.Action == "unsubscribe" || action.Action == "remove" {
			if _, err = tx.Exec(ctx, `DELETE FROM im_members member USING im_business_channels topic
				WHERE topic.parent_id=$1 AND topic.channel_type=5 AND member.conversation_id=topic.conversation_id
				AND member.user_id=$2 AND member.role<>'owner'`, action.ChannelID, action.TargetID); err != nil {
				return err
			}
		} else if action.Action == "expiry" {
			if _, err = tx.Exec(ctx, `UPDATE im_members member SET expires_at=$3 FROM im_business_channels topic
				WHERE topic.parent_id=$1 AND topic.channel_type=5 AND member.conversation_id=topic.conversation_id
				AND member.user_id=$2 AND member.role<>'owner'`, action.ChannelID, action.TargetID, action.ExpiresAt); err != nil {
				return err
			}
		}
	}
	if err = enqueueWukongChannelReconcileTyped(ctx, tx, action.ChannelID, uint8(action.ChannelType), "member-"+action.Action, action.At); err != nil {
		return err
	}
	if action.ChannelType == int(wukong.ChannelCommunity) {
		rows, queryErr := tx.Query(ctx, `SELECT conversation_id FROM im_business_channels WHERE parent_id=$1 AND channel_type=5`, action.ChannelID)
		if queryErr != nil {
			return queryErr
		}
		topicIDs := make([]string, 0)
		for rows.Next() {
			var topicID string
			if err = rows.Scan(&topicID); err != nil {
				rows.Close()
				return err
			}
			topicIDs = append(topicIDs, topicID)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		for _, topicID := range topicIDs {
			if err = enqueueWukongChannelReconcileTyped(ctx, tx, topicID, wukong.ChannelCommunityTopic, "parent-member-"+action.Action, action.At); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListBusinessChannelMembers(ctx context.Context, actorID, channelID string, channelType int, after string, limit int) ([]*BusinessChannelMember, string, error) {
	actorID, channelID, after = strings.TrimSpace(actorID), strings.TrimSpace(channelID), strings.TrimSpace(after)
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var allowed bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_business_channels channel
		JOIN im_members member ON member.conversation_id=channel.conversation_id AND member.user_id=$1
		WHERE channel.conversation_id=$2 AND channel.channel_type=$3
		AND (member.expires_at IS NULL OR member.expires_at>now()))`, actorID, channelID, channelType).Scan(&allowed); err != nil {
		return nil, "", err
	}
	if !allowed {
		return nil, "", ErrForbidden
	}
	rows, err := p.pool.Query(ctx, `SELECT member.conversation_id,member.user_id,user_row.name,COALESCE(user_row.handle,''),
		user_row.avatar_url,member.role,member.muted_until,member.expires_at,member.joined_at,
		GREATEST(member.joined_at,user_row.updated_at)
		FROM im_members member JOIN im_users user_row ON user_row.id=member.user_id
		WHERE member.conversation_id=$1 AND member.user_id>$2
		AND (member.expires_at IS NULL OR member.expires_at>now()) ORDER BY member.user_id LIMIT $3`, channelID, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]*BusinessChannelMember, 0, limit)
	for rows.Next() {
		if len(items) == limit {
			return items, items[len(items)-1].UserID, nil
		}
		item := &BusinessChannelMember{}
		if err = rows.Scan(&item.ChannelID, &item.UserID, &item.Name, &item.Handle, &item.AvatarURL,
			&item.Role, &item.MutedUntil, &item.ExpiresAt, &item.JoinedAt, &item.UpdatedAt); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	return items, "", rows.Err()
}

func (p *Postgres) ApplyBusinessChannelAccess(ctx context.Context, action BusinessChannelAccessAction) error {
	action.ActorID, action.ChannelID, action.TargetID = strings.TrimSpace(action.ActorID), strings.TrimSpace(action.ChannelID), strings.TrimSpace(action.TargetID)
	action.AccessType, action.Reason = strings.TrimSpace(action.AccessType), strings.TrimSpace(action.Reason)
	if action.ActorID == "" || action.ChannelID == "" || action.TargetID == "" ||
		(action.AccessType != "allow" && action.AccessType != "deny") || len(action.Reason) > 500 || action.At.IsZero() {
		return ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var role string
	if err = tx.QueryRow(ctx, `SELECT member.role FROM im_business_channels channel
		JOIN im_members member ON member.conversation_id=channel.conversation_id AND member.user_id=$3
			AND (member.expires_at IS NULL OR member.expires_at>$4)
		WHERE channel.conversation_id=$1 AND channel.channel_type=$2 FOR UPDATE OF channel`,
		action.ChannelID, action.ChannelType, action.ActorID, action.At).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	} else if err != nil {
		return err
	}
	if !businessChannelOperator(role) || action.TargetID == action.ActorID {
		return ErrForbidden
	}
	if action.Enabled {
		if _, err = tx.Exec(ctx, `INSERT INTO im_business_channel_access(conversation_id,user_id,access_type,reason,created_by,created_at)
			VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(conversation_id,user_id,access_type)
			DO UPDATE SET reason=excluded.reason,created_by=excluded.created_by,created_at=excluded.created_at`,
			action.ChannelID, action.TargetID, action.AccessType, action.Reason, action.ActorID, action.At); err != nil {
			return err
		}
		other := "allow"
		if action.AccessType == "allow" {
			other = "deny"
		}
		if _, err = tx.Exec(ctx, `DELETE FROM im_business_channel_access WHERE conversation_id=$1 AND user_id=$2 AND access_type=$3`, action.ChannelID, action.TargetID, other); err != nil {
			return err
		}
	} else if _, err = tx.Exec(ctx, `DELETE FROM im_business_channel_access WHERE conversation_id=$1 AND user_id=$2 AND access_type=$3`, action.ChannelID, action.TargetID, action.AccessType); err != nil {
		return err
	}
	if err = enqueueWukongChannelReconcileTyped(ctx, tx, action.ChannelID, uint8(action.ChannelType), "access-"+action.AccessType, action.At); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) AuthorizeBusinessChannelSend(ctx context.Context, userID, channelID string, channelType int, at time.Time) error {
	userID, channelID = strings.TrimSpace(userID), strings.TrimSpace(channelID)
	if userID == "" || channelID == "" || at.IsZero() {
		return ErrForbidden
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var role, postingPolicy string
	var mutedUntil *time.Time
	var slowMode int
	var banned, channelBan, disband, sendBan, denied, allowlistOn, allowed bool
	err = tx.QueryRow(ctx, `SELECT member.role,member.muted_until,user_row.banned,channel.posting_policy,
		channel.slow_mode_seconds,channel.ban,channel.disband,channel.send_ban,
		EXISTS(SELECT 1 FROM im_business_channel_access access WHERE access.conversation_id=channel.conversation_id AND access.user_id=$1 AND access.access_type='deny'),
		EXISTS(SELECT 1 FROM im_business_channel_access access WHERE access.conversation_id=channel.conversation_id AND access.access_type='allow'),
		EXISTS(SELECT 1 FROM im_business_channel_access access WHERE access.conversation_id=channel.conversation_id AND access.user_id=$1 AND access.access_type='allow')
		FROM im_business_channels channel JOIN im_members member ON member.conversation_id=channel.conversation_id AND member.user_id=$1
			AND (member.expires_at IS NULL OR member.expires_at>$4)
		JOIN im_users user_row ON user_row.id=member.user_id
		WHERE channel.conversation_id=$2 AND channel.channel_type=$3 FOR UPDATE OF channel`, userID, channelID, channelType, at).Scan(
		&role, &mutedUntil, &banned, &postingPolicy, &slowMode, &channelBan, &disband, &sendBan, &denied, &allowlistOn, &allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if banned || channelBan || disband || sendBan || denied || (allowlistOn && !allowed) ||
		(mutedUntil != nil && mutedUntil.After(at)) || (postingPolicy == "operators" && !businessChannelOperator(role)) {
		return ErrForbidden
	}
	if slowMode > 0 && !businessChannelOperator(role) {
		tag, execErr := tx.Exec(ctx, `INSERT INTO im_business_channel_send_state(conversation_id,user_id,last_sent_at)
			VALUES($1,$2,$3) ON CONFLICT(conversation_id,user_id) DO UPDATE SET last_sent_at=excluded.last_sent_at
			WHERE im_business_channel_send_state.last_sent_at<=excluded.last_sent_at-make_interval(secs=>$4)`, channelID, userID, at, slowMode)
		if execErr != nil {
			return execErr
		}
		if tag.RowsAffected() != 1 {
			return ErrForbidden
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListAdminBusinessChannels(ctx context.Context, query, category string, channelType int, after string, limit int) ([]*BusinessChannel, int64, string, error) {
	query, category, after = strings.TrimSpace(query), strings.TrimSpace(category), strings.TrimSpace(after)
	if len(query) > 200 || (channelType != 0 && !publicBusinessChannelType(channelType)) {
		return nil, 0, "", ErrConflict
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	pattern := "%" + query + "%"
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_business_channels channel
		JOIN im_conversations conversation ON conversation.id=channel.conversation_id
		WHERE channel.channel_type IN (4,5,6,9)
		AND ($1='' OR channel.conversation_id ILIKE $2 OR conversation.title ILIKE $2)
		AND ($3='' OR channel.category=$3) AND ($4=0 OR channel.channel_type=$4)`,
		query, pattern, category, channelType).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, businessChannelSelect+`
		WHERE channel.conversation_id>$2 AND channel.channel_type IN (4,5,6,9)
		AND ($3='' OR channel.conversation_id ILIKE $4 OR conversation.title ILIKE $4)
		AND ($5='' OR channel.category=$5) AND ($6=0 OR channel.channel_type=$6)
		ORDER BY channel.conversation_id LIMIT $7`, "", after, query, pattern, category, channelType, limit+1)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]*BusinessChannel, 0, limit)
	for rows.Next() {
		if len(items) == limit {
			return items, total, items[len(items)-1].ID, nil
		}
		item, scanErr := scanBusinessChannel(rows)
		if scanErr != nil {
			return nil, 0, "", scanErr
		}
		items = append(items, item)
	}
	return items, total, "", rows.Err()
}

func (p *Postgres) AdminBusinessChannelOwner(ctx context.Context, channelID string, channelType int) (string, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || !publicBusinessChannelType(channelType) {
		return "", ErrConflict
	}
	var ownerID string
	err := p.pool.QueryRow(ctx, `SELECT owner_id FROM im_business_channels
		WHERE conversation_id=$1 AND channel_type=$2`, channelID, channelType).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return ownerID, err
}

func (p *Postgres) ListAdminBusinessChannelAccess(ctx context.Context, channelID string, channelType int, accessType, after string, limit int) ([]*BusinessChannelAccess, string, error) {
	channelID, accessType, after = strings.TrimSpace(channelID), strings.TrimSpace(accessType), strings.TrimSpace(after)
	if channelID == "" || !publicBusinessChannelType(channelType) || (accessType != "" && accessType != "allow" && accessType != "deny") {
		return nil, "", ErrConflict
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var exists bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_business_channels WHERE conversation_id=$1 AND channel_type=$2)`, channelID, channelType).Scan(&exists); err != nil {
		return nil, "", err
	}
	if !exists {
		return nil, "", ErrNotFound
	}
	rows, err := p.pool.Query(ctx, `SELECT access.conversation_id,access.user_id,user_row.name,
		COALESCE(user_row.handle,''),user_row.avatar_url,access.access_type,access.reason,
		access.created_by,access.created_at
		FROM im_business_channel_access access JOIN im_users user_row ON user_row.id=access.user_id
		WHERE access.conversation_id=$1 AND ($2='' OR access.access_type=$2)
		AND (access.access_type||':'||access.user_id)>$3
		ORDER BY access.access_type,access.user_id LIMIT $4`, channelID, accessType, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]*BusinessChannelAccess, 0, limit)
	next := ""
	for rows.Next() {
		if len(items) == limit {
			last := items[len(items)-1]
			next = last.AccessType + ":" + last.UserID
			break
		}
		item := &BusinessChannelAccess{}
		if err = rows.Scan(&item.ChannelID, &item.UserID, &item.Name, &item.Handle, &item.AvatarURL,
			&item.AccessType, &item.Reason, &item.CreatedBy, &item.CreatedAt); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	return items, next, rows.Err()
}

func (p *Postgres) ExpireBusinessChannelMemberships(ctx context.Context, at time.Time, limit int) (int, error) {
	if at.IsZero() {
		return 0, ErrConflict
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `WITH picked AS (
		SELECT member.conversation_id,member.user_id FROM im_members member
		JOIN im_business_channels channel ON channel.conversation_id=member.conversation_id
		WHERE member.expires_at IS NOT NULL AND member.expires_at<=$1
		ORDER BY member.expires_at,member.conversation_id,member.user_id
		FOR UPDATE OF member SKIP LOCKED LIMIT $2
	) DELETE FROM im_members member USING picked
	WHERE member.conversation_id=picked.conversation_id AND member.user_id=picked.user_id
	RETURNING member.conversation_id,member.user_id`, at, limit)
	if err != nil {
		return 0, err
	}
	type expiredMember struct{ channelID, userID string }
	expired := make([]expiredMember, 0, limit)
	for rows.Next() {
		var item expiredMember
		if err = rows.Scan(&item.channelID, &item.userID); err != nil {
			rows.Close()
			return 0, err
		}
		expired = append(expired, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	channels := make(map[string]uint8)
	for _, item := range expired {
		var channelType uint8
		if err = tx.QueryRow(ctx, `SELECT channel_type FROM im_business_channels WHERE conversation_id=$1`, item.channelID).Scan(&channelType); err != nil {
			return 0, err
		}
		channels[item.channelID] = channelType
		payload, _ := json.Marshal(map[string]any{"channelId": item.channelID, "channelType": channelType})
		if err = enqueueWukongBusinessEvent(ctx, tx, "business_channel", item.channelID,
			"channel.subscription.expired", payload, []wukongCommandRecipient{{UserID: item.userID}}); err != nil {
			return 0, err
		}
	}
	for channelID, channelType := range channels {
		if err = enqueueWukongChannelReconcileTyped(ctx, tx, channelID, channelType, "temporary-membership-expired", at); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(expired), nil
}

func (p *WithRedis) CreateBusinessChannel(ctx context.Context, input BusinessChannelCreate, at time.Time) (*BusinessChannel, error) {
	if channels, ok := p.base.(BusinessChannelStore); ok {
		return channels.CreateBusinessChannel(ctx, input, at)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) GetBusinessChannel(ctx context.Context, userID, channelID string, channelType int) (*BusinessChannel, error) {
	if channels, ok := p.base.(BusinessChannelStore); ok {
		return channels.GetBusinessChannel(ctx, userID, channelID, channelType)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListBusinessChannels(ctx context.Context, userID, category, parentID string, channelType int, after string, limit int) ([]*BusinessChannel, string, error) {
	if channels, ok := p.base.(BusinessChannelStore); ok {
		return channels.ListBusinessChannels(ctx, userID, category, parentID, channelType, after, limit)
	}
	return nil, "", ErrUnsupported
}

func (p *WithRedis) UpdateBusinessChannel(ctx context.Context, channelID string, channelType int, update BusinessChannelUpdate) (*BusinessChannel, error) {
	if channels, ok := p.base.(BusinessChannelStore); ok {
		return channels.UpdateBusinessChannel(ctx, channelID, channelType, update)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ApplyBusinessChannelMemberAction(ctx context.Context, action BusinessChannelMemberAction) error {
	if channels, ok := p.base.(BusinessChannelStore); ok {
		return channels.ApplyBusinessChannelMemberAction(ctx, action)
	}
	return ErrUnsupported
}

func (p *WithRedis) ListBusinessChannelMembers(ctx context.Context, actorID, channelID string, channelType int, after string, limit int) ([]*BusinessChannelMember, string, error) {
	if channels, ok := p.base.(BusinessChannelStore); ok {
		return channels.ListBusinessChannelMembers(ctx, actorID, channelID, channelType, after, limit)
	}
	return nil, "", ErrUnsupported
}

func (p *WithRedis) ApplyBusinessChannelAccess(ctx context.Context, action BusinessChannelAccessAction) error {
	if channels, ok := p.base.(BusinessChannelStore); ok {
		return channels.ApplyBusinessChannelAccess(ctx, action)
	}
	return ErrUnsupported
}

func (p *WithRedis) AuthorizeBusinessChannelSend(ctx context.Context, userID, channelID string, channelType int, at time.Time) error {
	if channels, ok := p.base.(BusinessChannelStore); ok {
		return channels.AuthorizeBusinessChannelSend(ctx, userID, channelID, channelType, at)
	}
	return ErrUnsupported
}

func (p *WithRedis) ListAdminBusinessChannels(ctx context.Context, query, category string, channelType int, after string, limit int) ([]*BusinessChannel, int64, string, error) {
	if channels, ok := p.base.(BusinessChannelAdminStore); ok {
		return channels.ListAdminBusinessChannels(ctx, query, category, channelType, after, limit)
	}
	return nil, 0, "", ErrUnsupported
}

func (p *WithRedis) AdminBusinessChannelOwner(ctx context.Context, channelID string, channelType int) (string, error) {
	if channels, ok := p.base.(BusinessChannelAdminStore); ok {
		return channels.AdminBusinessChannelOwner(ctx, channelID, channelType)
	}
	return "", ErrUnsupported
}

func (p *WithRedis) ListAdminBusinessChannelAccess(ctx context.Context, channelID string, channelType int, accessType, after string, limit int) ([]*BusinessChannelAccess, string, error) {
	if channels, ok := p.base.(BusinessChannelAdminStore); ok {
		return channels.ListAdminBusinessChannelAccess(ctx, channelID, channelType, accessType, after, limit)
	}
	return nil, "", ErrUnsupported
}

func (p *WithRedis) ExpireBusinessChannelMemberships(ctx context.Context, at time.Time, limit int) (int, error) {
	if channels, ok := p.base.(BusinessMembershipExpiryStore); ok {
		return channels.ExpireBusinessChannelMemberships(ctx, at, limit)
	}
	return 0, ErrUnsupported
}

func (p *Postgres) loadBusinessChannelSnapshot(ctx context.Context, channelID string, channelType uint8) (wukong.ChannelSnapshot, error) {
	var item wukong.ChannelSnapshot
	var memberCount int
	var ban, disband, sendBan, allowStranger bool
	err := p.pool.QueryRow(ctx, `SELECT channel.conversation_id,channel.channel_type,
		(SELECT count(*) FROM im_members active WHERE active.conversation_id=channel.conversation_id AND (active.expires_at IS NULL OR active.expires_at>now())),
		channel.ban,channel.disband,channel.send_ban,channel.allow_stranger,
		ARRAY(SELECT member.user_id FROM im_members member WHERE member.conversation_id=channel.conversation_id AND (member.expires_at IS NULL OR member.expires_at>now()) ORDER BY member.joined_at,member.user_id),
		ARRAY(SELECT access.user_id FROM im_business_channel_access access WHERE access.conversation_id=channel.conversation_id AND access.access_type='allow' ORDER BY access.user_id),
		ARRAY(SELECT access.user_id FROM im_business_channel_access access WHERE access.conversation_id=channel.conversation_id AND access.access_type='deny' ORDER BY access.user_id)
		FROM im_business_channels channel JOIN im_conversations conversation ON conversation.id=channel.conversation_id
		WHERE channel.conversation_id=$1 AND channel.channel_type=$2`, channelID, channelType).Scan(
		&item.ChannelID, &item.ChannelType, &memberCount, &ban, &disband, &sendBan, &allowStranger,
		&item.Subscribers, &item.Allowlist, &item.Denylist)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	item.Cursor = fmt.Sprintf("%02d:%s", item.ChannelType, item.ChannelID)
	if memberCount >= 1000 {
		item.Large = 1
	}
	if ban {
		item.Ban = 1
	}
	if disband {
		item.Disband = 1
	}
	if sendBan {
		item.SendBan = 1
	}
	if allowStranger {
		item.AllowStranger = 1
	}
	return item, nil
}

func (p *Postgres) listBusinessChannelsForReconcile(ctx context.Context, after string, limit int) ([]wukong.ChannelSnapshot, error) {
	rows, err := p.pool.Query(ctx, `SELECT channel.conversation_id,channel.channel_type,
		(SELECT count(*) FROM im_members active WHERE active.conversation_id=channel.conversation_id AND (active.expires_at IS NULL OR active.expires_at>now())),
		channel.ban,channel.disband,channel.send_ban,channel.allow_stranger,
		ARRAY(SELECT member.user_id FROM im_members member WHERE member.conversation_id=channel.conversation_id AND (member.expires_at IS NULL OR member.expires_at>now()) ORDER BY member.joined_at,member.user_id),
		ARRAY(SELECT access.user_id FROM im_business_channel_access access WHERE access.conversation_id=channel.conversation_id AND access.access_type='allow' ORDER BY access.user_id),
		ARRAY(SELECT access.user_id FROM im_business_channel_access access WHERE access.conversation_id=channel.conversation_id AND access.access_type='deny' ORDER BY access.user_id)
		FROM im_business_channels channel JOIN im_conversations conversation ON conversation.id=channel.conversation_id
		WHERE (lpad(channel.channel_type::text,2,'0')||':'||channel.conversation_id)>$1
		ORDER BY lpad(channel.channel_type::text,2,'0')||':'||channel.conversation_id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]wukong.ChannelSnapshot, 0, limit)
	for rows.Next() {
		var item wukong.ChannelSnapshot
		var memberCount int
		var ban, disband, sendBan, allowStranger bool
		if err = rows.Scan(&item.ChannelID, &item.ChannelType, &memberCount, &ban, &disband, &sendBan, &allowStranger,
			&item.Subscribers, &item.Allowlist, &item.Denylist); err != nil {
			return nil, err
		}
		item.Cursor = fmt.Sprintf("%02d:%s", item.ChannelType, item.ChannelID)
		if memberCount >= 1000 {
			item.Large = 1
		}
		if ban {
			item.Ban = 1
		}
		if disband {
			item.Disband = 1
		}
		if sendBan {
			item.SendBan = 1
		}
		if allowStranger {
			item.AllowStranger = 1
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
