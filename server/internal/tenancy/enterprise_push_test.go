package tenancy

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/push"
	"github.com/linli/im/server/internal/store"
)

type countedPush struct{ sends int }

func (p *countedPush) Send(context.Context, store.OutboxItem) error {
	p.sends++
	return nil
}

func TestEnterprisePushGuard(t *testing.T) {
	dsn := os.Getenv("TEST_LIGHT_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_LIGHT_DATABASE_URL to an isolated local PostgreSQL instance")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	name := "lp_push_test_" + ID()[:16]
	if _, err = base.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Close()
		if _, err := base.Exec(ctx, `DROP DATABASE `+name); err != nil {
			t.Errorf("drop isolated push test database: %v", err)
		}
	}()
	if _, err = db.Exec(ctx, `CREATE TABLE lp_identity(user_id text PRIMARY KEY,active boolean NOT NULL,epoch bigint NOT NULL); INSERT INTO lp_identity VALUES('fixture',true,4)`); err != nil {
		t.Fatal(err)
	}
	vendor := &countedPush{}
	guard := push.EnterpriseProvider{Provider: vendor, DB: db, TenantID: "enterprise-a"}
	item := store.OutboxItem{UserID: "fixture", Payload: map[string]any{"tenantId": "enterprise-a", "enterpriseEpoch": float64(4)}}
	if err = guard.Send(ctx, item); err != nil || vendor.sends != 1 {
		t.Fatalf("current enterprise delivery: sends=%d err=%v", vendor.sends, err)
	}
	for _, payload := range []map[string]any{
		{"tenantId": "enterprise-b", "enterpriseEpoch": float64(4)},
		{"tenantId": "enterprise-a", "enterpriseEpoch": float64(3)},
		{"tenantId": "enterprise-a"},
	} {
		item.Payload = payload
		var delivery *push.DeliveryError
		if err = guard.Send(ctx, item); !errors.As(err, &delivery) || !delivery.Permanent() || vendor.sends != 1 {
			t.Fatalf("stale scope reached provider: sends=%d err=%v", vendor.sends, err)
		}
	}
	item.Payload = map[string]any{"tenantId": "enterprise-a", "enterpriseEpoch": int64(4)}
	if _, err = db.Exec(ctx, `UPDATE lp_identity SET active=false,epoch=5 WHERE user_id='fixture'`); err != nil {
		t.Fatal(err)
	}
	var delivery *push.DeliveryError
	if err = guard.Send(ctx, item); !errors.As(err, &delivery) || !delivery.Permanent() || vendor.sends != 1 {
		t.Fatalf("offline source reached provider: sends=%d err=%v", vendor.sends, err)
	}
	db.Close()
	if err = guard.Send(ctx, item); !errors.As(err, &delivery) || !delivery.Retryable || vendor.sends != 1 {
		t.Fatalf("unknown database state sent or discarded: sends=%d err=%v", vendor.sends, err)
	}
}
