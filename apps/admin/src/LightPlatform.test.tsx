import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {afterEach,expect,it,vi} from 'vitest';
import {LightPlatform,PlatformDialog} from './LightPlatform';

const tenant={id:'a',name:'测试企业 A',code:'A',enabled:true,isDefault:true,version:1,controlUrl:'https://a:8443',services:{apiBaseUrl:'https://a',imWsUrl:'wss://a/im',imTcpUrl:'tcp://a:5100',callSignalUrl:'wss://a/rtc',mediaBaseUrl:'https://a/media'}};
afterEach(()=>{cleanup();vi.unstubAllGlobals();history.replaceState({},'','/')});
function api(role='operator'){
 const fetch=vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{
  const path=String(input);let data:unknown=[];
  if(path.endsWith('/auth/me'))data={username:'admin',role};
  if(path.endsWith('/tenants'))data=[tenant];
  if(path.endsWith('/users/query'))data={items:[{id:'u',phone:'13800000001',tenantId:'a',assignmentVersion:1,profile:{name:'验收用户'},banned:false}],hasMore:false};
  if(path.endsWith('/users/u'))data={user:{id:'u',phone:'13800000001',tenantId:'a',profile:{name:'验收用户'}},memberships:[]};
  if(path.endsWith('/users/u/ban'))data={operationId:'op1',phase:'done'};
  if(path.endsWith('/operations'))data=[{id:'op1',userId:'u',actor:'operator-test',kind:'switch',source:'a',target:'b',version:3,phase:'source_revoking',reason:'用户申请切换\n保留旧资料',error:'旧企业确认丢失',updatedAt:'2026-10-01T00:00:00Z'}];
  return {ok:true,json:async()=>data} as Response;
 });vi.stubGlobal('fetch',fetch);return fetch;
}
it('restores login, opens a direct drawer and supports list edit',async()=>{
 api();history.replaceState({},'','/platform/tenants/a');render(<LightPlatform/>);
 expect(await screen.findByRole('dialog',{name:'企业详情'})).toBeInTheDocument();
 expect(screen.getByText('企业 API')).toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:'关闭'}));
 await waitFor(()=>expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
 expect(location.pathname).toBe('/platform/tenants');
 fireEvent.click(screen.getByRole('button',{name:'编辑'}));expect(screen.getByRole('dialog',{name:'编辑企业'})).toBeInTheDocument();
 expect(screen.getByLabelText('企业名称')).toHaveValue('测试企业 A');
});
it('readonly cannot mutate and sensitive search stays out of the URL',async()=>{
 api('viewer');history.replaceState({},'','/platform/users');render(<LightPlatform/>);
 expect(await screen.findByText('验收用户')).toBeInTheDocument();
 expect(screen.getByRole('button',{name:'切换'})).toBeDisabled();expect(screen.getByRole('button',{name:'封禁'})).toBeDisabled();
 fireEvent.change(screen.getByRole('textbox',{name:'搜索用户'}),{target:{value:'13800000001'}});
 expect(location.search).toBe('');
});
it('requires reason and confirmation, then shows operation result',async()=>{
 const fetch=api();history.replaceState({},'','/platform/users');render(<LightPlatform/>);
 fireEvent.click(await screen.findByRole('button',{name:'封禁'}));
 expect(screen.getByRole('button',{name:'确认执行'})).toBeDisabled();
 fireEvent.change(screen.getByLabelText('操作理由'),{target:{value:'本机验收'}});
 fireEvent.click(screen.getByLabelText('已确认对象与影响范围'));
 fireEvent.click(screen.getByRole('button',{name:'确认执行'}));
 expect(await screen.findByRole('status')).toHaveTextContent('操作已完成：op1');
 expect(fetch).toHaveBeenCalledWith('/platform/admin/users/u/ban',expect.objectContaining({method:'POST',body:expect.stringContaining('本机验收')}));
});
it('common dialog closes on Escape and restores focus and scrolling',()=>{
 const close=vi.fn();const button=document.createElement('button');document.body.append(button);button.focus();
 const view=render(<PlatformDialog title="确认" onClose={close}><input aria-label="理由"/></PlatformDialog>);
 expect(document.body.style.overflow).toBe('hidden');expect(document.documentElement.style.overflow).toBe('hidden');fireEvent.keyDown(document,{key:'Escape'});expect(close).toHaveBeenCalledOnce();view.unmount();expect(document.activeElement).toBe(button);expect(document.body.style.overflow).toBe('');expect(document.documentElement.style.overflow).toBe('');button.remove();
});

it('readonly can inspect operation object, actor, reason and error without being allowed to retry',async()=>{
 api('viewer');history.replaceState({},'','/platform/operations');render(<LightPlatform/>);
 fireEvent.click(await screen.findByRole('button',{name:'详情'}));
 const dialog=screen.getByRole('dialog',{name:'操作详情'});
 expect(dialog).toHaveTextContent('operator-test');expect(dialog).toHaveTextContent('用户申请切换');
 expect(dialog).toHaveTextContent('保留旧资料');expect(dialog).toHaveTextContent('旧企业确认丢失');
 expect(dialog).toHaveTextContent('source_revoking');expect(dialog).toHaveTextContent('op1');
 expect(screen.getByRole('button',{name:'重试'})).toBeDisabled();
 expect(location.pathname).toBe('/platform/operations');expect(location.search).toBe('');
 fireEvent.click(screen.getByRole('button',{name:'关闭'}));expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});
