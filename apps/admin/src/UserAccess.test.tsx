import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { IPAccountsPanel, IPValue, UserAccessLogPanel, UserAccessSummary, regionLabel } from './UserAccess';
import type { AdminApi, UserAccessLogPage, UserRecord } from './types';

const empty: UserAccessLogPage = {items:[],nextCursor:'',from:'',to:'',retentionDays:180,geoVersion:'pinned-test'};
const user = {id:'user_1',nickname:'测试账号',phone:'13900000001',avatar:'测',avatarUrl:'',access:{registrationSource:'app',registrationIp:'1.1.1.1',lastLoginIp:'2001:db8::1',lastLoginAt:'2026-09-02T02:00:00Z',matchedSources:['registration','history'],registrationRegion:{status:'unavailable'},lastLoginRegion:{status:'ok',country:'中国',province:'广东',city:'深圳',isp:'电信'}}} as UserRecord;
const notify=vi.fn(),onIP=vi.fn();
afterEach(()=>{cleanup();vi.restoreAllMocks();notify.mockClear();onIP.mockClear();});

describe('用户 IP 与认证记录',()=>{
  it('IPv6 可关联与复制，归属地缺失不隐藏 IP',async()=>{
    const copy=vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:copy}});
    render(<IPValue ip="2001:db8::1" notify={notify} onIP={onIP}/>);
    expect(screen.getByText('归属地暂不可用')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button',{name:'2001:db8::1'}));expect(onIP).toHaveBeenCalledWith('2001:db8::1');
    fireEvent.click(screen.getByRole('button',{name:'复制 IP 2001:db8::1'}));
    await waitFor(()=>expect(notify).toHaveBeenCalledWith('IP 已复制'));expect(copy).toHaveBeenCalledWith('2001:db8::1');
    copy.mockRejectedValueOnce(new Error('denied'));fireEvent.click(screen.getByRole('button',{name:'复制 IP 2001:db8::1'}));
    await waitFor(()=>expect(notify).toHaveBeenCalledWith('复制失败，请手动选择 IP 复制'));
  });
  it('后台来源和未知注册 IP 如实显示，不从登录补造',()=>{
    render(<UserAccessSummary access={{...user.access!,registrationSource:'admin',registrationIp:undefined}} full notify={notify} onIP={onIP}/>);
    expect(screen.getByText('后台创建')).toBeInTheDocument();expect(screen.getByText('未记录')).toBeInTheDocument();
    expect(screen.queryByText('1.1.1.1')).not.toBeInTheDocument();expect(screen.getByText('中国 · 广东 · 深圳 · 电信')).toBeInTheDocument();
    expect(regionLabel({status:'private',version:''})).toBe('内网地址');
    expect(regionLabel({status:'loopback',version:''})).toBe('回环地址');
  });
  it('失败身份、未知账号、稳定游标分页和筛选',async()=>{
    const getLogs=vi.fn().mockResolvedValueOnce({...empty,nextCursor:'cursor_2',items:[{id:'1',user,result:'failed',event:'login',method:'password',failureCode:'INVALID_CREDENTIALS',platform:'android',occurredAt:'2026-09-02T02:00:00Z',ip:'1.1.1.1'},{id:'2',result:'failed',event:'login',method:'qr',platform:'web',occurredAt:'2026-09-02T02:00:00Z'}]}).mockResolvedValue(empty);
    const api={getUserAccessLogs:getLogs} as unknown as AdminApi;
    render(<UserAccessLogPanel api={api} notify={notify} onIP={onIP}/>);
    expect(await screen.findByText('测试账号')).toBeInTheDocument();expect(screen.getByText('未识别账号')).toBeInTheDocument();
    expect(screen.getAllByText('身份未验证')).toHaveLength(2);
    fireEvent.click(screen.getByRole('button',{name:'下一页'}));
    await waitFor(()=>expect(getLogs).toHaveBeenLastCalledWith(expect.objectContaining({cursor:'cursor_2',limit:20})));
    expect(await screen.findByText('没有匹配的认证记录')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button',{name:'上一页'}));
    fireEvent.change(screen.getByLabelText('来源 IP'),{target:{value:'2001:db8::1'}});
    fireEvent.change(screen.getByLabelText('结果'),{target:{value:'failed'}});
    fireEvent.change(screen.getByLabelText('认证方式'),{target:{value:'otp'}});
    fireEvent.change(screen.getByLabelText('开始时间'),{target:{value:'2026-08-01T12:00'}});
    fireEvent.click(screen.getByRole('button',{name:'查询记录'}));
    await waitFor(()=>expect(getLogs).toHaveBeenLastCalledWith(expect.objectContaining({cursor:'',ip:'2001:db8::1',result:'failed',method:'otp',from:new Date('2026-08-01T12:00').toISOString()})));
  });
  it('用户日志固定用户，错误可以重试，非法时间不会提交',async()=>{
    const getLogs=vi.fn().mockRejectedValueOnce(new Error('数据库暂不可用')).mockResolvedValue(empty);
    render(<UserAccessLogPanel api={{getUserAccessLogs:getLogs} as unknown as AdminApi} userId="user_1" notify={notify} onIP={onIP}/>);
    expect(await screen.findByRole('alert')).toHaveTextContent('数据库暂不可用');
    fireEvent.click(screen.getByRole('button',{name:'重试'}));
    expect(await screen.findByText('没有匹配的认证记录')).toBeInTheDocument();
    expect(getLogs).toHaveBeenLastCalledWith(expect.objectContaining({userId:'user_1'}));
    fireEvent.change(screen.getByLabelText('开始时间'),{target:{value:'2026-09-02T12:00'}});
    fireEvent.change(screen.getByLabelText('结束时间'),{target:{value:'2026-09-01T12:00'}});
    fireEvent.click(screen.getByRole('button',{name:'查询记录'}));
    expect(notify).toHaveBeenCalledWith('开始时间不能晚于结束时间');expect(getLogs).toHaveBeenCalledTimes(2);
  });
  it('关联账号使用精确 IP 及 any 来源，显示去重总数和匹配来源',async()=>{
    const getUsers=vi.fn().mockResolvedValue({items:[user],total:1,nextCursor:''});
    render(<IPAccountsPanel api={{getUsers} as unknown as AdminApi} ip="1.1.1.1" notify={notify} onIP={onIP}/>);
    expect(await screen.findByText('共 1 个账号')).toBeInTheDocument();
    expect(getUsers).toHaveBeenCalledWith('','',1,20,'','1.1.1.1','any');
    expect(screen.getByText(/同 IP 不代表同一个人/)).toBeInTheDocument();
    expect(screen.getByText('匹配：注册 IP、180 天内成功历史')).toBeInTheDocument();
  });
});
