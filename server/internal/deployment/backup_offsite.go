package deployment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/linli/im/server/internal/backup"
)

// State here is independent of cold capture/service recovery. In particular,
// a completed local backup MUST NOT be presented as an offsite success.
type OffsiteReceipt struct {
	State     string          `json:"state"`
	Attempts  int             `json:"attempts"`
	ErrorCode string          `json:"errorCode,omitempty"`
	RetryAt   time.Time       `json:"retryAt,omitzero"`
	UpdatedAt time.Time       `json:"updatedAt"`
	Delivery  backup.Delivery `json:"delivery,omitzero"`
}

func ValidBackupOffsite(r BackupReceipt) bool {
	o := r.Offsite
	if r.Operation.OffsiteTargetID == "" || r.State != "completed" {
		return o == (OffsiteReceipt{})
	}
	if !serviceName.MatchString(r.Operation.OffsiteTargetID) || !r.Archive.valid() || o.UpdatedAt.IsZero() || o.Attempts < 0 || o.Attempts > 1000000 {
		return false
	}
	if o.State == "completed" {
		d := o.Delivery
		return o.Attempts > 0 && o.ErrorCode == "" && o.RetryAt.IsZero() && d.Version == 1 && d.TargetID == r.Operation.OffsiteTargetID && d.Binding == r.Operation.Binding && d.Binding.Valid() && d.Manifest.Name == "manifest" && d.Manifest.SHA256 == r.Archive.SHA256 && d.Manifest.Size > 0 && d.Manifest.Size <= 1<<20 && d.Manifest.Size < r.Archive.Bytes
	}
	if o.Delivery != (backup.Delivery{}) || o.RetryAt.IsZero() {
		return false
	}
	switch o.State {
	case "pending":
		return o.Attempts == 0 && o.ErrorCode == ""
	case "running":
		return o.Attempts > 0 && o.ErrorCode == ""
	case "unconfirmed":
		return o.Attempts > 0 && o.ErrorCode == "BACKUP_OFFSITE_UNCONFIRMED"
	}
	return false
}

type backupDeliveryIO interface {
	Deliver(context.Context, backup.Binding, string, []byte) (backup.Delivery, error)
	Close()
}

func (x *Executor) ConfigureOffsite(c backup.OffsiteConfig) error {
	x.mu.Lock()
	if c.Scope != "enterprise" || c.TenantID != x.state.TenantID || c.ServerID != x.state.ServerID {
		x.mu.Unlock()
		return ErrState
	}
	x.mu.Unlock()
	o, e := backup.NewOffsite(c)
	if e != nil {
		return ErrState
	}
	x.mu.Lock()
	independent := o.IndependentKey(x.backupKey)
	x.mu.Unlock()
	if !independent {
		o.Close()
		return ErrState
	}
	// Pin the destination and owner, not rotating credentials or CA file paths.
	// Credential rotation cannot change where an existing job sends its bytes.
	c.AccessKey, c.SecretKey, c.CAFile = "", "", ""
	raw, _ := json.Marshal(c)
	h := sha256.Sum256(append([]byte("frogim-offsite-configuration-v1\x00"), raw...))
	if e = x.configureOffsite(c.ID, hex.EncodeToString(h[:]), o); e != nil {
		o.Close()
	}
	return e
}

func (x *Executor) configureOffsite(id, configuration string, o backupDeliveryIO) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed || x.poisoned || x.running || x.offsiteRunning || x.offsite != nil || x.backupIO == nil || !serviceName.MatchString(id) || !fingerprint.MatchString(configuration) || o == nil {
		return ErrState
	}
	if x.state.OffsiteConfiguration != "" {
		if x.state.OffsiteConfiguration != configuration || x.state.BackupOffsiteTargetID != id {
			return ErrState
		}
	} else {
		// Existing immutable operations retain their original local-only scope.
		// Installing a target on upgrade must not strand service recovery from
		// an older agent. Only newly submitted operations require this target.
		x.state.OffsiteConfiguration, x.state.BackupOffsiteTargetID = configuration, id
		if e := x.saveLocked(); e != nil {
			return e
		}
	}
	x.offsite = o
	return nil
}

// Only immutable completed archives are read here. This worker neither stops
// services nor owns the maintenance lock. It can run while the business resumes
// or a later deployment executes, without recapturing/restoring old resources.
func (x *Executor) OffsiteOnce(ctx context.Context) (bool, error) {
	x.mu.Lock()
	if x.closed || x.poisoned {
		x.mu.Unlock()
		return false, ErrState
	}
	if x.offsite == nil || x.offsiteRunning {
		x.mu.Unlock()
		return false, nil
	}
	var candidates []backupRecord
	now := time.Now().UTC()
	for _, r := range x.state.Backups {
		if r.State == "completed" && r.Operation.OffsiteTargetID != "" && r.Offsite.State != "completed" && !r.Offsite.RetryAt.After(now) {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		x.mu.Unlock()
		return false, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Offsite.RetryAt.Equal(b.Offsite.RetryAt) {
			return a.Operation.ID < b.Operation.ID
		}
		return a.Offsite.RetryAt.Before(b.Offsite.RetryAt)
	})
	r := candidates[0]
	r.Offsite.State, r.Offsite.ErrorCode = "running", ""
	r.Offsite.Attempts = min(r.Offsite.Attempts+1, 1000000)
	r.Offsite.UpdatedAt = now
	r.UpdatedAt, r.Revision = now, r.Revision+1
	x.state.Backups[r.Operation.ID] = r
	if e := x.saveLocked(); e != nil {
		x.mu.Unlock()
		return false, e
	}
	x.offsiteRunning = true
	x.wg.Add(1)
	key, remote := bytes.Clone(x.backupKey), x.offsite
	path := filepath.Join(x.backupRoot, fmt.Sprintf("%s-attempt-%03d", r.Operation.ID, r.Attempt))
	x.mu.Unlock()
	defer x.wg.Done()
	defer clear(key)
	call, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	// Recheck the exact archived proof, not just a valid archive at the path.
	proof, e := inspectArchive(path, r.Operation.Binding, key)
	var delivered backup.Delivery
	if e == nil && proof == r.Archive {
		delivered, e = remote.Deliver(call, r.Operation.Binding, path, key)
	} else {
		e = ErrUnconfirmed
	}
	now = time.Now().UTC()
	r.Offsite.State, r.Offsite.Delivery, r.Offsite.RetryAt = "completed", delivered, time.Time{}
	r.Offsite.UpdatedAt = now
	if e != nil || !ValidBackupOffsite(r.BackupReceipt) {
		r.Offsite.State, r.Offsite.ErrorCode = "unconfirmed", "BACKUP_OFFSITE_UNCONFIRMED"
		r.Offsite.Delivery = backup.Delivery{}
		r.Offsite.RetryAt = now.Add(time.Duration(min(900, 1<<min(r.Offsite.Attempts, 10))) * time.Second)
		e = ErrUnconfirmed
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.offsiteRunning = false
	r.UpdatedAt, r.Revision = now, r.Revision+1
	x.state.Backups[r.Operation.ID] = r
	if saveErr := x.saveLocked(); saveErr != nil {
		return true, saveErr
	}
	return true, e
}
