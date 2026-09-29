import { useEffect, useRef, useState, type FormEvent } from 'react';
import { PlatformClient, PlatformError, type Page } from './api';

const platforms = ['android', 'ios', 'web', 'macos'] as const;
type Platform = typeof platforms[number];
const labels: Record<Platform, string> = { android: 'Android', ios: 'iOS', web: 'Web', macos: 'macOS' };
export type Release = { platform: Platform; enabled: boolean; revision: number; minimumVersion: string; latestVersion: string; forceUpdate: boolean; rolloutPercentage: number; releaseNotes: string; downloadUrl: string; updatedBy: string; updatedAt: string };
type History = Release & { id: string; reason: string; requestId: string };
type Draft = Pick<Release, 'enabled' | 'minimumVersion' | 'latestVersion' | 'forceUpdate' | 'rolloutPercentage' | 'releaseNotes' | 'downloadUrl'>;
const draftOf = (r: Release): Draft => ({ enabled: r.enabled, minimumVersion: r.minimumVersion, latestVersion: r.latestVersion, forceUpdate: r.forceUpdate, rolloutPercentage: r.rolloutPercentage, releaseNotes: r.releaseNotes, downloadUrl: r.downloadUrl });
const message = (e: unknown) => e instanceof Error ? e.message : '平台服务暂不可用';
const date = (v: string) => v ? new Date(v).toLocaleString() : '尚未发布';
function validRelease(value: unknown): value is Release {
  if (!value || typeof value !== 'object') return false;
  const r = value as Release;
  return platforms.includes(r.platform) && Number.isSafeInteger(r.revision) && r.revision >= 0 && typeof r.enabled === 'boolean' && typeof r.forceUpdate === 'boolean'
    && Number.isInteger(r.rolloutPercentage) && r.rolloutPercentage >= 0 && r.rolloutPercentage <= 100
    && ['minimumVersion', 'latestVersion', 'releaseNotes', 'downloadUrl', 'updatedBy', 'updatedAt'].every(k => typeof (r as unknown as Record<string, unknown>)[k] === 'string');
}

export function PlatformClientVersions({ client, writable, refresh }: { client: PlatformClient; writable: boolean; refresh: number }) {
  const [items, setItems] = useState<Release[] | null>(null), [error, setError] = useState(''), [reload, setReload] = useState(0);
  const [editing, setEditing] = useState<Release | null>(null), [notice, setNotice] = useState('');
  const [history, setHistory] = useState<Platform | null>(null);
  useEffect(() => {
    const abort = new AbortController(); let current = true;
    setItems(null); setError('');
    client.request<Page<Release>>('/client-versions', 'GET', undefined, abort.signal).then(data => {
      if (!Array.isArray(data.items) || data.items.length !== 4 || data.items.some(r => !validRelease(r)) || new Set(data.items.map(r => r.platform)).size !== 4) throw new Error('版本策略响应不完整，请重新加载');
      if (current) setItems(data.items);
    }).catch(e => { if (current) setError(message(e)); });
    return () => { current = false; abort.abort(); };
  }, [client, refresh, reload]);
  return <section aria-label="平台客户端版本">
    <p>统一控制所有企业使用的客户端。停用策略仅停止提示，不卸载已安装版本；低于最低版本时，强制更新不受灰度比例限制。</p>
    <p className="muted">这里登记已验收的 HTTPS 分发地址，不上传、签名或自动打包安装包。请勿填写临时签名链接或含查询参数的地址。</p>
    {notice && <p role="status">{notice}</p>}
    {error ? <p role="alert" className="error">{error}<button onClick={() => setReload(v => v + 1)}>重新加载策略</button></p> : !items ? <p role="status">正在加载版本策略…</p> : <div className="release-grid">{platforms.map(platform => {
      const r = items.find(i => i.platform === platform)!;
      return <article key={platform} className="release-card"><h2>{labels[platform]}</h2><strong>{r.enabled ? '策略已启用' : '策略已停用'}</strong>
        <dl><dt>最新 / 最低版本</dt><dd>{r.latestVersion} / {r.minimumVersion}</dd><dt>灰度 / 强制更新</dt><dd>{r.rolloutPercentage}% / {r.forceUpdate ? '是' : '否'}</dd><dt>策略版本</dt><dd>{r.revision}</dd><dt>上次发布</dt><dd>{r.revision ? `${date(r.updatedAt)} · ${r.updatedBy}` : '尚未发布'}</dd><dt>分发地址</dt><dd><code>{r.downloadUrl || '未配置'}</code></dd></dl>
        <div className="row-actions">{writable ? <button onClick={() => setEditing(r)}>编辑 {labels[platform]} 策略</button> : <span className="muted">只读</span>}<button onClick={() => setHistory(platform)}>{labels[platform]} 发布历史</button></div>
      </article>;
    })}</div>}
    {editing && <ReleaseEditor key={editing.platform} client={client} original={editing} onClose={() => setEditing(null)} onSaved={r => { setEditing(null); setNotice(`${labels[r.platform]} 策略版本 ${r.revision} 已提交。仅更新本环境的策略；未打包或上传安装包。`); setReload(v => v + 1); }} />}
    {history && <ReleaseHistory key={history} client={client} platform={history} onClose={() => setHistory(null)} />}
  </section>;
}

function ReleaseEditor({ client, original, onClose, onSaved }: { client: PlatformClient; original: Release; onClose: () => void; onSaved: (r: Release) => void }) {
  const dialog = useRef<HTMLDialogElement>(null), alive = useRef(true), running = useRef(false);
  const [draft, setDraft] = useState<Draft>(() => draftOf(original));
  const [review, setReview] = useState(false), [reason, setReason] = useState(''), [confirmed, setConfirmed] = useState(false), [busy, setBusy] = useState(false), [error, setError] = useState('');
  const [frozen, setFrozen] = useState<(Draft & { requestId: string; expectedRevision: number; reason: string; confirmed: true }) | null>(null);
  useEffect(() => { alive.current = true; dialog.current?.showModal(); return () => { alive.current = false; }; }, []);
  const dirty = JSON.stringify(draft) !== JSON.stringify(draftOf(original));
  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => { setDraft(v => ({ ...v, [key]: value })); setError(''); };
  async function submit(event: FormEvent) {
    event.preventDefault(); if (running.current) return;
    if (!review) {
      const parse = (v: string) => /^\d+(\.\d+){0,3}$/.test(v) && v.split('.').every(n => Number(n) <= 2147483647);
      if (!parse(draft.minimumVersion) || !parse(draft.latestVersion)) { setError('请填写最多四段的数字版本号'); return; }
      const a = draft.minimumVersion.split('.').map(Number), b = draft.latestVersion.split('.').map(Number);
      for (let i = 0; i < 4; i++) { if ((a[i] ?? 0) > (b[i] ?? 0)) { setError('最低版本不能高于最新版本'); return; } if ((a[i] ?? 0) < (b[i] ?? 0)) break; }
      if (!Number.isInteger(draft.rolloutPercentage) || draft.rolloutPercentage < 0 || draft.rolloutPercentage > 100) { setError('灰度比例须为 0–100 的整数'); return; }
      if (draft.enabled || draft.downloadUrl) {
        try { const u = new URL(draft.downloadUrl); if (u.protocol !== 'https:' || u.username || u.password || u.search || u.hash || /[?#]/.test(draft.downloadUrl)) throw new Error(); }
        catch { setError('请填写不含凭据、查询参数或片段的 HTTPS 分发地址'); return; }
      }
      setError(''); setReview(true); return;
    }
    if (!frozen && (reason.trim().length < 2 || !confirmed)) { setError('请填写操作理由并确认全平台影响'); return; }
    const body = frozen ?? { ...draft, requestId: crypto.randomUUID(), expectedRevision: original.revision, reason: reason.trim(), confirmed: true as const };
    setFrozen(body); running.current = true; setBusy(true); setError('');
    try {
      const result = await client.request<Release>(`/client-versions/${original.platform}`, 'PUT', body);
      if (!validRelease(result) || result.platform !== original.platform || result.revision !== original.revision + 1 || result.enabled !== body.enabled) throw new Error('响应无法确认，请按原请求重试或核对发布历史');
      if (alive.current) onSaved(result);
    } catch (e) { if (alive.current) setError(e instanceof PlatformError && e.code === 'CLIENT_VERSION_POLICY_CHANGED' ? '版本策略已被其他管理员修改。草稿未保存，请核对发布历史，关闭后重新加载；不会自动覆盖。' : message(e)); }
    finally { running.current = false; if (alive.current) setBusy(false); }
  }
  return <dialog ref={dialog} aria-labelledby="release-editor-title" onCancel={e => { e.preventDefault(); if (!busy) onClose(); }}><form onSubmit={submit}>
    <h2 id="release-editor-title">{review ? '确认发布' : '编辑'} {labels[original.platform]} 策略</h2>
    {!review ? <>
      <label className="confirmation"><input type="checkbox" checked={draft.enabled} onChange={e => set('enabled', e.target.checked)} />启用更新策略</label>
      <label>最低支持版本<input required maxLength={43} value={draft.minimumVersion} onChange={e => set('minimumVersion', e.target.value)} /></label>
      <label>最新发布版本<input required maxLength={43} value={draft.latestVersion} onChange={e => set('latestVersion', e.target.value)} /></label>
      <label>灰度比例（%）<input required type="number" min={0} max={100} step={1} value={draft.rolloutPercentage} onChange={e => set('rolloutPercentage', Number(e.target.value))} /></label>
      <label className="confirmation"><input type="checkbox" checked={draft.forceUpdate} onChange={e => set('forceUpdate', e.target.checked)} />灰度范围内强制更新</label>
      <label>更新说明<textarea maxLength={4000} value={draft.releaseNotes} onChange={e => set('releaseNotes', e.target.value)} /></label>
      <label>HTTPS 分发地址<input type="url" maxLength={2048} required={draft.enabled} value={draft.downloadUrl} onChange={e => set('downloadUrl', e.target.value)} /></label>
    </> : <>
      <p>影响所有企业的 {labels[original.platform]} 客户端。{draft.enabled ? `最低 ${draft.minimumVersion}，最新 ${draft.latestVersion}，灰度 ${draft.rolloutPercentage}%，灰度内${draft.forceUpdate ? '强制' : '可选'}更新；低于最低版本始终强制。` : '停用后不再给出更新或强制升级提示。'}当前策略版本：{original.revision}。</p>
      <p>请先确认分发包已验收且链接长期有效。本操作不会验证安装包签名、上传文件或回退已安装客户端。</p>
      <label>操作理由<textarea required minLength={2} maxLength={300} disabled={!!frozen || busy} value={reason} onChange={e => setReason(e.target.value)} /></label>
      <label className="confirmation"><input type="checkbox" required disabled={!!frozen || busy} checked={confirmed} onChange={e => setConfirmed(e.target.checked)} />我已确认影响所有企业，并核验分发链接与安装包</label>
      {!frozen && <button type="button" onClick={() => setReview(false)}>返回修改</button>}
    </>}
    {error && <p role="alert" className="error">{error}</p>}
    {frozen && <p>请求已锁定，重试复用原请求号。关闭不撤销可能已提交的发布；可在发布历史中核对请求号：<code>{frozen.requestId}</code></p>}
    <div className="dialog-actions"><button type="button" disabled={busy} onClick={onClose}>{frozen ? '关闭并核对历史' : '放弃并关闭'}</button><button className="primary" disabled={busy || (!review && !dirty)}>{busy ? '正在提交…' : frozen ? '按原请求重试' : review ? '确认发布策略' : '审阅更改'}</button></div>
  </form></dialog>;
}

function ReleaseHistory({ client, platform, onClose }: { client: PlatformClient; platform: Platform; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [page, setPage] = useState(1), [data, setData] = useState<Page<History> | null>(null), [error, setError] = useState(''), [reload, setReload] = useState(0);
  useEffect(() => { dialog.current?.showModal(); }, []);
  useEffect(() => {
    const abort = new AbortController(); let current = true; setData(null); setError('');
    client.request<Page<History>>(`/client-versions/${platform}/history?page=${page}&pageSize=10`, 'GET', undefined, abort.signal).then(result => {
      if (!Array.isArray(result.items) || !Number.isSafeInteger(result.total) || result.total < 0 || result.page !== page || result.pageSize !== 10
        || result.items.some(r => !validRelease(r) || r.platform !== platform || typeof r.id !== 'string' || typeof r.reason !== 'string' || typeof r.requestId !== 'string')) throw new Error('发布历史响应不完整，请重试查询');
      if (current) setData(result);
    }).catch(e => { if (current) setError(message(e)); });
    return () => { current = false; abort.abort(); };
  }, [client, platform, page, reload]);
  return <dialog ref={dialog} aria-label={`${labels[platform]} 发布历史`} onCancel={e => { e.preventDefault(); onClose(); }}><h2>{labels[platform]} 发布历史</h2><button onClick={onClose}>关闭历史</button>
    {error ? <p role="alert" className="error">{error}<button onClick={() => setReload(v => v + 1)}>重试历史查询</button></p> : !data ? <p role="status">正在加载历史…</p> : <>
      {!data.items.length && <p>尚无发布记录</p>}{data.items.map(r => <article className="release-history" key={r.id}><h3>策略 {r.revision} · {r.enabled ? '启用' : '停用'}</h3><p>{date(r.updatedAt)} · {r.updatedBy}</p><p>最低 {r.minimumVersion} / 最新 {r.latestVersion} · 灰度 {r.rolloutPercentage}% · {r.forceUpdate ? '灰度内强制' : '灰度内可选'}</p><p>理由：{r.reason}</p><p className="release-notes">{r.releaseNotes}</p><code>{r.downloadUrl}</code><small>请求号：{r.requestId}</small></article>)}
      <footer><span>共 {data.total} 条 · 第 {page} 页</span><div><button disabled={page <= 1} onClick={() => setPage(v => v - 1)}>上一页</button><button disabled={page * data.pageSize >= data.total} onClick={() => setPage(v => v + 1)}>下一页</button></div></footer>
    </>}
  </dialog>;
}
