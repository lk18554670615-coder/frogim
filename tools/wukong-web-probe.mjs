#!/usr/bin/env node

import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';

const defaults = {
  api: 'http://127.0.0.1:8080',
  web: 'http://127.0.0.1:8090',
  otp: '123456',
  timeout: 30_000,
  output: '',
  browser: process.env.WUKONG_WEB_BROWSER || '',
};

function parseArgs(argv) {
  const result = { ...defaults };
  for (let index = 0; index < argv.length; index += 1) {
    const key = argv[index];
    if (key === '--help' || key === '-h') {
      console.log(`Usage: node tools/wukong-web-probe.mjs [options]

Options:
  --api URL       business API (default ${defaults.api})
  --web URL       built Flutter Web origin (default ${defaults.web})
  --otp CODE      development OTP (default ${defaults.otp})
  --browser PATH  Chrome/Edge executable (or WUKONG_WEB_BROWSER)
  --timeout MS    overall timeout (default ${defaults.timeout})
  --output FILE   optional JSON evidence file
`);
      process.exit(0);
    }
    const name = key.startsWith('--') ? key.slice(2) : '';
    if (!name || !(name in result) || index + 1 >= argv.length) {
      throw new Error(`unknown or incomplete argument: ${key}`);
    }
    result[name] = argv[index + 1];
    index += 1;
  }
  result.timeout = Number(result.timeout);
  if (!Number.isInteger(result.timeout) || result.timeout < 10_000 || result.timeout > 180_000) {
    throw new Error('--timeout must be an integer between 10000 and 180000');
  }
  result.api = new URL(result.api).origin;
  result.web = new URL(result.web).origin;
  for (const [name, value] of [['--api', result.api], ['--web', result.web]]) {
    const hostname = new URL(value).hostname;
    if (!['127.0.0.1', 'localhost', '[::1]'].includes(hostname)) {
      throw new Error(`${name} must use a loopback host; this probe must never target production`);
    }
  }
  return result;
}

function browserExecutable(explicit) {
  const candidates = [
    explicit,
    process.env.PROGRAMFILES && path.join(process.env.PROGRAMFILES, 'Google', 'Chrome', 'Application', 'chrome.exe'),
    process.env['PROGRAMFILES(X86)'] && path.join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
    process.env.PROGRAMFILES && path.join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
    '/usr/bin/google-chrome',
    '/usr/bin/chromium',
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  ].filter(Boolean);
  const found = candidates.find((candidate) => existsSync(candidate));
  if (!found) throw new Error('Chrome or Edge was not found; pass --browser or WUKONG_WEB_BROWSER');
  return found;
}

async function freePort() {
  return await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      server.close((error) => error ? reject(error) : resolve(address.port));
    });
  });
}

const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function waitFor(fn, { timeout, description, interval = 100 }) {
  const deadline = Date.now() + timeout;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const value = await fn();
      if (value) return value;
    } catch (error) {
      lastError = error;
    }
    await sleep(interval);
  }
  const suffix = lastError ? `: ${lastError.message}` : '';
  throw new Error(`timed out waiting for ${description}${suffix}`);
}

class CDPClient {
  constructor(url) {
    this.url = url;
    this.nextId = 1;
    this.pending = new Map();
  }

  async open() {
    this.socket = new WebSocket(this.url);
    this.socket.addEventListener('message', (event) => {
      const message = JSON.parse(event.data);
      if (!message.id) return;
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      if (message.error) pending.reject(new Error(`${pending.method}: ${message.error.message}`));
      else pending.resolve(message.result);
    });
    this.socket.addEventListener('close', () => {
      for (const pending of this.pending.values()) pending.reject(new Error('CDP connection closed'));
      this.pending.clear();
    });
    await new Promise((resolve, reject) => {
      this.socket.addEventListener('open', resolve, { once: true });
      this.socket.addEventListener('error', reject, { once: true });
    });
  }

  call(method, params = {}) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { method, resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }

  async evaluate(expression) {
    const result = await this.call('Runtime.evaluate', {
      expression,
      awaitPromise: true,
      returnByValue: true,
      userGesture: true,
    });
    if (result.exceptionDetails) {
      const detail = result.exceptionDetails.exception?.description || result.exceptionDetails.text;
      throw new Error(`browser evaluation failed: ${detail}`);
    }
    return result.result.value;
  }

  close() {
    this.socket?.close();
  }
}

async function startBrowser(executable, webOrigin, timeout) {
  const port = await freePort();
  const profile = await mkdtemp(path.join(os.tmpdir(), 'linli-wukong-web-probe-'));
  const child = spawn(executable, [
    '--headless=new',
    '--disable-gpu',
    '--disable-background-networking',
    '--disable-component-update',
    '--no-first-run',
    '--no-default-browser-check',
    `--remote-debugging-port=${port}`,
    `--user-data-dir=${profile}`,
    `${webOrigin}/`,
  ], { stdio: ['ignore', 'ignore', 'pipe'], windowsHide: true });
  let diagnostics = '';
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (chunk) => { diagnostics = `${diagnostics}${chunk}`.slice(-8000); });
  const pages = await waitFor(async () => {
    if (child.exitCode !== null) throw new Error(`browser exited ${child.exitCode}: ${diagnostics}`);
    const response = await fetch(`http://127.0.0.1:${port}/json/list`);
    if (!response.ok) return null;
    const items = await response.json();
    return items.find((item) => item.type === 'page' && item.webSocketDebuggerUrl) ? items : null;
  }, { timeout, description: 'browser DevTools endpoint' });
  const page = pages.find((item) => item.type === 'page' && item.webSocketDebuggerUrl);
  return {
    child,
    profile,
    page,
    async close() {
      if (child.exitCode === null) child.kill();
      await Promise.race([
        new Promise((resolve) => child.once('exit', resolve)),
        sleep(2000),
      ]);
      const tempRoot = path.resolve(os.tmpdir());
      const resolved = path.resolve(profile);
      if (resolved.startsWith(`${tempRoot}${path.sep}`) && path.basename(resolved).startsWith('linli-wukong-web-probe-')) {
        await rm(resolved, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 }).catch(() => {});
      }
    },
  };
}

async function jsonRequest(url, { method = 'GET', token = '', platform = '', body } = {}) {
  const headers = { accept: 'application/json' };
  if (body !== undefined) headers['content-type'] = 'application/json';
  if (token) headers.authorization = `Bearer ${token}`;
  if (platform) headers['x-client-platform'] = platform;
  const response = await fetch(url, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  let parsed = null;
  if (text) {
    try { parsed = JSON.parse(text); } catch { parsed = text; }
  }
  if (!response.ok) {
    const error = new Error(`${method} ${new URL(url).pathname}: HTTP ${response.status} ${text}`);
    error.status = response.status;
    throw error;
  }
  return parsed;
}

async function provision(api, otp) {
  const suffix = String(Date.now()).slice(-8);
  const login = (phone, name, platform) => jsonRequest(`${api}/v2/auth/login`, {
    method: 'POST', platform, body: { phone, code: otp, name },
  });
  const alice = await login(`139${suffix}`, 'Web probe Alice', 'android');
  const bob = await login(`138${suffix}`, 'Web probe Bob', 'web');
  const friend = await jsonRequest(`${api}/v2/contacts/requests`, {
    method: 'POST', token: alice.accessToken,
    body: { userId: bob.user.id, message: 'web recovery probe' },
  });
  await jsonRequest(`${api}/v2/contacts/requests/${encodeURIComponent(friend.id)}/accept`, {
    method: 'POST', token: bob.accessToken, body: {},
  });
  const direct = await jsonRequest(`${api}/v2/channels/direct`, {
    method: 'POST', token: alice.accessToken, body: { userId: bob.user.id },
  });
  await sleep(500);
  return { alice, bob, conversationId: direct.id };
}

async function appendStreamEvent(api, session, conversationId, clientMsgNo, eventType, eventId, payload) {
  const url = `${api}/v2/messages/conversations/${encodeURIComponent(conversationId)}/streams/${encodeURIComponent(clientMsgNo)}/events`;
  return await waitFor(async () => {
    try {
      return await jsonRequest(url, {
        method: 'POST', token: session.accessToken,
        body: { eventId, eventType, eventKey: 'main', payload },
      });
    } catch (error) {
      if (error.status === 404) return null;
      throw error;
    }
  }, { timeout: 5000, description: `stream anchor index before ${eventType}`, interval: 100 });
}

async function sendStream(api, session, conversationId, label) {
  const clientMsgNo = `web_probe_${label}_${Date.now()}`;
  await jsonRequest(`${api}/v2/messages/conversations/${encodeURIComponent(conversationId)}/streams`, {
    method: 'POST', token: session.accessToken,
    body: { clientMsgNo, initialText: '' },
  });
  await appendStreamEvent(api, session, conversationId, clientMsgNo, 'stream.delta', `${clientMsgNo}_delta`, {
    kind: 'text', delta: label,
  });
  await appendStreamEvent(api, session, conversationId, clientMsgNo, 'stream.close', `${clientMsgNo}_close`, {
    end_reason: 0,
  });
  await appendStreamEvent(api, session, conversationId, clientMsgNo, 'stream.finish', `${clientMsgNo}_finish`, {});
  return clientMsgNo;
}

async function run(config) {
  const startedAt = new Date().toISOString();
  const deadline = Date.now() + config.timeout;
  const remaining = () => Math.max(1000, deadline - Date.now());
  const accounts = await provision(config.api, config.otp);
  const browser = await startBrowser(browserExecutable(config.browser), config.web, remaining());
  const cdp = new CDPClient(browser.page.webSocketDebuggerUrl);
  try {
    await cdp.open();
    await Promise.all([
      cdp.call('Page.enable'),
      cdp.call('Runtime.enable'),
      cdp.call('Network.enable'),
    ]);
    await cdp.call('Page.navigate', { url: `${config.web}/` });
    await waitFor(async () => (await cdp.evaluate('document.readyState')) !== 'loading', {
      timeout: remaining(), description: 'Web origin document',
    });
    const sdkUrl = `${config.web}/wukongimjssdk-1.3.5.umd.js`;
    const probeDocument = `<!doctype html><meta charset="utf-8"><title>WuKong Web Probe</title><script src=${JSON.stringify(sdkUrl)}></script>`;
    await cdp.evaluate(`document.open(); document.write(${JSON.stringify(probeDocument)}); document.close(); true`);
    await waitFor(async () => await cdp.evaluate('Boolean(globalThis.wk && globalThis.wk.WKSDK)'), {
      timeout: remaining(), description: 'official WuKong JS SDK 1.3.5',
    });

    const im = accounts.bob.imSession;
    await cdp.evaluate(`(() => {
      const sdk = globalThis.wk.WKSDK.shared();
      sdk.disconnect();
      sdk.config.uid = ${JSON.stringify(im.uid)};
      sdk.config.token = ${JSON.stringify(im.token)};
      sdk.config.addr = ${JSON.stringify(im.wsUrl)};
      sdk.config.deviceFlag = 1;
      sdk.config.debug = false;
      sdk.config.provider.connectAddrCallback = (complete) => complete(${JSON.stringify(im.wsUrl)});
      sdk.config.provider.syncConversationsCallback = async () => [];
      sdk.config.provider.syncMessagesCallback = async () => [];
      sdk.config.provider.syncMessageExtraCallback = async () => [];
      sdk.config.provider.syncRemindersCallback = async () => [];
      sdk.config.provider.reminderDoneCallback = async () => undefined;
      globalThis.__wkProbe = { states: [], messages: [], events: [], startedAt: Date.now() };
      sdk.connectManager.addConnectStatusListener((status, reasonCode) => {
        globalThis.__wkProbe.states.push({ status, reasonCode: reasonCode || 0, at: Date.now() });
      });
      sdk.chatManager.addMessageListener((message) => {
        globalThis.__wkProbe.messages.push({
          clientMsgNo: message.clientMsgNo || '',
          messageId: message.messageID || '',
          messageSeq: message.messageSeq || 0,
          contentType: message.contentType || 0,
        });
      });
      sdk.eventManager.addEventListener((event) => {
        globalThis.__wkProbe.events.push({
          id: event.id || '', type: event.type || '', timestamp: event.timestamp || 0,
          data: event.dataJson || null,
        });
      });
      sdk.connect();
      return sdk.config.sdkVersion;
    })()`);
    await waitFor(async () => await cdp.evaluate('__wkProbe.states.some((item) => item.status === 1)'), {
      timeout: Math.min(10_000, remaining()), description: 'initial JS SDK WSS connection',
    });

    const firstClientMsgNo = await sendStream(config.api, accounts.alice, accounts.conversationId, 'WEB_EVENT_BEFORE_RECOVERY');
    await waitFor(async () => await cdp.evaluate(`__wkProbe.messages.some((item) => item.clientMsgNo === ${JSON.stringify(firstClientMsgNo)})`), {
      timeout: Math.min(10_000, remaining()), description: 'initial stream anchor message',
    });
    await waitFor(async () => await cdp.evaluate("__wkProbe.events.some((item) => item.type === 'stream.delta' && JSON.stringify(item.data).includes('WEB_EVENT_BEFORE_RECOVERY'))"), {
      timeout: Math.min(10_000, remaining()), description: 'initial stream EventPacket',
    });

    await cdp.call('Network.emulateNetworkConditions', {
      offline: true, latency: 0, downloadThroughput: 0, uploadThroughput: 0,
      connectionType: 'none',
    });
    await cdp.evaluate("globalThis.wk.WKSDK.shared().connectManager.ws?.ws?.close(); true");
    await waitFor(async () => await cdp.evaluate('__wkProbe.states.some((item, index) => index > 0 && item.status === 0)'), {
      timeout: Math.min(5000, remaining()), description: 'offline disconnect notification',
    });
    await sleep(1000);
    const onlineAt = Date.now();
    await cdp.call('Network.emulateNetworkConditions', {
      offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1,
      connectionType: 'wifi',
    });
    await waitFor(async () => await cdp.evaluate('__wkProbe.states.filter((item) => item.status === 1).length >= 2'), {
      timeout: Math.min(10_000, remaining()), description: 'JS SDK reconnection within 10 seconds',
    });
    const reconnectedAt = Date.now();

    const secondClientMsgNo = await sendStream(config.api, accounts.alice, accounts.conversationId, 'WEB_EVENT_AFTER_RECOVERY');
    await waitFor(async () => await cdp.evaluate(`__wkProbe.messages.some((item) => item.clientMsgNo === ${JSON.stringify(secondClientMsgNo)})`), {
      timeout: Math.min(10_000, remaining()), description: 'post-recovery stream anchor message',
    });
    await waitFor(async () => await cdp.evaluate("__wkProbe.events.some((item) => item.type === 'stream.delta' && JSON.stringify(item.data).includes('WEB_EVENT_AFTER_RECOVERY'))"), {
      timeout: Math.min(10_000, remaining()), description: 'post-recovery stream EventPacket',
    });
    const browserEvidence = await cdp.evaluate('__wkProbe');
    const result = {
      startedAt,
      completedAt: new Date().toISOString(),
      sdk: im.sdk,
      sdkVersion: '1.3.5',
      deviceFlag: im.deviceFlag,
      wsProtocol: new URL(im.wsUrl).protocol,
      initialConnection: true,
      streamEventBeforeRecovery: true,
      disconnectObserved: true,
      reconnectMilliseconds: reconnectedAt - onlineAt,
      reconnectWithin10Seconds: reconnectedAt - onlineAt <= 10_000,
      streamEventAfterRecovery: true,
      browserStates: browserEvidence.states,
      receivedMessages: browserEvidence.messages.length,
      receivedEvents: browserEvidence.events.length,
    };
    if (!result.reconnectWithin10Seconds) throw new Error(`reconnect took ${result.reconnectMilliseconds}ms`);
    if (config.output) {
      await writeFile(path.resolve(config.output), `${JSON.stringify(result, null, 2)}\n`, { encoding: 'utf8', flag: 'wx' });
    }
    console.log(JSON.stringify(result, null, 2));
  } finally {
    cdp.close();
    await browser.close();
  }
}

try {
  await run(parseArgs(process.argv.slice(2)));
} catch (error) {
  console.error(`WuKong Web probe failed: ${error.stack || error.message}`);
  process.exitCode = 1;
}
