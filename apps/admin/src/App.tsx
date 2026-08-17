import { Component, ErrorInfo, FormEvent, ReactNode, createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  Activity, AlertTriangle, Bell, BookOpenCheck, Check, CheckCircle2, ChevronLeft, ChevronRight, Eye, EyeOff,
  CircleUserRound, Database, FileClock, Flag, Group, HardDrive, HeartPulse, LayoutDashboard, LockKeyhole,
  LogIn, LogOut, Menu, MessageSquareText, MoreHorizontal, PhoneCall, Plus, RefreshCcw, Save, Search,
  Server, Settings, ShieldAlert, ShieldCheck, Trash2, Users, Wifi, X,
} from 'lucide-react';
import { ApiError, getApi, loginAdmin } from './api';
import type {
  AdminApi, AdministratorRecord, AdministratorRoleRecord, AdminClientDeviceRecord, AdminDirectMessageRecord, AdminSession, AdminSettings, AdminUserBlockRecord, AdminUserDeviceRecord, AdminUserRelationRecord, AnnouncementInput, AnnouncementRecord, AuditLog, CallRecord, ClientPlatform, ClientVersionPolicy, ClientVersionReleaseRecord, DashboardData, GroupMemberRecord, GroupOverview, GroupRecord,
  FeedbackRecord, FriendshipRecord, HealthService, MediaRecord, MessageRecord, MomentModerationRecord, OnlineRecord, OperationsStatus, PageResult, ReportRecord, ReportResolutionAction, SensitiveWord, StatusTone, StickerPackModerationRecord, UserRecord,
  StickerCategoryInput, StickerCategoryOperationsRecord, StickerItemInput, StickerPackInput,
  LiveKitMetrics, LiveKitParticipant, LiveKitRoom, WukongChannel, WukongConnection, WukongDevice, WukongNode, WukongPlugin, WukongPluginEvent, WukongPluginLogEntry, WukongRobotMenu, WukongRobotProfile, WukongStoredMessage, WukongSystemUser,
  BusinessChannelInput, BusinessChannelMemberRecord, BusinessChannelRecord, BusinessChannelAccessRecord,
  SupportAgentRecord, SupportSessionRecord, SupportSkillRecord, UserOverview,
} from './types';

type Permission = 'users.write' | 'groups.write' | 'reports.write' | 'rules.write' | 'announcements.write' | 'settings.write' | 'versions.write' | 'content.write' | 'channels.write' | 'operations.write' | 'support.write';
type Notice = { id: number; tone: 'success' | 'danger'; message: string };
type PendingExit = { kind: 'navigate'; to: string } | { kind: 'logout' };
type ApiContextValue = {
  api: AdminApi;
  mode: 'live';
  session: AdminSession;
  logout: () => void;
  notify: (message: string, tone?: Notice['tone']) => void;
  can: (permission: Permission) => boolean;
};

const ApiContext = createContext<ApiContextValue | null>(null);
const UnsavedChangesContext = createContext<((message?: string) => void) | null>(null);
const InfrastructureUnsavedChangesContext = createContext<((message?: string) => void) | null>(null);
const SESSION_KEY = 'qingwaguagua_admin_session';
const LEGACY_SESSION_KEY = 'nexachat_admin_session';
const roleLabels: Record<string, string> = { platform_admin: '平台管理员', system_operator: '系统运维', moderator: '内容审核员', content_operator: '内容运营', support_agent: '客服坐席', support: '只读支持' };
const permissionLabels: Record<string, string> = {
  'users.write': '用户处置',
  'groups.write': '群组处置',
  'reports.write': '举报审核',
  'rules.write': '内容规则',
  'announcements.write': '公告运营',
  'settings.write': '系统策略',
  'versions.write': '版本发布',
  'content.write': '内容审核',
  'channels.write': '频道运营',
  'operations.write': '运维操作',
  'support.write': '客服管理',
};
const adminSourceLabels: Record<string, string> = {
  bootstrap: '启动配置',
  database: '后台创建',
  env: '环境配置',
};
const feedbackCategoryLabels: Record<string, string> = {
  bug: '故障',
  feature: '建议',
  abuse: '举报补充',
  other: '其他',
};
const reportCategoryLabels: Record<string, string> = {
  spam: '垃圾信息', fraud: '欺诈风险', harassment: '骚扰辱骂', pornography: '色情内容',
  violence: '暴力内容', privacy: '侵犯隐私', illegal: '违法内容', other: '其他举报',
};
const momentVisibilityLabels: Record<string, string> = {
  public: '所有好友', friends: '所有好友', private: '仅自己', custom: '部分好友', selected: '部分好友',
};
const momentMediaKindLabels: Record<string, string> = { none: '纯文字', image: '图片', video: '视频' };

function localizedEnum(value: string, labels: Record<string, string>, fallback: string) {
  if (labels[value]) return labels[value];
  return /[\u3400-\u9fff]/.test(value) ? value : fallback;
}

function useApi() {
  const value = useContext(ApiContext);
  if (!value) throw new Error('ApiContext is missing');
  return value;
}

function errorMessage(cause: unknown) {
  if (cause instanceof ApiError) return cause.requestId ? `${cause.message}（追踪号：${cause.requestId}）` : cause.message;
  if (cause instanceof Error) {
    const message = cause.message.trim();
    if (/[\u3400-\u9fff]/.test(message)) return message;
    if (/failed to fetch|networkerror|network request failed|load failed/i.test(message)) return '无法连接服务，请检查网络或服务状态';
    if (cause instanceof SyntaxError) return '服务返回的数据格式异常，请稍后重试';
  }
  return '操作失败，请稍后重试';
}

function useUnsavedChanges(active: boolean, message: string) {
  const setMessage = useContext(UnsavedChangesContext);
  if (!setMessage) throw new Error('UnsavedChangesContext is missing');
  useEffect(() => { setMessage(active ? message : undefined); return () => setMessage(undefined); }, [active, message, setMessage]);
  useEffect(() => {
    if (!active) return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ''; };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [active]);
}

function useInfrastructureUnsavedChanges(active: boolean, message: string) {
  const setMessage = useContext(InfrastructureUnsavedChangesContext);
  if (!setMessage) throw new Error('InfrastructureUnsavedChangesContext is missing');
  useEffect(() => { setMessage(active ? message : undefined); return () => setMessage(undefined); }, [active, message, setMessage]);
}

function usePath(shouldLeave?: () => boolean) {
  const [path, setPath] = useState(window.location.pathname);
  const pathRef = useRef(path);
  const shouldLeaveRef = useRef(shouldLeave);
  const bypassNextPop = useRef(false);
  shouldLeaveRef.current = shouldLeave;
  useEffect(() => {
    const update = () => {
      const next = window.location.pathname;
      if (next === pathRef.current) return;
      if (!bypassNextPop.current && shouldLeaveRef.current && !shouldLeaveRef.current()) {
        window.history.pushState({}, '', pathRef.current);
        return;
      }
      bypassNextPop.current = false;
      pathRef.current = next;
      setPath(next);
    };
    window.addEventListener('popstate', update);
    return () => window.removeEventListener('popstate', update);
  }, []);
  const navigate = (next: string, options: { force?: boolean } = {}) => {
    if (options.force) bypassNextPop.current = true;
    if (window.location.pathname !== next) window.history.pushState({}, '', next);
    window.dispatchEvent(new PopStateEvent('popstate'));
  };
  return { path, navigate };
}

function useCompactNavigation() {
  const query = '(max-width: 880px)';
  const [compact, setCompact] = useState(() => window.matchMedia?.(query).matches ?? false);
  useEffect(() => {
    if (!window.matchMedia) return;
    const media = window.matchMedia(query);
    const update = () => setCompact(media.matches);
    update();
    media.addEventListener?.('change', update);
    return () => media.removeEventListener?.('change', update);
  }, []);
  return compact;
}

function AppLink({ to, currentPath, navigate, className = '', children }: { to: string; currentPath: string; navigate: (to: string) => void; className?: string; children: ReactNode }) {
  return <a href={to} className={`${className} ${currentPath === to ? 'active' : ''}`.trim()} aria-current={currentPath === to ? 'page' : undefined} onClick={(event) => { if (event.button === 0 && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) { event.preventDefault(); navigate(to); } }}>{children}</a>;
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
  info: { label: '信息', tone: 'info' }, warning: { label: '需关注', tone: 'warning' }, neutral: { label: '状态', tone: 'neutral' },
  muted: { label: '全员禁言', tone: 'warning' }, dissolved: { label: '已解散', tone: 'neutral' }, pending: { label: '待审核', tone: 'warning' },
  reviewing: { label: '审核中', tone: 'info' }, resolved: { label: '已处理', tone: 'success' }, rejected: { label: '未违规', tone: 'neutral' },
  healthy: { label: '运行正常', tone: 'success' }, degraded: { label: '性能下降', tone: 'warning' }, down: { label: '服务中断', tone: 'danger' },
  success: { label: '成功', tone: 'success' }, failed: { label: '失败', tone: 'danger' }, unknown: { label: '状态未知', tone: 'warning' },
  draft: { label: '草稿', tone: 'neutral' }, scheduled: { label: '定时发布', tone: 'info' }, published: { label: '已发布', tone: 'success' }, withdrawn: { label: '已撤回', tone: 'neutral' },
  hidden: { label: '已隐藏', tone: 'warning' }, deleted: { label: '已删除', tone: 'danger' }, disabled: { label: '已停用', tone: 'neutral' },
  invited: { label: '呼叫中', tone: 'info' }, accepted: { label: '通话中', tone: 'success' }, cancelled: { label: '已取消', tone: 'neutral' }, ended: { label: '已结束', tone: 'neutral' }, missed: { label: '未接听', tone: 'warning' },
  queued: { label: '排队中', tone: 'warning' }, transferring: { label: '转接中', tone: 'info' }, available: { label: '可接待', tone: 'success' }, busy: { label: '忙碌', tone: 'warning' }, away: { label: '暂离', tone: 'neutral' }, offline: { label: '离线', tone: 'neutral' },
};
const metricToneLabels: Record<StatusTone, string> = { success: '正常', info: '实时', warning: '需关注', danger: '异常', neutral: '状态' };

function Badge({ value, label }: { value: string; label?: string }) {
  const item = statusMap[value] ?? { label: label ?? value, tone: 'neutral' as const };
  return <span className={`badge badge-${item.tone}`}><span className="status-dot" />{label ?? item.label}</span>;
}

function UserAvatar({ user, detail = false }: { user: UserRecord; detail?: boolean }) {
  return <span className={`avatar ${detail ? 'detail-avatar' : ''}`}>
    <span className="avatar-fallback">{user.avatar}</span>{user.avatarUrl && <img src={user.avatarUrl} alt="" onError={(event) => { event.currentTarget.hidden = true; }} />}
  </span>;
}

function UserIdentity({ user, fallbackId }: { user?: UserRecord; fallbackId: string }) {
  return <div className="identity">{user ? <UserAvatar user={user} /> : <span className="avatar"><span className="avatar-fallback">?</span></span>}<div><strong>{user?.nickname || '未知用户'}</strong><small className="mono">{user?.id || fallbackId}</small><small>{user?.phone || '手机号未记录'}</small></div></div>;
}

function tabListKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
  const tabs = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]:not(:disabled)')];
  if (!tabs.length) return;
  const current = Math.max(0, tabs.indexOf(event.target as HTMLButtonElement));
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : event.key === 'ArrowRight' ? (current + 1) % tabs.length : (current - 1 + tabs.length) % tabs.length;
  event.preventDefault();
  tabs[next].focus();
  tabs[next].click();
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

function EmptyState({ title, detail, icon = <Search size={24} /> }: { title: string; detail: string; icon?: ReactNode }) {
  return <div className="state-box"><span className="state-icon" aria-hidden="true">{icon}</span><div><strong>{title}</strong><p>{detail}</p></div></div>;
}

function contextualEmptyIcon(title: string) {
  if (title.includes('指标')) return <Activity size={24} />;
  if (/运行日志|生命周期事件|审计记录/.test(title)) return <FileClock size={24} />;
  if (title.includes('插件')) return <Settings size={24} />;
  if (title.includes('敏感词')) return <ShieldAlert size={24} />;
  if (/举报|名单|队列已处理完/.test(title)) return <ShieldCheck size={24} />;
  if (title.includes('公告')) return <Bell size={24} />;
  if (title.includes('媒体文件')) return <HardDrive size={24} />;
  if (/消息|反馈|客服会话|朋友圈/.test(title)) return <MessageSquareText size={24} />;
  if (/通话|房间/.test(title)) return <PhoneCall size={24} />;
  if (/在线|连接/.test(title)) return <Wifi size={24} />;
  if (title.includes('设备')) return <Server size={24} />;
  if (/群组|频道|技能组/.test(title)) return <Group size={24} />;
  if (/成员|好友关系/.test(title)) return <Users size={24} />;
  if (/用户|账号|坐席/.test(title)) return <CircleUserRound size={24} />;
  if (title.includes('表情包')) return <BookOpenCheck size={24} />;
  return <Search size={24} />;
}

function ConfirmDialog({ open, title, detail, confirmLabel, danger = false, confirmDisabled = false, discardConfirmation, onClose, onConfirm, children }: { open: boolean; title: string; detail: string; confirmLabel: string; danger?: boolean; confirmDisabled?: boolean; discardConfirmation?: { title: string; detail: string }; onClose: () => void; onConfirm: () => Promise<void> | void; children?: ReactNode }) {
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const [confirmingDiscard, setConfirmingDiscard] = useState(false);
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
  useEffect(() => { if (open) { setError(''); setConfirmingDiscard(false); } }, [open]);
  useEffect(() => {
    if (!confirmingDiscard) return;
    window.setTimeout(() => dialogRef.current?.querySelector<HTMLElement>('[data-discard-initial-focus]')?.focus());
  }, [confirmingDiscard]);
  if (!open) return null;
  const requestClose = () => {
    if (submitting) return;
    if (discardConfirmation) { setConfirmingDiscard(true); return; }
    onClose();
  };
  const continueEditing = () => {
    setConfirmingDiscard(false);
    window.setTimeout(() => dialogRef.current?.querySelector<HTMLElement>('[aria-label="关闭"]')?.focus());
  };
  const confirm = async () => {
    setSubmitting(true); setError('');
    try { await onConfirm(); onClose(); } catch (cause) { setError(errorMessage(cause)); } finally { setSubmitting(false); }
  };
  const keyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Escape' && !submitting) { event.preventDefault(); if (confirmingDiscard) continueEditing(); else requestClose(); return; }
    if (event.key !== 'Tab') return;
    const focusable = [...(dialogRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [href]') ?? [])];
    if (!focusable.length) return;
    const first = focusable[0], last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };
  return createPortal(<div className="modal-backdrop" role="presentation" onMouseDown={(event) => !danger && !submitting && event.target === event.currentTarget && !confirmingDiscard && requestClose()}>
    <div ref={dialogRef} className="modal" role="dialog" aria-modal="true" aria-labelledby="dialog-title" aria-describedby="dialog-detail" onKeyDown={keyDown}>
      {confirmingDiscard && discardConfirmation ? <>
        <div className="modal-icon danger"><ShieldAlert size={22} /></div>
        <h2 id="dialog-title">{discardConfirmation.title}</h2><p id="dialog-detail">{discardConfirmation.detail}</p>
        <div className="modal-actions"><button data-discard-initial-focus className="button secondary" onClick={continueEditing}>继续编辑</button><button className="button danger" onClick={onClose}>放弃修改</button></div>
      </> : <>
        <button className="icon-button modal-close" aria-label="关闭" onClick={requestClose} disabled={submitting}><X size={18} /></button>
        <div className={`modal-icon ${danger ? 'danger' : ''}`}>{danger ? <ShieldAlert size={22} /> : <BookOpenCheck size={22} />}</div>
        <h2 id="dialog-title">{title}</h2><p id="dialog-detail">{detail}</p>{children}
        {error && <div className="inline-notice danger" role="alert"><AlertTriangle size={15} />{error}</div>}
        <div className="modal-actions"><button data-initial-focus={danger ? '' : undefined} className="button secondary" onClick={requestClose} disabled={submitting}>取消</button><button className={`button ${danger ? 'danger' : 'primary'}`} onClick={() => void confirm()} disabled={submitting || confirmDisabled}>{submitting ? '正在处理…' : confirmLabel}</button></div>
      </>}
    </div>
  </div>, document.body);
}

function DetailDialog({ title, detail, onClose, children }: { title: string; detail: string; onClose: () => void; children: ReactNode }) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const previousFocus = useRef<HTMLElement | null>(null);
  useEffect(() => {
    previousFocus.current = document.activeElement as HTMLElement;
    const root = document.getElementById('root');
    if (root) { root.inert = true; root.setAttribute('aria-hidden', 'true'); }
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    window.setTimeout(() => dialogRef.current?.querySelector<HTMLElement>('[data-initial-focus]')?.focus());
    return () => {
      if (root) { root.inert = false; root.removeAttribute('aria-hidden'); }
      document.body.style.overflow = previousOverflow;
      previousFocus.current?.focus();
    };
  }, []);
  const keyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Escape') { event.preventDefault(); onClose(); return; }
    if (event.key !== 'Tab') return;
    const focusable = [...(dialogRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), [href]') ?? [])];
    if (!focusable.length) return;
    const first = focusable[0], last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };
  return createPortal(<div className="modal-backdrop detail-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
    <div ref={dialogRef} className="modal detail-modal" role="dialog" aria-modal="true" aria-labelledby="detail-dialog-title" aria-describedby="detail-dialog-description" onKeyDown={keyDown}>
      <button data-initial-focus className="icon-button modal-close" aria-label="关闭详情" onClick={onClose}><X size={18} /></button>
      <div className="detail-modal-heading"><div className="modal-icon"><BookOpenCheck size={22} /></div><div><h2 id="detail-dialog-title">{title}</h2><p id="detail-dialog-description">{detail}</p></div></div>
      <div className="detail-modal-body">{children}</div>
    </div>
  </div>, document.body);
}

function UserRelationTable({ items, empty, onMessages }: { items: AdminUserRelationRecord[]; empty: string; onMessages: (user: UserRecord) => void }) {
  if (!items.length) return <EmptyState icon={<Users size={24} />} title={empty} detail="列表来自当前服务端业务关系数据。" />;
  return <div className="table-wrap detail-table"><table><thead><tr><th>用户</th><th>备注 / 标签</th><th>成为好友</th><th>最近更新</th><th><span className="sr-only">操作</span></th></tr></thead><tbody>{items.map((relation) => { const item = relation.user; return <tr key={item.id}><td><div className="identity"><UserAvatar user={item} /><div><strong>{item.nickname}</strong><small className="mono">{item.handle || item.id}</small><small>{item.phone}</small></div></div></td><td><strong>{relation.remark || '未设置备注'}</strong><small>{relation.tags.length ? relation.tags.join('、') : '无标签'}</small></td><td>{relation.relationshipCreatedAt}</td><td>{relation.relationshipUpdatedAt}</td><td><button type="button" className="button secondary compact" onClick={() => onMessages(item)}>聊天记录</button></td></tr>; })}</tbody></table></div>;
}

function UserBlockTable({ items, empty }: { items: AdminUserBlockRecord[]; empty: string }) {
  if (!items.length) return <EmptyState icon={<ShieldAlert size={24} />} title={empty} detail="列表来自当前服务端黑名单数据。" />;
  return <div className="table-wrap detail-table"><table><thead><tr><th>用户</th><th>拉黑前好友备注</th><th>拉黑时间</th><th>状态</th></tr></thead><tbody>{items.map(({ user: item, remark, blockedAt }) => <tr key={item.id}><td><div className="identity"><UserAvatar user={item} /><div><strong>{item.nickname}</strong><small className="mono">{item.handle || item.id}</small><small>{item.phone}</small></div></div></td><td>{remark || '历史数据未记录'}</td><td>{blockedAt}</td><td><Badge value={item.status} /></td></tr>)}</tbody></table></div>;
}

function messageBodySummary(message: AdminDirectMessageRecord) {
  const body = message.body;
  for (const key of ['content', 'text', 'title', 'name']) {
    const value = body[key];
    if (typeof value === 'string' && value.trim()) return value;
  }
  return `消息类型 ${message.type}`;
}

function messageMediaURL(message: AdminDirectMessageRecord) {
  for (const key of ['downloadUrl', 'url', 'fileUrl', 'imageUrl', 'videoUrl']) {
    const value = message.body[key];
    if (typeof value === 'string' && /^https?:\/\//i.test(value)) return value;
  }
  return '';
}

function FriendConversationDialog({ owner, friend, onClose }: { owner: UserRecord; friend: UserRecord; onClose: () => void }) {
  const { api, mode, notify } = useApi();
  const [beforeSeq, setBeforeSeq] = useState(0); const [cursorHistory, setCursorHistory] = useState<number[]>([]);
  const [recalling, setRecalling] = useState<AdminDirectMessageRecord>(); const [reason, setReason] = useState('');
  const messages = useResource(() => api.getUserFriendMessages(owner.id, friend.id, beforeSeq, 50), [api, mode, owner.id, friend.id, beforeSeq]);
  const items = [...(messages.data?.items ?? [])].sort((left, right) => left.conversationSeq - right.conversationSeq);
  const loadEarlier = () => { if (!messages.data?.nextBeforeSeq) return; setCursorHistory((current) => [...current, beforeSeq]); setBeforeSeq(messages.data.nextBeforeSeq ?? 0); };
  const loadNewer = () => setCursorHistory((current) => { const next = [...current]; setBeforeSeq(next.pop() ?? 0); return next; });
  const recall = async () => { if (!recalling || !reason.trim()) throw new Error('请输入管理员撤回理由'); await api.recallUserFriendMessage(owner.id, friend.id, recalling.id, reason.trim()); notify('消息已全端撤回并写入审计'); await messages.reload(); setRecalling(undefined); setReason(''); };
  return <><DetailDialog title={`${owner.nickname} 与 ${friend.nickname} 的聊天记录`} detail="正文从 WuKongIM 历史接口实时读取；查看和管理员撤回都会写入审计。" onClose={onClose}>
    {messages.loading && <Skeleton rows={6} />}
    {messages.error && <ErrorState message={messages.error} retry={messages.reload} />}
    {messages.data && !items.length && <EmptyState icon={<MessageSquareText size={24} />} title="该好友会话暂无消息" detail="后台查询不会为两名用户创建空会话。" />}
    {!!items.length && <div className="conversation-history">{items.map((message) => { const mediaURL = messageMediaURL(message); const unavailable = Boolean(message.recalledAt || message.expiredAt || message.deleted || message.adminRecall); return <article className="conversation-message" key={message.id}><UserAvatar user={message.sender ?? (message.senderId === owner.id ? owner : friend)} /><div className="conversation-message-main"><header><strong>{message.sender?.nickname || message.senderId}</strong><span>序号 {message.conversationSeq} · {message.createdAt}</span></header><div className={`conversation-message-body ${unavailable ? 'muted' : ''}`}>{message.adminRecall || message.deleted ? '该消息已被管理员撤回' : message.recalledAt ? '该消息已撤回' : message.expiredAt ? '该消息已过期' : messageBodySummary(message)}</div>{mediaURL && !unavailable && <a className="conversation-attachment" href={mediaURL} target="_blank" rel="noreferrer">查看附件</a>}{message.editedAt && !unavailable && <small>已编辑 · 版本 {message.editVersion}</small>}{message.adminRecall && <small>处置人：{message.moderatedBy || '管理员'} · {message.moderationReason || '未提供理由'}</small>}</div><button type="button" className="button secondary compact danger-text" disabled={unavailable} title={unavailable ? '已撤回或过期的消息不能再次处置' : '管理员全端撤回'} onClick={() => { setReason(''); setRecalling(message); }}>撤回</button></article>; })}</div>}
    {messages.data && <div className="conversation-pagination"><button type="button" className="button secondary compact" disabled={!cursorHistory.length} onClick={loadNewer}>返回较新消息</button><span>{items.length ? `当前显示 ${items.length} 条` : '无消息'}</span><button type="button" className="button secondary compact" disabled={!messages.data.nextBeforeSeq} onClick={loadEarlier}>加载更早消息</button></div>}
  </DetailDialog>
  <ConfirmDialog open={Boolean(recalling)} title="管理员撤回消息" detail={recalling ? `将全端撤回序号 ${recalling.conversationSeq} 的消息，WuKongIM 原始消息不会物理删除。` : ''} confirmLabel="确认全端撤回" danger confirmDisabled={!reason.trim()} onClose={() => { setRecalling(undefined); setReason(''); }} onConfirm={recall}><label className="field-label">撤回理由<textarea autoFocus value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} placeholder="填写违规事实、工单或处置依据" required /></label></ConfirmDialog></>;
}

function UserDetailDialog({ user, onClose }: { user: UserRecord; onClose: () => void }) {
  const { api, mode } = useApi();
  const [section, setSection] = useState<'profile' | 'friends' | 'blocks' | 'devices'>('profile');
  const [chatFriend, setChatFriend] = useState<UserRecord>();
  const state = useResource(() => api.getUserOverview(user.id), [api, mode, user.id]);
  const friends = useResource(() => api.getUserFriends(user.id), [api, mode, user.id]);
  const blocks = useResource(() => api.getUserBlockedUsers(user.id), [api, mode, user.id]);
  const devices = useResource(() => api.getUserDevices(user.id), [api, mode, user.id]);
  const overview: UserOverview | undefined = state.data;
  const currentUser = overview?.user ?? user;
  if (chatFriend) return <FriendConversationDialog owner={currentUser} friend={chatFriend} onClose={() => setChatFriend(undefined)} />;
  return <DetailDialog title="用户详情" detail="账号资料、关系、黑名单与设备均来自服务端实时查询。" onClose={onClose}>
    {state.loading && <Skeleton rows={5} />}
    {state.error && <ErrorState message={state.error} retry={state.reload} />}
    {overview && <><div className="detail-identity"><UserAvatar user={currentUser} detail /><div><h3>{currentUser.nickname}</h3><p className="mono">{currentUser.id}</p></div><div className="detail-user-status"><Badge value={currentUser.status} /><Badge value={currentUser.online ? 'active' : 'neutral'} label={currentUser.online ? `在线 · ${currentUser.onlineConnections || 1} 个连接` : '离线'} /></div></div>
      <div className="detail-metrics"><div><span>设备</span><strong>{overview.deviceCount}</strong></div><div><span>好友</span><strong>{overview.friendCount}</strong></div><div><span>加入群组</span><strong>{overview.groupCount}</strong></div><div><span>呱呱号剩余修改</span><strong>{overview.handleChangesRemaining}</strong></div></div>
      <div className="tabs detail-tabs" role="tablist" aria-label="用户详情分类">{[
        ['profile', '账号资料'], ['friends', `好友 ${overview.friendCount}`], ['blocks', `黑名单 ${blocks.data?.length ?? 0}`], ['devices', `设备 ${(devices.data?.items.length ?? 0) + (devices.data?.pushRegistrations.length ?? 0)}`],
      ].map(([value, label]) => <button type="button" role="tab" aria-selected={section === value} className={section === value ? 'active' : ''} key={value} onClick={() => setSection(value as typeof section)}>{label}</button>)}</div>
      {section === 'profile' && <dl className="detail-list user-profile-list"><div><dt>呱呱号</dt><dd className="mono">{currentUser.handle}</dd></div><div><dt>手机号</dt><dd>{currentUser.phone}</dd></div><div><dt>性别</dt><dd>{overview.gender === 'male' ? '男' : overview.gender === 'female' ? '女' : '未展示'}</dd></div><div><dt>在线状态</dt><dd>{currentUser.online ? `当前在线（${currentUser.onlineConnections || 1} 个连接）` : '当前离线'}</dd></div><div><dt>最后离线</dt><dd>{currentUser.lastOfflineAt ? dateTimeLabel(currentUser.lastOfflineAt) : '暂无记录'}</dd></div><div><dt>注册时间</dt><dd>{currentUser.registeredAt}</dd></div><div><dt>呱呱号修改记录</dt><dd>已修改 {overview.handleChangesUsed} 次，剩余 {overview.handleChangesRemaining} 次</dd></div><div><dt>个性签名</dt><dd>{overview.signature || '未设置'}</dd></div>{currentUser.bannedUntil && <div className="detail-list-wide"><dt>封禁截止</dt><dd>{dateTimeLabel(currentUser.bannedUntil)}</dd></div>}</dl>}
      {section === 'friends' && <section className="detail-section user-detail-section"><div className="detail-section-heading"><div><h3>好友列表</h3><p>展示好友备注、标签、建立时间，并可审计查看聊天正文。</p></div></div>{friends.loading && <Skeleton rows={4} />}{friends.error && <ErrorState message={friends.error} retry={friends.reload} />}{friends.data && <UserRelationTable items={friends.data} empty="该用户暂无好友" onMessages={setChatFriend} />}</section>}
      {section === 'blocks' && <section className="detail-section user-detail-section"><div className="detail-section-heading"><div><h3>黑名单</h3><p>展示拉黑时间以及删除好友关系前保留的备注快照。</p></div></div>{blocks.loading && <Skeleton rows={4} />}{blocks.error && <ErrorState message={blocks.error} retry={blocks.reload} />}{blocks.data && <UserBlockTable items={blocks.data} empty="该用户黑名单为空" />}</section>}
      {section === 'devices' && <section className="detail-section user-detail-section"><div className="detail-section-heading"><div><h3>登录设备</h3><p>新版客户端登录或恢复会话后上报的真实设备信息。</p></div></div>{devices.loading && <Skeleton rows={4} />}{devices.error && <ErrorState message={devices.error} retry={devices.reload} />}{devices.data && !devices.data.items.length && <EmptyState icon={<CircleUserRound size={24} />} title="暂无登录设备" detail="升级后的客户端完成登录后会逐步显示在这里。" />}{!!devices.data?.items.length && <div className="table-wrap detail-table"><table><thead><tr><th>平台 / 设备</th><th>型号</th><th>系统</th><th>App 版本</th><th>首次 / 最近活跃</th></tr></thead><tbody>{devices.data.items.map((device: AdminClientDeviceRecord) => <tr key={device.installationId}><td><strong>{device.platform}</strong><small>{device.deviceName}</small><small className="mono">{device.installationId}</small></td><td>{device.deviceModel}</td><td>{device.osVersion}</td><td>{device.appVersion}</td><td>{device.firstSeenAt}<small>最近：{device.lastSeenAt}</small></td></tr>)}</tbody></table></div>}<div className="detail-section-heading device-subsection"><div><h3>推送登记</h3><p>仅展示通知配置，不返回推送 Token。</p></div></div>{devices.data && !devices.data.pushRegistrations.length && <EmptyState icon={<Bell size={24} />} title="暂无推送登记" detail="用户允许通知并完成推送登记后会显示在这里。" />}{!!devices.data?.pushRegistrations.length && <div className="table-wrap detail-table"><table><thead><tr><th>平台 / 登记</th><th>推送服务</th><th>通知</th><th>预览 / 声音 / 振动</th><th>最近更新</th></tr></thead><tbody>{devices.data.pushRegistrations.map((device: AdminUserDeviceRecord) => <tr key={device.id}><td><strong>{device.platform}</strong><small className="mono">{device.id}</small></td><td>{device.provider}</td><td><Badge value={device.notificationsEnabled ? 'active' : 'neutral'} label={device.notificationsEnabled ? '已开启' : '已关闭'} /></td><td>{device.previewEnabled ? '预览' : '不预览'} / {device.soundEnabled ? '声音' : '静音'} / {device.vibrationEnabled ? '振动' : '不振动'}</td><td>{device.updatedAt}</td></tr>)}</tbody></table></div>}</section>}
    </>}
  </DetailDialog>;
}

const groupRoleLabels: Record<string, string> = { owner: '群主', admin: '管理员', member: '成员' };
const joinPolicyLabels: Record<string, string> = { open: '允许直接加入', approval: '需要群主或管理员确认', invite: '仅允许邀请加入', closed: '暂停加入' };
type GroupMemberModeration = { member: GroupMemberRecord; title: string; detail: string; confirmLabel: string; danger?: boolean; run: (reason: string) => Promise<void> };
function GroupDetailDialog({ group, onClose }: { group: GroupRecord; onClose: () => void }) {
  const { api, mode, notify, can } = useApi();
  const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query); const [page, setPage] = useState(1); const [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  const [moderation, setModeration] = useState<GroupMemberModeration>(); const [reason, setReason] = useState('');
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery]);
  const overview = useResource(() => api.getGroupOverview(group.id), [api, mode, group.id]);
  const members = useResource(() => api.getGroupMembers(group.id, deferredQuery, page, 20, cursors[page] ?? ''), [api, mode, group.id, deferredQuery, page, cursors]);
  const paginate = (nextPage: number) => { if (nextPage > page && members.data?.nextCursor) setCursors((current) => ({ ...current, [nextPage]: members.data?.nextCursor ?? '' })); setPage(nextPage); };
  const data: GroupOverview | undefined = overview.data;
  const openModeration = (action: GroupMemberModeration) => { setReason(''); setModeration(action); };
  const executeModeration = async () => { if (!moderation || !reason.trim()) throw new Error('请输入处置理由'); await moderation.run(reason.trim()); notify(`${moderation.member.name}：${moderation.confirmLabel}已完成`); setModeration(undefined); setReason(''); await Promise.all([members.reload(), overview.reload()]); };
  const roleAction = (member: GroupMemberRecord) => openModeration({ member, title: member.role === 'admin' ? '取消群管理员' : '设为群管理员', detail: `${member.name}（${member.userId}）将变更为${member.role === 'admin' ? '普通成员' : '群管理员'}。`, confirmLabel: member.role === 'admin' ? '取消管理员' : '设为管理员', run: (why) => api.updateGroupMember(group.id, member.userId, { action: 'role', role: member.role === 'admin' ? 'member' : 'admin' }, why) });
  const muteAction = (member: GroupMemberRecord) => { const muted = Boolean(member.mutedUntil); openModeration({ member, title: muted ? '解除成员禁言' : '禁言成员一小时', detail: `${member.name}（${member.userId}）${muted ? '将恢复群内发言权限' : '将在一小时内无法发送群消息'}。`, confirmLabel: muted ? '解除禁言' : '确认禁言', danger: !muted, run: (why) => api.updateGroupMember(group.id, member.userId, { action: 'mute', ...(muted ? {} : { mutedUntil: new Date(Date.now() + 3_600_000).toISOString() }) }, why) }); };
  const removeAction = (member: GroupMemberRecord) => openModeration({ member, title: '移出群聊', detail: `${member.name}（${member.userId}）将立即失去群聊访问权限，历史审计记录会保留。`, confirmLabel: '确认移出', danger: true, run: (why) => api.removeGroupMember(group.id, member.userId, why) });
  return <><DetailDialog title="群组详情" detail="群资料、成员与消息统计来自服务端实时查询。" onClose={onClose}>
    {overview.loading && <Skeleton rows={3} />}
    {overview.error && <ErrorState message={overview.error} retry={overview.reload} />}
    {data && <><div className="detail-identity"><span className="group-detail-icon"><Users size={20} /></span><div><h3>{data.title}</h3><p className="mono">{data.id}</p></div><Badge value={group.status} /></div>
      <div className="detail-metrics"><div><span>成员</span><strong>{data.memberCount}</strong></div><div><span>累计消息</span><strong>{data.messageCount.toLocaleString()}</strong></div><div><span>公告版本</span><strong>{data.announcementVersion}</strong></div></div>
      <dl className="detail-list"><div><dt>群主 ID</dt><dd className="mono">{data.ownerId}</dd></div><div><dt>加入方式</dt><dd>{joinPolicyLabels[data.joinPolicy] ?? data.joinPolicy}</dd></div><div className="detail-list-wide"><dt>成员互加好友</dt><dd>{data.allowMemberAddFriend ? '允许' : '不允许'}</dd></div><div className="detail-list-wide"><dt>群公告</dt><dd>{data.announcement || '尚未设置群公告'}</dd></div></dl></>}
    <section className="detail-section"><div className="detail-section-heading"><div><h3>群成员</h3><p>可按昵称、呱呱号或用户 ID 查找。</p></div><label className="search-field detail-search"><span className="sr-only">搜索群成员</span><Search size={16} /><input aria-label="搜索群成员" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索群成员" /></label></div>
      {members.loading && <Skeleton rows={4} />}
      {members.error && <ErrorState message={members.error} retry={members.reload} />}
      {members.data && !members.data.items.length && <EmptyState icon={<Users size={24} />} title="没有匹配的群成员" detail="调整昵称、呱呱号或用户 ID 后重试。" />}
      {!!members.data?.items.length && <><div className="table-wrap detail-table"><table><thead><tr><th>成员</th><th>角色</th><th>加入时间</th><th>读取进度</th><th><span className="sr-only">成员治理操作</span></th></tr></thead><tbody>{members.data.items.map((member: GroupMemberRecord) => <tr key={member.userId}><td><strong>{member.groupNickname || member.name}</strong><small>{member.groupNickname ? `账号昵称：${member.name}` : `呱呱号：${member.handle}`}</small><small className="mono">{member.userId}</small></td><td><Badge value="active" label={groupRoleLabels[member.role] ?? member.role} />{member.mutedUntil && <small>禁言至 {dateTimeLabel(member.mutedUntil)}</small>}</td><td>{member.joinedAt}</td><td>{member.lastReadSeq.toLocaleString()} / {member.lastDeliveredSeq.toLocaleString()}</td><td><div className="row-actions wrap">{member.role !== 'owner' && <><button className="button secondary compact" disabled={!can('groups.write')} onClick={() => roleAction(member)}>{member.role === 'admin' ? '取消管理员' : '设为管理员'}</button><button className="button secondary compact" disabled={!can('groups.write')} onClick={() => muteAction(member)}>{member.mutedUntil ? '解除禁言' : '禁言 1 小时'}</button><button className="button secondary compact danger-text" disabled={!can('groups.write')} onClick={() => removeAction(member)}>移出群聊</button></>}</div></td></tr>)}</tbody></table></div><Pagination data={members.data} onPage={paginate} /></>}
    </section>
  </DetailDialog><ConfirmDialog open={Boolean(moderation)} title={moderation?.title ?? ''} detail={moderation?.detail ?? ''} confirmLabel={moderation?.confirmLabel ?? '确认处置'} danger={moderation?.danger} confirmDisabled={!reason.trim()} onClose={() => { setModeration(undefined); setReason(''); }} onConfirm={executeModeration}><label className="field-label">处置理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} placeholder="填写违规证据、运营工单或处置依据" required /></label></ConfirmDialog></>;
}

function Pagination({ data, onPage }: { data?: PageResult<unknown>; onPage: (page: number) => void }) {
  if (!data || (data.page <= 1 && !data.hasNext && data.total <= data.pageSize)) return null;
  const pages = Math.max(1, Math.ceil(data.total / data.pageSize));
  return <nav className="pagination" aria-label="分页"><span>第 {data.page} / {pages} 页，共 {data.total} 条</span><div><button className="icon-button" aria-label="上一页" disabled={data.page <= 1} onClick={() => onPage(data.page - 1)}><ChevronLeft size={17} /></button><button className="icon-button" aria-label="下一页" disabled={!data.hasNext} onClick={() => onPage(data.page + 1)}><ChevronRight size={17} /></button></div></nav>;
}

const overviewNavItem = { to: '/overview', label: '运行概览', icon: LayoutDashboard };
type NavigationItem = { to: string; label: string; icon: typeof Users; platformAdminOnly?: boolean };
type NavigationGroup = { id: string; label: string; items: NavigationItem[] };
const navGroups: NavigationGroup[] = [
  {
    id: 'users', label: '用户', items: [
      { to: '/users/new', label: '新增用户', icon: Plus },
      { to: '/users', label: '用户管理', icon: Users },
      { to: '/relationships', label: '关系管理', icon: Users },
      { to: '/feedback', label: '用户反馈', icon: MessageSquareText },
      { to: '/online', label: '在线状态', icon: Wifi },
    ],
  },
  {
    id: 'groups', label: '群组', items: [
      { to: '/groups', label: '群组管理', icon: Group },
      { to: '/business-channels', label: '频道运营', icon: Group },
    ],
  },
  {
    id: 'messages', label: '消息', items: [
      { to: '/messages', label: '消息检索', icon: MessageSquareText },
      { to: '/calls', label: '通话记录', icon: PhoneCall },
      { to: '/support-workbench', label: '客服工作台', icon: MessageSquareText },
      { to: '/announcements', label: '运营公告', icon: Bell },
    ],
  },
  {
    id: 'reports', label: '举报', items: [
      { to: '/reports', label: '举报审核', icon: Flag },
      { to: '/content-moderation', label: '内容审核', icon: ShieldCheck },
      { to: '/sensitive-words', label: '敏感词库', icon: ShieldAlert },
    ],
  },
  {
    id: 'tools', label: '工具', items: [
      { to: '/media', label: '文件存储', icon: HardDrive },
      { to: '/operations', label: '推送与任务', icon: Activity },
      { to: '/client-versions', label: '客户端版本', icon: RefreshCcw },
    ],
  },
  {
    id: 'settings', label: '设置', items: [
      { to: '/administrators', label: '管理员与角色', icon: ShieldCheck, platformAdminOnly: true },
      { to: '/im-infrastructure', label: 'IM 基础设施', icon: Server },
      { to: '/system-health', label: '系统健康', icon: HeartPulse },
      { to: '/audit', label: '审计日志', icon: FileClock },
      { to: '/settings', label: '系统设置', icon: Settings },
    ],
  },
];

function Shell() {
  const { session, logout } = useApi();
  const [navOpen, setNavOpen] = useState(false);
  const [expandedNavGroups, setExpandedNavGroups] = useState<Record<string, boolean>>(() => Object.fromEntries(navGroups.map((group) => [group.id, false])));
  const [unsavedMessage, setUnsavedMessage] = useState<string>();
  const [pendingExit, setPendingExit] = useState<PendingExit>();
  const compactNavigation = useCompactNavigation();
  const sidebarRef = useRef<HTMLElement>(null);
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const previousOverflow = useRef('');
  const { path, navigate } = usePath(() => !unsavedMessage || window.confirm(`${unsavedMessage}，确定离开当前页面吗？`));
  const visibleNavGroups = useMemo(() => navGroups.map((group) => ({ ...group, items: group.items.filter((item) => !item.platformAdminOnly || session.roleId === 'platform_admin') })), [session.roleId]);
  const activeGroupId = visibleNavGroups.find((group) => group.items.some((item) => item.to === path))?.id;
  useEffect(() => {
    if (activeGroupId) setExpandedNavGroups((current) => current[activeGroupId] ? current : { ...current, [activeGroupId]: true });
  }, [activeGroupId]);
  const requestNavigate = (to: string) => {
    if (to === path) return;
    if (unsavedMessage) { setPendingExit({ kind: 'navigate', to }); return; }
    navigate(to);
  };
  const requestLogout = () => {
    if (unsavedMessage) { setPendingExit({ kind: 'logout' }); return; }
    logout();
  };
  const confirmExit = () => {
    const next = pendingExit;
    setPendingExit(undefined);
    setUnsavedMessage(undefined);
    if (next?.kind === 'logout') logout();
    if (next?.kind === 'navigate') navigate(next.to, { force: true });
  };
  useEffect(() => setNavOpen(false), [path]);
  useEffect(() => { if (!compactNavigation) setNavOpen(false); }, [compactNavigation]);
  useEffect(() => {
    const sidebar = sidebarRef.current;
    if (!sidebar) return;
    sidebar.inert = compactNavigation && !navOpen;
    if (!compactNavigation || !navOpen) return;
    previousOverflow.current = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    window.setTimeout(() => sidebar.querySelector<HTMLElement>('[aria-current="page"], a, button')?.focus());
    return () => {
      document.body.style.overflow = previousOverflow.current;
      menuButtonRef.current?.focus();
    };
  }, [compactNavigation, navOpen]);
  const navigationKeyDown = (event: React.KeyboardEvent) => {
    if (!compactNavigation || !navOpen) return;
    if (event.key === 'Escape') { event.preventDefault(); setNavOpen(false); return; }
    if (event.key !== 'Tab') return;
    const focusable = [...(sidebarRef.current?.querySelectorAll<HTMLElement>('a:not([aria-disabled="true"]), button:not(:disabled)') ?? [])];
    if (!focusable.length) return;
    const first = focusable[0], last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };
  const pages: Record<string, ReactNode> = { '/overview': <OverviewPage />, '/users/new': <CreateUserPage />, '/users': <UsersPage />, '/groups': <GroupsPage />, '/reports': <ReportsPage />, '/messages': <MessagesPage />, '/media': <MediaPage />, '/online': <OnlinePage />, '/relationships': <RelationshipsPage />, '/feedback': <FeedbackPage />, '/operations': <OperationsPage />, '/announcements': <AnnouncementsPage />, '/calls': <CallsPage />, '/content-moderation': <ContentModerationPage />, '/business-channels': <BusinessChannelsPage />, '/support-workbench': <SupportWorkbenchPage />, '/im-infrastructure': <ImInfrastructurePage />, '/client-versions': <ClientVersionsPage />, '/sensitive-words': <SensitiveWordsPage />, '/system-health': <HealthPage />, '/audit': <AuditPage />, '/administrators': <AdministratorsPage />, '/change-password': <ChangePasswordPage />, '/settings': <SettingsPage /> };
  useEffect(() => { if (!pages[path]) { window.history.replaceState({}, '', '/overview'); window.dispatchEvent(new PopStateEvent('popstate')); } }, [path]); // eslint-disable-line react-hooks/exhaustive-deps
  return <UnsavedChangesContext.Provider value={setUnsavedMessage}><div className="app-shell">
    {compactNavigation && navOpen && <button aria-label="关闭导航" className="nav-scrim" onClick={() => setNavOpen(false)} />}
    <aside ref={sidebarRef} id="admin-navigation" className={`sidebar ${navOpen ? 'open' : ''}`} aria-label="运营控制台导航" onKeyDown={navigationKeyDown}>
      <div className="brand"><div className="brand-mark"><img src="/qingwaguagua-mark.png" alt="" /></div><div><strong>青蛙呱呱</strong><span>运营控制台</span></div></div>
      <nav aria-label="主导航">
        <AppLink to={overviewNavItem.to} currentPath={path} navigate={requestNavigate} className="nav-item nav-overview"><overviewNavItem.icon size={18} /><span>{overviewNavItem.label}</span></AppLink>
        {visibleNavGroups.map((group) => {
          const expanded = Boolean(expandedNavGroups[group.id]);
          const hasActive = group.id === activeGroupId;
          return <section className="nav-section" key={group.id}>
            <button type="button" className={`nav-section-toggle ${hasActive ? 'has-active' : ''}`} aria-expanded={expanded} aria-controls={`nav-group-${group.id}`} onClick={() => setExpandedNavGroups((current) => ({ ...current, [group.id]: !expanded }))}><span>{group.label}</span><ChevronRight size={15} /></button>
            <div id={`nav-group-${group.id}`} className="nav-section-items" hidden={!expanded}>{group.items.map((item) => <AppLink key={item.to} to={item.to} currentPath={path} navigate={requestNavigate} className="nav-item nav-leaf-item"><span>{item.label}</span></AppLink>)}</div>
          </section>;
        })}
      </nav>
      <div className="sidebar-foot"><div className="admin-avatar">{[...session.displayName][0]}</div><div><strong>{session.displayName}</strong><span>{session.roleName || roleLabels[session.roleId] || session.roleId}</span></div><button className="icon-button sidebar-logout" aria-label="修改密码" title="修改密码" onClick={() => requestNavigate('/change-password')}><LockKeyhole size={17} /></button><button className="icon-button sidebar-logout" aria-label="退出管理后台" title="退出登录" onClick={requestLogout}><LogOut size={17} /></button></div>
    </aside>
    <div className="workspace">
      <div className="topbar">
        <button ref={menuButtonRef} className="icon-button menu-button" aria-label="打开导航" aria-controls="admin-navigation" aria-expanded={navOpen} onClick={() => setNavOpen(true)}><Menu size={20} /></button>
        <div className="topbar-status"><span className="pulse-dot" /><span>服务端实时数据</span></div>
        <div className="topbar-context" aria-label="当前控制台">青蛙呱呱 IM</div>
      </div>
      <main className="main-content">{pages[path] ?? pages['/overview']}</main>
    </div>
  </div><ConfirmDialog open={Boolean(pendingExit)} title="放弃未保存的修改？" detail={`${unsavedMessage ?? '当前页面有未保存内容'}。离开后这些修改不会提交到服务端。`} confirmLabel={pendingExit?.kind === 'logout' ? '放弃修改并退出' : '放弃修改并离开'} danger onClose={() => setPendingExit(undefined)} onConfirm={confirmExit} /></UnsavedChangesContext.Provider>;
}

function TrendChart({ data }: { data: DashboardData['messageTrend'] }) {
  if (data.length < 2) return <div className="chart-empty"><Activity size={20} /><span>暂无可用趋势数据</span></div>;
  if (Math.max(...data.map((point) => point.count)) <= 0) return <div className="chart-empty"><Activity size={20} /><span>最近 12 小时暂无消息</span></div>;
  const width = 700, height = 190, padding = 14, max = Math.max(...data.map((point) => point.count));
  const coordinates = data.map((point, index) => ({ x: padding + index * ((width - padding * 2) / (data.length - 1)), y: height - padding - (point.count / max) * (height - padding * 2), point }));
  return <div className="chart-wrap"><svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label="消息量趋势"><line x1="0" y1="160" x2={width} y2="160" className="grid-line" /><line x1="0" y1="110" x2={width} y2="110" className="grid-line" /><line x1="0" y1="60" x2={width} y2="60" className="grid-line" /><polyline points={coordinates.map(({ x, y }) => `${x},${y}`).join(' ')} className="trend-line" />{coordinates.map(({ x, y, point }) => <circle key={point.time} cx={x} cy={y} r="3" className="trend-point"><title>{point.time}，{point.count} 条</title></circle>)}</svg><div className="chart-labels">{data.filter((_, index) => index % 2 === 0).map((point) => <span key={point.time}>{point.time}</span>)}</div></div>;
}

function channelMixGradient(data: DashboardData['channelMix']) {
  let offset = 0;
  const segments = data.map((item, index) => {
    const start = offset;
    offset += item.value;
    const end = index === data.length - 1 ? 100 : Math.min(100, offset);
    return `${item.color} ${start}% ${end}%`;
  });
  return segments.length ? `conic-gradient(${segments.join(', ')})` : 'none';
}

function OverviewPage() {
  const { api, mode } = useApi();
  const state = useResource(() => api.getDashboard(), [api, mode]);
  return <><PageHeader title="运行概览" description="业务运行、内容风险和系统活动。" actions={<button className="button secondary" onClick={() => void state.reload()}><RefreshCcw size={15} />刷新数据</button>} />
    {state.loading ? <Skeleton rows={6} /> : state.error || !state.data ? <ErrorState message={state.error} retry={state.reload} /> : <div className="dashboard">
      <section className="metric-strip" aria-label="核心指标">{state.data.metrics.map((item) => <div className="metric" key={item.label}><div><span>{item.label}</span><Badge value={item.tone} label={metricToneLabels[item.tone]} /></div><strong>{item.value}</strong><p>{item.delta}</p></div>)}</section>
      <section className="dashboard-grid">
        <div className="panel trend-panel"><div className="panel-heading"><div><h2>消息流量</h2><p>服务端可用的最新分时统计</p></div></div><TrendChart data={state.data.messageTrend} /></div>
        <div className="panel mix-panel"><div className="panel-heading"><div><h2>消息构成</h2><p>按会话类型统计</p></div></div>{state.data.channelMix.length ? <><div className="donut" style={{ '--segments': channelMixGradient(state.data.channelMix) } as React.CSSProperties}><div><strong>{Math.round(state.data.channelMix.reduce((sum, item) => sum + item.value, 0))}%</strong><span>已分类</span></div></div><div className="mix-legend">{state.data.channelMix.map((item) => <div key={item.label}><span style={{ background: item.color }} /><b>{item.label}</b><strong>{item.value}%</strong></div>)}</div></> : <div className="panel-empty">暂无构成数据</div>}</div>
        <div className="panel alert-panel"><div className="panel-heading"><div><h2>需要处理</h2><p>按风险和等待时间排序</p></div></div>{state.data.alerts.length ? state.data.alerts.map((alert) => <div className={`alert-row ${alert.severity}`} key={alert.id}><AlertTriangle size={18} /><div><strong>{alert.title}</strong><p>{alert.detail}</p></div><time>{alert.time}</time></div>) : <div className="panel-empty">当前没有服务端告警</div>}</div>
        <div className="panel activity-panel"><div className="panel-heading"><div><h2>最新操作</h2><p>敏感动作审计记录</p></div></div>{state.data.activity.length ? state.data.activity.map((log) => <div className="activity-row" key={log.id}><div className="activity-icon"><ShieldCheck size={16} /></div><div><strong>{log.action}</strong><p>{log.actor} · {log.target}</p></div><time>{log.createdAt}</time></div>) : <div className="panel-empty">暂无可展示的审计记录</div>}</div>
      </section>
    </div>}
  </>;
}

function Toolbar({ query, setQuery, placeholder, children }: { query: string; setQuery: (value: string) => void; placeholder: string; children?: ReactNode }) {
  return <div className="toolbar"><label className="search-field"><span className="sr-only">{placeholder}</span><Search size={17} /><input aria-label={placeholder} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={placeholder} /></label>{children}</div>;
}

function PickerLoadFeedback({ loading, error, empty, loadingLabel, errorLabel, emptyLabel, retry, retryLabel }: { loading: boolean; error: string; empty: boolean; loadingLabel: string; errorLabel: string; emptyLabel: string; retry: () => void | Promise<void>; retryLabel: string }) {
  if (loading) return <small className="picker-feedback" role="status"><RefreshCcw className="spin" size={13} />{loadingLabel}</small>;
  if (error) return <small className="picker-feedback picker-feedback-error" role="alert"><AlertTriangle size={13} /><span>{errorLabel}</span><button type="button" onClick={() => void retry()} aria-label={retryLabel}><RefreshCcw size={12} />重试</button></small>;
  return empty ? <small className="picker-feedback">{emptyLabel}</small> : null;
}

function AccountPicker({ value, onChange, selectLabel, searchLabel, compact = false, excludeIds = [] }: { value: string; onChange: (value: string) => void; selectLabel: string; searchLabel: string; compact?: boolean; excludeIds?: string[] }) {
  const { api, mode } = useApi();
  const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query);
  const candidates = useResource(() => api.getUsers(deferredQuery, 'active', 1, 20, ''), [api, mode, deferredQuery]);
  const available = candidates.data?.items.filter((item) => !excludeIds.includes(item.id)) ?? [];
  return <div className={`account-picker${compact ? ' account-picker-compact' : ''}`}>
    <label className="field-label">查找账号<input aria-label={searchLabel} value={query} onChange={(event) => setQuery(event.target.value)} placeholder="输入昵称、呱呱号或手机号" /></label>
    <label className="field-label">{selectLabel}<select aria-label={selectLabel} aria-busy={candidates.loading} disabled={!value && (candidates.loading || Boolean(candidates.error))} value={value} onChange={(event) => onChange(event.target.value)} required><option value="">请选择真实账号</option>{value && !available.some((item) => item.id === value) && <option value={value}>{value}</option>}{available.map((item) => <option key={item.id} value={item.id}>{item.nickname} · {item.handle || item.phone}</option>)}</select><PickerLoadFeedback loading={candidates.loading} error={candidates.error} empty={!available.length} loadingLabel="正在查询真实账号…" errorLabel="账号列表加载失败" emptyLabel="没有匹配的可用账号" retry={candidates.reload} retryLabel="重新加载账号列表" /></label>
  </div>;
}

function AccountMultiPicker({ values, onChange, label = '定向用户' }: { values: string[]; onChange: (values: string[]) => void; label?: string }) {
  const { api, mode } = useApi();
  const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query);
  const candidates = useResource(() => api.getUsers(deferredQuery, 'active', 1, 20, ''), [api, mode, deferredQuery]);
  const [knownNames, setKnownNames] = useState<Record<string, string>>({});
  useEffect(() => {
    const items = candidates.data?.items;
    if (!items?.length) return;
    setKnownNames((current) => ({ ...current, ...Object.fromEntries(items.map((item) => [item.id, `${item.nickname} · ${item.handle || item.phone}`])) }));
  }, [candidates.data]);
  const available = candidates.data?.items.filter((item) => !values.includes(item.id)) ?? [];
  return <fieldset className="selection-field account-multi-picker"><legend>{label}</legend>
    <div className="account-picker account-picker-compact"><label className="field-label">查找账号<input aria-label={`查找${label}`} value={query} onChange={(event) => setQuery(event.target.value)} placeholder="输入昵称、呱呱号或手机号" /></label><label className="field-label">添加账号<select aria-label={`添加${label}`} aria-busy={candidates.loading} disabled={candidates.loading || Boolean(candidates.error)} value="" onChange={(event) => { if (event.target.value) onChange([...new Set([...values, event.target.value])]); }}><option value="">选择要添加的真实账号</option>{available.map((item) => <option key={item.id} value={item.id}>{item.nickname} · {item.handle || item.phone}</option>)}</select><PickerLoadFeedback loading={candidates.loading} error={candidates.error} empty={!available.length} loadingLabel="正在查询真实账号…" errorLabel="账号列表加载失败" emptyLabel="没有可继续添加的匹配账号" retry={candidates.reload} retryLabel={`重新加载${label}账号列表`} /></label></div>
    <div className="selected-accounts" aria-label={`已选${label}`}>{values.map((id) => <span key={id}><span>{knownNames[id] ?? `账号 ${id}`}</span><button type="button" aria-label={`移除${knownNames[id] ?? id}`} onClick={() => onChange(values.filter((value) => value !== id))}><X size={13} /></button></span>)}{!values.length && <small>尚未选择账号</small>}</div>
  </fieldset>;
}

function MediaPicker({ value, onChange, label }: { value: string; onChange: (value: string) => void; label: string }) {
  const { api, mode } = useApi();
  const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query);
  const media = useResource(() => api.getMedia(deferredQuery, 'ready', 1, 50, ''), [api, mode, deferredQuery]);
  const images = media.data?.items.filter((item) => item.mime.startsWith('image/')) ?? [];
  return <div className="account-picker">
    <label className="field-label">查找图片<input aria-label={`查找${label}`} value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索文件名、上传者或媒体编号" /></label>
    <label className="field-label">{label}<select aria-label={label} aria-busy={media.loading} disabled={!value && (media.loading || Boolean(media.error))} value={value} onChange={(event) => onChange(event.target.value)} required><option value="">请选择已上传的可用图片</option>{value && !images.some((item) => item.id === value) && <option value={value}>{value}</option>}{images.map((item) => <option key={item.id} value={item.id}>{item.objectKey.split('/').pop() || item.id} · {formatBytes(item.size)}</option>)}</select><PickerLoadFeedback loading={media.loading} error={media.error} empty={!images.length} loadingLabel="正在加载可用图片…" errorLabel="图片列表加载失败" emptyLabel="没有匹配的可用图片，请先在文件存储中完成上传" retry={media.reload} retryLabel={`重新加载${label}图片列表`} /></label>
  </div>;
}

function CreateUserPage() {
  const { api, notify, can } = useApi();
  const [phone, setPhone] = useState(''); const [name, setName] = useState(''); const [gender, setGender] = useState<UserRecord['gender']>('unspecified');
  const [password, setPassword] = useState(''); const [reason, setReason] = useState(''); const [saving, setSaving] = useState(false); const [error, setError] = useState('');
  const validPhone = /^1[3-9]\d{9}$/.test(phone); const valid = validPhone && name.trim().length > 0 && password.length >= 8 && reason.trim().length > 0 && can('users.write');
  const submit = async (event: FormEvent) => {
    event.preventDefault(); if (!valid || saving) return; setSaving(true); setError('');
    try {
      const created = await api.createUser({ phone, name: name.trim(), password, gender }, reason.trim());
      notify(`用户 ${created.nickname} 已创建`); window.history.pushState({}, '', `/users?q=${encodeURIComponent(phone)}`); window.dispatchEvent(new PopStateEvent('popstate'));
    } catch (cause) { setError(errorMessage(cause)); } finally { setSaving(false); }
  };
  return <><PageHeader title="新增用户" description="由后台直接创建中国大陆手机号账号，不受公开注册开关和短信验证码影响。" /><form className="settings-section create-user-card" onSubmit={(event) => void submit(event)}>
    <div className="settings-title"><CircleUserRound size={20} /><div><h2>账号资料</h2><p>手机号国家码固定为 +86；初始密码遵循当前服务端动态密码策略。</p></div></div>
    <div className="form-grid"><label className="field-label">手机号<div className="phone-input"><span>+86</span><input aria-label="中国大陆手机号" inputMode="numeric" autoComplete="tel-national" value={phone} maxLength={11} onChange={(event) => setPhone(event.target.value.replace(/\D/g, '').slice(0, 11))} placeholder="13800138000" required /></div>{phone && !validPhone && <small className="field-error">请输入有效的 11 位中国大陆手机号</small>}</label><label className="field-label">昵称<input value={name} maxLength={40} onChange={(event) => setName(event.target.value)} placeholder="填写用户昵称" required /></label><label className="field-label">性别<select value={gender} onChange={(event) => setGender(event.target.value as UserRecord['gender'])}><option value="unspecified">未设置</option><option value="male">男</option><option value="female">女</option></select></label><label className="field-label">初始密码<input type="password" autoComplete="new-password" minLength={8} maxLength={72} value={password} onChange={(event) => setPassword(event.target.value)} placeholder="至少 8 位，以服务端策略为准" required /></label></div>
    <label className="field-label">操作理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} placeholder="填写工单编号、业务需求或创建依据" required /></label>
    {error && <div className="inline-notice danger" role="alert"><AlertTriangle size={15} />{error}</div>}{!can('users.write') && <div className="inline-notice warning" role="status"><AlertTriangle size={15} />当前角色没有创建用户权限</div>}
    <button className="button primary" type="submit" disabled={!valid || saving}><Plus size={17} />{saving ? '正在创建…' : '创建用户'}</button>
  </form></>;
}

function UsersPage() {
  const { api, mode, notify, can } = useApi();
  const [query, setQuery] = useState(() => new URLSearchParams(window.location.search).get('q') ?? ''), deferredQuery = useDebouncedValue(query); const [status, setStatus] = useState(''); const [page, setPage] = useState(1);
  const [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  const [selected, setSelected] = useState<UserRecord>(); const [detailUser, setDetailUser] = useState<UserRecord>(); const [reason, setReason] = useState(''); const [banHours, setBanHours] = useState(24);
  const [messageUser, setMessageUser] = useState<UserRecord>(); const [messageSender, setMessageSender] = useState(''); const [messageContent, setMessageContent] = useState(''); const [messageReason, setMessageReason] = useState('');
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery, status]);
  const state = useResource(() => api.getUsers(deferredQuery, status, page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, status, page, cursors]);
  const systemUsers = useResource(() => api.getWukongSystemUsers(), [api, mode]);
  const availableSystemUsers = systemUsers.data?.filter((item) => item.enabled && item.syncStatus === 'synced') ?? [];
  useEffect(() => { if (availableSystemUsers.length === 1) setMessageSender(availableSystemUsers[0].userId); }, [availableSystemUsers.length, availableSystemUsers[0]?.userId]);
  const paginate = (nextPage: number) => { if (nextPage > page && state.data?.nextCursor) setCursors((current) => ({ ...current, [nextPage]: state.data?.nextCursor ?? '' })); setPage(nextPage); };
  const openDisposition = (user: UserRecord) => { setReason(''); setBanHours(24); setSelected(user); };
  const closeDisposition = () => { setSelected(undefined); setReason(''); };
  const toggleBan = async () => { if (!selected || !reason.trim()) throw new Error('请输入处置理由'); const wasBanned = selected.status === 'banned'; if (wasBanned) await api.unbanUser(selected.id, reason.trim()); else await api.banUser(selected.id, reason.trim(), banHours); notify(wasBanned ? '已解除用户封禁' : banHours === 0 ? '已永久封禁用户' : `已封禁用户 ${banHours} 小时`); await state.reload(); };
  const openMessage = (user: UserRecord) => { setMessageUser(user); setMessageSender(availableSystemUsers.length === 1 ? availableSystemUsers[0].userId : ''); setMessageContent(''); setMessageReason(''); };
  const closeMessage = () => { setMessageUser(undefined); setMessageSender(''); setMessageContent(''); setMessageReason(''); };
  const sendSystemMessage = async () => { if (!messageUser || !messageSender || !messageContent.trim() || !messageReason.trim()) throw new Error('请选择系统账号并填写消息内容和发送理由'); await api.sendUserSystemMessage(messageUser.id, messageSender, messageContent.trim(), messageReason.trim()); notify(`系统消息已发送给 ${messageUser.nickname}`); closeMessage(); };
  const viewMessageRecords = (user: UserRecord) => { window.history.pushState({}, '', `/messages?q=${encodeURIComponent(user.id)}`); window.dispatchEvent(new PopStateEvent('popstate')); };
  return <><PageHeader title="用户管理" description="查询账号及在线状态，查看好友、黑名单和设备，并执行消息通知与账号治理。" actions={<button type="button" className="button primary" disabled={!can('users.write')} onClick={() => { window.history.pushState({}, '', '/users/new'); window.dispatchEvent(new PopStateEvent('popstate')); }}><Plus size={16} />新增用户</button>} />
    <Toolbar query={query} setQuery={setQuery} placeholder="搜索昵称、用户 ID、手机号或呱呱号"><select aria-label="用户状态" className="select-control" value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="active">账号正常</option><option value="banned">已封禁</option><option value="online">当前在线</option><option value="offline">当前离线</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="没有匹配的用户" emptyDetail="调整搜索词或状态筛选后重试。"><div className="table-wrap"><table className="users-table"><thead><tr><th>用户</th><th>呱呱号 / 性别</th><th>账号状态</th><th>在线状态</th><th>最近设备</th><th>注册时间</th><th><span className="sr-only">操作</span></th></tr></thead><tbody>{state.data?.items.map((user) => <tr key={user.id}><td><div className="identity"><UserAvatar user={user} /><div><strong>{user.nickname}</strong><small className="mono">{user.id}</small><small>{user.phone}</small></div></div></td><td><div><strong className="mono">{user.handle}</strong><small>{user.gender === 'male' ? '男' : user.gender === 'female' ? '女' : '未设置性别'} · 已修改 {user.handleChangeCount}/2 次</small></div></td><td><Badge value={user.status} />{user.bannedUntil && <small>至 {dateTimeLabel(user.bannedUntil)}</small>}</td><td><Badge value={user.online ? 'active' : 'neutral'} label={user.online ? `在线 · ${user.onlineConnections || 1}` : '离线'} />{!user.online && <small>{user.lastOfflineAt ? `最后离线 ${dateTimeLabel(user.lastOfflineAt)}` : '暂无离线记录'}</small>}</td><td>{user.latestDevice ? <div><strong>{user.latestDevice.deviceName || user.latestDevice.deviceModel || user.latestDevice.platform}</strong><small>{user.latestDevice.platform} · {user.latestDevice.osVersion || '系统未知'}</small><small>App {user.latestDevice.appVersion} · {user.latestDevice.lastSeenAt}</small></div> : <small>等待新版客户端上报</small>}</td><td>{user.registeredAt}</td><td><div className="row-actions user-actions"><button className="row-action" aria-label="查看详情" title="查看详情" onClick={() => setDetailUser(user)}>详情</button><button className="row-action" aria-label="查看发送记录" title="查看该用户发送记录" onClick={() => viewMessageRecords(user)}>记录</button><button className="row-action" aria-label="发系统消息" disabled={!can('users.write')} title={!can('users.write') ? '当前角色没有系统消息权限' : '发送系统消息'} onClick={() => openMessage(user)}>消息</button><button className="row-action" aria-label={user.status === 'banned' ? '解除封禁' : '封禁账号'} disabled={!can('users.write')} title={!can('users.write') ? '当前角色没有账号处置权限' : user.status === 'banned' ? '解除封禁' : '封禁账号'} onClick={() => openDisposition(user)}>{user.status === 'banned' ? '解封' : '封禁'}</button></div></td></tr>)}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
    {detailUser && <UserDetailDialog user={detailUser} onClose={() => setDetailUser(undefined)} />}
    <ConfirmDialog open={Boolean(messageUser)} title="发送系统消息" detail={messageUser ? `选择已启用且同步成功的系统账号，通过 WuKongIM 发送给 ${messageUser.nickname}（${messageUser.id}）。` : ''} confirmLabel="确认发送" confirmDisabled={!messageSender || !messageContent.trim() || !messageReason.trim() || systemUsers.loading || Boolean(systemUsers.error)} onClose={closeMessage} onConfirm={sendSystemMessage}><label className="field-label">发送账号<select autoFocus value={messageSender} disabled={systemUsers.loading || !availableSystemUsers.length} onChange={(event) => setMessageSender(event.target.value)} required><option value="">{systemUsers.loading ? '正在加载系统账号…' : availableSystemUsers.length ? '请选择系统账号' : '没有可用系统账号'}</option>{availableSystemUsers.map((item) => <option key={item.userId} value={item.userId}>{item.name || item.userId}（{item.userId}）</option>)}</select>{systemUsers.error && <small className="field-error">{systemUsers.error}</small>}{!systemUsers.loading && !systemUsers.error && !availableSystemUsers.length && <small className="field-error">请先前往“设置 → IM 基础设施 → 系统账号”启用并完成同步</small>}</label><label className="field-label">消息内容<textarea value={messageContent} maxLength={2000} onChange={(event) => setMessageContent(event.target.value)} placeholder="填写用户将在聊天列表中收到的通知内容" required /></label><label className="field-label">发送理由<textarea value={messageReason} maxLength={500} onChange={(event) => setMessageReason(event.target.value)} placeholder="填写运营工单或通知依据" required /></label></ConfirmDialog>
    <ConfirmDialog open={Boolean(selected)} title={selected?.status === 'banned' ? '解除用户封禁' : '封禁用户'} detail={selected ? `目标账号：${selected.nickname}（${selected.id}）。操作会写入审计日志。` : ''} confirmLabel={selected?.status === 'banned' ? '解除用户封禁' : '确认封禁'} danger={selected?.status !== 'banned'} confirmDisabled={!reason.trim()} onClose={closeDisposition} onConfirm={toggleBan}>{selected?.status !== 'banned' && <label className="field-label">封禁时长<select value={banHours} onChange={(event) => setBanHours(Number(event.target.value))}><option value={24}>24 小时</option><option value={72}>3 天</option><option value={168}>7 天</option><option value={720}>30 天</option><option value={0}>永久</option></select></label>}<label className="field-label">处置理由<textarea value={reason} onChange={(event) => setReason(event.target.value)} placeholder="说明证据、影响范围和处置依据" required /></label></ConfirmDialog>
  </>;
}

function GroupsPage() {
  const { api, mode, notify, can } = useApi();
  const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query); const [status,setStatus]=useState(''); const [page, setPage] = useState(1); const [cursors,setCursors]=useState<Record<number,string>>({1:''}); const [selected, setSelected] = useState<GroupRecord>(); const [detailGroup, setDetailGroup] = useState<GroupRecord>(); const [reason, setReason] = useState('');
  useEffect(() => {setPage(1);setCursors({1:''});}, [deferredQuery,status]);
  const state = useResource(() => api.getGroups(deferredQuery,status,page,20,cursors[page]??''), [api, mode, deferredQuery,status,page,cursors]);
  const paginate=(next:number)=>{if(next>page&&state.data?.nextCursor)setCursors(v=>({...v,[next]:state.data?.nextCursor??''}));setPage(next);};
  const openDisband = (group: GroupRecord) => { setReason(''); setSelected(group); };
  const closeDisband = () => { setSelected(undefined); setReason(''); };
  const disband = async () => { if (!selected || !reason.trim()) throw new Error('请输入解散理由'); await api.disbandGroup(selected.id, reason.trim()); notify('群组已解散'); await state.reload(); };
  return <><PageHeader title="群组管理" description="检查群活跃度、举报风险、群主与成员规模。" /><Toolbar query={query} setQuery={setQuery} placeholder="搜索群名称、群 ID 或群主"><select className="select-control" aria-label="群组状态" value={status} onChange={e=>setStatus(e.target.value)}><option value="">全部状态</option><option value="active">正常</option><option value="muted">全员禁言</option><option value="dissolved">已解散</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="没有匹配的群组" emptyDetail="请更换群名称、群 ID 或群主关键词。"><div className="table-wrap"><table><thead><tr><th>群组</th><th>状态</th><th>群主</th><th>成员</th><th>累计消息</th><th>举报</th><th>创建日期</th><th><span className="sr-only">操作</span></th></tr></thead><tbody>{state.data?.items.map((group) => <tr key={group.id}><td><div className="group-name"><span><Users size={17} /></span><div><strong>{group.name}</strong><small>{group.id}</small></div></div></td><td><Badge value={group.status} /></td><td>{group.owner}</td><td>{group.memberCount}</td><td>{group.messageCount.toLocaleString()}</td><td>{group.reportCount}</td><td>{group.createdAt}</td><td><div className="row-actions"><button className="row-action" onClick={() => setDetailGroup(group)}>查看详情</button><button className="row-action danger-text" disabled={group.status === 'dissolved' || !can('groups.write')} title={!can('groups.write') ? '当前角色没有群组处置权限' : undefined} onClick={() => openDisband(group)}>解散群组</button></div></td></tr>)}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
    {detailGroup && <GroupDetailDialog group={detailGroup} onClose={() => setDetailGroup(undefined)} />}
    <ConfirmDialog open={Boolean(selected)} title="解散群组" detail={selected ? `群组「${selected.name}」解散后无法恢复，成员将无法继续发送消息。` : ''} confirmLabel="解散群组" danger confirmDisabled={!reason.trim()} onClose={closeDisband} onConfirm={disband}><label className="field-label">解散理由<textarea value={reason} onChange={(event) => setReason(event.target.value)} placeholder="说明违规事实和解散依据" required /></label></ConfirmDialog>
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
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="当前队列已处理完" emptyDetail="新的举报进入后会自动出现在这里。"><div className="report-list">{state.data?.items.map((report) => <article className="report-item" key={report.id}><div className={`risk-marker risk-${report.risk}`}><ShieldAlert size={18} /></div><div className="report-main"><div className="report-title"><strong>{localizedEnum(report.category, reportCategoryLabels, '其他举报')}</strong><Badge value={report.status} /><span className={`risk-label risk-${report.risk}`}>{riskLabel[report.risk]}</span></div><p className="report-excerpt">“{report.excerpt}”</p><div className="report-meta"><span>举报对象：{report.target}</span><span>举报人：{report.reporter}</span><time>{report.createdAt}</time></div></div>{report.status !== 'resolved' && report.status !== 'rejected' && <button className="button secondary compact" disabled={!can('reports.write')} onClick={() => openReport(report)}>审核举报</button>}</article>)}</div><Pagination data={state.data} onPage={paginate} /></DataPanel>
    <ConfirmDialog open={Boolean(selected)} title="提交审核结果" detail={selected ? `${selected.category}，对象：${selected.target}` : ''} confirmLabel="提交审核结果" confirmDisabled={!note.trim()} onClose={() => setSelected(undefined)} onConfirm={resolve}><label className="field-label">处置动作<select value={action} onChange={(event) => setAction(event.target.value as ReportResolutionAction)}>{selected?.targetType === 'message' && <option value="delete_message">删除并撤回违规消息</option>}{(selected?.targetType === 'message' || selected?.targetType === 'user') && <option value="ban_user">封禁相关用户并撤销会话</option>}<option value="no_violation">判定未违规</option><option value="dismiss">证据不足，驳回举报</option></select></label><label className="field-label">审核备注<textarea value={note} onChange={(event) => setNote(event.target.value)} placeholder="记录判定依据，便于后续追溯" required /></label></ConfirmDialog>
  </>;
}

function SensitiveWordsPage() {
  const { api, mode, notify, can } = useApi(); const [query, setQuery] = useState(''); const [adding, setAdding] = useState(false); const [addingReason,setAddingReason]=useState(''); const [removing, setRemoving] = useState<SensitiveWord>(); const [removingReason,setRemovingReason]=useState(''); const [word, setWord] = useState(''); const [category, setCategory] = useState('诈骗');
  const state = useResource(() => api.getSensitiveWords(), [api, mode]);
  const filtered = state.data?.filter((item) => `${item.word}${item.category}`.includes(query));
  const add = async () => { if (!word.trim() || !addingReason.trim()) throw new Error('请输入敏感词和操作理由'); await api.addSensitiveWord({ word: word.trim(), category, action: 'block', matchType: 'exact' }, addingReason.trim()); setWord(''); notify('敏感词拦截规则已添加'); await state.reload(); };
  const remove = async () => { if (!removing||!removingReason.trim()) throw new Error('请输入删除理由'); await api.deleteSensitiveWord(removing.id,removingReason.trim()); notify('敏感词规则已删除'); await state.reload(); };
  const openAdd = () => { setWord(''); setAddingReason(''); setAdding(true); };
  const closeAdd = () => { setAdding(false); setWord(''); setAddingReason(''); };
  const openRemove = (item: SensitiveWord) => { setRemovingReason(''); setRemoving(item); };
  const closeRemove = () => { setRemoving(undefined); setRemovingReason(''); };
  return <><PageHeader title="敏感词库" description="维护服务端真实生效的包含匹配规则；命中后直接拒绝新文本消息。" actions={<button className="button primary" disabled={!can('rules.write')} onClick={openAdd}><Plus size={16} />添加规则</button>} />
    <Toolbar query={query} setQuery={setQuery} placeholder="搜索敏感词或分类"><span className="toolbar-note">共 {filtered?.length ?? 0} 条规则</span></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!filtered?.length} emptyTitle="没有匹配的敏感词" emptyDetail="调整关键词，或添加一条新的内容规则。"><div className="table-wrap"><table><thead><tr><th>敏感词</th><th>分类</th><th>匹配方式</th><th>命中动作</th><th>添加日期</th><th><span className="sr-only">操作</span></th></tr></thead><tbody>{filtered?.map((item) => <tr key={item.id}><td><strong>{item.word}</strong></td><td>{item.category}</td><td>包含匹配</td><td><Badge value="banned" label="直接拦截" /></td><td>{item.createdAt}</td><td><button className="icon-button table-icon danger-text" disabled={!can('rules.write')} aria-label={`删除 ${item.word}`} onClick={() => openRemove(item)}><Trash2 size={16} /></button></td></tr>)}</tbody></table></div></DataPanel>
    <ConfirmDialog open={adding} title="添加敏感词规则" detail="规则按不区分大小写的包含匹配执行，新增后立即应用于新文本消息。" confirmLabel="添加规则" confirmDisabled={!word.trim() || !addingReason.trim()} onClose={closeAdd} onConfirm={add}><label className="field-label">敏感词<input autoFocus value={word} onChange={(event) => setWord(event.target.value)} placeholder="例如：免费领取" required /></label><label className="field-label">分类<select value={category} onChange={(event) => setCategory(event.target.value)}><option>诈骗</option><option>黑产</option><option>金融风险</option><option>色情低俗</option><option>其他</option></select></label><label className="field-label">操作理由<textarea value={addingReason} maxLength={500} onChange={(event) => setAddingReason(event.target.value)} placeholder="说明规则来源和适用场景" required /></label></ConfirmDialog>
    <ConfirmDialog open={Boolean(removing)} title="删除敏感词规则" detail={removing ? `删除「${removing.word}」后，新消息将不再应用这条规则。` : ''} confirmLabel="删除规则" danger confirmDisabled={!removingReason.trim()} onClose={closeRemove} onConfirm={remove}><label className="field-label">删除理由<textarea value={removingReason} onChange={e=>setRemovingReason(e.target.value)} placeholder="说明失效、误拦截或规则替代情况" required/></label></ConfirmDialog>
  </>;
}

function messageLifecycle(message: MessageRecord) {
  if (message.expiredAt) return { value: 'rejected', label: '已过期', detail: dateTimeLabel(message.expiredAt) };
  if (message.recalled) return { value: 'rejected', label: '已撤回', detail: dateTimeLabel(message.recalledAt) };
  if (message.expiresAt) return { value: 'pending', label: '限时消息', detail: `到期 ${dateTimeLabel(message.expiresAt)}` };
  if (message.editedAt) return { value: 'info', label: `已编辑 v${message.editVersion}`, detail: dateTimeLabel(message.editedAt) };
  return { value: 'active', label: '有效', detail: `序号 ${message.conversationSeq}` };
}

const messageTypeLabels: Record<string, string> = {
  text: '文本', image: '图片 / GIF', audio: '语音', video: '视频', location: '位置', contact: '名片', file: '文件',
  chat_history: '合并聊天记录', system: '系统事件', sticker: '商店表情', moment: '朋友圈分享', call: '通话事件',
  live: '直播互动', support: '客服事件', screenshot: '截屏提示', custom: '其他自定义消息',
};

const messageTypeOptions = Object.entries(messageTypeLabels).filter(([value]) => value !== 'custom');

function MessagesPage() {
  const { api, mode } = useApi(); const [query, setQuery] = useState(() => new URLSearchParams(window.location.search).get('q') ?? ''), deferredQuery = useDebouncedValue(query); const [type, setType] = useState(''), [page, setPage] = useState(1); const [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery, type]);
  const state = useResource(() => api.getMessages(deferredQuery, type, page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, type, page, cursors]);
  const paginate = (nextPage: number) => { if (nextPage > page && state.data?.nextCursor) setCursors((current) => ({ ...current, [nextPage]: state.data?.nextCursor ?? '' })); setPage(nextPage); };
  return <><PageHeader title="消息治理索引" description="按消息元数据检索，并从 WuKongIM 实时加载当前结果页正文；每次查看都会写入审计日志。" /><Toolbar query={query} setQuery={setQuery} placeholder="搜索消息 ID、会话 ID、发送人或客户端消息 ID"><select className="select-control" aria-label="消息类型" value={type} onChange={(event) => setType(event.target.value)}><option value="">全部类型</option>{messageTypeOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="没有匹配的消息元数据" emptyDetail="调整消息 ID、会话、发送人或类型后重试。"><div className="table-wrap"><table><thead><tr><th>时间</th><th>类型</th><th>消息正文</th><th>发送人</th><th>会话 / 序号</th><th>生命周期</th></tr></thead><tbody>{state.data?.items.map((message: MessageRecord) => { const lifecycle = messageLifecycle(message); return <tr key={message.id}><td>{message.createdAt}</td><td>{messageTypeLabels[message.type] ?? messageTypeLabels.custom}</td><td><strong className="message-content-preview">{message.preview}</strong></td><td><UserIdentity user={message.sender} fallbackId={message.senderId} /></td><td><div><strong className="mono">{message.conversationId}</strong><small>序号 {message.conversationSeq}</small></div></td><td><Badge value={lifecycle.value} label={lifecycle.label} /><small>{lifecycle.detail}</small></td></tr>; })}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
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
  const {api,mode}=useApi(); const [query,setQuery]=useState(''), deferred=useDebouncedValue(query); const [page,setPage]=useState(1); const [cursors,setCursors]=useState<Record<number,string>>({1:''});
  useEffect(()=>{setPage(1);setCursors({1:''});},[deferred]);
  const friends=useResource(()=>api.getFriendships(deferred,page,20,cursors[page]??''),[api,mode,deferred,page,cursors]);
  const paginate=(next:number)=>{if(next>page&&friends.data?.nextCursor)setCursors(v=>({...v,[next]:friends.data?.nextCursor??''}));setPage(next);};
  return <><PageHeader title="关系管理" description="只读核对真实好友关系；后台不提供任意修改社交关系的按钮。"/><Toolbar query={query} setQuery={setQuery} placeholder="搜索用户 ID 或昵称"/>
    <section className="panel section-panel"><div className="panel-heading"><div><h2>好友关系</h2><p>每对关系只展示一次，来源为持久化关系表。</p></div></div><DataPanel loading={friends.loading} error={friends.error} retry={friends.reload} empty={!friends.data?.items.length} emptyTitle="没有匹配的好友关系" emptyDetail="建立双向好友关系后会显示在这里。"><div className="table-wrap"><table><thead><tr><th>用户</th><th>好友</th><th>建立时间</th><th>最近更新</th></tr></thead><tbody>{friends.data?.items.map((item:FriendshipRecord)=><tr key={`${item.userId}:${item.friendUserId}`}><td><strong>{item.userName}</strong><small className="mono">{item.userId}</small></td><td><strong>{item.friendName}</strong><small className="mono">{item.friendUserId}</small></td><td>{item.createdAt}</td><td>{item.updatedAt}</td></tr>)}</tbody></table></div><Pagination data={friends.data} onPage={paginate}/></DataPanel></section>
  </>;
}

function FeedbackPage() {
  const {api,mode}=useApi(); const [query,setQuery]=useState(''), deferred=useDebouncedValue(query); const [category,setCategory]=useState(''); const [page,setPage]=useState(1); const [cursors,setCursors]=useState<Record<number,string>>({1:''});
  useEffect(()=>{setPage(1);setCursors({1:''});},[deferred,category]);
  const feedback=useResource(()=>api.getFeedback(deferred,category,page,20,cursors[page]??''),[api,mode,deferred,category,page,cursors]);
  const paginate=(next:number)=>{if(next>page&&feedback.data?.nextCursor)setCursors(v=>({...v,[next]:feedback.data?.nextCursor??''}));setPage(next);};
  return <><PageHeader title="用户反馈" description="查看用户主动提交的故障、建议、举报补充和其他反馈。"/><Toolbar query={query} setQuery={setQuery} placeholder="搜索用户 ID、昵称、反馈内容或联系方式"><select className="select-control" aria-label="反馈分类" value={category} onChange={e=>setCategory(e.target.value)}><option value="">全部反馈</option><option value="bug">故障</option><option value="feature">建议</option><option value="abuse">举报补充</option><option value="other">其他</option></select></Toolbar>
    <section className="panel section-panel"><div className="panel-heading"><div><h2>反馈记录</h2><p>反馈内容、提交者及可选联系方式。</p></div></div><DataPanel loading={feedback.loading} error={feedback.error} retry={feedback.reload} empty={!feedback.data?.items.length} emptyTitle="没有匹配的反馈" emptyDetail="用户提交反馈后会显示在这里。"><div className="table-wrap"><table><thead><tr><th>时间</th><th>用户</th><th>分类</th><th>内容</th><th>联系方式</th></tr></thead><tbody>{feedback.data?.items.map((item:FeedbackRecord)=><tr key={item.id}><td>{item.createdAt}</td><td><strong>{item.userName}</strong><small className="mono">{item.userId}</small></td><td>{feedbackCategoryLabels[item.category] ?? '其他'}</td><td className="feedback-content">{item.content}</td><td>{item.contact||'未提供'}</td></tr>)}</tbody></table></div><Pagination data={feedback.data} onPage={paginate}/></DataPanel></section>
  </>;
}

const taskLabels:Record<string,string>={scheduledMessages:'定时消息',messageExpiry:'消息过期',mediaCleanup:'媒体清理',wukongOutbox:'WuKong 同步队列',wukongWebhook:'WuKong Webhook 队列',pending:'待处理',processing:'处理中',failed:'失败',waiting:'等待到期',status:'状态',lastRun:'最近执行',oldestPendingSeconds:'最老积压（秒）',lastCompletedAt:'最近完成',reconcilePending:'待对账',reconcileCompleted:'已对账',reconcileFailed:'对账失败'};
function taskSummary(value:unknown){if(!value||typeof value!=='object')return String(value??'暂无');return Object.entries(value as Record<string,unknown>).map(([key,item])=>`${taskLabels[key]??key}：${item===null?'暂无':String(item)}`).join(' · ');}
const backupStatusView:Record<OperationsStatus['backups']['status'],{value:string;label:string}>={healthy:{value:'healthy',label:'正常'},running:{value:'reviewing',label:'正在备份'},never:{value:'neutral',label:'尚无备份'},failed:{value:'failed',label:'最近失败'},warning:{value:'degraded',label:'存在未完成代次'},stale:{value:'degraded',label:'备份过期'},unavailable:{value:'degraded',label:'监控不可用'},unconfigured:{value:'neutral',label:'未配置'}};
function OperationsPage(){
  const {api,mode}=useApi(); const state=useResource(()=>api.getOperationsStatus(),[api,mode]);
  if(state.loading)return <><PageHeader title="推送、备份、诊断、任务与权限" description="只读运行视图。"/><Skeleton rows={7}/></>;
  if(state.error||!state.data)return <><PageHeader title="推送、备份、诊断、任务与权限" description="只读运行视图。"/><ErrorState message={state.error} retry={state.reload}/></>;
  const data:OperationsStatus=state.data; const taskEntries=Object.entries(data.tasks);
  const backupView=backupStatusView[data.backups.status];
  return <><PageHeader title="推送、备份、诊断、任务与权限" description="查看推送设备和队列、备份结果、脱敏客户端诊断、后台任务积压与角色边界；页面不提供命令执行、恢复或手动清理。" actions={<button className="button secondary" onClick={()=>void state.reload()}><RefreshCcw size={15}/>刷新</button>}/>
    <div className="operations-grid"><section className="panel section-panel"><div className="panel-heading"><div><h2>推送通道</h2><p>已登记设备与禁用设备。</p></div></div>{data.push.providers.length?data.push.providers.map(item=><div className="ops-row" key={item.provider}><strong>{item.provider}</strong><span>{item.activeDevices} 台启用 · {item.disabledDevices} 台禁用</span></div>):<div className="panel-empty">暂无推送设备</div>}</section>
    <section className="panel section-panel"><div className="panel-heading"><div><h2>推送队列</h2><p>发送状态与累计尝试次数。</p></div></div>{data.push.queue.length?data.push.queue.map(item=><div className="ops-row" key={item.status}><Badge value={item.status==='sent'?'success':item.status==='failed'?'failed':'pending'} label={({pending:'待发送',processing:'处理中',sent:'已发送',failed:'失败'} as Record<string,string>)[item.status]??item.status}/><span>{item.count} 条 · 尝试 {item.attempts} 次</span></div>):<div className="panel-empty">队列为空</div>}</section>
    <section className="panel section-panel"><div className="panel-heading"><div><h2>备份状态</h2><p>只读展示最近完整备份与异地镜像状态；恢复仍由受控脚本执行。</p></div><Badge value={backupView.value} label={backupView.label}/></div><div className="ops-row"><strong>最近成功</strong><span>{dateTimeLabel(data.backups.lastSuccessAt)}</span></div><div className="ops-row"><strong>最近耗时</strong><span>{data.backups.lastDurationSeconds} 秒</span></div><div className="ops-row"><strong>未完成代次</strong><span>{data.backups.incompleteGenerations}</span></div><div className="ops-row"><strong>异地备份</strong><span>{data.backups.offsiteEnabled?'已启用':'未启用'}</span></div></section>
    <section className="panel section-panel"><div className="panel-heading"><div><h2>客户端诊断</h2><p>最近 24 小时只保存事件类型、哈希指纹和耗时，不接收错误正文、堆栈、Token 或聊天内容。</p></div><Badge value={data.diagnostics.summary.crashes>0?'failed':'healthy'} label={data.diagnostics.summary.crashes>0?`${data.diagnostics.summary.crashes} 次崩溃`:'无崩溃'}/></div><div className="ops-row"><strong>连接 / 通话失败</strong><span>{data.diagnostics.summary.connectionFailures} / {data.diagnostics.summary.callFailures}</span></div><div className="ops-row"><strong>启动耗时 P95</strong><span>{data.diagnostics.summary.performanceP95Ms===undefined?'样本不足':`${data.diagnostics.summary.performanceP95Ms} ms（${data.diagnostics.summary.performanceSamples} 样本）`}</span></div>{data.diagnostics.items.length?data.diagnostics.items.slice(0,5).map(item=><div className="ops-row stacked" key={item.id}><strong>{item.platform} · {item.name}</strong><span>{item.userId} · {item.appVersion} · {dateTimeLabel(item.occurredAt)}{item.durationMs===undefined?'':` · ${item.durationMs} ms`}</span></div>):<div className="panel-empty">最近没有客户端诊断事件</div>}</section>
    <section className="panel section-panel"><div className="panel-heading"><div><h2>后台任务</h2><p>定时消息、消息过期、媒体清理以及 WuKong 同步/对账队列的只读状态。</p></div></div>{taskEntries.length?taskEntries.map(([name,value])=><div className="ops-row stacked" key={name}><strong>{taskLabels[name]??name}</strong><span>{taskSummary(value)}</span></div>):<div className="panel-empty">暂无后台任务状态</div>}</section>
    <section className="panel section-panel"><div className="panel-heading"><div><h2>管理员与角色</h2><p>{data.access.note}</p></div></div>{data.access.administrators.length ? data.access.administrators.map(item=><div className="ops-row" key={item.id}><div><strong>{item.id}</strong><small>{adminSourceLabels[item.source] ?? item.source} · {item.mutable?'可管理':'只读配置'}</small></div><Badge value="active" label={roleLabels[item.role] ?? item.role}/></div>) : <div className="panel-empty">暂无管理员记录</div>}{data.access.roles.length ? data.access.roles.map(role=><div className="role-row" key={role.id}><strong>{roleLabels[role.id] ?? role.id}</strong><span>{role.permissions.length ? role.permissions.map(permission => permissionLabels[permission] ?? permission).join(' · ') : '仅查看已授权页面'}</span></div>) : <div className="panel-empty">暂无角色权限定义</div>}</section></div>
  </>;
}

const dateTimeLabel = (value?: string) => {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '时间未知';
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(date);
};
const localDateTimeValue = (value?: string) => {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
};
const emptyAnnouncement: AnnouncementInput = { title: '', content: '', status: 'draft', pinned: false, targetType: 'all', targetUserIds: [], pushOnPublish: false };
const announcementDraft = (item: AnnouncementRecord): AnnouncementInput => ({ title: item.title, content: item.content, status: item.status === 'scheduled' ? 'scheduled' : 'draft', pinned: item.pinned, targetType: item.targetType, targetUserIds: item.targetUserIds, scheduledAt: item.scheduledAt, pushOnPublish: item.pushOnPublish });

function AnnouncementsPage() {
  const { api, mode, notify, can } = useApi();
  const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query), [status, setStatus] = useState(''), [page, setPage] = useState(1);
  const [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  const [editing, setEditing] = useState<AnnouncementRecord | 'new'>();
  const [form, setForm] = useState<AnnouncementInput>(emptyAnnouncement);
  const [action, setAction] = useState<{ type: 'publish' | 'withdraw' | 'delete'; item: AnnouncementRecord }>();
  const [enqueuePush, setEnqueuePush] = useState(true);
  const [editReason,setEditReason]=useState('');
  const [actionReason,setActionReason]=useState('');
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery, status]);
  const state = useResource(() => api.getAnnouncements(deferredQuery, status, page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, status, page, cursors]);
  const paginate = (nextPage: number) => { if (nextPage > page && state.data?.nextCursor) setCursors((current) => ({ ...current, [nextPage]: state.data?.nextCursor ?? '' })); setPage(nextPage); };
  const openCreate = () => { setForm({ ...emptyAnnouncement }); setEditReason(''); setEditing('new'); };
  const openEdit = (item: AnnouncementRecord) => { setForm(announcementDraft(item)); setEditReason(''); setEditing(item); };
  const closeEditor = () => { setEditing(undefined); setEditReason(''); };
  const openAnnouncementAction = (next: { type: 'publish' | 'withdraw' | 'delete'; item: AnnouncementRecord }) => { setActionReason(''); setAction(next); };
  const closeAnnouncementAction = () => { setAction(undefined); setActionReason(''); };
  const save = async () => {
    const title = form.title.trim(), content = form.content.trim();
    if (!title || !content) throw new Error('标题和正文不能为空');
    if (form.status === 'scheduled' && (!form.scheduledAt || new Date(form.scheduledAt) <= new Date())) throw new Error('定时发布时间必须晚于当前时间');
    if (form.targetType === 'users' && !form.targetUserIds.length) throw new Error('定向公告至少需要选择一个用户');
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
  const scheduledAt = form.scheduledAt ? new Date(form.scheduledAt) : undefined;
  const editorReady = Boolean(
    form.title.trim()
      && form.content.trim()
      && editReason.trim()
      && (form.status !== 'scheduled' || (scheduledAt && !Number.isNaN(scheduledAt.getTime()) && scheduledAt > new Date()))
      && (form.targetType !== 'users' || form.targetUserIds.length),
  );
  const editorBaseline = editing === 'new' ? emptyAnnouncement : editing ? announcementDraft(editing) : emptyAnnouncement;
  const editorDirty = Boolean(editing && (JSON.stringify(form) !== JSON.stringify(editorBaseline) || editReason.trim()));
  return <><PageHeader title="运营公告" description="创建草稿、定时发布、置顶、定向投放和撤回公告；维护模式公告仍在系统设置中独立管理。" actions={<button className="button primary" onClick={openCreate} disabled={!can('announcements.write')}><Plus size={16} />新建公告</button>} />
    <Toolbar query={query} setQuery={setQuery} placeholder="搜索公告 ID、标题或正文"><select className="select-control" aria-label="公告状态" value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="draft">草稿</option><option value="scheduled">定时发布</option><option value="published">已发布</option><option value="withdrawn">已撤回</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="还没有公告" emptyDetail="创建第一条公告，可保存草稿或安排定时发布。"><div className="table-wrap"><table><thead><tr><th>公告</th><th>范围</th><th>发布时间</th><th>推送</th><th>状态</th><th>操作</th></tr></thead><tbody>{state.data?.items.map((item) => <tr key={item.id}><td><div><strong>{item.pinned ? '置顶 · ' : ''}{item.title}</strong><small className="mono">{item.id}</small></div></td><td>{item.targetType === 'all' ? '全部用户' : `${item.targetUserIds.length} 位用户`}</td><td>{dateTimeLabel(item.publishedAt ?? item.scheduledAt)}</td><td>{item.pushOnPublish ? '离线推送' : '仅站内'}</td><td><Badge value={item.status} /></td><td><button className="row-action" disabled={!can('announcements.write') || !['draft', 'scheduled'].includes(item.status)} onClick={() => openEdit(item)}>编辑</button>{['draft', 'scheduled'].includes(item.status) && <button className="row-action" disabled={!can('announcements.write')} onClick={() => { setEnqueuePush(item.pushOnPublish); openAnnouncementAction({ type: 'publish', item }); }}>发布</button>}{item.status === 'published' && <button className="row-action" disabled={!can('announcements.write')} onClick={() => openAnnouncementAction({ type: 'withdraw', item })}>撤回</button>}{['draft', 'withdrawn'].includes(item.status) && <button className="row-action danger-text" disabled={!can('announcements.write')} onClick={() => openAnnouncementAction({ type: 'delete', item })}>删除</button>}</td></tr>)}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
    <ConfirmDialog open={Boolean(editing)} title={editing === 'new' ? '新建运营公告' : '编辑运营公告'} detail="公告内容会由客户端展示；指定用户必须从平台真实正常账号中选择。" confirmLabel={editing === 'new' ? '创建公告' : '保存更改'} confirmDisabled={!editorReady} discardConfirmation={editorDirty ? { title: '放弃未保存的公告？', detail: '标题、正文、投放范围和定时设置将全部丢失。' } : undefined} onClose={closeEditor} onConfirm={save}><label className="field-label">公告标题<input value={form.title} maxLength={80} onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))} placeholder="简洁说明本次通知" /></label><label className="field-label">公告正文<textarea value={form.content} maxLength={5000} onChange={(event) => setForm((current) => ({ ...current, content: event.target.value }))} placeholder="说明影响范围、时间和用户需要采取的操作" /></label><div className="form-grid"><label className="field-label">发布方式<select value={form.status} onChange={(event) => setForm((current) => ({ ...current, status: event.target.value as 'draft' | 'scheduled' }))}><option value="draft">保存草稿</option><option value="scheduled">定时发布</option></select></label><label className="field-label">投放范围<select value={form.targetType} onChange={(event) => setForm((current) => ({ ...current, targetType: event.target.value as 'all' | 'users', targetUserIds: event.target.value === 'users' ? current.targetUserIds : [] }))}><option value="all">全部用户</option><option value="users">指定用户</option></select></label></div>{form.status === 'scheduled' && <label className="field-label">定时发布时间<input type="datetime-local" min={localDateTimeValue(new Date(Date.now() + 60_000).toISOString())} value={localDateTimeValue(form.scheduledAt)} onChange={(event) => setForm((current) => ({ ...current, scheduledAt: event.target.value }))} /></label>}{form.targetType === 'users' && <AccountMultiPicker values={form.targetUserIds} onChange={(targetUserIds) => setForm((current) => ({ ...current, targetUserIds }))} label="公告接收用户" />}<Toggle label="置顶展示" description="置顶公告优先显示在客户端公告列表顶部。" checked={form.pinned} onChange={(value) => setForm((current) => ({ ...current, pinned: value }))} /><Toggle label="发布时离线推送" description="按投放范围写入推送队列；敏感凭据不会进入公告内容。" checked={form.pushOnPublish} onChange={(value) => setForm((current) => ({ ...current, pushOnPublish: value }))} /><label className="field-label">操作理由<textarea value={editReason} maxLength={500} onChange={(event) => setEditReason(event.target.value)} placeholder="说明投放目的、受众和审批依据" required /></label></ConfirmDialog>
    <ConfirmDialog open={Boolean(action)} title={action?.type === 'publish' ? '发布公告' : action?.type === 'withdraw' ? '撤回公告' : '删除公告'} detail={action ? `「${action.item.title}」${action.type === 'publish' ? '将立即对目标用户可见。' : action.type === 'withdraw' ? '撤回后客户端将不再展示。' : '删除后无法恢复。'}` : ''} confirmLabel={action?.type === 'publish' ? '立即发布' : action?.type === 'withdraw' ? '确认撤回' : '确认删除'} danger={action?.type !== 'publish'} confirmDisabled={!actionReason.trim()} onClose={closeAnnouncementAction} onConfirm={runAction}>{action?.type === 'publish' && <Toggle label="同时发送离线推送" description="仅向公告目标范围内、已注册推送设备的用户投递。" checked={enqueuePush} onChange={setEnqueuePush} />}<label className="field-label">操作理由<textarea value={actionReason} maxLength={500} onChange={(event) => setActionReason(event.target.value)} placeholder="说明状态变更原因和批准依据" required /></label></ConfirmDialog>
  </>;
}

function CallsPage() {
  const { api, mode } = useApi(); const [query, setQuery] = useState(''), deferredQuery = useDebouncedValue(query), [status, setStatus] = useState(''), [page, setPage] = useState(1), [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferredQuery, status]);
  const state = useResource(() => api.getCalls(deferredQuery, status, page, 20, cursors[page] ?? ''), [api, mode, deferredQuery, status, page, cursors]);
  const paginate = (nextPage: number) => { if (nextPage > page && state.data?.nextCursor) setCursors((current) => ({ ...current, [nextPage]: state.data?.nextCursor ?? '' })); setPage(nextPage); };
  const callLabels: Record<string, string> = { invited: '呼叫中', accepted: '通话中', ended: '已结束', rejected: '已拒绝', cancelled: '已取消', missed: '未接听' };
  const reasonLabels: Record<string, string> = { completed: '正常结束', declined: '对方拒绝', busy: '对方忙碌', timeout: '呼叫超时', cancelled_by_caller: '发起人取消', media_failed: '媒体连接失败' };
  return <><PageHeader title="通话记录" description="仅展示呼叫双方、状态、时长和结束原因；服务端不保存 SDP、ICE 或通话内容。" /><Toolbar query={query} setQuery={setQuery} placeholder="搜索通话、会话或用户 ID"><select className="select-control" aria-label="通话状态" value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="invited">呼叫中</option><option value="accepted">通话中</option><option value="ended">已结束</option><option value="rejected">已拒绝</option><option value="cancelled">已取消</option><option value="missed">未接听</option></select></Toolbar>
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.items.length} emptyTitle="没有匹配的通话" emptyDetail="通话邀请建立后，元数据会显示在这里。"><div className="table-wrap"><table><thead><tr><th>发起时间</th><th>通话</th><th>参与用户</th><th>类型</th><th>时长</th><th>结束原因</th><th>状态</th></tr></thead><tbody>{state.data?.items.map((call: CallRecord) => <tr key={call.id}><td>{dateTimeLabel(call.invitedAt)}</td><td><div><strong className="mono">{call.id}</strong><small className="mono">{call.conversationId}</small></div></td><td>{call.kind === 'group' ? <div><strong>{call.participantIds.length} 人群通话</strong><small className="mono">{call.participantIds.join('、') || '参与者信息缺失'}</small><small>已加入 {call.joinedUserIds.length} · 已拒绝 {call.declinedUserIds.length} · 已离开 {call.leftUserIds.length}</small></div> : <><span className="mono">{call.callerId}</span> → <span className="mono">{call.calleeId || '未知接听方'}</span></>}</td><td>{call.mediaType === 'video' ? '视频' : '语音'}</td><td>{call.durationSeconds ? `${Math.floor(call.durationSeconds / 60)}:${String(call.durationSeconds % 60).padStart(2, '0')}` : '—'}</td><td><div><strong>{reasonLabels[call.endReason] ?? (call.endReason || '—')}</strong>{call.endedBy && <small className="mono">结束人 {call.endedBy}</small>}</div></td><td><Badge value={call.status} label={callLabels[call.status] ?? call.status} /></td></tr>)}</tbody></table></div><Pagination data={state.data} onPage={paginate} /></DataPanel>
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
  const [operationBaseline, setOperationBaseline] = useState('');
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
  const closeOperation = () => { setCategoryDraft(undefined); setPackDraft(undefined); setItemEditor(undefined); setOperationReason(''); setOperationBaseline(''); };
  const openCategory = (item?: StickerCategoryOperationsRecord) => {
    const next: StickerCategoryInput = item ? { id: item.id, name: item.name, sortOrder: item.sortOrder, enabled: item.enabled } : { name: '', sortOrder: 1000, enabled: true };
    setOperationReason('');
    setOperationBaseline(JSON.stringify(next));
    setCategoryDraft(next);
  };
  const openPack = (item?: StickerPackModerationRecord) => {
    const next: StickerPackInput = item ? {
      id: item.id, categoryId: item.categoryId ?? '', name: item.name, description: item.description,
      coverMediaId: item.coverMediaId ?? '', status: item.status === 'reviewing' ? 'reviewing' : 'draft', sortOrder: item.sortOrder ?? 1000,
    } : { categoryId: categories.data?.[0]?.id ?? '', name: '', description: '', coverMediaId: '', status: 'draft', sortOrder: 1000 };
    setOperationReason('');
    setOperationBaseline(JSON.stringify(next));
    setPackDraft(next);
  };
  const openItem = (pack: StickerPackModerationRecord, item?: NonNullable<StickerPackModerationRecord['items']>[number]) => {
    const next: StickerItemInput = item ? { id: item.id, name: item.name, mediaId: item.mediaId, emoji: item.emoji, status: item.status, sortOrder: item.sortOrder } : { name: '', mediaId: '', emoji: '', status: 'published', sortOrder: 1000 };
    setOperationReason('');
    setOperationBaseline(JSON.stringify(next));
    setItemEditor({ pack, draft: next });
  };
  const categoryDirty = Boolean(categoryDraft && (JSON.stringify(categoryDraft) !== operationBaseline || operationReason.trim()));
  const packDirty = Boolean(packDraft && (JSON.stringify(packDraft) !== operationBaseline || operationReason.trim()));
  const itemDirty = Boolean(itemEditor && (JSON.stringify(itemEditor.draft) !== operationBaseline || operationReason.trim()));
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
    <div className="tabs" role="tablist" aria-label="内容类型" onKeyDown={tabListKeyDown}><button type="button" role="tab" tabIndex={kind === 'moments' ? 0 : -1} aria-selected={kind === 'moments'} className={kind === 'moments' ? 'active' : ''} onClick={() => setKind('moments')}>朋友圈</button><button type="button" role="tab" tabIndex={kind === 'stickers' ? 0 : -1} aria-selected={kind === 'stickers'} className={kind === 'stickers' ? 'active' : ''} onClick={() => setKind('stickers')}>表情包</button></div>
    <Toolbar query={query} setQuery={setQuery} placeholder={kind === 'moments' ? '搜索动态、作者或用户 ID' : '搜索表情包、分类或创建者'}><select className="select-control" aria-label="审核状态" value={status} onChange={(event) => setStatus(event.target.value)}>{kind === 'moments' ? <><option value="">全部状态</option><option value="published">已发布</option><option value="hidden">已隐藏</option><option value="deleted">已删除</option></> : <><option value="">全部状态</option><option value="reviewing">待审核</option><option value="published">已发布</option><option value="rejected">已驳回</option><option value="disabled">已下架</option><option value="draft">草稿</option></>}</select></Toolbar>
    {kind === 'stickers' && <section className="panel sticker-category-panel"><div className="panel-heading"><div><h2>表情分类</h2><p>点击分类可编辑名称、排序和启用状态。</p></div></div>{categories.loading ? <span className="muted">正在加载分类…</span> : categories.error ? <div className="inline-notice danger">{categories.error}</div> : <div className="sticker-category-list">{categories.data?.map((item) => <button type="button" className="button secondary compact" key={item.id} disabled={!can('content.write')} onClick={() => openCategory(item)}>{item.name}<Badge value={item.enabled ? 'enabled' : 'disabled'} label={item.enabled ? '启用' : '停用'} /></button>)}{!categories.data?.length && <span className="muted">暂无分类，请先创建分类。</span>}</div>}</section>}
    {kind === 'moments' ? <DataPanel loading={moments.loading} error={moments.error} retry={moments.reload} empty={!moments.data?.items.length} emptyTitle="没有匹配的朋友圈" emptyDetail="可切换状态或缩短关键词。"><div className="table-wrap"><table><thead><tr><th>时间</th><th>作者</th><th>内容</th><th>互动</th><th>可见范围</th><th>状态</th><th>操作</th></tr></thead><tbody>{moments.data?.items.map((item) => <tr key={item.id}><td>{item.createdAt}</td><td><strong>{item.authorName}</strong><small className="mono">{item.authorId}</small></td><td><div><strong className="mono">{item.id}</strong><small>{item.content || `[${localizedEnum(item.mediaKind, momentMediaKindLabels, '媒体')} × ${item.mediaCount}]`}</small></div></td><td>{item.likeCount} 赞 · {item.commentCount} 评</td><td>{localizedEnum(item.visibility, momentVisibilityLabels, '自定义范围')}</td><td><Badge value={item.status} /></td><td><div className="row-actions">{momentActions(item).map(([next, label]) => <button type="button" className="button secondary" disabled={!can('content.write')} key={next} onClick={() => setAction({ kind: 'moment', item, status: next })}>{label}</button>)}</div></td></tr>)}</tbody></table></div><Pagination data={moments.data} onPage={paginate} /></DataPanel> : <DataPanel loading={stickers.loading} error={stickers.error} retry={stickers.reload} empty={!stickers.data?.items.length} emptyTitle="没有匹配的表情包" emptyDetail="先创建分类，再创建表情包并添加表情项。"><div className="table-wrap"><table><thead><tr><th>表情包</th><th>分类</th><th>创建者</th><th>表情项</th><th>审核备注</th><th>状态</th><th>操作</th></tr></thead><tbody>{stickers.data?.items.map((item) => <tr key={item.id}><td><div><strong>{item.name}</strong><small className="mono">{item.id}</small><small>{item.description || '暂无描述'}</small></div></td><td>{item.categoryName}</td><td className="mono">{item.createdBy || '—'}</td><td><div><strong>{item.itemCount} 个</strong><div className="sticker-item-list">{item.items?.slice(0, 4).map((sticker) => <button type="button" className="row-action" key={sticker.id} disabled={!can('content.write') || item.status === 'published'} onClick={() => openItem(item, sticker)}>{sticker.emoji || sticker.name}</button>)}{(item.items?.length ?? 0) > 4 && <small>+{(item.items?.length ?? 0) - 4}</small>}</div></div></td><td>{item.reviewReason || '—'}</td><td><Badge value={item.status} /></td><td><div className="row-actions wrap"><button type="button" className="button secondary compact" disabled={!can('content.write') || item.status === 'published'} onClick={() => openPack(item)}>编辑</button><button type="button" className="button secondary compact" disabled={!can('content.write') || item.status === 'published'} onClick={() => openItem(item)}><Plus size={14} />表情</button>{stickerActions(item).map(([next, label]) => <button type="button" className="button secondary compact" disabled={!can('content.write')} key={next} onClick={() => setAction({ kind: 'sticker', item, status: next })}>{label}</button>)}</div></td></tr>)}</tbody></table></div><Pagination data={stickers.data} onPage={paginate} /></DataPanel>}
    <ConfirmDialog open={Boolean(action)} title={action?.kind === 'moment' ? '确认处置朋友圈' : '确认审核表情包'} detail={action ? `目标 ${action.item.id} 将变更为「${statusMap[action.status]?.label ?? action.status}」。` : ''} confirmLabel="确认并记录" danger={action?.status === 'deleted' || action?.status === 'rejected' || action?.status === 'disabled'} confirmDisabled={!reason.trim()} onClose={() => { setAction(undefined); setReason(''); }} onConfirm={execute}><label className="field-label">处置理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog>
    <ConfirmDialog open={Boolean(categoryDraft)} title={categoryDraft?.id ? '编辑表情分类' : '创建表情分类'} detail="分类用于组织客户端表情商店，可随时调整排序或停用。" confirmLabel="保存分类" confirmDisabled={!categoryDraft?.name.trim() || !operationReason.trim()} discardConfirmation={categoryDirty ? { title: '放弃未保存的分类？', detail: '分类名称、排序、启用状态和操作理由将全部丢失。' } : undefined} onClose={closeOperation} onConfirm={saveCategory}>{categoryDraft && <><div className="form-grid"><label className="field-label">分类名称<input value={categoryDraft.name} maxLength={100} onChange={(event) => setCategoryDraft((value) => value ? { ...value, name: event.target.value } : value)} required /></label><label className="field-label">排序<input type="number" value={categoryDraft.sortOrder} onChange={(event) => setCategoryDraft((value) => value ? { ...value, sortOrder: Number(event.target.value) } : value)} /></label></div><Toggle label="启用分类" description="停用后客户端商店不再展示该分类及其中表情包。" checked={categoryDraft.enabled} onChange={(enabled) => setCategoryDraft((value) => value ? { ...value, enabled } : value)} /><label className="field-label">操作理由<textarea value={operationReason} maxLength={500} onChange={(event) => setOperationReason(event.target.value)} required /></label></>}</ConfirmDialog>
    <ConfirmDialog open={Boolean(packDraft)} title={packDraft?.id ? '编辑表情包' : '创建表情包'} detail="封面从文件存储中的真实可用图片选择，避免错误编号或未完成上传的资源。" confirmLabel="保存表情包" confirmDisabled={!packDraft?.categoryId || !packDraft?.name.trim() || !packDraft?.coverMediaId.trim() || !operationReason.trim()} discardConfirmation={packDirty ? { title: '放弃未保存的表情包？', detail: '名称、封面、描述、状态和操作理由将全部丢失。' } : undefined} onClose={closeOperation} onConfirm={savePack}>{packDraft && <><div className="form-grid"><label className="field-label">所属分类<select value={packDraft.categoryId} onChange={(event) => setPackDraft((value) => value ? { ...value, categoryId: event.target.value } : value)}>{categories.data?.map((item) => <option key={item.id} value={item.id}>{item.name}{item.enabled ? '' : '（已停用）'}</option>)}</select></label><label className="field-label">表情包名称<input value={packDraft.name} maxLength={100} onChange={(event) => setPackDraft((value) => value ? { ...value, name: event.target.value } : value)} required /></label><label className="field-label">保存状态<select value={packDraft.status} onChange={(event) => setPackDraft((value) => value ? { ...value, status: event.target.value as StickerPackInput['status'] } : value)}><option value="draft">草稿</option><option value="reviewing">提交审核</option></select></label><label className="field-label">排序<input type="number" value={packDraft.sortOrder} onChange={(event) => setPackDraft((value) => value ? { ...value, sortOrder: Number(event.target.value) } : value)} /></label></div><MediaPicker value={packDraft.coverMediaId} onChange={(coverMediaId) => setPackDraft((value) => value ? { ...value, coverMediaId } : value)} label="表情包封面图片" /><label className="field-label">描述<textarea value={packDraft.description} maxLength={2000} onChange={(event) => setPackDraft((value) => value ? { ...value, description: event.target.value } : value)} /></label><label className="field-label">操作理由<textarea value={operationReason} maxLength={500} onChange={(event) => setOperationReason(event.target.value)} required /></label></>}</ConfirmDialog>
    <ConfirmDialog open={Boolean(itemEditor)} title={itemEditor?.draft.id ? '编辑表情项' : '添加表情项'} detail={itemEditor ? `添加到「${itemEditor.pack.name}」。请选择文件存储中已就绪的真实图片。` : ''} confirmLabel="保存表情项" confirmDisabled={!itemEditor?.draft.name.trim() || !itemEditor?.draft.mediaId.trim() || !operationReason.trim()} discardConfirmation={itemDirty ? { title: '放弃未保存的表情项？', detail: '名称、图片、快捷 Emoji、状态和操作理由将全部丢失。' } : undefined} onClose={closeOperation} onConfirm={saveItem}>{itemEditor && <><div className="form-grid"><label className="field-label">名称<input value={itemEditor.draft.name} maxLength={100} onChange={(event) => setItemEditor((value) => value ? { ...value, draft: { ...value.draft, name: event.target.value } } : value)} required /></label><label className="field-label">快捷 Emoji<input value={itemEditor.draft.emoji} maxLength={32} onChange={(event) => setItemEditor((value) => value ? { ...value, draft: { ...value.draft, emoji: event.target.value } } : value)} placeholder="例如 🙂" /></label><label className="field-label">状态<select value={itemEditor.draft.status} onChange={(event) => setItemEditor((value) => value ? { ...value, draft: { ...value.draft, status: event.target.value as StickerItemInput['status'] } } : value)}><option value="published">启用</option><option value="disabled">停用</option></select></label><label className="field-label">排序<input type="number" value={itemEditor.draft.sortOrder} onChange={(event) => setItemEditor((value) => value ? { ...value, draft: { ...value.draft, sortOrder: Number(event.target.value) } } : value)} /></label></div><MediaPicker value={itemEditor.draft.mediaId} onChange={(mediaId) => setItemEditor((value) => value ? { ...value, draft: { ...value.draft, mediaId } } : value)} label="表情图片" /><label className="field-label">操作理由<textarea value={operationReason} maxLength={500} onChange={(event) => setOperationReason(event.target.value)} required /></label></>}</ConfirmDialog>
  </>;
}

type InfrastructureTab = 'overview' | 'connections' | 'channels' | 'devices' | 'system-users' | 'robots' | 'plugins' | 'livekit';
const infrastructureTabs: Array<[InfrastructureTab, string]> = [['overview', '运行概览'], ['connections', '连接'], ['channels', '频道与消息'], ['devices', '设备'], ['system-users', '系统账号'], ['robots', '机器人'], ['plugins', '插件'], ['livekit', '音视频房间']];

function ImInfrastructurePage() {
  const [tab, setTab] = useState<InfrastructureTab>('overview');
  const [unsavedMessage, setUnsavedMessage] = useState<string>();
  const [pendingTab, setPendingTab] = useState<InfrastructureTab>();
  const requestTab = (next: InfrastructureTab) => {
    if (next === tab) return;
    if (unsavedMessage) { setPendingTab(next); return; }
    setTab(next);
  };
  const confirmTab = () => {
    const next = pendingTab;
    setPendingTab(undefined);
    setUnsavedMessage(undefined);
    if (next) setTab(next);
  };
  return <InfrastructureUnsavedChangesContext.Provider value={setUnsavedMessage}><PageHeader title="IM 基础设施" description="通过 Go 服务统一查看和处置 WuKongIM 与 LiveKit；上游管理令牌和密钥不会返回浏览器。" />
    <div className="tabs" role="tablist" aria-label="基础设施模块" onKeyDown={tabListKeyDown}>{infrastructureTabs.map(([value, label]) => <button type="button" role="tab" tabIndex={tab === value ? 0 : -1} aria-selected={tab === value} className={tab === value ? 'active' : ''} key={value} onClick={() => requestTab(value)}>{label}</button>)}</div>
    {tab === 'overview' ? <WukongOverviewPanel /> : tab === 'connections' ? <WukongConnectionsPanel /> : tab === 'channels' ? <WukongChannelsPanel /> : tab === 'devices' ? <WukongDevicesPanel /> : tab === 'system-users' ? <WukongSystemUsersPanel /> : tab === 'robots' ? <WukongRobotsPanel /> : tab === 'plugins' ? <WukongPluginsPanel /> : <LiveKitRoomsPanel />}
    <ConfirmDialog open={Boolean(pendingTab)} title="放弃未提交的插件发布？" detail={`${unsavedMessage ?? '插件发布资料尚未提交'}。切换后，已选择的文件、签名和发布理由都会丢失。`} confirmLabel="放弃修改并切换" danger onClose={() => setPendingTab(undefined)} onConfirm={confirmTab} />
  </InfrastructureUnsavedChangesContext.Provider>;
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
  return <><section className="operations-block"><div className="panel-heading"><div><h2>系统账号</h2><p>系统账号可主动向任意用户和频道发送消息。从真实正常账号中选择后，变更会通过业务 Outbox 同步到 WuKongIM 运行缓存。</p></div></div><div className="inline-form account-operation"><AccountPicker compact value={userId} onChange={setUserId} searchLabel="查找系统账号候选" selectLabel="系统账号候选" excludeIds={users.data?.filter((item) => item.enabled).map((item) => item.userId) ?? []} /><button type="button" className="button primary" disabled={!can('operations.write') || !userId.trim()} onClick={() => setAction({ userId: userId.trim(), name: userId.trim(), enabled: true })}><Plus size={15} />设为系统账号</button></div></section><div style={{ height: 14 }} />
    <DataPanel loading={users.loading} error={users.error} retry={users.reload} empty={!users.data?.length} emptyTitle="尚未配置系统账号" emptyDetail="普通业务消息不需要系统账号；仅通知、客服机器人等特殊身份需要配置。"><div className="table-wrap"><table><thead><tr><th>用户</th><th>业务状态</th><th>WuKong 同步</th><th>更新人</th><th>原因</th><th>更新时间</th><th>操作</th></tr></thead><tbody>{users.data?.map((item: WukongSystemUser) => <tr key={item.userId}><td><strong>{item.name || item.userId}</strong><small className="mono">{item.userId}</small></td><td><Badge value={item.enabled ? 'active' : 'disabled'} label={item.enabled ? '已启用' : '已撤销'} /></td><td><Badge value={item.syncStatus === 'failed' ? 'failed' : item.syncStatus === 'synced' ? 'active' : 'warning'} label={item.syncStatus === 'synced' ? '已同步' : item.syncStatus === 'failed' ? '同步失败' : '处理中'} /></td><td className="mono">{item.updatedBy}</td><td>{item.reason}</td><td>{item.updatedAt}</td><td><button type="button" className="button secondary compact danger-text" disabled={!can('operations.write') || !item.enabled} onClick={() => setAction({ userId: item.userId, name: item.name, enabled: false })}>撤销</button></td></tr>)}</tbody></table></div></DataPanel>
    <ConfirmDialog open={Boolean(action)} title={action?.enabled ? '设为系统账号' : '撤销系统账号'} detail={action ? `${action.name && action.name !== action.userId ? `${action.name}（${action.userId}）` : action.userId}${action.enabled ? '将可绕过普通频道发送权限' : '将恢复普通用户发送权限'}。` : ''} confirmLabel="确认并同步" danger={!action?.enabled} confirmDisabled={!reason.trim()} onClose={() => { setAction(undefined); setReason(''); }} onConfirm={execute}><label className="field-label">操作理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog></>;
}

function InfrastructureMetric({ label, value, detail, tone = 'active', badge = '实时' }: { label: string; value: string; detail: string; tone?: string; badge?: string }) {
  return <div className="metric"><div><span>{label}</span><Badge value={tone} label={badge} /></div><strong>{value}</strong><p>{detail}</p></div>;
}

function infrastructureCount(value: number | null) {
  return value === null ? '—' : value.toLocaleString();
}

function RuntimeSettingStatus({ value }: { value: boolean | null }) {
  if (value === null) return <Badge value="warning" label="状态未知" />;
  return <Badge value={value ? 'active' : 'disabled'} label={value ? '已开启' : '未开启'} />;
}

function WukongOverviewPanel() {
  const { api, mode } = useApi();
  const state = useResource(async () => { const [overview, settings, nodes] = await Promise.all([api.getWukongOverview(), api.getWukongSettings(), api.getWukongNodes()]); return { overview, settings, nodes }; }, [api, mode]);
  if (state.loading) return <Skeleton rows={7} />;
  if (state.error || !state.data) return <ErrorState message={state.error} retry={state.reload} />;
  const { overview, settings, nodes } = state.data;
  const resourceDetail = [overview.memoryBytes === null ? '' : `${Math.round(overview.memoryBytes / 1024 / 1024)} MB`, overview.goroutines === null ? '' : `${overview.goroutines} 协程`].filter(Boolean).join(' · ') || '资源明细未上报';
  return <><div className="metric-strip"><InfrastructureMetric label="当前连接" value={infrastructureCount(overview.connections)} detail={overview.userHandlers === null ? '用户处理器未上报' : `${overview.userHandlers} 个用户处理器`} tone={overview.connections === null ? 'warning' : 'active'} badge={overview.connections === null ? '未上报' : '实时'} /><InfrastructureMetric label="累计流入" value={infrastructureCount(overview.inMessages)} detail={overview.outMessages === null ? '流出指标未上报' : `流出 ${overview.outMessages.toLocaleString()}`} tone={overview.inMessages === null ? 'warning' : 'active'} badge={overview.inMessages === null ? '未上报' : '实时'} /><InfrastructureMetric label="资源" value={overview.cpu === null ? '—' : `${overview.cpu.toFixed(1)}%`} detail={resourceDetail} tone={overview.cpu === null ? 'warning' : 'active'} badge={overview.cpu === null ? '未上报' : '实时'} /><InfrastructureMetric label="重试队列" value={infrastructureCount(overview.retryQueue)} detail={overview.retryQueue === null ? '服务未返回该指标' : overview.retryQueue ? '需要关注积压' : '当前无积压'} tone={overview.retryQueue === null ? 'warning' : 'active'} badge={overview.retryQueue === null ? '未上报' : '实时'} /></div>
    <div className="configuration-grid"><div className="configuration-item"><span>Prometheus 指标</span><RuntimeSettingStatus value={settings.prometheusEnabled} /></div><div className="configuration-item"><span>Trace 追踪</span><RuntimeSettingStatus value={settings.traceEnabled} /></div><div className="configuration-item"><span>Loki 日志</span><RuntimeSettingStatus value={settings.lokiEnabled} /></div><div className="configuration-item"><span>压力测试模式</span><RuntimeSettingStatus value={settings.stressEnabled} /></div></div>
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
const pluginStatusLabels: Record<string, string> = { active: '运行中', normal: '运行中', disabled: '已停用', offline: '已离线', failed: '启动失败', installed: '已安装', upgraded: '已升级', uninstalled: '已卸载' };
const pluginActionLabels: Record<string, string> = { install: '安装', upgrade: '升级', enable: '启用', disable: '停用', uninstall: '卸载', config: '更新配置' };
function WukongPluginsPanel() {
  const { api, mode, notify, can } = useApi();
  const state = useResource(() => api.getWukongPlugins(), [api, mode]);
  const events = useResource(() => api.getWukongPluginEvents(), [api, mode]);
  const [logTarget, setLogTarget] = useState<WukongPlugin>();
  const logs = useResource(() => logTarget ? api.getWukongPluginLogs(logTarget.no, logTarget.nodeId, 200) : Promise.resolve([]), [api, mode, logTarget?.no, logTarget?.nodeId]);
  const [action, setAction] = useState<PluginAction>(); const [reason, setReason] = useState(''); const [configText, setConfigText] = useState('{}');
  const [publishMode, setPublishMode] = useState<'install' | 'upgrade'>('install'); const [publishNode, setPublishNode] = useState(1); const [upgradeNo, setUpgradeNo] = useState('');
  const [bundle, setBundle] = useState<File>(); const [manifest, setManifest] = useState<File>(); const [signature, setSignature] = useState(''); const [publishReason, setPublishReason] = useState(''); const [publishing, setPublishing] = useState(false); const [publishError, setPublishError] = useState(''); const [publishInputKey, setPublishInputKey] = useState(0);
  const publishDirty = Boolean(bundle || manifest || signature.trim() || publishReason.trim());
  useUnsavedChanges(publishDirty, '签名插件发布资料尚未提交');
  useInfrastructureUnsavedChanges(publishDirty, '签名插件发布资料尚未提交');
  const openConfig = (plugin: WukongPlugin) => { setConfigText(JSON.stringify(plugin.config, null, 2)); setAction({ kind: 'config', plugin }); };
  const execute = async () => {
    if (!action) return;
    if (action.kind === 'config') { const parsed = JSON.parse(configText) as unknown; if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('插件配置必须是 JSON 对象'); await api.updateWukongPluginConfig(action.plugin.no, action.plugin.nodeId, parsed as Record<string, unknown>, reason.trim()); }
    else if (action.kind === 'uninstall') await api.uninstallWukongPlugin(action.plugin.no, action.plugin.nodeId, reason.trim());
    else await api.setWukongPluginEnabled(action.plugin.no, action.plugin.nodeId, action.kind === 'enable', reason.trim());
    await Promise.all([state.reload(), events.reload()]); notify(action.kind === 'config' ? '插件配置已更新' : action.kind === 'uninstall' ? '插件已卸载' : action.kind === 'enable' ? '插件已启用并完成自检' : '插件已停用');
  };
  const publish = async () => {
    if (!Number.isInteger(publishNode) || publishNode < 1) { setPublishError('节点 ID 必须是大于 0 的整数'); return; }
    if (!bundle || !manifest || !signature.trim() || !publishReason.trim() || (publishMode === 'upgrade' && !upgradeNo)) return;
    setPublishError('');
    setPublishing(true);
    try { if (publishMode === 'install') await api.installWukongPlugin(bundle, manifest, signature, publishNode, publishReason.trim()); else await api.upgradeWukongPlugin(upgradeNo, bundle, manifest, signature, publishNode, publishReason.trim()); await Promise.all([state.reload(), events.reload()]); setBundle(undefined); setManifest(undefined); setSignature(''); setPublishReason(''); setPublishInputKey((value) => value + 1); notify(publishMode === 'install' ? '签名插件已安装并通过启动自证' : '签名插件已升级并通过启动自证'); }
    catch (cause) { setPublishError(errorMessage(cause)); }
    finally { setPublishing(false); }
  };
  const actionTitle = action?.kind === 'config' ? '更新插件配置' : action?.kind === 'uninstall' ? '卸载插件' : action?.kind === 'enable' ? '启用插件' : '停用插件';
  return <><div className="operations-block"><div className="panel-heading"><div><h2>签名插件发布</h2><p>上传原始 .wkp、签名清单和离线 Ed25519 签名；服务端校验白名单并在启动后核对自报元数据。AI Receive 插件会被拒绝。</p></div></div><div className="form-grid" aria-describedby={publishError ? 'plugin-publish-error' : undefined}><label className="field-label">发布方式<select value={publishMode} onChange={(event) => { setPublishMode(event.target.value as 'install' | 'upgrade'); setPublishError(''); }}><option value="install">首次安装</option><option value="upgrade">升级现有插件</option></select></label><label className="field-label">节点 ID<input type="number" min="1" value={publishNode} onChange={(event) => { setPublishNode(Number(event.target.value)); setPublishError(''); }} /></label>{publishMode === 'upgrade' && <label className="field-label">升级目标<select value={upgradeNo} onChange={(event) => { setUpgradeNo(event.target.value); setPublishError(''); }}><option value="">请选择</option>{state.data?.filter((item) => item.managed).map((item) => <option key={item.no} value={item.no}>{item.no}</option>)}</select></label>}<label className="field-label">签名清单 JSON<input key={`manifest-${publishInputKey}`} aria-label="签名清单 JSON" type="file" accept="application/json,.json" onChange={(event) => { setManifest(event.target.files?.[0]); setPublishError(''); }} /></label><label className="field-label">插件可执行文件<input key={`bundle-${publishInputKey}`} aria-label="插件可执行文件" type="file" accept=".wkp" onChange={(event) => { setBundle(event.target.files?.[0]); setPublishError(''); }} /></label><label className="field-label">Ed25519 签名（Base64）<textarea className="mono" value={signature} onChange={(event) => { setSignature(event.target.value); setPublishError(''); }} /></label><label className="field-label">发布理由 / 工单<textarea value={publishReason} maxLength={500} onChange={(event) => { setPublishReason(event.target.value); setPublishError(''); }} /></label></div>{publishError && <div id="plugin-publish-error" className="inline-notice danger" role="alert"><AlertTriangle size={15} />{publishError}</div>}<button type="button" className="button primary" disabled={!can('operations.write') || publishing || !bundle || !manifest || !signature.trim() || !publishReason.trim() || (publishMode === 'upgrade' && !upgradeNo)} onClick={() => void publish()}>{publishing ? '正在校验并启动…' : publishMode === 'install' ? '校验并安装' : '校验并升级'}</button></div><div style={{ height: 14 }} />
    <DataPanel loading={state.loading} error={state.error} retry={state.reload} empty={!state.data?.length} emptyTitle="没有已安装插件" emptyDetail="仅签名白名单中的插件允许安装；系统策略插件是受保护的内置插件。"><div className="table-wrap"><table><thead><tr><th>插件</th><th>节点</th><th>版本</th><th>方法</th><th>信任</th><th>状态</th><th>操作</th></tr></thead><tbody>{state.data?.map((item: WukongPlugin) => { const status = item.lifecycleStatus || item.status; return <tr key={`${item.nodeId}-${item.no}`}><td><strong>{item.name || item.no}</strong><small className="mono">{item.no}</small>{item.sha256 && <small className="mono">SHA-256 {item.sha256.slice(0, 12)}…</small>}</td><td>{item.nodeId}</td><td>{item.version}</td><td>{item.methods.join(', ') || '—'}</td><td><Badge value={item.verified ? 'active' : 'warning'} label={item.builtIn ? '内置校验' : item.managed && item.verified ? `签名 · ${item.keyId}` : '非托管'} /></td><td><Badge value={item.status === 'normal' || item.status === 'active' ? 'active' : item.status === 'offline' ? 'down' : 'disabled'} label={pluginStatusLabels[status] ?? '状态未知'} /></td><td><div className="row-actions"><button className="button secondary compact" onClick={() => setLogTarget(item)}>运行日志</button><button className="button secondary compact" disabled={!can('operations.write')} onClick={() => openConfig(item)}>配置</button>{item.managed && <button className="button secondary compact" disabled={!can('operations.write')} onClick={() => setAction({ kind: item.status === 'disabled' ? 'enable' : 'disable', plugin: item })}>{item.status === 'disabled' ? '启用' : '停用'}</button>}<button className="button secondary compact danger-text" disabled={!can('operations.write') || item.builtIn} title={item.builtIn ? '系统策略插件禁止卸载' : ''} onClick={() => setAction({ kind: 'uninstall', plugin: item })}>卸载</button></div></td></tr>; })}</tbody></table></div></DataPanel>
    {logTarget && <><div style={{ height: 14 }} /><section className="operations-block plugin-runtime-panel"><div className="panel-heading"><div><h2>插件运行日志</h2><p><span className="mono">{logTarget.no}</span> · 节点 {logTarget.nodeId} · 仅保留服务端内存中的有界脱敏尾部日志</p></div><div className="row-actions"><button className="button secondary compact" onClick={() => void logs.reload()}>刷新</button><button className="button secondary compact" onClick={() => setLogTarget(undefined)}>关闭</button></div></div><DataPanel loading={logs.loading} error={logs.error} retry={logs.reload} empty={!logs.data?.length} emptyTitle="暂无运行日志" emptyDetail="插件没有输出，或服务重启后内存日志尚未产生。"><div className="plugin-runtime-log" role="log" aria-label={`${logTarget.no} 运行日志`}>{logs.data?.map((entry: WukongPluginLogEntry) => <div className="plugin-runtime-line" key={entry.sequence}><time>{entry.timestamp ? new Date(entry.timestamp).toLocaleString('zh-CN') : '—'}</time><Badge value={entry.stream === 'stderr' ? 'warning' : 'active'} label={entry.stream || 'stdout'} /><code>{entry.message}</code></div>)}</div></DataPanel></section></>}
    <div style={{ height: 14 }} /><DataPanel loading={events.loading} error={events.error} retry={events.reload} empty={!events.data?.length} emptyTitle="没有插件生命周期事件" emptyDetail="安装、升级、停启和卸载结果会记录在这里。"><div className="table-wrap"><table><thead><tr><th>时间</th><th>插件</th><th>动作</th><th>结果</th><th>操作人</th><th>理由</th></tr></thead><tbody>{events.data?.map((item: WukongPluginEvent) => <tr key={item.id}><td>{item.createdAt}</td><td className="mono">{item.pluginNo}</td><td>{pluginActionLabels[item.action] ?? '其他操作'}</td><td><Badge value={item.status === 'failed' ? 'failed' : 'active'} label={item.status === 'failed' ? '失败' : '成功'} /></td><td>{item.actor}</td><td>{item.reason}</td></tr>)}</tbody></table></div></DataPanel>
    <ConfirmDialog open={Boolean(action)} title={actionTitle} detail={action ? `${action.plugin.no} · 节点 ${action.plugin.nodeId}` : ''} confirmLabel="确认执行" danger={action?.kind === 'uninstall' || action?.kind === 'disable'} confirmDisabled={!reason.trim()} onClose={() => { setAction(undefined); setReason(''); }} onConfirm={execute}>{action?.kind === 'config' && <label className="field-label">JSON 配置<textarea className="mono" value={configText} onChange={(event) => setConfigText(event.target.value)} required /></label>}<label className="field-label">操作理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog></>;
}

type LiveKitAction = { kind: 'participant'; room: string; identity: string } | { kind: 'room'; room: string };
function LiveKitRoomsPanel() {
  const { api, mode, notify, can } = useApi(); const rooms = useResource(() => api.getLiveKitRooms(), [api, mode]); const metrics = useResource(() => api.getLiveKitMetrics(), [api, mode]); const [selected, setSelected] = useState(''); const participants = useResource(() => selected ? api.getLiveKitParticipants(selected) : Promise.resolve([]), [api, mode, selected]); const [action, setAction] = useState<LiveKitAction>(); const [reason, setReason] = useState('');
  useEffect(() => { if (!selected && rooms.data?.length) setSelected(rooms.data[0].name); }, [rooms.data, selected]);
  const execute = async () => { if (!action) return; if (action.kind === 'participant') { await api.removeLiveKitParticipant(action.room, action.identity, reason.trim()); await participants.reload(); notify('参与者已移出房间'); } else { await api.deleteLiveKitRoom(action.room, reason.trim()); if (selected === action.room) setSelected(''); await rooms.reload(); notify('通话房间已关闭'); } };
  const live = metrics.data as LiveKitMetrics | undefined;
  return <><DataPanel loading={metrics.loading} error={metrics.error} retry={metrics.reload} empty={!live} emptyTitle="暂时没有 LiveKit 指标" emptyDetail="请检查 Prometheus 抓取状态。">{live && <div className="metric-strip" aria-label="LiveKit 资源指标"><InfrastructureMetric label="实时房间" value={live.activeRooms.toLocaleString()} detail={`${live.activeParticipants} 位在线参与者`} tone={live.healthy ? 'active' : 'failed'} badge={live.healthy ? '健康' : '异常'} /><InfrastructureMetric label="媒体进程资源" value={`${live.cpuPercent.toFixed(1)}%`} detail={`${formatBytes(live.residentMemoryBytes)} 内存`} /><InfrastructureMetric label="媒体网络" value={`${formatBytes(live.networkTransmitBytesPerSecond)}/s`} detail={`接收 ${formatBytes(live.networkReceiveBytesPerSecond)}/s`} /><InfrastructureMetric label="近 5 分钟丢包" value={`${live.packetLossPercent.toFixed(3)}%`} detail={`近 1 小时 ${live.participantJoinsLastHour} 人次 · ${live.roomsCompletedLastHour} 个房间`} tone={live.packetLossPercent > 1 ? 'warning' : 'active'} badge={live.packetLossPercent > 1 ? '需关注' : '正常'} /></div>}</DataPanel><div style={{ height: 14 }} /><DataPanel loading={rooms.loading} error={rooms.error} retry={rooms.reload} empty={!rooms.data?.length} emptyTitle="当前没有 LiveKit 房间" emptyDetail="通话建立后房间会显示在这里。"><div className="table-wrap"><table><thead><tr><th>房间</th><th>参与者</th><th>发布者</th><th>上限</th><th>录制</th><th>创建时间</th><th>操作</th></tr></thead><tbody>{rooms.data?.map((room: LiveKitRoom) => <tr key={room.sid}><td><button className="row-action mono" onClick={() => setSelected(room.name)}>{room.name}</button></td><td>{room.participantCount}</td><td>{room.publisherCount}</td><td>{room.maxParticipants}</td><td>{room.activeRecording ? '是' : '否'}</td><td>{room.createdAt}</td><td><button className="button secondary compact danger-text" disabled={!can('operations.write')} onClick={() => setAction({ kind: 'room', room: room.name })}>关闭房间</button></td></tr>)}</tbody></table></div></DataPanel>
    {selected && <><div style={{ height: 14 }} /><DataPanel loading={participants.loading} error={participants.error} retry={participants.reload} empty={!participants.data?.length} emptyTitle="房间内没有参与者" emptyDetail={selected}><div className="table-wrap"><table><thead><tr><th>参与者</th><th>状态</th><th>轨道</th><th>屏幕共享</th><th>加入时间</th><th>操作</th></tr></thead><tbody>{participants.data?.map((item: LiveKitParticipant) => <tr key={item.sid}><td><strong>{item.name || item.identity}</strong><small className="mono">{item.identity}</small></td><td><Badge value={item.state === 'ACTIVE' ? 'active' : 'warning'} label={item.state === 'ACTIVE' ? '在线' : '连接中'} /></td><td>{item.trackCount}</td><td>{item.screenSharing ? '正在共享' : '否'}</td><td>{item.joinedAt}</td><td><button className="button secondary compact" disabled={!can('operations.write')} onClick={() => setAction({ kind: 'participant', room: selected, identity: item.identity })}>移出房间</button></td></tr>)}</tbody></table></div></DataPanel></>}
    <ConfirmDialog open={Boolean(action)} title={action?.kind === 'room' ? '关闭 LiveKit 房间' : '移出参与者'} detail={action ? action.kind === 'room' ? `房间 ${action.room} 将立即关闭。` : `${action.identity} 将从 ${action.room} 断开。` : ''} confirmLabel="确认执行" danger confirmDisabled={!reason.trim()} onClose={() => { setAction(undefined); setReason(''); }} onConfirm={execute}><label className="field-label">操作理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog></>;
}

type ConfirmableAdminAction = { title: string; detail: string; danger?: boolean; run: (reason: string) => Promise<void> };
const channelTypeLabels: Record<number, string> = { 4: '社区', 5: '社区话题', 6: '资讯', 9: '直播' };
const businessChannelRoleLabels: Record<string, string> = { owner: '所有者', admin: '管理员', moderator: '版主', operator: '运营人员', member: '成员' };
const defaultBusinessChannelInput: BusinessChannelInput = { ownerId: '', channelType: 4, name: '', avatarUrl: '', parentId: '', description: '', visibility: 'public', joinPolicy: 'open', postingPolicy: 'members', slowModeSeconds: 0, metadata: {} };

function BusinessChannelsPage() {
  const { api, mode, can, notify } = useApi();
  const [query, setQuery] = useState(''), deferred = useDebouncedValue(query), [channelType, setChannelType] = useState(0), [page, setPage] = useState(1), [cursors, setCursors] = useState<Record<number, string>>({ 1: '' });
  const channels = useResource(() => api.getBusinessChannels(deferred, channelType, '', page, 20, cursors[page] ?? ''), [api, mode, deferred, channelType, page, cursors]);
  const topicParents = useResource(() => api.getBusinessChannels('', 4, '', 1, 100, ''), [api, mode]);
  const [selected, setSelected] = useState<BusinessChannelRecord>();
  const members = useResource(() => selected ? api.getBusinessChannelMembers(selected.id, selected.channelType) : Promise.resolve({ items: [] as BusinessChannelMemberRecord[] }), [api, mode, selected?.id, selected?.channelType]);
  const access = useResource(() => selected ? api.getBusinessChannelAccess(selected.id, selected.channelType) : Promise.resolve([] as BusinessChannelAccessRecord[]), [api, mode, selected?.id, selected?.channelType]);
  const [action, setAction] = useState<ConfirmableAdminAction>(), [reason, setReason] = useState('');
  const [createOpen, setCreateOpen] = useState(false), [createReason, setCreateReason] = useState(''), [draft, setDraft] = useState<BusinessChannelInput>(defaultBusinessChannelInput);
  const [memberUserId, setMemberUserId] = useState(''), [memberExpiry, setMemberExpiry] = useState('');
  const [accessUserId, setAccessUserId] = useState(''), [accessType, setAccessType] = useState<'allow' | 'deny'>('deny');
  const [slowMode, setSlowMode] = useState(0);
  const createDirty = createOpen && (JSON.stringify(draft) !== JSON.stringify(defaultBusinessChannelInput) || Boolean(createReason.trim()));
  const closeCreate = () => { setCreateOpen(false); setDraft(defaultBusinessChannelInput); setCreateReason(''); };
  useEffect(() => { setPage(1); setCursors({ 1: '' }); }, [deferred, channelType]);
  useEffect(() => { if (selected) setSlowMode(selected.slowModeSeconds); }, [selected]);
  useEffect(() => {
    if (!createOpen || draft.channelType !== 5 || draft.parentId || !topicParents.data?.items.length) return;
    setDraft((value) => ({ ...value, parentId: topicParents.data?.items[0].id ?? '' }));
  }, [createOpen, draft.channelType, draft.parentId, topicParents.data]);
  const paginate = (next: number) => { if (next > page && channels.data?.nextCursor) setCursors((value) => ({ ...value, [next]: channels.data?.nextCursor ?? '' })); setPage(next); };
  const refreshSelected = async () => { await Promise.all([channels.reload(), members.reload(), access.reload()]); };
  const openAction = (next: ConfirmableAdminAction) => { setReason(''); setAction(next); };
  const execute = async () => { if (!action) return; await action.run(reason.trim()); await refreshSelected(); notify('频道运营配置已更新'); };
  const updateChannel = (channel: BusinessChannelRecord, update: Partial<BusinessChannelRecord>, title: string, detail: string, danger = false) => {
    if (update.slowModeSeconds !== undefined && (!Number.isInteger(update.slowModeSeconds) || update.slowModeSeconds < 0 || update.slowModeSeconds > 86400)) { notify('慢速模式必须是 0 到 86400 之间的整数秒', 'danger'); return; }
    openAction({ title, detail, danger, run: async (why) => { const value = await api.updateBusinessChannel(channel.id, channel.channelType, update, why); setSelected(value); } });
  };
  const memberAction = (item: BusinessChannelMemberRecord, title: string, detail: string, run: (why: string) => Promise<void>, danger = false) => openAction({ title, detail, danger, run });
  const create = async () => { if (!Number.isInteger(draft.slowModeSeconds) || draft.slowModeSeconds < 0 || draft.slowModeSeconds > 86400) throw new Error('慢速模式必须是 0 到 86400 之间的整数秒'); const item = await api.createBusinessChannel(draft, createReason.trim()); setSelected(item); setDraft(defaultBusinessChannelInput); setCreateReason(''); await channels.reload(); notify(`${channelTypeLabels[item.channelType]}已创建`); };
  const addMember = () => {
    if (!selected || !memberUserId.trim()) return;
    const userId = memberUserId.trim();
    const expiryTimestamp = memberExpiry ? Date.parse(memberExpiry) : undefined;
    if (expiryTimestamp !== undefined && (!Number.isFinite(expiryTimestamp) || expiryTimestamp <= Date.now())) { notify('临时订阅到期时间必须晚于当前时间', 'danger'); return; }
    const expiresAt = expiryTimestamp === undefined ? undefined : new Date(expiryTimestamp).toISOString();
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
      <div className="business-grid"><div className="data-panel"><div className="panel-heading"><div><h2>成员与临时订阅</h2><p>到期成员由服务端定时清理并同步 WuKongIM。</p></div></div><div className="toolbar"><AccountPicker compact value={memberUserId} onChange={setMemberUserId} searchLabel="查找要添加的频道成员" selectLabel="频道成员账号" excludeIds={members.data?.items.map((item) => item.userId) ?? []} /><input className="select-control" aria-label="订阅到期时间" type="datetime-local" value={memberExpiry} onChange={(event) => setMemberExpiry(event.target.value)} /><button className="button primary" disabled={!can('channels.write') || !memberUserId.trim()} onClick={addMember}>添加</button></div><DataPanel loading={members.loading} error={members.error} retry={members.reload} empty={!members.data?.items.length} emptyTitle="没有频道成员" emptyDetail="添加成员或等待用户订阅。"><div className="table-wrap"><table><thead><tr><th>成员</th><th>角色</th><th>禁言</th><th>订阅期限</th><th>操作</th></tr></thead><tbody>{members.data?.items.map((item) => <tr key={item.userId}><td><strong>{item.name}</strong><small className="mono">{item.userId}</small></td><td>{businessChannelRoleLabels[item.role] ?? '未知角色'}</td><td>{item.mutedUntil ? dateTimeLabel(item.mutedUntil) : '否'}</td><td>{item.expiresAt ? dateTimeLabel(item.expiresAt) : '永久'}</td><td><div className="row-actions">{item.role !== 'owner' && <><button className="button secondary compact" disabled={!can('channels.write')} onClick={() => memberAction(item, '更新成员角色', `${item.userId} 将变更为${item.role === 'admin' ? '普通成员' : '管理员'}。`, (why) => api.updateBusinessChannelMember(selected.id, selected.channelType, item.userId, { role: item.role === 'admin' ? 'member' : 'admin' }, why))}>{item.role === 'admin' ? '设为成员' : '设为管理员'}</button><button className="button secondary compact" disabled={!can('channels.write')} onClick={() => memberAction(item, item.mutedUntil ? '解除成员禁言' : '禁言成员一小时', item.userId, (why) => api.updateBusinessChannelMember(selected.id, selected.channelType, item.userId, item.mutedUntil ? { clearMute: true } : { mutedUntil: new Date(Date.now() + 3600000).toISOString() }, why))}>{item.mutedUntil ? '解除禁言' : '禁言 1 小时'}</button><button className="button secondary compact" disabled={!can('channels.write')} onClick={() => memberAction(item, item.expiresAt ? '改为永久订阅' : '设置一天临时订阅', item.userId, (why) => api.updateBusinessChannelMember(selected.id, selected.channelType, item.userId, item.expiresAt ? { clearExpiry: true } : { expiresAt: new Date(Date.now() + 86400000).toISOString() }, why))}>{item.expiresAt ? '改为永久' : '临时 1 天'}</button><button className="button secondary compact danger-text" disabled={!can('channels.write')} onClick={() => memberAction(item, '移除频道成员', `${item.userId} 将立即失去频道访问权限。`, (why) => api.removeBusinessChannelMember(selected.id, selected.channelType, item.userId, why), true)}>移除</button></>}</div></td></tr>)}</tbody></table></div></DataPanel></div>
        <div className="data-panel"><div className="panel-heading"><div><h2>黑白名单</h2><p>黑名单拒绝访问；存在白名单时仅白名单成员可发送。</p></div></div><div className="toolbar"><AccountPicker compact value={accessUserId} onChange={setAccessUserId} searchLabel="查找要加入名单的账号" selectLabel="名单账号" /><select className="select-control" aria-label="名单类型" value={accessType} onChange={(event) => setAccessType(event.target.value as 'allow' | 'deny')}><option value="deny">黑名单</option><option value="allow">白名单</option></select><button className="button primary" disabled={!can('channels.write') || !accessUserId.trim()} onClick={addAccess}>加入</button></div><DataPanel loading={access.loading} error={access.error} retry={access.reload} empty={!access.data?.length} emptyTitle="名单为空" emptyDetail="当前没有额外访问限制。"><div className="table-wrap"><table><thead><tr><th>用户</th><th>类型</th><th>原因</th><th>操作</th></tr></thead><tbody>{access.data?.map((item) => <tr key={`${item.accessType}-${item.userId}`}><td><strong>{item.name}</strong><small className="mono">{item.userId}</small></td><td><Badge value={item.accessType === 'deny' ? 'failed' : 'active'} label={item.accessType === 'deny' ? '黑名单' : '白名单'} /></td><td>{item.reason}</td><td><button className="button secondary compact" disabled={!can('channels.write')} onClick={() => openAction({ title: '移除名单记录', detail: `${item.userId} 将移出${item.accessType === 'deny' ? '黑' : '白'}名单。`, run: (why) => api.setBusinessChannelAccess(selected.id, selected.channelType, item.userId, item.accessType, false, why) })}>移除</button></td></tr>)}</tbody></table></div></DataPanel></div></div></section>}
    <ConfirmDialog open={Boolean(action)} title={action?.title ?? ''} detail={action?.detail ?? ''} confirmLabel="确认并记录" danger={action?.danger} confirmDisabled={!reason.trim()} onClose={() => { setAction(undefined); setReason(''); }} onConfirm={execute}><label className="field-label">操作原因<textarea value={reason} maxLength={1000} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog>
    <ConfirmDialog open={createOpen} title="创建业务频道" detail="从真实账号和已有社区中选择归属；创建后将写入 PostgreSQL，并通过持久 Outbox 同步到 WuKongIM。" confirmLabel="确认创建" confirmDisabled={!createReason.trim() || !draft.ownerId.trim() || !draft.name.trim() || (draft.channelType === 5 && !draft.parentId?.trim())} discardConfirmation={createDirty ? { title: '放弃未保存的频道？', detail: '频道类型、名称、所有者、发布策略和操作理由将全部丢失。' } : undefined} onClose={closeCreate} onConfirm={create}>
      <div className="form-grid">
        <label className="field-label">频道类型<select value={draft.channelType} onChange={(event) => setDraft((value) => ({ ...value, channelType: Number(event.target.value) as BusinessChannelInput['channelType'], parentId: '' }))}><option value="4">社区</option><option value="5">社区话题</option><option value="6">资讯</option><option value="9">直播</option></select></label>
        <label className="field-label">频道名称<input value={draft.name} onChange={(event) => setDraft((value) => ({ ...value, name: event.target.value }))} required /></label>
      </div>
      <AccountPicker value={draft.ownerId} onChange={(ownerId) => setDraft((value) => ({ ...value, ownerId }))} searchLabel="查找频道所有者" selectLabel="频道所有者账号" />
      {draft.channelType === 5 && <label className="field-label">所属社区<select aria-label="话题所属社区" value={draft.parentId ?? ''} onChange={(event) => setDraft((value) => ({ ...value, parentId: event.target.value }))} required><option value="">请选择已有社区</option>{topicParents.data?.items.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select>{topicParents.loading && <small>正在加载真实社区…</small>}{!topicParents.loading && !topicParents.error && !topicParents.data?.items.length && <small className="danger-text">尚无可用社区，请先创建社区</small>}{topicParents.error && <small className="danger-text">社区列表加载失败，请重试</small>}</label>}
      <div className="form-grid"><label className="field-label">可见性<select value={draft.visibility} onChange={(event) => setDraft((value) => ({ ...value, visibility: event.target.value as BusinessChannelInput['visibility'] }))}><option value="public">公开</option><option value="private">私密</option></select></label><label className="field-label">加入策略<select value={draft.joinPolicy} onChange={(event) => setDraft((value) => ({ ...value, joinPolicy: event.target.value as BusinessChannelInput['joinPolicy'] }))}><option value="open">开放加入</option><option value="approval">需要审批</option><option value="invite">仅邀请</option><option value="closed">关闭加入</option></select></label><label className="field-label">发布策略<select value={draft.postingPolicy} onChange={(event) => setDraft((value) => ({ ...value, postingPolicy: event.target.value as BusinessChannelInput['postingPolicy'] }))}><option value="members">所有成员</option><option value="operators">仅运营人员</option></select></label><label className="field-label">慢速模式（秒）<input type="number" min="0" max="86400" value={draft.slowModeSeconds} onChange={(event) => setDraft((value) => ({ ...value, slowModeSeconds: Number(event.target.value) }))} /></label></div>
      <label className="field-label">描述<textarea value={draft.description} onChange={(event) => setDraft((value) => ({ ...value, description: event.target.value }))} /></label><label className="field-label">创建原因<textarea value={createReason} maxLength={1000} onChange={(event) => setCreateReason(event.target.value)} required /></label>
    </ConfirmDialog>
  </>;
}

type SupportTab = 'sessions' | 'skills' | 'agents';
const defaultSupportSkillDraft = (): Partial<SupportSkillRecord> => ({ name: '', description: '', routingStrategy: 'least_active', maxConcurrentPerAgent: 5, enabled: true });
type PendingSupportTransition = { kind: 'tab'; tab: SupportTab };
function SupportWorkbenchPage() {
  const { api, mode, can, notify } = useApi();
  const [tab, setTab] = useState<SupportTab>('sessions'), [query, setQuery] = useState(''), deferred = useDebouncedValue(query), [status, setStatus] = useState('');
  const skills = useResource(() => api.getSupportSkills(), [api, mode]);
  const agents = useResource(() => api.getSupportAgents(), [api, mode]);
  const sessions = useResource(() => api.getSupportSessions(deferred, status, '', 1, 100), [api, mode, deferred, status]);
  const [action, setAction] = useState<ConfirmableAdminAction>(), [reason, setReason] = useState(''), [targetAgentId, setTargetAgentId] = useState('');
  const [skillDraft, setSkillDraft] = useState<Partial<SupportSkillRecord>>(() => defaultSupportSkillDraft());
  const [agentUserId, setAgentUserId] = useState(''), [agentStatus, setAgentStatus] = useState<SupportAgentRecord['status']>('offline'), [agentCapacity, setAgentCapacity] = useState(5), [agentSkills, setAgentSkills] = useState('');
  const [pendingTransition, setPendingTransition] = useState<PendingSupportTransition>();
  const skillMatchesStored = Boolean(skills.data?.some((item) => JSON.stringify(item) === JSON.stringify(skillDraft)));
  const skillDirty = JSON.stringify(skillDraft) !== JSON.stringify(defaultSupportSkillDraft()) && !skillMatchesStored;
  const agentSnapshot = { userId: agentUserId, status: agentStatus, capacity: agentCapacity, skills: agentSkills };
  const agentMatchesStored = Boolean(agents.data?.some((item) => item.userId === agentUserId && item.status === agentStatus && item.maxConcurrent === agentCapacity && item.skillGroupIds.join(',') === agentSkills));
  const agentDirty = JSON.stringify(agentSnapshot) !== JSON.stringify({ userId: '', status: 'offline', capacity: 5, skills: '' }) && !agentMatchesStored;
  const supportDirty = tab === 'skills' ? skillDirty : tab === 'agents' ? agentDirty : false;
  useUnsavedChanges(supportDirty, `${tab === 'skills' ? '客服技能组' : '客服坐席'}有未保存的修改`);
  const resetSkillDraft = () => { setSkillDraft(defaultSupportSkillDraft()); };
  const resetAgentDraft = () => { setAgentUserId(''); setAgentStatus('offline'); setAgentCapacity(5); setAgentSkills(''); };
  const discardCurrentDraft = () => { if (tab === 'skills') resetSkillDraft(); if (tab === 'agents') resetAgentDraft(); };
  const requestTab = (next: SupportTab) => { if (next === tab) return; if (supportDirty) { setPendingTransition({ kind: 'tab', tab: next }); return; } discardCurrentDraft(); setTab(next); };
  const confirmTransition = () => {
    const next = pendingTransition;
    if (!next) return;
    discardCurrentDraft();
    setPendingTransition(undefined);
    setTab(next.tab);
  };
  const execute = async () => { if (!action) return; await action.run(reason.trim()); await Promise.all([skills.reload(), agents.reload(), sessions.reload()]); notify('客服工作台已更新'); };
  const openAction = (next: ConfirmableAdminAction) => { setReason(''); setAction(next); };
  const saveSkill = () => { if (!skillDraft.name?.trim()) return; const capacity = skillDraft.maxConcurrentPerAgent ?? 5; if (!Number.isInteger(capacity) || capacity < 1 || capacity > 100) { notify('每坐席并发必须是 1 到 100 之间的整数', 'danger'); return; } const input = { ...skillDraft, name: skillDraft.name.trim(), routingStrategy: skillDraft.routingStrategy ?? 'least_active', maxConcurrentPerAgent: capacity, enabled: skillDraft.enabled ?? true }; openAction({ title: input.id ? '更新客服技能组' : '创建客服技能组', detail: `${input.name} · ${input.routingStrategy === 'round_robin' ? '轮询分配' : '最少会话优先'}`, run: async (why) => { await api.saveSupportSkill(input, why); resetSkillDraft(); } }); };
  const saveAgent = () => { if (!agentUserId.trim()) return; if (!Number.isInteger(agentCapacity) || agentCapacity < 1 || agentCapacity > 100) { notify('总并发上限必须是 1 到 100 之间的整数', 'danger'); return; } const userId = agentUserId.trim(), skillGroupIds = [...new Set(agentSkills.split(',').map((item) => item.trim()).filter(Boolean))]; if (!skillGroupIds.length) { notify('请至少选择一个技能组', 'danger'); return; } const skillNames = skills.data?.filter((item) => skillGroupIds.includes(item.id)).map((item) => item.name).join('、') || `${skillGroupIds.length} 个技能组`; openAction({ title: '保存客服坐席', detail: `所选账号将绑定到${skillNames}。`, run: async (why) => { await api.saveSupportAgent(userId, { status: agentStatus, maxConcurrent: agentCapacity, skillGroupIds }, why); resetAgentDraft(); } }); };
  const sessionAction = (item: SupportSessionRecord, kind: 'claim' | 'transfer' | 'end') => {
    const target = targetAgentId.trim();
    if (kind !== 'end' && !target) return;
    openAction({ title: kind === 'claim' ? '认领客服会话' : kind === 'transfer' ? '转接客服会话' : '结束客服会话', detail: kind === 'end' ? `${item.id} 将结束，访客随后可以评价。` : `${item.id} 将分配给 ${target}。`, danger: kind === 'end', run: async (why) => { if (kind === 'claim') await api.claimSupportSession(item.id, target, why); else if (kind === 'transfer') await api.transferSupportSession(item.id, target, why); else await api.endSupportSession(item.id, why); } });
  };
  return <><PageHeader title="客服工作台" description="管理技能组、坐席容量、排队会话、自动分配、认领、转接、结束和评价结果。" />
    <div className="tabs" role="tablist" aria-label="客服模块" onKeyDown={tabListKeyDown}><button type="button" role="tab" tabIndex={tab === 'sessions' ? 0 : -1} aria-selected={tab === 'sessions'} className={tab === 'sessions' ? 'active' : ''} onClick={() => requestTab('sessions')}>会话队列</button><button type="button" role="tab" tabIndex={tab === 'skills' ? 0 : -1} aria-selected={tab === 'skills'} className={tab === 'skills' ? 'active' : ''} onClick={() => requestTab('skills')}>技能组</button><button type="button" role="tab" tabIndex={tab === 'agents' ? 0 : -1} aria-selected={tab === 'agents'} className={tab === 'agents' ? 'active' : ''} onClick={() => requestTab('agents')}>客服坐席</button></div>
    {tab === 'sessions' ? <><Toolbar query={query} setQuery={setQuery} placeholder="搜索会话、访客或主题"><select className="select-control" aria-label="客服会话状态" value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="queued">排队中</option><option value="active">处理中</option><option value="ended">已结束</option></select><select className="select-control" aria-label="目标客服" value={targetAgentId} onChange={(event) => setTargetAgentId(event.target.value)}><option value="">选择认领或转接坐席</option>{agents.data?.map((item) => <option key={item.userId} value={item.userId}>{item.name} · {item.handle || item.userId}（{statusMap[item.status]?.label ?? '状态未知'}）</option>)}</select></Toolbar><DataPanel loading={sessions.loading} error={sessions.error} retry={sessions.reload} empty={!sessions.data?.items.length} emptyTitle="没有客服会话" emptyDetail="新访客进入队列后会显示在这里。"><div className="table-wrap"><table><thead><tr><th>会话</th><th>访客</th><th>技能组</th><th>主题</th><th>队列</th><th>坐席</th><th>状态</th><th>评价</th><th>操作</th></tr></thead><tbody>{sessions.data?.items.map((item) => <tr key={item.id}><td className="mono">{item.id}</td><td><strong>{item.visitorName}</strong><small className="mono">{item.visitorId}</small></td><td>{item.skillGroupName}</td><td>{item.subject || '—'}</td><td>{item.queuePosition || '—'}</td><td>{item.agentName || item.assignedAgentId || '未分配'}</td><td><Badge value={item.status} /></td><td>{item.rating ? `${item.rating}/5` : '—'}</td><td><div className="row-actions">{item.status === 'queued' && <button className="button secondary compact" disabled={!can('support.write') || !targetAgentId.trim()} onClick={() => sessionAction(item, 'claim')}>认领</button>}{item.status === 'active' && <><button className="button secondary compact" disabled={!can('support.write') || !targetAgentId.trim()} onClick={() => sessionAction(item, 'transfer')}>转接</button><button className="button secondary compact danger-text" disabled={!can('support.write')} onClick={() => sessionAction(item, 'end')}>结束</button></>}</div></td></tr>)}</tbody></table></div></DataPanel></> : tab === 'skills' ? <div className="business-grid"><section className="panel"><div className="panel-heading"><div><h2>{skillDraft.id ? '编辑技能组' : '新建技能组'}</h2><p>每个技能组独立控制路由策略和坐席并发上限。</p></div></div><label className="field-label">名称<input value={skillDraft.name ?? ''} onChange={(event) => setSkillDraft((value) => ({ ...value, name: event.target.value }))} /></label><label className="field-label">描述<textarea value={skillDraft.description ?? ''} onChange={(event) => setSkillDraft((value) => ({ ...value, description: event.target.value }))} /></label><div className="form-grid"><label className="field-label">路由策略<select value={skillDraft.routingStrategy} onChange={(event) => setSkillDraft((value) => ({ ...value, routingStrategy: event.target.value as SupportSkillRecord['routingStrategy'] }))}><option value="least_active">最少会话优先</option><option value="round_robin">轮询分配</option></select></label><label className="field-label">每坐席并发<input type="number" min="1" max="100" value={skillDraft.maxConcurrentPerAgent} onChange={(event) => setSkillDraft((value) => ({ ...value, maxConcurrentPerAgent: Number(event.target.value) }))} /></label></div><Toggle label="启用技能组" description="停用后不再接收新访客。" checked={skillDraft.enabled ?? true} onChange={(enabled) => setSkillDraft((value) => ({ ...value, enabled }))} /><button className="button primary" disabled={!can('support.write') || !skillDraft.name?.trim()} onClick={saveSkill}><Save size={15} />保存技能组</button></section><DataPanel loading={skills.loading} error={skills.error} retry={skills.reload} empty={!skills.data?.length} emptyTitle="没有技能组" emptyDetail="创建技能组后才能配置坐席。"><div className="table-wrap"><table><thead><tr><th>技能组</th><th>路由</th><th>并发</th><th>排队</th><th>可用坐席</th><th>状态</th><th>操作</th></tr></thead><tbody>{skills.data?.map((item) => <tr key={item.id}><td><strong>{item.name}</strong><small className="mono">{item.id}</small></td><td>{item.routingStrategy === 'round_robin' ? '轮询' : '最少会话'}</td><td>{item.maxConcurrentPerAgent}</td><td>{item.queueCount}</td><td>{item.availableAgents}</td><td><Badge value={item.enabled ? 'active' : 'disabled'} /></td><td><button className="button secondary compact" onClick={() => setSkillDraft(item)}>编辑</button></td></tr>)}</tbody></table></div></DataPanel></div> : <div className="business-grid"><section className="panel"><div className="panel-heading"><div><h2>配置客服坐席</h2><p>从真实正常账号中选择坐席，并勾选该坐席可以处理的技能组。</p></div></div><AccountPicker value={agentUserId} onChange={setAgentUserId} searchLabel="查找客服坐席账号" selectLabel="客服坐席账号" /><fieldset className="selection-field"><legend>可处理技能组</legend><div className="selection-list">{skills.data?.map((item) => { const selected = agentSkills.split(',').includes(item.id); return <label key={item.id}><input type="checkbox" checked={selected} onChange={(event) => { const current = agentSkills.split(',').filter(Boolean); setAgentSkills((event.target.checked ? [...new Set([...current, item.id])] : current.filter((id) => id !== item.id)).join(',')); }} /><span><strong>{item.name}</strong><small>{item.description || '暂无说明'}</small></span></label>; })}</div>{skills.loading && <small>正在加载技能组…</small>}{!skills.loading && !skills.data?.length && <small className="danger-text">请先创建技能组</small>}</fieldset><div className="form-grid"><label className="field-label">状态<select value={agentStatus} onChange={(event) => setAgentStatus(event.target.value as SupportAgentRecord['status'])}><option value="offline">离线</option><option value="available">可接待</option><option value="busy">忙碌</option><option value="away">暂离</option></select></label><label className="field-label">总并发上限<input type="number" min="1" max="100" value={agentCapacity} onChange={(event) => setAgentCapacity(Number(event.target.value))} /></label></div><button className="button primary" disabled={!can('support.write') || !agentUserId.trim() || !agentSkills.trim()} onClick={saveAgent}><Save size={15} />保存坐席</button></section><DataPanel loading={agents.loading} error={agents.error} retry={agents.reload} empty={!agents.data?.length} emptyTitle="没有客服坐席" emptyDetail="绑定一个用户到技能组。"><div className="table-wrap"><table><thead><tr><th>坐席</th><th>状态</th><th>技能组</th><th>活跃会话</th><th>并发上限</th><th>操作</th></tr></thead><tbody>{agents.data?.map((item) => <tr key={item.userId}><td><strong>{item.name}</strong><small className="mono">{item.userId}</small></td><td><Badge value={item.status} /></td><td>{item.skillGroupIds.map((id) => skills.data?.find((skill) => skill.id === id)?.name ?? '未知技能组').join('、')}</td><td>{item.activeSessions}</td><td>{item.maxConcurrent}</td><td><button className="button secondary compact" onClick={() => { setAgentUserId(item.userId); setAgentStatus(item.status); setAgentCapacity(item.maxConcurrent); setAgentSkills(item.skillGroupIds.join(',')); }}>编辑</button></td></tr>)}</tbody></table></div></DataPanel></div>}
    <ConfirmDialog open={Boolean(action)} title={action?.title ?? ''} detail={action?.detail ?? ''} confirmLabel="确认并记录" danger={action?.danger} confirmDisabled={!reason.trim()} onClose={() => { setAction(undefined); setReason(''); }} onConfirm={execute}><label className="field-label">操作原因<textarea value={reason} maxLength={1000} onChange={(event) => setReason(event.target.value)} required /></label></ConfirmDialog>
    <ConfirmDialog open={Boolean(pendingTransition)} title="放弃未保存的客服配置？" detail={`${tab === 'skills' ? '技能组名称、路由策略和并发上限' : '坐席账号、技能组和接待状态'}将恢复到上次保存状态。`} confirmLabel="放弃修改并继续" danger onClose={() => setPendingTransition(undefined)} onConfirm={confirmTransition} />
  </>;
}

const clientPlatforms: ClientPlatform[] = ['android', 'ios', 'web', 'macos'];
const clientPlatformLabels: Record<ClientPlatform, string> = { android: 'Android', ios: 'iOS', web: 'Web', macos: 'macOS' };
function emptyClientVersion(platform: ClientPlatform): ClientVersionPolicy {
  return { platform, minimumVersion: '1.0.0', latestVersion: '1.0.0', forceUpdate: false, rolloutPercentage: 100, releaseNotes: '', downloadUrl: '', updatedBy: '', updatedAt: '' };
}

type RobotDraft = Pick<WukongRobotProfile, 'userId' | 'name' | 'enabled' | 'username' | 'placeholder' | 'inlineOn' | 'menus'>;

function WukongRobotsPanel() {
  const { api, mode, notify, can } = useApi();
  const robots = useResource(() => api.getWukongRobots(), [api, mode]);
  const systemUsers = useResource(() => api.getWukongSystemUsers(), [api, mode]);
  const [selectedUserId, setSelectedUserId] = useState('');
  const [draft, setDraft] = useState<RobotDraft>();
  const [reason, setReason] = useState('');
  const [draftBaseline, setDraftBaseline] = useState('');
  const availableUsers = systemUsers.data?.filter((item) => item.enabled && !robots.data?.some((robot) => robot.userId === item.userId)) ?? [];
  const openNew = () => {
    const account = systemUsers.data?.find((item) => item.userId === selectedUserId && item.enabled);
    if (!account) return;
    const next: RobotDraft = { userId: account.userId, name: account.name, enabled: true, username: '', placeholder: '请选择服务', inlineOn: false, menus: [{ cmd: '帮助', remark: '使用帮助', type: 'command' }] };
    setReason('');
    setDraftBaseline(JSON.stringify(next));
    setDraft(next);
  };
  const openEdit = (item: WukongRobotProfile) => {
    const next: RobotDraft = { userId: item.userId, name: item.name, enabled: item.enabled, username: item.username, placeholder: item.placeholder, inlineOn: item.inlineOn, menus: item.menus.map((menu) => ({ ...menu })) };
    setReason('');
    setDraftBaseline(JSON.stringify(next));
    setDraft(next);
  };
  const updateMenu = (index: number, update: Partial<WukongRobotMenu>) => setDraft((current) => current ? ({ ...current, menus: current.menus.map((menu, menuIndex) => menuIndex === index ? { ...menu, ...update } : menu) }) : current);
  const removeMenu = (index: number) => setDraft((current) => current ? ({ ...current, menus: current.menus.filter((_, menuIndex) => menuIndex !== index) }) : current);
  const addMenu = () => setDraft((current) => current && current.menus.length < 12 ? ({ ...current, menus: [...current.menus, { cmd: '', remark: '', type: 'command' }] }) : current);
  const menuCommands = draft?.menus.map((menu) => menu.cmd.trim()).filter(Boolean) ?? [];
  const validDraft = Boolean(draft && /^[a-z0-9_]{2,32}$/.test(draft.username.trim()) && draft.placeholder.trim().length <= 80 && (!draft.enabled || draft.menus.length > 0) && draft.menus.every((menu) => menu.cmd.trim() && menu.cmd.trim().length <= 40 && menu.remark.trim() && menu.remark.trim().length <= 30) && new Set(menuCommands).size === menuCommands.length && reason.trim());
  const draftDirty = Boolean(draft && (JSON.stringify(draft) !== draftBaseline || reason.trim()));
  const closeDraft = () => { setDraft(undefined); setReason(''); setDraftBaseline(''); };
  const save = async () => {
    if (!draft || !validDraft) return;
    await api.setWukongRobot(draft.userId, { enabled: draft.enabled, username: draft.username.trim(), placeholder: draft.placeholder.trim(), inlineOn: draft.inlineOn, menus: draft.menus.map((menu) => ({ cmd: menu.cmd.trim(), remark: menu.remark.trim(), type: 'command' })) }, reason.trim());
    setDraft(undefined); setReason(''); setSelectedUserId(''); await robots.reload();
    notify('机器人配置已保存');
  };
  return <>
    <section className="operations-block"><div className="panel-heading"><div><h2>机器人</h2><p>机器人必须先设为系统账号。命令菜单只对机器人所在的真实单聊或群聊成员显示，点击后以普通消息和机器人命令元数据发送。</p></div></div><div className="inline-form account-operation"><label className="field-label robot-account-select">机器人系统账号<select aria-label="机器人系统账号" value={selectedUserId} onChange={(event) => setSelectedUserId(event.target.value)}><option value="">选择尚未配置的系统账号</option>{availableUsers.map((item) => <option key={item.userId} value={item.userId}>{item.name || item.userId} · {item.userId}</option>)}</select></label><button type="button" className="button primary" disabled={!can('operations.write') || !selectedUserId} onClick={openNew}><Plus size={15} />配置机器人</button></div></section><div style={{ height: 14 }} />
    <DataPanel loading={robots.loading || systemUsers.loading} error={robots.error || systemUsers.error} retry={async () => { await Promise.all([robots.reload(), systemUsers.reload()]); }} empty={!robots.data?.length} emptyTitle="尚未配置机器人" emptyDetail="先在“系统账号”中启用一个真实账号，再在这里配置命令菜单。"><div className="table-wrap"><table><thead><tr><th>机器人</th><th>菜单</th><th>状态</th><th>版本</th><th>更新记录</th><th>操作</th></tr></thead><tbody>{robots.data?.map((item) => <tr key={item.userId}><td><strong>{item.name || item.userId}</strong><small>@{item.username || '未设置'} · {item.userId}</small></td><td><strong>{item.menus.length} 项</strong><small>{item.menus.slice(0, 3).map((menu) => menu.remark).join('、') || '未配置命令'}</small></td><td><Badge value={item.enabled ? 'active' : 'disabled'} label={item.enabled ? '已启用' : '已停用'} /></td><td className="mono">v{item.version}</td><td><strong>{item.updatedBy || '—'}</strong><small>{item.reason || '暂无说明'} · {item.updatedAt || '—'}</small></td><td><button type="button" className="button secondary compact" disabled={!can('operations.write')} onClick={() => openEdit(item)}>编辑</button></td></tr>)}</tbody></table></div></DataPanel>
    <ConfirmDialog open={Boolean(draft)} title={draft ? `配置机器人 · ${draft.name || draft.userId}` : '配置机器人'} detail="菜单名称面向用户显示，命令内容会原样发送给机器人账号。保存后仅在该机器人已经加入的会话中生效。" confirmLabel="保存配置" confirmDisabled={!validDraft} discardConfirmation={draftDirty ? { title: '放弃未保存的机器人配置？', detail: '机器人标识、输入提示、命令菜单和操作理由将全部丢失。' } : undefined} onClose={closeDraft} onConfirm={save}>{draft && <><div className="form-grid robot-identity-fields"><label className="field-label">机器人标识<input aria-label="机器人标识" value={draft.username} maxLength={32} onChange={(event) => setDraft((current) => current ? ({ ...current, username: event.target.value.toLowerCase().replace(/[^a-z0-9_]/g, '') }) : current)} placeholder="service_helper" /></label><label className="field-label">输入框提示<input aria-label="输入框提示" value={draft.placeholder} maxLength={80} onChange={(event) => setDraft((current) => current ? ({ ...current, placeholder: event.target.value }) : current)} placeholder="请选择服务" /></label></div><Toggle label="启用机器人菜单" description="停用后客户端不再展示命令入口，历史消息不受影响。" checked={draft.enabled} onChange={(enabled) => setDraft((current) => current ? ({ ...current, enabled }) : current)} /><fieldset className="robot-menu-field"><legend>命令菜单 <span>{draft.menus.length}/12</span></legend><div className="robot-menu-list">{draft.menus.map((menu, index) => <div className="robot-menu-row" key={index}><label className="field-label">菜单名称<input aria-label={`菜单名称 ${index + 1}`} value={menu.remark} maxLength={30} onChange={(event) => updateMenu(index, { remark: event.target.value })} placeholder="例如：人工客服" /></label><label className="field-label">发送命令<input aria-label={`发送命令 ${index + 1}`} value={menu.cmd} maxLength={40} onChange={(event) => updateMenu(index, { cmd: event.target.value })} placeholder="例如：转人工" /></label><button type="button" className="icon-button robot-menu-remove" aria-label={`删除菜单 ${index + 1}`} onClick={() => removeMenu(index)}><Trash2 size={15} /></button></div>)}</div><button type="button" className="button secondary compact robot-menu-add" disabled={draft.menus.length >= 12} onClick={addMenu}><Plus size={14} />添加菜单</button></fieldset><label className="field-label robot-reason">操作理由<textarea aria-label="机器人配置理由" value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} placeholder="填写业务用途、审批单或本次变更原因" required /></label></>}</ConfirmDialog>
  </>;
}

const clientVersionPattern = /^[0-9]+(?:\.[0-9]+){0,3}$/;
function parseClientVersion(value: string) {
  const normalized = value.trim().toLowerCase().replace(/^v/, '');
  if (!clientVersionPattern.test(normalized)) return undefined;
  const parts = normalized.split('.').map(Number);
  if (parts.some((part) => !Number.isSafeInteger(part) || part < 0 || part > 2_147_483_647)) return undefined;
  return [...parts, ...Array(4 - parts.length).fill(0)] as number[];
}

function clientVersionPolicyError(policy: ClientVersionPolicy) {
  const minimum = parseClientVersion(policy.minimumVersion);
  const latest = parseClientVersion(policy.latestVersion);
  if (!minimum || !latest) return '版本号需使用 1、1.2、1.2.3 或 1.2.3.4 格式';
  for (let index = 0; index < 4; index += 1) {
    if (minimum[index] > latest[index]) return '最新发布版本不能低于最低支持版本';
    if (minimum[index] < latest[index]) break;
  }
  if (!Number.isInteger(policy.rolloutPercentage) || policy.rolloutPercentage < 0 || policy.rolloutPercentage > 100) return '灰度比例必须是 0 到 100 的整数';
  const downloadUrl = policy.downloadUrl.trim();
  if (downloadUrl) {
    try {
      const parsed = new URL(downloadUrl);
      if (parsed.protocol !== 'https:' || !parsed.hostname || parsed.username || parsed.password || parsed.hash) throw new Error('invalid');
    } catch {
      return '下载地址必须是不含账号信息或锚点的 HTTPS 地址';
    }
  }
  return '';
}

function ClientVersionsPage() {
  const { api, mode, notify, can } = useApi();
  const state = useResource(() => api.getClientVersions(), [api, mode]);
  const [selected, setSelected] = useState<ClientPlatform>('android');
  const [draft, setDraft] = useState<ClientVersionPolicy>(() => emptyClientVersion('android'));
  const [baseline, setBaseline] = useState<ClientVersionPolicy>(() => emptyClientVersion('android'));
  const [draftTouched, setDraftTouched] = useState(false);
  const [pendingPlatform, setPendingPlatform] = useState<ClientPlatform>();
  const [confirming, setConfirming] = useState(false);
  const [reason, setReason] = useState('');
  const [historyOpen, setHistoryOpen] = useState(false);
  const [historyPage, setHistoryPage] = useState(1);
  const [historyCursors, setHistoryCursors] = useState<Record<number, string>>({ 1: '' });
  const [validationError, setValidationError] = useState('');
  const history = useResource(
    () => historyOpen
      ? api.getClientVersionHistory(selected, historyPage, 10, historyCursors[historyPage] ?? '')
      : Promise.resolve({ items: [], page: 1, pageSize: 10, total: 0, hasNext: false } satisfies PageResult<ClientVersionReleaseRecord>),
    [api, mode, historyOpen, selected, historyPage, historyCursors],
  );
  const configuredPolicy = state.data?.find((item) => item.platform === selected);
  const hasServerPolicy = Boolean(configuredPolicy);
  useEffect(() => {
    const next = configuredPolicy ?? emptyClientVersion(selected);
    setDraft(next);
    setBaseline(next);
    setDraftTouched(false);
  }, [configuredPolicy, selected]);
  useEffect(() => { setHistoryPage(1); setHistoryCursors({ 1: '' }); }, [selected]);
  const dirty = draftTouched && JSON.stringify(draft) !== JSON.stringify(baseline);
  useUnsavedChanges(dirty, `${clientPlatformLabels[selected]}版本策略有未发布的修改`);
  const selectPlatform = (platform: ClientPlatform) => { if (platform === selected) return; if (dirty) { setPendingPlatform(platform); return; } setSelected(platform); };
  const change = <K extends keyof ClientVersionPolicy>(key: K, value: ClientVersionPolicy[K]) => { setValidationError(''); setDraftTouched(true); setDraft((current) => ({ ...current, [key]: value })); };
  const paginateHistory = (nextPage: number) => { if (nextPage > historyPage && history.data?.nextCursor) setHistoryCursors((current) => ({ ...current, [nextPage]: history.data?.nextCursor ?? '' })); setHistoryPage(nextPage); };
  const openConfirmation = () => {
    const issue = clientVersionPolicyError(draft);
    if (issue) { setValidationError(issue); return; }
    setValidationError(''); setReason(''); setConfirming(true);
  };
  const save = async () => {
    const issue = clientVersionPolicyError(draft);
    if (issue) throw new Error(issue);
    const updated = await api.updateClientVersion(draft, reason.trim());
    setDraft(updated); setBaseline(updated); setDraftTouched(false); setReason(''); await state.reload(); if (historyOpen) await history.reload(); notify(`${clientPlatformLabels[selected]} 版本策略已发布`);
  };
  return <><PageHeader title="客户端版本" description="分别控制四端最低版本、可选或强制更新、稳定灰度比例及下载入口。最低版本限制始终覆盖灰度。" actions={<><button className={`button secondary ${historyOpen ? 'active' : ''}`} aria-expanded={historyOpen} onClick={() => setHistoryOpen((value) => !value)}><FileClock size={15} />发布历史</button><button className="button secondary" onClick={() => { void state.reload(); if (historyOpen) void history.reload(); }}><RefreshCcw size={15} />刷新策略</button></>} />
    {state.loading ? <Skeleton rows={6} /> : state.error ? <ErrorState message={state.error} retry={state.reload} /> : <>
      <div className="tabs" role="tablist" aria-label="客户端平台" onKeyDown={tabListKeyDown}>{clientPlatforms.map((platform) => <button type="button" role="tab" tabIndex={selected === platform ? 0 : -1} aria-selected={selected === platform} className={selected === platform ? 'active' : ''} key={platform} onClick={() => selectPlatform(platform)}>{clientPlatformLabels[platform]}</button>)}</div>
      {historyOpen && <section className="version-history" aria-label={`${clientPlatformLabels[selected]} 发布历史`}>
        <div className="version-history-title"><div><FileClock size={20} /><div><h2>{clientPlatformLabels[selected]} 发布历史</h2><p>按发布时间倒序展示，历史记录不会被后续策略覆盖。</p></div></div><button className="icon-button" aria-label="关闭发布历史" onClick={() => setHistoryOpen(false)}><X size={18} /></button></div>
        {history.loading ? <Skeleton rows={3} /> : history.error ? <ErrorState message={history.error} retry={history.reload} /> : !history.data?.items.length ? <EmptyState title="还没有发布记录" detail={`首次发布 ${clientPlatformLabels[selected]} 版本策略后，记录会显示在这里。`} /> : <>
          <div className="version-history-list">{history.data.items.map((item) => <article className="version-history-item" key={item.id}>
            <div className="version-history-head"><div><strong>v{item.latestVersion}</strong><span>{dateTimeLabel(item.updatedAt)} · {item.updatedBy || '系统'}</span></div><span className={`release-kind ${item.forceUpdate ? 'force' : ''}`}>{item.forceUpdate ? '强制更新' : '可选更新'}</span></div>
            <dl className="version-history-meta"><div><dt>最低支持</dt><dd>v{item.minimumVersion}</dd></div><div><dt>灰度比例</dt><dd>{item.rolloutPercentage}%</dd></div><div><dt>下载地址</dt><dd>{item.downloadUrl ? <a href={item.downloadUrl} target="_blank" rel="noreferrer">打开链接</a> : '未配置'}</dd></div></dl>
            <div className="version-history-copy"><span>发布原因</span><p>{item.reason || '早期记录未保存发布原因'}</p></div>
            <div className="version-history-copy"><span>更新说明</span><p>{item.releaseNotes || '早期记录未保存更新说明'}</p></div>
          </article>)}</div>
          <Pagination data={history.data} onPage={paginateHistory} />
        </>}
      </section>}
      <div className="settings-layout"><div className="settings-main"><section className="settings-section">
        <div className="settings-title"><RefreshCcw size={20} /><div><h2>{clientPlatformLabels[selected]} 发布策略</h2><p>版本号使用 1、1.2、1.2.3 或 1.2.3.4 格式；下载地址必须是 HTTPS。</p></div></div>
        {!hasServerPolicy && <div className="inline-notice warning" role="status"><AlertTriangle size={15} />服务端尚未配置 {clientPlatformLabels[selected]} 版本策略；以下内容仅是本地新建草稿，不是线上数据。</div>}
        <div className="form-grid"><label className="field-label">最低支持版本<input value={draft.minimumVersion} pattern="[0-9]+(\.[0-9]+){0,3}" onChange={(event) => change('minimumVersion', event.target.value)} required /></label><label className="field-label">最新发布版本<input value={draft.latestVersion} pattern="[0-9]+(\.[0-9]+){0,3}" onChange={(event) => change('latestVersion', event.target.value)} required /></label><label className="field-label">灰度比例（%）<input type="number" min="0" max="100" value={draft.rolloutPercentage} onChange={(event) => change('rolloutPercentage', Number(event.target.value))} required /></label></div>
        <Toggle label="将本次更新设为强制更新" description="已进入灰度且低于最新版本的客户端不能跳过；低于最低版本的客户端始终强制更新。" checked={draft.forceUpdate} onChange={(value) => change('forceUpdate', value)} />
        <label className="field-label">更新说明<textarea value={draft.releaseNotes} maxLength={4000} onChange={(event) => change('releaseNotes', event.target.value)} placeholder="主要变化、修复内容和用户注意事项" /></label>
        <label className="field-label">下载地址<input type="url" value={draft.downloadUrl} onChange={(event) => change('downloadUrl', event.target.value)} placeholder="https://downloads.example.com/app" /></label>
      </section></div><aside className="settings-save"><h2>发布确认</h2><p>灰度分组按本机安装标识稳定计算，同一设备不会在启动间随机跳组。</p><div className="save-check"><CheckCircle2 size={17} /><span>策略写入与操作理由会同时进入审计日志</span></div><p className="permission-note" role="status">{dirty ? '有未发布的版本策略更改' : hasServerPolicy ? '当前策略与服务端一致' : '尚未创建线上版本策略'}</p>{validationError && <div className="inline-notice danger" role="alert"><AlertTriangle size={15} />{validationError}</div>}<button className="button primary full" type="button" disabled={!dirty || !can('versions.write')} onClick={openConfirmation}><Save size={16} />{hasServerPolicy ? '保存并发布' : '创建并发布'}</button>{draft.updatedAt && <p className="permission-note">上次更新：{dateTimeLabel(draft.updatedAt)} · {draft.updatedBy || '系统'}</p>}{!can('versions.write') && <p className="permission-note">当前角色没有版本发布权限。</p>}</aside></div>
    </>}
    <ConfirmDialog open={confirming} title={`${hasServerPolicy ? '发布' : '创建并发布'} ${clientPlatformLabels[selected]} 版本策略`} detail={`最低版本 ${draft.minimumVersion}，最新版本 ${draft.latestVersion}，灰度 ${draft.rolloutPercentage}%${draft.forceUpdate ? '，本次为强制更新' : ''}。`} confirmLabel="确认发布" danger={draft.forceUpdate} confirmDisabled={!reason.trim()} onClose={() => { setConfirming(false); setReason(''); }} onConfirm={save}><label className="field-label">发布原因<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} placeholder="填写发布批次、变更单或紧急修复原因" required /></label></ConfirmDialog>
    <ConfirmDialog open={Boolean(pendingPlatform)} title="放弃未发布的版本策略？" detail={`切换到${pendingPlatform ? clientPlatformLabels[pendingPlatform] : '其他平台'}后，当前平台尚未发布的修改将丢失。`} confirmLabel="放弃更改并切换" danger onClose={() => setPendingPlatform(undefined)} onConfirm={() => { const next = pendingPlatform; setPendingPlatform(undefined); if (next) setSelected(next); }} />
  </>;
}

function HealthPage() {
  const { api, mode } = useApi(); const state = useResource(() => api.getHealth(), [api, mode]); const healthyCount = state.data?.filter((service) => service.status === 'healthy').length ?? 0;
  return <><PageHeader title="系统健康" description="查看服务端当前可提供的健康信息。" actions={<button className="button secondary" onClick={() => void state.reload()}><RefreshCcw size={15} />重新检测</button>} />
    {state.loading ? <Skeleton rows={6} /> : state.error || !state.data ? <ErrorState message={state.error} retry={state.reload} /> : <><div className={`health-summary ${healthyCount === state.data.length ? 'healthy' : 'attention'}`}><div className="health-orb"><HeartPulse size={24} /></div><div><strong>{healthyCount === state.data.length ? '当前检测项目正常' : `${state.data.length - healthyCount} 个检测项目需要关注`}</strong><p>状态来自实时健康接口，不代替外部监控和告警。</p></div><span>{healthyCount}/{state.data.length} 正常</span></div><div className="service-grid">{state.data.map((service) => <ServiceCard key={service.name} service={service} />)}</div></>}
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

type AdministratorEditor = { mode: 'create' | 'edit'; account?: AdministratorRecord; email: string; displayName: string; roleId: string; password: string; reason: string };
type AdministratorStatusAction = { account: AdministratorRecord; status: 'active' | 'disabled'; reason: string };
type AdministratorPasswordReset = { account: AdministratorRecord; password: string; confirmation: string; reason: string };
type AdministratorRoleEditor = { role?: AdministratorRoleRecord; name: string; description: string; permissions: string[]; reason: string };

function AdministratorsPage() {
  const { api, mode, notify, session } = useApi();
  const [tab, setTab] = useState<'accounts' | 'roles'>('accounts');
  const [query, setQuery] = useState('');
  const [status, setStatus] = useState('');
  const deferredQuery = useDebouncedValue(query);
  const accounts = useResource(() => api.getAdministrators(deferredQuery, status, 1, 100), [api, mode, deferredQuery, status]);
  const roles = useResource(() => api.getAdministratorRoles(), [api, mode]);
  const [editor, setEditor] = useState<AdministratorEditor>();
  const [statusAction, setStatusAction] = useState<AdministratorStatusAction>();
  const [passwordReset, setPasswordReset] = useState<AdministratorPasswordReset>();
  const [roleEditor, setRoleEditor] = useState<AdministratorRoleEditor>();
  const [roleDelete, setRoleDelete] = useState<{ role: AdministratorRoleRecord; reason: string }>();
  if (session.roleId !== 'platform_admin') return <><PageHeader title="管理员与角色" description="仅平台管理员可以访问管理员账号和角色配置。" /><EmptyState title="当前账号无权访问" detail="请联系平台管理员调整账号角色。" icon={<ShieldAlert size={24} />} /></>;
  const reload = async () => { await Promise.all([accounts.reload(), roles.reload()]); };
  const openCreate = () => setEditor({ mode: 'create', email: '', displayName: '', roleId: roles.data?.find((role) => role.id === 'support')?.id ?? roles.data?.[0]?.id ?? '', password: '', reason: '' });
  const openEdit = (account: AdministratorRecord) => setEditor({ mode: 'edit', account, email: account.email, displayName: account.displayName, roleId: account.roleId, password: '', reason: '' });
  const saveAccount = async () => {
    if (!editor) return;
    if (editor.mode === 'create') await api.createAdministrator({ email: editor.email.trim(), displayName: editor.displayName.trim(), roleId: editor.roleId, password: editor.password }, editor.reason.trim());
    else if (editor.account) await api.updateAdministrator(editor.account.id, { email: editor.email.trim(), displayName: editor.displayName.trim(), roleId: editor.roleId }, editor.reason.trim());
    notify(editor.mode === 'create' ? '管理员已创建' : '管理员资料已更新');
    await reload();
  };
  const changeStatus = async () => {
    if (!statusAction) return;
    await api.updateAdministrator(statusAction.account.id, { status: statusAction.status }, statusAction.reason.trim());
    notify(statusAction.status === 'active' ? '管理员已启用' : '管理员已停用');
    await reload();
  };
  const resetPassword = async () => {
    if (!passwordReset) return;
    await api.resetAdministratorPassword(passwordReset.account.id, passwordReset.password, passwordReset.reason.trim());
    notify('管理员密码已重置，原会话已失效');
    await accounts.reload();
  };
  const saveRole = async () => {
    if (!roleEditor) return;
    const input = { name: roleEditor.name.trim(), description: roleEditor.description.trim(), permissions: roleEditor.permissions };
    if (roleEditor.role) await api.updateAdministratorRole(roleEditor.role.id, input, roleEditor.reason.trim());
    else await api.createAdministratorRole(input, roleEditor.reason.trim());
    notify(roleEditor.role ? '自定义角色已更新，权限立即生效' : '自定义角色已创建');
    await reload();
  };
  const deleteRole = async () => {
    if (!roleDelete) return;
    await api.deleteAdministratorRole(roleDelete.role.id, roleDelete.reason.trim());
    notify('自定义角色已删除');
    await roles.reload();
  };
  const editorValid = Boolean(editor?.email.trim() && editor.displayName.trim() && editor.roleId && editor.reason.trim() && (editor.mode === 'edit' || (editor.password.length >= 8 && editor.password.length <= 128)));
  const roleEditorValid = Boolean(roleEditor && roleEditor.name.trim().length >= 2 && roleEditor.reason.trim());
  return <><PageHeader title="管理员与角色" description="管理数据库管理员账号、自定义角色及实时权限。系统内置角色保持只读。" actions={<button className="button primary" disabled={tab === 'accounts' ? !roles.data?.length : false} onClick={tab === 'accounts' ? openCreate : () => setRoleEditor({ name: '', description: '', permissions: [], reason: '' })}><Plus size={16} />{tab === 'accounts' ? '新增管理员' : '新建角色'}</button>} />
    <div className="tabs" role="tablist" aria-label="管理员管理分类" onKeyDown={tabListKeyDown}><button role="tab" aria-selected={tab === 'accounts'} className={tab === 'accounts' ? 'active' : ''} onClick={() => setTab('accounts')}>管理员账号</button><button role="tab" aria-selected={tab === 'roles'} className={tab === 'roles' ? 'active' : ''} onClick={() => setTab('roles')}>角色与权限</button></div>
    {tab === 'accounts' ? <><Toolbar query={query} setQuery={setQuery} placeholder="搜索邮箱、姓名、账号 ID 或角色"><select className="select-control" aria-label="管理员状态" value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="active">已启用</option><option value="disabled">已停用</option></select></Toolbar><DataPanel loading={accounts.loading} error={accounts.error} retry={accounts.reload} empty={!accounts.data?.items.length} emptyTitle="没有匹配的管理员" emptyDetail="调整搜索条件，或创建新的管理员账号。"><div className="table-wrap"><table><thead><tr><th>管理员</th><th>角色</th><th>状态</th><th>最近登录</th><th>密码更新</th><th>操作</th></tr></thead><tbody>{accounts.data?.items.map((account) => <tr key={account.id}><td><strong>{account.displayName}</strong><small>{account.email}</small><small className="mono">{account.id}</small></td><td><strong>{account.roleName || roleLabels[account.roleId] || account.roleId}</strong><small>{account.permissions.length ? `${account.permissions.length} 项写权限` : '默认只读'}</small></td><td><Badge value={account.status} /></td><td>{account.lastLoginAt ? dateTimeLabel(account.lastLoginAt) : '尚未登录'}</td><td>{dateTimeLabel(account.passwordUpdatedAt)}</td><td><div className="row-actions wrap"><button className="button secondary compact" onClick={() => openEdit(account)}>编辑</button><button className="button secondary compact" onClick={() => setPasswordReset({ account, password: '', confirmation: '', reason: '' })}>重置密码</button><button className={`button secondary compact ${account.status === 'active' ? 'danger-text' : ''}`} disabled={account.id === session.id && account.status === 'active'} title={account.id === session.id ? '不能停用当前登录账号' : ''} onClick={() => setStatusAction({ account, status: account.status === 'active' ? 'disabled' : 'active', reason: '' })}>{account.status === 'active' ? '停用' : '启用'}</button></div></td></tr>)}</tbody></table></div></DataPanel></> : <DataPanel loading={roles.loading} error={roles.error} retry={roles.reload} empty={!roles.data?.length} emptyTitle="暂无角色" emptyDetail="系统初始化后会自动创建六个内置角色。"><div className="role-management-grid">{roles.data?.map((role) => <section className="settings-section" key={role.id}><div className="panel-heading"><div><h2>{role.name}</h2><p>{role.description || '暂无说明'}</p></div><Badge value={role.builtIn ? 'neutral' : 'active'} label={role.builtIn ? '系统内置' : '自定义'} /></div><p className="permission-note">{role.accountCount} 个管理员使用 · <span className="mono">{role.id}</span></p><div className="permission-chip-list">{role.permissions.length ? role.permissions.map((permission) => <span key={permission}>{permissionLabels[permission] ?? permission}</span>) : <span>默认只读</span>}</div>{!role.builtIn && <div className="row-actions"><button className="button secondary compact" onClick={() => setRoleEditor({ role, name: role.name, description: role.description, permissions: [...role.permissions], reason: '' })}>编辑权限</button><button className="button secondary compact danger-text" disabled={role.accountCount > 0} title={role.accountCount > 0 ? '请先调整使用该角色的管理员' : ''} onClick={() => setRoleDelete({ role, reason: '' })}>删除</button></div>}</section>)}</div></DataPanel>}
    <ConfirmDialog open={Boolean(editor)} title={editor?.mode === 'create' ? '新增管理员' : '编辑管理员'} detail="邮箱不区分大小写；角色或账号资料变更后，该管理员的旧会话立即失效。" confirmLabel={editor?.mode === 'create' ? '创建管理员' : '保存修改'} confirmDisabled={!editorValid} onClose={() => setEditor(undefined)} onConfirm={saveAccount}>{editor && <><div className="form-grid"><label className="field-label">显示名称<input value={editor.displayName} maxLength={80} onChange={(event) => setEditor({ ...editor, displayName: event.target.value })} required /></label><label className="field-label">邮箱<input type="email" value={editor.email} maxLength={254} onChange={(event) => setEditor({ ...editor, email: event.target.value })} required /></label><label className="field-label">角色<select value={editor.roleId} onChange={(event) => setEditor({ ...editor, roleId: event.target.value })}>{roles.data?.map((role) => <option value={role.id} key={role.id}>{role.name}</option>)}</select></label>{editor.mode === 'create' && <label className="field-label">初始密码<input aria-label="初始密码" type="password" minLength={8} maxLength={128} value={editor.password} autoComplete="new-password" onChange={(event) => setEditor({ ...editor, password: event.target.value })} /><small className="field-hint">8–128 个字符</small></label>}</div><label className="field-label">操作原因<textarea value={editor.reason} maxLength={500} onChange={(event) => setEditor({ ...editor, reason: event.target.value })} placeholder="填写创建依据或变更工单" required /></label></>}</ConfirmDialog>
    <ConfirmDialog open={Boolean(statusAction)} title={statusAction?.status === 'disabled' ? '停用管理员' : '启用管理员'} detail={statusAction?.status === 'disabled' ? `${statusAction.account.displayName} 将立即无法登录，现有会话同时失效。` : `${statusAction?.account.displayName ?? ''} 将恢复后台登录权限。`} confirmLabel={statusAction?.status === 'disabled' ? '确认停用' : '确认启用'} danger={statusAction?.status === 'disabled'} confirmDisabled={!statusAction?.reason.trim()} onClose={() => setStatusAction(undefined)} onConfirm={changeStatus}>{statusAction && <label className="field-label">操作原因<textarea value={statusAction.reason} maxLength={500} onChange={(event) => setStatusAction({ ...statusAction, reason: event.target.value })} required /></label>}</ConfirmDialog>
    <ConfirmDialog open={Boolean(passwordReset)} title="重置管理员密码" detail={`${passwordReset?.account.displayName ?? ''} 的新密码会立即长期生效，所有旧会话同时失效。`} confirmLabel="确认重置" danger confirmDisabled={!passwordReset || passwordReset.password.length < 8 || passwordReset.password.length > 128 || passwordReset.password !== passwordReset.confirmation || !passwordReset.reason.trim()} onClose={() => setPasswordReset(undefined)} onConfirm={resetPassword}>{passwordReset && <><label className="field-label">新密码<input type="password" minLength={8} maxLength={128} autoComplete="new-password" value={passwordReset.password} onChange={(event) => setPasswordReset({ ...passwordReset, password: event.target.value })} /></label><label className="field-label">确认新密码<input type="password" minLength={8} maxLength={128} autoComplete="new-password" value={passwordReset.confirmation} onChange={(event) => setPasswordReset({ ...passwordReset, confirmation: event.target.value })} /></label><label className="field-label">操作原因<textarea value={passwordReset.reason} maxLength={500} onChange={(event) => setPasswordReset({ ...passwordReset, reason: event.target.value })} required /></label></>}</ConfirmDialog>
    <ConfirmDialog open={Boolean(roleEditor)} title={roleEditor?.role ? '编辑自定义角色' : '新建自定义角色'} detail="所有有效管理员默认拥有读取权限；这里只配置各功能域的写权限。" confirmLabel={roleEditor?.role ? '保存角色' : '创建角色'} confirmDisabled={!roleEditorValid} onClose={() => setRoleEditor(undefined)} onConfirm={saveRole}>{roleEditor && <><label className="field-label">角色名称<input value={roleEditor.name} minLength={2} maxLength={80} onChange={(event) => setRoleEditor({ ...roleEditor, name: event.target.value })} /></label><label className="field-label">角色说明<textarea value={roleEditor.description} maxLength={500} onChange={(event) => setRoleEditor({ ...roleEditor, description: event.target.value })} /></label><fieldset className="permission-editor"><legend>功能域写权限</legend>{Object.entries(permissionLabels).map(([permission, label]) => <label key={permission}><input type="checkbox" checked={roleEditor.permissions.includes(permission)} onChange={(event) => setRoleEditor({ ...roleEditor, permissions: event.target.checked ? [...roleEditor.permissions, permission] : roleEditor.permissions.filter((item) => item !== permission) })} />{label}</label>)}</fieldset><label className="field-label">操作原因<textarea value={roleEditor.reason} maxLength={500} onChange={(event) => setRoleEditor({ ...roleEditor, reason: event.target.value })} required /></label></>}</ConfirmDialog>
    <ConfirmDialog open={Boolean(roleDelete)} title="删除自定义角色" detail={`${roleDelete?.role.name ?? ''} 删除后无法恢复。已分配给管理员的角色不能删除。`} confirmLabel="确认删除" danger confirmDisabled={!roleDelete?.reason.trim()} onClose={() => setRoleDelete(undefined)} onConfirm={deleteRole}>{roleDelete && <label className="field-label">操作原因<textarea value={roleDelete.reason} maxLength={500} onChange={(event) => setRoleDelete({ ...roleDelete, reason: event.target.value })} required /></label>}</ConfirmDialog>
  </>;
}

function ChangePasswordPage() {
  const { api, notify, logout } = useApi();
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const valid = currentPassword.length > 0 && newPassword.length >= 8 && newPassword.length <= 128 && newPassword === confirmation && currentPassword !== newPassword;
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!valid) return;
    setSaving(true); setError('');
    try { await api.changeCurrentAdminPassword(currentPassword, newPassword); notify('密码已修改，请使用新密码重新登录'); logout(); }
    catch (cause) { setError(errorMessage(cause)); }
    finally { setSaving(false); }
  };
  return <><PageHeader title="修改密码" description="验证当前密码后更新管理员密码；成功后所有旧会话立即失效。" /><form className="settings-section password-change-card" onSubmit={(event) => void submit(event)}><div className="settings-title"><LockKeyhole size={20} /><div><h2>管理员密码</h2><p>密码长度为 8–128 个字符，不能与当前密码相同。</p></div></div><label className="field-label">当前密码<input type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} required /></label><label className="field-label">新密码<input type="password" autoComplete="new-password" minLength={8} maxLength={128} value={newPassword} onChange={(event) => setNewPassword(event.target.value)} required /></label><label className="field-label">确认新密码<input type="password" autoComplete="new-password" minLength={8} maxLength={128} value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required />{confirmation && confirmation !== newPassword && <small className="field-error">两次输入的新密码不一致</small>}</label>{error && <div className="inline-notice danger" role="alert"><AlertTriangle size={15} />{error}</div>}<button className="button primary" type="submit" disabled={!valid || saving}>{saving ? '正在修改…' : '修改密码并重新登录'}</button></form></>;
}

function SettingsPage() {
  const { api, mode, notify, can } = useApi(); const state = useResource(() => api.getSettings(), [api, mode]); const [form, setForm] = useState<AdminSettings>(); const [baseline, setBaseline] = useState<AdminSettings>(); const [saving, setSaving] = useState(false); const [confirming, setConfirming] = useState(false); const [reason, setReason] = useState(''); useEffect(() => { setForm(state.data); setBaseline(state.data); }, [state.data]);
  const dirty = Boolean(form && baseline && JSON.stringify(form) !== JSON.stringify(baseline));
  useUnsavedChanges(dirty, '系统业务策略有未保存的修改');
  const change = <K extends keyof AdminSettings>(key: K, value: AdminSettings[K]) => setForm((current) => current ? { ...current, [key]: value } : current);
  const submit = (event: FormEvent) => { event.preventDefault(); if (form && dirty && can('settings.write')) setConfirming(true); };
  const save = async () => { if (!form || !reason.trim()) throw new Error('请输入发布理由'); setSaving(true); try { const result = await api.updateSettings(form, reason.trim()); setForm(result); setBaseline(result); setConfirming(false); setReason(''); notify('系统设置已保存'); } finally { setSaving(false); } };
  const statuses: Array<[string, boolean]> = form ? [['PostgreSQL 数据库', form.configurationStatus.database], ['Redis 实时总线', form.configurationStatus.redis], ['对象存储', form.configurationStatus.objectStorage], ['短信验证码服务', form.configurationStatus.otpProvider], ['离线推送凭据', form.configurationStatus.pushProvider], ['LiveKit 媒体服务', form.configurationStatus.liveKit]] : [];
  return <><PageHeader title="系统设置" description="统一管理可热更新的业务策略；敏感密钥只展示配置状态，基础设施参数需修改环境变量并重启服务。" />{state.loading ? <Skeleton rows={8} /> : state.error || !form ? <ErrorState message={state.error} retry={state.reload} /> : <form className="settings-layout" onSubmit={(event) => void submit(event)}>
    <div className="settings-main"><section className="settings-section"><div className="settings-title"><CircleUserRound size={20} /><div><h2>注册与登录</h2><p>手机号验证码始终用于注册、换绑和找回密码；关闭注册不会影响已有账号登录。</p></div></div><Toggle label="允许新用户注册" description="关闭后密码注册接口会拒绝新账号。" checked={form.allowRegistration} onChange={(value) => change('allowRegistration', value)} /><div className="form-grid"><label className="field-label">密码最少字符数<input type="number" min="8" max="16" value={form.passwordMinLength} onChange={(event) => change('passwordMinLength', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section"><div className="settings-title"><MessageSquareText size={20} /><div><h2>消息、撤回与文件</h2><p>文本和撤回策略实时生效；上传上限由基础设施参数控制。消息删除策略需走独立合规流程。</p></div></div><div className="form-grid"><label className="field-label">文本最大字数<input type="number" min="100" max="10000" value={form.maxMessageTextLength} onChange={(event) => change('maxMessageTextLength', Number(event.target.value))} required /></label><label className="field-label">本人撤回时限（分钟）<input type="number" min="1" max="1440" value={form.messageRecallMinutes} onChange={(event) => change('messageRecallMinutes', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section"><div className="settings-title"><Group size={20} /><div><h2>群聊</h2><p>限制新建群与后续加人的最大规模。</p></div></div><div className="form-grid"><label className="field-label">群组最大成员数<input type="number" min="2" max="5000" value={form.maxGroupMembers} onChange={(event) => change('maxGroupMembers', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section"><div className="settings-title"><Users size={20} /><div><h2>好友与查找</h2><p>控制好友申请入口、查找方式及待处理申请的有效期。</p></div></div><Toggle label="允许发送好友申请" description="关闭后已有好友关系不受影响。" checked={form.allowFriendRequests} onChange={(value) => change('allowFriendRequests', value)} /><Toggle label="允许按呱呱号查找" description="关闭后客户端不展示呱呱号搜索入口，二维码添加不受影响。" checked={form.allowSearchByHandle} onChange={(value) => change('allowSearchByHandle', value)} /><Toggle label="允许按手机号查找" description="默认关闭；开启后仅返回允许展示的最小用户资料。" checked={form.allowSearchByPhone} onChange={(value) => change('allowSearchByPhone', value)} /><div className="form-grid"><label className="field-label">申请有效期（天）<input type="number" min="1" max="30" value={form.friendRequestExpiryDays} onChange={(event) => change('friendRequestExpiryDays', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section"><div className="settings-title"><Bell size={20} /><div><h2>公告与推送</h2><p>关闭后新建、编辑或手动发布公告不会写入离线推送队列，站内公告仍可发布。</p></div></div><Toggle label="允许公告离线推送" description="公告页面中的“发布时推送”仍需单独勾选。" checked={form.announcementPushEnabled} onChange={(value) => change('announcementPushEnabled', value)} /></section>
      <section className="settings-section"><div className="settings-title"><PhoneCall size={20} /><div><h2>音视频通话</h2><p>可即时关闭全部呼叫或仅关闭视频，正在进行的通话不会被强制中断。</p></div></div><Toggle label="启用音视频通话" description="关闭后新的语音和视频邀请都会被拒绝。" checked={form.callsEnabled} onChange={(value) => change('callsEnabled', value)} /><Toggle label="允许视频通话" description="关闭后仍可发起语音通话。" checked={form.videoCallsEnabled} onChange={(value) => change('videoCallsEnabled', value)} /></section>
      <section className="settings-section"><div className="settings-title"><ShieldAlert size={20} /><div><h2>风控与审核</h2><p>敏感词开关作用于新文本消息，审核时限用于运营 SLA。</p></div></div><Toggle label="启用敏感词拦截" description="关闭后词库保留，但不会拦截新文本消息。" checked={form.sensitiveWordEnabled} onChange={(value) => change('sensitiveWordEnabled', value)} /><div className="form-grid"><label className="field-label">举报处理时限（小时）<input type="number" min="1" max="168" value={form.reportSlaHours} onChange={(event) => change('reportSlaHours', Number(event.target.value))} required /></label></div></section>
      <section className="settings-section danger-zone"><div className="settings-title"><LockKeyhole size={20} /><div><h2>维护模式</h2><p>仅在版本升级或紧急故障处理时启用。</p></div></div><Toggle label="启用维护模式" description="启用后，仅管理员账号可以登录；用户将看到维护公告。" checked={form.maintenanceMode} onChange={(value) => change('maintenanceMode', value)} /><label className="field-label">维护公告<textarea value={form.announcement} onChange={(event) => change('announcement', event.target.value)} placeholder="预计完成时间、影响范围和客服联系方式" required={form.maintenanceMode} /></label></section>
      <section className="settings-section"><div className="settings-title"><Database size={20} /><div><h2>基础设施与密钥状态</h2><p>密钥永不回显；下列业务服务参数只读，修改环境变量后必须滚动重启。WuKongIM 连接容量在节点管理页查看。</p></div></div><div className="configuration-grid">{statuses.map(([label, configured]) => <div className="configuration-item" key={label}><span>{label}</span><Badge value={configured ? 'active' : 'failed'} label={configured ? '已配置' : '未配置'} /></div>)}</div><div className="infra-grid"><div><span>推送通道</span><strong>{form.infrastructure.pushProvider}</strong></div><div><span>文件上限</span><strong>{form.infrastructure.mediaMaxSizeMB} MB</strong></div><div><span>呼叫等待</span><strong>{form.infrastructure.callInviteTimeoutSeconds} 秒</strong></div><div><span>访问令牌</span><strong>{form.infrastructure.accessTokenMinutes} 分钟</strong></div><div><span>刷新令牌</span><strong>{form.infrastructure.refreshTokenHours} 小时</strong></div></div><div className="restart-note"><RefreshCcw size={14} />以上参数修改环境变量后需要重启服务，不会由本页面直接写入。</div></section>
    </div><aside className="settings-save"><h2>发布业务策略</h2><p>保存后服务端逐项校验并记录审计日志；热更新项无需重启。</p><div className="save-check"><CheckCircle2 size={17} /><span>数值范围与类型校验已启用</span></div><p className="permission-note" role="status">{dirty ? '有未保存的策略更改' : '当前设置与服务端一致'}</p><button className="button primary full" type="submit" disabled={saving || !dirty || !can('settings.write')}>{saving ? '正在保存…' : <><Save size={16} />保存并立即生效</>}</button>{!can('settings.write') && <p className="permission-note">当前角色没有设置发布权限。</p>}</aside>
  </form>}<ConfirmDialog open={confirming} title="发布系统业务策略" detail="设置保存后会立即影响新请求；基础设施只读项不会被修改。" confirmLabel="确认发布" danger={Boolean(form?.maintenanceMode)} confirmDisabled={saving || !reason.trim()} onClose={() => { setConfirming(false); setReason(''); }} onConfirm={save}><label className="field-label">发布理由<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} placeholder="填写变更单、运营策略或维护窗口原因" required /></label></ConfirmDialog></>;
}

function Toggle({ label, description, checked, onChange }: { label: string; description: string; checked: boolean; onChange: (value: boolean) => void }) {
  return <label className="toggle-row"><div><strong>{label}</strong><span>{description}</span></div><input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} /><span className="toggle-control" aria-hidden="true" /></label>;
}

function DataPanel({ loading, error, retry, empty, emptyTitle, emptyDetail, emptyIcon, children }: { loading: boolean; error: string; retry: () => void; empty: boolean; emptyTitle: string; emptyDetail: string; emptyIcon?: ReactNode; children: ReactNode }) {
  return <div className="data-panel">{loading ? <Skeleton rows={6} /> : error ? <ErrorState message={error} retry={retry} /> : empty ? <EmptyState title={emptyTitle} detail={emptyDetail} icon={emptyIcon ?? contextualEmptyIcon(emptyTitle)} /> : children}</div>;
}

function LoginPage({ onLogin, sessionNotice = '' }: { onLogin: (email: string, password: string) => Promise<void>; sessionNotice?: string }) {
  const [email, setEmail] = useState(''), [emailDirty, setEmailDirty] = useState(false), [emailTouched, setEmailTouched] = useState(false), [password, setPassword] = useState(''), [error, setError] = useState(''), [submitting, setSubmitting] = useState(false), [showPassword, setShowPassword] = useState(false);
  const normalizedEmail = email.trim();
  const emailInvalid = normalizedEmail.length > 0 && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(normalizedEmail);
  const emailError = emailTouched ? (!normalizedEmail ? '请输入管理员邮箱' : emailInvalid ? '请输入有效的管理员邮箱' : '') : '';
  const submit = async (event: FormEvent) => { event.preventDefault(); setEmailTouched(true); if (!normalizedEmail || emailInvalid || !password) return; setSubmitting(true); setError(''); try { await onLogin(normalizedEmail, password); } catch (cause) { setError(errorMessage(cause)); } finally { setSubmitting(false); } };
  return <main className="login-screen"><section className="login-card" aria-labelledby="login-title"><div className="login-brand"><img src="/qingwaguagua-mark.png" alt="" /><div><strong>青蛙呱呱</strong><span>运营控制台</span></div></div><div className="login-heading"><span>管理入口</span><h1 id="login-title">管理员登录</h1><p>使用数据库管理员账号登录。访问令牌只保存在当前标签页会话中。</p></div>{sessionNotice && <div className="inline-notice warning login-session-notice" role="status"><AlertTriangle size={15} />{sessionNotice}</div>}<form onSubmit={(event) => void submit(event)} noValidate><div className="field-label"><label htmlFor="admin-email">管理员邮箱</label><input id="admin-email" type="email" value={email} onBlur={() => { if (emailDirty) setEmailTouched(true); }} onChange={(event) => { setEmail(event.target.value); setEmailDirty(true); if (error) setError(''); }} autoComplete="username" required autoFocus aria-invalid={Boolean(emailError)} aria-describedby="admin-email-help" /><small id="admin-email-help" className={emailError ? 'field-error' : 'field-hint'}>{emailError || '使用管理员分配的邮箱地址'}</small></div><div className="field-label"><label htmlFor="admin-password">密码</label><div className="login-password-input"><input id="admin-password" aria-label="密码" type={showPassword ? 'text' : 'password'} value={password} onChange={(event) => { setPassword(event.target.value); if (error) setError(''); }} autoComplete="current-password" required /><button className="login-password-toggle" type="button" aria-label={showPassword ? '隐藏密码' : '显示密码'} aria-pressed={showPassword} onClick={() => setShowPassword((visible) => !visible)}>{showPassword ? <EyeOff size={17} /> : <Eye size={17} />}</button></div></div>{error && <div className="inline-notice danger" role="alert"><AlertTriangle size={15} />{error}</div>}<button className="button primary full login-submit" type="submit" disabled={submitting || !normalizedEmail || emailInvalid || !password}><LogIn size={17} />{submitting ? '正在验证…' : '登录控制台'}</button></form><div className="login-security"><ShieldCheck size={17} /><p>角色和权限由服务端数据库实时决定，本控制台不提供演示数据入口。</p></div></section></main>;
}

function readSession(): { session?: AdminSession; notice?: string } {
  const raw = sessionStorage.getItem(SESSION_KEY) ?? sessionStorage.getItem(LEGACY_SESSION_KEY);
  if (!raw) return {};
  try {
    const value = JSON.parse(raw ?? 'null') as AdminSession | null;
    if (value?.token && value.expiresAt > Date.now() && value.id && value.roleId && Array.isArray(value.permissions)) {
      sessionStorage.setItem(SESSION_KEY, JSON.stringify(value));
      sessionStorage.removeItem(LEGACY_SESSION_KEY);
      return { session: value };
    }
    sessionStorage.removeItem(SESSION_KEY);
    sessionStorage.removeItem(LEGACY_SESSION_KEY);
    return { notice: value?.token ? '管理员会话已到期，请重新登录' : '登录信息无效，请重新登录' };
  } catch { /* invalid session is discarded */ }
  sessionStorage.removeItem(SESSION_KEY);
  sessionStorage.removeItem(LEGACY_SESSION_KEY);
  return { notice: '登录信息无效，请重新登录' };
}

export function App() {
  const [initialSession] = useState(() => readSession());
  const [session, setSession] = useState<AdminSession | undefined>(initialSession.session);
  const [sessionNotice, setSessionNotice] = useState(initialSession.notice ?? '');
  const [notices, setNotices] = useState<Notice[]>([]);
  const notify = useCallback((message: string, tone: Notice['tone'] = 'success') => { const id = Date.now() + Math.random(); setNotices((current) => [...current, { id, tone, message }]); window.setTimeout(() => setNotices((current) => current.filter((notice) => notice.id !== id)), 4200); }, []);
  const logout = useCallback(() => { sessionStorage.removeItem(SESSION_KEY); sessionStorage.removeItem(LEGACY_SESSION_KEY); setSessionNotice(''); setSession(undefined); }, []);
  const invalidateSession = useCallback((message: string) => {
    const hadSession = Boolean(sessionStorage.getItem(SESSION_KEY) ?? sessionStorage.getItem(LEGACY_SESSION_KEY));
    if (!hadSession) return;
    sessionStorage.removeItem(SESSION_KEY);
    sessionStorage.removeItem(LEGACY_SESSION_KEY);
    setSessionNotice(message);
    setSession(undefined);
  }, []);
  useEffect(() => { const unauthorized = () => invalidateSession('管理员会话已失效，请重新登录'); window.addEventListener('nexachat:unauthorized', unauthorized); return () => window.removeEventListener('nexachat:unauthorized', unauthorized); }, [invalidateSession]);
  useEffect(() => {
    if (!session) return;
    const expire = () => invalidateSession('管理员会话已到期，请重新登录');
    const remaining = session.expiresAt - Date.now();
    if (remaining <= 0) { expire(); return; }
    const timer = window.setTimeout(expire, Math.min(remaining, 2_147_483_647));
    return () => window.clearTimeout(timer);
  }, [invalidateSession, session]);
  useEffect(() => {
    if (!session?.token) return;
    let active = true;
    void getApi(session.token).getCurrentAdmin().then((identity) => {
      if (!active || !identity.id || !identity.roleId) return;
      setSession((current) => {
        if (!current || current.token !== session.token) return current;
        const refreshed = { ...current, ...identity };
        sessionStorage.setItem(SESSION_KEY, JSON.stringify(refreshed));
        return refreshed;
      });
    }).catch((cause) => { if (active && cause instanceof ApiError && cause.status !== 401) notify(errorMessage(cause), 'danger'); });
    return () => { active = false; };
  }, [notify, session?.token]);
  const login = async (email: string, password: string) => { const candidate = await loginAdmin(email, password); sessionStorage.setItem(SESSION_KEY, JSON.stringify(candidate)); sessionStorage.removeItem(LEGACY_SESSION_KEY); setSessionNotice(''); setSession(candidate); };
  const api = useMemo(() => getApi(session?.token), [session?.token]);
  const value = session ? { api, mode: 'live' as const, session, logout, notify, can: (permission: Permission) => session.permissions.includes(permission) } : undefined;
  return <AppErrorBoundary>{value ? <ApiContext.Provider value={value}><Shell /></ApiContext.Provider> : <LoginPage onLogin={login} sessionNotice={sessionNotice} />}<div className="toast-region" aria-live="polite" aria-atomic="false">{notices.map((notice) => <div className={`toast ${notice.tone}`} role={notice.tone === 'danger' ? 'alert' : 'status'} aria-atomic="true" key={notice.id}>{notice.tone === 'success' ? <Check size={16} /> : <AlertTriangle size={16} />}{notice.message}</div>)}</div></AppErrorBoundary>;
}
