import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from './App';

const session = { token: 'test-admin-jwt', displayName: '测试管理员', role: 'platform_admin', expiresAt: Date.now() + 60_000 };

describe('邻里通讯管理后台', () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    window.history.replaceState({}, '', '/overview');
  });
  afterEach(() => { vi.unstubAllGlobals(); });

  it('展示概览核心指标', async () => {
    render(<App />);
    expect(screen.getByRole('heading', { name: '运行概览' })).toBeInTheDocument();
    expect(await screen.findByText('8,429')).toBeInTheDocument();
    expect(screen.getByText('99.993%')).toBeInTheDocument();
  });

  it('切换并持久化数据源', async () => {
    const view = render(<App />);
    await screen.findByText('8,429');
    fireEvent.change(screen.getByLabelText('数据源'), { target: { value: 'live' } });
    expect(localStorage.getItem('nexachat_data_mode')).toBe('live');
    view.unmount();
  });

  it('支持防抖搜索用户', async () => {
    window.history.replaceState({}, '', '/users');
    render(<App />);
    const input = screen.getByLabelText('搜索昵称、用户 ID 或手机号');
    await screen.findByText('林夏');
    fireEvent.change(input, { target: { value: '江宁' } });
    await waitFor(() => expect(screen.queryByText('林夏')).not.toBeInTheDocument());
    expect(await screen.findByText('江宁')).toBeInTheDocument();
  });

  it('兼容服务端扁平 dashboard 响应', async () => {
    localStorage.setItem('nexachat_data_mode', 'live');
    sessionStorage.setItem('nexachat_admin_session', JSON.stringify(session));
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true, status: 200, headers: new Headers(),
      json: async () => ({ users: 11, bannedUsers: 1, conversations: 5, messages: 133, pendingReports: 2, websocketConnections: 7 }),
    })));
    render(<App />);
    expect(await screen.findByText('用户总数')).toBeInTheDocument();
    expect(screen.getByText('11')).toBeInTheDocument();
    expect(screen.getByText('7')).toBeInTheDocument();
  });

  it('解析嵌套接口错误并停留在登录页', async () => {
    localStorage.setItem('nexachat_data_mode', 'live');
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false, status: 401, headers: new Headers({ 'x-request-id': 'req-1' }),
      json: async () => ({ error: { code: 'UNAUTHENTICATED', message: '邮箱、密码或动态验证码无效' } }),
    })));
    render(<App />);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('管理员邮箱'), 'ops@example.com');
    await user.type(screen.getByLabelText('密码'), 'wrong-password');
    await user.type(screen.getByLabelText('动态验证码（如已启用）'), '123456');
    await user.click(screen.getByRole('button', { name: '登录控制台' }));
    expect(await screen.findByText(/邮箱、密码或动态验证码无效/)).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '管理员登录' })).toBeInTheDocument();
  });

  it('使用服务端返回的角色建立短时会话', async () => {
    localStorage.setItem('nexachat_data_mode', 'live');
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({ accessToken: 'admin.jwt', displayName: '安全审核员', role: 'moderator', expiresIn: 900 }),
      })
      .mockResolvedValue({
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({ users: 0, conversations: 0, messages: 0, pendingReports: 0, websocketConnections: 0 }),
      });
    vi.stubGlobal('fetch', fetchMock);
    render(<App />);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('管理员邮箱'), 'moderator@example.com');
    await user.type(screen.getByLabelText('密码'), 'correct-password');
    await user.click(screen.getByRole('button', { name: '登录控制台' }));
    expect(await screen.findByText('安全审核员')).toBeInTheDocument();
    expect(JSON.parse(sessionStorage.getItem('nexachat_admin_session') ?? '{}')).toEqual(expect.objectContaining({ token: 'admin.jwt', role: 'moderator' }));
    expect(fetchMock).toHaveBeenNthCalledWith(1, expect.stringContaining('/auth/login'), expect.objectContaining({ method: 'POST' }));
  });

  it('危险操作弹窗接管焦点并支持 Escape', async () => {
    window.history.replaceState({}, '', '/users');
    render(<App />);
    const user = userEvent.setup();
    const actions = await screen.findAllByRole('button', { name: '账号处置' });
    await user.click(actions[0]);
    const dialog = screen.getByRole('dialog');
    expect(dialog).toBeInTheDocument();
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
    expect(screen.getByRole('button', { name: '删除规则' })).toBeInTheDocument();
  });

  it('举报状态筛选使用全部可键盘聚焦的普通按钮', async () => {
    window.history.replaceState({}, '', '/reports');
    render(<App />);
    const group = screen.getByRole('group', { name: '举报状态筛选' });
    const buttons = within(group).getAllByRole('button');
    expect(buttons).toHaveLength(5);
    buttons.forEach((button) => expect(button.tabIndex).toBe(0));
    expect(buttons[0]).toHaveAttribute('aria-pressed', 'true');
  });

  it('点击下一页时使用服务端 nextCursor 而非本地切片', async () => {
    localStorage.setItem('nexachat_data_mode', 'live');
    sessionStorage.setItem('nexachat_admin_session', JSON.stringify(session));
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
    localStorage.setItem('nexachat_data_mode', 'live');
    sessionStorage.setItem('nexachat_admin_session', JSON.stringify(session));
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
});
