'use strict';

// Flutter 3.44.8's Windows browser-test server compares a native path against
// "canvaskit/", producing 404s. Supply ONLY the existing FVM SDK CanvasKit assets
// to this test harness via CDP. No SDK patch, package upgrade, production API,
// TLS exception, installed browser profile or application logic interception.
// After starting the named test, pass its isolated browser's debugging port.
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const port = Number(process.argv[2]);
assert.ok(Number.isInteger(port) && port > 1024 && port <= 65535);
const root = fs.realpathSync(path.resolve(__dirname,
  '../.fvm/flutter_sdk/bin/cache/flutter_web_sdk/canvaskit'));
const suite = '/session_coordination_browser_test.html';

(async () => {
  const pages = await (await fetch(`http://127.0.0.1:${port}/json/list`,
    {signal: AbortSignal.timeout(5000)})).json();
  const candidates = pages.filter(p => {
    const u = new URL(p.url);
    return u.protocol === 'http:' && u.hostname === 'localhost' &&
      u.pathname === '/static/index.html' && u.searchParams.has('managerUrl');
  });
  assert.equal(candidates.length, 1, 'Expected one isolated Flutter test host');
  const page = candidates[0], origin = new URL(page.url).origin;
  const socket = new WebSocket(page.webSocketDebuggerUrl);
  let id = 10, count = 0;
  const send = (method, params) => socket.send(JSON.stringify({id: id++, method, params}));
  const timer = setTimeout(() => socket.close(), 45000);
  socket.onopen = () => socket.send(JSON.stringify({id: 1, method: 'Fetch.enable',
    params: {patterns: [{urlPattern: `${origin}/canvaskit/*`}]}}));
  socket.onmessage = e => {
    try {
      const message = JSON.parse(e.data);
      if (message.id === 1) {
        assert.ok(!message.error, 'Unable to supply test assets');
        send('Runtime.evaluate', {expression: `(() => {
          const frame = Array.from(document.querySelectorAll('iframe'))
            .find(f => new URL(f.src).pathname === ${JSON.stringify(suite)});
          if (!frame) throw Error('Expected test suite is not loaded');
          frame.contentWindow.location.reload();
        })()`});
      }
      if (message.error || message.result?.exceptionDetails) {
        throw Error('Test harness command failed');
      }
      if (message.method !== 'Fetch.requestPaused') return;
      const request = message.params, url = new URL(request.request.url);
      assert.equal(url.origin, origin);
      assert.match(url.pathname, /^\/canvaskit\/(chromium\/)?canvaskit\.(js|wasm)$/);
      const file = path.resolve(root, url.pathname.slice('/canvaskit/'.length));
      assert.ok(file.startsWith(root + path.sep));
      send('Fetch.fulfillRequest', {requestId: request.requestId, responseCode: 200,
        responseHeaders: [{name: 'Content-Type',
          value: file.endsWith('.wasm') ? 'application/wasm' : 'text/javascript'}],
        body: fs.readFileSync(file).toString('base64')});
      count++;
    } catch (_) {
      process.exitCode = 1;
      console.error('Could not provide local Flutter test assets');
      socket.close();
    }
  };
  socket.onclose = () => {
    clearTimeout(timer);
    console.log(`Provided ${count} local SDK resources; Flutter test output determines success.`);
  };
  socket.onerror = () => { process.exitCode = 1; socket.close(); };
})().catch(() => { console.error('Isolated test browser unavailable'); process.exitCode = 1; });
