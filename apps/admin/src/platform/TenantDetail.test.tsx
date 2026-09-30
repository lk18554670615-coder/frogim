import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { TenantDetail } from './TenantDetail';
import { PlatformClient, type TenantDetail as Detail } from './api';

afterEach(cleanup);
const tenant: Detail = {
  id: 'a', displayName: '企业 A', note: '旧备注', httpBaseUrl: 'https://a.example',
  status: 'suspended', isDefault: false, configVersion: 7, accessVersion: 3,
  directoryVersion: 2, archivedAt: null, archivedBy: null, currentDefaultId: 'b',
  createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-02T00:00:00Z',
  accountCount: 4, enabledCodeCount: 1, serverCount: 1, pendingJobCount: 0, maintenanceEnabled: true,
};

it('edits directory metadata and archives without changing service binding', async () => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  const client = new PlatformClient();
  const request = vi.spyOn(client, 'request').mockImplementation(async (path, method) => method === 'GET' ? tenant : { ...tenant, directoryVersion: path.endsWith('/archive') ? 4 : 3, archivedAt: path.endsWith('/archive') ? '2026-09-30T00:00:00Z' : null });
  const changed = vi.fn(), navigate = vi.fn();
  render(<TenantDetail id="a" client={client} writable onClose={vi.fn()} onChanged={changed} onNavigate={navigate} />);
  await screen.findByText('旧备注');
  fireEvent.click(screen.getByRole('button', { name: '修改名称与备注' }));
  fireEvent.change(screen.getByLabelText('企业名称'), { target: { value: '新名称' } });
  fireEvent.change(screen.getByLabelText('内部备注'), { target: { value: '新备注' } });
  fireEvent.change(screen.getByLabelText('操作理由'), { target: { value: '更正资料' } });
  fireEvent.click(screen.getByLabelText('我已确认上述操作与影响范围'));
  fireEvent.click(screen.getByRole('button', { name: '确认操作' }));
  await waitFor(() => expect(request).toHaveBeenCalledWith('/tenants/a', 'PATCH', { displayName: '新名称', note: '新备注', expectedDirectoryVersion: 2, reason: '更正资料', confirmed: true }));
  await waitFor(() => expect(changed).toHaveBeenCalled());
  fireEvent.click(screen.getByRole('button', { name: '账号' }));
  expect(navigate).toHaveBeenCalledWith('accounts', 'a');
});

it('shows archive restrictions and read-only detail', async () => {
  const client = new PlatformClient();
  vi.spyOn(client, 'request').mockResolvedValue({ ...tenant, pendingJobCount: 1 });
  render(<TenantDetail id="a" client={client} writable onClose={vi.fn()} onChanged={vi.fn()} onNavigate={vi.fn()} />);
  expect(await screen.findByRole('button', { name: '归档企业' })).toBeDisabled();
  cleanup();
  render(<TenantDetail id="a" client={client} writable={false} onClose={vi.fn()} onChanged={vi.fn()} onNavigate={vi.fn()} />);
  await screen.findByText('旧备注');
  expect(screen.queryByRole('button', { name: '修改名称与备注' })).not.toBeInTheDocument();
});
