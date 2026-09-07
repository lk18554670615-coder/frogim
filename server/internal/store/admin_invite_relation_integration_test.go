package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func inviteRelationTestStore(t *testing.T) *Postgres {
	t.Helper()
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := t.Context()
	schema := fmt.Sprintf("invite_rebind_%d", time.Now().UnixNano())
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		conn.Close(context.Background())
	})
	separator := "?"
	if strings.Contains(url, "?") {
		separator = "&"
	}
	p, err := NewPostgres(ctx, url+separator+"search_path="+schema+",public")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func inviteRelationUser(t *testing.T, p *Postgres, id string, n int, invitedBy string) *InviteCode {
	t.Helper()
	_, err := p.RegisterPasswordUserWithInvite(t.Context(), fmt.Sprintf("139%08d", n), id, id, "test-hash", time.Now().UTC().Add(-time.Hour), "optional", invitedBy)
	if err != nil {
		t.Fatal(err)
	}
	code, err := p.UserInviteCode(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestAdminInviteRelationLifecycle(t *testing.T) {
	p := inviteRelationTestStore(t)
	ctx := t.Context()
	at := time.Now().UTC().Truncate(time.Microsecond)
	a := inviteRelationUser(t, p, "a", 1, "")
	b := inviteRelationUser(t, p, "b", 2, "")
	c := inviteRelationUser(t, p, "c", 3, a.Code)
	bind, err := p.SetAdminInviteRelation(ctx, "admin", "b", " "+strings.ToLower(a.Code)+" ", "补绑", 0, at)
	if err != nil || bind.InviterID != "a" || bind.Version != 1 || bind.RegistrationMethod != "admin" || bind.BindingSource != "admin" {
		t.Fatalf("supplement: %+v %v", bind, err)
	}
	again, err := p.SetAdminInviteRelation(ctx, "admin", "b", a.Code, "retry", 0, at.Add(time.Second))
	if err != nil || again.Version != 1 {
		t.Fatalf("idempotence: %+v %v", again, err)
	}
	_, err = p.SetAdminInviteRelation(ctx, "admin", "a", b.Code, "cycle", 0, at)
	if !errors.Is(err, ErrInviteRelationCycle) {
		t.Fatalf("cycle: %v", err)
	}
	_, err = p.SetAdminInviteRelation(ctx, "admin", "b", b.Code, "self", 1, at)
	if !errors.Is(err, ErrInviteRelationCycle) {
		t.Fatalf("self: %v", err)
	}
	bind, err = p.SetAdminInviteRelation(ctx, "admin", "c", b.Code, "改绑", 1, at)
	if err != nil || bind.Version != 2 || bind.InviterID != "b" || bind.RegistrationMethod != "password" || !bind.CreatedAt.Before(at) {
		t.Fatalf("rebind preserves registration: %+v %v", bind, err)
	}
	_, err = p.SetAdminInviteRelation(ctx, "admin", "a", c.Code, "long cycle", 0, at)
	if !errors.Is(err, ErrInviteRelationCycle) {
		t.Fatalf("long cycle: %v", err)
	}
	_, err = p.SetAdminInviteRelation(ctx, "admin", "c", a.Code, "stale", 1, at)
	if !errors.Is(err, ErrInviteRelationStale) {
		t.Fatalf("stale: %v", err)
	}
	own, err := p.UserInviteCode(ctx, "c")
	if err != nil || own.ID != c.ID || own.SelfChangesUsed != 0 {
		t.Fatalf("own code changed: %+v %v", own, err)
	}
	var auditCount int
	var metadata []byte
	if err = p.pool.QueryRow(ctx, "SELECT count(*) FROM im_audits WHERE action='invite_relation.changed'").Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("audits: %d %v", auditCount, err)
	}
	if err = p.pool.QueryRow(ctx, "SELECT metadata FROM im_audits WHERE action='invite_relation.changed' AND target_id='c'").Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	var audit struct {
		Before, After InviteRelationBinding
		Reason        string
	}
	if err = json.Unmarshal(metadata, &audit); err != nil {
		t.Fatal(err)
	}
	if audit.Before.InviterID != "a" || audit.After.InviterID != "b" || audit.Reason != "改绑" {
		t.Fatalf("audit: %s", metadata)
	}
	overview, err := p.AdminUserOverview(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	invitation := overview["invitation"].(map[string]any)
	if invitation["boundCode"] != b.Code || invitation["bindingVersion"] != int64(2) {
		t.Fatalf("overview: %+v", invitation)
	}
	page, err := p.ListAdminInviteRelations(ctx, "", "admin", "", "", "", 20)
	if err != nil || page.Total != 1 || page.Items[0].BindingSource != "admin" {
		t.Fatalf("admin filter: %+v %v", page, err)
	}
}

func TestAdminInviteRelationRejectsInvalidSourcesAndRollsBack(t *testing.T) {
	p := inviteRelationTestStore(t)
	ctx := t.Context()
	at := time.Now().UTC()
	a := inviteRelationUser(t, p, "a", 1, "")
	inviteRelationUser(t, p, "b", 2, "")
	for _, code := range []string{"INVALID88", a.Code} {
		if code == a.Code {
			_, err := p.SetAdminInviteCodeStatus(ctx, "admin", a.ID, "disabled", "test", at)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := p.SetAdminInviteRelation(ctx, "admin", "b", code, "test", 0, at); !errors.Is(err, ErrInviteInvalid) {
			t.Fatalf("invalid %s: %v", code, err)
		}
	}
	reset, err := p.ResetAdminInviteCode(ctx, "admin", a.ID, "test", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.SetAdminInviteRelation(ctx, "admin", "b", a.Code, "retired", 0, at); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("retired: %v", err)
	}
	if _, err = p.pool.Exec(ctx, "UPDATE im_users SET banned=true WHERE id='a'"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.SetAdminInviteRelation(ctx, "admin", "b", reset.Code, "banned", 0, at); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("banned: %v", err)
	}
	if _, err = p.pool.Exec(ctx, "UPDATE im_users SET banned=false,deleted_at=now() WHERE id='a'"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.SetAdminInviteRelation(ctx, "admin", "b", reset.Code, "deleted", 0, at); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("deleted: %v", err)
	}
	if _, err = p.pool.Exec(ctx, "UPDATE im_users SET deleted_at=NULL WHERE id='a'"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.SetAdminInviteRelation(ctx, "admin", "missing", reset.Code, "missing", 0, at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err = p.pool.Exec(ctx, `CREATE FUNCTION reject_binding_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='invite_relation.changed' THEN RAISE EXCEPTION 'test audit unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_binding_audit BEFORE INSERT ON im_audits FOR EACH ROW EXECUTE FUNCTION reject_binding_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err = p.SetAdminInviteRelation(ctx, "admin", "b", reset.Code, "audit unavailable", 0, at); err == nil {
		t.Fatal("expected audit failure")
	}
	var count int
	if err = p.pool.QueryRow(ctx, "SELECT count(*) FROM im_user_invite_relations WHERE invitee_user_id='b'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("transaction leaked relation: %d %v", count, err)
	}
}

func TestAdminInviteRelationConcurrentWrites(t *testing.T) {
	p := inviteRelationTestStore(t)
	ctx := t.Context()
	at := time.Now().UTC()
	a := inviteRelationUser(t, p, "a", 1, "")
	b := inviteRelationUser(t, p, "b", 2, "")
	inviteRelationUser(t, p, "c", 3, "")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, code := range []string{a.Code, b.Code} {
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			<-start
			_, err := p.SetAdminInviteRelation(ctx, "admin", "c", code, "concurrent", 0, at)
			results <- err
		}(code)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, stales := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrInviteRelationStale) {
			stales++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || stales != 1 {
		t.Fatalf("successes=%d stales=%d", successes, stales)
	}
	results = make(chan error, 2)
	start = make(chan struct{})
	for _, item := range []struct{ user, code string }{{"a", b.Code}, {"b", a.Code}} {
		wg.Add(1)
		go func(user, code string) {
			defer wg.Done()
			<-start
			_, err := p.SetAdminInviteRelation(ctx, "admin", user, code, "concurrent cycle", 0, at)
			results <- err
		}(item.user, item.code)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, cycles := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrInviteRelationCycle) {
			cycles++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || cycles != 1 {
		t.Fatalf("successes=%d cycles=%d", successes, cycles)
	}
}

func TestAdminInviteRelationMigration65(t *testing.T) {
	p := inviteRelationTestStore(t)
	ctx := t.Context()
	a := inviteRelationUser(t, p, "a", 1, "")
	inviteRelationUser(t, p, "b", 2, a.Code)
	if _, err := p.pool.Exec(ctx, `ALTER TABLE im_user_invite_relations DROP COLUMN binding_source,DROP COLUMN updated_at,DROP COLUMN version; DELETE FROM im_schema_migrations WHERE version>=65; INSERT INTO im_schema_migrations(version) VALUES(64) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := p.migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var version int64
	var source, method string
	var created, updated time.Time
	if err := p.pool.QueryRow(ctx, "SELECT version,binding_source,registration_method,created_at,updated_at FROM im_user_invite_relations WHERE invitee_user_id='b'").Scan(&version, &source, &method, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != 65 || version != 1 || source != "registration" || method != "password" || !created.Equal(updated) {
		t.Fatalf("migration: %d %s %s %v %v", version, source, method, created, updated)
	}
}

func TestAdminInviteRelationConcurrentCodeReset(t *testing.T) {
	p := inviteRelationTestStore(t)
	ctx := t.Context()
	a := inviteRelationUser(t, p, "a", 1, "")
	inviteRelationUser(t, p, "b", 2, "")
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM im_user_invite_codes WHERE id=$1 FOR UPDATE", a.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := p.SetAdminInviteRelation(ctx, "admin", "b", a.Code, "reset race", 0, time.Now().UTC())
		result <- err
	}()
	// Wait until the binding reaches the locked code, then finish a reset. Its
	// new row takes an FK key-share lock on the inviter and must not deadlock.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT c.id,c.user_id,c.code,c.status,c.source,c.created_at%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("binding did not reach code lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(ctx, "UPDATE im_user_invite_codes SET status='retired' WHERE id=$1", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_user_invite_codes(id,user_id,code,status,source,created_by,created_at) VALUES('reset-a','a','RESETCODE88','active','admin','admin',now())`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("retired code accepted or lock failed: %v", err)
	}
}
