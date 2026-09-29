import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { PlatformBackupSchedules, type BackupSchedule } from './BackupSchedules';
import { PlatformClient } from './api';

const fetcher = vi.fn();
const plan: BackupSchedule = { tenantId: 'a', enabled: true, startMinuteUtc: 120, windowMinutes: 60, version: 4, actorId: 'operator', updatedAt: '2026-09-29T00:00:00Z', operatorEnabled: true };
let rows: BackupSchedule[];
function route(url: string) {
  if (url.includes('/tenants?')) return Response.json({ items: [{ id: 'a', displayName: '测试企业' }], total: 1 });
  if (url.includes('/maintenance?')) return Response.json({ items: [{ id: 'maintenance-123', tenantId: 'a', state: 'pending', phase: 'backup', slotDate: '2026-09-29', windowEnd: '2026-09-29T03:00:00Z', errorCode: 'MAINTENANCE_BACKUP_NEEDS_ATTENTION', backupId: 'backup-original', pauseId: 'realm-pause', resumeId: '' }], total: 1, page: 1, pageSize: 25 });
  return Response.json({ items: rows, total: rows.length, page: 1, pageSize: 25 });
}
beforeEach(() => { rows = []; fetcher.mockReset(); fetcher.mockImplementation(async (url: string) => route(url)); vi.stubGlobal('fetch', fetcher); HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); }; });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
function mount(writable = true) { return render(<PlatformBackupSchedules client={new PlatformClient()} writable={writable} refresh={0} />); }
async function draft() {
  fireEvent.click(await screen.findByRole('button', { name: '配置维护窗口' }));
  fireEvent.change(screen.getByLabelText('配置企业 ID'), { target: { value: 'a' } });
  fireEvent.click(screen.getByRole('button', { name: '读取当前计划' }));
  await screen.findByText(/测试企业 · 当前版本/);
}
function confirm() {
  fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '每日维护窗口经负责人确认' } });
  fireEvent.click(screen.getByLabelText('我已确认每日停机窗口及恢复影响'));
  fireEvent.click(screen.getByRole('button', { name: '保存维护计划' }));
}

describe('daily maintenance schedule', () => {
  it('is disabled by default, explains session impact, and exposes no write controls to readers', async () => {
    mount(false); await screen.findByText(/暂无记录/);
    expect(screen.queryByRole('button', { name: '配置维护窗口' })).not.toBeInTheDocument();
    expect(screen.getByText(/用户需重新登录/)).toBeInTheDocument();
  });
  it('requires explicit repeat-maintenance confirmation and sends no private paths or credentials', async () => {
    mount(); await draft(); expect(screen.getByLabelText('启用每日维护备份')).not.toBeChecked();
    fireEvent.submit(screen.getByRole('button', { name: '保存维护计划' }).closest('form')!);
    await screen.findByText('请填写操作理由，并确认每日维护影响');
    fireEvent.click(screen.getByLabelText('启用每日维护备份'));
    fetcher.mockResolvedValueOnce(Response.json({ ...plan, version: 1 })); confirm(); await screen.findByText(/已保存 a 的维护计划/);
    const body = JSON.parse(fetcher.mock.calls.find(([, o]) => o.method === 'PUT')![1].body);
    expect(body).toMatchObject({ enabled: true, startMinuteUtc: 120, windowMinutes: 60, expectedVersion: 0, confirmed: true });
    expect(Object.keys(body).sort()).toEqual(['confirmed', 'enabled', 'expectedVersion', 'reason', 'requestId', 'startMinuteUtc', 'windowMinutes'].sort());
  });
  it('locks ambiguous requests and prevents duplicate submit; retries exact request', async () => {
    rows = [plan]; mount(); await draft(); let finish!: (r: Response) => void;
    fetcher.mockImplementationOnce(() => new Promise<Response>(resolve => { finish = resolve; })); confirm();
    fireEvent.submit(screen.getByRole('button', { name: '提交中…' }).closest('form')!);
    expect(fetcher.mock.calls.filter(([, o]) => o.method === 'PUT')).toHaveLength(1);
    finish(Response.json({ ...plan, tenantId: 'another' })); await screen.findByText('返回结果无法确认，请按原请求重试或查询计划');
    expect(screen.getByLabelText('配置企业 ID')).toBeDisabled(); expect(screen.getByLabelText('每日开始时间（UTC）')).toBeDisabled();
    const original = fetcher.mock.calls.find(([, o]) => o.method === 'PUT')![1].body;
    fetcher.mockResolvedValueOnce(Response.json(plan)); fireEvent.click(screen.getByRole('button', { name: '按原请求重试' }));
    await screen.findByText(/已保存 a 的维护计划/);
    expect(fetcher.mock.calls.filter(([, o]) => o.method === 'PUT')[1][1].body).toBe(original);
  });
  it('never silently overwrites version conflicts and cancellation does not write', async () => {
    rows = [plan]; mount(); await draft(); fireEvent.click(screen.getByLabelText('启用每日维护备份'));
    fetcher.mockResolvedValueOnce(Response.json({ error: { code: 'MAINTENANCE_STATE_CHANGED' } }, { status: 409 })); confirm();
    await screen.findByText(/维护计划或企业状态已变化/);
    expect(JSON.parse(fetcher.mock.calls.find(([, o]) => o.method === 'PUT')![1].body)).toMatchObject({ enabled: false, expectedVersion: 4 });
    fireEvent.click(screen.getByRole('button', { name: '关闭窗口' })); await draft(); fireEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(fetcher.mock.calls.filter(([, o]) => o.method === 'PUT')).toHaveLength(1);
  });
  it('rejects malformed windows and drops loaded version on tenant changes', async () => {
    mount(); await draft(); fireEvent.change(screen.getByLabelText('维护窗口（分钟）'), { target: { value: '181' } });
    fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: 'verified window' } }); fireEvent.click(screen.getByLabelText('我已确认每日停机窗口及恢复影响'));
    fireEvent.submit(screen.getByRole('button', { name: '保存维护计划' }).closest('form')!); await screen.findByText(/窗口为 15–180 分钟/);
    fireEvent.change(screen.getByLabelText('配置企业 ID'), { target: { value: 'b' } }); expect(screen.getByRole('button', { name: '保存维护计划' })).toBeDisabled();
    expect(fetcher.mock.calls.some(([, o]) => o.method === 'PUT')).toBe(false);
  });
  it('explains disabled author and unconfirmed runs without offering unsafe resume or retry', async () => {
    rows = [{ ...plan, operatorEnabled: false }]; mount(); await screen.findByText(/原操作者已不可用/);
    expect(screen.getByText(/北京时间开始：10:00/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('查看内容'), { target: { value: 'maintenance' } });
    await screen.findByText(/原备份恢复未确认/); expect(screen.getByText('backup-original')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '恢复企业' })).not.toBeInTheDocument(); expect(screen.queryByRole('button', { name: '重试' })).not.toBeInTheDocument();
  });
});
