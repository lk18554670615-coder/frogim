'use strict';

// This opt-in probe only uses the known loopback preview. It never installs a
// CA, disables TLS verification, logs credentials or accepts arbitrary servers.
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const root = path.resolve(__dirname, '../../../.data/tenancy-local');
const ca = path.join(root, 'browser-ca.pem');
const platform = 'https://127.0.0.1:18443';
const enterprise = 'https://127.0.0.1:18444';
const output = process.stdout.write.bind(process.stdout);
let stage = 'configuration';

async function request(base, route, body, token, expected = 200) {
  assert.ok([platform, enterprise].includes(base));
  const response = await fetch(base + route, {
    method: body ? 'POST' : 'GET', redirect: 'error',
    headers: {'Content-Type': 'application/json', 'X-Client-Platform': 'web',
      'Origin': platform, ...(token ? {Authorization: `Bearer ${token}`} : {})},
    body: body ? JSON.stringify(body) : undefined,
    signal: AbortSignal.timeout(15000),
  });
  assert.equal(response.status, expected, 'unexpected HTTP status');
  if (expected === 204) return undefined;
  const value = await response.json();
  return value.data ?? value;
}

async function main() {
  assert.equal(path.resolve(process.env.NODE_EXTRA_CA_CERTS ?? ''), ca);
  assert.notEqual(process.env.NODE_TLS_REJECT_UNAUTHORIZED, '0');
  const marker = JSON.parse(fs.readFileSync(path.join(root, 'initialized.json')));
  assert.equal(marker.tenantId, 'default');
  assert.equal(marker.version, 1);
  const credentials = JSON.parse(fs.readFileSync(path.join(root, 'credentials.json')));
  stage = 'unified Web assets';
  const page = await fetch(platform + '/app/', {redirect: 'error', signal: AbortSignal.timeout(15000)});
  assert.equal(page.status, 200);
  const html = await page.text();
  assert.ok(html.includes('<base href="/app/">'));
  assert.ok(html.indexOf('wukong_session_scope.js') < html.indexOf('<script src="flutter_bootstrap.js"'));
  for (const route of ['wukong_session_scope.js', 'tenant_push_worker.js', 'linli_push_worker.js', 'main.dart.js', 'assets/assets/fonts/NotoSansSC-Regular.otf']) {
    const response = await fetch(platform + '/app/' + route, {redirect: 'error', signal: AbortSignal.timeout(15000)});
    assert.equal(response.status, 200);
    assert.ok(!response.headers.get('content-type').includes('text/html'), 'missing asset must not be HTML');
    if (route.endsWith('.otf')) assert.match(response.headers.get('cache-control'), /max-age=2592000/);
    if (route.endsWith('_push_worker.js')) assert.equal(response.headers.get('cache-control'), 'no-cache');
    await response.body.cancel();
  }
  stage = 'platform password login';
  const auth = await request(platform, '/v2/auth/password-login', {
    phone: credentials.UserPhone, password: credentials.UserPassword,
  });
  assert.equal(auth.tenantContext.httpBaseUrl, enterprise);
  let business;
  let sdk;
  let wk;
  let reset;
  let failedAt;
  // Pinned upstream SDK logs packet contents even with debug=false. Suppress
  // vendor diagnostics inside this probe, never forward raw exceptions/tokens.
  console.log = console.warn = console.error = console.debug = () => {};
  try {
    stage = 'enterprise ticket exchange';
    business = await request(enterprise, '/v2/auth/tenant-session', {sessionTicket: auth.sessionTicket});
    assert.equal(business.imSession.uid, business.user.id);
    const im = business.imSession;
    assert.equal(im.sdk, 'wukongimjssdk');
    assert.equal(im.deviceFlag, 1);
    assert.equal(im.wsUrl, 'wss://127.0.0.1:18444/im');
    wk = require('../web/wukongimjssdk-1.3.5.umd.js');
    reset = require('../web/wukong_session_scope.js');
    for (let iteration = 0; iteration < 2; iteration++) {
      stage = `verified TLS WSS connection ${iteration + 1}`;
      sdk = wk.WKSDK.shared();
      Object.assign(sdk.config, {uid: im.uid, token: im.token, addr: im.wsUrl, deviceFlag: 1, debug: false});
      sdk.config.provider.syncConversationsCallback = async () => [];
      sdk.config.provider.syncRemindersCallback = async () => [];
      await new Promise((resolve, reject) => {
        const timeout = setTimeout(() => reject(new Error('connection timeout')), 15000);
        sdk.connectManager.addConnectStatusListener((status, reason) => {
          if (status === 1) { clearTimeout(timeout); resolve(); }
          else if (status === 3 || status === 4 || (status === 0 && reason)) {
            clearTimeout(timeout); reject(new Error('connection rejected'));
          }
        });
        sdk.connect();
      });
      reset(wk, sdk);
      sdk = undefined;
    }
    stage = 'direct enterprise profile';
    const profile = await request(enterprise, '/v2/users/me', undefined, business.accessToken);
    assert.equal(profile.id, business.user.id);
  } catch (error) {
    failedAt = stage;
    throw error;
  } finally {
    if (sdk) reset(wk, sdk);
    try {
      stage = failedAt ?? 'enterprise logout';
      if (business) await request(enterprise, '/v2/auth/logout', {refreshToken: business.refreshToken}, business.accessToken, 204);
    } finally {
      stage = failedAt ?? 'platform logout';
      await request(platform, '/v2/auth/logout', {refreshToken: auth.refreshToken});
    }
  }
  output('PASS: loopback-only Web assets and separate no-cache push workers, 30-day font cache, platform login, enterprise direct profile, real pinned SDK WSS CONNACK twice after session reset, TLS verified. No message/call/device UI acceptance claimed.\n');
}

main().catch(() => {
  // Stage names are static; do not include response bodies, URL queries or
  // exception details which may contain tokens or private connection data.
  output(`FAIL: local preview probe at ${stage}; no credentials were printed.\n`);
  process.exitCode = 1;
});
