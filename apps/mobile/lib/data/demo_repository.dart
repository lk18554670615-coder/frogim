import 'dart:async';

import '../core/models.dart';
import 'im_repository.dart';
import 'secure_local_store.dart';

class DemoImRepository implements ImRepository {
  DemoImRepository({
    this.latency = const Duration(milliseconds: 180),
    SecureLocalStore? store,
  }) : _store = store ?? SecureLocalStore();

  final Duration latency;
  final SecureLocalStore _store;
  final _connection = StreamController<bool>.broadcast();
  final _events = StreamController<ImEvent>.broadcast();
  final Map<String, List<ChatMessage>> _cachedMessages = {};
  final Map<String, List<ScheduledMessage>> _scheduledMessages = {};
  final Map<String, GroupProfile> _groupProfiles = {};
  final Map<String, List<GroupMember>> _groupMemberState = {};
  final List<AppAnnouncement> _announcements = [
    AppAnnouncement(
      id: 'announcement-demo-1',
      title: '社区沟通公约',
      content: '请保护个人隐私，友好交流。遇到违规内容可在资料页提交举报。',
      status: 'published',
      pinned: true,
      publishedAt: DateTime(2026, 7, 31, 9),
    ),
  ];
  AppUser _profile = demoUser;
  late final List<Conversation> _conversations = _seedConversations();
  late final List<FriendRequest> _requests = [
    const FriendRequest(
      id: 'fr-chenche',
      user: AppUser(
        id: 'u6',
        name: '陈澈',
        handle: 'chenche',
        presence: '通过群聊「周末咖啡局」添加',
      ),
      note: '嗨，我们上周见过',
    ),
    const FriendRequest(
      id: 'fr-chuqing',
      user: AppUser(
        id: 'u7',
        name: '初晴',
        handle: 'chuqing',
        presence: '通过邻里号搜索添加',
      ),
      note: '想和你交流产品设计',
    ),
  ];
  late final List<GroupInvitation> _groupInvitations = [
    GroupInvitation(
      id: 'ginv-demo-1',
      conversationId: 'c-coffee',
      groupName: '周末咖啡局',
      inviter: people.first,
      status: 'pending',
      createdAt: DateTime.now().subtract(const Duration(minutes: 18)),
      expiresAt: DateTime.now().add(const Duration(hours: 20)),
    ),
  ];

  static const demoUser = AppUser(
    id: 'me',
    name: '许言',
    handle: 'xuyan_27',
    presence: '保持真诚，也保持好奇',
    phone: '13800138000',
    signature: '保持真诚，也保持好奇',
    isOnline: true,
    handleChangeCount: 1,
    handleChangesRemaining: 1,
    allowSearchByHandle: true,
    allowSearchByPhone: false,
  );

  static const people = [
    AppUser(
      id: 'u1',
      name: '林屿',
      handle: 'linyu',
      presence: '在设计新的社区空间',
      isOnline: true,
    ),
    AppUser(
      id: 'u2',
      name: '安然',
      handle: 'anran',
      presence: '今天也要保持好奇',
      avatarUrl: 'assets/avatars/an-ran.png',
      isOnline: true,
    ),
    AppUser(id: 'u3', name: '周末', handle: 'weekend', presence: '专注中，稍后回复'),
    AppUser(
      id: 'u4',
      name: '顾言',
      handle: 'guyan',
      presence: '去生活，去感受',
      isOnline: true,
    ),
    AppUser(id: 'u5', name: '南星', handle: 'nanxing', presence: '正在路上'),
    AppUser(
      id: 'u8',
      name: '李想',
      handle: 'lixiang',
      presence: '正在整理设计评审结论',
      avatarUrl: 'assets/avatars/li-xiang.png',
      isOnline: true,
    ),
  ];

  @override
  bool get isDemo => true;

  @override
  bool get supportsDemo => true;

  @override
  AppUser? get currentUser => _profile;

  @override
  Stream<bool> get connectionChanges => _connection.stream;

  @override
  Stream<ImEvent> get events => _events.stream;

  @override
  Future<void> enterDemo() async {}

  @override
  Future<bool> restoreSession() async => false;

  @override
  Future<String?> requestCode(String phone) async {
    await Future<void>.delayed(latency);
    if (phone.trim().length < 5) throw const FormatException('请输入有效手机号');
    return '123456';
  }

  @override
  Future<AppUser> login(String phone, String code) async {
    await Future<void>.delayed(latency);
    if (phone.length < 5 || code != '123456') {
      throw const FormatException('请输入有效手机号和验证码');
    }
    return _profile;
  }

  @override
  Future<AppUser> passwordLogin(String phone, String password) async {
    await Future<void>.delayed(latency);
    if (phone.trim().isEmpty || password != 'StrongPass123!') {
      throw const FormatException('手机号或密码错误');
    }
    return _profile;
  }

  @override
  Future<AppUser> register({
    required String phone,
    required String code,
    required String password,
    required String name,
  }) async {
    if (code != '123456' || password.length < 8 || name.trim().isEmpty) {
      throw const FormatException('注册信息无效');
    }
    _profile = _profile.copyWith(name: name.trim(), phone: phone.trim());
    return _profile;
  }

  @override
  Future<void> requestPasswordResetCode(String phone) async {
    await requestCode(phone);
  }

  @override
  Future<void> resetPassword({
    required String phone,
    required String code,
    required String password,
  }) async {
    if (code != '123456' || password.length < 8) {
      throw const FormatException('重置密码信息无效');
    }
  }

  @override
  Future<AppUser> profile() async => _profile;

  @override
  Future<AppUser> updateProfile({
    String? name,
    String? handle,
    String? signature,
    String? avatarMediaId,
  }) async {
    await Future<void>.delayed(latency);
    _profile = _profile.copyWith(
      name: name,
      handle: handle,
      signature: signature,
      presence: signature,
      avatarMediaId: avatarMediaId,
    );
    return _profile;
  }

  @override
  Future<String> uploadAvatar(MediaUpload upload) async {
    await Future<void>.delayed(latency);
    return 'demo-avatar-${DateTime.now().microsecondsSinceEpoch}';
  }

  @override
  Future<void> requestPhoneChangeCode(String phone) async {
    await requestCode(phone);
  }

  @override
  Future<AppUser> updatePhone(String phone, String code) async {
    if (code != '123456') throw const FormatException('验证码错误');
    _profile = _profile.copyWith(phone: phone);
    return _profile;
  }

  @override
  Future<void> requestAccountDeletionCode() async {
    await Future<void>.delayed(latency);
  }

  @override
  Future<void> deleteAccount(String code) async {
    await Future<void>.delayed(latency);
    if (code != '123456') throw const FormatException('验证码错误');
  }

  @override
  Future<List<UserDevice>> userDevices() async => [
    UserDevice(
      id: 'demo-device',
      platform: 'ios',
      provider: 'demo',
      updatedAt: DateTime.now(),
    ),
  ];

  @override
  Future<void> registerDevice({
    required String deviceId,
    required String platform,
    required String provider,
    required String pushToken,
    required bool notificationsEnabled,
    required bool previewEnabled,
    required bool soundEnabled,
    required bool vibrationEnabled,
  }) async {}

  @override
  Future<void> removeUserDevice(String deviceId) async {}

  @override
  Future<List<ChatMessage>> favorites() async {
    final stored = await _store.readJson('favorites');
    if (stored is! List<Object?>) return const [];
    return stored
        .whereType<Map<String, Object?>>()
        .map(ChatMessage.fromJson)
        .toList();
  }

  @override
  Future<void> submitFeedback({
    required String category,
    required String content,
    String contact = '',
  }) async {
    await _store.writeJson('feedback.latest', {
      'category': category,
      'content': content,
      'contact': contact,
    });
  }

  @override
  Future<List<AppAnnouncement>> announcements() async {
    await Future<void>.delayed(latency);
    return List.of(_announcements);
  }

  @override
  Future<void> markAnnouncementRead(String announcementId) async {
    await Future<void>.delayed(latency);
    final index = _announcements.indexWhere(
      (item) => item.id == announcementId,
    );
    if (index >= 0) {
      _announcements[index] = _announcements[index].copyWith(
        readAt: DateTime.now(),
      );
    }
  }

  @override
  Future<void> logout() async {
    _connection.add(false);
    await _store.clearAccountData();
  }

  @override
  Future<void> connect() async {
    await Future<void>.delayed(const Duration(milliseconds: 80));
    _connection.add(true);
  }

  @override
  Future<void> syncNow() async {}

  @override
  Future<List<Conversation>> conversations() async {
    await Future<void>.delayed(latency);
    return List.of(_conversations);
  }

  @override
  Future<List<AppUser>> contacts() async {
    await Future<void>.delayed(latency);
    return people;
  }

  @override
  Future<List<AppUser>> searchUsers(
    String query, {
    String by = 'handle',
  }) async {
    final normalized = query.trim().toLowerCase();
    if (normalized.isEmpty) return people;
    return people
        .where(
          (user) =>
              by == 'phone' ? false : user.handle.toLowerCase() == normalized,
        )
        .toList();
  }

  @override
  Future<UserSearchCapabilities> searchCapabilities() async =>
      const UserSearchCapabilities(
        allowSearchByHandle: true,
        allowSearchByPhone: false,
      );

  @override
  Future<List<FriendRequest>> friendRequests() async => List.of(_requests);

  @override
  Future<void> sendFriendRequest(
    String userId,
    String note, {
    String source = 'search',
    String? sourceId,
  }) async {
    await Future<void>.delayed(latency);
  }

  @override
  Future<void> acceptFriendRequest(String requestId) async {
    await Future<void>.delayed(latency);
    final index = _requests.indexWhere((request) => request.id == requestId);
    if (index >= 0) {
      _requests[index] = _requests[index].copyWith(status: 'accepted');
    }
  }

  @override
  Future<void> rejectFriendRequest(String requestId) async {
    await Future<void>.delayed(latency);
    final index = _requests.indexWhere((request) => request.id == requestId);
    if (index >= 0) {
      _requests[index] = _requests[index].copyWith(status: 'rejected');
    }
  }

  @override
  Future<void> cancelFriendRequest(String requestId) async {
    await Future<void>.delayed(latency);
    final index = _requests.indexWhere((request) => request.id == requestId);
    if (index >= 0) {
      _requests[index] = _requests[index].copyWith(status: 'cancelled');
    }
  }

  @override
  Future<void> updateFriendMetadata(
    String userId, {
    required String remark,
    required List<String> tags,
  }) async {
    await Future<void>.delayed(latency);
  }

  @override
  Future<void> deleteFriend(String userId) async {
    await Future<void>.delayed(latency);
  }

  @override
  Future<void> blockUser(String userId, bool blocked) async {
    await Future<void>.delayed(latency);
  }

  @override
  Future<List<AppUser>> blockedUsers() async => const [];

  @override
  Future<void> report({
    required String targetType,
    required String targetId,
    required String reason,
    String details = '',
  }) async {
    await Future<void>.delayed(latency);
  }

  @override
  Future<Conversation> createDirect(AppUser user) async {
    await Future<void>.delayed(latency);
    final existing = _conversations.where(
      (item) =>
          item.kind == ConversationKind.direct &&
          item.members.any((x) => x.id == user.id),
    );
    if (existing.isNotEmpty) return existing.first;
    final conversation = Conversation(
      id: 'c-${user.id}',
      title: user.name,
      subtitle: '开始新的对话',
      updatedAt: DateTime.now(),
      kind: ConversationKind.direct,
      members: [user],
    );
    _conversations.insert(0, conversation);
    return conversation;
  }

  @override
  Future<Conversation> createGroup(String name, List<AppUser> members) async {
    await Future<void>.delayed(latency);
    final conversation = Conversation(
      id: 'group-${DateTime.now().microsecondsSinceEpoch}',
      title: name.trim(),
      subtitle: '群聊已创建',
      updatedAt: DateTime.now(),
      kind: ConversationKind.group,
      members: members,
    );
    _conversations.insert(0, conversation);
    return conversation;
  }

  @override
  Future<GroupProfile> groupProfile(String conversationId) async {
    await Future<void>.delayed(latency);
    return _groupProfiles.putIfAbsent(conversationId, () {
      final conversation = _conversations.firstWhere(
        (item) => item.id == conversationId,
      );
      return GroupProfile(
        conversationId: conversationId,
        ownerId: _profile.id,
        name: conversation.title,
        avatarUrl: conversation.avatarUrl,
        announcement: '欢迎友好、真诚地交流。',
        announcementVersion: 1,
        joinPolicy: 'invite',
        allowMemberAddFriend: true,
        updatedAt: DateTime.now(),
      );
    });
  }

  @override
  Future<GroupProfile> updateGroupProfile(
    String conversationId, {
    String? name,
    String? avatarMediaId,
    String? joinPolicy,
    bool? allowMemberAddFriend,
    bool rotateQr = false,
  }) async {
    final old = await groupProfile(conversationId);
    final updated = GroupProfile(
      conversationId: old.conversationId,
      ownerId: old.ownerId,
      name: name ?? old.name,
      avatarUrl: avatarMediaId == null
          ? old.avatarUrl
          : avatarMediaId.isEmpty
          ? null
          : '/v1/media/$avatarMediaId',
      announcement: old.announcement,
      announcementVersion: old.announcementVersion,
      announcementReadAt: old.announcementReadAt,
      joinPolicy: joinPolicy ?? old.joinPolicy,
      allowMemberAddFriend: allowMemberAddFriend ?? old.allowMemberAddFriend,
      allMutedUntil: old.allMutedUntil,
      qrToken: rotateQr ? 'demo-qr-token' : old.qrToken,
      qrExpiresAt: rotateQr
          ? DateTime.now().add(const Duration(days: 1))
          : old.qrExpiresAt,
      updatedAt: DateTime.now(),
    );
    _groupProfiles[conversationId] = updated;
    return updated;
  }

  @override
  Future<GroupProfile> setGroupAnnouncement(
    String conversationId,
    String content,
  ) async {
    final old = await groupProfile(conversationId);
    final updated = GroupProfile(
      conversationId: old.conversationId,
      ownerId: old.ownerId,
      name: old.name,
      avatarUrl: old.avatarUrl,
      announcement: content,
      announcementVersion: old.announcementVersion + 1,
      joinPolicy: old.joinPolicy,
      allowMemberAddFriend: old.allowMemberAddFriend,
      allMutedUntil: old.allMutedUntil,
      updatedAt: DateTime.now(),
    );
    _groupProfiles[conversationId] = updated;
    return updated;
  }

  @override
  Future<void> markGroupAnnouncementRead(String conversationId) async {
    final old = await groupProfile(conversationId);
    _groupProfiles[conversationId] = GroupProfile(
      conversationId: old.conversationId,
      ownerId: old.ownerId,
      name: old.name,
      avatarUrl: old.avatarUrl,
      announcement: old.announcement,
      announcementVersion: old.announcementVersion,
      announcementReadAt: DateTime.now(),
      joinPolicy: old.joinPolicy,
      allowMemberAddFriend: old.allowMemberAddFriend,
      allMutedUntil: old.allMutedUntil,
      updatedAt: old.updatedAt,
    );
  }

  @override
  Future<List<GroupMember>> groupMembers(String conversationId) async {
    await Future<void>.delayed(latency);
    return List.of(
      _groupMemberState.putIfAbsent(conversationId, () {
        final members = _conversations
            .firstWhere((item) => item.id == conversationId)
            .members;
        return [
          GroupMember(
            user: _profile,
            role: 'owner',
            joinedAt: DateTime.now().subtract(const Duration(days: 30)),
          ),
          ...members
              .where((user) => user.id != _profile.id)
              .map(
                (user) => GroupMember(
                  user: user,
                  role: 'member',
                  joinedAt: DateTime.now().subtract(const Duration(days: 20)),
                ),
              ),
        ];
      }),
    );
  }

  @override
  Future<void> addGroupMembers(
    String conversationId,
    List<String> userIds,
  ) async {
    final members = await groupMembers(conversationId);
    for (final user in people.where((user) => userIds.contains(user.id))) {
      if (members.every((member) => member.user.id != user.id)) {
        members.add(
          GroupMember(user: user, role: 'member', joinedAt: DateTime.now()),
        );
      }
    }
    _groupMemberState[conversationId] = members;
  }

  @override
  Future<void> inviteGroupMember(String conversationId, String userId) async {
    await Future<void>.delayed(latency);
  }

  @override
  Future<List<GroupInvitation>> groupInvitations() async {
    await Future<void>.delayed(latency);
    return List.of(_groupInvitations);
  }

  @override
  Future<void> respondGroupInvitation(
    String invitationId,
    String action,
  ) async {
    await Future<void>.delayed(latency);
    final index = _groupInvitations.indexWhere(
      (item) => item.id == invitationId,
    );
    if (index < 0 || !const {'accept', 'reject', 'cancel'}.contains(action)) {
      throw const FormatException('群邀请已失效');
    }
    _groupInvitations[index] = _groupInvitations[index].copyWith(
      status: switch (action) {
        'accept' => 'accepted',
        'reject' => 'rejected',
        _ => 'cancelled',
      },
    );
  }

  @override
  Future<void> joinGroupByQr(String token) async {
    await Future<void>.delayed(latency);
  }

  @override
  Future<void> removeGroupMember(String conversationId, String userId) async {
    final members = await groupMembers(conversationId);
    members.removeWhere((member) => member.user.id == userId);
    _groupMemberState[conversationId] = members;
  }

  @override
  Future<void> setGroupRole(
    String conversationId,
    String userId,
    String role,
  ) async {
    final members = await groupMembers(conversationId);
    final index = members.indexWhere((member) => member.user.id == userId);
    if (index >= 0) {
      final old = members[index];
      members[index] = GroupMember(
        user: old.user,
        role: role,
        joinedAt: old.joinedAt,
        groupNickname: old.groupNickname,
      );
      _groupMemberState[conversationId] = members;
    }
  }

  @override
  Future<void> transferGroupOwner(String conversationId, String userId) async {
    await setGroupRole(conversationId, _profile.id, 'member');
    await setGroupRole(conversationId, userId, 'owner');
  }

  @override
  Future<void> setGroupNickname(String conversationId, String nickname) async {
    await Future<void>.delayed(latency);
  }

  @override
  Future<GroupProfile> setGroupAllMuted(
    String conversationId,
    bool muted,
  ) async {
    final old = await groupProfile(conversationId);
    final updated = GroupProfile(
      conversationId: old.conversationId,
      ownerId: old.ownerId,
      name: old.name,
      avatarUrl: old.avatarUrl,
      announcement: old.announcement,
      announcementVersion: old.announcementVersion,
      announcementReadAt: old.announcementReadAt,
      joinPolicy: old.joinPolicy,
      allowMemberAddFriend: old.allowMemberAddFriend,
      allMutedUntil: muted
          ? DateTime.now().add(const Duration(days: 3650))
          : DateTime.now(),
      updatedAt: DateTime.now(),
    );
    _groupProfiles[conversationId] = updated;
    return updated;
  }

  @override
  Future<void> leaveGroup(String conversationId) async {
    await Future<void>.delayed(latency);
    _conversations.removeWhere((item) => item.id == conversationId);
  }

  @override
  Future<void> disbandGroup(String conversationId, String reason) async {
    await leaveGroup(conversationId);
  }

  @override
  Future<List<ChatMessage>> messages(String conversationId) async {
    if (_cachedMessages[conversationId] case final cached?) {
      return List.of(cached);
    }
    final stored = await _store.readJson('messages.$conversationId');
    if (stored is List<Object?> && stored.isNotEmpty) {
      final decoded = stored
          .map((item) => ChatMessage.fromJson(item! as Map<String, Object?>))
          .toList();
      _cachedMessages[conversationId] = decoded;
      return List.of(decoded);
    }
    final now = DateTime.now();
    final group =
        conversationId.contains('team') || conversationId.contains('coffee');
    final seeded = [
      ChatMessage(
        id: 'm1-$conversationId',
        conversationId: conversationId,
        senderId: 'u1',
        senderName: '林屿',
        text: group ? '欢迎来到这个频道，重要进展都在这里同步。' : '今天过得怎么样？',
        sentAt: now.subtract(const Duration(minutes: 35)),
        isMine: false,
        conversationSeq: 1,
        status: MessageStatus.read,
      ),
      ChatMessage(
        id: 'm2-$conversationId',
        conversationId: conversationId,
        senderId: 'me',
        senderName: '我',
        text: '很不错，刚把新版本的体验走了一遍。',
        kind: MessageContentKind.reply,
        replyToId: 'm1-$conversationId',
        replyToText: group ? '重要进展都在这里同步。' : '今天过得怎么样？',
        sentAt: now.subtract(const Duration(minutes: 31)),
        isMine: true,
        conversationSeq: 2,
        status: MessageStatus.read,
      ),
      ChatMessage(
        id: 'm3-$conversationId',
        conversationId: conversationId,
        senderId: group ? 'u2' : 'u1',
        senderName: group ? '安然' : '林屿',
        text: group ? '太好了，我已经把新的动效稿上传了。' : '晚点一起看下新版本？',
        sentAt: now.subtract(const Duration(minutes: 26)),
        isMine: false,
        conversationSeq: 3,
        status: MessageStatus.delivered,
      ),
      ChatMessage(
        id: 'm4-$conversationId',
        conversationId: conversationId,
        senderId: 'u2',
        senderName: '安然',
        text: '刚拍到的画面，分享给你。',
        sentAt: now.subtract(const Duration(minutes: 17)),
        isMine: false,
        conversationSeq: 4,
        kind: MessageContentKind.image,
        mediaUrl: 'assets/avatars/weekend-coffee.png',
        status: MessageStatus.delivered,
      ),
      ChatMessage(
        id: 'm5-$conversationId',
        conversationId: conversationId,
        senderId: 'me',
        senderName: '我',
        text: '语音消息',
        sentAt: now.subtract(const Duration(minutes: 12)),
        isMine: true,
        conversationSeq: 5,
        kind: MessageContentKind.voice,
        durationSeconds: 8,
        status: MessageStatus.read,
      ),
    ];
    _cachedMessages[conversationId] = seeded;
    return List.of(seeded);
  }

  @override
  Future<ChatMessage> send(ChatMessage pending) async {
    await Future<void>.delayed(latency);
    return pending.copyWith(
      id: 'demo-${DateTime.now().microsecondsSinceEpoch}',
      conversationSeq: DateTime.now().millisecondsSinceEpoch,
      status: MessageStatus.sent,
    );
  }

  @override
  Future<ChatMessage> editMessage(String messageId, String text) async {
    await Future<void>.delayed(latency);
    final found = _findMessage(messageId);
    if (found == null) throw StateError('message not found');
    final edited = found.$2.copyWith(text: text, editedAt: DateTime.now());
    found.$1[found.$3] = edited;
    return edited;
  }

  @override
  Future<ChatMessage> setMessageReaction(
    String messageId,
    String emoji, {
    required bool active,
  }) async {
    await Future<void>.delayed(latency);
    final found = _findMessage(messageId);
    if (found == null) throw StateError('message not found');
    final reactions = List<MessageReaction>.of(found.$2.reactions);
    final index = reactions.indexWhere((reaction) => reaction.emoji == emoji);
    if (index < 0 && active) {
      reactions.add(
        MessageReaction(
          emoji: emoji,
          count: 1,
          reactedByMe: true,
          userIds: [_profile.id],
        ),
      );
    } else if (index >= 0) {
      final current = reactions[index];
      final count = active
          ? current.count + (current.reactedByMe ? 0 : 1)
          : current.count - (current.reactedByMe ? 1 : 0);
      if (count <= 0) {
        reactions.removeAt(index);
      } else {
        reactions[index] = MessageReaction(
          emoji: emoji,
          count: count,
          reactedByMe: active,
          userIds: active
              ? {...current.userIds, _profile.id}.toList()
              : current.userIds.where((id) => id != _profile.id).toList(),
        );
      }
    }
    final updated = found.$2.copyWith(reactions: reactions);
    found.$1[found.$3] = updated;
    return updated;
  }

  @override
  Future<List<ChatMessage>> pinnedMessages(String conversationId) async {
    await Future<void>.delayed(latency);
    return (_cachedMessages[conversationId] ?? const [])
        .where((message) => message.isPinned)
        .toList();
  }

  @override
  Future<void> setMessagePinned(
    String conversationId,
    String messageId, {
    required bool pinned,
  }) async {
    await Future<void>.delayed(latency);
    final messages = _cachedMessages[conversationId];
    final index = messages?.indexWhere((message) => message.id == messageId);
    if (messages == null || index == null || index < 0) {
      throw StateError('message not found');
    }
    messages[index] = messages[index].copyWith(
      isPinned: pinned,
      pinnedAt: pinned ? DateTime.now() : null,
      pinnedBy: pinned ? _profile.id : null,
    );
  }

  @override
  Future<List<ChatMessage>> searchMessages(
    String conversationId,
    String query, {
    int limit = 50,
  }) async {
    await Future<void>.delayed(latency);
    final normalized = query.trim().toLowerCase();
    if (normalized.isEmpty) return const [];
    return (_cachedMessages[conversationId] ?? const [])
        .where((message) => message.text.toLowerCase().contains(normalized))
        .take(limit)
        .toList();
  }

  (List<ChatMessage>, ChatMessage, int)? _findMessage(String messageId) {
    for (final messages in _cachedMessages.values) {
      final index = messages.indexWhere((message) => message.id == messageId);
      if (index >= 0) return (messages, messages[index], index);
    }
    return null;
  }

  @override
  Future<List<ChatMessage>> forwardMessages(
    String targetConversationId,
    List<String> sourceMessageIds, {
    required String mode,
    required String clientBatchId,
  }) async {
    await Future<void>.delayed(latency);
    final all = _cachedMessages.values.expand((items) => items);
    final sources = sourceMessageIds
        .map((id) => all.where((message) => message.id == id).firstOrNull)
        .whereType<ChatMessage>()
        .toList();
    if (mode == 'merged') {
      return [
        ChatMessage(
          id: 'demo-forward-$clientBatchId',
          clientMessageId: 'forward-$clientBatchId',
          conversationId: targetConversationId,
          senderId: _profile.id,
          senderName: _profile.name,
          text: '[聊天记录] ${sources.length} 条消息',
          sentAt: DateTime.now(),
          isMine: true,
          conversationSeq: DateTime.now().millisecondsSinceEpoch,
          status: MessageStatus.sent,
        ),
      ];
    }
    return [
      for (var index = 0; index < sources.length; index++)
        ChatMessage(
          id: 'demo-forward-$clientBatchId-$index',
          clientMessageId: 'forward-$clientBatchId-$index',
          conversationId: targetConversationId,
          senderId: _profile.id,
          senderName: _profile.name,
          text: sources[index].text,
          sentAt: DateTime.now(),
          isMine: true,
          conversationSeq: DateTime.now().millisecondsSinceEpoch + index,
          status: MessageStatus.sent,
          kind: sources[index].kind,
          mediaUrl: sources[index].mediaUrl,
          mediaId: sources[index].mediaId,
          fileName: sources[index].fileName,
          mimeType: sources[index].mimeType,
          durationSeconds: sources[index].durationSeconds,
          contactUserId: sources[index].contactUserId,
          contactName: sources[index].contactName,
          contactHandle: sources[index].contactHandle,
          contactAvatarUrl: sources[index].contactAvatarUrl,
          latitude: sources[index].latitude,
          longitude: sources[index].longitude,
          locationName: sources[index].locationName,
          locationAddress: sources[index].locationAddress,
        ),
    ];
  }

  @override
  Future<ChatMessage> sendMedia(
    ChatMessage pending,
    MediaUpload upload, {
    void Function(double progress)? onProgress,
  }) async {
    onProgress?.call(.25);
    await Future<void>.delayed(latency);
    onProgress?.call(1);
    return pending.copyWith(
      id: 'demo-${DateTime.now().microsecondsSinceEpoch}',
      conversationSeq: DateTime.now().millisecondsSinceEpoch,
      status: MessageStatus.sent,
      kind: upload.kind,
      mediaUrl: upload.localPath,
      mediaId: 'demo-media-${pending.clientMessageId}',
      fileName: upload.fileName,
      mimeType: upload.mimeType,
      durationSeconds: upload.durationSeconds,
    );
  }

  @override
  Future<void> saveFavorite(ChatMessage message) async {
    final stored = await _store.readJson('favorites');
    final favorites = stored is List<Object?>
        ? stored.whereType<Map<String, Object?>>().toList()
        : <Map<String, Object?>>[];
    favorites.removeWhere(
      (item) => item['clientMessageId'] == message.clientMessageId,
    );
    favorites.insert(0, message.toJson());
    await _store.writeJson('favorites', favorites.take(500).toList());
  }

  @override
  Future<void> removeFavorite(ChatMessage message) async {
    final stored = await _store.readJson('favorites');
    final favorites = stored is List<Object?>
        ? stored.whereType<Map<String, Object?>>().toList()
        : <Map<String, Object?>>[];
    favorites.removeWhere(
      (item) =>
          item['id'] == message.id ||
          item['clientMessageId'] == message.clientMessageId,
    );
    await _store.writeJson('favorites', favorites);
  }

  @override
  Future<void> markRead(String conversationId, int sequence) async {}

  @override
  Future<void> markDelivered(String conversationId, int sequence) async {
    await Future<void>.delayed(latency);
  }

  @override
  Future<void> updateConversationPreferences(
    String conversationId, {
    bool? pinned,
    bool? notificationsMuted,
    bool? manualUnread,
    bool? archived,
  }) async {
    await Future<void>.delayed(latency);
    final index = _conversations.indexWhere(
      (item) => item.id == conversationId,
    );
    if (index < 0) throw StateError('conversation not found');
    final current = _conversations[index];
    _conversations[index] = current.copyWith(
      pinned: pinned,
      muted: notificationsMuted,
      archived: archived,
      unread: manualUnread == true
          ? (current.unread > 0 ? current.unread : 1)
          : null,
    );
  }

  @override
  Future<List<ScheduledMessage>> scheduledMessages(
    String conversationId,
  ) async {
    await Future<void>.delayed(latency);
    return List.of(_scheduledMessages[conversationId] ?? const []);
  }

  @override
  Future<ScheduledMessage> scheduleMessage(
    String conversationId,
    String text,
    DateTime scheduledAt, {
    String? replyToId,
    int? expiresInSeconds,
  }) async {
    await Future<void>.delayed(latency);
    if (scheduledAt.isBefore(DateTime.now())) {
      throw const FormatException('发送时间必须晚于当前时间');
    }
    final item = ScheduledMessage(
      id: 'scheduled-${DateTime.now().microsecondsSinceEpoch}',
      conversationId: conversationId,
      text: text,
      scheduledAt: scheduledAt,
      status: 'scheduled',
      expiresInSeconds: expiresInSeconds,
    );
    _scheduledMessages.putIfAbsent(conversationId, () => []).add(item);
    return item;
  }

  @override
  Future<void> cancelScheduledMessage(String scheduledMessageId) async {
    await Future<void>.delayed(latency);
    for (final items in _scheduledMessages.values) {
      items.removeWhere((item) => item.id == scheduledMessageId);
    }
  }

  @override
  Future<LinkPreview?> linkPreview(String url) async {
    await Future<void>.delayed(latency);
    if (!url.startsWith('http://') && !url.startsWith('https://')) return null;
    return LinkPreview(
      url: url,
      title: '邻里链接预览',
      description: '此卡片内容由服务端链接预览接口返回。',
      siteName: Uri.tryParse(url)?.host ?? '网页',
    );
  }

  @override
  Future<void> hideConversation(String conversationId) async {}

  @override
  Future<void> recallMessage(String messageId) async {}

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {
    _cachedMessages[conversationId] = List.of(messages);
    await _store.writeJson(
      'messages.$conversationId',
      messages.map((message) => message.toJson()).toList(),
    );
  }

  @override
  Future<String> readDraft(String conversationId) async =>
      (await _store.readJson('draft.$conversationId') as String?) ?? '';

  @override
  Future<void> saveDraft(String conversationId, String text) async {
    final value = text.trimRight();
    if (value.isEmpty) {
      await _store.remove('draft.$conversationId');
    } else {
      await _store.writeJson('draft.$conversationId', value);
    }
  }

  @override
  Future<void> close() async {
    _connection.add(false);
  }

  List<Conversation> _seedConversations() {
    final now = DateTime.now();
    return [
      Conversation(
        id: 'c-team',
        title: '邻里产品小组',
        subtitle: '安然：新的动效稿已上传',
        updatedAt: now.subtract(const Duration(minutes: 8)),
        kind: ConversationKind.group,
        pinned: true,
        unread: 3,
        mentionUnreadCount: 2,
        lastMessageSeq: 9,
        members: people.take(4).toList(),
      ),
      Conversation(
        id: 'c-linyu',
        title: '林屿',
        subtitle: '晚点一起看下新版本？',
        updatedAt: now.subtract(const Duration(minutes: 26)),
        kind: ConversationKind.direct,
        unread: 1,
        mentionUnreadCount: 0,
        lastMessageSeq: 5,
        members: [people[0]],
      ),
      Conversation(
        id: 'c-design-review',
        title: '设计评审',
        subtitle: '李想：交互稿已经标好注释',
        updatedAt: now.subtract(const Duration(minutes: 43)),
        kind: ConversationKind.group,
        avatarUrl: 'assets/avatars/li-xiang.png',
        mentionUnreadCount: 0,
        members: [people[5], people[1], people[0]],
      ),
      Conversation(
        id: 'c-coffee',
        title: '周末咖啡局',
        subtitle: '周末：我订好了窗边的位置',
        updatedAt: now.subtract(const Duration(hours: 2)),
        kind: ConversationKind.group,
        avatarUrl: 'assets/avatars/weekend-coffee.png',
        muted: true,
        mentionUnreadCount: 0,
        members: people.skip(1).toList(),
      ),
      Conversation(
        id: 'c-anran',
        title: '安然',
        subtitle: '[图片] 云层像一片海',
        updatedAt: now.subtract(const Duration(days: 1)),
        kind: ConversationKind.direct,
        avatarUrl: 'assets/avatars/an-ran.png',
        mentionUnreadCount: 0,
        members: [people[1]],
      ),
    ];
  }
}
