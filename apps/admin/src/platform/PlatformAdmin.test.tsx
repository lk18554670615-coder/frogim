import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { PlatformAdmin } from './PlatformAdmin';
import { PlatformClient } from './api';

const request = vi.fn();
let signedIn = false;
beforeEach(() => {
  signedIn = false;
  vi.stubGlobal('fetch', request); request.mockReset();
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  request.mockImplementation(async (url: string) => {
    if (url.endsWith('/auth/login')) { signedIn = true; return Response.json({ ok: true }); }
    if (url.endsWith('/auth/logout')) { signedIn = false; return Response.json({ ok: true }); }
    if (url.endsWith('/auth/me')) return signedIn ? Response.json({ id: 'operator', username: '平台运营', role: 'operator' }) : Response.json({ error: { code: 'INVALID_CREDENTIALS' } }, { status: 401 });
    if (url.includes('/tenants?')) return Response.json({ items: [{ id: 'a', displayName: '默认企业', status: 'active', httpBaseUrl: 'https://a.example', isDefault: true, configVersion: 1, accessVersion: 1 }], total: 1, page: 1, pageSize: 25 });
    if (url.includes('/accounts?')) return Response.json({ items: [{ id: 'account-1', phone: '13800000701', tenantId: 'a', localUserId: 'local-1', state: 'active', assignmentVersion: 3, authVersion: 7, globallyBlocked: false, accessPending: false }], total: 1, page: 1, pageSize: 25 });
    if (url.includes('/jobs?')) return Response.json({ items: [{ id: 'job_1', kind: 'registration', accountId: 'u1', targetTenantId: 'a', assignmentVersion: 1, step: 'prepare_target', blocked: true, leased: false, attempts: 1, errorCode: 'INVITE_INVALID', updatedAt: '2026-09-27T00:00:00Z' }], total: 1, page: 1, pageSize: 25 });
    return Response.json({ items: [], total: 0, page: 1, pageSize: 25 });
  });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
async function login() {
  await screen.findByRole('button', { name: '登录平台' });
  fireEvent.change(screen.getByLabelText('平台账号'), { target: { value: 'operator' } });
  fireEvent.change(screen.getByLabelText('密码'), { target: { value: 'platform-password' } });
  fireEvent.click(screen.getByRole('button', { name: '登录平台' }));
  await screen.findByRole('heading', { name: '企业目录' });
}

describe('separate platform operations', () => {
  it('restores the platform session after a page reload and clears it on logout', async () => {
    const first = render(<PlatformAdmin />); await login();
    await screen.findByText('默认企业', { selector: 'strong' });
    first.unmount();
    const second = render(<PlatformAdmin />);
    await screen.findByRole('heading', { name: '企业目录' });
    expect(screen.queryByRole('button', { name: '登录平台' })).not.toBeInTheDocument();
    expect(request.mock.calls.filter(([url]) => url.endsWith('/auth/login'))).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: '退出登录' }));
    await screen.findByRole('button', { name: '登录平台' });
    second.unmount();
    render(<PlatformAdmin />);
    await screen.findByRole('button', { name: '登录平台' });
  });
  it('keeps the authenticated view when logout cannot revoke the cookie', async () => {
    render(<PlatformAdmin />); await login();
    await screen.findByText('默认企业', { selector: 'strong' });
    request.mockRejectedValueOnce(new TypeError('network unavailable'));
    fireEvent.click(screen.getByRole('button', { name: '退出登录' }));
    await screen.findByText('network unavailable');
    expect(screen.getByRole('heading', { name: '企业目录' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '退出登录' }));
    await screen.findByRole('button', { name: '登录平台' });
  });
  it('opens administrator management without mixing the enterprise permission realm', async () => {
    render(<PlatformAdmin />); await login(); await screen.findByText('默认企业', { selector: 'strong' });
    request.mockResolvedValueOnce(Response.json({ items: [{ id: 'operator', username: '平台运营', role: 'operator', enabled: true, authVersion: 1, createdAt: '', updatedAt: '' }], total: 1, page: 1, pageSize: 25 }));
    fireEvent.click(screen.getByRole('button', { name: '平台管理员' }));
    await screen.findByRole('table', { name: '平台管理员' });
    expect(screen.getByRole('button', { name: '修改我的密码' })).toBeInTheDocument();
    expect(request.mock.calls.some(([url]) => url.startsWith('/platform/admin/administrators?'))).toBe(true);
    expect(request.mock.calls.some(([url]) => url.startsWith('/v2/admin/'))).toBe(false);
  });
  it('opens the global version page in the platform realm and clears it on logout', async () => {
    render(<PlatformAdmin />); await login(); await screen.findByText('默认企业', { selector: 'strong' });
    const policies = ['android', 'ios', 'web', 'macos'].map(platform => ({ platform, enabled: false, revision: 0, minimumVersion: '0.0.0', latestVersion: '0.0.0', forceUpdate: false, rolloutPercentage: 100, releaseNotes: '', downloadUrl: '', updatedBy: '', updatedAt: '' }));
    request.mockResolvedValueOnce(Response.json({ items: policies, total: 4, page: 1, pageSize: 25 }));
    fireEvent.click(screen.getByRole('button', { name: '客户端版本' }));
    expect(await screen.findAllByText('策略已停用')).toHaveLength(4);
    expect(request.mock.calls.filter(([url]) => url.endsWith('/client-versions')).map(([url]) => url)).toEqual(['/platform/admin/client-versions']);
    expect(screen.getByRole('button', { name: '编辑 Android 策略' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '退出登录' }));
    await screen.findByRole('button', { name: '登录平台' });
    expect(screen.queryByRole('region', { name: '平台客户端版本' })).not.toBeInTheDocument();
  });
  it('suspends a realm with a durable confirmed request and separate progress page', async () => {
    render(<PlatformAdmin />); await login();
    fireEvent.click(await screen.findByRole('button', { name: '停用企业' }));
    expect(screen.getByRole('dialog')).toHaveTextContent('已分享的固定媒体链接继续有效');
    fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '维护期间停用企业' } });
    fireEvent.click(screen.getByLabelText('我已确认上述操作与影响范围'));
    request.mockRejectedValueOnce(new TypeError('realm network failed'));
    fireEvent.click(screen.getByRole('button', { name: '确认操作' }));
    await screen.findByText('realm network failed');
    expect(screen.getByLabelText('操作理由')).toBeDisabled();
    const original = JSON.parse(request.mock.calls.find(([url]) => url.endsWith('/tenants/a/access'))![1].body);
    expect(original).toMatchObject({ enabled: false, expectedAccessVersion: 1, reason: '维护期间停用企业', confirmed: true });
    request.mockResolvedValueOnce(Response.json({ jobId: 'realm-1', status: 'pending' }, { status: 202 }));
    fireEvent.click(screen.getByRole('button', { name: '确认操作' }));
    fireEvent.click(await screen.findByRole('button', { name: '查看企业启停任务' }));
    await screen.findByRole('heading', { name: '企业启停任务' });
    const writes = request.mock.calls.filter(([url]) => url.endsWith('/tenants/a/access'));
    expect(writes).toHaveLength(2); expect(JSON.parse(writes[1][1].body)).toEqual(original);
    expect(screen.queryByText(/任务 realm-1 已完成/)).not.toBeInTheDocument();
  });
  it('hides opposite transitions until completion and shows batched realm progress', async () => {
    render(<PlatformAdmin />); await login(); await screen.findByRole('button', { name: '停用企业' });
    request.mockResolvedValueOnce(Response.json({ items: [{ id: 'a', displayName: '默认企业', status: 'suspending', configVersion: 1, accessVersion: 2 }], total: 1, page: 1, pageSize: 25 }));
    fireEvent.click(screen.getByRole('button', { name: '刷新' })); await screen.findByText('停用确认中');
    expect(screen.queryByRole('button', { name: '恢复企业' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '停用企业' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '创建邀请码' })).toBeDisabled();
    request.mockResolvedValueOnce(Response.json({ items: [{ jobId: 'realm-1', requestId: 'realm-request', tenantId: 'a', enabled: false, accessVersion: 2, status: 'pending', remaining: 17, attempts: 2, updatedAt: '2026-09-28T00:00:00Z' }], total: 1, page: 1, pageSize: 25 }));
    fireEvent.click(screen.getByRole('button', { name: '企业启停任务' }));
    await screen.findByText('待处理身份 17 个');
    expect(screen.getByText('分批处理，尚未完成')).toBeInTheDocument();
    expect(screen.getByText('请求号：realm-request')).toBeInTheDocument();
  });
  it('freezes a global ban request after uncertainty and retries exactly the original body', async () => {
    render(<PlatformAdmin />); await login();
    fireEvent.click(screen.getByRole('button', { name: '账号归属' }));
    fireEvent.click(await screen.findByRole('button', { name: '全局封禁' }));
    expect(screen.getByRole('dialog')).toHaveTextContent('不删除聊天记录');
    fireEvent.click(screen.getByRole('button', { name: '确认操作' }));
    expect(request.mock.calls.filter(([url]) => url.endsWith('/access'))).toHaveLength(0);
    fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '测试违规账号封禁' } });
    fireEvent.click(screen.getByLabelText('我已确认上述操作与影响范围'));
    request.mockRejectedValueOnce(new TypeError('network failed'));
    fireEvent.click(screen.getByRole('button', { name: '确认操作' }));
    await screen.findByText('network failed');
    expect(screen.getByLabelText('操作理由')).toBeDisabled();
    expect(screen.getByRole('dialog')).toHaveTextContent('关闭窗口不取消服务端任务');
    const original = JSON.parse(request.mock.calls.find(([url]) => url.endsWith('/access'))![1].body);
    expect(original).toMatchObject({ expectedAuthVersion: 7, blocked: true, reason: '测试违规账号封禁', confirmed: true });
    expect(original.requestId).toMatch(/^[a-f0-9-]{36}$/);
    request.mockResolvedValueOnce(Response.json({ jobId: 'access-1', status: 'applying' }, { status: 202 }));
    fireEvent.click(screen.getByRole('button', { name: '确认操作' }));
    await screen.findByText(/任务 access-1 已受理，企业会话处理尚未确认/);
    const writes = request.mock.calls.filter(([url]) => url.endsWith('/access'));
    expect(writes).toHaveLength(2); expect(JSON.parse(writes[1][1].body)).toEqual(original);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(localStorage.length).toBe(0);
  });
  it('shows durable ban progress without falsely calling an unconfirmed operation complete', async () => {
    render(<PlatformAdmin />); await login(); await screen.findByText('默认企业', { selector: 'strong' });
    request.mockResolvedValueOnce(Response.json({ items: [{ jobId: 'job-ban', requestId: 'request-ban', accountId: 'account-1', tenantId: 'a', blocked: true, status: 'applying', errorCode: 'ENTERPRISE_ACCESS_UNCONFIRMED', attempts: 3, updatedAt: '2026-09-28T00:00:00Z' }], total: 1, page: 1, pageSize: 25 }));
    fireEvent.click(screen.getByRole('button', { name: '封禁任务' }));
    await screen.findByText('企业确认中');
    expect(screen.getByText(/企业会话撤权尚未确认/)).toBeInTheDocument();
    expect(screen.getByText('请求号：request-ban')).toBeInTheDocument();
    expect(screen.getByRole('combobox')).toHaveTextContent('等待前置任务');
    fireEvent.click(screen.getByRole('button', { name: '退出登录' }));
    await screen.findByRole('button', { name: '登录平台' });
    expect(screen.queryByText('job-ban')).not.toBeInTheDocument();
  });
  it('does not report malformed acceptance as success or lose the original request', async () => {
    render(<PlatformAdmin />); await login();
    fireEvent.click(screen.getByRole('button', { name: '账号归属' }));
    fireEvent.click(await screen.findByRole('button', { name: '全局封禁' }));
    fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '不确定响应检查' } });
    fireEvent.click(screen.getByLabelText('我已确认上述操作与影响范围'));
    request.mockResolvedValueOnce(Response.json({ ok: true }));
    fireEvent.click(screen.getByRole('button', { name: '确认操作' }));
    await screen.findByText('返回结果无法确认，请使用原请求重试或查询封禁任务');
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByLabelText('操作理由')).toBeDisabled();
    expect(screen.queryByText(/已受理，企业会话/)).not.toBeInTheDocument();
  });
  it('distinguishes global unblocking from enterprise bans and prevents pending reversals', async () => {
    render(<PlatformAdmin />); await login(); await screen.findByText('默认企业', { selector: 'strong' });
    const account = { id: 'blocked', phone: '13800000702', tenantId: 'a', localUserId: 'local', state: 'blocked', globallyBlocked: true, authVersion: 9, assignmentVersion: 1, accessPending: true };
    request.mockResolvedValueOnce(Response.json({ items: [account], total: 1, page: 1, pageSize: 25 }));
    fireEvent.click(screen.getByRole('button', { name: '账号归属' }));
    expect(await screen.findByRole('button', { name: '解除全局封禁' })).toBeDisabled();
    request.mockResolvedValueOnce(Response.json({ items: [{ ...account, accessPending: false }], total: 1, page: 1, pageSize: 25 }));
    fireEvent.click(screen.getByRole('button', { name: '刷新' }));
    fireEvent.click(await screen.findByRole('button', { name: '解除全局封禁' }));
    expect(screen.getByRole('dialog')).toHaveTextContent('企业自己的封禁');
    expect(screen.getByRole('dialog')).toHaveTextContent('不恢复旧会话');
  });
  it('requires confirmation and sends the observed configuration version for activation', async () => {
    const client = new PlatformClient();
    vi.spyOn(client, 'login').mockResolvedValue({ id: 'operator', username: '平台运营', role: 'operator' });
    const api = vi.spyOn(client, 'request').mockRejectedValueOnce(new Error('no restored session')).mockResolvedValue({ items: [{ id: 'new', displayName: '待开通企业', status: 'provisioning', httpBaseUrl: 'https://new.example', isDefault: false, configVersion: 7 }], total: 1, page: 1, pageSize: 25 });
    render(<PlatformAdmin client={client} />); await login();
    fireEvent.click(await screen.findByRole('button', { name: '检查并激活' }));
    expect(screen.getByRole('dialog')).toHaveTextContent('任一失败均不激活');
    fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '隔离环境验收' } });
    fireEvent.click(screen.getByLabelText('我已确认上述操作与影响范围'));
    api.mockRejectedValueOnce(new Error('企业运行依赖未就绪'));
    fireEvent.click(screen.getByRole('button', { name: '确认操作' }));
    await screen.findByRole('alert');
    expect(api).toHaveBeenCalledWith('/tenants/new/activate', 'POST', { expectedConfigVersion: 7, reason: '隔离环境验收', confirmed: true });
    expect(screen.getByLabelText('操作理由')).toHaveValue('隔离环境验收');
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });
  it('uses only the platform realm and shows a one-time code after reason and confirmation', async () => {
    render(<PlatformAdmin />); await login();
    fireEvent.click(await screen.findByRole('button', { name: '创建邀请码' }));
    expect(screen.getByRole('dialog')).toHaveTextContent('已注册账号不会因此调换企业');
    fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '企业开通测试' } });
    fireEvent.click(screen.getByLabelText('我已确认上述操作与影响范围'));
    request.mockImplementationOnce(async () => Response.json({ code: 'TEST-ONE-TIME', id: 'code1' }));
    fireEvent.click(screen.getByRole('button', { name: '确认操作' }));
    await screen.findByText('TEST-ONE-TIME');
    expect(request.mock.calls.every(([url]) => String(url).startsWith('/platform/admin/'))).toBe(true);
    const write = request.mock.calls.find(([url]) => String(url).endsWith('/tenants/a/codes'))!;
    expect(JSON.parse(write[1].body)).toEqual({ reason: '企业开通测试', confirmed: true });
    expect(localStorage.length).toBe(0);
    fireEvent.click(screen.getByRole('button', { name: '已保存，隐藏邀请码' }));
    expect(screen.queryByText('TEST-ONE-TIME')).not.toBeInTheDocument();
  });
  it('retains failed correction inputs and explains the safe retry boundary', async () => {
    render(<PlatformAdmin />); await login();
    fireEvent.click(screen.getByRole('button', { name: '身份任务' }));
    fireEvent.click(await screen.findByRole('button', { name: '修正邀请码' }));
    fireEvent.change(screen.getByLabelText('个人邀请码'), { target: { value: 'REF123' } });
    fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '修正无效邀请码' } });
    fireEvent.click(screen.getByLabelText('我已确认上述操作与影响范围'));
    request.mockImplementationOnce(async () => Response.json({ error: { code: 'ACCOUNT_UNAVAILABLE' } }, { status: 409 }));
    fireEvent.click(screen.getByRole('button', { name: '确认操作' }));
    await screen.findByRole('alert');
    expect(screen.getByLabelText('个人邀请码')).toHaveValue('REF123');
    expect(screen.getByRole('dialog')).toHaveTextContent('不更改企业归属或已有邀请关系');
  });
  it('hides write controls from a reader', async () => {
    const client = new PlatformClient();
    vi.spyOn(client, 'login').mockResolvedValue({ id: 'reader', username: '只读', role: 'reader' });
    render(<PlatformAdmin client={client} />); await login();
    await screen.findByText('默认企业', { selector: 'strong' });
    expect(screen.queryByRole('button', { name: '登记企业' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '创建邀请码' })).not.toBeInTheDocument();
    expect(screen.getByText('只读', { selector: 'span' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '账号归属' }));
    await screen.findByText('13800000701');
    expect(screen.queryByRole('button', { name: '全局封禁' })).not.toBeInTheDocument();
  });
  it('does not carry an enterprise credential or follow redirects', async () => {
    const client = new PlatformClient();
    await client.login('operator', 'secret');
    const [url, options] = request.mock.calls[0];
    expect(url).toBe('/platform/admin/auth/login');
    expect(options.credentials).toBe('same-origin'); expect(options.redirect).toBe('error');
    expect(options.headers['X-Platform-Session']).toBe('cookie');
    expect(options.headers.Authorization).toBeUndefined();
    client.clear();
    await client.request('/tenants?page=1');
    expect(request.mock.lastCall?.[1].headers.Authorization).toBeUndefined();
  });
  it('immediately clears the screen after an expired platform session', async () => {
    render(<PlatformAdmin />); await login(); await screen.findByText('默认企业', { selector: 'strong' });
    request.mockImplementationOnce(async () => Response.json({ error: { code: 'INVALID_CREDENTIALS' } }, { status: 401 }));
    fireEvent.click(screen.getByRole('button', { name: '刷新' }));
    await waitFor(() => expect(screen.getByRole('heading', { name: '平台运营登录' })).toBeInTheDocument());
    expect(screen.queryByText('https://a.example')).not.toBeInTheDocument();
  });
});
