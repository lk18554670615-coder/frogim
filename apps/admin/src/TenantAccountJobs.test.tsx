import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { accountJobLabel, TenantAccountJobsPanel } from './TenantAccountJobs';
import type { AdminApi, TenantAccountJob } from './types';

const pending: TenantAccountJob = { jobId: 'job_a', requestId: 'request_a', localUserId: 'usr_a', phone: '02800000001', name: '开通账号', status: 'pending', createdAt: '2026-09-28T00:00:00Z' };
describe('enterprise account provisioning status', () => {
  it('shows pending, then completed only after an explicit refresh', async () => {
    const getAccountProvisioningJobs = vi.fn().mockResolvedValueOnce({ managed: true, items: [pending] }).mockResolvedValue({ managed: true, items: [{ ...pending, status: 'completed' }] });
    render(<TenantAccountJobsPanel api={{ getAccountProvisioningJobs } as unknown as AdminApi} revision={0} />);
    expect(await screen.findByText('开通中（尚未成功）')).toBeInTheDocument();
    expect(screen.queryByText('已开通')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '刷新开户任务' }));
    expect(await screen.findByText('已开通')).toBeInTheDocument();
    expect(getAccountProvisioningJobs).toHaveBeenCalledTimes(2);
  });
  it('does not expose prior admin task results after changing API identity', async () => {
    let resolveOld!: (value: unknown) => void;
    const old = { getAccountProvisioningJobs: () => new Promise(resolve => { resolveOld = resolve; }) } as unknown as AdminApi;
    const next = { getAccountProvisioningJobs: vi.fn().mockResolvedValue({ managed: true, items: [] }) } as unknown as AdminApi;
    const { rerender } = render(<TenantAccountJobsPanel api={old} revision={0} />);
    rerender(<TenantAccountJobsPanel api={next} revision={0} />);
    expect(await screen.findByText('暂无开户任务')).toBeInTheDocument();
    resolveOld({ managed: true, items: [pending] });
    await waitFor(() => expect(screen.queryByText(pending.phone)).not.toBeInTheDocument());
  });
  it('hides standalone task panel, reports paused and query errors honestly', async () => {
    expect(accountJobLabel({ ...pending, status: 'blocked', errorCode: 'TENANT_PASSWORD_POLICY_REJECTED' })).toContain('密码策略');
    const api = { getAccountProvisioningJobs: vi.fn().mockResolvedValue({ managed: false, items: [] }) } as unknown as AdminApi;
    render(<TenantAccountJobsPanel api={api} revision={0} />);
    await waitFor(() => expect(screen.queryByRole('region', { name: '平台开户任务' })).not.toBeInTheDocument());
  });
});
