package httpapi

import (
	"net/http"

	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
)

func (x *API) writeCredentialResult(w http.ResponseWriter, result platform.TenantCredentialResult, err error) {
	if err != nil {
		writeError(w, 503, "PLATFORM_UNAVAILABLE", "密码任务结果尚未确认，请查询任务或使用原请求重试")
		return
	}
	switch result.ErrorCode {
	case "ACCOUNT_UNAVAILABLE":
		writeError(w, 409, result.ErrorCode, "账号不可操作或已有密码任务正在处理")
	case "REQUEST_CHANGED":
		writeError(w, 409, result.ErrorCode, "该请求号已用于其他内容，请先查询原任务")
	case "INVALID_ARGUMENT":
		writeError(w, 400, result.ErrorCode, "密码请求参数不正确")
	case "TENANT_PASSWORD_POLICY_REJECTED":
		writeError(w, 400, result.ErrorCode, "密码不符合本企业的密码长度要求")
	case "":
		if result.Item != nil && tenancy.ValidID(result.Item.ID) {
			write(w, 200, map[string]any{"item": result.Item})
			return
		}
		writeError(w, 503, "PLATFORM_UNAVAILABLE", "密码任务响应无法确认")
	default:
		writeError(w, 503, "PLATFORM_UNAVAILABLE", "密码任务响应无法确认")
	}
}

func (x *API) resetTenantUserPassword(w http.ResponseWriter, r *http.Request) {
	if x.cfg.TenantID == "" || x.platformControl == nil {
		writeError(w, 409, "PLATFORM_AUTH_REQUIRED", "此接口仅用于平台统一认证账号")
		return
	}
	var p struct {
		RequestID   string `json:"requestId"`
		NewPassword string `json:"newPassword"`
		Reason      string `json:"reason"`
		Confirmed   bool   `json:"confirmed"`
	}
	if decode(r, &p) != nil || !confirmedReason(p.Confirmed, p.Reason) || !tenancy.ValidID(p.RequestID) || !tenancy.ValidID(r.PathValue("id")) || len(p.NewPassword) > 72 || len([]rune(p.NewPassword)) < 8 {
		writeError(w, 400, "INVALID_ARGUMENT", "需要有效密码、请求号、操作理由和确认")
		return
	}
	var result platform.TenantCredentialResult
	err := x.platformControl.Call(r.Context(), "/internal/tenancy/admin/password-reset", platform.TenantPasswordReset{RequestID: p.RequestID, Actor: uid(r), LocalUserID: r.PathValue("id"), NewPassword: p.NewPassword, Reason: p.Reason, Confirmed: p.Confirmed}, &result)
	x.writeCredentialResult(w, result, err)
}

func (x *API) tenantUserCredentialStatus(w http.ResponseWriter, r *http.Request) {
	if x.cfg.TenantID == "" || x.platformControl == nil {
		writeError(w, 409, "PLATFORM_AUTH_REQUIRED", "此接口仅用于平台统一认证账号")
		return
	}
	if !tenancy.ValidID(r.PathValue("id")) || !tenancy.ValidID(r.PathValue("jobId")) {
		writeError(w, 400, "INVALID_ARGUMENT", "无效任务")
		return
	}
	var result platform.TenantCredentialResult
	err := x.platformControl.Call(r.Context(), "/internal/tenancy/admin/credential-job", platform.TenantCredentialQuery{LocalUserID: r.PathValue("id"), JobID: r.PathValue("jobId")}, &result)
	x.writeCredentialResult(w, result, err)
}

func (x *API) tenantUserCredentialJobs(w http.ResponseWriter, r *http.Request) {
	if x.cfg.TenantID == "" {
		write(w, 200, map[string]any{"managed": false, "items": []any{}})
		return
	}
	if x.platformControl == nil {
		writeError(w, 503, "PLATFORM_UNAVAILABLE", "平台暂不可用")
		return
	}
	if !tenancy.ValidID(r.PathValue("id")) {
		writeError(w, 400, "INVALID_ARGUMENT", "无效账号")
		return
	}
	var result platform.TenantCredentialResult
	err := x.platformControl.Call(r.Context(), "/internal/tenancy/admin/credential-job", platform.TenantCredentialQuery{LocalUserID: r.PathValue("id")}, &result)
	if err != nil || result.ErrorCode != "" {
		writeError(w, 503, "PLATFORM_UNAVAILABLE", "密码任务查询失败")
		return
	}
	if result.Items == nil {
		result.Items = []platform.CredentialJob{}
	}
	write(w, 200, map[string]any{"managed": true, "items": result.Items})
}
