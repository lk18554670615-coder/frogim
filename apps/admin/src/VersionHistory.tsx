import { useEffect, useState } from 'react';
import { PlatformDialog } from './PlatformDialog';

export type Publication={id:string;platform:string;version:number;policy:Record<string,unknown>;actor:string;reason:string;source:string;recordedAt:string};
type Props={client:string;name:string;currentVersion:number;request:(path:string)=>Promise<any>};
const text=(item:Publication,key:string)=>typeof item.policy[key]==='string'?item.policy[key] as string:'—';
const time=(value:string)=>new Date(value).toLocaleString();

export function VersionHistory({client,name,currentVersion,request}:Props){
  const [page,setPage]=useState(1),[reload,setReload]=useState(0);
  const [items,setItems]=useState<Publication[]>([]),[hasMore,setHasMore]=useState(false);
  const [loading,setLoading]=useState(true),[error,setError]=useState('');
  const [detail,setDetail]=useState<Publication|null>(null);
  useEffect(()=>{
    let active=true;setLoading(true);setError('');
    request('/versions/'+client+'/history?page='+page).then(result=>{
      if(active){setItems(result.items);setHasMore(result.hasMore);}
    }).catch(e=>{if(active)setError(e instanceof Error?e.message:String(e));})
      .finally(()=>{if(active)setLoading(false);});
    return()=>{active=false;};
  },[client,page,reload,request]);
  return <section className="lp-version-history" aria-label={name+' 发布历史'}>
    <div className="lp-version-title"><div><h3>{name} 发布历史</h3><p className="lp-version-hint">每次保存策略都会保留完整快照。记录的是更新策略发布，不代表安装包已上传或验收。</p></div><button onClick={()=>setReload(value=>value+1)} disabled={loading}>刷新历史</button></div>
    {loading?<div className="lp-empty" role="status">正在加载发布历史…</div>:error?<div className="lp-error" role="alert">发布历史加载失败：{error}<button onClick={()=>setReload(value=>value+1)}>重试</button></div>:items.length===0?<div className="lp-version-empty">暂无 {name} 发布历史。保存第一份策略后会显示在这里。</div>:<>
      <div className="lp-table-scroll"><table><thead><tr>{['发布版本','最低可用版本','更新方式','操作者 / 记录时间','操作'].map(label=><th key={label}>{label}</th>)}</tr></thead><tbody>{items.map(item=><tr key={item.id}>
        <td><strong>{text(item,'latestVersion')}</strong>{item.version===currentVersion&&<span className="lp-badge">当前</span>}<small>策略 #{item.version}{item.source==='baseline'?' · 初始快照':''}</small></td>
        <td>{text(item,'minimumVersion')}</td><td>{item.policy.forceUpdate===true?'强制更新':'推荐更新'}</td>
        <td>{item.actor||'原操作者未知'}<small>{time(item.recordedAt)}</small></td><td><button onClick={()=>setDetail(item)}>详情</button></td>
      </tr>)}</tbody></table></div>
      <div className="lp-pagination"><button disabled={page===1} onClick={()=>setPage(page-1)}>上一页</button><span>第 {page} 页</span><button disabled={!hasMore} onClick={()=>setPage(page+1)}>下一页</button></div>
    </>}
    {detail&&<PlatformDialog title={name+' 发布详情'} drawer onClose={()=>setDetail(null)}>
      <p className="lp-impact">这是保存时的只读策略快照，查看不会改变当前更新策略。</p>
      {detail.source==='baseline'&&<p className="lp-version-warning">这条记录来自引入发布历史时的当前策略快照。记录时间是快照保存时间，原发布时间及操作者未知；此前被覆盖的策略无法还原。</p>}
      <dl className="lp-version-review">{Object.entries({客户端:name,策略编号:'#'+detail.version,最新版本:text(detail,'latestVersion'),最低可用版本:text(detail,'minimumVersion'),更新方式:detail.policy.forceUpdate===true?'所有低于最新版本的客户端必须更新':'低于最低可用版本必须更新，其他旧版本可跳过',下载或更新地址:text(detail,'downloadUrl')||'未填写',更新说明:text(detail,'releaseNotes')||'未填写',操作者:detail.actor||'未知',操作理由:detail.reason,记录时间:time(detail.recordedAt)}).map(([label,value])=><div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
    </PlatformDialog>}
  </section>;
}
