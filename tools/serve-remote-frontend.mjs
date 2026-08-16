import { createReadStream, existsSync, statSync } from 'node:fs';
import { createServer } from 'node:http';
import { request as httpsRequest } from 'node:https';
import { extname, resolve, sep } from 'node:path';

const port = Number(process.env.FRONTEND_PORT ?? 8093);
const root = resolve(process.env.FRONTEND_ROOT ?? 'apps/mobile/build/web');
const remote = new URL(process.env.REMOTE_SERVER_ORIGIN ?? '');

if (remote.protocol !== 'https:' || !remote.hostname || remote.username || remote.password) {
  throw new Error('REMOTE_SERVER_ORIGIN must be a credential-free HTTPS origin');
}
if (!existsSync(resolve(root, 'index.html'))) {
  throw new Error(`Frontend build not found at ${root}`);
}

const requiredFrontendFiles = [
  'index.html',
  'flutter_bootstrap.js',
  'main.dart.js',
  'assets/AssetManifest.bin.json',
  'assets/FontManifest.json',
];
const missingFrontendFiles = requiredFrontendFiles.filter(
  (path) => !existsSync(resolve(root, path)),
);
if (missingFrontendFiles.length > 0) {
  throw new Error(
    `Frontend build is incomplete at ${root}; missing: ${missingFrontendFiles.join(', ')}`,
  );
}

const proxiedPrefixes = ['/v2/', '/api/', '/nexachat-media/', '/im'];
const proxiedExactPaths = new Set(['/health', '/ready']);
const contentTypes = new Map([
  ['.css', 'text/css; charset=utf-8'],
  ['.html', 'text/html; charset=utf-8'],
  ['.ico', 'image/x-icon'],
  ['.js', 'text/javascript; charset=utf-8'],
  ['.json', 'application/json; charset=utf-8'],
  ['.mjs', 'text/javascript; charset=utf-8'],
  ['.png', 'image/png'],
  ['.svg', 'image/svg+xml'],
  ['.wasm', 'application/wasm'],
  ['.webp', 'image/webp'],
  ['.woff', 'font/woff'],
  ['.woff2', 'font/woff2'],
]);

function shouldProxy(pathname) {
  return proxiedExactPaths.has(pathname) || proxiedPrefixes.some((prefix) => pathname.startsWith(prefix));
}

function proxyHeaders(headers) {
  const next = { ...headers, host: remote.host, origin: remote.origin };
  delete next['proxy-connection'];
  return next;
}

function proxyHttp(req, res) {
  const target = new URL(req.url ?? '/', remote);
  const upstream = httpsRequest(target, {
    method: req.method,
    headers: proxyHeaders(req.headers),
    servername: remote.hostname,
  }, (upstreamResponse) => {
    res.writeHead(upstreamResponse.statusCode ?? 502, upstreamResponse.headers);
    upstreamResponse.pipe(res);
  });
  upstream.on('error', (error) => {
    if (!res.headersSent) res.writeHead(502, { 'content-type': 'application/json; charset=utf-8' });
    res.end(JSON.stringify({ error: { code: 'REMOTE_UNAVAILABLE', message: error.message } }));
  });
  req.pipe(upstream);
}

function safeStaticPath(pathname) {
  const decoded = decodeURIComponent(pathname).replaceAll('\\', '/');
  const candidate = resolve(root, `.${decoded}`);
  return candidate === root || candidate.startsWith(`${root}${sep}`) ? candidate : undefined;
}

function serveStatic(req, res, pathname) {
  if (req.method !== 'GET' && req.method !== 'HEAD') {
    res.writeHead(405, { allow: 'GET, HEAD' });
    res.end();
    return;
  }
  let file = safeStaticPath(pathname === '/' ? '/index.html' : pathname);
  if (file && existsSync(file) && statSync(file).isDirectory()) file = resolve(file, 'index.html');
  if (!file || !existsSync(file) || !statSync(file).isFile()) {
    const acceptsHtml = String(req.headers.accept ?? '').includes('text/html');
    const isNavigation = !extname(pathname) && acceptsHtml;
    if (!isNavigation) {
      res.writeHead(404, {
        'content-type': 'text/plain; charset=utf-8',
        'cache-control': 'no-cache, no-store, must-revalidate',
        'x-content-type-options': 'nosniff',
      });
      res.end('Not found');
      return;
    }
    file = resolve(root, 'index.html');
  }
  const extension = extname(file).toLowerCase();
  res.writeHead(200, {
    'content-type': contentTypes.get(extension) ?? 'application/octet-stream',
    // Flutter keeps stable names such as main.dart.js between rebuilds. Local
    // preview must therefore revalidate every asset or the browser can run an
    // old bundle against newly generated manifests and service workers.
    'cache-control': 'no-cache, no-store, must-revalidate',
    'x-content-type-options': 'nosniff',
  });
  if (req.method === 'HEAD') res.end();
  else createReadStream(file).pipe(res);
}

const server = createServer((req, res) => {
  let pathname;
  try {
    pathname = new URL(req.url ?? '/', 'http://127.0.0.1').pathname;
  } catch {
    res.writeHead(400).end();
    return;
  }
  if (shouldProxy(pathname)) proxyHttp(req, res);
  else serveStatic(req, res, pathname);
});

server.on('upgrade', (req, clientSocket, head) => {
  const pathname = new URL(req.url ?? '/', 'http://127.0.0.1').pathname;
  if (!shouldProxy(pathname)) {
    clientSocket.end('HTTP/1.1 404 Not Found\r\nConnection: close\r\n\r\n');
    return;
  }
  const target = new URL(req.url ?? '/', remote);
  const upstream = httpsRequest(target, {
    method: req.method,
    headers: proxyHeaders(req.headers),
    servername: remote.hostname,
  });
  upstream.on('upgrade', (upstreamResponse, upstreamSocket, upstreamHead) => {
    const status = upstreamResponse.statusCode ?? 101;
    const message = upstreamResponse.statusMessage ?? 'Switching Protocols';
    const headers = upstreamResponse.rawHeaders.reduce((lines, value, index, values) =>
      index % 2 === 0 ? [...lines, `${value}: ${values[index + 1]}`] : lines, []);
    clientSocket.write(`HTTP/1.1 ${status} ${message}\r\n${headers.join('\r\n')}\r\n\r\n`);
    if (head.length) upstreamSocket.write(head);
    if (upstreamHead.length) clientSocket.write(upstreamHead);
    upstreamSocket.pipe(clientSocket).pipe(upstreamSocket);
  });
  upstream.on('response', (upstreamResponse) => {
    clientSocket.end(`HTTP/1.1 ${upstreamResponse.statusCode ?? 502} ${upstreamResponse.statusMessage ?? 'Bad Gateway'}\r\nConnection: close\r\n\r\n`);
  });
  upstream.on('error', () => clientSocket.end('HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n'));
  upstream.end();
});

server.listen(port, '127.0.0.1', () => {
  process.stdout.write(`Flutter Web: http://127.0.0.1:${port}\nRemote services: ${remote.origin}\n`);
});
