package httpapi

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/legacyimport"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/privatefile"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/tenancy"
	"github.com/linli/im/server/internal/wukong"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// This opt-in drill only operates on separately restored loopback databases.
// It starts no business listener or background dispatcher, uses real IM/RTC/S3
// controls and never enables the imported tenant or calls a push supplier.
type snapshotDrillConfig struct {
	Original, Enterprise, Platform, ReportDirectory string
	IMURL, IMTCP, RTCURL, RTCKey, RTCSecret         string
	S3Endpoint, S3Key, S3Secret                     string
	BatchLimit                                      int
	QuarantineUserIDs                               []string
	AllowPasswordless                               bool
	OldClientVersion                                string
}

func snapshotLocalURL(raw, scheme string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == scheme && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback() && u.Fragment == ""
}

func snapshotPool(t *testing.T, raw, database string, readonly bool) *pgxpool.Pool {
	t.Helper()
	u, e := url.Parse(raw)
	if e != nil || !snapshotLocalURL(raw, "postgres") || u.Path != "/"+database || u.RawQuery != "sslmode=disable" {
		t.Fatal("drill requires the exact isolated loopback database")
	}
	var p *pgxpool.Pool
	if readonly {
		p, e = legacyimport.OpenLocalReadOnly(t.Context(), raw)
	} else {
		p, e = legacyimport.OpenLocalForImport(t.Context(), raw)
	}
	if e != nil {
		t.Fatal("drill database unavailable")
	}
	t.Cleanup(p.Close)
	return p
}

func snapshotReport(t *testing.T, dir, name string, data any) {
	t.Helper()
	f, e := privatefile.Create(filepath.Join(dir, name))
	if e != nil {
		t.Fatal("new private report could not be created")
	}
	e = json.NewEncoder(f).Encode(data)
	ce := f.Close()
	if e != nil || ce != nil {
		t.Fatal("private report write failed")
	}
}

// Compare old columns only: migrations may add columns but must preserve
// existing relationships, message references and media keys byte for byte.
func snapshotTableDigest(t *testing.T, p *pgxpool.Pool, table string, columns []string) string {
	t.Helper()
	quoted := make([]string, len(columns))
	for i, c := range columns {
		quoted[i] = pgx.Identifier{c}.Sanitize()
	}
	sql := `COPY (SELECT row_to_json(r)::text FROM (SELECT ` + strings.Join(quoted, ",") + ` FROM ` + pgx.Identifier{table}.Sanitize() + `) r ORDER BY row_to_json(r)::text) TO STDOUT`
	c, e := p.Acquire(t.Context())
	if e != nil {
		t.Fatal("digest connection failed")
	}
	defer c.Release()
	h := sha256.New()
	if _, e = c.Conn().PgConn().CopyTo(t.Context(), h, sql); e != nil {
		t.Fatal("table digest failed", table)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestLegacySnapshotRehearsal(t *testing.T) {
	if os.Getenv("TENANCY_LEGACY_SNAPSHOT_DRILL") != "isolated-real-copy" {
		t.Skip("explicit real snapshot drill not configured")
	}
	path := os.Getenv("TENANCY_LEGACY_SNAPSHOT_CONFIG")
	if !filepath.IsAbs(path) {
		t.Fatal("absolute private drill configuration required")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal("configuration unavailable")
	}
	var c snapshotDrillConfig
	if json.Unmarshal(raw, &c) != nil || !filepath.IsAbs(c.ReportDirectory) || !snapshotLocalURL(c.IMURL, "http") || !snapshotLocalURL(c.RTCURL, "http") || !snapshotLocalURL("http://"+c.S3Endpoint, "http") {
		t.Fatal("invalid isolated service configuration")
	}
	original := snapshotPool(t, c.Original, "rehearsal_original", true)
	source := snapshotPool(t, c.Enterprise, "rehearsal_enterprise", false)
	target := snapshotPool(t, c.Platform, "rehearsal_platform", false)
	ctx := t.Context()
	ps, e := platform.Open(ctx, c.Platform)
	if e != nil {
		t.Fatal("platform schema failed")
	}
	t.Cleanup(ps.Close)
	const actor = "snapshot-operator"
	const address = "https://127.0.0.1:19443"
	var exists bool
	if target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_tenants WHERE id='default')`).Scan(&exists) != nil {
		t.Fatal("directory read failed")
	}
	if !exists {
		if e = ps.PutTenant(ctx, "default", "Isolated real snapshot", address, actor, "real snapshot rehearsal only", true); e != nil {
			t.Fatal("tenant preparation failed")
		}
		r, e := legacyimport.Preflight(ctx, source, target, "default", time.Now().UTC())
		snapshotReport(t, c.ReportDirectory, "preflight-before-adoption.json", r)
		t.Log(r.Summary())
		if e != nil || (!r.DataChecksPassed && len(c.QuarantineUserIDs) == 0) {
			t.Fatal("real snapshot inventory rejected; see private preflight issues")
		}
	}
	db, e := store.NewPostgresWithOptions(ctx, c.Enterprise, store.PostgresOptions{TenantID: "default", BindExistingTenant: true})
	if e != nil {
		t.Fatal("legacy schema upgrade/adoption failed")
	}
	t.Cleanup(db.Close)
	application, e := app.New(ctx, db)
	if e != nil {
		t.Fatal("application initialization failed")
	}
	secret, e := tenancy.Secret()
	if e != nil {
		t.Fatal(e)
	}
	cfg := config.Config{TenantID: "default", TenantPublicURL: address, JWTSecret: secret, MediaSigningSecret: secret, AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour, WukongEnabled: true, WukongAPIURL: c.IMURL, WukongTCPURL: c.IMTCP, WukongWSURL: "wss://127.0.0.1:19443/im", WukongTokenSecret: secret, LiveKitEnabled: true, LiveKitAPIURL: c.RTCURL, LiveKitURL: "wss://127.0.0.1:19443/livekit", LiveKitAPIKey: c.RTCKey, LiveKitAPISecret: c.RTCSecret, S3Endpoint: c.S3Endpoint, S3PublicEndpoint: c.S3Endpoint, S3AccessKey: c.S3Key, S3SecretKey: c.S3Secret, S3Bucket: "nexachat-media", S3Region: "us-east-1", HTTPRateLimitPerMinute: 10000}
	cfg.WukongManagerURL = c.IMURL
	cfg.WukongManagerToken = secret
	x := New(cfg, application)
	if err := x.SetupError(); err != nil {
		t.Fatal("real IM setup failed", err)
	}
	x.ConfigureTenant(db, nil)
	pki, _ := tenancyStackPKI(t, "default")
	control := tenancyStackServer(t, x.TenantControlHandler(), pki["default"], true)
	rpc, e := tenancy.NewRPC(control.URL, pki["platform"], tenancy.EnterpriseIdentity("default"))
	if e != nil {
		t.Fatal(e)
	}
	peers := platform.EnterpriseRPC{Peers: map[string]*tenancy.RPC{"default": rpc}}
	var state string
	if target.QueryRow(ctx, `SELECT status FROM platform_tenants WHERE id='default'`).Scan(&state) != nil {
		t.Fatal("tenant status unavailable")
	}
	if state == "provisioning" {
		if e = ps.ActivateTenant(ctx, "default", actor, "empty isolated directory readiness", true, 1, peers.CheckReadiness); e != nil {
			t.Fatal("real dependency readiness failed", e)
		}
		state = "active"
	}
	if state == "active" {
		if _, e = ps.RequestRealm(ctx, "default", "snapshot-pause", actor, "suspend before real identity import", 1, false, true); e != nil {
			t.Fatal("suspension request failed", e)
		}
	}
	rw := platform.RealmWorker{Store: ps, Enterprise: peers}
	deadline := time.Now().Add(10 * time.Minute)
	for state != "suspended" && time.Now().Before(deadline) {
		if _, e = rw.Once(ctx); e != nil {
			t.Fatal("real suspension worker failed", e)
		}
		if target.QueryRow(ctx, `SELECT status FROM platform_tenants WHERE id='default'`).Scan(&state) != nil {
			t.Fatal("suspension status failed")
		}
		if state != "suspended" {
			time.Sleep(time.Second)
		}
	}
	if state != "suspended" {
		t.Fatal("suspension remained unconfirmed; no import attempted")
	}
	if len(c.QuarantineUserIDs) > 0 {
		// Persist the original immutable request so a process restart repeats
		// the same operator decision instead of accepting a changed inventory.
		qpath := filepath.Join(c.ReportDirectory, "quarantine-request.json")
		var q legacyimport.QuarantineRequest
		if data, e := os.ReadFile(qpath); e == nil {
			if json.Unmarshal(data, &q) != nil {
				t.Fatal("quarantine receipt invalid")
			}
		} else if os.IsNotExist(e) {
			r, e := legacyimport.Preflight(ctx, source, target, "default", time.Now().UTC())
			if e != nil {
				t.Fatal("quarantine preflight failed")
			}
			q = legacyimport.QuarantineRequest{ID: "reviewed-invalid-phones", TenantID: "default", Actor: actor, Reason: "user approved retaining history and disabling invalid phone identities until verified recovery", ExpectedFingerprint: r.Fingerprint, UserIDs: c.QuarantineUserIDs, Confirmed: true}
			snapshotReport(t, c.ReportDirectory, "quarantine-request.json", q)
		} else {
			t.Fatal("quarantine request unavailable")
		}
		if n, e := legacyimport.QuarantineInvalidPhones(ctx, source, target, q); e != nil || n != len(c.QuarantineUserIDs) {
			t.Fatal("audited quarantine failed", e)
		}
	}
	const batch = "real-snapshot-import"
	b, e := legacyimport.ImportStatus(ctx, target, batch)
	if e != nil {
		r, e := legacyimport.Preflight(ctx, source, target, "default", time.Now().UTC())
		snapshotReport(t, c.ReportDirectory, "preflight-adopted.json", r)
		if e != nil || !r.DataChecksPassed {
			t.Fatal("adopted inventory rejected")
		}
		// Passwordless accounts require an explicit operator workflow; never
		// silently invent credentials or mark the snapshot safe to activate.
		if r.Counts.Passwordless != 0 && !c.AllowPasswordless {
			t.Fatal("passwordless accounts need an explicit reviewed import policy")
		}
		b, e = legacyimport.StartImport(ctx, source, target, legacyimport.ImportRequest{ID: batch, TenantID: "default", Actor: actor, Reason: "offline real snapshot rehearsal", ExpectedFingerprint: r.Fingerprint, Confirmed: true, AllowPasswordless: c.AllowPasswordless})
		if e != nil {
			t.Fatal("import reservation failed", e)
		}
	}
	processed := 0
	for b.State != "completed" {
		if c.BatchLimit > 0 && processed >= c.BatchLimit {
			snapshotReport(t, c.ReportDirectory, "interrupted-import.json", b)
			t.Log("Deliberate process boundary; import is incomplete and tenant remains suspended")
			return
		}
		ok, e := legacyimport.ResumeImportOne(ctx, source, target, batch, peers)
		if e != nil {
			t.Fatal("real credential revocation unconfirmed", e)
		}
		if !ok {
			time.Sleep(time.Second)
		} else {
			processed++
		}
		b, e = legacyimport.ImportStatus(ctx, target, batch)
		if e != nil {
			t.Fatal("import status failed")
		}
	}
	// Compare every original user's preserved identity and credential directly
	// in memory. Neither hashes nor phone numbers enter reports or test output.
	rows, e := original.Query(ctx, `SELECT id,phone,password_hash,banned,banned_until,deleted_at FROM im_users ORDER BY id`)
	if e != nil {
		t.Fatal("original users unavailable")
	}
	defer rows.Close()
	checked, deleted, quarantined := 0, 0, 0
	quarantineIDs := map[string]bool{}
	for _, id := range c.QuarantineUserIDs {
		quarantineIDs[id] = true
	}
	for rows.Next() {
		var id, phone, hash string
		var banned bool
		var until, gone *time.Time
		if rows.Scan(&id, &phone, &hash, &banned, &until, &gone) != nil {
			t.Fatal("original identity read failed")
		}
		var retained bool
		if quarantineIDs[id] {
			if source.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1 AND phone=$2 AND banned AND banned_until IS NULL AND deleted_at IS NULL AND password_hash='' AND local_identity_state='retired' AND platform_account_id IS NULL)`, id, phone).Scan(&retained) != nil || !retained {
				t.Fatal("quarantined record was changed or enabled")
			}
			quarantined++
			continue
		}
		if source.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1 AND banned=$2 AND banned_until IS NOT DISTINCT FROM $3 AND deleted_at IS NOT DISTINCT FROM $4)`, id, banned, until, gone).Scan(&retained) != nil || !retained {
			t.Fatal("user ID or restriction changed")
		}
		if gone != nil {
			deleted++
			continue
		}
		normalized, e := platform.NormalizePhone(phone)
		if e != nil {
			t.Fatal("unexpected phone normalization failure")
		}
		if target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_accounts WHERE local_user_id=$1 AND tenant_id='default' AND phone=$2 AND password_hash=$3 AND auth_version=2)`, id, normalized, hash).Scan(&retained) != nil || !retained {
			t.Fatal("import changed identity or original password hash")
		}
		checked++
	}
	if rows.Err() != nil {
		t.Fatal("original identity cursor failed")
	}
	var unsafe int
	if source.QueryRow(ctx, `SELECT (SELECT count(*) FROM im_refresh_sessions WHERE revoked_at IS NULL)+(SELECT count(*) FROM im_devices WHERE push_token<>'' OR notifications_enabled)+(SELECT count(*) FROM im_users WHERE deleted_at IS NULL AND password_hash<>'')`).Scan(&unsafe) != nil || unsafe != 0 {
		t.Fatal("old authentication or device bindings survived import")
	}
	for _, path := range []string{"/v2/auth/password-login", "/v2/auth/refresh", "/v2/users/me/devices"} {
		req, _ := http.NewRequest(http.MethodPost, "https://127.0.0.1"+path, strings.NewReader(`{}`))
		rec := &snapshotRecorder{header: make(http.Header)}
		if x.allowTenantRoute(rec, req) || rec.code != 409 {
			t.Fatal("legacy authentication route remained available", path)
		}
	}
	tables := map[string]string{}
	for _, table := range []string{"im_friendships", "im_friend_requests", "im_groups", "im_members", "im_conversations", "im_direct_index", "im_media", "im_wukong_media_channels", "im_wukong_message_extensions", "im_wukong_message_index", "im_message_reactions", "im_message_edits", "im_group_message_pins"} {
		cols, e := original.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position`, table)
		if e != nil {
			t.Fatal("column inventory failed")
		}
		var names []string
		for cols.Next() {
			var col string
			if cols.Scan(&col) != nil {
				t.Fatal("column read failed")
			}
			names = append(names, col)
		}
		err := cols.Err()
		cols.Close()
		if err != nil {
			t.Fatal("column inventory failed")
		}
		if len(names) == 0 {
			t.Fatal("required historical business table missing", table)
		}
		a := snapshotTableDigest(t, original, table, names)
		z := snapshotTableDigest(t, source, table, names)
		if a != z {
			t.Fatal("historical business table changed", table)
		}
		tables[table] = a
	}
	r, e := legacyimport.Preflight(ctx, source, target, "default", time.Now().UTC())
	if e != nil || !r.DataChecksPassed {
		t.Fatal("final identity reconciliation failed")
	}
	var enabled bool
	if source.QueryRow(ctx, `SELECT access_enabled FROM im_tenant_identity WHERE tenant_id='default'`).Scan(&enabled) != nil || enabled {
		t.Fatal("drill activated enterprise")
	}
	snapshotReport(t, c.ReportDirectory, "migration-completed.json", map[string]any{"status": "passed-isolated-import", "batch": b, "identities": checked, "deletedPreserved": deleted, "quarantined": quarantined, "businessTables": tables, "tenantEnabled": false, "externalClients": "pending", "voip": "pending", "checkedAt": time.Now().UTC()})
	t.Logf("Real snapshot imported: %d identities, %d deleted records retained; tenant remains suspended", checked, deleted)
}

func TestLegacySnapshotMemberships(t *testing.T) {
	if os.Getenv("TENANCY_LEGACY_SNAPSHOT_DRILL") != "isolated-real-copy" {
		t.Skip("explicit real snapshot drill not configured")
	}
	path := os.Getenv("TENANCY_LEGACY_SNAPSHOT_CONFIG")
	if !filepath.IsAbs(path) {
		t.Fatal("private config required")
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal("configuration unavailable")
	}
	var c snapshotDrillConfig
	if json.Unmarshal(data, &c) != nil || !filepath.IsAbs(c.ReportDirectory) {
		t.Fatal("invalid config")
	}
	a := snapshotPool(t, c.Original, "rehearsal_original", true)
	b := snapshotPool(t, c.Enterprise, "rehearsal_enterprise", true)
	result := map[string]string{}
	for _, table := range []string{"im_members", "im_conversations", "im_direct_index"} {
		rows, e := a.Query(t.Context(), `SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position`, table)
		if e != nil {
			t.Fatal("relationship columns unavailable")
		}
		var cols []string
		for rows.Next() {
			var col string
			if rows.Scan(&col) != nil {
				t.Fatal("relationship columns unreadable")
			}
			cols = append(cols, col)
		}
		err := rows.Err()
		rows.Close()
		if err != nil || len(cols) == 0 {
			t.Fatal("required relationship table missing")
		}
		h := snapshotTableDigest(t, a, table, cols)
		if h != snapshotTableDigest(t, b, table, cols) {
			t.Fatal("historical relationship changed", table)
		}
		result[table] = h
	}
	snapshotReport(t, c.ReportDirectory, "membership-relations.json", result)
	t.Log("All original member, conversation and direct-chat mappings match the migrated copy")
}

type snapshotRecorder struct {
	header http.Header
	code   int
}

func (s *snapshotRecorder) Header() http.Header         { return s.header }
func (s *snapshotRecorder) WriteHeader(n int)           { s.code = n }
func (s *snapshotRecorder) Write(p []byte) (int, error) { return len(p), nil }

// Restore the original dump into a separate rollback database before running
// this check. Comparing every table detects more than matching user counts.
// Passing this check proves database restoration, not a public routing rollback.
func TestLegacySnapshotDatabaseRestore(t *testing.T) {
	if os.Getenv("TENANCY_LEGACY_SNAPSHOT_DRILL") != "isolated-real-copy" {
		t.Skip("explicit real snapshot drill not configured")
	}
	path := os.Getenv("TENANCY_LEGACY_SNAPSHOT_CONFIG")
	if !filepath.IsAbs(path) {
		t.Fatal("absolute private configuration required")
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal("configuration unavailable")
	}
	var c snapshotDrillConfig
	if json.Unmarshal(data, &c) != nil || !filepath.IsAbs(c.ReportDirectory) {
		t.Fatal("invalid drill configuration")
	}
	a := snapshotPool(t, c.Original, "rehearsal_original", true)
	u, e := url.Parse(c.Original)
	if e != nil {
		t.Fatal("invalid original address")
	}
	u.Path = "/rehearsal_rollback"
	b := snapshotPool(t, u.String(), "rehearsal_rollback", true)
	rows, e := a.Query(t.Context(), `SELECT c.table_name,c.column_name FROM information_schema.columns c JOIN information_schema.tables t USING(table_schema,table_name) WHERE c.table_schema='public' AND t.table_type='BASE TABLE' ORDER BY c.table_name,c.ordinal_position`)
	if e != nil {
		t.Fatal("original table inventory failed")
	}
	columns := map[string][]string{}
	for rows.Next() {
		var table, col string
		if rows.Scan(&table, &col) != nil {
			t.Fatal("table inventory failed")
		}
		columns[table] = append(columns[table], col)
	}
	err := rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal("table inventory failed")
	}
	tables := map[string]string{}
	for table, cols := range columns {
		left := snapshotTableDigest(t, a, table, cols)
		if left != snapshotTableDigest(t, b, table, cols) {
			t.Fatal("rollback restore changed table", table)
		}
		tables[table] = left
	}
	for _, p := range []*pgxpool.Pool{a, b} {
		var version int
		if p.QueryRow(t.Context(), `SELECT max(version) FROM im_schema_migrations`).Scan(&version) != nil || version != 72 {
			t.Fatal("original schema was changed")
		}
	}
	snapshotReport(t, c.ReportDirectory, "database-restore.json", map[string]any{"status": "passed-database-restore", "tables": tables, "schema": 72, "checkedAt": time.Now().UTC(), "routeRollback": "pending", "productionModified": false})
	t.Logf("Restored %d tables match original snapshot; this is database recovery, not public cutover rollback", len(tables))
}

func TestLegacySnapshotMediaAndHistory(t *testing.T) {
	if os.Getenv("TENANCY_LEGACY_SNAPSHOT_DRILL") != "isolated-real-copy" {
		t.Skip("explicit real snapshot drill not configured")
	}
	path := os.Getenv("TENANCY_LEGACY_SNAPSHOT_CONFIG")
	if !filepath.IsAbs(path) {
		t.Fatal("private config required")
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal("configuration unavailable")
	}
	var c snapshotDrillConfig
	if json.Unmarshal(data, &c) != nil || !filepath.IsAbs(c.ReportDirectory) || !snapshotLocalURL("http://"+c.S3Endpoint, "http") || !snapshotLocalURL(c.IMURL, "http") {
		t.Fatal("invalid private configuration")
	}
	p := snapshotPool(t, c.Original, "rehearsal_original", true)
	s3, e := minio.New(c.S3Endpoint, &minio.Options{Creds: credentials.NewStaticV4(c.S3Key, c.S3Secret, ""), Secure: false})
	if e != nil {
		t.Fatal("S3 client failed")
	}
	f, e := os.Open(filepath.Join(c.ReportDirectory, "full-snapshot", "SHA256SUMS"))
	if e != nil {
		t.Fatal("verified snapshot manifest unavailable")
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	objects := 0
	var bytes int64
	for scan.Scan() {
		line := scan.Text()
		if len(line) < 67 {
			t.Fatal("malformed snapshot manifest")
		}
		name := strings.TrimPrefix(line[66:], "./")
		if !strings.HasPrefix(name, "minio/") {
			continue
		}
		key := strings.TrimPrefix(name, "minio/")
		obj, e := s3.GetObject(t.Context(), "nexachat-media", key, minio.GetObjectOptions{})
		if e != nil {
			t.Fatal("historical S3 object unavailable")
		}
		h := sha256.New()
		n, e := io.Copy(h, obj)
		_ = obj.Close()
		if e != nil || hex.EncodeToString(h.Sum(nil)) != line[:64] {
			t.Fatal("historical S3 content differs from backup")
		}
		objects++
		bytes += n
	}
	if scan.Err() != nil || objects == 0 {
		t.Fatal("no media objects verified")
	}
	client, e := wukong.NewClient(wukong.Config{APIURL: c.IMURL, ManagerURL: c.IMURL, ManagerToken: "isolated-rehearsal", Timeout: 5 * time.Second})
	if e != nil {
		t.Fatal("real IM client failed")
	}
	rows, e := p.Query(t.Context(), `SELECT sender_id,channel_id,message_seq,client_msg_no FROM im_wukong_message_index WHERE channel_type=2 AND message_seq>0 AND expired_at IS NULL AND expires_at IS NULL ORDER BY message_timestamp DESC,message_seq DESC LIMIT 100`)
	if e != nil {
		t.Fatal("historical message index unavailable")
	}
	defer rows.Close()
	matched := 0
	historyDigest := sha256.New()
	for rows.Next() {
		var sender, channel, clientNo string
		var seq uint32
		if rows.Scan(&sender, &channel, &seq, &clientNo) != nil {
			t.Fatal("message index read failed")
		}
		messages, e := client.SearchMessages(t.Context(), wukong.MessageSearchRequest{LoginUID: sender, ChannelID: channel, ChannelType: 2, MessageSeqs: []uint32{seq}})
		if e != nil || len(messages) != 1 || messages[0]["client_msg_no"] != clientNo {
			t.Fatal("restored IM history does not match database index")
		}
		if json.NewEncoder(historyDigest).Encode(messages[0]) != nil {
			t.Fatal("history digest failed")
		}
		matched++
	}
	if rows.Err() != nil || matched == 0 {
		t.Fatal("no IM historical messages verified")
	}
	snapshotReport(t, c.ReportDirectory, "media-history-restore.json", map[string]any{"status": "passed", "objects": objects, "mediaBytes": bytes, "sampledGroupMessages": matched, "messageSampleSHA256": hex.EncodeToString(historyDigest.Sum(nil)), "bucket": "nexachat-media", "objectKeysPreserved": true, "checkedAt": time.Now().UTC()})
	t.Logf("Verified %d original S3 objects and %d restored group messages against backup/index", objects, matched)
}

// The gateway and legacy API below are actual isolated Docker services, not
// mocked health responses. No public port is published by this drill.
func TestLegacySnapshotRollbackRoute(t *testing.T) {
	if os.Getenv("TENANCY_LEGACY_SNAPSHOT_DRILL") != "isolated-real-copy" {
		t.Skip("explicit real snapshot drill not configured")
	}
	path := os.Getenv("TENANCY_LEGACY_SNAPSHOT_CONFIG")
	if !filepath.IsAbs(path) {
		t.Fatal("private config required")
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal("configuration unavailable")
	}
	var c snapshotDrillConfig
	if json.Unmarshal(data, &c) != nil || !filepath.IsAbs(c.ReportDirectory) || c.OldClientVersion == "" {
		t.Fatal("invalid drill config")
	}
	ctx := t.Context()
	original := snapshotPool(t, c.Original, "rehearsal_original", true)
	source := snapshotPool(t, c.Enterprise, "rehearsal_enterprise", false)
	target := snapshotPool(t, c.Platform, "rehearsal_platform", false)
	o, e := legacyimport.DatabaseIdentity(ctx, original)
	if e != nil {
		t.Fatal("original database identity unavailable")
	}
	n, e := legacyimport.DatabaseIdentity(ctx, source)
	if e != nil {
		t.Fatal("candidate database identity unavailable")
	}
	r := legacyimport.CutoverRequest{ID: "real-snapshot-cutover", TenantID: "default", BatchID: "real-snapshot-import", Actor: "snapshot-operator", Reason: "isolated real backup migration and pre-opening rollback rehearsal", OriginalDatabase: o, AdoptedDatabase: n, OldVersions: map[string]string{"android": c.OldClientVersion, "ios": c.OldClientVersion, "web": c.OldClientVersion}}
	status, e := legacyimport.StartCutover(ctx, target, r, true)
	if e != nil {
		t.Fatal("cutover receipt failed", e)
	}
	proof := map[string]string{}
	for _, name := range []string{"backup-verified.json", "database-restore.json", "migration-completed.json", "media-history-restore.json"} {
		b, e := os.ReadFile(filepath.Join(c.ReportDirectory, name))
		if e != nil {
			t.Fatal("required completed rehearsal proof missing")
		}
		h := sha256.Sum256(b)
		proof[name] = hex.EncodeToString(h[:])
	}
	b, _ := json.Marshal(map[string]any{"scope": "isolated-copy-only", "proof": proof, "redisAndConfig": "separately captured rehearsal supplement; final stopped snapshot still required"})
	h := sha256.Sum256(b)
	digest := hex.EncodeToString(h[:])
	for _, phase := range []string{"stopped", "backed_up", "adopted", "imported"} {
		status, e = legacyimport.AdvanceCutover(ctx, source, target, r, legacyimport.CutoverStep{ExpectedPhase: status.Phase, Phase: phase, Reason: "verified isolated rehearsal evidence", EvidenceSHA256: digest, Confirmed: true})
		if e != nil {
			t.Fatal("cutover phase failed", phase, e)
		}
	}
	ps, e := platform.Open(ctx, c.Platform)
	if e != nil {
		t.Fatal("directory unavailable")
	}
	defer ps.Close()
	if _, e = ps.RequestRealm(ctx, "default", "forbidden-early-opening", "snapshot-operator", "verify opening boundary", 2, true, true); e == nil {
		t.Fatal("resume bypassed missing opening receipt")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	statusCode := func(address string) int {
		res, e := client.Get(address)
		if e != nil {
			return 0
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	if statusCode("http://127.0.0.1:18088/ready") != 503 {
		t.Fatal("rollback gateway was not in maintenance")
	}
	if statusCode("http://127.0.0.1:18080/ready") != 200 {
		t.Fatal("restored original API not ready")
	}
	status, e = legacyimport.AdvanceCutover(ctx, source, target, r, legacyimport.CutoverStep{ExpectedPhase: "imported", Phase: "rolled_back", Reason: "candidate kept suspended; restored original stack ready; opening never entered", EvidenceSHA256: digest, Confirmed: true})
	if e != nil {
		t.Fatal("pre-opening rollback rejected", e)
	}
	// Reload only the named private rehearsal gateway's loopback admin port.
	config := []byte(`{"admin":{"listen":"127.0.0.1:2018"},"apps":{"http":{"servers":{"drill":{"listen":["127.0.0.1:18088"],"routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:18080"}]}]}]}}}}}`)
	res, e := client.Post("http://127.0.0.1:2018/load", "application/json", bytes.NewReader(config))
	if e != nil {
		t.Fatal("private gateway reload failed")
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("private gateway rejected rollback config")
	}
	if statusCode("http://127.0.0.1:18088/ready") != 200 {
		t.Fatal("gateway did not reach restored original API")
	}
	var closed bool
	if source.QueryRow(ctx, `SELECT NOT access_enabled FROM im_tenant_identity WHERE tenant_id='default'`).Scan(&closed) != nil || !closed {
		t.Fatal("candidate enterprise was reopened")
	}
	var version int
	if original.QueryRow(ctx, `SELECT max(version) FROM im_schema_migrations`).Scan(&version) != nil || version != 72 {
		t.Fatal("original snapshot changed")
	}
	snapshotReport(t, c.ReportDirectory, "rollback-completed.json", map[string]any{"status": "passed-isolated-pre-opening-rollback", "receipt": status, "oldAPIReady": true, "gatewayMaintenanceToLegacy": true, "candidateDisabled": true, "originalSchema": version, "publicServerChanged": false, "checkedAt": time.Now().UTC()})
	t.Log("Actual legacy API readiness and Caddy rollback passed; imported candidate stayed disabled; opening was never entered")
}
