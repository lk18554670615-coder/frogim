package app

import (
	"context"
	"errors"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/linli/im/server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

var adminPermissionCatalog = map[string]struct{}{
	"users.write": {}, "groups.write": {}, "reports.write": {}, "rules.write": {}, "announcements.write": {},
	"settings.write": {}, "versions.write": {}, "content.write": {}, "channels.write": {}, "operations.write": {}, "support.write": {},
}

func (a *App) adminIdentities() (store.AdminIdentityStore, error) {
	s, ok := a.persistence.(store.AdminIdentityStore)
	if !ok {
		return nil, ErrUnavailable
	}
	return s, nil
}

func normalizeAdminEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 {
		return "", ErrInvalid
	}
	return email, nil
}

func validateAdminPassword(password string) error {
	if len(password) < 8 || len(password) > 128 {
		return ErrInvalid
	}
	return nil
}

func normalizeAdminPermissions(values []string) ([]string, error) {
	seen := map[string]struct{}{}
	permissions := make([]string, 0, len(values))
	for _, value := range values {
		permission := strings.TrimSpace(value)
		if _, ok := adminPermissionCatalog[permission]; !ok {
			return nil, ErrInvalid
		}
		if _, ok := seen[permission]; ok {
			continue
		}
		seen[permission] = struct{}{}
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	return permissions, nil
}

func (a *App) AuthenticateAdmin(ctx context.Context, email, password string) (*store.AdminAccount, error) {
	s, err := a.adminIdentities()
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeAdminEmail(email)
	if err != nil || password == "" {
		return nil, ErrForbidden
	}
	account, err := s.AdminAccountByEmail(ctx, normalized)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrForbidden
		}
		return nil, err
	}
	if account == nil || account.Status != "active" || bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(password)) != nil {
		return nil, ErrForbidden
	}
	return account, nil
}

func (a *App) AdminAccount(ctx context.Context, id string) (*store.AdminAccount, error) {
	s, err := a.adminIdentities()
	if err != nil {
		return nil, err
	}
	return s.AdminAccountByID(ctx, id)
}

func (a *App) RecordAdminLogin(ctx context.Context, id string, at time.Time) error {
	s, err := a.adminIdentities()
	if err != nil {
		return err
	}
	return s.RecordAdminAccountLogin(ctx, id, at)
}

func (a *App) AdminAccounts(ctx context.Context, query, status, cursor string, limit int) ([]*store.AdminAccount, int64, string, error) {
	s, err := a.adminIdentities()
	if err != nil {
		return nil, 0, "", err
	}
	return s.ListAdminAccounts(ctx, query, status, cursor, limit)
}

func (a *App) CreateAdminAccount(ctx context.Context, actorID, email, displayName, roleID, password string) (*store.AdminAccount, error) {
	s, err := a.adminIdentities()
	if err != nil {
		return nil, err
	}
	email, err = normalizeAdminEmail(email)
	displayName, roleID = strings.TrimSpace(displayName), strings.TrimSpace(roleID)
	if err != nil || displayName == "" || len(displayName) > 80 || roleID == "" || validateAdminPassword(password) != nil {
		return nil, ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}
	return s.CreateAdminAccount(ctx, store.AdminAccountCreate{ID: id("admin"), Email: email, DisplayName: displayName, PasswordHash: string(hash), RoleID: roleID, CreatedBy: actorID, At: time.Now().UTC()})
}

func (a *App) UpdateAdminAccount(ctx context.Context, actorID, accountID string, email, displayName, roleID, status *string) (*store.AdminAccount, error) {
	s, err := a.adminIdentities()
	if err != nil {
		return nil, err
	}
	if email != nil {
		normalized, normalizeErr := normalizeAdminEmail(*email)
		if normalizeErr != nil {
			return nil, ErrInvalid
		}
		email = &normalized
	}
	if displayName != nil {
		value := strings.TrimSpace(*displayName)
		if value == "" || len(value) > 80 {
			return nil, ErrInvalid
		}
		displayName = &value
	}
	if roleID != nil {
		value := strings.TrimSpace(*roleID)
		if value == "" {
			return nil, ErrInvalid
		}
		roleID = &value
	}
	if status != nil && *status != "active" && *status != "disabled" {
		return nil, ErrInvalid
	}
	return s.UpdateAdminAccount(ctx, store.AdminAccountUpdate{ID: accountID, ActorID: actorID, Email: email, DisplayName: displayName, RoleID: roleID, Status: status, At: time.Now().UTC()})
}

func (a *App) ResetAdminPassword(ctx context.Context, accountID, password string) error {
	if err := validateAdminPassword(password); err != nil {
		return err
	}
	s, err := a.adminIdentities()
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	return s.UpdateAdminAccountPassword(ctx, accountID, string(hash), time.Now().UTC())
}

func (a *App) ChangeAdminPassword(ctx context.Context, accountID, currentPassword, newPassword string) error {
	if err := validateAdminPassword(newPassword); err != nil || currentPassword == "" || currentPassword == newPassword {
		return ErrInvalid
	}
	s, err := a.adminIdentities()
	if err != nil {
		return err
	}
	account, err := s.AdminAccountByID(ctx, accountID)
	if err != nil {
		return err
	}
	if account.Status != "active" || bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(currentPassword)) != nil {
		return ErrForbidden
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if err != nil {
		return err
	}
	return s.UpdateAdminAccountPassword(ctx, accountID, string(hash), time.Now().UTC())
}

func (a *App) AdminRoles(ctx context.Context) ([]*store.AdminRole, error) {
	s, err := a.adminIdentities()
	if err != nil {
		return nil, err
	}
	return s.ListAdminRoles(ctx)
}

func (a *App) CreateAdminRole(ctx context.Context, actorID, name, description string, permissions []string) (*store.AdminRole, error) {
	s, err := a.adminIdentities()
	if err != nil {
		return nil, err
	}
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	permissions, err = normalizeAdminPermissions(permissions)
	if err != nil || len(name) < 2 || len(name) > 80 || len(description) > 500 {
		return nil, ErrInvalid
	}
	return s.CreateAdminRole(ctx, store.AdminRoleCreate{ID: id("admin_role"), Name: name, Description: description, CreatedBy: actorID, Permissions: permissions, At: time.Now().UTC()})
}

func (a *App) UpdateAdminRole(ctx context.Context, actorID, roleID, name, description string, permissions []string) (*store.AdminRole, error) {
	s, err := a.adminIdentities()
	if err != nil {
		return nil, err
	}
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	permissions, err = normalizeAdminPermissions(permissions)
	if err != nil || len(name) < 2 || len(name) > 80 || len(description) > 500 {
		return nil, ErrInvalid
	}
	return s.UpdateAdminRole(ctx, store.AdminRoleUpdate{ID: roleID, Name: name, Description: description, ActorID: actorID, Permissions: permissions, At: time.Now().UTC()})
}

func (a *App) DeleteAdminRole(ctx context.Context, roleID string) error {
	s, err := a.adminIdentities()
	if err != nil {
		return err
	}
	return s.DeleteAdminRole(ctx, roleID)
}

func IsAdminCredentialError(err error) bool {
	return errors.Is(err, ErrForbidden) || errors.Is(err, store.ErrNotFound)
}
