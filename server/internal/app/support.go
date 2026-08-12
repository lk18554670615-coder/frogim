package app

import (
	"context"
	"strings"
	"time"

	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukong"
)

func (a *App) SaveSupportSkillGroup(ctx context.Context, actorID string, input store.SupportSkillGroupInput) (*store.SupportSkillGroup, error) {
	support, ok := a.persistence.(store.SupportStore)
	if !ok {
		return nil, ErrNotFound
	}
	input.ActorID = strings.TrimSpace(actorID)
	if input.ID == "" {
		input.ID = id("support_skill")
	}
	if input.RoutingStrategy == "" {
		input.RoutingStrategy = "least_active"
	}
	if input.MaxConcurrentPerAgent == 0 {
		input.MaxConcurrentPerAgent = 5
	}
	item, err := support.SaveSupportSkillGroup(ctx, input, time.Now())
	if err != nil {
		return nil, mapStoreError(err)
	}
	return item, nil
}

func (a *App) SupportSkillGroups(ctx context.Context, includeDisabled bool) ([]*store.SupportSkillGroup, error) {
	if support, ok := a.persistence.(store.SupportStore); ok {
		items, err := support.ListSupportSkillGroups(ctx, includeDisabled)
		return items, mapStoreError(err)
	}
	return nil, ErrNotFound
}

func (a *App) SaveSupportAgent(ctx context.Context, input store.SupportAgentInput) (*store.SupportAgent, error) {
	if support, ok := a.persistence.(store.SupportStore); ok {
		if input.Status == "" {
			input.Status = "offline"
		}
		if input.MaxConcurrent == 0 {
			input.MaxConcurrent = 5
		}
		item, err := support.SaveSupportAgent(ctx, input, time.Now())
		return item, mapStoreError(err)
	}
	return nil, ErrNotFound
}

func (a *App) SupportAgents(ctx context.Context, skillGroupID string) ([]*store.SupportAgent, error) {
	if support, ok := a.persistence.(store.SupportStore); ok {
		items, err := support.ListSupportAgents(ctx, skillGroupID)
		return items, mapStoreError(err)
	}
	return nil, ErrNotFound
}

func (a *App) SetSupportAgentStatus(ctx context.Context, agentID, status string) (*store.SupportAgent, *store.SupportSession, error) {
	if support, ok := a.persistence.(store.SupportStore); ok {
		agent, session, err := support.SetSupportAgentStatus(ctx, agentID, status, time.Now())
		if err != nil {
			return nil, nil, mapStoreError(err)
		}
		if session != nil {
			a.publish([]string{session.VisitorID, agentID}, "support.session.assigned", session)
		}
		return agent, session, nil
	}
	return nil, nil, ErrNotFound
}

func (a *App) CreateSupportSession(ctx context.Context, visitorID, skillGroupID, subject string, channelType int, metadata map[string]any) (*store.SupportSession, bool, error) {
	if support, ok := a.persistence.(store.SupportStore); ok {
		if channelType == 0 {
			channelType = int(wukong.ChannelVisitor)
		}
		item, created, err := support.CreateSupportSession(ctx, store.SupportSessionCreate{
			ID: id("support_session"), VisitorID: visitorID, SkillGroupID: skillGroupID,
			Subject: subject, ChannelType: channelType, Metadata: metadata, At: time.Now(),
		})
		if err != nil {
			return nil, false, mapStoreError(err)
		}
		recipients := []string{item.VisitorID}
		if item.AssignedAgentID != "" {
			recipients = append(recipients, item.AssignedAgentID)
		}
		a.publish(recipients, "support.session.updated", item)
		return item, created, nil
	}
	return nil, false, ErrNotFound
}

func (a *App) SupportSession(ctx context.Context, actorID, sessionID string) (*store.SupportSession, error) {
	if support, ok := a.persistence.(store.SupportStore); ok {
		item, err := support.GetSupportSession(ctx, actorID, sessionID)
		return item, mapStoreError(err)
	}
	return nil, ErrNotFound
}

func (a *App) SupportSessions(ctx context.Context, actorID, status, skillGroupID string, limit int) ([]*store.SupportSession, error) {
	if support, ok := a.persistence.(store.SupportStore); ok {
		items, err := support.ListSupportSessions(ctx, actorID, status, skillGroupID, limit)
		return items, mapStoreError(err)
	}
	return nil, ErrNotFound
}

func (a *App) ClaimSupportSession(ctx context.Context, agentID, sessionID string) (*store.SupportSession, error) {
	if support, ok := a.persistence.(store.SupportStore); ok {
		item, err := support.ClaimSupportSession(ctx, agentID, sessionID, time.Now())
		if err != nil {
			return nil, mapStoreError(err)
		}
		a.publish([]string{item.VisitorID, item.AssignedAgentID}, "support.session.assigned", item)
		return item, nil
	}
	return nil, ErrNotFound
}

func (a *App) TransferSupportSession(ctx context.Context, actorID, sessionID, targetAgentID string) (*store.SupportSession, error) {
	if support, ok := a.persistence.(store.SupportStore); ok {
		item, err := support.TransferSupportSession(ctx, actorID, sessionID, targetAgentID, time.Now())
		if err != nil {
			return nil, mapStoreError(err)
		}
		a.publish([]string{item.VisitorID, actorID, targetAgentID}, "support.session.transferred", item)
		return item, nil
	}
	return nil, ErrNotFound
}

func (a *App) EndSupportSession(ctx context.Context, actorID, sessionID string) (*store.SupportSession, error) {
	if support, ok := a.persistence.(store.SupportStore); ok {
		item, err := support.EndSupportSession(ctx, actorID, sessionID, time.Now())
		if err != nil {
			return nil, mapStoreError(err)
		}
		a.publish([]string{item.VisitorID, item.AssignedAgentID}, "support.session.ended", item)
		return item, nil
	}
	return nil, ErrNotFound
}

func (a *App) RateSupportSession(ctx context.Context, visitorID, sessionID string, rating int, comment string) (*store.SupportSession, error) {
	if support, ok := a.persistence.(store.SupportStore); ok {
		item, err := support.RateSupportSession(ctx, visitorID, sessionID, rating, comment, time.Now())
		if err != nil {
			return nil, mapStoreError(err)
		}
		a.publish([]string{item.AssignedAgentID}, "support.session.rated", item)
		return item, nil
	}
	return nil, ErrNotFound
}

func (a *App) AdminSupportSessionsPage(ctx context.Context, query, status, skillGroupID, cursor string, limit int) ([]*store.SupportSession, int64, string, error) {
	admin, ok := a.persistence.(store.SupportAdminStore)
	if !ok {
		return nil, 0, "", ErrNotFound
	}
	items, total, next, err := admin.ListAdminSupportSessions(ctx, query, status, skillGroupID, cursor, limit)
	return items, total, next, mapStoreError(err)
}

func (a *App) AdminSupportSession(ctx context.Context, sessionID string) (*store.SupportSession, error) {
	items, _, _, err := a.AdminSupportSessionsPage(ctx, sessionID, "", "", "", 200)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID == strings.TrimSpace(sessionID) {
			return item, nil
		}
	}
	return nil, ErrNotFound
}
