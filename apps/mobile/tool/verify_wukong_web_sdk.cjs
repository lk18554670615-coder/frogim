'use strict';

const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');

const sdkPath = path.resolve(__dirname, '../web/wukongimjssdk-1.3.5.umd.js');
const bytes = fs.readFileSync(sdkPath);
assert.equal(
  crypto.createHash('sha256').update(bytes).digest('hex'),
  '47ae5759b21ec1ad67fb1ad7a63f4d6ddb0dfacd226a5fcb9104b99fcd763875',
  'bundled UMD file drifted from the frozen 1.3.5 artifact',
);

const wk = require(sdkPath);
const sdk = wk.WKSDK.shared();
assert.equal(sdk.config.sdkVersion, '1.3.5');
for (const [owner, methods] of [
  [sdk, ['connect', 'disconnect', 'register', 'newChannel', 'newMessageContent']],
  [sdk.chatManager, [
    'getSendPacketWithOptions',
    'sendSendPacket',
    'notifyMessageListeners',
    'addMessageListener',
    'addMessageStatusListener',
    'addCMDListener',
  ]],
  [sdk.conversationManager, ['addConversationListener']],
]) {
  for (const method of methods) assert.equal(typeof owner[method], 'function', method);
}

sdk.config.uid = 'contract-user';
const content = sdk.newMessageContent();
content.contentType = 1;
content.encodeJSON = () => ({type: 1, content: 'contract'});
const channel = sdk.newChannel('contract-channel', 2);
const options = new wk.SendOptions();
options.setting = new wk.Setting();
options.noPersist = true;
options.reddot = false;
const packet = sdk.chatManager.getSendPacketWithOptions(content, channel, options);
packet.clientMsgNo = 'stable-client-message-id';
packet.noPersist = true;
packet.reddot = false;
packet.syncOnce = true;
packet.expire = 60;
packet.setting.topic = true;
packet.topic = 'community@topic';

assert.equal(packet.clientMsgNo, 'stable-client-message-id');
assert.equal(packet.channelID, 'contract-channel');
assert.equal(packet.channelType, 2);
assert.equal(packet.noPersist, true);
assert.equal(packet.reddot, false);
assert.equal(packet.syncOnce, true);
assert.equal(packet.expire, 60);
assert.equal(packet.setting.topic, true);
assert.equal(packet.topic, 'community@topic');
const message = wk.Message.fromSendPacket(packet, content);
assert.equal(message.clientMsgNo, packet.clientMsgNo);
assert.equal(message.clientSeq, packet.clientSeq);

process.stdout.write('WuKongIM JS SDK 1.3.5 contract verified\n');
process.exit(0);
