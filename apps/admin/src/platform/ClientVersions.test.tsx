import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { PlatformClientVersions, type Release } from './ClientVersions';
import { PlatformClient } from './api';

const request = vi.fn();
const policies: Release[] = (['android', 'ios', 'web', 'macos'] as const).map(platform => ({ platform, enabled: false, revision: 0, minimumVersion: '0.0.0', latestVersion: '0.0.0', forceUpdate: false, rolloutPercentage: 100, releaseNotes: '', downloadUrl: '', updatedBy: '', updatedAt: '' }));
beforeEach(() => {
  request.mockReset(); vi.stubGlobal('fetch', request);
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  request.mockImplementation(async (url: string) => url.includes('/history?') ? Response.json({ items: [], total: 0, page: 1, pageSize: 10 }) : Response.json({ items: policies, total: 4, page: 1, pageSize: 25 }));
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
async function edit() {
  fireEvent.click(await screen.findByRole('button', { name: '编辑 Android 策略' }));
  fireEvent.click(screen.getByLabelText('启用更新策略'));
  fireEvent.change(screen.getByLabelText('最低支持版本'), { target: { value: '1.0.0' } });
  fireEvent.change(screen.getByLabelText('最新发布版本'), { target: { value: '2.0.0' } });
  fireEvent.change(screen.getByLabelText('HTTPS 分发地址'), { target: { value: 'https://downloads.example/app.apk' } });
  fireEvent.click(screen.getByRole('button', { name: '审阅更改' }));
}
function confirm() {
  fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '隔离环境发布验证' } });
  fireEvent.click(screen.getByLabelText('我已确认影响所有企业，并核验分发链接与安装包'));
  fireEvent.click(screen.getByRole('button', { name: '确认发布策略' }));
}
describe('platform global client releases', () => {
  it('shows four platform policies and hides writes for readers', async () => {
    render(<PlatformClientVersions client={new PlatformClient()} writable={false} refresh={0} />);
    expect(await screen.findAllByText('策略已停用')).toHaveLength(4);
    expect(screen.queryByRole('button', { name: /编辑.*策略/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'iOS 发布历史' }));
    expect(await screen.findByText('尚无发布记录')).toBeInTheDocument();
  });
  it('requires reason and explicit all-enterprise confirmation, then commits once', async () => {
    render(<PlatformClientVersions client={new PlatformClient()} writable refresh={0} />); await edit();
    expect(screen.getByRole('dialog')).toHaveTextContent('影响所有企业');
    fireEvent.submit(screen.getByRole('button', { name: '确认发布策略' }).closest('form')!);
    await screen.findByText('请填写操作理由并确认全平台影响');
    expect(request.mock.calls.some(([, o]) => o.method === 'PUT')).toBe(false);
    request.mockImplementationOnce(async (_url, options) => Response.json({ ...policies[0], ...JSON.parse(options.body), revision: 1, updatedBy: 'operator', updatedAt: '2026-09-28T00:00:00Z' }));
    confirm();
    await screen.findByText(/策略版本 1 已提交/);
    const writes = request.mock.calls.filter(([, o]) => o.method === 'PUT');
    expect(writes).toHaveLength(1); expect(JSON.parse(writes[0][1].body)).toMatchObject({ expectedRevision: 0, confirmed: true, enabled: true });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
  it('freezes the request and reuses it after a lost response', async () => {
    render(<PlatformClientVersions client={new PlatformClient()} writable refresh={0} />); await edit();
    request.mockRejectedValueOnce(new TypeError('network response lost')); confirm();
    await screen.findByText('network response lost');
    expect(screen.getByLabelText('操作理由')).toBeDisabled();
    const before = request.mock.calls.find(([, o]) => o.method === 'PUT')![1].body;
    request.mockImplementationOnce(async (_url, options) => Response.json({ ...policies[0], ...JSON.parse(options.body), revision: 1 }));
    fireEvent.click(screen.getByRole('button', { name: '按原请求重试' }));
    await screen.findByText(/策略版本 1 已提交/);
    expect(request.mock.calls.filter(([, o]) => o.method === 'PUT')[1][1].body).toBe(before);
  });
  it('keeps an unconfirmed result for explicit retry, not success', async () => {
    render(<PlatformClientVersions client={new PlatformClient()} writable refresh={0} />); await edit();
    request.mockResolvedValueOnce(Response.json({ ok: true })); confirm();
    await screen.findByText('响应无法确认，请按原请求重试或核对发布历史');
    expect(screen.queryByText(/已提交。/)).not.toBeInTheDocument();
  });
  it('does not overwrite a conflicting policy or discard the draft on refresh', async () => {
    const client = new PlatformClient(); const view = render(<PlatformClientVersions client={client} writable refresh={0} />); await edit();
    request.mockResolvedValueOnce(Response.json({ error: { code: 'CLIENT_VERSION_POLICY_CHANGED' } }, { status: 409 })); confirm();
    await screen.findByText(/不会自动覆盖/);
    view.rerender(<PlatformClientVersions client={client} writable refresh={1} />);
    await waitFor(() => expect(request.mock.calls.filter(([u]) => u.endsWith('/client-versions'))).toHaveLength(2));
    expect(screen.getByRole('dialog')).toHaveTextContent('最新 2.0.0');
    expect(screen.getByLabelText('操作理由')).toHaveValue('隔离环境发布验证');
  });
  it('rejects temporary URLs and invalid versions before review', async () => {
    render(<PlatformClientVersions client={new PlatformClient()} writable refresh={0} />);
    fireEvent.click(await screen.findByRole('button', { name: '编辑 Android 策略' }));
    fireEvent.click(screen.getByLabelText('启用更新策略'));
    fireEvent.change(screen.getByLabelText('HTTPS 分发地址'), { target: { value: 'https://downloads.example/app?token=secret' } });
    fireEvent.click(screen.getByRole('button', { name: '审阅更改' }));
    await screen.findByText('请填写不含凭据、查询参数或片段的 HTTPS 分发地址');
    fireEvent.change(screen.getByLabelText('最新发布版本'), { target: { value: '2-beta' } });
    fireEvent.submit(screen.getByRole('button', { name: '审阅更改' }).closest('form')!);
    await screen.findByText('请填写最多四段的数字版本号');
  });
  it('does not retry a committed release if refreshing the list fails', async () => {
    render(<PlatformClientVersions client={new PlatformClient()} writable refresh={0} />); await edit();
    request.mockImplementationOnce(async (_url, options) => Response.json({ ...policies[0], ...JSON.parse(options.body), revision: 1 }));
    request.mockRejectedValueOnce(new Error('list refresh unavailable')); confirm();
    await screen.findByText('list refresh unavailable');
    expect(screen.getByText(/策略版本 1 已提交/)).toBeInTheDocument();
    expect(request.mock.calls.filter(([, o]) => o.method === 'PUT')).toHaveLength(1);
  });
  it('rejects malformed or foreign history instead of presenting misleading records', async () => {
    render(<PlatformClientVersions client={new PlatformClient()} writable={false} refresh={0} />);
    request.mockResolvedValueOnce(Response.json({ items: [{ ...policies[0], id: 'foreign', requestId: 'foreign', reason: 'test' }], total: 1, page: 1, pageSize: 10 }));
    fireEvent.click(await screen.findByRole('button', { name: 'iOS 发布历史' }));
    await screen.findByText('发布历史响应不完整，请重试查询');
    expect(screen.queryByText('请求号：foreign')).not.toBeInTheDocument();
  });
});
