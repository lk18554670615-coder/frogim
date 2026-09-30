package platform

import (
	"encoding/json"
	"net/http"
)

// adminOverview is deliberately read-only and returns task metadata only.
// It never joins account contact details or enterprise business content.
func (a *API) adminOverview(w http.ResponseWriter, r *http.Request) {
	const query = `WITH tasks AS (
	 SELECT 'jobs' AS kind,id,COALESCE(target_tenant_id,source_tenant_id,'') AS tenant_id,
	   step AS state,error_code,created_at,updated_at,
	   blocked AND step<>'completed' AS attention,
	   NOT blocked AND step<>'completed' AS in_progress
	 FROM platform_jobs
	 UNION ALL SELECT 'access-jobs',id,COALESCE(tenant_id,''),state,error_code,created_at,updated_at,
	   state<>'completed' AND error_code<>'',state<>'completed' AND error_code=''
	 FROM platform_access_jobs
	 UNION ALL SELECT 'realm-jobs',id,tenant_id,state,error_code,created_at,updated_at,
	   state<>'completed' AND error_code<>'',state<>'completed' AND error_code=''
	 FROM platform_realm_jobs
	 UNION ALL SELECT 'deployments',id,tenant_id,state,error_code,created_at,updated_at,
	   state='unconfirmed',state='pending'
	 FROM platform_deployment_jobs
	 UNION ALL SELECT 'backups',id,tenant_id,state,error_code,created_at,updated_at,
	   state IN ('unconfirmed','failed'),state='pending'
	 FROM platform_backup_jobs
	 UNION ALL SELECT 'maintenance',id,tenant_id,state,error_code,created_at,updated_at,
	   state='failed',state='pending'
	 FROM platform_maintenance_runs
	), tenant_counts AS (
	 SELECT count(*) FILTER (WHERE archived_at IS NULL AND status='active') AS active,
	   count(*) FILTER (WHERE archived_at IS NULL AND status='provisioning') AS provisioning,
	   count(*) FILTER (WHERE archived_at IS NULL AND status IN ('suspending','suspended','resuming')) AS suspended,
	   count(*) FILTER (WHERE archived_at IS NOT NULL) AS archived
	 FROM platform_tenants
	)
	SELECT jsonb_build_object(
	 'generatedAt',clock_timestamp(),
	 'tenants',(SELECT jsonb_build_object('active',active,'provisioning',provisioning,'suspended',suspended,'archived',archived) FROM tenant_counts),
	 'work',(SELECT jsonb_build_object('inProgress',count(*) FILTER (WHERE in_progress),'attention',count(*) FILTER (WHERE attention)) FROM tasks),
	 'items',COALESCE((SELECT jsonb_agg(jsonb_build_object('kind',kind,'id',id,'tenantId',tenant_id,'state',state,'errorCode',error_code,'updatedAt',updated_at) ORDER BY created_at,kind,id)
	   FROM (SELECT * FROM tasks WHERE attention ORDER BY created_at,kind,id LIMIT 10) oldest),'[]'::jsonb))`
	var result json.RawMessage
	if err := a.Store.pool.QueryRow(r.Context(), query).Scan(&result); err != nil {
		failure(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}
