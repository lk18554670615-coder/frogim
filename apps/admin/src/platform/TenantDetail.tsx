import { useEffect, useRef, useState, type FormEvent } from 'react';
import { PlatformClient, type TenantDetail as Detail } from './api';

type Action = 'edit' | 'archive' | 'unarchive' | 'default';
type Related = 'accounts' | 'codes' | 'servers' | 'deployments' | 'backups' | 'maintenance' | 'jobs' | 'realm-jobs' | 'audits';
const errorText = (e: unknown) => e instanceof Error ? e.message : '请求失败，请刷新企业详情';
const date = (value: string) => new Date(value).toLocaleString('zh-CN', { hour12: false });

export function TenantDetail({ id, client, writable, onClose, onChanged, onNavigate }: {
  id: string; client: PlatformClient; writable: boolean; onClose: () => void;
  onChanged: () => void; onNavigate: (tab: Related, tenantId: string) => void;
}) {
  const [detail, setDetail] = useState<Detail | null>(null);
  const [error, setError] = useState(''), [reload, setReload] = useState(0);
  const [action, setAction] = useState<Action | null>(null);
  useEffect(() => {
    let active = true; const abort = new AbortController(); setDetail(null); setError('');
    client.request<Detail>(`/tenants/${encodeURIComponent(id)}`, 'GET', undefined, abort.signal)
      .then(value => { if (active) setDetail(value); })
      .catch(e => { if (active) setError(errorText(e)); });
    return () => { active = false; abort.abort(); };
  }, [id, client, reload]);
  return <section className="tenant-detail" aria-label="企业详情">
    <div className="heading"><h2>企业详情 · {id}</h2><button onClick={onClose}>关闭详情</button></div>
    {error && <p role="alert" className="error">{error}<button onClick={() => setReload(v => v + 1)}>重试</button></p>}
    {!detail && !error && <p role="status">正在加载企业详情…</p>}
    {detail && <>
      <div className="tenant-detail-grid">
        <div><span>名称</span><strong>{detail.displayName}</strong></div>
        <div><span>企业 ID</span><code>{detail.id}</code></div>
        <div><span>业务地址（固定）</span><code>{detail.httpBaseUrl}</code></div>
        <div><span>状态</span><strong>{detail.archivedAt ? '已归档' : detail.status}{detail.isDefault ? ' · 默认企业' : ''}</strong></div>
        <div><span>目录版本</span><strong>{detail.directoryVersion}</strong></div>
        <div><span>服务版本</span><strong>配置 {detail.configVersion} · 访问 {detail.accessVersion}</strong></div>
        <div><span>创建 / 最近更新</span><small>{date(detail.createdAt)} / {date(detail.updatedAt)}</small></div>
        <div><span>每日维护</span><strong>{detail.maintenanceEnabled ? '已启用' : '已关闭'}</strong></div>
      </div>
      <p><strong>内部备注：</strong>{detail.note || '无'}</p>
      {detail.archivedAt && <p>由 {detail.archivedBy || '平台管理员'} 于 {date(detail.archivedAt)} 归档。账号、业务记录与备份仍保留。</p>}
      <div className="tenant-detail-grid" aria-label="关联摘要">
        <div><span>平台账号</span><strong>{detail.accountCount}</strong></div>
        <div><span>有效邀请码</span><strong>{detail.enabledCodeCount}</strong></div>
        <div><span>登记服务器</span><strong>{detail.serverCount}</strong></div>
        <div><span>待完成任务</span><strong>{detail.pendingJobCount}</strong></div>
      </div>
      <div className="row-actions" aria-label="关联管理入口">
        {([['accounts', '账号'], ['codes', '邀请码'], ['servers', '服务器'], ['deployments', '部署'], ['backups', '备份'], ['maintenance', '每日维护'], ['jobs', '身份任务'], ['realm-jobs', '启停任务'], ['audits', '审计']] as const).map(([tab, label]) => <button key={tab} onClick={() => onNavigate(tab, id)}>{label}</button>)}
      </div>
      {writable && <div className="row-actions" aria-label="企业目录操作">
        <button onClick={() => setAction('edit')}>修改名称与备注</button>
        {!detail.isDefault && detail.status === 'active' && !detail.archivedAt && <button onClick={() => setAction('default')}>设为默认企业</button>}
        {!detail.archivedAt && !detail.isDefault && ['provisioning', 'suspended'].includes(detail.status) && <button disabled={detail.pendingJobCount > 0} onClick={() => setAction('archive')}>归档企业</button>}
        {detail.archivedAt && <button onClick={() => setAction('unarchive')}>取消归档</button>}
      </div>}
    </>}
    {action && detail && <TenantActionDialog key={`${id}-${action}`} client={client} detail={detail} action={action} onClose={() => setAction(null)} onSaved={updated => { setAction(null); setDetail(updated); setReload(v => v + 1); onChanged(); }} />}
  </section>;
}

function TenantActionDialog({ client, detail, action, onClose, onSaved }: {
  client: PlatformClient; detail: Detail; action: Action; onClose: () => void; onSaved: (detail: Detail) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null), active = useRef(true), sending = useRef(false);
  const [busy, setBusy] = useState(false), [error, setError] = useState('');
  useEffect(() => { active.current = true; dialog.current?.showModal(); return () => { active.current = false; }; }, []);
  const title = { edit: '修改企业资料', archive: '归档企业', unarchive: '取消归档', default: '切换默认企业' }[action];
  const explanation = {
    edit: '仅更新目录名称和内部备注，不修改企业 ID、业务地址、运行服务或访问状态。',
    archive: '企业须已停用或尚未激活且无待确认任务。归档会关闭每日维护计划和邀请码，保留账号、业务数据与备份。',
    unarchive: '取消归档后，企业仍保持原停用或待开通状态；邀请码和每日维护不会自动恢复。',
    default: `此后无企业码的新注册将归属 ${detail.displayName}；现有账号和两个企业的运行服务不变。`,
  }[action];
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); if (sending.current) return;
    const form = new FormData(e.currentTarget), reason = String(form.get('reason') ?? '').trim();
    if (reason.length < 2 || form.get('confirmed') !== 'yes') { setError('请填写操作理由并确认影响'); return; }
    let path = `/tenants/${encodeURIComponent(detail.id)}`, method = 'POST';
    const body: Record<string, unknown> = { expectedDirectoryVersion: detail.directoryVersion, reason, confirmed: true };
    if (action === 'edit') {
      method = 'PATCH'; body.displayName = String(form.get('displayName') ?? '').trim(); body.note = String(form.get('note') ?? '').trim();
    } else if (action === 'default') {
      path += '/make-default'; body.expectedCurrentDefaultId = detail.currentDefaultId;
    } else path += action === 'archive' ? '/archive' : '/unarchive';
    sending.current = true; setBusy(true); setError('');
    try { const updated = await client.request<Detail>(path, method, body); if (active.current) onSaved(updated); }
    catch (e) { if (active.current) setError(errorText(e)); }
    finally { sending.current = false; if (active.current) setBusy(false); }
  }
  return <dialog ref={dialog} aria-labelledby="tenant-action-title" onCancel={e => { e.preventDefault(); if (!busy) onClose(); }}>
    <form onSubmit={submit}><h2 id="tenant-action-title">{title}</h2><p>{explanation}</p>
      {action === 'edit' && <><label>企业名称<input name="displayName" defaultValue={detail.displayName} required maxLength={120} disabled={busy} /></label><label>内部备注<textarea name="note" defaultValue={detail.note} maxLength={1000} disabled={busy} /></label></>}
      <label>操作理由<textarea name="reason" required minLength={2} maxLength={300} disabled={busy} /></label>
      <label className="confirmation"><input name="confirmed" type="checkbox" value="yes" required disabled={busy} />我已确认上述操作与影响范围</label>
      {error && <p role="alert" className="error">{error}</p>}
      <div className="dialog-actions"><button type="button" disabled={busy} onClick={onClose}>取消</button><button className="primary" disabled={busy}>{busy ? '处理中…' : '确认操作'}</button></div>
    </form>
  </dialog>;
}
