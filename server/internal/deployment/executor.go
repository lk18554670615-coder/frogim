package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

var (
	ErrBundle      = errors.New("DEPLOYMENT_BUNDLE_INVALID")
	ErrState       = errors.New("DEPLOYMENT_STATE_UNAVAILABLE")
	ErrConflict    = errors.New("DEPLOYMENT_STATE_CHANGED")
	ErrUnconfirmed = errors.New("DEPLOYMENT_UNCONFIRMED")
)

type Operation struct {
	ID                 string `json:"id"`
	ServerID           string `json:"serverId"`
	TenantID           string `json:"tenantId"`
	HostFingerprint    string `json:"hostFingerprint"`
	ReleaseID          string `json:"releaseId"`
	ReleaseDigest      string `json:"releaseDigest"`
	ExpectedGeneration int64  `json:"expectedGeneration"`
	ExpectedReleaseID  string `json:"expectedReleaseId"`
	Action             string `json:"action"`
}
type Receipt struct {
	Operation  Operation `json:"operation"`
	State      string    `json:"state"`
	Attempts   int       `json:"attempts"`
	Generation int64     `json:"generation"`
	ErrorCode  string    `json:"errorCode,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt"`
}
type ExecutorStatus struct {
	ServerID              string `json:"serverId"`
	TenantID              string `json:"tenantId"`
	HostFingerprint       string `json:"hostFingerprint"`
	Generation            int64  `json:"generation"`
	CurrentReleaseID      string `json:"currentReleaseId"`
	CurrentReleaseDigest  string `json:"currentReleaseDigest"`
	ActiveOperationID     string `json:"activeOperationId,omitempty"`
	ActiveBackupID        string `json:"activeBackupId,omitempty"`
	BackupOffsiteTargetID string `json:"backupOffsiteTargetId,omitempty"`
}
type journal struct {
	Version int `json:"version"`
	ExecutorStatus
	Receipts             map[string]Receipt      `json:"receipts"`
	Backups              map[string]backupRecord `json:"backups"`
	BackupConfiguration  string                  `json:"backupConfiguration,omitempty"`
	OffsiteConfiguration string                  `json:"offsiteConfiguration,omitempty"`
}
type ReleaseRunner interface {
	Apply(context.Context, Release, Operation) error
	Verify(context.Context, Release, Operation) error
}

// This core does not authorize maintenance or deployment. The platform must
// freeze the enterprise and own a durable maintenance/deployment job BEFORE
// dispatching. Agent execution is opt-in through operator-owned configuration.
type Executor struct {
	mu                        sync.Mutex
	wg                        sync.WaitGroup
	closed, poisoned, running bool
	root                      *os.Root
	lock                      *os.File
	state                     journal
	catalog                   map[string]Release
	runtime                   string
	runner                    ReleaseRunner
	backupIO                  backupTaskIO
	backupRoot                string
	backupKey                 []byte
	offsite                   backupDeliveryIO
	offsiteRunning            bool
}

func OpenExecutor(dir, server, tenant, host, runtime string, catalog map[string]Release, runner ReleaseRunner) (*Executor, error) {
	if !filepath.IsAbs(dir) || !tenancy.ValidID(server) || !tenancy.ValidID(tenant) || !fingerprint.MatchString(host) || runner == nil || (runtime != "linux/amd64" && runtime != "linux/arm64") || len(catalog) == 0 {
		return nil, tenancy.ErrInvalid
	}
	// The operator must create a private state directory. No recursive chmod,
	// directory creation outside it, or broad recovery/deletion operation.
	root, e := os.OpenRoot(dir)
	if e != nil {
		return nil, ErrState
	}
	x := &Executor{root: root, runtime: runtime, runner: runner, catalog: map[string]Release{}}
	ok := false
	defer func() {
		if !ok {
			if x.lock != nil {
				x.lock.Close()
			}
			root.Close()
		}
	}()
	encoded, _ := json.Marshal(catalogList(catalog))
	if _, e := ReadCatalog(bytes.NewReader(encoded)); e != nil {
		return nil, e
	}
	for id, release := range catalog {
		if id != release.ID || !release.Valid() || release.Runtime != runtime || (release.TenantID != "" && !release.MatchesTarget(tenant, server)) {
			return nil, tenancy.ErrInvalid
		}
		release.RollbackTo = slices.Clone(release.RollbackTo)
		x.catalog[id] = release
	}
	// A regular file plus OS lock survives process crashes without stale PID
	// guessing. Only one process can own this journal at a time. The supervisor
	// must bind a target to one canonical state directory, never a fresh one.
	if info, e := root.Lstat("executor.lock"); e == nil && !info.Mode().IsRegular() {
		return nil, ErrState
	}
	x.lock, e = root.OpenFile("executor.lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil || lockExecutor(x.lock) != nil {
		return nil, ErrState
	}
	x.state = journal{Version: 2, ExecutorStatus: ExecutorStatus{ServerID: server, TenantID: tenant, HostFingerprint: host}, Receipts: map[string]Receipt{}, Backups: map[string]backupRecord{}}
	f, e := root.Open("state.json")
	if e == nil {
		defer f.Close()
		info, e := root.Lstat("state.json")
		if e != nil || !info.Mode().IsRegular() {
			return nil, ErrState
		}
		d := json.NewDecoder(io.LimitReader(f, 8<<20))
		d.DisallowUnknownFields()
		x.state = journal{}
		if d.Decode(&x.state) != nil || d.Decode(new(any)) != io.EOF || (x.state.Version != 1 && x.state.Version != 2) || x.state.ServerID != server || x.state.TenantID != tenant || x.state.HostFingerprint != host {
			return nil, ErrState
		}
		legacy := x.state.Version == 1
		if legacy {
			if len(x.state.Backups) != 0 || x.state.ActiveBackupID != "" || x.state.BackupConfiguration != "" {
				return nil, ErrState
			}
			x.state.Backups = map[string]backupRecord{}
			x.state.Version = 2
		}
		if !x.validJournal() {
			return nil, ErrState
		}
		// A v2 journal makes old agents fail closed rather than ignoring an
		// in-progress backup and dispatching a competing deployment.
		if legacy {
			if e = x.saveLocked(); e != nil {
				return nil, e
			}
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, ErrState
	} else if e = x.saveLocked(); e != nil {
		return nil, e
	}
	ok = true
	return x, nil
}

func (x *Executor) validJournal() bool {
	if x.state.Receipts == nil || len(x.state.Receipts) > 2000 || x.state.Generation < 0 {
		return false
	}
	if x.state.Generation == 0 && (x.state.CurrentReleaseID != "" || x.state.CurrentReleaseDigest != "") {
		return false
	}
	if x.state.Generation > 0 {
		r, ok := x.catalog[x.state.CurrentReleaseID]
		if !ok || r.Digest() != x.state.CurrentReleaseDigest {
			return false
		}
	}
	active := 0
	completed := map[int64]bool{}
	for id, r := range x.state.Receipts {
		if id != r.Operation.ID || !x.validOperation(r.Operation) || r.Attempts < 0 || r.UpdatedAt.IsZero() {
			return false
		}
		switch r.State {
		case "completed":
			if r.Generation != r.Operation.ExpectedGeneration+1 || r.Generation > x.state.Generation || completed[r.Generation] {
				return false
			}
			completed[r.Generation] = true
			if r.Generation == x.state.Generation && (r.Operation.ReleaseID != x.state.CurrentReleaseID || r.Operation.ReleaseDigest != x.state.CurrentReleaseDigest) {
				return false
			}
		case "pending", "applying", "unconfirmed":
			active++
			if x.state.ActiveOperationID != id || r.Operation.ExpectedGeneration != x.state.Generation || r.Operation.ExpectedReleaseID != x.state.CurrentReleaseID {
				return false
			}
		default:
			return false
		}
	}
	return int64(len(completed)) == x.state.Generation && (active == 1 && x.state.ActiveOperationID != "" || active == 0 && x.state.ActiveOperationID == "") && x.validBackupJournal()
}
func (x *Executor) validOperation(o Operation) bool {
	r, ok := x.catalog[o.ReleaseID]
	return tenancy.ValidID(o.ID) && o.ServerID == x.state.ServerID && o.TenantID == x.state.TenantID && o.HostFingerprint == x.state.HostFingerprint && ok && r.Digest() == o.ReleaseDigest && o.ExpectedGeneration >= 0 && (o.ExpectedReleaseID == "" || tenancy.ValidID(o.ExpectedReleaseID)) && (o.Action == "deploy" || o.Action == "rollback")
}

// Caller holds the lock; a failed atomic replacement poisons this process. It
// cannot execute using memory that may differ from the durable journal.
func (x *Executor) saveLocked() error {
	b, e := json.Marshal(x.state)
	if e != nil || len(b) > 8<<20 {
		x.poisoned = true
		return ErrState
	}
	id, e := tenancy.Secret()
	if e != nil {
		x.poisoned = true
		return ErrState
	}
	name := ".state-" + id + ".tmp"
	f, e := x.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		x.poisoned = true
		return ErrState
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		e = x.root.Rename(name, "state.json")
	}
	if e == nil {
		e = syncExecutorDir(x.root)
	}
	if e != nil {
		x.poisoned = true
		return ErrState
	}
	return nil
}
func (x *Executor) Close() {
	x.mu.Lock()
	x.closed = true
	x.mu.Unlock()
	x.wg.Wait()
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.lock != nil {
		if x.offsite != nil {
			x.offsite.Close()
		}
		clear(x.backupKey)
		x.lock.Close()
		x.lock = nil
		x.root.Close()
	}
}
func (x *Executor) Status(id string) (ExecutorStatus, Receipt, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed || x.poisoned {
		return ExecutorStatus{}, Receipt{}, ErrState
	}
	if id == "" {
		return x.state.ExecutorStatus, Receipt{}, nil
	}
	r, ok := x.state.Receipts[id]
	if !ok {
		return x.state.ExecutorStatus, Receipt{}, ErrConflict
	}
	return x.state.ExecutorStatus, r, nil
}
func (x *Executor) Submit(o Operation) (Receipt, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed || x.poisoned {
		return Receipt{}, ErrState
	}
	if !x.validOperation(o) {
		return Receipt{}, tenancy.ErrInvalid
	}
	if previous, ok := x.state.Receipts[o.ID]; ok {
		if previous.Operation != o {
			return Receipt{}, ErrConflict
		}
		return previous, nil
	}
	if x.state.ActiveOperationID != "" || x.state.ActiveBackupID != "" || x.state.Generation != o.ExpectedGeneration || x.state.CurrentReleaseID != o.ExpectedReleaseID || len(x.state.Receipts) >= 2000 {
		return Receipt{}, ErrConflict
	}
	r := x.catalog[o.ReleaseID]
	if o.Action == "rollback" {
		old, ok := x.catalog[o.ExpectedReleaseID]
		if !ok || old.SchemaVersion != r.SchemaVersion || r.Sequence >= old.Sequence || !slices.Contains(old.RollbackTo, r.ID) {
			return Receipt{}, ErrConflict
		}
	} else if old, ok := x.catalog[o.ExpectedReleaseID]; ok && (r.SchemaVersion < old.SchemaVersion || r.Sequence < old.Sequence) {
		return Receipt{}, ErrConflict
	}
	receipt := Receipt{Operation: o, State: "pending", UpdatedAt: time.Now().UTC()}
	x.state.ActiveOperationID = o.ID
	x.state.Receipts[o.ID] = receipt
	if e := x.saveLocked(); e != nil {
		return Receipt{}, e
	}
	return receipt, nil
}
func (x *Executor) Retry(id string, expectedAttempts int) (Receipt, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed || x.poisoned {
		return Receipt{}, ErrState
	}
	r, ok := x.state.Receipts[id]
	if !ok || x.running || x.state.ActiveBackupID != "" || x.state.ActiveOperationID != id || r.State != "unconfirmed" || r.Attempts != expectedAttempts {
		return Receipt{}, ErrConflict
	}
	r.State = "pending"
	r.ErrorCode = ""
	r.UpdatedAt = time.Now().UTC()
	x.state.Receipts[id] = r
	if e := x.saveLocked(); e != nil {
		return Receipt{}, e
	}
	return r, nil
}
func (x *Executor) Once(ctx context.Context) (bool, error) {
	x.mu.Lock()
	if x.closed || x.poisoned {
		x.mu.Unlock()
		return false, ErrState
	}
	r, ok := x.state.Receipts[x.state.ActiveOperationID]
	if !ok || x.running || x.state.ActiveBackupID != "" || r.State == "unconfirmed" {
		x.mu.Unlock()
		return false, nil
	}
	// Recovery of an interrupted apply only verifies; no silent re-execution.
	recovering := r.State == "applying"
	r.State = "applying"
	r.Attempts++
	r.ErrorCode = ""
	r.UpdatedAt = time.Now().UTC()
	x.state.Receipts[r.Operation.ID] = r
	if e := x.saveLocked(); e != nil {
		x.mu.Unlock()
		return false, e
	}
	x.running = true
	x.wg.Add(1)
	x.mu.Unlock()
	defer x.wg.Done()
	call, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	release := x.catalog[r.Operation.ReleaseID]
	var e error
	if !recovering {
		e = x.runner.Apply(call, release, r.Operation)
	}
	if e == nil {
		e = x.runner.Verify(call, release, r.Operation)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.running = false
	if e != nil {
		r.State = "unconfirmed"
		r.ErrorCode = "DEPLOYMENT_UNCONFIRMED"
	} else {
		r.State = "completed"
		r.Generation = r.Operation.ExpectedGeneration + 1
		x.state.Generation = r.Generation
		x.state.CurrentReleaseID = release.ID
		x.state.CurrentReleaseDigest = release.Digest()
		x.state.ActiveOperationID = ""
	}
	r.UpdatedAt = time.Now().UTC()
	x.state.Receipts[r.Operation.ID] = r
	if saveErr := x.saveLocked(); saveErr != nil {
		return true, saveErr
	}
	if e != nil {
		return true, ErrUnconfirmed
	}
	return true, nil
}
