'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const wk = require('../web/wukongimjssdk-1.3.5.umd.js');
const reset = require('../web/wukong_session_scope.js');

test('pinned SDK discards account managers, queued messages, receipts and listeners', () => {
  const previous = wk.WKSDK.shared();
  let current;
  let cancelled = 0;
  let notified = 0;
  try {
    previous.config.uid = 'same-local-id';
    previous.config.token = 'old-tenant-token';
    previous.channelManager.channelInfocacheMap['same-channel'] = {title: 'old tenant'};
    previous.channelManager.subscriberContexts['same-channel'] = {members: ['old member']};
    previous.chatManager.sendingQueues.set('pending-old', {clientMsgNo: 'pending-old'});
    previous.chatManager.sendPacketQueue.push({channelID: 'same-channel'});
    previous.conversationManager.conversations.push({channelID: 'same-channel'});
    previous.reminderManager.reminders.push({text: 'old reminder'});
    previous.receiptManager.channelMessagesMap.set('same-channel', [{messageID: 'old'}]);
    previous.chatManager.addMessageListener(() => { notified++; });
    previous.taskManager.taskMap.set('old-upload', {cancel() { cancelled++; }});
    const timer = previous.receiptManager.timer;
    assert.equal(reset(wk, previous), true);
    assert.equal(cancelled, 1);
    assert.equal(timer._destroyed, true, 'receipt polling must stop');
    current = wk.WKSDK.shared();
    assert.notEqual(current, previous);
    for (const name of ['chatManager', 'channelManager', 'conversationManager',
      'reminderManager', 'receiptManager', 'eventManager', 'taskManager',
      'connectManager', 'securityManager', 'messageContentManager']) {
      assert.notEqual(current[name], previous[name], name);
    }
    assert.notEqual(current.config.token, 'old-tenant-token');
    assert.deepEqual(current.channelManager.channelInfocacheMap, {});
    assert.deepEqual(current.channelManager.subscriberContexts, []);
    assert.equal(current.chatManager.sendingQueues.size, 0);
    assert.deepEqual(current.chatManager.sendPacketQueue, []);
    assert.deepEqual(current.conversationManager.conversations, []);
    assert.deepEqual(current.reminderManager.reminders, []);
    assert.equal(current.receiptManager.channelMessagesMap.size, 0);
    current.conversationManager.addNoUpdateContentType(1);
    const message = new wk.Message();
    message.content = current.newMessageContent();
    message.content.contentType = 1;
    current.chatManager.notifyMessageListeners(message);
    assert.equal(notified, 0, 'old account listeners must not receive new messages');
    assert.equal(reset(wk, previous), false, 'late disposal must not clear the new session');
    assert.equal(wk.WKSDK.shared(), current);
  } finally {
    reset(wk, current ?? previous);
  }
});
