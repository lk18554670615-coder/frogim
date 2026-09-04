package app

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInviteRequired   = errors.New("invite code required")
	ErrInviteInvalid    = errors.New("invite code invalid")
	ErrInviteDisabled   = errors.New("invite code disabled")
	ErrInviteChangeUsed = errors.New("invite code change already used")
)

var customInviteCodePattern = regexp.MustCompile(`^[A-Z0-9](?:[A-Z0-9_-]{4,18})[A-Z0-9]$`)
var invitePhonePattern = regexp.MustCompile(`^[0-9]{11}$`)
var reservedInviteCodes = map[string]struct{}{
	"ADMIN": {}, "SYSTEM": {}, "OFFICIAL": {}, "SUPPORT": {}, "SERVICE": {}, "SECURITY": {},
}

func normalizeInviteCode(value string) string { return strings.ToUpper(strings.TrimSpace(value)) }

func (a *App) validCustomInviteCode(value string) bool {
	if !customInviteCodePattern.MatchString(value) || invitePhonePattern.MatchString(value) {
		return false
	}
	if _, reserved := reservedInviteCodes[value]; reserved {
		return false
	}
	needle := strings.ToLower(value)
	for _, entry := range a.SensitiveWords() {
		word := strings.TrimSpace(strings.SplitN(entry, "|", 2)[0])
		if word != "" && strings.Contains(needle, strings.ToLower(word)) {
			return false
		}
	}
	return true
}

func mapInviteStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrInviteRequired):
		return ErrInviteRequired
	case errors.Is(err, store.ErrInviteInvalid):
		return ErrInviteInvalid
	case errors.Is(err, store.ErrInviteDisabled):
		return ErrInviteDisabled
	case errors.Is(err, store.ErrInviteChangeUsed):
		return ErrInviteChangeUsed
	case errors.Is(err, store.ErrConflict):
		return ErrConflict
	case errors.Is(err, store.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	default:
		return err
	}
}

func inviteMode(settings map[string]any) string {
	mode, _ := settings["inviteRegistrationMode"].(string)
	if mode != "disabled" && mode != "required" && mode != "optional" {
		return "optional"
	}
	return mode
}

func registrationPolicy(settings map[string]any) (bool, string) {
	enabled := true
	if value, ok := settings["registrationEnabled"].(bool); ok {
		enabled = value
	} else if value, ok := settings["allowRegistration"].(bool); ok {
		enabled = value
	}
	return enabled, inviteMode(settings)
}

func (a *App) InviteRegistrationMode() string { return inviteMode(a.Settings()) }

func (a *App) RegisterWithPasswordAndInvite(phone, name, password, inviteCode string) (*model.User, error) {
	phone, name, inviteCode = strings.TrimSpace(phone), strings.TrimSpace(name), normalizeInviteCode(inviteCode)
	if !ValidPhoneNumber(phone) || name == "" || len([]rune(name)) > 40 || !a.validPassword(password) {
		return nil, ErrInvalid
	}
	registrationEnabled, inviteMode := registrationPolicy(a.Settings())
	if !registrationEnabled {
		return nil, ErrForbidden
	}
	if inviteMode == "required" && inviteCode == "" {
		return nil, ErrInviteRequired
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	if s, ok := a.persistence.(store.InvitationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		u, storeErr := s.RegisterPasswordUserWithInvite(ctx, phone, name, id("usr"), hash, time.Now().UTC(), inviteMode, inviteCode)
		return u, mapInviteStoreError(storeErr)
	}
	if inviteCode != "" {
		return nil, ErrInviteInvalid
	}
	return a.RegisterWithPassword(phone, name, password)
}

func hashPassword(password string) (string, error) {
	hash, err := bcryptGenerate(password)
	return hash, err
}

// Kept as a seam so invitation registration uses the same bcrypt cost as all
// other password entry points without exporting password internals.
func bcryptGenerate(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(hash), err
}

func (a *App) LoginWithCreationAndInvite(phone, name, inviteCode string) (*model.User, bool, error) {
	phone, name, inviteCode = strings.TrimSpace(phone), strings.TrimSpace(name), normalizeInviteCode(inviteCode)
	if !ValidPhoneNumber(phone) || len([]rune(name)) > 80 {
		return nil, false, ErrInvalid
	}
	if name == "" {
		name = "用户" + phone[max(0, len(phone)-4):]
	}
	registrationEnabled, inviteMode := registrationPolicy(a.Settings())
	if s, ok := a.persistence.(store.InvitationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		u, created, storeErr := s.LoginOrCreateUserWithInvite(ctx, phone, name, id("usr"), time.Now().UTC(), registrationEnabled, inviteMode, inviteCode)
		return u, created, mapInviteStoreError(storeErr)
	}
	if !registrationEnabled {
		a.mu.RLock()
		uid := a.state.PhoneToUser[phone]
		a.mu.RUnlock()
		if uid == "" {
			return nil, false, ErrForbidden
		}
	}
	return a.LoginWithCreation(phone, name)
}

func (a *App) ValidateInviteCode(ctx context.Context, code string) (bool, error) {
	code = normalizeInviteCode(code)
	if code == "" || a.InviteRegistrationMode() == "disabled" {
		return false, nil
	}
	if s, ok := a.persistence.(store.InvitationStore); ok {
		return s.ValidateInviteCode(ctx, code)
	}
	return false, ErrUnavailable
}

func (a *App) UserInviteCode(ctx context.Context, userID string) (*store.InviteCode, error) {
	if s, ok := a.persistence.(store.InvitationStore); ok {
		item, err := s.UserInviteCode(ctx, userID)
		return item, mapInviteStoreError(err)
	}
	return nil, ErrUnavailable
}

func (a *App) ChangeUserInviteCode(ctx context.Context, userID, code string) (*store.InviteCode, error) {
	code = normalizeInviteCode(code)
	if !a.validCustomInviteCode(code) {
		return nil, ErrInvalid
	}
	if s, ok := a.persistence.(store.InvitationStore); ok {
		item, err := s.ChangeUserInviteCode(ctx, userID, code, time.Now().UTC())
		return item, mapInviteStoreError(err)
	}
	return nil, ErrUnavailable
}

func (a *App) AdminInviteCodes(ctx context.Context, query, status, cursor string, limit int) (store.InviteCodePage, error) {
	if status != "" && status != "active" && status != "disabled" && status != "retired" {
		return store.InviteCodePage{}, ErrInvalid
	}
	if s, ok := a.persistence.(store.InvitationStore); ok {
		return s.ListAdminInviteCodes(ctx, strings.TrimSpace(query), status, cursor, limit)
	}
	return store.InviteCodePage{}, ErrUnavailable
}

func parseInviteFilterTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func (a *App) AdminInviteRelations(ctx context.Context, query, method, from, to, cursor string, limit int) (store.InviteRelationPage, error) {
	if method != "" && method != "password" && method != "otp" {
		return store.InviteRelationPage{}, ErrInvalid
	}
	fromTime, fromErr := parseInviteFilterTime(from)
	toTime, toErr := parseInviteFilterTime(to)
	if fromErr != nil || toErr != nil || (!fromTime.IsZero() && !toTime.IsZero() && !fromTime.Before(toTime)) {
		return store.InviteRelationPage{}, ErrInvalid
	}
	if s, ok := a.persistence.(store.InvitationStore); ok {
		return s.ListAdminInviteRelations(ctx, strings.TrimSpace(query), method, from, to, cursor, limit)
	}
	return store.InviteRelationPage{}, ErrUnavailable
}
func (a *App) AdminSetInviteCodeStatus(ctx context.Context, actor, id, status, reason string) (*store.InviteCode, error) {
	if strings.TrimSpace(reason) == "" || len([]rune(reason)) > 500 {
		return nil, ErrInvalid
	}
	if s, ok := a.persistence.(store.InvitationStore); ok {
		item, err := s.SetAdminInviteCodeStatus(ctx, actor, id, status, strings.TrimSpace(reason), time.Now().UTC())
		return item, mapInviteStoreError(err)
	}
	return nil, ErrUnavailable
}
func (a *App) AdminResetInviteCode(ctx context.Context, actor, id, reason string) (*store.InviteCode, error) {
	if strings.TrimSpace(reason) == "" || len([]rune(reason)) > 500 {
		return nil, ErrInvalid
	}
	if s, ok := a.persistence.(store.InvitationStore); ok {
		item, err := s.ResetAdminInviteCode(ctx, actor, id, strings.TrimSpace(reason), time.Now().UTC())
		return item, mapInviteStoreError(err)
	}
	return nil, ErrUnavailable
}
