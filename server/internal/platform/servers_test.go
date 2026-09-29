package platform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

type inspectFunc func(context.Context, string, string) (deployment.Inspection, error)

func (f inspectFunc) Inspect(ctx context.Context, id, nonce string) (deployment.Inspection, error) {
	return f(ctx, id, nonce)
}
func serverFixture(tenant, host string) inspectFunc {
	return func(_ context.Context, id, nonce string) (deployment.Inspection, error) {
		return deployment.Inspection{Nonce: nonce, Protocol: 1, ServerID: id, TenantID: tenant, HTTPBaseURL: "https://" + tenant + ".example", HostFingerprint: strings.Repeat(host, 64), IsolationMode: "local_preview", Runtime: "linux/amd64", Capabilities: []string{"inspect"}}, nil
	}
}
func serverInput() ServerOperation {
	return ServerOperation{RequestID: "register-a", Action: "register", ServerID: "server-a", TenantID: "a", DisplayName: "Local A", ExpectedConfigVersion: 1, Reason: "local isolated verification", Confirmed: true}
}

func TestPlatformPostgresServerRegistry(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	token := releaseTestAdmin(t, s)
	in := serverInput()
	agent := serverFixture("a", "a")
	var calls atomic.Int32
	peer := inspectFunc(func(ctx context.Context, id, nonce string) (deployment.Inspection, error) {
		calls.Add(1)
		return agent(ctx, id, nonce)
	})
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			v, e := s.ManageServer(ctx, "release-operator", token, in, peer)
			if e != nil || v.Revision != 1 || v.IsolationMode != "local_preview" {
				t.Error("concurrent replay", e)
			}
		})
	}
	wg.Wait()
	before := calls.Load()
	if _, e := s.ManageServer(ctx, "release-operator", token, in, nil); e != nil {
		t.Fatal("offline replay", e)
	}
	if calls.Load() != before {
		t.Fatal("replay called agent")
	}
	changed := in
	changed.Reason = "changed request"
	if _, e := s.ManageServer(ctx, "release-operator", token, changed, peer); !errors.Is(e, ErrRequestChanged) {
		t.Fatal("changed input", e)
	}
	if e := s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	var count, version int
	if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_server_operations`).Scan(&count); e != nil || count != 1 {
		t.Fatal("receipts", count, e)
	}
	if e := s.pool.QueryRow(ctx, `SELECT max(version) FROM platform_schema_migrations`).Scan(&version); e != nil || version != SchemaVersion {
		t.Fatal(version, e)
	}
	recheck := in
	recheck.RequestID = "inspect-a"
	recheck.Action = "inspect"
	recheck.ExpectedRevision = 1
	out, e := s.ManageServer(ctx, "release-operator", token, recheck, peer)
	if e != nil || out.Revision != 2 {
		t.Fatal("inspection", e)
	}
	recheck.RequestID = "inspect-stale"
	if _, e = s.ManageServer(ctx, "release-operator", token, recheck, peer); !errors.Is(e, ErrServerChanged) {
		t.Fatal("stale revision", e)
	}
	recheck.ExpectedRevision = 2
	if _, e = s.ManageServer(ctx, "release-operator", token, recheck, serverFixture("a", "b")); !errors.Is(e, ErrServerChanged) {
		t.Fatal("replaced host", e)
	}
	var audit string
	if e = s.pool.QueryRow(ctx, `SELECT jsonb_agg(metadata)::text FROM platform_audits WHERE action LIKE 'server.%'`).Scan(&audit); e != nil || strings.Contains(audit, "http") || strings.Contains(audit, strings.Repeat("a", 64)) {
		t.Fatal("audit contains transport or raw host data", e)
	}
	if e = s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_audits WHERE action LIKE 'server.%'`).Scan(&count); e != nil || count != 2 {
		t.Fatal("duplicate audit", count, e)
	}
	api := &API{Store: s, Limiter: testLimit{}}
	status, data := adminTestCall(t, api, "GET", "/platform/admin/servers?q=server-a", token, "")
	if status != 200 || data["total"] != float64(1) {
		t.Fatal("list", status)
	}
	status, _ = adminTestCall(t, api, "GET", "/platform/admin/servers/operations/register-a", token, "")
	if status != 200 {
		t.Fatal("receipt", status)
	}
	status, _ = adminTestCall(t, api, "GET", "/platform/admin/servers/configured", token, "")
	if status != 200 {
		t.Fatal("configured", status)
	}
	if _, e = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET role='reader' WHERE id='release-operator'`); e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(in)
	status, _ = adminTestCall(t, api, "POST", "/platform/admin/servers/operations", token, string(body))
	if status != 401 {
		t.Fatal("read-only write", status)
	}
	if _, e = s.ManageServer(ctx, "release-operator", token, in, peer); !errors.Is(e, ErrDenied) {
		t.Fatal("reader replay", e)
	}
}

func TestPlatformPostgresServerBindingAndRaces(t *testing.T) {
	for _, race := range []string{"same-host", "same-tenant", "revoked-session", "tenant-changed", "bad-report", "audit-failure"} {
		t.Run(race, func(t *testing.T) {
			s := isolatedPlatform(t)
			ctx := t.Context()
			token := releaseTestAdmin(t, s)
			in := serverInput()
			fixture := serverFixture("a", "a")
			if race == "same-host" || race == "same-tenant" {
				if _, e := s.ManageServer(ctx, "release-operator", token, in, fixture); e != nil {
					t.Fatal(e)
				}
				in.RequestID = "register-b"
				in.ServerID = "server-b"
				if race == "same-host" {
					in.TenantID = "b"
					fixture = serverFixture("b", "a")
				} else {
					fixture = serverFixture("a", "b")
				}
			}
			expected := ErrServerChanged
			agent := inspectFunc(func(ctx context.Context, id, nonce string) (deployment.Inspection, error) {
				var err error
				switch race {
				case "revoked-session":
					_, err = s.pool.Exec(ctx, `UPDATE platform_admin_sessions SET revoked_at=now()`)
				case "tenant-changed":
					_, err = s.pool.Exec(ctx, `UPDATE platform_tenants SET config_version=config_version+1 WHERE id='a'`)
				case "audit-failure":
					_, err = s.pool.Exec(ctx, `ALTER TABLE platform_audits ADD CONSTRAINT reject_server_audit CHECK(action NOT LIKE 'server.%')`)
				}
				if err != nil {
					t.Error(err)
				}
				r, e := fixture(ctx, id, nonce)
				if race == "bad-report" {
					r.Nonce = "stale"
				}
				return r, e
			})
			if race == "revoked-session" {
				expected = ErrDenied
			}
			if race == "bad-report" {
				expected = ErrAgentUnavailable
			}
			_, err := s.ManageServer(ctx, "release-operator", token, in, agent)
			if race == "audit-failure" {
				if err == nil {
					t.Fatal("audit rollback")
				}
			} else if !errors.Is(err, expected) {
				t.Fatal(race, err)
			}
			var n int
			want := 0
			if race == "same-host" || race == "same-tenant" {
				want = 1
			}
			if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_servers`).Scan(&n); e != nil || n != want {
				t.Fatal("partial binding", n, e)
			}
			if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_server_operations`).Scan(&n); e != nil || n != want {
				t.Fatal("partial receipt", n, e)
			}
		})
	}
}

func TestPlatformPostgresServerInvalidRequests(t *testing.T) {
	s := isolatedPlatform(t)
	token := releaseTestAdmin(t, s)
	in := serverInput()
	api := &API{Store: s, Limiter: testLimit{}}
	called := false
	peer := inspectFunc(func(context.Context, string, string) (deployment.Inspection, error) {
		called = true
		return deployment.Inspection{}, nil
	})
	for _, change := range []func(*ServerOperation){func(v *ServerOperation) { v.Confirmed = false }, func(v *ServerOperation) { v.Action = "shell" }, func(v *ServerOperation) { v.ServerID = "../x" }, func(v *ServerOperation) { v.ExpectedConfigVersion = 0 }, func(v *ServerOperation) { v.Reason = "" }} {
		bad := in
		change(&bad)
		if _, e := s.ManageServer(t.Context(), "release-operator", token, bad, peer); !errors.Is(e, tenancy.ErrInvalid) {
			t.Fatal(e)
		}
	}
	if called {
		t.Fatal("invalid request contacted agent")
	}
	for _, body := range []string{`{"controlUrl":"https://untrusted.example"}`, `{"command":"restart"}`, `{"shell":"x"}`} {
		status, _ := adminTestCall(t, api, "POST", "/platform/admin/servers/operations", token, body)
		if status != 400 {
			t.Fatal("unknown field", status)
		}
	}
	status, _ := adminTestCall(t, api, "GET", "/platform/admin/servers", "", "")
	if status != 401 {
		t.Fatal("anonymous", status)
	}
	if _, e := s.ManageServer(t.Context(), "release-operator", token, in, AgentRPC{}); !errors.Is(e, ErrAgentUnavailable) {
		t.Fatal("unconfigured peer", e)
	}
}

func TestPlatformPostgresConcurrentHostClaim(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	token := releaseTestAdmin(t, s)
	var wg sync.WaitGroup
	var successes, conflicts atomic.Int32
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	for _, tenant := range []string{"a", "b"} {
		wg.Go(func() {
			in := serverInput()
			in.RequestID, in.ServerID, in.TenantID = "claim-"+tenant, "server-"+tenant, tenant
			peer := inspectFunc(func(ctx context.Context, id, nonce string) (deployment.Inspection, error) {
				ready <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return deployment.Inspection{}, ctx.Err()
				}
				return serverFixture(tenant, "a")(ctx, id, nonce)
			})
			_, err := s.ManageServer(ctx, "release-operator", token, in, peer)
			if err == nil {
				successes.Add(1)
			} else if errors.Is(err, ErrServerChanged) {
				conflicts.Add(1)
			} else {
				t.Error(err)
			}
		})
	}
	<-ready
	<-ready
	close(release)
	wg.Wait()
	if successes.Load() != 1 || conflicts.Load() != 1 {
		t.Fatal("same physical identifier assigned twice")
	}
}
