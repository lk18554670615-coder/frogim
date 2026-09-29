package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type tenantRealmStackPeer struct {
	platform.EnterpriseRPC
	loseCompleted, lost bool
}

func (p *tenantRealmStackPeer) SetRealm(ctx context.Context, op tenancy.RealmOperation) (tenancy.RealmAck, error) {
	ack, e := p.EnterpriseRPC.SetRealm(ctx, op)
	if e == nil && ack.State == "completed" && p.loseCompleted {
		p.loseCompleted = false
		p.lost = true
		return tenancy.RealmAck{}, errors.New("isolated completed ACK loss")
	}
	return ack, e
}
func (p *tenantRealmStackPeer) CheckReadiness(ctx context.Context, id, nonce string) (tenancy.Readiness, error) {
	r, e := p.EnterpriseRPC.CheckReadiness(ctx, id, nonce)
	// This disposable stack has real PostgreSQL/Redis/IM/LiveKit, not MinIO.
	// Only storage readiness is a fixture; runtime uses the unmodified RPC.
	if e != nil {
		return r, e
	}
	r.Checks["media"] = true
	return r, e
}

func testTenantRealmStack(t *testing.T, ps *platform.Store, peers platform.EnterpriseRPC, public string, client *http.Client, clientTLS *tls.Config, business map[string]*httptest.Server, apis map[string]*API, stores map[string]*store.Postgres, dbs map[string]*pgx.Conn, pconn *pgx.Conn, admins map[string]string) {
	ctx := t.Context()
	iw := platform.Worker{Store: ps, Enterprise: peers}
	for range 2 {
		if _, e := iw.Once(ctx); e != nil {
			t.Fatal(e)
		}
	} // Finish the earlier accepted fixture.
	sessions := map[string]map[string]any{}
	mobileSessions := map[string]map[string]any{}
	for index, id := range []string{"a", "b"} {
		phone := fmt.Sprintf("1380000010%d", index+3)
		status, _ := tenancyStackRequest(t, client, "POST", business[id].URL+"/v2/admin/users", admins[id], map[string]any{"requestId": "realm-" + id, "phone": phone, "name": "realm fixture", "gender": "unspecified", "password": "RealmPassword123!", "reason": "isolated realm test", "confirmed": true})
		if status != 202 {
			t.Fatal("realm fixture create", id, status)
		}
		for range 2 {
			if _, e := iw.Once(ctx); e != nil {
				t.Fatal(e)
			}
		}
		status, login := tenancyStackRequest(t, client, "POST", public+"/v2/auth/password-login", "", map[string]string{"phone": phone, "password": "RealmPassword123!"})
		if status != 200 {
			t.Fatal("realm fixture login", id, status)
		}
		status, session := tenancyStackRequest(t, client, "POST", business[id].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": login["sessionTicket"].(string)})
		if status != 200 {
			t.Fatal("realm fixture exchange", id, status)
		}
		sessions[id] = session
		status, mobileLogin := tenancyStackDeviceRequest(t, client, "android", "POST", public+"/v2/auth/password-login", "", map[string]string{"phone": phone, "password": "RealmPassword123!"})
		if status != 200 {
			t.Fatal("realm mobile login", id, status)
		}
		status, mobile := tenancyStackDeviceRequest(t, client, "android", "POST", business[id].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": mobileLogin["sessionTicket"].(string)})
		if status != 200 {
			t.Fatal("realm mobile exchange", id, status)
		}
		mobileSessions[id] = mobile
	}
	user := sessions["b"]["user"].(map[string]any)["id"].(string)
	imClosed := tenancyStackIMConnect(t, mobileSessions["b"], true)
	otherIMClosed := tenancyStackIMConnect(t, mobileSessions["a"], true)
	wssClosed := tenancyStackIMWSSConnect(t, sessions["b"], true)
	otherWSS := tenancyStackIMWSSConnect(t, sessions["a"], true)
	for _, probe := range []*tenancyIMProbe{imClosed, otherIMClosed, wssClosed, otherWSS} {
		probe.assertAlive(t)
	}
	old := sessions["b"]["accessToken"].(string)
	call := tenancyStackMediaJoin(t, client, clientTLS, business["b"].URL, apis["b"], stores["b"], dbs["b"], user, old, "realm")
	otherCall := tenancyStackMediaJoin(t, client, clientTLS, business["a"].URL, apis["a"], stores["a"], dbs["a"], sessions["a"]["user"].(map[string]any)["id"].(string), sessions["a"]["accessToken"].(string), "other-realm")
	assertPaused := func() {
		t.Helper()
		for _, closed := range []<-chan struct{}{call.disconnected, call.observerDisconnected} {
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("realm retained active media connection")
			}
		}
		participants, e := apis["b"].livekit.(livekitAdminControl).ListParticipants(ctx, call.room)
		if e != nil || len(participants) != 0 {
			t.Fatal("realm retained participants", e)
		}
		status, _ := tenancyStackRequest(t, client, "GET", business["b"].URL+"/livekit/rtc/validate?access_token="+url.QueryEscape(call.token), "", nil)
		if status != 401 {
			t.Fatal("old realm media token revived", status)
		}
		for _, closed := range []<-chan struct{}{otherCall.disconnected, otherCall.observerDisconnected} {
			select {
			case <-closed:
				t.Fatal("other realm call disconnected")
			default:
			}
		}
	}
	identity, _, e := stores["b"].TenantAuthIdentity(ctx, "b", user)
	if e != nil {
		t.Fatal(e)
	}
	// Force several batches and preserve both kinds of individual ban.
	for index := range 12 {
		id := fmt.Sprintf("realm-target-%02d", index)
		i := tenancy.Identity{TenantID: "b", AccountID: id, LocalUserID: id, AssignmentVersion: 1}
		if e := stores["b"].PrepareTenantIdentity(ctx, store.TenantProvision{OperationID: id, Identity: i, Phone: id, Name: "batch fixture", Method: "transfer"}); e != nil {
			t.Fatal(e)
		}
		if e := stores["b"].ActivateTenantIdentity(ctx, i); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = dbs["b"].Exec(ctx, `UPDATE im_users SET banned=true WHERE id='realm-target-00';UPDATE im_users SET local_identity_state='platform_blocked' WHERE id='realm-target-01'`); e != nil {
		t.Fatal(e)
	}
	j, e := ps.RequestRealm(ctx, "b", "stack-pause", "operator", "isolation pause", 1, false, true)
	if e != nil {
		t.Fatal(e)
	}
	status, _ := tenancyStackRequest(t, client, "POST", public+"/v2/auth/password-login", "", map[string]string{"phone": "13800000104", "password": "RealmPassword123!"})
	if status != 401 {
		t.Fatal("directory pause not immediate", status)
	}
	peer := &tenantRealmStackPeer{EnterpriseRPC: peers, loseCompleted: true}
	rw := platform.RealmWorker{Store: ps, Enterprise: peer}
	drain := func(job platform.RealmJob) {
		t.Helper()
		for attempt := 0; attempt < 80; attempt++ {
			if _, e := pconn.Exec(ctx, `UPDATE platform_realm_jobs SET retry_at=now() WHERE id=$1`, job.ID); e != nil {
				t.Fatal(e)
			}
			if worked, e := rw.Once(ctx); e != nil || !worked {
				t.Fatal("realm worker", worked, e)
			}
			var state string
			if e := pconn.QueryRow(ctx, `SELECT state FROM platform_realm_jobs WHERE id=$1`, job.ID).Scan(&state); e != nil {
				t.Fatal(e)
			}
			if peer.lost {
				if state != "pending" {
					t.Fatal("lost ACK reported complete")
				}
				if status, _ := tenancyStackRequest(t, client, "POST", public+"/v2/auth/password-login", "", map[string]string{"phone": "13800000104", "password": "RealmPassword123!"}); status != 401 {
					t.Fatal("unconfirmed transition allowed login", status)
				}
				peer.lost = false
			}
			if state == "completed" {
				return
			}
			// Real IM disconnect/online bookkeeping is asynchronous. Do not
			// mistake a rapid test retry loop for elapsed production backoff.
			select {
			case <-ctx.Done():
				t.Fatal("realm test cancelled")
			case <-time.After(200 * time.Millisecond):
			}
		}
		t.Fatal("realm operation did not finish")
	}
	drain(j)
	imClosed.assertClosed(t)
	wssClosed.assertClosed(t)
	otherIMClosed.assertAlive(t)
	otherWSS.assertAlive(t)
	tenancyStackIMConnect(t, mobileSessions["b"], false)
	tenancyStackIMConnect(t, sessions["b"], false)
	tenancyStackIMWSSConnect(t, sessions["b"], false)
	assertPaused()
	for _, path := range []string{"/v2/users/me", "/v2/auth/im-session"} {
		method := "GET"
		if path == "/v2/auth/im-session" {
			method = "POST"
		}
		status, _ := tenancyStackRequest(t, client, method, business["b"].URL+path, old, map[string]any{})
		if status != 401 {
			t.Fatal("paused business credential", status)
		}
	}
	status, _ = tenancyStackRequest(t, client, "GET", business["a"].URL+"/v2/users/me", sessions["a"]["accessToken"].(string), nil)
	if status != 200 {
		t.Fatal("other enterprise affected", status)
	}
	// Control/admin inspection remains available for recovery, not user business APIs.
	status, _ = tenancyStackRequest(t, client, "GET", business["b"].URL+"/v2/admin/users", admins["b"], nil)
	if status != 200 {
		t.Fatal("recovery administration unavailable", status)
	}
	readInventory := func(ctx context.Context, request tenancy.RecoveryInventoryRequest) (tenancy.RecoveryInventoryPage, error) {
		var page tenancy.RecoveryInventoryPage
		err := peers.Peers["b"].Call(ctx, "/internal/tenancy/recovery/inventory", request, &page)
		return page, err
	}
	inventory, digest, err := tenancy.CollectRecoveryInventory(ctx, "b", business["b"].URL, 2, readInventory)
	if err != nil || digest == "" || len(inventory.Users) < 12 || !inventory.Realm.SuspensionConfirmed {
		t.Fatal("authenticated recovery inventory", err)
	}
	if _, _, err := tenancy.CollectRecoveryInventory(ctx, "a", business["a"].URL, 2, readInventory); err == nil {
		t.Fatal("cross-tenant recovery inventory accepted")
	}
	resume, e := ps.RequestRealm(ctx, "b", "stack-resume", "operator", "isolation resume", 2, true, true)
	if e != nil {
		t.Fatal(e)
	}
	peer.loseCompleted = true
	drain(resume)
	if _, _, err := tenancy.CollectRecoveryInventory(ctx, "b", business["b"].URL, 2, readInventory); err == nil {
		t.Fatal("resumed enterprise supplied suspended inventory")
	}
	status, _ = tenancyStackRequest(t, client, "GET", business["b"].URL+"/v2/users/me", old, nil)
	if status != 401 {
		t.Fatal("old API credential revived", status)
	}
	assertPaused()
	status, login := tenancyStackRequest(t, client, "POST", public+"/v2/auth/password-login", "", map[string]string{"phone": "13800000104", "password": "RealmPassword123!"})
	if status != 200 {
		t.Fatal("resumed login", status)
	}
	status, fresh := tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": login["sessionTicket"].(string)})
	if status != 200 || fresh["user"].(map[string]any)["id"] != user {
		t.Fatal("resumed identity", status)
	}
	if fresh["imSession"].(map[string]any)["token"] == sessions["b"]["imSession"].(map[string]any)["token"] {
		t.Fatal("old IM token reinstalled")
	}
	tenancyStackIMConnect(t, sessions["b"], false)
	tenancyStackIMWSSConnect(t, sessions["b"], false)
	freshIM := tenancyStackIMWSSConnect(t, fresh, true)
	if _, e = peers.SetRealm(ctx, tenancy.RealmOperation{OperationID: j.ID, TenantID: "b", Version: 2}); e != nil {
		t.Fatal("completed operation replay", e)
	}
	status, _ = tenancyStackRequest(t, client, "GET", business["b"].URL+"/v2/users/me", fresh["accessToken"].(string), nil)
	if status != 200 {
		t.Fatal("old replay killed new session", status)
	}
	freshIM.assertAlive(t)
	otherIMClosed.assertAlive(t)
	otherWSS.assertAlive(t)
	got, _, e := stores["b"].TenantAuthIdentity(ctx, "b", user)
	if e != nil || got != identity {
		t.Fatal("identity changed", e)
	}
	for _, id := range []string{"realm-target-00", "realm-target-01"} {
		if _, _, e := stores["b"].TenantAuthIdentity(ctx, "b", id); e == nil {
			t.Fatal("individual ban lost", id)
		}
	}
}
