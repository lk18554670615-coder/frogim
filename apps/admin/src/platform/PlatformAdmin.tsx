import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { PlatformClient, PlatformError, taskErrors, type Operator, type Page, type Tenant, type Account, type EnterpriseCode, type IdentityJob, type AccessJob, type RealmJob, type Audit } from './api';
import { PlatformClientVersions } from './ClientVersions';
import { PlatformAdministrators } from './Administrators';
import { PlatformServers } from './Servers';
import { PlatformDeployments } from './Deployments';
import { PlatformBackups } from './Backups';
import { PlatformBackupSchedules } from './BackupSchedules';
import { TenantDetail } from './TenantDetail';
import { PlatformOverview } from './Overview';
import { PlatformChrome } from './PlatformChrome';
import { pageGroup, pageLabel, readRoute, routePath, type Tab } from './routes';

type Row = Tenant | Account | EnterpriseCode | IdentityJob | AccessJob | RealmJob | Audit;
type Action = { title: string; explanation: string; path: string; method?: string; durable?: boolean; taskTab?: 'access-jobs' | 'realm-jobs'; values: Record<string, unknown>; fields?: { key: string; label: string; optional?: boolean }[] };
const states: Record<string, string> = { active: '已激活', provisioning: '开通中', suspended: '已停用', suspending: '停用确认中', resuming: '恢复确认中', transferring: '调换中', blocked: '已封禁', deleted: '已注销', pending: '处理中', waiting: '等待前置任务', applying: '企业确认中', enabled: '启用', disabled: '停用', completed: '已完成', revoke_source: '源企业撤权', prepare_target: '准备目标身份', activate: '激活归属' };
const text = (error: unknown) => error instanceof Error && error.name !== 'AbortError' ? error.message : '请求已取消或超时，请重试';
const date = (value: string) => new Date(value).toLocaleString('zh-CN', { hour12: false });

export function PlatformAdmin({ client: injected }: { client?: PlatformClient }) {
  const client = useMemo(() => injected ?? new PlatformClient(), [injected]);
  const [operator, setOperator] = useState<Operator | null>(null);
  const [restoring, setRestoring] = useState(true);
  const [restoreError, setRestoreError] = useState('');
  const [restoreAttempt, setRestoreAttempt] = useState(0);
  const [loggingOut, setLoggingOut] = useState(false);
  const [tab, setTab] = useState<Tab>(() => readRoute().tab);
  const [selectedTenant, setSelectedTenant] = useState(() => readRoute().tenant);
  const [relatedTenant, setRelatedTenant] = useState(() => readRoute().tenantId);
  const [archiveFilter, setArchiveFilter] = useState(() => readRoute().archive);
  const [filter, setFilter] = useState(() => ({ q: '', tenantId: readRoute().tenantId, state: readRoute().state, action: '' }));
  const [page, setPage] = useState(() => readRoute().page);
  const [data, setData] = useState<Page<Row> | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [reload, setReload] = useState(0);
  const [action, setAction] = useState<Action | null>(null);
  const [newCode, setNewCode] = useState('');
  const [notice, setNotice] = useState('');
  const [noticeTab, setNoticeTab] = useState<'access-jobs' | 'realm-jobs'>('access-jobs');
  const epoch = useRef(0);
  useEffect(() => {
    const restoreRoute = () => {
      const route = readRoute();
      setData(null); setError('');
      setTab(route.tab); setSelectedTenant(route.tenant); setRelatedTenant(route.tenantId);
      setFilter({ q: '', tenantId: route.tenantId, state: route.state, action: '' });
      setArchiveFilter(route.archive); setPage(route.page); setNewCode('');
    };
    if (window.location.pathname === '/platform/' || window.location.pathname === '/platform') {
      window.history.replaceState(null, '', '/platform/overview');
    }
    window.addEventListener('popstate', restoreRoute);
    return () => window.removeEventListener('popstate', restoreRoute);
  }, []);
  useEffect(() => {
    const route = { tab, tenant: selectedTenant, tenantId: filter.tenantId, state: filter.state, archive: archiveFilter, page };
    const target = routePath(route);
    if (window.location.pathname.startsWith('/platform') && window.location.pathname + window.location.search !== target) {
      window.history.replaceState(null, '', target);
    }
  }, [tab, selectedTenant, filter.tenantId, filter.state, archiveFilter, page]);
  useEffect(() => {
    let active = true;
    let expired = false;
    client.onExpired = () => { expired = true; epoch.current++; setOperator(null); setData(null); setAction(null); setNewCode(''); setNotice(''); };
    client.request<Operator>('/auth/me').then(user => { if (active) { setOperator(user); setRestoreError(''); } })
      .catch(err => { if (active && !expired && !(err instanceof PlatformError && ['INVALID_CREDENTIALS', 'STALE_SESSION'].includes(err.code)) && !(err instanceof Error && err.name === 'AbortError')) setRestoreError('无法确认平台会话，请检查网络后重试。'); })
      .finally(() => { if (active) setRestoring(false); });
    return () => { active = false; epoch.current++; client.onExpired = undefined; client.clear(); };
  }, [client, restoreAttempt]);
  useEffect(() => {
    if (!operator || selectedTenant || tab === 'overview' || tab === 'client-versions' || tab === 'administrators' || tab === 'servers' || tab === 'deployments' || tab === 'backups' || tab === 'maintenance') { setData(null); setLoading(false); setError(''); return; }
    const controller = new AbortController();
    let current = true;
    setLoading(true); setError(''); setData(null);
    const query = new URLSearchParams({ ...filter, ...(tab === 'tenants' ? { archive: archiveFilter } : {}), page: String(page), pageSize: '25' });
    client.request<Page<Row>>(`/${tab}?${query}`, 'GET', undefined, controller.signal)
      .then(value => { if (current) setData(value); })
      .catch(err => { if (current) setError(text(err)); })
      .finally(() => { if (current) setLoading(false); });
    return () => { current = false; controller.abort(); };
  }, [client, operator, tab, selectedTenant, filter, archiveFilter, page, reload]);
  if (restoring) return <main className="platform-login"><p role="status">正在恢复平台会话…</p></main>;
  if (!operator && restoreError) return <main className="platform-login"><section className="platform-state" role="alert"><h1>会话状态暂时无法确认</h1><p>{restoreError}</p><button className="primary" onClick={() => { setRestoring(true); setRestoreError(''); setRestoreAttempt(v => v + 1); }}>重新连接</button></section></main>;
  if (!operator) return <PlatformLogin client={client} onLogin={user => { epoch.current++; setOperator(user); }} />;
  const writable = operator.role === 'operator';
  const changeTab = (next: Tab) => {
    window.history.pushState(null, '', routePath({ tab: next, tenant: '', tenantId: '', state: '', archive: 'active', page: 1 }));
    setData(null); setError('');
    setTab(next); setSelectedTenant(''); setRelatedTenant(''); setArchiveFilter('active');
    setFilter({ q: '', tenantId: '', state: '', action: '' }); setPage(1); setNewCode('');
  };
  const navigateRelated = (next: Tab, tenantId = '') => {
    window.history.pushState(null, '', routePath({ tab: next, tenant: '', tenantId, state: '', archive: 'active', page: 1 }));
    setData(null); setError('');
    setTab(next); setSelectedTenant(''); setRelatedTenant(tenantId); setArchiveFilter('active');
    setFilter({ q: '', tenantId, state: '', action: '' }); setPage(1);
  };
  const openTenant = (id: string) => { window.history.pushState(null, '', routePath({ tab: 'tenants', tenant: id, tenantId: '', state: '', archive: 'active', page: 1 })); setData(null); setError(''); setSelectedTenant(id); };
  const updateFilter = (key: string, value: string) => { setFilter(prev => ({ ...prev, [key]: value })); setPage(1); };
  const ask = (value: Action) => { setError(''); setAction(value); };
  const controls = (children: ReactNode) => writable ? children : <span className="muted">只读</span>;
  const statusOptions = tab === 'tenants' ? ['provisioning', 'active', 'suspending', 'suspended', 'resuming'] : tab === 'accounts' ? ['active', 'provisioning', 'transferring', 'blocked', 'deleted'] : tab === 'codes' ? ['enabled', 'disabled'] : tab === 'realm-jobs' ? ['pending', 'completed'] : tab === 'access-jobs' ? ['waiting', 'applying', 'completed'] : ['pending', 'blocked', 'completed'];
  return <><PlatformChrome tab={tab} operator={operator} loggingOut={loggingOut} onNavigate={changeTab} onLogout={async () => { if (loggingOut) return; setLoggingOut(true); try { await client.logout(); epoch.current++; setOperator(null); setData(null); setSelectedTenant(''); setNewCode(''); setNotice(''); setAction(null); } catch (err) { setError(text(err)); } finally { setLoggingOut(false); } }}>
    <div className="page-top"><div className="breadcrumbs"><button onClick={() => changeTab('overview')}>工作台</button><span aria-hidden="true">/</span>{selectedTenant ? <><button onClick={() => changeTab('tenants')}>企业目录</button><span aria-hidden="true">/</span><span>企业详情</span></> : <><span>{pageGroup(tab)}</span><span aria-hidden="true">/</span><span>{pageLabel(tab)}</span></>}</div><div className="page-header"><div><h1>{selectedTenant ? '企业详情' : pageLabel(tab)}</h1><p>{selectedTenant ? `企业 ID：${selectedTenant} · 查看资料、关联资源与运维操作` : tab === 'overview' ? '平台运行状态与待处理工作' : '查看记录、筛选结果并核对操作影响。'}</p></div><button className="button secondary" disabled={loading} onClick={() => setReload(v => v + 1)}>刷新</button></div></div>
      {tab === 'overview' ? <PlatformOverview client={client} refresh={reload} onNavigate={navigateRelated} /> : selectedTenant && tab === 'tenants' ? <TenantDetail key={selectedTenant} id={selectedTenant} client={client} writable={writable} onClose={() => changeTab('tenants')} onChanged={() => setReload(v => v + 1)} onNavigate={navigateRelated} /> : tab === 'maintenance' ? <PlatformBackupSchedules key={`maintenance-${relatedTenant}`} client={client} writable={writable} refresh={reload} initialTenantId={relatedTenant} /> : tab === 'backups' ? <PlatformBackups key={`backups-${relatedTenant}`} client={client} writable={writable} refresh={reload} initialTenantId={relatedTenant} /> : tab === 'deployments' ? <PlatformDeployments key={`deployments-${relatedTenant}`} client={client} writable={writable} refresh={reload} initialTenantId={relatedTenant} /> : tab === 'servers' ? <PlatformServers key={`servers-${relatedTenant}`} client={client} writable={writable} refresh={reload} initialTenantId={relatedTenant} /> : tab === 'administrators' ? <PlatformAdministrators client={client} operator={operator} refresh={reload} onPasswordChanged={() => { client.clear(); epoch.current++; setOperator(null); setData(null); }} /> : tab === 'client-versions' ? <PlatformClientVersions client={client} writable={writable} refresh={reload} /> : <><section className="filters" aria-label="筛选">
        {(tab === 'accounts' || tab === 'tenants') && <label>{tab === 'accounts' ? '手机号 / 账号 ID' : '企业名称 / ID'}<input value={filter.q} onChange={e => updateFilter('q', e.target.value)} /></label>}
        {(tab === 'access-jobs' || tab === 'realm-jobs' || tab === 'jobs') && <label>{tab === 'jobs' ? '任务 ID / 账号 ID' : '请求 / 任务 ID'}<input value={filter.q} onChange={e => updateFilter('q', e.target.value)} /></label>}
        {tab !== 'tenants' && <label>企业 ID<input value={filter.tenantId} onChange={e => updateFilter('tenantId', e.target.value)} /></label>}
        {tab !== 'audits' ? <label>状态<select value={filter.state} onChange={e => updateFilter('state', e.target.value)}><option value="">全部状态</option>{statusOptions.map(s => <option key={s} value={s}>{tab === 'jobs' && s === 'blocked' ? '待处理' : states[s]}</option>)}</select></label> : <label>审计动作<input value={filter.action} onChange={e => updateFilter('action', e.target.value)} /></label>}
        {tab === 'tenants' && <label>目录<select value={archiveFilter} onChange={e => { setArchiveFilter(e.target.value); setSelectedTenant(''); setPage(1); }}><option value="active">在用企业</option><option value="archived">已归档</option><option value="all">全部企业</option></select></label>}
        {tab === 'tenants' && writable && <button className="primary" onClick={() => ask({ title: '登记企业', explanation: '仅登记待开通企业，不购买服务器、不复制业务数据、不自动激活邀请码。', path: '/tenants', values: {}, fields: [{ key: 'id', label: '企业 ID' }, { key: 'displayName', label: '企业名称' }, { key: 'httpBaseUrl', label: '企业 HTTPS 根地址' }] })}>登记企业</button>}
      </section>
      {newCode && <section className="one-time-code" aria-label="新企业邀请码"><strong>邀请码只显示这一次，请安全保存</strong><code>{newCode}</code><button onClick={() => setNewCode('')}>已保存，隐藏邀请码</button></section>}
      {notice && <p role="status">{notice}<button onClick={() => changeTab(noticeTab)}>{noticeTab === 'realm-jobs' ? '查看企业启停任务' : '查看封禁任务'}</button><button onClick={() => setNotice('')}>关闭提示</button></p>}
      {error && <p role="alert" className="error">{error}<button onClick={() => setReload(v => v + 1)}>重试</button></p>}
      {loading && <p role="status">正在加载…</p>}
      {!loading && data && <><div className="table-scroll"><table><caption className="sr-only">{pageLabel(tab)}</caption><thead><tr>{(tab === 'tenants' ? ['企业', '业务地址', '状态', '版本', '操作'] : tab === 'accounts' ? ['账号', '当前企业', '归属与封禁', '归属版本', '操作'] : tab === 'codes' ? ['邀请码记录', '企业', '状态', '创建时间', '操作'] : tab === 'jobs' ? ['任务', '归属路径', '阶段', '执行情况', '操作'] : tab === 'realm-jobs' ? ['任务 / 企业', '访问代次', '进度', '执行情况', '更新时间'] : tab === 'access-jobs' ? ['任务', '账号 / 企业', '进度', '执行情况', '更新时间'] : ['时间 / 动作', '操作者', '关联记录', '操作理由', '安全元数据']).map(label => <th key={label} scope="col">{label}</th>)}</tr></thead><tbody>
        {data.items.map(row => {
          if (tab === 'tenants') { const t = row as Tenant; return <tr key={t.id}><td><strong>{t.displayName}</strong><code>{t.id}</code>{t.isDefault && <small>默认企业</small>}</td><td>{t.httpBaseUrl}</td><td><span className={'badge ' + (t.archivedAt ? 'badge-neutral' : t.status === 'active' ? 'badge-success' : t.status === 'provisioning' ? 'badge-info' : 'badge-warning')}>{t.archivedAt ? '已归档' : states[t.status]}</span></td><td>配置 {t.configVersion}<small>访问代次 {t.accessVersion}</small></td><td><div className="row-actions"><button onClick={() => openTenant(t.id)}>详情</button>{!t.archivedAt && controls(<div className="row-actions">{t.status === 'provisioning' && <button onClick={() => ask({ title: '检查并激活企业', explanation: '平台将通过预配置的双向 TLS 控制通道核对企业身份、业务地址、数据库绑定及媒体、IM、通话依赖。任一失败均不激活；此操作不购买或部署服务器。', path: `/tenants/${encodeURIComponent(t.id)}/activate`, values: { expectedConfigVersion: t.configVersion } })}>检查并激活</button>}{['active', 'suspended'].includes(t.status) && <button disabled={!Number.isSafeInteger(t.accessVersion) || t.accessVersion < 1} onClick={() => ask({ title: t.status === 'active' ? '停用企业' : '恢复企业', explanation: t.status === 'active' ? '受理后立即关闭平台登录，分批撤销该企业的业务、IM 和通话会话。企业确认后才算停用完成。不删除账号、群组、历史消息或文件；已分享的固定媒体链接继续有效。' : '重新检查企业服务并清理旧连接，确认完成后才能重新登录。不恢复旧凭据，不解除账号全局或企业封禁，不改变好友和历史记录。', path: `/tenants/${encodeURIComponent(t.id)}/access`, durable: true, taskTab: 'realm-jobs', values: { requestId: crypto.randomUUID(), expectedAccessVersion: t.accessVersion, enabled: t.status === 'suspended' } })}>{t.status === 'active' ? '停用企业' : '恢复企业'}</button>}<button disabled={t.status !== 'active'} onClick={() => ask({ title: '创建企业邀请码', explanation: `为 ${t.displayName} 创建首次注册使用的企业码。已注册账号不会因此调换企业。`, path: `/tenants/${encodeURIComponent(t.id)}/codes`, values: {} })}>创建邀请码</button></div>)}</div></td></tr>; }
          if (tab === 'accounts') { const a = row as Account; return <tr key={a.id}><td><strong>{a.phone}</strong><code>{a.id}</code></td><td>{a.tenantId}<small>本地身份：{a.localUserId}</small></td><td><span className={'badge ' + (a.state === 'active' ? 'badge-success' : a.state === 'blocked' ? 'badge-danger' : 'badge-warning')}>{states[a.state]}</span><small>{a.globallyBlocked ? '禁止平台登录' : '未全局封禁'}</small>{a.accessPending && <small>封禁 / 解封任务待确认</small>}</td><td>{a.assignmentVersion}</td><td>{controls(<div className="row-actions"><button disabled={a.state !== 'active' || a.globallyBlocked} onClick={() => ask({ title: '调换账号企业', explanation: '操作期间暂停登录。先确认源企业完成撤权，再激活目标企业的新身份；不迁移好友、群组、消息或业务权限。群主必须先转让群。无法通过此操作跳过撤权。', path: `/accounts/${encodeURIComponent(a.id)}/transfer`, values: {}, fields: [{ key: 'targetTenantId', label: '目标企业 ID' }] })}>调换企业</button><button disabled={a.accessPending || a.state === 'deleted' || (a.globallyBlocked && a.state !== 'blocked')} onClick={() => ask({ title: a.globallyBlocked ? '解除全局封禁' : '全局封禁账号', explanation: a.globallyBlocked ? '等待企业确认后恢复平台登录，仍需重新登录；保留原企业身份、聊天记录和企业自己的封禁，不恢复旧会话。' : '受理后立即禁止平台登录。企业完成业务、IM 与通话会话撤权后任务才算完成；若有开户、调换或改密任务，将先等待其结束。不删除聊天记录。', path: `/accounts/${encodeURIComponent(a.id)}/access`, durable: true, values: { requestId: crypto.randomUUID(), expectedAuthVersion: a.authVersion, blocked: !a.globallyBlocked } })}>{a.globallyBlocked ? '解除全局封禁' : '全局封禁'}</button></div>)}</td></tr>; }
          if (tab === 'codes') { const c = row as EnterpriseCode; return <tr key={c.id}><td><strong>{c.suffix ? `•••• ${c.suffix}` : '历史代码（不回显）'}</strong><code>{c.id}</code></td><td>{c.tenantId}</td><td><span className={'badge ' + (c.enabled ? 'badge-success' : 'badge-neutral')}>{c.enabled ? '启用' : '停用'}</span></td><td>{date(c.createdAt)}</td><td>{controls(<button onClick={() => ask({ title: c.enabled ? '停用企业邀请码' : '启用企业邀请码', explanation: '仅影响今后的首次注册，不改变已绑定用户的归属；企业未激活或已停用时仍不可注册。', path: `/codes/${encodeURIComponent(c.id)}/status`, method: 'PUT', values: { enabled: !c.enabled } })}>{c.enabled ? '停用' : '启用'}</button>)}</td></tr>; }
          if (tab === 'realm-jobs') { const j = row as RealmJob; return <tr key={j.jobId}><td><strong>{j.enabled ? '恢复企业' : '停用企业'} · {j.tenantId}</strong><code>{j.jobId}</code><small>请求号：{j.requestId}</small></td><td>{j.accessVersion}</td><td><span className={'badge ' + (j.status === 'completed' ? 'badge-success' : 'badge-warning')}>{states[j.status]}</span>{j.status !== 'completed' && <small>{j.remaining > 0 ? `待处理身份 ${j.remaining} 个` : '企业确认前不开放平台登录'}</small>}</td><td>已尝试 {j.attempts} 次<small>{taskErrors[j.errorCode ?? ''] || (j.errorCode ? '操作尚未确认，请检查企业服务' : j.status === 'completed' ? '企业已确认；旧凭据不会恢复' : '分批处理，尚未完成')}</small></td><td>{date(j.updatedAt)}</td></tr>; }
          if (tab === 'access-jobs') { const j = row as AccessJob; return <tr key={j.jobId}><td><strong>{j.blocked ? '全局封禁' : '解除全局封禁'}</strong><code>{j.jobId}</code><small>请求号：{j.requestId}</small></td><td>{j.accountId}<small>{j.tenantId}</small></td><td><span className={'badge ' + (j.status === 'completed' ? 'badge-success' : 'badge-warning')}>{states[j.status]}</span></td><td>已尝试 {j.attempts} 次<small>{taskErrors[j.errorCode ?? ''] || (j.errorCode ? '操作尚未确认，请检查企业服务' : j.status === 'completed' ? '企业已确认；不会恢复旧会话' : '禁止平台登录，等待企业确认')}</small></td><td>{date(j.updatedAt)}</td></tr>; }
          if (tab === 'jobs') { const j = row as IdentityJob; return <tr key={j.id}><td><strong>{j.kind === 'transfer' ? '企业调换' : '账号开通'}</strong><code>{j.id}</code><small>{j.accountId}</small></td><td>{j.sourceTenantId || '新注册'} → {j.targetTenantId}<small>归属版本 {j.assignmentVersion}</small></td><td><span className={'badge ' + (j.blocked ? 'badge-danger' : j.step === 'completed' ? 'badge-success' : 'badge-warning')}>{states[j.step]}</span>{j.blocked && <strong className="error">待处理</strong>}</td><td>{j.leased ? '正在执行' : `已尝试 ${j.attempts} 次`}<small>{taskErrors[j.errorCode] || (j.errorCode ? '操作未确认，请检查企业服务' : date(j.updatedAt))}</small></td><td>{controls(j.step !== 'completed' && <div className="row-actions"><button disabled={j.leased} onClick={() => ask({ title: '重试身份任务', explanation: '从当前阶段按原操作号重试，不重建账号，不跳过源企业撤权。请先处理已显示的阻断原因。', path: `/jobs/${encodeURIComponent(j.id)}/retry`, values: { expectedStep: j.step } })}>重试</button>{j.blocked && j.kind === 'registration' && j.step === 'prepare_target' && j.errorCode.startsWith('INVITE_') && <button disabled={j.leased} onClick={() => ask({ title: '修正注册邀请码', explanation: '仅修正尚未成功创建企业身份的个人邀请码，不更改企业归属或已有邀请关系。', path: `/jobs/${encodeURIComponent(j.id)}/registration-input`, method: 'PUT', values: { expectedStep: j.step }, fields: [{ key: 'personalInviteCode', label: '个人邀请码', optional: true }] })}>修正邀请码</button>}</div>)}</td></tr>; }
          const log = row as Audit; return <tr key={log.id}><td>{date(log.createdAt)}<code>{log.action}</code></td><td>{log.actorId}</td><td>{log.tenantId || '—'}<small>{log.accountId}</small><small>{log.jobId}</small></td><td>{log.reason || '—'}</td><td><code>{JSON.stringify(log.metadata)}</code></td></tr>;
        })}
        {data.items.length === 0 && <tr><td colSpan={5} className="empty">没有符合条件的记录</td></tr>}
      </tbody></table></div><footer><span>共 {data.total} 条 · 第 {page} 页</span><div><button disabled={page <= 1} onClick={() => setPage(p => p - 1)}>上一页</button><button disabled={page * data.pageSize >= data.total} onClick={() => setPage(p => p + 1)}>下一页</button></div></footer></>}
    </>}
    </PlatformChrome>
    {action && <Confirmation key={action.path} action={action} onClose={() => setAction(null)} onSave={async body => {
      const current = epoch.current;
      const result = await client.request<{ code?: string; jobId?: string; status?: string }>(action.path, action.method ?? 'POST', body);
      if (current !== epoch.current) return;
      if (action.durable && (typeof result.jobId !== 'string' || !/^[a-zA-Z0-9_-]{1,128}$/.test(result.jobId) || !(action.taskTab === 'realm-jobs' ? ['pending', 'completed'] : ['waiting', 'applying', 'completed']).includes(result.status ?? ''))) throw new Error(action.taskTab === 'realm-jobs' ? '返回结果无法确认，请使用原请求重试或查询企业启停任务' : '返回结果无法确认，请使用原请求重试或查询封禁任务');
      if (result.code) setNewCode(result.code);
      if (action.durable) setNoticeTab(action.taskTab ?? 'access-jobs');
      if (action.durable) setNotice(result.status === 'completed' ? `任务 ${result.jobId} 已完成，企业已确认。` : `任务 ${result.jobId} 已受理，企业会话处理尚未确认。请查看任务进度，不要重复创建操作。`);
      setAction(null); setReload(v => v + 1);
    }} />}
  </>;
}

function PlatformLogin({ client, onLogin }: { client: PlatformClient; onLogin: (user: Operator) => void }) {
  const [busy, setBusy] = useState(false), [error, setError] = useState('');
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (busy) return;
    const form = event.currentTarget; const values = new FormData(form);
    setBusy(true); setError('');
    try { const user = await client.login(String(values.get('username')), String(values.get('password'))); form.reset(); if (alive.current) onLogin(user); }
    catch (err) { if (alive.current) setError(text(err)); }
    finally { if (alive.current) setBusy(false); }
  }
  return <main className="platform-login"><form onSubmit={submit}><img src="/qingwaguagua-mark.png" alt="青蛙呱呱" /><h1>平台运营登录</h1><p>使用独立平台管理员账号。企业管理员账号不能登录这里。</p><label>平台账号<input name="username" autoComplete="username" required maxLength={80} /></label><label>密码<input name="password" type="password" autoComplete="current-password" required maxLength={72} /></label>{error && <p role="alert" className="error">{error}</p>}<button className="primary" disabled={busy}>{busy ? '登录中…' : '登录平台'}</button></form></main>;
}

function Confirmation({ action, onClose, onSave }: { action: Action; onClose: () => void; onSave: (body: unknown) => Promise<void> }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const targetParts = action.path.split('/').filter(Boolean);
  const targetKind: Record<string, string> = { tenants: '企业', accounts: '账号', codes: '邀请码', jobs: '身份任务' };
  const target = targetParts.length > 1 ? `${targetKind[targetParts[0]] ?? '记录'} · ${decodeURIComponent(targetParts[1])}` : '新登记记录';
  const [busy, setBusy] = useState(false), [error, setError] = useState('');
  const submitting = useRef(false);
  const frozenBody = useRef<Record<string, unknown> | null>(null);
  useEffect(() => { dialog.current?.showModal(); }, []);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); if (submitting.current) return;
    const fields = new FormData(e.currentTarget); const reason = String(fields.get('reason') || '').trim();
    if (!frozenBody.current && (reason.length < 2 || fields.get('confirmed') !== 'yes')) { setError('请填写至少 2 个字的操作理由，并确认影响范围'); return; }
    submitting.current = true; setBusy(true); setError('');
    const body = frozenBody.current ?? { ...action.values, ...Object.fromEntries((action.fields ?? []).map(field => [field.key, String(fields.get(field.key) || '').trim()])), reason, confirmed: true };
    if (action.durable) frozenBody.current = body;
    try { await onSave(body); }
    catch (err) { setError(text(err)); }
    finally { submitting.current = false; setBusy(false); }
  }
  const locked = busy || frozenBody.current !== null;
  return <dialog ref={dialog} aria-labelledby="confirm-title" onCancel={e => { e.preventDefault(); if (!busy) onClose(); }}><form onSubmit={submit}><h2 id="confirm-title">{action.title}</h2><div className="confirmation-scope"><strong>操作对象</strong><code>{target}</code><strong>影响范围</strong><p>{action.explanation}</p></div>{action.fields?.map(field => <label key={field.key}>{field.label}<input name={field.key} required={!field.optional} maxLength={field.key === 'httpBaseUrl' ? 300 : 80} disabled={locked} /></label>)}<label>操作理由<textarea name="reason" required minLength={2} maxLength={300} disabled={locked} /></label><label className="confirmation"><input name="confirmed" type="checkbox" value="yes" required disabled={locked} />我已确认上述操作与影响范围</label>{error && <p role="alert" className="error">{error}</p>}{action.durable && frozenBody.current && <p>请求内容已锁定，重试复用原请求号。关闭窗口不取消服务端任务；结果不明确时请先查看{action.taskTab === 'realm-jobs' ? '企业启停任务' : '封禁任务'}，不要创建新的相反操作。</p>}<div className="dialog-actions"><button type="button" onClick={onClose} disabled={busy}>{frozenBody.current ? '关闭窗口' : '取消'}</button><button className="primary" disabled={busy}>{busy ? '处理中…' : '确认操作'}</button></div></form></dialog>;
}
