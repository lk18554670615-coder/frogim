import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { PlatformServers } from './Servers';
import { PlatformClient } from './api';

const fetcher = vi.fn();
const row = { id: 'default-local', tenantId: 'default', displayName: '本机默认企业', hostFingerprint: 'a'.repeat(64), isolationMode: 'local_preview', runtime: 'linux/amd64', revision: 1, configVersion: 1, verifiedAt: '2026-09-28T01:00:00Z' };
beforeEach(() => {
  fetcher.mockReset(); vi.stubGlobal('fetch', fetcher);
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  fetcher.mockImplementation(async (url: string) => url.endsWith('/servers/configured') ? Response.json({ serverIds: ['default-local'] }) : url.includes('/tenants?') ? Response.json({ items: [{ id: 'default', displayName: '默认企业', configVersion: 1 }], total: 1 }) : Response.json({ items: [row], total: 1, page: 1, pageSize: 25 }));
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
function mount(writable = true) { return render(<PlatformServers client={new PlatformClient()} writable={writable} refresh={0} />); }
function confirm() {
  fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '本机代理校验' } });
  fireEvent.click(screen.getByLabelText('我已确认服务器与企业绑定，仅进行身份检查'));
  fireEvent.click(screen.getByRole('button', { name: '确认服务器操作' }));
}
describe('platform server registry', () => {
  it('labels inspection honestly and keeps reader controls read-only', async () => {
    mount(false); await screen.findByRole('table', { name: '服务器资源' });
    expect(screen.getByText('本机预览（非物理隔离）')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '登记服务器' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /重新校验/ })).not.toBeInTheDocument();
    expect(screen.getByText(/不执行部署、重启或删除/)).toBeInTheDocument();
  });
  it('registers only a configured ID and confirmed exact tenant, no control URL or command', async () => {
    mount(); fireEvent.click(await screen.findByRole('button', { name: '登记服务器' }));
    await screen.findByRole('option', { name: 'default-local' });
    fireEvent.change(screen.getByLabelText('预配置代理'), { target: { value: 'default-local' } });
    fireEvent.change(screen.getByLabelText('服务器名称'), { target: { value: row.displayName } });
    fireEvent.change(screen.getByLabelText('绑定企业 ID'), { target: { value: 'default' } });
    fireEvent.click(screen.getByRole('button', { name: '读取企业配置' })); await screen.findByText(/已确认企业：默认企业/);
    expect(screen.queryByLabelText(/控制地址/)).not.toBeInTheDocument();
    fetcher.mockResolvedValueOnce(Response.json(row)); confirm();
    await screen.findByText(/已完成代理校验与登记，未执行任何部署/);
    const post = fetcher.mock.calls.find(([, o]) => o.method === 'POST')!;
    expect(JSON.parse(post[1].body)).toMatchObject({ action: 'register', serverId: row.id, tenantId: 'default', expectedRevision: 0, expectedConfigVersion: 1, confirmed: true });
    expect(post[1].body).not.toMatch(/controlUrl|password|shell|command/);
  });
  it('keeps immutable request for retry and recovers a lost result without a second write', async () => {
    mount(); fireEvent.click(await screen.findByRole('button', { name: /重新校验/ }));
    fetcher.mockRejectedValueOnce(new TypeError('结果暂未确认')); confirm(); await screen.findByText('结果暂未确认');
    expect(screen.getByLabelText('操作理由')).toBeDisabled();
    const post = JSON.parse(fetcher.mock.calls.find(([, o]) => o.method === 'POST')![1].body);
    fetcher.mockResolvedValueOnce(Response.json({ ...row, revision: 2 }));
    fireEvent.click(screen.getByRole('button', { name: '查询提交结果' }));
    await screen.findByText(/未执行任何部署。/);
    expect(fetcher.mock.calls.filter(([, o]) => o.method === 'POST')).toHaveLength(1);
    expect(fetcher.mock.calls.some(([url, o]) => url.endsWith(`/operations/${post.requestId}`) && o.method === 'GET')).toBe(true);
  });
  it('never converts malformed or stale responses to success, and retries the same body', async () => {
    mount(); fireEvent.click(await screen.findByRole('button', { name: /重新校验/ }));
    fetcher.mockResolvedValueOnce(Response.json({ ok: true })); confirm(); await screen.findByText('响应无法确认，请查询原请求结果');
    const first = fetcher.mock.calls.find(([, o]) => o.method === 'POST')![1].body;
    fetcher.mockResolvedValueOnce(Response.json({ error: { code: 'SERVER_BINDING_CHANGED' } }, { status: 409 }));
    fireEvent.click(screen.getByRole('button', { name: '按原请求重试' })); await screen.findByText(/不会覆盖现有绑定/);
    expect(fetcher.mock.calls.filter(([, o]) => o.method === 'POST')[1][1].body).toEqual(first);
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });
  it('requires a reason and confirmation and cancels without a write', async () => {
    mount(); fireEvent.click(await screen.findByRole('button', { name: /重新校验/ }));
    fireEvent.submit(screen.getByRole('button', { name: '确认服务器操作' }).closest('form')!);
    await screen.findByText('请填写操作理由并确认绑定影响');
    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(fetcher.mock.calls.some(([, o]) => o.method === 'POST')).toBe(false);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
  it('ignores delayed writes when navigating away and does not persist request contents', async () => {
    const view = mount(); fireEvent.click(await screen.findByRole('button', { name: /重新校验/ }));
    let finish!: (r: Response) => void; fetcher.mockImplementationOnce(() => new Promise<Response>(resolve => { finish = resolve; }));
    confirm(); await waitFor(() => expect(screen.getByRole('button', { name: '校验中…' })).toBeDisabled());
    view.unmount(); finish(Response.json({ ...row, revision: 2 }));
    await Promise.resolve(); expect(screen.queryByRole('dialog')).not.toBeInTheDocument(); expect(localStorage.length).toBe(0);
  });
});
