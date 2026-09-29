package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
)

func (x *API) reserveTenantAdmin(ctx context.Context, actor, requestID string, in app.AdminUserBatchInput, reason string) (*platform.TenantAccountJob, string, string) {
	if x.platformControl == nil || x.tenantStore == nil {
		return nil, "PLATFORM_UNAVAILABLE", "平台账号服务暂不可用，请保留本次请求后重试"
	}
	if !tenancy.ValidID(requestID) {
		return nil, "INVALID_ARGUMENT", "需要有效开户请求号，请刷新管理后台"
	}
	if code, message := x.app.ValidateAdminUserCreation(in, reason); code != "" {
		return nil, code, message
	}
	var result platform.TenantAdminResult
	err := x.platformControl.Call(ctx, "/internal/tenancy/admin/accounts", platform.TenantAdminCreate{RequestID: requestID, Actor: actor, Phone: in.Phone, Name: in.Name, Password: in.Password, Gender: in.Gender, Reason: reason, Confirmed: true}, &result)
	if err != nil {
		return nil, "PLATFORM_UNAVAILABLE", "平台暂不可用，结果尚未确认；请查询开户任务或使用原请求重试"
	}
	switch result.ErrorCode {
	case "ACCOUNT_UNAVAILABLE":
		return nil, result.ErrorCode, "此号码不可开户"
	case "ACCOUNT_REQUEST_CHANGED":
		return nil, result.ErrorCode, "该请求号已用于其他开户内容，请先查询原开户任务"
	case "INVALID_ARGUMENT":
		return nil, result.ErrorCode, "开户参数不符合平台规则"
	case "":
		if result.Item != nil && result.Item.RequestID == requestID && tenancy.ValidID(result.Item.JobID) {
			return result.Item, "", ""
		}
	}
	return nil, "PLATFORM_UNAVAILABLE", "平台响应无法确认，请查询开户任务"
}

func (x *API) createTenantAdminUser(w http.ResponseWriter, r *http.Request, requestID string, in app.AdminUserBatchInput, reason string) {
	job, code, message := x.reserveTenantAdmin(r.Context(), uid(r), requestID, in, reason)
	if code != "" {
		status := 400
		if code == "PLATFORM_UNAVAILABLE" {
			status = 503
		} else if code == "ACCOUNT_UNAVAILABLE" || code == "ACCOUNT_REQUEST_CHANGED" {
			status = 409
		}
		writeError(w, status, code, message)
		return
	}
	write(w, http.StatusAccepted, map[string]any{"item": job})
}

type tenantBatchItem struct {
	ClientRow int                        `json:"clientRow"`
	Status    string                     `json:"status"`
	Job       *platform.TenantAccountJob `json:"job,omitempty"`
	Code      string                     `json:"code,omitempty"`
	Message   string                     `json:"message,omitempty"`
}

func (x *API) createTenantAdminBatch(w http.ResponseWriter, r *http.Request, requestID string, inputs []app.AdminUserBatchInput, reason string) {
	if !tenancy.ValidID(requestID) || len(requestID) > 64 {
		writeError(w, 400, "INVALID_ARGUMENT", "需要有效批量开户请求号")
		return
	}
	// Validate row identifiers before side effects; stable IDs survive retries
	// independent of worker scheduling. Duplicate phone rows are not submitted.
	rows := map[int]bool{}
	phones := map[string]int{}
	for _, in := range inputs {
		if in.ClientRow < 1 || in.ClientRow > 100000 || rows[in.ClientRow] {
			writeError(w, 400, "INVALID_ARGUMENT", "行号需要唯一且有效")
			return
		}
		rows[in.ClientRow] = true
		phones[strings.TrimPrefix(strings.TrimSpace(in.Phone), "+86")]++
	}
	items := make([]tenantBatchItem, len(inputs))
	var wg sync.WaitGroup
	work := make(chan int)
	actor := uid(r)
	for range min(4, len(inputs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				in := inputs[i]
				item := tenantBatchItem{ClientRow: in.ClientRow, Status: "failed"}
				if phones[strings.TrimPrefix(strings.TrimSpace(in.Phone), "+86")] > 1 {
					item.Code = "DUPLICATE_PHONE"
					item.Message = "批次内号码重复"
				} else {
					item.Job, item.Code, item.Message = x.reserveTenantAdmin(r.Context(), actor, requestID+"_"+strconv.Itoa(in.ClientRow), in, reason)
					if item.Job != nil {
						item.Status = "pending"
						if item.Job.Status == "completed" {
							item.Status = "created"
						}
					}
					if item.Code == "PLATFORM_UNAVAILABLE" {
						item.Status = "unknown"
					}
				}
				items[i] = item
			}
		}()
	}
	for i := range inputs {
		work <- i
	}
	close(work)
	wg.Wait()
	succeeded, pending, unknown := 0, 0, 0
	for _, item := range items {
		switch item.Status {
		case "created":
			succeeded++
		case "pending":
			pending++
		case "unknown":
			unknown++
		}
	}
	write(w, 200, map[string]any{"batchId": requestID, "total": len(items), "succeeded": succeeded, "pending": pending, "unknown": unknown, "failed": len(items) - succeeded - pending - unknown, "items": items})
}

func (x *API) tenantAdminJobs(w http.ResponseWriter, r *http.Request) {
	if x.cfg.TenantID == "" {
		write(w, 200, map[string]any{"managed": false, "items": []any{}})
		return
	}
	if x.platformControl == nil || x.tenantStore == nil {
		writeError(w, 503, "PLATFORM_UNAVAILABLE", "平台账号服务暂不可用")
		return
	}
	jobID := r.URL.Query().Get("jobId")
	if jobID != "" && !tenancy.ValidID(jobID) {
		writeError(w, 400, "INVALID_ARGUMENT", "任务号无效")
		return
	}
	var result platform.TenantAdminResult
	if err := x.platformControl.Call(r.Context(), "/internal/tenancy/admin/account-jobs", map[string]string{"jobId": jobID}, &result); err != nil || result.ErrorCode != "" {
		writeError(w, 503, "PLATFORM_UNAVAILABLE", "开户任务暂不可用")
		return
	}
	if result.Items == nil {
		result.Items = []platform.TenantAccountJob{}
	}
	write(w, 200, map[string]any{"managed": true, "items": result.Items})
}
