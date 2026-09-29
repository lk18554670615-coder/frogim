import { useEffect, useRef, useState, type FormEvent } from 'react';
import { PlatformClient, type Page, type Tenant } from './api';
import type { ServerResource } from './Servers';
import type { DeploymentJob } from './Deployments';

type Binding = { tenantId: string; serverId: string; releaseId: string; releaseDigest: string; generation: number; accessVersion: number; schemaVersion: number };
export type BackupJob = {
  id: string; requestId?: string; tenantId: string; serverId: string; state: string; phase: string; errorCode?: string;
  operation: { id: string; hostFingerprint: string; binding: Binding; offsiteTargetId?: string };
  receipt: { state?: string; phase?: string; revision?: number; attempt?: number; archive?: { sha256: string; bytes: number; files: number }; offsite?: { state: string; attempts: number; retryAt?: string; errorCode?: string; delivery?: { targetId: string; manifest: { sha256: string } } } };
  controlPending?: boolean; leased?: boolean;
};
type Conditions = { tenant: Tenant; server: ServerResource; deployment: DeploymentJob };
type BackupInput = { requestId: string; tenantId: string; serverId: string; releaseId: string; releaseDigest: string; expectedRevision: number; expectedConfigVersion: number; expectedAccessVersion: number; expectedGeneration: number; reason: string; confirmed: true };
type ControlInput = { requestId: string; action: 'retry' | 'cancel'; expectedRevision: number; reason: string; confirmed: true };
type Edit = 'new' | { job: BackupJob; action: 'retry' | 'cancel' };
const message = (e: unknown) => e instanceof Error ? e.message : '结果尚未确认，请查询原任务';
const states: Record<string, string> = { pending: '处理中', unconfirmed: '需检查后重试', completed: '本地备份已核验', failed: '备份失败，服务已恢复', cancelled: '已取消，服务已核验' };
const offsiteStates: Record<string, string> = { pending: '等待异地交付', running: '异地交付中', unconfirmed: '异地交付未确认，将自动重试', completed: '异地交付已核验' };
const phases: Record<string, string> = { queued: '等待分发', contacting_agent: '联系代理', quiescing: '暂停服务写入', snapshotting: '生成加密归档', restoring_services: '恢复原服务', verifying: '检查原服务', verifying_business: '平台核对业务状态', verified: '核验完成' };
const idPattern = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$/;
const sameBinding = (a: Binding, b: Binding) => a.tenantId === b.tenantId && a.serverId === b.serverId && a.releaseId === b.releaseId && a.releaseDigest === b.releaseDigest && a.generation === b.generation && a.accessVersion === b.accessVersion && a.schemaVersion === b.schemaVersion;

export function PlatformBackups({ client, writable, refresh }: { client: PlatformClient; writable: boolean; refresh: number }) {
  const [data, setData] = useState<Page<BackupJob> | null>(null), [q, setQ] = useState(''), [page, setPage] = useState(1), [reload, setReload] = useState(0);
  const [error, setError] = useState(''), [notice, setNotice] = useState(''), [editor, setEditor] = useState<Edit | null>(null);
  useEffect(() => {
    let alive = true; const abort = new AbortController(); setData(null); setError('');
    client.request<Page<BackupJob>>(`/backups?${new URLSearchParams({ q, page: String(page), pageSize: '25' })}`, 'GET', undefined, abort.signal)
      .then(v => { if (alive) setData(v); }).catch(e => { if (alive) setError(message(e)); });
    return () => { alive = false; abort.abort(); };
  }, [client, q, page, reload, refresh]);
  return <section aria-label="备份任务">
    <p className="preview-banner">冷备份会短暂停止企业服务。手动任务需先停用企业并等待确认，核验完成后再单独恢复访问；定时任务由维护流程恢复访问。异地交付独立重试，不延长停机。只有“异地交付已核验”代表收据已确认，不代表灾难恢复或生产验收通过。</p>
    <div className="filters"><label>企业 / 任务 / 原请求 ID<input value={q} onChange={e => { setQ(e.target.value); setPage(1); }} /></label><button onClick={() => setReload(v => v + 1)}>刷新任务</button>{writable && <button className="primary" onClick={() => setEditor('new')}>创建备份任务</button>}</div>
    {notice && <p role="status">{notice}</p>}{error && <p role="alert" className="error">{error}<button onClick={() => setReload(v => v + 1)}>重新加载任务</button></p>}
    {!data && !error && <p role="status">正在加载备份任务…</p>}
    {data && <><div className="table-scroll"><table><caption className="sr-only">备份任务</caption><thead><tr>{['任务 / 企业', '版本', '本地进度', '归档核验', '异地副本', '操作'].map(v => <th scope="col" key={v}>{v}</th>)}</tr></thead><tbody>{data.items.map(j => <tr key={j.id}>
      <td><code>{j.id}</code><small>{j.tenantId} · {j.serverId}</small><small>原请求 {j.requestId}</small></td>
      <td>{j.operation.binding.releaseId}<small>部署代次 {j.operation.binding.generation} · 数据库 {j.operation.binding.schemaVersion}</small></td>
      <td><strong>{states[j.state] ?? '状态待确认'}</strong><small>{phases[j.phase] ?? '请刷新查看'}</small>{j.errorCode && j.state !== 'completed' && <p className="error">{j.state === 'failed' ? '归档未成功，不可用于恢复；可另建备份任务。' : '尚未通过核验，企业继续锁定，请运维检查原任务。'}</p>}</td>
      <td>{j.receipt.archive?.bytes ? <><code>{j.receipt.archive.sha256}</code><small>{j.receipt.archive.files} 项 · {j.receipt.archive.bytes.toLocaleString()} 字节（加密归档）</small></> : '暂无可用归档'}<small>访问恢复由独立维护流程控制</small></td>
      <td>{j.operation.offsiteTargetId ? <><strong>{j.state === 'failed' || j.state === 'cancelled' ? '未生成异地副本' : j.errorCode === 'BACKUP_OFFSITE_CONTROL_UNCONFIRMED' ? '异地状态暂不可确认，平台将重试' : offsiteStates[j.receipt.offsite?.state ?? ''] ?? '等待本地归档核验'}</strong><small>目标 {j.operation.offsiteTargetId}</small>{j.receipt.offsite?.attempts ? <small>交付尝试 {j.receipt.offsite.attempts} 次</small> : null}{j.receipt.offsite?.retryAt && <small>下次交付尝试 {new Date(j.receipt.offsite.retryAt).toLocaleString()}</small>}{j.receipt.offsite?.delivery && <small>收据清单 <code>{j.receipt.offsite.delivery.manifest.sha256}</code></small>}</> : '仅本地归档，未配置异地交付'}</td>
      <td>{writable && !j.controlPending && !j.leased && Number.isSafeInteger(j.receipt.revision) && (j.receipt.revision ?? 0) > 0 && <>
        {j.state === 'unconfirmed' && j.receipt.state === 'unconfirmed' && <button onClick={() => setEditor({ job: j, action: 'retry' })}>检查后重试 · {j.id}</button>}
        {j.state === 'pending' && j.receipt.state === 'pending' && j.receipt.phase === 'queued' && <button onClick={() => setEditor({ job: j, action: 'cancel' })}>取消待执行 · {j.id}</button>}
      </>}{j.controlPending && '操作已受理，等待原任务确认'}</td>
    </tr>)}{!data.items.length && <tr><td colSpan={6} className="empty">暂无备份任务。不会自动接管或停止现有服务。</td></tr>}</tbody></table></div><footer><span>共 {data.total} 条 · 第 {page} 页</span><div><button disabled={page === 1} onClick={() => setPage(v => v - 1)}>上一页</button><button disabled={page * data.pageSize >= data.total} onClick={() => setPage(v => v + 1)}>下一页</button></div></footer></>}
    {writable && editor && <BackupEditor client={client} edit={editor} onClose={() => setEditor(null)} onSaved={j => { setEditor(null); setReload(v => v + 1); setNotice(`任务 ${j.id} 已受理，请刷新查看原任务；未自动恢复企业访问。`); }} />}
  </section>;
}

function BackupEditor({ client, edit, onClose, onSaved }: { client: PlatformClient; edit: Edit; onClose: () => void; onSaved: (j: BackupJob) => void }) {
  const dialog = useRef<HTMLDialogElement>(null), alive = useRef(true), working = useRef(false), frozen = useRef<BackupInput | ControlInput | null>(null);
  const [busy, setBusy] = useState(false), [locked, setLocked] = useState(false), [error, setError] = useState(''), [tenantId, setTenantId] = useState(''), [conditions, setConditions] = useState<Conditions | null>(null);
  useEffect(() => { alive.current = true; dialog.current?.showModal(); return () => { alive.current = false; }; }, []);
  async function lookup() {
    if (working.current || locked) return;
    if (!idPattern.test(tenantId)) { setError('请填写有效企业 ID'); return; }
    working.current = true; setBusy(true); setConditions(null); setError('');
    try {
      const query = new URLSearchParams({ q: tenantId, pageSize: '100' });
      const [tenants, servers, jobs] = await Promise.all([client.request<Page<Tenant>>(`/tenants?${query}`), client.request<Page<ServerResource>>(`/servers?${query}`), client.request<Page<DeploymentJob>>(`/deployments?${new URLSearchParams({ q: tenantId, pageSize: '1' })}`)]);
      const tenant = tenants.items.find(t => t.id === tenantId), server = servers.items.find(s => s.tenantId === tenantId), deployment = jobs.items[0];
      if (!tenant || !server || tenant.status !== 'suspended') throw new Error('请先登记服务器、停用企业并等待停用确认完成');
      if (!deployment || deployment.tenantId !== tenantId || deployment.serverId !== server.id || deployment.state !== 'completed' || deployment.generation < 1 || deployment.operation.releaseId !== deployment.release.id) throw new Error('找不到已核验的当前部署，不能猜测备份目标');
      if (![tenant.configVersion, tenant.accessVersion, server.revision, deployment.generation].every(v => Number.isSafeInteger(v) && v > 0) || tenant.accessVersion < 2 || !/^[a-f0-9]{64}$/.test(deployment.operation.releaseDigest)) throw new Error('部署版本无法确认，请刷新');
      if (alive.current) setConditions({ tenant, server, deployment });
    } catch (e) { if (alive.current) setError(message(e)); }
    finally { working.current = false; if (alive.current) setBusy(false); }
  }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (working.current) return;
    if (!frozen.current) {
      const values = new FormData(event.currentTarget), reason = String(values.get('reason') ?? '').trim();
      if (reason.length < 2 || values.get('confirmed') !== 'yes') { setError('请填写操作理由，并确认维护影响'); return; }
      if (edit !== 'new') frozen.current = { requestId: crypto.randomUUID(), action: edit.action, expectedRevision: edit.job.receipt.revision!, reason, confirmed: true };
      else {
        if (!conditions || conditions.tenant.id !== tenantId) { setError('请先读取备份条件'); return; }
        const { tenant, server, deployment } = conditions;
        frozen.current = { requestId: crypto.randomUUID(), tenantId, serverId: server.id, releaseId: deployment.operation.releaseId, releaseDigest: deployment.operation.releaseDigest, expectedRevision: server.revision, expectedConfigVersion: tenant.configVersion, expectedAccessVersion: tenant.accessVersion, expectedGeneration: deployment.generation, reason, confirmed: true };
      }
    }
    working.current = true; setBusy(true); setLocked(true); setError('');
    try {
      const sent = frozen.current;
      const j = await client.request<BackupJob>(edit === 'new' ? '/backups' : `/backups/${encodeURIComponent(edit.job.id)}/control`, 'POST', sent);
      const b = j.operation?.binding;
      if (!/^backup-[a-f0-9]{32}$/.test(j.id ?? '') || !states[j.state] || j.operation?.id !== j.id || !b || b.tenantId !== j.tenantId || b.serverId !== j.serverId ||
        (edit === 'new' ? !('tenantId' in sent) || j.tenantId !== sent.tenantId || j.serverId !== sent.serverId || b.releaseId !== sent.releaseId || b.releaseDigest !== sent.releaseDigest || b.generation !== sent.expectedGeneration || b.accessVersion !== sent.expectedAccessVersion : j.id !== edit.job.id || j.operation.hostFingerprint !== edit.job.operation.hostFingerprint || j.operation.offsiteTargetId !== edit.job.operation.offsiteTargetId || !sameBinding(b, edit.job.operation.binding))) throw new Error('返回结果无法确认，请按原请求重试或查询任务');
      if (alive.current) onSaved(j);
    } catch (e) { if (alive.current) setError(message(e)); }
    finally { working.current = false; if (alive.current) setBusy(false); }
  }
  return <dialog ref={dialog} aria-labelledby="backup-title" onCancel={e => { e.preventDefault(); if (!busy) onClose(); }}><form onSubmit={submit}>
    <h2 id="backup-title">{edit === 'new' ? '创建冷备份任务' : edit.action === 'retry' ? '重试原备份任务' : '取消待执行备份'}</h2>
    <p>{edit === 'new' ? '备份使用代理预置的独立密钥和目录，不接收浏览器指定的路径。服务恢复并通过核验之前，企业持续锁定。' : edit.action === 'retry' ? '检查原故障后，从代理已记录的阶段继续；不会另建任务，也不会自动恢复企业访问。' : '仅尚未开始的任务可取消；若代理已开始暂停服务，取消可能不再生效。关闭窗口不取消任务。'}</p>
    {edit === 'new' && <><label>企业 ID<input value={tenantId} disabled={busy || locked} onChange={e => { setTenantId(e.target.value); setConditions(null); }} /></label><button type="button" disabled={busy || locked} onClick={lookup}>读取备份条件</button>{conditions && <p role="status">{conditions.tenant.displayName} · {conditions.deployment.operation.releaseId} · 部署代次 {conditions.deployment.generation}</p>}</>}
    <label>操作理由<textarea name="reason" required minLength={2} maxLength={300} disabled={busy || locked} /></label>
    <label className="confirmation"><input name="confirmed" type="checkbox" value="yes" required disabled={busy || locked} />我已确认维护窗口，任务未核验前不恢复访问</label>
    {error && <p role="alert" className="error">{error}</p>}{locked && <p>请求号 {frozen.current?.requestId} 已锁定。结果不明时按原请求重试或到任务列表查询，不要重复创建。</p>}
    <div className="dialog-actions"><button type="button" disabled={busy} onClick={onClose}>{locked ? '关闭窗口' : '取消'}</button><button className="primary" disabled={busy || (edit === 'new' && !conditions)}>{busy ? '提交中…' : locked ? '按原请求重试' : '确认备份操作'}</button></div>
  </form></dialog>;
}
