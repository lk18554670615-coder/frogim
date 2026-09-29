'use strict';
// Isolated worker protocol tests: no browser permissions, live endpoints or keys.
const {readFileSync} = require('node:fs');
const {resolve} = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const {test} = require('node:test');
const {webcrypto} = require('node:crypto');
const source = readFileSync(resolve(__dirname, '../web/tenant_push_worker.js'), 'utf8');
const scope = 'https://platform.example/app/tenant-push-scope/';
const identity = {tenantId: 'default', localUserId: 'local-one', assignmentVersion: 1, authVersion: 2, realmVersion: 3};
const binding = {...identity, pushBindingId: 'binding-one', pushBindingRevision: 1, leaseExpiresAt: new Date(Date.now()+3600000).toISOString()};
const payload = {...binding, expiresAt: new Date(Date.now()+600000).toISOString(), conversationId: 'private-conversation'};
function fixture(storage = new Map()) {
  const listeners = {}, notifications = [], opened = [], messages = [];
  let broken = false;
  const windows = new Map();
  const addClient = (id, url = 'https://platform.example/app/') => windows.set(id, {id, url, postMessage: value => messages.push({id, value}), focus: async () => id});
  addClient('tab-a'); addClient('tab-b');
  const url = value => typeof value === 'string' ? value : value.url;
  const cache = {
    match: async value => storage.has(url(value)) ? new Response(storage.get(url(value))) : undefined,
    put: async (key, response) => storage.set(url(key), await response.text()),
    delete: async value => storage.delete(url(value)),
    keys: async () => [...storage.keys()].map(key => ({url:key})),
  };
  const context = {URL, Response, Date, crypto: webcrypto, caches: {open: async () => { if(broken) throw Error('storage unavailable'); return cache; }},
    self: {registration: {scope, getNotifications: async () => notifications.filter(n => !n.closed), showNotification: async (title, options) => notifications.push({title, ...options, closed:false, close(){this.closed=true;}})},
      clients: {get: async id => windows.get(id), matchAll: async () => [...windows.values()], openWindow: async target => opened.push(target), claim: async () => {}}, skipWaiting: async () => {}, addEventListener: (name, listener) => listeners[name] = listener}};
  vm.runInNewContext(source, context);
  async function dispatch(type, args) { let wait; listeners[type]({...args, waitUntil: p => wait=p}); await wait; }
  async function command(data, id = 'tab-a') { let result; await dispatch('message', {data, source:{id}, ports:[{postMessage:r=>result=JSON.parse(JSON.stringify(r))}]}); return result; }
  const begin = (key='session-a', ctx=identity, id='tab-a') => command({type:'linli.webpush.begin', sessionKey:key, context:ctx},id);
  const commit = (key='session-a', value=binding, id='tab-a') => command({type:'linli.webpush.commit', sessionKey:key, binding:value},id);
  const activate = async () => { assert.equal((await begin()).ok,true); assert.equal((await commit()).ok,true); };
  const push = (data=payload, extra={}) => dispatch('push',{data:{json:()=>({title:'New message',body:'Generic',data,...extra})}});
  return {storage, windows, addClient, notifications, opened, messages, command, begin, commit, activate, push, broken:()=>broken=true,
    click: n => dispatch('notificationclick',{notification:n}),
    take: (id,ctx=identity,client='tab-a') => command({type:'linli.webpush.take',id,context:ctx},client)};
}
test('nothing shown without a confirmed binding; pending fences previous notifications', async () => {
  const f=fixture(); await f.push(); assert.equal(f.notifications.length,0);
  await f.begin(); await f.push(); assert.equal(f.notifications.length,0);
  await f.commit(); await f.push(); assert.equal(f.notifications.length,1);
  await f.begin('session-b',identity,'tab-b'); assert.equal(f.notifications[0].closed,true);
  assert.equal((await f.commit()).ok,false); await f.push(); assert.equal(f.notifications.length,1);
});
test('binding survives worker restart and validates all identity/lease fields', async () => {
  const f=fixture(); await f.activate(); const next=fixture(f.storage);
  await next.push(payload,{icon:'https://attacker.test/track'});
  assert.equal(next.notifications.length,1); assert.equal(next.notifications[0].icon,'https://platform.example/app/icons/Icon-192.png');
  for(const key of Object.keys(identity)) {
    await next.push({...payload,[key]:typeof payload[key]==='number'?payload[key]+1:'another'});
    const missing={...payload};delete missing[key];await next.push(missing);
  }
  await next.push({...payload,pushBindingId:'other'}); await next.push({...payload,pushBindingRevision:2});
  await next.push({...payload,expiresAt:'2000-01-01T00:00:00Z'});
  assert.equal(next.notifications.length,1);
  assert.equal((await next.commit('session-a',{...binding,leaseExpiresAt:'2000-01-01T00:00:00Z'})).ok,false);
});
test('old clear and foreign tab commit cannot overwrite current owner',async()=>{
  const f=fixture();await f.activate();await f.begin('session-b',identity,'tab-b');
  assert.equal((await f.commit('session-b',binding,'tab-a')).ok,false);
  assert.equal((await f.commit('session-b',binding,'tab-b')).ok,true);
  await f.command({type:'linli.webpush.clear',sessionKey:'session-a'});await f.push();assert.equal(f.notifications.length,1);
  await f.command({type:'linli.webpush.clear',sessionKey:'session-b'},'tab-b');await f.push();assert.equal(f.notifications.length,1);assert.equal(f.notifications[0].closed,true);
});
test('invalid source and inaccessible storage fail closed',async()=>{
  const f=fixture();await f.activate();f.addClient('foreign','https://evil.test/app/');
  assert.equal((await f.begin('malicious',identity,'foreign')).ok,false);
  f.addClient('prefix','https://platform.example/application/');assert.equal((await f.begin('malicious',identity,'prefix')).ok,false);
  f.broken();await f.push();assert.equal(f.notifications.length,0);assert.equal((await f.begin()).ok,false);
});
test('click targets only the registered owner, not the first same-origin account tab',async()=>{
  const f=fixture();await f.begin('session-b',identity,'tab-b');await f.commit('session-b',binding,'tab-b');await f.push();
  await f.click(f.notifications[0]);assert.equal(f.messages.length,1);assert.equal(f.messages[0].id,'tab-b');assert.equal(f.opened.length,0);
});
test('closed owner opens opaque one-time handoff; fresh binding revision may consume it',async()=>{
  const f=fixture();await f.activate();await f.push();f.windows.delete('tab-a');await f.click(f.notifications[0]);
  assert.equal(f.messages.length,0);assert.equal(f.opened.length,1);
  const target=new URL(f.opened[0]);assert.deepEqual([...target.searchParams.keys()],['tenantPush']);
  assert.equal(target.href.includes('private-conversation'),false);assert.equal(target.href.includes('local-one'),false);
  const id=target.searchParams.get('tenantPush');f.addClient('new');await f.begin('session-new',identity,'new');
  await f.commit('session-new',{...binding,pushBindingRevision:2},'new');
  assert.deepEqual((await f.take(id,identity,'new')).payload,payload);assert.equal((await f.take(id,identity,'new')).payload,null);
});
test('handoff cannot be consumed by another account or after binding revocation',async()=>{
  const f=fixture();await f.activate();await f.push();f.windows.delete('tab-a');await f.click(f.notifications[0]);
  const id=new URL(f.opened[0]).searchParams.get('tenantPush');const other={...identity,localUserId:'other'};
  await f.begin('session-b',other,'tab-b');await f.commit('session-b',{...binding,...other},'tab-b');
  assert.equal((await f.take(id,other,'tab-b')).payload,null);
  await f.click(f.notifications[0]);assert.equal(f.opened.length,1);
});
test('serialized begin/clear race leaves new binding intact',async()=>{
  const f=fixture();await f.activate();await Promise.all([f.begin('session-b',identity,'tab-b'),f.command({type:'linli.webpush.clear',sessionKey:'session-a'})]);
  assert.equal((await f.commit('session-b',binding,'tab-b')).ok,true);await f.push();assert.equal(f.notifications.length,1);
});
test('persisted lease expiry and unsafe identity prevent display',async()=>{
  const f=fixture();await f.activate();
  const saved=JSON.parse(f.storage.get(scope+'binding'));saved.binding.leaseExpiresAt='2000-01-01T00:00:00Z';
  f.storage.set(scope+'binding',JSON.stringify(saved));await f.push();assert.equal(f.notifications.length,0);
  assert.equal((await f.begin('session-a',{...identity,assignmentVersion:Number.MAX_SAFE_INTEGER+1})).ok,false);
  assert.equal((await f.begin('session-a',{...identity,tenantId:'../other'})).ok,false);
});
test('navigation storage is bounded and expired handoff is deleted without navigation',async()=>{
  const f=fixture();await f.activate();await f.push();f.windows.delete('tab-a');
  await f.click(f.notifications[0]);await f.click(f.notifications[0]);
  const entries=[...f.storage.keys()].filter(k=>k.includes('/navigation/'));assert.equal(entries.length,1);
  const stored=JSON.parse(f.storage.get(entries[0]));stored.expiresAt='2000-01-01T00:00:00Z';f.storage.set(entries[0],JSON.stringify(stored));
  await f.begin('session-b',identity,'tab-b');await f.commit('session-b',binding,'tab-b');
  assert.equal((await f.take(entries[0].split('/').pop(),identity,'tab-b')).payload,null);
  assert.equal(f.storage.has(entries[0]),false);
});
