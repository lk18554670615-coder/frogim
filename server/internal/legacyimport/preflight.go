// Package legacyimport owns default-enterprise migration checks. Preflight is
// deliberately read-only: a clean inventory is not permission to activate users
// or proof that old business/IM/RTC sessions have been revoked.
package legacyimport

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

const MaxRows = 100000
const MaxIssues = 1000
const MaxIssueReferences = 100

type Issue struct {
	Code                string   `json:"code"`
	Severity            string   `json:"severity"`
	SourceUserIDs       []string `json:"sourceUserIds,omitempty"`
	TargetAccountIDs    []string `json:"targetAccountIds,omitempty"`
	SourceUserCount     int      `json:"sourceUserCount,omitempty"`
	TargetAccountCount  int      `json:"targetAccountCount,omitempty"`
	ReferencesTruncated bool     `json:"referencesTruncated,omitempty"`
}
type Counts struct {
	SourceUsers      int `json:"sourceUsers"`
	Candidates       int `json:"candidates"`
	ValidCandidates  int `json:"validCandidates"`
	LinkedUsers      int `json:"linkedUsers"`
	DeletedUsers     int `json:"deletedUsers"`
	RetiredUsers     int `json:"retiredUsers"`
	Passwordless     int `json:"passwordless"`
	Banned           int `json:"banned"`
	TemporaryBans    int `json:"temporaryBans"`
	NormalizedPhones int `json:"normalizedPhones"`
	TargetAccounts   int `json:"targetAccounts"`
}
type Report struct {
	Version                   int       `json:"version"`
	TenantID                  string    `json:"tenantId"`
	SourceSchemaVersion       int       `json:"sourceSchemaVersion"`
	TargetSchemaVersion       int       `json:"targetSchemaVersion"`
	SourceBound               bool      `json:"sourceBound"`
	TargetState               string    `json:"targetState"`
	CheckedAt                 time.Time `json:"checkedAt"`
	DataChecksPassed          bool      `json:"dataChecksPassed"`
	ReadOnly                  bool      `json:"readOnly"`
	RequiresCutoverValidation bool      `json:"requiresCutoverValidation"`
	Consistency               string    `json:"consistency"`
	Fingerprint               string    `json:"fingerprint"`
	Counts                    Counts    `json:"counts"`
	Errors                    int       `json:"errors"`
	Warnings                  int       `json:"warnings"`
	Issues                    []Issue   `json:"issues"`
	IssuesTruncated           bool      `json:"issuesTruncated"`
}

// These records never leave this package and are never serialized to reports,
// logs, command output or files. The only credential field is the original hash;
// preflight neither asks for nor derives a user's plaintext password.
type sourceUser struct {
	id, phone, passwordHash, platformID, state string
	assignment, auth                           int64
	banned, deleted                            bool
	bannedUntil                                *time.Time
}
type targetAccount struct {
	id, phone, tenant, localID, state string
	assignment, auth                  int64
}
type inventory struct {
	tenant, boundTenant, targetState string
	sourceVersion, targetVersion     int
	source                           []sourceUser
	target                           []targetAccount
}

var hashFormat = regexp.MustCompile(`^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$`)
var bcryptEncoding = base64.NewEncoding("./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789").WithPadding(base64.NoPadding).Strict()

func hashCost(hash string) (int, error) {
	if !hashFormat.MatchString(hash) {
		return 0, tenancy.ErrInvalid
	}
	salt, e := bcryptEncoding.DecodeString(hash[7:29])
	if e != nil || len(salt) != 16 {
		return 0, tenancy.ErrInvalid
	}
	sum, e := bcryptEncoding.DecodeString(hash[29:])
	if e != nil || len(sum) != 23 {
		return 0, tenancy.ErrInvalid
	}
	return bcrypt.Cost([]byte(hash))
}

func analyze(in inventory, at time.Time) Report {
	r := Report{Version: 1, TenantID: in.tenant, SourceSchemaVersion: in.sourceVersion, TargetSchemaVersion: in.targetVersion, SourceBound: in.boundTenant != "", TargetState: in.targetState, CheckedAt: at.UTC(), ReadOnly: true, RequiresCutoverValidation: true, Consistency: "independent_repeatable_read_snapshots", Issues: []Issue{}}
	r.Counts.SourceUsers = len(in.source)
	r.Counts.TargetAccounts = len(in.target)
	sort.Slice(in.source, func(i, j int) bool { return in.source[i].id < in.source[j].id })
	sort.Slice(in.target, func(i, j int) bool { return in.target[i].id < in.target[j].id })
	candidate := map[string]bool{}
	invalid := map[string]bool{}
	add := func(code, severity string, users, accounts []string) {
		if severity == "error" {
			r.Errors++
			for _, id := range users {
				invalid[id] = true
			}
		} else {
			r.Warnings++
		}
		if len(r.Issues) < MaxIssues {
			r.Issues = append(r.Issues, Issue{Code: code, Severity: severity,
				SourceUserIDs: users[:min(len(users), MaxIssueReferences)], TargetAccountIDs: accounts[:min(len(accounts), MaxIssueReferences)],
				SourceUserCount: len(users), TargetAccountCount: len(accounts), ReferencesTruncated: len(users) > MaxIssueReferences || len(accounts) > MaxIssueReferences})
		} else {
			r.IssuesTruncated = true
		}
	}
	if in.boundTenant != "" && in.boundTenant != in.tenant {
		add("SOURCE_TENANT_MISMATCH", "error", nil, nil)
	}
	if in.boundTenant == "" {
		add("SOURCE_ADOPTION_REQUIRED", "warning", nil, nil)
	}
	if in.targetState != "provisioning" && in.targetState != "suspended" {
		add("TARGET_MAINTENANCE_REQUIRED", "warning", nil, nil)
	}
	targetByID := map[string]targetAccount{}
	targetByPhone := map[string][]string{}
	targetByLocal := map[string][]string{}
	for _, a := range in.target {
		targetByID[a.id] = a
		phone, e := platform.NormalizePhone(a.phone)
		if e != nil || phone != a.phone {
			add("TARGET_PHONE_NOT_CANONICAL", "error", nil, []string{a.id})
		}
		if e == nil {
			targetByPhone[phone] = append(targetByPhone[phone], a.id)
		}
		if a.tenant == in.tenant {
			targetByLocal[a.localID] = append(targetByLocal[a.localID], a.id)
		}
	}
	for _, phone := range sortedKeys(targetByPhone) {
		if ids := targetByPhone[phone]; len(ids) > 1 {
			add("TARGET_PHONE_COLLISION", "error", nil, ids)
		}
	}
	sourceByPhone := map[string][]string{}
	for _, u := range in.source {
		if u.deleted {
			r.Counts.DeletedUsers++
			continue
		}
		if u.state == "retired" {
			r.Counts.RetiredUsers++
			continue
		}
		if !tenancy.ValidID(u.id) {
			add("SOURCE_ID_UNSUPPORTED", "error", []string{u.id}, nil)
		}
		phone, e := platform.NormalizePhone(u.phone)
		if e != nil {
			add("SOURCE_PHONE_INVALID", "error", []string{u.id}, nil)
		} else {
			sourceByPhone[phone] = append(sourceByPhone[phone], u.id)
			if phone != u.phone {
				r.Counts.NormalizedPhones++
				add("SOURCE_PHONE_WILL_NORMALIZE", "warning", []string{u.id}, nil)
			}
		}
		if u.platformID != "" {
			r.Counts.LinkedUsers++
			a, ok := targetByID[u.platformID]
			if !ok || e != nil || a.phone != phone || a.tenant != in.tenant || a.localID != u.id || a.assignment != u.assignment || a.auth != u.auth {
				add("LINKED_IDENTITY_MISMATCH", "error", []string{u.id}, []string{u.platformID})
			}
			stable := a.state == "active" && (u.state == "active" || u.state == "prepared") || a.state == "blocked" && (u.state == "active" || u.state == "platform_blocked")
			if !stable {
				add("LINKED_IDENTITY_BUSY", "error", []string{u.id}, []string{u.platformID})
			}
			continue
		}
		r.Counts.Candidates++
		candidate[u.id] = true
		if u.state != "active" || u.assignment != 1 || u.auth != 1 {
			add("SOURCE_IDENTITY_NOT_PRISTINE", "error", []string{u.id}, nil)
		}
		if ids := targetByLocal[u.id]; len(ids) > 0 {
			add("TARGET_LOCAL_ID_OCCUPIED", "error", []string{u.id}, ids)
		}
		if e == nil {
			if ids := targetByPhone[phone]; len(ids) > 0 {
				add("TARGET_PHONE_OCCUPIED", "error", []string{u.id}, ids)
			}
		}
		if u.passwordHash == "" {
			r.Counts.Passwordless++
			add("PASSWORDLESS_LOGIN_PATH_REQUIRED", "warning", []string{u.id}, nil)
		} else {
			cost, err := hashCost(u.passwordHash)
			if err != nil {
				add("PASSWORD_HASH_UNSUPPORTED", "error", []string{u.id}, nil)
			} else if cost > 16 {
				add("PASSWORD_HASH_COST_UNSAFE", "error", []string{u.id}, nil)
			} else if cost < 12 {
				add("PASSWORD_HASH_LEGACY_COST", "warning", []string{u.id}, nil)
			}
		}
		if u.banned {
			r.Counts.Banned++
			if u.bannedUntil != nil {
				if u.bannedUntil.After(at) {
					r.Counts.TemporaryBans++
					add("TEMPORARY_BAN_EXPIRY_MUST_BE_PRESERVED", "warning", []string{u.id}, nil)
				} else {
					add("EXPIRED_BAN_REQUIRES_RECONCILIATION", "warning", []string{u.id}, nil)
				}
			}
		} else if u.bannedUntil != nil {
			add("BAN_FLAG_AND_EXPIRY_INCONSISTENT", "warning", []string{u.id}, nil)
		}
	}
	for _, phone := range sortedKeys(sourceByPhone) {
		if ids := sourceByPhone[phone]; len(ids) > 1 {
			add("SOURCE_PHONE_COLLISION", "error", ids, nil)
		}
	}
	for id := range candidate {
		if !invalid[id] {
			r.Counts.ValidCandidates++
		}
	}
	r.DataChecksPassed = r.Errors == 0
	r.Fingerprint = fingerprintInventory(in)
	return r
}
func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// This is a comparison fingerprint, not a credential or an authorization token.
// A later importer must reread both databases and enforce transaction fences.
func fingerprintInventory(in inventory) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode([]any{"default-enterprise-preflight-v1", in.tenant, in.boundTenant, in.targetState, in.sourceVersion, in.targetVersion})
	for _, u := range in.source {
		digest := sha256.Sum256([]byte(u.passwordHash))
		_ = enc.Encode([]any{u.id, u.phone, hex.EncodeToString(digest[:]), u.platformID, u.state, u.assignment, u.auth, u.banned, u.deleted, u.bannedUntil})
	}
	for _, a := range in.target {
		_ = enc.Encode([]any{a.id, a.phone, a.tenant, a.localID, a.state, a.assignment, a.auth})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Summary contains only counts. Detailed user/account IDs belong in an explicitly
// requested private report, never the default terminal output.
func (r Report) Summary() string {
	b, _ := json.Marshal(struct {
		Version     int    `json:"version"`
		Passed      bool   `json:"dataChecksPassed"`
		ReadOnly    bool   `json:"readOnly"`
		Counts      Counts `json:"counts"`
		Errors      int    `json:"errors"`
		Warnings    int    `json:"warnings"`
		Fingerprint string `json:"fingerprint"`
	}{r.Version, r.DataChecksPassed, true, r.Counts, r.Errors, r.Warnings, r.Fingerprint})
	return strings.TrimSpace(string(b))
}
