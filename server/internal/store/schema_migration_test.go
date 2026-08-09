package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestDevicePreferenceColumnsAreUpgradedForExistingTables(t *testing.T) {
	if schemaVersion < 22 {
		t.Fatalf("device preference upgrade requires schema version 22 or newer, got %d", schemaVersion)
	}
	for _, column := range []string{
		"notifications_enabled",
		"preview_enabled",
		"sound_enabled",
		"vibration_enabled",
	} {
		statement := "ALTER TABLE im_devices ADD COLUMN IF NOT EXISTS " + column
		if !strings.Contains(normalizedSchema, statement) {
			t.Fatalf("migration does not upgrade existing im_devices.%s", column)
		}
	}
}

func TestMessageFanoutAndRetentionSchemaIsVersioned(t *testing.T) {
	if schemaVersion < 25 {
		t.Fatalf("performance schema requires version 25 or newer, got %d", schemaVersion)
	}
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS im_message_fanout",
		"im_message_fanout_pending_idx",
		"im_push_outbox_retention_idx",
		"im_event_outbox_retention_idx",
		"CREATE EXTENSION IF NOT EXISTS pg_stat_statements",
		"ALTER TABLE im_conversations ADD COLUMN IF NOT EXISTS member_count",
		"CREATE TRIGGER im_members_count_insert",
	} {
		if !strings.Contains(normalizedSchema, statement) {
			t.Fatalf("runtime schema is missing %q", statement)
		}
	}
}

func TestPostgresMigratesLegacyDevicePreferenceColumns(t *testing.T) {
	databaseURL := os.Getenv("IM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("device_migration_%d", time.Now().UnixNano())
	if _, err = adminConn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		adminConn.Close(ctx)
		t.Fatal(err)
	}
	adminConn.Close(ctx)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cleanupConn, cleanupErr := pgx.Connect(cleanupCtx, databaseURL)
		if cleanupErr != nil {
			t.Errorf("connect for schema cleanup: %v", cleanupErr)
			return
		}
		defer cleanupConn.Close(cleanupCtx)
		if _, cleanupErr = cleanupConn.Exec(cleanupCtx, `DROP SCHEMA IF EXISTS `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); cleanupErr != nil {
			t.Errorf("drop migration test schema: %v", cleanupErr)
		}
	})
	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}
	isolatedURL := databaseURL + separator + "search_path=" + schema + ",public"
	conn, err := pgx.Connect(ctx, isolatedURL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `
		CREATE TABLE im_schema_migrations(version integer PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now());
		INSERT INTO im_schema_migrations(version) VALUES(21);
		CREATE TABLE im_users(id text PRIMARY KEY,phone text NOT NULL UNIQUE,name text NOT NULL,banned boolean NOT NULL DEFAULT false,created_at timestamptz NOT NULL);
		CREATE TABLE im_devices(id text PRIMARY KEY,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,platform text NOT NULL,provider text NOT NULL,push_token text NOT NULL,updated_at timestamptz NOT NULL DEFAULT now(),UNIQUE(provider,push_token));
	`)
	if err != nil {
		conn.Close(ctx)
		t.Fatal(err)
	}
	conn.Close(ctx)

	repo, err := NewPostgres(ctx, isolatedURL)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	for _, column := range []string{"notifications_enabled", "preview_enabled", "sound_enabled", "vibration_enabled"} {
		var exists bool
		if err = repo.pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM information_schema.columns
			WHERE table_schema='public' AND table_name='im_devices' AND column_name=$1
		)`, column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("legacy im_devices was not upgraded with %s", column)
		}
	}
}
