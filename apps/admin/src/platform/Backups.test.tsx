import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { PlatformBackups, type BackupJob } from './Backups';
import { PlatformClient } from './api';

const fetcher = vi.fn();
const binding = { tenantId: 'a', serverId: 'server-a', releaseId: 'release-one', releaseDigest: 'b'.repeat(64), generation: 3, accessVersion: 2, schemaVersion: 79 };
const job = (): BackupJob => ({ id: `backup-${'a'.repeat(32)}`, tenantId: 'a', serverId: 'server-a', state: 'pending', phase: 'queued', operation: { id: `backup-${'a'.repeat(32)}`, hostFingerprint: 'd'.repeat(64), binding }, receipt: {} });
let rows: BackupJob[] = [];
const route = (url: string) => {
  if (url.includes('/tenants?')) return Response.json({ items: [{ id: 'a', displayName: '测试企业', status: 'suspended', configVersion: 1, accessVersion: 2 }] });
  if (url.includes('/servers?')) return Response.json({ items: [{ id: 'server-a', tenantId: 'a', revision: 4 }] });
  if (url.includes('/deployments?')) return Response.json({ items: [{ tenantId: 'a', serverId: 'server-a', state: 'completed', generation: 3, release: { id: 'release-one' }, operation: { releaseId: 'release-one', releaseDigest: binding.releaseDigest } }] });
  return Response.json({ items: rows, total: rows.length, pageSize: 25, page: 1 });
};
beforeEach(() => {
  rows = []; fetcher.mockReset(); vi.stubGlobal('fetch', fetcher); fetcher.mockImplementation(async (url: string) => route(url));
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
function mount(writable = true) { return render(<PlatformBackups client={new PlatformClient()} writable={writable} refresh={0} />); }
async function draft() {
  fireEvent.click(await screen.findByRole('button', { name: '创建备份任务' }));
  fireEvent.change(screen.getByLabelText('企业 ID'), { target: { value: 'a' } });
  fireEvent.click(screen.getByRole('button', { name: '读取备份条件' }));
  await screen.findByText('测试企业 · release-one · 部署代次 3');
}
function confirm() {
  fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '预定维护窗口' } });
  fireEvent.click(screen.getByLabelText('我已确认维护窗口，任务未核验前不恢复访问'));
  fireEvent.click(screen.getByRole('button', { name: '确认备份操作' }));
}
describe('platform backups', () => {
  it('keeps readers read-only and separates archive completion from tenant access', async () => {
    rows = [{ ...job(), state: 'completed', phase: 'verified', receipt: { archive: { sha256: 'c'.repeat(64), bytes: 2048, files: 8 } } }];
    mount(false); await screen.findByText('本地备份已核验');
    expect(screen.getByText('仅本地归档，未配置异地交付')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '创建备份任务' })).not.toBeInTheDocument();
    expect(screen.getByText('访问恢复由独立维护流程控制')).toBeInTheDocument();
  });
  it.each(['pending', 'running', 'unconfirmed', 'completed'])('distinguishes offsite %s from local completion', async state => {
    const original = job();
    rows = [{ ...original, state: 'completed', phase: 'verified', operation: { ...original.operation, offsiteTargetId: 'remote-a' }, receipt: { offsite: { state, attempts: 2 } } }];
    const view = mount(false);
    await screen.findByText('本地备份已核验');
    const expected: Record<string, string> = { pending: '等待异地交付', running: '异地交付中', unconfirmed: '异地交付未确认，将自动重试', completed: '异地交付已核验' };
    expect(screen.getByText(expected[state])).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /检查后重试/ })).not.toBeInTheDocument();
    view.unmount(); rows = [{ ...rows[0], errorCode: 'BACKUP_OFFSITE_CONTROL_UNCONFIRMED' }]; mount(false);
    await screen.findByText('异地状态暂不可确认，平台将重试');
    expect(screen.queryByText(/企业继续锁定/)).not.toBeInTheDocument();
  });
  it('requires reason and confirmation and sends version-fenced inputs without paths or secrets', async () => {
    mount(); await draft();
    fireEvent.submit(screen.getByRole('button', { name: '确认备份操作' }).closest('form')!);
    await screen.findByText('请填写操作理由，并确认维护影响');
    fetcher.mockResolvedValueOnce(Response.json(job())); confirm(); await screen.findByText(/已受理，请刷新查看原任务/);
    const body = JSON.parse(fetcher.mock.calls.find(([, o]) => o.method === 'POST')![1].body);
    expect(body).toMatchObject({ tenantId: 'a', serverId: 'server-a', releaseDigest: binding.releaseDigest, expectedGeneration: 3, expectedAccessVersion: 2, expectedRevision: 4, confirmed: true });
    expect(body).not.toHaveProperty('directory'); expect(body).not.toHaveProperty('keyFile'); expect(body).not.toHaveProperty('command');
  });
  it('preserves immutable request after lost acknowledgement and prevents double click', async () => {
    mount(); await draft(); let finish!: (r: Response) => void;
    fetcher.mockImplementationOnce(() => new Promise<Response>(resolve => { finish = resolve; }));
    confirm(); fireEvent.submit(screen.getByRole('button', { name: '提交中…' }).closest('form')!);
    expect(fetcher.mock.calls.filter(([, o]) => o.method === 'POST')).toHaveLength(1);
    finish(Response.json({ ok: true })); await screen.findByText('返回结果无法确认，请按原请求重试或查询任务');
    expect(screen.getByLabelText('企业 ID')).toBeDisabled(); expect(screen.getByLabelText('操作理由')).toBeDisabled();
    const original = fetcher.mock.calls.find(([, o]) => o.method === 'POST')![1].body;
    fetcher.mockResolvedValueOnce(Response.json(job())); fireEvent.click(screen.getByRole('button', { name: '按原请求重试' }));
    await screen.findByText(/已受理，请刷新查看原任务/);
    expect(fetcher.mock.calls.filter(([, o]) => o.method === 'POST')[1][1].body).toEqual(original);
  });
  it('rejects active tenants or an unrelated deployment and does not write on cancel', async () => {
    mount(); fireEvent.click(await screen.findByRole('button', { name: '创建备份任务' }));
    fireEvent.change(screen.getByLabelText('企业 ID'), { target: { value: 'a' } });
    fetcher.mockImplementation(async (url: string) => url.includes('/tenants?') ? Response.json({ items: [{ id: 'a', status: 'active' }] }) : route(url));
    fireEvent.click(screen.getByRole('button', { name: '读取备份条件' })); await screen.findByText(/请先登记服务器/);
    expect(screen.getByRole('button', { name: '确认备份操作' })).toBeDisabled();
    fetcher.mockImplementation(async (url: string) => url.includes('/deployments?') ? Response.json({ items: [{ tenantId: 'b', serverId: 'server-b', state: 'completed' }] }) : route(url));
    fireEvent.click(screen.getByRole('button', { name: '读取备份条件' })); await screen.findByText(/不能猜测备份目标/);
    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(fetcher.mock.calls.some(([, o]) => o.method === 'POST')).toBe(false);
  });
  it('retries only the exact unconfirmed receipt', async () => {
    rows = [{ ...job(), state: 'unconfirmed', receipt: { state: 'unconfirmed', phase: 'restoring_services', revision: 7 } }];
    mount(); fireEvent.click(await screen.findByRole('button', { name: /检查后重试/ }));
    expect(screen.queryByLabelText('企业 ID')).not.toBeInTheDocument();
    fetcher.mockResolvedValueOnce(Response.json({ ...rows[0], state: 'pending' })); confirm(); await screen.findByText(/已受理，请刷新查看原任务/);
    const [url, options] = fetcher.mock.calls.find(([, o]) => o.method === 'POST')!;
    expect(url).toMatch(/\/backups\/backup-[a-f0-9]+\/control$/);
    expect(JSON.parse(options.body)).toMatchObject({ action: 'retry', expectedRevision: 7, confirmed: true });
  });
  it('allows cancellation only before capture starts, never during recovery', async () => {
    rows = [{ ...job(), receipt: { state: 'pending', phase: 'queued', revision: 1 } }];
    const view = mount(); fireEvent.click(await screen.findByRole('button', { name: /取消待执行/ }));
    fetcher.mockResolvedValueOnce(Response.json(rows[0])); confirm(); await screen.findByText(/已受理，请刷新查看原任务/);
    expect(JSON.parse(fetcher.mock.calls.find(([, o]) => o.method === 'POST')![1].body)).toMatchObject({ action: 'cancel', expectedRevision: 1 });
    view.unmount(); rows = [{ ...job(), receipt: { state: 'pending', phase: 'restoring_services', revision: 9 } }];
    mount(); await screen.findByRole('table', { name: '备份任务' }); expect(screen.queryByRole('button', { name: /取消待执行/ })).not.toBeInTheDocument();
  });
  it('does not accept a control acknowledgement that changes the offsite target', async () => {
    const original = job();
    rows = [{ ...original, operation: { ...original.operation, offsiteTargetId: 'remote-a' }, state: 'unconfirmed', receipt: { state: 'unconfirmed', revision: 7 } }];
    mount(); fireEvent.click(await screen.findByRole('button', { name: /检查后重试/ }));
    fetcher.mockResolvedValueOnce(Response.json({ ...rows[0], state: 'pending', operation: { ...rows[0].operation, offsiteTargetId: 'remote-b' } }));
    confirm(); await screen.findByText('返回结果无法确认，请按原请求重试或查询任务');
    expect(screen.queryByText(/已受理，请刷新查看原任务/)).not.toBeInTheDocument();
  });
});
