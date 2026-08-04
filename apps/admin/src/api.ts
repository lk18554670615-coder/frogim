import type {
  AdminApi,
  AnnouncementInput,
  AnnouncementRecord,
  AdminRole,
  AdminSession,
  AdminSettings,
  AuditLog,
  CallRecord,
  DashboardData,
  GroupRecord,
  FriendshipRecord,
  FeedbackRecord,
  HealthService,
  MediaRecord,
  MessageRecord,
  OnlineRecord,
  OperationsStatus,
  PageResult,
  ReportRecord,
  ReportResolutionAction,
  ReportResolutionResult,
  SensitiveWord,
  UserRecord,
} from './types';

type JsonObject = Record<string, unknown>;
const demoAllowed = import.meta.env.DEV || import.meta.env.VITE_ALLOW_DEMO === 'true';

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code = 'UNKNOWN',
    public readonly requestId = '',
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

const wait = (ms = 160) => new Promise((resolve) => setTimeout(resolve, ms));
const object = (value: unknown): JsonObject => value && typeof value === 'object' && !Array.isArray(value) ? value as JsonObject : {};
const string = (value: unknown, fallback = '') => typeof value === 'string' ? value : fallback;
const number = (value: unknown, fallback = 0) => typeof value === 'number' && Number.isFinite(value) ? value : fallback;
const boolean = (value: unknown, fallback = false) => typeof value === 'boolean' ? value : fallback;
const list = (value: unknown) => Array.isArray(value) ? value : [];
const formatDate = (value: unknown) => {
  const raw = string(value);
  if (!raw) return '暂无';
  const date = new Date(raw);
  return Number.isNaN(date.valueOf()) ? raw : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(date);
};
const initial = (name: string) => [...name][0] || '用';
const reportResolutionActions: ReportResolutionAction[] = ['dismiss', 'no_violation', 'delete_message', 'ban_user'];

function demoPageResult<T>(items: T[], page = 1, pageSize = 20): PageResult<T> {
  const safePage = Math.max(1, page);
  const safeSize = Math.min(100, Math.max(1, pageSize));
  const visible = items.slice((safePage - 1) * safeSize, safePage * safeSize);
  return { items: visible, page: safePage, pageSize: safeSize, total: items.length, hasNext: safePage * safeSize < items.length };
}

function unwrapItems(payload: unknown): { items: unknown[]; total?: number; nextCursor?: string; page?: number; pageSize?: number } {
  if (Array.isArray(payload)) return { items: payload };
  const root = object(payload);
  const data = object(root.data);
  const source = Array.isArray(root.items) ? root : Array.isArray(data.items) ? data : root;
  const items = list(source.items);
  const total = typeof source.total === 'number' ? source.total : undefined;
  const nextCursor = typeof source.nextCursor === 'string' && source.nextCursor ? source.nextCursor : undefined;
  const page = typeof source.page === 'number' ? source.page : undefined;
  const pageSize = typeof source.pageSize === 'number' ? source.pageSize : typeof source.limit === 'number' ? source.limit : undefined;
  return { items, total, nextCursor, page, pageSize };
}

function serverPage<T>(payload: unknown, adapter: (value: unknown) => T, requestedPage: number, requestedSize: number): PageResult<T> {
  const source = unwrapItems(payload);
  const page = Math.max(1, source.page ?? requestedPage);
  const pageSize = Math.min(100, Math.max(1, source.pageSize ?? requestedSize));
  const items = source.items.map(adapter);
  const total = Math.max(source.total ?? ((page - 1) * pageSize + items.length), items.length);
  return { items, page, pageSize, total, nextCursor: source.nextCursor, hasNext: Boolean(source.nextCursor) || (source.total !== undefined && page * pageSize < total) };
}

function adaptUser(value: unknown): UserRecord {
  const raw = object(value);
  const nickname = string(raw.nickname, string(raw.name, '未命名用户'));
  const status = raw.status === 'risk' ? 'risk' : boolean(raw.banned) || raw.status === 'banned' ? 'banned' : 'active';
  return {
    id: string(raw.id, 'unknown'), nickname, phone: string(raw.phone, '未提供'), handle: string(raw.handle, '未设置'), handleChangeCount: number(raw.handleChangeCount), bannedUntil: string(raw.bannedUntil) || undefined, avatar: string(raw.avatar, initial(nickname)), status,
    registeredAt: formatDate(raw.registeredAt ?? raw.createdAt), lastSeen: string(raw.lastSeen, '暂无'),
    deviceCount: number(raw.deviceCount), messageCount: number(raw.messageCount),
  };
}

function adaptGroup(value: unknown): GroupRecord {
  const raw = object(value);
  const status = raw.status === 'muted' || raw.status === 'dissolved' ? raw.status : 'active';
  return {
    id: string(raw.id, 'unknown'), name: string(raw.name, string(raw.title, '未命名群组')), owner: string(raw.owner, '暂无'),
    memberCount: number(raw.memberCount), messageCount: number(raw.messageCount, number(raw.lastMessageSeq)), status,
    createdAt: formatDate(raw.createdAt), reportCount: number(raw.reportCount),
  };
}

function adaptReport(value: unknown): ReportRecord {
  const raw = object(value);
  const targetType = raw.targetType === 'group' || raw.targetType === 'message' ? raw.targetType : 'user';
  const status = raw.status === 'reviewing' || raw.status === 'resolved' || raw.status === 'rejected' ? raw.status : 'pending';
  const risk = raw.risk === 'high' || raw.risk === 'low' ? raw.risk : 'medium';
  return {
    id: string(raw.id, 'unknown'), target: string(raw.target, `${targetType} ${string(raw.targetId, 'unknown')}`), targetType,
    reporter: string(raw.reporter, string(raw.reporterId, '匿名')), category: string(raw.category, string(raw.reason, '其他')),
    excerpt: string(raw.excerpt, string(raw.details, '没有补充说明')), status, risk, createdAt: formatDate(raw.createdAt),
  };
}

function adaptAudit(value: unknown): AuditLog {
  const raw = object(value);
  return {
    id: string(raw.id, crypto.randomUUID()), actor: string(raw.actor, string(raw.actorId, '系统')),
    action: string(raw.action, '未知操作'), target: string(raw.target, `${string(raw.targetType)} ${string(raw.targetId)}`.trim()),
    result: raw.result === 'failed' ? 'failed' : 'success', ip: string(raw.ip, '未记录'), createdAt: formatDate(raw.createdAt),
  };
}

function adaptMessage(value: unknown): MessageRecord {
  const raw = object(value); const body = object(raw.body);
  const preview = string(body.text, string(body.fileName, string(body.caption, JSON.stringify(body))));
  return {
    id: string(raw.id), clientMsgId: string(raw.clientMsgId), conversationId: string(raw.conversationId), senderId: string(raw.senderId),
    conversationSeq: number(raw.conversationSeq), type: string(raw.type, 'unknown'), preview: preview || '内容受保护', recalled: Boolean(raw.recalledAt),
    recalledAt: string(raw.recalledAt) || undefined, expiresAt: string(raw.expiresAt) || undefined, expiredAt: string(raw.expiredAt) || undefined,
    editedAt: string(raw.editedAt) || undefined, editVersion: number(raw.editVersion), createdAt: formatDate(raw.createdAt),
  };
}

function adaptMedia(value: unknown): MediaRecord {
  const raw = object(value);
  return { id: string(raw.id), ownerId: string(raw.ownerId), objectKey: string(raw.objectKey), mime: string(raw.mime, 'application/octet-stream'), status: string(raw.status, 'unknown'), size: number(raw.size), checksum: string(raw.checksum) };
}

function adaptOnline(value: unknown): OnlineRecord {
  const raw = object(value); return { userId: string(raw.userId), connections: number(raw.connections) };
}
function adaptFriendship(value: unknown): FriendshipRecord { const raw=object(value); return { userId:string(raw.UserID,string(raw.userId)),friendUserId:string(raw.FriendUserID,string(raw.friendUserId)),userName:string(raw.UserName,string(raw.userName)),friendName:string(raw.FriendName,string(raw.friendName)),createdAt:formatDate(raw.CreatedAt??raw.createdAt),updatedAt:formatDate(raw.UpdatedAt??raw.updatedAt) }; }
function adaptFeedback(value: unknown): FeedbackRecord { const raw=object(value); return { id:string(raw.ID,string(raw.id)),userId:string(raw.UserID,string(raw.userId)),userName:string(raw.UserName,string(raw.userName)),category:string(raw.Category,string(raw.category,'other')),content:string(raw.Content,string(raw.content)),contact:string(raw.Contact,string(raw.contact)),createdAt:formatDate(raw.CreatedAt??raw.createdAt) }; }

function adaptAnnouncement(value: unknown): AnnouncementRecord {
  const raw = object(value);
  const status = raw.status === 'scheduled' || raw.status === 'published' || raw.status === 'withdrawn' ? raw.status : 'draft';
  return {
    id: string(raw.id), title: string(raw.title), content: string(raw.content), status,
    pinned: boolean(raw.pinned), targetType: raw.targetType === 'users' ? 'users' : 'all',
    targetUserIds: list(raw.targetUserIds).filter((id): id is string => typeof id === 'string'),
    scheduledAt: string(raw.scheduledAt) || undefined, publishedAt: string(raw.publishedAt) || undefined,
    pushOnPublish: boolean(raw.pushOnPublish), createdBy: string(raw.createdBy, '系统'), createdAt: string(raw.createdAt),
  };
}

function adaptCall(value: unknown): CallRecord {
  const raw = object(value);
  return {
    id: string(raw.id), conversationId: string(raw.conversationId), callerId: string(raw.callerId), calleeId: string(raw.calleeId),
    mediaType: raw.mediaType === 'video' ? 'video' : 'audio', status: string(raw.status, 'unknown'), endReason: string(raw.endReason),
    invitedAt: string(raw.invitedAt), acceptedAt: string(raw.acceptedAt) || undefined, endedAt: string(raw.endedAt) || undefined,
    durationSeconds: number(raw.durationSeconds),
  };
}

function adaptDashboard(payload: unknown): DashboardData {
  const raw = object(object(payload).data ?? payload);
  if (Array.isArray(raw.metrics)) {
    return {
      metrics: raw.metrics as DashboardData['metrics'], messageTrend: list(raw.messageTrend) as DashboardData['messageTrend'],
      channelMix: list(raw.channelMix) as DashboardData['channelMix'], alerts: list(raw.alerts) as DashboardData['alerts'],
      activity: list(raw.activity).map(adaptAudit),
    };
  }
  const users = number(raw.users);
  const messages = number(raw.messages);
  const reports = number(raw.pendingReports);
  const sockets = number(raw.websocketConnections);
  return {
    metrics: [
      { label: '用户总数', value: users.toLocaleString(), delta: `${number(raw.bannedUsers)} 个封禁账号`, tone: 'info' },
      { label: '累计消息', value: messages.toLocaleString(), delta: '服务端持久化统计', tone: 'success' },
      { label: '待审举报', value: reports.toLocaleString(), delta: reports > 0 ? '请及时处理' : '当前队列为空', tone: reports > 0 ? 'warning' : 'success' },
      { label: '实时连接', value: sockets.toLocaleString(), delta: `${number(raw.conversations)} 个会话`, tone: 'info' },
    ],
    messageTrend: [], channelMix: [], alerts: [], activity: [],
  };
}

function adaptSettings(payload: unknown): AdminSettings {
  const raw = object(object(payload).data ?? payload);
  const status = object(raw.configurationStatus), infrastructure = object(raw.infrastructure);
  return {
    allowRegistration: boolean(raw.allowRegistration, boolean(raw.registrationEnabled, true)),
    passwordMinLength: number(raw.passwordMinLength, 8),
    maxMessageTextLength: number(raw.maxMessageTextLength, 5000), messageRecallMinutes: number(raw.messageRecallMinutes, 2),
    maxGroupMembers: number(raw.maxGroupMembers, 500),
    allowFriendRequests: boolean(raw.allowFriendRequests, true), allowSearchByHandle: boolean(raw.allowSearchByHandle, true), allowSearchByPhone: boolean(raw.allowSearchByPhone, false), friendRequestExpiryDays: number(raw.friendRequestExpiryDays, 7),
    announcementPushEnabled: boolean(raw.announcementPushEnabled, true), callsEnabled: boolean(raw.callsEnabled, true),
    videoCallsEnabled: boolean(raw.videoCallsEnabled, true), sensitiveWordEnabled: boolean(raw.sensitiveWordEnabled, true), reportSlaHours: number(raw.reportSlaHours, 8),
    maintenanceMode: boolean(raw.maintenanceMode), announcement: string(raw.announcement),
    configurationStatus: { database: boolean(status.database), redis: boolean(status.redis), objectStorage: boolean(status.objectStorage), otpProvider: boolean(status.otpProvider), pushProvider: boolean(status.pushProvider), turn: boolean(status.turn), adminTOTP: boolean(status.adminTOTP) },
    infrastructure: { pushProvider: string(infrastructure.pushProvider, 'noop'), mediaMaxSizeMB: number(infrastructure.mediaMaxSizeMB, 100), callInviteTimeoutSeconds: number(infrastructure.callInviteTimeoutSeconds, 30), websocketMaxPerUser: number(infrastructure.websocketMaxPerUser, 5), websocketMaxPerIP: number(infrastructure.websocketMaxPerIP, 20), accessTokenMinutes: number(infrastructure.accessTokenMinutes, 15), refreshTokenHours: number(infrastructure.refreshTokenHours, 720) },
    restartRequiredKeys: list(raw.restartRequiredKeys).filter((key): key is string => typeof key === 'string'),
  };
}

const users: UserRecord[] = [
  { id: 'u_10291', nickname: '林夏', phone: '138****7204', handle: 'linxia', handleChangeCount: 1, avatar: '林', status: 'active', registeredAt: '2026-07-21 09:12', lastSeen: '刚刚在线', deviceCount: 2, messageCount: 1820 },
  { id: 'u_10288', nickname: '江宁', phone: '186****1138', handle: 'jiangning', handleChangeCount: 0, avatar: '江', status: 'risk', registeredAt: '2026-07-20 18:44', lastSeen: '8 分钟前', deviceCount: 4, messageCount: 5931 },
  { id: 'u_10276', nickname: '周可', phone: '159****3866', handle: 'zhouke', handleChangeCount: 2, avatar: '周', status: 'banned', registeredAt: '2026-07-19 12:31', lastSeen: '2 天前', deviceCount: 1, messageCount: 423 },
  { id: 'u_10241', nickname: '陈屿', phone: '177****8510', handle: 'chenyu', handleChangeCount: 1, avatar: '陈', status: 'active', registeredAt: '2026-07-16 08:05', lastSeen: '35 分钟前', deviceCount: 2, messageCount: 2105 },
  { id: 'u_10192', nickname: '苏远', phone: '131****9022', handle: 'suyuan', handleChangeCount: 0, avatar: '苏', status: 'active', registeredAt: '2026-07-10 14:09', lastSeen: '1 小时前', deviceCount: 1, messageCount: 884 },
  { id: 'u_10134', nickname: '杨漾', phone: '185****4812', handle: 'yangyang', handleChangeCount: 2, avatar: '杨', status: 'risk', registeredAt: '2026-07-02 21:17', lastSeen: '在线', deviceCount: 5, messageCount: 12840 },
];

const groups: GroupRecord[] = [
  { id: 'g_3912', name: '社区摄影交流', owner: '林夏', memberCount: 286, messageCount: 18520, status: 'active', createdAt: '2026-04-12', reportCount: 0 },
  { id: 'g_3891', name: '周末徒步计划', owner: '陈屿', memberCount: 147, messageCount: 9361, status: 'active', createdAt: '2026-04-03', reportCount: 1 },
  { id: 'g_3804', name: '数码好物分享', owner: '江宁', memberCount: 498, messageCount: 46820, status: 'muted', createdAt: '2026-03-14', reportCount: 8 },
];

const reports: ReportRecord[] = [
  { id: 'r_8281', target: '消息 m_701188', targetType: 'message', reporter: 'u_10291', category: '疑似诈骗', excerpt: '点击链接即可领取会员退款，名额有限……', status: 'pending', risk: 'high', createdAt: '今天 10:42' },
  { id: 'r_8277', target: '数码好物分享', targetType: 'group', reporter: 'u_10182', category: '违规营销', excerpt: '群内持续发布第三方平台导流信息', status: 'reviewing', risk: 'medium', createdAt: '今天 09:18' },
  { id: 'r_8242', target: '消息 m_698811', targetType: 'message', reporter: 'u_10202', category: '不实信息', excerpt: '相关内容已由审核员确认并删除', status: 'resolved', risk: 'low', createdAt: '昨天 16:07' },
];

let sensitiveWords: SensitiveWord[] = [
  { id: 'sw_1', word: '代开发票', category: '黑产', matchType: 'exact', action: 'block', createdAt: '2026-07-29' },
  { id: 'sw_2', word: '免费领取*', category: '诈骗', matchType: 'fuzzy', action: 'review', createdAt: '2026-07-28' },
];

const auditLogs: AuditLog[] = [
  { id: 'a_5198', actor: '安全审核员 01', action: '封禁用户 24 小时', target: 'u_10134', result: 'success', ip: '10.24.8.15', createdAt: '今天 11:06:24' },
  { id: 'a_5197', actor: '运营管理员', action: '更新敏感词', target: '兼职刷单', result: 'success', ip: '10.24.6.11', createdAt: '今天 10:51:02' },
];

const healthServices: HealthService[] = [
  { name: 'WebSocket 网关', status: 'healthy', latency: 18, uptime: '99.998%', version: 'v0.1.0', detail: '3/3 实例正常' },
  { name: '消息服务', status: 'healthy', latency: 24, uptime: '99.995%', version: 'v0.1.0', detail: '4/4 实例正常' },
  { name: '实时事件路由', status: 'degraded', latency: 86, uptime: '99.940%', version: 'Redis', detail: 'Pub/Sub 延迟偏高' },
];

let settings: AdminSettings = { allowRegistration: true, passwordMinLength: 8, maxMessageTextLength: 5000, messageRecallMinutes: 2, maxGroupMembers: 500, allowFriendRequests: true, allowSearchByHandle: true, allowSearchByPhone: false, friendRequestExpiryDays: 7, announcementPushEnabled: true, callsEnabled: true, videoCallsEnabled: true, sensitiveWordEnabled: true, reportSlaHours: 8, maintenanceMode: false, announcement: '', configurationStatus: { database: true, redis: true, objectStorage: true, otpProvider: true, pushProvider: true, turn: true, adminTOTP: true }, infrastructure: { pushProvider: 'getui', mediaMaxSizeMB: 100, callInviteTimeoutSeconds: 30, websocketMaxPerUser: 5, websocketMaxPerIP: 20, accessTokenMinutes: 15, refreshTokenHours: 720 }, restartRequiredKeys: ['pushProvider', 'mediaMaxSizeMB', 'callInviteTimeoutSeconds', 'websocketMaxPerUser', 'websocketMaxPerIP', 'accessTokenMinutes', 'refreshTokenHours'] };
let announcements: AnnouncementRecord[] = [];

const demoApi: AdminApi = {
  async getDashboard() {
    await wait();
    return {
      metrics: [
        { label: '在线用户', value: '8,429', delta: '较昨日 +12.4%', tone: 'success' },
        { label: '今日消息', value: '1.82M', delta: '峰值 3,214 条/秒', tone: 'info' },
        { label: '待审举报', value: '23', delta: '其中高风险 4 条', tone: 'warning' },
        { label: '消息成功率', value: '99.993%', delta: '近 24 小时', tone: 'success' },
      ],
      messageTrend: [{ time: '00:00', count: 30 }, { time: '04:00', count: 18 }, { time: '08:00', count: 62 }, { time: '12:00', count: 68 }, { time: '16:00', count: 92 }, { time: '20:00', count: 100 }],
      channelMix: [{ label: '单聊', value: 68, color: 'var(--primary)' }, { label: '群聊', value: 27, color: 'var(--info)' }, { label: '系统消息', value: 5, color: 'var(--warning)' }],
      alerts: [{ id: 'al_1', title: '实时事件路由延迟偏高', detail: '请检查 Redis Pub/Sub 连接和订阅者', severity: 'warning', time: '3 分钟前' }],
      activity: auditLogs,
    };
  },
  async getUsers(query = '', status = '', page = 1, pageSize = 20) { await wait(); const needle = query.toLowerCase(); return demoPageResult(users.filter((u) => (!needle || `${u.nickname}${u.id}${u.phone}`.toLowerCase().includes(needle)) && (!status || u.status === status)), page, pageSize); },
  async banUser(id) { await wait(); const user = users.find((u) => u.id === id); if (user) user.status = 'banned'; },
  async unbanUser(id) { await wait(); const user = users.find((u) => u.id === id); if (user) user.status = 'active'; },
  async getGroups(query = '', status = '', page = 1, pageSize = 20) { await wait(); const needle = query.toLowerCase(); return demoPageResult(groups.filter((g) => (!needle || `${g.name}${g.id}${g.owner}`.toLowerCase().includes(needle)) && (!status || g.status===status)), page, pageSize); },
  async disbandGroup(id) { await wait(); const group = groups.find((g) => g.id === id); if (group) group.status = 'dissolved'; },
  async getReports(query = '', status = '', page = 1, pageSize = 20) { await wait(); const needle = query.toLowerCase(); return demoPageResult(reports.filter((r) => (!needle || `${r.id}${r.reporter}${r.target}${r.category}${r.excerpt}`.toLowerCase().includes(needle)) && (!status || r.status === status)), page, pageSize); },
  async resolveReport(id, action) { await wait(); const report = reports.find((r) => r.id === id); const status = action === 'dismiss' || action === 'no_violation' ? 'rejected' : 'resolved'; if (report) report.status = status; return { status, action }; },
  async getSensitiveWords() { await wait(); return [...sensitiveWords]; },
  async addSensitiveWord(input) { await wait(); const item = { ...input, id: `sw_${Date.now()}`, createdAt: new Date().toISOString().slice(0, 10) }; sensitiveWords = [item, ...sensitiveWords]; return item; },
  async deleteSensitiveWord(id) { await wait(); sensitiveWords = sensitiveWords.filter((word) => word.id !== id); },
  async getHealth() { await wait(); return healthServices; },
  async getAuditLogs(query = '', status = '', page = 1, pageSize = 20) { await wait(); const needle = query.toLowerCase(); return demoPageResult(auditLogs.filter((log) => (!needle || `${log.actor}${log.action}${log.target}${log.ip}`.toLowerCase().includes(needle)) && (!status || log.result === status)), page, pageSize); },
  async getMessages(_query = '', _type = '', page = 1, pageSize = 20) { await wait(); return demoPageResult<MessageRecord>([], page, pageSize); },
  async getMedia(_query = '', _status = '', page = 1, pageSize = 20) { await wait(); return demoPageResult<MediaRecord>([], page, pageSize); },
  async getOnline() { await wait(); return [] as OnlineRecord[]; },
  async getFriendships(query='',page=1,pageSize=20){await wait();const sample:FriendshipRecord[]=[{userId:'u_10134',friendUserId:'u_10826',userName:'林知夏',friendName:'周与安',createdAt:'2026-07-28 09:24',updatedAt:'2026-07-31 18:12'},{userId:'u_10942',friendUserId:'u_11703',userName:'陈默',friendName:'沈清禾',createdAt:'2026-07-26 14:08',updatedAt:'2026-07-30 21:46'}];const needle=query.toLowerCase();return demoPageResult(sample.filter(item=>!needle||`${item.userId}${item.friendUserId}${item.userName}${item.friendName}`.toLowerCase().includes(needle)),page,pageSize);},
  async getFeedback(query='',category='',page=1,pageSize=20){await wait();const sample:FeedbackRecord[]=[{id:'fb_7031',userId:'u_10826',userName:'周与安',category:'bug',content:'切换网络后图片发送状态停留在处理中。',contact:'应用内回复',createdAt:'2026-07-31 20:18'},{id:'fb_7026',userId:'u_11703',userName:'沈清禾',category:'feature',content:'希望群公告支持置顶和已读人数。',contact:'未提供',createdAt:'2026-07-31 16:42'}];const needle=query.toLowerCase();return demoPageResult(sample.filter(item=>(!needle||`${item.userId}${item.userName}${item.content}${item.contact}`.toLowerCase().includes(needle))&&(!category||item.category===category)),page,pageSize);},
  async getOperationsStatus(){await wait();return {push:{providers:[{provider:'getui',activeDevices:8342,disabledDevices:126},{provider:'apns_voip',activeDevices:6128,disabledDevices:84}],queue:[{status:'pending',count:18,attempts:20},{status:'sent',count:42861,attempts:43102}]},tasks:{scheduledMessages:{pending:7,processing:0,failed:0},messageExpiry:{waiting:124},mediaCleanup:{status:'healthy',lastRun:'2026-08-01 01:45'}},access:{current:{id:'demo',role:'platform_admin'},administrators:[{id:'demo',role:'platform_admin',source:'演示环境',mutable:false}],roles:[{id:'platform_admin',permissions:['全部管理权限']},{id:'moderator',permissions:['只读','用户处置','举报处置','敏感词']},{id:'support',permissions:['只读']}],note:'角色权限由服务端强制执行，管理员凭据不会在页面回显。'}} satisfies OperationsStatus;},
  async getAnnouncements(query = '', status = '', page = 1, pageSize = 20) { await wait(); const needle = query.toLowerCase(); return demoPageResult(announcements.filter((item) => (!needle || `${item.id}${item.title}${item.content}`.toLowerCase().includes(needle)) && (!status || item.status === status)), page, pageSize); },
  async createAnnouncement(input: AnnouncementInput) { await wait(); const item: AnnouncementRecord = { ...input, id: `announcement_${Date.now()}`, createdBy: 'demo_admin', createdAt: new Date().toISOString() }; announcements = [item, ...announcements]; return item; },
  async updateAnnouncement(id, input) { await wait(); const item = announcements.find((value) => value.id === id); if (!item) throw new ApiError('公告不存在', 404, 'NOT_FOUND'); Object.assign(item, input); return { ...item }; },
  async publishAnnouncement(id, enqueuePush) { await wait(); const item = announcements.find((value) => value.id === id); if (!item) throw new ApiError('公告不存在', 404, 'NOT_FOUND'); item.status = 'published'; item.pushOnPublish = enqueuePush; item.publishedAt = new Date().toISOString(); item.scheduledAt = undefined; return { ...item }; },
  async withdrawAnnouncement(id) { await wait(); const item = announcements.find((value) => value.id === id); if (!item) throw new ApiError('公告不存在', 404, 'NOT_FOUND'); item.status = 'withdrawn'; return { ...item }; },
  async deleteAnnouncement(id) { await wait(); announcements = announcements.filter((value) => value.id !== id); },
  async getCalls(_query = '', _status = '', page = 1, pageSize = 20) { await wait(); return demoPageResult<CallRecord>([], page, pageSize); },
  async getSettings() { await wait(); return { ...settings }; },
  async updateSettings(next) { await wait(); settings = { ...next }; return { ...settings }; },
};

const baseUrl = import.meta.env.VITE_ADMIN_API_URL ?? '/api/v1/admin';

async function request(path: string, token: string, init?: RequestInit, emitUnauthorized = true): Promise<unknown> {
  const response = await fetch(`${baseUrl}${path}`, {
    ...init,
    cache: 'no-store',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}), ...init?.headers },
  });
  if (!response.ok) {
    const requestId = response.headers.get('x-request-id') ?? '';
    let code = 'HTTP_ERROR';
    let message = `请求失败（${response.status}）`;
    try {
      const payload = object(await response.json());
      const error = object(payload.error);
      code = string(error.code, string(payload.code, code));
      message = string(error.message, string(payload.message, message));
    } catch { /* retain status error */ }
    if (response.status === 401 && emitUnauthorized) window.dispatchEvent(new CustomEvent('nexachat:unauthorized'));
    throw new ApiError(message, response.status, code, requestId);
  }
  if (response.status === 204) return undefined;
  return response.json();
}

function liveApi(token: string): AdminApi {
  return {
    async getDashboard() { return adaptDashboard(await request('/dashboard', token)); },
    async getUsers(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/users?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptUser, page, pageSize); },
    async banUser(id, reason, durationHours) { await request(`/users/${encodeURIComponent(id)}/ban`, token, { method: 'POST', body: JSON.stringify({ reason, durationHours }) }); },
    async unbanUser(id, reason) { await request(`/users/${encodeURIComponent(id)}/unban`, token, { method: 'POST', body: JSON.stringify({ reason }) }); },
    async getGroups(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/groups?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptGroup, page, pageSize); },
    async disbandGroup(id, reason) { await request(`/groups/${encodeURIComponent(id)}/disband`, token, { method: 'POST', body: JSON.stringify({ reason }) }); },
    async getReports(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/reports?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptReport, page, pageSize); },
    async resolveReport(id, action, note) { const raw = object(await request(`/reports/${encodeURIComponent(id)}/resolve`, token, { method: 'POST', body: JSON.stringify({ action, note }) })); const status = raw.status === 'rejected' ? 'rejected' : 'resolved'; const responseAction = string(raw.action, action); return { status, action: reportResolutionActions.includes(responseAction as ReportResolutionAction) ? responseAction as ReportResolutionAction : action } satisfies ReportResolutionResult; },
    async getSensitiveWords() { const source = unwrapItems(await request('/sensitive-words', token)); return source.items.map((value) => { const raw = object(value); const word = string(raw.word); return { id: string(raw.id), word, category: string(raw.category, '其他'), matchType: 'exact' as const, action: 'block' as const, createdAt: formatDate(raw.createdAt) }; }); },
    async addSensitiveWord(input) { const raw = object(await request('/sensitive-words', token, { method: 'POST', body: JSON.stringify({ word: input.word, category: input.category }) })); return { ...input, matchType: 'exact', action: 'block', id: string(raw.id), word: string(raw.word, input.word), category: string(raw.category, input.category), createdAt: formatDate(raw.createdAt ?? new Date().toISOString()) }; },
    async deleteSensitiveWord(id, reason) { await request(`/sensitive-words/${encodeURIComponent(id)}`, token, { method: 'DELETE', body: JSON.stringify({ reason }) }); },
    async getHealth() { const payload = await request('/health', token); const source = unwrapItems(payload); if (source.items.length) return source.items.map((value) => { const raw = object(value); const status = raw.status === 'down' || raw.status === 'degraded' ? raw.status : 'healthy'; return { name: string(raw.name, '服务'), status, latency: number(raw.latency), uptime: string(raw.uptime, '暂无'), version: string(raw.version, '暂无'), detail: string(raw.detail, '服务已响应') }; }); const raw = object(payload); return [{ name: string(raw.service, 'IM API'), status: raw.status === 'ok' ? 'healthy' : 'down', latency: 0, uptime: typeof raw.uptimeSeconds === 'number' ? `${Math.floor(raw.uptimeSeconds / 60)} 分钟` : '暂无', version: '暂无', detail: raw.status === 'ok' ? 'API 进程运行正常' : 'API 返回异常状态' }]; },
    async getAuditLogs(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/audit-logs?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptAudit, page, pageSize); },
    async getMessages(q = '', type = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/messages?q=${encodeURIComponent(q)}&type=${encodeURIComponent(type)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptMessage, page, pageSize); },
    async getMedia(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/media?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptMedia, page, pageSize); },
    async getOnline() { return unwrapItems(await request('/online', token)).items.map(adaptOnline); },
    async getFriendships(q='',page=1,pageSize=20,cursor=''){const payload=await request(`/friendships?q=${encodeURIComponent(q)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`,token);return serverPage(payload,adaptFriendship,page,pageSize);},
    async getFeedback(q='',category='',page=1,pageSize=20,cursor=''){const payload=await request(`/feedback?q=${encodeURIComponent(q)}&category=${encodeURIComponent(category)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`,token);return serverPage(payload,adaptFeedback,page,pageSize);},
    async getOperationsStatus(){const [push,tasks,access]=await Promise.all([request('/push',token),request('/tasks',token),request('/access',token)]);return {push:object(push) as OperationsStatus['push'],tasks:object(object(tasks).tasks),access:object(access) as unknown as OperationsStatus['access']};},
    async getAnnouncements(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/announcements?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptAnnouncement, page, pageSize); },
    async createAnnouncement(input) { return adaptAnnouncement(await request('/announcements', token, { method: 'POST', body: JSON.stringify(input) })); },
    async updateAnnouncement(id, input) { return adaptAnnouncement(await request(`/announcements/${encodeURIComponent(id)}`, token, { method: 'PUT', body: JSON.stringify(input) })); },
    async publishAnnouncement(id, enqueuePush) { return adaptAnnouncement(await request(`/announcements/${encodeURIComponent(id)}/publish`, token, { method: 'POST', body: JSON.stringify({ enqueuePush }) })); },
    async withdrawAnnouncement(id) { return adaptAnnouncement(await request(`/announcements/${encodeURIComponent(id)}/withdraw`, token, { method: 'POST' })); },
    async deleteAnnouncement(id, reason) { await request(`/announcements/${encodeURIComponent(id)}`, token, { method: 'DELETE', body: JSON.stringify({ reason }) }); },
    async getCalls(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/calls?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptCall, page, pageSize); },
    async getSettings() { return adaptSettings(await request('/settings', token)); },
    async updateSettings(input) { const { configurationStatus: _configurationStatus, infrastructure: _infrastructure, restartRequiredKeys: _restartRequiredKeys, ...runtime } = input; const payload = await request('/settings', token, { method: 'PUT', body: JSON.stringify({ ...runtime, registrationEnabled: input.allowRegistration }) }); return adaptSettings(payload); },
  };
}

export function getApi(mode: 'demo' | 'live', token = ''): AdminApi {
  return demoAllowed && mode === 'demo' ? demoApi : liveApi(token);
}

function adaptRole(value: unknown): AdminRole {
  if (value === 'platform_admin' || value === 'super_admin' || value === 'admin') return 'platform_admin';
  if (value === 'moderator') return 'moderator';
  return 'support';
}

export async function loginAdmin(email: string, password: string, totp = ''): Promise<AdminSession> {
  const payload = object(await request('/auth/login', '', {
    method: 'POST',
    body: JSON.stringify({ email, password, ...(totp ? { totp } : {}) }),
  }, false));
  const raw = object(payload.data ?? payload);
  const token = string(raw.accessToken, string(raw.token));
  if (!token) throw new ApiError('登录响应缺少访问令牌', 502, 'INVALID_AUTH_RESPONSE');
  return {
    token,
    displayName: string(raw.displayName, string(raw.name, email)),
    role: adaptRole(raw.role),
    expiresAt: Date.now() + Math.max(60, number(raw.expiresIn, 900)) * 1000,
  };
}
