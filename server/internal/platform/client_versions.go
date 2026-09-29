package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/clientversion"
	"github.com/linli/im/server/internal/tenancy"
)

var ErrReleaseChanged = errors.New("client release policy changed")

type ClientRelease struct {
	clientversion.Policy
	Enabled  bool  `json:"enabled"`
	Revision int64 `json:"revision"`
}
type ClientReleaseInput struct {
	Enabled           bool   `json:"enabled"`
	MinimumVersion    string `json:"minimumVersion"`
	LatestVersion     string `json:"latestVersion"`
	ForceUpdate       bool   `json:"forceUpdate"`
	RolloutPercentage int    `json:"rolloutPercentage"`
	ReleaseNotes      string `json:"releaseNotes"`
	DownloadURL       string `json:"downloadUrl"`
}
type ClientReleaseWrite struct {
	ClientReleaseInput
	RequestID        string `json:"requestId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Reason           string `json:"reason"`
	Confirmed        bool   `json:"confirmed"`
}

func (s *Store) ClientRelease(ctx context.Context, platform string) (ClientRelease, error) {
	var r ClientRelease
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT enabled,revision,policy,updated_at FROM platform_client_version_policies WHERE platform=$1`, platform).Scan(&r.Enabled, &r.Revision, &raw, &r.UpdatedAt)
	if err != nil {
		return r, err
	}
	at := r.UpdatedAt
	if err = json.Unmarshal(raw, &r.Policy); err != nil {
		return r, err
	}
	r.UpdatedAt = at
	return r, nil
}

// Policy, immutable release snapshot and audit commit together. The request
// lock and immutable response make a lost commit response safe to retry.
func (s *Store) PublishClientRelease(ctx context.Context, platform, actor string, in ClientReleaseWrite) (ClientRelease, error) {
	var zero ClientRelease
	p := clientversion.Normalize(clientversion.Policy{Platform: platform, MinimumVersion: in.MinimumVersion, LatestVersion: in.LatestVersion, ForceUpdate: in.ForceUpdate, RolloutPercentage: in.RolloutPercentage, ReleaseNotes: in.ReleaseNotes, DownloadURL: in.DownloadURL})
	in.Reason = strings.TrimSpace(in.Reason)
	if !tenancy.ValidID(actor) || !tenancy.ValidID(in.RequestID) || !adminReason(in.Reason, in.Confirmed) || in.ExpectedRevision < 0 || !clientversion.Valid(p) || p.Platform != platform || len(p.DownloadURL) > 2048 {
		return zero, tenancy.ErrInvalid
	}
	// Enabling even an optional release requires a usable HTTPS destination.
	// No remote fetch: this action does not validate or upload an APK/IPA.
	if in.Enabled && p.DownloadURL == "" {
		return zero, tenancy.ErrInvalid
	}
	if p.DownloadURL != "" {
		u, e := url.Parse(p.DownloadURL)
		if e != nil || u.Hostname() == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(p.DownloadURL, "#") {
			return zero, tenancy.ErrInvalid
		}
	}
	in.MinimumVersion, in.LatestVersion, in.ReleaseNotes, in.DownloadURL = p.MinimumVersion, p.LatestVersion, p.ReleaseNotes, p.DownloadURL
	raw, err := json.Marshal(in.ClientReleaseInput)
	if err != nil {
		return zero, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	// Recheck the actor inside the publishing transaction; role revocation and
	// publication serialize on the admin row, not a stale middleware result.
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT enabled AND role='operator' FROM platform_admin_accounts WHERE id=$1 FOR SHARE`, actor).Scan(&allowed); errors.Is(err, pgx.ErrNoRows) || (err == nil && !allowed) {
		return zero, ErrDenied
	}
	if err != nil {
		return zero, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,78))`, actor+":"+in.RequestID); err != nil {
		return zero, err
	}
	var same bool
	var previous []byte
	err = tx.QueryRow(ctx, `SELECT platform=$3 AND expected_revision=$4 AND input=$5::jsonb AND reason=$6,snapshot FROM platform_client_version_releases WHERE actor_id=$1 AND request_id=$2`, actor, in.RequestID, platform, in.ExpectedRevision, raw, in.Reason).Scan(&same, &previous)
	if err == nil {
		if !same {
			return zero, ErrRequestChanged
		}
		if err = json.Unmarshal(previous, &zero); err != nil {
			return ClientRelease{}, err
		}
		return zero, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	var revision int64
	var enabled bool
	if err = tx.QueryRow(ctx, `SELECT revision,enabled FROM platform_client_version_policies WHERE platform=$1 FOR UPDATE`, platform).Scan(&revision, &enabled); err != nil {
		return zero, err
	}
	if revision != in.ExpectedRevision {
		return zero, ErrReleaseChanged
	}
	p.UpdatedBy = actor
	p.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	r := ClientRelease{Policy: p, Enabled: in.Enabled, Revision: revision + 1}
	snapshot, err := json.Marshal(r)
	if err != nil {
		return zero, err
	}
	id, err := newID("release")
	if err != nil {
		return zero, err
	}
	policy, err := json.Marshal(p)
	if err != nil {
		return zero, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_client_version_releases(id,platform,actor_id,request_id,expected_revision,revision,input,snapshot,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, platform, actor, in.RequestID, revision, r.Revision, raw, snapshot, in.Reason); err != nil {
		return zero, err
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_client_version_policies SET enabled=$2,revision=$3,policy=$4,updated_at=$5 WHERE platform=$1`, platform, r.Enabled, r.Revision, policy, p.UpdatedAt); err != nil {
		return zero, err
	}
	// The immutable history contains release notes and public download URL.
	// The general audit only indexes the release and security-relevant changes.
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,reason,metadata) VALUES($1,'client_version.published',$2,jsonb_build_object('releaseId',$3::text,'platform',$4::text,'beforeRevision',$5::bigint,'revision',$6::bigint,'beforeEnabled',$7::boolean,'enabled',$8::boolean,'forceUpdate',$9::boolean,'minimumVersion',$10::text,'latestVersion',$11::text))`, actor, in.Reason, id, platform, revision, r.Revision, enabled, r.Enabled, p.ForceUpdate, p.MinimumVersion, p.LatestVersion); err != nil {
		return zero, err
	}
	return r, tx.Commit(ctx)
}

func (a *API) clientVersion(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d, err := a.Store.evaluateClientRelease(r.Context(), q.Get("platform"), q.Get("version"), q.Get("installId"))
	if err != nil {
		failure(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, map[string]any{"data": d})
}

func (s *Store) evaluateClientRelease(ctx context.Context, platform, version, installID string) (*clientversion.Decision, error) {
	d, err := clientversion.Evaluate(platform, version, installID, nil)
	if err != nil {
		return nil, tenancy.ErrInvalid
	}
	p, err := s.ClientRelease(ctx, d.Platform)
	if err != nil {
		return nil, ErrUnavailable
	}
	if p.Enabled {
		d, err = clientversion.Evaluate(platform, version, installID, &p.Policy)
		if err != nil {
			return nil, ErrUnavailable
		}
	}
	return d, nil
}

// Only public version metadata crosses this read-only legacy-address bridge.
// Login, refresh, device credentials and version administration never do.
func (a *API) tenantClientVersion(w http.ResponseWriter, r *http.Request) {
	tenant := strings.TrimPrefix(tenancy.PeerIdentity(r), "spiffe://frogim/tenant/")
	var known bool
	if !tenancy.ValidID(tenant) || a.Store.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM platform_tenants WHERE id=$1)`, tenant).Scan(&known) != nil || !known {
		failure(w, ErrDenied)
		return
	}
	var q struct{ Platform, Version, InstallID string }
	if readJSON(w, r, &q) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	d, err := a.Store.evaluateClientRelease(r.Context(), q.Platform, q.Version, q.InstallID)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, d)
}
func (a *API) adminClientVersions(w http.ResponseWriter, r *http.Request) {
	a.adminPage(w, r, `SELECT platform AS ordering,policy||jsonb_build_object('enabled',enabled,'revision',revision,'updatedAt',updated_at) AS data FROM platform_client_version_policies`)
}
func (a *API) adminClientVersionHistory(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("platform")
	if !clientversion.Supported(p) {
		failure(w, tenancy.ErrInvalid)
		return
	}
	a.adminPage(w, r, `SELECT -revision AS ordering,snapshot||jsonb_build_object('id',id,'reason',reason,'requestId',request_id) AS data FROM platform_client_version_releases WHERE platform=$3`, p)
}
func (a *API) adminPublishClientVersion(w http.ResponseWriter, r *http.Request) {
	var in ClientReleaseWrite
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	result, err := a.Store.PublishClientRelease(r.Context(), r.PathValue("platform"), actorID(r), in)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, result)
}
