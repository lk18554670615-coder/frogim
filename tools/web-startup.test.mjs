import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import test from 'node:test';

const startupSource = readFileSync('apps/mobile/web/app_startup.js', 'utf8');
const bootstrapSource = readFileSync('apps/mobile/web/flutter_bootstrap.js', 'utf8')
  .replace('{{flutter_js}}', '').replace('{{flutter_build_config}}', '');
function page() {
  const events = new Map(), nodes = new Map();
  let removed = false, reloaded = false, timer;
  for (const id of ['app-startup', 'app-startup-status', 'app-startup-retry']) {
    nodes.set(id, { textContent: '', classList: { add() {} }, remove() { removed = true; },
      addEventListener(type, fn) { events.set(`${id}:${type}`, fn); } });
  }
  const context = { URL, navigator: {}, document: { baseURI: 'https://example.test/app/',
    getElementById: id => nodes.get(id), querySelector: () => ({ content: 'releases/0123456789abcdef/' }),
    addEventListener: (type, fn) => events.set(type, fn) } };
  context.window = { location: { origin: 'https://example.test', reload() { reloaded = true; } },
    setTimeout(fn) { timer = fn; return 1; }, clearTimeout() {},
    addEventListener: (type, fn) => events.set(type, fn) };
  return { context, events, nodes, slow: () => timer(), removed: () => removed, reloaded: () => reloaded };
}

test('slow loading remains recoverable; hide only after the first Flutter frame', () => {
  const p = page(); runInNewContext(startupSource, p.context);
  p.slow(); assert.match(p.nodes.get('app-startup-status').textContent, /仍在加载/);
  assert.equal(p.removed(), false);
  p.events.get('DOMContentLoaded')(); p.events.get('app-startup-retry:click')();
  assert.equal(p.reloaded(), true);
  p.events.get('flutter-first-frame')(); assert.equal(p.removed(), true);
});
test('worker retirement preserves Push and other applications', async () => {
  const p = page(); const retired = [];
  p.context.navigator.serviceWorker = { getRegistrations: async () => [
    'https://example.test/app/flutter_service_worker.js',
    'https://example.test/app/linli_push_worker.js',
    'https://example.test/other/flutter_service_worker.js'
  ].map(url => ({ active: { scriptURL: url }, unregister() { retired.push(url); } })) };
  runInNewContext(startupSource, p.context); await new Promise(setImmediate);
  assert.deepEqual(retired, ['https://example.test/app/flutter_service_worker.js']);
});
test('versioned URLs reach the loader and engine; runApp alone does not hide the overlay', async () => {
  const p = page(); let load, engineConfig;
  p.context.window._flutter = { loader: { load(options) { load = options; return Promise.resolve(); } } };
  runInNewContext(startupSource, p.context); runInNewContext(bootstrapSource, p.context);
  assert.equal(load.config.entrypointBaseUrl, 'https://example.test/app/releases/0123456789abcdef/');
  assert.equal(load.config.assetBase, load.config.entrypointBaseUrl);
  assert.equal(load.config.canvasKitBaseUrl, load.config.assetBase + 'canvaskit/');
  assert.equal(load.config.fontFallbackBaseUrl, load.config.assetBase + 'font-fallbacks/');
  load.onEntrypointLoaded({ initializeEngine(config) { engineConfig = config; return Promise.resolve({ runApp() {} }); } });
  await new Promise(setImmediate);
  assert.equal(engineConfig.assetBase, load.config.assetBase); assert.equal(p.removed(), false);
  p.events.get('flutter-first-frame')(); assert.equal(p.removed(), true);
});
test('initialization failure produces a recoverable error without hiding it', async () => {
  const p = page(); let load;
  p.context.window._flutter = { loader: { load(options) { load = options; return Promise.resolve(); } } };
  runInNewContext(startupSource, p.context); runInNewContext(bootstrapSource, p.context);
  load.onEntrypointLoaded({ initializeEngine() { return Promise.reject(new Error('engine failed')); } });
  await new Promise(setImmediate);
  assert.match(p.nodes.get('app-startup-status').textContent, /加载失败/); assert.equal(p.removed(), false);
});
test('unpackaged local Web resolves font shards under the application base', () => {
  const p = page(); let load;
  p.context.document.querySelector = () => null;
  p.context.window._flutter = { loader: { load(options) { load = options; return Promise.resolve(); } } };
  runInNewContext(bootstrapSource, p.context);
  assert.equal(load.config.fontFallbackBaseUrl, 'https://example.test/app/font-fallbacks/');
});
