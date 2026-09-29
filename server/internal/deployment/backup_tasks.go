package deployment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/linli/im/server/internal/backup"
)

type BackupOperation struct {
	ID              string         `json:"id"`
	HostFingerprint string         `json:"hostFingerprint"`
	Binding         backup.Binding `json:"binding"`
	OffsiteTargetID string         `json:"offsiteTargetId,omitempty"`
}
type ArchiveProof struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Files  int    `json:"files"`
}

func (p ArchiveProof) valid() bool {
	return fingerprint.MatchString(p.SHA256) && p.Bytes > 0 && p.Files == 8
}

type BackupReceipt struct {
	Operation         BackupOperation `json:"operation"`
	State             string          `json:"state"`
	Phase             string          `json:"phase"`
	Attempt           int             `json:"attempt"`
	Revision          int64           `json:"revision"`
	ErrorCode         string          `json:"errorCode,omitempty"`
	RecoveryErrorCode string          `json:"recoveryErrorCode,omitempty"`
	Archive           ArchiveProof    `json:"archive"`
	Offsite           OffsiteReceipt  `json:"offsite,omitzero"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}
type backupSource struct {
	Containers map[string]string `json:"containers"`
	Volumes    map[string]string `json:"volumes"`
}

func (s backupSource) clone() backupSource {
	return backupSource{maps.Clone(s.Containers), maps.Clone(s.Volumes)}
}
func (s backupSource) valid() bool {
	if len(s.Containers) != 9 || len(s.Volumes) != 6 {
		return false
	}
	for k, v := range s.Containers {
		if !serviceName.MatchString(k) || !fingerprint.MatchString(v) {
			return false
		}
	}
	for k, v := range s.Volumes {
		if !serviceName.MatchString(k) || !fingerprint.MatchString(v) {
			return false
		}
	}
	return true
}

type backupRecord struct {
	BackupReceipt
	Source backupSource `json:"source"`
}
type backupPlan struct {
	Request    BackupOperation
	Release    Release
	Deployment Operation
	Attempt    int
}
type backupTaskIO interface {
	Prepare(context.Context, backupPlan, []byte) (backupSource, error)
	Quiesce(context.Context, backupPlan, backupSource) error
	Capture(context.Context, backupPlan, backupSource, string, []byte) (ArchiveProof, error)
	Restore(context.Context, backupPlan, backupSource) error
	Verify(context.Context, backupPlan, backupSource) error
}

func (x *Executor) validBackupOperation(o BackupOperation) bool {
	if o.OffsiteTargetID != "" && (!serviceName.MatchString(o.OffsiteTargetID) || o.OffsiteTargetID != x.state.BackupOffsiteTargetID) {
		return false
	}
	r, ok := x.catalog[o.Binding.ReleaseID]
	return serviceName.MatchString(o.ID) && o.HostFingerprint == x.state.HostFingerprint && o.Binding.Valid() && o.Binding.SchemaVersion == 79 && o.Binding.TenantID == x.state.TenantID && o.Binding.ServerID == x.state.ServerID && ok && r.MatchesTarget(o.Binding.TenantID, o.Binding.ServerID) && r.Digest() == o.Binding.ReleaseDigest && r.SchemaVersion == o.Binding.SchemaVersion
}
func (x *Executor) matchesBackup(o BackupOperation) bool {
	return x.validBackupOperation(o) && x.state.Generation == o.Binding.Generation && x.state.CurrentReleaseID == o.Binding.ReleaseID && x.state.CurrentReleaseDigest == o.Binding.ReleaseDigest
}
func (x *Executor) validBackupJournal() bool {
	if x.state.Version != 2 || x.state.Backups == nil || len(x.state.Backups) > 2000 {
		return false
	}
	if x.state.BackupConfiguration != "" && !fingerprint.MatchString(x.state.BackupConfiguration) {
		return false
	}
	if (x.state.BackupOffsiteTargetID == "") != (x.state.OffsiteConfiguration == "") || x.state.OffsiteConfiguration != "" && (!fingerprint.MatchString(x.state.OffsiteConfiguration) || !serviceName.MatchString(x.state.BackupOffsiteTargetID) || x.state.BackupConfiguration == "") {
		return false
	}
	active := 0
	for id, r := range x.state.Backups {
		if !ValidBackupOffsite(r.BackupReceipt) {
			return false
		}
		if id != r.Operation.ID || !x.validBackupOperation(r.Operation) || r.Operation.Binding.Generation > x.state.Generation || r.Attempt < 1 || r.Attempt > 100 || r.Revision < 1 || r.UpdatedAt.IsZero() || x.state.BackupConfiguration == "" {
			return false
		}
		if !slices.Contains([]string{"queued", "quiescing", "snapshotting", "restoring_services", "verifying", "finished"}, r.Phase) {
			return false
		}
		if r.Phase != "queued" && r.State != "cancelled" && !r.Source.valid() {
			return false
		}
		if r.Archive != (ArchiveProof{}) && !r.Archive.valid() {
			return false
		}
		if r.State == "cancelled" {
			if r.Phase != "finished" || len(r.Source.Containers) != 0 || len(r.Source.Volumes) != 0 || r.Archive != (ArchiveProof{}) || r.ErrorCode != "" || r.RecoveryErrorCode != "" {
				return false
			}
		} else if r.State == "completed" || r.State == "failed" {
			if r.Phase != "finished" || r.RecoveryErrorCode != "" || (r.State == "completed" && (!r.Archive.valid() || r.ErrorCode != "")) || (r.State == "failed" && (r.ErrorCode == "" || r.Archive != (ArchiveProof{}))) {
				return false
			}
		} else {
			if !slices.Contains([]string{"pending", "running", "unconfirmed"}, r.State) || r.Phase == "finished" || !x.matchesBackup(r.Operation) || x.state.ActiveBackupID != id {
				return false
			}
			active++
		}
	}
	return (active == 0 && x.state.ActiveBackupID == "" || active == 1 && x.state.ActiveBackupID != "") && (x.state.ActiveBackupID == "" || x.state.ActiveOperationID == "")
}

// ConfigureBackups is private operator configuration, not request data. The
// directory and independent key are pinned in the journal; changing either
// requires a future audited rotation/migration, not silently losing old sets.
func (x *Executor) ConfigureBackups(root, keyFile string) error {
	if !filepath.IsAbs(root) || !filepath.IsAbs(keyFile) {
		return ErrState
	}
	info, e := os.Lstat(root)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrState
	}
	canonical, e := filepath.EvalSymlinks(root)
	if e != nil {
		return ErrState
	}
	info, e = os.Lstat(keyFile)
	if e != nil || !info.Mode().IsRegular() || info.Size() != 32 {
		return ErrState
	}
	keyPath, e := filepath.EvalSymlinks(keyFile)
	if e != nil {
		return ErrState
	}
	if strings.EqualFold(filepath.VolumeName(canonical), filepath.VolumeName(keyPath)) {
		rel, e := filepath.Rel(canonical, keyPath)
		if e != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return ErrState
		}
	}
	f, e := os.Open(keyFile)
	if e != nil {
		return ErrState
	}
	actual, e := f.Stat()
	if e != nil || !os.SameFile(info, actual) {
		f.Close()
		return ErrState
	}
	key, e := io.ReadAll(io.LimitReader(f, 33))
	f.Close()
	defer clear(key)
	if e != nil || len(key) != 32 {
		return ErrState
	}
	r, ok := x.runner.(*ComposeRunner)
	if !ok {
		return ErrState
	}
	return x.configureBackupIO(canonical, key, &composeBackupIO{r: r})
}
func (x *Executor) configureBackupIO(root string, key []byte, runner backupTaskIO) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed || x.poisoned || x.running || x.backupIO != nil || !filepath.IsAbs(root) || len(key) != 32 || runner == nil {
		return ErrState
	}
	h := sha256.New()
	h.Write([]byte("frogim-backup-configuration-v1\x00" + filepath.Clean(root) + "\x00"))
	h.Write(key)
	digest := hex.EncodeToString(h.Sum(nil))
	if x.state.BackupConfiguration != "" && x.state.BackupConfiguration != digest {
		return ErrState
	}
	if x.state.BackupConfiguration == "" {
		x.state.BackupConfiguration = digest
		if e := x.saveLocked(); e != nil {
			return e
		}
	}
	x.backupRoot = root
	x.backupKey = bytes.Clone(key)
	x.backupIO = runner
	return nil
}
func (x *Executor) BackupStatus(id string) (ExecutorStatus, BackupReceipt, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed || x.poisoned {
		return ExecutorStatus{}, BackupReceipt{}, ErrState
	}
	if id == "" {
		return x.state.ExecutorStatus, BackupReceipt{}, nil
	}
	r, ok := x.state.Backups[id]
	if !ok {
		return x.state.ExecutorStatus, BackupReceipt{}, ErrConflict
	}
	return x.state.ExecutorStatus, r.BackupReceipt, nil
}
func (x *Executor) SubmitBackup(o BackupOperation) (BackupReceipt, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed || x.poisoned || x.backupIO == nil {
		return BackupReceipt{}, ErrState
	}
	if !x.validBackupOperation(o) {
		return BackupReceipt{}, ErrConflict
	}
	if previous, ok := x.state.Backups[o.ID]; ok {
		if previous.Operation != o {
			return BackupReceipt{}, ErrConflict
		}
		return previous.BackupReceipt, nil
	}
	if !x.matchesBackup(o) || x.state.ActiveOperationID != "" || x.state.ActiveBackupID != "" || x.running || len(x.state.Backups) >= 2000 {
		return BackupReceipt{}, ErrConflict
	}
	if o.OffsiteTargetID != x.state.BackupOffsiteTargetID || o.OffsiteTargetID != "" && x.offsite == nil {
		return BackupReceipt{}, ErrConflict
	}
	r := backupRecord{BackupReceipt: BackupReceipt{Operation: o, State: "pending", Phase: "queued", Attempt: 1, Revision: 1, UpdatedAt: time.Now().UTC()}}
	x.state.ActiveBackupID = o.ID
	x.state.Backups[o.ID] = r
	if e := x.saveLocked(); e != nil {
		return BackupReceipt{}, e
	}
	return r.BackupReceipt, nil
}

// Retry uses a revision fence, not only the attempt: stale UI/ACK replay cannot
// restart a newer phase. Unconfirmed restoration retries restoration only.
func (x *Executor) RetryBackup(id string, revision int64) (BackupReceipt, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed || x.poisoned || x.backupIO == nil {
		return BackupReceipt{}, ErrState
	}
	r, ok := x.state.Backups[id]
	if !ok || x.running || r.Revision != revision || !x.matchesBackup(r.Operation) || x.state.ActiveOperationID != "" || (x.state.ActiveBackupID != "" && x.state.ActiveBackupID != id) {
		return BackupReceipt{}, ErrConflict
	}
	switch r.State {
	case "unconfirmed":
	case "failed":
		if r.Attempt >= 100 {
			return BackupReceipt{}, ErrConflict
		}
		r.Attempt++
		r.Phase = "queued"
		r.Source = backupSource{}
		r.Archive = ArchiveProof{}
		r.ErrorCode = ""
	default:
		return BackupReceipt{}, ErrConflict
	}
	r.State = "pending"
	r.Revision++
	r.UpdatedAt = time.Now().UTC()
	x.state.Backups[id] = r
	x.state.ActiveBackupID = id
	if e := x.saveLocked(); e != nil {
		return BackupReceipt{}, e
	}
	return r.BackupReceipt, nil
}
func (x *Executor) backupPlan(o BackupOperation) (backupPlan, error) {
	p := backupPlan{Request: o, Release: x.catalog[o.Binding.ReleaseID]}
	if !x.matchesBackup(o) {
		return p, ErrConflict
	}
	for _, r := range x.state.Receipts {
		if r.State == "completed" && r.Generation == o.Binding.Generation {
			p.Deployment = r.Operation
			return p, nil
		}
	}
	return p, ErrState
}

// Cancellation is safe only BEFORE the durable stop intent. Once quiescing has
// begun, recovery must finish or remain explicitly unconfirmed; no escape hatch
// may clear the deployment lock while original services could still be stopped.
func (x *Executor) CancelBackup(id string, revision int64) (BackupReceipt, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed || x.poisoned || x.backupIO == nil {
		return BackupReceipt{}, ErrState
	}
	r, ok := x.state.Backups[id]
	if ok && r.State == "cancelled" && r.Revision == revision+1 {
		return r.BackupReceipt, nil
	}
	if !ok || x.running || r.Revision != revision || x.state.ActiveBackupID != id || r.Phase != "queued" || !slices.Contains([]string{"pending", "unconfirmed", "running"}, r.State) {
		return BackupReceipt{}, ErrConflict
	}
	r.State = "cancelled"
	r.Phase = "finished"
	r.Source = backupSource{}
	r.Archive = ArchiveProof{}
	r.ErrorCode = ""
	r.RecoveryErrorCode = ""
	r.Revision++
	r.UpdatedAt = time.Now().UTC()
	x.state.Backups[id] = r
	x.state.ActiveBackupID = ""
	if e := x.saveLocked(); e != nil {
		return BackupReceipt{}, e
	}
	return r.BackupReceipt, nil
}
func (x *Executor) BackupOnce(ctx context.Context) (bool, error) {
	x.mu.Lock()
	if x.closed || x.poisoned {
		x.mu.Unlock()
		return false, ErrState
	}
	r, ok := x.state.Backups[x.state.ActiveBackupID]
	if !ok || x.running || r.State == "unconfirmed" || x.backupIO == nil {
		x.mu.Unlock()
		return false, nil
	}
	p, e := x.backupPlan(r.Operation)
	if e != nil {
		x.mu.Unlock()
		return false, e
	}
	p.Attempt = r.Attempt
	r.State = "running"
	r.Revision++
	r.UpdatedAt = time.Now().UTC()
	x.state.Backups[r.Operation.ID] = r
	if e = x.saveLocked(); e != nil {
		x.mu.Unlock()
		return false, e
	}
	x.running = true
	x.wg.Add(1)
	runner := x.backupIO
	root := x.backupRoot
	key := bytes.Clone(x.backupKey)
	r.Source = r.Source.clone()
	x.mu.Unlock()
	defer x.wg.Done()
	defer clear(key)
	call, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	phase := r.Phase
	switch phase {
	case "queued":
		r.Source, e = runner.Prepare(call, p, key)
		if e == nil && !r.Source.valid() {
			e = ErrUnconfirmed
		}
		if e == nil {
			r.Phase = "quiescing"
		} else {
			r.Source = backupSource{}
		}
	case "quiescing":
		e = runner.Quiesce(call, p, r.Source)
		if e != nil {
			r.ErrorCode = "BACKUP_QUIESCE_UNCONFIRMED"
			r.Phase = "restoring_services"
		} else {
			r.Phase = "snapshotting"
		}
	case "snapshotting":
		// A job-owned attempt path is derived locally, never supplied by HTTP.
		path := filepath.Join(root, fmt.Sprintf("%s-attempt-%03d", r.Operation.ID, r.Attempt))
		r.Archive, e = runner.Capture(call, p, r.Source, path, key)
		if e != nil || !r.Archive.valid() {
			r.Archive = ArchiveProof{}
			r.ErrorCode = "BACKUP_ARCHIVE_UNCONFIRMED"
			e = ErrUnconfirmed
		}
		r.Phase = "restoring_services"
	case "restoring_services":
		e = runner.Restore(call, p, r.Source)
		if e == nil {
			r.Phase = "verifying"
		}
	case "verifying":
		e = runner.Verify(call, p, r.Source)
		if e == nil {
			r.Phase = "finished"
			if r.Archive.valid() && r.ErrorCode == "" {
				r.State = "completed"
				if r.Operation.OffsiteTargetID != "" {
					r.Offsite = OffsiteReceipt{State: "pending", UpdatedAt: time.Now().UTC(), RetryAt: time.Now().UTC()}
				}
			} else {
				r.State = "failed"
			}
		}
	default:
		e = ErrState
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.running = false
	if e != nil && (phase == "queued" || phase == "restoring_services" || phase == "verifying") {
		r.State = "unconfirmed"
		r.RecoveryErrorCode = map[string]string{"queued": "BACKUP_PREFLIGHT_UNCONFIRMED", "restoring_services": "BACKUP_SERVICE_RECOVERY_UNCONFIRMED", "verifying": "BACKUP_SERVICE_VERIFICATION_UNCONFIRMED"}[phase]
		// Preserve an archive failure across a restoration failure. A successful
		// archive remains distinct from service recovery and is not re-captured.
		if r.ErrorCode == "" && phase == "queued" {
			r.ErrorCode = "BACKUP_PREFLIGHT_UNCONFIRMED"
		}
	} else {
		r.RecoveryErrorCode = ""
		if r.Phase != "finished" {
			r.State = "pending"
			if phase == "queued" {
				r.ErrorCode = ""
			}
		}
	}
	if r.State == "completed" || r.State == "failed" {
		x.state.ActiveBackupID = ""
	}
	r.Revision++
	r.UpdatedAt = time.Now().UTC()
	r.Source = r.Source.clone()
	x.state.Backups[r.Operation.ID] = r
	if saveErr := x.saveLocked(); saveErr != nil {
		return true, saveErr
	}
	if e != nil {
		return true, ErrUnconfirmed
	}
	return true, nil
}

// Existing completed files are authenticated after a lost ACK. Partial/corrupt
// attempts are preserved; explicit retry gets a NEW attempt directory.
func inspectArchive(path string, binding backup.Binding, key []byte) (ArchiveProof, error) {
	a, e := backup.Open(path, binding, key)
	if e != nil {
		return ArchiveProof{}, e
	}
	defer a.Close()
	files := a.Files()
	if len(files) != 8 {
		return ArchiveProof{}, ErrUnconfirmed
	}
	want := append(slices.Clone(coldVolumes), "compose", "release", "database")
	got := []string{}
	var size int64
	for _, f := range files {
		got = append(got, f.Name)
		size += f.Size
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		return ArchiveProof{}, ErrUnconfirmed
	}
	m := a.ManifestProof()
	return ArchiveProof{SHA256: m.SHA256, Bytes: size + m.Size, Files: 8}, nil
}
