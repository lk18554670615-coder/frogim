import { useEffect, useRef, useState, type ReactNode } from 'react';
import { PlatformDialog } from './PlatformDialog';
import './platform-users.css';

export type UserProfile = { name?: string; handle?: string; gender?: string; signature?: string; avatarMediaId?: string; avatarUrl?: string; allowSearchByHandle?: boolean; allowSearchByPhone?: boolean; banned?: boolean; createdAt?: string; deletedAt?: string };
export type ProfileSync = { profileVersion: number; syncedAt?: string | null; syncError?: string };
export type PlatformUser = { id: string; phone: string; tenantId: string; assignmentVersion: number; banned: boolean; pendingOperation?: string; profile: UserProfile; registeredAt?: string; membershipCount?: number; currentMembership?: ProfileSync | null };
type Membership = ProfileSync & { tenantId: string; profile: UserProfile; current: boolean };
type Details = { user: PlatformUser; memberships: Membership[] };
type Tenant = { id: string; name: string; enabled: boolean };
type Request = (path: string, body?: unknown, method?: string, signal?: AbortSignal) => Promise<any>;
type Filters = { tenant: string; tenantScope: 'current' | 'membership'; banned: string; pending: string; page: number };

function readFilters(search: string): Filters {
  const p = new URLSearchParams(search);
  const flag = (key: string) => ['yes', 'no'].includes(p.get(key) || '') ? p.get(key)! : '';
  const page = Number(p.get('page'));
  return { tenant: p.get('tenant') || '', tenantScope: p.get('tenantScope') === 'membership' ? 'membership' : 'current', banned: flag('banned'), pending: flag('pending'), page: Number.isInteger(page) && page > 0 && page <= 100000 ? page : 1 };
}
function safeSearch(filters: Filters, profileTenant?: string) {
  const p = new URLSearchParams();
  if (filters.tenant) p.set('tenant', filters.tenant);
  if (filters.tenantScope !== 'current') p.set('tenantScope', filters.tenantScope);
  if (filters.banned) p.set('banned', filters.banned);
  if (filters.pending) p.set('pending', filters.pending);
  if (filters.page > 1) p.set('page', String(filters.page));
  if (profileTenant) p.set('profileTenant', profileTenant);
  return p.size ? '?' + p.toString() : '';
}
export function userDate(value?: string | null) {
  if (!value || value.startsWith('0001-')) return '未设置';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '未设置' : date.toLocaleString('zh-CN', { hour12: false });
}
function hasSnapshot(sync?: ProfileSync | null) { return !!sync && sync.profileVersion > 0 && !!sync.syncedAt; }
function SyncSummary({ sync }: { sync?: ProfileSync | null }) {
  return <div className="pu-sync"><span className={'pu-badge ' + (sync?.syncError ? 'pu-warning' : hasSnapshot(sync) ? 'pu-success' : '')}>{sync?.syncError ? '同步异常' : hasSnapshot(sync) ? '已同步' : '尚未同步'}</span><small>{hasSnapshot(sync) ? userDate(sync?.syncedAt) : '完整资料待同步'}</small></div>;
}
function Avatar({ userId, tenantId, profile }: { userId: string; tenantId: string; profile: UserProfile }) {
  const [failed, setFailed] = useState(false);
  const src = profile.avatarMediaId ? `/platform/admin/users/${encodeURIComponent(userId)}/avatar/${encodeURIComponent(tenantId)}` : '';
  useEffect(() => setFailed(false), [src, profile.avatarMediaId]);
  return src && !failed ? <img key={profile.avatarMediaId} className="pu-avatar" alt="用户头像" loading="lazy" src={src} onError={() => setFailed(true)} /> : <span className="pu-avatar pu-avatar-placeholder" aria-label="默认头像">{(profile.name || '用户').slice(0, 1)}</span>;
}
function CopyId({ id }: { id: string }) {
  const [message, setMessage] = useState('');
  return <span className="pu-id"><code title={id}>{id.length > 18 ? id.slice(0, 8) + '…' + id.slice(-6) : id}</code><button type="button" aria-label="复制用户 ID" title="复制完整用户 ID" onClick={() => { if (!navigator.clipboard?.writeText) { setMessage('复制不可用'); return; } void navigator.clipboard.writeText(id).then(() => setMessage('已复制')).catch(() => setMessage('复制失败')); }}>复制</button>{message && <small role="status">{message}</small>}</span>;
}
function AccountStatus({ user, tenant }: { user: PlatformUser; tenant?: Tenant }) {
  return <div className="pu-statuses"><span className={'pu-badge ' + (user.banned ? 'pu-danger' : 'pu-success')}>{user.banned ? '全局封禁' : '未全局封禁'}</span>{user.pendingOperation && <span className="pu-badge pu-warning">操作未完成</span>}{tenant?.enabled === false && <span className="pu-badge pu-warning">企业停止登录</span>}{hasSnapshot(user.currentMembership) ? <>{user.profile.deletedAt && <span className="pu-badge pu-danger">当前企业已注销</span>}{user.profile.banned && <span className="pu-badge pu-danger">当前企业封禁</span>}</> : <span className="pu-badge">企业状态待同步</span>}</div>;
}

export function PlatformUsers({ page, search, tenants, canWrite, request, refreshVersion, navigate, renderActions, onCreate, hideDetail = false }: { page: string; search: string; tenants: Tenant[]; canWrite: boolean; request: Request; refreshVersion: number; navigate: (path: string, search: string) => void; renderActions: (user: PlatformUser) => ReactNode; onCreate: () => void; hideDetail?: boolean }) {
  const filters = readFilters(search);
  const [query, setQuery] = useState(''), [debounced, setDebounced] = useState('');
  const [items, setItems] = useState<PlatformUser[]>([]), [hasMore, setHasMore] = useState(false), [loading, setLoading] = useState(true), [error, setError] = useState(''), [resultKey, setResultKey] = useState('');
  const [detail, setDetail] = useState<Details | null>(null), [detailLoading, setDetailLoading] = useState(false), [detailError, setDetailError] = useState('');
  const listGeneration = useRef(0), detailGeneration = useRef(0);
  const userId = page.startsWith('users/') ? decodeURIComponent(page.slice(6)) : '';
  const profileTenant = new URLSearchParams(search).get('profileTenant');
  const tenantName = (id: string) => tenants.find(t => t.id === id)?.name || id;
  const baseSearch = safeSearch(filters);
  const criteriaKey = JSON.stringify({ query, ...filters, refreshVersion });
  const listLoading = loading || resultKey !== criteriaKey;
  const updateFilters = (change: Partial<Filters>) => navigate(page, safeSearch({ ...filters, page: 1, ...change }, userId ? profileTenant || undefined : undefined));
  useEffect(() => { const timer = setTimeout(() => setDebounced(query), 250); return () => clearTimeout(timer); }, [query]);
  useEffect(() => {
    const generation = ++listGeneration.current, abort = new AbortController();
    setLoading(true); setError('');
    if (query !== debounced) return () => { abort.abort(); ++listGeneration.current; };
    const body = { query: debounced, tenant: filters.tenant, tenantScope: filters.tenantScope, page: filters.page, ...(filters.banned ? { banned: filters.banned === 'yes' } : {}), ...(filters.pending ? { pending: filters.pending === 'yes' } : {}) };
    void request('/users/query', body, 'POST', abort.signal).then(data => {
      if (generation !== listGeneration.current) return;
      setItems(data.items); setHasMore(data.hasMore); setResultKey(criteriaKey);
    }).catch(e => { if (generation === listGeneration.current && !abort.signal.aborted) { setItems([]); setResultKey(criteriaKey); setError(e instanceof Error ? e.message : String(e)); } }).finally(() => { if (generation === listGeneration.current) setLoading(false); });
    return () => { abort.abort(); ++listGeneration.current; };
  }, [query, debounced, filters.tenant, filters.tenantScope, filters.banned, filters.pending, filters.page, refreshVersion, request]);
  useEffect(() => {
    const generation = ++detailGeneration.current, abort = new AbortController();
    setDetail(null); setDetailError(''); setDetailLoading(!!userId);
    if (!userId) return;
    void request('/users/' + encodeURIComponent(userId), undefined, 'GET', abort.signal).then(data => { if (generation !== detailGeneration.current) return; if (data.user?.id !== userId) throw new Error('用户资料与请求对象不一致'); setDetail(data); }).catch(e => { if (generation === detailGeneration.current && !abort.signal.aborted) setDetailError(e instanceof Error ? e.message : String(e)); }).finally(() => { if (generation === detailGeneration.current) setDetailLoading(false); });
    return () => { abort.abort(); ++detailGeneration.current; };
  }, [userId, refreshVersion, request]);
  const selected = detail?.memberships.find(m => m.tenantId === (profileTenant || detail.user.tenantId));
  const synced = hasSnapshot(selected);
  const field = (value: unknown) => !synced ? '待同步' : value === undefined || value === null || value === '' ? '未设置' : typeof value === 'boolean' ? value ? '开启' : '关闭' : String(value);
  return <>
    <section className="lp-panel pu-panel">
      <div className="lp-toolbar"><div><h2>用户账号</h2><p>展示当前企业资料；其他企业账号与历史资料可在详情中查看。</p></div><button disabled={!canWrite} onClick={onCreate} className="lp-primary">创建用户</button></div>
      <div className="pu-filters"><label className="pu-search">搜索用户<input aria-label="搜索用户" value={query} maxLength={120} placeholder="手机号、用户 ID、当前企业昵称或呱呱号" onChange={e => { setQuery(e.target.value); if (filters.page !== 1) updateFilters({ page: 1 }); }} /></label>
        <label>企业<select aria-label="筛选企业" value={filters.tenant} onChange={e => updateFilters({ tenant: e.target.value })}><option value="">全部企业</option>{tenants.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}</select></label>
        <label>企业范围<select aria-label="企业筛选范围" value={filters.tenantScope} onChange={e => updateFilters({ tenantScope: e.target.value as Filters['tenantScope'] })}><option value="current">当前属于该企业</option><option value="membership">在该企业保留账号</option></select></label>
        <label>全局封禁<select aria-label="全局封禁筛选" value={filters.banned} onChange={e => updateFilters({ banned: e.target.value })}><option value="">全部</option><option value="yes">已封禁</option><option value="no">未封禁</option></select></label>
        <label>操作状态<select aria-label="操作状态筛选" value={filters.pending} onChange={e => updateFilters({ pending: e.target.value })}><option value="">全部</option><option value="yes">操作未完成</option><option value="no">无未完成操作</option></select></label>
      </div>
      {error ? <div className="lp-error" role="alert">{error}</div> : listLoading ? <div className="lp-empty" role="status">正在加载用户…</div> : !items.length ? <div className="lp-empty">暂无符合条件的用户</div> : <div className="lp-table-scroll"><table className="pu-table"><thead><tr>{['用户', '呱呱号', '当前企业', '企业账号', '账号状态', '资料同步', '操作'].map(label => <th key={label}>{label}</th>)}</tr></thead><tbody>{items.map(user => <tr key={user.id}>
        <td><div className="pu-identity"><Avatar userId={user.id} tenantId={user.tenantId} profile={user.profile} /><div><button className="pu-name" onClick={() => navigate('users/' + encodeURIComponent(user.id), baseSearch)}>{user.profile?.name || '昵称待同步'}</button><small>{user.phone}</small><CopyId id={user.id} /></div></div></td>
        <td>{hasSnapshot(user.currentMembership) ? user.profile.handle || '未设置' : '待同步'}</td><td>{tenantName(user.tenantId)}</td><td><button className="pu-count" onClick={() => navigate('users/' + encodeURIComponent(user.id), baseSearch)}>{user.membershipCount ?? '—'} 家</button></td>
        <td><AccountStatus user={user} tenant={tenants.find(t => t.id === user.tenantId)} /></td><td><SyncSummary sync={user.currentMembership} /></td><td><div className="pu-row-actions"><button onClick={() => navigate('users/' + encodeURIComponent(user.id), baseSearch)}>资料</button>{renderActions(user)}</div></td>
      </tr>)}</tbody></table></div>}
      <div className="lp-pagination"><button disabled={filters.page === 1 || listLoading} onClick={() => updateFilters({ page: filters.page - 1 })}>上一页</button><span>第 {filters.page} 页 · 每页最多 100 个用户</span><button disabled={!hasMore || listLoading} onClick={() => updateFilters({ page: filters.page + 1 })}>下一页</button></div>
    </section>
    {userId && !hideDetail && <PlatformDialog title="用户资料" drawer onClose={() => navigate('users', baseSearch)}>
      {detailLoading ? <div className="lp-empty" role="status">正在加载资料…</div> : detailError ? <div className="lp-error" role="alert">{detailError}</div> : detail && detail.user.id === userId && <div className="pu-details">
        <section className="pu-global"><div className="pu-detail-heading"><Avatar userId={detail.user.id} tenantId={detail.user.tenantId} profile={detail.user.profile} /><div><h3>{detail.user.profile.name || '用户资料待同步'}</h3><p>{detail.user.phone}</p></div></div><h4>全局身份</h4><dl className="pu-fields"><div><dt>用户 ID</dt><dd><CopyId id={detail.user.id} /></dd></div><div><dt>手机号</dt><dd>{detail.user.phone}</dd></div><div><dt>当前企业</dt><dd>{tenantName(detail.user.tenantId)}</dd></div><div><dt>账号注册时间</dt><dd>{userDate(detail.user.registeredAt)}</dd></div><div><dt>全局封禁</dt><dd><span className={'pu-badge ' + (detail.user.banned ? 'pu-danger' : 'pu-success')}>{detail.user.banned ? '已封禁' : '未封禁'}</span></dd></div><div><dt>未完成操作</dt><dd>{detail.user.pendingOperation || '无'}</dd></div></dl><div className="pu-detail-actions">{renderActions(detail.user)}</div></section>
        <section className="pu-enterprises"><h4>企业账号 <small>{detail.memberships.length} 家</small></h4><div role="tablist" aria-label="企业资料" className="pu-tabs">{detail.memberships.map(member => <button key={member.tenantId} role="tab" id={'pu-tab-' + member.tenantId} aria-controls="pu-profile-panel" aria-selected={selected?.tenantId === member.tenantId} onClick={() => navigate(page, safeSearch(filters, member.tenantId))}>{tenantName(member.tenantId)}{member.current && <span className="pu-badge">当前可登录归属</span>}</button>)}</div>
          {!selected ? <div className="lp-empty">{detail.memberships.length ? '该企业不在此用户的账号列表中' : '尚无企业账号资料'}</div> : <div role="tabpanel" id="pu-profile-panel" aria-labelledby={'pu-tab-' + selected.tenantId} className="pu-profile-panel">
            <div className="pu-detail-heading"><Avatar userId={detail.user.id} tenantId={selected.tenantId} profile={selected.profile} /><div><h3>{tenantName(selected.tenantId)}</h3><p>{selected.current ? '当前登录归属' : '账号与历史保留，当前不能登录此企业'}</p></div></div>
            {tenants.find(t => t.id === selected.tenantId)?.enabled === false && <p className="pu-warning-box">该企业已停止登录。</p>}
            {selected.syncError && <p className="pu-warning-box" role="alert">{selected.syncError}。以下展示最后保存的资料快照。</p>}
            {!synced && <p className="pu-warning-box">完整资料尚未同步，不代表资料为空或企业账号正常。</p>}
            <dl className="pu-fields"><div><dt>昵称</dt><dd>{field(selected.profile.name)}</dd></div><div><dt>呱呱号</dt><dd>{field(selected.profile.handle)}</dd></div><div><dt>性别</dt><dd>{field(({ male: '男', female: '女', unspecified: '未设置' } as Record<string, string>)[selected.profile.gender || 'unspecified'])}</dd></div><div><dt>企业封禁</dt><dd>{!synced ? '待同步' : selected.profile.banned ? '已封禁' : '未封禁'}</dd></div><div className="pu-wide"><dt>个性签名</dt><dd>{field(selected.profile.signature)}</dd></div><div><dt>允许呱呱号搜索</dt><dd>{field(selected.profile.allowSearchByHandle)}</dd></div><div><dt>允许手机号搜索</dt><dd>{field(selected.profile.allowSearchByPhone)}</dd></div><div><dt>企业注册时间</dt><dd>{synced ? userDate(selected.profile.createdAt) : '待同步'}</dd></div><div><dt>企业注销时间</dt><dd>{!synced ? '待同步' : selected.profile.deletedAt ? userDate(selected.profile.deletedAt) : '未注销'}</dd></div><div><dt>资料版本</dt><dd>{selected.profileVersion > 0 ? selected.profileVersion : '待同步'}</dd></div><div><dt>最后同步时间</dt><dd>{selected.syncedAt ? userDate(selected.syncedAt) : '尚未同步'}</dd></div></dl><p className="pu-snapshot-note">资料来自企业同步快照，最后同步时间不代表实时状态。</p>
          </div>}
        </section>
      </div>}
    </PlatformDialog>}
  </>;
}
