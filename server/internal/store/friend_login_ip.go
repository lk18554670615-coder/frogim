package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// FriendLoginIPStore deliberately combines authorization, relationship
// validation, the sensitive read and its audit in one persistence boundary.
type FriendLoginIPStore interface {
	ReadFriendLoginIP(context.Context, string, string, string) (string, string, error)
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
	err = tx.QueryRow(ctx, `SELECT is_internal_user FROM im_users
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

func (p *WithRedis) ReadFriendLoginIP(ctx context.Context, viewerID, conversationID, requestIP string) (string, string, error) {
	if s, ok := p.base.(FriendLoginIPStore); ok {
		return s.ReadFriendLoginIP(ctx, viewerID, conversationID, requestIP)
	}
	return "", "", ErrForbidden
}
