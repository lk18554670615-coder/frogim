import { useEffect, useRef, useState, type FormEvent } from 'react';
import { PlatformClient, type Page, type Tenant } from './api';

export type BackupSchedule = { tenantId: string; enabled: boolean; startMinuteUtc: number; windowMinutes: number; version: number; actorId: string; operatorEnabled?: boolean; updatedAt: string };
export type MaintenanceRun = { id: string; tenantId: string; state: string; phase: string; slotDate: string; windowEnd: string; pauseId: string; backupId: string; resumeId: string; backupResult: string; errorCode: string };
type Input = { requestId: string; enabled: boolean; startMinuteUtc: number; windowMinutes: number; expectedVersion: number; reason: string; confirmed: true };
const message = (e: unknown) => e instanceof Error ? e.message : '结果尚未确认，请刷新查询';
const clock = (minute: number) => `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`;
const utcDate = (value: string) => Number.isFinite(Date.parse(value)) ? new Date(value).toISOString().replace('T', ' ').replace('.000Z', ' UTC') : '时间待核对';
const states: Record<string, string> = { pending: '处理中', completed: '本地备份和访问恢复已核验', failed: '备份失败，企业访问已恢复', skipped: '本次已跳过' };
const phases: Record<string, string> = { prepare: '检查维护条件', pause: '确认企业停用', backup: '等待备份核验', resume: '确认企业恢复', finished: '结束' };
const errors: Record<string, string> = { MAINTENANCE_TENANT_NOT_ACTIVE: '企业原已停用，不接管其他人的维护', MAINTENANCE_WINDOW_EXPIRED: '维护窗口已结束，不再新建备份', MAINTENANCE_BACKUP_NEEDS_ATTENTION: '原备份恢复未确认，请在备份任务中检查后重试；不会自动重试或解锁', MAINTENANCE_BACKUP_FAILED: '未获得有效备份，不能用于恢复', MAINTENANCE_CONTROL_UNCONFIRMED: '控制状态未确认，保持原任务并继续核对' };

export function PlatformBackupSchedules({ client, writable, refresh }: { client: PlatformClient; writable: boolean; refresh: number }) {
  const [q, setQ] = useState(''), [view, setView] = useState<'backup-schedules' | 'maintenance'>('backup-schedules'), [page, setPage] = useState(1), [reload, setReload] = useState(0);
  const [loaded, setLoaded] = useState<{ key: string; data: Page<BackupSchedule | MaintenanceRun> } | null>(null), [error, setError] = useState(''), [notice, setNotice] = useState('');
  const [editor, setEditor] = useState<string | null>(null);
  const queryKey = JSON.stringify([view, q, page, reload, refresh]);
  const data = loaded?.key === queryKey ? loaded.data : null;
  useEffect(() => {
    let alive = true; const abort = new AbortController(); setLoaded(null); setError('');
    client.request<Page<BackupSchedule | MaintenanceRun>>(`/${view}?${new URLSearchParams({ q, page: String(page), pageSize: '25' })}`, 'GET', undefined, abort.signal)
      .then(v => { if (alive) setLoaded({ key: queryKey, data: v }); }).catch(e => { if (alive) setError(message(e)); });
    return () => { alive = false; abort.abort(); };
  }, [client, view, q, page, queryKey]);
  return <section aria-label="每日维护备份">
    <p className="preview-banner">默认关闭。启用后每天在 UTC 维护窗口内短暂停用企业、生成加密备份并核验恢复，用户需重新登录。仅支持已经核验部署的企业；人工停用的企业会跳过。关闭计划只阻止后续任务，已经开始的任务仍需安全恢复。异地交付独立进行，请在“备份任务”查看其收据；维护完成不代表异地副本或灾难恢复验收通过。</p>
    <div className="filters"><label>查看内容<select value={view} onChange={e => { setView(e.target.value as typeof view); setPage(1); }}><option value="backup-schedules">维护计划</option><option value="maintenance">执行记录</option></select></label><label>企业 ID<input value={q} onChange={e => { setQ(e.target.value); setPage(1); }} /></label>{writable && <button className="primary" onClick={() => setEditor('')}>配置维护窗口</button>}</div>
    {notice && <p role="status">{notice}</p>}{error && <p role="alert" className="error">{error}<button onClick={() => setReload(v => v + 1)}>重新加载</button></p>}
    {!data && !error && <p role="status">正在加载…</p>}
    {data && <><div className="table-scroll"><table><caption className="sr-only">{view === 'backup-schedules' ? '维护计划' : '执行记录'}</caption><thead><tr>{(view === 'backup-schedules' ? ['企业', '每日窗口（UTC）', '状态 / 版本', '操作'] : ['企业 / 任务', '窗口（UTC）', '进度', '关联任务']).map(v => <th key={v} scope="col">{v}</th>)}</tr></thead><tbody>{data.items.map(row => {
      if (view === 'backup-schedules') { const s = row as BackupSchedule; return <tr key={s.tenantId}><td><code>{s.tenantId}</code></td><td>{clock(s.startMinuteUtc)} · {s.windowMinutes} 分钟<small>北京时间开始：{clock((s.startMinuteUtc + 480) % 1440)}</small></td><td>{s.enabled ? '已启用' : '已关闭'} · 版本 {s.version}{s.enabled && s.operatorEnabled === false && <p className="error">原操作者已不可用，不启动新任务；请有效运营管理员重新确认。</p>}</td><td>{writable ? <button onClick={() => setEditor(s.tenantId)}>修改维护窗口</button> : '只读'}</td></tr>; }
      const j = row as MaintenanceRun; return <tr key={j.id}><td>{j.tenantId}<code>{j.id}</code></td><td>{j.slotDate}<small>截止 {utcDate(j.windowEnd)}</small></td><td><strong>{states[j.state] ?? '状态待确认'}</strong><small>{phases[j.phase] ?? '待核对'}</small>{j.errorCode && <p className="error">{errors[j.errorCode] ?? '任务未确认，请运维检查'}</p>}</td><td>停用：<code>{j.pauseId || '未开始'}</code>备份：<code>{j.backupId || '未开始'}</code>恢复：<code>{j.resumeId || '未开始'}</code></td></tr>;
    })}</tbody></table>{data.items.length === 0 && <p>暂无记录，未配置企业默认关闭每日备份。</p>}</div><div className="pagination"><button disabled={page <= 1} onClick={() => setPage(v => v - 1)}>上一页</button><span>第 {page} 页 · 共 {data.total} 条</span><button disabled={page * data.pageSize >= data.total} onClick={() => setPage(v => v + 1)}>下一页</button></div></>}
    {editor !== null && <ScheduleEditor key={editor} client={client} initialTenant={editor} onClose={() => setEditor(null)} onSaved={s => { setEditor(null); setNotice(`已保存 ${s.tenantId} 的维护计划（版本 ${s.version}）。现有任务会继续安全恢复，不会被取消。`); setReload(v => v + 1); }} />}
  </section>;
}

function ScheduleEditor({ client, initialTenant, onClose, onSaved }: { client: PlatformClient; initialTenant: string; onClose: () => void; onSaved: (s: BackupSchedule) => void }) {
  const dialog = useRef<HTMLDialogElement>(null), alive = useRef(true), working = useRef(false), frozen = useRef<{ tenant: string; input: Input } | null>(null);
  const [tenant, setTenant] = useState(initialTenant), [loaded, setLoaded] = useState<{ tenant: Tenant; version: number } | null>(null);
  const [enabled, setEnabled] = useState(false), [start, setStart] = useState('02:00'), [duration, setDuration] = useState('60'), [busy, setBusy] = useState(false), [locked, setLocked] = useState(false), [error, setError] = useState('');
  useEffect(() => { alive.current = true; dialog.current?.showModal(); return () => { alive.current = false; }; }, []);
  async function lookup() {
    if (working.current || frozen.current) return;
    const id = tenant.trim(); if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$/.test(id)) { setError('请输入有效企业 ID'); return; }
    working.current = true; setBusy(true); setLoaded(null); setError('');
    try {
      const [tenants, plans] = await Promise.all([client.request<Page<Tenant>>(`/tenants?${new URLSearchParams({ q: id })}`), client.request<Page<BackupSchedule>>(`/backup-schedules?${new URLSearchParams({ q: id })}`)]);
      if (!alive.current) return;
      const t = tenants.items.find(t => t.id === id), s = plans.items.find(s => s.tenantId === id);
      if (!t || plans.total !== (s ? 1 : 0) || (s && (!Number.isSafeInteger(s.version) || s.version < 1 || !Number.isInteger(s.startMinuteUtc) || s.startMinuteUtc < 0 || s.startMinuteUtc > 1439 || !Number.isInteger(s.windowMinutes) || s.windowMinutes < 15 || s.windowMinutes > 180))) throw new Error('企业或维护配置无法确认，请重新加载');
      setTenant(id); setLoaded({ tenant: t, version: s?.version ?? 0 }); setEnabled(s?.enabled ?? false); setStart(clock(s?.startMinuteUtc ?? 120)); setDuration(String(s?.windowMinutes ?? 60));
    } catch (e) { if (alive.current) setError(message(e)); }
    finally { working.current = false; if (alive.current) setBusy(false); }
  }
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); if (working.current || !loaded) return;
    if (!frozen.current) {
      const fields = new FormData(e.currentTarget), reason = String(fields.get('reason') ?? '').trim();
      if (reason.length < 2 || reason.length > 300 || fields.get('confirmed') !== 'yes') { setError('请填写操作理由，并确认每日维护影响'); return; }
      const parts = /^(\d{2}):(\d{2})$/.exec(start), minutes = Number(duration);
      if (!parts || Number(parts[1]) > 23 || Number(parts[2]) > 59 || !Number.isInteger(minutes) || minutes < 15 || minutes > 180) { setError('开始时间必须为 UTC 00:00–23:59，窗口为 15–180 分钟'); return; }
      frozen.current = { tenant: loaded.tenant.id, input: { requestId: crypto.randomUUID(), enabled, startMinuteUtc: Number(parts[1]) * 60 + Number(parts[2]), windowMinutes: minutes, expectedVersion: loaded.version, reason, confirmed: true } };
    }
    working.current = true; setBusy(true); setLocked(true); setError('');
    try {
      const sent = frozen.current;
      const s = await client.request<BackupSchedule>(`/backup-schedules/${encodeURIComponent(sent.tenant)}`, 'PUT', sent.input);
      if (s.tenantId !== sent.tenant || s.enabled !== sent.input.enabled || s.startMinuteUtc !== sent.input.startMinuteUtc || s.windowMinutes !== sent.input.windowMinutes || !Number.isSafeInteger(s.version) || s.version < 1 || s.version < sent.input.expectedVersion || s.version > sent.input.expectedVersion + 1) throw new Error('返回结果无法确认，请按原请求重试或查询计划');
      if (alive.current) onSaved(s);
    } catch (e) { if (alive.current) setError(message(e)); }
    finally { working.current = false; if (alive.current) setBusy(false); }
  }
  return <dialog ref={dialog} aria-labelledby="maintenance-title" onCancel={e => { e.preventDefault(); if (!busy) onClose(); }}><form onSubmit={submit}><h2 id="maintenance-title">每日维护窗口</h2>
    <p>这是每日重复的停机授权：会断开原会话，备份后用户需重新登录。停止新任务不会中止已经开始的安全恢复；不能覆盖人工停用状态。同一企业同一 UTC 窗口日期最多执行一次。</p>
    <label>配置企业 ID<input value={tenant} disabled={busy || locked} onChange={e => { setTenant(e.target.value); setLoaded(null); }} /></label><button type="button" disabled={busy || locked} onClick={lookup}>读取当前计划</button>
    {loaded && <p role="status">{loaded.tenant.displayName} · 当前版本 {loaded.version || '未配置'}</p>}
    <label className="confirmation"><input type="checkbox" checked={enabled} disabled={busy || locked || !loaded} onChange={e => setEnabled(e.target.checked)} />启用每日维护备份</label>
    <label>每日开始时间（UTC）<input type="time" value={start} disabled={busy || locked || !loaded} onChange={e => setStart(e.target.value)} /></label>
    <label>维护窗口（分钟）<input type="number" min={15} max={180} step={1} value={duration} disabled={busy || locked || !loaded} onChange={e => setDuration(e.target.value)} /></label>
    <p>窗口结束后不再新建停用或备份任务；已受理的备份和企业恢复仍会继续。执行时长须在外部生产验收中确认。</p>
    <label>操作理由<textarea name="reason" required minLength={2} maxLength={300} disabled={busy || locked} /></label>
    <label className="confirmation"><input name="confirmed" type="checkbox" value="yes" required disabled={busy || locked} />我已确认每日停机窗口及恢复影响</label>
    {error && <p role="alert" className="error">{error}</p>}{locked && <p>原请求 {frozen.current?.input.requestId} 已锁定；结果不明时按原请求重试。冲突时关闭窗口，重新读取后再确认，不覆盖别人刚修改的计划。</p>}
    <div className="dialog-actions"><button type="button" disabled={busy} onClick={onClose}>{locked ? '关闭窗口' : '取消'}</button><button className="primary" disabled={busy || !loaded}>{busy ? '提交中…' : locked ? '按原请求重试' : '保存维护计划'}</button></div>
  </form></dialog>;
}
