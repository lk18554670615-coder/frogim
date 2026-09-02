import { useEffect, useState, type DependencyList, type ReactNode } from 'react';
import { Copy, RefreshCcw } from 'lucide-react';
import type { AdminApi, IPRegion, UserAccessFilters, UserAccessProfile, UserRecord } from './types';
import './user-access.css';

type Notify = (message: string) => void;
export const ipSourceLabels: Record<string,string> = {registration:'注册 IP',last_login:'最近登录 IP',history:'180 天内成功历史'};
const methodLabels: Record<string,string> = {otp:'验证码',password:'密码',qr:'扫码',admin:'后台开户'};
const failureLabels: Record<string,string> = {INVALID_CODE:'验证码不正确或已过期',INVALID_CREDENTIALS:'凭据验证失败',FORBIDDEN:'账号或操作不可用',ACCOUNT_EXISTS:'账号已存在',RATE_LIMITED:'认证尝试过于频繁',SMS_NOT_CONFIGURED:'验证码服务未配置',SMS_UNAVAILABLE:'验证码服务暂不可用',IM_UNAVAILABLE:'IM 服务暂不可用',QR_LOGIN_NOT_FOUND:'扫码凭据无效',QR_LOGIN_EXPIRED:'扫码凭据过期',QR_LOGIN_USED:'扫码凭据已使用',QR_LOGIN_ACCOUNT_UNAVAILABLE:'扫码账号不可用',INVALID_ARGUMENT:'认证参数不符合要求',AUTH_UNAVAILABLE:'认证服务暂不可用'};
function timestamp(value?: string) { if(!value)return '未记录';const d=new Date(value);return Number.isNaN(d.getTime())?'未记录':new Intl.DateTimeFormat('zh-CN',{dateStyle:'medium',timeStyle:'short'}).format(d); }
export function regionLabel(r?: IPRegion) {
  if(!r)return '归属地暂不可用';
  if(r.status==='ok')return [r.country,r.province,r.city,r.isp].filter(Boolean).join(' · ') || '归属地未知';
  return ({private:'内网地址',loopback:'回环地址',reserved:'保留地址',unknown:'未记录',not_found:'未查到归属地'} as Record<string,string>)[r.status] ?? '归属地暂不可用';
}
export function IPValue({ip,region,onIP,notify}:{ip?:string;region?:IPRegion;onIP:(ip:string)=>void;notify:Notify}) {
  if(!ip)return <span className="muted">未记录</span>;
  const copy=async()=>{try{await navigator.clipboard.writeText(ip);notify('IP 已复制');}catch{notify('复制失败，请手动选择 IP 复制');}};
  return <span className="ip-value"><span className="ip-address-actions"><button type="button" className="ip-address mono" title="查看同 IP 账号" onClick={()=>onIP(ip)}>{ip}</button><button type="button" className="ip-copy" aria-label={`复制 IP ${ip}`} onClick={()=>void copy()}><Copy size={13}/></button></span><small>{regionLabel(region)}</small></span>;
}
export function UserAccessSummary({access,onIP,notify,full=false}:{access?:UserAccessProfile;onIP:(ip:string)=>void;notify:Notify;full?:boolean}) {
  return <div className="ip-summary">{full&&<div><span>注册来源：</span><strong>{access?.registrationSource==='admin'?'后台创建':access?.registrationSource==='app'?'App 注册':'未记录'}</strong></div>}<div><span>注册 IP</span><IPValue ip={access?.registrationIp} region={access?.registrationRegion} onIP={onIP} notify={notify}/></div><div><span>最近登录</span><IPValue ip={access?.lastLoginIp} region={access?.lastLoginRegion} onIP={onIP} notify={notify}/><small>{timestamp(access?.lastLoginAt)}</small></div>{!!access?.matchedSources.length&&<small>匹配：{access.matchedSources.map(s=>ipSourceLabels[s]??s).join('、')}</small>}</div>;
}
function useRemote<T>(load:()=>Promise<T>,deps:DependencyList) {
  const [data,setData]=useState<T>();const [error,setError]=useState('');const [loading,setLoading]=useState(true);const [revision,setRevision]=useState(0);
  useEffect(()=>{let active=true;setLoading(true);setError('');setData(undefined);load().then(v=>{if(active)setData(v);},e=>{if(active)setError(e instanceof Error?e.message:'加载失败');}).finally(()=>{if(active)setLoading(false);});return()=>{active=false;};},[...deps,revision]);
  return {data,error,loading,reload:()=>setRevision(v=>v+1)};
}
function RemoteState({loading,error,reload,children}:{loading:boolean;error:string;reload:()=>void;children:ReactNode}) {
  if(loading)return <p role="status">正在加载…</p>;if(error)return <div role="alert"><p>{error}</p><button className="button secondary" onClick={reload}>重试</button></div>;return <>{children}</>;
}
function Identity({user}:{user?:UserRecord}) {return user?<div className="identity"><span className="avatar">{user.avatarUrl?<img src={user.avatarUrl} alt=""/>:user.avatar}</span><span><strong>{user.nickname}</strong><small className="mono">{user.id}</small><small>{user.phone}</small></span></div>:<span>未识别账号</span>;}
export function UserAccessLogPanel({api,userId,notify,onIP}:{api:AdminApi;userId?:string;notify:Notify;onIP:(ip:string)=>void}) {
  const [draft,setDraft]=useState({ip:'',result:'',method:'',event:'',from:'',to:''});const [filters,setFilters]=useState<UserAccessFilters>({});const [cursors,setCursors]=useState(['']);
  const cursor=cursors[cursors.length-1];const state=useRemote(()=>api.getUserAccessLogs({...filters,userId,cursor,limit:20}),[api,userId,filters,cursor]);
  const apply=()=>{if(draft.from&&draft.to&&new Date(draft.from)>new Date(draft.to)){notify('开始时间不能晚于结束时间');return;}setCursors(['']);setFilters({...draft,from:draft.from?new Date(draft.from).toISOString():undefined,to:draft.to?new Date(draft.to).toISOString():undefined});};
  return <section className="access-log-panel"><p className="muted">详细记录保留 180 天，默认查看最近 30 天。失败尝试不代表本人操作；归属地按当前离线库解析，仅供参考。</p><form className="access-filters" onSubmit={e=>{e.preventDefault();apply();}}>
    <label>来源 IP<input value={draft.ip} placeholder="完整 IPv4 / IPv6" onChange={e=>setDraft({...draft,ip:e.target.value})}/></label>
    <label>结果<select value={draft.result} onChange={e=>setDraft({...draft,result:e.target.value})}><option value="">全部结果</option><option value="success">成功</option><option value="failed">失败</option></select></label>
    <label>事件<select value={draft.event} onChange={e=>setDraft({...draft,event:e.target.value})}><option value="">全部事件</option><option value="register">注册</option><option value="login">登录</option></select></label>
    <label>认证方式<select value={draft.method} onChange={e=>setDraft({...draft,method:e.target.value})}><option value="">全部方式</option>{Object.entries(methodLabels).map(([v,l])=><option key={v} value={v}>{l}</option>)}</select></label>
    <label>开始时间<input type="datetime-local" value={draft.from} onChange={e=>setDraft({...draft,from:e.target.value})}/></label><label>结束时间<input type="datetime-local" value={draft.to} onChange={e=>setDraft({...draft,to:e.target.value})}/></label>
    <button className="button primary" type="submit">查询记录</button><button className="button secondary" type="button" onClick={state.reload}><RefreshCcw size={14}/>刷新</button></form>
    <RemoteState {...state}><>{state.data?.items.length?<div className="table-wrap"><table><thead><tr>{!userId&&<th>账号</th>}<th>时间 / 事件</th><th>方式 / 平台</th><th>来源 IP / 归属地</th><th>结果</th></tr></thead><tbody>{state.data.items.map(e=><tr key={e.id}>{!userId&&<td><Identity user={e.user}/></td>}<td>{timestamp(e.occurredAt)}<small>{e.event==='register'?'注册':'登录'}</small></td><td>{methodLabels[e.method]??e.method}<small>{e.platform==='unknown'?'平台未提供':e.platform}</small></td><td><IPValue ip={e.ip} region={e.region} onIP={onIP} notify={notify}/></td><td><strong className={e.result==='success'?'success-text':'danger-text'}>{e.result==='success'?'成功':'失败'}</strong>{e.result==='failed'&&<><small>{failureLabels[e.failureCode??'']??'认证未完成'}</small><small>身份未验证</small></>}</td></tr>)}</tbody></table></div>:<p className="access-empty">没有匹配的认证记录</p>}
    <div className="access-pagination"><button className="button secondary compact" disabled={cursors.length===1} onClick={()=>setCursors(v=>v.slice(0,-1))}>上一页</button><span>第 {cursors.length} 页</span><button className="button secondary compact" disabled={!state.data?.nextCursor} onClick={()=>setCursors(v=>[...v,state.data!.nextCursor])}>下一页</button></div><small className="muted">归属地库：{state.data?.geoVersion||'暂无'}</small></></RemoteState></section>;
}
export function IPAccountsPanel({api,ip,notify,onIP}:{api:AdminApi;ip:string;notify:Notify;onIP:(ip:string)=>void}) {
  const [cursors,setCursors]=useState(['']);const cursor=cursors[cursors.length-1];
  const state=useRemote(()=>api.getUsers('','',cursors.length,20,cursor,ip,'any'),[api,ip,cursor]);
  return <section><p className="ip-association-note">同 IP 不代表同一个人。仅根据注册 IP、最近成功登录 IP 和 180 天内成功历史关联；失败尝试不参与。</p><RemoteState {...state}>{state.data?.items.length?<><p>共 {state.data.total} 个账号</p><div className="table-wrap"><table><thead><tr><th>用户</th><th>IP 信息 / 匹配来源</th></tr></thead><tbody>{state.data.items.map(u=><tr key={u.id}><td><Identity user={u}/></td><td><UserAccessSummary access={u.access} onIP={onIP} notify={notify}/></td></tr>)}</tbody></table></div><div className="access-pagination"><button className="button secondary compact" disabled={cursors.length===1} onClick={()=>setCursors(v=>v.slice(0,-1))}>上一页</button><span>第 {cursors.length} 页</span><button className="button secondary compact" disabled={!state.data.nextCursor} onClick={()=>setCursors(v=>[...v,state.data!.nextCursor!])}>下一页</button></div></>:<p>没有匹配的账号</p>}</RemoteState></section>;
}
