package legacyimport

import (
	"errors"
	"testing"
)

func TestPostgresLegacyImportRejectsUnfinishedDeployment(t *testing.T) {
	f, _, _ := importFixture(t)
	addLegacy(t, f, "fixture-old", "19900000999", false, nil)
	r := importRequest(t, f, "must-not-start")
	execFixture(t, f.target, `INSERT INTO platform_admin_accounts(id,username,password_hash,role) VALUES('deployment-fixture','deployment-fixture','unused','operator');
 INSERT INTO platform_servers(id,tenant_id,display_name,host_fingerprint,isolation_mode,runtime,revision,verified_at) VALUES('fixture-server','default','fixture',repeat('a',64),'local_preview','linux/amd64',1,now());
 INSERT INTO platform_deployment_jobs(id,actor_id,request_id,tenant_id,server_id,input,operation,release,binding,state) VALUES('unfinished','deployment-fixture','unfinished','default','fixture-server','{}','{}','{}','{}','unconfirmed')`)
	if _, e := StartImport(t.Context(), f.source, f.target, r); !errors.Is(e, ErrMaintenance) {
		t.Fatal("concurrent import accepted", e)
	}
	var n int
	if e := f.target.QueryRow(t.Context(), `SELECT count(*) FROM platform_accounts`).Scan(&n); e != nil || n != 0 {
		t.Fatal("partial reservation", n, e)
	}
}
