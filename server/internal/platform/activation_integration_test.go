package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
)

func TestPlatformPostgresActivationChecksAndIdempotence(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	if err := s.PutTenant(ctx, "new", "New", "https://new.example", "operator", "test", false); err != nil {
		t.Fatal(err)
	}
	report := func(_ context.Context, id, nonce string) (tenancy.Readiness, error) {
		return tenancy.Readiness{Nonce: nonce, TenantID: id, HTTPBaseURL: "https://new.example", SchemaVersion: 79, Checks: map[string]bool{"databaseBinding": true, "databaseAndCache": true, "im": true, "media": true, "calls": true}, Realm: &tenancy.RealmSnapshot{Version: 1, Enabled: true}}, nil
	}
	for _, broken := range []string{"identity", "url", "nonce", "schema", "media", "realm_missing", "realm_disabled", "realm_stale", "realm_inconsistent"} {
		err := s.ActivateTenant(ctx, "new", "operator", "test", true, 1, func(c context.Context, id, n string) (tenancy.Readiness, error) {
			r, _ := report(c, id, n)
			switch broken {
			case "identity":
				r.TenantID = "other"
			case "url":
				r.HTTPBaseURL = "https://other.example"
			case "nonce":
				r.Nonce = "old"
			case "schema":
				r.SchemaVersion = 72
			case "media":
				r.Checks["media"] = false
			case "realm_missing":
				r.Realm = nil
			case "realm_disabled":
				r.Realm.Enabled = false
			case "realm_stale":
				r.Realm.Version = 2
			case "realm_inconsistent":
				r.Realm.SuspensionConfirmed = true
			}
			return r, nil
		})
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s was accepted: %v", broken, err)
		}
	}
	if err := s.ActivateTenant(ctx, "new", "operator", "test", false, 1, report); !errors.Is(err, tenancy.ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.ActivateTenant(ctx, "new", "operator", "test", true, 2, report); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.ActivateTenant(ctx, "new", "operator", "test", true, 1, report); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_audits WHERE tenant_id='new' AND action='tenant.activated'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err := s.PutTenant(ctx, "racy", "Racy", "https://new2.example", "operator", "test", false); err != nil {
		t.Fatal(err)
	}
	err := s.ActivateTenant(ctx, "racy", "operator", "test", true, 1, func(c context.Context, id, n string) (tenancy.Readiness, error) {
		r, _ := report(c, id, n)
		r.HTTPBaseURL = "https://new2.example"
		_, err := s.pool.Exec(c, `UPDATE platform_tenants SET config_version=2 WHERE id='racy'`)
		return r, err
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatal("concurrent configuration accepted", err)
	}
	// An old readiness response must not activate after a pause/resume cycle,
	// even when the HTTP address and configuration revision are unchanged.
	if err := s.PutTenant(ctx, "realm-racy", "Realm race", "https://new3.example", "operator", "test", false); err != nil {
		t.Fatal(err)
	}
	err = s.ActivateTenant(ctx, "realm-racy", "operator", "test", true, 1, func(c context.Context, id, n string) (tenancy.Readiness, error) {
		r, _ := report(c, id, n)
		r.HTTPBaseURL = "https://new3.example"
		_, err := s.pool.Exec(c, `UPDATE platform_tenants SET access_version=3 WHERE id='realm-racy'`)
		return r, err
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatal("concurrent realm change accepted", err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_audits WHERE tenant_id='realm-racy' AND action='tenant.activated'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected activation produced a success audit", count, err)
	}
}
