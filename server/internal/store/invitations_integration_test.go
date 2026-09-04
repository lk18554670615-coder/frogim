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
)

func TestInvitationPostgresLifecycle(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := t.Context()
	schema := fmt.Sprintf("invitations_%d", time.Now().UnixNano())
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err = conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
	separator := "?"
	if strings.Contains(url, "?") {
		separator = "&"
	}
	p, err := NewPostgres(ctx, url+separator+"search_path="+schema+",public")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if schemaVersion != 63 {
		t.Fatalf("schema version=%d", schemaVersion)
	}

	// Simulate a pre-63 user and verify that the migration backfill is
	// repeatable and creates exactly one current code.
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES('legacy','13900000001','legacy',now())`); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `DELETE FROM im_schema_migrations WHERE version=63; INSERT INTO im_schema_migrations(version) VALUES(62) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err = p.migrate(ctx); err != nil {
		t.Fatal("upgrade migration", err)
	}
	if err = p.migrate(ctx); err != nil {
		t.Fatal("repeat migration", err)
	}
	var legacyCodes int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_user_invite_codes WHERE user_id='legacy' AND status IN ('active','disabled')`).Scan(&legacyCodes); err != nil || legacyCodes != 1 {
		t.Fatalf("legacy current codes=%d err=%v", legacyCodes, err)
	}
	legacyCode, err := p.UserInviteCode(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.SetAdminInviteCodeStatus(ctx, "admin", legacyCode.ID, "disabled", "test disabled ownership", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ChangeUserInviteCode(ctx, "legacy", "LEGACY_88", time.Now().UTC()); !errors.Is(err, ErrInviteDisabled) {
		t.Fatalf("disabled code self change error=%v", err)
	}

	at := time.Now().UTC().Truncate(time.Microsecond)
	inviter, err := p.RegisterPasswordUserWithInvite(ctx, "13900000002", "inviter", "inviter", "hash", at, "optional", "")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := p.UserInviteCode(ctx, inviter.ID)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := p.ValidateInviteCode(ctx, strings.ToLower(profile.Code))
	if err != nil || !valid {
		t.Fatalf("case-insensitive validation=%v err=%v", valid, err)
	}

	invitee, err := p.RegisterPasswordUserWithInvite(ctx, "13900000003", "invitee", "invitee", "hash", at.Add(time.Second), "required", profile.Code)
	if err != nil {
		t.Fatal(err)
	}
	var relationCount int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_user_invite_relations WHERE invitee_user_id=$1 AND inviter_user_id=$2 AND invite_code_id=$3`, invitee.ID, inviter.ID, profile.ID).Scan(&relationCount); err != nil || relationCount != 1 {
		t.Fatalf("relation count=%d err=%v", relationCount, err)
	}
	if _, err = p.RegisterPasswordUserWithInvite(ctx, "13900000003", "duplicate", "duplicate", "hash", at, "optional", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate registration error=%v", err)
	}

	custom, err := p.ChangeUserInviteCode(ctx, inviter.ID, "MY-CODE_88", at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if valid, err = p.ValidateInviteCode(ctx, profile.Code); err != nil || valid {
		t.Fatalf("retired code validation=%v err=%v", valid, err)
	}
	if _, err = p.ChangeUserInviteCode(ctx, inviter.ID, "SECOND88", at.Add(3*time.Second)); !errors.Is(err, ErrInviteChangeUsed) {
		t.Fatalf("second self change error=%v", err)
	}

	if _, err = p.SetAdminInviteCodeStatus(ctx, "admin", custom.ID, "disabled", "test disable", at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if valid, err = p.ValidateInviteCode(ctx, custom.Code); err != nil || valid {
		t.Fatalf("disabled code validation=%v err=%v", valid, err)
	}
	if _, err = p.ChangeUserInviteCode(ctx, inviter.ID, "BYPASS88", at.Add(5*time.Second)); !errors.Is(err, ErrInviteChangeUsed) {
		t.Fatalf("used quota must remain authoritative: %v", err)
	}
	if _, err = p.SetAdminInviteCodeStatus(ctx, "admin", custom.ID, "active", "test enable", at.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	if valid, err = p.ValidateInviteCode(ctx, custom.Code); err != nil || !valid {
		t.Fatalf("re-enabled code validation=%v err=%v", valid, err)
	}

	reset, err := p.ResetAdminInviteCode(ctx, "admin", custom.ID, "test reset", at.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if valid, err = p.ValidateInviteCode(ctx, custom.Code); err != nil || valid {
		t.Fatalf("reset old code validation=%v err=%v", valid, err)
	}
	if valid, err = p.ValidateInviteCode(ctx, reset.Code); err != nil || !valid {
		t.Fatalf("reset new code validation=%v err=%v", valid, err)
	}
	profile, err = p.UserInviteCode(ctx, inviter.ID)
	if err != nil || profile.SelfChangesUsed != 1 || profile.SelfChangesRemaining != 0 {
		t.Fatalf("admin reset altered self-change quota: %+v err=%v", profile, err)
	}
}
