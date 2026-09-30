export type Operator = { id: string; username: string; role: 'operator' | 'reader' };
export type Tenant = { id: string; displayName: string; httpBaseUrl: string; status: string; isDefault: boolean; configVersion: number; accessVersion: number; directoryVersion: number; archivedAt: string | null };
export type TenantDetail = Tenant & { note: string; currentDefaultId: string; archivedBy: string | null; createdAt: string; updatedAt: string; accountCount: number; enabledCodeCount: number; serverCount: number; pendingJobCount: number; maintenanceEnabled: boolean };
export type RealmJob = { jobId: string; requestId: string; tenantId: string; enabled: boolean; accessVersion: number; status: 'pending' | 'completed'; remaining: number; errorCode?: string; attempts: number; updatedAt: string };
export type Account = { id: string; phone: string; tenantId: string; localUserId: string; state: string; assignmentVersion: number; authVersion: number; globallyBlocked: boolean; accessPending: boolean; createdAt: string };
export type AccessJob = { jobId: string; requestId: string; accountId: string; tenantId: string; blocked: boolean; status: 'waiting' | 'applying' | 'completed'; errorCode?: string; attempts: number; updatedAt: string };
export type EnterpriseCode = { id: string; tenantId: string; enabled: boolean; suffix: string; createdAt: string };
export type IdentityJob = { id: string; kind: string; accountId: string; sourceTenantId: string | null; targetTenantId: string; assignmentVersion: number; step: string; errorCode: string; attempts: number; blocked: boolean; leased: boolean; updatedAt: string };
export type Audit = { id: string; actorId: string; action: string; accountId: string | null; tenantId: string | null; jobId: string | null; reason: string; metadata: Record<string, unknown>; createdAt: string };
export type Page<T> = { items: T[]; total: number; page: number; pageSize: number };

export class PlatformError extends Error {
  constructor(public code: string, message: string) { super(message); }
}

/** Browser sessions use a path-scoped HttpOnly cookie; no token enters web storage. */
export class PlatformClient {
  private generation = 0;
  private requests = new Set<AbortController>();
  onExpired: (() => void) | undefined;

  async login(username: string, password: string): Promise<Operator> {
    this.clear();
    const epoch = this.generation;
    const session = await this.request<{ ok: boolean }>('/auth/login', 'POST', { username, password }, undefined, { 'X-Platform-Session': 'cookie' });
    if (epoch !== this.generation || session.ok !== true) throw new PlatformError('INVALID_SESSION', '平台会话无效');
    try { return await this.request<Operator>('/auth/me'); }
    catch (error) { this.clear(); throw error; }
  }

  async logout(): Promise<void> {
    const epoch = this.generation;
    try { await this.request('/auth/logout', 'POST', {}); }
    finally { if (epoch === this.generation) this.clear(); }
  }

  clear(): void {
    this.generation++;
    for (const request of this.requests) request.abort();
    this.requests.clear();
  }

  async request<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal, extraHeaders?: Record<string, string>): Promise<T> {
    if (!/^\/[a-z0-9/-]+(?:\?[^#]*)?$/i.test(path) || path.includes('..')) throw new Error('Invalid platform route');
    const epoch = this.generation;
    const controller = new AbortController();
    const abort = () => controller.abort();
    signal?.addEventListener('abort', abort, { once: true });
    if (signal?.aborted) abort();
    this.requests.add(controller);
    const timeout = setTimeout(abort, 15000);
    try {
      const response = await fetch(`/platform/admin${path}`, {
        method, credentials: 'same-origin', redirect: 'error', cache: 'no-store', signal: controller.signal,
        headers: { 'Content-Type': 'application/json', ...extraHeaders },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const result = await response.json();
      if (epoch !== this.generation) throw new PlatformError('STALE_SESSION', '会话已变更');
      if (!response.ok) {
        if (response.status === 401) { this.clear(); this.onExpired?.(); }
        if (result.error?.code === 'GROUP_OWNERSHIP_TRANSFER_REQUIRED') throw new PlatformError(result.error.code, '请先转让该用户在源企业拥有的群，再调换企业');
        if (result.error?.code === 'ACCOUNT_OPERATION_PENDING') throw new PlatformError(result.error.code, '该账号尚有未完成任务，请查看封禁任务或身份任务，等待确认后再操作');
        if (result.error?.code === 'ADMIN_CURRENT_PASSWORD_INVALID') throw new PlatformError(result.error.code, '当前密码不正确，请关闭窗口后重新填写');
        if (result.error?.code === 'ADMIN_SELF_ACCESS_CHANGE') throw new PlatformError(result.error.code, '不能修改本人的启停状态或角色，请由另一名运营管理员操作');
        if (result.error?.code === 'LAST_PLATFORM_OPERATOR') throw new PlatformError(result.error.code, '必须保留至少一名启用的运营管理员');
        if (result.error?.code === 'SERVER_BINDING_CHANGED') throw new PlatformError(result.error.code, '服务器绑定或企业配置已变化，不会覆盖现有绑定；请关闭窗口并刷新后确认');
        if (result.error?.code === 'HOST_AGENT_UNAVAILABLE') throw new PlatformError(result.error.code, '代理不可用或身份校验失败，未保存本次操作。请由运维检查配置后重试。');
        if (result.error?.code === 'DEPLOYMENT_MAINTENANCE_REQUIRED') throw new PlatformError(result.error.code, '企业须首次开通或已确认停用，且没有冲突任务；部署未确认前不能恢复访问。');
        if (result.error?.code === 'DEPLOYMENT_STATE_CHANGED') throw new PlatformError(result.error.code, '发布目录、服务器或部署代次已变化；请查询原任务，关闭窗口并重新读取部署条件，不会覆盖已有任务。');
        if (result.error?.code === 'MAINTENANCE_STATE_CHANGED') throw new PlatformError(result.error.code, '维护计划或企业状态已变化；请关闭窗口并重新读取，不会覆盖其他操作者的修改。');
        if (result.error?.code === 'TENANT_DIRECTORY_CHANGED') throw new PlatformError(result.error.code, '企业目录已变化，请刷新详情后重新确认');
        if (result.error?.code === 'TENANT_ARCHIVE_BLOCKED') throw new PlatformError(result.error.code, '请先停用企业、完成关联任务并切换默认企业');
        throw new PlatformError(result.error?.code ?? 'UNAVAILABLE', response.status === 401 ? '账号或会话不可用，请重新登录' : response.status === 409 ? '当前状态已变化或账号不可操作，请刷新后重试' : response.status === 400 ? '请检查填写的内容、操作理由与确认项' : response.status === 429 ? '操作过于频繁，请稍后重试' : '平台服务暂不可用，请稍后重试');
      }
      return result as T;
    } finally {
      clearTimeout(timeout); signal?.removeEventListener('abort', abort); this.requests.delete(controller);
    }
  }
}

export const taskErrors: Record<string, string> = {
  GROUP_OWNERSHIP_TRANSFER_REQUIRED: '请先在源企业转让该用户拥有的群，再重试',
  TENANT_REGISTRATION_DISABLED: '目标企业已关闭注册，请先确认注册策略',
  TENANT_PASSWORD_POLICY_REJECTED: '密码不符合目标企业策略，需要处理注册资料',
  INVITE_REQUIRED: '目标企业要求个人邀请码，请修正注册资料',
  INVITE_INVALID: '个人邀请码无效，请修正注册资料',
  INVITE_DISABLED: '个人邀请码已停用，请修正注册资料',
  ENTERPRISE_OPERATION_UNCONFIRMED: '企业操作尚未确认；系统按原操作号重试，不会跳过撤权',
  IDENTITY_PROGRESS_UNCONFIRMED: '任务进度尚未确认或目标企业已停用；保留当前阶段，系统按原任务重试',
  ENTERPRISE_ACCESS_UNCONFIRMED: '企业会话撤权尚未确认；保持禁止平台登录，系统按原任务重试',
  ENTERPRISE_REALM_UNCONFIRMED: '企业停用或恢复尚未确认；平台登录保持关闭，系统按原任务重试',
  WAITING_ACCOUNT_OPERATION: '正在等待已受理的开户、调换或改密任务结束；期间禁止平台登录',
};
