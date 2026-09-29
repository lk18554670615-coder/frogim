package legacyimport

import (
	"errors"
	"testing"
)

func TestPostgresQuarantineRequiresCurrentSuspensionAndPersistsAudit(t *testing.T) {
	f, _, _ := importFixture(t)
	addLegacy(t, f, "invalid-phone", "123456", false, nil)
	addLegacy(t, f, "valid-phone", "19900000123", false, nil)
	report, e := f.check(t, "default")
	if e != nil || report.DataChecksPassed {
		t.Fatal("invalid fixture")
	}
	r := QuarantineRequest{ID: "quarantine-1", TenantID: "default", Actor: "operator", Reason: "verified inventory, retain history and deny login", ExpectedFingerprint: report.Fingerprint, UserIDs: []string{"invalid-phone"}, Confirmed: true}
	bad := r
	bad.UserIDs = []string{"valid-phone"}
	if _, e = QuarantineInvalidPhones(t.Context(), f.source, f.target, bad); !errors.Is(e, ErrImportConflict) {
		t.Fatal("valid phone was quarantined", e)
	}
	execFixture(t, f.source, `UPDATE im_tenant_realm_operations SET state='revoking'`)
	if _, e = QuarantineInvalidPhones(t.Context(), f.source, f.target, r); !errors.Is(e, ErrMaintenance) {
		t.Fatal("unconfirmed revocation bypassed", e)
	}
	execFixture(t, f.source, `UPDATE im_tenant_realm_operations SET state='completed'`)
	// Audit failure must roll back the identity mutation in the same transaction.
	execFixture(t, f.source, `CREATE FUNCTION reject_quarantine_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$; CREATE TRIGGER reject_quarantine BEFORE INSERT ON im_audits FOR EACH ROW EXECUTE FUNCTION reject_quarantine_audit()`)
	if _, e = QuarantineInvalidPhones(t.Context(), f.source, f.target, r); e == nil {
		t.Fatal("audit failure accepted")
	}
	var untouched bool
	if f.source.QueryRow(t.Context(), `SELECT local_identity_state='active' AND NOT banned AND password_hash<>'' FROM im_users WHERE id='invalid-phone'`).Scan(&untouched) != nil || !untouched {
		t.Fatal("audit failure partially quarantined identity")
	}
	execFixture(t, f.source, `DROP TRIGGER reject_quarantine ON im_audits`)
	for range 2 {
		if n, e := QuarantineInvalidPhones(t.Context(), f.source, f.target, r); e != nil || n != 1 {
			t.Fatal("quarantine/replay failed", e)
		}
	}
	var retained bool
	if f.source.QueryRow(t.Context(), `SELECT phone='123456' AND deleted_at IS NULL AND banned AND banned_until IS NULL AND password_hash='' AND local_identity_state='retired' FROM im_users WHERE id='invalid-phone'`).Scan(&retained) != nil || !retained {
		t.Fatal("quarantine did not preserve disabled record")
	}
	r.Reason = "changed"
	if _, e = QuarantineInvalidPhones(t.Context(), f.source, f.target, r); !errors.Is(e, ErrImportConflict) {
		t.Fatal("changed replay accepted", e)
	}
	report, e = f.check(t, "default")
	if e != nil || !report.DataChecksPassed || report.Counts.RetiredUsers != 1 || report.Counts.Candidates != 1 {
		t.Fatal("quarantined inventory not excluded")
	}
}
