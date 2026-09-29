import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { PlatformAdministrators } from './Administrators';
import { PlatformClient } from './api';

const request = vi.fn();
const rows = [{ id: 'one', username: 'OperatorOne', role: 'operator', enabled: true, authVersion: 1, createdAt: '', updatedAt: '' }, { id: 'two', username: 'ReaderTwo', role: 'reader', enabled: true, authVersion: 2, createdAt: '', updatedAt: '' }];
const operator = { id: 'one', username: 'OperatorOne', role: 'operator' as const };
beforeEach(() => {
  request.mockReset(); vi.stubGlobal('fetch', request);
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  request.mockResolvedValue(Response.json({ items: rows, total: 2, page: 1, pageSize: 25 }));
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
function mount(current = operator, onPasswordChanged = vi.fn()) { return render(<PlatformAdministrators client={new PlatformClient()} operator={current} refresh={0} onPasswordChanged={onPasswordChanged} />); }
function confirm() {
  fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '本机隔离权限验证' } });
  fireEvent.click(screen.getByLabelText('我已确认目标账号、角色及旧会话失效的影响'));
  fireEvent.click(screen.getByRole('button', { name: '确认管理员操作' }));
}
describe('platform administrator lifecycle', () => {
  it('keeps reader inspection and own password change, never operator controls', async () => {
    render(<PlatformAdministrators client={new PlatformClient()} operator={{ id: 'two', username: 'ReaderTwo', role: 'reader' }} refresh={0} onPasswordChanged={vi.fn()} />);
    await screen.findByRole('table', { name: '平台管理员' });
    expect(screen.queryByRole('button', { name: '新增平台管理员' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /调整权限/ })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '修改我的密码' })).toBeInTheDocument();
  });
  it('never offers self access changes and requires confirmation before account creation', async () => {
    mount(); await screen.findByRole('table');
    expect(screen.queryByRole('button', { name: '调整权限 · OperatorOne' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '新增平台管理员' }));
    expect(screen.getByLabelText('管理角色')).toHaveValue('reader');
    fireEvent.change(screen.getByLabelText('新管理员账号'), { target: { value: 'NewReader' } });
    fireEvent.change(screen.getByLabelText('新密码'), { target: { value: 'TestOnlyPassword123!' } });
    fireEvent.submit(screen.getByRole('button', { name: '确认管理员操作' }).closest('form')!);
    await screen.findByText('请填写操作理由并确认权限影响');
    expect(request.mock.calls.some(([, o]) => o.method === 'POST')).toBe(false);
    request.mockResolvedValueOnce(Response.json({ ...rows[1], id: 'new', username: 'NewReader', authVersion: 1 })); confirm();
    await screen.findByText('NewReader 的操作已提交。状态与历史会话以服务器结果为准。');
    expect(localStorage.length).toBe(0);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
  it('checks lost results without retransmitting the password, and locks the original request', async () => {
    mount(); fireEvent.click(await screen.findByRole('button', { name: '重置密码 · ReaderTwo' }));
    fireEvent.change(screen.getByLabelText('新密码'), { target: { value: 'TestOnlyPassword123!' } });
    request.mockRejectedValueOnce(new TypeError('acceptance lost')); confirm();
    await screen.findByText('acceptance lost');
    expect(screen.getByLabelText('新密码')).toBeDisabled();
    const posted = JSON.parse(request.mock.calls.find(([, o]) => o.method === 'POST')![1].body);
    expect(posted).toMatchObject({ action: 'password', targetId: 'two', expectedVersion: 2, role: '', enabled: null });
    request.mockResolvedValueOnce(Response.json({ ...rows[1], authVersion: 3 }));
    fireEvent.click(screen.getByRole('button', { name: '查询提交结果' }));
    await screen.findByText(/ReaderTwo 的操作已提交/);
    expect(request.mock.calls.some(([u, o]) => u.endsWith(`/operations/${posted.requestId}`) && o.method === 'GET' && !o.body)).toBe(true);
    expect(request.mock.calls.filter(([, o]) => o.method === 'POST')).toHaveLength(1);
  });
  it('requires current password and exits the local session after a successful self change', async () => {
    const changed = vi.fn(); mount(operator, changed);
    fireEvent.click(await screen.findByRole('button', { name: '修改我的密码' }));
    expect(screen.getByLabelText('当前密码')).toBeRequired();
    fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'PreviousTestPassword123!' } });
    fireEvent.change(screen.getByLabelText('新密码'), { target: { value: 'TestOnlyPassword123!' } });
    request.mockResolvedValueOnce(Response.json({ ...rows[0], authVersion: 2 })); confirm();
    await vi.waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
  });
  it('does not treat malformed or conflicting responses as completed changes', async () => {
    mount(); fireEvent.click(await screen.findByRole('button', { name: '调整权限 · ReaderTwo' }));
    fireEvent.click(screen.getByLabelText('启用管理员账号'));
    request.mockResolvedValueOnce(Response.json({ ok: true })); confirm();
    await screen.findByText('响应无法确认，请查询原请求结果');
    const original = request.mock.calls.find(([, o]) => o.method === 'POST')![1].body;
    request.mockResolvedValueOnce(Response.json({ error: { code: 'ADMIN_ACCOUNT_CHANGED' } }, { status: 409 }));
    fireEvent.click(screen.getByRole('button', { name: '按原请求重试' }));
    await screen.findByText(/不会覆盖新设置/);
    expect(request.mock.calls.filter(([, o]) => o.method === 'POST')[1][1].body).toEqual(original);
    expect(within(screen.getByRole('dialog')).getByLabelText('启用管理员账号')).not.toBeChecked();
  });
  it('bounds passwords by UTF-8 bytes, not just input character count', async () => {
    mount(); fireEvent.click(await screen.findByRole('button', { name: '重置密码 · ReaderTwo' }));
    fireEvent.change(screen.getByLabelText('新密码'), { target: { value: '密'.repeat(25) } }); confirm();
    await screen.findByText('新密码需要至少 12 个字符且不超过 72 个 UTF-8 字节');
    expect(request.mock.calls.some(([, o]) => o.method === 'POST')).toBe(false);
  });
});
