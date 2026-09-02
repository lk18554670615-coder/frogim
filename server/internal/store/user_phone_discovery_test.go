package store

import (
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/model"
)

func TestPhoneDiscoveryDefaultSchemaIsVersioned(t *testing.T) {
	if schemaVersion < 59 || !strings.Contains(normalizedSchema, "ALTER TABLE im_users ALTER COLUMN allow_search_by_phone SET DEFAULT true") {
		t.Fatal("new-user phone discovery default requires schema 59 migration")
	}
}

func TestPostgresPhoneDiscoveryDefaultsPreserveExistingChoices(t *testing.T) {
	for _, baseline := range []string{"fresh", "schema58"} {
		t.Run(baseline, func(t *testing.T) {
			p := newIsolatedWukongStore(t)
			ctx := t.Context()
			if baseline == "schema58" {
				// Reconstruct the previous default, with both explicit privacy choices.
				if _, err := p.pool.Exec(ctx, `
ALTER TABLE im_users ALTER COLUMN allow_search_by_phone SET DEFAULT false;
INSERT INTO im_users(id,phone,name,created_at) VALUES('legacy_disabled','13800000001','Legacy disabled',now());
INSERT INTO im_users(id,phone,name,allow_search_by_phone,created_at) VALUES('legacy_enabled','13800000002','Legacy enabled',true,now());
DELETE FROM im_schema_migrations WHERE version>=59;
INSERT INTO im_schema_migrations(version) VALUES(58) ON CONFLICT DO NOTHING;`); err != nil {
					t.Fatal(err)
				}
				if err := p.migrate(ctx); err != nil {
					t.Fatalf("upgrade schema 58: %v", err)
				}
				for uid, want := range map[string]bool{"legacy_disabled": false, "legacy_enabled": true} {
					user, err := p.GetUser(ctx, uid)
					if err != nil || user.AllowSearchByPhone != want {
						t.Fatalf("migration changed %s: %+v %v", uid, user, err)
					}
					user, err = p.LoginOrCreateUser(ctx, user.Phone, "Login", "unused", time.Now())
					if err != nil || user.ID != uid || user.AllowSearchByPhone != want {
						t.Fatalf("login changed %s: %+v %v", uid, user, err)
					}
				}
			}
			var version int
			if err := p.pool.QueryRow(ctx, `SELECT max(version) FROM im_schema_migrations`).Scan(&version); err != nil || version != schemaVersion {
				t.Fatalf("schema version=%d err=%v", version, err)
			}
			for _, method := range []string{"otp", "password", "admin"} {
				uid, phone := "new_"+method, "phone_"+method
				var user *model.User
				var err error
				switch method {
				case "otp":
					user, err = p.LoginOrCreateUser(ctx, phone, "New user", uid, time.Now())
				case "password":
					user, err = p.RegisterPasswordUser(ctx, phone, "New user", uid, "test-hash", time.Now())
				case "admin":
					user, err = p.CreateAdminPasswordUser(ctx, "admin", phone, "New user", uid, "test-hash", "unspecified", "test default", time.Now())
				}
				if err != nil || user == nil || !user.AllowSearchByPhone {
					t.Fatalf("%s default: %+v %v", method, user, err)
				}
				matches, err := p.SearchUsersByIdentifier(ctx, phone, "phone", 20)
				if err != nil || len(matches) != 1 || matches[0].ID != uid || matches[0].Phone != "" {
					t.Fatalf("new-user phone search: %+v %v", matches, err)
				}
				disabled := false
				if _, err = p.UpdateUserProfile(ctx, uid, UserProfileUpdate{AllowSearchByPhone: &disabled}); err != nil {
					t.Fatal(err)
				}
				user, err = p.LoginOrCreateUser(ctx, phone, "Must preserve opt-out", "unused", time.Now())
				if err != nil || user.ID != uid || user.AllowSearchByPhone {
					t.Fatalf("login reset opt-out: %+v %v", user, err)
				}
				if method != "otp" {
					user, _, err = p.PasswordCredentials(ctx, phone)
					if err != nil || user.AllowSearchByPhone {
						t.Fatalf("password credentials reset opt-out: %+v %v", user, err)
					}
				}
				matches, err = p.SearchUsersByIdentifier(ctx, phone, "phone", 20)
				if err != nil || len(matches) != 0 {
					t.Fatalf("opted-out user discoverable: %+v %v", matches, err)
				}
			}
			if err := p.migrate(ctx); err != nil {
				t.Fatalf("repeat migration: %v", err)
			}
			if _, err := p.pool.Exec(ctx, normalizedSchema); err != nil {
				t.Fatalf("reapply idempotent schema: %v", err)
			}
			for uid, want := range map[string]bool{"new_otp": false, "new_password": false, "new_admin": false} {
				user, err := p.GetUser(ctx, uid)
				if err != nil || user.AllowSearchByPhone != want {
					t.Fatalf("repeated schema reset opt-out: %+v %v", user, err)
				}
			}
		})
	}
}
