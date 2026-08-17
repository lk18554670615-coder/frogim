import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from './App';

const session = { token: 'test-admin-jwt', id: 'admin_1', email: 'admin@example.com', displayName: '测试管理员', roleId: 'platform_admin', roleName: '平台管理员', permissions: ['users.write', 'groups.write', 'reports.write', 'rules.write', 'announcements.write', 'settings.write', 'versions.write', 'content.write', 'channels.write', 'operations.write', 'support.write'], expiresAt: Date.now() + 60_000 };

let fixtureStickerCategories: Array<Record<string, unknown>> = [];
let fixtureStickerPacks: Array<Record<string, unknown>> = [];
let fixtureRobots: Array<Record<string, unknown>> = [];

function response(payload: unknown, status = 200) {
  return { ok: status >= 200 && status < 300, status, headers: new Headers(), json: async () => payload };
}

async function liveFixture(input: RequestInfo | URL, init?: RequestInit) {
  const url = String(input);
  const method = init?.method ?? 'GET';
  if (url.includes('/dashboard')) return response({
    metrics: [
      { label: '在线用户', value: '8,429', delta: '较上个时段 +2.4%', tone: 'success' },
      { label: '今日消息', value: '1.82M', delta: '实时统计', tone: 'info' },
      { label: '待审举报', value: '23', delta: '请及时处理', tone: 'warning' },
      { label: '消息成功率', value: '99.993%', delta: '近 24 小时', tone: 'success' },
    ],
    messageTrend: [{ time: '10:00', count: 120 }, { time: '11:00', count: 168 }],
    channelMix: [{ label: '单聊', value: 72, color: 'var(--primary)' }, { label: '群聊', value: 28, color: 'var(--info)' }],
    alerts: [], activity: [],
  });
  if (url.includes('/users/u_10291/friends/u_10288/messages/') && url.endsWith('/recall') && method === 'POST') return response({ recalled: true, conversationId: 'conv_direct_1', conversationSeq: 8 });
  if (url.includes('/users/u_10291/friends/u_10288/messages')) return response({ conversationId: 'conv_direct_1', participants: { u_10291: { id: 'u_10291', name: '林夏' }, u_10288: { id: 'u_10288', name: '江宁' } }, items: [{ id: '88', clientMsgId: 'client-88', conversationId: 'conv_direct_1', conversationSeq: 8, senderId: 'u_10288', sender: { id: 'u_10288', name: '江宁' }, type: 'text', body: { content: '需要管理员检查的消息' }, createdAt: '2026-08-17T08:00:00Z', encrypted: false, deleted: false, adminRecall: false }], nextBeforeSeq: 0 });
  if (url.endsWith('/users/u_10291/friends')) return response({ items: [{ user: { id: 'u_10288', name: '江宁', handle: 'jiangning', phone: '13800001002' }, remark: '产品同事', tags: ['产品'], relationshipCreatedAt: '2026-08-02T08:00:00Z', relationshipUpdatedAt: '2026-08-12T08:00:00Z' }] });
  if (url.includes('/users/u_10291/blocks')) return response({ items: [{ user: { id: 'u_blocked', name: '被屏蔽用户', handle: 'blocked-user' }, remark: '旧备注', blockedAt: '2026-08-03T08:00:00Z' }] });
  if (url.includes('/users/u_10291/devices')) return response({ items: [{ userId: 'u_10291', installationId: 'install_android_1', platform: 'android', deviceName: 'Pixel 9', deviceModel: 'tokay', osVersion: 'Android 16', appVersion: '1.0.0', firstSeenAt: '2026-08-15T08:00:00Z', lastSeenAt: '2026-08-16T08:00:00Z' }], pushRegistrations: [{ id: 'device_android_1', userId: 'u_10291', platform: 'android', provider: 'fcm', notificationsEnabled: true, previewEnabled: false, soundEnabled: true, vibrationEnabled: true, updatedAt: '2026-08-16T08:00:00Z' }] });
  if (url.includes('/users/u_10291/system-message') && method === 'POST') return response({ targetUid: 'u_10291', senderUid: 'u_notice', conversationId: 'conv_notice_1', messageId: 1001, clientMsgNo: 'admin-notice-1' }, 201);
  if (url.includes('/users/u_10291')) return response({ user: { id: 'u_10291', name: '林夏', phone: '13800001001', handle: 'linxia', signature: '在青蛙呱呱保持联系', gender: 'female', handleChangeCount: 1, online: true, onlineConnections: 2, createdAt: '2026-08-01T08:00:00Z' }, deviceCount: 2, friendCount: 18, groupCount: 4, handleChangesUsed: 1, handleChangesRemaining: 1 });
  if (url.endsWith('/users') && method === 'POST') return response({ item: { id: 'u_created', name: '新建账号', phone: '13900139000', handle: 'gg_created', gender: 'female', status: 'active', createdAt: '2026-08-17T08:00:00Z' } }, 201);
  if (url.includes('/users')) {
    const query = new URL(url, 'http://localhost').searchParams.get('q') ?? '';
    const users = [
      { id: 'u_10291', name: '林夏', phone: '13800001001', handle: 'linxia', status: 'active', online: true, onlineConnections: 2, createdAt: '2026-08-01T08:00:00Z' },
      { id: 'u_10288', name: '江宁', phone: '13800001002', handle: 'jiangning', status: 'active', online: false, lastOfflineAt: '2026-08-16T08:00:00Z', createdAt: '2026-08-02T08:00:00Z' },
    ].filter((item) => !query || `${item.name}${item.id}${item.phone}`.includes(query));
    return response({ items: users, total: users.length });
  }
  if (url.includes('/groups/g_1/members')) return response({ items: [
    { conversationId: 'g_1', userId: 'u_10291', name: '林夏', phone: '13800001001', handle: 'linxia', role: 'owner', lastReadSeq: 1280, lastDeliveredSeq: 1280, joinedAt: '2026-08-01T08:00:00Z' },
    { conversationId: 'g_1', userId: 'u_10288', name: '江宁', phone: '13800001002', handle: 'jiangning', role: 'member', lastReadSeq: 1240, lastDeliveredSeq: 1250, joinedAt: '2026-08-02T08:00:00Z' },
  ], total: 2 });
  if (url.includes('/groups/g_1/messages/') && url.endsWith('/recall') && method === 'POST') return response({ recalled: true });
  if (url.includes('/groups/g_1/messages') && method === 'GET') return response({ items: [{ id: '901', conversationSeq: 9, senderId: 'u_10288', sender: { id: 'u_10288', name: '江宁', phone: '13800001002', handle: 'jiangning' }, type: 'text', body: { content: '群内真实消息正文' }, createdAt: '2026-08-17T08:00:00Z', recalled: false, expired: false }], nextBeforeSeq: 0 });
  if (url.includes('/groups/g_1/blacklist') && method === 'GET') return response({ items: [{ user: { id: 'u_blocked', name: '广告账号', phone: '13900009999' }, operatorId: 'admin_1', operatorName: '测试管理员', remark: '多次发布广告', createdAt: '2026-08-17T08:00:00Z' }] });
  if (url.includes('/groups/g_1')) return response({ id: 'g_1', title: '产品交流群', ownerId: 'u_10291', owner: { id: 'u_10291', name: '林夏', phone: '13800001001', handle: 'linxia' }, announcement: '文明交流，保护隐私', announcementVersion: 2, joinPolicy: 'approval', allowMemberAddFriend: true, messageCount: 1280, memberCount: 1 });
  if (url.includes('/groups')) return response({ items: [{ id: 'g_1', title: '产品交流群', ownerId: 'u_10291', owner: { id: 'u_10291', name: '林夏', phone: '13800001001', handle: 'linxia' }, memberCount: 1, messageCount: 1280, status: 'active', createdAt: '2026-08-01T08:00:00Z', reportCount: 0 }], total: 1 });
  if (url.includes('/sensitive-words')) return response({ items: [{ id: 'sw_1', word: '代开发票', category: '黑产', matchType: 'exact', action: 'block', createdAt: '2026-08-01T08:00:00Z' }], total: 1 });
  if (url.includes('/reports')) return response({ items: [], total: 0 });
  if (url.includes('/health')) return response({ items: [{ name: 'WuKongIM 长连接', status: 'healthy', latency: 18, uptime: '99.998%', version: 'v2.2.5', detail: '服务端探针响应正常' }] });
  if (url.includes('/settings')) return response({
    allowRegistration: true, passwordMinLength: 8, maxMessageTextLength: 5000, messageRecallMinutes: 2, maxGroupMembers: 500,
    allowFriendRequests: true, allowSearchByHandle: true, allowSearchByPhone: false, friendRequestExpiryDays: 7,
    announcementPushEnabled: true, callsEnabled: true, videoCallsEnabled: true, sensitiveWordEnabled: true,
    reportSlaHours: 8, maintenanceMode: false, announcement: '', restartRequiredKeys: [],
    configurationStatus: { database: true, redis: true, objectStorage: true, otpProvider: true, pushProvider: true, liveKit: true },
    infrastructure: { pushProvider: 'fcm', mediaMaxSizeMB: 100, callInviteTimeoutSeconds: 30, accessTokenMinutes: 15, refreshTokenHours: 720 },
  });
  if (url.includes('/sticker-categories')) {
    if (method === 'POST') {
      const body = JSON.parse(String(init?.body));
      const item = { id: 'festival', name: body.name, sortOrder: body.sortOrder, enabled: body.enabled };
      fixtureStickerCategories.push(item);
      return response({ item });
    }
    return response({ items: fixtureStickerCategories });
  }
  if (url.includes('/sticker-packs')) {
    if (method === 'POST') {
      const body = JSON.parse(String(init?.body));
      const item = { id: 'pack_new', categoryId: body.categoryId, categoryName: '节日', name: body.name, description: body.description, coverMediaId: body.coverMediaId, status: body.status, itemCount: 0, createdBy: '运营管理员', updatedAt: '2026-08-15T08:00:00Z' };
      fixtureStickerPacks.push(item);
      return response({ item });
    }
    return response({ items: fixtureStickerPacks, total: fixtureStickerPacks.length });
  }
  if (url.includes('/moments')) return response({ items: [], total: 0 });
  if (url.includes('/wukong/overview')) return response({ server_id: '1', version: 'v2.2.5-20260422', uptime: '6d 12h', connections: 7, user_handler_count: 6, cpu: 2.4, mem: 104857600, goroutine: 40, in_msgs: 18, out_msgs: 34, retry_queue: 0 });
  if (url.includes('/wukong/settings')) return response({ logger: { trace_on: 0, loki_on: 1 }, prometheus_on: 1, stress_on: 0 });
  if (url.includes('/wukong/nodes')) return response({ data: [{ id: 1, online: 1, is_leader: 1, version: 'v2.2.5-20260422', slot_count: 64, slot_leader_count: 64 }] });
  if (url.includes('/wukong/robots/')) {
    const body = JSON.parse(String(init?.body));
    const item = { userId: decodeURIComponent(url.split('/').pop() ?? ''), name: '系统通知', username: body.username, placeholder: body.placeholder, enabled: body.enabled, inlineOn: body.inlineOn, version: 2, menus: body.menus, updatedBy: '测试管理员', reason: body.reason, updatedAt: '2026-08-16T08:00:00Z' };
    fixtureRobots = [item];
    return response({ item });
  }
  if (url.includes('/wukong/robots')) return response({ items: fixtureRobots });
  if (url.includes('/wukong/system-users')) return response({ items: [{ userId: 'u_notice', name: '系统通知', enabled: true, syncStatus: 'synced', updatedBy: '系统', reason: '系统通知账号', updatedAt: '2026-08-15T08:00:00Z' }] });
  if (url.includes('/wukong/plugin-events')) return response({ items: [] });
  if (url.includes('/wukong/plugins/') && url.includes('/logs')) return response({ entries: [{ sequence: 1, stream: 'stdout', timestamp: Date.now(), message: 'policy plugin ready' }] });
  if (url.includes('/wukong/plugins')) return response({ items: [{ no: 'wk.plugin.safe', node_id: 1, name: 'Safety Policy', version: '1.0.0', status: 'active', methods: ['Send'], priority: 10, is_ai: 0, config: {}, managed: true, verified: true, built_in: true, lifecycle_status: 'active', file_name: 'safe.wkp', sha256: 'sha256', key_id: 'builtin', installed_at: '2026-08-01T08:00:00Z', updated_at: '2026-08-15T08:00:00Z' }] });
  if (url.includes('/livekit/metrics')) return response({ healthy: true, activeRooms: 1, activeParticipants: 2, cpuPercent: 3.2, residentMemoryBytes: 134217728, networkReceiveBytesPerSecond: 4096, networkTransmitBytesPerSecond: 8192, packetLossPercent: 0.02, participantJoinsLastHour: 12, roomsCompletedLastHour: 5, sampledAt: '2026-08-15T08:00:00Z' });
  if (url.includes('/livekit/rooms/') && url.includes('/participants')) return response({ items: [] });
  if (url.includes('/livekit/rooms')) return response({ items: [{ sid: 'RM_01', name: 'call_20260815_01', createdAt: '2026-08-15T08:00:00Z', participantCount: 2, publisherCount: 2, maxParticipants: 10, activeRecording: false }] });
  if (url.includes('/push')) return response({ providers: [{ provider: 'fcm', activeDevices: 120, disabledDevices: 2 }], queue: [] });
  if (url.includes('/backups')) return response({ configured: true, available: true, status: 'healthy', lastStatus: true, running: false, lastDurationSeconds: 37, incompleteGenerations: 0, offsiteEnabled: true, lastSuccessAt: '2026-08-15T07:00:00Z' });
  if (url.includes('/client-diagnostics')) return response({ summary: { windowHours: 24, crashes: 0, connectionFailures: 1, callFailures: 0, performanceSamples: 12, performanceP95Ms: 1380 }, items: [] });
  if (url.includes('/tasks')) return response({ tasks: { wukongOutbox: { pending: 0, processing: 0, failed: 0, oldestPendingSeconds: 0, reconcileCompleted: 12, reconcilePending: 0, reconcileFailed: 0 }, wukongWebhook: { pending: 0, processing: 0, failed: 0, oldestPendingSeconds: 0 } } });
  if (url.includes('/access')) return response({ current: { id: 'admin_1', role: 'platform_admin' }, administrators: [], roles: [], note: '权限数据来自服务端' });
  if (url.includes('/channels/') && url.includes('/members')) return response({ items: [{ channelId: 'community_1', channelType: 4, userId: 'u_operator', name: '运营小青', handle: 'operator_green', role: 'operator', mutedUntil: '', expiresAt: '', createdAt: '2026-08-10T08:00:00Z', updatedAt: '2026-08-15T08:00:00Z' }] });
  if (url.includes('/channels/') && url.includes('/access')) return response({ items: [] });
  if (url.includes('/channels')) return response({ items: [{ id: 'community_1', channelType: 4, category: 'community', name: '产品交流社区', avatarUrl: '', ownerId: 'u_owner', parentId: '', description: '产品使用和意见交流', visibility: 'public', joinPolicy: 'open', postingPolicy: 'members', slowModeSeconds: 0, memberCount: 126, ban: false, disband: false, sendBan: false, allowStranger: true, metadata: {}, createdAt: '2026-08-01T08:00:00Z', updatedAt: '2026-08-15T08:00:00Z' }], total: 1 });
  if (url.includes('/support/skills')) return response({ items: [{ id: 'support_general', name: '综合咨询', description: '账号和产品问题', routingStrategy: 'least_active', maxConcurrentPerAgent: 5, enabled: true, queueCount: 1, availableAgents: 1, createdAt: '2026-08-01T08:00:00Z', updatedAt: '2026-08-15T08:00:00Z' }] });
  if (url.includes('/support/agents')) return response({ items: [
    { userId: 'u_support', name: '客服坐席', handle: 'support', avatarUrl: '', status: 'available', maxConcurrent: 5, activeSessions: 1, skillGroupIds: ['support_general'], createdAt: '2026-08-01T08:00:00Z', updatedAt: '2026-08-15T08:00:00Z' },
    { userId: 'u_support_2', name: '高级客服', handle: 'support_senior', avatarUrl: '', status: 'available', maxConcurrent: 8, activeSessions: 0, skillGroupIds: ['support_general'], createdAt: '2026-08-02T08:00:00Z', updatedAt: '2026-08-15T08:00:00Z' },
  ] });
  if (url.includes('/support/sessions')) return response({ items: [{ id: 'support_session_1', visitorId: 'u_visitor', visitorName: '访客 A', skillGroupId: 'support_general', skillGroupName: '综合咨询', channelId: 'u_visitor', channelType: 10, subject: '账号登录问题', status: 'active', queuePosition: 0, assignedAgentId: 'u_support', agentName: '客服坐席', transferCount: 0, rating: 0, ratingComment: '', createdAt: '2026-08-15T08:00:00Z', updatedAt: '2026-08-15T08:00:00Z' }], total: 1 });
  if (url.includes('/media')) return response({ items: [
    { id: 'media_cover_20260815', ownerId: 'u_10291', objectKey: 'stickers/new-year-cover.png', mime: 'image/png', status: 'ready', size: 28672, checksum: 'cover-checksum' },
    { id: 'media_sticker_20260815', ownerId: 'u_10291', objectKey: 'stickers/new-year-smile.webp', mime: 'image/webp', status: 'ready', size: 18432, checksum: 'sticker-checksum' },
  ], total: 2 });
  if (url.includes('/messages')) return response({ items: [], total: 0 });
  return response({ items: [], total: 0 });
}

describe('青蛙呱呱管理后台', () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    fixtureStickerCategories = [];
    fixtureStickerPacks = [];
    fixtureRobots = [{ userId: 'u_notice', name: '系统通知', username: 'service_helper', placeholder: '请选择服务', enabled: true, inlineOn: false, version: 1, menus: [{ cmd: '帮助', remark: '使用帮助', type: 'command' }], updatedBy: '系统', reason: '客服入口', updatedAt: '2026-08-15T08:00:00Z' }];
    sessionStorage.setItem('qingwaguagua_admin_session', JSON.stringify(session));
    vi.stubGlobal('fetch', vi.fn(liveFixture));
    window.history.replaceState({}, '', '/overview');
  });
  afterEach(() => { vi.unstubAllGlobals(); });

  it('展示概览核心指标', async () => {
    render(<App />);
    expect(screen.getByRole('heading', { name: '运行概览' })).toBeInTheDocument();
    expect(await screen.findByText('8,429')).toBeInTheDocument();
    expect(screen.getByText('99.993%')).toBeInTheDocument();
  });

  it('概览危险指标不会被错误标记为正常', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => response({
      metrics: [{ label: '消息成功率', value: '82%', delta: '低于服务目标', tone: 'danger' }],
      messageTrend: [], channelMix: [], alerts: [], activity: [],
    })));
    render(<App />);
    expect(await screen.findByText('82%')).toBeInTheDocument();
    expect(screen.getByText('异常')).toBeInTheDocument();
    expect(screen.queryByText('正常')).not.toBeInTheDocument();
  });

  it('只连接生产实时接口且不提供演示切换', async () => {
    render(<App />);
    expect(await screen.findByText('服务端实时数据')).toBeInTheDocument();
    expect(screen.queryByLabelText('数据源')).not.toBeInTheDocument();
    expect(localStorage.getItem('nexachat_data_mode')).toBeNull();
  });

  it('按业务域组织侧栏并自动展开当前页面分组', async () => {
    window.history.replaceState({}, '', '/system-health');
    render(<App />);
    const navigation = screen.getByRole('navigation', { name: '主导航' });
    expect(within(navigation).getByRole('link', { name: '运行概览' })).toBeInTheDocument();
    expect(within(navigation).getByRole('button', { name: '设置' })).toHaveAttribute('aria-expanded', 'true');
    expect(within(navigation).getByRole('button', { name: '用户' })).toHaveAttribute('aria-expanded', 'false');
    expect(within(navigation).queryByRole('button', { name: '平台运维' })).not.toBeInTheDocument();
    expect(within(navigation).queryByRole('button', { name: '业务管理' })).not.toBeInTheDocument();
    await userEvent.click(within(navigation).getByRole('button', { name: '用户' }));
    expect(within(navigation).getByRole('button', { name: '用户' })).toHaveAttribute('aria-expanded', 'true');
  });

  it('窄屏侧栏关闭时不可聚焦，打开后支持 Escape 并把焦点还给菜单按钮', async () => {
    vi.stubGlobal('matchMedia', vi.fn(() => ({
      matches: true,
      media: '(max-width: 1120px)',
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })));
    render(<App />);
    const user = userEvent.setup();
    const navigation = screen.getByLabelText('运营控制台导航');
    const menu = screen.getByRole('button', { name: '打开导航' });
    await waitFor(() => expect((navigation as HTMLElement).inert).toBe(true));
    expect(menu).toHaveAttribute('aria-expanded', 'false');
    await user.click(menu);
    expect((navigation as HTMLElement).inert).toBe(false);
    expect(menu).toHaveAttribute('aria-expanded', 'true');
    await waitFor(() => expect(screen.getByRole('link', { name: '运行概览' })).toHaveFocus());
    fireEvent.keyDown(navigation, { key: 'Escape' });
    await waitFor(() => expect(menu).toHaveFocus());
    expect((navigation as HTMLElement).inert).toBe(true);
    expect(menu).toHaveAttribute('aria-expanded', 'false');
  });

  it('支持防抖搜索用户', async () => {
    window.history.replaceState({}, '', '/users');
    render(<App />);
    const input = screen.getByLabelText('搜索昵称、用户 ID、手机号或呱呱号');
    await screen.findByText('林夏');
    fireEvent.change(input, { target: { value: '江宁' } });
    await waitFor(() => expect(screen.queryByText('林夏')).not.toBeInTheDocument());
    expect(await screen.findByText('江宁')).toBeInTheDocument();
  });

  it('用户反馈分类始终展示中文而不是服务端枚举', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/friendships')) return response({ items: [], total: 0 });
      if (url.includes('/feedback')) return response({
        items: [{ id: 'feedback_1', userId: 'u_10291', userName: '林夏', category: 'bug', content: '扫码页偶发无法打开相册', contact: '', createdAt: '2026-08-16T08:00:00Z' }],
        total: 1,
      });
      return liveFixture(input, init);
    }));
    window.history.replaceState({}, '', '/feedback');

    render(<App />);

    expect(await screen.findByText('扫码页偶发无法打开相册')).toBeInTheDocument();
    expect(screen.getByText('故障', { selector: 'td' })).toBeInTheDocument();
    expect(screen.queryByText('bug')).not.toBeInTheDocument();
  });

  it('举报分类始终展示中文而不是服务端枚举', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).includes('/reports')) return response({
        items: [{
          id: 'report_spam_1', targetType: 'message', targetId: 'message_1', target: '消息 message_1',
          reporterId: 'u_10291', reporter: '林夏', category: 'spam', details: '重复发送推广链接',
          status: 'pending', risk: 'medium', createdAt: '2026-08-16T08:00:00Z',
        }],
        total: 1,
      });
      return liveFixture(input, init);
    }));
    window.history.replaceState({}, '', '/reports');

    render(<App />);

    expect(await screen.findByText('垃圾信息')).toBeInTheDocument();
    expect(screen.queryByText('spam')).not.toBeInTheDocument();
  });

  it('按需加载真实用户详情并支持键盘关闭', async () => {
    window.history.replaceState({}, '', '/users');
    render(<App />);
    await screen.findByText('林夏');
    await userEvent.click(screen.getAllByRole('button', { name: '查看详情' })[0]);
    const dialog = await screen.findByRole('dialog', { name: '用户详情' });
    expect(within(dialog).getByText('在青蛙呱呱保持联系')).toBeInTheDocument();
    expect(within(dialog).getByText('女')).toBeInTheDocument();
    expect(within(dialog).getByText('18')).toBeInTheDocument();
    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: '用户详情' })).not.toBeInTheDocument();
  });

  it('用户详情展示真实好友、黑名单和登记设备', async () => {
    window.history.replaceState({}, '', '/users');
    render(<App />);
    await screen.findByText('林夏');
    await userEvent.click(screen.getAllByRole('button', { name: '查看详情' })[0]);
    const dialog = await screen.findByRole('dialog', { name: '用户详情' });
    expect(within(dialog).getByText('在线 · 2 个连接')).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole('tab', { name: '好友 18' }));
    expect(await within(dialog).findByText('产品同事')).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole('tab', { name: '黑名单 1' }));
    expect(await within(dialog).findByText('被屏蔽用户')).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole('tab', { name: '设备 2' }));
    expect(await within(dialog).findByText('install_android_1')).toBeInTheDocument();
    expect(within(dialog).getByText('device_android_1')).toBeInTheDocument();
  });

  it('新增用户固定中国区号并提交完整资料与审计理由', async () => {
    window.history.replaceState({}, '', '/users/new'); render(<App />);
    expect(await screen.findByRole('heading', { name: '新增用户' })).toBeInTheDocument();
    expect(screen.getByText('+86')).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText('中国大陆手机号'), '13900139000');
    await userEvent.type(screen.getByLabelText('昵称'), '新建账号');
    await userEvent.selectOptions(screen.getByLabelText('性别'), 'female');
    await userEvent.type(screen.getByLabelText('初始密码'), 'StrongPass123!');
    await userEvent.type(screen.getByLabelText('操作理由'), '运营工单 USER-9');
    await userEvent.click(screen.getByRole('button', { name: '创建用户' }));
    await waitFor(() => expect(window.location.pathname).toBe('/users'));
    const write = vi.mocked(fetch).mock.calls.find(([input, init]) => String(input).endsWith('/users') && init?.method === 'POST');
    expect(JSON.parse(String(write?.[1]?.body))).toEqual({ phone: '13900139000', name: '新建账号', password: 'StrongPass123!', gender: 'female', reason: '运营工单 USER-9', confirmed: true });
  });

  it('好友聊天记录展示真实正文并要求理由后管理员撤回', async () => {
    window.history.replaceState({}, '', '/users'); render(<App />); await screen.findByText('林夏');
    await userEvent.click(screen.getAllByRole('button', { name: '查看详情' })[0]);
    const detail = await screen.findByRole('dialog', { name: '用户详情' });
    await userEvent.click(within(detail).getByRole('tab', { name: '好友 18' }));
    await userEvent.click(await within(detail).findByRole('button', { name: '聊天记录' }));
    const history = await screen.findByRole('dialog', { name: '林夏 与 江宁 的聊天记录' });
    expect(await within(history).findByText('需要管理员检查的消息')).toBeInTheDocument();
    await userEvent.click(within(history).getByRole('button', { name: '撤回' }));
    const recall = await screen.findByRole('dialog', { name: '管理员撤回消息' });
    expect(within(recall).getByRole('button', { name: '确认全端撤回' })).toBeDisabled();
    await userEvent.type(within(recall).getByLabelText('撤回理由'), '违规内容复核确认');
    await userEvent.click(within(recall).getByRole('button', { name: '确认全端撤回' }));
    expect(await screen.findByText('消息已全端撤回并写入审计')).toBeInTheDocument();
    const write = vi.mocked(fetch).mock.calls.find(([input, init]) => String(input).endsWith('/messages/88/recall') && init?.method === 'POST');
    expect(JSON.parse(String(write?.[1]?.body))).toEqual({ reason: '违规内容复核确认', confirmed: true });
  });

  it('通过真实 WuKongIM 接口发送系统消息并携带审计理由', async () => {
    window.history.replaceState({}, '', '/users');
    render(<App />);
    await screen.findByText('林夏');
    await userEvent.click(screen.getAllByRole('button', { name: '发系统消息' })[0]);
    const dialog = screen.getByRole('dialog', { name: '发送系统消息' });
    await userEvent.type(within(dialog).getByLabelText('消息内容'), '请及时更新客户端');
    await userEvent.type(within(dialog).getByLabelText('发送理由'), '版本升级通知 OPS-18');
    await userEvent.click(within(dialog).getByRole('button', { name: '确认发送' }));
    expect(await screen.findByText('系统消息已发送给 林夏')).toBeInTheDocument();
    const write = vi.mocked(fetch).mock.calls.find(([input, init]) => String(input).includes('/users/u_10291/system-message') && init?.method === 'POST');
    expect(JSON.parse(String(write?.[1]?.body))).toEqual({ senderUid: 'u_notice', content: '请及时更新客户端', reason: '版本升级通知 OPS-18', confirmed: true });
  });

  it('展示群资料和服务端群成员列表', async () => {
    window.history.replaceState({}, '', '/groups');
    render(<App />);
    await screen.findByText('产品交流群');
    await userEvent.click(screen.getByRole('button', { name: '查看详情' }));
    const dialog = await screen.findByRole('dialog', { name: '群组详情' });
    expect(within(dialog).getByText('文明交流，保护隐私')).toBeInTheDocument();
    expect(within(dialog).getByText('群主')).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole('tab', { name: '群成员 1' }));
    expect(within(dialog).getByText('u_10291')).toBeInTheDocument();
  });

  it('群成员治理提供角色、禁言和移出操作且提交审计理由', async () => {
    window.history.replaceState({}, '', '/groups');
    render(<App />);
    await screen.findByText('产品交流群');
    await userEvent.click(screen.getByRole('button', { name: '查看详情' }));
    const detail = await screen.findByRole('dialog', { name: '群组详情' });
    await userEvent.click(within(detail).getByRole('tab', { name: '群成员 1' }));
    expect(within(detail).getByRole('button', { name: '设为管理员' })).toBeInTheDocument();
    expect(within(detail).getByRole('button', { name: '禁言 1 小时' })).toBeInTheDocument();
    expect(within(detail).getByRole('button', { name: '移出群聊' })).toBeInTheDocument();

    await userEvent.click(within(detail).getByRole('button', { name: '禁言 1 小时' }));
    const confirmation = await screen.findByRole('dialog', { name: '禁言成员一小时' });
    expect(within(confirmation).getByRole('button', { name: '确认禁言' })).toBeDisabled();
    await userEvent.type(within(confirmation).getByLabelText('处置理由'), '群内违规发言，工单 GROUP-4');
    await userEvent.click(within(confirmation).getByRole('button', { name: '确认禁言' }));
    expect(await screen.findByText('江宁：确认禁言已完成')).toBeInTheDocument();
    const write = vi.mocked(fetch).mock.calls.find(([input, init]) => String(input).includes('/groups/g_1/members/u_10288') && init?.method === 'PATCH');
    expect(JSON.parse(String(write?.[1]?.body))).toEqual(expect.objectContaining({ action: 'mute', reason: '群内违规发言，工单 GROUP-4', confirmed: true }));
  });

  it('群详情按需加载 WuKongIM 正文，并展示发送人组件和黑名单', async () => {
    window.history.replaceState({}, '', '/groups');
    render(<App />);
    await screen.findByText('产品交流群');
    await userEvent.click(screen.getByRole('button', { name: '查看详情' }));
    const detail = await screen.findByRole('dialog', { name: '群组详情' });

    expect(vi.mocked(fetch).mock.calls.some(([input]) => String(input).includes('/groups/g_1/messages'))).toBe(false);
    await userEvent.click(within(detail).getByRole('tab', { name: /^聊天记录/ }));
    expect(await within(detail).findByText('群内真实消息正文')).toBeInTheDocument();
    expect(within(detail).getByText('13800001002')).toBeInTheDocument();
    expect(within(detail).getByRole('button', { name: '删除（全端撤回）' })).toBeInTheDocument();

    await userEvent.click(within(detail).getByRole('tab', { name: /^黑名单/ }));
    expect(await within(detail).findByText('广告账号')).toBeInTheDocument();
    expect(within(detail).getByText('多次发布广告')).toBeInTheDocument();
    expect(within(detail).getByRole('button', { name: '解除黑名单' })).toBeInTheDocument();
  });

  it('消息治理可以筛选全部内置和自定义消息类型', async () => {
    window.history.replaceState({}, '', '/messages');
    render(<App />);
    const typeFilter = screen.getByLabelText('消息类型');
    expect(within(typeFilter).getAllByRole('option').map((option) => option.textContent)).toEqual([
      '全部类型', '文本', '图片 / GIF', '语音', '视频', '位置', '名片', '文件', '合并聊天记录',
      '系统事件', '商店表情', '朋友圈分享', '通话事件', '直播互动', '客服事件', '截屏提示',
    ]);
    expect(await screen.findByText('没有匹配的消息元数据')).toBeInTheDocument();
  });

  it('消息治理展示从 WuKongIM 实时加载的正文', async () => {
    window.history.replaceState({}, '', '/messages');
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).includes('/messages')) return response({ items: [{
        id: '2089149153182388224', clientMsgId: 'client-live-1', conversationId: 'conv_direct_1',
        senderId: 'u_10291', sender: { id: 'u_10291', name: '林夏', phone: '13800001001' }, conversationSeq: 13, type: 'text', body: { text: '服务端实时正文' },
        createdAt: '2026-08-17T08:35:00Z',
      }], total: 1 });
      return liveFixture(input, init);
    }));
    render(<App />);
    expect(await screen.findByText('服务端实时正文')).toBeInTheDocument();
    expect(screen.getByText('林夏')).toBeInTheDocument();
    expect(screen.getByText('u_10291')).toBeInTheDocument();
    expect(screen.getByText('13800001001')).toBeInTheDocument();
    expect(screen.queryByText('2089149153182388224')).not.toBeInTheDocument();
    expect(screen.queryByText('client-live-1')).not.toBeInTheDocument();
    expect(screen.queryByText('内容受保护')).not.toBeInTheDocument();
    expect(screen.getByText(/每次查看都会写入审计日志/)).toBeInTheDocument();
  });

  it('兼容服务端扁平 dashboard 响应', async () => {
    sessionStorage.setItem('qingwaguagua_admin_session', JSON.stringify(session));
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true, status: 200, headers: new Headers(),
      json: async () => ({
        users: 11, bannedUsers: 1, conversations: 5, messages: 133, pendingReports: 2, wukongConnections: 7, wukongStatus: 'ok',
        messageTrend: [
          { time: '2026-08-13T04:00:00Z', count: 3 },
          { time: '2026-08-13T05:00:00Z', count: 7 },
        ],
        channelMix: [{ kind: 'direct', count: 7 }, { kind: 'group', count: 3 }],
        activity: [{ id: 'audit-1', actorId: 'admin-1', action: 'settings.updated', targetType: 'settings', targetId: 'global', result: 'success', createdAt: '2026-08-13T05:00:00Z' }],
      }),
    })));
    render(<App />);
    expect(await screen.findByText('用户总数')).toBeInTheDocument();
    expect(screen.getByText('11')).toBeInTheDocument();
    expect(screen.getByText('7')).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '消息量趋势' })).toBeInTheDocument();
    expect(screen.getByText('单聊')).toBeInTheDocument();
    expect(screen.getByText('70%')).toBeInTheDocument();
    expect(screen.getByText('2 条举报等待处理')).toBeInTheDocument();
    expect(screen.getByText('settings.updated')).toBeInTheDocument();
  });

  it('解析嵌套接口错误并停留在登录页', async () => {
    sessionStorage.clear();
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false, status: 401, headers: new Headers({ 'x-request-id': 'req-1' }),
      json: async () => ({ error: { code: 'UNAUTHENTICATED', message: 'invalid credentials' } }),
    })));
    render(<App />);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('管理员邮箱'), 'ops@example.com');
    await user.type(screen.getByLabelText('密码'), 'wrong-password');
    await user.click(screen.getByRole('button', { name: '登录控制台' }));
    expect(await screen.findByText(/邮箱或密码不正确/)).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '管理员登录' })).toBeInTheDocument();
    await user.type(screen.getByLabelText('密码'), 'a');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('网络连接失败时展示中文可执行提示而不是浏览器英文异常', async () => {
    sessionStorage.clear();
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch'); }));
    render(<App />);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('管理员邮箱'), 'ops@example.com');
    await user.type(screen.getByLabelText('密码'), 'password');
    await user.click(screen.getByRole('button', { name: '登录控制台' }));
    expect(await screen.findByText('无法连接服务，请检查网络或服务状态')).toBeInTheDocument();
    expect(screen.queryByText('Failed to fetch')).not.toBeInTheDocument();
  });

  it('管理员可以确认密码输入且登录页不再包含动态验证码', async () => {
    sessionStorage.clear();
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    render(<App />);
    const user = userEvent.setup();
    const password = screen.getByLabelText('密码');
    expect(password).toHaveAttribute('type', 'password');
    await user.type(password, 'correct-password');
    await user.click(screen.getByRole('button', { name: '显示密码' }));
    expect(password).toHaveAttribute('type', 'text');
    expect(screen.getByRole('button', { name: '隐藏密码' })).toHaveAttribute('aria-pressed', 'true');
    await user.type(screen.getByLabelText('管理员邮箱'), 'ops@example.com');
    expect(screen.queryByLabelText(/动态验证码/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '登录控制台' })).toBeEnabled();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('管理员邮箱仅在失焦后提示格式错误且不会发送无效请求', async () => {
    sessionStorage.clear();
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    render(<App />);
    const user = userEvent.setup();
    const email = screen.getByLabelText('管理员邮箱');
    const password = screen.getByLabelText('密码');

    await user.type(email, 'not-an-email');
    expect(screen.queryByText('请输入有效的管理员邮箱')).not.toBeInTheDocument();
    await user.type(password, 'correct-password');

    expect(email).toHaveAttribute('aria-invalid', 'true');
    expect(screen.getByText('请输入有效的管理员邮箱')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '登录控制台' })).toBeDisabled();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('管理员邮箱未编辑时焦点切换不会提前显示必填错误', async () => {
    sessionStorage.clear();
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    render(<App />);
    const user = userEvent.setup();
    const email = screen.getByLabelText('管理员邮箱');

    expect(email).toHaveFocus();
    await user.click(screen.getByLabelText('密码'));

    expect(email).toHaveAttribute('aria-invalid', 'false');
    expect(screen.queryByText('请输入管理员邮箱')).not.toBeInTheDocument();
    expect(screen.getByText('使用管理员分配的邮箱地址')).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('使用服务端返回的角色建立短时会话', async () => {
    sessionStorage.clear();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({ accessToken: 'admin.jwt', id: 'admin_moderator', email: 'moderator@example.com', displayName: '安全审核员', roleId: 'moderator', roleName: '内容审核员', permissions: ['users.write', 'reports.write', 'rules.write', 'content.write'], expiresIn: 900 }),
      })
      .mockResolvedValue({
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({ users: 0, conversations: 0, messages: 0, pendingReports: 0, wukongConnections: 0, wukongStatus: 'unavailable' }),
      });
    vi.stubGlobal('fetch', fetchMock);
    render(<App />);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('管理员邮箱'), 'moderator@example.com');
    await user.type(screen.getByLabelText('密码'), 'correct-password');
    await user.click(screen.getByRole('button', { name: '登录控制台' }));
    expect(await screen.findByText('安全审核员')).toBeInTheDocument();
    expect(JSON.parse(sessionStorage.getItem('qingwaguagua_admin_session') ?? '{}')).toEqual(expect.objectContaining({ token: 'admin.jwt', roleId: 'moderator', roleName: '内容审核员', permissions: ['users.write', 'reports.write', 'rules.write', 'content.write'] }));
    expect(fetchMock).toHaveBeenNthCalledWith(1, expect.stringContaining('/auth/login'), expect.objectContaining({ method: 'POST' }));
  });

  it('启动时通过 auth me 刷新自定义角色和实时权限', async () => {
    window.history.replaceState({}, '', '/users');
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/auth/me')) return response({ id: 'admin_1', email: 'admin@example.com', displayName: '值班同事', roleId: 'readonly_custom', roleName: '值班只读', permissions: [] });
      if (url.includes('/users')) return response({ items: [{ id: 'u_1', name: '真实用户', phone: '13800000000', handle: 'real_user', status: 'active', createdAt: '2026-08-01T08:00:00Z' }], total: 1 });
      return response({ items: [] });
    }));
    render(<App />);
    expect(await screen.findByText('值班只读')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '封禁账号' })).toBeDisabled();
    expect(JSON.parse(sessionStorage.getItem('qingwaguagua_admin_session') ?? '{}')).toEqual(expect.objectContaining({ roleId: 'readonly_custom', roleName: '值班只读', permissions: [] }));
  });

  it('平台管理员可以从管理员与角色页面创建数据库账号', async () => {
    window.history.replaceState({}, '', '/administrators');
    let createdBody: Record<string, unknown> | undefined;
    const roles = [{ id: 'platform_admin', name: '平台管理员', description: '全部权限', builtIn: true, permissions: session.permissions, accountCount: 1, createdBy: 'system', createdAt: '2026-08-01T08:00:00Z', updatedAt: '2026-08-01T08:00:00Z' }, { id: 'support', name: '只读支持', description: '只读查看', builtIn: true, permissions: [], accountCount: 0, createdBy: 'system', createdAt: '2026-08-01T08:00:00Z', updatedAt: '2026-08-01T08:00:00Z' }];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/auth/me')) return response(session);
      if (url.endsWith('/roles')) return response({ items: roles });
      if (url.includes('/administrators?')) return response({ items: [{ id: 'admin_1', email: 'admin@example.com', displayName: '测试管理员', roleId: 'platform_admin', roleName: '平台管理员', status: 'active', permissions: session.permissions, passwordUpdatedAt: '2026-08-01T08:00:00Z', createdBy: 'bootstrap', createdAt: '2026-08-01T08:00:00Z', updatedAt: '2026-08-01T08:00:00Z' }], total: 1 });
      if (url.endsWith('/administrators') && init?.method === 'POST') { createdBody = JSON.parse(String(init.body)); return response({ id: 'admin_2', ...createdBody, roleName: '只读支持', status: 'active', permissions: [], passwordUpdatedAt: '2026-08-17T08:00:00Z', createdBy: 'admin_1', createdAt: '2026-08-17T08:00:00Z', updatedAt: '2026-08-17T08:00:00Z' }, 201); }
      return response({ items: [] });
    });
    vi.stubGlobal('fetch', fetchMock);
    render(<App />);
    const user = userEvent.setup();
    await screen.findByText('admin@example.com');
    await user.click(screen.getByRole('button', { name: '新增管理员' }));
    await user.type(screen.getByLabelText('显示名称'), '只读值班');
    await user.type(screen.getByLabelText('邮箱'), 'readonly@example.com');
    await user.selectOptions(screen.getByLabelText('角色'), 'support');
    await user.type(screen.getByLabelText('初始密码'), 'Password123!');
    await user.type(screen.getByLabelText('操作原因'), '工单 ADMIN-1');
    await user.click(screen.getByRole('button', { name: '创建管理员' }));
    await waitFor(() => expect(createdBody).toEqual({ email: 'readonly@example.com', displayName: '只读值班', roleId: 'support', password: 'Password123!', reason: '工单 ADMIN-1', confirmed: true }));
  });

  it('平台管理员可以创建带实时功能域权限的自定义角色', async () => {
    window.history.replaceState({}, '', '/administrators');
    let roleBody: Record<string, unknown> | undefined;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/auth/me')) return response(session);
      if (url.endsWith('/roles') && init?.method === 'POST') { roleBody = JSON.parse(String(init.body)); return response({ id: 'role_custom', ...roleBody, builtIn: false, accountCount: 0, createdBy: 'admin_1', createdAt: '2026-08-17T08:00:00Z', updatedAt: '2026-08-17T08:00:00Z' }, 201); }
      if (url.endsWith('/roles')) return response({ items: [{ id: 'platform_admin', name: '平台管理员', description: '全部权限', builtIn: true, permissions: session.permissions, accountCount: 1, createdBy: 'system', createdAt: '2026-08-01T08:00:00Z', updatedAt: '2026-08-01T08:00:00Z' }] });
      if (url.includes('/administrators?')) return response({ items: [], total: 0 });
      return response({ items: [] });
    }));
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', { name: '角色与权限' }));
    await user.click(screen.getByRole('button', { name: '新建角色' }));
    await user.type(screen.getByLabelText('角色名称'), '用户值班');
    await user.type(screen.getByLabelText('角色说明'), '处理普通用户工单');
    await user.click(screen.getByRole('checkbox', { name: '用户处置' }));
    await user.type(screen.getByLabelText('操作原因'), '工单 ROLE-1');
    await user.click(screen.getByRole('button', { name: '创建角色' }));
    await waitFor(() => expect(roleBody).toEqual({ name: '用户值班', description: '处理普通用户工单', permissions: ['users.write'], reason: '工单 ROLE-1', confirmed: true }));
  });

  it('管理员修改本人密码后立即退出并清除旧会话', async () => {
    window.history.replaceState({}, '', '/change-password');
    let passwordBody: Record<string, unknown> | undefined;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/auth/me')) return response(session);
      if (url.includes('/auth/change-password') && init?.method === 'POST') { passwordBody = JSON.parse(String(init.body)); return response(undefined, 204); }
      return response({ items: [] });
    }));
    render(<App />);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('当前密码'), 'OldPassword123!');
    await user.type(screen.getByLabelText('新密码'), 'NewPassword456!');
    await user.type(screen.getByLabelText('确认新密码'), 'NewPassword456!');
    await user.click(screen.getByRole('button', { name: '修改密码并重新登录' }));
    expect(await screen.findByRole('heading', { name: '管理员登录' })).toBeInTheDocument();
    expect(passwordBody).toEqual({ currentPassword: 'OldPassword123!', newPassword: 'NewPassword456!' });
    expect(sessionStorage.getItem('qingwaguagua_admin_session')).toBeNull();
  });

  it('页面保持打开时也会在管理员会话到期后自动退出', async () => {
    sessionStorage.setItem('qingwaguagua_admin_session', JSON.stringify({ ...session, expiresAt: Date.now() + 30 }));
    render(<App />);
    expect(await screen.findByText('测试管理员')).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: '管理员登录' })).toBeInTheDocument();
    expect(screen.getByText('管理员会话已到期，请重新登录')).toBeInTheDocument();
    expect(sessionStorage.getItem('qingwaguagua_admin_session')).toBeNull();
  });

  it('刷新页面发现已过期会话时在登录表单内说明原因', () => {
    sessionStorage.setItem('qingwaguagua_admin_session', JSON.stringify({ ...session, expiresAt: Date.now() - 1 }));
    render(<App />);
    expect(screen.getByRole('heading', { name: '管理员登录' })).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('管理员会话已到期，请重新登录');
    expect(sessionStorage.getItem('qingwaguagua_admin_session')).toBeNull();
  });

  it('并发 401 只展示一条稳定的会话失效说明', async () => {
    render(<App />);
    expect(await screen.findByText('测试管理员')).toBeInTheDocument();
    window.dispatchEvent(new CustomEvent('nexachat:unauthorized'));
    window.dispatchEvent(new CustomEvent('nexachat:unauthorized'));
    expect(await screen.findByRole('heading', { name: '管理员登录' })).toBeInTheDocument();
    expect(screen.getAllByText('管理员会话已失效，请重新登录')).toHaveLength(1);
    expect(sessionStorage.getItem('qingwaguagua_admin_session')).toBeNull();
  });

  it('危险操作弹窗接管焦点并支持 Escape', async () => {
    window.history.replaceState({}, '', '/users');
    render(<App />);
    const user = userEvent.setup();
    const actions = await screen.findAllByRole('button', { name: '封禁账号' });
    await user.click(actions[0]);
    const dialog = screen.getByRole('dialog');
    expect(dialog).toBeInTheDocument();
    expect(screen.getByLabelText('处置理由')).toHaveValue('');
    expect(screen.getByRole('button', { name: '确认封禁' })).toBeDisabled();
    await waitFor(() => expect(screen.getByRole('button', { name: '取消' })).toHaveFocus());
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('删除敏感词前要求二次确认', async () => {
    window.history.replaceState({}, '', '/sensitive-words');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '删除 代开发票' }));
    expect(screen.getByRole('heading', { name: '删除敏感词规则' })).toBeInTheDocument();
    const confirm = screen.getByRole('button', { name: '删除规则' });
    expect(screen.getByLabelText('删除理由')).toHaveValue('');
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText('删除理由'), '规则已被更精准的条目替代');
    expect(confirm).toBeEnabled();
  });

  it('解散群组必须由操作人主动填写理由', async () => {
    window.history.replaceState({}, '', '/groups');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '解散群组' }));
    const dialog = screen.getByRole('dialog');
    const confirm = within(dialog).getByRole('button', { name: '解散群组' });
    expect(within(dialog).getByLabelText('解散理由')).toHaveValue('');
    expect(confirm).toBeDisabled();
    await user.type(within(dialog).getByLabelText('解散理由'), '多次处置后仍持续传播违规内容');
    expect(confirm).toBeEnabled();
  });

  it('新建公告在完成内容与操作理由前不允许提交', async () => {
    window.history.replaceState({}, '', '/announcements');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '新建公告' }));
    const confirm = screen.getByRole('button', { name: '创建公告' });
    expect(screen.getByLabelText('操作理由')).toHaveValue('');
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText('操作理由'), '运营计划 OPS-2026-0815');
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText('公告标题'), '系统维护通知');
    await user.type(screen.getByLabelText('公告正文'), '今晚 23:00 开始进行系统维护，预计 30 分钟。');
    expect(confirm).toBeEnabled();
  });

  it('公告编辑存在未保存内容时关闭会先确认并保留草稿', async () => {
    window.history.replaceState({}, '', '/announcements');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '新建公告' }));
    await user.type(screen.getByLabelText('公告标题'), '临时维护公告');
    await user.type(screen.getByLabelText('公告正文'), '今晚进行短时维护。');
    await user.type(screen.getByLabelText('操作理由'), '变更单 OPS-2026-0816');

    await user.click(screen.getByRole('button', { name: '关闭' }));
    expect(screen.getByRole('heading', { name: '放弃未保存的公告？' })).toBeInTheDocument();
    expect(screen.getByText('标题、正文、投放范围和定时设置将全部丢失。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '继续编辑' })).toHaveFocus();

    await user.click(screen.getByRole('button', { name: '继续编辑' }));
    expect(screen.getByRole('heading', { name: '新建运营公告' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '关闭' })).toHaveFocus();
    expect(screen.getByLabelText('公告标题')).toHaveValue('临时维护公告');
    expect(screen.getByLabelText('公告正文')).toHaveValue('今晚进行短时维护。');
    expect(screen.getByLabelText('操作理由')).toHaveValue('变更单 OPS-2026-0816');

    await user.keyboard('{Escape}');
    expect(screen.getByRole('heading', { name: '放弃未保存的公告？' })).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(screen.getByRole('heading', { name: '新建运营公告' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '关闭' })).toHaveFocus();

    await user.click(screen.getByRole('button', { name: '取消' }));
    await user.click(screen.getByRole('button', { name: '放弃修改' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '新建公告' }));
    expect(screen.getByLabelText('公告标题')).toHaveValue('');
    expect(screen.getByLabelText('公告正文')).toHaveValue('');
    expect(screen.getByLabelText('操作理由')).toHaveValue('');
  });

  it('定向公告从真实账号中添加接收用户而不手填用户 ID', async () => {
    window.history.replaceState({}, '', '/announcements');
    render(<App />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole('button', { name: '新建公告' }));
    await user.selectOptions(screen.getByLabelText('投放范围'), 'users');
    const receiver = screen.getByLabelText('添加公告接收用户');
    await waitFor(() => expect(within(receiver).getByRole('option', { name: '林夏 · linxia' })).toBeInTheDocument());
    expect(screen.queryByLabelText('用户 ID（逗号或换行分隔）')).not.toBeInTheDocument();

    await user.selectOptions(receiver, 'u_10291');
    expect(screen.getByLabelText('已选公告接收用户')).toHaveTextContent('林夏 · linxia');
    expect(screen.getByRole('button', { name: '移除林夏 · linxia' })).toBeInTheDocument();
  });

  it('举报状态筛选使用全部可键盘聚焦的普通按钮', async () => {
    window.history.replaceState({}, '', '/reports');
    render(<App />);
    const group = screen.getByRole('group', { name: '举报状态筛选' });
    const buttons = within(group).getAllByRole('button');
    expect(buttons).toHaveLength(5);
    buttons.forEach((button) => expect(button.tabIndex).toBe(0));
    expect(buttons[0]).toHaveAttribute('aria-pressed', 'true');
    expect(await screen.findByText('当前队列已处理完')).toBeInTheDocument();
  });

  it('点击下一页时使用服务端 nextCursor 而非本地切片', async () => {
    sessionStorage.setItem('qingwaguagua_admin_session', JSON.stringify(session));
    const firstPage = Array.from({ length: 20 }, (_, index) => ({ id: `u_${index}`, name: `用户${index}`, createdAt: '2026-07-31T00:00:00Z' }));
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const next = String(input).includes('cursor=next-user');
      return { ok: true, status: 200, headers: new Headers(), json: async () => next ? { items: [{ id: 'u_20', name: '用户20', createdAt: '2026-07-31T00:00:00Z' }], total: 21, nextCursor: '' } : { items: firstPage, total: 21, nextCursor: 'next-user' } };
    });
    vi.stubGlobal('fetch', fetchMock);
    window.history.replaceState({}, '', '/users');
    render(<App />);
    expect(await screen.findByText('用户0')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    expect(await screen.findByText('用户20')).toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('cursor=next-user'))).toBe(true);
  });

  it('举报处置失败时保留弹窗错误且不显示成功通知', async () => {
    sessionStorage.setItem('qingwaguagua_admin_session', JSON.stringify(session));
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/resolve')) return { ok: false, status: 409, headers: new Headers(), json: async () => ({ error: { code: 'TARGET_CHANGED', message: '目标消息状态已变化' } }) };
      return {
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({ items: [{ id: 'r_1', targetType: 'message', targetId: 'm_1', reporterId: 'u_2', reason: '疑似诈骗', details: '测试内容', status: 'pending', createdAt: '2026-07-31T00:00:00Z' }], total: 1 }),
      };
    });
    vi.stubGlobal('fetch', fetchMock);
    window.history.replaceState({}, '', '/reports');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '审核举报' }));
    expect(screen.getByRole('option', { name: '删除并撤回违规消息' })).toHaveValue('delete_message');
    await user.type(screen.getByLabelText('审核备注'), '确认违规');
    await user.click(screen.getByRole('button', { name: '提交审核结果' }));
    expect(await screen.findByText('目标消息状态已变化')).toBeInTheDocument();
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.queryByText('违规内容已完成处置')).not.toBeInTheDocument();
  });

  it('系统健康页面使用不与服务端探针冲突的可刷新路由', async () => {
    window.history.replaceState({}, '', '/system-health');
    render(<App />);
    expect(screen.getByRole('heading', { name: '系统健康' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '系统健康' })).toHaveAttribute('href', '/system-health');
    expect(await screen.findByText('WuKongIM 长连接')).toBeInTheDocument();
  });

  it('系统健康页面不会把实时消息或管理端口故障汇总为正常', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).includes('/health')) return response({ items: [
        { name: '青蛙呱呱 API', status: 'healthy', latency: 1, uptime: '2 天', version: '当前部署', detail: 'HTTP 服务已响应' },
        { name: 'PostgreSQL 数据库', status: 'healthy', latency: 2, uptime: '实时探测', version: '当前部署', detail: '依赖服务响应正常' },
        { name: 'WuKongIM 实时消息', status: 'down', latency: 4, uptime: '实时探测', version: '当前部署', detail: '依赖服务当前无法连接，请检查进程、端口和网络' },
        { name: 'WuKongIM 管理接口', status: 'down', latency: 5, uptime: '实时探测', version: '当前部署', detail: '依赖服务当前无法连接，请检查进程、端口和网络' },
      ] });
      return liveFixture(input, init);
    }));
    window.history.replaceState({}, '', '/system-health');
    render(<App />);
    expect(await screen.findByText('2 个检测项目需要关注')).toBeInTheDocument();
    expect(screen.getAllByText('服务中断')).toHaveLength(2);
    expect(screen.queryByText('当前检测项目正常')).not.toBeInTheDocument();
  });

  it('系统健康页面把缺失状态明确标记为未知并计入关注项', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).includes('/health')) return response({ items: [
        { name: '对象存储', latency: 4 },
      ] });
      return liveFixture(input, init);
    }));
    window.history.replaceState({}, '', '/system-health');
    render(<App />);

    expect(await screen.findByText('1 个检测项目需要关注')).toBeInTheDocument();
    expect(screen.getByText('状态未知')).toBeInTheDocument();
    expect(screen.getByText('服务未返回健康状态')).toBeInTheDocument();
    expect(screen.queryByText('当前检测项目正常')).not.toBeInTheDocument();
  });

  it('运行概览不会把服务端缺失的计数和趋势伪造成零', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).includes('/dashboard')) return response({ wukongStatus: 'ok' });
      return liveFixture(input, init);
    }));
    window.history.replaceState({}, '', '/overview');
    render(<App />);

    expect((await screen.findAllByText('—')).length).toBeGreaterThanOrEqual(4);
    expect(screen.getByText('消息总数未上报')).toBeInTheDocument();
    expect(screen.getByText('举报队列未上报')).toBeInTheDocument();
    expect(screen.getByText('暂无可用趋势数据')).toBeInTheDocument();
    expect(screen.queryByText('当前队列为空')).not.toBeInTheDocument();
  });

  it('发布系统设置前要求二次确认和理由', async () => {
    window.history.replaceState({}, '', '/settings');
    render(<App />);
    const user = userEvent.setup();
    await screen.findByRole('heading', { name: '系统设置' });
    const saveButton = await screen.findByRole('button', { name: '保存并立即生效' });
    expect(saveButton).toBeDisabled();
    expect(screen.getByText('当前设置与服务端一致')).toBeInTheDocument();
    await user.click(screen.getByRole('checkbox', { name: /允许新用户注册/ }));
    expect(saveButton).toBeEnabled();
    expect(screen.getByText('有未保存的策略更改')).toBeInTheDocument();
    await user.click(saveButton);
    expect(screen.getByRole('heading', { name: '发布系统业务策略' })).toBeInTheDocument();
    const confirm = screen.getByRole('button', { name: '确认发布' });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText('发布理由'), '变更单 OPS-2026-08-12');
    expect(confirm).toBeEnabled();
  });

  it('系统设置保存失败时保留确认窗口并显示服务端错误', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        return response({ error: { code: 'DATASTORE_UNAVAILABLE' } }, 503);
      }
      return liveFixture(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);
    window.history.replaceState({}, '', '/settings');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('checkbox', { name: /允许新用户注册/ }));
    await user.click(screen.getByRole('button', { name: '保存并立即生效' }));
    await user.type(screen.getByLabelText('发布理由'), '变更单 OPS-2026-0816');
    await user.click(screen.getByRole('button', { name: '确认发布' }));

    expect(await screen.findByText('数据服务暂时不可用，请稍后重试')).toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: '发布系统业务策略' })).toBeInTheDocument();
    expect(screen.getByLabelText('发布理由')).toHaveValue('变更单 OPS-2026-0816');
  });

  it('系统设置有未保存修改时保护侧栏导航和退出登录', async () => {
    window.history.replaceState({}, '', '/settings');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('checkbox', { name: /允许新用户注册/ }));

    await user.click(screen.getByRole('link', { name: '运行概览' }));
    expect(screen.getByRole('heading', { name: '放弃未保存的修改？' })).toBeInTheDocument();
    expect(screen.getByText(/系统业务策略有未保存的修改/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '取消' }));
    expect(screen.getByRole('heading', { name: '系统设置' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '退出管理后台' }));
    expect(screen.getByRole('button', { name: '放弃修改并退出' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '取消' }));
    expect(screen.getByRole('heading', { name: '系统设置' })).toBeInTheDocument();

    await user.click(screen.getByRole('link', { name: '运行概览' }));
    await user.click(screen.getByRole('button', { name: '放弃修改并离开' }));
    expect(await screen.findByRole('heading', { name: '运行概览' })).toBeInTheDocument();
  });

  it('系统设置有未保存修改时保护浏览器前进后退', async () => {
    const confirmLeave = vi.fn(() => false);
    vi.stubGlobal('confirm', confirmLeave);
    window.history.replaceState({}, '', '/settings');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('checkbox', { name: /允许新用户注册/ }));

    window.history.pushState({}, '', '/overview');
    fireEvent.popState(window);
    expect(confirmLeave).toHaveBeenCalledWith('系统业务策略有未保存的修改，确定离开当前页面吗？');
    expect(window.location.pathname).toBe('/settings');
    expect(screen.getByRole('heading', { name: '系统设置' })).toBeInTheDocument();

    confirmLeave.mockReturnValue(true);
    window.history.pushState({}, '', '/overview');
    fireEvent.popState(window);
    expect(await screen.findByRole('heading', { name: '运行概览' })).toBeInTheDocument();
  });

  it('公告时间异常时降级展示而不是让整页崩溃', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).includes('/announcements')) {
        return response({ items: [{ id: 'notice_bad_time', title: '异常时间公告', content: '用于验证时间容错', status: 'published', targetType: 'all', targetUserIds: [], pushOnPublish: false, publishedAt: 'not-a-date' }], total: 1 });
      }
      return liveFixture(input, init);
    }));
    window.history.replaceState({}, '', '/announcements');
    render(<App />);

    expect(await screen.findByText('异常时间公告')).toBeInTheDocument();
    expect(screen.getByText('时间未知')).toBeInTheDocument();
    expect(screen.queryByText('页面暂时无法显示')).not.toBeInTheDocument();
  });

  it('内容审核要求填写理由并发送确认字段', async () => {
    sessionStorage.setItem('qingwaguagua_admin_session', JSON.stringify(session));
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === 'POST') return { ok: true, status: 200, headers: new Headers(), json: async () => ({ id: 'moment_1', status: 'hidden' }) };
      if (url.includes('/sticker-packs')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [], total: 0 }) };
      return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ id: 'moment_1', authorId: 'u_1', authorName: '测试用户', content: '待审核动态', mediaKind: 'none', media: [], visibility: 'public', likeCount: 0, commentCount: 0, status: 'published', createdAt: '2026-08-11T00:00:00Z' }], total: 1 }) };
    });
    vi.stubGlobal('fetch', fetchMock);
    window.history.replaceState({}, '', '/content-moderation');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '隐藏' }));
    const confirm = screen.getByRole('button', { name: '确认并记录' });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText('处置理由'), '举报复核确认违规');
    await user.click(confirm);
    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(true));
    const write = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST');
    expect(JSON.parse(String(write?.[1]?.body))).toEqual({ status: 'hidden', reason: '举报复核确认违规', confirmed: true });
  });

  it('朋友圈媒体类型和可见范围始终展示中文', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).includes('/moments')) return response({
        items: [{
          id: 'moment_image_1', authorId: 'u_10291', authorName: '林夏', content: '',
          mediaKind: 'image', media: [{ id: 'media_1' }, { id: 'media_2' }], visibility: 'public',
          likeCount: 2, commentCount: 1, status: 'published', createdAt: '2026-08-16T08:00:00Z',
        }],
        total: 1,
      });
      return liveFixture(input, init);
    }));
    window.history.replaceState({}, '', '/content-moderation');

    render(<App />);

    expect(await screen.findByText('[图片 × 2]')).toBeInTheDocument();
    expect(screen.getByText('所有好友')).toBeInTheDocument();
    expect(screen.queryByText('image')).not.toBeInTheDocument();
    expect(screen.queryByText('public')).not.toBeInTheDocument();
  });

  it('表情商店可以创建分类和表情包', async () => {
    window.history.replaceState({}, '', '/content-moderation');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', { name: '表情包' }));
    await user.click(await screen.findByRole('button', { name: '创建分类' }));
    const categoryConfirm = screen.getByRole('button', { name: '保存分类' });
    expect(categoryConfirm).toBeDisabled();
    await user.type(screen.getByLabelText('分类名称'), '节日');
    await user.type(screen.getByLabelText('操作理由'), '初始化节日分类');
    await user.click(categoryConfirm);
    expect(await screen.findByRole('button', { name: /节日/ })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '创建表情包' }));
    const packConfirm = screen.getByRole('button', { name: '保存表情包' });
    await user.type(screen.getByLabelText('表情包名称'), '新年祝福');
    await waitFor(() => expect(within(screen.getByLabelText('表情包封面图片')).getByRole('option', { name: 'new-year-cover.png · 28.0 KB' })).toBeInTheDocument());
    await user.selectOptions(screen.getByLabelText('表情包封面图片'), 'media_cover_20260815');
    expect(screen.queryByLabelText('封面媒体 ID')).not.toBeInTheDocument();
    await user.type(screen.getByLabelText('操作理由'), '创建运营表情包');
    expect(packConfirm).toBeEnabled();
    await user.click(packConfirm);
    expect(await screen.findByText('新年祝福')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '表情' }));
    const stickerImage = screen.getByLabelText('表情图片');
    await waitFor(() => expect(within(stickerImage).getByRole('option', { name: 'new-year-smile.webp · 18.0 KB' })).toBeInTheDocument());
    expect(screen.queryByLabelText('图片媒体 ID')).not.toBeInTheDocument();
    await user.selectOptions(stickerImage, 'media_sticker_20260815');
    expect(stickerImage).toHaveValue('media_sticker_20260815');
  });

  it('表情运营弹窗关闭前确认并保留未保存内容', async () => {
    window.history.replaceState({}, '', '/content-moderation');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', { name: '表情包' }));

    await user.click(await screen.findByRole('button', { name: '创建分类' }));
    await user.type(screen.getByLabelText('分类名称'), '节日专题');
    await user.type(screen.getByLabelText('操作理由'), '准备节日运营内容');
    await user.click(screen.getByRole('button', { name: '关闭' }));
    expect(screen.getByRole('heading', { name: '放弃未保存的分类？' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '继续编辑' }));
    expect(screen.getByLabelText('分类名称')).toHaveValue('节日专题');
    expect(screen.getByLabelText('操作理由')).toHaveValue('准备节日运营内容');
    await user.click(screen.getByRole('button', { name: '保存分类' }));

    await user.click(await screen.findByRole('button', { name: '创建表情包' }));
    await user.type(screen.getByLabelText('表情包名称'), '春节祝福');
    await waitFor(() => expect(within(screen.getByLabelText('表情包封面图片')).getByRole('option', { name: 'new-year-cover.png · 28.0 KB' })).toBeInTheDocument());
    await user.selectOptions(screen.getByLabelText('表情包封面图片'), 'media_cover_20260815');
    await user.type(screen.getByLabelText('描述'), '用于春节期间的祝福表情');
    await user.type(screen.getByLabelText('操作理由'), '春节运营排期');
    await user.click(screen.getByRole('button', { name: '取消' }));
    expect(screen.getByRole('heading', { name: '放弃未保存的表情包？' })).toBeInTheDocument();
    expect(screen.getByText('名称、封面、描述、状态和操作理由将全部丢失。')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '继续编辑' }));
    expect(screen.getByLabelText('表情包名称')).toHaveValue('春节祝福');
    expect(screen.getByLabelText('表情包封面图片')).toHaveValue('media_cover_20260815');
    expect(screen.getByLabelText('描述')).toHaveValue('用于春节期间的祝福表情');
    await user.click(screen.getByRole('button', { name: '取消' }));
    await user.click(screen.getByRole('button', { name: '放弃修改' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('系统运维角色可以发布客户端版本策略', async () => {
    sessionStorage.setItem('qingwaguagua_admin_session', JSON.stringify({ ...session, roleId: 'system_operator', roleName: '系统运维', permissions: ['settings.write', 'versions.write', 'operations.write'] }));
    const policy = { platform: 'android', minimumVersion: '1.0.0', latestVersion: '1.1.0', forceUpdate: false, rolloutPercentage: 100, releaseNotes: '', downloadUrl: 'https://download.example.com/app.apk', updatedBy: 'ops', updatedAt: '2026-08-11T00:00:00Z' };
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => ({ ok: true, status: 200, headers: new Headers(), json: async () => init?.method === 'PUT' ? policy : { items: [policy] } }));
    vi.stubGlobal('fetch', fetchMock);
    window.history.replaceState({}, '', '/client-versions');
    render(<App />);
    const user = userEvent.setup();
    const publish = await screen.findByRole('button', { name: '保存并发布' });
    expect(publish).toBeDisabled();
    expect(screen.getByText('当前策略与服务端一致')).toBeInTheDocument();
    await user.type(screen.getByLabelText('更新说明'), '修复消息同步问题');
    expect(publish).toBeEnabled();
    expect(screen.getByText('有未发布的版本策略更改')).toBeInTheDocument();
    await user.click(publish);
    await user.type(screen.getByLabelText('发布原因'), '发布单 REL-1024');
    await user.click(screen.getByRole('button', { name: '确认发布' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'PUT')).toBe(true));
    const write = fetchMock.mock.calls.find(([, init]) => init?.method === 'PUT');
    expect(JSON.parse(String(write?.[1]?.body))).toEqual(expect.objectContaining({ reason: '发布单 REL-1024', confirmed: true }));
  });

  it('服务端缺失平台版本策略时不伪装成线上默认数据', async () => {
    window.history.replaceState({}, '', '/client-versions');
    render(<App />);
    const user = userEvent.setup();

    expect(await screen.findByText('服务端尚未配置 Android 版本策略；以下内容仅是本地新建草稿，不是线上数据。')).toBeInTheDocument();
    expect(screen.getByText('尚未创建线上版本策略')).toBeInTheDocument();
    expect(screen.queryByText('当前策略与服务端一致')).not.toBeInTheDocument();

    const create = screen.getByRole('button', { name: '创建并发布' });
    expect(create).toBeDisabled();
    await user.type(screen.getByLabelText('更新说明'), '首次创建 Android 发布策略');
    expect(create).toBeEnabled();
    await user.click(create);
    expect(screen.getByRole('heading', { name: '创建并发布 Android 版本策略' })).toBeInTheDocument();
  });

  it('客户端版本策略在二次确认前完成本地规则校验', async () => {
    window.history.replaceState({}, '', '/client-versions');
    render(<App />);
    const user = userEvent.setup();
    const latest = await screen.findByLabelText('最新发布版本');
    await user.clear(latest);
    await user.type(latest, '0.9.0');
    await user.click(screen.getByRole('button', { name: '创建并发布' }));
    expect(screen.getByRole('alert')).toHaveTextContent('最新发布版本不能低于最低支持版本');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    await user.clear(latest);
    await user.type(latest, '1.1.0');
    await user.type(screen.getByLabelText('下载地址'), 'http://downloads.example.com/app.apk');
    await user.click(screen.getByRole('button', { name: '创建并发布' }));
    expect(screen.getByRole('alert')).toHaveTextContent('HTTPS 地址');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    await user.clear(screen.getByLabelText('下载地址'));
    await user.type(screen.getByLabelText('下载地址'), 'https://downloads.example.com/app.apk');
    await user.click(screen.getByRole('button', { name: '创建并发布' }));
    expect(screen.getByRole('heading', { name: '创建并发布 Android 版本策略' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '确认发布' })).toBeDisabled();
  });

  it('切换客户端平台前确认放弃尚未发布的修改', async () => {
    window.history.replaceState({}, '', '/client-versions');
    render(<App />);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('更新说明'), '尚未发布的 Android 修改');
    await user.click(screen.getByRole('tab', { name: 'iOS' }));
    expect(screen.getByRole('heading', { name: '放弃未发布的版本策略？' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Android' })).toHaveAttribute('aria-selected', 'true');
    await user.click(screen.getByRole('button', { name: '取消' }));
    expect(screen.getByLabelText('更新说明')).toHaveValue('尚未发布的 Android 修改');
    await user.click(screen.getByRole('tab', { name: 'iOS' }));
    await user.click(screen.getByRole('button', { name: '放弃更改并切换' }));
    expect(screen.getByRole('tab', { name: 'iOS' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByLabelText('更新说明')).toHaveValue('');
  });

  it('按平台展示客户端发布历史的完整策略快照', async () => {
    const policy = { platform: 'android', minimumVersion: '1.0.0', latestVersion: '1.2.0', forceUpdate: false, rolloutPercentage: 100, releaseNotes: '修复消息同步', downloadUrl: 'https://download.example.com/app.apk', updatedBy: 'ops-admin', updatedAt: '2026-08-17T01:57:00Z' };
    const release = { ...policy, id: 'release-1', reason: '发布单 REL-1025' };
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/client-versions/android/history')) return response({ items: [release], total: 1 });
      if (url.includes('/client-versions')) return response({ items: [policy] });
      return liveFixture(input);
    });
    vi.stubGlobal('fetch', fetchMock);
    window.history.replaceState({}, '', '/client-versions');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '发布历史' }));
    expect(await screen.findByText('v1.2.0')).toBeInTheDocument();
    expect(screen.getByText('发布单 REL-1025')).toBeInTheDocument();
    expect(screen.getAllByText('修复消息同步')).toHaveLength(2);
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/client-versions/android/history'))).toBe(true);
  });

  it('群通话记录展示完整参与状态和友好失败原因', async () => {
    sessionStorage.setItem('qingwaguagua_admin_session', JSON.stringify(session));
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true, status: 200, headers: new Headers(),
      json: async () => ({ items: [{
        id: 'call_group_1', conversationId: 'group_1', kind: 'group', callerId: 'u_1', participantIds: ['u_1', 'u_2', 'u_3'],
        joinedUserIds: ['u_1', 'u_2'], declinedUserIds: ['u_3'], leftUserIds: ['u_2'], mediaType: 'video', status: 'ended',
        endReason: 'media_failed', endedBy: 'u_2', invitedAt: '2026-08-13T00:00:00Z', durationSeconds: 12,
      }], total: 1 }),
    })));
    window.history.replaceState({}, '', '/calls');
    render(<App />);
    expect(await screen.findByText('3 人群通话')).toBeInTheDocument();
    expect(screen.getByText('u_1、u_2、u_3')).toBeInTheDocument();
    expect(screen.getByText('已加入 2 · 已拒绝 1 · 已离开 1')).toBeInTheDocument();
    expect(screen.getByText('媒体连接失败')).toBeInTheDocument();
    expect(screen.getByText('结束人 u_2')).toBeInTheDocument();
  });

  it('不同业务空态使用对应语义图标而不是统一搜索图标', async () => {
    window.history.replaceState({}, '', '/online');
    const onlineView = render(<App />);
    const onlineTitle = await screen.findByText('当前没有在线用户');
    expect(onlineTitle.closest('.state-box')?.querySelector('.lucide-wifi')).not.toBeNull();
    expect(onlineTitle.closest('.state-box')?.querySelector('.lucide-search')).toBeNull();
    onlineView.unmount();

    window.history.replaceState({}, '', '/reports');
    const reportView = render(<App />);
    const reportTitle = await screen.findByText('当前队列已处理完');
    expect(reportTitle.closest('.state-box')?.querySelector('.lucide-shield-check')).not.toBeNull();
    reportView.unmount();

    window.history.replaceState({}, '', '/calls');
    const callView = render(<App />);
    const callTitle = await screen.findByText('没有匹配的通话');
    expect(callTitle.closest('.state-box')?.querySelector('.lucide-phone-call')).not.toBeNull();
    callView.unmount();
  });

  it('统一展示 WuKongIM 节点并可切换到 LiveKit 房间', async () => {
    window.history.replaceState({}, '', '/im-infrastructure');
    render(<App />);
    expect(screen.getByRole('heading', { name: 'IM 基础设施' })).toBeInTheDocument();
    expect((await screen.findAllByText('v2.2.5-20260422')).length).toBeGreaterThan(0);
    expect(await screen.findByText('Prometheus 指标')).toBeInTheDocument();
    expect(screen.getByText('Trace 追踪')).toBeInTheDocument();
    expect(screen.getByText('Loki 日志')).toBeInTheDocument();
    expect(screen.getByText('压力测试模式')).toBeInTheDocument();
    const user = userEvent.setup();
    const overviewTab = screen.getByRole('tab', { name: '运行概览' });
    overviewTab.focus();
    await user.keyboard('{ArrowRight}');
    expect(screen.getByRole('tab', { name: '连接' })).toHaveFocus();
    expect(screen.getByRole('tab', { name: '连接' })).toHaveAttribute('aria-selected', 'true');
    await user.keyboard('{End}');
    expect(screen.getByRole('tab', { name: '音视频房间' })).toHaveFocus();
    expect(screen.getByRole('tab', { name: '音视频房间' })).toHaveAttribute('aria-selected', 'true');
    await user.click(screen.getByRole('tab', { name: '音视频房间' }));
    expect(await screen.findByLabelText('LiveKit 资源指标')).toBeInTheDocument();
    expect(screen.getByText('媒体进程资源')).toBeInTheDocument();
    expect(screen.getByText('近 5 分钟丢包')).toBeInTheDocument();
    expect((await screen.findAllByText('call_20260815_01')).length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: '关闭房间' })).toBeEnabled();
    await user.click(screen.getByRole('tab', { name: '插件' }));
    expect(screen.getByRole('heading', { name: '签名插件发布' })).toBeInTheDocument();
    expect(screen.getByText(/AI Receive 插件会被拒绝/)).toBeInTheDocument();
    expect(await screen.findByText('运行中')).toBeInTheDocument();
    await user.click(await screen.findByRole('button', { name: '运行日志' }));
    expect(screen.getByRole('heading', { name: '插件运行日志' })).toBeInTheDocument();
    expect(await screen.findByText('policy plugin ready')).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: '卸载' })).toBeDisabled();
  });

  it('基础设施缺失指标时显示未上报而不是虚假的零值和关闭状态', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/wukong/overview')) return response({ server_id: '1', version: 'v2.2.5' });
      if (url.includes('/wukong/settings')) return response({ logger: {} });
      if (url.includes('/wukong/nodes')) return response({ data: [] });
      return liveFixture(input, init);
    }));
    window.history.replaceState({}, '', '/im-infrastructure');
    render(<App />);

    expect((await screen.findAllByText('未上报')).length).toBeGreaterThanOrEqual(4);
    expect(screen.getByText('用户处理器未上报')).toBeInTheDocument();
    expect(screen.getByText('资源明细未上报')).toBeInTheDocument();
    expect(screen.getAllByText('状态未知')).toHaveLength(4);
    expect(screen.queryByText('0.0%')).not.toBeInTheDocument();
  });

  it('审计结果缺失时明确显示状态未知而不是成功', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => response({
      items: [{ id: 'audit_missing_result', action: 'settings.updated', target: 'global', createdAt: '2026-08-16T00:00:00Z' }],
      total: 1,
    })));
    window.history.replaceState({}, '', '/audit');
    render(<App />);

    const unknownResult = await screen.findByText('状态未知');
    expect(unknownResult).toBeInTheDocument();
    expect(within(unknownResult.closest('tr')!).queryByText('成功')).not.toBeInTheDocument();
    expect(screen.getByText('未提供')).toBeInTheDocument();
  });

  it('可在 IM 基础设施中管理系统账号', async () => {
    window.history.replaceState({}, '', '/im-infrastructure');
    render(<App />);
    const user = userEvent.setup();
    await user.click(screen.getByRole('tab', { name: '系统账号' }));
    expect(screen.getByRole('heading', { name: '系统账号' })).toBeInTheDocument();
    expect(await screen.findByText('系统通知')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '设为系统账号' })).toBeDisabled();
    await waitFor(() => expect(within(screen.getByLabelText('系统账号候选')).getByRole('option', { name: '林夏 · linxia' })).toBeInTheDocument());
    await user.selectOptions(screen.getByLabelText('系统账号候选'), 'u_10291');
    expect(screen.queryByLabelText('用户 UID')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '设为系统账号' })).toBeEnabled();
  });

  it('后台任务展示 WuKong 同步、Webhook 与对账状态', async () => {
    window.history.replaceState({}, '', '/operations');
    render(<App />);
    expect(screen.getByRole('heading', { name: '推送、备份、诊断、任务与权限' })).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: '备份状态' })).toBeInTheDocument();
    expect(screen.getByText('最近成功')).toBeInTheDocument();
    expect(screen.getByText('37 秒')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '客户端诊断' })).toBeInTheDocument();
    expect(screen.getByText('1380 ms（12 样本）')).toBeInTheDocument();
    expect(await screen.findByText('WuKong 同步队列')).toBeInTheDocument();
    expect(screen.getByText('WuKong Webhook 队列')).toBeInTheDocument();
    expect(screen.getByText(/已对账：12/)).toBeInTheDocument();
    expect(screen.getAllByText(/最老积压（秒）：0/)).toHaveLength(2);
    expect(screen.getByText('admin_1')).toBeInTheDocument();
    expect(screen.getByText('暂无角色权限定义')).toBeInTheDocument();
  });

  it('可编辑真实机器人命令菜单并要求填写审计理由', async () => {
    window.history.replaceState({}, '', '/im-infrastructure');
    render(<App />);
    const user = userEvent.setup();
    await user.click(screen.getByRole('tab', { name: '机器人' }));
    expect(screen.getByRole('heading', { name: '机器人' })).toBeInTheDocument();
    expect(await screen.findByText('使用帮助')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '编辑' }));
    expect(screen.getByRole('heading', { name: /配置机器人/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '保存配置' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: '添加菜单' }));
    await user.type(screen.getByLabelText('菜单名称 2'), '人工客服');
    await user.type(screen.getByLabelText('发送命令 2'), '转人工');
    await user.type(screen.getByLabelText('机器人配置理由'), '客服菜单更新单 CS-18');
    expect(screen.getByRole('button', { name: '保存配置' })).toBeEnabled();
    await user.click(screen.getByRole('button', { name: '关闭' }));
    expect(screen.getByRole('heading', { name: '放弃未保存的机器人配置？' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '继续编辑' }));
    expect(screen.getByLabelText('菜单名称 2')).toHaveValue('人工客服');
    expect(screen.getByLabelText('发送命令 2')).toHaveValue('转人工');
    expect(screen.getByLabelText('机器人配置理由')).toHaveValue('客服菜单更新单 CS-18');
    await user.click(screen.getByRole('button', { name: '保存配置' }));
    await waitFor(() => expect(screen.queryByRole('heading', { name: /配置机器人/ })).not.toBeInTheDocument());
    const write = vi.mocked(fetch).mock.calls.find(([input, init]) => String(input).includes('/wukong/robots/u_notice') && init?.method === 'PUT');
    expect(JSON.parse(String(write?.[1]?.body))).toEqual(expect.objectContaining({ confirmed: true, reason: '客服菜单更新单 CS-18', menus: expect.arrayContaining([expect.objectContaining({ cmd: '转人工', remark: '人工客服' })]) }));
  });

  it('频道运营覆盖成员、临时订阅与黑白名单入口', async () => {
    window.history.replaceState({}, '', '/business-channels');
    render(<App />);
    expect(screen.getByRole('heading', { name: '频道运营' })).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '运营管理' }));
    expect(screen.getByRole('heading', { name: '成员与临时订阅' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '黑白名单' })).toBeInTheDocument();
    expect(screen.getByLabelText('频道成员账号')).toBeInTheDocument();
    expect(screen.getByLabelText('名单账号')).toBeInTheDocument();
    expect(screen.queryByLabelText('成员用户 ID')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('名单用户 ID')).not.toBeInTheDocument();
    expect(await screen.findByText('运营人员')).toBeInTheDocument();
    const memberAccount = screen.getByLabelText('频道成员账号');
    await waitFor(() => expect(within(memberAccount).getByRole('option', { name: '林夏 · linxia' })).toBeInTheDocument());
    await user.selectOptions(memberAccount, 'u_10291');
    await user.click(screen.getByRole('button', { name: '添加' }));
    expect(screen.getByRole('heading', { name: '添加频道成员' })).toBeInTheDocument();
    const confirm = screen.getByRole('button', { name: '确认并记录' });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText('操作原因'), '临时活动订阅');
    expect(confirm).toBeEnabled();
  });

  it('创建频道从真实账号和已有社区中选择归属而不要求原始 ID', async () => {
    window.history.replaceState({}, '', '/business-channels');
    render(<App />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole('button', { name: '创建频道' }));
    const dialog = screen.getByRole('dialog', { name: '创建业务频道' });
    expect(within(dialog).getByLabelText('查找频道所有者')).toBeInTheDocument();
    expect(within(dialog).getByLabelText('频道所有者账号')).toBeInTheDocument();
    expect(within(dialog).queryByLabelText('所有者用户 ID')).not.toBeInTheDocument();

    const ownerOption = await within(dialog).findByRole('option', { name: '林夏 · linxia' });
    expect(ownerOption).toBeInTheDocument();
    await user.selectOptions(within(dialog).getByLabelText('频道所有者账号'), 'u_10291');
    await user.selectOptions(within(dialog).getByLabelText('频道类型'), '5');

    const parent = within(dialog).getByLabelText('话题所属社区');
    expect(parent).toBeInTheDocument();
    expect(within(parent).getByRole('option', { name: '产品交流社区' })).toBeInTheDocument();
    expect(within(dialog).queryByLabelText('父社区 ID')).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole('button', { name: '关闭' }));
    expect(screen.getByRole('heading', { name: '放弃未保存的频道？' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '继续编辑' }));
    expect(screen.getByLabelText('频道所有者账号')).toHaveValue('u_10291');
    expect(screen.getByLabelText('频道类型')).toHaveValue('5');
  });

  it('频道运营在本地拦截非法慢速模式和已过期的临时订阅', async () => {
    window.history.replaceState({}, '', '/business-channels');
    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '运营管理' }));

    fireEvent.change(screen.getByLabelText('慢速模式秒数'), { target: { value: '90000' } });
    await user.click(screen.getByRole('button', { name: '保存慢速模式' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('慢速模式必须是 0 到 86400 之间的整数秒');
    expect(screen.queryByRole('heading', { name: '更新慢速模式' })).not.toBeInTheDocument();

    const memberAccount = screen.getByLabelText('频道成员账号');
    await waitFor(() => expect(within(memberAccount).getByRole('option', { name: '江宁 · jiangning' })).toBeInTheDocument());
    await user.selectOptions(memberAccount, 'u_10288');
    fireEvent.change(screen.getByLabelText('订阅到期时间'), { target: { value: '2020-01-01T08:00' } });
    await user.click(screen.getByRole('button', { name: '添加' }));
    expect(await screen.findByText('临时订阅到期时间必须晚于当前时间')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '添加频道成员' })).not.toBeInTheDocument();
  });

  it('客服工作台提供队列认领、转接和结束处置', async () => {
    window.history.replaceState({}, '', '/support-workbench');
    render(<App />);
    expect(screen.getByRole('heading', { name: '客服工作台' })).toBeInTheDocument();
    expect(await screen.findByText('账号登录问题')).toBeInTheDocument();
    const user = userEvent.setup();
    await user.selectOptions(screen.getByLabelText('目标客服'), 'u_support_2');
    await user.click(screen.getByRole('button', { name: '转接' }));
    expect(screen.getByRole('heading', { name: '转接客服会话' })).toBeInTheDocument();
    const confirm = screen.getByRole('button', { name: '确认并记录' });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText('操作原因'), '升级到高级客服');
    expect(confirm).toBeEnabled();
  });

  it('客服坐席从真实账号与真实技能组中选择，不要求手填技术 ID', async () => {
    window.history.replaceState({}, '', '/support-workbench');
    render(<App />);
    const user = userEvent.setup();

    await user.click(screen.getByRole('tab', { name: '客服坐席' }));
    await screen.findByRole('option', { name: '林夏 · linxia' });
    expect(screen.getByLabelText('客服坐席账号')).toBeInTheDocument();
    expect(screen.queryByLabelText('用户 ID')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('技能组 ID')).not.toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText('客服坐席账号'), 'u_10291');
    await user.click(screen.getByRole('checkbox', { name: /综合咨询/ }));
    expect(screen.getByRole('button', { name: '保存坐席' })).toBeEnabled();
  });

  it('客服配置在本地拦截越界并发和缺失技能组', async () => {
    window.history.replaceState({}, '', '/support-workbench');
    render(<App />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole('tab', { name: '技能组' }));
    await user.type(screen.getByLabelText('名称'), '大客户支持');
    fireEvent.change(screen.getByLabelText('每坐席并发'), { target: { value: '101' } });
    await user.click(screen.getByRole('button', { name: '保存技能组' }));
    expect(await screen.findByText('每坐席并发必须是 1 到 100 之间的整数')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '创建客服技能组' })).not.toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: '客服坐席' }));
    await user.click(screen.getByRole('button', { name: '放弃修改并继续' }));
    await screen.findByRole('option', { name: '林夏 · linxia' });
    await user.selectOptions(screen.getByLabelText('客服坐席账号'), 'u_10291');
    const skill = screen.getByRole('checkbox', { name: /综合咨询/ });
    await user.click(skill);
    fireEvent.change(screen.getByLabelText('总并发上限'), { target: { value: '0' } });
    await user.click(screen.getByRole('button', { name: '保存坐席' }));
    expect(await screen.findByText('总并发上限必须是 1 到 100 之间的整数')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '保存客服坐席' })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('总并发上限'), { target: { value: '5' } });
    await user.click(skill);
    expect(screen.getByRole('button', { name: '保存坐席' })).toBeDisabled();
  });

  it('客服配置切换页签前确认并保留未保存内容', async () => {
    window.history.replaceState({}, '', '/support-workbench');
    render(<App />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole('tab', { name: '技能组' }));
    await user.type(screen.getByLabelText('名称'), 'VIP 客服');
    await user.type(screen.getByLabelText('描述'), '服务重点客户');
    await user.click(screen.getByRole('tab', { name: '客服坐席' }));
    expect(screen.getByRole('heading', { name: '放弃未保存的客服配置？' })).toBeInTheDocument();
    expect(screen.getByText('技能组名称、路由策略和并发上限将恢复到上次保存状态。')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '取消' }));
    expect(screen.getByRole('tab', { name: '技能组' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByLabelText('名称')).toHaveValue('VIP 客服');
    expect(screen.getByLabelText('描述')).toHaveValue('服务重点客户');

    await user.click(screen.getByRole('tab', { name: '客服坐席' }));
    await user.click(screen.getByRole('button', { name: '放弃修改并继续' }));
    expect(screen.getByRole('tab', { name: '客服坐席' })).toHaveAttribute('aria-selected', 'true');
    await screen.findByRole('option', { name: '林夏 · linxia' });
    await user.selectOptions(screen.getByLabelText('客服坐席账号'), 'u_10291');
    await user.click(screen.getByRole('checkbox', { name: /综合咨询/ }));
    await user.click(screen.getByRole('tab', { name: '会话队列' }));
    expect(screen.getByRole('heading', { name: '放弃未保存的客服配置？' })).toBeInTheDocument();
    expect(screen.getByText('坐席账号、技能组和接待状态将恢复到上次保存状态。')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '取消' }));
    expect(screen.getByLabelText('客服坐席账号')).toHaveValue('u_10291');
    expect(screen.getByRole('checkbox', { name: /综合咨询/ })).toBeChecked();
  });
});
