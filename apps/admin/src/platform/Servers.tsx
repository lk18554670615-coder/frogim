import { useEffect, useRef, useState, type FormEvent } from 'react';
import { PlatformClient, PlatformError, type Page, type Tenant } from './api';

export type ServerResource = { id: string; tenantId: string; displayName: string; hostFingerprint: string; isolationMode: 'local_preview' | 'dedicated_host'; runtime: string; revision: number; configVersion: number; verifiedAt: string };
type Operation = { requestId: string; action: 'register' | 'inspect'; serverId: string; tenantId: string; displayName: string; expectedRevision: number; expectedConfigVersion: number; reason: string; confirmed: boolean };
const message = (e: unknown) => e instanceof PlatformError && e.code === 'SERVER_OPERATION_NOT_FOUND' ? '尚未找到本请求结果，可使用原请求重试；不要重复登记。' : e instanceof Error ? e.message : '请求未完成，请查询原请求结果';
const idPattern = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$/;

export function PlatformServers({ client, writable, refresh, initialTenantId = '' }: { client: PlatformClient; writable: boolean; refresh: number; initialTenantId?: string }) {
  const [data, setData] = useState<Page<ServerResource> | null>(null), [q, setQ] = useState(initialTenantId), [page, setPage] = useState(1);
  const [revision, setRevision] = useState(0), [error, setError] = useState(''), [notice, setNotice] = useState('');
  const [editor, setEditor] = useState<ServerResource | 'new' | null>(null);
  useEffect(() => {
    let active = true; const abort = new AbortController(); setError(''); setData(null);
    const params = new URLSearchParams({ q, page: String(page), pageSize: '25' });
    client.request<Page<ServerResource>>(`/servers?${params}`, 'GET', undefined, abort.signal)
      .then(value => { if (active) setData(value); }).catch(e => { if (active) setError(message(e)); });
    return () => { active = false; abort.abort(); };
  }, [client, q, page, refresh, revision]);
  return <section aria-label="服务器资源">
    <p className="preview-banner">本页仅支持登记绑定和代理身份检查，不执行部署、重启或删除。受控部署需另建部署任务。主机标识用于发现重复绑定，不能代替云服务器物理隔离验收。</p>
    <div className="filters"><label>服务器名称 / ID / 企业 ID<input value={q} onChange={e => { setQ(e.target.value); setPage(1); }} /></label>{writable && <button className="primary" onClick={() => setEditor('new')}>登记服务器</button>}</div>
    {notice && <p role="status">{notice}</p>}{error && <p role="alert" className="error">{error}<button onClick={() => setRevision(v => v + 1)}>重试</button></p>}
    {!data && !error && <p role="status">正在加载服务器…</p>}
    {data && <><div className="table-scroll"><table><caption className="sr-only">服务器资源</caption><thead><tr>{['服务器 / 企业', '隔离模式', '登记校验范围', '最近校验', '操作'].map(label => <th key={label} scope="col">{label}</th>)}</tr></thead><tbody>
      {data.items.map(row => <tr key={row.id}><td><strong>{row.displayName}</strong><code>{row.id}</code><small>企业：{row.tenantId}</small></td><td>{row.isolationMode === 'local_preview' ? '本机预览（非物理隔离）' : '独立主机配置（仍需验收）'}<small title={row.hostFingerprint}>主机标识 {row.hostFingerprint.slice(0, 12)}…</small></td><td>仅检查<small>{row.runtime}</small></td><td>{new Date(row.verifiedAt).toLocaleString('zh-CN', { hour12: false })}<small>校验版本 {row.revision} · 不代表当前在线</small></td><td>{writable ? <button onClick={() => setEditor(row)}>重新校验 · {row.displayName}</button> : '只读'}</td></tr>)}
      {data.items.length === 0 && <tr><td colSpan={5} className="empty">尚无匹配的服务器记录。需由运维先配置受信代理，再登记绑定。</td></tr>}
    </tbody></table></div><footer><span>共 {data.total} 条 · 第 {page} 页</span><div><button disabled={page === 1} onClick={() => setPage(p => p - 1)}>上一页</button><button disabled={page * data.pageSize >= data.total} onClick={() => setPage(p => p + 1)}>下一页</button></div></footer></>}
    {writable && editor && <ServerEditor key={editor === 'new' ? 'new' : editor.id} client={client} server={editor === 'new' ? undefined : editor} onClose={() => setEditor(null)} onSaved={row => { setEditor(null); setRevision(v => v + 1); setNotice(`${row.displayName} 已完成代理校验与登记，未执行任何部署。`); }} />}
  </section>;
}

function ServerEditor({ client, server, onClose, onSaved }: { client: PlatformClient; server?: ServerResource; onClose: () => void; onSaved: (v: ServerResource) => void }) {
  const dialog = useRef<HTMLDialogElement>(null), alive = useRef(true), working = useRef(false), frozen = useRef<Operation | null>(null);
  const [busy, setBusy] = useState(false), [locked, setLocked] = useState(false), [error, setError] = useState('');
  const [choices, setChoices] = useState<string[]>([]), [serverId, setServerId] = useState(server?.id ?? ''), [name, setName] = useState(server?.displayName ?? '');
  const [tenantId, setTenantId] = useState(server?.tenantId ?? ''), [tenant, setTenant] = useState<{ id: string; displayName: string; configVersion: number } | null>(server ? { id: server.tenantId, displayName: server.tenantId, configVersion: server.configVersion } : null);
  useEffect(() => {
    alive.current = true; dialog.current?.showModal(); const abort = new AbortController();
    if (!server) client.request<{ serverIds: string[] }>('/servers/configured', 'GET', undefined, abort.signal).then(v => { if (alive.current) setChoices(v.serverIds); }).catch(e => { if (alive.current) setError(message(e)); });
    return () => { alive.current = false; abort.abort(); frozen.current = null; };
  }, [client, server]);
  async function lookup() {
    if (working.current || locked) return;
    if (!idPattern.test(tenantId)) { setError('请填写有效的企业 ID'); return; }
    working.current = true; setBusy(true); setError(''); setTenant(null);
    try {
      const result = await client.request<Page<Tenant>>(`/tenants?${new URLSearchParams({ q: tenantId, pageSize: '100' })}`);
      // Tenant ID matches sort first only accidentally; never choose a fuzzy
      // name match or silently fall back to the default tenant.
      const found = result.items.find(v => v.id === tenantId);
      if (!found || !Number.isSafeInteger(found.configVersion) || found.configVersion < 1) throw new Error('未找到该企业，请检查 ID 或先在企业目录登记');
      if (alive.current) setTenant(found);
    } catch (e) { if (alive.current) setError(message(e)); }
    finally { working.current = false; if (alive.current) setBusy(false); }
  }
  function accepted(row: ServerResource) {
    const sent = frozen.current;
    if (!sent || row.id !== sent.serverId || row.tenantId !== sent.tenantId || row.displayName !== sent.displayName || row.revision !== sent.expectedRevision + 1 || !['local_preview', 'dedicated_host'].includes(row.isolationMode) || !/^[a-f0-9]{64}$/.test(row.hostFingerprint) || !Number.isFinite(Date.parse(row.verifiedAt))) throw new Error('响应无法确认，请查询原请求结果');
    if (alive.current) onSaved(row);
  }
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); if (working.current) return;
    if (!frozen.current) {
      const form = new FormData(e.currentTarget), reason = String(form.get('reason') ?? '').trim();
      if (!tenant || tenant.id !== tenantId || !idPattern.test(serverId) || (!server && !choices.includes(serverId)) || !name.trim()) { setError('请选择预配置代理、填写名称并读取企业配置'); return; }
      if (reason.length < 2 || form.get('confirmed') !== 'yes') { setError('请填写操作理由并确认绑定影响'); return; }
      frozen.current = { requestId: crypto.randomUUID(), action: server ? 'inspect' : 'register', serverId, tenantId, displayName: name.trim(), expectedRevision: server?.revision ?? 0, expectedConfigVersion: tenant.configVersion, reason, confirmed: true };
      setLocked(true);
    }
    working.current = true; setBusy(true); setError('');
    try { accepted(await client.request<ServerResource>('/servers/operations', 'POST', frozen.current)); }
    catch (e) { if (alive.current) setError(message(e)); }
    finally { working.current = false; if (alive.current) setBusy(false); }
  }
  async function lookupResult() {
    if (!frozen.current || working.current) return; working.current = true; setBusy(true); setError('');
    try { accepted(await client.request<ServerResource>(`/servers/operations/${frozen.current.requestId}`)); }
    catch (e) { if (alive.current) setError(message(e)); }
    finally { working.current = false; if (alive.current) setBusy(false); }
  }
  return <dialog ref={dialog} aria-labelledby="server-editor-title" onCancel={e => { e.preventDefault(); if (!busy) onClose(); }}><form onSubmit={submit}>
    <h2 id="server-editor-title">{server ? '重新校验服务器' : '登记服务器'}</h2>
    <p>服务器与企业一对一绑定。平台将核对代理证书、企业地址和主机标识；不会购买服务器、部署应用或激活企业。</p>
    {server ? <p>{server.id} → {server.tenantId} · 原校验版本 {server.revision}</p> : <>
      <label>预配置代理<select required value={serverId} disabled={busy || locked} onChange={e => setServerId(e.target.value)}><option value="">请选择运维已配置的代理</option>{choices.map(id => <option key={id} value={id}>{id}</option>)}</select></label>
      {!choices.length && <small>未配置代理时不能登记；此页面不接受控制地址、密钥或命令。</small>}
      <label>服务器名称<input required maxLength={80} value={name} disabled={busy || locked} onChange={e => setName(e.target.value)} /></label>
      <label>绑定企业 ID<input required maxLength={80} value={tenantId} disabled={busy || locked} onChange={e => { setTenantId(e.target.value.trim()); setTenant(null); }} /></label><button type="button" disabled={busy || locked} onClick={lookup}>读取企业配置</button>
    </>}
    {tenant && <p>已确认企业：{tenant.displayName}（{tenant.id}）· 配置版本 {tenant.configVersion}</p>}
    <label>操作理由<textarea name="reason" required minLength={2} maxLength={300} disabled={busy || locked} /></label>
    <label className="confirmation"><input type="checkbox" name="confirmed" value="yes" required disabled={busy || locked} />我已确认服务器与企业绑定，仅进行身份检查</label>
    {error && <p role="alert" className="error">{error}</p>}
    {locked && <p>请求号：<code>{frozen.current?.requestId}</code>重试复用原请求。关闭窗口不撤销已提交结果；若结果未知，请先查询。</p>}
    <div className="dialog-actions"><button type="button" disabled={busy} onClick={onClose}>{locked ? '关闭窗口' : '取消'}</button>{locked && <button type="button" disabled={busy} onClick={lookupResult}>查询提交结果</button>}<button className="primary" disabled={busy}>{busy ? '校验中…' : locked ? '按原请求重试' : '确认服务器操作'}</button></div>
  </form></dialog>;
}
