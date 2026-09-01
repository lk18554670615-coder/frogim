import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from './App';

const session = {
  token: 'test-admin-jwt',
  displayName: '测试管理员',
  id: 'admin_1', username: 'admin', email: 'admin@example.com', roleId: 'platform_admin', roleName: '平台管理员', permissions: ['users.write', 'announcements.write', 'channels.write', 'operations.write'],
  expiresAt: Date.now() + 60_000,
};

function response(payload: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ 'x-request-id': status >= 400 ? 'req-picker-load' : '' }),
    json: async () => payload,
  };
}

describe('真实资源选择器失败恢复', () => {
  beforeEach(() => {
    sessionStorage.clear();
    sessionStorage.setItem('qingwaguagua_admin_session', JSON.stringify(session));
    window.history.replaceState({}, '', '/support-workbench');
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('账号列表失败后提供真实重试并恢复选择能力', async () => {
    let userRequests = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/users?')) {
        userRequests += 1;
        if (userRequests === 1) return response({ error: { code: 'SERVICE_UNAVAILABLE', message: '账号服务暂时不可用' } }, 503);
        return response({ items: [{ id: 'u-real-1', nickname: '真实用户', handle: 'real_user', phone: '+8613800000000', status: 'active' }], page: 1, pageSize: 20, total: 1 });
      }
      if (url.includes('/support/skills')) return response({ items: [{ id: 'skill-1', name: '在线客服', description: '处理普通咨询', routingStrategy: 'least_active', maxConcurrentPerAgent: 5, enabled: true, queueCount: 0, availableAgents: 0 }] });
      if (url.includes('/support/agents')) return response({ items: [] });
      if (url.includes('/support/sessions')) return response({ items: [], page: 1, pageSize: 100, total: 0 });
      return response({ items: [] });
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', { name: '客服坐席' }));

    const select = await screen.findByRole('combobox', { name: '客服坐席账号' });
    expect(select).toBeDisabled();
    expect(await screen.findByRole('alert')).toHaveTextContent('账号列表加载失败');

    await user.click(screen.getByRole('button', { name: '重新加载账号列表' }));

    await waitFor(() => expect(select).toBeEnabled());
    expect(await screen.findByRole('option', { name: '真实用户 · real_user' })).toBeInTheDocument();
    expect(userRequests).toBe(2);
  });
});
