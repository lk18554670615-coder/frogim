import { Component, ErrorInfo, FormEvent, ReactNode, createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  Activity, AlertTriangle, Bell, BookOpenCheck, Check, CheckCircle2, ChevronLeft, ChevronRight,
  CircleUserRound, Database, FileClock, Flag, Group, HardDrive, HeartPulse, LayoutDashboard, LockKeyhole,
  LogIn, LogOut, Menu, MessageSquareText, MoreHorizontal, PhoneCall, Plus, RefreshCcw, Save, Search,
  Settings, ShieldAlert, ShieldCheck, Trash2, Users, Wifi, X,
} from 'lucide-react';
import { ApiError, getApi, loginAdmin } from './api';
import type {
  AdminApi, AdminRole, AdminSession, AdminSettings, AnnouncementInput, AnnouncementRecord, AuditLog, CallRecord, DashboardData, DataMode, GroupRecord,
  FeedbackRecord, FriendshipRecord, HealthService, MediaRecord, MessageRecord, OnlineRecord, OperationsStatus, PageResult, ReportRecord, ReportResolutionAction, SensitiveWord, StatusTone, UserRecord,
} from './types';

type Permission = 'users.write' | 'groups.write' | 'reports.write' | 'rules.write' | 'announcements.write' | 'settings.write';
type Notice = { id: number; tone: 'success' | 'danger'; message: string };
type ApiContextValue = {
  api: AdminApi;
  mode: DataMode;
  session: AdminSession;
  allowDemo: boolean;
  setMode: (mode: DataMode) => void;
  logout: () => void;
  notify: (message: string, tone?: Notice['tone']) => void;
  can: (permission: Permission) => boolean;
};

const ApiContext = createContext<ApiContextValue | null>(null);
const SESSION_KEY = 'nexachat_admin_session';
const MODE_KEY = 'nexachat_data_mode';
const allowDemoBuild = import.meta.env.DEV || import.meta.env.VITE_ALLOW_DEMO === 'true';
const roleLabels: Record<AdminRole, string> = { platform_admin: '平台管理员', moderator: '内容审核员', support: '客服专员' };
const rolePermissions: Record<AdminRole, Permission[]> = {
  platform_admin: ['users.write', 'groups.write', 'reports.write', 'rules.write', 'announcements.write', 'settings.write'],
  moderator: ['users.write', 'reports.write', 'rules.write'],
  support: [],
};

function useApi() {
  const value = useContext(ApiContext);
  if (!value) throw new Error('ApiContext is missing');
  return value;
}

function errorMessage(cause: unknown) {
  if (cause instanceof ApiError) return cause.requestId ? `${cause.message}（追踪号：${cause.requestId}）` : cause.message;
  return cause instanceof Error ? cause.message : '操作失败，请稍后重试';
}

function usePath() {
  const [path, setPath] = useState(window.location.pathname);
  useEffect(() => {
    const update = () => setPath(window.location.pathname);
    window.addEventListener('popstate', update);
    return () => window.removeEventListener('popstate', update);
  }, []);
  const navigate = (next: string) => {
    if (window.location.pathname !== next) window.history.pushState({}, '', next);
    window.dispatchEvent(new PopStateEvent('popstate'));
  };
  return { path, navigate };
}

function AppLink({ to, currentPath, navigate, className = '', children }: { to: string; currentPath: string; navigate: (to: string) => void; className?: string; children: ReactNode }) {
  return <a href={to} className={`${className} ${currentPath === to ? 'active' : ''}`.trim()} aria-current={currentPath === to ? 'page' : undefined} onClick={(event) => { if (!event.metaKey && !event.ctrlKey) { event.preventDefault(); navigate(to); } }}>{children}</a>;
}

function useDebouncedValue<T>(value: T, delay = 250) {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => { const timer = window.setTimeout(() => setDebounced(value), delay); return () => window.clearTimeout(timer); }, [value, delay]);
  return debounced;
}

function useResource<T>(loader: () => Promise<T>, dependencies: unknown[] = []) {
  const [data, setData] = useState<T>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const request = useRef(0);
  const reload = useCallback(async () => {
    const current = ++request.current;
    setLoading(true);
    setError('');
    try {
      const next = await loader();
      if (current === request.current) setData(next);
    } catch (cause) {
      if (current === request.current) setError(errorMessage(cause));
    } finally {
      if (current === request.current) setLoading(false);
    }
  }, dependencies); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { void reload(); return () => { request.current += 1; }; }, [reload]);
  return { data, loading, error, reload };
}

class AppErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error('admin render failure', error, info.componentStack); }
  render() {
    if (this.state.failed) return <main className="fatal-state" role="alert"><AlertTriangle size={28} /><h1>页面暂时无法显示</h1><p>请刷新页面。如果问题持续，请联系技术支持并附上发生时间。</p><button className="button primary" onClick={() => window.location.reload()}>刷新控制台</button></main>;
    return this.props.children;
  }
}

const statusMap: Record<string, { label: string; tone: StatusTone }> = {
  active: { label: '正常', tone: 'success' }, banned: { label: '已封禁', tone: 'danger' }, risk: { label: '风险关注', tone: 'warning' },
  muted: { label: '全员禁言', tone: 'warning' }, dissolved: { label: '已解散', tone: 'neutral' }, pending: { label: '待审核', tone: 'warning' },
  reviewing: { label: '审核中', tone: 'info' }, resolved: { label: '已处理', tone: 'success' }, rejected: { label: '未违规', tone: 'neutral' },
  healthy: { label: '运行正常', tone: 'success' }, degraded: { label: '性能下降', tone: 'warning' }, down: { label: '服务中断', tone: 'danger' },
  success: { label: '成功', tone: 'success' }, failed: { label: '失败', tone: 'danger' },
  draft: { label: '草稿', tone: 'neutral' }, scheduled: { label: '定时发布', tone: 'info' }, published: { label: '已发布', tone: 'success' }, withdrawn: { label: '已撤回', tone: 'neutral' },
  invited: { label: '呼叫中', tone: 'info' }, accepted: { label: '通话中', tone: 'success' }, cancelled: { label: '已取消', tone: 'neutral' }, ended: { label: '已结束', tone: 'neutral' }, missed: { label: '未接听', tone: 'warning' },
};

function Badge({ value, label }: { value: string; label?: string }) {
  const item = statusMap[value] ?? { label: label ?? value, tone: 'neutral' as const };
  return <span className={`badge badge-${item.tone}`}><span className="status-dot" />{label ?? item.label}</span>;
}

function PageHeader({ title, description, actions }: { title: string; description: string; actions?: ReactNode }) {
  return <header className="page-header"><div><h1>{title}</h1><p>{description}</p></div>{actions && <div className="header-actions">{actions}</div>}</header>;
}

function Skeleton({ rows = 4 }: { rows?: number }) {
  return <div className="skeleton-stack" aria-label="正在加载" aria-busy="true">{Array.from({ length: rows }, (_, index) => <div className="skeleton-row" key={index}><span /><span /><span /></div>)}</div>;
}

function ErrorState({ message, retry }: { message: string; retry: () => void }) {
  return <div className="state-box state-error" role="alert"><AlertTriangle size={22} /><div><strong>暂时无法加载</strong><p>{message}</p></div><button className="button secondary" onClick={retry}><RefreshCcw size={15} />重新加载</button></div>;
}

function EmptyState({ title, detail }: { title: string; detail: string }) {
  return <div className="state-box"><Search size={24} /><div><strong>{title}</strong><p>{detail}</p></div></div>;
}

function ConfirmDialog({ open, title, detail, confirmLabel, danger = false, confirmDisabled = false, onClose, onConfirm, children }: { open: boolean; title: string; detail: string; confirmLabel: string; danger?: boolean; confirmDisabled?: boolean; onClose: () => void; onConfirm: () => Promise<void> | void; children?: ReactNode }) {
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const dialogRef = useRef<HTMLDivElement>(null);
  const previousFocus = useRef<HTMLElement | null>(null);
  useEffect(() => {
    if (!open) return;
    previousFocus.current = document.activeElement as HTMLElement;
    const root = document.getElementById('root');
    if (root) { root.inert = true; root.setAttribute('aria-hidden', 'true'); }
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    window.setTimeout(() => {
      const preferred = dialogRef.current?.querySelector<HTMLElement>('[data-initial-focus]');
      const fallback = dialogRef.current?.querySelector<HTMLElement>('input, textarea, select, button');
      (preferred ?? fallback)?.focus();
    });
    return () => {
      if (root) { root.inert = false; root.removeAttribute('aria-hidden'); }
      document.body.style.overflow = previousOverflow;
      previousFocus.current?.focus();
    };
  }, [open]);
  useEffect(() => { if (open) setError(''); }, [open]);
  if (!open) return null;
  const confirm = async () => {
    setSubmitting(true); setError('');
    try { await onConfirm(); onClose(); } catch (cause) { setError(errorMessage(cause)); } finally { setSubmitting(false); }
  };
  const keyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Escape' && !submitting) { event.preventDefault(); onClose(); return; }
    if (event.key !== 'Tab') return;
    const focusable = [...(dialogRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [href]') ?? [])];
    if (!focusable.length) return;
    const first = focusable[0], last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };
  return createPortal(<div className="modal-backdrop" role="presentation" onMouseDown={(event) => !danger && !submitting && event.target === event.currentTarget && onClose()}>
    <div ref={dialogRef} className="modal" role="dialog" aria-modal="true" aria-labelledby="dialog-title" aria-describedby="dialog-detail" onKeyDown={keyDown}>
      <button className="icon-button modal-close" aria-label="关闭" onClick={onClose} disabled={submitting}><X size={18} /></button>
      <div className={`modal-icon ${danger ? 'danger' : ''}`}>{danger ? <ShieldAlert size={22} /> : <BookOpenCheck size={22} />}</div>
      <h2 id="dialog-title">{title}</h2><p id="dialog-detail">{detail}</p>{children}
      {error && <div className="inline-notice danger" role="alert"><AlertTriangle size={15} />{error}</div>}
      <div className="modal-actions"><button data-initial-focus={danger ? '' : undefined} className="button secondary" onClick={onClose} disabled={submitting}>取消</button><button className={`button ${danger ? 'danger' : 'primary'}`} onClick={() => void confirm()} disabled={submitting || confirmDisabled}>{submitting ? '正在处理…' : confirmLabel}</button></div>
    </div>
  </div>, document.body);
}

function Pagination({ data, onPage }: { data?: PageResult<unknown>; onPage: (page: number) => void }) {
  if (!data || (data.page <= 1 && !data.hasNext && data.total <= data.pageSize)) return null;
  const pages = Math.max(1, Math.ceil(data.total / data.pageSize));
  return <nav className="pagination" aria-label="分页"><span>第 {data.page} / {pages} 页，共 {data.total} 条</span><div><button className="icon-button" aria-label="上一页" disabled={data.page <= 1} onClick={() => onPage(data.page - 1)}><ChevronLeft size={17} /></button><button className="icon-button" aria-label="下一页" disabled={!data.hasNext} onClick={() => onPage(data.page + 1)}><ChevronRight size={17} /></button></div></nav>;
}

const navItems = [
  { to: '/overview', label: '运行概览', icon: LayoutDashboard }, { to: '/users', label: '用户管理', icon: Users },
  { to: '/groups', label: '群组管理', icon: Group }, { to: '/reports', label: '举报审核', icon: Flag },
  { to: '/messages', label: '消息检索', icon: MessageSquareText }, { to: '/media', label: '文件存储', icon: HardDrive },
  { to: '/online', label: '在线状态', icon: Wifi },
  { to: '/relationships', label: '关系与反馈', icon: Users }, { to: '/operations', label: '推送与任务', icon: Activity },
  { to: '/announcements', label: '运营公告', icon: Bell }, { to: '/calls', label: '通话记录', icon: PhoneCall },
  { to: '/sensitive-words', label: '敏感词库', icon: ShieldAlert }, { to: '/health', label: '系统健康', icon: HeartPulse },
  { to: '/audit', label: '审计日志', icon: FileClock }, { to: '/settings', label: '系统设置', icon: Settings },
];

function Shell() {
  const { mode, setMode, allowDemo, session, logout } = useApi();
  const [navOpen, setNavOpen] = useState(false);
  const { path, navigate } = usePath();
  useEffect(() => setNavOpen(false), [path]);
  const pages: Record<string, ReactNode> = { '/overview': <OverviewPage />, '/users': <UsersPage />, '/groups': <GroupsPage />, '/reports': <ReportsPage />, '/messages': <MessagesPage />, '/media': <MediaPage />, '/online': <OnlinePage />, '/relationships': <RelationshipsPage />, '/operations': <OperationsPage />, '/announcements': <AnnouncementsPage />, '/calls': <CallsPage />, '/sensitive-words': <SensitiveWordsPage />, '/health': <HealthPage />, '/audit': <AuditPage />, '/settings': <SettingsPage /> };
  useEffect(() => { if (!pages[path]) { window.history.replaceState({}, '', '/overview'); window.dispatchEvent(new PopStateEvent('popstate')); } }, [path]); // eslint-disable-line react-hooks/exhaustive-deps
  return <div className="app-shell">
    {navOpen && <button aria-label="关闭导航" className="nav-scrim" onClick={() => setNavOpen(false)} />}
    <aside className={`sidebar ${navOpen ? 'open' : ''}`} aria-label="运营控制台导航">
      <div className="brand"><div className="brand-mark"><MessageSquareText size={22} /></div><div><strong>邻里通讯</strong><span>运营控制台</span></div></div>
      <nav aria-label="主导航">{navItems.map((item) => <AppLink key={item.to} to={item.to} currentPath={path} navigate={navigate} className="nav-item"><item.icon size={18} /><span>{item.label}</span></AppLink>)}</nav>
      <div className="sidebar-foot"><div className="admin-avatar">{[...session.displayName][0]}</div><div><strong>{session.displayName}</strong><span>{roleLabels[session.role]}</span></div><button className="icon-button sidebar-logout" aria-label="退出管理后台" title="退出登录" onClick={logout}><LogOut size={17} /></button></div>
    </aside>
    <div className="workspace">
      <div className="topbar">
        <button className="icon-button menu-button" aria-label="打开导航" onClick={() => setNavOpen(true)}><Menu size={20} /></button>
        <div className="topbar-status"><span className={`pulse-dot ${mode === 'demo' ? 'demo' : ''}`} /><span>{mode === 'live' ? '实时接口' : '演示环境，不会修改生产数据'}</span></div>
        <div className="topbar-actions">
          {allowDemo && <label className="mode-switch"><Database size={15} /><span>数据源</span><select aria-label="数据源" value={mode} onChange={(event) => setMode(event.target.value as DataMode)}><option value="demo">演示数据</option><option value="live">实时接口</option></select></label>}
        </div>
      </div>
      <main className="main-content">{pages[path] ?? pages['/overview']}</main>
    </div>
  </div>;
}

function TrendChart({ data }: { data: DashboardData['messageTrend'] }) {
  if (data.length < 2 || Math.max(...data.map((point) => point.count)) <= 0) return <div className="chart-empty"><Activity size={20} /><span>服务端尚未提供分时趋势</span></div>;
  const width = 700, height = 190, padding = 14, max = Math.max(...data.map((point) => point.count));
  const coordinates = data.map((point, index) => ({ x: padding + index * ((width - padding * 2) / (data.length - 1)), y: height - padding - (point.count / max) * (height - padding * 2), point }));
  return <div className="chart-wrap"><svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label="消息量趋势"><line x1="0" y1="160" x2={width} y2="160" className="grid-line" /><line x1="0" y1="110" x2={width} y2="110" className="grid-line" /><line x1="0" y1="60" x2={width} y2="60" className="grid-line" /><polyline points={coordinates.map(({ x, y }) => `${x},${y}`).join(' ')} className="trend-line" />{coordinates.map(({ x, y, point }) => <circle key={point.time} cx={x} cy={y} r="3" className="trend-point"><title>{point.time}，{point.count} 条</title></circle>)}</svg><div className="chart-labels">{data.filter((_, index) => index % 2 === 0).map((point) => <span key={point.time}>{point.time}</span>)}</div></div>;
}

function OverviewPage() {
  const { api, mode } = useApi();
  const state = useResource(() => api.getDashboard(), [api, mode]);
  return <><PageHeader title="运行概览" description="业务运行、内容风险和系统活动。" actions={<button className="button secondary" onClick={() => void state.reload()}><RefreshCcw size={15} />刷新数据</button>} />
    {state.loading ? <Skeleton rows={6} /> : state.error || !state.data ? <ErrorState message={state.error} retry={state.reload} /> : <div className="dashboard">
      <section className="metric-strip" aria-label="核心指标">{state.data.metrics.map((item) => <div className="metric" key={item.label}><div><span>{item.label}</span><Badge value={item.tone} label={item.tone === 'warning' ? '需关注' : '正常'} /></div><strong>{item.value}</strong><p>{item.delta}</p></div>)}</section>
      <section className="dashboard-grid">
        <div className="panel trend-panel"><div className="panel-heading"><div><h2>消息流量</h2><p>服务端可用的最新分时统计</p></div></div><TrendChart data={state.data.messageTrend} /></div>
        <div className="panel mix-panel"><div className="panel-heading"><div><h2>消息构成</h2><p>按会话类型统计</p></div></div>{state.data.channelMix.length ? <><div className="donut" style={{ '--segments': 'conic-gradient(var(--primary) 0 68%, var(--info) 68% 95%, var(--warning) 95% 100%)' } as React.CSSProperties}><div><strong>{state.data.channelMix.reduce((sum, item) => sum + item.value, 0)}%</strong><span>已分类</span></div></div><div className="mix-legend">{state.data.channelMix.map((item) => <div key={item.label}><span style={{ background: item.color }} /><b>{item.label}</b><strong>{item.value}%</strong></div>)}</div></> : <div className="panel-empty">暂无构成数据</div>}</div>
        <div className="panel alert-panel"><div className="panel-heading"><div><h2>需要处理</h2><p>按风险和等待时间排序</p></div></div>{state.data.alerts.length ? state.data.alerts.map((alert) => <div className={`alert-row ${alert.severity}`} key={alert.id}><AlertTriangle size={18} /><div><strong>{alert.title}</strong><p>{alert.detail}</p></div><time>{alert.time}</time></div>) : <div className="panel-empty">当前没有服务端告警</div>}</div>
        <div className="panel activity-panel"><div className="panel-heading"><div><h2>最新操作</h2><p>敏感动作审计记录</p></div></div>{state.data.activity.length ? state.data.activity.map((log) => <div className="activity-row" key={log.id}><div className="activity-icon"><ShieldCheck size={16} /></div><div><strong>{log.action}</strong><p>{log.actor} · {log.target}</p></div><time>{log.createdAt}</time></div>) : <div className="panel-empty">暂无可展示的审计记录</div>}</div>
      </section>
    </div>}
  </>;
}

function Toolbar({ query, setQuery, placeholder, children }: { query: string; setQuery: (value: string) => void; placeholder: string; children?: ReactNode }) {
  return <div className="toolbar"><label className="search-field"><span className="sr-only">{placeholder}</span><Search size={17} /><input aria-label={placeholder} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={placeholder} /></label>{children}</div>;
}

function UsersPage() {
  const { api, mode, notify, can } = useApi();
  const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query); const [status, setStatus] = useState(''); const [page, setPage] = useState(1);
  const [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  const [selected, setSelected] = useState<UserRecord>(); const [reason, setReason] = useState('异常群发或骚扰行为'); const [banHours, setBanHours] = useState(24);
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery, status]);
  const state = useResource(() => api.getUsers(deferredQuery, status, page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, status, page, cursors]);
  const paginate = (nextPage: number) => { if (nextPage > page && state.data?.nextCursor) setCursors((current) => ({ ...current, [nextPage]: state.data?.nextCursor ?? '' })); setPage(nextPage); };
  const toggleBan = async () => { if (!selected || !reason.trim()) throw new Error('请输入处置理由'); const wasBanned = selected.status === 'banned'; if (wasBanned) await api.unbanUser(selected.id, reason.trim()); else await api.banUser(selected.id, reason.trim(), banHours); notify(wasBanned ? '已解除用户封禁' : banHours === 0 ? '已永久封禁用户' : `已封禁用户 ${banHours} 小时`); await state.reload(); };
  return <><PageHeader title="用户管理" description="查询账号、查看风险状态并执行账号治理。" />
    <Toolbar query={query} setQuery={setQuery} placeholder="搜索昵称、用户 ID 或手机号"><select aria-label="用户状态" className="select-control" value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="active">正常</option><option value="risk">风险关注</option><option value="banned">已封禁</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="没有匹配的用户" emptyDetail="调整搜索词或状态筛选后重试。"><div className="table-wrap"><table><thead><tr><th>用户</th><th>邻里号</th><th>状态</th><th>注册时间</th><th>设备</th><th><span className="sr-only">操作</span></th></tr></thead><tbody>{state.data?.items.map((user) => <tr key={user.id}><td><div className="identity"><span className="avatar">{user.avatar}</span><div><strong>{user.nickname}</strong><small>{user.id} · {user.phone}</small></div></div></td><td><div><strong className="mono">{user.handle}</strong><small>已修改 {user.handleChangeCount}/2 次</small></div></td><td><Badge value={user.status} />{user.bannedUntil && <small>至 {dateTimeLabel(user.bannedUntil)}</small>}</td><td>{user.registeredAt}</td><td>{user.deviceCount || '暂无'}</td><td><button className="row-action" disabled={!can('users.write')} title={!can('users.write') ? '当前角色没有账号处置权限' : undefined} onClick={() => setSelected(user)}>{user.status === 'banned' ? '解除封禁' : '账号处置'}</button></td></tr>)}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
    <ConfirmDialog open={Boolean(selected)} title={selected?.status === 'banned' ? '解除用户封禁' : '封禁用户'} detail={selected ? `目标账号：${selected.nickname}（${selected.id}）。操作会写入审计日志。` : ''} confirmLabel={selected?.status === 'banned' ? '解除用户封禁' : '确认封禁'} danger={selected?.status !== 'banned'} confirmDisabled={!reason.trim()} onClose={() => setSelected(undefined)} onConfirm={toggleBan}>{selected?.status !== 'banned' && <label className="field-label">封禁时长<select value={banHours} onChange={(event) => setBanHours(Number(event.target.value))}><option value={24}>24 小时</option><option value={72}>3 天</option><option value={168}>7 天</option><option value={720}>30 天</option><option value={0}>永久</option></select></label>}<label className="field-label">处置理由<textarea value={reason} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog>
  </>;
}

function GroupsPage() {
  const { api, mode, notify, can } = useApi();
  const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query); const [status,setStatus]=useState(''); const [page, setPage] = useState(1); const [cursors,setCursors]=useState<Record<number,string>>({1:''}); const [selected, setSelected] = useState<GroupRecord>(); const [reason, setReason] = useState('群内持续传播违规内容');
  useEffect(() => {setPage(1);setCursors({1:''});}, [deferredQuery,status]);
  const state = useResource(() => api.getGroups(deferredQuery,status,page,20,cursors[page]??''), [api, mode, deferredQuery,status,page,cursors]);
  const paginate=(next:number)=>{if(next>page&&state.data?.nextCursor)setCursors(v=>({...v,[next]:state.data?.nextCursor??''}));setPage(next);};
  const disband = async () => { if (!selected) return; await api.disbandGroup(selected.id, reason); notify('群组已解散'); await state.reload(); };
  return <><PageHeader title="群组管理" description="检查群活跃度、举报风险、群主与成员规模。" /><Toolbar query={query} setQuery={setQuery} placeholder="搜索群名称、群 ID 或群主"><select className="select-control" aria-label="群组状态" value={status} onChange={e=>setStatus(e.target.value)}><option value="">全部状态</option><option value="active">正常</option><option value="muted">全员禁言</option><option value="dissolved">已解散</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="没有匹配的群组" emptyDetail="请更换群名称、群 ID 或群主关键词。"><div className="table-wrap"><table><thead><tr><th>群组</th><th>状态</th><th>群主</th><th>成员</th><th>累计消息</th><th>举报</th><th>创建日期</th><th><span className="sr-only">操作</span></th></tr></thead><tbody>{state.data?.items.map((group) => <tr key={group.id}><td><div className="group-name"><span><Users size={17} /></span><div><strong>{group.name}</strong><small>{group.id}</small></div></div></td><td><Badge value={group.status} /></td><td>{group.owner}</td><td>{group.memberCount}</td><td>{group.messageCount.toLocaleString()}</td><td>{group.reportCount}</td><td>{group.createdAt}</td><td><button className="row-action danger-text" disabled={group.status === 'dissolved' || !can('groups.write')} onClick={() => setSelected(group)}>解散群组</button></td></tr>)}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
    <ConfirmDialog open={Boolean(selected)} title="解散群组" detail={selected ? `群组「${selected.name}」解散后无法恢复，成员将无法继续发送消息。` : ''} confirmLabel="解散群组" danger confirmDisabled={!reason.trim()} onClose={() => setSelected(undefined)} onConfirm={disband}><label className="field-label">解散理由<textarea value={reason} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog>
  </>;
}

function ReportsPage() {
  const { api, mode, notify, can } = useApi();
  const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query); const [status, setStatus] = useState('pending'), [page, setPage] = useState(1), [selected, setSelected] = useState<ReportRecord>(), [action, setAction] = useState<ReportResolutionAction>('no_violation'), [note, setNote] = useState('');
  const [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery, status]);
  const state = useResource(() => api.getReports(deferredQuery, status, page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, status, page, cursors]);
  const paginate = (nextPage: number) => { if (nextPage > page && state.data?.nextCursor) setCursors((current) => ({ ...current, [nextPage]: state.data?.nextCursor ?? '' })); setPage(nextPage); };
  const openReport = (report: ReportRecord) => { setSelected(report); setAction(report.targetType === 'message' ? 'delete_message' : report.targetType === 'user' ? 'ban_user' : 'no_violation'); setNote(''); };
  const resolve = async () => { if (!selected) return; const result = await api.resolveReport(selected.id, action, note); notify(result.status === 'rejected' ? '举报已驳回并记录依据' : '违规内容已完成处置'); await state.reload(); };
  const riskLabel = { low: '低风险', medium: '中风险', high: '高风险' };
  return <><PageHeader title="举报审核" description="优先处理高风险内容，并为每次判断留下依据。" />
    <Toolbar query={query} setQuery={setQuery} placeholder="搜索举报 ID、举报人、对象或原因" />
    <div className="tabs" role="group" aria-label="举报状态筛选">{[['pending', '待审核'], ['reviewing', '审核中'], ['resolved', '已处置'], ['rejected', '未违规'], ['', '全部']].map(([value, label]) => <button type="button" aria-pressed={status === value} className={status === value ? 'active' : ''} key={label} onClick={() => setStatus(value)}>{label}</button>)}</div>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="当前队列已处理完" emptyDetail="新的举报进入后会自动出现在这里。"><div className="report-list">{state.data?.items.map((report) => <article className="report-item" key={report.id}><div className={`risk-marker risk-${report.risk}`}><ShieldAlert size={18} /></div><div className="report-main"><div className="report-title"><strong>{report.category}</strong><Badge value={report.status} /><span className={`risk-label risk-${report.risk}`}>{riskLabel[report.risk]}</span></div><p className="report-excerpt">“{report.excerpt}”</p><div className="report-meta"><span>举报对象：{report.target}</span><span>举报人：{report.reporter}</span><time>{report.createdAt}</time></div></div>{report.status !== 'resolved' && report.status !== 'rejected' && <button className="button secondary compact" disabled={!can('reports.write')} onClick={() => openReport(report)}>审核举报</button>}</article>)}</div><Pagination data={state.data} onPage={paginate} /></DataPanel>
    <ConfirmDialog open={Boolean(selected)} title="提交审核结果" detail={selected ? `${selected.category}，对象：${selected.target}` : ''} confirmLabel="提交审核结果" confirmDisabled={!note.trim()} onClose={() => setSelected(undefined)} onConfirm={resolve}><label className="field-label">处置动作<select value={action} onChange={(event) => setAction(event.target.value as ReportResolutionAction)}>{selected?.targetType === 'message' && <option value="delete_message">删除并撤回违规消息</option>}{(selected?.targetType === 'message' || selected?.targetType === 'user') && <option value="ban_user">封禁相关用户并撤销会话</option>}<option value="no_violation">判定未违规</option><option value="dismiss">证据不足，驳回举报</option></select></label><label className="field-label">审核备注<textarea value={note} onChange={(event) => setNote(event.target.value)} placeholder="记录判定依据，便于后续追溯" required /></label></ConfirmDialog>
  </>;
}

function SensitiveWordsPage() {
  const { api, mode, notify, can } = useApi(); const [query, setQuery] = useState(''); const [adding, setAdding] = useState(false); const [removing, setRemoving] = useState<SensitiveWord>(); const [removingReason,setRemovingReason]=useState('规则已失效或误拦截'); const [word, setWord] = useState(''); const [category, setCategory] = useState('诈骗');
  const state = useResource(() => api.getSensitiveWords(), [api, mode]);
  const filtered = state.data?.filter((item) => `${item.word}${item.category}`.includes(query));
  const add = async () => { if (!word.trim()) throw new Error('请输入敏感词'); await api.addSensitiveWord({ word: word.trim(), category, action: 'block', matchType: 'exact' }); setWord(''); notify('敏感词拦截规则已添加'); await state.reload(); };
  const remove = async () => { if (!removing||!removingReason.trim()) throw new Error('请输入删除理由'); await api.deleteSensitiveWord(removing.id,removingReason.trim()); notify('敏感词规则已删除'); await state.reload(); };
  return <><PageHeader title="敏感词库" description="维护服务端真实生效的包含匹配规则；命中后直接拒绝新文本消息。" actions={<button className="button primary" disabled={!can('rules.write')} onClick={() => setAdding(true)}><Plus size={16} />添加规则</button>} />
    <Toolbar query={query} setQuery={setQuery} placeholder="搜索敏感词或分类"><span className="toolbar-note">共 {filtered?.length ?? 0} 条规则</span></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!filtered?.length} emptyTitle="没有匹配的敏感词" emptyDetail="调整关键词，或添加一条新的内容规则。"><div className="table-wrap"><table><thead><tr><th>敏感词</th><th>分类</th><th>匹配方式</th><th>命中动作</th><th>添加日期</th><th><span className="sr-only">操作</span></th></tr></thead><tbody>{filtered?.map((item) => <tr key={item.id}><td><strong>{item.word}</strong></td><td>{item.category}</td><td>包含匹配</td><td><Badge value="banned" label="直接拦截" /></td><td>{item.createdAt}</td><td><button className="icon-button table-icon danger-text" disabled={!can('rules.write')} aria-label={`删除 ${item.word}`} onClick={() => setRemoving(item)}><Trash2 size={16} /></button></td></tr>)}</tbody></table></div></DataPanel>
    <ConfirmDialog open={adding} title="添加敏感词规则" detail="规则按不区分大小写的包含匹配执行，新增后立即应用于新文本消息。" confirmLabel="添加规则" confirmDisabled={!word.trim()} onClose={() => setAdding(false)} onConfirm={add}><label className="field-label">敏感词<input autoFocus value={word} onChange={(event) => setWord(event.target.value)} placeholder="例如：免费领取" required /></label><label className="field-label">分类<select value={category} onChange={(event) => setCategory(event.target.value)}><option>诈骗</option><option>黑产</option><option>金融风险</option><option>色情低俗</option><option>其他</option></select></label></ConfirmDialog>
    <ConfirmDialog open={Boolean(removing)} title="删除敏感词规则" detail={removing ? `删除「${removing.word}」后，新消息将不再应用这条规则。` : ''} confirmLabel="删除规则" danger confirmDisabled={!removingReason.trim()} onClose={() => setRemoving(undefined)} onConfirm={remove}><label className="field-label">删除理由<textarea value={removingReason} onChange={e=>setRemovingReason(e.target.value)} required/></label></ConfirmDialog>
  </>;
}

function messageLifecycle(message: MessageRecord) {
  if (message.expiredAt) return { value: 'rejected', label: '已过期', detail: dateTimeLabel(message.expiredAt) };
  if (message.recalled) return { value: 'rejected', label: '已撤回', detail: dateTimeLabel(message.recalledAt) };
  if (message.expiresAt) return { value: 'pending', label: '限时消息', detail: `到期 ${dateTimeLabel(message.expiresAt)}` };
  if (message.editedAt) return { value: 'info', label: `已编辑 v${message.editVersion}`, detail: dateTimeLabel(message.editedAt) };
  return { value: 'active', label: '有效', detail: `序号 ${message.conversationSeq}` };
}

function MessagesPage() {
  const { api, mode } = useApi(); const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query); const [type, setType] = useState(''), [page, setPage] = useState(1); const [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery, type]);
  const state = useResource(() => api.getMessages(deferredQuery, type, page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, type, page, cursors]);
  const paginate = (nextPage: number) => { if (nextPage > page && state.data?.nextCursor) setCursors((current) => ({ ...current, [nextPage]: state.data?.nextCursor ?? '' })); setPage(nextPage); };
  return <><PageHeader title="消息治理索引" description="依法治理仅检索消息元数据；私聊正文不会在后台任意展示或搜索，内容处置必须从举报证据链进入。" /><Toolbar query={query} setQuery={setQuery} placeholder="搜索消息 ID、会话 ID、发送人或客户端消息 ID"><select className="select-control" aria-label="消息类型" value={type} onChange={(event) => setType(event.target.value)}><option value="">全部类型</option><option value="text">文本</option><option value="image">图片</option><option value="audio">语音</option><option value="video">视频</option><option value="file">文件</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="没有匹配的消息元数据" emptyDetail="调整消息 ID、会话、发送人或类型后重试。"><div className="table-wrap"><table><thead><tr><th>时间</th><th>类型</th><th>消息标识</th><th>发送人</th><th>会话 / 序号</th><th>生命周期</th></tr></thead><tbody>{state.data?.items.map((message: MessageRecord) => { const lifecycle = messageLifecycle(message); return <tr key={message.id}><td>{message.createdAt}</td><td>{message.type}</td><td><div><strong>内容受保护</strong><small className="mono">{message.id}</small><small className="mono">{message.clientMsgId || '无客户端标识'}</small></div></td><td className="mono">{message.senderId}</td><td><div><strong className="mono">{message.conversationId}</strong><small>序号 {message.conversationSeq}</small></div></td><td><Badge value={lifecycle.value} label={lifecycle.label} /><small>{lifecycle.detail}</small></td></tr>; })}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
  </>;
}

const formatBytes = (size: number) => size < 1024 ? `${size} B` : size < 1024 * 1024 ? `${(size / 1024).toFixed(1)} KB` : `${(size / 1024 / 1024).toFixed(1)} MB`;
function MediaPage() {
  const { api, mode } = useApi(); const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query); const [status, setStatus] = useState(''), [page, setPage] = useState(1); const [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery, status]);
  const state = useResource(() => api.getMedia(deferredQuery, status, page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, status, page, cursors]);
  const paginate = (nextPage: number) => { if (nextPage > page && state.data?.nextCursor) setCursors((current) => ({ ...current, [nextPage]: state.data?.nextCursor ?? '' })); setPage(nextPage); };
  return <><PageHeader title="文件与存储" description="检查上传归属、媒体状态、对象键和实际占用。" /><Toolbar query={query} setQuery={setQuery} placeholder="搜索媒体 ID、用户、对象键或 MIME"><select className="select-control" aria-label="媒体状态" value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="pending">上传中</option><option value="ready">可用</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="没有匹配的媒体文件" emptyDetail="上传完成的文件会显示在这里。"><div className="table-wrap"><table><thead><tr><th>媒体</th><th>归属用户</th><th>类型</th><th>大小</th><th>状态</th><th>校验值</th></tr></thead><tbody>{state.data?.items.map((media: MediaRecord) => <tr key={media.id}><td><div><strong className="mono">{media.id}</strong><small className="mono">{media.objectKey}</small></div></td><td className="mono">{media.ownerId}</td><td>{media.mime}</td><td>{formatBytes(media.size)}</td><td><Badge value={media.status === 'ready' ? 'active' : 'pending'} label={media.status === 'ready' ? '可用' : '上传中'} /></td><td className="mono">{media.checksum || '暂无'}</td></tr>)}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
  </>;
}

function OnlinePage() {
  const { api, mode } = useApi(); const state = useResource(() => api.getOnline(), [api, mode]);
  return <><PageHeader title="在线状态" description="当前实例上的 WebSocket 在线用户与连接数，不展示来源 IP。" actions={<button className="button secondary" onClick={() => void state.reload()}><RefreshCcw size={15} />刷新</button>} />
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.length} emptyTitle="当前没有在线用户" emptyDetail="建立 WebSocket 会话后会实时出现在这里。"><div className="table-wrap"><table><thead><tr><th>用户 ID</th><th>连接数</th><th>状态</th></tr></thead><tbody>{state.data?.map((item: OnlineRecord) => <tr key={item.userId}><td className="mono">{item.userId}</td><td>{item.connections}</td><td><Badge value="active" label="在线" /></td></tr>)}</tbody></table></div></DataPanel>
  </>;
}

function RelationshipsPage() {
  const {api,mode}=useApi(); const [query,setQuery]=useState(''), deferred=useDebouncedValue(query); const [category,setCategory]=useState(''); const [friendPage,setFriendPage]=useState(1),[feedbackPage,setFeedbackPage]=useState(1); const [friendCursors,setFriendCursors]=useState<Record<number,string>>({1:''}),[feedbackCursors,setFeedbackCursors]=useState<Record<number,string>>({1:''});
  useEffect(()=>{setFriendPage(1);setFeedbackPage(1);setFriendCursors({1:''});setFeedbackCursors({1:''});},[deferred,category]);
  const friends=useResource(()=>api.getFriendships(deferred,friendPage,20,friendCursors[friendPage]??''),[api,mode,deferred,friendPage,friendCursors]);
  const feedback=useResource(()=>api.getFeedback(deferred,category,feedbackPage,20,feedbackCursors[feedbackPage]??''),[api,mode,deferred,category,feedbackPage,feedbackCursors]);
  const pageFriends=(next:number)=>{if(next>friendPage&&friends.data?.nextCursor)setFriendCursors(v=>({...v,[next]:friends.data?.nextCursor??''}));setFriendPage(next);};
  const pageFeedback=(next:number)=>{if(next>feedbackPage&&feedback.data?.nextCursor)setFeedbackCursors(v=>({...v,[next]:feedback.data?.nextCursor??''}));setFeedbackPage(next);};
  return <><PageHeader title="关系与用户反馈" description="只读核对真实好友关系和用户提交的反馈；后台不提供任意修改社交关系的按钮。"/><Toolbar query={query} setQuery={setQuery} placeholder="搜索用户 ID、昵称、反馈内容或联系方式"><select className="select-control" aria-label="反馈分类" value={category} onChange={e=>setCategory(e.target.value)}><option value="">全部反馈</option><option value="bug">故障</option><option value="feature">建议</option><option value="abuse">举报补充</option><option value="other">其他</option></select></Toolbar>
    <section className="panel section-panel"><div className="panel-heading"><div><h2>好友关系</h2><p>每对关系只展示一次，来源为持久化关系表。</p></div></div><DataPanel loading={friends.loading} error={friends.error} retry={friends.reload} empty={!friends.data?.items.length} emptyTitle="没有匹配的好友关系" emptyDetail="建立双向好友关系后会显示在这里。"><div className="table-wrap"><table><thead><tr><th>用户</th><th>好友</th><th>建立时间</th><th>最近更新</th></tr></thead><tbody>{friends.data?.items.map((item:FriendshipRecord)=><tr key={`${item.userId}:${item.friendUserId}`}><td><strong>{item.userName}</strong><small className="mono">{item.userId}</small></td><td><strong>{item.friendName}</strong><small className="mono">{item.friendUserId}</small></td><td>{item.createdAt}</td><td>{item.updatedAt}</td></tr>)}</tbody></table></div><Pagination data={friends.data} onPage={pageFriends}/></DataPanel></section>
    <section className="panel section-panel"><div className="panel-heading"><div><h2>用户反馈</h2><p>反馈内容、提交者及可选联系方式。</p></div></div><DataPanel loading={feedback.loading} error={feedback.error} retry={feedback.reload} empty={!feedback.data?.items.length} emptyTitle="没有匹配的反馈" emptyDetail="用户提交反馈后会显示在这里。"><div className="table-wrap"><table><thead><tr><th>时间</th><th>用户</th><th>分类</th><th>内容</th><th>联系方式</th></tr></thead><tbody>{feedback.data?.items.map((item:FeedbackRecord)=><tr key={item.id}><td>{item.createdAt}</td><td><strong>{item.userName}</strong><small className="mono">{item.userId}</small></td><td>{item.category}</td><td className="feedback-content">{item.content}</td><td>{item.contact||'未提供'}</td></tr>)}</tbody></table></div><Pagination data={feedback.data} onPage={pageFeedback}/></DataPanel></section>
  </>;
}

const taskLabels:Record<string,string>={scheduledMessages:'定时消息',messageExpiry:'消息过期',mediaCleanup:'媒体清理',pending:'待处理',processing:'处理中',failed:'失败',waiting:'等待到期',status:'状态',lastRun:'最近执行'};
function taskSummary(value:unknown){if(!value||typeof value!=='object')return String(value??'暂无');return Object.entries(value as Record<string,unknown>).map(([key,item])=>`${taskLabels[key]??key}：${String(item)}`).join(' · ');}
function OperationsPage(){
  const {api,mode}=useApi(); const state=useResource(()=>api.getOperationsStatus(),[api,mode]);
  if(state.loading)return <><PageHeader title="推送、任务与权限" description="只读运行视图。"/><Skeleton rows={7}/></>;
  if(state.error||!state.data)return <><PageHeader title="推送、任务与权限" description="只读运行视图。"/><ErrorState message={state.error} retry={state.reload}/></>;
  const data:OperationsStatus=state.data; const taskEntries=Object.entries(data.tasks);
  return <><PageHeader title="推送、任务与权限" description="查看推送设备和队列、后台任务积压、当前管理员与角色边界；页面不提供命令执行或手动清理。" actions={<button className="button secondary" onClick={()=>void state.reload()}><RefreshCcw size={15}/>刷新</button>}/>
    <div className="operations-grid"><section className="panel section-panel"><div className="panel-heading"><div><h2>推送通道</h2><p>已登记设备与禁用设备。</p></div></div>{data.push.providers.length?data.push.providers.map(item=><div className="ops-row" key={item.provider}><strong>{item.provider}</strong><span>{item.activeDevices} 台启用 · {item.disabledDevices} 台禁用</span></div>):<div className="panel-empty">暂无推送设备</div>}</section>
    <section className="panel section-panel"><div className="panel-heading"><div><h2>推送队列</h2><p>发送状态与累计尝试次数。</p></div></div>{data.push.queue.length?data.push.queue.map(item=><div className="ops-row" key={item.status}><Badge value={item.status==='sent'?'success':item.status==='failed'?'failed':'pending'} label={({pending:'待发送',processing:'处理中',sent:'已发送',failed:'失败'} as Record<string,string>)[item.status]??item.status}/><span>{item.count} 条 · 尝试 {item.attempts} 次</span></div>):<div className="panel-empty">队列为空</div>}</section>
    <section className="panel section-panel"><div className="panel-heading"><div><h2>后台任务</h2><p>定时消息、消息过期和媒体清理的只读状态。</p></div></div>{taskEntries.length?taskEntries.map(([name,value])=><div className="ops-row stacked" key={name}><strong>{taskLabels[name]??name}</strong><span>{taskSummary(value)}</span></div>):<div className="panel-empty">暂无后台任务状态</div>}</section>
    <section className="panel section-panel"><div className="panel-heading"><div><h2>管理员与角色</h2><p>{data.access.note}</p></div></div>{data.access.administrators.map(item=><div className="ops-row" key={item.id}><div><strong>{item.id}</strong><small>{item.source} · {item.mutable?'可管理':'只读配置'}</small></div><Badge value="active" label={roleLabels[item.role]}/></div>)}{data.access.roles.map(role=><div className="role-row" key={role.id}><strong>{roleLabels[role.id]}</strong><span>{role.permissions.join(' · ')}</span></div>)}</section></div>
  </>;
}

const dateTimeLabel = (value?: string) => value ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) : '—';
const localDateTimeValue = (value?: string) => value ? new Date(new Date(value).getTime() - new Date(value).getTimezoneOffset() * 60_000).toISOString().slice(0, 16) : '';
const emptyAnnouncement: AnnouncementInput = { title: '', content: '', status: 'draft', pinned: false, targetType: 'all', targetUserIds: [], pushOnPublish: false };

function AnnouncementsPage() {
  const { api, mode, notify, can } = useApi();
  const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query), [status, setStatus] = useState(''), [page, setPage] = useState(1);
  const [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  const [editing, setEditing] = useState<AnnouncementRecord | 'new'>();
  const [form, setForm] = useState<AnnouncementInput>(emptyAnnouncement);
  const [action, setAction] = useState<{ type: 'publish' | 'withdraw' | 'delete'; item: AnnouncementRecord }>();
  const [enqueuePush, setEnqueuePush] = useState(true);
  const [actionReason,setActionReason]=useState('公告已过期或内容需要更正');
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery, status]);
  const state = useResource(() => api.getAnnouncements(deferredQuery, status, page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, status, page, cursors]);
  const paginate = (nextPage: number) => { if (nextPage > page && state.data?.nextCursor) setCursors((current) => ({ ...current, [nextPage]: state.data?.nextCursor ?? '' })); setPage(nextPage); };
  const openCreate = () => { setForm({ ...emptyAnnouncement }); setEditing('new'); };
  const openEdit = (item: AnnouncementRecord) => { setForm({ title: item.title, content: item.content, status: item.status === 'scheduled' ? 'scheduled' : 'draft', pinned: item.pinned, targetType: item.targetType, targetUserIds: item.targetUserIds, scheduledAt: item.scheduledAt, pushOnPublish: item.pushOnPublish }); setEditing(item); };
  const save = async () => {
    const title = form.title.trim(), content = form.content.trim();
    if (!title || !content) throw new Error('标题和正文不能为空');
    if (form.status === 'scheduled' && (!form.scheduledAt || new Date(form.scheduledAt) <= new Date())) throw new Error('定时发布时间必须晚于当前时间');
    if (form.targetType === 'users' && !form.targetUserIds.length) throw new Error('定向公告至少需要一个用户 ID');
    const input = { ...form, title, content, scheduledAt: form.status === 'scheduled' ? new Date(form.scheduledAt as string).toISOString() : undefined, targetUserIds: form.targetType === 'users' ? [...new Set(form.targetUserIds)] : [] };
    if (editing === 'new') await api.createAnnouncement(input); else if (editing) await api.updateAnnouncement(editing.id, input);
    notify(editing === 'new' ? '公告草稿已创建' : '公告已更新'); await state.reload();
  };
  const runAction = async () => {
    if (!action) return;
    if (action.type === 'publish') await api.publishAnnouncement(action.item.id, enqueuePush);
    if (action.type === 'withdraw') await api.withdrawAnnouncement(action.item.id);
    if (action.type === 'delete') { if(!actionReason.trim()) throw new Error('请输入删除理由'); await api.deleteAnnouncement(action.item.id,actionReason.trim()); }
    notify(action.type === 'publish' ? '公告已发布' : action.type === 'withdraw' ? '公告已撤回' : '公告已删除'); await state.reload();
  };
  return <><PageHeader title="运营公告" description="创建草稿、定时发布、置顶、定向投放和撤回公告；维护模式公告仍在系统设置中独立管理。" actions={<button className="button primary" onClick={openCreate} disabled={!can('announcements.write')}><Plus size={16} />新建公告</button>} />
    <Toolbar query={query} setQuery={setQuery} placeholder="搜索公告 ID、标题或正文"><select className="select-control" aria-label="公告状态" value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="draft">草稿</option><option value="scheduled">定时发布</option><option value="published">已发布</option><option value="withdrawn">已撤回</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="还没有公告" emptyDetail="创建第一条公告，可保存草稿或安排定时发布。"><div className="table-wrap"><table><thead><tr><th>公告</th><th>范围</th><th>发布时间</th><th>推送</th><th>状态</th><th>操作</th></tr></thead><tbody>{state.data?.items.map((item) => <tr key={item.id}><td><div><strong>{item.pinned ? '置顶 · ' : ''}{item.title}</strong><small className="mono">{item.id}</small></div></td><td>{item.targetType === 'all' ? '全部用户' : `${item.targetUserIds.length} 位用户`}</td><td>{dateTimeLabel(item.publishedAt ?? item.scheduledAt)}</td><td>{item.pushOnPublish ? '离线推送' : '仅站内'}</td><td><Badge value={item.status} /></td><td><button className="row-action" disabled={!can('announcements.write') || !['draft', 'scheduled'].includes(item.status)} onClick={() => openEdit(item)}>编辑</button>{['draft', 'scheduled'].includes(item.status) && <button className="row-action" disabled={!can('announcements.write')} onClick={() => { setEnqueuePush(item.pushOnPublish); setAction({ type: 'publish', item }); }}>发布</button>}{item.status === 'published' && <button className="row-action" disabled={!can('announcements.write')} onClick={() => setAction({ type: 'withdraw', item })}>撤回</button>}{['draft', 'withdrawn'].includes(item.status) && <button className="row-action danger-text" disabled={!can('announcements.write')} onClick={() => setAction({ type: 'delete', item })}>删除</button>}</td></tr>)}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
    <ConfirmDialog open={Boolean(editing)} title={editing === 'new' ? '新建运营公告' : '编辑运营公告'} detail="公告内容会由客户端展示；定向用户 ID 必须来自平台现有用户。" confirmLabel={editing === 'new' ? '创建公告' : '保存更改'} onClose={() => setEditing(undefined)} onConfirm={save}><label className="field-label">公告标题<input value={form.title} maxLength={80} onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))} placeholder="简洁说明本次通知" /></label><label className="field-label">公告正文<textarea value={form.content} maxLength={5000} onChange={(event) => setForm((current) => ({ ...current, content: event.target.value }))} placeholder="说明影响范围、时间和用户需要采取的操作" /></label><div className="form-grid"><label className="field-label">发布方式<select value={form.status} onChange={(event) => setForm((current) => ({ ...current, status: event.target.value as 'draft' | 'scheduled' }))}><option value="draft">保存草稿</option><option value="scheduled">定时发布</option></select></label><label className="field-label">投放范围<select value={form.targetType} onChange={(event) => setForm((current) => ({ ...current, targetType: event.target.value as 'all' | 'users' }))}><option value="all">全部用户</option><option value="users">指定用户</option></select></label></div>{form.status === 'scheduled' && <label className="field-label">定时发布时间<input type="datetime-local" min={localDateTimeValue(new Date(Date.now() + 60_000).toISOString())} value={localDateTimeValue(form.scheduledAt)} onChange={(event) => setForm((current) => ({ ...current, scheduledAt: event.target.value }))} /></label>}{form.targetType === 'users' && <label className="field-label">用户 ID（逗号或换行分隔）<textarea value={form.targetUserIds.join('\n')} onChange={(event) => setForm((current) => ({ ...current, targetUserIds: event.target.value.split(/[\s,，]+/).map((value) => value.trim()).filter(Boolean) }))} placeholder="user_001&#10;user_002" /></label>}<Toggle label="置顶展示" description="置顶公告优先显示在客户端公告列表顶部。" checked={form.pinned} onChange={(value) => setForm((current) => ({ ...current, pinned: value }))} /><Toggle label="发布时离线推送" description="按投放范围写入推送队列；敏感凭据不会进入公告内容。" checked={form.pushOnPublish} onChange={(value) => setForm((current) => ({ ...current, pushOnPublish: value }))} /></ConfirmDialog>
    <ConfirmDialog open={Boolean(action)} title={action?.type === 'publish' ? '发布公告' : action?.type === 'withdraw' ? '撤回公告' : '删除公告'} detail={action ? `「${action.item.title}」${action.type === 'publish' ? '将立即对目标用户可见。' : action.type === 'withdraw' ? '撤回后客户端将不再展示。' : '删除后无法恢复。'}` : ''} confirmLabel={action?.type === 'publish' ? '立即发布' : action?.type === 'withdraw' ? '确认撤回' : '确认删除'} danger={action?.type !== 'publish'} confirmDisabled={action?.type === 'delete' && !actionReason.trim()} onClose={() => setAction(undefined)} onConfirm={runAction}>{action?.type === 'publish' && <Toggle label="同时发送离线推送" description="仅向公告目标范围内、已注册推送设备的用户投递。" checked={enqueuePush} onChange={setEnqueuePush} />}{action?.type==='delete'&&<label className="field-label">删除理由<textarea value={actionReason} onChange={e=>setActionReason(e.target.value)} required/></label>}</ConfirmDialog>
  </>;
}

function CallsPage() {
  const { api, mode } = useApi(); const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query), [status, setStatus] = useState(''), [page, setPage] = useState(1), [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery, status]);
  const state = useResource(() => api.getCalls(deferredQuery, status, page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, status, page, cursors]);
  const paginate = (nextPage: number) => { if (nextPage > page && state.data?.nextCursor) setCursors((current) => ({ ...current, [nextPage]: state.data?.nextCursor ?? '' })); setPage(nextPage); };
  const callLabels: Record<string, string> = { invited: '呼叫中', accepted: '通话中', ended: '已结束', rejected: '已拒绝', cancelled: '已取消', missed: '未接听' };
  return <><PageHeader title="通话记录" description="仅展示呼叫双方、状态、时长和结束原因；服务端不保存 SDP、ICE 或通话内容。" /><Toolbar query={query} setQuery={setQuery} placeholder="搜索通话、会话或用户 ID"><select className="select-control" aria-label="通话状态" value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="invited">呼叫中</option><option value="accepted">通话中</option><option value="ended">已结束</option><option value="rejected">已拒绝</option><option value="cancelled">已取消</option><option value="missed">未接听</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="没有匹配的通话" emptyDetail="通话邀请建立后，元数据会显示在这里。"><div className="table-wrap"><table><thead><tr><th>发起时间</th><th>通话</th><th>双方用户</th><th>类型</th><th>时长</th><th>结束原因</th><th>状态</th></tr></thead><tbody>{state.data?.items.map((call: CallRecord) => <tr key={call.id}><td>{dateTimeLabel(call.invitedAt)}</td><td><div><strong className="mono">{call.id}</strong><small className="mono">{call.conversationId}</small></div></td><td><span className="mono">{call.callerId}</span> → <span className="mono">{call.calleeId}</span></td><td>{call.mediaType === 'video' ? '视频' : '语音'}</td><td>{call.durationSeconds ? `${Math.floor(call.durationSeconds / 60)}:${String(call.durationSeconds % 60).padStart(2, '0')}` : '—'}</td><td>{call.endReason || '—'}</td><td><Badge value={call.status} label={callLabels[call.status] ?? call.status} /></td></tr>)}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
  </>;
}

function HealthPage() {
  const { api, mode } = useApi(); const state = useResource(() => api.getHealth(), [api, mode]); const healthyCount = state.data?.filter((service) => service.status === 'healthy').length ?? 0;
  return <><PageHeader title="系统健康" description="查看服务端当前可提供的健康信息。" actions={<button className="button secondary" onClick={() => void state.reload()}><RefreshCcw size={15} />重新检测</button>} />
    {state.loading ? <Skeleton rows={6} /> : state.error || !state.data ? <ErrorState message={state.error} retry={state.reload} /> : <><div className="health-summary"><div className="health-orb"><HeartPulse size={24} /></div><div><strong>{healthyCount === state.data.length ? '当前检测项目正常' : `${state.data.length - healthyCount} 个检测项目需要关注`}</strong><p>状态来自实时健康接口，不代替外部监控和告警。</p></div><span>{healthyCount}/{state.data.length} 正常</span></div><div className="service-grid">{state.data.map((service) => <ServiceCard key={service.name} service={service} />)}</div></>}
  </>;
}

function ServiceCard({ service }: { service: HealthService }) {
  return <article className="service-card"><div className="service-head"><div className={`service-icon ${service.status}`}><Activity size={18} /></div><Badge value={service.status} /></div><h2>{service.name}</h2><p>{service.detail}</p><dl><div><dt>响应延迟</dt><dd>{service.latency ? `${service.latency} ms` : '暂无'}</dd></div><div><dt>运行时间</dt><dd>{service.uptime}</dd></div><div><dt>版本</dt><dd>{service.version}</dd></div></dl></article>;
}

function AuditPage() {
  const { api, mode } = useApi(); const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query); const [status, setStatus] = useState(''), [page, setPage] = useState(1); const [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery, status]);
  const state = useResource(() => api.getAuditLogs(deferredQuery, status, page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, status, page, cursors]);
  const paginate = (nextPage: number) => { if (nextPage > page && state.data?.nextCursor) setCursors((current) => ({ ...current, [nextPage]: state.data?.nextCursor ?? '' })); setPage(nextPage); };
  return <><PageHeader title="审计日志" description="追溯管理员、审核员和系统任务的敏感操作。" /><Toolbar query={query} setQuery={setQuery} placeholder="搜索操作者、动作、目标或 IP"><select aria-label="审计结果" className="select-control" value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部结果</option><option value="success">成功</option><option value="failed">失败</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="没有匹配的审计记录" emptyDetail="尝试缩短关键词或切换分页。"><div className="table-wrap"><table><thead><tr><th>时间</th><th>操作者</th><th>动作</th><th>目标</th><th>来源 IP</th><th>结果</th></tr></thead><tbody>{state.data?.items.map((log: AuditLog) => <tr key={log.id}><td className="mono">{log.createdAt}</td><td>{log.actor}</td><td><strong>{log.action}</strong></td><td className="mono">{log.target}</td><td className="mono">{log.ip}</td><td><Badge value={log.result} /></td></tr>)}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
  </>;
}

function SettingsPage() {
  const { api, mode, notify, can } = useApi(); const state = useResource(() => api.getSettings(), [api, mode]); const [form, setForm] = useState<AdminSettings>(); const [saving, setSaving] = useState(false); useEffect(() => setForm(state.data), [state.data]);
  const change = <K extends keyof AdminSettings>(key: K, value: AdminSettings[K]) => setForm((current) => current ? { ...current, [key]: value } : current);
  const submit = async (event: FormEvent) => { event.preventDefault(); if (!form || !can('settings.write')) return; setSaving(true); try { const result = await api.updateSettings(form); setForm(result); notify('系统设置已保存'); } catch (cause) { notify(errorMessage(cause), 'danger'); } finally { setSaving(false); } };
  const statuses: Array<[string, boolean]> = form ? [['PostgreSQL 数据库', form.configurationStatus.database], ['Redis 实时总线', form.configurationStatus.redis], ['对象存储', form.configurationStatus.objectStorage], ['短信验证码服务', form.configurationStatus.otpProvider], ['离线推送凭据', form.configurationStatus.pushProvider], ['TURN 中继凭据', form.configurationStatus.turn], ['管理员 TOTP', form.configurationStatus.adminTOTP]] : [];
  return <><PageHeader title="系统设置" description="统一管理可热更新的业务策略；敏感密钥只展示配置状态，基础设施参数需修改环境变量并重启服务。" />{state.loading ? <Skeleton rows={8} /> : state.error || !form ? <ErrorState message={state.error} retry={state.reload} /> : <form className="settings-layout" onSubmit={(event) => void submit(event)}>
    <div className="settings-main"><section className="settings-section"><div className="settings-title"><CircleUserRound size={20} /><div><h2>注册与登录</h2><p>手机号验证码始终用于注册、换绑和找回密码；关闭注册不会影响已有账号登录。</p></div></div><Toggle label="允许新用户注册" description="关闭后密码注册接口会拒绝新账号。" checked={form.allowRegistration} onChange={(value) => change('allowRegistration', value)} /><div className="form-grid"><label className="field-label">密码最少字符数<input type="number" min="8" max="16" value={form.passwordMinLength} onChange={(event) => change('passwordMinLength', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section"><div className="settings-title"><MessageSquareText size={20} /><div><h2>消息、撤回与文件</h2><p>文本和撤回策略实时生效；上传上限由基础设施参数控制。消息删除策略需走独立合规流程。</p></div></div><div className="form-grid"><label className="field-label">文本最大字数<input type="number" min="100" max="10000" value={form.maxMessageTextLength} onChange={(event) => change('maxMessageTextLength', Number(event.target.value))} required /></label><label className="field-label">本人撤回时限（分钟）<input type="number" min="1" max="1440" value={form.messageRecallMinutes} onChange={(event) => change('messageRecallMinutes', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section"><div className="settings-title"><Group size={20} /><div><h2>群聊</h2><p>限制新建群与后续加人的最大规模。</p></div></div><div className="form-grid"><label className="field-label">群组最大成员数<input type="number" min="2" max="5000" value={form.maxGroupMembers} onChange={(event) => change('maxGroupMembers', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section"><div className="settings-title"><Users size={20} /><div><h2>好友与查找</h2><p>控制好友申请入口、查找方式及待处理申请的有效期。</p></div></div><Toggle label="允许发送好友申请" description="关闭后已有好友关系不受影响。" checked={form.allowFriendRequests} onChange={(value) => change('allowFriendRequests', value)} /><Toggle label="允许按邻里号查找" description="关闭后客户端不展示邻里号搜索入口，二维码添加不受影响。" checked={form.allowSearchByHandle} onChange={(value) => change('allowSearchByHandle', value)} /><Toggle label="允许按手机号查找" description="默认关闭；开启后仅返回允许展示的最小用户资料。" checked={form.allowSearchByPhone} onChange={(value) => change('allowSearchByPhone', value)} /><div className="form-grid"><label className="field-label">申请有效期（天）<input type="number" min="1" max="30" value={form.friendRequestExpiryDays} onChange={(event) => change('friendRequestExpiryDays', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section"><div className="settings-title"><Bell size={20} /><div><h2>公告与推送</h2><p>关闭后新建、编辑或手动发布公告不会写入离线推送队列，站内公告仍可发布。</p></div></div><Toggle label="允许公告离线推送" description="公告页面中的“发布时推送”仍需单独勾选。" checked={form.announcementPushEnabled} onChange={(value) => change('announcementPushEnabled', value)} /></section>
      <section className="settings-section"><div className="settings-title"><PhoneCall size={20} /><div><h2>音视频通话</h2><p>可即时关闭全部呼叫或仅关闭视频，正在进行的通话不会被强制中断。</p></div></div><Toggle label="启用音视频通话" description="关闭后新的语音和视频邀请都会被拒绝。" checked={form.callsEnabled} onChange={(value) => change('callsEnabled', value)} /><Toggle label="允许视频通话" description="关闭后仍可发起语音通话。" checked={form.videoCallsEnabled} onChange={(value) => change('videoCallsEnabled', value)} /></section>
      <section className="settings-section"><div className="settings-title"><ShieldAlert size={20} /><div><h2>风控与审核</h2><p>敏感词开关作用于新文本消息，审核时限用于运营 SLA。</p></div></div><Toggle label="启用敏感词拦截" description="关闭后词库保留，但不会拦截新文本消息。" checked={form.sensitiveWordEnabled} onChange={(value) => change('sensitiveWordEnabled', value)} /><div className="form-grid"><label className="field-label">举报处理时限（小时）<input type="number" min="1" max="168" value={form.reportSlaHours} onChange={(event) => change('reportSlaHours', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section danger-zone"><div className="settings-title"><LockKeyhole size={20} /><div><h2>维护模式</h2><p>仅在版本升级或紧急故障处理时启用。</p></div></div><Toggle label="启用维护模式" description="启用后，仅管理员账号可以登录；用户将看到维护公告。" checked={form.maintenanceMode} onChange={(value) => change('maintenanceMode', value)} /><label className="field-label">维护公告<textarea value={form.announcement} onChange={(event) => change('announcement', event.target.value)} placeholder="预计完成时间、影响范围和客服联系方式" required={form.maintenanceMode} /></label></section>
      <section className="settings-section"><div className="settings-title"><Database size={20} /><div><h2>基础设施与密钥状态</h2><p>密钥永不回显；下列容量和连接参数只读，修改环境变量后必须滚动重启。</p></div></div><div className="configuration-grid">{statuses.map(([label, configured]) => <div className="configuration-item" key={label}><span>{label}</span><Badge value={configured ? 'active' : 'failed'} label={configured ? '已配置' : '未配置'} /></div>)}</div><div className="infra-grid"><div><span>推送通道</span><strong>{form.infrastructure.pushProvider}</strong></div><div><span>文件上限</span><strong>{form.infrastructure.mediaMaxSizeMB} MB</strong></div><div><span>呼叫等待</span><strong>{form.infrastructure.callInviteTimeoutSeconds} 秒</strong></div><div><span>单用户连接</span><strong>{form.infrastructure.websocketMaxPerUser}</strong></div><div><span>单 IP 连接</span><strong>{form.infrastructure.websocketMaxPerIP}</strong></div><div><span>访问令牌</span><strong>{form.infrastructure.accessTokenMinutes} 分钟</strong></div><div><span>刷新令牌</span><strong>{form.infrastructure.refreshTokenHours} 小时</strong></div></div><div className="restart-note"><RefreshCcw size={14} />以上参数修改环境变量后需要重启服务，不会由本页面直接写入。</div></section>
    </div><aside className="settings-save"><h2>发布业务策略</h2><p>保存后服务端逐项校验并记录审计日志；热更新项无需重启。</p><div className="save-check"><CheckCircle2 size={17} /><span>数值范围与类型校验已启用</span></div><button className="button primary full" type="submit" disabled={saving || !can('settings.write')}>{saving ? '正在保存…' : <><Save size={16} />保存并立即生效</>}</button>{!can('settings.write') && <p className="permission-note">当前角色没有设置发布权限。</p>}</aside>
  </form>}</>;
}

function Toggle({ label, description, checked, onChange }: { label: string; description: string; checked: boolean; onChange: (value: boolean) => void }) {
  return <label className="toggle-row"><div><strong>{label}</strong><span>{description}</span></div><input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} /><span className="toggle-control" aria-hidden="true" /></label>;
}

function DataPanel({ loading, error, retry, empty, emptyTitle, emptyDetail, children }: { loading: boolean; error: string; retry: () => void; empty: boolean; emptyTitle: string; emptyDetail: string; children: ReactNode }) {
  return <div className="data-panel">{loading ? <Skeleton rows={6} /> : error ? <ErrorState message={error} retry={retry} /> : empty ? <EmptyState title={emptyTitle} detail={emptyDetail} /> : children}</div>;
}

function LoginPage({ onLogin }: { onLogin: (email: string, password: string, totp: string) => Promise<void> }) {
  const [email, setEmail] = useState(''), [password, setPassword] = useState(''), [totp, setTotp] = useState(''), [error, setError] = useState(''), [submitting, setSubmitting] = useState(false);
  const submit = async (event: FormEvent) => { event.preventDefault(); if (!email.trim() || !password) return; setSubmitting(true); setError(''); try { await onLogin(email.trim(), password, totp.trim()); } catch (cause) { setError(errorMessage(cause)); } finally { setSubmitting(false); } };
  return <main className="login-screen"><section className="login-card" aria-labelledby="login-title"><div className="login-mark"><MessageSquareText size={28} /></div><div className="login-heading"><span>邻里通讯运营控制台</span><h1 id="login-title">管理员登录</h1><p>使用管理员账号登录。访问令牌只保存在当前标签页会话中。</p></div><form onSubmit={(event) => void submit(event)}><label className="field-label">管理员邮箱<input type="email" value={email} onChange={(event) => setEmail(event.target.value)} autoComplete="username" required autoFocus /></label><label className="field-label">密码<input type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" required /></label><label className="field-label">动态验证码（如已启用）<input value={totp} onChange={(event) => setTotp(event.target.value.replace(/\D/g, '').slice(0, 6))} autoComplete="one-time-code" inputMode="numeric" pattern="[0-9]{6}" placeholder="6 位验证码" /></label>{error && <div className="inline-notice danger" role="alert"><AlertTriangle size={15} />{error}</div>}<button className="button primary full login-submit" type="submit" disabled={submitting || !email.trim() || !password}><LogIn size={17} />{submitting ? '正在验证…' : '登录控制台'}</button></form><div className="login-security"><ShieldCheck size={17} /><p>权限由服务端管理员会话和角色决定。生产环境不会提供演示入口或共享管理密钥。</p></div></section></main>;
}

function readSession(): AdminSession | undefined {
  try {
    const value = JSON.parse(sessionStorage.getItem(SESSION_KEY) ?? 'null') as AdminSession | null;
    if (value?.token && value.expiresAt > Date.now()) return value;
  } catch { /* invalid session is discarded */ }
  sessionStorage.removeItem(SESSION_KEY);
  return undefined;
}

export function App() {
  const initialStoredMode = localStorage.getItem(MODE_KEY);
  const [mode, updateMode] = useState<DataMode>(allowDemoBuild && initialStoredMode !== 'live' ? 'demo' : 'live');
  const [session, setSession] = useState<AdminSession | undefined>(() => readSession());
  const [notices, setNotices] = useState<Notice[]>([]);
  const demoSession = useMemo<AdminSession>(() => ({ token: 'demo', displayName: '演示管理员', role: 'platform_admin', expiresAt: Number.MAX_SAFE_INTEGER }), []);
  const activeSession = mode === 'demo' && allowDemoBuild ? demoSession : session;
  const notify = useCallback((message: string, tone: Notice['tone'] = 'success') => { const id = Date.now() + Math.random(); setNotices((current) => [...current, { id, tone, message }]); window.setTimeout(() => setNotices((current) => current.filter((notice) => notice.id !== id)), 4200); }, []);
  const logout = useCallback(() => { sessionStorage.removeItem(SESSION_KEY); setSession(undefined); localStorage.setItem(MODE_KEY, 'live'); updateMode('live'); }, []);
  useEffect(() => { const unauthorized = () => { sessionStorage.removeItem(SESSION_KEY); setSession(undefined); localStorage.setItem(MODE_KEY, 'live'); updateMode('live'); notify('管理员会话已失效，请重新登录', 'danger'); }; window.addEventListener('nexachat:unauthorized', unauthorized); return () => window.removeEventListener('nexachat:unauthorized', unauthorized); }, [notify]);
  const login = async (email: string, password: string, totp: string) => { const candidate = await loginAdmin(email, password, totp); sessionStorage.setItem(SESSION_KEY, JSON.stringify(candidate)); localStorage.setItem(MODE_KEY, 'live'); setSession(candidate); updateMode('live'); };
  const setMode = (next: DataMode) => { if (!allowDemoBuild) return; localStorage.setItem(MODE_KEY, next); updateMode(next); };
  const api = useMemo(() => getApi(mode, activeSession?.token), [mode, activeSession?.token]);
  const value = activeSession ? { api, mode, session: activeSession, allowDemo: allowDemoBuild, setMode, logout, notify, can: (permission: Permission) => rolePermissions[activeSession.role].includes(permission) } : undefined;
  return <AppErrorBoundary>{value ? <ApiContext.Provider value={value}><Shell /></ApiContext.Provider> : <LoginPage onLogin={login} />}<div className="toast-region" aria-live="polite" aria-atomic="false">{notices.map((notice) => <div className={`toast ${notice.tone}`} key={notice.id}>{notice.tone === 'success' ? <Check size={16} /> : <AlertTriangle size={16} />}{notice.message}</div>)}</div></AppErrorBoundary>;
}
