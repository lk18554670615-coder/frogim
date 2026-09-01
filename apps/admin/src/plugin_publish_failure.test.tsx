import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from './App';

const session = {
  token: 'test-admin-jwt',
  displayName: '测试管理员',
  id: 'admin_1', username: 'admin', email: 'admin@example.com', roleId: 'platform_admin', roleName: '平台管理员', permissions: ['operations.write'],
  expiresAt: Date.now() + 60_000,
};

function response(payload: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ 'x-request-id': status >= 400 ? 'req-plugin-signature' : '' }),
    json: async () => payload,
  };
}

describe('签名插件发布失败状态', () => {
  beforeEach(() => {
    sessionStorage.clear();
    sessionStorage.setItem('qingwaguagua_admin_session', JSON.stringify(session));
    window.history.replaceState({}, '', '/im-infrastructure');
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('服务端拒绝发布时展示可追踪错误、保留输入并允许原地重试', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/wukong/plugins/install') && init?.method === 'POST') {
        return response({ error: { code: 'INVALID_SIGNATURE', message: '签名校验失败：密钥不受信任' } }, 422);
      }
      if (url.includes('/wukong/overview')) {
        return response({ server_id: '1', version: 'v2.2.5', uptime: '1d', connections: 1 });
      }
      if (url.includes('/wukong/settings')) return response({});
      if (url.includes('/wukong/nodes')) return response({ data: [] });
      if (url.includes('/wukong/plugin-events')) return response({ items: [] });
      if (url.includes('/wukong/plugins')) return response({ items: [] });
      return response({ items: [] });
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', { name: '插件' }));

    const manifest = new File(['{"name":"safe-plugin"}'], 'manifest.json', { type: 'application/json' });
    Object.defineProperty(manifest, 'text', { value: async () => '{"name":"safe-plugin"}' });
    const bundle = new File(['signed-plugin'], 'safe-plugin.wkp', { type: 'application/octet-stream' });
    await user.upload(screen.getByLabelText('签名清单 JSON'), manifest);
    await user.upload(screen.getByLabelText('插件可执行文件'), bundle);
    await user.type(screen.getByLabelText('Ed25519 签名（Base64）'), 'invalid-signature');
    await user.type(screen.getByLabelText('发布理由 / 工单'), '发布单 REL-2048');

    const publish = screen.getByRole('button', { name: '校验并安装' });
    expect(publish).toBeEnabled();
    await user.click(publish);

    expect(await screen.findByRole('alert')).toHaveTextContent('签名校验失败：密钥不受信任');
    expect(screen.getByRole('alert')).toHaveTextContent('req-plugin-signature');
    await waitFor(() => expect(publish).toBeEnabled());
    expect(screen.getByLabelText('Ed25519 签名（Base64）')).toHaveValue('invalid-signature');
    expect(screen.getByLabelText('发布理由 / 工单')).toHaveValue('发布单 REL-2048');
    expect(fetchMock.mock.calls.filter(([input]) => String(input).includes('/wukong/plugins/install'))).toHaveLength(1);
  });

  it('节点编号无效时在表单内提示且不发送安装请求', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/wukong/plugin-events')) return response({ items: [] });
      if (url.includes('/wukong/plugins')) return response({ items: [] });
      return response({ items: [] });
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', { name: '插件' }));
    const manifest = new File(['{}'], 'manifest.json', { type: 'application/json' });
    Object.defineProperty(manifest, 'text', { value: async () => '{}' });
    await user.upload(screen.getByLabelText('签名清单 JSON'), manifest);
    await user.upload(
      screen.getByLabelText('插件可执行文件'),
      new File(['plugin'], 'plugin.wkp', { type: 'application/octet-stream' }),
    );
    await user.type(screen.getByLabelText('Ed25519 签名（Base64）'), 'signature');
    await user.type(screen.getByLabelText('发布理由 / 工单'), '发布单 REL-2049');
    await user.clear(screen.getByLabelText('节点 ID'));
    await user.type(screen.getByLabelText('节点 ID'), '0');
    await user.click(screen.getByRole('button', { name: '校验并安装' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('节点 ID 必须是大于 0 的整数');
    expect(fetchMock.mock.calls.filter(([input]) => String(input).includes('/wukong/plugins/install'))).toHaveLength(0);
    await user.clear(screen.getByLabelText('节点 ID'));
    await user.type(screen.getByLabelText('节点 ID'), '2');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('发布成功后清空文件与文本草稿避免重复安装', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/wukong/plugins/install') && init?.method === 'POST') {
        return response({ item: { no: 'safe-plugin', nodeId: 1, status: 'active' } });
      }
      if (url.includes('/wukong/plugin-events')) return response({ items: [] });
      if (url.includes('/wukong/plugins')) return response({ items: [] });
      return response({ items: [] });
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', { name: '插件' }));
    const manifest = new File(['{"name":"safe-plugin"}'], 'manifest.json', { type: 'application/json' });
    Object.defineProperty(manifest, 'text', { value: async () => '{"name":"safe-plugin"}' });
    await user.upload(screen.getByLabelText('签名清单 JSON'), manifest);
    await user.upload(
      screen.getByLabelText('插件可执行文件'),
      new File(['signed-plugin'], 'safe-plugin.wkp', { type: 'application/octet-stream' }),
    );
    await user.type(screen.getByLabelText('Ed25519 签名（Base64）'), 'valid-signature');
    await user.type(screen.getByLabelText('发布理由 / 工单'), '发布单 REL-2050');
    const publish = screen.getByRole('button', { name: '校验并安装' });
    await user.click(publish);

    expect(await screen.findByRole('status')).toHaveTextContent('签名插件已安装并通过启动自证');
    await waitFor(() => expect(publish).toBeDisabled());
    expect(screen.getByLabelText<HTMLInputElement>('签名清单 JSON').files).toHaveLength(0);
    expect(screen.getByLabelText<HTMLInputElement>('插件可执行文件').files).toHaveLength(0);
    expect(screen.getByLabelText('Ed25519 签名（Base64）')).toHaveValue('');
    expect(screen.getByLabelText('发布理由 / 工单')).toHaveValue('');
    expect(fetchMock.mock.calls.filter(([input]) => String(input).includes('/wukong/plugins/install'))).toHaveLength(1);
  });

  it('插件发布草稿切换基础设施页签前确认且继续编辑会保留内容', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/wukong/plugin-events')) return response({ items: [] });
      if (url.includes('/wukong/plugins')) return response({ items: [] });
      if (url.includes('/livekit/rooms')) return response({ items: [] });
      if (url.includes('/livekit/metrics')) return response({});
      return response({ items: [] });
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', { name: '插件' }));
    const manifest = new File(['{}'], 'manifest.json', { type: 'application/json' });
    Object.defineProperty(manifest, 'text', { value: async () => '{}' });
    await user.upload(screen.getByLabelText('签名清单 JSON'), manifest);
    await user.upload(
      screen.getByLabelText('插件可执行文件'),
      new File(['plugin'], 'plugin.wkp', { type: 'application/octet-stream' }),
    );
    await user.type(screen.getByLabelText('Ed25519 签名（Base64）'), 'pending-signature');
    await user.type(screen.getByLabelText('发布理由 / 工单'), '发布单 REL-2051');

    await user.click(screen.getByRole('tab', { name: '音视频房间' }));
    expect(screen.getByRole('heading', { name: '放弃未提交的插件发布？' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '插件' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('button', { name: '取消' })).toHaveFocus();
    await user.click(screen.getByRole('button', { name: '取消' }));
    expect(screen.getByLabelText('Ed25519 签名（Base64）')).toHaveValue('pending-signature');
    expect(screen.getByLabelText('发布理由 / 工单')).toHaveValue('发布单 REL-2051');

    await user.click(screen.getByRole('tab', { name: '音视频房间' }));
    await user.click(screen.getByRole('button', { name: '放弃修改并切换' }));
    await waitFor(() => expect(screen.getByRole('tab', { name: '音视频房间' })).toHaveAttribute('aria-selected', 'true'));
    expect(screen.queryByLabelText('Ed25519 签名（Base64）')).not.toBeInTheDocument();
  });

  it('插件发布草稿离开 IM 基础设施前走全局未保存确认', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/wukong/plugin-events')) return response({ items: [] });
      if (url.includes('/wukong/plugins')) return response({ items: [] });
      if (url.includes('/users')) return response({ items: [], total: 0, page: 1, pageSize: 20 });
      return response({ items: [] });
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<App />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', { name: '插件' }));
    await user.type(screen.getByLabelText('Ed25519 签名（Base64）'), 'draft-signature');
    await user.click(screen.getByRole('button', { name: '用户与关系' }));
    await user.click(screen.getByRole('link', { name: /用户管理/ }));

    expect(screen.getByRole('heading', { name: '放弃未保存的修改？' })).toBeInTheDocument();
    expect(screen.getByText(/签名插件发布资料尚未提交/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '取消' }));
    expect(screen.getByLabelText('Ed25519 签名（Base64）')).toHaveValue('draft-signature');
  });
});
