import type {
  AdminApi,
  AnnouncementInput,
  AnnouncementRecord,
  AdminRole,
  AdminSession,
  AdminSettings,
  AuditLog,
  CallRecord,
  ClientVersionPolicy,
  DashboardData,
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
  WukongOverview,
  WukongRuntimeSettings,
  WukongNode,
  WukongConnection,
  WukongChannel,
  WukongStoredMessage,
  WukongDevice,
  WukongSystemUser,
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

function unwrapItem(payload: unknown): unknown {
  const root = object(payload);
  const data = object(root.data);
  return root.item ?? data.item ?? data.data ?? root.data ?? payload;
}

function adaptClientVersion(value: unknown): ClientVersionPolicy {
  const outer = object(value);
  const nested = object(outer.data);
  const raw = Object.keys(nested).length ? nested : outer;
  const platform = raw.platform === 'ios' || raw.platform === 'web' || raw.platform === 'macos' ? raw.platform : 'android';
  return {
    platform,
    minimumVersion: string(raw.minimumVersion, '1.0.0'),
    latestVersion: string(raw.latestVersion, '1.0.0'),
    forceUpdate: boolean(raw.forceUpdate),
    rolloutPercentage: Math.min(100, Math.max(0, number(raw.rolloutPercentage, 100))),
    releaseNotes: string(raw.releaseNotes),
    downloadUrl: string(raw.downloadUrl),
    updatedBy: string(raw.updatedBy),
    updatedAt: string(raw.updatedAt),
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
const managerData = (payload: unknown) => {
  const raw = object(payload);
  return list(Array.isArray(raw.data) ? raw.data : raw.items);
};

function adaptWukongOverview(value: unknown): WukongOverview {
  const raw = object(value);
  return {
    serverId: string(raw.server_id), version: string(raw.version), uptime: string(raw.uptime), connections: number(raw.connections),
    userHandlers: number(raw.user_handler_count), cpu: number(raw.cpu), memoryBytes: number(raw.mem), goroutines: number(raw.goroutine),
    inMessages: number(raw.in_msgs), outMessages: number(raw.out_msgs), retryQueue: number(raw.retry_queue),
  };
}

function adaptWukongSettings(value: unknown): WukongRuntimeSettings {
  const raw = object(value);
  const logger = object(raw.logger);
  return {
    traceEnabled: flag(logger.trace_on), lokiEnabled: flag(logger.loki_on),
    prometheusEnabled: flag(raw.prometheus_on), stressEnabled: flag(raw.stress_on),
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
  const users = number(raw.users);
  const messages = number(raw.messages);
  const reports = number(raw.pendingReports);
  const sockets = number(raw.wukongConnections);
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
  if (!alerts.length && reports > 0) alerts.push({
    id: 'pending-reports', title: '存在待审举报', detail: `${reports} 条举报等待处理`, severity: reports >= 10 ? 'critical' : 'warning', time: '现在',
  });
  if (raw.wukongStatus !== 'ok') alerts.push({
    id: 'wukong-unavailable', title: 'WuKongIM 状态不可用', detail: '请检查节点连接和服务健康状态', severity: 'critical', time: '现在',
  });
  return {
    metrics: [
      { label: '用户总数', value: users.toLocaleString(), delta: `${number(raw.bannedUsers)} 个封禁账号`, tone: 'info' },
      { label: '累计消息', value: messages.toLocaleString(), delta: '服务端持久化统计', tone: 'success' },
      { label: '待审举报', value: reports.toLocaleString(), delta: reports > 0 ? '请及时处理' : '当前队列为空', tone: reports > 0 ? 'warning' : 'success' },
      { label: 'WuKong 连接', value: sockets.toLocaleString(), delta: raw.wukongStatus === 'ok' ? `${number(raw.conversations)} 个业务会话` : 'WuKong 状态不可用', tone: raw.wukongStatus === 'ok' ? 'info' : 'warning' },
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
  { name: 'WuKongIM 长连接', status: 'healthy', latency: 18, uptime: '99.998%', version: 'v2.2.5', detail: '固定版本探针正常' },
  { name: '消息服务', status: 'healthy', latency: 24, uptime: '99.995%', version: 'v0.1.0', detail: '4/4 实例正常' },
  { name: '业务事件与命令', status: 'degraded', latency: 86, uptime: '99.940%', version: 'WuKongIM CMD', detail: '命令投递延迟偏高' },
];

let settings: AdminSettings = { allowRegistration: true, passwordMinLength: 8, maxMessageTextLength: 5000, messageRecallMinutes: 2, maxGroupMembers: 500, allowFriendRequests: true, allowSearchByHandle: true, allowSearchByPhone: false, friendRequestExpiryDays: 7, announcementPushEnabled: true, callsEnabled: true, videoCallsEnabled: true, sensitiveWordEnabled: true, reportSlaHours: 8, maintenanceMode: false, announcement: '', configurationStatus: { database: true, redis: true, objectStorage: true, otpProvider: true, pushProvider: true, liveKit: true, adminTOTP: true }, infrastructure: { pushProvider: 'getui', mediaMaxSizeMB: 100, callInviteTimeoutSeconds: 30, accessTokenMinutes: 15, refreshTokenHours: 720 }, restartRequiredKeys: ['pushProvider', 'mediaMaxSizeMB', 'callInviteTimeoutSeconds', 'accessTokenMinutes', 'refreshTokenHours'] };
let announcements: AnnouncementRecord[] = [];
let clientVersions: ClientVersionPolicy[] = (['android', 'ios', 'web', 'macos'] as const).map((platform) => ({ platform, minimumVersion: '1.0.0', latestVersion: '1.0.0', forceUpdate: false, rolloutPercentage: 100, releaseNotes: '', downloadUrl: '', updatedBy: 'demo_admin', updatedAt: new Date().toISOString() }));
let moderationMoments: MomentModerationRecord[] = [{ id: 'moment_demo', authorId: 'u_10134', authorName: '林知夏', content: '周末社区活动记录', mediaKind: 'images', mediaCount: 3, visibility: 'friends', likeCount: 12, commentCount: 4, status: 'published', createdAt: '今天 10:20' }];
let stickerCategories: StickerCategoryOperationsRecord[] = [{ id: 'daily', name: '日常', sortOrder: 10, enabled: true }];
let moderationStickerPacks: StickerPackModerationRecord[] = [{ id: 'sticker_pack_demo', name: '邻里日常', categoryId: 'daily', categoryName: '日常', description: '社区日常表情', coverMediaId: 'media_demo_cover', status: 'reviewing', sortOrder: 10, itemCount: 0, items: [], createdBy: 'u_10826', reviewedBy: '', reviewReason: '', updatedAt: '今天 09:30' }];
let businessChannels: BusinessChannelRecord[] = [{ id: 'community_demo', channelType: 4, category: 'community', name: '产品交流社区', avatarUrl: '', ownerId: 'u_demo', parentId: '', description: '产品使用和建议交流', visibility: 'public', joinPolicy: 'open', postingPolicy: 'members', slowModeSeconds: 0, memberCount: 126, ban: false, disband: false, sendBan: false, allowStranger: false, metadata: {}, createdAt: '今天 08:00', updatedAt: '刚刚' }];
const businessChannelMembers: BusinessChannelMemberRecord[] = [{ channelId: 'community_demo', userId: 'u_demo', name: '演示用户', handle: 'demo', avatarUrl: '', role: 'owner', joinedAt: '今天 08:00', updatedAt: '今天 08:00' }];
const businessChannelAccess: BusinessChannelAccessRecord[] = [];
let supportSkills: SupportSkillRecord[] = [{ id: 'support_general', name: '综合咨询', description: '通用咨询队列', routingStrategy: 'least_active', maxConcurrentPerAgent: 5, enabled: true, queueCount: 2, availableAgents: 1, createdAt: '今天 08:00', updatedAt: '刚刚' }];
let supportAgents: SupportAgentRecord[] = [{ userId: 'u_support', name: '演示坐席', handle: 'support', avatarUrl: '', status: 'available', maxConcurrent: 5, activeSessions: 1, skillGroupIds: ['support_general'], createdAt: '今天 08:00', updatedAt: '刚刚' }];
let supportSessions: SupportSessionRecord[] = [{ id: 'support_session_demo', visitorId: 'u_visitor', visitorName: '访客 A', skillGroupId: 'support_general', skillGroupName: '综合咨询', channelId: 'u_visitor', channelType: 10, subject: '账号登录问题', status: 'active', queuePosition: 0, assignedAgentId: 'u_support', agentName: '演示坐席', transferCount: 0, rating: 0, ratingComment: '', createdAt: '5 分钟前', updatedAt: '刚刚' }];
let wukongSystemUsers: WukongSystemUser[] = [{ userId: 'u_notice', name: '系统通知', enabled: true, syncStatus: 'synced', updatedBy: 'demo', reason: '初始化系统通知账号', updatedAt: new Date().toISOString() }];

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
      alerts: [{ id: 'al_1', title: '业务命令投递延迟偏高', detail: '请检查业务 Outbox 与 WuKongIM CMD 投递状态', severity: 'warning', time: '3 分钟前' }],
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
  async getOperationsStatus(){await wait();return {push:{providers:[{provider:'getui',activeDevices:8342,disabledDevices:126},{provider:'apns_voip',activeDevices:6128,disabledDevices:84}],queue:[{status:'pending',count:18,attempts:20},{status:'sent',count:42861,attempts:43102}]},backups:{configured:true,available:true,status:'healthy',lastStatus:true,running:false,lastDurationSeconds:37,incompleteGenerations:0,offsiteEnabled:false,lastAttemptAt:'2026-08-13T02:00:00Z',lastSuccessAt:'2026-08-13T02:00:00Z'},diagnostics:{summary:{windowHours:24,crashes:0,connectionFailures:1,callFailures:0,performanceSamples:12,performanceP95Ms:1380},items:[{id:'diag_demo',userId:'u_10134',kind:'performance',name:'app_start',fingerprint:'a'.repeat(64),platform:'android',appVersion:'1.0.0+1',durationMs:920,occurredAt:'2026-08-13T02:00:00Z'}]},tasks:{scheduledMessages:{pending:7,processing:0,failed:0},messageExpiry:{waiting:124},mediaCleanup:{status:'healthy',lastRun:'2026-08-01 01:45'},wukongOutbox:{pending:0,processing:0,failed:0,oldestPendingSeconds:0,lastCompletedAt:'2026-08-13T00:00:00Z',reconcilePending:0,reconcileCompleted:12,reconcileFailed:0},wukongWebhook:{pending:0,processing:0,failed:0,oldestPendingSeconds:0,lastCompletedAt:'2026-08-13T00:00:00Z'}},access:{current:{id:'demo',role:'platform_admin'},administrators:[{id:'demo',role:'platform_admin',source:'演示环境',mutable:false}],roles:[{id:'platform_admin',permissions:['全部管理权限']},{id:'system_operator',permissions:['系统运维','版本策略']},{id:'moderator',permissions:['用户处置','举报处置','内容审核']},{id:'content_operator',permissions:['内容运营','公告']},{id:'support_agent',permissions:['客服工作台']},{id:'support',permissions:['只读']}],note:'角色权限由服务端强制执行，管理员凭据不会在页面回显。'}} satisfies OperationsStatus;},
  async getAnnouncements(query = '', status = '', page = 1, pageSize = 20) { await wait(); const needle = query.toLowerCase(); return demoPageResult(announcements.filter((item) => (!needle || `${item.id}${item.title}${item.content}`.toLowerCase().includes(needle)) && (!status || item.status === status)), page, pageSize); },
  async createAnnouncement(input: AnnouncementInput) { await wait(); const item: AnnouncementRecord = { ...input, id: `announcement_${Date.now()}`, createdBy: 'demo_admin', createdAt: new Date().toISOString() }; announcements = [item, ...announcements]; return item; },
  async updateAnnouncement(id, input) { await wait(); const item = announcements.find((value) => value.id === id); if (!item) throw new ApiError('公告不存在', 404, 'NOT_FOUND'); Object.assign(item, input); return { ...item }; },
  async publishAnnouncement(id, enqueuePush) { await wait(); const item = announcements.find((value) => value.id === id); if (!item) throw new ApiError('公告不存在', 404, 'NOT_FOUND'); item.status = 'published'; item.pushOnPublish = enqueuePush; item.publishedAt = new Date().toISOString(); item.scheduledAt = undefined; return { ...item }; },
  async withdrawAnnouncement(id) { await wait(); const item = announcements.find((value) => value.id === id); if (!item) throw new ApiError('公告不存在', 404, 'NOT_FOUND'); item.status = 'withdrawn'; return { ...item }; },
  async deleteAnnouncement(id) { await wait(); announcements = announcements.filter((value) => value.id !== id); },
  async getCalls(_query = '', _status = '', page = 1, pageSize = 20) { await wait(); return demoPageResult<CallRecord>([], page, pageSize); },
  async getSettings() { await wait(); return { ...settings }; },
  async updateSettings(next) { await wait(); settings = { ...next }; return { ...settings }; },
  async getClientVersions() { await wait(); return clientVersions.map((item) => ({ ...item })); },
  async updateClientVersion(next) { await wait(); const updated = { ...next, updatedBy: 'demo_admin', updatedAt: new Date().toISOString() }; clientVersions = clientVersions.filter((item) => item.platform !== next.platform); clientVersions.push(updated); return { ...updated }; },
  async getModerationMoments(query = '', status = '', page = 1, pageSize = 20) { await wait(); const needle = query.toLowerCase(); return demoPageResult(moderationMoments.filter((item) => (!needle || `${item.id}${item.authorId}${item.authorName}${item.content}`.toLowerCase().includes(needle)) && (!status || item.status === status)), page, pageSize); },
  async moderateMoment(id, status) { await wait(); const item = moderationMoments.find((value) => value.id === id); if (item) item.status = status; },
  async getModerationStickerPacks(query = '', status = '', page = 1, pageSize = 20) { await wait(); const needle = query.toLowerCase(); return demoPageResult(moderationStickerPacks.filter((item) => (!needle || `${item.id}${item.name}${item.categoryName}${item.createdBy}`.toLowerCase().includes(needle)) && (!status || item.status === status)), page, pageSize); },
  async reviewStickerPack(id, status, reason) { await wait(); const item = moderationStickerPacks.find((value) => value.id === id); if (!item) throw new ApiError('表情包不存在', 404, 'NOT_FOUND'); item.status = status; item.reviewReason = reason; item.reviewedBy = 'demo_admin'; return { ...item }; },
  async getStickerCategories() { await wait(); return stickerCategories.map((item) => ({ ...item })); },
  async saveStickerCategory(input) { await wait(); const item = { id: input.id ?? `category_${Date.now()}`, name: input.name, sortOrder: input.sortOrder, enabled: input.enabled }; stickerCategories = [item, ...stickerCategories.filter((value) => value.id !== item.id)]; return { ...item }; },
  async saveStickerPack(input) { await wait(); const existing = moderationStickerPacks.find((value) => value.id === input.id); const item: StickerPackModerationRecord = { id: input.id ?? `pack_${Date.now()}`, name: input.name, categoryId: input.categoryId, categoryName: stickerCategories.find((value) => value.id === input.categoryId)?.name ?? input.categoryId, description: input.description, coverMediaId: input.coverMediaId, status: input.status, sortOrder: input.sortOrder, itemCount: existing?.itemCount ?? 0, items: existing?.items ?? [], createdBy: existing?.createdBy ?? 'demo_admin', reviewedBy: '', reviewReason: '', updatedAt: new Date().toISOString() }; moderationStickerPacks = [item, ...moderationStickerPacks.filter((value) => value.id !== item.id)]; return { ...item }; },
  async saveStickerItem(packId, input) { await wait(); const pack = moderationStickerPacks.find((value) => value.id === packId); if (!pack) throw new ApiError('表情包不存在', 404, 'NOT_FOUND'); const item: StickerItemOperationsRecord = { id: input.id ?? `sticker_${Date.now()}`, packId, name: input.name, mediaId: input.mediaId, emoji: input.emoji, sortOrder: input.sortOrder, status: input.status }; pack.items = [...(pack.items ?? []).filter((value) => value.id !== item.id), item]; pack.itemCount = pack.items.length; return item; },
  async getWukongOverview() { await wait(); return { serverId: '1', version: 'v2.2.5-20260422', uptime: '2h16m', connections: 126, userHandlers: 113, cpu: 12.4, memoryBytes: 512 * 1024 * 1024, goroutines: 284, inMessages: 128420, outMessages: 256801, retryQueue: 0 }; },
  async getWukongSettings() { await wait(); return { traceEnabled: false, lokiEnabled: false, prometheusEnabled: true, stressEnabled: false }; },
  async getWukongNodes() { await wait(); return [{ id: 1, online: true, leader: true, apiAddress: 'internal', version: 'v2.2.5-20260422', slotCount: 64, slotLeaderCount: 64 }]; },
  async getWukongConnections(uid = '', page = 1, pageSize = 20) { await wait(); const items: WukongConnection[] = [{ id: 1, uid: 'u_demo', ip: '127.0.0.1', device: 'Web', deviceId: 'demo-web', nodeId: 1, lastActivity: '刚刚', inMessages: 18, outMessages: 34 }]; return demoPageResult(items.filter((item) => !uid || item.uid.includes(uid)), page, pageSize); },
  async getWukongChannels() { await wait(); return [{ channelId: 'group_demo', channelType: 2, subscriberCount: 8, denylistCount: 0, allowlistCount: 0, banned: false, disbanded: false, createdAt: Date.now() }]; },
  async getWukongMessages() { await wait(); return [] as WukongStoredMessage[]; },
  async getWukongDevices(uid = '') { await wait(); const items: WukongDevice[] = [{ uid: 'u_demo', deviceFlag: 1, deviceLevel: 1, tokenPresent: true, createdAt: Date.now(), updatedAt: Date.now() }]; return items.filter((item) => !uid || item.uid.includes(uid)); },
  async quitWukongDevice() { await wait(); },
  async getWukongSystemUsers() { await wait(); return [...wukongSystemUsers]; },
  async setWukongSystemUser(uid, enabled, reason) { await wait(); const existing = wukongSystemUsers.find((item) => item.userId === uid); const item: WukongSystemUser = { userId: uid, name: existing?.name ?? uid, enabled, syncStatus: 'pending', updatedBy: 'demo', reason, updatedAt: new Date().toISOString() }; wukongSystemUsers = [item, ...wukongSystemUsers.filter((value) => value.userId !== uid)]; return item; },
  async getWukongPlugins() { await wait(); return [{ no: 'wk.plugin.im-policy', nodeId: 1, name: 'wk.plugin.im-policy-linux-amd64.wkp', version: '1.0.0', status: 'normal', methods: ['Send'], priority: 1, isAi: false, config: { endpoint: 'http://server/internal/wukong/policy/send', secret: '******' }, managed: false, verified: true, builtIn: true, lifecycleStatus: 'active', fileName: 'wk.plugin.im-policy-linux-amd64.wkp', sha256: '', keyId: '', installedAt: '', updatedAt: '' }]; },
  async installWukongPlugin(_bundle, manifest, _signature, nodeId) { await wait(); const raw = JSON.parse(await manifest.text()) as Record<string, unknown>; return { pluginNo: String(raw.pluginNo), nodeId, name: String(raw.name), fileName: String(raw.fileName), version: String(raw.version), methods: Array.isArray(raw.methods) ? raw.methods.map(String) : [], sha256: String(raw.sha256), sizeBytes: Number(raw.size), keyId: String(raw.keyId), status: 'active', lastActor: 'demo', lastReason: 'demo', installedAt: new Date().toISOString(), updatedAt: new Date().toISOString() }; },
  async upgradeWukongPlugin(_no, _bundle, manifest, _signature, nodeId) { await wait(); const raw = JSON.parse(await manifest.text()) as Record<string, unknown>; return { pluginNo: String(raw.pluginNo), nodeId, name: String(raw.name), fileName: String(raw.fileName), version: String(raw.version), methods: Array.isArray(raw.methods) ? raw.methods.map(String) : [], sha256: String(raw.sha256), sizeBytes: Number(raw.size), keyId: String(raw.keyId), status: 'active', lastActor: 'demo', lastReason: 'demo', installedAt: new Date().toISOString(), updatedAt: new Date().toISOString() }; },
  async setWukongPluginEnabled(no, nodeId, enabled) { await wait(); return { pluginNo: no, nodeId, name: no, fileName: `${no}-linux-amd64.wkp`, version: '1.0.0', methods: [], sha256: '', sizeBytes: 0, keyId: '', status: enabled ? 'active' : 'disabled', lastActor: 'demo', lastReason: 'demo', installedAt: '', updatedAt: new Date().toISOString() }; },
  async getWukongPluginEvents() { await wait(); return []; },
  async getWukongPluginLogs() { await wait(); return [{ sequence: 1, stream: 'stdout', timestamp: Date.now(), message: 'policy plugin ready' }]; },
  async updateWukongPluginConfig() { await wait(); },
  async uninstallWukongPlugin() { await wait(); },
  async getLiveKitRooms() { await wait(); return [{ sid: 'RM_demo', name: 'call_demo', createdAt: '刚刚', participantCount: 2, publisherCount: 2, maxParticipants: 9, activeRecording: false }]; },
  async getLiveKitMetrics() { await wait(); return { healthy: true, activeRooms: 1, activeParticipants: 2, cpuPercent: 2.4, residentMemoryBytes: 96 * 1024 * 1024, networkReceiveBytesPerSecond: 2048, networkTransmitBytesPerSecond: 4096, packetLossPercent: 0, participantJoinsLastHour: 8, roomsCompletedLastHour: 3, sampledAt: new Date().toISOString() }; },
  async getLiveKitParticipants() { await wait(); return [{ sid: 'PA_demo', identity: 'u_demo', name: '演示用户', state: 'ACTIVE', joinedAt: '刚刚', trackCount: 2, screenSharing: false }]; },
  async removeLiveKitParticipant() { await wait(); },
  async deleteLiveKitRoom() { await wait(); },
  async getBusinessChannels(query = '', channelType = 0, category = '', page = 1, pageSize = 20) { await wait(); const needle = query.toLowerCase(); return demoPageResult(businessChannels.filter((item) => (!needle || `${item.id}${item.name}${item.ownerId}`.toLowerCase().includes(needle)) && (!channelType || item.channelType === channelType) && (!category || item.category === category)), page, pageSize); },
  async createBusinessChannel(input) { await wait(); const item: BusinessChannelRecord = { id: `${input.channelType === 5 ? `${input.parentId}@topic` : input.channelType === 4 ? 'community' : input.channelType === 6 ? 'info' : 'live'}_${Date.now()}`, category: input.channelType === 4 ? 'community' : input.channelType === 5 ? 'community_topic' : input.channelType === 6 ? 'info' : 'live', name: input.name, avatarUrl: input.avatarUrl ?? '', ownerId: input.ownerId, parentId: input.parentId ?? '', description: input.description ?? '', visibility: input.visibility, joinPolicy: input.joinPolicy, postingPolicy: input.postingPolicy, slowModeSeconds: input.slowModeSeconds, memberCount: 1, ban: false, disband: false, sendBan: false, allowStranger: false, metadata: input.metadata ?? {}, channelType: input.channelType, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }; businessChannels = [item, ...businessChannels]; return { ...item }; },
  async updateBusinessChannel(id, _channelType, update) { await wait(); const item = businessChannels.find((value) => value.id === id); if (!item) throw new ApiError('频道不存在', 404, 'NOT_FOUND'); Object.assign(item, update, { updatedAt: new Date().toISOString() }); return { ...item }; },
  async getBusinessChannelMembers(id) { await wait(); return { items: businessChannelMembers.filter((item) => item.channelId === id) }; },
  async addBusinessChannelMember(id, _channelType, userId, expiresAt) { await wait(); businessChannelMembers.push({ channelId: id, userId, name: userId, handle: '', avatarUrl: '', role: 'member', expiresAt, joinedAt: new Date().toISOString(), updatedAt: new Date().toISOString() }); },
  async updateBusinessChannelMember(id, _channelType, userId, update) { await wait(); const item = businessChannelMembers.find((value) => value.channelId === id && value.userId === userId); if (!item) throw new ApiError('成员不存在', 404, 'NOT_FOUND'); if (update.role) item.role = update.role; if (update.clearMute) item.mutedUntil = undefined; else if (update.mutedUntil) item.mutedUntil = update.mutedUntil; if (update.clearExpiry) item.expiresAt = undefined; else if (update.expiresAt) item.expiresAt = update.expiresAt; },
  async removeBusinessChannelMember(id, _channelType, userId) { await wait(); const index = businessChannelMembers.findIndex((item) => item.channelId === id && item.userId === userId); if (index >= 0) businessChannelMembers.splice(index, 1); },
  async getBusinessChannelAccess(id) { await wait(); return businessChannelAccess.filter((item) => item.channelId === id); },
  async setBusinessChannelAccess(id, _channelType, userId, accessType, enabled, reason) { await wait(); const index = businessChannelAccess.findIndex((item) => item.channelId === id && item.userId === userId); if (index >= 0) businessChannelAccess.splice(index, 1); if (enabled) businessChannelAccess.push({ channelId: id, userId, name: userId, handle: '', avatarUrl: '', accessType, reason, createdBy: 'demo_admin', createdAt: new Date().toISOString() }); },
  async getSupportSkills() { await wait(); return supportSkills.map((item) => ({ ...item })); },
  async saveSupportSkill(input) { await wait(); const item: SupportSkillRecord = { id: input.id ?? `support_skill_${Date.now()}`, name: input.name, description: input.description ?? '', routingStrategy: input.routingStrategy, maxConcurrentPerAgent: input.maxConcurrentPerAgent, enabled: input.enabled, queueCount: 0, availableAgents: 0, createdAt: input.createdAt ?? new Date().toISOString(), updatedAt: new Date().toISOString() }; supportSkills = supportSkills.filter((value) => value.id !== item.id); supportSkills.push(item); return { ...item }; },
  async getSupportAgents(skillGroupId = '') { await wait(); return supportAgents.filter((item) => !skillGroupId || item.skillGroupIds.includes(skillGroupId)).map((item) => ({ ...item })); },
  async saveSupportAgent(userId, input) { await wait(); const current = supportAgents.find((item) => item.userId === userId); const item: SupportAgentRecord = { userId, name: current?.name ?? userId, handle: current?.handle ?? '', avatarUrl: current?.avatarUrl ?? '', status: input.status, maxConcurrent: input.maxConcurrent, activeSessions: current?.activeSessions ?? 0, skillGroupIds: input.skillGroupIds, lastAssignedAt: current?.lastAssignedAt, createdAt: current?.createdAt ?? new Date().toISOString(), updatedAt: new Date().toISOString() }; supportAgents = supportAgents.filter((value) => value.userId !== userId); supportAgents.push(item); return { ...item }; },
  async getSupportSessions(query = '', status = '', skillGroupId = '', page = 1, pageSize = 20) { await wait(); const needle = query.toLowerCase(); return demoPageResult(supportSessions.filter((item) => (!needle || `${item.id}${item.visitorName}${item.subject}`.toLowerCase().includes(needle)) && (!status || item.status === status) && (!skillGroupId || item.skillGroupId === skillGroupId)), page, pageSize); },
  async claimSupportSession(id, agentId) { await wait(); const item = supportSessions.find((value) => value.id === id); if (!item) throw new ApiError('客服会话不存在', 404, 'NOT_FOUND'); item.status = 'active'; item.assignedAgentId = agentId; item.agentName = agentId; return { ...item }; },
  async transferSupportSession(id, targetAgentId) { await wait(); const item = supportSessions.find((value) => value.id === id); if (!item) throw new ApiError('客服会话不存在', 404, 'NOT_FOUND'); item.assignedAgentId = targetAgentId; item.agentName = targetAgentId; item.transferCount += 1; return { ...item }; },
  async endSupportSession(id) { await wait(); const item = supportSessions.find((value) => value.id === id); if (!item) throw new ApiError('客服会话不存在', 404, 'NOT_FOUND'); item.status = 'ended'; return { ...item }; },
};

const baseUrl = import.meta.env.VITE_ADMIN_API_URL ?? '/api/v2/admin';

async function request(path: string, token: string, init?: RequestInit, emitUnauthorized = true): Promise<unknown> {
  const formBody = typeof FormData !== 'undefined' && init?.body instanceof FormData;
  const response = await fetch(`${baseUrl}${path}`, {
    ...init,
    cache: 'no-store',
    credentials: 'same-origin',
    headers: { ...(formBody ? {} : { 'Content-Type': 'application/json' }), ...(token ? { Authorization: `Bearer ${token}` } : {}), ...init?.headers },
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
    async banUser(id, reason, durationHours) { await request(`/users/${encodeURIComponent(id)}/ban`, token, { method: 'POST', body: JSON.stringify({ reason, durationHours, confirmed: true }) }); },
    async unbanUser(id, reason) { await request(`/users/${encodeURIComponent(id)}/unban`, token, { method: 'POST', body: JSON.stringify({ reason, confirmed: true }) }); },
    async getGroups(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/groups?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptGroup, page, pageSize); },
    async disbandGroup(id, reason) { await request(`/groups/${encodeURIComponent(id)}/disband`, token, { method: 'POST', body: JSON.stringify({ reason, confirmed: true }) }); },
    async getReports(q = '', status = '', page = 1, pageSize = 20, cursor = '') { const payload = await request(`/reports?q=${encodeURIComponent(q)}&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}&limit=${pageSize}`, token); return serverPage(payload, adaptReport, page, pageSize); },
    async resolveReport(id, action, note) { const raw = object(await request(`/reports/${encodeURIComponent(id)}/resolve`, token, { method: 'POST', body: JSON.stringify({ action, reason: note, confirmed: true }) })); const status = raw.status === 'rejected' ? 'rejected' : 'resolved'; const responseAction = string(raw.action, action); return { status, action: reportResolutionActions.includes(responseAction as ReportResolutionAction) ? responseAction as ReportResolutionAction : action } satisfies ReportResolutionResult; },
    async getSensitiveWords() { const source = unwrapItems(await request('/sensitive-words', token)); return source.items.map((value) => { const raw = object(value); const word = string(raw.word); return { id: string(raw.id), word, category: string(raw.category, '其他'), matchType: 'exact' as const, action: 'block' as const, createdAt: formatDate(raw.createdAt) }; }); },
    async addSensitiveWord(input, reason) { const raw = object(await request('/sensitive-words', token, { method: 'POST', body: JSON.stringify({ word: input.word, category: input.category, reason, confirmed: true }) })); return { ...input, matchType: 'exact', action: 'block', id: string(raw.id), word: string(raw.word, input.word), category: string(raw.category, input.category), createdAt: formatDate(raw.createdAt ?? new Date().toISOString()) }; },
    async deleteSensitiveWord(id, reason) { await request(`/sensitive-words/${encodeURIComponent(id)}`, token, { method: 'DELETE', body: JSON.stringify({ reason, confirmed: true }) }); },
    async getHealth() { const payload = await request('/health', token); const source = unwrapItems(payload); if (source.items.length) return source.items.map((value) => { const raw = object(value); const status = raw.status === 'down' || raw.status === 'degraded' ? raw.status : 'healthy'; return { name: string(raw.name, '服务'), status, latency: number(raw.latency), uptime: string(raw.uptime, '暂无'), version: string(raw.version, '暂无'), detail: string(raw.detail, '服务已响应') }; }); const raw = object(payload); return [{ name: string(raw.service, 'IM API'), status: raw.status === 'ok' ? 'healthy' : 'down', latency: 0, uptime: typeof raw.uptimeSeconds === 'number' ? `${Math.floor(raw.uptimeSeconds / 60)} 分钟` : '暂无', version: '暂无', detail: raw.status === 'ok' ? 'API 进程运行正常' : 'API 返回异常状态' }]; },
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

export function getApi(mode: 'demo' | 'live', token = ''): AdminApi {
  return demoAllowed && mode === 'demo' ? demoApi : liveApi(token);
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
