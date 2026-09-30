import { useEffect, useState } from 'react';
import { Activity, ArrowUpRight, Building2, ClipboardList, ShieldAlert } from 'lucide-react';
import { PlatformClient, taskErrors, type Overview as OverviewData } from './api';
import type { Tab } from './routes';

const workNames: Record<OverviewData['items'][number]['kind'], string> = {
  jobs: '身份任务', 'access-jobs': '封禁任务', 'realm-jobs': '企业启停任务',
  deployments: '部署任务', backups: '备份任务', maintenance: '每日维护',
};
const time = (value: string) => Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '时间待核对';

export function PlatformOverview({ client, refresh, onNavigate }: { client: PlatformClient; refresh: number; onNavigate: (tab: Tab, tenantId?: string) => void }) {
  const [data, setData] = useState<OverviewData | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true;
    const abort = new AbortController();
    setData(null); setError('');
    client.request<OverviewData>('/overview', 'GET', undefined, abort.signal)
      .then(value => { if (!value.tenants || !value.work || !Array.isArray(value.items)) throw new Error('总览数据不完整，请刷新后重试'); if (active) setData(value); })
      .catch(e => { if (active) setError(e instanceof Error ? e.message : '总览加载失败'); });
    return () => { active = false; abort.abort(); };
  }, [client, refresh]);

  if (error) return <section className="platform-state error" role="alert"><h2>总览暂不可用</h2><p>{error}</p><p>可通过左侧导航继续查看各管理页。</p></section>;
  if (!data) return <section className="platform-state" role="status">正在汇总平台运行状态…</section>;
  return <div className="platform-overview">
    <p className="dashboard-source"><Activity size={14} aria-hidden="true" />数据截至 {time(data.generatedAt)}；点击页面右上角“刷新”获取新状态。</p>
    <div className="metric-strip" aria-label="运行摘要">
      <button className="metric" onClick={() => onNavigate('tenants')}><div className="metric-heading"><span>在用企业</span><span className="metric-icon"><Building2 size={22} /></span></div><strong>{data.tenants.active}</strong><p>待开通 {data.tenants.provisioning} · 停用中及已停用 {data.tenants.suspended}</p></button>
      <button className="metric" onClick={() => onNavigate('jobs')}><div className="metric-heading"><span>进行中任务</span><span className="metric-icon"><ClipboardList size={22} /></span></div><strong>{data.work.inProgress}</strong><p>各类任务进行中，未计入待处理</p></button>
      <button className={`metric ${data.work.attention ? 'attention' : ''}`} onClick={() => document.getElementById('attention-list')?.scrollIntoView({ block: 'start' })}><div className="metric-heading"><span>需要处理</span><span className="metric-icon"><ShieldAlert size={22} /></span></div><strong>{data.work.attention}</strong><p>异常或结果未确认的任务</p></button>
    </div>
    <div className="dashboard-grid"><section className="panel" id="attention-list" aria-label="待处理事项"><div className="panel-heading"><div><h2>需要人工核对</h2><p>按创建时间显示最早的 10 条；进入任务页核对状态后再处理。</p></div><span className="count-pill">共 {data.work.attention} 条</span></div>
      {data.items.length === 0 ? <div className="platform-empty"><strong>目前没有待处理任务</strong><p>任务状态可能变化，请按需手动刷新。</p></div> : <div className="attention-list">{data.items.map(item => <button key={`${item.kind}-${item.id}`} onClick={() => onNavigate(item.kind as Tab, item.tenantId)}><span className="attention-mark" aria-hidden="true" /><span><strong>{workNames[item.kind]}</strong><small>{item.tenantId || '平台'} · 更新于 {time(item.updatedAt)}</small><code>{item.id}</code><small>{taskErrors[item.errorCode] || (item.errorCode ? `错误代码：${item.errorCode}` : '结果需要核对')}</small></span><span className="attention-open">查看任务 →</span></button>)}</div>}
    </section>
    <section className="panel" aria-label="常用入口"><div className="panel-heading"><div><h2>常用管理</h2><p>直接进入最常使用的平台管理页。</p></div></div><div className="quick-grid">{([['tenants', '企业目录', '查看企业资料及关联资源'], ['accounts', '账号归属', '核对平台账号归属'], ['client-versions', '客户端版本', '检查版本发布策略'], ['audits', '运维审计', '查看平台操作记录']] as const).map(([tab, title, detail]) => <button key={tab} onClick={() => onNavigate(tab)}><span><strong>{title}</strong><small>{detail}</small></span><ArrowUpRight size={16} aria-hidden="true" /></button>)}</div></section></div>
  </div>;
}
