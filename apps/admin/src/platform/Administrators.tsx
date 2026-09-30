import { useEffect, useRef, useState, type FormEvent } from 'react';
import { PlatformClient, PlatformError, type Operator, type Page } from './api';

type Administrator = { id: string; username: string; role: 'operator' | 'reader'; enabled: boolean; authVersion: number; createdAt: string; updatedAt: string };
type Operation = { action: 'create' | 'access' | 'password'; target?: Administrator };
type Write = { action: Operation['action']; targetId: string; username: string; role: string; enabled: boolean | null; expectedVersion: number; requestId: string; reason: string; confirmed: true; password: string; currentPassword: string };
const roles = { operator: '运营管理员', reader: '只读管理员' };
const message = (e: unknown) => e instanceof Error ? e.message : '平台服务暂不可用';
const valid = (r: unknown): r is Administrator => !!r && typeof r === 'object' && typeof (r as Administrator).id === 'string' && typeof (r as Administrator).username === 'string' && ['operator', 'reader'].includes((r as Administrator).role) && typeof (r as Administrator).enabled === 'boolean' && Number.isSafeInteger((r as Administrator).authVersion) && (r as Administrator).authVersion > 0;

export function PlatformAdministrators({ client, operator, refresh, onPasswordChanged }: { client: PlatformClient; operator: Operator; refresh: number; onPasswordChanged: () => void }) {
  const [page, setPage] = useState(1), [query, setQuery] = useState(''), [state, setState] = useState('');
  const [data, setData] = useState<Page<Administrator> | null>(null), [error, setError] = useState(''), [notice, setNotice] = useState(''), [reload, setReload] = useState(0);
  const [operation, setOperation] = useState<Operation | null>(null);
  useEffect(() => {
    let current = true; const abort = new AbortController(); setData(null); setError('');
    const qs = new URLSearchParams({ page: String(page), pageSize: '25', q: query, state });
    client.request<Page<Administrator>>(`/administrators?${qs}`, 'GET', undefined, abort.signal).then(result => {
      if (!Array.isArray(result.items) || result.items.some(r => !valid(r)) || !Number.isInteger(result.total) || result.total < 0 || result.page !== page || result.pageSize !== 25) throw new Error('管理员列表响应不完整');
      if (current) setData(result);
    }).catch(e => { if (current) setError(message(e)); });
    return () => { current = false; abort.abort(); };
  }, [client, page, query, state, refresh, reload]);
  return <section aria-label="平台管理员管理"><p>这是独立的平台权限域，不影响企业后台管理员。停用、调整角色或重置密码会撤销该账号的全部旧会话；恢复账号不会恢复旧会话。</p>
    <p className="muted">不能修改本人的启停状态或角色。本人修改密码需要验证当前密码；新密码为 6–32 个字符，且不超过 72 个 UTF-8 字节。</p>
    {notice && <p role="status">{notice}</p>}
    <div className="filters"><label>管理员账号搜索<input value={query} maxLength={80} onChange={e => { setQuery(e.target.value); setPage(1); }} /></label><label>管理员状态<select value={state} onChange={e => { setState(e.target.value); setPage(1); }}><option value="">全部</option><option value="enabled">启用</option><option value="disabled">停用</option></select></label>{operator.role === 'operator' && <button onClick={() => setOperation({ action: 'create' })}>新增平台管理员</button>}</div>
    {error ? <p role="alert" className="error">{error}<button onClick={() => setReload(v => v + 1)}>重试管理员查询</button></p> : !data ? <p role="status">正在加载管理员…</p> : <>
      <div className="table-scroll"><table aria-label="平台管理员"><thead><tr><th>账号</th><th>角色</th><th>状态</th><th>认证版本</th><th>操作</th></tr></thead><tbody>{data.items.map(r => <tr key={r.id}><td><strong>{r.username}</strong>{r.id === operator.id && <small>当前账号</small>}</td><td>{roles[r.role]}</td><td>{r.enabled ? '启用' : '停用'}</td><td>{r.authVersion}</td><td><div className="row-actions">{operator.role === 'operator' && r.id !== operator.id && <button onClick={() => setOperation({ action: 'access', target: r })}>调整权限 · {r.username}</button>}{(operator.role === 'operator' || r.id === operator.id) && <button onClick={() => setOperation({ action: 'password', target: r })}>{r.id === operator.id ? '修改我的密码' : `重置密码 · ${r.username}`}</button>}</div></td></tr>)}{!data.items.length && <tr><td colSpan={5}>没有符合条件的管理员</td></tr>}</tbody></table></div>
      <footer><span>共 {data.total} 条 · 第 {page} 页</span><div><button disabled={page <= 1} onClick={() => setPage(v => v - 1)}>上一页</button><button disabled={page * data.pageSize >= data.total} onClick={() => setPage(v => v + 1)}>下一页</button></div></footer>
    </>}
    {operation && <AdministratorEditor client={client} operator={operator} operation={operation} onClose={() => setOperation(null)} onSaved={result => {
      if (operation.action === 'password' && result.id === operator.id) { onPasswordChanged(); return; }
      setOperation(null); setNotice(`${result.username} 的操作已提交。状态与历史会话以服务器结果为准。`); setReload(v => v + 1);
    }} />}
  </section>;
}

function AdministratorEditor({ client, operator, operation, onClose, onSaved }: { client: PlatformClient; operator: Operator; operation: Operation; onClose: () => void; onSaved: (r: Administrator) => void }) {
  const ref = useRef<HTMLDialogElement>(null), live = useRef(true), running = useRef(false);
  const [busy, setBusy] = useState(false), [error, setError] = useState(''), [frozen, setFrozen] = useState<Write | null>(null);
  const self = operation.target?.id === operator.id;
  const title = operation.action === 'create' ? '新增平台管理员' : operation.action === 'access' ? `调整权限 · ${operation.target!.username}` : self ? '修改我的密码' : `重置密码 · ${operation.target!.username}`;
  useEffect(() => { live.current = true; ref.current?.showModal(); return () => { live.current = false; }; }, []);
  function accept(r: unknown) {
    if (!valid(r) || (operation.target && r.id !== operation.target.id)) throw new Error('响应无法确认，请查询原请求结果');
    if (live.current) onSaved(r);
  }
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); if (running.current) return;
    const values = new FormData(e.currentTarget);
    if (!frozen && (String(values.get('reason') || '').trim().length < 2 || values.get('confirmed') !== 'yes')) { setError('请填写操作理由并确认权限影响'); return; }
    const body: Write = frozen ?? { action: operation.action, targetId: operation.target?.id ?? '', username: String(values.get('username') || '').trim(), role: String(values.get('role') || ''), enabled: operation.action === 'password' ? null : operation.action === 'create' || values.get('enabled') === 'yes', expectedVersion: operation.target?.authVersion ?? 0, requestId: crypto.randomUUID(), reason: String(values.get('reason') || '').trim(), confirmed: true, password: String(values.get('password') || ''), currentPassword: String(values.get('currentPassword') || '') };
    const passwordLength = Array.from(body.password).length;
    if (body.action !== 'access' && (passwordLength < 6 || passwordLength > 32 || new TextEncoder().encode(body.password).length > 72)) { setError('新密码需要 6–32 个字符且不超过 72 个 UTF-8 字节'); return; }
    if (body.action === 'create' && !/^[a-zA-Z0-9][a-zA-Z0-9_-]{2,79}$/.test(body.username)) { setError('账号需 3–80 位字母、数字、短横线或下划线，以字母或数字开头'); return; }
    setFrozen(body); running.current = true; setBusy(true); setError('');
    try { accept(await client.request('/administrators/operations', 'POST', body)); }
    catch (e) { if (live.current) setError(e instanceof PlatformError && e.code === 'ADMIN_ACCOUNT_CHANGED' ? '管理员状态已变化，请核对列表后重新操作；不会覆盖新设置。' : message(e)); }
    finally { running.current = false; if (live.current) setBusy(false); }
  }
  async function check() {
    if (!frozen || running.current) return; running.current = true; setBusy(true); setError('');
    try { accept(await client.request(`/administrators/operations/${frozen.requestId}`)); }
    catch (e) { if (live.current) setError(e instanceof PlatformError && e.code === 'ADMIN_OPERATION_NOT_FOUND' ? '尚未查到提交结果，可按原请求重试；不要更换请求号。' : message(e)); }
    finally { running.current = false; if (live.current) setBusy(false); }
  }
  const locked = !!frozen || busy;
  return <dialog ref={ref} aria-label={title} onCancel={e => { e.preventDefault(); if (!busy) onClose(); }}><form onSubmit={submit}><h2>{title}</h2>
    <p>{operation.action === 'create' ? '运营管理员可管理全平台企业、账号与更新策略；只读管理员只能查看。' : '更改生效后，目标账号的全部旧登录会话将失效。'}{self && ' 修改成功或提交结果不明时，请重新登录核对；不要把超时理解为失败。'}</p>
    {operation.action === 'create' && <label>新管理员账号<input name="username" autoComplete="off" required maxLength={80} disabled={locked} /></label>}
    {operation.action !== 'password' && <label>管理角色<select name="role" defaultValue={operation.target?.role ?? 'reader'} disabled={locked}><option value="reader">只读管理员</option><option value="operator">运营管理员</option></select></label>}
    {operation.action === 'access' && <label className="confirmation"><input name="enabled" type="checkbox" value="yes" defaultChecked={operation.target?.enabled} disabled={locked} />启用管理员账号</label>}
    {self && <label>当前密码<input name="currentPassword" type="password" autoComplete="current-password" required maxLength={72} disabled={locked} /></label>}
    {operation.action !== 'access' && <label>新密码<input name="password" type="password" autoComplete="new-password" required maxLength={32} disabled={locked} /></label>}
    <label>操作理由<textarea name="reason" required minLength={2} maxLength={300} disabled={locked} /></label>
    <label className="confirmation"><input name="confirmed" value="yes" type="checkbox" required disabled={locked} />我已确认目标账号、角色及旧会话失效的影响</label>
    {error && <p role="alert" className="error">{error}</p>}
    {frozen && <p>请求号：<code>{frozen.requestId}</code>。关闭不撤销已提交操作。密码仅暂留当前弹窗内存，结果不明时请先查询；不写入浏览器缓存。</p>}
    <div className="dialog-actions"><button type="button" onClick={onClose} disabled={busy}>关闭</button>{frozen && <button type="button" disabled={busy} onClick={() => void check()}>查询提交结果</button>}<button className="primary" disabled={busy}>{busy ? '正在处理…' : frozen ? '按原请求重试' : '确认管理员操作'}</button></div>
  </form></dialog>;
}
