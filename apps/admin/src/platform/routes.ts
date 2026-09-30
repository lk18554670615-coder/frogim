export const pageGroups = [
  { label: '工作台', pages: [['overview', '总览']] },
  { label: '企业', pages: [['tenants', '企业目录'], ['codes', '企业邀请码'], ['servers', '服务器资源']] },
  { label: '运行', pages: [['realm-jobs', '企业启停任务'], ['deployments', '部署任务'], ['backups', '备份任务'], ['maintenance', '每日维护']] },
  { label: '账号', pages: [['accounts', '账号归属'], ['jobs', '身份任务'], ['access-jobs', '封禁任务']] },
  { label: '发布', pages: [['client-versions', '客户端版本']] },
  { label: '治理', pages: [['administrators', '平台管理员'], ['audits', '运维审计']] },
] as const;

export type Tab = (typeof pageGroups)[number]['pages'][number][0];
export type Route = { tab: Tab; tenant: string; tenantId: string; state: string; archive: string; page: number };
const allPages: readonly (readonly [Tab, string])[] = pageGroups.flatMap(group => group.pages as readonly (readonly [Tab, string])[]);
const tabs = new Set<string>(allPages.map(([tab]) => tab));
export const pageLabel = (tab: Tab) => allPages.find(([id]) => id === tab)?.[1] ?? '总览';
export const pageGroup = (tab: Tab) => pageGroups.find(group => group.pages.some(([id]) => id === tab))?.label ?? '工作台';

export function readRoute(): Route {
  const url = new URL(window.location.href);
  const parts = url.pathname.replace(/^\/platform\/?/, '').split('/').filter(Boolean);
  const tab = tabs.has(parts[0]) ? parts[0] as Tab : 'overview';
  const tenant = tab === 'tenants' && parts.length === 2 && /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$/.test(parts[1]) ? parts[1] : '';
  const pageValue = Number(url.searchParams.get('page'));
  return {
    tab, tenant,
    tenantId: (url.searchParams.get('tenantId') ?? '').slice(0, 80),
    state: (url.searchParams.get('state') ?? '').slice(0, 40),
    archive: ['active', 'archived', 'all'].includes(url.searchParams.get('archive') ?? '') ? url.searchParams.get('archive')! : 'active',
    page: Number.isSafeInteger(pageValue) && pageValue >= 1 && pageValue <= 10000 ? pageValue : 1,
  };
}

export function routePath(route: Route): string {
  const path = `/platform/${route.tab}${route.tenant ? `/${encodeURIComponent(route.tenant)}` : ''}`;
  const params = new URLSearchParams();
  if (route.tab !== 'overview' && !route.tenant) {
    if (route.tenantId) params.set('tenantId', route.tenantId);
    if (route.state) params.set('state', route.state);
    if (route.tab === 'tenants' && route.archive !== 'active') params.set('archive', route.archive);
    if (route.page > 1) params.set('page', String(route.page));
  }
  return path + (params.size ? `?${params}` : '');
}
