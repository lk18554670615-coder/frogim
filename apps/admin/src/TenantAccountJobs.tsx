import { useEffect, useRef, useState } from 'react';
import type { AdminApi, TenantAccountJob } from './types';

export function accountJobLabel(job: TenantAccountJob): string {
  if (job.status === 'completed') return '已开通';
  if (job.status === 'blocked') return job.errorCode === 'TENANT_PASSWORD_POLICY_REJECTED' ? '已暂停：密码策略变化，请联系平台处理' : '已暂停，请联系平台处理';
  return '开通中（尚未成功）';
}

// Manual refresh avoids background privileged polling. A newer load/unmount
// fences delayed responses, including an administrator account switch.
export function TenantAccountJobsPanel({ api, revision }: { api: AdminApi; revision: number }) {
  const [state, setState] = useState<{ managed: boolean; items: TenantAccountJob[] }>();
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const epoch = useRef(0);
  const refresh = async () => {
    const generation = ++epoch.current;
    setLoading(true); setError('');
    try { const value = await api.getAccountProvisioningJobs(); if (epoch.current === generation) setState(value); }
    catch (cause) { if (epoch.current === generation) setError(cause instanceof Error ? cause.message : '开户任务查询失败'); }
    finally { if (epoch.current === generation) setLoading(false); }
  };
  useEffect(() => { setState(undefined); void refresh(); return () => { epoch.current++; }; }, [api, revision]);
  if (state?.managed === false) return null;
  return <section className="settings-section" aria-label="平台开户任务">
    <h2>平台开户任务</h2><p>仅显示本企业最近 100 笔任务。提交成功不代表已开通；网络超时后先核对这里，不要更换请求号重复开户。</p>
    <button type="button" className="button secondary compact" disabled={loading} onClick={() => void refresh()}>{loading ? '正在查询…' : '刷新开户任务'}</button>
    {error && <p role="alert" className="danger-text">{error}</p>}
    {state?.managed && <div className="table-wrap"><table><thead><tr><th>号码 / 昵称</th><th>状态</th><th>提交时间</th><th>请求号</th></tr></thead><tbody>{state.items.map(job => <tr key={job.jobId}><td>{job.phone}<small>{job.name}</small></td><td>{accountJobLabel(job)}</td><td>{new Date(job.createdAt).toLocaleString()}</td><td className="mono">{job.requestId}</td></tr>)}</tbody></table>{state.items.length === 0 && <p>暂无开户任务</p>}</div>}
  </section>;
}
