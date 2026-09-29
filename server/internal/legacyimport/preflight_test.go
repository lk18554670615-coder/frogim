package legacyimport

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func candidate(t *testing.T) sourceUser {
	t.Helper()
	hash, e := bcrypt.GenerateFromPassword([]byte("fixture-only-not-a-real-password"), 4)
	if e != nil {
		t.Fatal(e)
	}
	return sourceUser{id: "legacy-user", phone: "19900000111", passwordHash: string(hash), state: "active", assignment: 1, auth: 1}
}
func fixture(t *testing.T) inventory {
	return inventory{tenant: "default", boundTenant: "default", targetState: "suspended", sourceVersion: 78, targetVersion: 12, source: []sourceUser{candidate(t)}}
}
func hasIssue(r Report, code string) bool {
	for _, i := range r.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

var testTime = time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)

func TestPreflightPreservesIdentityAndNeverSerializesCredentials(t *testing.T) {
	in := fixture(t)
	in.source[0].phone = " +8619900000111 "
	deleted := candidate(t)
	deleted.id = "deleted-user"
	deleted.deleted = true
	deleted.phone = "deleted_phone"
	deleted.passwordHash = "not-a-hash"
	retired := candidate(t)
	retired.id = "retired-user"
	retired.state = "retired"
	retired.phone = "retired_phone"
	retired.passwordHash = "not-a-hash"
	in.source = append(in.source, deleted, retired)
	r := analyze(in, testTime)
	if !r.DataChecksPassed || r.Counts.Candidates != 1 || r.Counts.ValidCandidates != 1 || r.Counts.DeletedUsers != 1 || r.Counts.RetiredUsers != 1 || r.Counts.NormalizedPhones != 1 || !r.ReadOnly || !r.RequiresCutoverValidation {
		t.Fatal("incorrect inventory summary")
	}
	b, _ := json.Marshal(r)
	for _, private := range []string{in.source[0].passwordHash, "19900000111", "+8619900000111", "fixture-only-not-a-real-password"} {
		if strings.Contains(string(b), private) || strings.Contains(r.Summary(), private) {
			t.Fatal("report contains private authentication material")
		}
	}
	if strings.Contains(r.Summary(), "legacy-user") {
		t.Fatal("default summary exposes user IDs")
	}
	if !hasIssue(r, "SOURCE_PHONE_WILL_NORMALIZE") || !hasIssue(r, "PASSWORD_HASH_LEGACY_COST") {
		t.Fatal("missing compatibility warning")
	}
}

func TestPreflightConflictMatrix(t *testing.T) {
	cases := []struct {
		name, code string
		change     func(*inventory)
	}{
		{"invalid-phone", "SOURCE_PHONE_INVALID", func(i *inventory) { i.source[0].phone = "+85212345678" }},
		{"duplicate-normalized-phone", "SOURCE_PHONE_COLLISION", func(i *inventory) {
			other := i.source[0]
			other.id = "other-user"
			other.phone = "+86" + other.phone
			i.source = append(i.source, other)
		}},
		{"target-other-tenant", "TARGET_PHONE_OCCUPIED", func(i *inventory) {
			i.target = []targetAccount{{id: "foreign", phone: i.source[0].phone, tenant: "other", localID: "other-user", state: "active", assignment: 1, auth: 1}}
		}},
		{"target-same-tenant", "TARGET_PHONE_OCCUPIED", func(i *inventory) {
			i.target = []targetAccount{{id: "same-tenant", phone: i.source[0].phone, tenant: "default", localID: "other-user", state: "active", assignment: 1, auth: 1}}
		}},
		{"target-old-local-id", "TARGET_LOCAL_ID_OCCUPIED", func(i *inventory) {
			i.target = []targetAccount{{id: "occupied", phone: "19900000333", tenant: "default", localID: i.source[0].id, state: "deleted", assignment: 1, auth: 1}}
		}},
		{"target-noncanonical", "TARGET_PHONE_NOT_CANONICAL", func(i *inventory) {
			i.target = []targetAccount{{id: "target", phone: "+8619900000444", tenant: "default", localID: "another", state: "active", assignment: 1, auth: 1}}
		}},
		{"target-collision", "TARGET_PHONE_COLLISION", func(i *inventory) {
			i.target = []targetAccount{{id: "target-a", phone: "+8619900000444"}, {id: "target-b", phone: "19900000444"}}
		}},
		{"target-id-mismatch", "LINKED_IDENTITY_MISMATCH", func(i *inventory) { i.source[0].platformID = "missing" }},
		{"non-pristine", "SOURCE_IDENTITY_NOT_PRISTINE", func(i *inventory) { i.source[0].assignment = 2 }},
		{"retired-unlinked-transition", "SOURCE_IDENTITY_NOT_PRISTINE", func(i *inventory) { i.source[0].state = "credentials_resetting" }},
		{"wrong-tenant", "SOURCE_TENANT_MISMATCH", func(i *inventory) { i.boundTenant = "other" }},
		{"invalid-source-id", "SOURCE_ID_UNSUPPORTED", func(i *inventory) { i.source[0].id = "../not-a-user" }},
		{"plaintext-in-hash-column", "PASSWORD_HASH_UNSUPPORTED", func(i *inventory) { i.source[0].passwordHash = "plain-secret-sentinel" }},
		{"wrong-hash-algorithm", "PASSWORD_HASH_UNSUPPORTED", func(i *inventory) { i.source[0].passwordHash = "$argon2id$opaque" }},
		{"huge-bcrypt-cost", "PASSWORD_HASH_COST_UNSAFE", func(i *inventory) { h := i.source[0].passwordHash; i.source[0].passwordHash = h[:4] + "31" + h[6:] }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			in := fixture(t)
			test.change(&in)
			r := analyze(in, testTime)
			if r.DataChecksPassed || !hasIssue(r, test.code) {
				t.Fatal("missing blocking issue", test.code)
			}
		})
	}
}

func TestPreflightPasswordlessAndBanPoliciesRemainExplicit(t *testing.T) {
	in := fixture(t)
	in.source[0].passwordHash = ""
	in.source[0].banned = true
	until := testTime.Add(time.Hour)
	in.source[0].bannedUntil = &until
	r := analyze(in, testTime)
	if !r.DataChecksPassed || r.Counts.Passwordless != 1 || r.Counts.Banned != 1 || r.Counts.TemporaryBans != 1 || !hasIssue(r, "PASSWORDLESS_LOGIN_PATH_REQUIRED") || !hasIssue(r, "TEMPORARY_BAN_EXPIRY_MUST_BE_PRESERVED") {
		t.Fatal("policy warning lost")
	}
	expired := analyze(in, until)
	if !hasIssue(expired, "EXPIRED_BAN_REQUIRES_RECONCILIATION") || expired.Counts.TemporaryBans != 0 {
		t.Fatal("expiry boundary")
	}
	if r.Fingerprint != expired.Fingerprint {
		t.Fatal("time-only classification changed credential snapshot fingerprint")
	}
	in.source[0].banned = false
	r = analyze(in, testTime)
	if !hasIssue(r, "BAN_FLAG_AND_EXPIRY_INCONSISTENT") {
		t.Fatal("inconsistent ban omitted")
	}
}

func TestPreflightAlreadyLinkedIdentityAndGeneration(t *testing.T) {
	in := fixture(t)
	in.source[0].platformID = "account"
	in.source[0].passwordHash = ""
	in.target = []targetAccount{{id: "account", phone: in.source[0].phone, tenant: "default", localID: in.source[0].id, state: "active", assignment: 1, auth: 1}}
	r := analyze(in, testTime)
	if !r.DataChecksPassed || r.Counts.LinkedUsers != 1 || r.Counts.Candidates != 0 || r.Counts.Passwordless != 0 {
		t.Fatal("existing mapping treated as import")
	}
	in.target[0].auth = 2
	if analyze(in, testTime).DataChecksPassed {
		t.Fatal("auth generation mismatch")
	}
	in.target[0].auth = 1
	in.target[0].state = "deleted"
	if analyze(in, testTime).DataChecksPassed {
		t.Fatal("deleted directory identity accepted")
	}
	in.target[0].state = "blocked"
	in.source[0].state = "platform_blocked"
	if !analyze(in, testTime).DataChecksPassed {
		t.Fatal("stable blocked mapping lost")
	}
}

func TestPreflightDeterminismDriftAndBoundedIssues(t *testing.T) {
	in := fixture(t)
	other := in.source[0]
	other.id = "a-first"
	other.phone = "19900000222"
	in.source = append(in.source, other)
	a := analyze(in, testTime)
	in.source[0], in.source[1] = in.source[1], in.source[0]
	b := analyze(in, testTime)
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("row order changed fingerprint")
	}
	in.source[0].phone = "19900000555"
	if analyze(in, testTime).Fingerprint == b.Fingerprint {
		t.Fatal("phone drift ignored")
	}
	in = fixture(t)
	in.source[0].passwordHash = ""
	a = analyze(in, testTime)
	in.source[0].passwordHash = candidate(t).passwordHash
	if analyze(in, testTime).Fingerprint == a.Fingerprint {
		t.Fatal("credential drift ignored")
	}
	for range MaxIssues + 10 {
		in.source = append(in.source, sourceUser{id: "invalid", state: "active"})
	}
	r := analyze(in, testTime)
	if !r.IssuesTruncated || len(r.Issues) != MaxIssues || r.Errors <= MaxIssues || r.DataChecksPassed {
		t.Fatal("truncated report hid errors")
	}
}

func TestHashParserCanonicalEncodingAndBoundedWork(t *testing.T) {
	original := candidate(t).passwordHash
	for _, minor := range []string{"2a", "2b", "2y"} {
		h := "$" + minor + original[3:]
		if _, e := hashCost(h); e != nil {
			t.Fatal("supported bcrypt rejected", minor)
		}
	}
	for _, h := range []string{original + "x", original[:len(original)-1], "$2x" + original[3:], original[:28] + "B" + original[29:], original[:59] + "B"} {
		if _, e := hashCost(h); e == nil {
			t.Fatal("malformed bcrypt accepted")
		}
	}
}

func TestPreflightCollisionReferencesAreBoundedWithoutHidingInvalidCandidates(t *testing.T) {
	in := fixture(t)
	u := in.source[0]
	// Avoid per-user warnings exhausting the issue cap before the collision.
	u.passwordHash = u.passwordHash[:4] + "12" + u.passwordHash[6:]
	in.source = nil
	for i := 0; i < MaxIssueReferences+15; i++ {
		u.id = fmt.Sprintf("fixture-%03d", i)
		in.source = append(in.source, u)
	}
	r := analyze(in, testTime)
	if r.DataChecksPassed || r.Counts.ValidCandidates != 0 {
		t.Fatal("collision accepted")
	}
	for _, issue := range r.Issues {
		if issue.Code == "SOURCE_PHONE_COLLISION" {
			if !issue.ReferencesTruncated || len(issue.SourceUserIDs) != MaxIssueReferences || issue.SourceUserCount != MaxIssueReferences+15 {
				t.Fatal("collision references not bounded/count lost")
			}
			return
		}
	}
	t.Fatal("collision issue missing")
}
