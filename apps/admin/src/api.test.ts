import { afterEach, describe, expect, it, vi } from 'vitest';
import { getApi, loginAdmin } from './api';

describe('live API adapter', () => {
  afterEach(() => { vi.unstubAllGlobals(); });

  it('消费服务端 items/total/nextCursor 而不在本地再切片', async () => {
    const items = Array.from({ length: 20 }, (_, index) => ({ id: `u_${index}`, name: `用户${index}`, banned: index === 0, createdAt: '2026-07-31T00:00:00Z' }));
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ items, total: 25, nextCursor: 'cursor-page-2' }) }));
    vi.stubGlobal('fetch', fetchMock);
    const page = await getApi('live', 'token').getUsers('林', 'active', 1, 20, 'cursor-page-1');
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
    const page = await getApi('live', 'token').getUsers('', '', 1, 20);
    expect(page.items).toHaveLength(25);
    expect(page.total).toBe(25);
    expect(page.hasNext).toBe(false);
  });

  it('保留服务端错误码、信息和追踪号', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false, status: 409, headers: new Headers({ 'x-request-id': 'req-conflict' }),
      json: async () => ({ error: { code: 'CONFLICT', message: '资源状态已变化' } }),
    })));
    await expect(getApi('live', 'token').getDashboard()).rejects.toEqual(expect.objectContaining({ status: 409, code: 'CONFLICT', message: '资源状态已变化', requestId: 'req-conflict' }));
  });

  it('适配管理员登录会话并传递 TOTP', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({
      ok: true, status: 200, headers: new Headers(),
      json: async () => ({ data: { accessToken: 'jwt', displayName: '运营管理员', role: 'admin', expiresIn: 600 } }),
    }));
    vi.stubGlobal('fetch', fetchMock);
    const result = await loginAdmin('ops@example.com', 'password', '123456');
    expect(result).toEqual(expect.objectContaining({ token: 'jwt', displayName: '运营管理员', role: 'platform_admin' }));
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({ email: 'ops@example.com', password: 'password', totp: '123456' });
  });

  it('传递真实举报处置动作并消费处理结果', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({
      ok: true, status: 200, headers: new Headers(),
      json: async () => ({ status: 'resolved', action: 'delete_message' }),
    }));
    vi.stubGlobal('fetch', fetchMock);
    const result = await getApi('live', 'token').resolveReport('r_1', 'delete_message', '确认为违规消息');
    expect(result).toEqual({ status: 'resolved', action: 'delete_message' });
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({ action: 'delete_message', reason: '确认为违规消息', confirmed: true });
  });

  it('发布客户端版本策略时携带原因和显式确认', async () => {
    const response = { platform: 'android' as const, minimumVersion: '1.2.0', latestVersion: '1.3.0', forceUpdate: true, rolloutPercentage: 25, releaseNotes: '安全更新', downloadUrl: 'https://download.example.com/app.apk', updatedBy: 'ops', updatedAt: '2026-08-11T00:00:00Z' };
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({ ok: true, status: 200, headers: new Headers(), json: async () => response }));
    vi.stubGlobal('fetch', fetchMock);
    const updated = await getApi('live', 'token').updateClientVersion(response, '发布单 REL-1024');
    expect(updated).toEqual(expect.objectContaining({ platform: 'android', rolloutPercentage: 25, forceUpdate: true }));
    expect(String(fetchMock.mock.calls[0][0])).toContain('/client-versions/android');
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual(expect.objectContaining({ minimumVersion: '1.2.0', latestVersion: '1.3.0', reason: '发布单 REL-1024', confirmed: true }));
  });

  it('审核朋友圈时携带目标状态、原因和显式确认', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ id: 'moment_1', status: 'hidden' }) }));
    vi.stubGlobal('fetch', fetchMock);
    await getApi('live', 'token').moderateMoment('moment_1', 'hidden', '举报复核确认违规');
    expect(String(fetchMock.mock.calls[0][0])).toContain('/moments/moment_1/moderate');
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({ status: 'hidden', reason: '举报复核确认违规', confirmed: true });
  });

  it('审核表情包时适配返回值并携带审计信息', async () => {
    const response = { id: 'pack_1', name: '节日表情', categoryName: '节日', status: 'disabled', items: [{ id: 'sticker_1' }], createdBy: 'creator', reviewedBy: 'moderator', reviewReason: '版权投诉', updatedAt: '2026-08-11T00:00:00Z' };
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({ ok: true, status: 200, headers: new Headers(), json: async () => response }));
    vi.stubGlobal('fetch', fetchMock);
    const reviewed = await getApi('live', 'token').reviewStickerPack('pack_1', 'disabled', '版权投诉');
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
    const api = getApi('live', 'token');
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
      if (url.includes('/wukong/system-users/') && init?.method === 'PUT') return { ok: true, status: 202, headers: new Headers(), json: async () => ({ item: { userId: 'u_notice', name: '系统通知', enabled: false, syncStatus: 'pending', updatedBy: 'ops', reason: '改回普通账号', updatedAt: '2026-08-12T00:00:00Z' } }) };
      if (init?.method) return { ok: true, status: 204, headers: new Headers(), json: async () => ({}) };
      if (url.includes('/wukong/overview')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ server_id: '1', version: 'v2.2.5', connections: 7, retry_queue: 0 }) };
      if (url.includes('/wukong/settings')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ logger: { trace_on: 0, loki_on: 1 }, prometheus_on: 1, stress_on: 0 }) };
      if (url.includes('/wukong/nodes')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ data: [{ id: 1, online: 1, is_leader: 1, slot_count: 64, slot_leader_count: 64 }] }) };
      if (url.includes('/wukong/devices')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ data: [{ uid: 'u1', device_flag: 1, device_level: 1, token_on: 1 }] }) };
      if (url.includes('/wukong/system-users')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ userId: 'u_notice', name: '系统通知', enabled: true, syncStatus: 'synced', updatedBy: 'ops', reason: '通知账号', updatedAt: '2026-08-12T00:00:00Z' }] }) };
      if (url.includes('/livekit/metrics')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ healthy: true, activeRooms: 1, activeParticipants: 2, cpuPercent: 3.5, residentMemoryBytes: 104857600, networkReceiveBytesPerSecond: 2048, networkTransmitBytesPerSecond: 4096, packetLossPercent: 0.25, participantJoinsLastHour: 12, roomsCompletedLastHour: 4, sampledAt: '2026-08-13T00:00:00Z' }) };
      if (url.includes('/livekit/rooms')) return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [{ sid: 'RM_1', name: 'call_1', participantCount: 2, publisherCount: 1, maxParticipants: 9, activeRecording: false }] }) };
      return { ok: true, status: 200, headers: new Headers(), json: async () => ({ items: [] }) };
    });
    vi.stubGlobal('fetch', fetchMock);
    const api = getApi('live', 'token');
    expect(await api.getWukongOverview()).toEqual(expect.objectContaining({ serverId: '1', connections: 7 }));
    expect(await api.getWukongSettings()).toEqual({ traceEnabled: false, lokiEnabled: true, prometheusEnabled: true, stressEnabled: false });
    expect(await api.getWukongNodes()).toEqual([expect.objectContaining({ id: 1, online: true, leader: true })]);
    expect(await api.getWukongDevices('u1', 1)).toEqual([expect.objectContaining({ uid: 'u1', deviceFlag: 1, tokenPresent: true })]);
    expect(await api.getWukongSystemUsers()).toEqual([expect.objectContaining({ userId: 'u_notice', enabled: true, syncStatus: 'synced' })]);
    expect(await api.getLiveKitRooms()).toEqual([expect.objectContaining({ name: 'call_1', participantCount: 2, maxParticipants: 9 })]);
    expect(await api.getLiveKitMetrics()).toEqual(expect.objectContaining({ healthy: true, activeRooms: 1, activeParticipants: 2, cpuPercent: 3.5, packetLossPercent: 0.25 }));
    await api.quitWukongDevice('u1', 1, '安全处置');
    expect(await api.setWukongSystemUser('u_notice', false, '改回普通账号')).toEqual(expect.objectContaining({ userId: 'u_notice', enabled: false, syncStatus: 'pending' }));
    await api.removeLiveKitParticipant('call_1', 'u1', '异常连接');
    const writes = fetchMock.mock.calls.filter(([, init]) => init?.method).map(([, init]) => JSON.parse(String(init?.body)) as Record<string, unknown>);
    expect(writes).toContainEqual({ deviceFlag: 1, reason: '安全处置', confirmed: true });
    expect(writes).toContainEqual({ enabled: false, reason: '改回普通账号', confirmed: true });
    expect(writes).toContainEqual({ reason: '异常连接', confirmed: true });
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
    const api = getApi('live', 'token');
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
    const api = getApi('live', 'token');
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
});
