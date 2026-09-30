import { useEffect, useRef, useState, type MouseEvent, type ReactNode } from 'react';
import { Activity, Archive, Bell, ChevronDown, ChevronLeft, ChevronRight, ClipboardList, CloudUpload, DatabaseBackup, FileClock, KeyRound, LayoutDashboard, LogOut, Menu, Search, Server, ShieldCheck, Users } from 'lucide-react';
import type { Operator } from './api';
import { pageGroups, pageLabel, routePath, type Tab } from './routes';

const groupIcons = { 企业: Server, 运行: Activity, 账号: Users, 发布: CloudUpload, 治理: ShieldCheck };
const pageIcons: Record<Tab, typeof Users> = {
  overview: LayoutDashboard, tenants: Server, codes: KeyRound, servers: Server,
  'realm-jobs': Activity, deployments: CloudUpload, backups: DatabaseBackup,
  maintenance: Archive, accounts: Users, jobs: ClipboardList,
  'access-jobs': ShieldCheck, 'client-versions': Bell,
  administrators: ShieldCheck, audits: FileClock,
};
const routeFor = (tab: Tab) => routePath({ tab, tenant: '', tenantId: '', state: '', archive: 'active', page: 1 });

export function PlatformChrome({ tab, operator, loggingOut, onNavigate, onLogout, children }: {
  tab: Tab; operator: Operator; loggingOut: boolean;
  onNavigate: (tab: Tab) => void; onLogout: () => void; children: ReactNode;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const [hovered, setHovered] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [compact, setCompact] = useState(() => window.matchMedia?.('(max-width: 1120px)').matches ?? false);
  const [expanded, setExpanded] = useState<Record<string, boolean>>(() => Object.fromEntries(pageGroups.map(group => [group.label, group.pages.some(([id]) => id === tab)])));
  const [search, setSearch] = useState('');
  const searchRef = useRef<HTMLInputElement>(null);
  const normalized = search.trim().toLocaleLowerCase();
  const results = normalized ? pageGroups.flatMap(group => group.pages.map(([id, label]) => ({ id, label, group: group.label }))).filter(item => `${item.label} ${item.group} ${item.id}`.toLocaleLowerCase().includes(normalized)).slice(0, 8) : [];

  useEffect(() => {
    const group = pageGroups.find(item => item.pages.some(([id]) => id === tab));
    if (group) setExpanded(current => current[group.label] ? current : { ...current, [group.label]: true });
    setMobileOpen(false);
  }, [tab]);
  useEffect(() => {
    const media = window.matchMedia?.('(max-width: 1120px)');
    if (!media) return;
    const update = () => { setCompact(media.matches); if (!media.matches) setMobileOpen(false); };
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, []);
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); searchRef.current?.focus(); }
      if (event.key === 'Escape') { setSearch(''); if (document.activeElement === searchRef.current) searchRef.current?.blur(); setMobileOpen(false); }
    };
    document.addEventListener('keydown', shortcut);
    return () => document.removeEventListener('keydown', shortcut);
  }, []);

  const visit = (event: MouseEvent<HTMLAnchorElement>, next: Tab) => {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault(); setSearch(''); setMobileOpen(false); onNavigate(next);
  };
  const link = (id: Tab, label: string, className: string) => {
    const Icon = pageIcons[id];
    return <a key={id} href={routeFor(id)} className={`${className} ${tab === id ? 'active' : ''}`} aria-current={tab === id ? 'page' : undefined} onClick={event => visit(event, id)}><Icon size={className.includes('leaf') ? 15 : 18} aria-hidden="true" /><span>{label}</span></a>;
  };

  return <div className={`app-shell tailadmin-shell platform-shell ${collapsed ? 'sidebar-collapsed' : ''} ${hovered ? 'sidebar-hovered' : ''} ${mobileOpen ? 'platform-nav-open' : ''}`}>
    {mobileOpen && <button className="nav-scrim" aria-label="关闭导航" onClick={() => setMobileOpen(false)} />}
    <aside id="platform-navigation" className="sidebar" aria-label="平台导航" onMouseEnter={() => { if (collapsed) setHovered(true); }} onMouseLeave={() => setHovered(false)}>
      <div className="brand"><div className="brand-mark"><img src="/qingwaguagua-mark.png" alt="" /></div><div className="brand-copy"><strong>青蛙呱呱</strong><span>平台管理</span></div></div>
      <nav aria-label="主导航"><p className="sidebar-nav-label">功能菜单</p>{link('overview', '总览', 'nav-item nav-overview')}
        {pageGroups.filter(group => group.label !== '工作台').map(group => {
          const Icon = groupIcons[group.label as keyof typeof groupIcons];
          const isActive = group.pages.some(([id]) => id === tab);
          return <section className="nav-section" key={group.label}>
            <button type="button" className={`nav-section-toggle ${isActive ? 'has-active' : ''}`} aria-expanded={Boolean(expanded[group.label])} aria-controls={`platform-nav-${group.label}`} onClick={() => { if (collapsed) setCollapsed(false); setExpanded(current => ({ ...current, [group.label]: !current[group.label] })); }}><Icon className="nav-section-icon" size={18} aria-hidden="true" /><span>{group.label}</span><ChevronDown className="nav-section-chevron" size={15} aria-hidden="true" /></button>
            <div id={`platform-nav-${group.label}`} className="nav-section-items" hidden={!expanded[group.label]}>{group.pages.map(([id, label]) => link(id, label, 'nav-item nav-leaf-item'))}</div>
          </section>;
        })}
      </nav>
      <div className="sidebar-foot"><div className="admin-avatar" aria-hidden="true">{[...operator.username][0] || '管'}</div><div className="sidebar-account-copy"><strong>@{operator.username}</strong><span>{operator.role === 'operator' ? '运营管理员' : '只读管理员'}</span></div><div className="sidebar-account-actions"><button className="icon-button sidebar-logout" title="退出登录" aria-label="退出登录" disabled={loggingOut} onClick={onLogout}><LogOut size={17} /></button></div></div>
    </aside>
    <div className="workspace"><div className="topbar"><div className="topbar-primary">
      <button type="button" className="icon-button menu-button" aria-label={compact ? (mobileOpen ? '关闭导航' : '打开导航') : (collapsed ? '展开侧栏' : '收起侧栏')} aria-controls="platform-navigation" aria-expanded={compact ? mobileOpen : !collapsed} onClick={() => compact ? setMobileOpen(value => !value) : setCollapsed(value => !value)}>{compact ? <Menu size={20} /> : collapsed ? <ChevronRight size={19} /> : <ChevronLeft size={19} />}</button>
      <div className="global-search-shell"><Search size={18} aria-hidden="true" /><input ref={searchRef} type="search" aria-label="查找功能" placeholder="搜索功能或输入命令…" autoComplete="off" value={search} onChange={event => setSearch(event.target.value)} /><kbd>Ctrl K</kbd>
        {normalized && <div className="global-search-results" role="listbox" aria-label="搜索结果">{results.length ? results.map(item => { const Icon = pageIcons[item.id]; return <a key={item.id} href={routeFor(item.id)} role="option" aria-selected={tab === item.id} onClick={event => visit(event, item.id)}><span className="search-result-icon"><Icon size={16} /></span><span><strong>{item.label}</strong><small>{item.group}</small></span><ChevronRight size={15} /></a>; }) : <div className="global-search-empty">没有匹配的功能</div>}</div>}
      </div><strong className="mobile-page-label">{pageLabel(tab)}</strong>
    </div><div className="topbar-actions"><div className="topbar-status">数据按需刷新</div><div className="topbar-user"><span className="admin-avatar">{[...operator.username][0] || '管'}</span><span><strong>{operator.username}</strong><small>{operator.role === 'operator' ? '运营管理员' : '只读管理员'}</small></span></div></div></div>
    <main className="main-content">{children}</main></div>
  </div>;
}
