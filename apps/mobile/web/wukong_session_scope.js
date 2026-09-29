// Pinned WuKongIM JS SDK 1.3.5. Its shared managers retain account data after
// disconnect(), so closing a transport alone is not a tenant boundary.
(function (root) {
  "use strict";
  function reset(wk, sdk) {
    if (!sdk || wk.WKSDK.instance !== sdk) return false;
    sdk.disconnect(); // destroys the old socket and disables reconnection
    if (sdk.receiptManager.timer) clearInterval(sdk.receiptManager.timer);
    if (sdk.channelManager.subscriberContextTick) clearInterval(sdk.channelManager.subscriberContextTick);
    for (const task of sdk.taskManager.taskMap.values()) task.cancel();
    sdk.taskManager.taskMap.clear();
    for (const name of ["connectManager", "chatManager", "channelManager",
      "conversationManager", "securityManager", "reminderManager",
      "receiptManager", "eventManager", "messageContentManager"]) {
      const manager = sdk[name];
      if (manager && manager.constructor.instance === manager) {
        manager.constructor.instance = undefined;
      }
    }
    // New managers receive new provider callbacks/listeners, queues and caches.
    // Late provider results are rejected by the disposed Dart gateway as well.
    wk.WKSDK.instance = undefined;
    return true;
  }
  if (typeof module !== "undefined" && module.exports) module.exports = reset;
  root.frogimResetWukongSession = function (sdk) { return reset(root.wk, sdk); };
})(globalThis);
