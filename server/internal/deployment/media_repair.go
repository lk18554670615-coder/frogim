package deployment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"maps"
	"strings"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/tenancy"
)

// Operator-only, cold repair. This is not a platform/agent HTTP command and
// cannot stop services, resume a realm, finish a revocation or issue a token.
type MediaRepairRequest struct {
	RequestID        string `json:"requestId"`
	PauseOperationID string `json:"pauseOperationId"`
	Actor            string `json:"actor"`
	Reason           string `json:"reason"`
	Confirmed        bool   `json:"confirmed"`
}

func (r MediaRepairRequest) valid() bool {
	return tenancy.ValidID(r.RequestID) && tenancy.ValidID(r.PauseOperationID) && len(strings.TrimSpace(r.Actor)) > 0 && len(r.Actor) <= 100 && len(strings.TrimSpace(r.Reason)) >= 3 && len(r.Reason) <= 500 && r.Confirmed
}

type MediaRepairResult struct {
	RequestID       string `json:"requestId"`
	TenantID        string `json:"tenantId"`
	AccessVersion   int64  `json:"accessVersion"`
	ClearedAttempts int    `json:"clearedAttempts"`
	RuntimeProof    string `json:"runtimeProof"`
	AccessEnabled   bool   `json:"accessEnabled"`
}
type mediaRepairInput struct {
	Request      MediaRepairRequest `json:"request"`
	Binding      backup.Binding     `json:"binding"`
	RuntimeProof string             `json:"runtimeProof"`
}

// All nine original containers and six volumes must match the durable agent
// journal, and all writers/signaling/LiveKit must already be stopped. Reopening
// that journal requires the agent to be stopped too. No fresh-journal adoption.
// PostgreSQL is the only remaining service. A paused (not necessarily drained)
// realm is mandatory; deleting an attempt based only on age is never allowed.
func (x *Executor) RepairColdMedia(ctx context.Context, binding backup.Binding, request MediaRepairRequest) (MediaRepairResult, error) {
	var empty MediaRepairResult
	if !binding.Valid() || binding.Scope != "" || binding.AccessVersion < 2 || !request.valid() {
		return empty, ErrBundle
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.state.ActiveBackupID != "" || x.offsiteRunning {
		return empty, ErrConflict
	}
	r, release, operation, b, e := x.coldSource(ctx, binding)
	if e != nil {
		return empty, e
	}
	containers, e := r.ownedBackupContainers(ctx, release, operation, b, true)
	if e != nil {
		return empty, e
	}
	volumes, e := r.backupVolumeIdentity(ctx, b)
	if e != nil {
		return empty, e
	}
	source := backupSource{Containers: containers, Volumes: volumes}
	if !source.valid() {
		return empty, ErrUnconfirmed
	}
	raw, _ := json.Marshal(source)
	hash := sha256.Sum256(raw)
	proof := hex.EncodeToString(hash[:])
	input := mediaRepairInput{request, binding, proof}
	query := mediaRepairSQL(input)
	out, e := r.command(ctx, []byte(query), "exec", "-i", containers["enterprise-db"], "psql", "-X", "-q", "-U", "enterprise", "-d", "enterprise", "-At", "-v", "ON_ERROR_STOP=1", "-f", "-")
	if e != nil {
		return empty, ErrUnconfirmed
	}
	var result MediaRepairResult
	if json.Unmarshal(bytes.TrimSpace(out), &result) != nil || result.RequestID != request.RequestID || result.TenantID != binding.TenantID || result.AccessVersion != binding.AccessVersion || result.RuntimeProof != proof || result.ClearedAttempts < 1 || result.AccessEnabled {
		return empty, ErrUnconfirmed
	}
	// A supervisor restarting/changing an original process during the repair
	// invalidates operational confirmation. The DB audit remains recoverable.
	current, e := r.ownedBackupContainers(ctx, release, operation, b, true)
	if e != nil || !maps.Equal(current, containers) {
		return empty, ErrUnconfirmed
	}
	currentVolumes, e := r.backupVolumeIdentity(ctx, b)
	if e != nil || !maps.Equal(currentVolumes, volumes) {
		return empty, ErrUnconfirmed
	}
	return result, nil
}

// The only interpolated values are locally generated base64 JSON and a hex ID.
// Operator text never becomes SQL syntax or a shell argument. psql uses stdin,
// -X, ON_ERROR_STOP and a single transaction; stderr is never forwarded.
func mediaRepairSQL(input mediaRepairInput) string {
	raw, _ := json.Marshal(input)
	encoded := base64.StdEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte("frogim-cold-media-repair-v1\x00" + input.Binding.TenantID + "\x00" + input.Request.RequestID))
	id := "aud_media_repair_" + hex.EncodeToString(hash[:])
	return `BEGIN;
SET LOCAL lock_timeout='5s'; SET LOCAL statement_timeout='30s';
DO $repair$
DECLARE
 input jsonb:=convert_from(decode('` + encoded + `','base64'),'UTF8')::jsonb;
 prior jsonb; cleared integer; outcome jsonb;
BEGIN
 PERFORM pg_advisory_xact_lock(490739177);
 PERFORM 1 FROM im_tenant_identity WHERE tenant_id=input->'binding'->>'tenantId'
  AND access_version=(input->'binding'->>'accessVersion')::bigint AND NOT access_enabled FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'repair gate unavailable'; END IF;
 IF (SELECT MAX(version) FROM im_schema_migrations) IS DISTINCT FROM (input->'binding'->>'schemaVersion')::integer
  OR NOT EXISTS(SELECT 1 FROM im_tenant_realm_operations WHERE operation_id=input->'request'->>'pauseOperationId'
   AND access_version=(input->'binding'->>'accessVersion')::bigint AND NOT enabled AND state IN ('revoking','completed'))
  OR EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND backend_type='client backend' AND pid<>pg_backend_pid())
 THEN RAISE EXCEPTION 'repair suspension unconfirmed'; END IF;
 LOCK TABLE im_tenant_media_attempts IN ACCESS EXCLUSIVE MODE;
 SELECT metadata INTO prior FROM im_audits WHERE id='` + id + `' FOR UPDATE;
 IF FOUND THEN
  IF prior->'input' IS DISTINCT FROM input OR prior->'result' IS NULL THEN RAISE EXCEPTION 'repair request changed'; END IF;
 ELSE
  DELETE FROM im_tenant_media_attempts;
  GET DIAGNOSTICS cleared=ROW_COUNT;
  IF cleared<1 THEN RAISE EXCEPTION 'no ambiguous media attempts'; END IF;
  outcome:=jsonb_build_object('requestId',input->'request'->>'requestId','tenantId',input->'binding'->>'tenantId',
   'accessVersion',(input->'binding'->>'accessVersion')::bigint,'clearedAttempts',cleared,
   'runtimeProof',input->>'runtimeProof','accessEnabled',false);
  INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at)
   VALUES('` + id + `',input->'request'->>'actor','tenant.media.cold_repaired','tenant',input->'binding'->>'tenantId',
    jsonb_build_object('input',input,'result',outcome),clock_timestamp());
 END IF;
END $repair$;
COMMIT;
SELECT metadata->'result' FROM im_audits WHERE id='` + id + `';
`
}
