import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { PlatformDeployments, type DeploymentJob } from './Deployments';
import { PlatformClient } from './api';

const fetcher = vi.fn();
const release = { id: 'release-one', digest: 'a'.repeat(64), sequence: 1, runtime: 'linux/amd64', schemaVersion: 79, composeSha256: 'b'.repeat(64), rollbackTo: [], tenantId: 'a', serverId: 'server-a' };
const tenant = { id: 'a', displayName: '企业甲', status: 'suspended', configVersion: 1, accessVersion: 2 };
const server = { id: 'server-a', tenantId: 'a', revision: 3, runtime: 'linux/amd64' };
let jobs: DeploymentJob[] = [];
const result = (body: Record<string, unknown>): DeploymentJob => ({ id: 'deploy-first', requestId: body.requestId as string, tenantId: 'a', serverId: 'server-a', state: 'pending', phase: 'queued', agentAttempts: 0, generation: 0, operation: { id: 'deploy-first', serverId: 'server-a', tenantId: 'a', hostFingerprint: 'd'.repeat(64), releaseId: 'release-one', releaseDigest: release.digest, expectedGeneration: 0, expectedReleaseId: '', action: 'deploy' }, release });
function route(url: string) {
  if (url.endsWith('/deployment-releases')) return Response.json({ items: [release] });
  if (url.includes('/tenants?')) return Response.json({ items: [tenant], total: 1 });
  if (url.includes('/servers?')) return Response.json({ items: [server], total: 1 });
  return Response.json({ items: jobs, total: jobs.length, page: 1, pageSize: 25 });
}
beforeEach(() => {
  jobs = []; fetcher.mockReset(); vi.stubGlobal('fetch', fetcher); fetcher.mockImplementation(async (url: string) => route(url));
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
function mount(writable = true) { return render(<PlatformDeployments client={new PlatformClient()} writable={writable} refresh={0} />); }
async function draft() {
  fireEvent.click(await screen.findByRole('button', { name: '创建部署任务' }));
  fireEvent.change(screen.getByLabelText('企业 ID'), { target: { value: 'a' } });
  fireEvent.click(screen.getByRole('button', { name: '读取部署条件' }));
  await screen.findByRole('option', { name: /release-one/ });
  fireEvent.change(screen.getByLabelText('目标发布包'), { target: { value: 'release-one' } });
}
function confirm(button = '确认部署任务') {
  fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '预定维护窗口' } });
  fireEvent.click(screen.getByLabelText('我已确认维护窗口与目标发布，未完成前不恢复访问'));
  fireEvent.click(screen.getByRole('button', { name: button }));
}
describe('platform deployment tasks', () => {
  it('excludes unbound and other enterprise/server bundles', async () => {
    fetcher.mockImplementation(async (url: string) => url.endsWith('/deployment-releases') ? Response.json({ items: [
      { ...release, id: 'wrong-tenant', tenantId: 'b' },
      { ...release, id: 'wrong-server', serverId: 'server-b' },
      { ...release, id: 'unbound', tenantId: undefined, serverId: undefined },
    ] }) : route(url));
    mount(); fireEvent.click(await screen.findByRole('button', { name: '创建部署任务' }));
    fireEvent.change(screen.getByLabelText('企业 ID'), { target: { value: 'a' } });
    fireEvent.click(screen.getByRole('button', { name: '读取部署条件' }));
    await screen.findByText(/没有匹配当前企业/);
    expect(screen.queryByRole('option', { name: /wrong-tenant|wrong-server|unbound/ })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '确认部署任务' })).toBeDisabled();
    expect(fetcher.mock.calls.some(([, o]) => o.method === 'POST')).toBe(false);
  });
  it('keeps readers read-only and distinguishes readiness from activation', async () => {
    jobs = [{ ...result({}), state: 'completed', phase: 'verified', generation: 1, agentAttempts: 1 }];
    mount(false); await screen.findByRole('table', { name: '部署任务' });
    expect(screen.queryByRole('button', { name: '创建部署任务' })).not.toBeInTheDocument();
    expect(screen.getByText('部署核验完成')).toBeInTheDocument();
    expect(screen.getByText('未自动恢复企业')).toBeInTheDocument();
  });
  it('submits only the exact bound target and catalog digest after reason and confirmation', async () => {
    mount(); await draft();
    fireEvent.submit(screen.getByRole('button', { name: '确认部署任务' }).closest('form')!);
    await screen.findByText('请填写操作理由，并确认维护和重试影响');
    fetcher.mockImplementationOnce(async (_url, options) => Response.json(result(JSON.parse(options.body))));
    confirm(); await screen.findByText(/已受理，尚未宣称部署成功/);
    const body = JSON.parse(fetcher.mock.calls.find(([, o]) => o.method === 'POST')![1].body);
    expect(body).toMatchObject({ tenantId: 'a', serverId: 'server-a', releaseDigest: release.digest, expectedRevision: 3, expectedAccessVersion: 2, expectedGeneration: 0, expectedReleaseId: '', confirmed: true });
    expect(body).not.toHaveProperty('command'); expect(body).not.toHaveProperty('controlUrl'); expect(body).not.toHaveProperty('expectedReleaseID');
  });
  it('freezes the original request on ambiguous response and prevents duplicate clicks', async () => {
    mount(); await draft(); let finish!: (r: Response) => void;
    fetcher.mockImplementationOnce(() => new Promise<Response>(resolve => { finish = resolve; }));
    confirm(); fireEvent.submit(screen.getByRole('button', { name: '提交中…' }).closest('form')!);
    expect(fetcher.mock.calls.filter(([, o]) => o.method === 'POST')).toHaveLength(1);
    finish(Response.json({ ok: true })); await screen.findByText('返回结果无法确认，请按原请求重试或查询任务');
    expect(screen.getByLabelText('企业 ID')).toBeDisabled(); expect(screen.getByLabelText('操作理由')).toBeDisabled();
    const original = fetcher.mock.calls.find(([, o]) => o.method === 'POST')![1].body;
    fetcher.mockImplementationOnce(async (_url, o) => Response.json(result(JSON.parse(o.body))));
    fireEvent.click(screen.getByRole('button', { name: '按原请求重试' })); await screen.findByText(/已受理/);
    expect(fetcher.mock.calls.filter(([, o]) => o.method === 'POST')[1][1].body).toEqual(original);
  });
  it('rejects active tenants, unknown exact IDs, and unfinished earlier tasks', async () => {
    mount(); fireEvent.click(await screen.findByRole('button', { name: '创建部署任务' }));
    fireEvent.change(screen.getByLabelText('企业 ID'), { target: { value: 'a' } });
    fetcher.mockImplementation(async (url: string) => url.includes('/tenants?') ? Response.json({ items: [{ ...tenant, status: 'active' }] }) : route(url));
    fireEvent.click(screen.getByRole('button', { name: '读取部署条件' })); await screen.findByText(/请先在企业目录停用企业/);
    expect(screen.getByRole('button', { name: '确认部署任务' })).toBeDisabled();
    fetcher.mockImplementation(async (url: string) => route(url)); jobs = [result({})];
    fireEvent.click(screen.getByRole('button', { name: '读取部署条件' })); await screen.findByText(/存在未完成或无法确认的原任务/);
    fireEvent.change(screen.getByLabelText('企业 ID'), { target: { value: 'other' } });
    fireEvent.click(screen.getByRole('button', { name: '读取部署条件' })); await screen.findByText(/未找到该企业的已登记服务器/);
    expect(fetcher.mock.calls.some(([, o]) => o.method === 'POST')).toBe(false);
  });
  it('retries only the original unconfirmed job with the observed attempt count', async () => {
    jobs = [{ ...result({}), state: 'unconfirmed', phase: 'unconfirmed', agentAttempts: 2, errorCode: 'DEPLOYMENT_EXECUTION_UNCONFIRMED' }];
    mount(); fireEvent.click(await screen.findByRole('button', { name: /检查后重试/ }));
    expect(screen.queryByLabelText('目标发布包')).not.toBeInTheDocument();
    fetcher.mockResolvedValueOnce(Response.json({ ...jobs[0], state: 'pending', phase: 'retry_queued' }));
    confirm('确认重试原任务'); await screen.findByText(/已受理/);
    const [url, options] = fetcher.mock.calls.find(([, o]) => o.method === 'POST')!;
    expect(url).toMatch(/deployments\/deploy-first\/retry$/); expect(JSON.parse(options.body)).toMatchObject({ expectedAttempts: 2, confirmed: true });
    expect(JSON.parse(options.body)).not.toHaveProperty('releaseId');
  });
  it('keeps execution disabled for an empty catalog and cancels without mutation', async () => {
    fetcher.mockImplementation(async (url: string) => url.endsWith('/deployment-releases') ? Response.json({ items: [] }) : route(url));
    mount(); fireEvent.click(await screen.findByRole('button', { name: '创建部署任务' }));
    await screen.findByText(/尚未配置服务端发布目录/);
    expect(screen.getByRole('button', { name: '确认部署任务' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '取消' })); expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(fetcher.mock.calls.some(([, o]) => o.method === 'POST')).toBe(false);
  });
  it('drops delayed responses after leaving the page without persisting credentials or drafts', async () => {
    const view = mount(); await draft(); let finish!: (r: Response) => void;
    fetcher.mockImplementationOnce(() => new Promise<Response>(resolve => { finish = resolve; }));
    confirm(); await waitFor(() => expect(screen.getByRole('button', { name: '提交中…' })).toBeDisabled());
    view.unmount(); finish(Response.json(result({}))); await Promise.resolve();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument(); expect(localStorage.length).toBe(0);
  });
});
