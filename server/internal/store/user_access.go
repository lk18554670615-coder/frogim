package store

import (
	"context"
	"github.com/linli/im/server/internal/model"
	"time"
)

const UserAccessRetention = 180 * 24 * time.Hour

// These complete records are admin-only. Never embed them in model.User or an
// App response; the peer-login-info endpoint projects only the last login IP.
type UserAccessProfile struct {
	RegistrationSource string     `json:"registrationSource"`
	RegistrationIP     string     `json:"registrationIp,omitempty"`
	LastLoginIP        string     `json:"lastLoginIp,omitempty"`
	LastLoginAt        *time.Time `json:"lastLoginAt,omitempty"`
	MatchedSources     []string   `json:"matchedSources"`
}
type UserAccessLog struct {
	ID          string      `json:"id"`
	UserID      string      `json:"userId,omitempty"`
	User        *model.User `json:"user,omitempty"`
	Event       string      `json:"event"`
	Method      string      `json:"method"`
	Result      string      `json:"result"`
	FailureCode string      `json:"failureCode,omitempty"`
	IP          string      `json:"ip,omitempty"`
	Platform    string      `json:"platform"`
	OccurredAt  time.Time   `json:"occurredAt"`
	// Only the async worker sees the submitted phone; it is never persisted.
	LookupPhone string `json:"-"`
}
type UserAccessQuery struct {
	UserID, IP, Event, Result, Method, Cursor string
	From, To                                  time.Time
	Limit                                     int
}
type UserAccessPage struct {
	Items      []UserAccessLog `json:"items"`
	NextCursor string          `json:"nextCursor"`
}
type UserAccessStore interface {
	RecordUserAccess(context.Context, UserAccessLog) error
	UserAccessProfiles(context.Context, []string, string) (map[string]UserAccessProfile, error)
	ListUserAccessLogs(context.Context, UserAccessQuery) (UserAccessPage, error)
	ListAdminUsersByIP(context.Context, string, string, string, int, string, string) ([]*model.User, int64, string, error)
}

func (p *WithRedis) RecordUserAccess(ctx context.Context, e UserAccessLog) error {
	if s, ok := p.base.(UserAccessStore); ok {
		return s.RecordUserAccess(ctx, e)
	}
	return ErrUnsupported
}
func (p *WithRedis) UserAccessProfiles(ctx context.Context, ids []string, ip string) (map[string]UserAccessProfile, error) {
	if s, ok := p.base.(UserAccessStore); ok {
		return s.UserAccessProfiles(ctx, ids, ip)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListUserAccessLogs(ctx context.Context, q UserAccessQuery) (UserAccessPage, error) {
	if s, ok := p.base.(UserAccessStore); ok {
		return s.ListUserAccessLogs(ctx, q)
	}
	return UserAccessPage{}, ErrUnsupported
}
func (p *WithRedis) ListAdminUsersByIP(ctx context.Context, q, status, cursor string, limit int, ip, source string) ([]*model.User, int64, string, error) {
	if s, ok := p.base.(UserAccessStore); ok {
		return s.ListAdminUsersByIP(ctx, q, status, cursor, limit, ip, source)
	}
	return nil, 0, "", ErrUnsupported
}
