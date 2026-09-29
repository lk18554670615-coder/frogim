/* Platform Web Push. Never shares state/scope with legacy or asset workers. */
'use strict';
const appUrl = new URL('../', self.registration.scope);
const stateUrl = new URL('binding', self.registration.scope).href;
const cacheName = 'linli-tenant-push-v1';
const identityKeys = ['tenantId', 'localUserId', 'assignmentVersion', 'authVersion', 'realmVersion'];
let queue = Promise.resolve();
function serialize(work) { const next = queue.then(work); queue = next.catch(() => {}); return next; }
function validIdentity(value) {
  return value && ['tenantId', 'localUserId'].every(k => typeof value[k] === 'string' && /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$/.test(value[k])) &&
    ['assignmentVersion', 'authVersion', 'realmVersion'].every(k => Number.isSafeInteger(value[k]) && value[k] > 0);
}
function sameIdentity(a, b) { return validIdentity(a) && validIdentity(b) && identityKeys.every(k => a[k] === b[k]); }
function future(value) { return typeof value === 'string' && Number.isFinite(Date.parse(value)) && Date.parse(value) > Date.now(); }
function validBinding(value) {
  return validIdentity(value) && typeof value.pushBindingId === 'string' && /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$/.test(value.pushBindingId) &&
    Number.isSafeInteger(value.pushBindingRevision) && value.pushBindingRevision > 0 && future(value.leaseExpiresAt);
}
function matches(state, data) {
  return state && state.active && validBinding(state.binding) && sameIdentity(state.binding, data) &&
    data.pushBindingId === state.binding.pushBindingId && data.pushBindingRevision === state.binding.pushBindingRevision && future(data.expiresAt);
}
async function read(url = stateUrl) {
  const found = await (await caches.open(cacheName)).match(url);
  if (!found) return null;
  try { return await found.json(); } catch (_) { return null; }
}
async function write(url, data) {
  await (await caches.open(cacheName)).put(url, new Response(JSON.stringify(data), {headers: {'Content-Type': 'application/json'}}));
}
async function closeNotifications() { for (const n of await self.registration.getNotifications()) n.close(); }
function appClient(client) {
  if (!client || typeof client.url !== 'string') return false;
  const url = new URL(client.url);
  return url.origin === appUrl.origin && url.pathname.startsWith(appUrl.pathname);
}
async function command(event) {
  const request = event.data;
  if (!request || !event.source || !appClient(await self.clients.get(event.source.id))) return {ok: false};
  const state = await read();
  const sessionKey = request.sessionKey;
  const validKey = typeof sessionKey === 'string' && /^[a-zA-Z0-9:_-]{8,160}$/.test(sessionKey);
  switch (request.type) {
    case 'linli.webpush.begin': {
      if (!validKey || !validIdentity(request.context)) return {ok: false};
      const context = Object.fromEntries(identityKeys.map(k => [k, request.context[k]]));
      await write(stateUrl, {sessionKey, clientId: event.source.id, binding: context, active: false});
      await closeNotifications();
      return {ok: true};
    }
    case 'linli.webpush.commit': {
      if (!state || state.sessionKey !== sessionKey || state.clientId !== event.source.id || !validBinding(request.binding) || !sameIdentity(state.binding, request.binding)) return {ok: false};
      const keys = [...identityKeys, 'pushBindingId', 'pushBindingRevision', 'leaseExpiresAt'];
      const binding = Object.fromEntries(keys.map(k => [k, request.binding[k]]));
      await write(stateUrl, {sessionKey, clientId: state.clientId, binding, active: true});
      return {ok: true};
    }
    case 'linli.webpush.clear': {
      if (state && validKey && state.sessionKey === sessionKey && state.clientId === event.source.id) {
        await (await caches.open(cacheName)).delete(stateUrl);
        await closeNotifications();
      }
      return {ok: true};
    }
    case 'linli.webpush.take': {
      if (typeof request.id !== 'string' || !/^[a-f0-9-]{36}$/.test(request.id)) return {ok: false};
      const url = new URL('navigation/' + request.id, self.registration.scope).href;
      const handoff = await read(url);
      await (await caches.open(cacheName)).delete(url);
      // Reopening the app may create a new session/binding revision. The click
      // was verified against the old binding; delivery into the app additionally
      // requires the newly authenticated identity, never a guessed tenant.
      const payload = handoff && handoff.payload;
      const allowed = state && state.active && validBinding(state.binding) &&
        state.clientId === event.source.id && sameIdentity(state.binding, request.context) &&
        sameIdentity(request.context, payload) && future(handoff && handoff.expiresAt) &&
        matches({active: true, binding: handoff && handoff.binding}, payload);
      return {ok: true, payload: allowed ? payload : null};
    }
    default: return {ok: false};
  }
}
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', event => event.waitUntil(self.clients.claim()));
self.addEventListener('message', event => {
  event.waitUntil(serialize(async () => {
    let response;
    try { response = await command(event); } catch (_) { response = {ok: false}; }
    if (event.ports && event.ports[0]) event.ports[0].postMessage(response);
  }));
});
self.addEventListener('push', event => {
  event.waitUntil(serialize(async () => {
    let payload;
    try { payload = event.data ? event.data.json() : {}; } catch (_) { return; }
    const data = payload && payload.data && typeof payload.data === 'object' ? payload.data : {};
    if (!matches(await read(), data)) return;
    await self.registration.showNotification(typeof payload.title === 'string' && payload.title ? payload.title : '青蛙呱呱', {
      body: typeof payload.body === 'string' && payload.body ? payload.body : '你有一条新通知',
      tag: typeof payload.tag === 'string' ? payload.tag : 'linli-notification',
      icon: new URL('icons/Icon-192.png', appUrl).href, badge: new URL('icons/Icon-192.png', appUrl).href,
      silent: payload.silent === true, vibrate: payload.vibrate === true ? [180, 80, 180] : undefined, data,
    });
  }).catch(() => {}));
});
self.addEventListener('notificationclick', event => {
  event.notification.close();
  event.waitUntil(serialize(async () => {
    const payload = event.notification.data || {};
    const state = await read();
    if (!matches(state, payload)) return;
    const windows = await self.clients.matchAll({type: 'window', includeUncontrolled: true});
    for (const client of windows) {
      if (client.id === state.clientId && appClient(client)) {
        client.postMessage({type: 'linli.webpush.open', payload});
        return client.focus();
      }
    }
    const id = crypto.randomUUID();
    const cache = await caches.open(cacheName);
    for (const entry of await cache.keys()) {
      if (entry.url.startsWith(new URL('navigation/', self.registration.scope).href)) await cache.delete(entry);
    }
    await write(new URL('navigation/' + id, self.registration.scope).href, {
      payload, binding: state.binding, expiresAt: new Date(Date.now() + 5 * 60 * 1000).toISOString(),
    });
    const target = new URL(appUrl);
    target.searchParams.set('tenantPush', id);
    return self.clients.openWindow(target.href);
  }).catch(() => {}));
});
