/* Linli Web Push worker. It has a narrow scope so it can coexist with
   Flutter's generated offline-cache service worker. */
'use strict';

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

self.addEventListener('push', (event) => {
  let payload = {};
  try {
    payload = event.data ? event.data.json() : {};
  } catch (_) {
    payload = {};
  }
  const title = typeof payload.title === 'string' && payload.title
    ? payload.title
    : '邻里通讯';
  const body = typeof payload.body === 'string' && payload.body
    ? payload.body
    : '你有一条新通知';
  const data = payload.data && typeof payload.data === 'object'
    ? payload.data
    : {};
  event.waitUntil(self.registration.showNotification(title, {
    body,
    tag: typeof payload.tag === 'string' ? payload.tag : 'linli-notification',
    icon: typeof payload.icon === 'string' ? payload.icon : 'icons/Icon-192.png',
    badge: typeof payload.badge === 'string' ? payload.badge : 'icons/Icon-192.png',
    silent: payload.silent === true,
    vibrate: payload.vibrate === true ? [180, 80, 180] : undefined,
    data,
  }));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const payload = event.notification.data && typeof event.notification.data === 'object'
    ? event.notification.data
    : {};
  event.waitUntil((async () => {
    const appUrl = new URL('../', self.registration.scope);
    const conversationId = typeof payload.conversationId === 'string'
      ? payload.conversationId
      : '';
    if (conversationId) appUrl.searchParams.set('conversationId', conversationId);
    const windows = await self.clients.matchAll({
      type: 'window',
      includeUncontrolled: true,
    });
    for (const client of windows) {
      const clientUrl = new URL(client.url);
      if (clientUrl.origin === appUrl.origin && clientUrl.pathname.startsWith(appUrl.pathname)) {
        client.postMessage({type: 'linli.webpush.open', payload});
        return client.focus();
      }
    }
    return self.clients.openWindow(appUrl.href);
  })());
});
