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

func TestRetentionSchemaIsVersioned(t *testing.T) {
	if schemaVersion < 25 {
		t.Fatalf("performance schema requires version 25 or newer, got %d", schemaVersion)
	}
	for _, statement := range []string{
		"im_push_outbox_retention_idx",
		"CREATE EXTENSION IF NOT EXISTS pg_stat_statements",
		"ALTER TABLE im_conversations ADD COLUMN IF NOT EXISTS member_count",
		"CREATE TRIGGER im_members_count_insert",
	} {
		if !strings.Contains(normalizedSchema, statement) {
			t.Fatalf("runtime schema is missing %q", statement)
		}
	}
}

func TestWukongSchemaIsVersioned(t *testing.T) {
	if schemaVersion < 30 {
		t.Fatalf("WuKongIM schema requires version 28 or newer, got %d", schemaVersion)
	}
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS im_wukong_webhook_events",
		"im_wukong_webhook_pending_idx",
		"CREATE TABLE IF NOT EXISTS im_wukong_outbox",
		"im_wukong_outbox_pending_idx",
		"CREATE TABLE IF NOT EXISTS im_wukong_message_extensions",
		"CREATE TABLE IF NOT EXISTS im_wukong_message_index",
		"im_wukong_message_index_conversation_idx",
		"ALTER TABLE im_wukong_message_index ADD COLUMN IF NOT EXISTS expired_at",
		"im_wukong_message_index_expiry_idx",
		"ALTER TABLE im_scheduled_messages DROP CONSTRAINT IF EXISTS im_scheduled_messages_sent_message_id_fkey",
		"ALTER TABLE im_message_reactions DROP CONSTRAINT IF EXISTS im_message_reactions_message_id_fkey",
		"ALTER TABLE im_group_message_pins DROP CONSTRAINT IF EXISTS im_group_message_pins_message_id_fkey",
		"CREATE TABLE IF NOT EXISTS im_wukong_media_channels",
		"im_wukong_media_channels_channel_idx",
		"CREATE TABLE IF NOT EXISTS im_wukong_credentials",
		"CREATE TABLE IF NOT EXISTS im_wukong_reminders",
		"im_wukong_reminders_user_version_idx",
	} {
		if !strings.Contains(normalizedSchema, statement) {
			t.Fatalf("WuKongIM schema is missing %q", statement)
		}
	}
}

func TestWukongMessageExpirySchemaIsVersioned(t *testing.T) {
	if schemaVersion < 43 {
		t.Fatalf("WuKong message expiry requires schema version 43 or newer, got %d", schemaVersion)
	}
	for _, statement := range []string{
		"ALTER TABLE im_wukong_message_index ADD COLUMN IF NOT EXISTS media_id",
		"ALTER TABLE im_wukong_message_index ADD COLUMN IF NOT EXISTS expires_at",
		"ALTER TABLE im_wukong_message_index ADD COLUMN IF NOT EXISTS expired_at",
		"CREATE INDEX IF NOT EXISTS im_wukong_message_index_expiry_idx",
	} {
		if !strings.Contains(normalizedSchema, statement) {
			t.Fatalf("WuKong message expiry schema is missing %q", statement)
		}
	}
}

func TestWukongWebhookProjectionSchemaIsVersioned(t *testing.T) {
	if schemaVersion < 44 {
		t.Fatalf("WuKong webhook projections require schema version 44 or newer, got %d", schemaVersion)
	}
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS im_wukong_presence",
		"im_wukong_presence_online_idx",
	} {
		if !strings.Contains(normalizedSchema, statement) {
			t.Fatalf("WuKong webhook projection schema is missing %q", statement)
		}
	}
}

func TestWukongSystemAccountsSchemaIsVersioned(t *testing.T) {
	if schemaVersion < 45 {
		t.Fatalf("WuKong system accounts require schema version 45 or newer, got %d", schemaVersion)
	}
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS im_wukong_system_users",
		"im_wukong_system_users_enabled_idx",
	} {
		if !strings.Contains(normalizedSchema, fragment) {
			t.Fatalf("WuKong system account schema is missing %q", fragment)
		}
	}
}

func TestLegacyMessagePayloadTableIsRemovedAtSchemaVersion46(t *testing.T) {
	if schemaVersion < 46 {
		t.Fatalf("legacy message payload removal requires schema version 46 or newer, got %d", schemaVersion)
	}
	if !strings.Contains(normalizedSchema, "DROP TABLE IF EXISTS im_messages") {
		t.Fatal("legacy im_messages cleanup is missing")
	}
	if strings.Contains(normalizedSchema, "CREATE TABLE IF NOT EXISTS im_messages") {
		t.Fatal("legacy im_messages payload table is recreated by normalized schema")
	}
	if !strings.Contains(normalizedSchema, "DROP TABLE IF EXISTS im_message_fanout") || strings.Contains(normalizedSchema, "CREATE TABLE IF NOT EXISTS im_message_fanout") {
		t.Fatal("legacy message fanout table is not fully retired")
	}
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS im_message_edits(message_id text NOT NULL,",
		"CREATE TABLE IF NOT EXISTS im_message_reactions(message_id text NOT NULL,",
		"CREATE TABLE IF NOT EXISTS im_group_message_pins(conversation_id text NOT NULL REFERENCES im_groups(conversation_id) ON DELETE CASCADE,message_id text NOT NULL,",
		"CREATE TABLE IF NOT EXISTS im_favorites(user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,message_id text NOT NULL,",
	} {
		if !strings.Contains(normalizedSchema, fragment) {
			t.Fatalf("WuKong business extension is not independent of the retired payload table: %q", fragment)
		}
	}
}

func TestTemporaryBusinessMembershipSchemaIsVersioned(t *testing.T) {
	if schemaVersion < 39 {
		t.Fatalf("temporary business membership requires schema version 39 or newer, got %d", schemaVersion)
	}
	for _, statement := range []string{
		"ALTER TABLE im_members ADD COLUMN IF NOT EXISTS expires_at",
		"CREATE INDEX IF NOT EXISTS im_members_expiry_idx",
	} {
		if !strings.Contains(normalizedSchema, statement) {
			t.Fatalf("temporary membership schema is missing %q", statement)
		}
	}
}

func TestSignedPluginLifecycleSchemaIsVersioned(t *testing.T) {
	if schemaVersion < 40 {
		t.Fatalf("signed plugin lifecycle requires schema version 40 or newer, got %d", schemaVersion)
	}
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS im_wukong_plugin_releases",
		"im_wukong_plugin_releases_status_idx",
		"CREATE TABLE IF NOT EXISTS im_wukong_plugin_events",
		"im_wukong_plugin_events_plugin_idx",
	} {
		if !strings.Contains(normalizedSchema, statement) {
			t.Fatalf("signed plugin lifecycle schema is missing %q", statement)
		}
	}
}

func TestGroupCallParticipantSchemaIsVersioned(t *testing.T) {
	if schemaVersion < 41 {
		t.Fatalf("group calls require schema version 41 or newer, got %d", schemaVersion)
	}
	for _, statement := range []string{
		"call_kind text",
		"participant_ids text[]",
		"joined_user_ids text[]",
		"declined_user_ids text[]",
		"left_user_ids text[]",
		"ALTER TABLE im_call_sessions ALTER COLUMN callee_id DROP NOT NULL",
	} {
		if !strings.Contains(normalizedSchema, statement) {
			t.Fatalf("group call schema is missing %q", statement)
		}
	}
}

func TestLegacySyncTablesAreRemovedAtSchemaVersion42(t *testing.T) {
	if schemaVersion < 42 {
		t.Fatalf("legacy sync removal requires schema version 42 or newer, got %d", schemaVersion)
	}
	for _, statement := range []string{
		"DROP TABLE IF EXISTS im_sync_events",
		"DROP TABLE IF EXISTS im_user_cursors",
	} {
		if !strings.Contains(normalizedSchema, statement) {
			t.Fatalf("legacy sync cleanup is missing %q", statement)
		}
	}
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS im_sync_events",
		"CREATE TABLE IF NOT EXISTS im_user_cursors",
	} {
		if strings.Contains(normalizedSchema, statement) {
			t.Fatalf("legacy sync table was recreated by normalized schema: %q", statement)
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

func TestPostgresSchema44ScrubsLegacyWebhookPayloads(t *testing.T) {
	databaseURL := os.Getenv("IM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("webhook_migration_%d", time.Now().UnixNano())
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
			t.Errorf("connect for webhook schema cleanup: %v", cleanupErr)
			return
		}
		defer cleanupConn.Close(cleanupCtx)
		if _, cleanupErr = cleanupConn.Exec(cleanupCtx, `DROP SCHEMA IF EXISTS `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); cleanupErr != nil {
			t.Errorf("drop webhook migration schema: %v", cleanupErr)
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
		INSERT INTO im_schema_migrations(version) VALUES(43);
		CREATE TABLE im_wukong_webhook_events(
		 id text PRIMARY KEY,event_type text NOT NULL,payload jsonb NOT NULL,
		 status text NOT NULL DEFAULT 'pending',attempts integer NOT NULL DEFAULT 0,
		 available_at timestamptz NOT NULL DEFAULT now(),locked_at timestamptz,
		 last_error text NOT NULL DEFAULT '',received_at timestamptz NOT NULL,completed_at timestamptz
		);
		INSERT INTO im_wukong_webhook_events(id,event_type,payload,status,received_at)
		VALUES('legacy-offline','msg.offline','{"payload":"offline secret"}','pending',now()-interval '10 minutes');
		INSERT INTO im_wukong_webhook_events(id,event_type,payload,status,received_at,completed_at)
		VALUES('legacy-notify','msg.notify','{"payload":"message secret"}','completed',now()-interval '10 minutes',now());
	`)
	if err != nil {
		conn.Close(ctx)
		t.Fatal(err)
	}
	conn.Close(ctx)

	repository, err := NewPostgres(ctx, isolatedURL)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	var version int
	if err = repository.pool.QueryRow(ctx, `SELECT max(version) FROM im_schema_migrations`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	rows, err := repository.pool.Query(ctx, `SELECT id,status,payload::text FROM im_wukong_webhook_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var id, status, payload string
		if err = rows.Scan(&id, &status, &payload); err != nil {
			t.Fatal(err)
		}
		if status != "completed" || payload != "{}" {
			t.Fatalf("legacy webhook id=%s status=%s payload=%s", id, status, payload)
		}
		seen++
	}
	if err = rows.Err(); err != nil || seen != 2 {
		t.Fatalf("legacy webhook rows=%d err=%v", seen, err)
	}
}
