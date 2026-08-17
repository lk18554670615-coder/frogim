package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

func TestAdminIdentitySchemaIsVersioned(t *testing.T) {
	if schemaVersion != 53 {
		t.Fatalf("admin identity migration must be schema version 53, got %d", schemaVersion)
	}
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS im_admin_accounts",
		"CREATE TABLE IF NOT EXISTS im_admin_roles",
		"CREATE TABLE IF NOT EXISTS im_admin_role_permissions",
		"im_admin_accounts_email_unique_idx",
		"('platform_admin','平台管理员'",
		"('support','只读支持'",
	} {
		if !strings.Contains(normalizedSchema, statement) {
			t.Fatalf("administrator schema is missing %q", statement)
		}
	}
}

func TestPostgresAdminBootstrapRolesAndProtections(t *testing.T) {
	databaseURL := os.Getenv("IM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("admin_identity_%d", time.Now().UnixNano())
	if _, err = adminConn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		adminConn.Close(ctx)
		t.Fatal(err)
	}
	adminConn.Close(ctx)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, cleanupErr := pgx.Connect(cleanupCtx, databaseURL)
		if cleanupErr != nil {
			t.Errorf("connect for administrator schema cleanup: %v", cleanupErr)
			return
		}
		defer conn.Close(cleanupCtx)
		if _, cleanupErr = conn.Exec(cleanupCtx, `DROP SCHEMA IF EXISTS `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); cleanupErr != nil {
			t.Errorf("drop administrator test schema: %v", cleanupErr)
		}
	})
	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}
	repository, err := NewPostgres(ctx, databaseURL+separator+"search_path="+schema+",public")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	roles, err := repository.ListAdminRoles(ctx)
	if err != nil || len(roles) != 6 {
		t.Fatalf("built-in roles=%d err=%v", len(roles), err)
	}
	var platformRole *AdminRole
	for _, role := range roles {
		if role.ID == "platform_admin" {
			platformRole = role
			break
		}
	}
	if platformRole == nil || !platformRole.BuiltIn || len(platformRole.Permissions) != 11 {
		t.Fatalf("platform role=%+v", platformRole)
	}
	if _, err = repository.BootstrapAdmin(ctx, AdminAccountCreate{}); err == nil || !strings.Contains(err.Error(), "bootstrap credentials are incomplete") {
		t.Fatalf("empty bootstrap err=%v", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("InitialPassword123!"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.BootstrapAdmin(ctx, AdminAccountCreate{ID: "bad_email", Email: "not-an-email", PasswordHash: string(hash), RoleID: "platform_admin", At: time.Now().UTC()}); err == nil {
		t.Fatal("bootstrap accepted an invalid administrator email")
	}
	at := time.Now().UTC().Truncate(time.Second)
	created, err := repository.BootstrapAdmin(ctx, AdminAccountCreate{ID: "admin_root", Email: "Root@Example.com", DisplayName: "Root Admin", PasswordHash: string(hash), RoleID: "platform_admin", At: at})
	if err != nil || !created {
		t.Fatalf("bootstrap created=%v err=%v", created, err)
	}
	created, err = repository.BootstrapAdmin(ctx, AdminAccountCreate{ID: "admin_replacement", Email: "replacement@example.com", PasswordHash: string(hash), RoleID: "support", At: at})
	if err != nil || created {
		t.Fatalf("second bootstrap created=%v err=%v", created, err)
	}
	root, err := repository.AdminAccountByEmail(ctx, "ROOT@example.com")
	if err != nil || root.ID != "admin_root" || root.Email != "root@example.com" || root.AuthVersion != 1 {
		t.Fatalf("root=%+v err=%v", root, err)
	}
	if _, err = repository.CreateAdminAccount(ctx, AdminAccountCreate{ID: "admin_duplicate", Email: "ROOT@EXAMPLE.COM", DisplayName: "Duplicate", PasswordHash: string(hash), RoleID: "support", CreatedBy: root.ID, At: at}); !errors.Is(err, ErrConflict) {
		t.Fatalf("case-insensitive duplicate err=%v", err)
	}
	if _, err = repository.CreateAdminAccount(ctx, AdminAccountCreate{ID: "admin_missing_role", Email: "missing-role@example.com", DisplayName: "Missing Role", PasswordHash: string(hash), RoleID: "missing_role", CreatedBy: root.ID, At: at}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing role err=%v", err)
	}

	custom, err := repository.CreateAdminRole(ctx, AdminRoleCreate{ID: "role_custom", Name: "值班运营", Description: "测试自定义角色", CreatedBy: root.ID, Permissions: []string{"users.write"}, At: at})
	if err != nil || custom.BuiltIn || len(custom.Permissions) != 1 {
		t.Fatalf("custom role=%+v err=%v", custom, err)
	}
	if _, err = repository.UpdateAdminRole(ctx, AdminRoleUpdate{ID: "support", Name: "Changed", ActorID: root.ID, At: at}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("built-in role update err=%v", err)
	}
	customAdmin, err := repository.CreateAdminAccount(ctx, AdminAccountCreate{ID: "admin_custom", Email: "custom@example.com", DisplayName: "Custom Admin", PasswordHash: string(hash), RoleID: custom.ID, CreatedBy: root.ID, At: at})
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.DeleteAdminRole(ctx, custom.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("assigned role delete err=%v", err)
	}
	custom, err = repository.UpdateAdminRole(ctx, AdminRoleUpdate{ID: custom.ID, Name: custom.Name, Description: custom.Description, ActorID: root.ID, Permissions: []string{"groups.write", "users.write"}, At: at.Add(time.Second)})
	if err != nil || len(custom.Permissions) != 2 {
		t.Fatalf("updated custom role=%+v err=%v", custom, err)
	}
	customAdmin, err = repository.AdminAccountByID(ctx, customAdmin.ID)
	if err != nil || len(customAdmin.Permissions) != 2 {
		t.Fatalf("realtime custom permissions=%+v err=%v", customAdmin, err)
	}
	updatedEmail, updatedName := "custom-updated@example.com", "Updated Custom Admin"
	customAdmin, err = repository.UpdateAdminAccount(ctx, AdminAccountUpdate{ID: customAdmin.ID, ActorID: root.ID, Email: &updatedEmail, DisplayName: &updatedName, At: at.Add(time.Second)})
	if err != nil || customAdmin.Email != updatedEmail || customAdmin.DisplayName != updatedName || customAdmin.AuthVersion != 2 {
		t.Fatalf("updated administrator=%+v err=%v", customAdmin, err)
	}
	statusDisabled := "disabled"
	if _, err = repository.UpdateAdminAccount(ctx, AdminAccountUpdate{ID: root.ID, ActorID: root.ID, Status: &statusDisabled, At: at}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("self disable err=%v", err)
	}
	customRoleID := custom.ID
	if _, err = repository.UpdateAdminAccount(ctx, AdminAccountUpdate{ID: root.ID, ActorID: "another_admin", RoleID: &customRoleID, At: at}); !errors.Is(err, ErrConflict) {
		t.Fatalf("last platform administrator downgrade err=%v", err)
	}
	secondPlatform, err := repository.CreateAdminAccount(ctx, AdminAccountCreate{ID: "admin_platform_2", Email: "platform2@example.com", DisplayName: "Second Platform", PasswordHash: string(hash), RoleID: "platform_admin", CreatedBy: root.ID, At: at})
	if err != nil {
		t.Fatal(err)
	}
	root, err = repository.UpdateAdminAccount(ctx, AdminAccountUpdate{ID: root.ID, ActorID: secondPlatform.ID, RoleID: &customRoleID, At: at.Add(2 * time.Second)})
	if err != nil || root.RoleID != custom.ID || root.AuthVersion != 2 {
		t.Fatalf("root role update=%+v err=%v", root, err)
	}
	beforeVersion := customAdmin.AuthVersion
	if err = repository.UpdateAdminAccountPassword(ctx, customAdmin.ID, string(hash), at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	customAdmin, err = repository.AdminAccountByID(ctx, customAdmin.ID)
	if err != nil || customAdmin.AuthVersion != beforeVersion+1 {
		t.Fatalf("password auth version=%+v err=%v", customAdmin, err)
	}
	if _, err = repository.UpdateAdminAccount(ctx, AdminAccountUpdate{ID: secondPlatform.ID, ActorID: root.ID, Status: &statusDisabled, At: at.Add(4 * time.Second)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("last active platform administrator disable err=%v", err)
	}
}
