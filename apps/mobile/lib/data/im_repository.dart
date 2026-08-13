import '../core/models.dart';

abstract interface class ImRepository {
  Stream<bool> get connectionChanges;
  Stream<ImEvent> get events;
  bool get isDemo;
  bool get supportsDemo;
  AppUser? get currentUser;

  Future<bool> restoreSession();
  Future<String?> requestCode(String phone);
  Future<void> enterDemo();
  Future<AppUser> login(String phone, String code);
  Future<AppUser> passwordLogin(String phone, String password);
  Future<AppUser> register({
    required String phone,
    required String code,
    required String password,
    required String name,
  });
  Future<void> requestPasswordResetCode(String phone);
  Future<void> resetPassword({
    required String phone,
    required String code,
    required String password,
  });
  Future<void> logout();
  Future<AppUser> profile();
  Future<AppUser> updateProfile({
    String? name,
    String? handle,
    String? signature,
    String? avatarMediaId,
  });
  Future<String> uploadAvatar(MediaUpload upload);
  Future<void> requestPhoneChangeCode(String phone);
  Future<AppUser> updatePhone(String phone, String code);
  Future<void> requestAccountDeletionCode();
  Future<void> deleteAccount(String code);
  Future<List<UserDevice>> userDevices();
  Future<List<ImDeviceSession>> imDeviceSessions();
  Future<void> registerDevice({
    required String deviceId,
    required String platform,
    required String provider,
    required String pushToken,
    required bool notificationsEnabled,
    required bool previewEnabled,
    required bool soundEnabled,
    required bool vibrationEnabled,
  });
  Future<void> removeUserDevice(String deviceId);
  Future<void> quitImDeviceSession(int deviceFlag);
  Future<List<ChatMessage>> favorites();
  Future<void> submitFeedback({
    required String category,
    required String content,
    String contact,
  });
  Future<List<AppAnnouncement>> announcements();
  Future<void> markAnnouncementRead(String announcementId);
  Future<List<Conversation>> conversations();
  Future<List<AppUser>> contacts();
  Future<List<AppUser>> searchUsers(String query, {String by = 'handle'});
  Future<UserSearchCapabilities> searchCapabilities();
  Future<List<FriendRequest>> friendRequests();
  Future<void> sendFriendRequest(
    String userId,
    String note, {
    String source = 'search',
    String? sourceId,
  });
  Future<void> acceptFriendRequest(String requestId);
  Future<void> rejectFriendRequest(String requestId);
  Future<void> cancelFriendRequest(String requestId);
  Future<void> updateFriendMetadata(
    String userId, {
    required String remark,
    required List<String> tags,
  });
  Future<void> deleteFriend(String userId);
  Future<void> blockUser(String userId, bool blocked);
  Future<List<AppUser>> blockedUsers();
  Future<void> report({
    required String targetType,
    required String targetId,
    required String reason,
    String details,
  });
  Future<Conversation> createDirect(AppUser user);
  Future<Conversation> createGroup(String name, List<AppUser> members);
  Future<GroupProfile> groupProfile(String conversationId);
  Future<GroupProfile> updateGroupProfile(
    String conversationId, {
    String? name,
    String? avatarMediaId,
    String? joinPolicy,
    bool? allowMemberAddFriend,
    bool rotateQr = false,
  });
  Future<GroupProfile> setGroupAnnouncement(
    String conversationId,
    String content,
  );
  Future<void> markGroupAnnouncementRead(String conversationId);
  Future<List<GroupMember>> groupMembers(String conversationId);
  Future<void> addGroupMembers(String conversationId, List<String> userIds);
  Future<void> inviteGroupMember(String conversationId, String userId);
  Future<List<GroupInvitation>> groupInvitations();
  Future<void> respondGroupInvitation(String invitationId, String action);
  Future<void> joinGroupByQr(String token);
  Future<void> removeGroupMember(String conversationId, String userId);
  Future<void> setGroupRole(String conversationId, String userId, String role);
  Future<void> setGroupMemberMuted(
    String conversationId,
    String userId,
    DateTime? until,
  );
  Future<void> transferGroupOwner(String conversationId, String userId);
  Future<void> setGroupNickname(String conversationId, String nickname);
  Future<GroupProfile> setGroupAllMuted(String conversationId, bool muted);
  Future<void> leaveGroup(String conversationId);
  Future<void> disbandGroup(String conversationId, String reason);
  Future<List<ChatMessage>> messages(String conversationId);
  Future<ChatMessage> send(ChatMessage pending);
  Future<ChatMessage> sendMedia(
    ChatMessage pending,
    MediaUpload upload, {
    void Function(double progress)? onProgress,
  });
  Future<ChatMessage> editMessage(String messageId, String text);
  Future<List<MessageEditRevision>> messageEditHistory(String messageId);
  Future<ChatMessage> setMessageReaction(
    String messageId,
    String emoji, {
    required bool active,
  });
  Future<List<ChatMessage>> pinnedMessages(String conversationId);
  Future<void> setMessagePinned(
    String conversationId,
    String messageId, {
    required bool pinned,
  });
  Future<List<ChatMessage>> searchMessages(
    String conversationId,
    String query, {
    int limit = 50,
  });
  Future<List<ChatMessage>> forwardMessages(
    String targetConversationId,
    List<String> sourceMessageIds, {
    required String mode,
    required String clientBatchId,
  });
  Future<void> saveFavorite(ChatMessage message);
  Future<void> removeFavorite(ChatMessage message);
  Future<void> markRead(String conversationId, int sequence);
  Future<void> markDelivered(String conversationId, int sequence);
  Future<void> setTyping(String conversationId, bool typing);
  Future<void> updateConversationPreferences(
    String conversationId, {
    bool? pinned,
    bool? notificationsMuted,
    bool? manualUnread,
    bool? archived,
  });
  Future<List<ScheduledMessage>> scheduledMessages(String conversationId);
  Future<ScheduledMessage> scheduleMessage(
    String conversationId,
    String text,
    DateTime scheduledAt, {
    String? replyToId,
    int? expiresInSeconds,
  });
  Future<ScheduledMessage> updateScheduledMessage(
    String scheduledMessageId, {
    required String text,
    required DateTime scheduledAt,
    int? expiresInSeconds,
  });
  Future<void> cancelScheduledMessage(String scheduledMessageId);
  Future<LinkPreview?> linkPreview(String url);
  Future<void> hideConversation(String conversationId);
  Future<void> recallMessage(String messageId);
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  );
  Future<String> readDraft(String conversationId);
  Future<void> saveDraft(String conversationId, String text);
  Future<void> syncNow();
  Future<void> connect();
  Future<void> close();
}
