import type {
  AdminApi,
  AnnouncementInput,
  AnnouncementRecord,
  AdminRole,
  AdminSession,
  AdminSettings,
  AdminSystemMessageResult,
  AdminUserDeviceRecord,
  AuditLog,
  CallRecord,
  ClientPlatform,
  ClientVersionPolicy,
  ClientVersionReleaseRecord,
  DashboardData,
  GroupMemberRecord,
  GroupOverview,
  GroupRecord,
  FriendshipRecord,
  FeedbackRecord,
  HealthService,
  MediaRecord,
  MessageRecord,
  MomentModerationRecord,
  OnlineRecord,
  OperationsStatus,
  PageResult,
  ReportRecord,
  ReportResolutionAction,
  ReportResolutionResult,
  SensitiveWord,
  StickerPackModerationRecord,
  StickerCategoryOperationsRecord,
  StickerCategoryInput,
  StickerPackInput,
  StickerItemOperationsRecord,
  StickerItemInput,
  UserRecord,
  UserOverview,
  WukongOverview,
  WukongRuntimeSettings,
  WukongNode,
  WukongConnection,
  WukongChannel,
  WukongStoredMessage,
  WukongDevice,
  WukongSystemUser,
  WukongRobotProfile,
  WukongPlugin,
  WukongPluginRelease,
  WukongPluginEvent,
  WukongPluginLogEntry,
  LiveKitRoom,
  LiveKitParticipant,
  LiveKitMetrics,
  BusinessChannelRecord,
  BusinessChannelMemberRecord,
  BusinessChannelAccessRecord,
  SupportSkillRecord,
  SupportAgentRecord,
  SupportSessionRecord,
} from './types';

type JsonObject = Record<string, unknown>;

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

const object = (value: unknown): JsonObject => value && typeof value === 'object' && !Array.isArray(value) ? value as JsonObject : {};
const string = (value: unknown, fallback = '') => typeof value === 'string' ? value : fallback;
const number = (value: unknown, fallback = 0) => typeof value === 'number' && Number.isFinite(value) ? value : fallback;
const boolean = (value: unknown, fallback = false) => typeof value === 'boolean' ? value : fallback;
const optionalNumber = (value: unknown) => typeof value === 'number' && Number.isFinite(value) ? value : null;
const list = (value: unknown) => Array.isArray(value) ? value : [];
const chineseTextPattern = /[\u3400-\u9fff]/;
const apiErrorMessages: Record<string, string> = {
  UNAUTHENTICATED: '登录状态已失效，请重新登录',
  INVALID_CREDENTIALS: '邮箱、密码或动态验证码无效',
  TOTP_REQUIRED: '请输入动态验证码后重试',
  INVALID_TOTP: '动态验证码无效或已过期',
  FORBIDDEN: '当前账号没有执行此操作的权限',
  NOT_FOUND: '目标记录不存在或已被删除',
  CONFLICT: '数据状态已发生变化，请刷新后重试',
  INVALID_ARGUMENT: '提交内容不符合要求，请检查后重试',
  CONFIRMATION_REQUIRED: '请确认操作并填写原因',
  RATE_LIMITED: '操作过于频繁，请稍后重试',
  PAYLOAD_TOO_LARGE: '提交内容过大，请缩小文件或内容后重试',
  DATASTORE_UNAVAILABLE: '数据服务暂时不可用，请稍后重试',
  DATASOURCE_UNAVAILABLE: '同步数据源暂时不可用，请检查服务状态后重试',
  WUKONG_UNAVAILABLE: '即时通信服务暂时不可用，请稍后重试',
  WUKONG_UPSTREAM_ERROR: '即时通信管理服务暂时不可用，请检查服务状态后重试',
  IM_UNAVAILABLE: '即时通信服务暂时不可用，请稍后重试',
  IM_DISABLED: '即时通信服务当前未启用',
  LIVEKIT_UNAVAILABLE: '音视频服务暂时不可用，请稍后重试',
  LIVEKIT_UPSTREAM_ERROR: '音视频管理服务暂时不可用，请检查服务状态后重试',
  MEDIA_UNAVAILABLE: '文件存储服务暂时不可用，请稍后重试',
  SMS_UNAVAILABLE: '短信验证码服务暂时不可用，请稍后重试',
  SMS_NOT_CONFIGURED: '短信验证码服务尚未完成配置',
  DEVICE_STATE_UNAVAILABLE: '设备已下线，但凭据状态暂未同步，请稍后刷新确认',
  PLUGIN_STORE_UNAVAILABLE: '插件状态存储暂时不可用，请稍后重试',
  PLUGIN_LIFECYCLE_UNAVAILABLE: '插件生命周期管理暂时不可用，请稍后重试',
  NOT_READY: '核心依赖尚未就绪，请检查系统健康状态',
  MAINTENANCE: '系统正在维护，请稍后重试',
  INTERNAL: '服务处理失败，请稍后重试',
  INTERNAL_ERROR: '服务处理失败，请稍后重试',
};

function localizedApiErrorMessage(code: string, rawMessage: string, status: number) {
  const mapped = apiErrorMessages[code.trim().toUpperCase()];
  const genericStatusMessage = /^请求失败（\d+）$/.test(rawMessage);
  if (mapped && (!rawMessage || genericStatusMessage)) return mapped;
  if (rawMessage && chineseTextPattern.test(rawMessage)) return rawMessage;
  if (mapped) return mapped;
  if (status === 400 || status === 422) return '提交内容不符合要求，请检查后重试';
  if (status === 401) return '登录状态已失效，请重新登录';
  if (status === 403) return '当前账号没有执行此操作的权限';
  if (status === 404) return '请求的功能或记录不存在';
  if (status === 409) return '数据状态已发生变化，请刷新后重试';
  if (status === 413) return '提交内容过大，请缩小后重试';
  if (status === 429) return '操作过于频繁，请稍后重试';
  if (status >= 500) return '服务暂时不可用，请稍后重试';
  return '请求未完成，请稍后重试';
}
const formatDate = (value: unknown) => {
  const raw = string(value);
  if (!raw) return '暂无';
  const date = new Date(raw);
  return Number.isNaN(date.valueOf()) ? raw : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(date);
};
const initial = (name: string) => [...name][0] || '用';
const reportResolutionActions: ReportResolutionAction[] = ['dismiss', 'no_violation', 'delete_message', 'ban_user'];
const backupStatuses: OperationsStatus['backups']['status'][] = ['healthy', 'running', 'never', 'failed', 'warning', 'stale', 'unavailable', 'unconfigured'];

function adaptBackupStatus(value: unknown): OperationsStatus['backups'] {
  const raw = object(value);
  const candidate = string(raw.status, 'unavailable') as OperationsStatus['backups']['status'];
  return {
    configured: boolean(raw.configured),
    available: boolean(raw.available),
    status: backupStatuses.includes(candidate) ? candidate : 'unavailable',
    lastStatus: boolean(raw.lastStatus),
    running: boolean(raw.running),
    lastDurationSeconds: Math.max(0, number(raw.lastDurationSeconds)),
    incompleteGenerations: Math.max(0, number(raw.incompleteGenerations)),
    offsiteEnabled: boolean(raw.offsiteEnabled),
    lastAttemptAt: string(raw.lastAttemptAt) || undefined,
    lastSuccessAt: string(raw.lastSuccessAt) || undefined,
  };
}

function adaptClientDiagnostics(value: unknown): OperationsStatus['diagnostics'] {
  const raw = object(value);
  const summary = object(raw.summary);
  const allowedKinds = ['crash', 'performance', 'connection', 'call'] as const;
  return {
    summary: {
      windowHours: number(summary.windowHours, 24),
      crashes: number(summary.crashes),
      connectionFailures: number(summary.connectionFailures),
      callFailures: number(summary.callFailures),
      performanceSamples: number(summary.performanceSamples),
      performanceP95Ms: typeof summary.performanceP95Ms === 'number' ? summary.performanceP95Ms : undefined,
    },
    items: list(raw.items).map((value) => {
      const item = object(value);
      const candidate = string(item.kind) as typeof allowedKinds[number];
      return {
        id: string(item.id), userId: string(item.userId),
        kind: allowedKinds.includes(candidate) ? candidate : 'crash',
        name: string(item.name), fingerprint: string(item.fingerprint),
        platform: string(item.platform, 'unknown'), appVersion: string(item.appVersion, 'unknown'),
        durationMs: typeof item.durationMs === 'number' ? item.durationMs : undefined,
        occurredAt: string(item.occurredAt),
      };
    }),
  };
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

function localPageResult<T>(items: T[], requestedPage: number, requestedSize: number): PageResult<T> {
  const page = Math.max(1, requestedPage);
  const pageSize = Math.min(100, Math.max(1, requestedSize));
  const start = (page - 1) * pageSize;
  return { items: items.slice(start, start + pageSize), page, pageSize, total: items.length, hasNext: start + pageSize < items.length };
}

function adaptUser(value: unknown): UserRecord {
  const raw = object(value);
  const nickname = string(raw.nickname, string(raw.name, '未命名用户'));
  const status = boolean(raw.banned) || raw.status === 'banned' ? 'banned' : 'active';
  const lastOfflineAt = string(raw.lastOfflineAt) || undefined;
  return {
    id: string(raw.id, 'unknown'), nickname, phone: string(raw.phone, '未提供'), handle: string(raw.handle, '未设置'), remark: string(raw.remark), tags: list(raw.tags).map((tag) => string(tag)).filter(Boolean), handleChangeCount: number(raw.handleChangeCount), bannedUntil: string(raw.bannedUntil) || undefined,
    avatar: string(raw.avatar, initial(nickname)), avatarUrl: string(raw.avatarUrl), status,
    online: boolean(raw.online), onlineConnections: number(raw.onlineConnections), lastOfflineAt,
    registeredAt: formatDate(raw.registeredAt ?? raw.createdAt), lastSeen: string(raw.lastSeen, lastOfflineAt ? formatDate(lastOfflineAt) : '暂无'),
    deviceCount: number(raw.deviceCount), messageCount: number(raw.messageCount),
  };
}

function adaptAdminUserDevice(value: unknown): AdminUserDeviceRecord {
  const raw = object(value);
  return {
    id: string(raw.id, 'unknown'), userId: string(raw.userId), platform: string(raw.platform, 'unknown'), provider: string(raw.provider, '未配置'),
    notificationsEnabled: boolean(raw.notificationsEnabled), previewEnabled: boolean(raw.previewEnabled),
    soundEnabled: boolean(raw.soundEnabled), vibrationEnabled: boolean(raw.vibrationEnabled), updatedAt: formatDate(raw.updatedAt),
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
  const result = raw.result === 'success' || raw.result === 'failed' ? raw.result : 'unknown';
  return {
    id: string(raw.id, crypto.randomUUID()), actor: string(raw.actor, string(raw.actorId, '未提供')),
    action: string(raw.action, '未知操作'), target: string(raw.target, `${string(raw.targetType)} ${string(raw.targetId)}`.trim()),
    result, ip: string(raw.ip, '未记录'), createdAt: formatDate(raw.createdAt),
  };
}

function unwrapItem(payload: unknown): unknown {
  const root = object(payload);
  const data = object(root.data);
  return root.item ?? data.item ?? data.data ?? root.data ?? payload;
}

function adaptClientVersion(value: unknown): ClientVersionPolicy {
  const outer = object(value);
  const nested = object(outer.data);
  const raw = Object.keys(nested).length ? nested : outer;
  const platformValue = string(raw.platform);
  if (!['android', 'ios', 'web', 'macos'].includes(platformValue)) {
    throw new Error('服务端版本策略缺少有效平台');
  }
  const minimumVersion = string(raw.minimumVersion);
  const latestVersion = string(raw.latestVersion);
  if (!minimumVersion || !latestVersion) {
    throw new Error('服务端版本策略缺少必需版本号');
  }
  if (typeof raw.forceUpdate !== 'boolean') {
    throw new Error('服务端版本策略缺少强制更新状态');
  }
  if (typeof raw.rolloutPercentage !== 'number' || !Number.isFinite(raw.rolloutPercentage)) {
    throw new Error('服务端版本策略缺少有效灰度比例');
  }
  const platform = platformValue as ClientVersionPolicy['platform'];
  return {
    platform,
    minimumVersion,
    latestVersion,
    forceUpdate: raw.forceUpdate,
    rolloutPercentage: Math.min(100, Math.max(0, raw.rolloutPercentage)),
    releaseNotes: string(raw.releaseNotes),
    downloadUrl: string(raw.downloadUrl),
    updatedBy: string(raw.updatedBy),
    updatedAt: string(raw.updatedAt),
  };
}

function adaptClientVersionRelease(value: unknown): ClientVersionReleaseRecord {
  const raw = object(value);
  const metadata = object(raw.metadata);
  const snapshot = Object.keys(metadata).length ? {
    ...metadata,
    id: raw.id,
    platform: raw.targetId,
    updatedBy: raw.actorId,
    updatedAt: raw.createdAt,
  } : raw;
  return {
    ...adaptClientVersion(snapshot),
    id: string(raw.id, crypto.randomUUID()),
    reason: string(snapshot.reason, '未填写发布原因'),
  };
}

function adaptModerationMoment(value: unknown): MomentModerationRecord {
  const raw = object(value);
  const status = raw.status === 'hidden' || raw.status === 'deleted' ? raw.status : 'published';
  return {
    id: string(raw.id, 'unknown'), authorId: string(raw.authorId), authorName: string(raw.authorName, '未知用户'),
    content: string(raw.content), mediaKind: string(raw.mediaKind, 'none'), mediaCount: list(raw.media).length,
    visibility: string(raw.visibility), likeCount: number(raw.likeCount), commentCount: number(raw.commentCount),
    status, createdAt: formatDate(raw.createdAt),
  };
}

function adaptModerationStickerPack(value: unknown): StickerPackModerationRecord {
  const raw = object(value);
  const allowed = ['draft', 'reviewing', 'published', 'rejected', 'disabled'];
  const rawStatus = string(raw.status, 'draft');
  const status = (allowed.includes(rawStatus) ? rawStatus : 'draft') as StickerPackModerationRecord['status'];
  return {
    id: string(raw.id, 'unknown'), name: string(raw.name, '未命名表情包'), categoryName: string(raw.categoryName),
    description: string(raw.description), status, itemCount: list(raw.items).length, createdBy: string(raw.createdBy),
    reviewedBy: string(raw.reviewedBy), reviewReason: string(raw.reviewReason), updatedAt: formatDate(raw.updatedAt),
    categoryId: string(raw.categoryId), coverMediaId: string(raw.coverMediaId), sortOrder: number(raw.sortOrder),
    items: list(raw.items).map(adaptStickerItem),
  };
}

function adaptStickerCategory(value: unknown): StickerCategoryOperationsRecord {
  const raw = object(value);
  return { id: string(raw.id), name: string(raw.name), sortOrder: number(raw.sortOrder), enabled: boolean(raw.enabled, true) };
}

function adaptStickerItem(value: unknown): StickerItemOperationsRecord {
  const raw = object(value);
  return {
    id: string(raw.id), packId: string(raw.packId), name: string(raw.name), mediaId: string(raw.mediaId),
    emoji: string(raw.emoji), sortOrder: number(raw.sortOrder), status: raw.status === 'disabled' ? 'disabled' : 'published',
  };
}

const flag = (value: unknown) => boolean(value) || number(value) === 1;
const optionalFlag = (value: unknown) => {
  if (typeof value === 'boolean') return value;
  if (value === 0 || value === 1) return value === 1;
  return null;
};
const managerData = (payload: unknown) => {
  const raw = object(payload);
  return list(Array.isArray(raw.data) ? raw.data : raw.items);
};

function adaptWukongOverview(value: unknown): WukongOverview {
  const raw = object(value);
  return {
    serverId: string(raw.server_id), version: string(raw.version), uptime: string(raw.uptime), connections: optionalNumber(raw.connections),
    userHandlers: optionalNumber(raw.user_handler_count), cpu: optionalNumber(raw.cpu), memoryBytes: optionalNumber(raw.mem), goroutines: optionalNumber(raw.goroutine),
    inMessages: optionalNumber(raw.in_msgs), outMessages: optionalNumber(raw.out_msgs), retryQueue: optionalNumber(raw.retry_queue),
  };
}

function adaptWukongSettings(value: unknown): WukongRuntimeSettings {
  const raw = object(value);
  const logger = object(raw.logger);
  return {
    traceEnabled: optionalFlag(logger.trace_on), lokiEnabled: optionalFlag(logger.loki_on),
    prometheusEnabled: optionalFlag(raw.prometheus_on), stressEnabled: optionalFlag(raw.stress_on),
  };
}

function adaptWukongNode(value: unknown): WukongNode {
  const raw = object(value);
  return { id: number(raw.id), online: flag(raw.online), leader: flag(raw.is_leader), apiAddress: string(raw.api_server_addr, string(raw.api_addr)), version: string(raw.version), slotCount: number(raw.slot_count), slotLeaderCount: number(raw.slot_leader_count) };
}

function adaptWukongConnection(value: unknown): WukongConnection {
  const raw = object(value);
  return { id: number(raw.id), uid: string(raw.uid), ip: string(raw.ip), device: string(raw.device), deviceId: string(raw.device_id), nodeId: number(raw.node_id), lastActivity: formatDate(raw.last_activity), inMessages: number(raw.in_msgs), outMessages: number(raw.out_msgs) };
}

function adaptWukongChannel(value: unknown): WukongChannel {
  const raw = object(value);
  return { channelId: string(raw.channel_id), channelType: number(raw.channel_type), subscriberCount: number(raw.subscriber_count), denylistCount: number(raw.denylist_count), allowlistCount: number(raw.allowlist_count), banned: flag(raw.ban), disbanded: flag(raw.disband), createdAt: number(raw.created_at) };
}

function adaptWukongMessage(value: unknown): WukongStoredMessage {
  const raw = object(value);
  return { messageId: string(raw.message_idstr, String(number(raw.message_id))), messageSeq: number(raw.message_seq), clientMsgNo: string(raw.client_msg_no), fromUid: string(raw.from_uid), channelId: string(raw.channel_id), channelType: number(raw.channel_type), timestamp: number(raw.timestamp) };
}

function adaptWukongDevice(value: unknown): WukongDevice {
  const raw = object(value);
  return { uid: string(raw.uid), deviceFlag: number(raw.device_flag), deviceLevel: number(raw.device_level), tokenPresent: Boolean(string(raw.token)) || flag(raw.token_on), createdAt: number(raw.created_at), updatedAt: number(raw.updated_at) };
}

function adaptWukongSystemUser(value: unknown): WukongSystemUser {
  const raw = object(value);
  const rawStatus = string(raw.syncStatus, 'synced');
  const syncStatus = (['pending', 'processing', 'failed'].includes(rawStatus) ? rawStatus : 'synced') as WukongSystemUser['syncStatus'];
  return {
    userId: string(raw.userId), name: string(raw.name), enabled: boolean(raw.enabled), syncStatus,
    updatedBy: string(raw.updatedBy), reason: string(raw.reason), updatedAt: formatDate(raw.updatedAt),
  };
}

function adaptBusinessChannel(value: unknown): BusinessChannelRecord {
  const raw = object(value);
  const rawType = number(raw.channelType);
  const channelType = (rawType === 5 || rawType === 6 || rawType === 9 ? rawType : 4) as BusinessChannelRecord['channelType'];
  const visibility = raw.visibility === 'private' ? 'private' : 'public';
  const joinPolicy = raw.joinPolicy === 'approval' || raw.joinPolicy === 'invite' || raw.joinPolicy === 'closed' ? raw.joinPolicy : 'open';
  const postingPolicy = raw.postingPolicy === 'operators' ? 'operators' : 'members';
  return {
    id: string(raw.id, string(raw.channelId)), channelType, category: string(raw.category), name: string(raw.name, '未命名频道'),
    avatarUrl: string(raw.avatarUrl), ownerId: string(raw.ownerId), parentId: string(raw.parentId), description: string(raw.description),
    visibility, joinPolicy, postingPolicy, slowModeSeconds: number(raw.slowModeSeconds), memberCount: number(raw.memberCount),
    ban: boolean(raw.ban), disband: boolean(raw.disband), sendBan: boolean(raw.sendBan), allowStranger: boolean(raw.allowStranger),
    metadata: object(raw.metadata), createdAt: formatDate(raw.createdAt), updatedAt: formatDate(raw.updatedAt),
  };
}

function adaptBusinessChannelMember(value: unknown): BusinessChannelMemberRecord {
  const raw = object(value);
  return {
    channelId: string(raw.channelId), userId: string(raw.userId), name: string(raw.name, '未知用户'), handle: string(raw.handle),
    avatarUrl: string(raw.avatarUrl), role: string(raw.role, 'member'), mutedUntil: string(raw.mutedUntil) || undefined,
    expiresAt: string(raw.expiresAt) || undefined, joinedAt: formatDate(raw.joinedAt), updatedAt: formatDate(raw.updatedAt),
  };
}

function adaptBusinessChannelAccess(value: unknown): BusinessChannelAccessRecord {
  const raw = object(value);
  return {
    channelId: string(raw.channelId), userId: string(raw.userId), name: string(raw.name, '未知用户'), handle: string(raw.handle),
    avatarUrl: string(raw.avatarUrl), accessType: raw.accessType === 'allow' ? 'allow' : 'deny', reason: string(raw.reason),
    createdBy: string(raw.createdBy), createdAt: formatDate(raw.createdAt),
  };
}

function adaptSupportSkill(value: unknown): SupportSkillRecord {
  const raw = object(value);
  return {
    id: string(raw.id), name: string(raw.name, '未命名技能组'), description: string(raw.description),
    routingStrategy: raw.routingStrategy === 'round_robin' ? 'round_robin' : 'least_active',
    maxConcurrentPerAgent: number(raw.maxConcurrentPerAgent, 5), enabled: boolean(raw.enabled),
    queueCount: number(raw.queueCount), availableAgents: number(raw.availableAgents),
    createdAt: formatDate(raw.createdAt), updatedAt: formatDate(raw.updatedAt),
  };
}

function adaptSupportAgent(value: unknown): SupportAgentRecord {
  const raw = object(value);
  const rawStatus = string(raw.status);
  const status = (rawStatus === 'available' || rawStatus === 'busy' || rawStatus === 'away' ? rawStatus : 'offline') as SupportAgentRecord['status'];
  return {
    userId: string(raw.userId), name: string(raw.name, '未知坐席'), handle: string(raw.handle), avatarUrl: string(raw.avatarUrl),
    status, maxConcurrent: number(raw.maxConcurrent, 5), activeSessions: number(raw.activeSessions),
    skillGroupIds: list(raw.skillGroupIds).filter((item): item is string => typeof item === 'string'),
    lastAssignedAt: string(raw.lastAssignedAt) || undefined, createdAt: formatDate(raw.createdAt), updatedAt: formatDate(raw.updatedAt),
  };
}

function adaptSupportSession(value: unknown): SupportSessionRecord {
  const raw = object(value);
  const rawStatus = string(raw.status);
  const status = (rawStatus === 'active' || rawStatus === 'transferring' || rawStatus === 'ended' ? rawStatus : 'queued') as SupportSessionRecord['status'];
  return {
    id: string(raw.id), visitorId: string(raw.visitorId), visitorName: string(raw.visitorName, '访客'),
    skillGroupId: string(raw.skillGroupId), skillGroupName: string(raw.skillGroupName), channelId: string(raw.channelId),
    channelType: number(raw.channelType), subject: string(raw.subject), status, queuePosition: number(raw.queuePosition),
    assignedAgentId: string(raw.assignedAgentId), agentName: string(raw.agentName), transferCount: number(raw.transferCount),
    rating: number(raw.rating), ratingComment: string(raw.ratingComment), createdAt: formatDate(raw.createdAt), updatedAt: formatDate(raw.updatedAt),
  };
}

function adaptWukongPlugin(value: unknown): WukongPlugin {
  const raw = object(value);
  return { no: string(raw.no), nodeId: number(raw.node_id, number(raw.nodeId)), name: string(raw.name), version: string(raw.version), status: string(raw.status), methods: list(raw.methods).filter((item): item is string => typeof item === 'string'), priority: number(raw.priority), isAi: flag(raw.is_ai), config: object(raw.config), managed: boolean(raw.managed), verified: boolean(raw.verified), builtIn: boolean(raw.built_in), lifecycleStatus: string(raw.lifecycle_status), fileName: string(raw.file_name), sha256: string(raw.sha256), keyId: string(raw.key_id), installedAt: formatDate(raw.installed_at), updatedAt: formatDate(raw.updated_at) };
}

function adaptWukongPluginRelease(value: unknown): WukongPluginRelease {
  const raw = object(value);
  return { pluginNo: string(raw.pluginNo), nodeId: number(raw.nodeId), name: string(raw.name), fileName: string(raw.fileName), version: string(raw.version), methods: list(raw.methods).filter((item): item is string => typeof item === 'string'), sha256: string(raw.sha256), sizeBytes: number(raw.sizeBytes), keyId: string(raw.keyId), status: string(raw.status), lastActor: string(raw.lastActor), lastReason: string(raw.lastReason), installedAt: formatDate(raw.installedAt), updatedAt: formatDate(raw.updatedAt) };
}

function adaptWukongPluginEvent(value: unknown): WukongPluginEvent {
  const raw = object(value);
  return { id: number(raw.ID, number(raw.id)), pluginNo: string(raw.PluginNo, string(raw.pluginNo)), action: string(raw.Action, string(raw.action)), status: string(raw.Status, string(raw.status)), actor: string(raw.Actor, string(raw.actor)), reason: string(raw.Reason, string(raw.reason)), details: object(raw.Details ?? raw.details), createdAt: formatDate(raw.CreatedAt ?? raw.createdAt) };
}

function adaptWukongPluginLogEntry(value: unknown): WukongPluginLogEntry {
  const raw = object(value);
  return { sequence: number(raw.sequence), stream: string(raw.stream), timestamp: number(raw.timestamp), message: string(raw.message) };
}

function adaptLiveKitRoom(value: unknown): LiveKitRoom {
  const raw = object(value);
  return { sid: string(raw.sid), name: string(raw.name), createdAt: formatDate(raw.createdAt), participantCount: number(raw.participantCount), publisherCount: number(raw.publisherCount), maxParticipants: number(raw.maxParticipants), activeRecording: boolean(raw.activeRecording) };
}

function adaptLiveKitParticipant(value: unknown): LiveKitParticipant {
  const raw = object(value); const tracks = list(raw.tracks).map(object);
  return { sid: string(raw.sid), identity: string(raw.identity), name: string(raw.name), state: string(raw.state), joinedAt: formatDate(raw.joinedAt), trackCount: tracks.length, screenSharing: tracks.some((track) => string(track.source).startsWith('SCREEN_SHARE')) };
}

function adaptLiveKitMetrics(value: unknown): LiveKitMetrics {
  const raw = object(value);
  return {
    healthy: boolean(raw.healthy), activeRooms: number(raw.activeRooms), activeParticipants: number(raw.activeParticipants),
    cpuPercent: number(raw.cpuPercent), residentMemoryBytes: number(raw.residentMemoryBytes),
    networkReceiveBytesPerSecond: number(raw.networkReceiveBytesPerSecond), networkTransmitBytesPerSecond: number(raw.networkTransmitBytesPerSecond),
    packetLossPercent: number(raw.packetLossPercent), participantJoinsLastHour: number(raw.participantJoinsLastHour), roomsCompletedLastHour: number(raw.roomsCompletedLastHour),
    sampledAt: string(raw.sampledAt),
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
  const userIds = (value: unknown) => list(value).filter((id): id is string => typeof id === 'string' && Boolean(id));
  return {
    id: string(raw.id), conversationId: string(raw.conversationId), kind: raw.kind === 'group' ? 'group' : 'direct',
    callerId: string(raw.callerId), calleeId: string(raw.calleeId), participantIds: userIds(raw.participantIds),
    joinedUserIds: userIds(raw.joinedUserIds), declinedUserIds: userIds(raw.declinedUserIds), leftUserIds: userIds(raw.leftUserIds),
    mediaType: raw.mediaType === 'video' ? 'video' : 'audio', status: string(raw.status, 'unknown'), endReason: string(raw.endReason),
    endedBy: string(raw.endedBy),
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
  const users = optionalNumber(raw.users);
  const bannedUsers = optionalNumber(raw.bannedUsers);
  const messages = optionalNumber(raw.messages);
  const reports = optionalNumber(raw.pendingReports);
  const sockets = optionalNumber(raw.wukongConnections);
  const conversations = optionalNumber(raw.conversations);
  const messageTrend = list(raw.messageTrend).map((value) => {
    const point = object(value);
    const rawTime = string(point.time);
    const parsed = new Date(rawTime);
    return {
      time: rawTime && !Number.isNaN(parsed.valueOf())
        ? parsed.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
        : rawTime,
      count: Math.max(0, number(point.count)),
    };
  }).filter((point) => point.time);
  const rawMix = list(raw.channelMix).map((value) => object(value));
  const mixTotal = rawMix.reduce((sum, item) => sum + Math.max(0, number(item.count, number(item.value))), 0);
  const mixLabels: Record<string, { label: string; color: string }> = {
    direct: { label: '单聊', color: 'var(--primary)' },
    group: { label: '群聊', color: 'var(--info)' },
    other: { label: '扩展频道', color: 'var(--warning)' },
  };
  const channelMix = rawMix.map((item) => {
    const kind = string(item.kind, 'other');
    const presentation = mixLabels[kind] ?? mixLabels.other;
    const amount = Math.max(0, number(item.count, number(item.value)));
    return {
      label: string(item.label, presentation.label),
      value: mixTotal > 0 ? Math.round((amount * 1000) / mixTotal) / 10 : 0,
      color: string(item.color, presentation.color),
    };
  }).filter((item) => item.value > 0);
  const alerts: DashboardData['alerts'] = list(raw.alerts) as DashboardData['alerts'];
  if (!alerts.length && reports !== null && reports > 0) alerts.push({
    id: 'pending-reports', title: '存在待审举报', detail: `${reports} 条举报等待处理`, severity: reports >= 10 ? 'critical' : 'warning', time: '现在',
  });
  if (raw.wukongStatus !== 'ok') alerts.push({
    id: 'wukong-unavailable', title: 'WuKongIM 状态不可用', detail: '请检查节点连接和服务健康状态', severity: 'critical', time: '现在',
  });
  return {
    metrics: [
      { label: '用户总数', value: users?.toLocaleString() ?? '—', delta: bannedUsers === null ? '封禁账号数未上报' : `${bannedUsers} 个封禁账号`, tone: users === null ? 'warning' : 'info' },
      { label: '累计消息', value: messages?.toLocaleString() ?? '—', delta: messages === null ? '消息总数未上报' : '服务端持久化统计', tone: messages === null ? 'warning' : 'success' },
      { label: '待审举报', value: reports?.toLocaleString() ?? '—', delta: reports === null ? '举报队列未上报' : reports > 0 ? '请及时处理' : '当前队列为空', tone: reports === null || reports > 0 ? 'warning' : 'success' },
      { label: 'WuKong 连接', value: sockets?.toLocaleString() ?? '—', delta: raw.wukongStatus !== 'ok' ? 'WuKong 状态不可用' : conversations === null ? '业务会话数未上报' : `${conversations} 个业务会话`, tone: raw.wukongStatus === 'ok' && sockets !== null ? 'info' : 'warning' },
    ],
    messageTrend, channelMix, alerts, activity: list(raw.activity).map(adaptAudit),
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
    configurationStatus: { database: boolean(status.database), redis: boolean(status.redis), objectStorage: boolean(status.objectStorage), otpProvider: boolean(status.otpProvider), pushProvider: boolean(status.pushProvider), liveKit: boolean(status.liveKit), adminTOTP: boolean(status.adminTOTP) },
    infrastructure: { pushProvider: string(infrastructure.pushProvider, 'noop'), mediaMaxSizeMB: number(infrastructure.mediaMaxSizeMB, 100), callInviteTimeoutSeconds: number(infrastructure.callInviteTimeoutSeconds, 30), accessTokenMinutes: number(infrastructure.accessTokenMinutes, 15), refreshTokenHours: number(infrastructure.refreshTokenHours, 720) },
    restartRequiredKeys: list(raw.restartRequiredKeys).filter((key): key is string => typeof key === 'string'),
  };
}

const baseUrl = import.meta.env.VITE_ADMIN_API_URL ?? '/api/v2/admin';
const jsonRequestTimeoutMs = 20_000;
const uploadRequestTimeoutMs = 120_000;

async function request(path: string, token: string, init?: RequestInit, emitUnauthorized = true): Promise<unknown> {
  const formBody = typeof FormData !== 'undefined' && init?.body instanceof FormData;
  const timeoutController = new AbortController();
  const upstreamSignal = init?.signal;
  const forwardAbort = () => timeoutController.abort(upstreamSignal?.reason);
  if (upstreamSignal?.aborted) forwardAbort();
  else upstreamSignal?.addEventListener('abort', forwardAbort, { once: true });
  const timeout = window.setTimeout(() => timeoutController.abort('request-timeout'), formBody ? uploadRequestTimeoutMs : jsonRequestTimeoutMs);
  let response: Response;
  try {
    response = await fetch(`${baseUrl}${path}`, {
      ...init,
      cache: 'no-store',
      credentials: 'same-origin',
      signal: timeoutController.signal,
      headers: { ...(formBody ? {} : { 'Content-Type': 'application/json' }), ...(token ? { Authorization: `Bearer ${token}` } : {}), ...init?.headers },
    });
  } catch (cause) {
    if (timeoutController.signal.aborted && !upstreamSignal?.aborted) {
      throw new ApiError('请求超时，请检查网络或服务状态后重试', 0, 'REQUEST_TIMEOUT');
    }
    throw cause;
  } finally {
    window.clearTimeout(timeout);
    upstreamSignal?.removeEventListener('abort', forwardAbort);
  }
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
    throw new ApiError(localizedApiErrorMessage(code, message, response.status), response.status, code, requestId);
  }
  if (response.status === 204) return undefined;
  return response.json();
}

function adaptWukongRobotProfile(value: unknown): WukongRobotProfile {
  const raw = object(value);
  return {
    userId: string(raw.userId), name: string(raw.name), username: string(raw.username),
    placeholder: string(raw.placeholder), enabled: boolean(raw.enabled), inlineOn: boolean(raw.inlineOn),
    version: number(raw.version), menus: list(raw.menus).map((value) => {
      const menu = object(value);
      return { cmd: string(menu.cmd), remark: string(menu.remark), type: 'command' as const };
    }),
    updatedBy: string(raw.updatedBy), reason: string(raw.reason), updatedAt: formatDate(raw.updatedAt),
  };
}

function adaptUserOverview(value: unknown): UserOverview {
  const raw = object(value);
  const userRaw = object(raw.user);
  const user = adaptUser({
    ...userRaw,
    deviceCount: number(raw.deviceCount, number(userRaw.deviceCount)),
  });
  return {
    user,
    signature: string(userRaw.signature),
    gender: userRaw.gender === 'male' || userRaw.gender === 'female' ? userRaw.gender : 'unspecified',
    deviceCount: number(raw.deviceCount, user.deviceCount),
    friendCount: number(raw.friendCount),
    groupCount: number(raw.groupCount),
    handleChangesUsed: number(raw.handleChangesUsed, user.handleChangeCount),
    handleChangesRemaining: number(raw.handleChangesRemaining, Math.max(0, 2 - user.handleChangeCount)),
  };
}

function adaptGroupOverview(value: unknown): GroupOverview {
  const raw = object(value);
  return {
    id: string(raw.id, 'unknown'),
    title: string(raw.title, '未命名群组'),
    ownerId: string(raw.ownerId, '暂无'),
    announcement: string(raw.announcement),
    announcementVersion: number(raw.announcementVersion),
    joinPolicy: string(raw.joinPolicy, 'approval'),
    allowMemberAddFriend: boolean(raw.allowMemberAddFriend),
    messageCount: number(raw.messageCount),
    memberCount: number(raw.memberCount),
  };
}

function adaptGroupMember(value: unknown): GroupMemberRecord {
  const raw = object(value);
  return {
    conversationId: string(raw.conversationId),
    userId: string(raw.userId, string(raw.id, 'unknown')),
    name: string(raw.name, '未命名用户'),
    handle: string(raw.handle, '未设置'),
    avatarUrl: string(raw.avatarUrl),
    role: string(raw.role, 'member'),
    mutedUntil: string(raw.mutedUntil) || undefined,
    lastReadSeq: number(raw.lastReadSeq),
    lastDeliveredSeq: number(raw.lastDeliveredSeq),
    groupNickname: string(raw.groupNickname),
    joinedAt: formatDate(raw.joinedAt),
  };
}

function liveApi(token: string): AdminApi {
  return {
    async getDashboard() { return adaptDashboard(await request('/dashboard', token)); },
    async getUsers(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/users?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptUser, page, pageSize); },
    async getUserOverview(id) { return adaptUserOverview(await request(`/users/${encodeURIComponent(id)}`, token)); },
    async getUserFriends(id) { return unwrapItems(await request(`/users/${encodeURIComponent(id)}/friends`, token)).items.map(adaptUser); },
    async getUserBlockedUsers(id) { return unwrapItems(await request(`/users/${encodeURIComponent(id)}/blocks`, token)).items.map(adaptUser); },
    async getUserDevices(id) { return unwrapItems(await request(`/users/${encodeURIComponent(id)}/devices`, token)).items.map(adaptAdminUserDevice); },
    async sendUserSystemMessage(id, content, reason) {
      const raw = object(await request(`/users/${encodeURIComponent(id)}/system-message`, token, { method: 'POST', body: JSON.stringify({ content, reason, confirmed: true }) }));
      return { targetUid: string(raw.targetUid), senderUid: string(raw.senderUid), conversationId: string(raw.conversationId), messageId: String(raw.messageId ?? ''), clientMsgNo: string(raw.clientMsgNo) } satisfies AdminSystemMessageResult;
    },
    async banUser(id, reason, durationHours) { await request(`/users/${encodeURIComponent(id)}/ban`, token, { method: 'POST', body: JSON.stringify({ reason, durationHours, confirmed: true }) }); },
    async unbanUser(id, reason) { await request(`/users/${encodeURIComponent(id)}/unban`, token, { method: 'POST', body: JSON.stringify({ reason, confirmed: true }) }); },
    async getGroups(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/groups?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptGroup, page, pageSize); },
    async getGroupOverview(id) { return adaptGroupOverview(await request(`/groups/${encodeURIComponent(id)}`, token)); },
    async getGroupMembers(id, q = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/groups/${encodeURIComponent(id)}/members?q=${encodeURIComponent(q)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptGroupMember, page, pageSize); },
    async updateGroupMember(id, userId, update, reason) { await request(`/groups/${encodeURIComponent(id)}/members/${encodeURIComponent(userId)}`, token, { method: 'PATCH', body: JSON.stringify({ ...update, reason, confirmed: true }) }); },
    async removeGroupMember(id, userId, reason) { await request(`/groups/${encodeURIComponent(id)}/members/${encodeURIComponent(userId)}`, token, { method: 'DELETE', body: JSON.stringify({ reason, confirmed: true }) }); },
    async disbandGroup(id, reason) { await request(`/groups/${encodeURIComponent(id)}/disband`, token, { method: 'POST', body: JSON.stringify({ reason, confirmed: true }) }); },
    async getReports(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/reports?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptReport, page, pageSize); },
    async resolveReport(id, action, note) { const raw = object(await request(`/reports/${encodeURIComponent(id)}/resolve`, token, { method: 'POST', body: JSON.stringify({ action, reason: note, confirmed: true }) })); const status = raw.status === 'rejected' ? 'rejected' : 'resolved'; const responseAction = string(raw.action, action); return { status, action: reportResolutionActions.includes(responseAction as ReportResolutionAction) ? responseAction as ReportResolutionAction : action } satisfies ReportResolutionResult; },
    async getSensitiveWords() { const source = unwrapItems(await request('/sensitive-words', token)); return source.items.map((value) => { const raw = object(value); const word = string(raw.word); return { id: string(raw.id), word, category: string(raw.category, '其他'), matchType: 'exact' as const, action: 'block' as const, createdAt: formatDate(raw.createdAt) }; }); },
    async addSensitiveWord(input, reason) { const raw = object(await request('/sensitive-words', token, { method: 'POST', body: JSON.stringify({ word: input.word, category: input.category, reason, confirmed: true }) })); return { ...input, matchType: 'exact', action: 'block', id: string(raw.id), word: string(raw.word, input.word), category: string(raw.category, input.category), createdAt: formatDate(raw.createdAt ?? new Date().toISOString()) }; },
    async deleteSensitiveWord(id, reason) { await request(`/sensitive-words/${encodeURIComponent(id)}`, token, { method: 'DELETE', body: JSON.stringify({ reason, confirmed: true }) }); },
    async getHealth() { const payload = await request('/health', token); const source = unwrapItems(payload); if (source.items.length) return source.items.map((value) => { const raw = object(value); const status = raw.status === 'healthy' || raw.status === 'down' || raw.status === 'degraded' ? raw.status : 'unknown'; return { name: string(raw.name, '服务'), status, latency: number(raw.latency), uptime: string(raw.uptime, '暂无'), version: string(raw.version, '暂无'), detail: string(raw.detail, status === 'unknown' ? '服务未返回健康状态' : '服务已响应') }; }); const raw = object(payload); const status = raw.status === 'ok' ? 'healthy' : raw.status ? 'down' : 'unknown'; return [{ name: string(raw.service, 'IM API'), status, latency: 0, uptime: typeof raw.uptimeSeconds === 'number' ? `${Math.floor(raw.uptimeSeconds / 60)} 分钟` : '暂无', version: '暂无', detail: status === 'healthy' ? 'API 进程运行正常' : status === 'unknown' ? '健康接口未返回有效状态' : 'API 返回异常状态' }]; },
    async getAuditLogs(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/audit-logs?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptAudit, page, pageSize); },
    async getMessages(q = '', type = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/messages?q=${encodeURIComponent(q)}&type=${encodeURIComponent(type)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptMessage, page, pageSize); },
    async getMedia(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/media?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptMedia, page, pageSize); },
    async getOnline() { return unwrapItems(await request('/online', token)).items.map(adaptOnline); },
    async getFriendships(q='',page=1,pageSize=20,cursor=''){const payload=await request(`/friendships?q=${encodeURIComponent(q)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`,token);return serverPage(payload,adaptFriendship,page,pageSize);},
    async getFeedback(q='',category='',page=1,pageSize=20,cursor=''){const payload=await request(`/feedback?q=${encodeURIComponent(q)}&category=${encodeURIComponent(category)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`,token);return serverPage(payload,adaptFeedback,page,pageSize);},
    async getOperationsStatus(){const [push,backups,diagnostics,tasks,access]=await Promise.all([request('/push',token),request('/backups',token),request('/client-diagnostics?limit=20',token),request('/tasks',token),request('/access',token)]);return {push:object(push) as OperationsStatus['push'],backups:adaptBackupStatus(backups),diagnostics:adaptClientDiagnostics(diagnostics),tasks:object(object(tasks).tasks),access:object(access) as unknown as OperationsStatus['access']};},
    async getAnnouncements(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/announcements?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptAnnouncement, page, pageSize); },
    async createAnnouncement(input, reason) { return adaptAnnouncement(await request('/announcements', token, { method: 'POST', body: JSON.stringify({ ...input, reason, confirmed: true }) })); },
    async updateAnnouncement(id, input, reason) { return adaptAnnouncement(await request(`/announcements/${encodeURIComponent(id)}`, token, { method: 'PUT', body: JSON.stringify({ ...input, reason, confirmed: true }) })); },
    async publishAnnouncement(id, enqueuePush, reason) { return adaptAnnouncement(await request(`/announcements/${encodeURIComponent(id)}/publish`, token, { method: 'POST', body: JSON.stringify({ enqueuePush, reason, confirmed: true }) })); },
    async withdrawAnnouncement(id, reason) { return adaptAnnouncement(await request(`/announcements/${encodeURIComponent(id)}/withdraw`, token, { method: 'POST', body: JSON.stringify({ reason, confirmed: true }) })); },
    async deleteAnnouncement(id, reason) { await request(`/announcements/${encodeURIComponent(id)}`, token, { method: 'DELETE', body: JSON.stringify({ reason, confirmed: true }) }); },
    async getCalls(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/calls?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptCall, page, pageSize); },
    async getSettings() { return adaptSettings(await request('/settings', token)); },
    async updateSettings(input, reason) { const { configurationStatus: _configurationStatus, infrastructure: _infrastructure, restartRequiredKeys: _restartRequiredKeys, ...runtime } = input; const payload = await request('/settings', token, { method: 'PUT', body: JSON.stringify({ ...runtime, registrationEnabled: input.allowRegistration, reason, confirmed: true }) }); return adaptSettings(payload); },
    async getClientVersions() { const source = unwrapItems(await request('/client-versions', token)); return source.items.map(adaptClientVersion); },
    async getClientVersionHistory(platform: ClientPlatform, page = 1, pageSize = 20, cursor = '') {
      try {
        const payload = await request(`/client-versions/${encodeURIComponent(platform)}/history?cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token);
        return serverPage(payload, adaptClientVersionRelease, page, pageSize);
      } catch (cause) {
        if (!(cause instanceof ApiError) || cause.status !== 404) throw cause;
        const source = unwrapItems(await request('/audit-logs?q=client_version_policy.updated&status=success&limit=100', token));
        const records = source.items
          .map((value) => object(value))
          .filter((value) => value.action === 'client_version_policy.updated' && value.targetId === platform)
          .map(adaptClientVersionRelease);
        return localPageResult(records, page, pageSize);
      }
    },
    async updateClientVersion(input, reason) { return adaptClientVersion(await request(`/client-versions/${encodeURIComponent(input.platform)}`, token, { method: 'PUT', body: JSON.stringify({ minimumVersion: input.minimumVersion, latestVersion: input.latestVersion, forceUpdate: input.forceUpdate, rolloutPercentage: input.rolloutPercentage, releaseNotes: input.releaseNotes, downloadUrl: input.downloadUrl, reason, confirmed: true }) })); },
    async getModerationMoments(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/moments?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptModerationMoment, page, pageSize); },
    async moderateMoment(id, status, reason) { await request(`/moments/${encodeURIComponent(id)}/moderate`, token, { method: 'POST', body: JSON.stringify({ status, reason, confirmed: true }) }); },
    async getModerationStickerPacks(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/sticker-packs?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptModerationStickerPack, page, pageSize); },
    async reviewStickerPack(id, status, reason) { return adaptModerationStickerPack(await request(`/sticker-packs/${encodeURIComponent(id)}/review`, token, { method: 'POST', body: JSON.stringify({ status, reason, confirmed: true }) })); },
    async getStickerCategories() { return unwrapItems(await request('/sticker-categories', token)).items.map(adaptStickerCategory); },
    async saveStickerCategory(input: StickerCategoryInput, reason) { const path = input.id ? `/sticker-categories/${encodeURIComponent(input.id)}` : '/sticker-categories'; const method = input.id ? 'PUT' : 'POST'; return adaptStickerCategory(unwrapItem(await request(path, token, { method, body: JSON.stringify({ ...input, reason, confirmed: true }) }))); },
    async saveStickerPack(input: StickerPackInput, reason) { const path = input.id ? `/sticker-packs/${encodeURIComponent(input.id)}` : '/sticker-packs'; const method = input.id ? 'PUT' : 'POST'; return adaptModerationStickerPack(unwrapItem(await request(path, token, { method, body: JSON.stringify({ ...input, reason, confirmed: true }) }))); },
    async saveStickerItem(packId, input: StickerItemInput, reason) { const path = input.id ? `/sticker-packs/${encodeURIComponent(packId)}/items/${encodeURIComponent(input.id)}` : `/sticker-packs/${encodeURIComponent(packId)}/items`; const method = input.id ? 'PUT' : 'POST'; return adaptStickerItem(unwrapItem(await request(path, token, { method, body: JSON.stringify({ ...input, reason, confirmed: true }) }))); },
    async getWukongOverview() { return adaptWukongOverview(await request('/wukong/overview', token)); },
    async getWukongSettings() { return adaptWukongSettings(await request('/wukong/settings', token)); },
    async getWukongNodes() { return managerData(await request('/wukong/nodes', token)).map(adaptWukongNode); },
    async getWukongConnections(uid = '', page = 1, pageSize = 20) { const payload = object(await request(`/wukong/connections?uid=${encodeURIComponent(uid)}&page=${page}&limit=${pageSize}`, token)); const items = list(payload.connections).map(adaptWukongConnection); const total = number(payload.total, items.length); return { items, page, pageSize, total, hasNext: page * pageSize < total }; },
    async getWukongChannels(channelId = '', channelType = 0, limit = 50) { return managerData(await request(`/wukong/channels?channelId=${encodeURIComponent(channelId)}&channelType=${channelType}&limit=${limit}`, token)).map(adaptWukongChannel); },
    async getWukongMessages(channelId = '', channelType = 0, fromUid = '', limit = 50) { return managerData(await request(`/wukong/messages?channelId=${encodeURIComponent(channelId)}&channelType=${channelType}&fromUid=${encodeURIComponent(fromUid)}&limit=${limit}`, token)).map(adaptWukongMessage); },
    async getWukongDevices(uid = '', deviceFlag = -1, limit = 50) { return managerData(await request(`/wukong/devices?uid=${encodeURIComponent(uid)}&deviceFlag=${deviceFlag}&limit=${limit}`, token)).map(adaptWukongDevice); },
    async quitWukongDevice(uid, deviceFlag, reason) { await request(`/wukong/devices/${encodeURIComponent(uid)}/quit`, token, { method: 'POST', body: JSON.stringify({ deviceFlag, reason, confirmed: true }) }); },
    async getWukongSystemUsers() { return unwrapItems(await request('/wukong/system-users', token)).items.map(adaptWukongSystemUser); },
    async setWukongSystemUser(uid, enabled, reason) { return adaptWukongSystemUser(unwrapItem(await request(`/wukong/system-users/${encodeURIComponent(uid)}`, token, { method: 'PUT', body: JSON.stringify({ enabled, reason, confirmed: true }) }))); },
    async getWukongRobots() { return unwrapItems(await request('/wukong/robots', token)).items.map(adaptWukongRobotProfile); },
    async setWukongRobot(uid, input, reason) { return adaptWukongRobotProfile(unwrapItem(await request(`/wukong/robots/${encodeURIComponent(uid)}`, token, { method: 'PUT', body: JSON.stringify({ ...input, reason, confirmed: true }) }))); },
    async getWukongPlugins(nodeId = 0) { return unwrapItems(await request(`/wukong/plugins?nodeId=${nodeId}`, token)).items.map(adaptWukongPlugin); },
    async installWukongPlugin(bundle, manifest, signature, nodeId, reason) { const form = new FormData(); form.set('bundle', bundle); form.set('manifest', await manifest.text()); form.set('signature', signature.trim()); form.set('nodeId', String(nodeId)); form.set('reason', reason); form.set('confirmed', 'true'); return adaptWukongPluginRelease(unwrapItem(await request('/wukong/plugins/install', token, { method: 'POST', body: form }))); },
    async upgradeWukongPlugin(no, bundle, manifest, signature, nodeId, reason) { const form = new FormData(); form.set('bundle', bundle); form.set('manifest', await manifest.text()); form.set('signature', signature.trim()); form.set('nodeId', String(nodeId)); form.set('reason', reason); form.set('confirmed', 'true'); return adaptWukongPluginRelease(unwrapItem(await request(`/wukong/plugins/${encodeURIComponent(no)}/upgrade`, token, { method: 'PUT', body: form }))); },
    async setWukongPluginEnabled(no, nodeId, enabled, reason) { return adaptWukongPluginRelease(unwrapItem(await request(`/wukong/plugins/${encodeURIComponent(no)}/${enabled ? 'enable' : 'disable'}`, token, { method: 'POST', body: JSON.stringify({ nodeId, reason, confirmed: true }) }))); },
    async getWukongPluginEvents(no = '') { return unwrapItems(await request(`/wukong/plugin-events?pluginNo=${encodeURIComponent(no)}&limit=100`, token)).items.map(adaptWukongPluginEvent); },
    async getWukongPluginLogs(no, nodeId = 0, limit = 100) { const payload = object(await request(`/wukong/plugins/${encodeURIComponent(no)}/logs?nodeId=${nodeId}&limit=${limit}`, token)); return list(payload.entries).map(adaptWukongPluginLogEntry); },
    async updateWukongPluginConfig(no, nodeId, config, reason) { await request(`/wukong/plugins/${encodeURIComponent(no)}/config`, token, { method: 'PUT', body: JSON.stringify({ nodeId, config, reason, confirmed: true }) }); },
    async uninstallWukongPlugin(no, nodeId, reason) { await request(`/wukong/plugins/${encodeURIComponent(no)}`, token, { method: 'DELETE', body: JSON.stringify({ nodeId, reason, confirmed: true }) }); },
    async getLiveKitRooms() { return unwrapItems(await request('/livekit/rooms', token)).items.map(adaptLiveKitRoom); },
    async getLiveKitMetrics() { return adaptLiveKitMetrics(await request('/livekit/metrics', token)); },
    async getLiveKitParticipants(room) { return unwrapItems(await request(`/livekit/rooms/${encodeURIComponent(room)}/participants`, token)).items.map(adaptLiveKitParticipant); },
    async removeLiveKitParticipant(room, identity, reason) { await request(`/livekit/rooms/${encodeURIComponent(room)}/participants/${encodeURIComponent(identity)}`, token, { method: 'DELETE', body: JSON.stringify({ reason, confirmed: true }) }); },
    async deleteLiveKitRoom(room, reason) { await request(`/livekit/rooms/${encodeURIComponent(room)}`, token, { method: 'DELETE', body: JSON.stringify({ reason, confirmed: true }) }); },
    async getBusinessChannels(q = '', channelType = 0, category = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/channels?q=${encodeURIComponent(q)}&channelType=${channelType}&category=${encodeURIComponent(category)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptBusinessChannel, page, pageSize); },
    async createBusinessChannel(input, reason) { return adaptBusinessChannel(unwrapItem(await request('/channels', token, { method: 'POST', body: JSON.stringify({ ...input, reason, confirmed: true }) }))); },
    async updateBusinessChannel(id, channelType, update, reason) { return adaptBusinessChannel(unwrapItem(await request(`/channels/${encodeURIComponent(id)}?channelType=${channelType}`, token, { method: 'PATCH', body: JSON.stringify({ ...update, reason, confirmed: true }) }))); },
    async getBusinessChannelMembers(id, channelType, cursor = '') { const source = unwrapItems(await request(`/channels/${encodeURIComponent(id)}/members?channelType=${channelType}&cursor=${encodeURIComponent(cursor)}&limit=200`, token)); return { items: source.items.map(adaptBusinessChannelMember), nextCursor: source.nextCursor }; },
    async addBusinessChannelMember(id, channelType, userId, expiresAt, reason) { await request(`/channels/${encodeURIComponent(id)}/members/${encodeURIComponent(userId)}?channelType=${channelType}`, token, { method: 'PUT', body: JSON.stringify({ expiresAt, reason, confirmed: true }) }); },
    async updateBusinessChannelMember(id, channelType, userId, update, reason) { await request(`/channels/${encodeURIComponent(id)}/members/${encodeURIComponent(userId)}?channelType=${channelType}`, token, { method: 'PATCH', body: JSON.stringify({ ...update, reason, confirmed: true }) }); },
    async removeBusinessChannelMember(id, channelType, userId, reason) { await request(`/channels/${encodeURIComponent(id)}/members/${encodeURIComponent(userId)}?channelType=${channelType}`, token, { method: 'DELETE', body: JSON.stringify({ reason, confirmed: true }) }); },
    async getBusinessChannelAccess(id, channelType) { return unwrapItems(await request(`/channels/${encodeURIComponent(id)}/access?channelType=${channelType}&limit=200`, token)).items.map(adaptBusinessChannelAccess); },
    async setBusinessChannelAccess(id, channelType, userId, accessType, enabled, reason) { await request(`/channels/${encodeURIComponent(id)}/access/${accessType}/${encodeURIComponent(userId)}?channelType=${channelType}`, token, { method: enabled ? 'PUT' : 'DELETE', body: JSON.stringify({ reason, confirmed: true }) }); },
    async getSupportSkills() { return unwrapItems(await request('/support/skills', token)).items.map(adaptSupportSkill); },
    async saveSupportSkill(input, reason) { const path = input.id ? `/support/skills/${encodeURIComponent(input.id)}` : '/support/skills'; return adaptSupportSkill(unwrapItem(await request(path, token, { method: input.id ? 'PUT' : 'POST', body: JSON.stringify({ ...input, reason, confirmed: true }) }))); },
    async getSupportAgents(skillGroupId = '') { return unwrapItems(await request(`/support/agents?skillGroupId=${encodeURIComponent(skillGroupId)}`, token)).items.map(adaptSupportAgent); },
    async saveSupportAgent(userId, input, reason) { return adaptSupportAgent(unwrapItem(await request(`/support/agents/${encodeURIComponent(userId)}`, token, { method: 'PUT', body: JSON.stringify({ ...input, reason, confirmed: true }) }))); },
    async getSupportSessions(q = '', status = '', skillGroupId = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/support/sessions?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&skillGroupId=${encodeURIComponent(skillGroupId)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptSupportSession, page, pageSize); },
    async claimSupportSession(id, agentId, reason) { return adaptSupportSession(unwrapItem(await request(`/support/sessions/${encodeURIComponent(id)}/claim`, token, { method: 'POST', body: JSON.stringify({ agentId, reason, confirmed: true }) }))); },
    async transferSupportSession(id, targetAgentId, reason) { return adaptSupportSession(unwrapItem(await request(`/support/sessions/${encodeURIComponent(id)}/transfer`, token, { method: 'POST', body: JSON.stringify({ targetAgentId, reason, confirmed: true }) }))); },
    async endSupportSession(id, reason) { return adaptSupportSession(unwrapItem(await request(`/support/sessions/${encodeURIComponent(id)}/end`, token, { method: 'POST', body: JSON.stringify({ reason, confirmed: true }) }))); },
  };
}

export function getApi(token = ''): AdminApi {
  return liveApi(token);
}

function adaptRole(value: unknown): AdminRole {
  if (value === 'platform_admin' || value === 'super_admin' || value === 'admin') return 'platform_admin';
  if (value === 'system_operator') return 'system_operator';
  if (value === 'moderator') return 'moderator';
  if (value === 'content_operator') return 'content_operator';
  if (value === 'support_agent') return 'support_agent';
  return 'support';
}

export async function loginAdmin(email: string, password: string, totp = ''): Promise<AdminSession> {
  let response: unknown;
  try {
    response = await request('/auth/login', '', {
      method: 'POST',
      body: JSON.stringify({ email, password, ...(totp ? { totp } : {}) }),
    }, false);
  } catch (cause) {
    // 登录请求本身没有现有管理员会话。部分旧服务端仍把凭据错误
    // 返回为 UNAUTHENTICATED；这里不能把它误报成“会话已失效”。
    if (cause instanceof ApiError && cause.status === 401 && cause.code !== 'TOTP_REQUIRED' && cause.code !== 'INVALID_TOTP') {
      throw new ApiError('邮箱、密码或动态验证码不正确', 401, 'INVALID_CREDENTIALS', cause.requestId);
    }
    throw cause;
  }
  const payload = object(response);
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
