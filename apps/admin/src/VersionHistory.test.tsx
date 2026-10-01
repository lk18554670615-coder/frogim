import { cleanup,fireEvent,render,screen,waitFor } from '@testing-library/react';
import { afterEach,expect,it,vi } from 'vitest';
import { VersionHistory,type Publication } from './VersionHistory';

afterEach(cleanup);
const row:Publication={id:'3',platform:'android',version:3,policy:{latestVersion:'1.0.14',minimumVersion:'1.0.10',forceUpdate:false,downloadUrl:'https://example.com/new.apk',releaseNotes:'更新说明\n第二行'},actor:'admin',reason:'发布修复版',source:'publish',recordedAt:'2026-10-01T04:00:00Z'};
it('shows current badge and readonly complete snapshot, and explains imported baseline uncertainty',async()=>{
  const request=vi.fn().mockResolvedValue({items:[row,{...row,id:'2',version:2,source:'baseline',actor:'',reason:'引入时快照'}],hasMore:false});
  render(<VersionHistory client="android" name="Android" currentVersion={3} request={request}/>);
  await screen.findByText('当前');fireEvent.click(screen.getAllByRole('button',{name:'详情'})[0]);
  const dialog=screen.getByRole('dialog',{name:'Android 发布详情'});
  expect(dialog).toHaveTextContent('https://example.com/new.apk');expect(dialog).toHaveTextContent('发布修复版');expect(dialog).toHaveTextContent('第二行');
  expect(screen.queryByRole('button',{name:'保存策略'})).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:'关闭'}));fireEvent.click(screen.getAllByRole('button',{name:'详情'})[1]);
  expect(screen.getByRole('dialog')).toHaveTextContent('原发布时间及操作者未知');
  expect(request).toHaveBeenCalledOnce();
});
it('paginates history, keeps error separate and supports retry',async()=>{
  const request=vi.fn().mockResolvedValueOnce({items:[row],hasMore:true}).mockRejectedValueOnce(new Error('连接中断')).mockResolvedValueOnce({items:[{...row,id:'1',version:1}],hasMore:false});
  render(<VersionHistory client="android" name="Android" currentVersion={3} request={request}/>);
  await screen.findByText('当前');fireEvent.click(screen.getByRole('button',{name:'下一页'}));
  expect(await screen.findByRole('alert')).toHaveTextContent('连接中断');fireEvent.click(screen.getByRole('button',{name:'重试'}));
  await screen.findByText('策略 #1');expect(screen.getByRole('button',{name:'下一页'})).toBeDisabled();
  expect(request).toHaveBeenLastCalledWith('/versions/android/history?page=2');
});
it('ignores a late response after switching clients',async()=>{
  let resolve:(value:unknown)=>void=()=>{};
  const request=vi.fn().mockImplementationOnce(()=>new Promise(done=>{resolve=done;})).mockResolvedValue({items:[],hasMore:false});
  const view=render(<VersionHistory key="android" client="android" name="Android" currentVersion={3} request={request}/>);
  view.rerender(<VersionHistory key="ios" client="ios" name="iOS" currentVersion={0} request={request}/>);
  await screen.findByText(/暂无 iOS 发布历史/);resolve({items:[row],hasMore:false});
  await waitFor(()=>expect(screen.queryByText('1.0.14')).not.toBeInTheDocument());
});
