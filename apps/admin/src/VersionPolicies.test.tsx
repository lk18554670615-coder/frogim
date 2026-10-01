import { cleanup,fireEvent,render,screen,waitFor } from '@testing-library/react';
import { afterEach,expect,it,vi } from 'vitest';
import { VersionPolicies } from './VersionPolicies';
import { PlatformDialog } from './PlatformDialog';

const android={platform:'android',version:7,policy:{latestVersion:'1.0.12',minimumVersion:'1.0.10',forceUpdate:false,downloadUrl:'https://example.com/app.apk',releaseNotes:'修复消息同步',retainedField:'keep'}};
afterEach(()=>{cleanup();history.replaceState({},'','/');});
function setup(items=[android],canWrite=true){
  const request=vi.fn().mockImplementation((path:string)=>Promise.resolve(path.includes('/history?')?{items:[],hasMore:false}:{ok:true})),saved=vi.fn().mockResolvedValue(undefined);
  render(<VersionPolicies items={items} loading={false} canWrite={canWrite} request={request} onSaved={saved}/>);
  return {request,saved};
}
function review(){fireEvent.click(screen.getByRole('button',{name:'下一步：确认影响'}));}
function confirm(){fireEvent.change(screen.getByLabelText('操作理由'),{target:{value:'发布本机验收策略'}});fireEvent.click(screen.getByRole('checkbox',{name:/已确认 Android/}));}

it('shows only the selected client, supports safe deep links and distinguishes configured from unconfigured',()=>{
  history.replaceState({},'','/platform/versions?client=ios');
  const {request}=setup();
  expect(screen.getByText('尚未配置 iOS 更新策略')).toBeInTheDocument();
  expect(screen.queryByText('1.0.12')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:'Android 已配置'}));
  expect(location.search).toBe('?client=android');expect(screen.getByText('修复消息同步')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:'Web 未配置'}));
  expect(screen.getByText('尚未配置 Web 更新策略')).toBeInTheDocument();
  history.replaceState({},'','/platform/versions?client=android');fireEvent(window,new PopStateEvent('popstate'));
  expect(screen.getByText('修复消息同步')).toBeInTheDocument();expect(request.mock.calls.some(call=>call[2]==='PUT')).toBe(false);
});
it('validates version range and URL inline before sending any request',()=>{
  const {request}=setup([]);
  fireEvent.click(screen.getByRole('button',{name:'配置 Android 策略'}));
  expect(screen.getByLabelText('最新版本')).toHaveValue('');
  review();expect(screen.getByLabelText('最新版本')).toHaveFocus();
  fireEvent.change(screen.getByLabelText('最新版本'),{target:{value:'1.0.12'}});
  fireEvent.change(screen.getByLabelText('最低可用版本'),{target:{value:'1.0.13'}});
  review();expect(screen.getByText('最低可用版本不能高于最新版本。')).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('最低可用版本'),{target:{value:'1.0.10'}});
  fireEvent.change(screen.getByLabelText('下载／更新地址'),{target:{value:'javascript:alert(1)'}});
  review();expect(screen.getByLabelText('下载／更新地址')).toHaveAttribute('aria-invalid','true');
  expect(request.mock.calls.some(call=>call[2]==='PUT')).toBe(false);
});
it('previews forced update impact, requires reason and confirmation, and preserves the current record version',async()=>{
  const {request,saved}=setup();
  fireEvent.click(screen.getByRole('button',{name:'编辑 Android 策略'}));
  fireEvent.click(screen.getByRole('radio',{name:/强制更新至最新版本/}));review();
  expect(screen.getByText(/必须更新至最新版本/)).toBeInTheDocument();
  expect(screen.getByRole('button',{name:'保存策略'})).toBeDisabled();confirm();
  fireEvent.click(screen.getByRole('button',{name:'保存策略'}));
  await waitFor(()=>expect(saved).toHaveBeenCalledWith('Android 版本策略已保存'));
  expect(request).toHaveBeenCalledWith('/versions/android',expect.objectContaining({version:7,reason:'发布本机验收策略',confirmed:true,policy:expect.objectContaining({forceUpdate:true,retainedField:'keep'})}),'PUT');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});
it('creates an unconfigured client without assuming a release number',async()=>{
  const {request}=setup([]);fireEvent.click(screen.getByRole('button',{name:'配置 Android 策略'}));
  fireEvent.change(screen.getByLabelText('最新版本'),{target:{value:'2.0.1'}});
  fireEvent.change(screen.getByLabelText('最低可用版本'),{target:{value:'2.0.0'}});
  fireEvent.change(screen.getByLabelText('下载／更新地址'),{target:{value:'https://example.com/new.apk'}});
  review();confirm();fireEvent.click(screen.getByRole('button',{name:'保存策略'}));
  await waitFor(()=>expect(request).toHaveBeenCalledWith('/versions/android',expect.objectContaining({version:0,policy:expect.objectContaining({latestVersion:'2.0.1',minimumVersion:'2.0.0'})}),'PUT'));
});
it('explains concurrent modification and reloads a fresh version instead of retrying version zero',async()=>{
  const {request}=setup();
  request.mockRejectedValueOnce(new Error('CONFLICT')).mockResolvedValueOnce([{...android,version:9,policy:{...android.policy,latestVersion:'1.0.14'}}]);
  fireEvent.click(screen.getByRole('button',{name:'编辑 Android 策略'}));review();confirm();
  fireEvent.click(screen.getByRole('button',{name:'保存策略'}));
  expect(await screen.findByRole('alert')).toHaveTextContent('已被其他管理员更新');
  expect(screen.queryByRole('button',{name:'保存策略'})).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:'重新载入最新策略'}));
  await waitFor(()=>expect(screen.getByLabelText('最新版本')).toHaveValue('1.0.14'));
  review();expect(screen.getByRole('button',{name:'保存策略'})).toBeDisabled();confirm();
  fireEvent.click(screen.getByRole('button',{name:'保存策略'}));
  await waitFor(()=>expect(request).toHaveBeenLastCalledWith('/versions/android',expect.objectContaining({version:9}),'PUT'));
});
it('keeps all client policies readonly for a viewer',()=>{
  const {request}=setup([android],false);
  expect(screen.getByRole('button',{name:'编辑 Android 策略'})).toBeDisabled();
  fireEvent.click(screen.getByRole('button',{name:'iOS 未配置'}));
  expect(screen.getByRole('button',{name:'配置 iOS 策略'})).toBeDisabled();expect(request.mock.calls.some(call=>call[2]==='PUT')).toBe(false);
});
it('opens history at the top, keeps client and history selection in safe URLs and restores browser navigation',async()=>{
  history.replaceState({},'','/platform/versions?client=android&tab=history');
  const {request}=setup();
  expect(screen.getByRole('button',{name:'发布历史'})).toHaveAttribute('aria-pressed','true');
  await screen.findByText(/暂无 Android 发布历史/);
  expect(screen.queryByRole('button',{name:'编辑 Android 策略'})).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:'iOS 未配置'}));
  await screen.findByText(/暂无 iOS 发布历史/);
  expect(location.search).toBe('?client=ios&tab=history');
  fireEvent.click(screen.getByRole('button',{name:'当前策略'}));
  expect(location.search).toBe('?client=ios');expect(screen.getByRole('button',{name:'配置 iOS 策略'})).toBeInTheDocument();
  history.replaceState({},'','/platform/versions?client=android&tab=history');fireEvent(window,new PopStateEvent('popstate'));
  await screen.findByText(/暂无 Android 发布历史/);
  expect(request).toHaveBeenCalledWith('/versions/android/history?page=1');
});
it('common dialog preserves typing focus across parent renders and uses the latest close callback',()=>{
  const before=vi.fn(),after=vi.fn();
  const view=render(<PlatformDialog title="版本" onClose={before}><input aria-label="最新版本"/></PlatformDialog>);
  screen.getByLabelText('最新版本').focus();
  view.rerender(<PlatformDialog title="版本" onClose={after}><input aria-label="最新版本"/></PlatformDialog>);
  expect(screen.getByLabelText('最新版本')).toHaveFocus();
  fireEvent.keyDown(document,{key:'Escape'});expect(after).toHaveBeenCalledOnce();expect(before).not.toHaveBeenCalled();
});
