import { useEffect, useRef, useState } from 'react';
import { ApiError } from './api';
import type { AdminApi, TenantCredentialJob } from './types';

export function TenantPasswordReset({ api, userId, canWrite }: { api: AdminApi; userId: string; canWrite: boolean }) {
  const [data, setData] = useState<{ managed: boolean; items: TenantCredentialJob[] }>();
  const [error, setError] = useState(''), [busy, setBusy] = useState(false);
  const [password, setPassword] = useState(''), [confirmation, setConfirmation] = useState(''), [reason, setReason] = useState('');
  const [confirming, setConfirming] = useState(false), [unknown, setUnknown] = useState(false);
  const request = useRef(''), epoch = useRef(0), working = useRef(false);
  const clear = () => { setPassword(''); setConfirmation(''); setReason(''); setConfirming(false); setUnknown(false); request.current = ''; };
  const refresh = async () => {
    if (working.current) return;
    const generation = epoch.current; working.current = true; setBusy(true); setError('');
    try {
      const value = await api.getUserCredentialJobs(userId);
      if (generation !== epoch.current) return;
      setData(value);
      if (request.current && value.items.some(j => j.requestId === request.current)) clear();
    } catch (cause) { if (generation === epoch.current) setError(cause instanceof Error ? cause.message : '密码任务查询失败'); }
    finally { if (generation === epoch.current) { working.current = false; setBusy(false); } }
  };
  useEffect(() => { epoch.current++; working.current = false; setData(undefined); clear(); void refresh(); return () => { epoch.current++; }; }, [api, userId]);
  const pending = data?.items.some(j => j.status === 'pending') === true;
  const valid = [...password].length >= 8 && new TextEncoder().encode(password).length <= 72 && password === confirmation && reason.trim().length > 0;
  const submit = async () => {
    if (working.current || !valid || !canWrite || !data?.managed || pending) return;
    request.current ||= crypto.randomUUID();
    const generation = epoch.current; working.current = true; setBusy(true); setError('');
    try {
      const job = await api.resetTenantUserPassword(userId, request.current, password, reason.trim());
      if (generation !== epoch.current) return;
      setData(previous => ({ managed: true, items: [job, ...(previous?.items ?? []).filter(j => j.jobId !== job.jobId)] }));
      clear();
    } catch (cause) {
      if (generation !== epoch.current) return;
      const definite = cause instanceof ApiError && cause.status >= 400 && cause.status < 500;
      setUnknown(!definite); setError(cause instanceof Error ? cause.message : '结果未确认，请刷新任务或使用原请求重试');
      if (definite) { request.current = ''; setConfirming(false); }
    } finally { if (generation === epoch.current) { working.current = false; setBusy(false); } }
  };
  if (data?.managed === false) return null;
  return <section className="detail-section" aria-label="平台登录密码">
    <h3>平台登录密码</h3>
    <p>重置普通用户的统一登录密码，保留好友、群组和消息。受理后暂停新登录；企业确认旧 API／IM 登录凭据失效后新密码才生效。</p>
    <button className="button secondary compact" type="button" disabled={busy} onClick={() => void refresh()}>刷新密码任务</button>
    {error && <p role="alert" className="danger-text">{error}</p>}
    {data?.items.map(job => <p key={job.jobId}><strong>{job.status === 'completed' ? '密码重置已完成' : '密码重置处理中（尚未完成）'}</strong><small className="mono">请求号：{job.requestId}</small>{job.errorCode && <small>企业撤权尚未确认，服务端会继续原任务；不要重复新建。</small>}</p>)}
    {data?.managed && !canWrite && <p>仅具有用户管理写权限的管理员可以重置。</p>}
    {data?.managed && canWrite && <fieldset disabled={busy || pending} className="settings-section">
      <legend>重置用户密码</legend>
      <label className="field-label">用户新密码<input type="password" autoComplete="new-password" value={password} readOnly={unknown} onChange={e => { setPassword(e.target.value); setConfirming(false); }} /></label>
      <small>至少 8 个字符，以企业策略为准；UTF-8 编码最多 72 字节。</small>
      <label className="field-label">确认用户新密码<input type="password" autoComplete="new-password" value={confirmation} readOnly={unknown} onChange={e => { setConfirmation(e.target.value); setConfirming(false); }} /></label>
      <label className="field-label">重置理由<textarea maxLength={500} value={reason} readOnly={unknown} onChange={e => { setReason(e.target.value); setConfirming(false); }} /></label>
      {unknown && <p role="status">上次请求结果未确认，内容已锁定。请先刷新任务，或使用相同请求重试。</p>}
      {confirming ? <div role="group" aria-label="确认重置用户密码"><p>确认重置此用户的统一登录密码？各设备需要重新登录，此操作不能撤销。</p><button className="button primary" type="button" disabled={!valid} onClick={() => void submit()}>{unknown ? '使用原请求重试' : '确认重置用户密码'}</button>{!unknown && <button className="button secondary" type="button" onClick={() => setConfirming(false)}>取消</button>}</div> : <button type="button" className="button secondary" disabled={!valid} onClick={() => setConfirming(true)}>重置用户密码</button>}
    </fieldset>}
  </section>;
}
