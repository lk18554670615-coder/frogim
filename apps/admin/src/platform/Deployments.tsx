import { useEffect, useRef, useState, type FormEvent } from 'react';
import { PlatformClient, type Page, type Tenant } from './api';
import type { ServerResource } from './Servers';

export type DeploymentRelease = { id: string; sequence: number; runtime: string; schemaVersion: number; composeSha256: string; rollbackTo: string[] | null; digest: string; tenantId?: string; serverId?: string };
type Operation = { id: string; serverId: string; tenantId: string; hostFingerprint: string; releaseId: string; releaseDigest: string; expectedGeneration: number; expectedReleaseId: string; action: string };
export type DeploymentJob = { id: string; requestId?: string; tenantId: string; serverId: string; state: 'pending' | 'unconfirmed' | 'completed'; phase: string; errorCode?: string; agentAttempts: number; generation: number; operation: Operation; release: DeploymentRelease };
type Conditions = { tenant: Tenant; server: ServerResource; generation: number; previous: string; previousSchema: number; previousSequence: number; rollbackTo: string[] };
type Request = { requestId: string; tenantId: string; serverId: string; releaseId: string; releaseDigest: string; action: string; expectedRevision: number; expectedConfigVersion: number; expectedAccessVersion: number; expectedGeneration: number; expectedReleaseId: string; reason: string; confirmed: true };
type Retry = { requestId: string; expectedAttempts: number; reason: string; confirmed: true };
const message = (e: unknown) => e instanceof Error ? e.message : '结果尚未确认，请查询原任务';
const states = { pending: '处理中', unconfirmed: '执行结果未确认', completed: '部署核验完成' };
const phases: Record<string, string> = { queued: '待分发', contacting_agent: '联系代理', pending: '代理待执行', applying: '代理执行中', retry_queued: '等待显式重试', unconfirmed: '需人工检查后重试', verifying_business: '核验企业业务', verified: '容器与业务已核验' };
const errors: Record<string, string> = { DEPLOYMENT_CONTROL_UNCONFIRMED: '代理、维护状态或任务回执尚未确认，企业继续锁定', DEPLOYMENT_BUSINESS_UNCONFIRMED: '容器已确认，业务依赖、数据库版本或停用状态未通过核验', DEPLOYMENT_EXECUTION_UNCONFIRMED: '代理执行未确认，不会自动重新部署；请运维检查后显式重试' };
const idPattern = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$/;

export function PlatformDeployments({ client, writable, refresh }: { client: PlatformClient; writable: boolean; refresh: number }) {
  const [data, setData] = useState<Page<DeploymentJob> | null>(null), [q, setQ] = useState(''), [page, setPage] = useState(1), [reload, setReload] = useState(0);
  const [error, setError] = useState(''), [notice, setNotice] = useState(''), [editor, setEditor] = useState<DeploymentJob | 'new' | null>(null);
  useEffect(() => {
    let alive = true; const abort = new AbortController(); setData(null); setError('');
    client.request<Page<DeploymentJob>>(`/deployments?${new URLSearchParams({ q, page: String(page), pageSize: '25' })}`, 'GET', undefined, abort.signal)
      .then(v => { if (alive) setData(v); }).catch(e => { if (alive) setError(message(e)); });
    return () => { alive = false; abort.abort(); };
  }, [client, q, page, refresh, reload]);
  return <section aria-label="部署任务">
    <p className="preview-banner">只允许部署运维预置且摘要固定的发布包。企业须首次开通或已确认停用；任务未完成时禁止恢复访问。核验完成后仍需单独激活或恢复，不代表物理隔离、备份恢复或生产验收通过。</p>
    <div className="filters"><label>企业 / 任务 / 原请求 ID<input value={q} onChange={e => { setQ(e.target.value); setPage(1); }} /></label>{writable && <button className="primary" onClick={() => setEditor('new')}>创建部署任务</button>}</div>
    {notice && <p role="status">{notice}</p>}{error && <p role="alert" className="error">{error}<button onClick={() => setReload(v => v + 1)}>重新加载任务</button></p>}
    {!data && !error && <p role="status">正在加载部署任务…</p>}
    {data && <><div className="table-scroll"><table><caption className="sr-only">部署任务</caption><thead><tr>{['任务 / 企业', '发布目标', '进度', '执行情况', '操作'].map(v => <th scope="col" key={v}>{v}</th>)}</tr></thead><tbody>
      {data.items.map(row => <tr key={row.id}><td><code>{row.id}</code><small>企业 {row.tenantId} · 服务器 {row.serverId}</small><small>原请求 {row.requestId}</small></td><td>{row.operation.action === 'rollback' ? '回滚至' : '部署'} {row.operation.releaseId}<small>数据库版本 {row.release.schemaVersion} · {row.release.runtime}</small></td><td><strong>{states[row.state]}</strong><small>{phases[row.phase] ?? '状态待确认'}</small></td><td>代理尝试 {row.agentAttempts} 次<small>已确认部署代次 {row.generation}</small>{row.errorCode && <p className="error">{errors[row.errorCode] ?? '尚未确认，请联系运维检查'}</p>}</td><td>{row.state === 'unconfirmed' && writable ? <button onClick={() => setEditor(row)}>检查后重试 · {row.id}</button> : row.state === 'completed' ? '未自动恢复企业' : '等待原任务确认'}</td></tr>)}
      {!data.items.length && <tr><td colSpan={5} className="empty">暂无部署任务。现有服务不会自动纳管或重新部署。</td></tr>}
    </tbody></table></div><footer><span>共 {data.total} 条 · 第 {page} 页</span><div><button disabled={page === 1} onClick={() => setPage(v => v - 1)}>上一页</button><button disabled={page * data.pageSize >= data.total} onClick={() => setPage(v => v + 1)}>下一页</button></div></footer></>}
    {writable && editor && <DeploymentEditor client={client} retry={editor === 'new' ? undefined : editor} onClose={() => setEditor(null)} onSaved={job => { setEditor(null); setReload(v => v + 1); setNotice(`任务 ${job.id} ${job.state === 'completed' ? '部署核验完成，仍未恢复企业访问' : '已受理，尚未宣称部署成功'}。请刷新查看进度。`); }} />}
  </section>;
}

function DeploymentEditor({ client, retry, onClose, onSaved }: { client: PlatformClient; retry?: DeploymentJob; onClose: () => void; onSaved: (j: DeploymentJob) => void }) {
  const dialog = useRef<HTMLDialogElement>(null), alive = useRef(true), working = useRef(false), frozen = useRef<Request | Retry | null>(null);
  const [busy, setBusy] = useState(false), [locked, setLocked] = useState(false), [error, setError] = useState(''), [tenantId, setTenantId] = useState('');
  const [conditions, setConditions] = useState<Conditions | null>(null), [releases, setReleases] = useState<DeploymentRelease[]>([]), [releaseId, setReleaseId] = useState(''), [action, setAction] = useState('deploy');
  useEffect(() => {
    alive.current = true; dialog.current?.showModal(); const abort = new AbortController();
    if (!retry) client.request<{ items: DeploymentRelease[] }>('/deployment-releases', 'GET', undefined, abort.signal).then(v => { if (alive.current) setReleases(v.items); }).catch(e => { if (alive.current) setError(message(e)); });
    return () => { alive.current = false; abort.abort(); frozen.current = null; };
  }, [client, retry]);
  async function lookup() {
    if (working.current || locked) return;
    if (!idPattern.test(tenantId)) { setError('请填写有效企业 ID'); return; }
    working.current = true; setBusy(true); setError(''); setConditions(null); setReleaseId('');
    try {
      const query = new URLSearchParams({ q: tenantId, pageSize: '100' });
      const [tenants, servers, jobs] = await Promise.all([client.request<Page<Tenant>>(`/tenants?${query}`), client.request<Page<ServerResource>>(`/servers?${query}`), client.request<Page<DeploymentJob>>(`/deployments?${new URLSearchParams({ q: tenantId, pageSize: '1' })}`)]);
      const tenant = tenants.items.find(t => t.id === tenantId), server = servers.items.find(s => s.tenantId === tenantId);
      if (!tenant || !server) throw new Error('未找到该企业的已登记服务器；不会自动选择默认企业');
      if (!['suspended', 'provisioning'].includes(tenant.status)) throw new Error('请先在企业目录停用企业，并等待停用任务确认完成');
      if (![tenant.configVersion, tenant.accessVersion, server.revision].every(v => Number.isSafeInteger(v) && v >= 1)) throw new Error('配置版本无法确认，请刷新');
      const previous = jobs.items[0];
      if (previous && (previous.tenantId !== tenantId || previous.serverId !== server.id || previous.state !== 'completed' || !Number.isSafeInteger(previous.generation) || previous.generation < 1)) throw new Error('存在未完成或无法确认的原任务，请先查询处理');
      if (alive.current) setConditions({ tenant, server, generation: previous?.generation ?? 0, previous: previous?.operation.releaseId ?? '', previousSchema: previous?.release.schemaVersion ?? 0, previousSequence: previous?.release.sequence ?? 0, rollbackTo: previous?.release.rollbackTo ?? [] });
    } catch (e) { if (alive.current) setError(message(e)); }
    finally { working.current = false; if (alive.current) setBusy(false); }
  }
  const choices = releases.filter(r => conditions && r.tenantId === conditions.tenant.id && r.serverId === conditions.server.id && r.runtime === conditions.server.runtime && (action === 'rollback' ? conditions.rollbackTo.includes(r.id) && r.schemaVersion === conditions.previousSchema && r.sequence < conditions.previousSequence : r.sequence >= conditions.previousSequence && r.schemaVersion >= conditions.previousSchema));
  function accept(job: DeploymentJob) {
    const sent = frozen.current;
    if (!sent || !idPattern.test(job.id ?? '') || !['pending', 'unconfirmed', 'completed'].includes(job.state) || !job.operation || job.operation.id !== job.id || job.operation.tenantId !== job.tenantId || job.operation.serverId !== job.serverId || !Number.isSafeInteger(job.agentAttempts) || job.agentAttempts < 0 || !Number.isSafeInteger(job.generation) || job.generation < 0 || (job.state === 'completed' && job.generation !== job.operation.expectedGeneration + 1)) throw new Error('返回结果无法确认，请按原请求重试或查询任务');
    if (retry ? job.id !== retry.id || job.tenantId !== retry.tenantId || job.serverId !== retry.serverId || job.operation.releaseDigest !== retry.operation.releaseDigest : !('tenantId' in sent) || job.tenantId !== sent.tenantId || job.serverId !== sent.serverId || job.operation.releaseId !== sent.releaseId || job.operation.releaseDigest !== sent.releaseDigest || job.operation.expectedGeneration !== sent.expectedGeneration || job.operation.expectedReleaseId !== sent.expectedReleaseId || job.operation.action !== sent.action) throw new Error('返回结果与原请求不符，请先查询原任务');
    if (alive.current) onSaved(job);
  }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (working.current) return;
    if (!frozen.current) {
      const values = new FormData(event.currentTarget), reason = String(values.get('reason') ?? '').trim();
      if (reason.length < 2 || values.get('confirmed') !== 'yes') { setError('请填写操作理由，并确认维护和重试影响'); return; }
      if (retry) frozen.current = { requestId: crypto.randomUUID(), expectedAttempts: retry.agentAttempts, reason, confirmed: true };
      else {
        const release = choices.find(r => r.id === releaseId);
        if (!conditions || conditions.tenant.id !== tenantId || !release) { setError('请读取企业部署条件并选择已配置发布包'); return; }
        frozen.current = { requestId: crypto.randomUUID(), tenantId, serverId: conditions.server.id, releaseId, releaseDigest: release.digest, action, expectedRevision: conditions.server.revision, expectedConfigVersion: conditions.tenant.configVersion, expectedAccessVersion: conditions.tenant.accessVersion, expectedGeneration: conditions.generation, expectedReleaseId: conditions.previous, reason, confirmed: true };
      }
      setLocked(true);
    }
    working.current = true; setBusy(true); setError('');
    try { accept(await client.request<DeploymentJob>(retry ? `/deployments/${retry.id}/retry` : '/deployments', 'POST', frozen.current)); }
    catch (e) { if (alive.current) setError(message(e)); }
    finally { working.current = false; if (alive.current) setBusy(false); }
  }
  return <dialog ref={dialog} aria-labelledby="deployment-editor-title" onCancel={e => { e.preventDefault(); if (!busy) onClose(); }}><form onSubmit={submit}>
    <h2 id="deployment-editor-title">{retry ? '显式重试原部署' : '创建部署任务'}</h2>
    <p>{retry ? `仅重试原任务 ${retry.id}，目标发布 ${retry.operation.releaseId} 不变。不会取消未知结果或创建替代部署。` : '部署会修改已绑定服务器上的受管服务。不接管已有未知容器、不购买服务器、不清空数据；只选择预置固定摘要发布包。'}</p>
    {!retry && <><label>企业 ID<input value={tenantId} disabled={busy || locked} maxLength={80} onChange={e => { setTenantId(e.target.value.trim()); setConditions(null); setReleaseId(''); }} /></label><button type="button" disabled={busy || locked} onClick={lookup}>读取部署条件</button>
      {conditions && <p>企业：{conditions.tenant.displayName} · 服务器 {conditions.server.id}<br />访问代次 {conditions.tenant.accessVersion} · 已确认部署代次 {conditions.generation}<br />当前发布 {conditions.previous || '未进行受管部署'}；提交时服务端再次校验。</p>}
      <label>部署操作<select value={action} disabled={busy || locked} onChange={e => { setAction(e.target.value); setReleaseId(''); }}><option value="deploy">部署 / 升级</option><option value="rollback">受控回滚（不降级数据库）</option></select></label>
      <label>目标发布包<select value={releaseId} disabled={busy || locked || !conditions} onChange={e => setReleaseId(e.target.value)}><option value="">请选择已配置发布</option>{choices.map(r => <option key={r.id} value={r.id}>{r.id} · schema {r.schemaVersion} · {r.runtime}</option>)}</select></label>
      {!releases.length && <p>尚未配置服务端发布目录，不能创建部署。当前本机代理默认仅检查，不会自动启用执行权限。</p>}
      {!!releases.length && conditions && !choices.length && <p>没有匹配当前企业、服务器和操作条件的发布包；不会使用其他企业的配置。</p>}
    </>}
    <label>操作理由<textarea name="reason" required minLength={2} maxLength={300} disabled={busy || locked} /></label>
    <label className="confirmation"><input type="checkbox" name="confirmed" value="yes" required disabled={busy || locked} />我已确认维护窗口与目标发布，未完成前不恢复访问</label>
    {error && <p role="alert" className="error">{error}</p>}
    {locked && <p>请求号：<code>{frozen.current?.requestId}</code>关闭窗口不会取消已受理任务。重试复用原请求；可在任务列表查询企业或原请求号。</p>}
    <div className="dialog-actions"><button type="button" disabled={busy} onClick={onClose}>{locked ? '关闭窗口' : '取消'}</button><button className="primary" disabled={busy || (!retry && !locked && (!conditions || !releaseId))}>{busy ? '提交中…' : locked ? '按原请求重试' : retry ? '确认重试原任务' : '确认部署任务'}</button></div>
  </form></dialog>;
}
