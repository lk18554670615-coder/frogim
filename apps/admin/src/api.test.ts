import { afterEach, describe, expect, it, vi } from 'vitest';
import { getApi, loginAdmin } from './api';

describe('live API adapter', () => {
  afterEach(() => { vi.unstubAllGlobals(); });

  it('消费服务端 items/total/nextCursor 而不在本地再切片', async () => {
    const items = Array.from({ length: 20 }, (_, index) => ({ id: `u_${index}`, name: `用户${index}`, banned: index === 0, createdAt: '2026-07-31T00:00:00Z' }));
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ items, total: 25, nextCursor: 'cursor-page-2' }) }));
    vi.stubGlobal('fetch', fetchMock);
    const page = await getApi('token').getUsers('林', 'active', 1, 20, 'cursor-page-1');
    expect(page.items).toHaveLength(20);
    expect(page.total).toBe(25);
    expect(page.hasNext).toBe(true);
    expect(page.nextCursor).toBe('cursor-page-2');
    expect(page.items[0].nickname).toBe('用户0');
    expect(page.items[0].status).toBe('banned');
    expect(String(fetchMock.mock.calls[0][0])).toContain('q=%E6%9E%97&status=active&cursor=cursor-page-1&limit=20');
  });

  it('不对旧响应做本地假分页', async () => {
    const items = Array.from({ length: 25 }, (_, index) => ({ id: `u_${index}`, name: `用户${index}` }));
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ items }) })));
    const page = await getApi('token').getUsers('', '', 1, 20);
    expect(page.items).toHaveLength(25);
    expect(page.total).toBe(25);
    expect(page.hasNext).toBe(false);
  });

  it('消费真实用户详情、群详情和群成员分页接口', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/users/u_1')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ user: { id: 'u_1', name: '林夏', phone: '13800001001', handle: 'linxia', signature: '保持联系', gender: 'female', handleChangeCount: 1, createdAt: '2026-08-01T08:00:00Z' }, deviceCount: 2, friendCount: 18, groupCount: 4, handleChangesUsed: 1, handleChangesRemaining: 1 }) };
      if (url.includes('/groups/g_1/members')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ conversationId: 'g_1', userId: 'u_1', name: '林夏', handle: 'linxia', role: 'owner', lastReadSeq: 12, lastDeliveredSeq: 12, joinedAt: '2026-08-01T08:00:00Z' }], total: 25, nextCursor: '20' }) };
      return { ok: true, status: 200, headers: new Headers(), json: async () => ({ id: 'g_1', title: '产品交流群', ownerId: 'u_1', announcement: '文明交流', announcementVersion: 2, joinPolicy: 'approval', allowMemberAddFriend: true, messageCount: 1280, memberCount: 25 }) };
    });
    vi.stubGlobal('fetch', fetchMock);
    const api = getApi('token');
    expect(await api.getUserOverview('u_1')).toEqual(expect.objectContaining({ signature: '保持联系', gender: 'female', deviceCount: 2, friendCount: 18, groupCount: 4, handleChangesRemaining: 1 }));
    expect(await api.getGroupOverview('g_1')).toEqual(expect.objectContaining({ title: '产品交流群', ownerId: 'u_1', memberCount: 25 }));
    const members = await api.getGroupMembers('g_1', '林', 1, 20, '0');
    expect(members.items).toEqual([expect.objectContaining({ userId: 'u_1', role: 'owner', handle: 'linxia' })]);
    expect(members.nextCursor).toBe('20');
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContainEqual(expect.stringContaining('/groups/g_1/members?q=%E6%9E%97&cursor=0&limit=20'));
  });

  it('群成员治理接口携带动作、确认和审计理由', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({ ok: true, status: 204, headers: new Headers(), json: async () => ({}) }));
    vi.stubGlobal('fetch', fetchMock);
    const api = getApi('token');
    await api.updateGroupMember('g_1', 'u_1', { action: 'role', role: 'admin' }, '运营工单 GROUP-3');
    await api.updateGroupMember('g_1', 'u_1', { action: 'mute', mutedUntil: '2026-08-16T10:00:00.000Z' }, '群内违规发言');
    await api.removeGroupMember('g_1', 'u_1', '确认移出违规成员');
    const writes = fetchMock.mock.calls.map(([input, init]) => ({ url: String(input), method: init?.method, body: JSON.parse(String(init?.body)) as Record<string, unknown> }));
    expect(writes).toEqual([
      expect.objectContaining({ method: 'PATCH', body: { action: 'role', role: 'admin', reason: '运营工单 GROUP-3', confirmed: true } }),
      expect.objectContaining({ method: 'PATCH', body: { action: 'mute', mutedUntil: '2026-08-16T10:00:00.000Z', reason: '群内违规发言', confirmed: true } }),
      expect.objectContaining({ method: 'DELETE', body: { reason: '确认移出违规成员', confirmed: true } }),
    ]);
    expect(writes.every((write) => write.url.includes('/groups/g_1/members/u_1'))).toBe(true);
  });

  it('群封禁列表、历史、黑名单和治理请求使用真实服务端接口', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/groups/g_1/messages?')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({
        items: [{ id: '901', conversationSeq: 9, senderId: 'u_1', sender: { id: 'u_1', name: '林夏', phone: '13800001001' }, type: 'text', body: { content: '真实群消息' }, createdAt: '2026-08-17T08:00:00Z' }],
        nextBeforeSeq: 8,
      }) };
      if (url.endsWith('/groups/g_1/blacklist') && !init?.method) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ user: { id: 'u_2', name: '江宁' }, operatorId: 'admin_1', operatorName: '平台管理员', remark: '广告账号', createdAt: '2026-08-17T08:00:00Z' }] }) };
      if (url.includes('/groups?')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ id: 'g_1', title: '产品交流群', ownerId: 'u_1', owner: { id: 'u_1', name: '林夏' }, memberCount: 2, messageCount: 9, status: 'banned', banned: true, createdAt: '2026-08-01T08:00:00Z' }], total: 1 }) };
      return { ok: true, status: 200, headers: new Headers(), json: async () => ({}) };
    });
    vi.stubGlobal('fetch', fetchMock);
    const api = getApi('token');

    const groups = await api.getGroups('产品', '', 1, 20, '', 'banned');
    expect(groups.items[0]).toEqual(expect.objectContaining({ id: 'g_1', banned: true, owner: expect.objectContaining({ id: 'u_1' }) }));
    expect(String(fetchMock.mock.calls[0][0])).toContain('scope=banned');
    expect((await api.getGroupMessages('g_1', 10, 20)).items[0]).toEqual(expect.objectContaining({ body: { content: '真实群消息' }, sender: expect.objectContaining({ phone: '13800001001' }) }));
    expect((await api.getGroupBlacklist('g_1'))[0]).toEqual(expect.objectContaining({ remark: '广告账号', operatorName: '平台管理员' }));

    await api.sendGroupMessage('g_1', 'u_1', '群公告提醒', '运营工单 GROUP-11');
    await api.recallGroupMessage('g_1', '901', '清理违规消息');
    await api.addGroupBlacklist('g_1', 'u_2', '广告账号', '多次发布广告');
    await api.removeGroupBlacklist('g_1', 'u_2', '申诉通过');
    await api.setGroupMuteAll('g_1', true, '紧急治理');
    await api.setGroupBan('g_1', true, '严重违规');
    await api.setGroupBan('g_1', false, '复核通过');

    const writes = fetchMock.mock.calls.filter(([, init]) => Boolean(init?.method)).map(([input, init]) => ({ url: String(input), method: init?.method, body: JSON.parse(String(init?.body)) }));
    expect(writes).toEqual(expect.arrayContaining([
      expect.objectContaining({ url: expect.stringContaining('/groups/g_1/messages'), method: 'POST', body: { senderUid: 'u_1', content: '群公告提醒', reason: '运营工单 GROUP-11', confirmed: true } }),
      expect.objectContaining({ url: expect.stringContaining('/groups/g_1/messages/901/recall'), method: 'POST', body: { reason: '清理违规消息', confirmed: true } }),
      expect.objectContaining({ url: expect.stringContaining('/groups/g_1/blacklist/u_2'), method: 'PUT', body: { remark: '广告账号', reason: '多次发布广告', confirmed: true } }),
      expect.objectContaining({ url: expect.stringContaining('/groups/g_1/blacklist/u_2'), method: 'DELETE', body: { reason: '申诉通过', confirmed: true } }),
      expect.objectContaining({ url: expect.stringContaining('/groups/g_1/mute-all'), method: 'POST', body: { muted: true, reason: '紧急治理', confirmed: true } }),
      expect.objectContaining({ url: expect.stringContaining('/groups/g_1/ban'), method: 'POST', body: { reason: '严重违规', confirmed: true } }),
      expect.objectContaining({ url: expect.stringContaining('/groups/g_1/unban'), method: 'POST', body: { reason: '复核通过', confirmed: true } }),
    ]));
  });

  it('保留服务端错误码、信息和追踪号', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false, status: 409, headers: new Headers({ 'x-request-id': 'req-conflict' }),
      json: async () => ({ error: { code: 'CONFLICT', message: '资源状态已变化' } }),
    })));
    await expect(getApi('token').getDashboard()).rejects.toEqual(expect.objectContaining({ status: 409, code: 'CONFLICT', message: '资源状态已变化', requestId: 'req-conflict' }));
  });

  it('按错误码把服务端英文错误转换为可执行的中文提示', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false, status: 503, headers: new Headers({ 'x-request-id': 'req-service' }),
      json: async () => ({ error: { code: 'WUKONG_UNAVAILABLE', message: 'wukong service is unavailable' } }),
    })));
    await expect(getApi('token').getDashboard()).rejects.toEqual(expect.objectContaining({
      status: 503,
      code: 'WUKONG_UNAVAILABLE',
      message: '即时通信服务暂时不可用，请稍后重试',
      requestId: 'req-service',
    }));
  });

  it('已登录接口返回 401 时只发出会话失效事件并保留结构化错误', async () => {
    const unauthorized = vi.fn();
    window.addEventListener('nexachat:unauthorized', unauthorized);
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false, status: 401, headers: new Headers({ 'x-request-id': 'req-expired' }),
      json: async () => ({ error: { code: 'UNAUTHENTICATED', message: 'token expired' } }),
    })));

    await expect(getApi('expired-token').getDashboard()).rejects.toEqual(expect.objectContaining({
      status: 401,
      code: 'UNAUTHENTICATED',
      message: '登录状态已失效，请重新登录',
      requestId: 'req-expired',
    }));
    expect(unauthorized).toHaveBeenCalledTimes(1);
    window.removeEventListener('nexachat:unauthorized', unauthorized);
  });

  it('管理员登录失败不会误触发现有会话失效事件', async () => {
    const unauthorized = vi.fn();
    window.addEventListener('nexachat:unauthorized', unauthorized);
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false, status: 401, headers: new Headers({ 'x-request-id': 'req-login' }),
      json: async () => ({ error: { code: 'UNAUTHENTICATED', message: 'invalid credentials' } }),
    })));

    await expect(loginAdmin('ops', 'wrong-password')).rejects.toEqual(expect.objectContaining({
      status: 401,
      code: 'INVALID_CREDENTIALS',
      message: '账号或密码不正确',
      requestId: 'req-login',
    }));
    expect(unauthorized).not.toHaveBeenCalled();
    window.removeEventListener('nexachat:unauthorized', unauthorized);
  });

  it('WuKongIM 管理接口故障不会退化为含糊的通用错误', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false, status: 502, headers: new Headers({ 'x-request-id': 'req-wukong-manager' }),
      json: async () => ({ error: { code: 'WUKONG_UPSTREAM_ERROR', message: 'WuKongIM management request failed' } }),
    })));
    await expect(getApi('token').getWukongDevices()).rejects.toEqual(expect.objectContaining({
      status: 502,
      code: 'WUKONG_UPSTREAM_ERROR',
      message: '即时通信管理服务暂时不可用，请检查服务状态后重试',
      requestId: 'req-wukong-manager',
    }));
  });

  it('适配数据库管理员登录会话和动态权限', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({
      ok: true, status: 200, headers: new Headers(),
      json: async () => ({ data: { accessToken: 'jwt', id: 'admin_1', username: 'ops', email: 'ops@example.com', displayName: '运营管理员', roleId: 'custom_ops', roleName: '运营值班', permissions: ['users.write'], expiresIn: 600 } }),
    }));
    vi.stubGlobal('fetch', fetchMock);
    const result = await loginAdmin('ops', 'password');
    expect(result).toEqual(expect.objectContaining({ token: 'jwt', id: 'admin_1', displayName: '运营管理员', roleId: 'custom_ops', roleName: '运营值班', permissions: ['users.write'] }));
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({ username: 'ops', password: 'password' });
  });

  it('传递真实举报处置动作并消费处理结果', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({
      ok: true, status: 200, headers: new Headers(),
      json: async () => ({ status: 'resolved', action: 'delete_message' }),
    }));
    vi.stubGlobal('fetch', fetchMock);
    const result = await getApi('token').resolveReport('r_1', 'delete_message', '确认为违规消息');
    expect(result).toEqual({ status: 'resolved', action: 'delete_message' });
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({ action: 'delete_message', reason: '确认为违规消息', confirmed: true });
  });

  it('发布客户端版本策略时携带原因和显式确认', async () => {
    const response = { platform: 'android' as const, minimumVersion: '1.2.0', latestVersion: '1.3.0', forceUpdate: true, rolloutPercentage: 25, releaseNotes: '安全更新', downloadUrl: 'https://download.example.com/app.apk', updatedBy: 'ops', updatedAt: '2026-08-11T00:00:00Z' };
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({ ok: true, status: 200, headers: new Headers(), json: async () => response }));
    vi.stubGlobal('fetch', fetchMock);
    const updated = await getApi('token').updateClientVersion(response, '发布单 REL-1024');
    expect(updated).toEqual(expect.objectContaining({ platform: 'android', rolloutPercentage: 25, forceUpdate: true }));
    expect(String(fetchMock.mock.calls[0][0])).toContain('/client-versions/android');
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual(expect.objectContaining({ minimumVersion: '1.2.0', latestVersion: '1.3.0', reason: '发布单 REL-1024', confirmed: true }));
  });

  it('客户端版本策略响应缺字段时拒绝伪造 Android 默认策略', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      headers: new Headers(),
      json: async () => ({ items: [{ minimumVersion: '1.0.0', latestVersion: '1.1.0', forceUpdate: false, rolloutPercentage: 100 }] }),
    })));

    await expect(getApi('token').getClientVersions()).rejects.toThrow('缺少有效平台');
  });

  it('消费用户关系与设备接口并通过 WuKongIM 发送系统消息', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/users/u_1/friends')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ user: { id: 'u_2', name: '江宁', handle: 'jiangning' }, remark: '产品同事', tags: ['产品'], relationshipCreatedAt: '2026-08-01T08:00:00Z', relationshipUpdatedAt: '2026-08-02T08:00:00Z' }] }) };
      if (url.endsWith('/users/u_1/blocks')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ user: { id: 'u_3', name: '屏蔽账号' }, remark: '旧备注', blockedAt: '2026-08-03T08:00:00Z' }] }) };
      if (url.endsWith('/users/u_1/devices')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ userId: 'u_1', installationId: 'install_1', platform: 'android', deviceName: 'Pixel 9', deviceModel: 'tokay', osVersion: 'Android 16', appVersion: '1.0.0', firstSeenAt: '2026-08-15T08:00:00Z', lastSeenAt: '2026-08-16T08:00:00Z' }], pushRegistrations: [{ id: 'device_1', userId: 'u_1', platform: 'android', provider: 'fcm', notificationsEnabled: true, previewEnabled: false, soundEnabled: true, vibrationEnabled: true, updatedAt: '2026-08-16T08:00:00Z' }] }) };
      expect(init?.method).toBe('POST');
      return { ok: true, status: 201, headers: new Headers(), json: async () => ({ targetUid: 'u_1', senderUid: 'u_notice', conversationId: 'conv_1', messageId: 88, clientMsgNo: 'admin-notice-1' }) };
    });
    vi.stubGlobal('fetch', fetchMock);
    const api = getApi('token');
    expect(await api.getUserFriends('u_1')).toEqual([expect.objectContaining({ user: expect.objectContaining({ id: 'u_2' }), remark: '产品同事', tags: ['产品'] })]);
    expect(await api.getUserBlockedUsers('u_1')).toEqual([expect.objectContaining({ user: expect.objectContaining({ id: 'u_3' }), remark: '旧备注' })]);
    expect(await api.getUserDevices('u_1')).toEqual({ items: [expect.objectContaining({ installationId: 'install_1', platform: 'android', deviceName: 'Pixel 9' })], pushRegistrations: [expect.objectContaining({ id: 'device_1', notificationsEnabled: true })] });
    expect(await api.sendUserSystemMessage('u_1', 'u_notice', '版本升级通知', '运营工单 OPS-18')).toEqual(expect.objectContaining({ messageId: '88', senderUid: 'u_notice' }));
    const write = fetchMock.mock.calls[fetchMock.mock.calls.length - 1];
    expect(String(write?.[0])).toContain('/users/u_1/system-message');
    expect(JSON.parse(String(write?.[1]?.body))).toEqual({ senderUid: 'u_notice', content: '版本升级通知', reason: '运营工单 OPS-18', confirmed: true });
  });

  it('新增用户和管理员聊天撤回均携带确认与理由', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/users') && init?.method === 'POST') return { ok: true, status: 201, headers: new Headers(), json: async () => ({ item: { id: 'u_new', name: '新用户', phone: '13800138000', handle: 'gg_new', gender: 'female' } }) };
      if (url.includes('/messages?')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ conversationId: 'conv_1', participants: { u_1: { id: 'u_1', name: '甲' }, u_2: { id: 'u_2', name: '乙' } }, items: [{ id: 'm_1', conversationId: 'conv_1', conversationSeq: 9, senderId: 'u_1', type: 'text', body: { content: '正文' }, createdAt: '2026-08-17T08:00:00Z', encrypted: false }], nextBeforeSeq: 8 }) };
      return { ok: true, status: 200, headers: new Headers(), json: async () => ({ recalled: true }) };
    });
    vi.stubGlobal('fetch', fetchMock); const api = getApi('token');
    expect(await api.createUser({ phone: '13800138000', name: '新用户', password: 'StrongPass123!', gender: 'female' }, '工单 USER-1')).toEqual(expect.objectContaining({ id: 'u_new', gender: 'female' }));
    expect(await api.getUserFriendMessages('u_1', 'u_2', 10, 20)).toEqual(expect.objectContaining({ conversationId: 'conv_1', nextBeforeSeq: 8, items: [expect.objectContaining({ id: 'm_1', encrypted: false })] }));
    await api.recallUserFriendMessage('u_1', 'u_2', 'm_1', '违规内容');
    const bodies = fetchMock.mock.calls.filter(([, init]) => init?.body).map(([, init]) => JSON.parse(String(init?.body)) as Record<string, unknown>);
    expect(bodies).toContainEqual({ phone: '13800138000', name: '新用户', password: 'StrongPass123!', gender: 'female', reason: '工单 USER-1', confirmed: true });
    expect(bodies).toContainEqual({ reason: '违规内容', confirmed: true });
  });

  it('新历史接口未上线时从真实审计记录读取发布快照', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).includes('/client-versions/android/history')) return { ok: false, status: 404, headers: new Headers(), json: async () => ({ error: { code: 'NOT_FOUND', message: '接口未上线' } }) };
      return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ id: 'audit-release-1', actorId: 'ops', action: 'client_version_policy.updated', targetType: 'client_version_policy', targetId: 'android', metadata: { minimumVersion: '1.0.0', latestVersion: '1.2.0', forceUpdate: false, rolloutPercentage: 50, releaseNotes: '发布说明', downloadUrl: 'https://download.example.com/app.apk', reason: '灰度发布' }, createdAt: '2026-08-17T00:00:00Z' }] }) };
    });
    vi.stubGlobal('fetch', fetchMock);
    const page = await getApi('token').getClientVersionHistory('android');
    expect(page.items).toEqual([expect.objectContaining({ id: 'audit-release-1', platform: 'android', latestVersion: '1.2.0', rolloutPercentage: 50, reason: '灰度发布', updatedBy: 'ops' })]);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('审核朋友圈时携带目标状态、原因和显式确认', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ id: 'moment_1', status: 'hidden' }) }));
    vi.stubGlobal('fetch', fetchMock);
    await getApi('token').moderateMoment('moment_1', 'hidden', '举报复核确认违规');
    expect(String(fetchMock.mock.calls[0][0])).toContain('/moments/moment_1/moderate');
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({ status: 'hidden', reason: '举报复核确认违规', confirmed: true });
  });

  it('审核表情包时适配返回值并携带审计信息', async () => {
    const response = { id: 'pack_1', name: '节日表情', categoryName: '节日', status: 'disabled', items: [{ id: 'sticker_1' }], createdBy: 'creator', reviewedBy: 'moderator', reviewReason: '版权投诉', updatedAt: '2026-08-11T00:00:00Z' };
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({ ok: true, status: 200, headers: new Headers(), json: async () => response }));
    vi.stubGlobal('fetch', fetchMock);
    const reviewed = await getApi('token').reviewStickerPack('pack_1', 'disabled', '版权投诉');
    expect(reviewed).toEqual(expect.objectContaining({ id: 'pack_1', status: 'disabled', itemCount: 1, reviewReason: '版权投诉' }));
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({ status: 'disabled', reason: '版权投诉', confirmed: true });
  });

  it('表情商店运营使用分类、表情包和表情项写入契约', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/sticker-categories') && !init?.method) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ id: 'daily', name: '日常', sortOrder: 10, enabled: true }] }) };
      if (url.endsWith('/sticker-categories')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ item: { id: 'festival', name: '节日', sortOrder: 20, enabled: true } }) };
      if (url.endsWith('/sticker-packs')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ item: { id: 'pack_1', categoryId: 'festival', categoryName: '节日', name: '新年', description: '新年表情', coverMediaId: 'media_cover', status: 'reviewing', sortOrder: 30, items: [] } }) };
      return { ok: true, status: 200, headers: new Headers(), json: async () => ({ item: { id: 'sticker_1', packId: 'pack_1', name: '恭喜', mediaId: 'media_sticker', emoji: '🎉', status: 'published', sortOrder: 10 } }) };
    });
    vi.stubGlobal('fetch', fetchMock);
    const api = getApi('token');
    expect(await api.getStickerCategories()).toEqual([{ id: 'daily', name: '日常', sortOrder: 10, enabled: true }]);
    await api.saveStickerCategory({ name: '节日', sortOrder: 20, enabled: true }, '运营分类初始化');
    await api.saveStickerPack({ categoryId: 'festival', name: '新年', description: '新年表情', coverMediaId: 'media_cover', status: 'reviewing', sortOrder: 30 }, '运营表情包初始化');
    await api.saveStickerItem('pack_1', { name: '恭喜', mediaId: 'media_sticker', emoji: '🎉', status: 'published', sortOrder: 10 }, '添加首个表情');
    const writes = fetchMock.mock.calls.filter(([, init]) => init?.method).map(([input, init]) => ({ url: String(input), method: init?.method, body: JSON.parse(String(init?.body)) as Record<string, unknown> }));
    expect(writes).toContainEqual(expect.objectContaining({ url: expect.stringMatching(/\/sticker-categories$/), method: 'POST', body: expect.objectContaining({ name: '节日', reason: '运营分类初始化', confirmed: true }) }));
    expect(writes).toContainEqual(expect.objectContaining({ url: expect.stringMatching(/\/sticker-packs$/), method: 'POST', body: expect.objectContaining({ coverMediaId: 'media_cover', status: 'reviewing', reason: '运营表情包初始化', confirmed: true }) }));
    expect(writes).toContainEqual(expect.objectContaining({ url: expect.stringMatching(/\/sticker-packs\/pack_1\/items$/), method: 'POST', body: expect.objectContaining({ mediaId: 'media_sticker', reason: '添加首个表情', confirmed: true }) }));
  });

  it('适配 WuKong 与 LiveKit 管理数据且所有处置携带确认理由', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/wukong/robots/') && init?.method === 'PUT') return { ok: true, status: 200, headers: new Headers(), json: async () => ({ item: { userId: 'u_notice', name: '系统通知', username: 'service_helper', placeholder: '请选择服务', enabled: true, inlineOn: false, version: 2, menus: [{ cmd: '转人工', remark: '人工客服', type: 'command' }], updatedBy: 'ops', reason: '客服菜单更新', updatedAt: '2026-08-16T00:00:00Z' } }) };
      if (url.includes('/wukong/system-users/') && init?.method === 'PUT') return { ok: true, status: 202, headers: new Headers(), json: async () => ({ item: { userId: 'u_notice', name: '系统通知', enabled: false, syncStatus: 'pending', updatedBy: 'ops', reason: '改回普通账号', updatedAt: '2026-08-12T00:00:00Z' } }) };
      if (init?.method) return { ok: true, status: 204, headers: new Headers(), json: async () => ({}) };
      if (url.includes('/wukong/overview')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ server_id: '1', version: 'v2.2.5', connections: 7, retry_queue: 0 }) };
      if (url.includes('/wukong/settings')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ logger: { trace_on: 0, loki_on: 1 }, prometheus_on: 1, stress_on: 0 }) };
      if (url.includes('/wukong/nodes')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ data: [{ id: 1, online: 1, is_leader: 1, slot_count: 64, slot_leader_count: 64 }] }) };
      if (url.includes('/wukong/devices')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ data: [{ uid: 'u1', device_flag: 1, device_level: 1, token_on: 1 }] }) };
      if (url.includes('/wukong/robots')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ userId: 'u_notice', name: '系统通知', username: 'service_helper', placeholder: '请选择服务', enabled: true, inlineOn: false, version: 1, menus: [{ cmd: '帮助', remark: '使用帮助', type: 'command' }], updatedBy: 'ops', reason: '客服入口', updatedAt: '2026-08-12T00:00:00Z' }] }) };
      if (url.includes('/wukong/system-users')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ userId: 'u_notice', name: '系统通知', enabled: true, syncStatus: 'synced', updatedBy: 'ops', reason: '通知账号', updatedAt: '2026-08-12T00:00:00Z' }] }) };
      if (url.includes('/livekit/metrics')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ healthy: true, activeRooms: 1, activeParticipants: 2, cpuPercent: 3.5, residentMemoryBytes: 104857600, networkReceiveBytesPerSecond: 2048, networkTransmitBytesPerSecond: 4096, packetLossPercent: 0.25, participantJoinsLastHour: 12, roomsCompletedLastHour: 4, sampledAt: '2026-08-13T00:00:00Z' }) };
      if (url.includes('/livekit/rooms')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ sid: 'RM_1', name: 'call_1', participantCount: 2, publisherCount: 1, maxParticipants: 9, activeRecording: false }] }) };
      return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [] }) };
    });
    vi.stubGlobal('fetch', fetchMock);
    const api = getApi('token');
    expect(await api.getWukongOverview()).toEqual(expect.objectContaining({ serverId: '1', connections: 7, cpu: null, inMessages: null }));
    expect(await api.getWukongSettings()).toEqual({ traceEnabled: false, lokiEnabled: true, prometheusEnabled: true, stressEnabled: false });
    expect(await api.getWukongNodes()).toEqual([expect.objectContaining({ id: 1, online: true, leader: true })]);
    expect(await api.getWukongDevices('u1', 1)).toEqual([expect.objectContaining({ uid: 'u1', deviceFlag: 1, tokenPresent: true })]);
    expect(await api.getWukongSystemUsers()).toEqual([expect.objectContaining({ userId: 'u_notice', enabled: true, syncStatus: 'synced' })]);
    expect(await api.getWukongRobots()).toEqual([expect.objectContaining({ userId: 'u_notice', username: 'service_helper', version: 1, menus: [{ cmd: '帮助', remark: '使用帮助', type: 'command' }] })]);
    expect(await api.getLiveKitRooms()).toEqual([expect.objectContaining({ name: 'call_1', participantCount: 2, maxParticipants: 9 })]);
    expect(await api.getLiveKitMetrics()).toEqual(expect.objectContaining({ healthy: true, activeRooms: 1, activeParticipants: 2, cpuPercent: 3.5, packetLossPercent: 0.25 }));
    await api.quitWukongDevice('u1', 1, '安全处置');
    expect(await api.setWukongSystemUser('u_notice', false, '改回普通账号')).toEqual(expect.objectContaining({ userId: 'u_notice', enabled: false, syncStatus: 'pending' }));
    expect(await api.setWukongRobot('u_notice', { enabled: true, username: 'service_helper', placeholder: '请选择服务', inlineOn: false, menus: [{ cmd: '转人工', remark: '人工客服', type: 'command' }] }, '客服菜单更新')).toEqual(expect.objectContaining({ userId: 'u_notice', version: 2 }));
    await api.removeLiveKitParticipant('call_1', 'u1', '异常连接');
    const writes = fetchMock.mock.calls.filter(([, init]) => init?.method).map(([, init]) => JSON.parse(String(init?.body)) as Record<string, unknown>);
    expect(writes).toContainEqual({ deviceFlag: 1, reason: '安全处置', confirmed: true });
    expect(writes).toContainEqual({ enabled: false, reason: '改回普通账号', confirmed: true });
    expect(writes).toContainEqual({ enabled: true, username: 'service_helper', placeholder: '请选择服务', inlineOn: false, menus: [{ cmd: '转人工', remark: '人工客服', type: 'command' }], reason: '客服菜单更新', confirmed: true });
    expect(writes).toContainEqual({ reason: '异常连接', confirmed: true });
  });

  it('WuKong 未返回的实时指标与开关保持未知而不是伪造成零或关闭', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/wukong/overview')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ server_id: '1', version: 'v2.2.5' }) };
      return { ok: true, status: 200, headers: new Headers(), json: async () => ({ logger: {} }) };
    });
    vi.stubGlobal('fetch', fetchMock);
    const api = getApi('token');

    expect(await api.getWukongOverview()).toEqual(expect.objectContaining({
      connections: null,
      userHandlers: null,
      cpu: null,
      memoryBytes: null,
      retryQueue: null,
    }));
    expect(await api.getWukongSettings()).toEqual({
      traceEnabled: null,
      lokiEnabled: null,
      prometheusEnabled: null,
      stressEnabled: null,
    });
  });

  it('审计记录缺少结果时保持未知而不是伪造成成功', async () => {
    const fetchMock = vi.fn(async () => ({
      ok: true,
      status: 200,
      headers: new Headers(),
      json: async () => ({ items: [{ id: 'audit_missing_result', action: 'settings.updated', createdAt: '2026-08-16T00:00:00Z' }] }),
    }));
    vi.stubGlobal('fetch', fetchMock);

    const result = await getApi('token').getAuditLogs();
    expect(result.items).toEqual([
      expect.objectContaining({ id: 'audit_missing_result', actor: '未提供', result: 'unknown' }),
    ]);
  });

  it('健康探针缺少状态时保持未知而不是伪造成正常', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      headers: new Headers(),
      json: async () => ({ items: [{ name: '对象存储', latency: 4 }] }),
    })));

    expect(await getApi('token').getHealth()).toEqual([
      expect.objectContaining({
        name: '对象存储',
        status: 'unknown',
        detail: '服务未返回健康状态',
      }),
    ]);
  });

  it('运行概览缺少计数时显示未上报而不是伪造成零', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      headers: new Headers(),
      json: async () => ({ wukongStatus: 'ok' }),
    })));

    const dashboard = await getApi('token').getDashboard();
    expect(dashboard.metrics).toEqual([
      expect.objectContaining({ label: '用户总数', value: '—', delta: '用户统计未完整上报', tone: 'warning' }),
      expect.objectContaining({ label: '活跃群组', value: '—', delta: '群组统计未上报', tone: 'warning' }),
      expect.objectContaining({ label: '今日消息', value: '—', delta: '消息统计未上报', tone: 'warning' }),
      expect.objectContaining({ label: '待审举报', value: '—', delta: '举报队列未上报', tone: 'warning' }),
    ]);
  });

  it('业务频道与客服工作台使用审计写入契约', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/channels') && init?.method === 'POST') return { ok: true, status: 201, headers: new Headers(), json: async () => ({ item: { id: 'info_1', channelType: 6, category: 'info', name: '官方资讯', ownerId: 'u_owner', visibility: 'public', joinPolicy: 'open', postingPolicy: 'operators', memberCount: 1 } }) };
      if (url.includes('/support/sessions/s_1/transfer')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ item: { id: 's_1', visitorId: 'u_visitor', skillGroupId: 'skill_1', channelId: 'u_visitor', channelType: 10, status: 'active', assignedAgentId: 'u_agent_2', transferCount: 1 } }) };
      if (url.includes('/channels')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ id: 'community_1', channelType: 4, category: 'community', name: '社区', ownerId: 'u_owner', visibility: 'public', joinPolicy: 'open', postingPolicy: 'members', memberCount: 3 }], total: 1 }) };
      return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [] }) };
    });
    vi.stubGlobal('fetch', fetchMock);
    const api = getApi('token');
    const page = await api.getBusinessChannels('社区', 4);
    expect(page.items).toEqual([expect.objectContaining({ id: 'community_1', channelType: 4, name: '社区' })]);
    const created = await api.createBusinessChannel({ ownerId: 'u_owner', channelType: 6, name: '官方资讯', visibility: 'public', joinPolicy: 'open', postingPolicy: 'operators', slowModeSeconds: 0 }, '运营申请 CH-1');
    expect(created).toEqual(expect.objectContaining({ id: 'info_1', channelType: 6 }));
    const transferred = await api.transferSupportSession('s_1', 'u_agent_2', '升级处理');
    expect(transferred).toEqual(expect.objectContaining({ id: 's_1', assignedAgentId: 'u_agent_2', transferCount: 1 }));
    const writes = fetchMock.mock.calls.filter(([, init]) => init?.method).map(([input, init]) => ({ url: String(input), body: JSON.parse(String(init?.body)) as Record<string, unknown> }));
    expect(writes).toContainEqual(expect.objectContaining({ url: expect.stringContaining('/channels'), body: expect.objectContaining({ ownerId: 'u_owner', reason: '运营申请 CH-1', confirmed: true }) }));
    expect(writes).toContainEqual(expect.objectContaining({ url: expect.stringContaining('/support/sessions/s_1/transfer'), body: { targetAgentId: 'u_agent_2', reason: '升级处理', confirmed: true } }));
  });

  it('签名插件发布使用 multipart 且停启操作保留确认与审计理由', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/plugin-events')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ ID: 7, PluginNo: 'wk.plugin.safe', Action: 'install', Status: 'active', Actor: 'ops', Reason: '发布单 PLUGIN-1', Details: { version: '1.2.3' }, CreatedAt: '2026-08-11T00:00:00Z' }] }) };
      if (url.includes('/logs?')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ plugin_no: 'wk.plugin.safe', node_id: 1, entries: [{ sequence: 8, stream: 'stderr', timestamp: 1770000000000, message: 'policy timeout' }] }) };
      return { ok: true, status: 200, headers: new Headers(), json: async () => ({ item: { pluginNo: 'wk.plugin.safe', nodeId: 1, name: 'wk.plugin.safe-linux-amd64.wkp', fileName: 'wk.plugin.safe-linux-amd64.wkp', version: '1.2.3', methods: ['Send'], sha256: 'abc', sizeBytes: 10, keyId: 'release-key', status: 'active', lastActor: 'ops', lastReason: '发布单 PLUGIN-1' } }) };
    });
    vi.stubGlobal('fetch', fetchMock);
    const api = getApi('token');
    const manifest = new File([JSON.stringify({ schemaVersion: 1, pluginNo: 'wk.plugin.safe' })], 'manifest.json', { type: 'application/json' });
    const bundle = new File(['binary'], 'wk.plugin.safe-linux-amd64.wkp');
    const release = await api.installWukongPlugin(bundle, manifest, 'base64-signature', 1, '发布单 PLUGIN-1');
    expect(release).toEqual(expect.objectContaining({ pluginNo: 'wk.plugin.safe', status: 'active', keyId: 'release-key' }));
    const installCall = fetchMock.mock.calls.find(([input]) => String(input).endsWith('/wukong/plugins/install'));
    const form = installCall?.[1]?.body as FormData;
    expect(form).toBeInstanceOf(FormData);
    expect(form.get('confirmed')).toBe('true');
    expect(form.get('reason')).toBe('发布单 PLUGIN-1');
    expect(new Headers(installCall?.[1]?.headers).has('Content-Type')).toBe(false);
    await api.setWukongPluginEnabled('wk.plugin.safe', 1, false, '紧急停用');
    const disableCall = fetchMock.mock.calls.find(([input]) => String(input).includes('/wk.plugin.safe/disable'));
    expect(JSON.parse(String(disableCall?.[1]?.body))).toEqual({ nodeId: 1, reason: '紧急停用', confirmed: true });
    expect(await api.getWukongPluginEvents()).toEqual([expect.objectContaining({ id: 7, pluginNo: 'wk.plugin.safe', action: 'install', status: 'active' })]);
    expect(await api.getWukongPluginLogs('wk.plugin.safe', 1, 50)).toEqual([expect.objectContaining({ sequence: 8, stream: 'stderr', message: 'policy timeout' })]);
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/wukong/plugins/wk.plugin.safe/logs?nodeId=1&limit=50'))).toBe(true);
  });

  it('服务端长时间无响应时终止请求并返回可重试的中文超时错误', async () => {
    vi.useFakeTimers();
    try {
      const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true });
      }));
      vi.stubGlobal('fetch', fetchMock);

      const pending = getApi('token').getUsers();
      const assertion = expect(pending).rejects.toMatchObject({
        name: 'ApiError',
        code: 'REQUEST_TIMEOUT',
        status: 0,
        message: '请求超时，请检查网络或服务状态后重试',
      });
      await vi.advanceTimersByTimeAsync(20_000);
      await assertion;

      expect(fetchMock).toHaveBeenCalledOnce();
      expect(fetchMock.mock.calls[0][1]?.signal?.aborted).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });
});
