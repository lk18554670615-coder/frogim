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
    expect(JSON.parse(String(init.body))).toEqual({ action: 'delete_message', note: '确认为违规消息' });
  });
});
