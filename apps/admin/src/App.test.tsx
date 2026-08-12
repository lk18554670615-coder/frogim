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
      json: async () => ({ users: 11, bannedUsers: 1, conversations: 5, messages: 133, pendingReports: 2, wukongConnections: 7, wukongStatus: 'ok' }),
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
        json: async () => ({ users: 0, conversations: 0, messages: 0, pendingReports: 0, wukongConnections: 0, wukongStatus: 'unavailable' }),
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

  it('系统健康页面使用不与服务端探针冲突的可刷新路由', async () => {
    window.history.replaceState({}, '', '/system-health');
    render(<App />);
    expect(screen.getByRole('heading', { name: '系统健康' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '系统健康' })).toHaveAttribute('href', '/system-health');
    expect(await screen.findByText('WuKongIM 长连接')).toBeInTheDocument();
  });

  it('发布系统设置前要求二次确认和理由', async () => {
    window.history.replaceState({}, '', '/settings');
    render(<App />);
    const user = userEvent.setup();
    await screen.findByRole('heading', { name: '系统设置' });
    await user.click(await screen.findByRole('button', { name: '保存并立即生效' }));
    expect(screen.getByRole('heading', { name: '发布系统业务策略' })).toBeInTheDocument();
    const confirm = screen.getByRole('button', { name: '确认发布' });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText('发布理由'), '变更单 OPS-2026-08-12');
    expect(confirm).toBeEnabled();
  });

  it('内容审核要求填写理由并发送确认字段', async () => {
    localStorage.setItem('nexachat_data_mode', 'live');
    sessionStorage.setItem('nexachat_admin_session', JSON.stringify(session));
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
    await user.type(screen.getByLabelText('封面媒体 ID'), 'media_cover_demo');
    await user.type(screen.getByLabelText('操作理由'), '创建运营表情包');
    expect(packConfirm).toBeEnabled();
    await user.click(packConfirm);
    expect(await screen.findByText('新年祝福')).toBeInTheDocument();
  });

  it('系统运维角色可以发布客户端版本策略', async () => {
    localStorage.setItem('nexachat_data_mode', 'live');
    sessionStorage.setItem('nexachat_admin_session', JSON.stringify({ ...session, role: 'system_operator' }));
    const policy = { platform: 'android', minimumVersion: '1.0.0', latestVersion: '1.1.0', forceUpdate: false, rolloutPercentage: 100, releaseNotes: '', downloadUrl: 'https://download.example.com/app.apk', updatedBy: 'ops', updatedAt: '2026-08-11T00:00:00Z' };
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => ({ ok: true, status: 200, headers: new Headers(), json: async () => init?.method === 'PUT' ? policy : { items: [policy] } }));
    vi.stubGlobal('fetch', fetchMock);
    window.history.replaceState({}, '', '/client-versions');
    render(<App />);
    const user = userEvent.setup();
    const publish = await screen.findByRole('button', { name: '保存并发布' });
    expect(publish).toBeEnabled();
    await user.click(publish);
    await user.type(screen.getByLabelText('发布原因'), '发布单 REL-1024');
    await user.click(screen.getByRole('button', { name: '确认发布' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'PUT')).toBe(true));
    const write = fetchMock.mock.calls.find(([, init]) => init?.method === 'PUT');
    expect(JSON.parse(String(write?.[1]?.body))).toEqual(expect.objectContaining({ reason: '发布单 REL-1024', confirmed: true }));
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
    await user.click(screen.getByRole('tab', { name: '音视频房间' }));
    expect(await screen.findByText('call_demo')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '关闭房间' })).toBeEnabled();
    await user.click(screen.getByRole('tab', { name: '插件' }));
    expect(screen.getByRole('heading', { name: '签名插件发布' })).toBeInTheDocument();
    expect(screen.getByText(/AI Receive 插件会被拒绝/)).toBeInTheDocument();
    await user.click(await screen.findByRole('button', { name: '运行日志' }));
    expect(screen.getByRole('heading', { name: '插件运行日志' })).toBeInTheDocument();
    expect(await screen.findByText('policy plugin ready')).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: '卸载' })).toBeDisabled();
  });

  it('可在 IM 基础设施中管理系统账号', async () => {
    window.history.replaceState({}, '', '/im-infrastructure');
    render(<App />);
    const user = userEvent.setup();
    await user.click(screen.getByRole('tab', { name: '系统账号' }));
    expect(screen.getByRole('heading', { name: '系统账号' })).toBeInTheDocument();
    expect(await screen.findByText('系统通知')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '设为系统账号' })).toBeDisabled();
    await user.type(screen.getByLabelText('用户 UID'), 'u_notice_2');
    expect(screen.getByRole('button', { name: '设为系统账号' })).toBeEnabled();
  });

  it('频道运营覆盖成员、临时订阅与黑白名单入口', async () => {
    window.history.replaceState({}, '', '/business-channels');
    render(<App />);
    expect(screen.getByRole('heading', { name: '频道运营' })).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '运营管理' }));
    expect(screen.getByRole('heading', { name: '成员与临时订阅' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '黑白名单' })).toBeInTheDocument();
    await user.type(screen.getByLabelText('成员用户 ID'), 'u_temp');
    await user.click(screen.getByRole('button', { name: '添加' }));
    expect(screen.getByRole('heading', { name: '添加频道成员' })).toBeInTheDocument();
    const confirm = screen.getByRole('button', { name: '确认并记录' });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText('操作原因'), '临时活动订阅');
    expect(confirm).toBeEnabled();
  });

  it('客服工作台提供队列认领、转接和结束处置', async () => {
    window.history.replaceState({}, '', '/support-workbench');
    render(<App />);
    expect(screen.getByRole('heading', { name: '客服工作台' })).toBeInTheDocument();
    expect(await screen.findByText('账号登录问题')).toBeInTheDocument();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('目标客服 ID'), 'u_support_2');
    await user.click(screen.getByRole('button', { name: '转接' }));
    expect(screen.getByRole('heading', { name: '转接客服会话' })).toBeInTheDocument();
    const confirm = screen.getByRole('button', { name: '确认并记录' });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText('操作原因'), '升级到高级客服');
    expect(confirm).toBeEnabled();
  });
});
