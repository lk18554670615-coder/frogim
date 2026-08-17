package teststore

import (
	"context"
	"time"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// Memory is a minimal business-state fixture for isolated tests. It is kept
// outside the production store package and deliberately has no message engine.
type Memory struct{}

var testAdminPasswordHash = func() string {
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), bcrypt.MinCost)
	return string(hash)
}()

func (Memory) Load(context.Context) (*model.State, error) { return model.NewState(), nil }
func (Memory) Save(context.Context, *model.State) error   { return nil }
func (Memory) Ping(context.Context) error                 { return nil }
func (Memory) Close()                                     {}

func testAdminAccount() *store.AdminAccount {
	return &store.AdminAccount{
		ID:           "test-admin",
		Email:        "admin@example.com",
		DisplayName:  "Test Admin",
		PasswordHash: testAdminPasswordHash,
		RoleID:       "platform_admin",
		RoleName:     "平台管理员",
		Status:       "active",
		AuthVersion:  1,
		Permissions: []string{
			"platform.write", "users.write", "groups.write", "messages.write",
			"reports.write", "content.write", "operations.write", "support.write",
			"infrastructure.write", "settings.write", "audits.write",
		},
	}
}

// The fixed administrator keeps isolated HTTP tests focused on the endpoint
// under test while production always resolves the identity from PostgreSQL.
func (Memory) AdminAccountByEmail(_ context.Context, email string) (*store.AdminAccount, error) {
	account := testAdminAccount()
	if email != account.Email {
		return nil, store.ErrNotFound
	}
	return account, nil
}

func (Memory) AdminAccountByID(_ context.Context, id string) (*store.AdminAccount, error) {
	if id == "support-1" {
		return &store.AdminAccount{
			ID: "support-1", Email: "support@example.com", DisplayName: "Support",
			RoleID: "support", RoleName: "支持人员", Status: "active", AuthVersion: 1,
		}, nil
	}
	account := testAdminAccount()
	if id != account.ID {
		return nil, store.ErrNotFound
	}
	return account, nil
}

func (Memory) ListAdminAccounts(context.Context, string, string, string, int) ([]*store.AdminAccount, int64, string, error) {
	return []*store.AdminAccount{testAdminAccount()}, 1, "", nil
}

func (Memory) CreateAdminAccount(context.Context, store.AdminAccountCreate) (*store.AdminAccount, error) {
	return nil, store.ErrUnsupported
}

func (Memory) UpdateAdminAccount(context.Context, store.AdminAccountUpdate) (*store.AdminAccount, error) {
	return nil, store.ErrUnsupported
}

func (Memory) UpdateAdminAccountPassword(context.Context, string, string, time.Time) error {
	return store.ErrUnsupported
}

func (Memory) RecordAdminAccountLogin(context.Context, string, time.Time) error { return nil }

func (Memory) ListAdminRoles(context.Context) ([]*store.AdminRole, error) {
	return []*store.AdminRole{{
		ID:          "platform_admin",
		Name:        "平台管理员",
		Description: "Test administrator",
		BuiltIn:     true,
		Permissions: append([]string(nil), testAdminAccount().Permissions...),
	}}, nil
}

func (Memory) CreateAdminRole(context.Context, store.AdminRoleCreate) (*store.AdminRole, error) {
	return nil, store.ErrUnsupported
}

func (Memory) UpdateAdminRole(context.Context, store.AdminRoleUpdate) (*store.AdminRole, error) {
	return nil, store.ErrUnsupported
}

func (Memory) DeleteAdminRole(context.Context, string) error { return store.ErrUnsupported }
