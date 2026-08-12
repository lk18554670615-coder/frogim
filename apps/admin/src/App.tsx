import { Component, ErrorInfo, FormEvent, ReactNode, createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  Activity, AlertTriangle, Bell, BookOpenCheck, Check, CheckCircle2, ChevronLeft, ChevronRight,
  CircleUserRound, Database, FileClock, Flag, Group, HardDrive, HeartPulse, LayoutDashboard, LockKeyhole,
  LogIn, LogOut, Menu, MessageSquareText, MoreHorizontal, PhoneCall, Plus, RefreshCcw, Save, Search,
  Server, Settings, ShieldAlert, ShieldCheck, Trash2, Users, Wifi, X,
} from 'lucide-react';
import { ApiError, getApi, loginAdmin } from './api';
import type {
  AdminApi, AdminRole, AdminSession, AdminSettings, AnnouncementInput, AnnouncementRecord, AuditLog, CallRecord, ClientPlatform, ClientVersionPolicy, DashboardData, DataMode, GroupRecord,
  FeedbackRecord, FriendshipRecord, HealthService, MediaRecord, MessageRecord, MomentModerationRecord, OnlineRecord, OperationsStatus, PageResult, ReportRecord, ReportResolutionAction, SensitiveWord, StatusTone, StickerPackModerationRecord, UserRecord,
  StickerCategoryInput, StickerCategoryOperationsRecord, StickerItemInput, StickerPackInput,
  LiveKitParticipant, LiveKitRoom, WukongChannel, WukongConnection, WukongDevice, WukongNode, WukongPlugin, WukongPluginEvent, WukongPluginLogEntry, WukongStoredMessage, WukongSystemUser,
  BusinessChannelInput, BusinessChannelMemberRecord, BusinessChannelRecord, BusinessChannelAccessRecord,
  SupportAgentRecord, SupportSessionRecord, SupportSkillRecord,
} from './types';

type Permission = 'users.write' | 'groups.write' | 'reports.write' | 'rules.write' | 'announcements.write' | 'settings.write' | 'versions.write' | 'content.write' | 'channels.write' | 'operations.write' | 'support.write';
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
const roleLabels: Record<AdminRole, string> = { platform_admin: '平台管理员', system_operator: '系统运维', moderator: '内容审核员', content_operator: '内容运营', support_agent: '客服坐席', support: '只读支持' };
const rolePermissions: Record<AdminRole, Permission[]> = {
  platform_admin: ['users.write', 'groups.write', 'reports.write', 'rules.write', 'announcements.write', 'settings.write', 'versions.write', 'content.write', 'channels.write', 'operations.write', 'support.write'],
  system_operator: ['settings.write', 'versions.write', 'operations.write'],
  moderator: ['users.write', 'reports.write', 'rules.write', 'content.write'],
  content_operator: ['announcements.write', 'content.write', 'channels.write'],
  support_agent: ['support.write'],
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
  hidden: { label: '已隐藏', tone: 'warning' }, deleted: { label: '已删除', tone: 'danger' }, disabled: { label: '已下架', tone: 'neutral' },
  invited: { label: '呼叫中', tone: 'info' }, accepted: { label: '通话中', tone: 'success' }, cancelled: { label: '已取消', tone: 'neutral' }, ended: { label: '已结束', tone: 'neutral' }, missed: { label: '未接听', tone: 'warning' },
  queued: { label: '排队中', tone: 'warning' }, transferring: { label: '转接中', tone: 'info' }, available: { label: '可接待', tone: 'success' }, busy: { label: '忙碌', tone: 'warning' }, away: { label: '暂离', tone: 'neutral' }, offline: { label: '离线', tone: 'neutral' },
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
  { to: '/content-moderation', label: '内容审核', icon: ShieldCheck },
  { to: '/business-channels', label: '频道运营', icon: Group },
  { to: '/support-workbench', label: '客服工作台', icon: MessageSquareText },
  { to: '/im-infrastructure', label: 'IM 基础设施', icon: Server },
  { to: '/client-versions', label: '客户端版本', icon: RefreshCcw },
  { to: '/sensitive-words', label: '敏感词库', icon: ShieldAlert }, { to: '/system-health', label: '系统健康', icon: HeartPulse },
  { to: '/audit', label: '审计日志', icon: FileClock }, { to: '/settings', label: '系统设置', icon: Settings },
];

function Shell() {
  const { mode, setMode, allowDemo, session, logout } = useApi();
  const [navOpen, setNavOpen] = useState(false);
  const { path, navigate } = usePath();
  useEffect(() => setNavOpen(false), [path]);
  const pages: Record<string, ReactNode> = { '/overview': <OverviewPage />, '/users': <UsersPage />, '/groups': <GroupsPage />, '/reports': <ReportsPage />, '/messages': <MessagesPage />, '/media': <MediaPage />, '/online': <OnlinePage />, '/relationships': <RelationshipsPage />, '/operations': <OperationsPage />, '/announcements': <AnnouncementsPage />, '/calls': <CallsPage />, '/content-moderation': <ContentModerationPage />, '/business-channels': <BusinessChannelsPage />, '/support-workbench': <SupportWorkbenchPage />, '/im-infrastructure': <ImInfrastructurePage />, '/client-versions': <ClientVersionsPage />, '/sensitive-words': <SensitiveWordsPage />, '/system-health': <HealthPage />, '/audit': <AuditPage />, '/settings': <SettingsPage /> };
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
  const { api, mode, notify, can } = useApi(); const [query, setQuery] = useState(''); const [adding, setAdding] = useState(false); const [addingReason,setAddingReason]=useState('新增内容安全拦截规则'); const [removing, setRemoving] = useState<SensitiveWord>(); const [removingReason,setRemovingReason]=useState('规则已失效或误拦截'); const [word, setWord] = useState(''); const [category, setCategory] = useState('诈骗');
  const state = useResource(() => api.getSensitiveWords(), [api, mode]);
  const filtered = state.data?.filter((item) => `${item.word}${item.category}`.includes(query));
  const add = async () => { if (!word.trim() || !addingReason.trim()) throw new Error('请输入敏感词和操作理由'); await api.addSensitiveWord({ word: word.trim(), category, action: 'block', matchType: 'exact' }, addingReason.trim()); setWord(''); notify('敏感词拦截规则已添加'); await state.reload(); };
  const remove = async () => { if (!removing||!removingReason.trim()) throw new Error('请输入删除理由'); await api.deleteSensitiveWord(removing.id,removingReason.trim()); notify('敏感词规则已删除'); await state.reload(); };
  return <><PageHeader title="敏感词库" description="维护服务端真实生效的包含匹配规则；命中后直接拒绝新文本消息。" actions={<button className="button primary" disabled={!can('rules.write')} onClick={() => setAdding(true)}><Plus size={16} />添加规则</button>} />
    <Toolbar query={query} setQuery={setQuery} placeholder="搜索敏感词或分类"><span className="toolbar-note">共 {filtered?.length ?? 0} 条规则</span></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!filtered?.length} emptyTitle="没有匹配的敏感词" emptyDetail="调整关键词，或添加一条新的内容规则。"><div className="table-wrap"><table><thead><tr><th>敏感词</th><th>分类</th><th>匹配方式</th><th>命中动作</th><th>添加日期</th><th><span className="sr-only">操作</span></th></tr></thead><tbody>{filtered?.map((item) => <tr key={item.id}><td><strong>{item.word}</strong></td><td>{item.category}</td><td>包含匹配</td><td><Badge value="banned" label="直接拦截" /></td><td>{item.createdAt}</td><td><button className="icon-button table-icon danger-text" disabled={!can('rules.write')} aria-label={`删除 ${item.word}`} onClick={() => setRemoving(item)}><Trash2 size={16} /></button></td></tr>)}</tbody></table></div></DataPanel>
    <ConfirmDialog open={adding} title="添加敏感词规则" detail="规则按不区分大小写的包含匹配执行，新增后立即应用于新文本消息。" confirmLabel="添加规则" confirmDisabled={!word.trim() || !addingReason.trim()} onClose={() => setAdding(false)} onConfirm={add}><label className="field-label">敏感词<input autoFocus value={word} onChange={(event) => setWord(event.target.value)} placeholder="例如：免费领取" required /></label><label className="field-label">分类<select value={category} onChange={(event) => setCategory(event.target.value)}><option>诈骗</option><option>黑产</option><option>金融风险</option><option>色情低俗</option><option>其他</option></select></label><label className="field-label">操作理由<textarea value={addingReason} maxLength={500} onChange={(event) => setAddingReason(event.target.value)} required /></label></ConfirmDialog>
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
  return <><PageHeader title="在线状态" description="WuKongIM 当前节点上的在线用户与设备连接数，不展示来源 IP。" actions={<button className="button secondary" onClick={() => void state.reload()}><RefreshCcw size={15} />刷新</button>} />
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.length} emptyTitle="当前没有在线用户" emptyDetail="客户端连接 WuKongIM 后会实时出现在这里。"><div className="table-wrap"><table><thead><tr><th>用户 ID</th><th>连接数</th><th>状态</th></tr></thead><tbody>{state.data?.map((item: OnlineRecord) => <tr key={item.userId}><td className="mono">{item.userId}</td><td>{item.connections}</td><td><Badge value="active" label="在线" /></td></tr>)}</tbody></table></div></DataPanel>
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
  const [editReason,setEditReason]=useState('创建或更新运营公告');
  const [actionReason,setActionReason]=useState('公告运营状态变更');
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
    if (!editReason.trim()) throw new Error('请输入操作理由');
    if (editing === 'new') await api.createAnnouncement(input, editReason.trim()); else if (editing) await api.updateAnnouncement(editing.id, input, editReason.trim());
    notify(editing === 'new' ? '公告草稿已创建' : '公告已更新'); await state.reload();
  };
  const runAction = async () => {
    if (!action) return;
    if (!actionReason.trim()) throw new Error('请输入操作理由');
    if (action.type === 'publish') await api.publishAnnouncement(action.item.id, enqueuePush, actionReason.trim());
    if (action.type === 'withdraw') await api.withdrawAnnouncement(action.item.id, actionReason.trim());
    if (action.type === 'delete') { if(!actionReason.trim()) throw new Error('请输入删除理由'); await api.deleteAnnouncement(action.item.id,actionReason.trim()); }
    notify(action.type === 'publish' ? '公告已发布' : action.type === 'withdraw' ? '公告已撤回' : '公告已删除'); await state.reload();
  };
  return <><PageHeader title="运营公告" description="创建草稿、定时发布、置顶、定向投放和撤回公告；维护模式公告仍在系统设置中独立管理。" actions={<button className="button primary" onClick={openCreate} disabled={!can('announcements.write')}><Plus size={16} />新建公告</button>} />
    <Toolbar query={query} setQuery={setQuery} placeholder="搜索公告 ID、标题或正文"><select className="select-control" aria-label="公告状态" value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="draft">草稿</option><option value="scheduled">定时发布</option><option value="published">已发布</option><option value="withdrawn">已撤回</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="还没有公告" emptyDetail="创建第一条公告，可保存草稿或安排定时发布。"><div className="table-wrap"><table><thead><tr><th>公告</th><th>范围</th><th>发布时间</th><th>推送</th><th>状态</th><th>操作</th></tr></thead><tbody>{state.data?.items.map((item) => <tr key={item.id}><td><div><strong>{item.pinned ? '置顶 · ' : ''}{item.title}</strong><small className="mono">{item.id}</small></div></td><td>{item.targetType === 'all' ? '全部用户' : `${item.targetUserIds.length} 位用户`}</td><td>{dateTimeLabel(item.publishedAt ?? item.scheduledAt)}</td><td>{item.pushOnPublish ? '离线推送' : '仅站内'}</td><td><Badge value={item.status} /></td><td><button className="row-action" disabled={!can('announcements.write') || !['draft', 'scheduled'].includes(item.status)} onClick={() => openEdit(item)}>编辑</button>{['draft', 'scheduled'].includes(item.status) && <button className="row-action" disabled={!can('announcements.write')} onClick={() => { setEnqueuePush(item.pushOnPublish); setAction({ type: 'publish', item }); }}>发布</button>}{item.status === 'published' && <button className="row-action" disabled={!can('announcements.write')} onClick={() => setAction({ type: 'withdraw', item })}>撤回</button>}{['draft', 'withdrawn'].includes(item.status) && <button className="row-action danger-text" disabled={!can('announcements.write')} onClick={() => setAction({ type: 'delete', item })}>删除</button>}</td></tr>)}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
    <ConfirmDialog open={Boolean(editing)} title={editing === 'new' ? '新建运营公告' : '编辑运营公告'} detail="公告内容会由客户端展示；定向用户 ID 必须来自平台现有用户。" confirmLabel={editing === 'new' ? '创建公告' : '保存更改'} confirmDisabled={!editReason.trim()} onClose={() => setEditing(undefined)} onConfirm={save}><label className="field-label">公告标题<input value={form.title} maxLength={80} onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))} placeholder="简洁说明本次通知" /></label><label className="field-label">公告正文<textarea value={form.content} maxLength={5000} onChange={(event) => setForm((current) => ({ ...current, content: event.target.value }))} placeholder="说明影响范围、时间和用户需要采取的操作" /></label><div className="form-grid"><label className="field-label">发布方式<select value={form.status} onChange={(event) => setForm((current) => ({ ...current, status: event.target.value as 'draft' | 'scheduled' }))}><option value="draft">保存草稿</option><option value="scheduled">定时发布</option></select></label><label className="field-label">投放范围<select value={form.targetType} onChange={(event) => setForm((current) => ({ ...current, targetType: event.target.value as 'all' | 'users' }))}><option value="all">全部用户</option><option value="users">指定用户</option></select></label></div>{form.status === 'scheduled' && <label className="field-label">定时发布时间<input type="datetime-local" min={localDateTimeValue(new Date(Date.now() + 60_000).toISOString())} value={localDateTimeValue(form.scheduledAt)} onChange={(event) => setForm((current) => ({ ...current, scheduledAt: event.target.value }))} /></label>}{form.targetType === 'users' && <label className="field-label">用户 ID（逗号或换行分隔）<textarea value={form.targetUserIds.join('\n')} onChange={(event) => setForm((current) => ({ ...current, targetUserIds: event.target.value.split(/[\s,，]+/).map((value) => value.trim()).filter(Boolean) }))} placeholder="user_001&#10;user_002" /></label>}<Toggle label="置顶展示" description="置顶公告优先显示在客户端公告列表顶部。" checked={form.pinned} onChange={(value) => setForm((current) => ({ ...current, pinned: value }))} /><Toggle label="发布时离线推送" description="按投放范围写入推送队列；敏感凭据不会进入公告内容。" checked={form.pushOnPublish} onChange={(value) => setForm((current) => ({ ...current, pushOnPublish: value }))} /><label className="field-label">操作理由<textarea value={editReason} maxLength={500} onChange={(event) => setEditReason(event.target.value)} required /></label></ConfirmDialog>
    <ConfirmDialog open={Boolean(action)} title={action?.type === 'publish' ? '发布公告' : action?.type === 'withdraw' ? '撤回公告' : '删除公告'} detail={action ? `「${action.item.title}」${action.type === 'publish' ? '将立即对目标用户可见。' : action.type === 'withdraw' ? '撤回后客户端将不再展示。' : '删除后无法恢复。'}` : ''} confirmLabel={action?.type === 'publish' ? '立即发布' : action?.type === 'withdraw' ? '确认撤回' : '确认删除'} danger={action?.type !== 'publish'} confirmDisabled={!actionReason.trim()} onClose={() => setAction(undefined)} onConfirm={runAction}>{action?.type === 'publish' && <Toggle label="同时发送离线推送" description="仅向公告目标范围内、已注册推送设备的用户投递。" checked={enqueuePush} onChange={setEnqueuePush} />}<label className="field-label">操作理由<textarea value={actionReason} maxLength={500} onChange={(event) => setActionReason(event.target.value)} required /></label></ConfirmDialog>
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

type ContentAction = { kind: 'moment'; item: MomentModerationRecord; status: MomentModerationRecord['status'] } | { kind: 'sticker'; item: StickerPackModerationRecord; status: StickerPackModerationRecord['status'] };
function ContentModerationPage() {
  const { api, mode, notify, can } = useApi();
  const [kind, setKind] = useState<'moments' | 'stickers'>('moments');
  const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query);
  const [status, setStatus] = useState(''), [page, setPage] = useState(1), [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  const [action, setAction] = useState<ContentAction>(); const [reason, setReason] = useState('');
  const [categoryDraft, setCategoryDraft] = useState<StickerCategoryInput>();
  const [packDraft, setPackDraft] = useState<StickerPackInput>();
  const [itemEditor, setItemEditor] = useState<{ pack: StickerPackModerationRecord; draft: StickerItemInput }>();
  const [operationReason, setOperationReason] = useState('');
  useEffect(() => { setPage(1); setCursors({ 1: '' }); setStatus(''); }, [kind]);
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery, status]);
  const moments = useResource(() => api.getModerationMoments(deferredQuery, kind === 'moments' ? status : '', page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, kind, status, page, cursors]);
  const stickers = useResource(() => api.getModerationStickerPacks(deferredQuery, kind === 'stickers' ? status : '', page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, kind, status, page, cursors]);
  const categories = useResource(() => kind === 'stickers' ? api.getStickerCategories() : Promise.resolve([] as StickerCategoryOperationsRecord[]), [api, mode, kind]);
  const current = kind === 'moments' ? moments : stickers;
  const paginate = (nextPage: number) => { if (nextPage > page && current.data?.nextCursor) setCursors((values) => ({ ...values, [nextPage]: current.data?.nextCursor ?? '' })); setPage(nextPage); };
  const execute = async () => {
    if (!action) return;
    if (action.kind === 'moment') await api.moderateMoment(action.item.id, action.status, reason.trim());
    else await api.reviewStickerPack(action.item.id, action.status, reason.trim());
    setReason(''); await (action.kind === 'moment' ? moments.reload() : stickers.reload()); notify('内容审核结果已生效');
  };
  const momentActions = (item: MomentModerationRecord): Array<[MomentModerationRecord['status'], string]> => item.status === 'published' ? [['hidden', '隐藏'], ['deleted', '删除']] : item.status === 'hidden' ? [['published', '恢复'], ['deleted', '删除']] : [];
  const stickerActions = (item: StickerPackModerationRecord): Array<[StickerPackModerationRecord['status'], string]> => item.status === 'reviewing' ? [['published', '通过'], ['rejected', '驳回']] : item.status === 'published' ? [['disabled', '下架']] : [['reviewing', '重新送审']];
  const closeOperation = () => { setCategoryDraft(undefined); setPackDraft(undefined); setItemEditor(undefined); setOperationReason(''); };
  const openCategory = (item?: StickerCategoryOperationsRecord) => {
    setOperationReason('');
    setCategoryDraft(item ? { id: item.id, name: item.name, sortOrder: item.sortOrder, enabled: item.enabled } : { name: '', sortOrder: 1000, enabled: true });
  };
  const openPack = (item?: StickerPackModerationRecord) => {
    setOperationReason('');
    setPackDraft(item ? {
      id: item.id, categoryId: item.categoryId ?? '', name: item.name, description: item.description,
      coverMediaId: item.coverMediaId ?? '', status: item.status === 'reviewing' ? 'reviewing' : 'draft', sortOrder: item.sortOrder ?? 1000,
    } : { categoryId: categories.data?.[0]?.id ?? '', name: '', description: '', coverMediaId: '', status: 'draft', sortOrder: 1000 });
  };
  const openItem = (pack: StickerPackModerationRecord, item?: NonNullable<StickerPackModerationRecord['items']>[number]) => {
    setOperationReason('');
    setItemEditor({ pack, draft: item ? { id: item.id, name: item.name, mediaId: item.mediaId, emoji: item.emoji, status: item.status, sortOrder: item.sortOrder } : { name: '', mediaId: '', emoji: '', status: 'published', sortOrder: 1000 } });
  };
  const saveCategory = async () => {
    if (!categoryDraft) return;
    await api.saveStickerCategory(categoryDraft, operationReason.trim());
    await categories.reload(); notify(categoryDraft.id ? '表情分类已更新' : '表情分类已创建');
  };
  const savePack = async () => {
    if (!packDraft) return;
    await api.saveStickerPack(packDraft, operationReason.trim());
    await stickers.reload(); notify(packDraft.id ? '表情包已更新并等待审核' : '表情包已创建');
  };
  const saveItem = async () => {
    if (!itemEditor) return;
    await api.saveStickerItem(itemEditor.pack.id, itemEditor.draft, operationReason.trim());
    await stickers.reload(); notify(itemEditor.draft.id ? '表情项已更新' : '表情项已添加');
  };
  const stickerOperations = kind === 'stickers' ? <div className="row-actions"><button type="button" className="button secondary" disabled={!can('content.write')} onClick={() => openCategory()}><Plus size={16} />创建分类</button><button type="button" className="button primary" disabled={!can('content.write') || !categories.data?.length} onClick={() => openPack()}><Plus size={16} />创建表情包</button></div> : undefined;
  return <><PageHeader title="内容审核" description="审核朋友圈；表情商店支持分类、表情包、表情项运营及上下架。所有写操作都会记录原因和审计。" actions={stickerOperations} />
    <div className="tabs" role="tablist" aria-label="内容类型"><button type="button" role="tab" aria-selected={kind === 'moments'} className={kind === 'moments' ? 'active' : ''} onClick={() => setKind('moments')}>朋友圈</button><button type="button" role="tab" aria-selected={kind === 'stickers'} className={kind === 'stickers' ? 'active' : ''} onClick={() => setKind('stickers')}>表情包</button></div>
    <Toolbar query={query} setQuery={setQuery} placeholder={kind === 'moments' ? '搜索动态、作者或用户 ID' : '搜索表情包、分类或创建者'}><select className="select-control" aria-label="审核状态" value={status} onChange={(event) => setStatus(event.target.value)}>{kind === 'moments' ? <><option value="">全部状态</option><option value="published">已发布</option><option value="hidden">已隐藏</option><option value="deleted">已删除</option></> : <><option value="">全部状态</option><option value="reviewing">待审核</option><option value="published">已发布</option><option value="rejected">已驳回</option><option value="disabled">已下架</option><option value="draft">草稿</option></>}</select></Toolbar>
    {kind === 'stickers' && <section className="panel sticker-category-panel"><div className="panel-heading"><div><h2>表情分类</h2><p>点击分类可编辑名称、排序和启用状态。</p></div></div>{categories.loading ? <span className="muted">正在加载分类…</span> : categories.error ? <div className="inline-notice danger">{categories.error}</div> : <div className="sticker-category-list">{categories.data?.map((item) => <button type="button" className="button secondary compact" key={item.id} disabled={!can('content.write')} onClick={() => openCategory(item)}>{item.name}<Badge value={item.enabled ? 'enabled' : 'disabled'} label={item.enabled ? '启用' : '停用'} /></button>)}{!categories.data?.length && <span className="muted">暂无分类，请先创建分类。</span>}</div>}</section>}
    {kind === 'moments' ? <DataPanel loading={moments.loading} error={moments.error} retry={moments.reload} empty={!moments.data?.items.length} emptyTitle="没有匹配的朋友圈" emptyDetail="可切换状态或缩短关键词。"><div className="table-wrap"><table><thead><tr><th>时间</th><th>作者</th><th>内容</th><th>互动</th><th>可见范围</th><th>状态</th><th>操作</th></tr></thead><tbody>{moments.data?.items.map((item) => <tr key={item.id}><td>{item.createdAt}</td><td><strong>{item.authorName}</strong><small className="mono">{item.authorId}</small></td><td><div><strong className="mono">{item.id}</strong><small>{item.content || `[${item.mediaKind} × ${item.mediaCount}]`}</small></div></td><td>{item.likeCount} 赞 · {item.commentCount} 评</td><td>{item.visibility}</td><td><Badge value={item.status} /></td><td><div className="row-actions">{momentActions(item).map(([next, label]) => <button type="button" className="button secondary" disabled={!can('content.write')} key={next} onClick={() => setAction({ kind: 'moment', item, status: next })}>{label}</button>)}</div></td></tr>)}</tbody></table></div><Pagination data={moments.data} onPage={paginate} /></DataPanel> : <DataPanel loading={stickers.loading} error={stickers.error} retry={stickers.reload} empty={!stickers.data?.items.length} emptyTitle="没有匹配的表情包" emptyDetail="先创建分类，再创建表情包并添加表情项。"><div className="table-wrap"><table><thead><tr><th>表情包</th><th>分类</th><th>创建者</th><th>表情项</th><th>审核备注</th><th>状态</th><th>操作</th></tr></thead><tbody>{stickers.data?.items.map((item) => <tr key={item.id}><td><div><strong>{item.name}</strong><small className="mono">{item.id}</small><small>{item.description || '暂无描述'}</small></div></td><td>{item.categoryName}</td><td className="mono">{item.createdBy || '—'}</td><td><div><strong>{item.itemCount} 个</strong><div className="sticker-item-list">{item.items?.slice(0, 4).map((sticker) => <button type="button" className="row-action" key={sticker.id} disabled={!can('content.write') || item.status === 'published'} onClick={() => openItem(item, sticker)}>{sticker.emoji || sticker.name}</button>)}{(item.items?.length ?? 0) > 4 && <small>+{(item.items?.length ?? 0) - 4}</small>}</div></div></td><td>{item.reviewReason || '—'}</td><td><Badge value={item.status} /></td><td><div className="row-actions wrap"><button type="button" className="button secondary compact" disabled={!can('content.write') || item.status === 'published'} onClick={() => openPack(item)}>编辑</button><button type="button" className="button secondary compact" disabled={!can('content.write') || item.status === 'published'} onClick={() => openItem(item)}><Plus size={14} />表情</button>{stickerActions(item).map(([next, label]) => <button type="button" className="button secondary compact" disabled={!can('content.write')} key={next} onClick={() => setAction({ kind: 'sticker', item, status: next })}>{label}</button>)}</div></td></tr>)}</tbody></table></div><Pagination data={stickers.data} onPage={paginate} /></DataPanel>}
    <ConfirmDialog open={Boolean(action)} title={action?.kind === 'moment' ? '确认处置朋友圈' : '确认审核表情包'} detail={action ? `目标 ${action.item.id} 将变更为「${statusMap[action.status]?.label ?? action.status}」。` : ''} confirmLabel="确认并记录" danger={action?.status === 'deleted' || action?.status === 'rejected' || action?.status === 'disabled'} confirmDisabled={!reason.trim()} onClose={() => { setAction(undefined); setReason(''); }} onConfirm={execute}><label className="field-label">处置理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog>
    <ConfirmDialog open={Boolean(categoryDraft)} title={categoryDraft?.id ? '编辑表情分类' : '创建表情分类'} detail="分类用于组织客户端表情商店，可随时调整排序或停用。" confirmLabel="保存分类" confirmDisabled={!categoryDraft?.name.trim() || !operationReason.trim()} onClose={closeOperation} onConfirm={saveCategory}>{categoryDraft && <><div className="form-grid"><label className="field-label">分类名称<input value={categoryDraft.name} maxLength={100} onChange={(event) => setCategoryDraft((value) => value ? { ...value, name: event.target.value } : value)} required /></label><label className="field-label">排序<input type="number" value={categoryDraft.sortOrder} onChange={(event) => setCategoryDraft((value) => value ? { ...value, sortOrder: Number(event.target.value) } : value)} /></label></div><Toggle label="启用分类" description="停用后客户端商店不再展示该分类及其中表情包。" checked={categoryDraft.enabled} onChange={(enabled) => setCategoryDraft((value) => value ? { ...value, enabled } : value)} /><label className="field-label">操作理由<textarea value={operationReason} maxLength={500} onChange={(event) => setOperationReason(event.target.value)} required /></label></>}</ConfirmDialog>
    <ConfirmDialog open={Boolean(packDraft)} title={packDraft?.id ? '编辑表情包' : '创建表情包'} detail="封面必须填写已上传且状态正常的图片媒体 ID；可在“文件存储”中查询。" confirmLabel="保存表情包" confirmDisabled={!packDraft?.categoryId || !packDraft?.name.trim() || !packDraft?.coverMediaId.trim() || !operationReason.trim()} onClose={closeOperation} onConfirm={savePack}>{packDraft && <><div className="form-grid"><label className="field-label">所属分类<select value={packDraft.categoryId} onChange={(event) => setPackDraft((value) => value ? { ...value, categoryId: event.target.value } : value)}>{categories.data?.map((item) => <option key={item.id} value={item.id}>{item.name}{item.enabled ? '' : '（已停用）'}</option>)}</select></label><label className="field-label">表情包名称<input value={packDraft.name} maxLength={100} onChange={(event) => setPackDraft((value) => value ? { ...value, name: event.target.value } : value)} required /></label><label className="field-label">封面媒体 ID<input className="mono" value={packDraft.coverMediaId} onChange={(event) => setPackDraft((value) => value ? { ...value, coverMediaId: event.target.value } : value)} required /></label><label className="field-label">保存状态<select value={packDraft.status} onChange={(event) => setPackDraft((value) => value ? { ...value, status: event.target.value as StickerPackInput['status'] } : value)}><option value="draft">草稿</option><option value="reviewing">提交审核</option></select></label><label className="field-label">排序<input type="number" value={packDraft.sortOrder} onChange={(event) => setPackDraft((value) => value ? { ...value, sortOrder: Number(event.target.value) } : value)} /></label></div><label className="field-label">描述<textarea value={packDraft.description} maxLength={2000} onChange={(event) => setPackDraft((value) => value ? { ...value, description: event.target.value } : value)} /></label><label className="field-label">操作理由<textarea value={operationReason} maxLength={500} onChange={(event) => setOperationReason(event.target.value)} required /></label></>}</ConfirmDialog>
    <ConfirmDialog open={Boolean(itemEditor)} title={itemEditor?.draft.id ? '编辑表情项' : '添加表情项'} detail={itemEditor ? `添加到「${itemEditor.pack.name}」。图片必须填写已上传且状态正常的媒体 ID。` : ''} confirmLabel="保存表情项" confirmDisabled={!itemEditor?.draft.name.trim() || !itemEditor?.draft.mediaId.trim() || !operationReason.trim()} onClose={closeOperation} onConfirm={saveItem}>{itemEditor && <><div className="form-grid"><label className="field-label">名称<input value={itemEditor.draft.name} maxLength={100} onChange={(event) => setItemEditor((value) => value ? { ...value, draft: { ...value.draft, name: event.target.value } } : value)} required /></label><label className="field-label">图片媒体 ID<input className="mono" value={itemEditor.draft.mediaId} onChange={(event) => setItemEditor((value) => value ? { ...value, draft: { ...value.draft, mediaId: event.target.value } } : value)} required /></label><label className="field-label">快捷 Emoji<input value={itemEditor.draft.emoji} maxLength={32} onChange={(event) => setItemEditor((value) => value ? { ...value, draft: { ...value.draft, emoji: event.target.value } } : value)} placeholder="例如 🙂" /></label><label className="field-label">状态<select value={itemEditor.draft.status} onChange={(event) => setItemEditor((value) => value ? { ...value, draft: { ...value.draft, status: event.target.value as StickerItemInput['status'] } } : value)}><option value="published">启用</option><option value="disabled">停用</option></select></label><label className="field-label">排序<input type="number" value={itemEditor.draft.sortOrder} onChange={(event) => setItemEditor((value) => value ? { ...value, draft: { ...value.draft, sortOrder: Number(event.target.value) } } : value)} /></label></div><label className="field-label">操作理由<textarea value={operationReason} maxLength={500} onChange={(event) => setOperationReason(event.target.value)} required /></label></>}</ConfirmDialog>
  </>;
}

type InfrastructureTab = 'overview' | 'connections' | 'channels' | 'devices' | 'system-users' | 'plugins' | 'livekit';
const infrastructureTabs: Array<[InfrastructureTab, string]> = [['overview', '运行概览'], ['connections', '连接'], ['channels', '频道与消息'], ['devices', '设备'], ['system-users', '系统账号'], ['plugins', '插件'], ['livekit', '音视频房间']];

function ImInfrastructurePage() {
  const [tab, setTab] = useState<InfrastructureTab>('overview');
  return <><PageHeader title="IM 基础设施" description="通过 Go 服务统一查看和处置 WuKongIM 与 LiveKit；上游管理令牌和密钥不会返回浏览器。" />
    <div className="tabs" role="tablist" aria-label="基础设施模块">{infrastructureTabs.map(([value, label]) => <button type="button" role="tab" aria-selected={tab === value} className={tab === value ? 'active' : ''} key={value} onClick={() => setTab(value)}>{label}</button>)}</div>
    {tab === 'overview' ? <WukongOverviewPanel /> : tab === 'connections' ? <WukongConnectionsPanel /> : tab === 'channels' ? <WukongChannelsPanel /> : tab === 'devices' ? <WukongDevicesPanel /> : tab === 'system-users' ? <WukongSystemUsersPanel /> : tab === 'plugins' ? <WukongPluginsPanel /> : <LiveKitRoomsPanel />}
  </>;
}

function WukongSystemUsersPanel() {
  const { api, mode, notify, can } = useApi();
  const users = useResource(() => api.getWukongSystemUsers(), [api, mode]);
  const [userId, setUserId] = useState('');
  const [action, setAction] = useState<{ userId: string; name: string; enabled: boolean }>();
  const [reason, setReason] = useState('');
  const execute = async () => {
    if (!action) return;
    await api.setWukongSystemUser(action.userId, action.enabled, reason.trim());
    setUserId(''); setReason(''); setAction(undefined); await users.reload();
    notify(action.enabled ? '系统账号已提交，正在同步 WuKongIM' : '系统账号权限已撤销，正在同步 WuKongIM');
  };
  return <><section className="operations-block"><div className="panel-heading"><div><h2>系统账号</h2><p>系统账号可主动向任意用户和频道发送消息。输入已存在的用户 UID，变更会通过业务 Outbox 同步到 WuKongIM 运行缓存。</p></div></div><div className="inline-form"><label className="field-label">用户 UID<input className="mono" value={userId} onChange={(event) => setUserId(event.target.value)} placeholder="例如 usr_notice" /></label><button type="button" className="button primary" disabled={!can('operations.write') || !userId.trim()} onClick={() => setAction({ userId: userId.trim(), name: userId.trim(), enabled: true })}><Plus size={15} />设为系统账号</button></div></section><div style={{ height: 14 }} />
    <DataPanel loading={users.loading} error={users.error} retry={users.reload} empty={!users.data?.length} emptyTitle="尚未配置系统账号" emptyDetail="普通业务消息不需要系统账号；仅通知、客服机器人等特殊身份需要配置。"><div className="table-wrap"><table><thead><tr><th>用户</th><th>业务状态</th><th>WuKong 同步</th><th>更新人</th><th>原因</th><th>更新时间</th><th>操作</th></tr></thead><tbody>{users.data?.map((item: WukongSystemUser) => <tr key={item.userId}><td><strong>{item.name || item.userId}</strong><small className="mono">{item.userId}</small></td><td><Badge value={item.enabled ? 'active' : 'disabled'} label={item.enabled ? '已启用' : '已撤销'} /></td><td><Badge value={item.syncStatus === 'failed' ? 'failed' : item.syncStatus === 'synced' ? 'active' : 'warning'} label={item.syncStatus === 'synced' ? '已同步' : item.syncStatus === 'failed' ? '同步失败' : '处理中'} /></td><td className="mono">{item.updatedBy}</td><td>{item.reason}</td><td>{item.updatedAt}</td><td><button type="button" className="button secondary compact danger-text" disabled={!can('operations.write') || !item.enabled} onClick={() => setAction({ userId: item.userId, name: item.name, enabled: false })}>撤销</button></td></tr>)}</tbody></table></div></DataPanel>
    <ConfirmDialog open={Boolean(action)} title={action?.enabled ? '设为系统账号' : '撤销系统账号'} detail={action ? `${action.name || action.userId}（${action.userId}）${action.enabled ? '将可绕过普通频道发送权限' : '将恢复普通用户发送权限'}。` : ''} confirmLabel="确认并同步" danger={!action?.enabled} confirmDisabled={!reason.trim()} onClose={() => { setAction(undefined); setReason(''); }} onConfirm={execute}><label className="field-label">操作理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog></>;
}

function InfrastructureMetric({ label, value, detail }: { label: string; value: string; detail: string }) {
  return <div className="metric"><div><span>{label}</span><Badge value="active" label="实时" /></div><strong>{value}</strong><p>{detail}</p></div>;
}

function WukongOverviewPanel() {
  const { api, mode } = useApi();
  const state = useResource(async () => { const [overview, settings, nodes] = await Promise.all([api.getWukongOverview(), api.getWukongSettings(), api.getWukongNodes()]); return { overview, settings, nodes }; }, [api, mode]);
  if (state.loading) return <Skeleton rows={7} />;
  if (state.error || !state.data) return <ErrorState message={state.error} retry={state.reload} />;
  const { overview, settings, nodes } = state.data;
  return <><div className="metric-strip"><InfrastructureMetric label="当前连接" value={overview.connections.toLocaleString()} detail={`${overview.userHandlers} 个用户处理器`} /><InfrastructureMetric label="累计流入" value={overview.inMessages.toLocaleString()} detail={`流出 ${overview.outMessages.toLocaleString()}`} /><InfrastructureMetric label="资源" value={`${overview.cpu.toFixed(1)}%`} detail={`${Math.round(overview.memoryBytes / 1024 / 1024)} MB · ${overview.goroutines} 协程`} /><InfrastructureMetric label="重试队列" value={overview.retryQueue.toLocaleString()} detail={overview.retryQueue ? '需要关注积压' : '当前无积压'} /></div>
    <div className="configuration-grid"><div className="configuration-item"><span>Prometheus 指标</span><Badge value={settings.prometheusEnabled ? 'active' : 'disabled'} label={settings.prometheusEnabled ? '已开启' : '未开启'} /></div><div className="configuration-item"><span>Trace 追踪</span><Badge value={settings.traceEnabled ? 'active' : 'disabled'} label={settings.traceEnabled ? '已开启' : '未开启'} /></div><div className="configuration-item"><span>Loki 日志</span><Badge value={settings.lokiEnabled ? 'active' : 'disabled'} label={settings.lokiEnabled ? '已开启' : '未开启'} /></div><div className="configuration-item"><span>压力测试模式</span><Badge value={settings.stressEnabled ? 'warning' : 'disabled'} label={settings.stressEnabled ? '已开启' : '未开启'} /></div></div>
    <div className="data-panel"><div className="table-wrap"><table><thead><tr><th>节点</th><th>状态</th><th>角色</th><th>版本</th><th>槽位</th><th>API 地址</th></tr></thead><tbody>{nodes.map((node: WukongNode) => <tr key={node.id}><td className="mono">{node.id}</td><td><Badge value={node.online ? 'active' : 'down'} label={node.online ? '在线' : '离线'} /></td><td>{node.leader ? 'Leader' : 'Follower'}</td><td>{node.version || overview.version}</td><td>{node.slotLeaderCount}/{node.slotCount}</td><td className="mono">{node.apiAddress || '内部地址'}</td></tr>)}</tbody></table></div></div>
    <p className="infrastructure-note">WuKongIM {overview.version || '版本未知'} · 节点 {overview.serverId || '—'} · 运行 {overview.uptime || '暂无'}</p></>;
}

function WukongConnectionsPanel() {
  const { api, mode } = useApi(); const [query, setQuery] = useState(''), deferred = useDebouncedValue(query); const [page, setPage] = useState(1);
  useEffect(() => setPage(1), [deferred]);
  const state = useResource(() => api.getWukongConnections(deferred, page, 20), [api, mode, deferred, page]);
  return <><Toolbar query={query} setQuery={setQuery} placeholder="按用户 UID 搜索连接"><button className="button secondary" onClick={() => void state.reload()}><RefreshCcw size={15} />刷新</button></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="没有在线连接" emptyDetail="用户上线后连接会显示在这里。"><div className="table-wrap"><table><thead><tr><th>连接</th><th>用户</th><th>设备</th><th>来源</th><th>节点</th><th>消息流量</th><th>最后活动</th></tr></thead><tbody>{state.data?.items.map((item: WukongConnection) => <tr key={item.id}><td className="mono">{item.id}</td><td className="mono">{item.uid}</td><td>{item.device}<small className="mono">{item.deviceId}</small></td><td className="mono">{item.ip}</td><td>{item.nodeId}</td><td>入 {item.inMessages} · 出 {item.outMessages}</td><td>{item.lastActivity}</td></tr>)}</tbody></table></div><Pagination data={state.data} onPage={setPage} /></DataPanel></>;
}

function WukongChannelsPanel() {
  const { api, mode } = useApi(); const [channel, setChannel] = useState(''), deferred = useDebouncedValue(channel); const [channelType, setChannelType] = useState(0); const [fromUid, setFromUid] = useState('');
  const channels = useResource(() => api.getWukongChannels(deferred, channelType, 50), [api, mode, deferred, channelType]);
  const messages = useResource(() => api.getWukongMessages(deferred, channelType, fromUid, 50), [api, mode, deferred, channelType, fromUid]);
  return <><Toolbar query={channel} setQuery={setChannel} placeholder="按频道 ID 搜索"><select className="select-control" aria-label="频道类型" value={channelType} onChange={(event) => setChannelType(Number(event.target.value))}><option value="0">全部类型</option><option value="1">单聊</option><option value="2">群聊</option><option value="3">客服</option><option value="4">社区</option><option value="5">社区话题</option><option value="6">资讯</option><option value="9">直播</option><option value="10">访客</option></select><input className="select-control" aria-label="消息发送者" value={fromUid} onChange={(event) => setFromUid(event.target.value)} placeholder="发送者 UID" /></Toolbar>
    <DataPanel loading={channels.loading} error={channels.error} retry={channels.reload} empty={!channels.data?.length} emptyTitle="没有匹配的频道" emptyDetail="缩短频道 ID 或切换类型。"><div className="table-wrap"><table><thead><tr><th>频道</th><th>类型</th><th>订阅者</th><th>黑/白名单</th><th>状态</th></tr></thead><tbody>{channels.data?.map((item: WukongChannel) => <tr key={`${item.channelId}-${item.channelType}`}><td className="mono">{item.channelId}</td><td>{item.channelType}</td><td>{item.subscriberCount}</td><td>{item.denylistCount}/{item.allowlistCount}</td><td><Badge value={item.disbanded || item.banned ? 'failed' : 'active'} label={item.disbanded ? '已解散' : item.banned ? '已封禁' : '正常'} /></td></tr>)}</tbody></table></div></DataPanel>
    <div style={{ height: 14 }} /><DataPanel loading={messages.loading} error={messages.error} retry={messages.reload} empty={!messages.data?.length} emptyTitle="没有匹配的 WuKong 消息" emptyDetail="可指定频道、类型或发送者。"><div className="table-wrap"><table><thead><tr><th>消息 ID</th><th>序号</th><th>频道</th><th>发送者</th><th>客户端编号</th><th>时间戳</th></tr></thead><tbody>{messages.data?.map((item: WukongStoredMessage) => <tr key={`${item.messageId}-${item.channelId}`}><td className="mono">{item.messageId}</td><td>{item.messageSeq}</td><td className="mono">{item.channelId} / {item.channelType}</td><td className="mono">{item.fromUid}</td><td className="mono">{item.clientMsgNo}</td><td>{item.timestamp}</td></tr>)}</tbody></table></div></DataPanel></>;
}

function WukongDevicesPanel() {
  const { api, mode, notify, can } = useApi(); const [query, setQuery] = useState(''), deferred = useDebouncedValue(query); const [flag, setFlag] = useState(-1); const [target, setTarget] = useState<WukongDevice>(); const [reason, setReason] = useState('');
  const state = useResource(() => api.getWukongDevices(deferred, flag, 50), [api, mode, deferred, flag]);
  const execute = async () => { if (!target) return; await api.quitWukongDevice(target.uid, target.deviceFlag, reason.trim()); await state.reload(); notify('设备连接已强制下线'); };
  return <><Toolbar query={query} setQuery={setQuery} placeholder="按用户 UID 搜索设备"><select className="select-control" aria-label="设备平台" value={flag} onChange={(event) => setFlag(Number(event.target.value))}><option value="-1">全部设备</option><option value="0">App</option><option value="1">Web</option><option value="2">桌面端</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.length} emptyTitle="没有匹配的设备" emptyDetail="设备完成 WuKong Token 注册后会显示。"><div className="table-wrap"><table><thead><tr><th>用户</th><th>设备标识</th><th>设备级别</th><th>Token</th><th>更新时间</th><th>操作</th></tr></thead><tbody>{state.data?.map((item: WukongDevice) => <tr key={`${item.uid}-${item.deviceFlag}`}><td className="mono">{item.uid}</td><td>{item.deviceFlag}</td><td>{item.deviceLevel ? '主设备' : '从设备'}</td><td><Badge value={item.tokenPresent ? 'active' : 'failed'} label={item.tokenPresent ? '已配置' : '未配置'} /></td><td>{item.updatedAt || '—'}</td><td><button className="button secondary compact" disabled={!can('operations.write')} onClick={() => setTarget(item)}>强制下线</button></td></tr>)}</tbody></table></div></DataPanel>
    <ConfirmDialog open={Boolean(target)} title="强制设备下线" detail={target ? `用户 ${target.uid} 的设备 ${target.deviceFlag} 将立即断开。` : ''} confirmLabel="确认下线" danger confirmDisabled={!reason.trim()} onClose={() => { setTarget(undefined); setReason(''); }} onConfirm={execute}><label className="field-label">操作理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog></>;
}

type PluginAction = { kind: 'config' | 'uninstall' | 'enable' | 'disable'; plugin: WukongPlugin };
function WukongPluginsPanel() {
  const { api, mode, notify, can } = useApi();
  const state = useResource(() => api.getWukongPlugins(), [api, mode]);
  const events = useResource(() => api.getWukongPluginEvents(), [api, mode]);
  const [logTarget, setLogTarget] = useState<WukongPlugin>();
  const logs = useResource(() => logTarget ? api.getWukongPluginLogs(logTarget.no, logTarget.nodeId, 200) : Promise.resolve([]), [api, mode, logTarget?.no, logTarget?.nodeId]);
  const [action, setAction] = useState<PluginAction>(); const [reason, setReason] = useState(''); const [configText, setConfigText] = useState('{}');
  const [publishMode, setPublishMode] = useState<'install' | 'upgrade'>('install'); const [publishNode, setPublishNode] = useState(1); const [upgradeNo, setUpgradeNo] = useState('');
  const [bundle, setBundle] = useState<File>(); const [manifest, setManifest] = useState<File>(); const [signature, setSignature] = useState(''); const [publishReason, setPublishReason] = useState(''); const [publishing, setPublishing] = useState(false);
  const openConfig = (plugin: WukongPlugin) => { setConfigText(JSON.stringify(plugin.config, null, 2)); setAction({ kind: 'config', plugin }); };
  const execute = async () => {
    if (!action) return;
    if (action.kind === 'config') { const parsed = JSON.parse(configText) as unknown; if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('插件配置必须是 JSON 对象'); await api.updateWukongPluginConfig(action.plugin.no, action.plugin.nodeId, parsed as Record<string, unknown>, reason.trim()); }
    else if (action.kind === 'uninstall') await api.uninstallWukongPlugin(action.plugin.no, action.plugin.nodeId, reason.trim());
    else await api.setWukongPluginEnabled(action.plugin.no, action.plugin.nodeId, action.kind === 'enable', reason.trim());
    await Promise.all([state.reload(), events.reload()]); notify(action.kind === 'config' ? '插件配置已更新' : action.kind === 'uninstall' ? '插件已卸载' : action.kind === 'enable' ? '插件已启用并完成自检' : '插件已停用');
  };
  const publish = async () => {
    if (!bundle || !manifest || !signature.trim() || !publishReason.trim() || publishNode < 1 || (publishMode === 'upgrade' && !upgradeNo)) return;
    setPublishing(true);
    try { if (publishMode === 'install') await api.installWukongPlugin(bundle, manifest, signature, publishNode, publishReason.trim()); else await api.upgradeWukongPlugin(upgradeNo, bundle, manifest, signature, publishNode, publishReason.trim()); await Promise.all([state.reload(), events.reload()]); setBundle(undefined); setManifest(undefined); setSignature(''); setPublishReason(''); notify(publishMode === 'install' ? '签名插件已安装并通过启动自证' : '签名插件已升级并通过启动自证'); }
    finally { setPublishing(false); }
  };
  const actionTitle = action?.kind === 'config' ? '更新插件配置' : action?.kind === 'uninstall' ? '卸载插件' : action?.kind === 'enable' ? '启用插件' : '停用插件';
  return <><div className="operations-block"><div className="panel-heading"><div><h2>签名插件发布</h2><p>上传原始 .wkp、签名清单和离线 Ed25519 签名；服务端校验白名单并在启动后核对自报元数据。AI Receive 插件会被拒绝。</p></div></div><div className="form-grid"><label className="field-label">发布方式<select value={publishMode} onChange={(event) => setPublishMode(event.target.value as 'install' | 'upgrade')}><option value="install">首次安装</option><option value="upgrade">升级现有插件</option></select></label><label className="field-label">节点 ID<input type="number" min="1" value={publishNode} onChange={(event) => setPublishNode(Number(event.target.value))} /></label>{publishMode === 'upgrade' && <label className="field-label">升级目标<select value={upgradeNo} onChange={(event) => setUpgradeNo(event.target.value)}><option value="">请选择</option>{state.data?.filter((item) => item.managed).map((item) => <option key={item.no} value={item.no}>{item.no}</option>)}</select></label>}<label className="field-label">签名清单 JSON<input aria-label="签名清单 JSON" type="file" accept="application/json,.json" onChange={(event) => setManifest(event.target.files?.[0])} /></label><label className="field-label">插件可执行文件<input aria-label="插件可执行文件" type="file" accept=".wkp" onChange={(event) => setBundle(event.target.files?.[0])} /></label><label className="field-label">Ed25519 签名（Base64）<textarea className="mono" value={signature} onChange={(event) => setSignature(event.target.value)} /></label><label className="field-label">发布理由 / 工单<textarea value={publishReason} maxLength={500} onChange={(event) => setPublishReason(event.target.value)} /></label></div><button className="button primary" disabled={!can('operations.write') || publishing || !bundle || !manifest || !signature.trim() || !publishReason.trim() || (publishMode === 'upgrade' && !upgradeNo)} onClick={() => void publish()}>{publishing ? '正在校验并启动…' : publishMode === 'install' ? '校验并安装' : '校验并升级'}</button></div><div style={{ height: 14 }} />
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.length} emptyTitle="没有已安装插件" emptyDetail="仅签名白名单中的插件允许安装；系统策略插件是受保护的内置插件。"><div className="table-wrap"><table><thead><tr><th>插件</th><th>节点</th><th>版本</th><th>方法</th><th>信任</th><th>状态</th><th>操作</th></tr></thead><tbody>{state.data?.map((item: WukongPlugin) => <tr key={`${item.nodeId}-${item.no}`}><td><strong>{item.name || item.no}</strong><small className="mono">{item.no}</small>{item.sha256 && <small className="mono">SHA-256 {item.sha256.slice(0, 12)}…</small>}</td><td>{item.nodeId}</td><td>{item.version}</td><td>{item.methods.join(', ') || '—'}</td><td><Badge value={item.verified ? 'active' : 'warning'} label={item.builtIn ? '内置校验' : item.managed && item.verified ? `签名 · ${item.keyId}` : '非托管'} /></td><td><Badge value={item.status === 'normal' || item.status === 'active' ? 'active' : item.status === 'offline' ? 'down' : 'disabled'} label={item.lifecycleStatus || item.status} /></td><td><div className="row-actions"><button className="button secondary compact" onClick={() => setLogTarget(item)}>运行日志</button><button className="button secondary compact" disabled={!can('operations.write')} onClick={() => openConfig(item)}>配置</button>{item.managed && <button className="button secondary compact" disabled={!can('operations.write')} onClick={() => setAction({ kind: item.status === 'disabled' ? 'enable' : 'disable', plugin: item })}>{item.status === 'disabled' ? '启用' : '停用'}</button>}<button className="button secondary compact danger-text" disabled={!can('operations.write') || item.builtIn} title={item.builtIn ? '系统策略插件禁止卸载' : ''} onClick={() => setAction({ kind: 'uninstall', plugin: item })}>卸载</button></div></td></tr>)}</tbody></table></div></DataPanel>
    {logTarget && <><div style={{ height: 14 }} /><section className="operations-block plugin-runtime-panel"><div className="panel-heading"><div><h2>插件运行日志</h2><p><span className="mono">{logTarget.no}</span> · 节点 {logTarget.nodeId} · 仅保留服务端内存中的有界脱敏尾部日志</p></div><div className="row-actions"><button className="button secondary compact" onClick={() => void logs.reload()}>刷新</button><button className="button secondary compact" onClick={() => setLogTarget(undefined)}>关闭</button></div></div><DataPanel loading={logs.loading} error={logs.error} retry={logs.reload} empty={!logs.data?.length} emptyTitle="暂无运行日志" emptyDetail="插件没有输出，或服务重启后内存日志尚未产生。"><div className="plugin-runtime-log" role="log" aria-label={`${logTarget.no} 运行日志`}>{logs.data?.map((entry: WukongPluginLogEntry) => <div className="plugin-runtime-line" key={entry.sequence}><time>{entry.timestamp ? new Date(entry.timestamp).toLocaleString('zh-CN') : '—'}</time><Badge value={entry.stream === 'stderr' ? 'warning' : 'active'} label={entry.stream || 'stdout'} /><code>{entry.message}</code></div>)}</div></DataPanel></section></>}
    <div style={{ height: 14 }} /><DataPanel loading={events.loading} error={events.error} retry={events.reload} empty={!events.data?.length} emptyTitle="没有插件生命周期事件" emptyDetail="安装、升级、停启和卸载结果会记录在这里。"><div className="table-wrap"><table><thead><tr><th>时间</th><th>插件</th><th>动作</th><th>结果</th><th>操作人</th><th>理由</th></tr></thead><tbody>{events.data?.map((item: WukongPluginEvent) => <tr key={item.id}><td>{item.createdAt}</td><td className="mono">{item.pluginNo}</td><td>{item.action}</td><td><Badge value={item.status === 'failed' ? 'failed' : 'active'} label={item.status} /></td><td>{item.actor}</td><td>{item.reason}</td></tr>)}</tbody></table></div></DataPanel>
    <ConfirmDialog open={Boolean(action)} title={actionTitle} detail={action ? `${action.plugin.no} · 节点 ${action.plugin.nodeId}` : ''} confirmLabel="确认执行" danger={action?.kind === 'uninstall' || action?.kind === 'disable'} confirmDisabled={!reason.trim()} onClose={() => { setAction(undefined); setReason(''); }} onConfirm={execute}>{action?.kind === 'config' && <label className="field-label">JSON 配置<textarea className="mono" value={configText} onChange={(event) => setConfigText(event.target.value)} required /></label>}<label className="field-label">操作理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog></>;
}

type LiveKitAction = { kind: 'participant'; room: string; identity: string } | { kind: 'room'; room: string };
function LiveKitRoomsPanel() {
  const { api, mode, notify, can } = useApi(); const rooms = useResource(() => api.getLiveKitRooms(), [api, mode]); const [selected, setSelected] = useState(''); const participants = useResource(() => selected ? api.getLiveKitParticipants(selected) : Promise.resolve([]), [api, mode, selected]); const [action, setAction] = useState<LiveKitAction>(); const [reason, setReason] = useState('');
  useEffect(() => { if (!selected && rooms.data?.length) setSelected(rooms.data[0].name); }, [rooms.data, selected]);
  const execute = async () => { if (!action) return; if (action.kind === 'participant') { await api.removeLiveKitParticipant(action.room, action.identity, reason.trim()); await participants.reload(); notify('参与者已移出房间'); } else { await api.deleteLiveKitRoom(action.room, reason.trim()); if (selected === action.room) setSelected(''); await rooms.reload(); notify('通话房间已关闭'); } };
  return <><DataPanel loading={rooms.loading} error={rooms.error} retry={rooms.reload} empty={!rooms.data?.length} emptyTitle="当前没有 LiveKit 房间" emptyDetail="通话建立后房间会显示在这里。"><div className="table-wrap"><table><thead><tr><th>房间</th><th>参与者</th><th>发布者</th><th>上限</th><th>录制</th><th>创建时间</th><th>操作</th></tr></thead><tbody>{rooms.data?.map((room: LiveKitRoom) => <tr key={room.sid}><td><button className="row-action mono" onClick={() => setSelected(room.name)}>{room.name}</button></td><td>{room.participantCount}</td><td>{room.publisherCount}</td><td>{room.maxParticipants}</td><td>{room.activeRecording ? '是' : '否'}</td><td>{room.createdAt}</td><td><button className="button secondary compact danger-text" disabled={!can('operations.write')} onClick={() => setAction({ kind: 'room', room: room.name })}>关闭房间</button></td></tr>)}</tbody></table></div></DataPanel>
    {selected && <><div style={{ height: 14 }} /><DataPanel loading={participants.loading} error={participants.error} retry={participants.reload} empty={!participants.data?.length} emptyTitle="房间内没有参与者" emptyDetail={selected}><div className="table-wrap"><table><thead><tr><th>参与者</th><th>状态</th><th>轨道</th><th>屏幕共享</th><th>加入时间</th><th>操作</th></tr></thead><tbody>{participants.data?.map((item: LiveKitParticipant) => <tr key={item.sid}><td><strong>{item.name || item.identity}</strong><small className="mono">{item.identity}</small></td><td><Badge value={item.state === 'ACTIVE' ? 'active' : 'warning'} label={item.state} /></td><td>{item.trackCount}</td><td>{item.screenSharing ? '正在共享' : '否'}</td><td>{item.joinedAt}</td><td><button className="button secondary compact" disabled={!can('operations.write')} onClick={() => setAction({ kind: 'participant', room: selected, identity: item.identity })}>移出房间</button></td></tr>)}</tbody></table></div></DataPanel></>}
    <ConfirmDialog open={Boolean(action)} title={action?.kind === 'room' ? '关闭 LiveKit 房间' : '移出参与者'} detail={action ? action.kind === 'room' ? `房间 ${action.room} 将立即关闭。` : `${action.identity} 将从 ${action.room} 断开。` : ''} confirmLabel="确认执行" danger confirmDisabled={!reason.trim()} onClose={() => { setAction(undefined); setReason(''); }} onConfirm={execute}><label className="field-label">操作理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog></>;
}

type ConfirmableAdminAction = { title: string; detail: string; danger?: boolean; run: (reason: string) => Promise<void> };
const channelTypeLabels: Record<number, string> = { 4: '社区', 5: '社区话题', 6: '资讯', 9: '直播' };
const defaultBusinessChannelInput: BusinessChannelInput = { ownerId: '', channelType: 4, name: '', avatarUrl: '', parentId: '', description: '', visibility: 'public', joinPolicy: 'open', postingPolicy: 'members', slowModeSeconds: 0, metadata: {} };

function BusinessChannelsPage() {
  const { api, mode, can, notify } = useApi();
  const [query, setQuery] = useState(''), deferred = useDebouncedValue(query), [channelType, setChannelType] = useState(0), [page, setPage] = useState(1), [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  const channels = useResource(() => api.getBusinessChannels(deferred, channelType, '', page, 20, cursors[page] ?? ''), [api, mode, deferred, channelType, page, cursors]);
  const [selected, setSelected] = useState<BusinessChannelRecord>();
  const members = useResource(() => selected ? api.getBusinessChannelMembers(selected.id, selected.channelType) : Promise.resolve({ items: [] as BusinessChannelMemberRecord[] }), [api, mode, selected?.id, selected?.channelType]);
  const access = useResource(() => selected ? api.getBusinessChannelAccess(selected.id, selected.channelType) : Promise.resolve([] as BusinessChannelAccessRecord[]), [api, mode, selected?.id, selected?.channelType]);
  const [action, setAction] = useState<ConfirmableAdminAction>(), [reason, setReason] = useState('');
  const [createOpen, setCreateOpen] = useState(false), [createReason, setCreateReason] = useState(''), [draft, setDraft] = useState<BusinessChannelInput>(defaultBusinessChannelInput);
  const [memberUserId, setMemberUserId] = useState(''), [memberExpiry, setMemberExpiry] = useState('');
  const [accessUserId, setAccessUserId] = useState(''), [accessType, setAccessType] = useState<'allow' | 'deny'>('deny');
  const [slowMode, setSlowMode] = useState(0);
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferred, channelType]);
  useEffect(() => { if (selected) setSlowMode(selected.slowModeSeconds); }, [selected]);
  const paginate = (next: number) => { if (next > page && channels.data?.nextCursor) setCursors((value) => ({ ...value, [next]: channels.data?.nextCursor ?? '' })); setPage(next); };
  const refreshSelected = async () => { await Promise.all([channels.reload(), members.reload(), access.reload()]); };
  const openAction = (next: ConfirmableAdminAction) => { setReason(''); setAction(next); };
  const execute = async () => { if (!action) return; await action.run(reason.trim()); await refreshSelected(); notify('频道运营配置已更新'); };
  const updateChannel = (channel: BusinessChannelRecord, update: Partial<BusinessChannelRecord>, title: string, detail: string, danger = false) => openAction({ title, detail, danger, run: async (why) => { const value = await api.updateBusinessChannel(channel.id, channel.channelType, update, why); setSelected(value); } });
  const memberAction = (item: BusinessChannelMemberRecord, title: string, detail: string, run: (why: string) => Promise<void>, danger = false) => openAction({ title, detail, danger, run });
  const create = async () => { const item = await api.createBusinessChannel(draft, createReason.trim()); setSelected(item); setDraft(defaultBusinessChannelInput); setCreateReason(''); await channels.reload(); notify(`${channelTypeLabels[item.channelType]}已创建`); };
  const addMember = () => {
    if (!selected || !memberUserId.trim()) return;
    const userId = memberUserId.trim();
    const expiresAt = memberExpiry ? new Date(memberExpiry).toISOString() : undefined;
    openAction({ title: '添加频道成员', detail: `${userId} 将加入 ${selected.name}${expiresAt ? '，并在指定时间自动到期' : ''}。`, run: async (why) => { await api.addBusinessChannelMember(selected.id, selected.channelType, userId, expiresAt, why); setMemberUserId(''); setMemberExpiry(''); } });
  };
  const addAccess = () => {
    if (!selected || !accessUserId.trim()) return;
    const userId = accessUserId.trim(), type = accessType;
    openAction({ title: type === 'deny' ? '加入黑名单' : '加入白名单', detail: `${userId} 将写入 ${selected.name} 的${type === 'deny' ? '黑' : '白'}名单。`, danger: type === 'deny', run: async (why) => { await api.setBusinessChannelAccess(selected.id, selected.channelType, userId, type, true, why); setAccessUserId(''); } });
  };
  return <><PageHeader title="频道运营" description="统一管理社区、社区话题、资讯和直播频道，包括发布策略、慢速模式、成员、临时订阅和黑白名单。" actions={<button className="button primary" disabled={!can('channels.write')} onClick={() => setCreateOpen(true)}><Plus size={16} />创建频道</button>} />
    <Toolbar query={query} setQuery={setQuery} placeholder="搜索频道名称、ID 或所有者"><select className="select-control" aria-label="业务频道类型" value={channelType} onChange={(event) => setChannelType(Number(event.target.value))}><option value="0">全部业务频道</option><option value="4">社区</option><option value="5">社区话题</option><option value="6">资讯</option><option value="9">直播</option></select></Toolbar>
    <DataPanel loading={channels.loading} error={channels.error} retry={channels.reload} empty={!channels.data?.items.length} emptyTitle="没有匹配的业务频道" emptyDetail="创建第一个社区、资讯或直播频道。"><div className="table-wrap"><table><thead><tr><th>频道</th><th>类型</th><th>所有者</th><th>成员</th><th>发布策略</th><th>慢速模式</th><th>状态</th><th>操作</th></tr></thead><tbody>{channels.data?.items.map((item) => <tr key={`${item.channelType}-${item.id}`}><td><strong>{item.name}</strong><small className="mono">{item.id}</small></td><td>{channelTypeLabels[item.channelType]}</td><td className="mono">{item.ownerId}</td><td>{item.memberCount}</td><td>{item.postingPolicy === 'operators' ? '仅运营人员' : '所有成员'}</td><td>{item.slowModeSeconds ? `${item.slowModeSeconds} 秒` : '关闭'}</td><td><Badge value={item.disband ? 'dissolved' : item.ban || item.sendBan ? 'muted' : 'active'} /></td><td><button className="button secondary compact" onClick={() => setSelected(item)}>运营管理</button></td></tr>)}</tbody></table></div><Pagination data={channels.data} onPage={paginate} /></DataPanel>
    {selected && <section className="operations-block" aria-label={`${selected.name} 运营管理`}><div className="panel-heading"><div><h2>{selected.name}</h2><p className="mono">{selected.id} · {channelTypeLabels[selected.channelType]} · {selected.visibility === 'public' ? '公开' : '私密'}</p></div><button className="icon-button" aria-label="关闭频道管理" onClick={() => setSelected(undefined)}><X size={17} /></button></div>
      <div className="toolbar"><button className="button secondary" disabled={!can('channels.write')} onClick={() => updateChannel(selected, { ban: !selected.ban }, selected.ban ? '解除频道封禁' : '封禁频道', selected.ban ? '恢复频道访问。' : '频道将无法正常访问。', !selected.ban)}>{selected.ban ? '解除封禁' : '封禁频道'}</button><button className="button secondary" disabled={!can('channels.write')} onClick={() => updateChannel(selected, { sendBan: !selected.sendBan }, selected.sendBan ? '解除全员禁言' : '开启全员禁言', selected.sendBan ? '恢复频道发送能力。' : '除策略允许者外停止发送。', !selected.sendBan)}>{selected.sendBan ? '解除禁言' : '全员禁言'}</button><input className="select-control" aria-label="慢速模式秒数" type="number" min="0" max="86400" value={slowMode} onChange={(event) => setSlowMode(Number(event.target.value))} /><button className="button secondary" disabled={!can('channels.write')} onClick={() => updateChannel(selected, { slowModeSeconds: slowMode }, '更新慢速模式', `发送间隔将调整为 ${slowMode} 秒。`)}>保存慢速模式</button><button className="button secondary danger-text" disabled={!can('channels.write') || selected.disband} onClick={() => updateChannel(selected, { disband: true }, '解散频道', '频道将永久标记为已解散，并同步到 WuKongIM。', true)}>解散</button></div>
      <div className="business-grid"><div className="data-panel"><div className="panel-heading"><div><h2>成员与临时订阅</h2><p>到期成员由服务端定时清理并同步 WuKongIM。</p></div></div><div className="toolbar"><input className="search-input" aria-label="成员用户 ID" value={memberUserId} onChange={(event) => setMemberUserId(event.target.value)} placeholder="用户 ID" /><input className="select-control" aria-label="订阅到期时间" type="datetime-local" value={memberExpiry} onChange={(event) => setMemberExpiry(event.target.value)} /><button className="button primary" disabled={!can('channels.write') || !memberUserId.trim()} onClick={addMember}>添加</button></div><DataPanel loading={members.loading} error={members.error} retry={members.reload} empty={!members.data?.items.length} emptyTitle="没有频道成员" emptyDetail="添加成员或等待用户订阅。"><div className="table-wrap"><table><thead><tr><th>成员</th><th>角色</th><th>禁言</th><th>订阅期限</th><th>操作</th></tr></thead><tbody>{members.data?.items.map((item) => <tr key={item.userId}><td><strong>{item.name}</strong><small className="mono">{item.userId}</small></td><td>{item.role}</td><td>{item.mutedUntil ? dateTimeLabel(item.mutedUntil) : '否'}</td><td>{item.expiresAt ? dateTimeLabel(item.expiresAt) : '永久'}</td><td><div className="row-actions">{item.role !== 'owner' && <><button className="button secondary compact" disabled={!can('channels.write')} onClick={() => memberAction(item, '更新成员角色', `${item.userId} 将变更为${item.role === 'admin' ? '普通成员' : '管理员'}。`, (why) => api.updateBusinessChannelMember(selected.id, selected.channelType, item.userId, { role: item.role === 'admin' ? 'member' : 'admin' }, why))}>{item.role === 'admin' ? '设为成员' : '设为管理员'}</button><button className="button secondary compact" disabled={!can('channels.write')} onClick={() => memberAction(item, item.mutedUntil ? '解除成员禁言' : '禁言成员一小时', item.userId, (why) => api.updateBusinessChannelMember(selected.id, selected.channelType, item.userId, item.mutedUntil ? { clearMute: true } : { mutedUntil: new Date(Date.now() + 3600000).toISOString() }, why))}>{item.mutedUntil ? '解除禁言' : '禁言 1 小时'}</button><button className="button secondary compact" disabled={!can('channels.write')} onClick={() => memberAction(item, item.expiresAt ? '改为永久订阅' : '设置一天临时订阅', item.userId, (why) => api.updateBusinessChannelMember(selected.id, selected.channelType, item.userId, item.expiresAt ? { clearExpiry: true } : { expiresAt: new Date(Date.now() + 86400000).toISOString() }, why))}>{item.expiresAt ? '改为永久' : '临时 1 天'}</button><button className="button secondary compact danger-text" disabled={!can('channels.write')} onClick={() => memberAction(item, '移除频道成员', `${item.userId} 将立即失去频道访问权限。`, (why) => api.removeBusinessChannelMember(selected.id, selected.channelType, item.userId, why), true)}>移除</button></>}</div></td></tr>)}</tbody></table></div></DataPanel></div>
        <div className="data-panel"><div className="panel-heading"><div><h2>黑白名单</h2><p>黑名单拒绝访问；存在白名单时仅白名单成员可发送。</p></div></div><div className="toolbar"><input className="search-input" aria-label="名单用户 ID" value={accessUserId} onChange={(event) => setAccessUserId(event.target.value)} placeholder="用户 ID" /><select className="select-control" aria-label="名单类型" value={accessType} onChange={(event) => setAccessType(event.target.value as 'allow' | 'deny')}><option value="deny">黑名单</option><option value="allow">白名单</option></select><button className="button primary" disabled={!can('channels.write') || !accessUserId.trim()} onClick={addAccess}>加入</button></div><DataPanel loading={access.loading} error={access.error} retry={access.reload} empty={!access.data?.length} emptyTitle="名单为空" emptyDetail="当前没有额外访问限制。"><div className="table-wrap"><table><thead><tr><th>用户</th><th>类型</th><th>原因</th><th>操作</th></tr></thead><tbody>{access.data?.map((item) => <tr key={`${item.accessType}-${item.userId}`}><td><strong>{item.name}</strong><small className="mono">{item.userId}</small></td><td><Badge value={item.accessType === 'deny' ? 'failed' : 'active'} label={item.accessType === 'deny' ? '黑名单' : '白名单'} /></td><td>{item.reason}</td><td><button className="button secondary compact" disabled={!can('channels.write')} onClick={() => openAction({ title: '移除名单记录', detail: `${item.userId} 将移出${item.accessType === 'deny' ? '黑' : '白'}名单。`, run: (why) => api.setBusinessChannelAccess(selected.id, selected.channelType, item.userId, item.accessType, false, why) })}>移除</button></td></tr>)}</tbody></table></div></DataPanel></div></div></section>}
    <ConfirmDialog open={Boolean(action)} title={action?.title ?? ''} detail={action?.detail ?? ''} confirmLabel="确认并记录" danger={action?.danger} confirmDisabled={!reason.trim()} onClose={() => { setAction(undefined); setReason(''); }} onConfirm={execute}><label className="field-label">操作原因<textarea value={reason} maxLength={1000} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog>
    <ConfirmDialog open={createOpen} title="创建业务频道" detail="频道将写入 PostgreSQL，并通过持久 Outbox 同步到 WuKongIM。" confirmLabel="确认创建" confirmDisabled={!createReason.trim() || !draft.ownerId.trim() || !draft.name.trim() || (draft.channelType === 5 && !draft.parentId?.trim())} onClose={() => { setCreateOpen(false); setCreateReason(''); }} onConfirm={create}><div className="form-grid"><label className="field-label">频道类型<select value={draft.channelType} onChange={(event) => setDraft((value) => ({ ...value, channelType: Number(event.target.value) as BusinessChannelInput['channelType'] }))}><option value="4">社区</option><option value="5">社区话题</option><option value="6">资讯</option><option value="9">直播</option></select></label><label className="field-label">所有者用户 ID<input value={draft.ownerId} onChange={(event) => setDraft((value) => ({ ...value, ownerId: event.target.value }))} required /></label><label className="field-label">频道名称<input value={draft.name} onChange={(event) => setDraft((value) => ({ ...value, name: event.target.value }))} required /></label>{draft.channelType === 5 && <label className="field-label">父社区 ID<input value={draft.parentId} onChange={(event) => setDraft((value) => ({ ...value, parentId: event.target.value }))} required /></label>}<label className="field-label">可见性<select value={draft.visibility} onChange={(event) => setDraft((value) => ({ ...value, visibility: event.target.value as BusinessChannelInput['visibility'] }))}><option value="public">公开</option><option value="private">私密</option></select></label><label className="field-label">加入策略<select value={draft.joinPolicy} onChange={(event) => setDraft((value) => ({ ...value, joinPolicy: event.target.value as BusinessChannelInput['joinPolicy'] }))}><option value="open">开放加入</option><option value="approval">需要审批</option><option value="invite">仅邀请</option><option value="closed">关闭加入</option></select></label><label className="field-label">发布策略<select value={draft.postingPolicy} onChange={(event) => setDraft((value) => ({ ...value, postingPolicy: event.target.value as BusinessChannelInput['postingPolicy'] }))}><option value="members">所有成员</option><option value="operators">仅运营人员</option></select></label><label className="field-label">慢速模式（秒）<input type="number" min="0" max="86400" value={draft.slowModeSeconds} onChange={(event) => setDraft((value) => ({ ...value, slowModeSeconds: Number(event.target.value) }))} /></label></div><label className="field-label">描述<textarea value={draft.description} onChange={(event) => setDraft((value) => ({ ...value, description: event.target.value }))} /></label><label className="field-label">创建原因<textarea value={createReason} maxLength={1000} onChange={(event) => setCreateReason(event.target.value)} required /></label></ConfirmDialog>
  </>;
}

type SupportTab = 'sessions' | 'skills' | 'agents';
function SupportWorkbenchPage() {
  const { api, mode, can, notify } = useApi();
  const [tab, setTab] = useState<SupportTab>('sessions'), [query, setQuery] = useState(''), deferred = useDebouncedValue(query), [status, setStatus] = useState('');
  const skills = useResource(() => api.getSupportSkills(), [api, mode]);
  const agents = useResource(() => api.getSupportAgents(), [api, mode]);
  const sessions = useResource(() => api.getSupportSessions(deferred, status, '', 1, 100), [api, mode, deferred, status]);
  const [action, setAction] = useState<ConfirmableAdminAction>(), [reason, setReason] = useState(''), [targetAgentId, setTargetAgentId] = useState('');
  const [skillDraft, setSkillDraft] = useState<Partial<SupportSkillRecord>>({ name: '', description: '', routingStrategy: 'least_active', maxConcurrentPerAgent: 5, enabled: true });
  const [agentUserId, setAgentUserId] = useState(''), [agentStatus, setAgentStatus] = useState<SupportAgentRecord['status']>('offline'), [agentCapacity, setAgentCapacity] = useState(5), [agentSkills, setAgentSkills] = useState('');
  const execute = async () => { if (!action) return; await action.run(reason.trim()); await Promise.all([skills.reload(), agents.reload(), sessions.reload()]); notify('客服工作台已更新'); };
  const openAction = (next: ConfirmableAdminAction) => { setReason(''); setAction(next); };
  const saveSkill = () => { if (!skillDraft.name?.trim()) return; const input = { ...skillDraft, name: skillDraft.name.trim(), routingStrategy: skillDraft.routingStrategy ?? 'least_active', maxConcurrentPerAgent: skillDraft.maxConcurrentPerAgent ?? 5, enabled: skillDraft.enabled ?? true }; openAction({ title: input.id ? '更新客服技能组' : '创建客服技能组', detail: `${input.name} · ${input.routingStrategy === 'round_robin' ? '轮询分配' : '最少会话优先'}`, run: async (why) => { await api.saveSupportSkill(input, why); setSkillDraft({ name: '', description: '', routingStrategy: 'least_active', maxConcurrentPerAgent: 5, enabled: true }); } }); };
  const saveAgent = () => { if (!agentUserId.trim()) return; const userId = agentUserId.trim(), skillGroupIds = agentSkills.split(',').map((item) => item.trim()).filter(Boolean); openAction({ title: '保存客服坐席', detail: `${userId} 将绑定 ${skillGroupIds.length} 个技能组。`, run: async (why) => { await api.saveSupportAgent(userId, { status: agentStatus, maxConcurrent: agentCapacity, skillGroupIds }, why); setAgentUserId(''); setAgentSkills(''); } }); };
  const sessionAction = (item: SupportSessionRecord, kind: 'claim' | 'transfer' | 'end') => {
    const target = targetAgentId.trim();
    if (kind !== 'end' && !target) return;
    openAction({ title: kind === 'claim' ? '认领客服会话' : kind === 'transfer' ? '转接客服会话' : '结束客服会话', detail: kind === 'end' ? `${item.id} 将结束，访客随后可以评价。` : `${item.id} 将分配给 ${target}。`, danger: kind === 'end', run: async (why) => { if (kind === 'claim') await api.claimSupportSession(item.id, target, why); else if (kind === 'transfer') await api.transferSupportSession(item.id, target, why); else await api.endSupportSession(item.id, why); } });
  };
  return <><PageHeader title="客服工作台" description="管理技能组、坐席容量、排队会话、自动分配、认领、转接、结束和评价结果。" />
    <div className="tabs" role="tablist" aria-label="客服模块"><button role="tab" aria-selected={tab === 'sessions'} className={tab === 'sessions' ? 'active' : ''} onClick={() => setTab('sessions')}>会话队列</button><button role="tab" aria-selected={tab === 'skills'} className={tab === 'skills' ? 'active' : ''} onClick={() => setTab('skills')}>技能组</button><button role="tab" aria-selected={tab === 'agents'} className={tab === 'agents' ? 'active' : ''} onClick={() => setTab('agents')}>客服坐席</button></div>
    {tab === 'sessions' ? <><Toolbar query={query} setQuery={setQuery} placeholder="搜索会话、访客或主题"><select className="select-control" aria-label="客服会话状态" value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="queued">排队中</option><option value="active">处理中</option><option value="ended">已结束</option></select><input className="select-control" aria-label="目标客服 ID" value={targetAgentId} onChange={(event) => setTargetAgentId(event.target.value)} placeholder="认领/转接目标客服 ID" /></Toolbar><DataPanel loading={sessions.loading} error={sessions.error} retry={sessions.reload} empty={!sessions.data?.items.length} emptyTitle="没有客服会话" emptyDetail="新访客进入队列后会显示在这里。"><div className="table-wrap"><table><thead><tr><th>会话</th><th>访客</th><th>技能组</th><th>主题</th><th>队列</th><th>坐席</th><th>状态</th><th>评价</th><th>操作</th></tr></thead><tbody>{sessions.data?.items.map((item) => <tr key={item.id}><td className="mono">{item.id}</td><td><strong>{item.visitorName}</strong><small className="mono">{item.visitorId}</small></td><td>{item.skillGroupName}</td><td>{item.subject || '—'}</td><td>{item.queuePosition || '—'}</td><td>{item.agentName || item.assignedAgentId || '未分配'}</td><td><Badge value={item.status} /></td><td>{item.rating ? `${item.rating}/5` : '—'}</td><td><div className="row-actions">{item.status === 'queued' && <button className="button secondary compact" disabled={!can('support.write') || !targetAgentId.trim()} onClick={() => sessionAction(item, 'claim')}>认领</button>}{item.status === 'active' && <><button className="button secondary compact" disabled={!can('support.write') || !targetAgentId.trim()} onClick={() => sessionAction(item, 'transfer')}>转接</button><button className="button secondary compact danger-text" disabled={!can('support.write')} onClick={() => sessionAction(item, 'end')}>结束</button></>}</div></td></tr>)}</tbody></table></div></DataPanel></> : tab === 'skills' ? <div className="business-grid"><section className="panel"><div className="panel-heading"><div><h2>{skillDraft.id ? '编辑技能组' : '新建技能组'}</h2><p>每个技能组独立控制路由策略和坐席并发上限。</p></div></div><label className="field-label">名称<input value={skillDraft.name ?? ''} onChange={(event) => setSkillDraft((value) => ({ ...value, name: event.target.value }))} /></label><label className="field-label">描述<textarea value={skillDraft.description ?? ''} onChange={(event) => setSkillDraft((value) => ({ ...value, description: event.target.value }))} /></label><div className="form-grid"><label className="field-label">路由策略<select value={skillDraft.routingStrategy} onChange={(event) => setSkillDraft((value) => ({ ...value, routingStrategy: event.target.value as SupportSkillRecord['routingStrategy'] }))}><option value="least_active">最少会话优先</option><option value="round_robin">轮询分配</option></select></label><label className="field-label">每坐席并发<input type="number" min="1" max="100" value={skillDraft.maxConcurrentPerAgent} onChange={(event) => setSkillDraft((value) => ({ ...value, maxConcurrentPerAgent: Number(event.target.value) }))} /></label></div><Toggle label="启用技能组" description="停用后不再接收新访客。" checked={skillDraft.enabled ?? true} onChange={(enabled) => setSkillDraft((value) => ({ ...value, enabled }))} /><button className="button primary" disabled={!can('support.write') || !skillDraft.name?.trim()} onClick={saveSkill}><Save size={15} />保存技能组</button></section><DataPanel loading={skills.loading} error={skills.error} retry={skills.reload} empty={!skills.data?.length} emptyTitle="没有技能组" emptyDetail="创建技能组后才能配置坐席。"><div className="table-wrap"><table><thead><tr><th>技能组</th><th>路由</th><th>并发</th><th>排队</th><th>可用坐席</th><th>状态</th><th>操作</th></tr></thead><tbody>{skills.data?.map((item) => <tr key={item.id}><td><strong>{item.name}</strong><small className="mono">{item.id}</small></td><td>{item.routingStrategy === 'round_robin' ? '轮询' : '最少会话'}</td><td>{item.maxConcurrentPerAgent}</td><td>{item.queueCount}</td><td>{item.availableAgents}</td><td><Badge value={item.enabled ? 'active' : 'disabled'} /></td><td><button className="button secondary compact" onClick={() => setSkillDraft(item)}>编辑</button></td></tr>)}</tbody></table></div></DataPanel></div> : <div className="business-grid"><section className="panel"><div className="panel-heading"><div><h2>配置客服坐席</h2><p>用户必须存在且未被封禁；技能组 ID 使用逗号分隔。</p></div></div><label className="field-label">用户 ID<input value={agentUserId} onChange={(event) => setAgentUserId(event.target.value)} /></label><label className="field-label">技能组 ID<input value={agentSkills} onChange={(event) => setAgentSkills(event.target.value)} placeholder="support_general,support_vip" /></label><div className="form-grid"><label className="field-label">状态<select value={agentStatus} onChange={(event) => setAgentStatus(event.target.value as SupportAgentRecord['status'])}><option value="offline">离线</option><option value="available">可接待</option><option value="busy">忙碌</option><option value="away">暂离</option></select></label><label className="field-label">总并发上限<input type="number" min="1" max="100" value={agentCapacity} onChange={(event) => setAgentCapacity(Number(event.target.value))} /></label></div><button className="button primary" disabled={!can('support.write') || !agentUserId.trim() || !agentSkills.trim()} onClick={saveAgent}><Save size={15} />保存坐席</button></section><DataPanel loading={agents.loading} error={agents.error} retry={agents.reload} empty={!agents.data?.length} emptyTitle="没有客服坐席" emptyDetail="绑定一个用户到技能组。"><div className="table-wrap"><table><thead><tr><th>坐席</th><th>状态</th><th>技能组</th><th>活跃会话</th><th>并发上限</th><th>操作</th></tr></thead><tbody>{agents.data?.map((item) => <tr key={item.userId}><td><strong>{item.name}</strong><small className="mono">{item.userId}</small></td><td><Badge value={item.status} /></td><td>{item.skillGroupIds.join(', ')}</td><td>{item.activeSessions}</td><td>{item.maxConcurrent}</td><td><button className="button secondary compact" onClick={() => { setAgentUserId(item.userId); setAgentStatus(item.status); setAgentCapacity(item.maxConcurrent); setAgentSkills(item.skillGroupIds.join(',')); }}>编辑</button></td></tr>)}</tbody></table></div></DataPanel></div>}
    <ConfirmDialog open={Boolean(action)} title={action?.title ?? ''} detail={action?.detail ?? ''} confirmLabel="确认并记录" danger={action?.danger} confirmDisabled={!reason.trim()} onClose={() => { setAction(undefined); setReason(''); }} onConfirm={execute}><label className="field-label">操作原因<textarea value={reason} maxLength={1000} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog>
  </>;
}

const clientPlatforms: ClientPlatform[] = ['android', 'ios', 'web', 'macos'];
const clientPlatformLabels: Record<ClientPlatform, string> = { android: 'Android', ios: 'iOS', web: 'Web', macos: 'macOS' };
function emptyClientVersion(platform: ClientPlatform): ClientVersionPolicy {
  return { platform, minimumVersion: '1.0.0', latestVersion: '1.0.0', forceUpdate: false, rolloutPercentage: 100, releaseNotes: '', downloadUrl: '', updatedBy: '', updatedAt: '' };
}

function ClientVersionsPage() {
  const { api, mode, notify, can } = useApi();
  const state = useResource(() => api.getClientVersions(), [api, mode]);
  const [selected, setSelected] = useState<ClientPlatform>('android');
  const [draft, setDraft] = useState<ClientVersionPolicy>(() => emptyClientVersion('android'));
  const [confirming, setConfirming] = useState(false);
  const [reason, setReason] = useState('');
  useEffect(() => setDraft(state.data?.find((item) => item.platform === selected) ?? emptyClientVersion(selected)), [state.data, selected]);
  const change = <K extends keyof ClientVersionPolicy>(key: K, value: ClientVersionPolicy[K]) => setDraft((current) => ({ ...current, [key]: value }));
  const save = async () => {
    const updated = await api.updateClientVersion(draft, reason.trim());
    setDraft(updated); setReason(''); await state.reload(); notify(`${clientPlatformLabels[selected]} 版本策略已发布`);
  };
  return <><PageHeader title="客户端版本" description="分别控制四端最低版本、可选或强制更新、稳定灰度比例及下载入口。最低版本限制始终覆盖灰度。" actions={<button className="button secondary" onClick={() => void state.reload()}><RefreshCcw size={15} />刷新策略</button>} />
    {state.loading ? <Skeleton rows={6} /> : state.error ? <ErrorState message={state.error} retry={state.reload} /> : <>
      <div className="tabs" role="tablist" aria-label="客户端平台">{clientPlatforms.map((platform) => <button type="button" role="tab" aria-selected={selected === platform} className={selected === platform ? 'active' : ''} key={platform} onClick={() => setSelected(platform)}>{clientPlatformLabels[platform]}</button>)}</div>
      <div className="settings-layout"><div className="settings-main"><section className="settings-section">
        <div className="settings-title"><RefreshCcw size={20} /><div><h2>{clientPlatformLabels[selected]} 发布策略</h2><p>版本号使用 1、1.2、1.2.3 或 1.2.3.4 格式；下载地址必须是 HTTPS。</p></div></div>
        <div className="form-grid"><label className="field-label">最低支持版本<input value={draft.minimumVersion} pattern="[0-9]+(\.[0-9]+){0,3}" onChange={(event) => change('minimumVersion', event.target.value)} required /></label><label className="field-label">最新发布版本<input value={draft.latestVersion} pattern="[0-9]+(\.[0-9]+){0,3}" onChange={(event) => change('latestVersion', event.target.value)} required /></label><label className="field-label">灰度比例（%）<input type="number" min="0" max="100" value={draft.rolloutPercentage} onChange={(event) => change('rolloutPercentage', Number(event.target.value))} required /></label></div>
        <Toggle label="将本次更新设为强制更新" description="已进入灰度且低于最新版本的客户端不能跳过；低于最低版本的客户端始终强制更新。" checked={draft.forceUpdate} onChange={(value) => change('forceUpdate', value)} />
        <label className="field-label">更新说明<textarea value={draft.releaseNotes} maxLength={4000} onChange={(event) => change('releaseNotes', event.target.value)} placeholder="主要变化、修复内容和用户注意事项" /></label>
        <label className="field-label">下载地址<input type="url" value={draft.downloadUrl} onChange={(event) => change('downloadUrl', event.target.value)} placeholder="https://downloads.example.com/app" /></label>
      </section></div><aside className="settings-save"><h2>发布确认</h2><p>灰度分组按本机安装标识稳定计算，同一设备不会在启动间随机跳组。</p><div className="save-check"><CheckCircle2 size={17} /><span>策略写入与操作理由会同时进入审计日志</span></div><button className="button primary full" type="button" disabled={!can('versions.write')} onClick={() => setConfirming(true)}><Save size={16} />保存并发布</button>{draft.updatedAt && <p className="permission-note">上次更新：{dateTimeLabel(draft.updatedAt)} · {draft.updatedBy || '系统'}</p>}{!can('versions.write') && <p className="permission-note">当前角色没有版本发布权限。</p>}</aside></div>
    </>}
    <ConfirmDialog open={confirming} title={`发布 ${clientPlatformLabels[selected]} 版本策略`} detail={`最低版本 ${draft.minimumVersion}，最新版本 ${draft.latestVersion}，灰度 ${draft.rolloutPercentage}%${draft.forceUpdate ? '，本次为强制更新' : ''}。`} confirmLabel="确认发布" danger={draft.forceUpdate} confirmDisabled={!reason.trim()} onClose={() => { setConfirming(false); setReason(''); }} onConfirm={save}><label className="field-label">发布原因<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} placeholder="填写发布批次、变更单或紧急修复原因" required /></label></ConfirmDialog>
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
  const { api, mode, notify, can } = useApi(); const state = useResource(() => api.getSettings(), [api, mode]); const [form, setForm] = useState<AdminSettings>(); const [saving, setSaving] = useState(false); const [confirming, setConfirming] = useState(false); const [reason, setReason] = useState(''); useEffect(() => setForm(state.data), [state.data]);
  const change = <K extends keyof AdminSettings>(key: K, value: AdminSettings[K]) => setForm((current) => current ? { ...current, [key]: value } : current);
  const submit = (event: FormEvent) => { event.preventDefault(); if (form && can('settings.write')) setConfirming(true); };
  const save = async () => { if (!form || !reason.trim()) throw new Error('请输入发布理由'); setSaving(true); try { const result = await api.updateSettings(form, reason.trim()); setForm(result); setConfirming(false); setReason(''); notify('系统设置已保存'); } catch (cause) { notify(errorMessage(cause), 'danger'); } finally { setSaving(false); } };
  const statuses: Array<[string, boolean]> = form ? [['PostgreSQL 数据库', form.configurationStatus.database], ['Redis 实时总线', form.configurationStatus.redis], ['对象存储', form.configurationStatus.objectStorage], ['短信验证码服务', form.configurationStatus.otpProvider], ['离线推送凭据', form.configurationStatus.pushProvider], ['LiveKit 媒体服务', form.configurationStatus.liveKit], ['管理员 TOTP', form.configurationStatus.adminTOTP]] : [];
  return <><PageHeader title="系统设置" description="统一管理可热更新的业务策略；敏感密钥只展示配置状态，基础设施参数需修改环境变量并重启服务。" />{state.loading ? <Skeleton rows={8} /> : state.error || !form ? <ErrorState message={state.error} retry={state.reload} /> : <form className="settings-layout" onSubmit={(event) => void submit(event)}>
    <div className="settings-main"><section className="settings-section"><div className="settings-title"><CircleUserRound size={20} /><div><h2>注册与登录</h2><p>手机号验证码始终用于注册、换绑和找回密码；关闭注册不会影响已有账号登录。</p></div></div><Toggle label="允许新用户注册" description="关闭后密码注册接口会拒绝新账号。" checked={form.allowRegistration} onChange={(value) => change('allowRegistration', value)} /><div className="form-grid"><label className="field-label">密码最少字符数<input type="number" min="8" max="16" value={form.passwordMinLength} onChange={(event) => change('passwordMinLength', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section"><div className="settings-title"><MessageSquareText size={20} /><div><h2>消息、撤回与文件</h2><p>文本和撤回策略实时生效；上传上限由基础设施参数控制。消息删除策略需走独立合规流程。</p></div></div><div className="form-grid"><label className="field-label">文本最大字数<input type="number" min="100" max="10000" value={form.maxMessageTextLength} onChange={(event) => change('maxMessageTextLength', Number(event.target.value))} required /></label><label className="field-label">本人撤回时限（分钟）<input type="number" min="1" max="1440" value={form.messageRecallMinutes} onChange={(event) => change('messageRecallMinutes', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section"><div className="settings-title"><Group size={20} /><div><h2>群聊</h2><p>限制新建群与后续加人的最大规模。</p></div></div><div className="form-grid"><label className="field-label">群组最大成员数<input type="number" min="2" max="5000" value={form.maxGroupMembers} onChange={(event) => change('maxGroupMembers', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section"><div className="settings-title"><Users size={20} /><div><h2>好友与查找</h2><p>控制好友申请入口、查找方式及待处理申请的有效期。</p></div></div><Toggle label="允许发送好友申请" description="关闭后已有好友关系不受影响。" checked={form.allowFriendRequests} onChange={(value) => change('allowFriendRequests', value)} /><Toggle label="允许按邻里号查找" description="关闭后客户端不展示邻里号搜索入口，二维码添加不受影响。" checked={form.allowSearchByHandle} onChange={(value) => change('allowSearchByHandle', value)} /><Toggle label="允许按手机号查找" description="默认关闭；开启后仅返回允许展示的最小用户资料。" checked={form.allowSearchByPhone} onChange={(value) => change('allowSearchByPhone', value)} /><div className="form-grid"><label className="field-label">申请有效期（天）<input type="number" min="1" max="30" value={form.friendRequestExpiryDays} onChange={(event) => change('friendRequestExpiryDays', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section"><div className="settings-title"><Bell size={20} /><div><h2>公告与推送</h2><p>关闭后新建、编辑或手动发布公告不会写入离线推送队列，站内公告仍可发布。</p></div></div><Toggle label="允许公告离线推送" description="公告页面中的“发布时推送”仍需单独勾选。" checked={form.announcementPushEnabled} onChange={(value) => change('announcementPushEnabled', value)} /></section>
      <section className="settings-section"><div className="settings-title"><PhoneCall size={20} /><div><h2>音视频通话</h2><p>可即时关闭全部呼叫或仅关闭视频，正在进行的通话不会被强制中断。</p></div></div><Toggle label="启用音视频通话" description="关闭后新的语音和视频邀请都会被拒绝。" checked={form.callsEnabled} onChange={(value) => change('callsEnabled', value)} /><Toggle label="允许视频通话" description="关闭后仍可发起语音通话。" checked={form.videoCallsEnabled} onChange={(value) => change('videoCallsEnabled', value)} /></section>
      <section className="settings-section"><div className="settings-title"><ShieldAlert size={20} /><div><h2>风控与审核</h2><p>敏感词开关作用于新文本消息，审核时限用于运营 SLA。</p></div></div><Toggle label="启用敏感词拦截" description="关闭后词库保留，但不会拦截新文本消息。" checked={form.sensitiveWordEnabled} onChange={(value) => change('sensitiveWordEnabled', value)} /><div className="form-grid"><label className="field-label">举报处理时限（小时）<input type="number" min="1" max="168" value={form.reportSlaHours} onChange={(event) => change('reportSlaHours', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section danger-zone"><div className="settings-title"><LockKeyhole size={20} /><div><h2>维护模式</h2><p>仅在版本升级或紧急故障处理时启用。</p></div></div><Toggle label="启用维护模式" description="启用后，仅管理员账号可以登录；用户将看到维护公告。" checked={form.maintenanceMode} onChange={(value) => change('maintenanceMode', value)} /><label className="field-label">维护公告<textarea value={form.announcement} onChange={(event) => change('announcement', event.target.value)} placeholder="预计完成时间、影响范围和客服联系方式" required={form.maintenanceMode} /></label></section>
      <section className="settings-section"><div className="settings-title"><Database size={20} /><div><h2>基础设施与密钥状态</h2><p>密钥永不回显；下列业务服务参数只读，修改环境变量后必须滚动重启。WuKongIM 连接容量在节点管理页查看。</p></div></div><div className="configuration-grid">{statuses.map(([label, configured]) => <div className="configuration-item" key={label}><span>{label}</span><Badge value={configured ? 'active' : 'failed'} label={configured ? '已配置' : '未配置'} /></div>)}</div><div className="infra-grid"><div><span>推送通道</span><strong>{form.infrastructure.pushProvider}</strong></div><div><span>文件上限</span><strong>{form.infrastructure.mediaMaxSizeMB} MB</strong></div><div><span>呼叫等待</span><strong>{form.infrastructure.callInviteTimeoutSeconds} 秒</strong></div><div><span>访问令牌</span><strong>{form.infrastructure.accessTokenMinutes} 分钟</strong></div><div><span>刷新令牌</span><strong>{form.infrastructure.refreshTokenHours} 小时</strong></div></div><div className="restart-note"><RefreshCcw size={14} />以上参数修改环境变量后需要重启服务，不会由本页面直接写入。</div></section>
    </div><aside className="settings-save"><h2>发布业务策略</h2><p>保存后服务端逐项校验并记录审计日志；热更新项无需重启。</p><div className="save-check"><CheckCircle2 size={17} /><span>数值范围与类型校验已启用</span></div><button className="button primary full" type="submit" disabled={saving || !can('settings.write')}>{saving ? '正在保存…' : <><Save size={16} />保存并立即生效</>}</button>{!can('settings.write') && <p className="permission-note">当前角色没有设置发布权限。</p>}</aside>
  </form>}<ConfirmDialog open={confirming} title="发布系统业务策略" detail="设置保存后会立即影响新请求；基础设施只读项不会被修改。" confirmLabel="确认发布" danger={Boolean(form?.maintenanceMode)} confirmDisabled={saving || !reason.trim()} onClose={() => { setConfirming(false); setReason(''); }} onConfirm={save}><label className="field-label">发布理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} placeholder="填写变更单、运营策略或维护窗口原因" required /></label></ConfirmDialog></>;
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
