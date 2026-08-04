package store

import (
	"context"
	"os"
	"strings"
	"testing"

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

func TestPostgresMigratesLegacyDevicePreferenceColumns(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
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

	repo, err := NewPostgres(ctx, url)
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
