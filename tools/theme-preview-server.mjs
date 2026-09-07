// Local visual QA only. Uses the admin test fixtures, never a production API.
// node tools/theme-preview-server.mjs [port=4186]
import { createServer } from 'node:http';
import { readFile, stat } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, extname, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(resolve(root, 'apps/admin/package.json'));
const ts = require('typescript');
const source = await readFile(resolve(root, 'apps/admin/src/App.test.tsx'), 'utf8');
const fixtureSource = source.slice(source.indexOf('const session ='), source.indexOf("describe('青蛙呱呱管理后台'"));
if (!fixtureSource.includes('async function liveFixture(')) throw new Error('Admin fixture not found');
const fixture = new Function(ts.transpile(fixtureSource) + '\nreturn {session, liveFixture};')();
const mime = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.mjs': 'text/javascript', '.css': 'text/css', '.json': 'application/json', '.png': 'image/png', '.svg': 'image/svg+xml', '.wasm': 'application/wasm', '.woff2': 'font/woff2', '.woff': 'font/woff', '.ttf': 'font/ttf', '.otf': 'font/otf' };
const port = Number(process.argv[2] ?? 4186);
createServer(async (req, res) => {
  try {
    const url = new URL(req.url, `http://127.0.0.1:${port}`);
    res.setHeader('Cache-Control', 'no-store');
    if (url.pathname === '/preview-desktop') {
      const src = url.searchParams.get('app') === 'mobile'
        ? `/mobile/?dark=${url.searchParams.get('dark') === '1' ? '1' : '0'}` : '/users';
      res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
      res.end(`<!doctype html><html lang="zh-CN"><title>1280px 本地配色预览 · 模拟数据</title>
        <style>body{margin:0;background:#F2F5F8}iframe{width:1280px;height:960px;border:0;transform-origin:top left}</style>
        <iframe title="1280px 配色预览" src="${src}"></iframe>
        <script>function fit(){document.querySelector('iframe').style.transform='scale('+Math.min(innerWidth/1280,1)+')'}addEventListener('resize',fit);fit();</script></html>`);
      return;
    }
    if (url.pathname.startsWith('/api/')) {
      let payload;
      if (url.pathname.endsWith('/auth/login') || url.pathname.endsWith('/auth/me')) {
        payload = { ...fixture.session, accessToken: 'local-preview-only', expiresIn: 3600, displayName: '本地配色预览' };
      } else if (req.method !== 'GET') {
        res.writeHead(403, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ message: '配色预览不执行后台写操作' }));
        return;
      } else {
        payload = await (await fixture.liveFixture(req.url)).json();
      }
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify(payload));
      return;
    }
    const mobile = url.pathname.startsWith('/mobile/');
    const base = resolve(root, mobile ? 'apps/mobile/build/theme-preview' : 'apps/admin/dist');
    const relative = decodeURIComponent(mobile ? url.pathname.slice(8) : url.pathname.slice(1));
    let file = resolve(base, relative || 'index.html');
    if (!file.startsWith(base + sep)) { res.writeHead(403); res.end(); return; }
    try { if (!(await stat(file)).isFile()) throw new Error('not a file'); }
    catch { if (extname(file)) throw new Error('missing asset'); file = resolve(base, 'index.html'); }
    res.setHeader('Content-Type', mime[extname(file)] ?? 'application/octet-stream');
    res.end(await readFile(file));
  } catch {
    res.writeHead(404); res.end('Local preview resource unavailable');
  }
}).listen(port, '127.0.0.1', () => console.log(`Local-only preview: http://127.0.0.1:${port}/ ; Flutter: /mobile/`));
