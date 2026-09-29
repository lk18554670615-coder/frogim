package platform

import (
	"net/http"
	"strings"

	"github.com/linli/im/server/internal/tenancy"
)

func (a *API) adminBackupSchedules(w http.ResponseWriter, r *http.Request) {
	a.adminPage(w, r, `SELECT s.tenant_id AS ordering,jsonb_build_object('tenantId',s.tenant_id,'enabled',s.enabled,'startMinuteUtc',s.start_minute_utc,'windowMinutes',s.window_minutes,'version',s.version,'actorId',s.actor_id,'updatedAt',s.updated_at,
 'operatorEnabled',a.enabled AND a.role='operator') AS data FROM platform_backup_schedules s JOIN platform_admin_accounts a ON a.id=s.actor_id WHERE ($3='' OR s.tenant_id=$3)`, strings.TrimSpace(r.URL.Query().Get("q")))
}
func (a *API) adminSetBackupSchedule(w http.ResponseWriter, r *http.Request) {
	var in BackupScheduleInput
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	out, e := a.Store.SetBackupSchedule(r.Context(), actorID(r), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), r.PathValue("id"), in)
	if e != nil {
		failure(w, e)
		return
	}
	respond(w, 200, out)
}
func (a *API) adminMaintenance(w http.ResponseWriter, r *http.Request) {
	a.adminPage(w, r, `SELECT m.created_at::text||m.id AS ordering,jsonb_build_object('id',m.id,'tenantId',m.tenant_id,'actorId',m.actor_id,'scheduleVersion',m.schedule_version,'slotDate',m.slot_date,'windowEnd',m.window_end,
 'state',m.state,'phase',m.phase,'pauseId',m.pause_id,'backupId',m.backup_id,'resumeId',m.resume_id,'backupResult',m.backup_result,'errorCode',m.error_code,'updatedAt',m.updated_at) AS data FROM platform_maintenance_runs m WHERE ($3='' OR m.tenant_id=$3 OR m.id=$3)`, strings.TrimSpace(r.URL.Query().Get("q")))
}
