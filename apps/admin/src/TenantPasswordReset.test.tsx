import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from './api';
import { TenantPasswordReset } from './TenantPasswordReset';
import type { AdminApi, TenantCredentialJob } from './types';

const pending: TenantCredentialJob = { jobId: 'cred_a', requestId: 'request_a', status: 'pending' };
const fill = async () => {
  await userEvent.type(screen.getByLabelText('用户新密码'), 'TestPassword123!');
  await userEvent.type(screen.getByLabelText('确认用户新密码'), 'TestPassword123!');
  await userEvent.type(screen.getByLabelText('重置理由'), '用户申请');
  await userEvent.click(screen.getByRole('button', { name: /^重置用户密码$/ }));
};
describe('tenant user password reset', () => {
  it('requires confirmation, clears password when accepted, waits for task completion', async () => {
    const getUserCredentialJobs = vi.fn().mockResolvedValueOnce({ managed: true, items: [] }).mockResolvedValue({ managed: true, items: [{ ...pending, status: 'completed' }] });
    const resetTenantUserPassword = vi.fn().mockResolvedValue(pending);
    render(<TenantPasswordReset api={{ getUserCredentialJobs, resetTenantUserPassword } as unknown as AdminApi} userId="u1" canWrite />);
    await screen.findByLabelText('用户新密码'); await fill();
    expect(resetTenantUserPassword).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('button', { name: '确认重置用户密码' }));
    expect(await screen.findByText('密码重置处理中（尚未完成）')).toBeInTheDocument();
    expect(screen.getByLabelText('用户新密码')).toHaveValue('');
    expect(screen.getByLabelText('用户新密码')).toBeDisabled();
    expect(screen.queryByText('密码重置已完成')).not.toBeInTheDocument();
    expect(resetTenantUserPassword).toHaveBeenCalledWith('u1', expect.any(String), 'TestPassword123!', '用户申请');
    await userEvent.click(screen.getByRole('button', { name: '刷新密码任务' }));
    expect(await screen.findByText('密码重置已完成')).toBeInTheDocument();
  });
  it('keeps the same operation on an uncertain network result and does not silently retry', async () => {
    const getUserCredentialJobs = vi.fn().mockResolvedValue({ managed: true, items: [] });
    const resetTenantUserPassword = vi.fn().mockRejectedValueOnce(new Error('网络不可用')).mockResolvedValue(pending);
    render(<TenantPasswordReset api={{ getUserCredentialJobs, resetTenantUserPassword } as unknown as AdminApi} userId="u1" canWrite />);
    await screen.findByLabelText('用户新密码'); await fill();
    await userEvent.click(screen.getByRole('button', { name: '确认重置用户密码' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('网络不可用');
    expect(screen.getByLabelText('用户新密码')).toHaveAttribute('readonly');
    expect(resetTenantUserPassword).toHaveBeenCalledTimes(1);
    await userEvent.click(screen.getByRole('button', { name: '使用原请求重试' }));
    await screen.findByText('密码重置处理中（尚未完成）');
    expect(resetTenantUserPassword.mock.calls[0]).toEqual(resetTenantUserPassword.mock.calls[1]);
  });
  it('keeps editable input for definitive policy rejection', async () => {
    const api = { getUserCredentialJobs: vi.fn().mockResolvedValue({ managed: true, items: [] }), resetTenantUserPassword: vi.fn().mockRejectedValue(new ApiError('密码过短', 400, 'TENANT_PASSWORD_POLICY_REJECTED')) } as unknown as AdminApi;
    render(<TenantPasswordReset api={api} userId="u1" canWrite />);
    await screen.findByLabelText('用户新密码'); await fill();
    await userEvent.click(screen.getByRole('button', { name: '确认重置用户密码' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('密码过短');
    expect(screen.getByLabelText('用户新密码')).toHaveValue('TestPassword123!');
    expect(screen.getByLabelText('用户新密码')).not.toHaveAttribute('readonly');
  });
  it('hides standalone controls and refuses read-only editing', async () => {
    const standalone = { getUserCredentialJobs: vi.fn().mockResolvedValue({ managed: false, items: [] }) } as unknown as AdminApi;
    const { rerender } = render(<TenantPasswordReset api={standalone} userId="u1" canWrite />);
    await waitFor(() => expect(screen.queryByRole('region', { name: '平台登录密码' })).not.toBeInTheDocument());
    const reader = { getUserCredentialJobs: vi.fn().mockResolvedValue({ managed: true, items: [pending] }) } as unknown as AdminApi;
    rerender(<TenantPasswordReset api={reader} userId="u1" canWrite={false} />);
    await screen.findByText('仅具有用户管理写权限的管理员可以重置。');
    expect(screen.queryByLabelText('用户新密码')).not.toBeInTheDocument();
  });
  it('ignores delayed writes after user/admin context change', async () => {
    let resolve!: (job: TenantCredentialJob) => void;
    const original = { getUserCredentialJobs: vi.fn().mockResolvedValue({ managed: true, items: [] }), resetTenantUserPassword: vi.fn(() => new Promise<TenantCredentialJob>(r => { resolve = r; })) } as unknown as AdminApi;
    const { rerender } = render(<TenantPasswordReset api={original} userId="u1" canWrite />);
    await screen.findByLabelText('用户新密码'); await fill();
    await userEvent.click(screen.getByRole('button', { name: '确认重置用户密码' }));
    const next = { getUserCredentialJobs: vi.fn().mockResolvedValue({ managed: true, items: [] }) } as unknown as AdminApi;
    rerender(<TenantPasswordReset api={next} userId="u2" canWrite />);
    await screen.findByLabelText('用户新密码');
    await act(async () => resolve(pending));
    expect(screen.queryByText('密码重置处理中（尚未完成）')).not.toBeInTheDocument();
    expect(screen.getByLabelText('用户新密码')).toHaveValue('');
  });
});
