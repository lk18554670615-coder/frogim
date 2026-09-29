package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/legacyimport"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
	"github.com/linli/im/server/internal/wukong"
	"golang.org/x/crypto/bcrypt"
)

func testLegacyImportStack(t *testing.T, ps *platform.Store, peers platform.EnterpriseRPC, pdsn, sdsn string, pconn, sconn *pgx.Conn, api *API, public, business string, client *http.Client, loseAck *atomic.Bool) {
	ctx := t.Context()
	iw := platform.Worker{Store: ps, Enterprise: peers}
	for range 2 {
		if _, e := iw.Once(ctx); e != nil {
			t.Fatal(e)
		}
	} // complete earlier accepted fixture
	uid := fmt.Sprintf("legacy-stack-%d", time.Now().UnixNano())
	hash, e := bcrypt.GenerateFromPassword([]byte("LegacyStackPassword123!"), 4)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = sconn.Exec(ctx, `INSERT INTO im_users(id,phone,name,password_hash,created_at) VALUES($1,'19900000991','legacy preserved identity',$2,now())`, uid, string(hash)); e != nil {
		t.Fatal("legacy fixture insert failed")
	}
	oldIM, e := tenancy.Secret()
	if e != nil {
		t.Fatal(e)
	}
	if e = api.wukongClient.ProvisionUser(ctx, wukong.UserTokenRequest{UID: uid, Token: oldIM, DeviceFlag: wukong.DeviceApp, DeviceLevel: wukong.DeviceLevelMaster}); e != nil {
		t.Fatal("legacy IM fixture unavailable")
	}
	oldSession := map[string]any{"imSession": map[string]any{"uid": uid, "token": oldIM, "deviceFlag": float64(wukong.DeviceApp), "tcpUrl": "tcp://127.0.0.1:15174"}}
	oldConnection := tenancyStackIMConnect(t, oldSession, true)
	oldConnection.assertAlive(t)
	var version int64
	if pconn.QueryRow(ctx, `SELECT access_version FROM platform_tenants WHERE id='a'`).Scan(&version) != nil {
		t.Fatal("realm fixture unavailable")
	}
	if _, e = ps.RequestRealm(ctx, "a", "legacy-pause", "test-operator", "isolated migration pause", version, false, true); e != nil {
		t.Fatal(e)
	}
	rw := platform.RealmWorker{Store: ps, Enterprise: &tenantRealmStackPeer{EnterpriseRPC: peers}}
	settleRealm := func(want string) {
		t.Helper()
		for range 30 {
			if _, e = rw.Once(ctx); e != nil {
				t.Fatal(e)
			}
			var state string
			if pconn.QueryRow(ctx, `SELECT status FROM platform_tenants WHERE id='a'`).Scan(&state) != nil {
				t.Fatal("realm status unavailable")
			}
			if state == want {
				return
			}
			if _, e = pconn.Exec(ctx, `UPDATE platform_realm_jobs SET retry_at=now() WHERE tenant_id='a'`); e != nil {
				t.Fatal(e)
			}
			// Real IM disconnect bookkeeping is asynchronous; retry after actual
			// elapsed time, without weakening the completed-ack requirement.
			select {
			case <-ctx.Done():
				t.Fatal("migration fixture cancelled")
			case <-time.After(200 * time.Millisecond):
			}
		}
		t.Fatal("realm operation did not settle")
	}
	settleRealm("suspended")
	select {
	case <-oldConnection.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("legacy IM connection survived acknowledged suspension")
	}
	tenancyStackIMConnect(t, oldSession, false)
	s, e := legacyimport.OpenLocalForImport(ctx, sdsn)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e := legacyimport.OpenLocalForImport(ctx, pdsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	r, e := legacyimport.Preflight(ctx, s, p, "a", time.Now().UTC())
	if e != nil || !r.DataChecksPassed {
		// Safe issue DTOs contain codes and synthetic fixture IDs, never hashes.
		t.Fatal("real-stack legacy preflight failed", e, r.Issues)
	}
	b, e := legacyimport.StartImport(ctx, s, p, legacyimport.ImportRequest{ID: "stack-import", TenantID: "a", Actor: "test-operator", Reason: "isolated real-stack import", ExpectedFingerprint: r.Fingerprint, Confirmed: true})
	if e != nil || b.Total != 1 {
		t.Fatal("real-stack import reservation failed", e)
	}
	loseAck.Store(true)
	if _, e = legacyimport.ResumeImportOne(ctx, s, p, b.ID, peers); !errors.Is(e, legacyimport.ErrImportUnavailable) {
		t.Fatal("lost mTLS acknowledgement did not retain task", e)
	}
	if _, e = pconn.Exec(ctx, `UPDATE platform_legacy_import_items SET retry_at=now() WHERE batch_id=$1`, b.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = legacyimport.ResumeImportOne(ctx, s, p, b.ID, peers); e != nil {
		t.Fatal("real mTLS resume failed", e)
	}
	b, e = legacyimport.ImportStatus(ctx, p, b.ID)
	if e != nil || b.State != "completed" || b.Completed != 1 {
		t.Fatal("real import incomplete")
	}
	if _, e = ps.Login(ctx, "19900000991", "LegacyStackPassword123!"); !errors.Is(e, platform.ErrDenied) {
		t.Fatal("import automatically resumed tenant")
	}
	if _, e = ps.RequestRealm(ctx, "a", "legacy-resume", "test-operator", "explicit fixture resumption", version+1, true, true); e != nil {
		t.Fatal(e)
	}
	settleRealm("active")
	status, login := tenancyStackRequest(t, client, "POST", public+"/v2/auth/password-login", "", map[string]string{"phone": "19900000991", "password": "LegacyStackPassword123!"})
	if status != 200 {
		t.Fatal("historical password cannot use unified login", status)
	}
	status, session := tenancyStackRequest(t, client, "POST", business+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": login["sessionTicket"].(string)})
	if status != 200 || session["user"].(map[string]any)["id"] != uid {
		t.Fatal("history identity not retained", status)
	}
	tenancyStackIMConnect(t, session, true)
	tenancyStackIMConnect(t, oldSession, false)
	if _, e = legacyimport.ResumeImportOne(ctx, s, p, b.ID, peers); e != nil {
		t.Fatal("completed replay failed", e)
	}
	status, _ = tenancyStackRequest(t, client, "GET", business+"/v2/users/me", session["accessToken"].(string), nil)
	if status != 200 {
		t.Fatal("replay invalidated new login", status)
	}
}
