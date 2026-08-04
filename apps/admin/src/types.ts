export type DataMode = 'demo' | 'live';
export type AdminRole = 'platform_admin' | 'moderator' | 'support';
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
  callerId: string;
  calleeId: string;
  mediaType: 'audio' | 'video';
  status: string;
  endReason: string;
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
  configurationStatus: Record<'database' | 'redis' | 'objectStorage' | 'otpProvider' | 'pushProvider' | 'turn' | 'adminTOTP', boolean>;
  infrastructure: {
    pushProvider: string;
    mediaMaxSizeMB: number;
    callInviteTimeoutSeconds: number;
    websocketMaxPerUser: number;
    websocketMaxPerIP: number;
    accessTokenMinutes: number;
    refreshTokenHours: number;
  };
  restartRequiredKeys: string[];
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
  addSensitiveWord(input: Omit<SensitiveWord, 'id' | 'createdAt'>): Promise<SensitiveWord>;
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
  createAnnouncement(input: AnnouncementInput): Promise<AnnouncementRecord>;
  updateAnnouncement(id: string, input: AnnouncementInput): Promise<AnnouncementRecord>;
  publishAnnouncement(id: string, enqueuePush: boolean): Promise<AnnouncementRecord>;
  withdrawAnnouncement(id: string): Promise<AnnouncementRecord>;
  deleteAnnouncement(id: string, reason: string): Promise<void>;
  getCalls(query?: string, status?: string, page?: number, pageSize?: number, cursor?: string): Promise<PageResult<CallRecord>>;
  getSettings(): Promise<AdminSettings>;
  updateSettings(settings: AdminSettings): Promise<AdminSettings>;
}
