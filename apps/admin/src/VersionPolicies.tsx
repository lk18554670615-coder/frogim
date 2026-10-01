import { useEffect, useRef, useState } from 'react';
import { PlatformDialog } from './PlatformDialog';
import { VersionHistory } from './VersionHistory';

type Client='android'|'ios'|'web';
type Policy=Record<string,unknown>;
type VersionRow={platform:string;version:number;policy:Policy};
type Draft={latestVersion:string;minimumVersion:string;forceUpdate:boolean;downloadUrl:string;releaseNotes:string};
type Props={items:VersionRow[];loading:boolean;canWrite:boolean;request:(path:string,body?:unknown,method?:string)=>Promise<any>;onSaved:(message:string)=>Promise<void>};
const clients:Client[]=['android','ios','web'];
const names:Record<Client,string>={android:'Android',ios:'iOS',web:'Web'};
function selectedClient():Client {
  const value=new URLSearchParams(location.search).get('client');
  return clients.includes(value as Client)?value as Client:'android';
}
function selectedSection():'current'|'history' {
  return new URLSearchParams(location.search).get('tab')==='history'?'history':'current';
}
function toDraft(policy:Policy={}):Draft {
  const text=(key:string)=>typeof policy[key]==='string'?policy[key] as string:'';
  return {latestVersion:text('latestVersion'),minimumVersion:text('minimumVersion'),forceUpdate:policy.forceUpdate===true,downloadUrl:text('downloadUrl'),releaseNotes:text('releaseNotes')};
}
function parts(value:string):number[]|null {
  if(!/^\d+(?:\.\d+){0,3}$/.test(value))return null;
  const numbers=value.split('.').map(Number);
  return numbers.every(n=>Number.isInteger(n)&&n<=2147483647)?numbers:null;
}
function compare(a:number[],b:number[]):number {
  for(let i=0;i<4;i++){const difference=(a[i]??0)-(b[i]??0);if(difference)return difference;}
  return 0;
}
function validate(draft:Draft):Partial<Record<keyof Draft,string>> {
  const errors:Partial<Record<keyof Draft,string>>={};
  const latest=parts(draft.latestVersion.trim()),minimum=parts(draft.minimumVersion.trim());
  if(!latest)errors.latestVersion='填写 1–4 段数字版本号，例如 1.0.12。';
  if(!minimum)errors.minimumVersion='填写最低可用版本，例如 1.0.10。';
  else if(latest&&compare(minimum,latest)>0)errors.minimumVersion='最低可用版本不能高于最新版本。';
  if(draft.downloadUrl.trim()){
    try {
      const url=new URL(draft.downloadUrl.trim());
      if(!['http:','https:'].includes(url.protocol)||url.username||url.password||url.search||url.hash)throw new Error();
    }catch{errors.downloadUrl='填写完整的 HTTP(S) 地址，不包含账号密码、查询参数或片段。';}
  }
  return errors;
}
function errorMessage(error:unknown):string {
  const message=error instanceof Error?error.message:String(error);
  if(message==='CONFLICT')return '这份策略已被其他管理员更新。请重新载入最新策略后再编辑，重新载入会替换当前未保存内容。';
  if(message==='INVALID_VERSION_POLICY')return '版本策略未通过校验，请检查版本范围及下载地址；正式环境须使用 HTTPS 地址。';
  return message;
}
function UpdateImpact({policy}:{policy:Draft}) {
  return <ul className="lp-version-impact">
    <li>低于 <strong>{policy.minimumVersion}</strong>：必须升级后才能继续使用。</li>
    {compare(parts(policy.minimumVersion)??[],parts(policy.latestVersion)??[])<0&&<li><strong>{policy.minimumVersion}</strong> 至低于 <strong>{policy.latestVersion}</strong>：{policy.forceUpdate?'必须更新至最新版本。':'提示更新，允许暂时跳过。'}</li>}
    <li><strong>{policy.latestVersion}</strong> 及以上：不要求更新。</li>
  </ul>;
}

export function VersionPolicies({items,loading,canWrite,request,onSaved}:Props) {
  const [client,setClient]=useState<Client>(selectedClient);
  const [section,setSection]=useState<'current'|'history'>(selectedSection);
  const [draft,setDraft]=useState<Draft|null>(null);
  const [original,setOriginal]=useState<VersionRow|null>(null);
  const [step,setStep]=useState<'edit'|'review'>('edit');
  const [errors,setErrors]=useState<Partial<Record<keyof Draft,string>>>({});
  const [invalidSubmit,setInvalidSubmit]=useState(0);
  const [reason,setReason]=useState(''),[confirmed,setConfirmed]=useState(false);
  const [busy,setBusy]=useState(false),[error,setError]=useState(''),[conflict,setConflict]=useState(false);
  const content=useRef<HTMLDivElement>(null);
  const current=items.find(row=>row.platform===client);
  const policy=toDraft(current?.policy);
  useEffect(()=>{const pop=()=>{setClient(selectedClient());setSection(selectedSection());setDraft(null);};addEventListener('popstate',pop);return()=>removeEventListener('popstate',pop);},[]);
  useEffect(()=>{if(draft){content.current?.parentElement?.scrollTo?.({top:0});content.current?.focus({preventScroll:true});}},[step,!!draft]);
  useEffect(()=>{if(invalidSubmit)content.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();},[invalidSubmit]);
  const choose=(value:Client)=>{
    const url=new URL(location.href);url.searchParams.set('client',value);
    history.pushState({},'',url.pathname+url.search);setClient(value);
  };
  const chooseSection=(value:'current'|'history')=>{
    const url=new URL(location.href);
    if(value==='history')url.searchParams.set('tab','history');else url.searchParams.delete('tab');
    history.pushState({},'',url.pathname+url.search);setSection(value);
  };
  const open=()=>{setOriginal(current??null);setDraft(toDraft(current?.policy));setErrors({});setError('');setConflict(false);setReason('');setConfirmed(false);setStep('edit');};
  const change=(key:keyof Draft,value:string|boolean)=>{setDraft(draft?{...draft,[key]:value}:null);setErrors({...errors,[key]:undefined});setError('');};
  const submit=async()=>{
    if(!draft||!canWrite||!confirmed||!reason.trim()||busy||conflict)return;
    setBusy(true);setError('');
    try{
      await request('/versions/'+client,{version:original?.version??0,reason:reason.trim(),confirmed:true,policy:{...original?.policy,...draft,latestVersion:draft.latestVersion.trim(),minimumVersion:draft.minimumVersion.trim(),downloadUrl:draft.downloadUrl.trim()}},'PUT');
      setDraft(null);await onSaved(names[client]+' 版本策略已保存');
    }catch(e){setError(errorMessage(e));setConflict(e instanceof Error&&e.message==='CONFLICT');}
    finally{setBusy(false);}
  };
  const reload=async()=>{
    setBusy(true);
    try{const rows:VersionRow[]=await request('/versions');const fresh=rows.find(row=>row.platform===client);setOriginal(fresh??null);setDraft(toDraft(fresh?.policy));setStep('edit');setErrors({});setReason('');setConfirmed(false);setError('');setConflict(false);}
    catch(e){setError(errorMessage(e));}finally{setBusy(false);}
  };
  return <section className="lp-panel lp-versions" aria-label="客户端版本策略">
    <div className="lp-version-heading"><div><h2>客户端更新策略</h2><p>按客户端分别设置，适用于全部企业；每个客户端只有一份当前策略。</p></div></div>
    <div className="lp-version-tabs" role="group" aria-label="客户端类型">{clients.map(value=><button type="button" key={value} aria-pressed={client===value} className={client===value?'active':''} onClick={()=>choose(value)}>{names[value]}{' '}<span>{items.some(row=>row.platform===value)?'已配置':'未配置'}</span></button>)}</div>
    <div className="lp-version-sections" role="group" aria-label="版本管理内容">{([['current','当前策略'],['history','发布历史']] as const).map(([value,label])=><button type="button" key={value} aria-pressed={section===value} className={section===value?'active':''} onClick={()=>chooseSection(value)}>{label}</button>)}</div>
    {loading?<div className="lp-empty" role="status">正在加载版本策略…</div>:<div className="lp-version-content">
      {section==='history'?<VersionHistory key={client+':'+(current?.version??0)} client={client} name={names[client]} currentVersion={current?.version??0} request={request}/>:<>
      <div className="lp-version-title"><h3>{names[client]} 更新策略</h3><button className="lp-primary" disabled={!canWrite} onClick={open}>{current?'编辑':'配置'} {names[client]} 策略</button></div>
      {!canWrite&&<p className="lp-version-hint">当前为只读权限，可以查看策略，不能修改。</p>}
      {!current?<div className="lp-version-empty"><h3>尚未配置 {names[client]} 更新策略</h3><p>当前不会由平台要求此客户端升级。配置后可统一提示新版本并限制过旧版本。</p></div>:<>
        <dl className="lp-version-summary"><div><dt>最新版本</dt><dd>{policy.latestVersion||'—'}</dd></div><div><dt>最低可用版本</dt><dd>{policy.minimumVersion||'—'}</dd></div><div><dt>更新方式</dt><dd>{policy.forceUpdate?'所有旧版本强制更新':'推荐更新'}</dd></div></dl>
        <div className="lp-version-section"><h4>用户受到的影响</h4><UpdateImpact policy={policy}/></div>
        <div className="lp-version-section"><h4>下载／更新地址</h4>{policy.downloadUrl?<a href={policy.downloadUrl} target="_blank" rel="noreferrer">{policy.downloadUrl}</a>:<p className="lp-version-warning">尚未填写下载地址，低版本用户可能无法从升级提示进入更新渠道。</p>}</div>
        <div className="lp-version-section"><h4>更新说明</h4><p className="lp-version-notes">{policy.releaseNotes||'未填写更新说明'}</p></div>
      </>}
      </>}
    </div>}
    {draft&&<PlatformDialog title={(step==='edit'?'编辑':'确认')+' '+names[client]+' 版本策略'} onClose={()=>{if(!busy)setDraft(null);}} footer={step==='edit'?<div className="lp-actions"><button type="button" onClick={()=>setDraft(null)}>取消</button><button className="lp-primary" type="submit" form="lp-version-policy">下一步：确认影响</button></div>:<div className="lp-actions"><button type="button" disabled={busy} onClick={()=>{setStep('edit');setConfirmed(false);}}>返回编辑</button>{conflict?<button type="button" disabled={busy} className="lp-primary" onClick={()=>void reload()}>重新载入最新策略</button>:<button className="lp-primary" type="submit" form="lp-version-policy" disabled={busy||!canWrite||!confirmed||!reason.trim()}>{busy?'保存中…':'保存策略'}</button>}</div>}>
      <div ref={content} tabIndex={-1} className="lp-version-form">
        <p className="lp-impact">仅修改 {names[client]} 客户端的更新策略，影响全部企业使用此客户端的用户。</p>
        {step==='edit'?<form id="lp-version-policy" noValidate onSubmit={e=>{e.preventDefault();const result=validate(draft);setErrors(result);if(Object.keys(result).length){setInvalidSubmit(value=>value+1);return;}setDraft({...draft,latestVersion:draft.latestVersion.trim(),minimumVersion:draft.minimumVersion.trim(),downloadUrl:draft.downloadUrl.trim()});setStep('review');}}>
          <div className="lp-version-fields">{(['latestVersion','minimumVersion'] as const).map(key=><label key={key}>{key==='latestVersion'?'最新版本':'最低可用版本'}<input aria-label={key==='latestVersion'?'最新版本':'最低可用版本'} value={draft[key]} placeholder={key==='latestVersion'?'例如 1.0.12':'例如 1.0.10'} aria-invalid={!!errors[key]} aria-describedby={key+'-hint'} onChange={e=>change(key,e.target.value)} required/><small id={key+'-hint'} className={errors[key]?'lp-field-error':''}>{errors[key]||(key==='latestVersion'?'支持 1–4 段数字版本号。':'低于此版本必须升级；不能高于最新版本。')}</small></label>)}</div>
          <fieldset className="lp-version-choice"><legend>最低可用版本及以上的旧客户端</legend><label><input type="radio" name="updateMode" checked={!draft.forceUpdate} onChange={()=>change('forceUpdate',false)}/><span>推荐更新<small>提示新版本，允许用户暂时跳过。</small></span></label><label><input type="radio" name="updateMode" checked={draft.forceUpdate} onChange={()=>change('forceUpdate',true)}/><span>强制更新至最新版本<small>所有低于最新版本的客户端必须升级。</small></span></label></fieldset>
          <label>下载／更新地址<input aria-label="下载／更新地址" value={draft.downloadUrl} placeholder="https://…/app.apk 或升级页面" aria-invalid={!!errors.downloadUrl} aria-describedby="downloadUrl-hint" onChange={e=>change('downloadUrl',e.target.value)}/><small id="downloadUrl-hint" className={errors.downloadUrl?'lp-field-error':''}>{errors.downloadUrl||'Android 可填 APK 地址；iOS 可填商店／安装页；Web 可填更新入口。正式环境使用 HTTPS。'}</small></label>
          <label>更新说明<textarea rows={3} value={draft.releaseNotes} placeholder="告诉用户这次更新的主要变化" onChange={e=>change('releaseNotes',e.target.value)}/></label>

        </form>:<form id="lp-version-policy" onSubmit={e=>{e.preventDefault();void submit();}}>
          <dl className="lp-version-review"><div><dt>最新版本</dt><dd>{draft.latestVersion}</dd></div><div><dt>最低可用版本</dt><dd>{draft.minimumVersion}</dd></div><div><dt>下载／更新地址</dt><dd>{draft.downloadUrl||'未填写'}</dd></div><div><dt>更新说明</dt><dd>{draft.releaseNotes||'未填写'}</dd></div></dl>
          <UpdateImpact policy={draft}/>
          {!draft.downloadUrl&&<p className="lp-version-warning">未填写更新入口，请确认低版本用户另有可用的升级渠道。</p>}
          <label>操作理由<textarea required maxLength={1000} rows={2} value={reason} onChange={e=>setReason(e.target.value)} placeholder="例如：发布新版本，停止支持存在问题的旧版本" disabled={busy}/></label>
          <label className="lp-check"><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)} disabled={busy}/>已确认 {names[client]} 策略及对全部企业用户的影响</label>
          {error&&<p className="lp-error" role="alert">{error}</p>}

        </form>}
      </div>
    </PlatformDialog>}
  </section>;
}
