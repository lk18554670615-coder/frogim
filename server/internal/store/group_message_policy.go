package store

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// This is presentation policy, not a restriction on access to raw IM events.
func IsPrivateGroupNoticeEvent(event string) bool {
	switch event {
	case "group.invite.accepted", "group.invite.rejected", "group.invite.cancelled",
		"group.member.joined", "group.members.added", "group.member_added",
		"group.member.leave", "group.member.remove", "group.blacklist.added", "screenshot.taken":
		return true
	}
	return false
}

type PushPresentationPolicyStore interface {
	CanPresentPush(context.Context, OutboxItem) (bool, error)
}

func (p *WithRedis) CanPresentPush(ctx context.Context, item OutboxItem) (bool, error) {
	if policy, ok := p.base.(PushPresentationPolicyStore); ok {
		return policy.CanPresentPush(ctx, item)
	}
	return true, nil
}

func (p *Postgres) CanPresentPush(ctx context.Context, item OutboxItem) (bool, error) {
	message, ok := item.Payload["message"].(map[string]any)
	if !ok {
		return true, nil // Personal invitations and other actionable notifications stay intact.
	}
	cid, _ := message["conversationId"].(string)
	mid, _ := message["id"].(string)
	var role string
	err := p.pool.QueryRow(ctx, `SELECT COALESCE(m.role,'') FROM im_groups g
		LEFT JOIN im_members m ON m.conversation_id=g.conversation_id AND m.user_id=$2
		AND (m.expires_at IS NULL OR m.expires_at>now()) WHERE g.conversation_id=$1`, cid, item.UserID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if role == "" {
		return false, nil
	}
	managementOnly, classified := message["managementOnly"].(bool)
	// Screenshot pushes queued before this policy may explicitly contain false.
	screenshot := message["type"] == "screenshot" || message["type"] == "screenshot_notice"
	// Old queued system pushes have no event classification. Do not surface an
	// ambiguous management notification to ordinary members after upgrading.
	if (managementOnly || screenshot || (!classified && message["type"] == "system")) && role != "owner" && role != "admin" {
		return false, nil
	}
	var recalled bool
	messageID, parseErr := strconv.ParseInt(mid, 10, 64)
	if parseErr != nil {
		return true, nil
	}
	err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_wukong_message_extensions
		WHERE message_id=$1 AND payload->>'recalledAt' IS NOT NULL)`, messageID).Scan(&recalled)
	return !recalled, err
}
