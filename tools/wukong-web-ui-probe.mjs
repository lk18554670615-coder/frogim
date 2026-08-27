#!/usr/bin/env node

import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';

const config = {
  api: new URL(process.env.WUKONG_WEB_UI_API || 'http://127.0.0.1:8080').origin,
  web: new URL(process.argv[2] || 'http://127.0.0.1:8090').origin,
  browser: process.env.WUKONG_WEB_BROWSER || '',
  phone: process.env.WUKONG_WEB_UI_PHONE || '13800000001',
  otp: process.env.WUKONG_WEB_UI_OTP || '123456',
  conversationId: process.env.WUKONG_WEB_UI_CONVERSATION || 'conv_c8c1ec65e38f9b709488c065',
  timeout: 30_000,
};

for (const origin of [config.api, config.web]) {
  if (!['127.0.0.1', 'localhost', '[::1]'].includes(new URL(origin).hostname)) {
    throw new Error('the Web UI probe only accepts loopback origins');
  }
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
  if (!found) throw new Error('Chrome or Edge was not found; set WUKONG_WEB_BROWSER');
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

async function waitFor(fn, description, timeout = config.timeout) {
  const deadline = Date.now() + timeout;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const value = await fn();
      if (value) return value;
    } catch (error) {
      lastError = error;
    }
    await sleep(100);
  }
  throw new Error(`timed out waiting for ${description}${lastError ? `: ${lastError.message}` : ''}`);
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
  const parsed = text ? JSON.parse(text) : null;
  if (!response.ok) throw new Error(`${method} ${new URL(url).pathname}: HTTP ${response.status} ${text}`);
  return parsed;
}

async function clickByName(cdp, name, { role = '', occurrence = 0, xOffset = null } = {}) {
  const ax = await cdp.call('Accessibility.getFullAXTree');
  const nodes = ax.nodes.filter((node) =>
    node.name?.value === name
    && node.backendDOMNodeId
    && (!role || node.role?.value === role)
  );
  const node = nodes[occurrence];
  if (!node) throw new Error(`accessibility element not found: ${role}:${name}[${occurrence}]`);
  const model = await cdp.call('DOM.getBoxModel', { backendNodeId: node.backendDOMNodeId });
  const quad = model.model.border;
  const x = xOffset === null
    ? (quad[0] + quad[2] + quad[4] + quad[6]) / 4
    : quad[0] + xOffset;
  const y = (quad[1] + quad[3] + quad[5] + quad[7]) / 4;
  await cdp.call('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
  await cdp.call('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 });
  await cdp.call('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 });
}

async function typeIntoInput(cdp, label, text) {
  const rect = await cdp.evaluate(`(() => {
    const input = [...document.querySelectorAll('input')]
      .find((item) => item.getAttribute('aria-label') === ${JSON.stringify(label)});
    if (!input) throw new Error('input not found: ${label}');
    const bounds = input.getBoundingClientRect();
    return { x: bounds.x + bounds.width / 2, y: bounds.y + bounds.height / 2 };
  })()`);
  await cdp.call('Input.dispatchMouseEvent', { type: 'mouseMoved', x: rect.x, y: rect.y });
  await cdp.call('Input.dispatchMouseEvent', { type: 'mousePressed', x: rect.x, y: rect.y, button: 'left', clickCount: 1 });
  await cdp.call('Input.dispatchMouseEvent', { type: 'mouseReleased', x: rect.x, y: rect.y, button: 'left', clickCount: 1 });
  await cdp.call('Input.insertText', { text });
}

async function startBrowser() {
  const executable = browserExecutable(config.browser);
  const port = await freePort();
  const profile = await mkdtemp(path.join(os.tmpdir(), 'linli-wukong-web-ui-probe-'));
  const child = spawn(executable, [
    '--headless=new',
    '--disable-gpu',
    '--disable-background-networking',
    '--disable-component-update',
    '--no-first-run',
    '--no-default-browser-check',
    '--window-size=1280,800',
    `--remote-debugging-port=${port}`,
    `--user-data-dir=${profile}`,
    `${config.web}/`,
  ], { stdio: ['ignore', 'ignore', 'pipe'], windowsHide: true });
  let diagnostics = '';
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (chunk) => { diagnostics = `${diagnostics}${chunk}`.slice(-8000); });
  const page = await waitFor(async () => {
    if (child.exitCode !== null) throw new Error(`browser exited ${child.exitCode}: ${diagnostics}`);
    const response = await fetch(`http://127.0.0.1:${port}/json/list`);
    if (!response.ok) return null;
    const pages = await response.json();
    return pages.find((item) => item.type === 'page' && item.webSocketDebuggerUrl);
  }, 'browser DevTools endpoint');
  return {
    child,
    profile,
    page,
    async close() {
      if (child.exitCode === null) child.kill();
      await Promise.race([new Promise((resolve) => child.once('exit', resolve)), sleep(2000)]);
      const tempRoot = path.resolve(os.tmpdir());
      const resolved = path.resolve(profile);
      if (resolved.startsWith(`${tempRoot}${path.sep}`) && path.basename(resolved).startsWith('linli-wukong-web-ui-probe-')) {
        await rm(resolved, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 }).catch(() => {});
      }
    },
  };
}

const browser = await startBrowser();
const cdp = new CDPClient(browser.page.webSocketDebuggerUrl);
try {
  await cdp.open();
  const browserEvents = { exceptions: [], logs: [], requests: [], failures: [] };
  cdp.socket.addEventListener('message', (event) => {
    const message = JSON.parse(event.data);
    if (message.method === 'Runtime.exceptionThrown') {
      browserEvents.exceptions.push(message.params.exceptionDetails.exception?.description || message.params.exceptionDetails.text);
    } else if (message.method === 'Log.entryAdded') {
      browserEvents.logs.push({ level: message.params.entry.level, text: message.params.entry.text });
    } else if (message.method === 'Network.requestWillBeSent') {
      const url = message.params.request.url;
      if (url.includes('/v2/') || url.includes(':9000/')) {
        browserEvents.requests.push({ method: message.params.request.method, url });
      }
    } else if (message.method === 'Network.loadingFailed') {
      browserEvents.failures.push({
        errorText: message.params.errorText,
        canceled: message.params.canceled || false,
      });
    }
  });
  await Promise.all([
    cdp.call('Page.enable'),
    cdp.call('Runtime.enable'),
    cdp.call('Accessibility.enable'),
    cdp.call('DOM.enable'),
    cdp.call('Log.enable'),
    cdp.call('Network.enable'),
  ]);
  await waitFor(async () => (await cdp.evaluate('document.readyState')) !== 'loading', 'Flutter document');
  await sleep(4000);
  await cdp.evaluate(`(() => {
    const placeholder = document.querySelector('flt-semantics-placeholder');
    if (placeholder) placeholder.click();
    return Boolean(placeholder);
  })()`);
  await sleep(1000);
  await typeIntoInput(cdp, '手机号', config.phone);
  await typeIntoInput(cdp, '验证码', config.otp);
  await clickByName(cdp, '同意用户协议和隐私政策 我已阅读并同意 和', { role: 'checkbox', xOffset: 16 });
  await clickByName(cdp, '验证码登录', { role: 'button', occurrence: 0 });
  try {
    await waitFor(async () => {
      const ax = await cdp.call('Accessibility.getFullAXTree');
      return ax.nodes.some((node) => node.role?.value === 'textbox' && node.name?.value === '输入消息');
    }, 'authenticated Web workspace');
  } catch (error) {
    const ax = await cdp.call('Accessibility.getFullAXTree');
    const state = await cdp.evaluate(`({
      inputs: [...document.querySelectorAll('input')].map((input) => ({ label: input.getAttribute('aria-label'), value: input.value })),
      semantics: [...document.querySelectorAll('[aria-label]')].map((element) => ({ label: element.getAttribute('aria-label'), checked: element.getAttribute('aria-checked') })).filter((item) => item.label),
    })`);
    throw new Error(`${error.message}; state=${JSON.stringify(state)}; ax=${JSON.stringify(ax.nodes.map((node) => ({ role: node.role?.value || '', name: node.name?.value || '', checked: node.properties?.find((item) => item.name === 'checked')?.value?.value })).filter((node) => node.name).slice(0, 80))}`);
  }
  await waitFor(async () => {
    const ax = await cdp.call('Accessibility.getFullAXTree');
    return ax.nodes.some((node) => String(node.name?.value || '').includes('Bob'));
  }, 'Bob conversation');

  const apiLogin = await jsonRequest(`${config.api}/v2/auth/login`, {
    method: 'POST',
    platform: 'web',
    body: { phone: config.phone, code: config.otp },
  });
  const historyUrl = `${config.api}/v2/messages/conversations/${encodeURIComponent(config.conversationId)}/history?limit=100`;
  const before = await jsonRequest(historyUrl, { token: apiLogin.accessToken });
  const previousIds = new Set(before.items.map((item) => item.id));
  const fileName = `web-drop-${Date.now()}.png`;
  const pngBase64 = 'iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAARASURBVFhHvVdbbBRVGJ5gIdtFa7JTiSBWs5fO7gyXBmkrwQsmChISL23BKCBiykVIH0BIvcXEGoOSCGq5JFAeIKYPPOADptjSbSnw4IvwQEtoI7wooYTEmEi3BTr7mXOZmXPOzM6W0PDwdc6e/8z/fef///lPj6ZpGgimlEQxrawCkVgapbrlobwwIkXs4jqKWIZyEC6HVyN/HonEZMIJOg6F6IMLcDjIuCQSYwKmlJQiopuFdx0096CgIkwQbm3aYxWeOm6cFMIgP8Jvwkm4tcCcqy8GodgaVUDABgm3FkqsOpmoTbUXWqubggBxQaGxijBbMTvn1KT8B70YthN1XrWpflUwASFfgLDQqHoNq9Y2Yf2mZqzf3IzVH25Hde0ivPlqGuveSuODtwkyaHg9jUrD9Be26pMjVACx1bxch96zv8O2beTzeeTJ086z8X9DsLNx2NkEQw9/ZhPo/SmF6gUZ6iOsYXEBiggOsuPR0VFGRsn5U0TffOR7EhKYiDhGTiVQvyzN+kwAOS1CXwQoTCxY/AZyIzk/oQoqII58NsFARNCoMNzuiKNqXkZOhzv2CXCiYaKj8wwLNycaGcnhq12teGnpuzQttRw11RZqFmYoXlyUQctHBnK/MTFURHccv36X8qKgpCMgAiYqjBcwPj7OyZmIuve28JfmKHCceb9JIVIB3UzAvdMJzH6WREHg8teAhxX1jVK+Lw0MeoWkW5j+BBtHOciYzgmOB44m3TQQMcuX8I4r1YBFGpFIziLwzvtNUvhPdfW5Lx353MCf7SlK3PNjCqf3pij51fYkDn9quCSde1I8DSwdJCpypKUakL+EhjVbuQAmoit73nW8cWUa+3caNCJfbjDwRSMjPdBsoLEh7Qrt/iHlkhPULSUClBT4i5BNPr+kXsr/8PAtlM2cL4fPyT91JtfE409auPkLrwEqIE57QngEHGe6iekz5uLv68PS59Z68BiiUtF5eXR3xGti345KTs7w1/EkouVKL+Cc8mkoGHd8touRC7Xwx8V+7N57CF9/u89Fy5Z5aNlsUOxuMnChLSn1ARL+bWuCC5CAF6GyE93CozPmoit7zk2DBFeUDbuvirfhJIOQd4LOPZXuVxMEfysW1OlPP4c7d+5KUZCF3IPdawlnARPgiLjdkURsFvct7lyMgCTAFyITY2NjlGz45i30DwzhytA1DA5dQ//AILZ/8g2+32bg8rEkrvzMcOloCjdOsB7w78lEyDnA/AfXgLAolxvFwbZ2lD9TLe+gIOZAf8pC68cG/jmZ9Apc8TsxAeUWXlm+2jfnR3BLXlwrHEIqPAEB4Rcd+myivfi5EAi3DpwIqAseFmgjIv+W04kQxT7cz9oCoFe1NLuY+IwPA+RiUlZBrmbRAnkWMQk7duDm33IuqRq9KBYXMblwL6fu9Xwqu56X6hl58f0IK7ZW59fzqd71/H8CCp8bsxIu/wAAAABJRU5ErkJggg==';
  const dropDispatch = await cdp.evaluate(`(async () => {
    const bytes = Uint8Array.from(atob(${JSON.stringify(pngBase64)}), (character) => character.charCodeAt(0));
    const file = new File([bytes], ${JSON.stringify(fileName)}, { type: 'image/png' });
    const transfer = new DataTransfer();
    transfer.items.add(file);
    const prevented = {};
    for (const type of ['dragenter', 'dragover', 'drop']) {
      const event = new DragEvent(type, {
        bubbles: true,
        cancelable: true,
        dataTransfer: transfer,
        clientX: 640,
        clientY: 360,
      });
      document.body.dispatchEvent(event);
      prevented[type] = event.defaultPrevented;
      await new Promise((resolve) => setTimeout(resolve, 150));
    }
    return { files: transfer.files.length, name: transfer.files[0]?.name || '', prevented };
  })()`);
  if (dropDispatch.files !== 1 || dropDispatch.name !== fileName
      || !dropDispatch.prevented.dragenter || !dropDispatch.prevented.dragover || !dropDispatch.prevented.drop) {
    throw new Error(`browser did not construct the expected dropped file: ${JSON.stringify(dropDispatch)}`);
  }

  let droppedMessage;
  try {
    droppedMessage = await waitFor(async () => {
      const history = await jsonRequest(historyUrl, { token: apiLogin.accessToken });
      return history.items.find((item) =>
        !previousIds.has(item.id)
        && item.senderId === apiLogin.user.id
        && item.type === 'image'
        && item.body?.fileName === fileName
        && item.body?.mime === 'image/png'
        && item.body?.mediaId
      );
    }, 'dropped image in WuKong history');
  } catch (error) {
    const ax = await cdp.call('Accessibility.getFullAXTree');
    throw new Error(`${error.message}; drop=${JSON.stringify(dropDispatch)}; browser=${JSON.stringify(browserEvents)}; ax=${JSON.stringify(ax.nodes.map((node) => ({ role: node.role?.value || '', name: node.name?.value || '' })).filter((node) => node.name).slice(-100))}`);
  }
  let renderedMessage;
  try {
    renderedMessage = await waitFor(async () => {
      const ax = await cdp.call('Accessibility.getFullAXTree');
      const images = ax.nodes
        .filter((node) => node.role?.value === 'button' && String(node.name?.value || '').includes('图片消息'))
        .map((node) => String(node.name?.value || ''));
      const latest = images.at(-1) || '';
      return latest && !latest.includes('发送中') && !latest.includes('发送失败') && !latest.includes('加载失败')
        ? latest
        : null;
    }, 'rendered and acknowledged dropped image');
  } catch (error) {
    const ax = await cdp.call('Accessibility.getFullAXTree');
    const images = ax.nodes
      .map((node) => String(node.name?.value || ''))
      .filter((name) => name.includes('图片'));
    throw new Error(`${error.message}; images=${JSON.stringify(images)}; dropped=${JSON.stringify(droppedMessage)}; browser=${JSON.stringify(browserEvents)}`);
  }
  if (browserEvents.failures.some((failure) => !failure.canceled)) {
    throw new Error(`browser network failures: ${JSON.stringify(browserEvents.failures)}`);
  }
  const browserErrors = await cdp.call('Runtime.evaluate', {
    expression: 'globalThis.__linliWebProbeErrors || []',
    returnByValue: true,
  });
  console.log(JSON.stringify({
    pageTitle: await cdp.evaluate('document.title'),
    authenticated: true,
    bobConversationLoaded: true,
    dragEventsPrevented: dropDispatch.prevented,
    droppedFile: {
      name: fileName,
      mimeType: droppedMessage.body.mime,
      mediaId: droppedMessage.body.mediaId,
      messageId: droppedMessage.id,
      conversationSeq: droppedMessage.conversationSeq,
    },
    wuKongHistoryVerified: true,
    renderedMessage,
    browserErrors: browserErrors.result.value,
    browserEvents,
  }, null, 2));
} finally {
  cdp.close();
  await browser.close();
}
