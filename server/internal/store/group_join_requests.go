package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/model"
)

const groupJoinRequestColumns = `id,conversation_id,requester_id,invitee_id,policy_version,status,created_at,expires_at,updated_at,COALESCE(reviewed_by,''),resolved_at,resolution_reason`
const groupJoinRequestUserColumns = `id,''::text,name,COALESCE(handle,''),handle_change_count,signature,COALESCE(avatar_media_id,''),avatar_url,allow_search_by_handle,allow_search_by_phone,gender,banned,created_at`

func scanGroupJoinRequest(row pgx.Row) (*model.GroupJoinRequest, error) {
	request := &model.GroupJoinRequest{}
	err := row.Scan(
		&request.ID, &request.ConversationID, &request.RequesterID, &request.InviteeID,
		&request.PolicyVersion, &request.Status, &request.CreatedAt, &request.ExpiresAt,
		&request.UpdatedAt, &request.ReviewedBy, &request.ResolvedAt, &request.ResolutionReason,
	)
	return request, err
}

func groupJoinContext(ctx context.Context, tx pgx.Tx, conversationID, actorID string) (role, policy string, version int64, dissolved *time.Time, err error) {
	err = tx.QueryRow(ctx, `SELECT COALESCE(member.role,''),group_record.join_policy,group_record.join_policy_version,group_record.dissolved_at
		FROM im_groups group_record
		LEFT JOIN im_members member ON member.conversation_id=group_record.conversation_id AND member.user_id=$2
		WHERE group_record.conversation_id=$1 FOR UPDATE OF group_record`, conversationID, actorID).
		Scan(&role, &policy, &version, &dissolved)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func activeFriend(ctx context.Context, tx pgx.Tx, userID, friendID string) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM im_friendships friendship
		JOIN im_users friend ON friend.id=friendship.friend_user_id
		WHERE friendship.user_id=$1 AND friendship.friend_user_id=$2
		  AND friend.deleted_at IS NULL AND NOT friend.banned
	)`, userID, friendID).Scan(&allowed)
	return allowed, err
}

func validateGroupInvitee(ctx context.Context, tx pgx.Tx, conversationID, requesterID, inviteeID string) (alreadyMember bool, err error) {
	friend, err := activeFriend(ctx, tx, requesterID, inviteeID)
	if err != nil {
		return false, err
	}
	if !friend {
		return false, ErrFriendRequired
	}
	var blocked bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_group_blacklist WHERE conversation_id=$1 AND user_id=$2),
		EXISTS(SELECT 1 FROM im_members WHERE conversation_id=$1 AND user_id=$2)`, conversationID, inviteeID).
		Scan(&blocked, &alreadyMember); err != nil {
		return false, err
	}
	if blocked {
		return false, ErrForbidden
	}
	return alreadyMember, nil
}

func (p *Postgres) addGroupMembersByPolicyTx(ctx context.Context, tx pgx.Tx, actor, conversationID string, userIDs []string, maxMembers int, at time.Time) ([]string, error) {
	role, policy, _, dissolved, err := groupJoinContext(ctx, tx, conversationID, actor)
	if err != nil {
		return nil, err
	}
	if dissolved != nil {
		return nil, ErrConflict
	}
	manager := role == "owner" || role == "admin"
	if !manager && !(role == "member" && policy == "invite") {
		return nil, ErrJoinPolicy
	}

	unique := make([]string, 0, len(userIDs))
	seen := make(map[string]struct{}, len(userIDs))
	for _, raw := range userIDs {
		userID := strings.TrimSpace(raw)
		if userID == "" || userID == actor {
			return nil, ErrFriendRequired
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		unique = append(unique, userID)
	}
	if len(unique) == 0 || len(unique) > 500 {
		return nil, ErrConflict
	}
	for _, userID := range unique {
		if _, err = validateGroupInvitee(ctx, tx, conversationID, actor, userID); err != nil {
			return nil, err
		}
	}

	rows, err := tx.Query(ctx, `SELECT candidate.id FROM unnest($2::text[]) candidate(id)
		WHERE NOT EXISTS(SELECT 1 FROM im_members member WHERE member.conversation_id=$1 AND member.user_id=candidate.id)`, conversationID, unique)
	if err != nil {
		return nil, err
	}
	missing := make([]string, 0, len(unique))
	for rows.Next() {
		var userID string
		if err = rows.Scan(&userID); err != nil {
			rows.Close()
			return nil, err
		}
		missing = append(missing, userID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(missing) == 0 {
		return nil, nil
	}
	if maxMembers <= 0 {
		maxMembers = 500
	}
	var memberCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_members WHERE conversation_id=$1`, conversationID).Scan(&memberCount); err != nil {
		return nil, err
	}
	if memberCount+len(missing) > maxMembers {
		return nil, ErrConflict
	}
	if err = p.addGroupMembersWithHistory(ctx, tx, conversationID, missing, at); err != nil {
		return nil, err
	}
	if err = emitGroupSystem(ctx, tx, conversationID, actor, "group.members.added", map[string]any{"userIds": missing}, at); err != nil {
		return nil, err
	}
	if err = enqueueWukongChannelReconcile(ctx, tx, conversationID, "members-added", at); err != nil {
		return nil, err
	}
	return missing, nil
}

func (p *Postgres) AddGroupMembersByPolicy(ctx context.Context, actor, conversationID string, userIDs []string, maxMembers int, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = p.addGroupMembersByPolicyTx(ctx, tx, actor, conversationID, userIDs, maxMembers, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) InviteGroupMemberByPolicy(ctx context.Context, request *model.GroupJoinRequest, maxMembers int, at time.Time) (*model.GroupInviteOutcome, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	role, policy, version, dissolved, err := groupJoinContext(ctx, tx, request.ConversationID, request.RequesterID)
	if err != nil {
		return nil, err
	}
	if dissolved != nil {
		return nil, ErrConflict
	}
	if role == "" {
		return nil, ErrForbidden
	}
	alreadyMember, err := validateGroupInvitee(ctx, tx, request.ConversationID, request.RequesterID, request.InviteeID)
	if err != nil {
		return nil, err
	}
	if alreadyMember {
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &model.GroupInviteOutcome{Action: "added", Duplicate: true, AlreadyInGroup: true}, nil
	}
	manager := role == "owner" || role == "admin"
	if manager || policy == "invite" {
		added, addErr := p.addGroupMembersByPolicyTx(ctx, tx, request.RequesterID, request.ConversationID, []string{request.InviteeID}, maxMembers, at)
		if addErr != nil {
			return nil, addErr
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &model.GroupInviteOutcome{Action: "added", Duplicate: len(added) == 0, AlreadyInGroup: len(added) == 0}, nil
	}
	if policy != "member_approval" {
		return nil, ErrJoinPolicy
	}

	existing, existingErr := scanGroupJoinRequest(tx.QueryRow(ctx, `SELECT `+groupJoinRequestColumns+` FROM im_group_join_requests WHERE conversation_id=$1 AND invitee_id=$2 AND status='pending'`, request.ConversationID, request.InviteeID))
	if existingErr == nil {
		if !at.Before(existing.ExpiresAt) {
			if _, err = tx.Exec(ctx, `UPDATE im_group_join_requests SET status='expired',resolved_at=$2,updated_at=$2,resolution_reason='expired' WHERE id=$1`, existing.ID, at); err != nil {
				return nil, err
			}
		} else {
			if err = tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &model.GroupInviteOutcome{Action: "pending_approval", Request: existing, Duplicate: true}, nil
		}
	} else if !errors.Is(existingErr, pgx.ErrNoRows) {
		return nil, existingErr
	}

	request.PolicyVersion = version
	request.Status = "pending"
	request.CreatedAt = at
	request.UpdatedAt = at
	request.ExpiresAt = at.Add(7 * 24 * time.Hour)
	created, err := scanGroupJoinRequest(tx.QueryRow(ctx, `INSERT INTO im_group_join_requests(
		id,conversation_id,requester_id,invitee_id,policy_version,status,created_at,expires_at,updated_at)
		VALUES($1,$2,$3,$4,$5,'pending',$6,$7,$6) RETURNING `+groupJoinRequestColumns,
		request.ID, request.ConversationID, request.RequesterID, request.InviteeID, version, at, request.ExpiresAt))
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{
		"requestId": created.ID, "conversationId": created.ConversationID,
		"requesterId": created.RequesterID, "inviteeId": created.InviteeID,
		"status": created.Status, "expiresAt": created.ExpiresAt,
	})
	managerIDs, err := groupManagerIDs(ctx, tx, created.ConversationID)
	if err != nil {
		return nil, err
	}
	for _, userID := range managerIDs {
		if err = appendUserBusinessEvent(ctx, tx, userID, "group.join.request.created", payload, at); err != nil {
			return nil, err
		}
		if err = insertPrivatePush(ctx, tx, userID, "group.join.request.created", payload); err != nil {
			return nil, err
		}
	}
	if err = auditGroupJoinRequest(ctx, tx, request.RequesterID, "group.join.request.created", created, at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &model.GroupInviteOutcome{Action: "pending_approval", Request: created}, nil
}

func groupManagerIDs(ctx context.Context, tx pgx.Tx, conversationID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT user_id FROM im_members WHERE conversation_id=$1 AND role IN ('owner','admin') ORDER BY user_id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func auditGroupJoinRequest(ctx context.Context, tx pgx.Tx, actor, action string, request *model.GroupJoinRequest, at time.Time) error {
	auditID, err := secureOpaqueToken("aud_gjr_")
	if err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{
		"conversationId": request.ConversationID,
		"requesterId":    request.RequesterID,
		"inviteeId":      request.InviteeID,
		"status":         request.Status,
	})
	_, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at)
		VALUES($1,$2,$3,'group_join_request',$4,$5,$6)`, auditID, actor, action, request.ID, metadata, at)
	return err
}

func (p *Postgres) ListGroupJoinRequests(ctx context.Context, actor, conversationID, status string, limit int, at time.Time) ([]*model.GroupJoinRequest, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if status == "" {
		status = "pending"
	}
	allowed := map[string]bool{"all": true, "pending": true, "approved": true, "rejected": true, "cancelled": true, "expired": true, "invalidated": true}
	if !allowed[status] {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	role, _, _, dissolved, err := groupJoinContext(ctx, tx, conversationID, actor)
	if err != nil {
		return nil, err
	}
	if dissolved != nil || (role != "owner" && role != "admin") {
		return nil, ErrForbidden
	}
	if _, err = tx.Exec(ctx, `UPDATE im_group_join_requests SET status='expired',resolved_at=$2,updated_at=$2,resolution_reason='expired'
		WHERE conversation_id=$1 AND status='pending' AND expires_at<=$2`, conversationID, at); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+groupJoinRequestColumns+` FROM im_group_join_requests
		WHERE conversation_id=$1 AND ($2='all' OR status=$2)
		ORDER BY created_at DESC,id DESC LIMIT $3`, conversationID, status, limit)
	if err != nil {
		return nil, err
	}
	var requests []*model.GroupJoinRequest
	for rows.Next() {
		request, scanErr := scanGroupJoinRequest(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		requests = append(requests, request)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var groupName string
	if err = tx.QueryRow(ctx, `SELECT title FROM im_conversations WHERE id=$1`, conversationID).Scan(&groupName); err != nil {
		return nil, err
	}
	for _, request := range requests {
		request.GroupName = groupName
		request.Requester, err = scanUser(tx.QueryRow(ctx, `SELECT `+groupJoinRequestUserColumns+` FROM im_users WHERE id=$1`, request.RequesterID))
		if err != nil {
			return nil, err
		}
		request.Invitee, err = scanUser(tx.QueryRow(ctx, `SELECT `+groupJoinRequestUserColumns+` FROM im_users WHERE id=$1`, request.InviteeID))
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return requests, nil
}

func (p *Postgres) TransitionGroupJoinRequest(ctx context.Context, actor, conversationID, requestID, action string, maxMembers int, at time.Time) (*model.GroupJoinRequest, bool, error) {
	if action != "approve" && action != "reject" && action != "cancel" {
		return nil, false, ErrUnsupported
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	// Keep the same group -> request lock order used by policy changes and
	// request creation so an approval racing a policy switch cannot deadlock.
	role, policy, version, dissolved, err := groupJoinContext(ctx, tx, conversationID, actor)
	if err != nil {
		return nil, false, err
	}
	request, err := scanGroupJoinRequest(tx.QueryRow(ctx, `SELECT `+groupJoinRequestColumns+` FROM im_group_join_requests WHERE id=$1 AND conversation_id=$2 FOR UPDATE`, requestID, conversationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	manager := role == "owner" || role == "admin"
	if (action == "cancel" && actor != request.RequesterID) || (action != "cancel" && !manager) {
		return nil, false, ErrForbidden
	}
	target := map[string]string{"approve": "approved", "reject": "rejected", "cancel": "cancelled"}[action]
	if request.Status == target {
		return request, true, tx.Commit(ctx)
	}
	if request.Status != "pending" {
		return nil, false, ErrConflict
	}
	if !at.Before(request.ExpiresAt) {
		request, err = p.resolveGroupJoinRequest(ctx, tx, request, actor, "expired", "expired", at)
		if err != nil {
			return nil, false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return request, false, ErrJoinRequestExpired
	}
	if dissolved != nil || policy != "member_approval" || version != request.PolicyVersion {
		request, err = p.resolveGroupJoinRequest(ctx, tx, request, actor, "invalidated", "policy_changed", at)
		if err != nil {
			return nil, false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return request, false, ErrJoinPolicy
	}
	if action == "approve" {
		var requesterMember bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_members WHERE conversation_id=$1 AND user_id=$2)`, request.ConversationID, request.RequesterID).Scan(&requesterMember); err != nil {
			return nil, false, err
		}
		if !requesterMember {
			return nil, false, ErrForbidden
		}
		alreadyMember, validateErr := validateGroupInvitee(ctx, tx, request.ConversationID, request.RequesterID, request.InviteeID)
		if validateErr != nil {
			return nil, false, validateErr
		}
		if !alreadyMember {
			var memberCount int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_members WHERE conversation_id=$1`, request.ConversationID).Scan(&memberCount); err != nil {
				return nil, false, err
			}
			if maxMembers <= 0 {
				maxMembers = 500
			}
			if memberCount+1 > maxMembers {
				return nil, false, ErrConflict
			}
			if err = p.addGroupMembersWithHistory(ctx, tx, request.ConversationID, []string{request.InviteeID}, at); err != nil {
				return nil, false, err
			}
			if err = emitGroupSystem(ctx, tx, request.ConversationID, actor, "group.members.added", map[string]any{"userIds": []string{request.InviteeID}, "source": "member_approval"}, at); err != nil {
				return nil, false, err
			}
			if err = enqueueWukongChannelReconcile(ctx, tx, request.ConversationID, "join-request-approved", at); err != nil {
				return nil, false, err
			}
		}
		reason := "approved"
		if alreadyMember {
			reason = "already_member"
		}
		request, err = p.resolveGroupJoinRequest(ctx, tx, request, actor, "approved", reason, at)
	} else {
		request, err = p.resolveGroupJoinRequest(ctx, tx, request, actor, target, target, at)
	}
	if err != nil {
		return nil, false, err
	}
	if err = auditGroupJoinRequest(ctx, tx, actor, "group.join.request."+request.Status, request, at); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return request, false, nil
}

func (p *Postgres) resolveGroupJoinRequest(ctx context.Context, tx pgx.Tx, request *model.GroupJoinRequest, actor, status, reason string, at time.Time) (*model.GroupJoinRequest, error) {
	resolved, err := scanGroupJoinRequest(tx.QueryRow(ctx, `UPDATE im_group_join_requests
		SET status=$2,reviewed_by=NULLIF($3,''),resolved_at=$4,updated_at=$4,resolution_reason=$5
		WHERE id=$1 RETURNING `+groupJoinRequestColumns, request.ID, status, actor, at, reason))
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{
		"requestId": resolved.ID, "conversationId": resolved.ConversationID,
		"requesterId": resolved.RequesterID, "inviteeId": resolved.InviteeID,
		"status": resolved.Status, "resolutionReason": resolved.ResolutionReason,
	})
	recipients, err := groupManagerIDs(ctx, tx, resolved.ConversationID)
	if err != nil {
		return nil, err
	}
	recipients = append(recipients, resolved.RequesterID)
	seen := map[string]bool{}
	for _, userID := range recipients {
		if userID == "" || seen[userID] {
			continue
		}
		seen[userID] = true
		if err = appendUserBusinessEvent(ctx, tx, userID, "group.join.request.updated", payload, at); err != nil {
			return nil, err
		}
	}
	if status == "approved" || status == "rejected" || status == "invalidated" || status == "expired" {
		if err = insertPrivatePush(ctx, tx, resolved.RequesterID, "group.join.request.updated", payload); err != nil {
			return nil, err
		}
	}
	return resolved, nil
}

func (p *Postgres) setGroupJoinPolicy(ctx context.Context, tx pgx.Tx, actor, conversationID, policy string, at time.Time) error {
	var previous string
	var version int64
	if err := tx.QueryRow(ctx, `SELECT join_policy,join_policy_version FROM im_groups WHERE conversation_id=$1 AND dissolved_at IS NULL FOR UPDATE`, conversationID).
		Scan(&previous, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if previous == policy {
		return nil
	}
	version++
	if _, err := tx.Exec(ctx, `UPDATE im_groups SET join_policy=$2,join_policy_version=$3,updated_at=$4 WHERE conversation_id=$1`, conversationID, policy, version, at); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT `+groupJoinRequestColumns+` FROM im_group_join_requests WHERE conversation_id=$1 AND status='pending' FOR UPDATE`, conversationID)
	if err != nil {
		return err
	}
	var pending []*model.GroupJoinRequest
	for rows.Next() {
		request, scanErr := scanGroupJoinRequest(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		pending = append(pending, request)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, request := range pending {
		if _, err = p.resolveGroupJoinRequest(ctx, tx, request, actor, "invalidated", "policy_changed", at); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE im_group_invites SET status='cancelled',resolved_at=COALESCE(resolved_at,$2),updated_at=$2 WHERE conversation_id=$1 AND status='pending'`, conversationID, at); err != nil {
		return err
	}
	return nil
}
