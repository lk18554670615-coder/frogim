package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/wukong"
)

const supportSessionSelect = `SELECT session.id,session.visitor_id,visitor.name,session.skill_group_id,skill.name,
	session.channel_id,session.channel_type,session.subject,session.status,COALESCE(session.assigned_agent_id,''),
	COALESCE(agent.name,''),session.metadata,session.queue_entered_at,session.assigned_at,session.ended_at,
	session.transfer_count,COALESCE(session.rating,0),session.rating_comment,COALESCE(session.ended_by,''),
	session.rated_at,session.created_at,session.updated_at,
	CASE WHEN session.status='queued' THEN 1+(SELECT count(*) FROM im_support_sessions ahead
		WHERE ahead.skill_group_id=session.skill_group_id AND ahead.status='queued'
		AND (ahead.queue_entered_at,ahead.id)<(session.queue_entered_at,session.id)) ELSE 0 END
	FROM im_support_sessions session
	JOIN im_users visitor ON visitor.id=session.visitor_id
	JOIN im_support_skill_groups skill ON skill.id=session.skill_group_id
	LEFT JOIN im_users agent ON agent.id=session.assigned_agent_id`

func scanSupportSession(row pgx.Row) (*SupportSession, error) {
	item := &SupportSession{}
	var metadata []byte
	err := row.Scan(&item.ID, &item.VisitorID, &item.VisitorName, &item.SkillGroupID, &item.SkillGroupName,
		&item.ChannelID, &item.ChannelType, &item.Subject, &item.Status, &item.AssignedAgentID,
		&item.AgentName, &metadata, &item.QueueEnteredAt, &item.AssignedAt, &item.EndedAt,
		&item.TransferCount, &item.Rating, &item.RatingComment, &item.EndedBy,
		&item.RatedAt, &item.CreatedAt, &item.UpdatedAt, &item.QueuePosition)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(metadata, &item.Metadata); err != nil {
		return nil, err
	}
	return item, nil
}

func validSupportStatus(status string) bool {
	return status == "offline" || status == "available" || status == "busy" || status == "away"
}

func lockSupportSkillIDsTx(ctx context.Context, tx pgx.Tx, rawIDs []string) error {
	ids := append([]string(nil), rawIDs...)
	sort.Strings(ids)
	previous := ""
	for _, id := range ids {
		if id == "" || id == previous {
			continue
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id); err != nil {
			return err
		}
		previous = id
	}
	return nil
}

func lockSupportSkillsForAgentTx(ctx context.Context, tx pgx.Tx, agentID, onlySkillGroupID string) error {
	if onlySkillGroupID != "" {
		return lockSupportSkillIDsTx(ctx, tx, []string{onlySkillGroupID})
	}
	rows, err := tx.Query(ctx, `SELECT skill_group_id FROM im_support_agent_skills
		WHERE user_id=$1 AND enabled ORDER BY skill_group_id`, agentID)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return lockSupportSkillIDsTx(ctx, tx, ids)
}

func (p *Postgres) SaveSupportSkillGroup(ctx context.Context, input SupportSkillGroupInput, at time.Time) (*SupportSkillGroup, error) {
	input.ID, input.Name = strings.TrimSpace(input.ID), strings.TrimSpace(input.Name)
	input.Description, input.RoutingStrategy = strings.TrimSpace(input.Description), strings.TrimSpace(input.RoutingStrategy)
	input.ActorID = strings.TrimSpace(input.ActorID)
	if input.ID == "" || input.Name == "" || len(input.Name) > 100 || len(input.Description) > 2000 || input.ActorID == "" ||
		(input.RoutingStrategy != "least_active" && input.RoutingStrategy != "round_robin") ||
		input.MaxConcurrentPerAgent < 1 || input.MaxConcurrentPerAgent > 100 || at.IsZero() {
		return nil, ErrConflict
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO im_support_skill_groups(
		id,name,description,routing_strategy,max_concurrent_per_agent,enabled,created_by,created_at,updated_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)
	ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,
		routing_strategy=excluded.routing_strategy,max_concurrent_per_agent=excluded.max_concurrent_per_agent,
		enabled=excluded.enabled,updated_at=excluded.updated_at`, input.ID, input.Name, input.Description,
		input.RoutingStrategy, input.MaxConcurrentPerAgent, input.Enabled, input.ActorID, at)
	if err != nil {
		return nil, err
	}
	items, err := p.ListSupportSkillGroups(ctx, true)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID == input.ID {
			return item, nil
		}
	}
	return nil, ErrNotFound
}

func (p *Postgres) ListSupportSkillGroups(ctx context.Context, includeDisabled bool) ([]*SupportSkillGroup, error) {
	rows, err := p.pool.Query(ctx, `SELECT skill.id,skill.name,skill.description,skill.routing_strategy,
		skill.max_concurrent_per_agent,skill.enabled,skill.created_at,skill.updated_at,
		(SELECT count(*) FROM im_support_sessions session WHERE session.skill_group_id=skill.id AND session.status='queued'),
		(SELECT count(*) FROM im_support_agent_skills agent_skill JOIN im_support_agents agent ON agent.user_id=agent_skill.user_id
		 WHERE agent_skill.skill_group_id=skill.id AND agent_skill.enabled AND agent.status='available')
		FROM im_support_skill_groups skill WHERE $1 OR skill.enabled ORDER BY lower(skill.name),skill.id`, includeDisabled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*SupportSkillGroup, 0)
	for rows.Next() {
		item := &SupportSkillGroup{}
		if err = rows.Scan(&item.ID, &item.Name, &item.Description, &item.RoutingStrategy,
			&item.MaxConcurrentPerAgent, &item.Enabled, &item.CreatedAt, &item.UpdatedAt,
			&item.QueueCount, &item.AvailableAgents); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) SaveSupportAgent(ctx context.Context, input SupportAgentInput, at time.Time) (*SupportAgent, error) {
	input.UserID, input.Status = strings.TrimSpace(input.UserID), strings.TrimSpace(input.Status)
	if input.UserID == "" || !validSupportStatus(input.Status) || input.MaxConcurrent < 1 || input.MaxConcurrent > 100 || at.IsZero() {
		return nil, ErrConflict
	}
	cleanSkills := make([]string, 0, len(input.SkillGroupIDs))
	seen := map[string]struct{}{}
	for _, raw := range input.SkillGroupIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, ErrConflict
		}
		if _, duplicate := seen[id]; !duplicate {
			seen[id] = struct{}{}
			cleanSkills = append(cleanSkills, id)
		}
	}
	if len(cleanSkills) == 0 {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var validUser bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1 AND deleted_at IS NULL AND NOT banned)`, input.UserID).Scan(&validUser); err != nil {
		return nil, err
	}
	if !validUser {
		return nil, ErrNotFound
	}
	var validSkills int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_support_skill_groups WHERE id=ANY($1::text[])`, cleanSkills).Scan(&validSkills); err != nil {
		return nil, err
	}
	if validSkills != len(cleanSkills) {
		return nil, ErrNotFound
	}
	rows, err := tx.Query(ctx, `SELECT skill_group_id FROM im_support_agent_skills WHERE user_id=$1 ORDER BY skill_group_id`, input.UserID)
	if err != nil {
		return nil, err
	}
	lockedSkills := append([]string(nil), cleanSkills...)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		lockedSkills = append(lockedSkills, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err = lockSupportSkillIDsTx(ctx, tx, lockedSkills); err != nil {
		return nil, err
	}
	var removesActiveSkill bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_support_sessions session
		WHERE session.assigned_agent_id=$1 AND session.status IN ('active','transferring')
		AND NOT (session.skill_group_id=ANY($2::text[])))`, input.UserID, cleanSkills).Scan(&removesActiveSkill); err != nil {
		return nil, err
	}
	if removesActiveSkill {
		return nil, ErrConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_support_agents(user_id,status,max_concurrent,created_at,updated_at)
		VALUES($1,$2,$3,$4,$4) ON CONFLICT(user_id) DO UPDATE SET status=excluded.status,
		max_concurrent=excluded.max_concurrent,updated_at=excluded.updated_at`, input.UserID, input.Status, input.MaxConcurrent, at); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM im_support_agent_skills WHERE user_id=$1`, input.UserID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_support_agent_skills(skill_group_id,user_id,created_at,updated_at)
		SELECT unnest($2::text[]),$1,$3,$3`, input.UserID, cleanSkills, at); err != nil {
		return nil, err
	}
	if input.Status == "available" {
		if _, err = assignQueuedSessionsToAgentTx(ctx, tx, input.UserID, "", at, "support-agent-saved"); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.getSupportAgent(ctx, input.UserID)
}

func scanSupportAgent(row pgx.Row) (*SupportAgent, error) {
	item := &SupportAgent{}
	err := row.Scan(&item.UserID, &item.Name, &item.Handle, &item.AvatarURL, &item.Status,
		&item.MaxConcurrent, &item.LastAssignedAt, &item.CreatedAt, &item.UpdatedAt,
		&item.ActiveSessions, &item.SkillGroupIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

const supportAgentSelect = `SELECT agent.user_id,user_row.name,COALESCE(user_row.handle,''),user_row.avatar_url,
	agent.status,agent.max_concurrent,agent.last_assigned_at,agent.created_at,agent.updated_at,
	(SELECT count(*) FROM im_support_sessions session WHERE session.assigned_agent_id=agent.user_id AND session.status IN ('active','transferring')),
	ARRAY(SELECT agent_skill.skill_group_id FROM im_support_agent_skills agent_skill WHERE agent_skill.user_id=agent.user_id AND agent_skill.enabled ORDER BY agent_skill.skill_group_id)
	FROM im_support_agents agent JOIN im_users user_row ON user_row.id=agent.user_id`

func (p *Postgres) getSupportAgent(ctx context.Context, userID string) (*SupportAgent, error) {
	return scanSupportAgent(p.pool.QueryRow(ctx, supportAgentSelect+` WHERE agent.user_id=$1`, userID))
}

func (p *Postgres) ListSupportAgents(ctx context.Context, skillGroupID string) ([]*SupportAgent, error) {
	skillGroupID = strings.TrimSpace(skillGroupID)
	rows, err := p.pool.Query(ctx, supportAgentSelect+` WHERE $1='' OR EXISTS(
		SELECT 1 FROM im_support_agent_skills own WHERE own.user_id=agent.user_id AND own.skill_group_id=$1 AND own.enabled)
		ORDER BY agent.status,lower(user_row.name),agent.user_id`, skillGroupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*SupportAgent, 0)
	for rows.Next() {
		item, scanErr := scanSupportAgent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func supportChannelID(visitorID, skillGroupID string, channelType int) (string, error) {
	if channelType == int(wukong.ChannelVisitor) {
		return visitorID, nil
	}
	if channelType == int(wukong.ChannelCustomer) {
		if strings.ContainsAny(visitorID, "|#&@") || strings.ContainsAny(skillGroupID, "|#&@") {
			return "", ErrConflict
		}
		return visitorID + "|" + skillGroupID, nil
	}
	return "", ErrConflict
}

func (p *Postgres) CreateSupportSession(ctx context.Context, input SupportSessionCreate) (*SupportSession, bool, error) {
	input.ID, input.VisitorID, input.SkillGroupID = strings.TrimSpace(input.ID), strings.TrimSpace(input.VisitorID), strings.TrimSpace(input.SkillGroupID)
	input.Subject = strings.TrimSpace(input.Subject)
	if input.ID == "" || input.VisitorID == "" || input.SkillGroupID == "" || len(input.Subject) > 240 || input.At.IsZero() {
		return nil, false, ErrConflict
	}
	channelID, err := supportChannelID(input.VisitorID, input.SkillGroupID, input.ChannelType)
	if err != nil || len(channelID) > 160 {
		return nil, false, ErrConflict
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	metadata, err := json.Marshal(input.Metadata)
	if err != nil || len(metadata) > 64<<10 {
		return nil, false, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	var existingID string
	err = tx.QueryRow(ctx, `SELECT id FROM im_support_sessions WHERE visitor_id=$1 AND status IN ('queued','active','transferring') FOR UPDATE`, input.VisitorID).Scan(&existingID)
	if err == nil {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, false, commitErr
		}
		item, getErr := p.GetSupportSession(ctx, input.VisitorID, existingID)
		return item, false, getErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	var visitorName, skillName string
	if err = tx.QueryRow(ctx, `SELECT visitor.name,skill.name FROM im_users visitor CROSS JOIN im_support_skill_groups skill
		WHERE visitor.id=$1 AND visitor.deleted_at IS NULL AND NOT visitor.banned AND skill.id=$2 AND skill.enabled`, input.VisitorID, input.SkillGroupID).Scan(&visitorName, &skillName); errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrNotFound
	} else if err != nil {
		return nil, false, err
	}
	category := "visitor"
	if input.ChannelType == int(wukong.ChannelCustomer) {
		category = "customer_service"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,created_at,updated_at)
		VALUES($1,$2,$3,$4,$4) ON CONFLICT(id) DO UPDATE SET title=excluded.title,updated_at=excluded.updated_at`,
		channelID, category, "在线客服 · "+skillName, input.At); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_business_channels(
		conversation_id,channel_type,category,owner_id,visibility,join_policy,posting_policy,metadata,created_at,updated_at
	) VALUES($1,$2,$3,$4,'private','closed','members',$5,$6,$6)
	ON CONFLICT(conversation_id) DO UPDATE SET channel_type=excluded.channel_type,category=excluded.category,
		owner_id=excluded.owner_id,metadata=excluded.metadata,disband=false,ban=false,send_ban=false,updated_at=excluded.updated_at`,
		channelID, input.ChannelType, category, input.VisitorID, metadata, input.At); err != nil {
		return nil, false, err
	}
	// A reused visitor channel must not leak a new session to agents from an
	// earlier ended session. The visitor remains the durable business member.
	if _, err = tx.Exec(ctx, `DELETE FROM im_members WHERE conversation_id=$1 AND user_id<>$2`, channelID, input.VisitorID); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at)
		VALUES($1,$2,'visitor',$3) ON CONFLICT(conversation_id,user_id) DO UPDATE SET role='visitor',joined_at=excluded.joined_at`,
		channelID, input.VisitorID, input.At); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_support_sessions(
		id,visitor_id,skill_group_id,channel_id,channel_type,subject,status,metadata,queue_entered_at,created_at,updated_at
	) VALUES($1,$2,$3,$4,$5,$6,'queued',$7,$8,$8,$8)`, input.ID, input.VisitorID, input.SkillGroupID,
		channelID, input.ChannelType, input.Subject, metadata, input.At); err != nil {
		return nil, false, mapBusinessChannelConstraint(err)
	}
	assignedAgent, err := assignSupportSessionTx(ctx, tx, input.ID, "", input.At)
	if err != nil {
		return nil, false, err
	}
	if err = enqueueWukongChannelReconcileTyped(ctx, tx, channelID, uint8(input.ChannelType), "support-created", input.At); err != nil {
		return nil, false, err
	}
	event := "support.session.queued"
	if assignedAgent != "" {
		event = "support.session.assigned"
	}
	if err = enqueueSupportEvent(ctx, tx, input.ID, input.VisitorID, event, assignedAgent, input.At); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	item, err := p.GetSupportSession(ctx, input.VisitorID, input.ID)
	return item, true, err
}

func assignSupportSessionTx(ctx context.Context, tx pgx.Tx, sessionID, requestedAgentID string, at time.Time) (string, error) {
	var skillGroupID, lockedSkillGroupID, agentID string
	if err := tx.QueryRow(ctx, `SELECT skill_group_id FROM im_support_sessions WHERE id=$1 AND status='queued'`, sessionID).Scan(&skillGroupID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrConflict
		}
		return "", err
	}
	// Routing decisions for one skill group are serialized so round-robin and
	// capacity checks remain exact when multiple visitors enter concurrently.
	if err := lockSupportSkillIDsTx(ctx, tx, []string{skillGroupID}); err != nil {
		return "", err
	}
	if err := tx.QueryRow(ctx, `SELECT skill_group_id FROM im_support_sessions
		WHERE id=$1 AND status='queued' FOR UPDATE`, sessionID).Scan(&lockedSkillGroupID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrConflict
		}
		return "", err
	}
	if lockedSkillGroupID != skillGroupID {
		return "", ErrConflict
	}
	query := `SELECT agent.user_id FROM im_support_agents agent
		JOIN im_support_agent_skills agent_skill ON agent_skill.user_id=agent.user_id AND agent_skill.skill_group_id=$1 AND agent_skill.enabled
		JOIN im_support_skill_groups skill ON skill.id=agent_skill.skill_group_id AND skill.enabled
		CROSS JOIN LATERAL (SELECT count(*) AS active_count FROM im_support_sessions active
			WHERE active.assigned_agent_id=agent.user_id AND active.status IN ('active','transferring')) total_load
		CROSS JOIN LATERAL (SELECT count(*) AS active_count FROM im_support_sessions active
			WHERE active.assigned_agent_id=agent.user_id AND active.skill_group_id=$1
			AND active.status IN ('active','transferring')) skill_load
		WHERE (($2='' AND agent.status='available') OR ($2<>'' AND agent.user_id=$2 AND agent.status IN ('available','busy')))
		AND total_load.active_count<agent.max_concurrent
		AND skill_load.active_count<skill.max_concurrent_per_agent
		ORDER BY agent_skill.priority,
			CASE WHEN skill.routing_strategy='least_active' THEN skill_load.active_count END,
			CASE WHEN skill.routing_strategy='least_active' THEN total_load.active_count END,
			agent.last_assigned_at NULLS FIRST,
			CASE WHEN skill.routing_strategy='round_robin' THEN skill_load.active_count END,
			agent.user_id
		FOR UPDATE OF agent LIMIT 1`
	err := tx.QueryRow(ctx, query, skillGroupID, requestedAgentID).Scan(&agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		if requestedAgentID != "" {
			return "", ErrForbidden
		}
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var channelID string
	if err = tx.QueryRow(ctx, `UPDATE im_support_sessions SET status='active',assigned_agent_id=$2,assigned_at=$3,updated_at=$3
		WHERE id=$1 AND status='queued' RETURNING channel_id`, sessionID, agentID, at).Scan(&channelID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_support_agents SET last_assigned_at=$2,updated_at=$2 WHERE user_id=$1`, agentID, at); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'agent',$3)
		ON CONFLICT(conversation_id,user_id) DO UPDATE SET role='agent',joined_at=excluded.joined_at`, channelID, agentID, at); err != nil {
		return "", err
	}
	return agentID, nil
}

// assignQueuedSessionsToAgentTx fills every currently available slot and
// returns the assigned session IDs in FIFO order. The caller keeps the first
// ID for the status API response; all assignments are persisted and notified.
func assignQueuedSessionsToAgentTx(ctx context.Context, tx pgx.Tx, agentID, skillGroupID string, at time.Time, reason string) ([]string, error) {
	if err := lockSupportSkillsForAgentTx(ctx, tx, agentID, skillGroupID); err != nil {
		return nil, err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM im_support_agents WHERE user_id=$1 FOR UPDATE`, agentID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrForbidden
	} else if err != nil {
		return nil, err
	}
	if status != "available" {
		return nil, nil
	}
	assignedIDs := make([]string, 0)
	for len(assignedIDs) < 100 {
		var sessionID string
		err := tx.QueryRow(ctx, `SELECT session.id FROM im_support_sessions session
			JOIN im_support_agent_skills own ON own.skill_group_id=session.skill_group_id AND own.user_id=$1 AND own.enabled
			JOIN im_support_skill_groups skill ON skill.id=session.skill_group_id AND skill.enabled
			JOIN im_support_agents agent ON agent.user_id=$1 AND agent.status='available'
			WHERE session.status='queued' AND ($2='' OR session.skill_group_id=$2)
			AND (SELECT count(*) FROM im_support_sessions active WHERE active.assigned_agent_id=$1
				AND active.status IN ('active','transferring'))<agent.max_concurrent
			AND (SELECT count(*) FROM im_support_sessions active WHERE active.assigned_agent_id=$1
				AND active.skill_group_id=session.skill_group_id AND active.status IN ('active','transferring'))<skill.max_concurrent_per_agent
			ORDER BY session.queue_entered_at,session.id FOR UPDATE OF session LIMIT 1`, agentID, skillGroupID).Scan(&sessionID)
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, err
		}
		if _, err = assignSupportSessionTx(ctx, tx, sessionID, agentID, at); err != nil {
			return nil, err
		}
		var channelID string
		var channelType uint8
		if err = tx.QueryRow(ctx, `SELECT channel_id,channel_type FROM im_support_sessions WHERE id=$1`, sessionID).Scan(&channelID, &channelType); err != nil {
			return nil, err
		}
		if err = enqueueWukongChannelReconcileTyped(ctx, tx, channelID, channelType, reason, at); err != nil {
			return nil, err
		}
		if err = enqueueSupportEvent(ctx, tx, sessionID, agentID, "support.session.assigned", agentID, at); err != nil {
			return nil, err
		}
		assignedIDs = append(assignedIDs, sessionID)
	}
	return assignedIDs, nil
}

func enqueueSupportEvent(ctx context.Context, tx pgx.Tx, sessionID, actorID, event, agentID string, at time.Time) error {
	data := map[string]any{"schemaVersion": 1, "sessionId": sessionID, "assignedAgentId": agentID}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var eventID int64
	if err = tx.QueryRow(ctx, `INSERT INTO im_support_session_events(session_id,event,actor_id,data,created_at)
		VALUES($1,$2,$3,$4,$5) RETURNING id`, sessionID, event, actorID, raw, at).Scan(&eventID); err != nil {
		return err
	}
	var channelID string
	var channelType uint8
	if err = tx.QueryRow(ctx, `SELECT channel_id,channel_type FROM im_support_sessions WHERE id=$1`, sessionID).Scan(&channelID, &channelType); err != nil {
		return err
	}
	return enqueueWukongOutbox(ctx, tx, fmt.Sprintf("support-event:%d", eventID), wukong.OperationStoredMessage,
		"support_session", sessionID, wukong.StoredMessageRequest{
			ClientMsgNo: fmt.Sprintf("support_event_%d", eventID), FromUID: "____system",
			ChannelID: channelID, ChannelType: channelType,
			Payload: map[string]any{"type": wukong.ContentTypeSupportEvent, "schemaVersion": 1,
				"event": event, "sessionId": sessionID, "assignedAgentId": agentID},
		})
}

func (p *Postgres) GetSupportSession(ctx context.Context, actorID, sessionID string) (*SupportSession, error) {
	actorID, sessionID = strings.TrimSpace(actorID), strings.TrimSpace(sessionID)
	if actorID == "" || sessionID == "" {
		return nil, ErrForbidden
	}
	item, err := scanSupportSession(p.pool.QueryRow(ctx, supportSessionSelect+`
		WHERE session.id=$2 AND (session.visitor_id=$1 OR session.assigned_agent_id=$1 OR
		(session.status='queued' AND EXISTS(SELECT 1 FROM im_support_agent_skills own
		 WHERE own.user_id=$1 AND own.skill_group_id=session.skill_group_id AND own.enabled)))`, actorID, sessionID))
	if err == ErrNotFound {
		return nil, ErrForbidden
	}
	return item, err
}

func (p *Postgres) ListSupportSessions(ctx context.Context, actorID, status, skillGroupID string, limit int) ([]*SupportSession, error) {
	actorID, status, skillGroupID = strings.TrimSpace(actorID), strings.TrimSpace(status), strings.TrimSpace(skillGroupID)
	if actorID == "" || (status != "" && status != "queued" && status != "active" && status != "transferring" && status != "ended") {
		return nil, ErrConflict
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx, supportSessionSelect+`
		WHERE ($2='' OR session.status=$2) AND ($3='' OR session.skill_group_id=$3)
		AND (session.visitor_id=$1 OR session.assigned_agent_id=$1 OR
		(session.status='queued' AND EXISTS(SELECT 1 FROM im_support_agent_skills own
		 WHERE own.user_id=$1 AND own.skill_group_id=session.skill_group_id AND own.enabled)))
		ORDER BY CASE WHEN session.status='queued' THEN 0 ELSE 1 END,session.queue_entered_at,session.id LIMIT $4`, actorID, status, skillGroupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*SupportSession, 0, limit)
	for rows.Next() {
		item, scanErr := scanSupportSession(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) ClaimSupportSession(ctx context.Context, agentID, sessionID string, at time.Time) (*SupportSession, error) {
	agentID, sessionID = strings.TrimSpace(agentID), strings.TrimSpace(sessionID)
	if agentID == "" || sessionID == "" || at.IsZero() {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	assignedAgent, err := assignSupportSessionTx(ctx, tx, sessionID, agentID, at)
	if err != nil {
		return nil, err
	}
	var channelID string
	var channelType uint8
	if err = tx.QueryRow(ctx, `SELECT channel_id,channel_type FROM im_support_sessions WHERE id=$1`, sessionID).Scan(&channelID, &channelType); err != nil {
		return nil, err
	}
	if err = enqueueWukongChannelReconcileTyped(ctx, tx, channelID, channelType, "support-claimed", at); err != nil {
		return nil, err
	}
	if err = enqueueSupportEvent(ctx, tx, sessionID, agentID, "support.session.assigned", assignedAgent, at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.GetSupportSession(ctx, agentID, sessionID)
}

func (p *Postgres) SetSupportAgentStatus(ctx context.Context, agentID, status string, at time.Time) (*SupportAgent, *SupportSession, error) {
	agentID, status = strings.TrimSpace(agentID), strings.TrimSpace(status)
	if agentID == "" || !validSupportStatus(status) || at.IsZero() {
		return nil, nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockSupportSkillsForAgentTx(ctx, tx, agentID, ""); err != nil {
		return nil, nil, err
	}
	tag, err := tx.Exec(ctx, `UPDATE im_support_agents SET status=$2,updated_at=$3 WHERE user_id=$1`, agentID, status, at)
	if err != nil {
		return nil, nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, nil, ErrForbidden
	}
	assignedIDs := make([]string, 0)
	if status == "available" {
		assignedIDs, err = assignQueuedSessionsToAgentTx(ctx, tx, agentID, "", at, "support-auto-assigned")
		if err != nil {
			return nil, nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	agent, err := p.getSupportAgent(ctx, agentID)
	if err != nil || len(assignedIDs) == 0 {
		return agent, nil, err
	}
	session, err := p.GetSupportSession(ctx, agentID, assignedIDs[0])
	return agent, session, err
}

func (p *Postgres) TransferSupportSession(ctx context.Context, actorID, sessionID, targetAgentID string, at time.Time) (*SupportSession, error) {
	actorID, sessionID, targetAgentID = strings.TrimSpace(actorID), strings.TrimSpace(sessionID), strings.TrimSpace(targetAgentID)
	if actorID == "" || sessionID == "" || targetAgentID == "" || actorID == targetAgentID || at.IsZero() {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var unlockedSkillGroupID string
	if err = tx.QueryRow(ctx, `SELECT skill_group_id FROM im_support_sessions WHERE id=$1 AND status='active'`, sessionID).Scan(&unlockedSkillGroupID); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	} else if err != nil {
		return nil, err
	}
	if err = lockSupportSkillIDsTx(ctx, tx, []string{unlockedSkillGroupID}); err != nil {
		return nil, err
	}
	var currentAgent, visitorID, skillGroupID, channelID string
	var channelType uint8
	if err = tx.QueryRow(ctx, `SELECT assigned_agent_id,visitor_id,skill_group_id,channel_id,channel_type
		FROM im_support_sessions WHERE id=$1 AND status='active' FOR UPDATE`, sessionID).Scan(&currentAgent, &visitorID, &skillGroupID, &channelID, &channelType); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	} else if err != nil {
		return nil, err
	}
	if skillGroupID != unlockedSkillGroupID {
		return nil, ErrConflict
	}
	if actorID != currentAgent {
		return nil, ErrForbidden
	}
	var targetAllowed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_support_agents agent
		JOIN im_support_agent_skills skill ON skill.user_id=agent.user_id AND skill.skill_group_id=$2 AND skill.enabled
		JOIN im_support_skill_groups skill_group ON skill_group.id=skill.skill_group_id AND skill_group.enabled
		WHERE agent.user_id=$1 AND agent.status IN ('available','busy')
		AND (SELECT count(*) FROM im_support_sessions active WHERE active.assigned_agent_id=agent.user_id AND active.status IN ('active','transferring'))
		< LEAST(agent.max_concurrent,skill_group.max_concurrent_per_agent))`, targetAgentID, skillGroupID).Scan(&targetAllowed); err != nil {
		return nil, err
	}
	if !targetAllowed {
		return nil, ErrForbidden
	}
	if _, err = tx.Exec(ctx, `UPDATE im_support_sessions SET status='active',assigned_agent_id=$2,assigned_at=$3,
		transfer_count=transfer_count+1,updated_at=$3 WHERE id=$1`, sessionID, targetAgentID, at); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM im_members WHERE conversation_id=$1 AND user_id=$2`, channelID, currentAgent); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'agent',$3)
		ON CONFLICT(conversation_id,user_id) DO UPDATE SET role='agent',joined_at=excluded.joined_at`, channelID, targetAgentID, at); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_support_agents SET last_assigned_at=$2,updated_at=$2 WHERE user_id=$1`, targetAgentID, at); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{"sessionId": sessionID, "fromAgentId": currentAgent, "toAgentId": targetAgentID})
	if err = enqueueWukongBusinessEvent(ctx, tx, "support_session", sessionID, "support.session.transferred", payload,
		[]wukongCommandRecipient{{UserID: visitorID}, {UserID: currentAgent}, {UserID: targetAgentID}}); err != nil {
		return nil, err
	}
	if err = enqueueWukongChannelReconcileTyped(ctx, tx, channelID, channelType, "support-transferred", at); err != nil {
		return nil, err
	}
	if err = enqueueSupportEvent(ctx, tx, sessionID, actorID, "support.session.transferred", targetAgentID, at); err != nil {
		return nil, err
	}
	if _, err = assignQueuedSessionsToAgentTx(ctx, tx, currentAgent, skillGroupID, at, "support-transfer-capacity"); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.GetSupportSession(ctx, targetAgentID, sessionID)
}

func (p *Postgres) EndSupportSession(ctx context.Context, actorID, sessionID string, at time.Time) (*SupportSession, error) {
	actorID, sessionID = strings.TrimSpace(actorID), strings.TrimSpace(sessionID)
	if actorID == "" || sessionID == "" || at.IsZero() {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var skillGroupID string
	if err = tx.QueryRow(ctx, `SELECT skill_group_id FROM im_support_sessions
		WHERE id=$1 AND status IN ('queued','active','transferring')`, sessionID).Scan(&skillGroupID); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	} else if err != nil {
		return nil, err
	}
	if err = lockSupportSkillIDsTx(ctx, tx, []string{skillGroupID}); err != nil {
		return nil, err
	}
	var visitorID, agentID, channelID string
	var channelType uint8
	err = tx.QueryRow(ctx, `SELECT visitor_id,COALESCE(assigned_agent_id,''),channel_id,channel_type
		FROM im_support_sessions WHERE id=$1 AND status IN ('queued','active','transferring') FOR UPDATE`, sessionID).Scan(&visitorID, &agentID, &channelID, &channelType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if actorID != visitorID && actorID != agentID {
		return nil, ErrForbidden
	}
	if _, err = tx.Exec(ctx, `UPDATE im_support_sessions SET status='ended',ended_at=$2,ended_by=$3,updated_at=$2 WHERE id=$1`, sessionID, at, actorID); err != nil {
		return nil, err
	}
	if agentID != "" {
		payload, _ := json.Marshal(map[string]any{"sessionId": sessionID, "endedBy": actorID})
		if err = enqueueWukongBusinessEvent(ctx, tx, "support_session", sessionID, "support.session.ended", payload,
			[]wukongCommandRecipient{{UserID: visitorID}, {UserID: agentID}}); err != nil {
			return nil, err
		}
	}
	if err = enqueueSupportEvent(ctx, tx, sessionID, actorID, "support.session.ended", agentID, at); err != nil {
		return nil, err
	}
	if agentID != "" {
		if _, err = tx.Exec(ctx, `DELETE FROM im_members WHERE conversation_id=$1 AND user_id=$2`, channelID, agentID); err != nil {
			return nil, err
		}
	}
	if err = enqueueWukongChannelReconcileTyped(ctx, tx, channelID, channelType, "support-ended", at); err != nil {
		return nil, err
	}
	if agentID != "" {
		if _, err = assignQueuedSessionsToAgentTx(ctx, tx, agentID, skillGroupID, at, "support-ended-capacity"); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.GetSupportSession(ctx, actorID, sessionID)
}

func (p *Postgres) RateSupportSession(ctx context.Context, visitorID, sessionID string, rating int, comment string, at time.Time) (*SupportSession, error) {
	visitorID, sessionID, comment = strings.TrimSpace(visitorID), strings.TrimSpace(sessionID), strings.TrimSpace(comment)
	if visitorID == "" || sessionID == "" || rating < 1 || rating > 5 || len(comment) > 1000 || at.IsZero() {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var agentID string
	err = tx.QueryRow(ctx, `UPDATE im_support_sessions SET rating=$3,rating_comment=$4,rated_at=$5,updated_at=$5
		WHERE id=$1 AND visitor_id=$2 AND status='ended' AND rating IS NULL
		RETURNING COALESCE(assigned_agent_id,'')`, sessionID, visitorID, rating, comment, at).Scan(&agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(map[string]any{"schemaVersion": 1, "rating": rating, "comment": comment})
	if _, err = tx.Exec(ctx, `INSERT INTO im_support_session_events(session_id,event,actor_id,data,created_at)
		VALUES($1,'support.session.rated',$2,$3,$4)`, sessionID, visitorID, data, at); err != nil {
		return nil, err
	}
	if agentID != "" {
		payload, _ := json.Marshal(map[string]any{"sessionId": sessionID})
		if err = enqueueWukongBusinessEvent(ctx, tx, "support_session", sessionID, "support.session.rated", payload,
			[]wukongCommandRecipient{{UserID: agentID}}); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.GetSupportSession(ctx, visitorID, sessionID)
}

func (p *Postgres) ListAdminSupportSessions(ctx context.Context, query, status, skillGroupID, after string, limit int) ([]*SupportSession, int64, string, error) {
	query, status, skillGroupID, after = strings.TrimSpace(query), strings.TrimSpace(status), strings.TrimSpace(skillGroupID), strings.TrimSpace(after)
	if len(query) > 200 || (status != "" && status != "queued" && status != "active" && status != "transferring" && status != "ended") {
		return nil, 0, "", ErrConflict
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	pattern := "%" + query + "%"
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_support_sessions session
		JOIN im_users visitor ON visitor.id=session.visitor_id
		WHERE ($1='' OR session.id ILIKE $2 OR session.subject ILIKE $2 OR visitor.name ILIKE $2)
		AND ($3='' OR session.status=$3) AND ($4='' OR session.skill_group_id=$4)`,
		query, pattern, status, skillGroupID).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, supportSessionSelect+`
		WHERE session.id>$1
		AND ($2='' OR session.id ILIKE $3 OR session.subject ILIKE $3 OR visitor.name ILIKE $3)
		AND ($4='' OR session.status=$4) AND ($5='' OR session.skill_group_id=$5)
		ORDER BY session.id LIMIT $6`, after, query, pattern, status, skillGroupID, limit+1)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]*SupportSession, 0, limit)
	for rows.Next() {
		if len(items) == limit {
			return items, total, items[len(items)-1].ID, nil
		}
		item, scanErr := scanSupportSession(rows)
		if scanErr != nil {
			return nil, 0, "", scanErr
		}
		items = append(items, item)
	}
	return items, total, "", rows.Err()
}

func (p *WithRedis) SaveSupportSkillGroup(ctx context.Context, input SupportSkillGroupInput, at time.Time) (*SupportSkillGroup, error) {
	if support, ok := p.base.(SupportStore); ok {
		return support.SaveSupportSkillGroup(ctx, input, at)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListSupportSkillGroups(ctx context.Context, includeDisabled bool) ([]*SupportSkillGroup, error) {
	if support, ok := p.base.(SupportStore); ok {
		return support.ListSupportSkillGroups(ctx, includeDisabled)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) SaveSupportAgent(ctx context.Context, input SupportAgentInput, at time.Time) (*SupportAgent, error) {
	if support, ok := p.base.(SupportStore); ok {
		return support.SaveSupportAgent(ctx, input, at)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListSupportAgents(ctx context.Context, skillGroupID string) ([]*SupportAgent, error) {
	if support, ok := p.base.(SupportStore); ok {
		return support.ListSupportAgents(ctx, skillGroupID)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) SetSupportAgentStatus(ctx context.Context, userID, status string, at time.Time) (*SupportAgent, *SupportSession, error) {
	if support, ok := p.base.(SupportStore); ok {
		return support.SetSupportAgentStatus(ctx, userID, status, at)
	}
	return nil, nil, ErrUnsupported
}

func (p *WithRedis) CreateSupportSession(ctx context.Context, input SupportSessionCreate) (*SupportSession, bool, error) {
	if support, ok := p.base.(SupportStore); ok {
		return support.CreateSupportSession(ctx, input)
	}
	return nil, false, ErrUnsupported
}

func (p *WithRedis) GetSupportSession(ctx context.Context, actorID, sessionID string) (*SupportSession, error) {
	if support, ok := p.base.(SupportStore); ok {
		return support.GetSupportSession(ctx, actorID, sessionID)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListSupportSessions(ctx context.Context, actorID, status, skillGroupID string, limit int) ([]*SupportSession, error) {
	if support, ok := p.base.(SupportStore); ok {
		return support.ListSupportSessions(ctx, actorID, status, skillGroupID, limit)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ClaimSupportSession(ctx context.Context, agentID, sessionID string, at time.Time) (*SupportSession, error) {
	if support, ok := p.base.(SupportStore); ok {
		return support.ClaimSupportSession(ctx, agentID, sessionID, at)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) TransferSupportSession(ctx context.Context, actorID, sessionID, targetAgentID string, at time.Time) (*SupportSession, error) {
	if support, ok := p.base.(SupportStore); ok {
		return support.TransferSupportSession(ctx, actorID, sessionID, targetAgentID, at)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) EndSupportSession(ctx context.Context, actorID, sessionID string, at time.Time) (*SupportSession, error) {
	if support, ok := p.base.(SupportStore); ok {
		return support.EndSupportSession(ctx, actorID, sessionID, at)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) RateSupportSession(ctx context.Context, visitorID, sessionID string, rating int, comment string, at time.Time) (*SupportSession, error) {
	if support, ok := p.base.(SupportStore); ok {
		return support.RateSupportSession(ctx, visitorID, sessionID, rating, comment, at)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListAdminSupportSessions(ctx context.Context, query, status, skillGroupID, after string, limit int) ([]*SupportSession, int64, string, error) {
	if support, ok := p.base.(SupportAdminStore); ok {
		return support.ListAdminSupportSessions(ctx, query, status, skillGroupID, after, limit)
	}
	return nil, 0, "", ErrUnsupported
}
