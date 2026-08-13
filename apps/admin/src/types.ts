export type DataMode = 'demo' | 'live';
export type AdminRole = 'platform_admin' | 'system_operator' | 'moderator' | 'content_operator' | 'support_agent' | 'support';
export type ClientPlatform = 'android' | 'ios' | 'web' | 'macos';
export type StatusTone = 'success' | 'warning' | 'danger' | 'info' | 'neutral';

export interface AdminSession {
  token: string;
  displayName: string;
  role: AdminRole;
  expiresAt: number;
}

export interface PageResult<T> {
  items: T[];
  page: number;
  pageSize: number;
  total: number;
  hasNext: boolean;
  nextCursor?: string;
}

export type ReportResolutionAction = 'dismiss' | 'no_violation' | 'delete_message' | 'ban_user';

export interface ReportResolutionResult {
  status: 'resolved' | 'rejected';
  action: ReportResolutionAction;
}

export interface DashboardData {
  metrics: Array<{ label: string; value: string; delta: string; tone: StatusTone }>;
  messageTrend: Array<{ time: string; count: number }>;
  channelMix: Array<{ label: string; value: number; color: string }>;
  alerts: Array<{ id: string; title: string; detail: string; severity: 'warning' | 'critical'; time: string }>;
  activity: AuditLog[];
}

export interface UserRecord {
  id: string;
  nickname: string;
  phone: string;
  handle: string;
  handleChangeCount: number;
  bannedUntil?: string;
  avatar: string;
  status: 'active' | 'banned' | 'risk';
  registeredAt: string;
  lastSeen: string;
  deviceCount: number;
  messageCount: number;
}

export interface GroupRecord {
  id: string;
  name: string;
  owner: string;
  memberCount: number;
  messageCount: number;
  status: 'active' | 'muted' | 'dissolved';
  createdAt: string;
  reportCount: number;
}

export interface ReportRecord {
  id: string;
  target: string;
  targetType: 'user' | 'group' | 'message';
  reporter: string;
  category: string;
  excerpt: string;
  status: 'pending' | 'reviewing' | 'resolved' | 'rejected';
  risk: 'low' | 'medium' | 'high';
  createdAt: string;
}

export interface SensitiveWord {
  id: string;
  word: string;
  category: string;
  matchType: 'exact' | 'fuzzy';
  action: 'review' | 'block';
  createdAt: string;
}

export interface HealthService {
  name: string;
  status: 'healthy' | 'degraded' | 'down';
  latency: number;
  uptime: string;
  version: string;
  detail: string;
}

export interface AuditLog {
  id: string;
  actor: string;
  action: string;
  target: string;
  result: 'success' | 'failed';
  ip: string;
  createdAt: string;
}

export interface MessageRecord {
  id: string;
  clientMsgId: string;
  conversationId: string;
  senderId: string;
  conversationSeq: number;
  type: string;
  preview: string;
  recalled: boolean;
  recalledAt?: string;
  expiresAt?: string;
  expiredAt?: string;
  editedAt?: string;
  editVersion: number;
  createdAt: string;
}

export interface MediaRecord {
  id: string;
  ownerId: string;
  objectKey: string;
  mime: string;
  status: string;
  size: number;
  checksum: string;
}

export interface OnlineRecord {
  userId: string;
  connections: number;
}
export interface FriendshipRecord { userId: string; friendUserId: string; userName: string; friendName: string; createdAt: string; updatedAt: string; }
export interface FeedbackRecord { id: string; userId: string; userName: string; category: string; content: string; contact: string; createdAt: string; }
export interface OperationsStatus { push: { providers: Array<{ provider: string; activeDevices: number; disabledDevices: number }>; queue: Array<{ status: string; count: number; attempts: number }> }; tasks: Record<string, unknown>; access: { current: { id: string; role: AdminRole }; administrators: Array<{ id: string; role: AdminRole; source: string; mutable: boolean }>; roles: Array<{ id: AdminRole; permissions: string[] }>; note: string } }

export interface AnnouncementRecord {
  id: string;
  title: string;
  content: string;
  status: 'draft' | 'scheduled' | 'published' | 'withdrawn';
  pinned: boolean;
  targetType: 'all' | 'users';
  targetUserIds: string[];
  scheduledAt?: string;
  publishedAt?: string;
  pushOnPublish: boolean;
  createdBy: string;
  createdAt: string;
}

export type AnnouncementInput = Pick<AnnouncementRecord, 'title' | 'content' | 'status' | 'pinned' | 'targetType' | 'targetUserIds' | 'scheduledAt' | 'pushOnPublish'>;

export interface CallRecord {
  id: string;
  conversationId: string;
  kind: 'direct' | 'group';
  callerId: string;
  calleeId: string;
  participantIds: string[];
  joinedUserIds: string[];
  declinedUserIds: string[];
  leftUserIds: string[];
  mediaType: 'audio' | 'video';
  status: string;
  endReason: string;
  endedBy: string;
  invitedAt: string;
  acceptedAt?: string;
  endedAt?: string;
  durationSeconds: number;
}

export interface AdminSettings {
  allowRegistration: boolean;
  passwordMinLength: number;
  maxMessageTextLength: number;
  messageRecallMinutes: number;
  maxGroupMembers: number;
  allowFriendRequests: boolean;
  allowSearchByHandle: boolean;
  allowSearchByPhone: boolean;
  friendRequestExpiryDays: number;
  announcementPushEnabled: boolean;
  callsEnabled: boolean;
  videoCallsEnabled: boolean;
  sensitiveWordEnabled: boolean;
  reportSlaHours: number;
  maintenanceMode: boolean;
  announcement: string;
  configurationStatus: Record<'database' | 'redis' | 'objectStorage' | 'otpProvider' | 'pushProvider' | 'liveKit' | 'adminTOTP', boolean>;
  infrastructure: {
    pushProvider: string;
    mediaMaxSizeMB: number;
    callInviteTimeoutSeconds: number;
    accessTokenMinutes: number;
    refreshTokenHours: number;
  };
  restartRequiredKeys: string[];
}

export interface ClientVersionPolicy {
  platform: ClientPlatform;
  minimumVersion: string;
  latestVersion: string;
  forceUpdate: boolean;
  rolloutPercentage: number;
  releaseNotes: string;
  downloadUrl: string;
  updatedBy: string;
  updatedAt: string;
}

export interface MomentModerationRecord {
  id: string;
  authorId: string;
  authorName: string;
  content: string;
  mediaKind: string;
  mediaCount: number;
  visibility: string;
  likeCount: number;
  commentCount: number;
  status: 'published' | 'hidden' | 'deleted';
  createdAt: string;
}

export interface StickerPackModerationRecord {
  id: string;
  name: string;
  categoryName: string;
  description: string;
  status: 'draft' | 'reviewing' | 'published' | 'rejected' | 'disabled';
  itemCount: number;
  createdBy: string;
  reviewedBy: string;
  reviewReason: string;
  updatedAt: string;
  categoryId?: string;
  coverMediaId?: string;
  sortOrder?: number;
  items?: StickerItemOperationsRecord[];
}

export interface StickerCategoryOperationsRecord {
  id: string;
  name: string;
  sortOrder: number;
  enabled: boolean;
}

export interface StickerItemOperationsRecord {
  id: string;
  packId: string;
  name: string;
  mediaId: string;
  emoji: string;
  sortOrder: number;
  status: 'published' | 'disabled';
}

export interface StickerCategoryInput {
  id?: string;
  name: string;
  sortOrder: number;
  enabled: boolean;
}

export interface StickerPackInput {
  id?: string;
  categoryId: string;
  name: string;
  description: string;
  coverMediaId: string;
  status: 'draft' | 'reviewing';
  sortOrder: number;
}

export interface StickerItemInput {
  id?: string;
  name: string;
  mediaId: string;
  emoji: string;
  status: 'published' | 'disabled';
  sortOrder: number;
}

export interface WukongOverview {
  serverId: string;
  version: string;
  uptime: string;
  connections: number;
  userHandlers: number;
  cpu: number;
  memoryBytes: number;
  goroutines: number;
  inMessages: number;
  outMessages: number;
  retryQueue: number;
}

export interface WukongRuntimeSettings {
  traceEnabled: boolean;
  lokiEnabled: boolean;
  prometheusEnabled: boolean;
  stressEnabled: boolean;
}

export interface WukongNode {
  id: number;
  online: boolean;
  leader: boolean;
  apiAddress: string;
  version: string;
  slotCount: number;
  slotLeaderCount: number;
}

export interface WukongConnection {
  id: number;
  uid: string;
  ip: string;
  device: string;
  deviceId: string;
  nodeId: number;
  lastActivity: string;
  inMessages: number;
  outMessages: number;
}

export interface WukongChannel {
  channelId: string;
  channelType: number;
  subscriberCount: number;
  denylistCount: number;
  allowlistCount: number;
  banned: boolean;
  disbanded: boolean;
  createdAt: number;
}

export interface WukongStoredMessage {
  messageId: string;
  messageSeq: number;
  clientMsgNo: string;
  fromUid: string;
  channelId: string;
  channelType: number;
  timestamp: number;
}

export interface WukongDevice {
  uid: string;
  deviceFlag: number;
  deviceLevel: number;
  tokenPresent: boolean;
  createdAt: number;
  updatedAt: number;
}

export interface WukongSystemUser {
  userId: string;
  name: string;
  enabled: boolean;
  syncStatus: 'pending' | 'processing' | 'synced' | 'failed';
  updatedBy: string;
  reason: string;
  updatedAt: string;
}

export interface WukongPlugin {
  no: string;
  nodeId: number;
  name: string;
  version: string;
  status: string;
  methods: string[];
  priority: number;
  isAi: boolean;
  config: Record<string, unknown>;
  managed: boolean;
  verified: boolean;
  builtIn: boolean;
  lifecycleStatus: string;
  fileName: string;
  sha256: string;
  keyId: string;
  installedAt: string;
  updatedAt: string;
}

export interface WukongPluginRelease {
  pluginNo: string;
  nodeId: number;
  name: string;
  fileName: string;
  version: string;
  methods: string[];
  sha256: string;
  sizeBytes: number;
  keyId: string;
  status: string;
  lastActor: string;
  lastReason: string;
  installedAt: string;
  updatedAt: string;
}

export interface WukongPluginEvent {
  id: number;
  pluginNo: string;
  action: string;
  status: string;
  actor: string;
  reason: string;
  details: Record<string, unknown>;
	createdAt: string;
}

export interface WukongPluginLogEntry {
  sequence: number;
  stream: 'stdout' | 'stderr' | string;
  timestamp: number;
  message: string;
}

export interface LiveKitRoom {
  sid: string;
  name: string;
  createdAt: string;
  participantCount: number;
  publisherCount: number;
  maxParticipants: number;
  activeRecording: boolean;
}

export interface LiveKitParticipant {
  sid: string;
  identity: string;
  name: string;
  state: string;
  joinedAt: string;
  trackCount: number;
  screenSharing: boolean;
}

export interface LiveKitMetrics {
  healthy: boolean;
  activeRooms: number;
  activeParticipants: number;
  cpuPercent: number;
  residentMemoryBytes: number;
  networkReceiveBytesPerSecond: number;
  networkTransmitBytesPerSecond: number;
  packetLossPercent: number;
  participantJoinsLastHour: number;
  roomsCompletedLastHour: number;
  sampledAt: string;
}

export interface BusinessChannelRecord {
  id: string;
  channelType: 4 | 5 | 6 | 9;
  category: string;
  name: string;
  avatarUrl: string;
  ownerId: string;
  parentId: string;
  description: string;
  visibility: 'public' | 'private';
  joinPolicy: 'open' | 'approval' | 'invite' | 'closed';
  postingPolicy: 'members' | 'operators';
  slowModeSeconds: number;
  memberCount: number;
  ban: boolean;
  disband: boolean;
  sendBan: boolean;
  allowStranger: boolean;
  metadata: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
}

export interface BusinessChannelMemberRecord {
  channelId: string;
  userId: string;
  name: string;
  handle: string;
  avatarUrl: string;
  role: string;
  mutedUntil?: string;
  expiresAt?: string;
  joinedAt: string;
  updatedAt: string;
}

export interface BusinessChannelAccessRecord {
  channelId: string;
  userId: string;
  name: string;
  handle: string;
  avatarUrl: string;
  accessType: 'allow' | 'deny';
  reason: string;
  createdBy: string;
  createdAt: string;
}

export interface BusinessChannelInput {
  ownerId: string;
  channelType: 4 | 5 | 6 | 9;
  name: string;
  avatarUrl?: string;
  parentId?: string;
  description?: string;
  visibility: 'public' | 'private';
  joinPolicy: 'open' | 'approval' | 'invite' | 'closed';
  postingPolicy: 'members' | 'operators';
  slowModeSeconds: number;
  metadata?: Record<string, unknown>;
}

export interface SupportSkillRecord {
  id: string;
  name: string;
  description: string;
  routingStrategy: 'least_active' | 'round_robin';
  maxConcurrentPerAgent: number;
  enabled: boolean;
  queueCount: number;
  availableAgents: number;
  createdAt: string;
  updatedAt: string;
}

export interface SupportAgentRecord {
  userId: string;
  name: string;
  handle: string;
  avatarUrl: string;
  status: 'offline' | 'available' | 'busy' | 'away';
  maxConcurrent: number;
  activeSessions: number;
  skillGroupIds: string[];
  lastAssignedAt?: string;
  createdAt: string;
  updatedAt: string;
}

export interface SupportSessionRecord {
  id: string;
  visitorId: string;
  visitorName: string;
  skillGroupId: string;
  skillGroupName: string;
  channelId: string;
  channelType: number;
  subject: string;
  status: 'queued' | 'active' | 'transferring' | 'ended';
  queuePosition: number;
  assignedAgentId: string;
  agentName: string;
  transferCount: number;
  rating: number;
  ratingComment: string;
  createdAt: string;
  updatedAt: string;
}

export interface AdminApi {
  getDashboard(): Promise<DashboardData>;
  getUsers(query?: string, status?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<UserRecord>>;
  banUser(id: string, reason: string, durationHours: number): Promise<void>;
  unbanUser(id: string, reason: string): Promise<void>;
  getGroups(query?: string, status?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<GroupRecord>>;
  disbandGroup(id: string, reason: string): Promise<void>;
  getReports(query?: string, status?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<ReportRecord>>;
  resolveReport(id: string, action: ReportResolutionAction, note: string): Promise<ReportResolutionResult>;
  getSensitiveWords(): Promise<SensitiveWord[]>;
  addSensitiveWord(input: Omit<SensitiveWord, 'id' | 'createdAt'>, reason: string): Promise<SensitiveWord>;
  deleteSensitiveWord(id: string, reason: string): Promise<void>;
  getHealth(): Promise<HealthService[]>;
  getAuditLogs(query?: string, status?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<AuditLog>>;
  getMessages(query?: string, type?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<MessageRecord>>;
  getMedia(query?: string, status?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<MediaRecord>>;
  getOnline(): Promise<OnlineRecord[]>;
  getFriendships(query?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<FriendshipRecord>>;
  getFeedback(query?: string, category?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<FeedbackRecord>>;
  getOperationsStatus(): Promise<OperationsStatus>;
  getAnnouncements(query?: string, status?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<AnnouncementRecord>>;
  createAnnouncement(input: AnnouncementInput, reason: string): Promise<AnnouncementRecord>;
  updateAnnouncement(id: string, input: AnnouncementInput, reason: string): Promise<AnnouncementRecord>;
  publishAnnouncement(id: string, enqueuePush: boolean, reason: string): Promise<AnnouncementRecord>;
  withdrawAnnouncement(id: string, reason: string): Promise<AnnouncementRecord>;
  deleteAnnouncement(id: string, reason: string): Promise<void>;
  getCalls(query?: string, status?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<CallRecord>>;
  getSettings(): Promise<AdminSettings>;
  updateSettings(settings: AdminSettings, reason: string): Promise<AdminSettings>;
  getClientVersions(): Promise<ClientVersionPolicy[]>;
  updateClientVersion(policy: ClientVersionPolicy, reason: string): Promise<ClientVersionPolicy>;
  getModerationMoments(query?: string, status?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<MomentModerationRecord>>;
  moderateMoment(id: string, status: MomentModerationRecord['status'], reason: string): Promise<void>;
  getModerationStickerPacks(query?: string, status?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<StickerPackModerationRecord>>;
  reviewStickerPack(id: string, status: StickerPackModerationRecord['status'], reason: string): Promise<StickerPackModerationRecord>;
  getStickerCategories(): Promise<StickerCategoryOperationsRecord[]>;
  saveStickerCategory(input: StickerCategoryInput, reason: string): Promise<StickerCategoryOperationsRecord>;
  saveStickerPack(input: StickerPackInput, reason: string): Promise<StickerPackModerationRecord>;
  saveStickerItem(packId: string, input: StickerItemInput, reason: string): Promise<StickerItemOperationsRecord>;
  getWukongOverview(): Promise<WukongOverview>;
  getWukongSettings(): Promise<WukongRuntimeSettings>;
  getWukongNodes(): Promise<WukongNode[]>;
  getWukongConnections(uid?: string, page?: number, pageSize?: number): Promise<PageResult<WukongConnection>>;
  getWukongChannels(channelId?: string, channelType?: number, limit?: number): Promise<WukongChannel[]>;
  getWukongMessages(channelId?: string, channelType?: number, fromUid?: string, limit?: number): Promise<WukongStoredMessage[]>;
  getWukongDevices(uid?: string, deviceFlag?: number, limit?: number): Promise<WukongDevice[]>;
  quitWukongDevice(uid: string, deviceFlag: number, reason: string): Promise<void>;
  getWukongSystemUsers(): Promise<WukongSystemUser[]>;
  setWukongSystemUser(uid: string, enabled: boolean, reason: string): Promise<WukongSystemUser>;
  getWukongPlugins(nodeId?: number): Promise<WukongPlugin[]>;
  installWukongPlugin(bundle: File, manifest: File, signature: string, nodeId: number, reason: string): Promise<WukongPluginRelease>;
  upgradeWukongPlugin(no: string, bundle: File, manifest: File, signature: string, nodeId: number, reason: string): Promise<WukongPluginRelease>;
  setWukongPluginEnabled(no: string, nodeId: number, enabled: boolean, reason: string): Promise<WukongPluginRelease>;
  getWukongPluginEvents(no?: string): Promise<WukongPluginEvent[]>;
  getWukongPluginLogs(no: string, nodeId?: number, limit?: number): Promise<WukongPluginLogEntry[]>;
  updateWukongPluginConfig(no: string, nodeId: number, config: Record<string, unknown>, reason: string): Promise<void>;
  uninstallWukongPlugin(no: string, nodeId: number, reason: string): Promise<void>;
  getLiveKitRooms(): Promise<LiveKitRoom[]>;
  getLiveKitMetrics(): Promise<LiveKitMetrics>;
  getLiveKitParticipants(room: string): Promise<LiveKitParticipant[]>;
  removeLiveKitParticipant(room: string, identity: string, reason: string): Promise<void>;
  deleteLiveKitRoom(room: string, reason: string): Promise<void>;
  getBusinessChannels(query?: string, channelType?: number, category?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<BusinessChannelRecord>>;
  createBusinessChannel(input: BusinessChannelInput, reason: string): Promise<BusinessChannelRecord>;
  updateBusinessChannel(id: string, channelType: number, update: Partial<BusinessChannelRecord>, reason: string): Promise<BusinessChannelRecord>;
  getBusinessChannelMembers(id: string, channelType: number, cursor?: string): Promise<{ items: BusinessChannelMemberRecord[]; nextCursor?: string }>;
  addBusinessChannelMember(id: string, channelType: number, userId: string, expiresAt: string | undefined, reason: string): Promise<void>;
  updateBusinessChannelMember(id: string, channelType: number, userId: string, update: { role?: string; mutedUntil?: string; clearMute?: boolean; expiresAt?: string; clearExpiry?: boolean }, reason: string): Promise<void>;
  removeBusinessChannelMember(id: string, channelType: number, userId: string, reason: string): Promise<void>;
  getBusinessChannelAccess(id: string, channelType: number): Promise<BusinessChannelAccessRecord[]>;
  setBusinessChannelAccess(id: string, channelType: number, userId: string, accessType: 'allow' | 'deny', enabled: boolean, reason: string): Promise<void>;
  getSupportSkills(): Promise<SupportSkillRecord[]>;
  saveSupportSkill(input: Partial<SupportSkillRecord> & Pick<SupportSkillRecord, 'name' | 'routingStrategy' | 'maxConcurrentPerAgent' | 'enabled'>, reason: string): Promise<SupportSkillRecord>;
  getSupportAgents(skillGroupId?: string): Promise<SupportAgentRecord[]>;
  saveSupportAgent(userId: string, input: Pick<SupportAgentRecord, 'status' | 'maxConcurrent' | 'skillGroupIds'>, reason: string): Promise<SupportAgentRecord>;
  getSupportSessions(query?: string, status?: string, skillGroupId?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<SupportSessionRecord>>;
  claimSupportSession(id: string, agentId: string, reason: string): Promise<SupportSessionRecord>;
  transferSupportSession(id: string, targetAgentId: string, reason: string): Promise<SupportSessionRecord>;
  endSupportSession(id: string, reason: string): Promise<SupportSessionRecord>;
}
