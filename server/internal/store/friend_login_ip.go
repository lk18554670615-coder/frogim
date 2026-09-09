package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type FriendLoginIPPermissionUpdate struct {
	BatchID   string   `json:"batchId"`
	Allowed   bool     `json:"allowed"`
	Requested int      `json:"requested"`
	Changed   int      `json:"changed"`
	Unchanged int      `json:"unchanged"`
	UserIDs   []string `json:"userIds"`
}

// FriendLoginIPStore deliberately combines authorization, relationship
// validation, the sensitive read and its audit in one persistence boundary.
type FriendLoginIPStore interface {
	FriendLoginIPPermission(context.Context, string) (bool, error)
	SetFriendLoginIPPermission(context.Context, string, []string, bool, string, string) (FriendLoginIPPermissionUpdate, error)
	ReadFriendLoginIP(context.Context, string, string, string) (string, string, error)
}

func (p *Postgres) FriendLoginIPPermission(ctx context.Context, uid string) (bool, error) {
	var allowed bool
	err := p.pool.QueryRow(ctx, `SELECT can_view_friend_login_ip FROM im_users WHERE id=$1 AND deleted_at IS NULL`, uid).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return allowed, err
}

func (p *Postgres) SetFriendLoginIPPermission(ctx context.Context, actor string, userIDs []string, allowed bool, reason, requestIP string) (FriendLoginIPPermissionUpdate, error) {
	result := FriendLoginIPPermissionUpdate{Allowed: allowed, Requested: len(userIDs), UserIDs: append([]string(nil), userIDs...)}
	batchID, err := secureOpaqueToken("ip_permission_")
	if err != nil {
		return result, err
	}
	result.BatchID = batchID
	ordered := append([]string(nil), userIDs...)
	sort.Strings(ordered)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,can_view_friend_login_ip FROM im_users WHERE id=ANY($1::text[]) AND deleted_at IS NULL ORDER BY id FOR UPDATE`, ordered)
	if err != nil {
		return result, err
	}
	previous := make(map[string]bool, len(ordered))
	for rows.Next() {
		var id string
		var value bool
		if err = rows.Scan(&id, &value); err != nil {
			rows.Close()
			return result, err
		}
		previous[id] = value
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return result, err
	}
	if len(previous) != len(ordered) {
		return result, ErrNotFound
	}
	now := time.Now().UTC()
	for _, id := range ordered {
		before := previous[id]
		if before == allowed {
			result.Unchanged++
		} else {
			if _, err = tx.Exec(ctx, `UPDATE im_users SET can_view_friend_login_ip=$2,updated_at=$3 WHERE id=$1`, id, allowed, now); err != nil {
				return result, err
			}
			result.Changed++
		}
		if err = deletionAudit(ctx, tx, actor, "user.friend_login_ip_permission.updated", "user", id, requestIP, map[string]any{
			"before": before, "after": allowed, "changed": before != allowed, "reason": strings.TrimSpace(reason), "batchId": batchID,
		}); err != nil {
			return result, err
		}
		if before == allowed {
			continue
		}
		changeID, tokenErr := secureOpaqueToken("permission_")
		if tokenErr != nil {
			return result, tokenErr
		}
		payload, _ := json.Marshal(map[string]any{"userId": id, "changeId": changeID})
		if err = appendUserBusinessEvent(ctx, tx, id, "user.friend_login_ip_permission.updated", payload, now); err != nil {
			return result, err
		}
	}
	if err = deletionAudit(ctx, tx, actor, "user.friend_login_ip_permission.batch_updated", "user_permission_batch", batchID, requestIP, map[string]any{
		"allowed": allowed, "requested": result.Requested, "changed": result.Changed, "unchanged": result.Unchanged,
		"userIds": ordered, "reason": strings.TrimSpace(reason),
	}); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	result.UserIDs = ordered
	return result, nil
}

func friendLoginIPViewAuditID(viewerID, peerID string, at time.Time) string {
	bucket := at.UTC().Truncate(time.Hour).Format(time.RFC3339)
	digest := sha256.Sum256([]byte(viewerID + "\x00" + peerID + "\x00" + bucket))
	return "aud_peer_ip_" + hex.EncodeToString(digest[:16])
}

func (p *Postgres) ReadFriendLoginIP(ctx context.Context, viewerID, conversationID, requestIP string) (string, string, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT can_view_friend_login_ip FROM im_users
		WHERE id=$1 AND NOT banned AND deleted_at IS NULL FOR SHARE`, viewerID).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !allowed) {
		return "", "", ErrForbidden
	}
	if err != nil {
		return "", "", err
	}
	var peerID, loginIP string
	err = tx.QueryRow(ctx, `SELECT peer_member.user_id,COALESCE(host(access.last_login_ip),'')
		FROM im_conversations conversation
		JOIN im_direct_index direct ON direct.conversation_id=conversation.id
		JOIN im_members viewer_member ON viewer_member.conversation_id=conversation.id AND viewer_member.user_id=$1
		JOIN im_members peer_member ON peer_member.conversation_id=conversation.id AND peer_member.user_id<>$1
		JOIN im_users peer_user ON peer_user.id=peer_member.user_id AND peer_user.deleted_at IS NULL
		JOIN im_friendships viewer_friend ON viewer_friend.user_id=$1 AND viewer_friend.friend_user_id=peer_member.user_id
		JOIN im_friendships peer_friend ON peer_friend.user_id=peer_member.user_id AND peer_friend.friend_user_id=$1
		LEFT JOIN im_user_access_profiles access ON access.user_id=peer_member.user_id
		WHERE conversation.id=$2 AND conversation.kind='direct' AND conversation.member_count=2
		  AND (viewer_member.expires_at IS NULL OR viewer_member.expires_at>now())
		  AND (peer_member.expires_at IS NULL OR peer_member.expires_at>now())
		  AND NOT EXISTS(SELECT 1 FROM im_business_channels business WHERE business.conversation_id=conversation.id)
		LIMIT 1 FOR SHARE OF viewer_member,peer_member,peer_user,viewer_friend,peer_friend`, viewerID, conversationID).Scan(&peerID, &loginIP)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	now := time.Now().UTC()
	metadata, err := json.Marshal(map[string]any{"conversationId": conversationID})
	if err != nil {
		return "", "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,result,ip,created_at)
		VALUES($1,$2,'user.friend_login_ip.viewed','user',$3,$4,'success',$5,$6)
		ON CONFLICT(id) DO NOTHING`, friendLoginIPViewAuditID(viewerID, peerID, now), viewerID, peerID, metadata, requestIP, now); err != nil {
		return "", "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return peerID, loginIP, nil
}

func (p *WithRedis) FriendLoginIPPermission(ctx context.Context, uid string) (bool, error) {
	if s, ok := p.base.(FriendLoginIPStore); ok {
		return s.FriendLoginIPPermission(ctx, uid)
	}
	return false, ErrForbidden
}

func (p *WithRedis) SetFriendLoginIPPermission(ctx context.Context, actor string, userIDs []string, allowed bool, reason, requestIP string) (FriendLoginIPPermissionUpdate, error) {
	if s, ok := p.base.(FriendLoginIPStore); ok {
		return s.SetFriendLoginIPPermission(ctx, actor, userIDs, allowed, reason, requestIP)
	}
	return FriendLoginIPPermissionUpdate{}, ErrForbidden
}

func (p *WithRedis) ReadFriendLoginIP(ctx context.Context, viewerID, conversationID, requestIP string) (string, string, error) {
	if s, ok := p.base.(FriendLoginIPStore); ok {
		return s.ReadFriendLoginIP(ctx, viewerID, conversationID, requestIP)
	}
	return "", "", ErrForbidden
}
