package directorybackup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linli/im/server/internal/backup"
)

type testDailyRemote struct {
	mu         sync.Mutex
	fail       bool
	enter      chan struct{}
	block      bool
	deliveries map[string]backup.Delivery
	indexes    map[string]backup.Delivery
}

func (r *testDailyRemote) Deliver(ctx context.Context, b backup.Binding, path string, key []byte) (backup.Delivery, error) {
	r.mu.Lock()
	block, enter, fail := r.block, r.enter, r.fail
	r.mu.Unlock()
	if block {
		close(enter)
		<-ctx.Done()
		return backup.Delivery{}, ctx.Err()
	}
	if fail {
		return backup.Delivery{}, backup.ErrOffsite
	}
	p, e := Verify(path, Request{Expected: b, Actor: "test", Reason: "test verification"}, key)
	if e != nil {
		return backup.Delivery{}, e
	}
	d := backup.Delivery{Version: 1, TargetID: "platform-offsite", Binding: b, Manifest: p}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveries[b.BackupID] = d
	return d, nil
}
func (r *testDailyRemote) PublishDailyReceipt(_ context.Context, date string, d backup.Delivery, _ []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.indexes[date]; ok && old != d {
		return backup.ErrOffsite
	}
	r.indexes[date] = d
	return nil
}
func scheduleFixture(t *testing.T) (*Scheduler, *testDailyRemote) {
	t.Helper()
	db := integrationDatabase(t)
	seedDirectory(t, db)
	id, e := db.Inspect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r, b, key := fixture()
	r.Expected.DirectoryID = id.DirectoryID
	o, e := backup.NewOffsite(backup.OffsiteConfig{ID: "platform-offsite", Scope: "platform", DirectoryID: id.DirectoryID, Endpoint: "https://backup.example.test", Bucket: "isolated-backups", Prefix: "frogim", Region: "us-east-1", AccessKey: "test-access", SecretKey: strings.Repeat("s", 43)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(o.Close)
	s, e := NewScheduler(db, r.Expected, b, t.TempDir(), key, o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	now := time.Now().UTC().AddDate(0, 0, 1)
	now = time.Date(now.Year(), now.Month(), now.Day(), 12, 1, 0, 0, time.UTC)
	s.clock = func() time.Time { return now }
	remote := &testDailyRemote{deliveries: map[string]backup.Delivery{}, indexes: map[string]backup.Delivery{}}
	s.remote = remote
	return s, remote
}
func enableDaily(t *testing.T, s *Scheduler) ScheduleChange {
	t.Helper()
	in := ScheduleChange{RequestID: "configure-one", Enabled: true, StartMinuteUTC: 720, WindowMinutes: 30, Actor: "test-operator", Reason: "daily backup test", Confirmed: true}
	got, e := s.Configure(t.Context(), in)
	if e != nil || got.Version != 1 {
		t.Fatal("configure", e)
	}
	return in
}
func oneRun(t *testing.T, s *Scheduler) DailyRun {
	t.Helper()
	_, runs, e := s.Status(t.Context())
	if e != nil || len(runs) != 1 {
		t.Fatal("one run", e, len(runs))
	}
	return runs[0]
}
func TestDailyWindowUTCAndBoundaries(t *testing.T) {
	for _, tt := range []struct {
		now, date string
		ok        bool
	}{
		{"2026-09-29T23:50:00Z", "2026-09-29", true}, {"2026-09-30T00:00:00Z", "2026-09-29", true}, {"2026-09-30T00:20:00Z", "2026-09-29", false},
	} {
		now, _ := time.Parse(time.RFC3339, tt.now)
		date, _, ok := dailyWindow(now, 1430, 30)
		if date != tt.date || ok != tt.ok {
			t.Fatal(tt)
		}
	}
}
func TestPlatformDailyPostgresScheduleCaptureDeliveryAndQuarantine(t *testing.T) {
	s, remote := scheduleFixture(t)
	in := enableDaily(t, s)
	if got, e := s.Configure(t.Context(), in); e != nil || got.Version != 1 {
		t.Fatal("lost config ACK", e)
	}
	wrong := in
	wrong.Enabled = false
	if _, e := s.Configure(t.Context(), wrong); e == nil {
		t.Fatal("reused request changed")
	}
	wrong.RequestID = "other"
	if _, e := s.Configure(t.Context(), wrong); e == nil {
		t.Fatal("stale config")
	}
	if e := s.CaptureOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	r := oneRun(t, s)
	if r.State != "captured" || r.Proof.Size == 0 {
		t.Fatal("not captured", r.State)
	}
	if e := s.CaptureOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	remote.fail = true
	if e := s.DeliverOnce(t.Context()); e != nil {
		t.Fatal("durable failed delivery", e)
	}
	if got := oneRun(t, s); got.State != "captured" || got.ErrorCode != "OFFSITE_UNCONFIRMED" {
		t.Fatal("failure not recorded")
	}
	now := s.clock().Add(2 * time.Minute)
	s.clock = func() time.Time { return now }
	remote.fail = false
	if e := s.DeliverOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	got := oneRun(t, s)
	if got.State != "completed" || got.Delivery.Manifest != r.Proof || len(remote.indexes) != 1 {
		t.Fatal("not delivered")
	}
	if e := s.DeliverOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	conn, e := s.db.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	var count int
	if e = conn.QueryRow(t.Context(), `SELECT count(*) FROM platform_audits WHERE action='platform.directory_daily.completed'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("duplicate completion audit")
	}
	// The snapshot includes an enabled schedule. Restore disables it and all
	// access remains fenced by the existing recovery guard.
	target := integrationDatabase(t)
	if e = target.Stage(t.Context(), r.Request, filepath.Join(s.root, r.Request.Expected.BackupID), s.key); e != nil {
		t.Fatal("stage daily", e)
	}
	restored, e := target.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close(context.Background())
	var enabled bool
	if e = restored.QueryRow(t.Context(), `SELECT enabled FROM platform_directory_schedule`).Scan(&enabled); e != nil || enabled {
		t.Fatal("restore reenabled schedule", e)
	}
}
func TestPlatformDailyPostgresPartialRequiresAuditedNewAttempt(t *testing.T) {
	s, _ := scheduleFixture(t)
	enableDaily(t, s)
	dump := s.db.Tools.Dump
	s.db.Tools.Dump = filepath.Join(s.root, "missing-pg-dump")
	if e := s.CaptureOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	old := oneRun(t, s)
	if old.State != "capturing" {
		t.Fatal(old.State)
	}
	now := s.clock().Add(2 * time.Minute)
	s.clock = func() time.Time { return now }
	if e := s.CaptureOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	if r := oneRun(t, s); r.State != "needs_attention" {
		t.Fatal(r.State)
	}
	path := filepath.Join(s.root, old.Request.Expected.BackupID)
	if _, e := os.Stat(path); e != nil {
		t.Fatal("partial lost")
	}
	in := RetryChange{RunID: old.ID, ExpectedAttempt: 1, RequestID: "retry-one", Actor: "operator", Reason: "reviewed partial native dump failure", Confirmed: true}
	r, e := s.Retry(t.Context(), in)
	if e != nil || r.Attempt != 2 || r.Request.Expected.BackupID == old.Request.Expected.BackupID {
		t.Fatal("retry", e)
	}
	if repeated, e := s.Retry(t.Context(), in); e != nil || repeated.Request != r.Request {
		t.Fatal("repeat retry", e)
	}
	s.db.Tools.Dump = dump
	if e = s.CaptureOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = s.DeliverOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	if oneRun(t, s).State != "completed" {
		t.Fatal("retry not complete")
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal("partial overwritten/deleted")
	}
}
func TestPlatformDailyPostgresLostAcknowledgmentAndAuditFailure(t *testing.T) {
	s, remote := scheduleFixture(t)
	enableDaily(t, s)
	if e := s.CaptureOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	r := oneRun(t, s)
	conn, e := s.db.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	// Complete ciphertext exists, while both completion transactions were lost.
	_, e = conn.Exec(t.Context(), `UPDATE platform_directory_backups SET state='pending',manifest_sha256='',manifest_size=0,completed_at=NULL;
DELETE FROM platform_audits WHERE action IN ('platform.directory_backup.completed','platform.directory_daily.captured');
UPDATE platform_directory_daily_runs SET state='capturing',proof='{}';`)
	if e != nil {
		t.Fatal(e)
	}
	// A changed release must not recapture a complete older archive.
	s.expected.ReleaseID = "platform-upgraded"
	if e = s.CaptureOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	if oneRun(t, s).Proof != r.Proof {
		t.Fatal("recovered ciphertext changed")
	}
	_, e = conn.Exec(t.Context(), `CREATE FUNCTION public.reject_daily_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='platform.directory_daily.delivering' THEN RAISE EXCEPTION 'fixture audit failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_daily_audit BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION public.reject_daily_audit();`)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DeliverOnce(t.Context()); e == nil {
		t.Fatal("missing start audit allowed upload")
	}
	if oneRun(t, s).State != "captured" || len(remote.deliveries) != 0 {
		t.Fatal("start audit rollback leaked upload")
	}
	_, e = conn.Exec(t.Context(), `CREATE OR REPLACE FUNCTION public.reject_daily_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='platform.directory_daily.completed' THEN RAISE EXCEPTION 'fixture audit failure'; END IF; RETURN NEW; END $$`)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DeliverOnce(t.Context()); e == nil {
		t.Fatal("audit failure completed")
	}
	if oneRun(t, s).State != "delivering" || len(remote.indexes) != 1 {
		t.Fatal("lost completion state")
	}
	if _, e = conn.Exec(t.Context(), `DROP TRIGGER reject_daily_audit ON platform_audits; DROP FUNCTION public.reject_daily_audit()`); e != nil {
		t.Fatal(e)
	}
	if e = s.DeliverOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	if oneRun(t, s).State != "completed" || len(remote.deliveries) != 1 {
		t.Fatal("ACK replay recaptured")
	}
}
func TestPlatformDailyPostgresDeliveryLockLossAndIndependentCapture(t *testing.T) {
	s, remote := scheduleFixture(t)
	enableDaily(t, s)
	if e := s.CaptureOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	remote.enter = make(chan struct{})
	remote.block = true
	done := make(chan error, 1)
	go func() { done <- s.DeliverOnce(t.Context()) }()
	select {
	case <-remote.enter:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery not entered")
	}
	// Another process cannot deliver or change destination while active.
	if e := s.DeliverOnce(t.Context()); e == nil {
		t.Fatal("concurrent delivery accepted")
	}
	// A second immutable snapshot can proceed while storage is unavailable.
	other := *s
	now := s.clock().AddDate(0, 0, 1)
	other.clock = func() time.Time { return now }
	if e := other.CaptureOnce(t.Context()); e != nil {
		t.Fatal("delivery blocked next day capture", e)
	}
	conn, e := s.db.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	var terminated bool
	e = conn.QueryRow(t.Context(), `SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype='advisory' AND objid=$1::oid AND granted`, directoryDeliveryLock).Scan(&terminated)
	if e != nil || !terminated {
		t.Fatal("kill worker lock", e)
	}
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("lock loss acknowledged")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lost lock not cancelled")
	}
	_, runs, e := s.Status(t.Context())
	if e != nil || len(runs) != 2 {
		t.Fatal(e)
	}
	for _, r := range runs {
		if r.State == "completed" {
			t.Fatal("unconfirmed delivery completed")
		}
	}
	remote.mu.Lock()
	remote.block = false
	remote.mu.Unlock()
	if e = s.DeliverOnce(t.Context()); e != nil {
		t.Fatal("resume lost lock", e)
	}
}
func TestPlatformDailyPostgresWindowDisableAndConfigurationPin(t *testing.T) {
	s, _ := scheduleFixture(t)
	in := enableDaily(t, s)
	conn, e := s.locked(t.Context(), directoryScheduleLock)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.enqueue(t.Context(), conn); e != nil {
		t.Fatal(e)
	}
	conn.Close(context.Background())
	in.ExpectedVersion = 1
	in.RequestID = "disable"
	in.Enabled = false
	if _, e = s.Configure(t.Context(), in); e != nil {
		t.Fatal(e)
	}
	if e = s.CaptureOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	if oneRun(t, s).State != "skipped" {
		t.Fatal("queued backup ran after disable")
	}
	wrong := *s
	wrong.configHash = strings.Repeat("e", 64)
	if e = wrong.CaptureOnce(t.Context()); e == nil {
		t.Fatal("changed destination silently accepted")
	}
	status, runs, e := s.Status(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(struct {
		Schedule Schedule
		Runs     []DailyRun
	}{status, runs})
	if strings.Contains(string(raw), s.db.DSN) || strings.Contains(string(raw), s.root) {
		t.Fatal("secret/path exposed in status")
	}
}

func TestPlatformDailyPostgresMissedWindowAndScheduleAuditRollback(t *testing.T) {
	s, _ := scheduleFixture(t)
	in := enableDaily(t, s)
	now := s.clock().Add(time.Hour)
	s.clock = func() time.Time { return now }
	if e := s.CaptureOnce(t.Context()); e != nil {
		t.Fatal(e)
	}
	r := oneRun(t, s)
	if r.State != "skipped" || r.ErrorCode != "WINDOW_MISSED" {
		t.Fatal("missed schedule hidden")
	}
	conn, e := s.db.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	_, e = conn.Exec(t.Context(), `CREATE FUNCTION public.reject_schedule_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='platform.directory_schedule.updated' THEN RAISE EXCEPTION 'fixture audit failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_schedule_audit BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION public.reject_schedule_audit();`)
	if e != nil {
		t.Fatal(e)
	}
	in.RequestID = "audit-failure"
	in.ExpectedVersion = 1
	in.Enabled = false
	if _, e = s.Configure(t.Context(), in); e == nil {
		t.Fatal("audit failure changed schedule")
	}
	status, _, e := s.Status(t.Context())
	if e != nil || status.Version != 1 || !status.Enabled {
		t.Fatal("schedule transaction not rolled back")
	}
}
