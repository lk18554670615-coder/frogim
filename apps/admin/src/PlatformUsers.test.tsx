import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { LightPlatform } from './LightPlatform';

const at='2026-01-03T00:00:00Z';
const services={apiBaseUrl:'https://a',imWsUrl:'wss://a/im',imTcpUrl:'tcp://a:5100',callSignalUrl:'wss://a/livekit',mediaBaseUrl:'https://a/media'};
const tenants=[{id:'a',name:'企业甲',enabled:true,services},{id:'b',name:'企业乙',enabled:false,services}];
const user={id:'user-012345678901234567890123',phone:'13800000001',tenantId:'a',assignmentVersion:2,banned:false,pendingOperation:'op-pending',registeredAt:at,membershipCount:2,currentMembership:{profileVersion:4,syncedAt:at,syncError:''},profile:{name:'甲企业昵称',handle:'alpha',avatarMediaId:'avatar-a',banned:true,createdAt:at}};
const memberships=[{tenantId:'a',current:true,profile:user.profile,profileVersion:4,syncedAt:at,syncError:''},{tenantId:'b',current:false,profile:{name:'乙企业昵称',handle:'beta',signature:'乙的签名',gender:'female',banned:false,allowSearchByPhone:true,allowSearchByHandle:false,createdAt:at},profileVersion:2,syncedAt:at,syncError:'企业连接不可达，无法确认最新资料'}];
afterEach(()=>{cleanup();vi.unstubAllGlobals();history.replaceState({},'','/');});
function setup(role='operator',details:any={user,memberships},query?: (body:any)=>Promise<any>) {
  const fetch=vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{
    const path=String(input);let data:unknown=[];
    if(path.endsWith('/auth/me'))data={username:'admin',role};
    if(path.endsWith('/tenants'))data=tenants;
    if(path.endsWith('/users/query'))data=query?await query(JSON.parse(String(init?.body))):{items:[user],hasMore:false};
    if(path.endsWith('/users/'+user.id))data=details;
    if(path.endsWith('/ban'))data={operationId:'op-complete'};
    return {ok:true,json:async()=>data} as Response;
  });vi.stubGlobal('fetch',fetch);return fetch;
}
it('shows independent account flags and metadata; avatar failure falls back and full ID copies',async()=>{
  setup();const writeText=vi.fn(async()=>{});vi.stubGlobal('navigator',{...navigator,clipboard:{writeText}});
  history.replaceState({},'','/platform/users');render(<LightPlatform/>);
  expect(await screen.findByText('甲企业昵称')).toBeInTheDocument();
  expect(screen.getByText('2 家')).toBeInTheDocument();expect(screen.getByText('alpha')).toBeInTheDocument();
  expect(screen.getByText('未全局封禁')).toBeInTheDocument();expect(screen.getByText('当前企业封禁')).toBeInTheDocument();expect(screen.getByText('操作未完成',{selector:'span'})).toBeInTheDocument();
  fireEvent.error(screen.getByAltText('用户头像'));expect(screen.getByLabelText('默认头像')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:'复制用户 ID'}));expect(writeText).toHaveBeenCalledWith(user.id);
});
it('enterprise filters and selected tabs survive close, direct open and back navigation',async()=>{
  const fetch=setup();history.replaceState({},'','/platform/users?tenant=a&tenantScope=membership&page=2');render(<LightPlatform/>);
  fireEvent.click(await screen.findByRole('button',{name:'资料'}));
  const dialog=await screen.findByRole('dialog',{name:'用户资料'});
  expect(within(dialog).getByRole('tab',{name:/企业甲/})).toHaveAttribute('aria-selected','true');
  fireEvent.click(within(dialog).getByRole('tab',{name:'企业乙'}));
  expect(within(dialog).getByRole('tabpanel')).toHaveTextContent('乙企业昵称');
  expect(within(dialog).getByRole('tabpanel')).toHaveTextContent('乙的签名');expect(within(dialog).getByRole('tabpanel')).toHaveTextContent('该企业已停止登录');
  expect(within(dialog).getByRole('tabpanel')).toHaveTextContent('最后保存的资料快照');
  expect(location.search).toContain('profileTenant=b');
  const detailCalls=fetch.mock.calls.filter(([path])=>String(path).endsWith('/users/'+user.id)).length;
  fireEvent.click(within(dialog).getByRole('button',{name:'关闭'}));expect(location.search).toBe('?tenant=a&tenantScope=membership&page=2');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  act(()=>history.back());await waitFor(()=>expect(location.search).toContain('profileTenant=b'));
  expect(await screen.findByRole('tabpanel')).toHaveTextContent('乙企业昵称');
  expect(detailCalls).toBe(1);
});
it('direct detail selects requested enterprise and readonly cannot write',async()=>{
  setup('viewer');history.replaceState({},'','/platform/users/'+user.id+'?profileTenant=b');render(<LightPlatform/>);
  const panel=await screen.findByRole('tabpanel');expect(panel).toHaveTextContent('乙企业昵称');expect(panel).toHaveTextContent('女');
  const dialog=screen.getByRole('dialog');expect(within(dialog).getByRole('button',{name:'封禁'})).toBeDisabled();expect(within(dialog).getByRole('button',{name:'重置密码'})).toBeDisabled();
  expect(within(dialog).queryByText('avatarMediaId')).not.toBeInTheDocument();expect(within(dialog).queryByText('profileVersion')).not.toBeInTheDocument();
});
it('missing snapshots do not present empty boolean fields as confirmed status',async()=>{
  setup('viewer',{user,memberships:[{tenantId:'a',current:true,profile:{name:'初始化昵称'},profileVersion:0,syncedAt:null,syncError:''}]});
  history.replaceState({},'','/platform/users/'+user.id);render(<LightPlatform/>);
  const panel=await screen.findByRole('tabpanel');expect(panel).toHaveTextContent('完整资料尚未同步');expect(within(panel).getAllByText('待同步').length).toBeGreaterThan(4);
  expect(within(panel).queryByText('未封禁')).not.toBeInTheDocument();expect(within(panel).queryByText('关闭')).not.toBeInTheDocument();
});
it('search is debounced POST-only and remains when closing a drawer',async()=>{
  const fetch=setup();history.replaceState({},'','/platform/users');render(<LightPlatform/>);await screen.findByText('甲企业昵称');
  fireEvent.change(screen.getByRole('textbox',{name:'搜索用户'}),{target:{value:user.phone}});
  await waitFor(()=>expect(fetch).toHaveBeenCalledWith('/platform/admin/users/query',expect.objectContaining({method:'POST',body:expect.stringContaining(user.phone)})));
  expect(location.search).toBe('');fireEvent.click(await screen.findByRole('button',{name:'资料'}));
  fireEvent.click(await screen.findByRole('button',{name:'关闭'}));expect(screen.getByRole('textbox',{name:'搜索用户'})).toHaveValue(user.phone);
  fireEvent.change(screen.getByLabelText('全局封禁筛选'),{target:{value:'yes'}});
  await waitFor(()=>expect(fetch).toHaveBeenCalledWith('/platform/admin/users/query',expect.objectContaining({body:expect.stringContaining('"banned":true')})));
  expect(location.search).not.toContain(user.phone);
});
it('late list responses cannot overwrite a newer enterprise filter',async()=>{
  let resolveOld:(data:any)=>void=()=>{};
  setup('operator',undefined,body=>body.tenant==='a'?new Promise(resolve=>{resolveOld=resolve}):Promise.resolve({items:[user],hasMore:false}));
  history.replaceState({},'','/platform/users?tenant=a');render(<LightPlatform/>);
  await screen.findByRole('option',{name:'企业乙'});fireEvent.change(screen.getByLabelText('筛选企业'),{target:{value:'b'}});
  await screen.findByText('甲企业昵称');await act(async()=>resolveOld({items:[{...user,id:'late',profile:{name:'迟到用户'}}],hasMore:true}));
  expect(screen.queryByText('迟到用户')).not.toBeInTheDocument();expect(screen.getByRole('button',{name:'下一页'})).toBeDisabled();
});
it('late detail response cannot overwrite the next user',async()=>{
  let resolveOld:(data:any)=>void=()=>{};
  vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL)=>{
    const path=String(input);let data:unknown=[];
    if(path.endsWith('/auth/me'))data={username:'admin',role:'viewer'};
    if(path.endsWith('/tenants'))data=tenants;
    if(path.endsWith('/users/query'))data={items:[],hasMore:false};
    if(path.endsWith('/users/'+user.id))data=await new Promise(resolve=>{resolveOld=resolve});
    if(path.endsWith('/users/next-user'))data={user:{...user,id:'next-user',profile:{name:'新用户'}},memberships:[]};
    return {ok:true,json:async()=>data} as Response;
  }));
  history.replaceState({},'','/platform/users/'+user.id);render(<LightPlatform/>);
  await screen.findByRole('dialog',{name:'用户资料'});
  await act(async()=>{history.pushState({},'','/platform/users/next-user');dispatchEvent(new PopStateEvent('popstate'));});
  await screen.findByRole('heading',{name:'新用户'});
  await act(async()=>resolveOld({user,memberships}));
  expect(screen.getByRole('heading',{name:'新用户'})).toBeInTheDocument();expect(screen.queryByRole('heading',{name:'甲企业昵称'})).not.toBeInTheDocument();
});
it('an unknown enterprise tag never substitutes another enterprise profile',async()=>{
  setup();history.replaceState({},'','/platform/users/'+user.id+'?profileTenant=missing');render(<LightPlatform/>);
  expect(await screen.findByText('该企业不在此用户的账号列表中')).toBeInTheDocument();expect(screen.queryByRole('tabpanel')).not.toBeInTheDocument();
});
