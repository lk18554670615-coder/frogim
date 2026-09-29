package directorybackup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/tenancy"
)

// These locks must not collide with administrator and push transactions.
const directoryScheduleLock int64 = 490740002
const directoryDeliveryLock int64 = 490740003

type dailyRemote interface {
	Deliver(context.Context, backup.Binding, string, []byte) (backup.Delivery, error)
	PublishDailyReceipt(context.Context, string, backup.Delivery, []byte) error
}

// Scheduler runs outside the authentication API, with pinned native PG tools
// and operator-owned credentials. Capture and delivery have separate locks so
// slow offsite storage cannot starve the next day's online MVCC snapshot.
type Scheduler struct {
	db               Database
	expected         backup.Binding
	bundle           Bundle
	root, configHash string
	key              []byte
	remote           dailyRemote
	clock            func() time.Time
}

func NewScheduler(db Database, expected backup.Binding, bundle Bundle, root string, key []byte, remote *backup.Offsite) (*Scheduler, error) {
	if expected.SchemaVersion != tenancy.PlatformSchemaVersion || !expected.Valid() || expected.Scope != "platform" || bundle.validate(expected, key) != nil || !filepath.IsAbs(root) || remote == nil || !remote.Accepts(expected) || !remote.IndependentKey(key) {
		return nil, ErrInvalid
	}
	info, e := os.Lstat(root)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalid
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return nil, ErrInvalid
	}
	if _, _, e = connectionConfig(db.DSN); e != nil {
		return nil, e
	}
	// Pin archive location/key/destination, not rotating storage credentials or
	// platform release. Captured archives retain their own release binding.
	hashRoot := root
	if runtime.GOOS == "windows" {
		hashRoot = strings.ToLower(hashRoot)
	}
	keyHash := sha256.Sum256(key)
	raw, _ := json.Marshal([]string{expected.DirectoryID, hashRoot, hex.EncodeToString(keyHash[:]), remote.DestinationFingerprint()})
	hash := sha256.Sum256(raw)
	return &Scheduler{db: db, expected: expected, bundle: bundle, root: root, key: append([]byte(nil), key...), configHash: hex.EncodeToString(hash[:]), remote: remote, clock: time.Now}, nil
}
func (s *Scheduler) Close() { clear(s.key) }

type Schedule struct {
	Enabled        bool  `json:"enabled"`
	StartMinuteUTC int   `json:"startMinuteUtc"`
	WindowMinutes  int   `json:"windowMinutes"`
	Version        int64 `json:"version"`
}
type ScheduleChange struct {
	RequestID       string `json:"requestId"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Enabled         bool   `json:"enabled"`
	StartMinuteUTC  int    `json:"startMinuteUtc"`
	WindowMinutes   int    `json:"windowMinutes"`
	Actor           string `json:"actor"`
	Reason          string `json:"reason"`
	Confirmed       bool   `json:"confirmed"`
}

var operationID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,99}$`)

func validOperator(actor, reason string) bool {
	return strings.TrimSpace(actor) != "" && len(actor) <= 100 && len(strings.TrimSpace(reason)) >= 3 && len(reason) <= 500
}

func (s *Scheduler) locked(ctx context.Context, keys ...int64) (*pgx.Conn, error) {
	conn, e := s.db.connect(ctx)
	if e != nil {
		return nil, e
	}
	fail := func() (*pgx.Conn, error) { conn.Close(context.Background()); return nil, ErrUnconfirmed }
	var ok bool
	for _, key := range keys {
		if conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&ok) != nil || !ok {
			return fail()
		}
	}
	if conn.QueryRow(ctx, `SELECT pg_try_advisory_lock_shared($1)`, tenancy.PlatformMigrationLock).Scan(&ok) != nil || !ok {
		return fail()
	}
	if checkDirectory(ctx, conn, s.expected, false) != nil {
		conn.Close(context.Background())
		return nil, ErrInvalid
	}
	return conn, nil
}

func (s *Scheduler) Configure(ctx context.Context, in ScheduleChange) (Schedule, error) {
	var empty Schedule
	if !in.Confirmed || !operationID.MatchString(in.RequestID) || !validOperator(in.Actor, in.Reason) || in.ExpectedVersion < 0 || in.StartMinuteUTC < 0 || in.StartMinuteUTC > 1439 || in.WindowMinutes < 15 || in.WindowMinutes > 180 {
		return empty, ErrInvalid
	}
	conn, e := s.locked(ctx, directoryScheduleLock, directoryDeliveryLock)
	if e != nil {
		return empty, e
	}
	defer conn.Close(context.Background())
	tx, e := conn.Begin(ctx)
	if e != nil {
		return empty, ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	input, _ := json.Marshal(struct {
		Change        ScheduleChange `json:"change"`
		Configuration string         `json:"configuration"`
	}{in, s.configHash})
	var same bool
	var snapshot []byte
	e = tx.QueryRow(ctx, `SELECT input=$3::jsonb,snapshot FROM platform_directory_schedule_operations WHERE actor_id=$1 AND request_id=$2`, in.Actor, in.RequestID, input).Scan(&same, &snapshot)
	if e == nil {
		if !same || json.Unmarshal(snapshot, &empty) != nil {
			return Schedule{}, ErrInvalid
		}
		return empty, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return empty, ErrUnconfirmed
	}
	var version int64
	var oldHash string
	e = tx.QueryRow(ctx, `SELECT version,configuration_hash FROM platform_directory_schedule WHERE singleton FOR UPDATE`).Scan(&version, &oldHash)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return empty, ErrUnconfirmed
	}
	if version != in.ExpectedVersion {
		return empty, ErrInvalid
	}
	if oldHash != "" && oldHash != s.configHash {
		var pending bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_directory_daily_runs WHERE state NOT IN ('completed','skipped'))`).Scan(&pending) != nil {
			return empty, ErrUnconfirmed
		}
		if pending {
			return empty, ErrInvalid
		}
	}
	out := Schedule{in.Enabled, in.StartMinuteUTC, in.WindowMinutes, version + 1}
	_, e = tx.Exec(ctx, `INSERT INTO platform_directory_schedule(singleton,directory_id,enabled,start_minute_utc,window_minutes,version,configuration_hash,actor_id,reason) VALUES(true,$1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(singleton) DO UPDATE SET enabled=excluded.enabled,start_minute_utc=excluded.start_minute_utc,window_minutes=excluded.window_minutes,version=excluded.version,configuration_hash=excluded.configuration_hash,actor_id=excluded.actor_id,reason=excluded.reason,updated_at=clock_timestamp()`, s.expected.DirectoryID, out.Enabled, out.StartMinuteUTC, out.WindowMinutes, out.Version, s.configHash, in.Actor, in.Reason)
	if e != nil {
		return empty, ErrUnconfirmed
	}
	snapshot, _ = json.Marshal(out)
	_, e = tx.Exec(ctx, `INSERT INTO platform_directory_schedule_operations(actor_id,request_id,input,snapshot) VALUES($1,$2,$3,$4)`, in.Actor, in.RequestID, input, snapshot)
	if e != nil {
		return empty, ErrUnconfirmed
	}
	metadata, _ := json.Marshal(struct {
		BeforeVersion int64    `json:"beforeVersion"`
		After         Schedule `json:"after"`
	}{version, out})
	if auditDaily(ctx, tx, in.Actor, "platform.directory_schedule.updated", in.RequestID, in.Reason, metadata) != nil || tx.Commit(ctx) != nil {
		return empty, ErrUnconfirmed
	}
	return out, nil
}
func auditDaily(ctx context.Context, tx pgx.Tx, actor, action, id, reason string, metadata []byte) error {
	_, e := tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,job_id,reason,metadata) VALUES($1,$2,$3,$4,$5)`, actor, action, id, reason, metadata)
	if e != nil {
		return ErrUnconfirmed
	}
	return nil
}

type DailyRun struct {
	ID               string          `json:"id"`
	Date             string          `json:"date"`
	ScheduleVersion  int64           `json:"scheduleVersion"`
	WindowEnd        time.Time       `json:"windowEnd"`
	Request          Request         `json:"request"`
	Attempt          int             `json:"attempt"`
	State            string          `json:"state"`
	Proof            backup.File     `json:"proof"`
	Delivery         backup.Delivery `json:"delivery"`
	DeliveryAttempts int             `json:"deliveryAttempts"`
	ErrorCode        string          `json:"errorCode"`
	RetryAt          time.Time       `json:"retryAt"`
	configHash       string
}

const runColumns = `id,slot_date::text,schedule_version,window_end,request,attempt,state,proof,delivery,delivery_attempts,error_code,retry_at,configuration_hash`

func scanDaily(row pgx.Row) (DailyRun, error) {
	var r DailyRun
	var request, proof, delivery []byte
	e := row.Scan(&r.ID, &r.Date, &r.ScheduleVersion, &r.WindowEnd, &request, &r.Attempt, &r.State, &proof, &delivery, &r.DeliveryAttempts, &r.ErrorCode, &r.RetryAt, &r.configHash)
	if e != nil {
		return r, e
	}
	if json.Unmarshal(request, &r.Request) != nil || json.Unmarshal(proof, &r.Proof) != nil || json.Unmarshal(delivery, &r.Delivery) != nil || !r.Request.valid() {
		return r, ErrInvalid
	}
	return r, nil
}
func (s *Scheduler) Status(ctx context.Context) (Schedule, []DailyRun, error) {
	conn, e := s.locked(ctx)
	if e != nil {
		return Schedule{}, nil, e
	}
	defer conn.Close(context.Background())
	var schedule Schedule
	e = conn.QueryRow(ctx, `SELECT enabled,start_minute_utc,window_minutes,version FROM platform_directory_schedule WHERE singleton`).Scan(&schedule.Enabled, &schedule.StartMinuteUTC, &schedule.WindowMinutes, &schedule.Version)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return schedule, nil, ErrUnconfirmed
	}
	rows, e := conn.Query(ctx, `SELECT `+runColumns+` FROM platform_directory_daily_runs ORDER BY slot_date DESC LIMIT 31`)
	if e != nil {
		return schedule, nil, ErrUnconfirmed
	}
	defer rows.Close()
	runs := []DailyRun{}
	for rows.Next() {
		r, e := scanDaily(rows)
		if e != nil {
			return schedule, nil, ErrUnconfirmed
		}
		runs = append(runs, r)
	}
	if rows.Err() != nil {
		return schedule, nil, ErrUnconfirmed
	}
	return schedule, runs, nil
}
func newDailyID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return ""
	}
	return "daily-" + hex.EncodeToString(b[:])
}
func dailyWindow(now time.Time, start, minutes int) (string, time.Time, bool) {
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	begin := day.Add(time.Duration(start) * time.Minute)
	if now.Before(begin) {
		begin = begin.AddDate(0, 0, -1)
	}
	end := begin.Add(time.Duration(minutes) * time.Minute)
	return begin.Format("2006-01-02"), end, !now.Before(begin) && now.Before(end)
}
func (s *Scheduler) enqueue(ctx context.Context, conn *pgx.Conn) error {
	var schedule Schedule
	var hash, actor, reason string
	var configuredAt time.Time
	e := conn.QueryRow(ctx, `SELECT enabled,start_minute_utc,window_minutes,version,configuration_hash,actor_id,reason,updated_at FROM platform_directory_schedule WHERE singleton`).Scan(&schedule.Enabled, &schedule.StartMinuteUTC, &schedule.WindowMinutes, &schedule.Version, &hash, &actor, &reason, &configuredAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return ErrUnconfirmed
	}
	if hash != s.configHash {
		return ErrInvalid
	}
	if !schedule.Enabled {
		return nil
	}
	date, end, within := dailyWindow(s.clock(), schedule.StartMinuteUTC, schedule.WindowMinutes)
	// After an outage, explicitly record the most recent missed slot rather
	// than presenting silence as successful daily protection. Do not invent a
	// missed run from before this schedule was configured.
	if !within && !configuredAt.Before(end) {
		return nil
	}
	state, code := "queued", ""
	if !within {
		state, code = "skipped", "WINDOW_MISSED"
	}
	id := newDailyID()
	if id == "" {
		return ErrUnconfirmed
	}
	r := Request{Expected: s.expected, Actor: actor, Reason: reason}
	r.Expected.BackupID = id
	request, _ := json.Marshal(r)
	tx, e := conn.Begin(ctx)
	if e != nil {
		return ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `INSERT INTO platform_directory_daily_runs(id,directory_id,slot_date,schedule_version,configuration_hash,window_end,request,state,error_code) VALUES($1,$2,$3::date,$4,$5,$6,$7,$8,$9) ON CONFLICT(directory_id,slot_date) DO NOTHING`, id, s.expected.DirectoryID, date, schedule.Version, s.configHash, end, request, state, code)
	if e != nil {
		return ErrUnconfirmed
	}
	if tag.RowsAffected() == 1 {
		metadata, _ := json.Marshal(map[string]any{"date": date, "scheduleVersion": schedule.Version, "errorCode": code})
		if auditDaily(ctx, tx, actor, "platform.directory_daily."+state, id, reason, metadata) != nil {
			return ErrUnconfirmed
		}
	}
	if tx.Commit(ctx) != nil {
		return ErrUnconfirmed
	}
	return nil
}

func (s *Scheduler) updateRun(ctx context.Context, conn *pgx.Conn, r DailyRun, state, code string, proof backup.File, delivery backup.Delivery, delay time.Duration) error {
	tx, e := conn.Begin(ctx)
	if e != nil {
		return ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	p, _ := json.Marshal(proof)
	d, _ := json.Marshal(delivery)
	_, e = tx.Exec(ctx, `UPDATE platform_directory_daily_runs SET state=$2,error_code=$3,proof=$4,delivery=$5,retry_at=$6,updated_at=clock_timestamp() WHERE id=$1`, r.ID, state, code, p, d, s.clock().UTC().Add(delay))
	if e != nil {
		return ErrUnconfirmed
	}
	metadata, _ := json.Marshal(map[string]any{"date": r.Date, "attempt": r.Attempt, "state": state, "errorCode": code, "manifest": proof, "delivery": delivery})
	if auditDaily(ctx, tx, r.Request.Actor, "platform.directory_daily."+state, r.ID, r.Request.Reason, metadata) != nil || tx.Commit(ctx) != nil {
		return ErrUnconfirmed
	}
	return nil
}

func (s *Scheduler) CaptureOnce(ctx context.Context) error {
	conn, e := s.locked(ctx, directoryScheduleLock)
	if e != nil {
		return e
	}
	defer conn.Close(context.Background())
	if e = s.enqueue(ctx, conn); e != nil {
		return e
	}
	filter, e := directoryRecoveryFilter(ctx, conn)
	if e != nil {
		return e
	}
	r, e := scanDaily(conn.QueryRow(ctx, `SELECT `+runColumns+` FROM platform_directory_daily_runs WHERE `+filter+`state IN ('queued','capturing') AND retry_at<=$1 ORDER BY slot_date LIMIT 1`, s.clock().UTC()))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return ErrUnconfirmed
	}
	if r.configHash != s.configHash {
		return ErrInvalid
	}
	path := filepath.Join(s.root, r.Request.Expected.BackupID)
	// Complete archives survive a lost database ACK or release upgrade. Do not
	// recapture them using today's source database or today's runtime config.
	if r.State == "capturing" {
		if proof, e := Verify(path, r.Request, s.key); e == nil {
			if e = complete(ctx, conn, r.Request, proof); e != nil {
				return e
			}
			return s.updateRun(ctx, conn, r, "captured", "", proof, backup.Delivery{}, 0)
		}
		if _, e := os.Lstat(path); e == nil || !errors.Is(e, os.ErrNotExist) {
			return s.updateRun(ctx, conn, r, "needs_attention", "PARTIAL_ARCHIVE_RETAINED", backup.File{}, backup.Delivery{}, 0)
		}
	}
	var enabled bool
	if conn.QueryRow(ctx, `SELECT enabled FROM platform_directory_schedule WHERE singleton`).Scan(&enabled) != nil {
		return ErrUnconfirmed
	}
	if r.State == "queued" && (!enabled || !s.clock().Before(r.WindowEnd)) {
		return s.updateRun(ctx, conn, r, "skipped", "WINDOW_CLOSED_OR_DISABLED", backup.File{}, backup.Delivery{}, 0)
	}
	expected := s.expected
	expected.BackupID = r.Request.Expected.BackupID
	if expected != r.Request.Expected {
		return s.updateRun(ctx, conn, r, "needs_attention", "RELEASE_CHANGED_BEFORE_CAPTURE", backup.File{}, backup.Delivery{}, 0)
	}
	if e = s.updateRun(ctx, conn, r, "capturing", "", backup.File{}, backup.Delivery{}, 0); e != nil {
		return e
	}
	var proof backup.File
	e = watched(ctx, conn, func(work context.Context) error {
		var e error
		proof, e = s.db.Capture(work, r.Request, s.bundle, path, s.key)
		return e
	})
	if e != nil {
		// Retry the same identity first. On restart the authenticated complete
		// set is recovered; a partial set requires explicit audited new attempt.
		return s.updateRun(ctx, conn, r, "capturing", "CAPTURE_UNCONFIRMED", backup.File{}, backup.Delivery{}, time.Minute)
	}
	return s.updateRun(ctx, conn, r, "captured", "", proof, backup.Delivery{}, 0)
}

func (s *Scheduler) DeliverOnce(ctx context.Context) error {
	conn, e := s.locked(ctx, directoryDeliveryLock)
	if e != nil {
		return e
	}
	defer conn.Close(context.Background())
	filter, e := directoryRecoveryFilter(ctx, conn)
	if e != nil {
		return e
	}
	r, e := scanDaily(conn.QueryRow(ctx, `SELECT `+runColumns+` FROM platform_directory_daily_runs WHERE `+filter+`state IN ('captured','delivering') AND retry_at<=$1 ORDER BY retry_at,slot_date LIMIT 1`, s.clock().UTC()))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return ErrUnconfirmed
	}
	if r.configHash != s.configHash {
		return ErrInvalid
	}
	tx, e := conn.Begin(ctx)
	if e != nil {
		return ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	if e = tx.QueryRow(ctx, `UPDATE platform_directory_daily_runs SET state='delivering',delivery_attempts=delivery_attempts+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING delivery_attempts`, r.ID).Scan(&r.DeliveryAttempts); e != nil {
		return ErrUnconfirmed
	}
	started, _ := json.Marshal(map[string]any{"date": r.Date, "attempt": r.DeliveryAttempts, "manifest": r.Proof})
	if auditDaily(ctx, tx, r.Request.Actor, "platform.directory_daily.delivering", r.ID, r.Request.Reason, started) != nil || tx.Commit(ctx) != nil {
		return ErrUnconfirmed
	}
	var delivery backup.Delivery
	e = watched(ctx, conn, func(work context.Context) error {
		path := filepath.Join(s.root, r.Request.Expected.BackupID)
		proof, e := Verify(path, r.Request, s.key)
		if e != nil || proof != r.Proof {
			return ErrInvalid
		}
		delivery, e = s.remote.Deliver(work, r.Request.Expected, path, s.key)
		if e != nil {
			return e
		}
		if delivery.Binding != r.Request.Expected || delivery.Manifest != r.Proof || delivery.Version != 1 {
			return ErrInvalid
		}
		return s.remote.PublishDailyReceipt(work, r.Date, delivery, s.key)
	})
	if e != nil {
		return s.updateRun(ctx, conn, r, "captured", "OFFSITE_UNCONFIRMED", r.Proof, backup.Delivery{}, time.Duration(min(900, 30*r.DeliveryAttempts))*time.Second)
	}
	return s.updateRun(ctx, conn, r, "completed", "", r.Proof, delivery, 0)
}

type RetryChange struct {
	RunID           string `json:"runId"`
	ExpectedAttempt int    `json:"expectedAttempt"`
	RequestID       string `json:"requestId"`
	Actor           string `json:"actor"`
	Reason          string `json:"reason"`
	Confirmed       bool   `json:"confirmed"`
}

func (s *Scheduler) Retry(ctx context.Context, in RetryChange) (DailyRun, error) {
	var empty DailyRun
	if !in.Confirmed || !operationID.MatchString(in.RunID) || !operationID.MatchString(in.RequestID) || in.ExpectedAttempt < 1 || !validOperator(in.Actor, in.Reason) {
		return empty, ErrInvalid
	}
	conn, e := s.locked(ctx, directoryScheduleLock, directoryDeliveryLock)
	if e != nil {
		return empty, e
	}
	defer conn.Close(context.Background())
	tx, e := conn.Begin(ctx)
	if e != nil {
		return empty, ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	input, _ := json.Marshal(struct {
		Retry         RetryChange `json:"retry"`
		Configuration string      `json:"configuration"`
	}{in, s.configHash})
	var same bool
	var snapshot []byte
	e = tx.QueryRow(ctx, `SELECT input=$3::jsonb,snapshot FROM platform_directory_schedule_operations WHERE actor_id=$1 AND request_id=$2`, in.Actor, in.RequestID, input).Scan(&same, &snapshot)
	if e == nil {
		if !same || json.Unmarshal(snapshot, &empty) != nil {
			return DailyRun{}, ErrInvalid
		}
		return empty, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return empty, ErrUnconfirmed
	}
	r, e := scanDaily(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM platform_directory_daily_runs WHERE id=$1 FOR UPDATE`, in.RunID))
	if e != nil || r.State != "needs_attention" || r.Attempt != in.ExpectedAttempt || r.configHash != s.configHash {
		return empty, ErrInvalid
	}
	// The partial attempt is retained forever under its original ID. A new
	// attempt cannot replace an already discovered daily offsite receipt.
	old := r.Request.Expected.BackupID
	r.Request = Request{Expected: s.expected, Actor: in.Actor, Reason: in.Reason}
	r.Request.Expected.BackupID = newDailyID()
	if r.Request.Expected.BackupID == "" {
		return empty, ErrUnconfirmed
	}
	r.Attempt++
	r.State = "capturing"
	r.ErrorCode = ""
	r.RetryAt = s.clock().UTC()
	request, _ := json.Marshal(r.Request)
	_, e = tx.Exec(ctx, `UPDATE platform_directory_daily_runs SET request=$2,attempt=$3,state='capturing',error_code='',retry_at=$4,updated_at=clock_timestamp() WHERE id=$1`, r.ID, request, r.Attempt, r.RetryAt)
	if e != nil {
		return empty, ErrUnconfirmed
	}
	snapshot, _ = json.Marshal(r)
	_, e = tx.Exec(ctx, `INSERT INTO platform_directory_schedule_operations(actor_id,request_id,input,snapshot) VALUES($1,$2,$3,$4)`, in.Actor, in.RequestID, input, snapshot)
	if e != nil {
		return empty, ErrUnconfirmed
	}
	metadata, _ := json.Marshal(map[string]any{"date": r.Date, "oldBackupId": old, "newBackupId": r.Request.Expected.BackupID, "attempt": r.Attempt})
	if auditDaily(ctx, tx, in.Actor, "platform.directory_daily.retry_requested", r.ID, in.Reason, metadata) != nil || tx.Commit(ctx) != nil {
		return empty, ErrUnconfirmed
	}
	return r, nil
}
