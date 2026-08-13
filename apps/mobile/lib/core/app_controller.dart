import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:flutter/foundation.dart';

import '../calls/call_controller.dart';
import '../calls/call_repository.dart';
import '../data/im_repository.dart';
import '../im/business_features.dart';
import '../im/structured_event_text.dart';
import 'client_message_id.dart';
import 'models.dart';

class AppController extends ChangeNotifier {
  AppController(this.repository);

  final ImRepository repository;
  late final CallController? callController = repository is CallRepository
      ? CallController(
          repository: repository as CallRepository,
          currentUser: () => currentUser,
          findConversation: (id) =>
              conversations.where((item) => item.id == id).firstOrNull,
        )
      : null;
  bool authenticated = false;
  bool initializing = true;
  bool loading = false;
  bool connected = false;
  bool codeRequested = false;
  String? developmentCode;
  String? error;
  AppUser? currentUser;
  AppNotice? notice;
  List<Conversation> conversations = [];
  List<AppUser> contacts = [];
  List<FriendRequest> requests = [];
  List<GroupInvitation> groupInvitations = [];
  List<AppAnnouncement> announcements = [];
  final Map<String, List<ChatMessage>> _messages = {};
  final Map<String, String> _drafts = {};
  final Map<String, MediaUpload> _pendingMedia = {};
  final Map<String, double> _mediaUploadProgress = {};
  final Map<String, List<ScheduledMessage>> _scheduledMessages = {};
  final Map<String, String> scheduledMessageErrors = {};
  final Set<String> scheduledMessageLoading = {};
  final Map<String, int> _lastDeliveredSeq = {};
  final Map<String, int> _pendingDeliveredSeq = {};
  final Map<String, Timer> _deliveryTimers = {};
  final Map<String, Map<String, DateTime>> _typingUsers = {};
  final Map<String, Timer> _typingExpiryTimers = {};
  final Map<String, Timer> _typingStopTimers = {};
  final Map<String, DateTime> _lastTypingSent = {};
  final Set<String> _typingAnnounced = {};
  Timer? _conversationRefreshTimer;
  bool _conversationRefreshRunning = false;
  bool _conversationRefreshQueued = false;
  final Set<String> _linkPreviewAttempted = {};
  final Set<String> messageLoading = {};
  final Map<String, String> messageErrors = {};
  StreamSubscription<bool>? _connectionSubscription;
  StreamSubscription<ImEvent>? _eventSubscription;
  final Random _random = Random.secure();
  String? _activeConversationId;
  String? _pendingConversationId;
  String? _pushDeviceId;
  String? _voipPushDeviceId;
  bool _disposed = false;

  bool get isDemo => repository.isDemo;
  bool get supportsDemo => repository.supportsDemo;
  int get notificationUnreadCount => conversations
      .where((conversation) => !conversation.muted)
      .fold(0, (total, conversation) => total + conversation.unread);
  bool get hasMutedUnread => conversations.any(
    (conversation) => conversation.muted && conversation.unread > 0,
  );
  bool get supportsMentionUnread => conversations.any(
    (conversation) => conversation.mentionUnreadCount != null,
  );
  int get mentionUnreadCount => conversations.fold(
    0,
    (total, conversation) => total + (conversation.mentionUnreadCount ?? 0),
  );
  String? get pendingConversationId => _pendingConversationId;
  String? get activeConversationId => _activeConversationId;
  String? typingLabelFor(String conversationId) {
    final now = DateTime.now();
    final userIds = (_typingUsers[conversationId] ?? const {}).entries
        .where((entry) => entry.value.isAfter(now))
        .map((entry) => entry.key)
        .toList();
    if (userIds.isEmpty) return null;
    final conversation = conversations
        .where((item) => item.id == conversationId)
        .firstOrNull;
    if (conversation == null || conversation.kind != ConversationKind.group) {
      return '正在输入…';
    }
    final names = userIds
        .map(
          (userId) => conversation.members
              .where((member) => member.id == userId)
              .firstOrNull
              ?.name,
        )
        .whereType<String>()
        .where((name) => name.trim().isNotEmpty)
        .toList();
    if (names.isEmpty) return '有人正在输入…';
    if (names.length == 1) return '${names.single} 正在输入…';
    return '${names.take(2).join('、')} 等正在输入…';
  }

  List<ChatMessage> messagesFor(String conversationId) =>
      List.unmodifiable(_messages[conversationId] ?? const []);
  String draftFor(String conversationId) => _drafts[conversationId] ?? '';
  double? mediaUploadProgressFor(String clientMessageId) =>
      _mediaUploadProgress[clientMessageId];
  List<ScheduledMessage> scheduledMessagesFor(String conversationId) =>
      List.unmodifiable(_scheduledMessages[conversationId] ?? const []);
  int get archivedConversationCount =>
      conversations.where((conversation) => conversation.archived).length;
  bool get supportsBusinessFeatures => repository is BusinessFeatureRepository;

  BusinessFeatureRepository get _businessFeatures {
    final active = repository;
    if (active is BusinessFeatureRepository) {
      return active as BusinessFeatureRepository;
    }
    throw StateError('当前模式不支持业务频道与客服功能');
  }

  Future<String> loadDraft(String conversationId) async {
    final draft = await repository.readDraft(conversationId);
    if (draft.isEmpty) {
      _drafts.remove(conversationId);
    } else {
      _drafts[conversationId] = draft;
    }
    notifyListeners();
    return draft;
  }

  Future<void> saveDraft(
    String conversationId,
    String text, {
    bool notify = true,
  }) async {
    if (_disposed) return;
    final value = text.trimRight();
    if (value.isEmpty) {
      _drafts.remove(conversationId);
    } else {
      _drafts[conversationId] = value;
    }
    if (notify) notifyListeners();
    await repository.saveDraft(conversationId, value);
  }

  Future<void> initialize() async {
    initializing = true;
    notifyListeners();
    try {
      if (await repository.restoreSession()) {
        authenticated = true;
        currentUser = repository.currentUser;
        _subscribe();
        await _loadCore();
        unawaited(_connectSafely());
      }
    } catch (exception) {
      // 恢复会话后的短暂网络错误不应清除登录态。
      error = _messageFor(exception, fallback: '已恢复账号，消息正在重连');
    } finally {
      initializing = false;
      notifyListeners();
    }
  }

  Future<void> requestCode(String phone) async {
    final normalized = phone.trim();
    if (normalized.length < 5) {
      error = '请输入有效手机号';
      notifyListeners();
      return;
    }
    loading = true;
    error = null;
    notifyListeners();
    try {
      developmentCode = await repository.requestCode(normalized);
      codeRequested = true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '验证码发送失败，请稍后重试');
    } finally {
      loading = false;
      notifyListeners();
    }
  }

  Future<void> login(String phone, String code) async {
    loading = true;
    error = null;
    notifyListeners();
    try {
      currentUser = await repository.login(phone.trim(), code.trim());
      authenticated = true;
      _subscribe();
      await _loadCore();
      notice = repository.isDemo
          ? const AppNotice(title: '重要提醒', message: '会议室设备维护将于今晚 22:00 进行')
          : null;
      unawaited(_connectSafely());
    } catch (exception) {
      authenticated = false;
      error = _messageFor(exception, fallback: '登录失败，请检查信息后重试');
    } finally {
      loading = false;
      notifyListeners();
    }
  }

  Future<void> passwordLogin(String phone, String password) async {
    if (phone.trim().isEmpty || password.isEmpty) {
      error = '请输入手机号和密码';
      notifyListeners();
      return;
    }
    loading = true;
    error = null;
    notifyListeners();
    try {
      currentUser = await repository.passwordLogin(phone.trim(), password);
      authenticated = true;
      _subscribe();
      await _loadCore();
      unawaited(_connectSafely());
    } catch (exception) {
      authenticated = false;
      error = _messageFor(exception, fallback: '手机号或密码错误');
    } finally {
      loading = false;
      notifyListeners();
    }
  }

  Future<bool> registerAccount({
    required String phone,
    required String code,
    required String password,
    required String name,
  }) async {
    if (phone.trim().isEmpty ||
        code.trim().isEmpty ||
        password.isEmpty ||
        name.trim().isEmpty) {
      error = '请完整填写注册信息';
      notifyListeners();
      return false;
    }
    loading = true;
    error = null;
    notifyListeners();
    try {
      currentUser = await repository.register(
        phone: phone.trim(),
        code: code.trim(),
        password: password,
        name: name.trim(),
      );
      authenticated = true;
      _subscribe();
      await _loadCore();
      unawaited(_connectSafely());
      return true;
    } catch (exception) {
      authenticated = false;
      error = _messageFor(exception, fallback: '注册失败');
      return false;
    } finally {
      loading = false;
      notifyListeners();
    }
  }

  Future<bool> requestResetCode(String phone) async {
    if (phone.trim().isEmpty) {
      error = '请输入手机号';
      notifyListeners();
      return false;
    }
    loading = true;
    error = null;
    notifyListeners();
    try {
      await repository.requestPasswordResetCode(phone.trim());
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '验证码发送失败');
      return false;
    } finally {
      loading = false;
      notifyListeners();
    }
  }

  Future<bool> resetPassword({
    required String phone,
    required String code,
    required String password,
  }) async {
    if (phone.trim().isEmpty || code.trim().isEmpty || password.isEmpty) {
      error = '请完整填写重置信息';
      notifyListeners();
      return false;
    }
    loading = true;
    error = null;
    notifyListeners();
    try {
      await repository.resetPassword(
        phone: phone.trim(),
        code: code.trim(),
        password: password,
      );
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '密码重置失败');
      return false;
    } finally {
      loading = false;
      notifyListeners();
    }
  }

  Future<void> loginAsDemo() async {
    try {
      await repository.enterDemo();
      await login('13800138000', '123456');
    } catch (exception) {
      error = _messageFor(exception, fallback: '当前构建未启用演示模式');
      notifyListeners();
    }
  }

  void _subscribe() {
    _connectionSubscription ??= repository.connectionChanges.listen((value) {
      connected = value;
      if (value) unawaited(_flushPendingDeliveries());
      notifyListeners();
    });
    _eventSubscription ??= repository.events.listen(_handleEvent);
  }

  Future<void> _connectSafely() async {
    try {
      await repository.connect();
    } catch (_) {
      connected = false;
      notifyListeners();
    }
  }

  Future<void> _loadCore() async {
    conversations = await repository.conversations();
    _sortConversations();
    final results = await Future.wait<Object>([
      _loadOptional(repository.contacts(), contacts),
      _loadOptional(repository.friendRequests(), requests),
      _loadOptional(repository.groupInvitations(), groupInvitations),
      _loadOptional(repository.announcements(), announcements),
    ]);
    contacts = results[0] as List<AppUser>;
    requests = results[1] as List<FriendRequest>;
    groupInvitations = results[2] as List<GroupInvitation>;
    announcements = results[3] as List<AppAnnouncement>;
  }

  Future<T> _loadOptional<T>(Future<T> operation, T fallback) async {
    try {
      return await operation;
    } catch (_) {
      return fallback;
    }
  }

  Future<void> refresh() async {
    loading = true;
    error = null;
    notifyListeners();
    try {
      await repository.syncNow();
      await _loadCore();
    } catch (exception) {
      error = _messageFor(exception, fallback: '刷新失败，请检查网络后重试');
    } finally {
      loading = false;
      notifyListeners();
    }
  }

  Future<List<BusinessChannelSummary>> loadBusinessChannels({
    int channelType = 0,
    String parentId = '',
  }) => _businessFeatures.businessChannels(
    channelType: channelType,
    parentId: parentId,
  );

  Future<BusinessChannelSummary> createBusinessChannel({
    required int channelType,
    required String name,
    String parentId = '',
    String description = '',
    String postingPolicy = 'members',
    int slowModeSeconds = 0,
  }) => _businessFeatures.createBusinessChannel(
    channelType: channelType,
    name: name,
    parentId: parentId,
    description: description,
    postingPolicy: postingPolicy,
    slowModeSeconds: slowModeSeconds,
  );

  Future<BusinessChannelSummary> loadBusinessChannel(
    String channelId,
    int channelType,
  ) => _businessFeatures.businessChannel(channelId, channelType);

  Future<BusinessChannelSummary> updateBusinessChannel(
    BusinessChannelSummary channel, {
    String? name,
    String? description,
    String? visibility,
    String? joinPolicy,
    String? postingPolicy,
    int? slowModeSeconds,
    bool? sendBan,
    bool? allowStranger,
  }) => _businessFeatures.updateBusinessChannel(
    channel.id,
    channel.channelType,
    name: name,
    description: description,
    visibility: visibility,
    joinPolicy: joinPolicy,
    postingPolicy: postingPolicy,
    slowModeSeconds: slowModeSeconds,
    sendBan: sendBan,
    allowStranger: allowStranger,
  );

  Future<List<BusinessChannelMemberSummary>> loadBusinessChannelMembers(
    BusinessChannelSummary channel,
  ) =>
      _businessFeatures.businessChannelMembers(channel.id, channel.channelType);

  Future<void> addBusinessChannelMember(
    BusinessChannelSummary channel,
    String userId, {
    DateTime? expiresAt,
  }) => _businessFeatures.addBusinessChannelMember(
    channel.id,
    channel.channelType,
    userId,
    expiresAt: expiresAt,
  );

  Future<void> removeBusinessChannelMember(
    BusinessChannelSummary channel,
    String userId,
  ) => _businessFeatures.removeBusinessChannelMember(
    channel.id,
    channel.channelType,
    userId,
  );

  Future<void> updateBusinessChannelMember(
    BusinessChannelSummary channel,
    String userId, {
    String? role,
    DateTime? mutedUntil,
    bool clearMute = false,
    DateTime? expiresAt,
    bool clearExpiry = false,
  }) => _businessFeatures.updateBusinessChannelMember(
    channel.id,
    channel.channelType,
    userId,
    role: role,
    mutedUntil: mutedUntil,
    clearMute: clearMute,
    expiresAt: expiresAt,
    clearExpiry: clearExpiry,
  );

  Future<void> setBusinessChannelAccess(
    BusinessChannelSummary channel,
    String userId,
    String accessType,
    bool enabled, {
    String reason = '',
  }) => _businessFeatures.setBusinessChannelAccess(
    channel.id,
    channel.channelType,
    userId,
    accessType,
    enabled,
    reason: reason,
  );

  Future<List<BusinessChannelAccessSummary>> loadBusinessChannelAccess(
    BusinessChannelSummary channel, {
    String accessType = '',
  }) => _businessFeatures.businessChannelAccess(
    channel.id,
    channel.channelType,
    accessType: accessType,
  );

  Future<Conversation?> enterBusinessChannel(
    BusinessChannelSummary channel, {
    DateTime? expiresAt,
  }) async {
    if (!channel.subscribed) {
      await _businessFeatures.subscribeBusinessChannel(
        channel.id,
        channel.channelType,
        expiresAt: expiresAt,
      );
    }
    return _refreshAndFindBusinessConversation(channel.id);
  }

  Future<void> leaveBusinessChannel(BusinessChannelSummary channel) async {
    await _businessFeatures.unsubscribeBusinessChannel(
      channel.id,
      channel.channelType,
    );
    await refresh();
  }

  Future<Conversation?> _refreshAndFindBusinessConversation(
    String channelId,
  ) async {
    await repository.syncNow();
    conversations = await repository.conversations();
    _sortConversations();
    notifyListeners();
    return conversations.where((item) => item.id == channelId).firstOrNull;
  }

  Future<List<SupportSkillGroupSummary>> loadSupportSkillGroups() =>
      _businessFeatures.supportSkillGroups();

  Future<List<SupportAgentSummary>> loadSupportAgents({
    String skillGroupId = '',
  }) => _businessFeatures.supportAgents(skillGroupId: skillGroupId);

  Future<SupportAgentSummary> setSupportAgentStatus(String status) =>
      _businessFeatures.setSupportAgentStatus(status);

  Future<List<SupportSessionSummary>> loadSupportSessions({
    String status = '',
    String skillGroupId = '',
  }) => _businessFeatures.supportSessions(
    status: status,
    skillGroupId: skillGroupId,
  );

  Future<(SupportSessionSummary, Conversation?)> startSupportSession({
    required String skillGroupId,
    String subject = '',
  }) async {
    final session = await _businessFeatures.createSupportSession(
      skillGroupId: skillGroupId,
      subject: subject,
    );
    final conversation = await _refreshAndFindBusinessConversation(
      session.channelId,
    );
    return (session, conversation);
  }

  Future<Conversation?> enterSupportSession(SupportSessionSummary session) =>
      _refreshAndFindBusinessConversation(session.channelId);

  Future<SupportSessionSummary> claimSupportSession(String sessionId) =>
      _businessFeatures.claimSupportSession(sessionId);

  Future<SupportSessionSummary> transferSupportSession(
    String sessionId,
    String targetAgentId,
  ) => _businessFeatures.transferSupportSession(sessionId, targetAgentId);

  Future<SupportSessionSummary> endSupportSession(String sessionId) =>
      _businessFeatures.endSupportSession(sessionId);

  Future<SupportSessionSummary> rateSupportSession(
    String sessionId,
    int rating,
    String comment,
  ) => _businessFeatures.rateSupportSession(sessionId, rating, comment);

  Future<MomentPage> loadMoments({
    String authorId = '',
    String cursor = '',
    int limit = 20,
  }) => _businessFeatures.moments(
    authorId: authorId,
    cursor: cursor,
    limit: limit,
  );

  Future<MomentSummary> publishMoment({
    required String content,
    List<MediaUpload> uploads = const [],
    String visibility = 'public',
    List<String> visibleUserIds = const [],
    Map<String, Object?> location = const {},
    void Function(int index, double progress)? onProgress,
  }) async {
    if (uploads.length > 9) {
      throw const FormatException('朋友圈最多上传 9 张图片');
    }
    final videos = uploads
        .where((upload) => upload.kind == MessageContentKind.video)
        .length;
    if (videos > 0 && (videos != 1 || uploads.length != 1)) {
      throw const FormatException('朋友圈视频必须单独发布');
    }
    if (uploads.any(
      (upload) =>
          upload.kind != MessageContentKind.image &&
          upload.kind != MessageContentKind.video,
    )) {
      throw const FormatException('朋友圈只支持图片或单个视频');
    }
    final mediaIds = <String>[];
    for (var index = 0; index < uploads.length; index++) {
      final media = await _businessFeatures.uploadBusinessMedia(
        uploads[index],
        onProgress: (progress) => onProgress?.call(index, progress),
      );
      mediaIds.add(media.id);
    }
    return _businessFeatures.createMoment(
      content: content.trim(),
      mediaKind: uploads.isEmpty
          ? 'none'
          : videos == 1
          ? 'video'
          : 'images',
      mediaIds: mediaIds,
      visibility: visibility,
      visibleUserIds: visibleUserIds,
      location: location,
    );
  }

  Future<MomentSummary> toggleMomentLike(MomentSummary moment) =>
      _businessFeatures.setMomentLike(moment.id, !moment.likedByMe);

  Future<MomentCommentSummary> commentMoment(
    MomentSummary moment,
    String content, {
    String parentId = '',
  }) => _businessFeatures.createMomentComment(
    moment.id,
    content.trim(),
    parentId: parentId,
  );

  Future<void> removeMoment(MomentSummary moment) =>
      _businessFeatures.deleteMoment(moment.id);

  Future<void> removeMomentComment(
    MomentSummary moment,
    MomentCommentSummary comment,
  ) => _businessFeatures.deleteMomentComment(moment.id, comment.id);

  Future<List<MomentReminderSummary>> loadMomentReminders({int limit = 100}) =>
      _businessFeatures.momentReminders(limit: limit);

  Future<void> markMomentRemindersRead(List<int> reminderIds) =>
      _businessFeatures.markMomentRemindersRead(reminderIds);

  Future<List<StickerCategorySummary>> loadStickerCategories() =>
      _businessFeatures.stickerCategories();

  Future<List<StickerPackSummary>> loadStickerPacks({String categoryId = ''}) =>
      _businessFeatures.stickerPacks(categoryId: categoryId);

  Future<List<StickerItemSummary>> loadRecentStickers({int limit = 50}) =>
      _businessFeatures.recentStickers(limit: limit);

  Future<List<StickerItemSummary>> loadFavoriteStickers({int limit = 50}) =>
      _businessFeatures.favoriteStickers(limit: limit);

  Future<void> toggleStickerPackFavorite(StickerPackSummary pack) =>
      _businessFeatures.setStickerPackFavorite(pack.id, !pack.favorite);

  Future<void> toggleStickerFavorite(StickerItemSummary sticker) =>
      _businessFeatures.setStickerFavorite(sticker.id, !sticker.favorite);

  Future<List<AppUser>> searchUsers(
    String query, {
    String by = 'handle',
  }) async {
    try {
      return await repository.searchUsers(query, by: by);
    } catch (exception) {
      error = _messageFor(exception, fallback: '搜索失败，请稍后重试');
      notifyListeners();
      return const [];
    }
  }

  Future<UserSearchCapabilities?> loadSearchCapabilities() async {
    try {
      return await repository.searchCapabilities();
    } catch (exception) {
      error = _messageFor(exception, fallback: '搜索能力加载失败');
      notifyListeners();
      return null;
    }
  }

  Future<bool> markAnnouncementRead(AppAnnouncement announcement) async {
    if (!announcement.unread) return true;
    try {
      await repository.markAnnouncementRead(announcement.id);
      final index = announcements.indexWhere(
        (item) => item.id == announcement.id,
      );
      if (index >= 0) {
        announcements[index] = announcements[index].copyWith(
          readAt: DateTime.now(),
        );
      }
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '公告已读状态同步失败');
      notifyListeners();
      return false;
    }
  }

  Future<void> refreshAnnouncements() async {
    try {
      announcements = await repository.announcements();
      notifyListeners();
    } catch (exception) {
      error = _messageFor(exception, fallback: '公告刷新失败');
      notifyListeners();
    }
  }

  Future<void> loadMessages(String conversationId, {bool force = false}) async {
    if (!force && _messages.containsKey(conversationId)) return;
    messageLoading.add(conversationId);
    messageErrors.remove(conversationId);
    notifyListeners();
    try {
      final messages = await repository.messages(conversationId);
      _messages[conversationId] = messages;
      _scheduleDelivered(
        conversationId,
        messages.fold<int>(
          0,
          (highest, message) => max(highest, message.conversationSeq),
        ),
      );
      _hydrateLinkPreviews(conversationId);
    } catch (exception) {
      messageErrors[conversationId] = _messageFor(
        exception,
        fallback: '消息加载失败，请重试',
      );
      _messages.putIfAbsent(conversationId, () => []);
    } finally {
      messageLoading.remove(conversationId);
      notifyListeners();
    }
  }

  Future<void> loadScheduledMessages(
    String conversationId, {
    bool force = false,
  }) async {
    if (!force && _scheduledMessages.containsKey(conversationId)) return;
    scheduledMessageLoading.add(conversationId);
    scheduledMessageErrors.remove(conversationId);
    notifyListeners();
    try {
      final items = await repository.scheduledMessages(conversationId);
      items.sort((a, b) => a.scheduledAt.compareTo(b.scheduledAt));
      _scheduledMessages[conversationId] = items;
    } catch (exception) {
      scheduledMessageErrors[conversationId] = _messageFor(
        exception,
        fallback: '定时消息加载失败，请检查网络后重试',
      );
      _scheduledMessages.putIfAbsent(conversationId, () => []);
    } finally {
      scheduledMessageLoading.remove(conversationId);
      notifyListeners();
    }
  }

  Future<bool> scheduleMessage(
    String conversationId,
    String text,
    DateTime scheduledAt, {
    ChatMessage? replyTo,
    int? expiresInSeconds,
  }) async {
    final normalized = text.trim();
    if (normalized.isEmpty) return false;
    if (!scheduledAt.isAfter(DateTime.now().add(const Duration(seconds: 20)))) {
      error = '定时发送时间至少晚于当前时间 20 秒';
      notifyListeners();
      return false;
    }
    try {
      final item = await repository.scheduleMessage(
        conversationId,
        normalized,
        scheduledAt,
        replyToId: replyTo?.id,
        expiresInSeconds: expiresInSeconds,
      );
      final list = _scheduledMessages.putIfAbsent(conversationId, () => []);
      list.removeWhere((existing) => existing.id == item.id);
      list.add(item);
      list.sort((a, b) => a.scheduledAt.compareTo(b.scheduledAt));
      scheduledMessageErrors.remove(conversationId);
      notifyListeners();
      return true;
    } catch (exception) {
      scheduledMessageErrors[conversationId] = _messageFor(
        exception,
        fallback: '定时消息创建失败，请检查网络后重试',
      );
      notifyListeners();
      return false;
    }
  }

  Future<bool> cancelScheduledMessage(ScheduledMessage message) async {
    try {
      await repository.cancelScheduledMessage(message.id);
      _scheduledMessages[message.conversationId]?.removeWhere(
        (item) => item.id == message.id,
      );
      notifyListeners();
      return true;
    } catch (exception) {
      scheduledMessageErrors[message.conversationId] = _messageFor(
        exception,
        fallback: '取消定时消息失败，请检查网络后重试',
      );
      notifyListeners();
      return false;
    }
  }

  Future<bool> updateScheduledMessage(
    ScheduledMessage message, {
    required String text,
    required DateTime scheduledAt,
  }) async {
    final normalized = text.trim();
    if (normalized.isEmpty) {
      scheduledMessageErrors[message.conversationId] = '定时消息内容不能为空';
      notifyListeners();
      return false;
    }
    if (!scheduledAt.isAfter(DateTime.now().add(const Duration(seconds: 20)))) {
      scheduledMessageErrors[message.conversationId] = '定时发送时间至少晚于当前时间 20 秒';
      notifyListeners();
      return false;
    }
    try {
      final updated = await repository.updateScheduledMessage(
        message.id,
        text: normalized,
        scheduledAt: scheduledAt,
        expiresInSeconds: message.expiresInSeconds,
      );
      final list = _scheduledMessages.putIfAbsent(
        message.conversationId,
        () => [],
      );
      final index = list.indexWhere((item) => item.id == message.id);
      if (index < 0) {
        list.add(updated);
      } else {
        list[index] = updated;
      }
      list.sort((a, b) => a.scheduledAt.compareTo(b.scheduledAt));
      scheduledMessageErrors.remove(message.conversationId);
      notifyListeners();
      return true;
    } catch (exception) {
      scheduledMessageErrors[message.conversationId] = _messageFor(
        exception,
        fallback: '定时消息修改失败，请检查网络后重试',
      );
      notifyListeners();
      return false;
    }
  }

  Future<bool> retryScheduledMessage(ScheduledMessage message) async {
    if (!message.canRetry) return false;
    final created = await scheduleMessage(
      message.conversationId,
      message.text,
      DateTime.now().add(const Duration(minutes: 1)),
      expiresInSeconds: message.expiresInSeconds,
    );
    if (!created) return false;
    try {
      await repository.cancelScheduledMessage(message.id);
      _scheduledMessages[message.conversationId]?.removeWhere(
        (item) => item.id == message.id,
      );
    } catch (_) {
      // The replacement is durable; a stale failed row can be reconciled by reload.
    }
    notifyListeners();
    return true;
  }

  Future<ChatMessage?> sendMessage(
    String conversationId,
    String text, {
    ChatMessage? replyTo,
    List<MessageMention> mentions = const [],
    int? expiresInSeconds,
  }) async {
    final normalized = text.trim();
    if (normalized.isEmpty) return null;
    final clientId = _newClientMessageId();
    final pending = ChatMessage(
      id: 'local-$clientId',
      clientMessageId: clientId,
      conversationId: conversationId,
      senderId: currentUser?.id ?? 'me',
      senderName: currentUser?.name ?? '我',
      text: normalized,
      sentAt: DateTime.now(),
      isMine: true,
      kind: replyTo == null
          ? MessageContentKind.text
          : MessageContentKind.reply,
      replyToId: replyTo?.id,
      replyToText: replyTo?.text,
      mentions: mentions,
      status: MessageStatus.sending,
      expiresAt: expiresInSeconds == null
          ? null
          : DateTime.now().add(Duration(seconds: expiresInSeconds)),
    );
    final list = _messages.putIfAbsent(conversationId, () => []);
    list.add(pending);
    _updateConversation(conversationId, normalized);
    await repository.persistMessages(conversationId, list);
    notifyListeners();
    return _sendPending(pending);
  }

  Future<ChatMessage> sendMedia(
    String conversationId,
    MediaUpload upload, {
    ChatMessage? replyTo,
  }) async {
    final clientId = _newClientMessageId();
    final label = switch (upload.kind) {
      MessageContentKind.image => '[图片]',
      MessageContentKind.voice => '[语音]',
      MessageContentKind.video => '[视频]',
      _ => '[文件] ${upload.fileName}',
    };
    final pending = ChatMessage(
      id: 'local-$clientId',
      clientMessageId: clientId,
      conversationId: conversationId,
      senderId: currentUser?.id ?? 'me',
      senderName: currentUser?.name ?? '我',
      text: label,
      sentAt: DateTime.now(),
      isMine: true,
      kind: upload.kind,
      mediaUrl: upload.localPath,
      fileName: upload.fileName,
      mimeType: upload.mimeType,
      durationSeconds: upload.durationSeconds,
      replyToId: replyTo?.id,
      replyToText: replyTo?.text,
      status: MessageStatus.sending,
    );
    _pendingMedia[clientId] = upload;
    _mediaUploadProgress[clientId] = 0;
    final list = _messages.putIfAbsent(conversationId, () => []);
    list.add(pending);
    _updateConversation(conversationId, label);
    await repository.persistMessages(conversationId, list);
    notifyListeners();
    return _sendPending(pending);
  }

  Future<ChatMessage> sendSticker(
    String conversationId,
    StickerItemSummary sticker,
  ) async {
    final clientId = _newClientMessageId();
    final pending = ChatMessage(
      id: 'local-$clientId',
      clientMessageId: clientId,
      conversationId: conversationId,
      senderId: currentUser?.id ?? 'me',
      senderName: currentUser?.name ?? '我',
      text: sticker.name.isEmpty ? '[表情]' : '[表情] ${sticker.name}',
      sentAt: DateTime.now(),
      isMine: true,
      kind: MessageContentKind.sticker,
      stickerId: sticker.id,
      mediaId: sticker.mediaId,
      mediaUrl: sticker.url,
      fileName: sticker.name,
      mimeType: sticker.mime,
      status: MessageStatus.sending,
    );
    final list = _messages.putIfAbsent(conversationId, () => []);
    list.add(pending);
    _updateConversation(conversationId, pending.text);
    await repository.persistMessages(conversationId, list);
    notifyListeners();
    final sent = await _sendPending(pending);
    if (sent.status != MessageStatus.failed) {
      await _businessFeatures.recordStickerUse(sticker.id);
    }
    return sent;
  }

  Future<ChatMessage> sendMomentShare(
    String conversationId,
    MomentSummary moment,
  ) async {
    final clientId = _newClientMessageId();
    final preview = moment.content.trim().isEmpty
        ? '[朋友圈]'
        : '[朋友圈] ${moment.content.trim()}';
    final pending = ChatMessage(
      id: 'local-$clientId',
      clientMessageId: clientId,
      conversationId: conversationId,
      senderId: currentUser?.id ?? 'me',
      senderName: currentUser?.name ?? '我',
      text: preview,
      sentAt: DateTime.now(),
      isMine: true,
      kind: MessageContentKind.momentShare,
      momentId: moment.id,
      mediaId: moment.media.firstOrNull?.id,
      mediaUrl: moment.media.firstOrNull?.url,
      mimeType: moment.media.firstOrNull?.mime,
      status: MessageStatus.sending,
    );
    final list = _messages.putIfAbsent(conversationId, () => []);
    list.add(pending);
    _updateConversation(conversationId, preview);
    await repository.persistMessages(conversationId, list);
    notifyListeners();
    return _sendPending(pending);
  }

  Future<ChatMessage> sendLiveEvent(
    String conversationId, {
    required String event,
    required String label,
    Map<String, Object?> data = const {},
  }) async {
    final conversation = conversations
        .where((item) => item.id == conversationId)
        .firstOrNull;
    if (conversation?.channelType != 9) {
      throw const FormatException('直播互动只能发送到直播频道');
    }
    const allowedEvents = {'live.like', 'live.applause', 'live.follow'};
    if (!allowedEvents.contains(event)) {
      throw const FormatException('不支持的直播互动事件');
    }
    final normalizedLabel = label.trim();
    if (normalizedLabel.isEmpty) {
      throw const FormatException('直播互动文案不能为空');
    }
    final clientId = _newClientMessageId();
    final pending = ChatMessage(
      id: 'local-$clientId',
      clientMessageId: clientId,
      conversationId: conversationId,
      senderId: currentUser?.id ?? 'me',
      senderName: currentUser?.name ?? '我',
      text: normalizedLabel,
      sentAt: DateTime.now(),
      isMine: true,
      kind: MessageContentKind.liveEvent,
      event: event,
      eventData: data,
      status: MessageStatus.sending,
    );
    final list = _messages.putIfAbsent(conversationId, () => []);
    list.add(pending);
    _updateConversation(conversationId, normalizedLabel);
    await repository.persistMessages(conversationId, list);
    notifyListeners();
    return _sendPending(pending);
  }

  Future<ChatMessage> sendScreenshotNotice(String conversationId) async {
    final clientId = _newClientMessageId();
    final actor = currentUser?.name.trim();
    final text = '${actor == null || actor.isEmpty ? '当前用户' : actor} 截取了聊天界面';
    final pending = ChatMessage(
      id: 'local-$clientId',
      clientMessageId: clientId,
      conversationId: conversationId,
      senderId: currentUser?.id ?? 'me',
      senderName: currentUser?.name ?? '我',
      text: text,
      sentAt: DateTime.now(),
      isMine: true,
      kind: MessageContentKind.screenshotNotice,
      status: MessageStatus.sending,
    );
    final list = _messages.putIfAbsent(conversationId, () => []);
    list.add(pending);
    _updateConversation(conversationId, '[截屏提示]');
    await repository.persistMessages(conversationId, list);
    notifyListeners();
    return _sendPending(pending);
  }

  Future<ChatMessage> sendContact(
    String conversationId,
    AppUser contact, {
    ChatMessage? replyTo,
  }) async {
    final clientId = _newClientMessageId();
    final label = '[名片] ${contact.name}';
    final pending = ChatMessage(
      id: 'local-$clientId',
      clientMessageId: clientId,
      conversationId: conversationId,
      senderId: currentUser?.id ?? 'me',
      senderName: currentUser?.name ?? '我',
      text: label,
      sentAt: DateTime.now(),
      isMine: true,
      kind: MessageContentKind.contact,
      contactUserId: contact.id,
      contactName: contact.name,
      contactHandle: contact.handle,
      contactAvatarUrl: contact.avatarUrl,
      replyToId: replyTo?.id,
      replyToText: replyTo?.text,
      status: MessageStatus.sending,
    );
    final list = _messages.putIfAbsent(conversationId, () => []);
    list.add(pending);
    _updateConversation(conversationId, label);
    await repository.persistMessages(conversationId, list);
    notifyListeners();
    return _sendPending(pending);
  }

  Future<ChatMessage> sendLocation(
    String conversationId, {
    required double latitude,
    required double longitude,
    required String name,
    String? address,
    ChatMessage? replyTo,
  }) async {
    final normalizedName = name.trim();
    if (normalizedName.isEmpty) {
      throw const FormatException('位置名称不能为空');
    }
    final clientId = _newClientMessageId();
    final label = '[位置] $normalizedName';
    final pending = ChatMessage(
      id: 'local-$clientId',
      clientMessageId: clientId,
      conversationId: conversationId,
      senderId: currentUser?.id ?? 'me',
      senderName: currentUser?.name ?? '我',
      text: label,
      sentAt: DateTime.now(),
      isMine: true,
      kind: MessageContentKind.location,
      latitude: latitude,
      longitude: longitude,
      locationName: normalizedName,
      locationAddress: address?.trim(),
      replyToId: replyTo?.id,
      replyToText: replyTo?.text,
      status: MessageStatus.sending,
    );
    final list = _messages.putIfAbsent(conversationId, () => []);
    list.add(pending);
    _updateConversation(conversationId, label);
    await repository.persistMessages(conversationId, list);
    notifyListeners();
    return _sendPending(pending);
  }

  Future<ChatMessage> _sendPending(ChatMessage pending) async {
    final list = _messages[pending.conversationId]!;
    try {
      var upload = _pendingMedia[pending.clientMessageId];
      if (upload == null &&
          (pending.kind == MessageContentKind.image ||
              pending.kind == MessageContentKind.file ||
              pending.kind == MessageContentKind.video ||
              pending.kind == MessageContentKind.voice)) {
        final path = pending.mediaUrl;
        if (path == null || !await File(path).exists()) {
          throw const FileSystemException('本地文件已不可用，请重新选择');
        }
        upload = MediaUpload(
          bytes: await File(path).readAsBytes(),
          fileName: pending.fileName ?? path.split(Platform.pathSeparator).last,
          mimeType: pending.mimeType ?? 'application/octet-stream',
          kind: pending.kind,
          localPath: path,
          durationSeconds: pending.durationSeconds,
        );
        _pendingMedia[pending.clientMessageId] = upload;
      }
      final sent = upload == null
          ? await repository.send(pending)
          : await repository.sendMedia(
              pending,
              upload,
              onProgress: (progress) {
                _mediaUploadProgress[pending.clientMessageId] = progress.clamp(
                  0,
                  1,
                );
                notifyListeners();
              },
            );
      _replaceMessage(pending.conversationId, pending.clientMessageId, sent);
      _pendingMedia.remove(pending.clientMessageId);
      _mediaUploadProgress.remove(pending.clientMessageId);
      await repository.persistMessages(pending.conversationId, list);
      notifyListeners();
      _hydrateLinkPreview(sent);
      return sent;
    } catch (_) {
      _mediaUploadProgress.remove(pending.clientMessageId);
      final failed = pending.copyWith(status: MessageStatus.failed);
      _replaceMessage(pending.conversationId, pending.clientMessageId, failed);
      await repository.persistMessages(pending.conversationId, list);
      notifyListeners();
      return failed;
    }
  }

  Future<void> retryMessage(ChatMessage message) async {
    if (message.status != MessageStatus.failed) return;
    final sending = message.copyWith(status: MessageStatus.sending);
    _replaceMessage(message.conversationId, message.clientMessageId, sending);
    notifyListeners();
    await _sendPending(sending);
  }

  Future<bool> editMessage(ChatMessage message, String text) async {
    final normalized = text.trim();
    if (normalized.isEmpty ||
        !message.isMine ||
        message.id.startsWith('local-') ||
        (message.kind != MessageContentKind.text &&
            message.kind != MessageContentKind.reply)) {
      return false;
    }
    try {
      final edited = await repository.editMessage(message.id, normalized);
      _replaceMessage(message.conversationId, message.clientMessageId, edited);
      await repository.persistMessages(
        message.conversationId,
        _messages[message.conversationId]!,
      );
      _updateConversation(message.conversationId, edited.text);
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '消息编辑失败，请稍后重试');
      notifyListeners();
      return false;
    }
  }

  Future<List<MessageEditRevision>?> loadMessageEditHistory(
    ChatMessage message,
  ) async {
    if (message.id.startsWith('local-') || message.editedAt == null) {
      return const [];
    }
    try {
      return await repository.messageEditHistory(message.id);
    } catch (exception) {
      error = _messageFor(exception, fallback: '编辑记录加载失败，请稍后重试');
      notifyListeners();
      return null;
    }
  }

  Future<bool> toggleReaction(ChatMessage message, String emoji) async {
    if (message.id.startsWith('local-') ||
        message.status == MessageStatus.recalled) {
      return false;
    }
    final current = message.reactions
        .where((reaction) => reaction.emoji == emoji)
        .firstOrNull;
    try {
      final updated = await repository.setMessageReaction(
        message.id,
        emoji,
        active: !(current?.reactedByMe ?? false),
      );
      _replaceMessage(message.conversationId, message.clientMessageId, updated);
      await repository.persistMessages(
        message.conversationId,
        _messages[message.conversationId]!,
      );
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '消息回应失败，请稍后重试');
      notifyListeners();
      return false;
    }
  }

  Future<bool> toggleMessagePinned(ChatMessage message) async {
    if (message.id.startsWith('local-') ||
        message.status == MessageStatus.recalled) {
      return false;
    }
    try {
      final next = !message.isPinned;
      await repository.setMessagePinned(
        message.conversationId,
        message.id,
        pinned: next,
      );
      _replaceMessage(
        message.conversationId,
        message.clientMessageId,
        message.copyWith(
          isPinned: next,
          pinnedAt: next ? DateTime.now() : message.pinnedAt,
          pinnedBy: next ? currentUser?.id : message.pinnedBy,
        ),
      );
      await repository.persistMessages(
        message.conversationId,
        _messages[message.conversationId]!,
      );
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '置顶状态更新失败，请稍后重试');
      notifyListeners();
      return false;
    }
  }

  Future<List<ChatMessage>> loadPinnedMessages(String conversationId) async {
    try {
      return await repository.pinnedMessages(conversationId);
    } catch (exception) {
      error = _messageFor(exception, fallback: '置顶消息加载失败');
      notifyListeners();
      rethrow;
    }
  }

  Future<List<ChatMessage>> searchConversationMessages(
    String conversationId,
    String query,
  ) async {
    final normalized = query.trim().toLowerCase();
    if (normalized.isEmpty) return const [];
    final local = (_messages[conversationId] ?? const <ChatMessage>[]).where(
      (message) => message.text.toLowerCase().contains(normalized),
    );
    try {
      final remote = await repository.searchMessages(
        conversationId,
        query.trim(),
      );
      final merged = <String, ChatMessage>{};
      for (final message in [...remote, ...local]) {
        merged[message.id] = message;
      }
      final result = merged.values.toList()
        ..sort((a, b) => b.sentAt.compareTo(a.sentAt));
      return result;
    } catch (exception) {
      error = _messageFor(exception, fallback: '云端搜索失败，已显示本机结果');
      notifyListeners();
      return local.toList()..sort((a, b) => b.sentAt.compareTo(a.sentAt));
    }
  }

  Future<void> recallMessage(ChatMessage message) async {
    if (!message.isMine || message.id.startsWith('local-')) return;
    try {
      await repository.recallMessage(message.id);
      _replaceMessage(
        message.conversationId,
        message.clientMessageId,
        message.copyWith(text: '', status: MessageStatus.recalled),
      );
      await repository.persistMessages(
        message.conversationId,
        _messages[message.conversationId]!,
      );
      notifyListeners();
    } catch (exception) {
      error = _messageFor(exception, fallback: '撤回失败，可能已超过可撤回时间');
      notifyListeners();
    }
  }

  Future<void> deleteMessage(ChatMessage message) async {
    final list = _messages[message.conversationId];
    list?.removeWhere(
      (item) => item.clientMessageId == message.clientMessageId,
    );
    if (list != null) {
      await repository.persistMessages(message.conversationId, list);
    }
    notifyListeners();
  }

  Future<void> deleteMessages(
    String conversationId,
    Set<String> clientMessageIds,
  ) async {
    final list = _messages[conversationId];
    if (list == null) return;
    list.removeWhere(
      (message) => clientMessageIds.contains(message.clientMessageId),
    );
    for (final id in clientMessageIds) {
      _pendingMedia.remove(id);
    }
    await repository.persistMessages(conversationId, list);
    notifyListeners();
  }

  Future<bool> favoriteMessage(ChatMessage message) async {
    try {
      await repository.saveFavorite(message);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '收藏失败，请稍后重试');
      notifyListeners();
      return false;
    }
  }

  Future<bool> removeFavorite(ChatMessage message) async {
    try {
      await repository.removeFavorite(message);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '取消收藏失败，请稍后重试');
      notifyListeners();
      return false;
    }
  }

  Future<List<ChatMessage>> forwardMessages(
    List<ChatMessage> messages,
    String conversationId, {
    required String mode,
  }) async {
    final sourceIds = messages
        .where(
          (item) =>
              !item.id.startsWith('local-') &&
              item.status != MessageStatus.recalled,
        )
        .map((item) => item.id)
        .toList();
    if (sourceIds.isEmpty) return const [];
    try {
      final forwarded = await repository.forwardMessages(
        conversationId,
        sourceIds,
        mode: mode,
        clientBatchId: _newClientMessageId(),
      );
      final list = _messages[conversationId];
      if (list != null) {
        for (final item in forwarded) {
          if (!list.any((existing) => existing.id == item.id)) list.add(item);
        }
        await repository.persistMessages(conversationId, list);
      }
      if (forwarded.isNotEmpty) {
        _updateConversation(conversationId, forwarded.last.text);
      }
      notifyListeners();
      return forwarded;
    } catch (exception) {
      error = _messageFor(exception, fallback: '转发失败，请稍后重试');
      notifyListeners();
      return const [];
    }
  }

  Future<ChatMessage?> forwardMessage(
    ChatMessage message,
    String conversationId,
  ) async {
    final forwarded = await forwardMessages(
      [message],
      conversationId,
      mode: 'separate',
    );
    return forwarded.firstOrNull;
  }

  Future<void> clearLocalMessages(String conversationId) async {
    _messages[conversationId] = [];
    await repository.persistMessages(conversationId, const []);
    notifyListeners();
  }

  Future<void> markRead(String id) async {
    final index = conversations.indexWhere(
      (conversation) => conversation.id == id,
    );
    if (index < 0) return;
    final conversation = conversations[index];
    conversations[index] = conversation.copyWith(
      unread: 0,
      lastReadSeq: conversation.lastMessageSeq,
      mentionUnreadCount: conversation.mentionUnreadCount == null ? null : 0,
    );
    notifyListeners();
    try {
      await repository.markRead(id, conversation.lastMessageSeq);
    } catch (_) {
      // The durable sync cursor will reconcile this on reconnect.
    }
  }

  void _scheduleDelivered(String conversationId, int sequence) {
    if (sequence <= (_lastDeliveredSeq[conversationId] ?? 0)) return;
    _pendingDeliveredSeq[conversationId] = max(
      sequence,
      _pendingDeliveredSeq[conversationId] ?? 0,
    );
    _deliveryTimers[conversationId]?.cancel();
    _deliveryTimers[conversationId] = Timer(
      const Duration(milliseconds: 160),
      () => unawaited(_flushDelivered(conversationId)),
    );
  }

  Future<void> _flushDelivered(String conversationId) async {
    _deliveryTimers.remove(conversationId)?.cancel();
    final sequence = _pendingDeliveredSeq[conversationId] ?? 0;
    if (sequence <= (_lastDeliveredSeq[conversationId] ?? 0)) {
      _pendingDeliveredSeq.remove(conversationId);
      return;
    }
    try {
      await repository.markDelivered(conversationId, sequence);
      _lastDeliveredSeq[conversationId] = sequence;
      if ((_pendingDeliveredSeq[conversationId] ?? 0) <= sequence) {
        _pendingDeliveredSeq.remove(conversationId);
      }
    } catch (_) {
      // Keep the highest pending sequence; reconnect will retry monotonically.
    }
  }

  Future<void> _flushPendingDeliveries() async {
    for (final conversationId in _pendingDeliveredSeq.keys.toList()) {
      await _flushDelivered(conversationId);
    }
  }

  void setActiveConversation(String? conversationId) {
    if (_disposed) return;
    final previous = _activeConversationId;
    if (previous != null && previous != conversationId) {
      updateTyping(previous, false);
    }
    _activeConversationId = conversationId;
    if (conversationId != null) unawaited(markRead(conversationId));
  }

  void updateTyping(String conversationId, bool typing) {
    if (_disposed || !authenticated || conversationId.isEmpty) return;
    _typingStopTimers.remove(conversationId)?.cancel();
    if (!typing) {
      _lastTypingSent.remove(conversationId);
      if (!_typingAnnounced.remove(conversationId)) return;
      unawaited(_sendTyping(conversationId, false));
      return;
    }
    _typingStopTimers[conversationId] = Timer(
      const Duration(seconds: 4),
      () => updateTyping(conversationId, false),
    );
    final now = DateTime.now();
    final lastSent = _lastTypingSent[conversationId];
    if (_typingAnnounced.contains(conversationId) &&
        lastSent != null &&
        now.difference(lastSent) < const Duration(seconds: 3)) {
      return;
    }
    _typingAnnounced.add(conversationId);
    _lastTypingSent[conversationId] = now;
    unawaited(_sendTyping(conversationId, true));
  }

  Future<void> _sendTyping(String conversationId, bool typing) async {
    try {
      await repository.setTyping(conversationId, typing);
    } catch (_) {
      if (typing) {
        _typingAnnounced.remove(conversationId);
        _lastTypingSent.remove(conversationId);
      }
      // Typing is an ephemeral hint and must never block message composition.
    }
  }

  Future<void> registerPushDevice({
    required String deviceId,
    required String platform,
    required String cid,
    required bool notificationsEnabled,
    required bool previewEnabled,
    required bool soundEnabled,
    required bool vibrationEnabled,
  }) async {
    if (!authenticated || cid.trim().isEmpty) return;
    await repository.registerDevice(
      deviceId: deviceId,
      platform: platform,
      provider: 'getui',
      pushToken: cid.trim(),
      notificationsEnabled: notificationsEnabled,
      previewEnabled: previewEnabled,
      soundEnabled: soundEnabled,
      vibrationEnabled: vibrationEnabled,
    );
    _pushDeviceId = deviceId;
  }

  Future<void> registerVoipPushDevice({
    required String deviceId,
    required String token,
    required bool notificationsEnabled,
    required bool previewEnabled,
    required bool soundEnabled,
    required bool vibrationEnabled,
  }) async {
    if (!authenticated || token.trim().isEmpty) return;
    await repository.registerDevice(
      deviceId: deviceId,
      platform: 'ios',
      provider: 'apns_voip',
      pushToken: token.trim(),
      notificationsEnabled: notificationsEnabled,
      previewEnabled: previewEnabled,
      soundEnabled: soundEnabled,
      vibrationEnabled: vibrationEnabled,
    );
    _voipPushDeviceId = deviceId;
  }

  void refreshPushConfiguration() => notifyListeners();

  void handlePushPayload(Map<String, dynamic> payload) {
    final normalized = _flattenPushPayload(payload);
    unawaited(callController?.handlePushPayload(normalized));
    final conversationId =
        normalized['conversationId']?.toString() ??
        normalized['conversation_id']?.toString();
    if (conversationId != null && conversationId.isNotEmpty) {
      _pendingConversationId = conversationId;
    }
    unawaited(refresh());
    notifyListeners();
  }

  void clearPendingConversationId(String value) {
    if (_pendingConversationId != value) return;
    _pendingConversationId = null;
  }

  Future<bool> markUnread(String id) => _updateConversationPreferences(
    id,
    manualUnread: true,
    localUpdate: (conversation) =>
        conversation.copyWith(unread: max(1, conversation.unread)),
    fallback: '标为未读失败，请稍后重试',
  );

  Future<bool> toggleConversationPinned(String id) {
    final conversation = conversations.firstWhere(
      (item) => item.id == id,
      orElse: () => throw StateError('conversation not found'),
    );
    return _updateConversationPreferences(
      id,
      pinned: !conversation.pinned,
      localUpdate: (item) => item.copyWith(pinned: !item.pinned),
      fallback: '置顶状态更新失败，请稍后重试',
    );
  }

  Future<bool> toggleConversationMuted(String id) {
    final conversation = conversations.firstWhere(
      (item) => item.id == id,
      orElse: () => throw StateError('conversation not found'),
    );
    return _updateConversationPreferences(
      id,
      notificationsMuted: !conversation.muted,
      localUpdate: (item) => item.copyWith(muted: !item.muted),
      fallback: '消息免打扰更新失败，请稍后重试',
    );
  }

  Future<bool> toggleConversationArchived(String id) {
    final conversation = conversations.firstWhere(
      (item) => item.id == id,
      orElse: () => throw StateError('conversation not found'),
    );
    return _updateConversationPreferences(
      id,
      archived: !conversation.archived,
      localUpdate: (item) => item.copyWith(archived: !item.archived),
      fallback: conversation.archived ? '恢复会话失败，请稍后重试' : '归档会话失败，请稍后重试',
    );
  }

  Future<bool> hideConversation(String id) async {
    final index = conversations.indexWhere((item) => item.id == id);
    if (index < 0) return false;
    final removed = conversations.removeAt(index);
    notifyListeners();
    try {
      await repository.hideConversation(id);
      return true;
    } catch (exception) {
      conversations.add(removed);
      _sortConversations();
      error = _messageFor(exception, fallback: '删除会话失败，请稍后重试');
      notifyListeners();
      return false;
    }
  }

  Future<bool> _updateConversationPreferences(
    String id, {
    bool? pinned,
    bool? notificationsMuted,
    bool? manualUnread,
    bool? archived,
    required Conversation Function(Conversation conversation) localUpdate,
    required String fallback,
  }) async {
    final index = conversations.indexWhere((item) => item.id == id);
    if (index < 0) return false;
    final previous = conversations[index];
    conversations[index] = localUpdate(previous);
    _sortConversations();
    notifyListeners();
    try {
      await repository.updateConversationPreferences(
        id,
        pinned: pinned,
        notificationsMuted: notificationsMuted,
        manualUnread: manualUnread,
        archived: archived,
      );
      return true;
    } catch (exception) {
      final rollbackIndex = conversations.indexWhere((item) => item.id == id);
      if (rollbackIndex >= 0) conversations[rollbackIndex] = previous;
      _sortConversations();
      error = _messageFor(exception, fallback: fallback);
      notifyListeners();
      return false;
    }
  }

  Future<Conversation?> createDirect(AppUser user) async {
    try {
      final conversation = await repository.createDirect(user);
      _upsertConversation(conversation);
      notifyListeners();
      return conversation;
    } catch (exception) {
      error = _messageFor(exception, fallback: '无法创建单聊');
      notifyListeners();
      return null;
    }
  }

  Future<Conversation?> createGroup(String name, List<AppUser> members) async {
    try {
      final conversation = await repository.createGroup(name.trim(), members);
      _upsertConversation(conversation);
      notifyListeners();
      return conversation;
    } catch (exception) {
      error = _messageFor(exception, fallback: '群聊创建失败');
      notifyListeners();
      return null;
    }
  }

  Future<bool> acceptRequest(FriendRequest request) async {
    try {
      await repository.acceptFriendRequest(request.id);
      _replaceFriendRequest(request, 'accepted');
      contacts = await repository.contacts();
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '接受好友申请失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> rejectRequest(FriendRequest request) async {
    try {
      await repository.rejectFriendRequest(request.id);
      _replaceFriendRequest(request, 'rejected');
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '拒绝好友申请失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> cancelRequest(FriendRequest request) async {
    try {
      await repository.cancelFriendRequest(request.id);
      _replaceFriendRequest(request, 'cancelled');
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '撤回好友申请失败');
      notifyListeners();
      return false;
    }
  }

  void _replaceFriendRequest(FriendRequest request, String status) {
    final index = requests.indexWhere((item) => item.id == request.id);
    if (index >= 0) requests[index] = request.copyWith(status: status);
  }

  Future<bool> sendFriendRequest(
    AppUser user,
    String note, {
    String source = 'search',
    String? sourceId,
  }) async {
    try {
      await repository.sendFriendRequest(
        user.id,
        note.trim(),
        source: source,
        sourceId: sourceId,
      );
      requests = await repository.friendRequests();
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '好友申请发送失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> respondGroupInvitation(
    GroupInvitation invitation,
    String action,
  ) async {
    try {
      await repository.respondGroupInvitation(invitation.id, action);
      final index = groupInvitations.indexWhere(
        (item) => item.id == invitation.id,
      );
      if (index >= 0) {
        groupInvitations[index] = invitation.copyWith(
          status: switch (action) {
            'accept' => 'accepted',
            'reject' => 'rejected',
            _ => 'cancelled',
          },
        );
      }
      if (action == 'accept') await refresh();
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '群邀请处理失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> updateFriendMetadata(
    AppUser user, {
    required String remark,
    required List<String> tags,
  }) async {
    try {
      await repository.updateFriendMetadata(
        user.id,
        remark: remark.trim(),
        tags: tags,
      );
      final index = contacts.indexWhere((item) => item.id == user.id);
      if (index >= 0) {
        contacts[index] = contacts[index].copyWith(
          remark: remark.trim(),
          tags: tags,
        );
      }
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '备注与标签保存失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> deleteFriend(AppUser user) async {
    try {
      await repository.deleteFriend(user.id);
      contacts.removeWhere((item) => item.id == user.id);
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '删除联系人失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> blockUser(AppUser user, bool blocked) async {
    try {
      await repository.blockUser(user.id, blocked);
      if (blocked) contacts.removeWhere((item) => item.id == user.id);
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '黑名单更新失败');
      notifyListeners();
      return false;
    }
  }

  Future<List<AppUser>> loadBlockedUsers() async {
    try {
      return await repository.blockedUsers();
    } catch (exception) {
      error = _messageFor(exception, fallback: '黑名单加载失败');
      notifyListeners();
      return const [];
    }
  }

  Future<GroupProfile?> loadGroupProfile(String conversationId) async {
    try {
      return await repository.groupProfile(conversationId);
    } catch (exception) {
      error = _messageFor(exception, fallback: '群资料加载失败');
      notifyListeners();
      return null;
    }
  }

  Future<List<GroupMember>?> loadGroupMembers(String conversationId) async {
    try {
      return await repository.groupMembers(conversationId);
    } catch (exception) {
      error = _messageFor(exception, fallback: '群成员加载失败');
      notifyListeners();
      return null;
    }
  }

  Future<GroupProfile?> updateGroupProfile(
    String conversationId, {
    String? name,
    MediaUpload? avatar,
    String? joinPolicy,
    bool? allowMemberAddFriend,
    bool rotateQr = false,
  }) async {
    try {
      final avatarMediaId = avatar == null
          ? null
          : await repository.uploadAvatar(avatar);
      final profile = await repository.updateGroupProfile(
        conversationId,
        name: name,
        avatarMediaId: avatarMediaId,
        joinPolicy: joinPolicy,
        allowMemberAddFriend: allowMemberAddFriend,
        rotateQr: rotateQr,
      );
      if (name != null || avatarMediaId != null) {
        final index = conversations.indexWhere(
          (item) => item.id == conversationId,
        );
        if (index >= 0) {
          conversations[index] = conversations[index].copyWith(
            title: name,
            avatarUrl: profile.avatarUrl,
          );
        }
      }
      notifyListeners();
      return profile;
    } catch (exception) {
      error = _messageFor(exception, fallback: '群资料保存失败');
      notifyListeners();
      return null;
    }
  }

  Future<GroupProfile?> saveGroupAnnouncement(
    String conversationId,
    String content,
  ) async {
    try {
      return await repository.setGroupAnnouncement(
        conversationId,
        content.trim(),
      );
    } catch (exception) {
      error = _messageFor(exception, fallback: '群公告发布失败');
      notifyListeners();
      return null;
    }
  }

  Future<bool> markGroupAnnouncementRead(String conversationId) async {
    try {
      await repository.markGroupAnnouncementRead(conversationId);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '群公告已读状态同步失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> addGroupMembers(
    String conversationId,
    List<AppUser> users,
  ) async {
    try {
      await repository.addGroupMembers(
        conversationId,
        users.map((user) => user.id).toList(),
      );
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '添加群成员失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> inviteGroupMember(String conversationId, AppUser user) async {
    try {
      await repository.inviteGroupMember(conversationId, user.id);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '群邀请发送失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> joinGroupByQr(String token) async {
    try {
      await repository.joinGroupByQr(token);
      await refresh();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '加入群聊失败，请确认二维码仍在有效期内');
      notifyListeners();
      return false;
    }
  }

  Future<bool> removeGroupMember(String conversationId, AppUser user) async {
    try {
      await repository.removeGroupMember(conversationId, user.id);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '移除群成员失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> setGroupRole(
    String conversationId,
    AppUser user,
    String role,
  ) async {
    try {
      await repository.setGroupRole(conversationId, user.id, role);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '群管理员设置失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> setGroupMemberMuted(
    String conversationId,
    AppUser user,
    DateTime? until,
  ) async {
    try {
      await repository.setGroupMemberMuted(conversationId, user.id, until);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '群成员禁言设置失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> transferGroupOwner(String conversationId, AppUser user) async {
    try {
      await repository.transferGroupOwner(conversationId, user.id);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '群主转让失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> setGroupNickname(String conversationId, String nickname) async {
    try {
      await repository.setGroupNickname(conversationId, nickname.trim());
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '群昵称保存失败');
      notifyListeners();
      return false;
    }
  }

  Future<GroupProfile?> setGroupAllMuted(
    String conversationId,
    bool muted,
  ) async {
    try {
      return await repository.setGroupAllMuted(conversationId, muted);
    } catch (exception) {
      error = _messageFor(exception, fallback: '全员禁言设置失败');
      notifyListeners();
      return null;
    }
  }

  Future<bool> leaveGroup(String conversationId) async {
    try {
      await repository.leaveGroup(conversationId);
      conversations.removeWhere((item) => item.id == conversationId);
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '退出群聊失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> disbandGroup(String conversationId, String reason) async {
    try {
      await repository.disbandGroup(conversationId, reason.trim());
      conversations.removeWhere((item) => item.id == conversationId);
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '解散群聊失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> reportTarget({
    required String targetType,
    required String targetId,
    required String reason,
    String details = '',
  }) async {
    try {
      await repository.report(
        targetType: targetType,
        targetId: targetId,
        reason: reason,
        details: details,
      );
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '举报提交失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> refreshProfile() async {
    try {
      currentUser = await repository.profile();
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '个人资料加载失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> saveProfile({
    required String name,
    required String handle,
    required String signature,
    MediaUpload? avatar,
  }) async {
    try {
      final avatarMediaId = avatar == null
          ? null
          : await repository.uploadAvatar(avatar);
      currentUser = await repository.updateProfile(
        name: name.trim(),
        handle: handle.trim().toLowerCase(),
        signature: signature.trim(),
        avatarMediaId: avatarMediaId,
      );
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '个人资料保存失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> requestPhoneUpdateCode(String phone) async {
    try {
      await repository.requestPhoneChangeCode(phone.trim());
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '验证码发送失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> updatePhone(String phone, String code) async {
    try {
      currentUser = await repository.updatePhone(phone.trim(), code.trim());
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '手机号换绑失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> requestAccountDeletionCode() async {
    try {
      await repository.requestAccountDeletionCode();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '注销验证码发送失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> deleteAccount(String code) async {
    loading = true;
    notifyListeners();
    try {
      await repository.deleteAccount(code.trim());
      await _clearAuthenticatedState();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '账号注销失败，请稍后重试');
      loading = false;
      notifyListeners();
      return false;
    }
  }

  Future<List<UserDevice>> loadUserDevices() async {
    try {
      return await repository.userDevices();
    } catch (exception) {
      error = _messageFor(exception, fallback: '登录设备加载失败');
      notifyListeners();
      return const [];
    }
  }

  Future<List<ImDeviceSession>> loadImDeviceSessions() async {
    try {
      return await repository.imDeviceSessions();
    } catch (exception) {
      error = _messageFor(exception, fallback: '登录会话加载失败');
      notifyListeners();
      return const [];
    }
  }

  Future<bool> removeUserDevice(String deviceId) async {
    try {
      await repository.removeUserDevice(deviceId);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '设备退出失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> quitImDeviceSession(int deviceFlag) async {
    try {
      await repository.quitImDeviceSession(deviceFlag);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '会话下线失败');
      notifyListeners();
      return false;
    }
  }

  Future<List<ChatMessage>> loadFavorites() async {
    try {
      return await repository.favorites();
    } catch (exception) {
      error = _messageFor(exception, fallback: '收藏加载失败');
      notifyListeners();
      return const [];
    }
  }

  Future<bool> submitFeedback({
    required String category,
    required String content,
    String contact = '',
  }) async {
    try {
      await repository.submitFeedback(
        category: category,
        content: content,
        contact: contact,
      );
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '反馈提交失败');
      notifyListeners();
      return false;
    }
  }

  Future<void> logout() async {
    loading = true;
    notifyListeners();
    await _connectionSubscription?.cancel();
    await _eventSubscription?.cancel();
    _connectionSubscription = null;
    _eventSubscription = null;
    for (final deviceId in [_pushDeviceId, _voipPushDeviceId]) {
      if (deviceId == null) continue;
      try {
        await repository.removeUserDevice(deviceId);
      } catch (_) {
        // Session logout must still complete while the server is unavailable.
      }
    }
    _pushDeviceId = null;
    _voipPushDeviceId = null;
    await repository.logout();
    await _clearAuthenticatedState();
  }

  Future<void> _clearAuthenticatedState() async {
    authenticated = false;
    connected = false;
    currentUser = null;
    conversations = [];
    contacts = [];
    requests = [];
    groupInvitations = [];
    announcements = [];
    _messages.clear();
    _pendingMedia.clear();
    _mediaUploadProgress.clear();
    _scheduledMessages.clear();
    scheduledMessageErrors.clear();
    scheduledMessageLoading.clear();
    _lastDeliveredSeq.clear();
    _pendingDeliveredSeq.clear();
    for (final timer in _deliveryTimers.values) {
      timer.cancel();
    }
    _deliveryTimers.clear();
    for (final timer in _typingExpiryTimers.values) {
      timer.cancel();
    }
    for (final timer in _typingStopTimers.values) {
      timer.cancel();
    }
    _typingUsers.clear();
    _typingExpiryTimers.clear();
    _typingStopTimers.clear();
    _lastTypingSent.clear();
    _typingAnnounced.clear();
    _activeConversationId = null;
    _conversationRefreshTimer?.cancel();
    _conversationRefreshTimer = null;
    _conversationRefreshQueued = false;
    _linkPreviewAttempted.clear();
    messageErrors.clear();
    messageLoading.clear();
    loading = false;
    notifyListeners();
  }

  void _handleEvent(ImEvent event) {
    switch (event.type) {
      case ImEventType.messageCreated:
        final raw = event.payload['message'] as Map<String, Object?>?;
        if (raw == null) return;
        final message = _messageFromEvent(raw);
        if (!message.isMine) {
          _clearTypingUser(message.conversationId, message.senderId);
        }
        final list = _messages.putIfAbsent(message.conversationId, () => []);
        final index = list.indexWhere(
          (existing) =>
              existing.id == message.id ||
              existing.clientMessageId == message.clientMessageId,
        );
        if (index >= 0) {
          list[index] = message;
        } else {
          list.add(message);
        }
        _updateConversation(
          message.conversationId,
          message.text,
          incrementUnread:
              !message.isMine &&
              _activeConversationId != message.conversationId,
          incrementMention:
              !message.isMine &&
              _activeConversationId != message.conversationId &&
              event.payload['mentioned'] == true,
        );
        if (!message.isMine &&
            _activeConversationId == message.conversationId) {
          unawaited(markRead(message.conversationId));
        }
        _scheduleDelivered(message.conversationId, message.conversationSeq);
        _hydrateLinkPreview(message);
        unawaited(repository.persistMessages(message.conversationId, list));
      case ImEventType.messageChanged:
        final raw = event.payload['message'] as Map<String, Object?>?;
        if (raw != null) {
          final message = _messageFromEvent(raw);
          final list = _messages[message.conversationId];
          final index = list?.indexWhere(
            (existing) =>
                existing.id == message.id ||
                existing.clientMessageId == message.clientMessageId,
          );
          if (list != null && index != null && index >= 0) {
            list[index] = message;
            unawaited(repository.persistMessages(message.conversationId, list));
          }
          break;
        }
        final conversationId = event.payload['conversationId'] as String?;
        if (conversationId != null && _messages.containsKey(conversationId)) {
          unawaited(loadMessages(conversationId, force: true));
        }
      case ImEventType.messageRecalled:
        final id = event.payload['messageId'] as String?;
        if (id == null) return;
        for (final list in _messages.values) {
          final index = list.indexWhere((message) => message.id == id);
          if (index >= 0) {
            list[index] = list[index].copyWith(
              text: '',
              status: MessageStatus.recalled,
            );
          }
        }
      case ImEventType.messageDelivered:
        _applyReceipt(event.payload, delivered: true);
      case ImEventType.messageRead:
        _applyReceipt(event.payload, delivered: false);
      case ImEventType.messageExpired:
        final id = event.payload['messageId'] as String?;
        if (id == null) return;
        for (final list in _messages.values) {
          final index = list.indexWhere((message) => message.id == id);
          if (index >= 0) {
            list[index] = list[index].copyWith(
              text: '',
              status: MessageStatus.expired,
              kind: MessageContentKind.system,
            );
          }
        }
      case ImEventType.conversationChanged:
        _scheduleConversationRefresh();
      case ImEventType.friendChanged:
        unawaited(_refreshSocial());
      case ImEventType.groupInvitationChanged:
        unawaited(_refreshGroupInvitations());
      case ImEventType.announcementChanged:
        unawaited(refreshAnnouncements());
      case ImEventType.scheduledChanged:
        final scheduled = event.payload['scheduledMessage'];
        final scheduledPayload = scheduled is Map<String, Object?>
            ? scheduled
            : const <String, Object?>{};
        final conversationId =
            scheduledPayload['conversationId'] as String? ??
            event.payload['conversationId'] as String?;
        if (conversationId != null && conversationId.isNotEmpty) {
          unawaited(loadScheduledMessages(conversationId, force: true));
        }
      case ImEventType.typing:
        _applyTypingEvent(event.payload);
        break;
      case ImEventType.unknown:
        break;
    }
    notifyListeners();
  }

  void _applyTypingEvent(Map<String, Object?> payload) {
    final conversationId = payload['conversationId']?.toString() ?? '';
    final userId = payload['userId']?.toString() ?? '';
    if (conversationId.isEmpty || userId.isEmpty || userId == currentUser?.id) {
      return;
    }
    if (payload['typing'] != true) {
      _clearTypingUser(conversationId, userId);
      return;
    }
    final users = _typingUsers.putIfAbsent(conversationId, () => {});
    // Use a local bounded TTL so a skewed or malicious timestamp cannot leave
    // a permanent typing state. The server's contract currently expires at 6s.
    users[userId] = DateTime.now().add(const Duration(seconds: 6));
    _scheduleTypingExpiry(conversationId);
  }

  void _clearTypingUser(String conversationId, String userId) {
    final users = _typingUsers[conversationId];
    if (users == null) return;
    users.remove(userId);
    if (users.isEmpty) {
      _typingUsers.remove(conversationId);
      _typingExpiryTimers.remove(conversationId)?.cancel();
      return;
    }
    _scheduleTypingExpiry(conversationId);
  }

  void _scheduleTypingExpiry(String conversationId) {
    _typingExpiryTimers.remove(conversationId)?.cancel();
    final users = _typingUsers[conversationId];
    if (users == null || users.isEmpty) return;
    final now = DateTime.now();
    final nextExpiry = users.values.reduce(
      (current, value) => value.isBefore(current) ? value : current,
    );
    final delay = nextExpiry.isAfter(now)
        ? nextExpiry.difference(now) + const Duration(milliseconds: 20)
        : Duration.zero;
    _typingExpiryTimers[conversationId] = Timer(delay, () {
      _typingExpiryTimers.remove(conversationId);
      final active = _typingUsers[conversationId];
      if (active == null) return;
      final current = DateTime.now();
      active.removeWhere((_, expiresAt) => !expiresAt.isAfter(current));
      if (active.isEmpty) {
        _typingUsers.remove(conversationId);
      } else {
        _scheduleTypingExpiry(conversationId);
      }
      if (!_disposed) notifyListeners();
    });
  }

  void _scheduleConversationRefresh() {
    if (_disposed || !authenticated) return;
    if (_conversationRefreshRunning) {
      _conversationRefreshQueued = true;
      return;
    }
    _conversationRefreshTimer?.cancel();
    _conversationRefreshTimer = Timer(
      const Duration(milliseconds: 120),
      () => unawaited(_refreshConversationsFromEvent()),
    );
  }

  Future<void> _refreshConversationsFromEvent() async {
    if (_disposed || !authenticated) return;
    if (_conversationRefreshRunning) {
      _conversationRefreshQueued = true;
      return;
    }
    _conversationRefreshTimer = null;
    _conversationRefreshRunning = true;
    try {
      final refreshed = await repository.conversations();
      if (_disposed || !authenticated) return;
      conversations = refreshed;
      _sortConversations();
      notifyListeners();
    } catch (_) {
      // SDK conversation notifications are best-effort invalidations. A
      // transient metadata failure must not replace the current list.
    } finally {
      _conversationRefreshRunning = false;
      if (_conversationRefreshQueued && !_disposed) {
        _conversationRefreshQueued = false;
        _scheduleConversationRefresh();
      }
    }
  }

  void _applyReceipt(Map<String, Object?> payload, {required bool delivered}) {
    final conversationId = payload['conversationId'] as String?;
    final sequence = (payload['seq'] as num?)?.toInt() ?? 0;
    if (conversationId == null || sequence <= 0) return;
    final list = _messages[conversationId] ?? const <ChatMessage>[];
    final deliveredCount = (payload['deliveredCount'] as num?)?.toInt();
    final readCount = (payload['readCount'] as num?)?.toInt();
    for (var i = 0; i < list.length; i++) {
      final message = list[i];
      if (!message.isMine ||
          message.conversationSeq <= 0 ||
          message.conversationSeq > sequence ||
          message.status == MessageStatus.sending ||
          message.status == MessageStatus.failed ||
          message.status == MessageStatus.recalled ||
          message.status == MessageStatus.expired) {
        continue;
      }
      list[i] = message.copyWith(
        status: delivered && message.status == MessageStatus.read
            ? MessageStatus.read
            : delivered
            ? MessageStatus.delivered
            : MessageStatus.read,
        deliveredCount: deliveredCount,
        readCount: readCount,
      );
    }
  }

  Future<void> _refreshSocial() async {
    try {
      contacts = await repository.contacts();
      requests = await repository.friendRequests();
      notifyListeners();
    } catch (_) {}
  }

  Future<void> _refreshGroupInvitations() async {
    try {
      groupInvitations = await repository.groupInvitations();
      notifyListeners();
    } catch (_) {}
  }

  ChatMessage _messageFromEvent(Map<String, Object?> raw) {
    // The WuKong gateway already maps SDK messages into the page model. Keep
    // accepting the legacy business-event shape below until the remaining
    // non-chat WebSocket events have moved to CMD.
    if (raw['sentAt'] is String &&
        raw['status'] is String &&
        raw['kind'] is String) {
      return ChatMessage.fromJson(raw);
    }
    final body = raw['body'] as Map<String, Object?>? ?? const {};
    final previewRaw = raw['linkPreview'] is Map<String, Object?>
        ? raw['linkPreview']! as Map<String, Object?>
        : body['linkPreview'] is Map<String, Object?>
        ? body['linkPreview']! as Map<String, Object?>
        : null;
    final senderId = raw['senderId']! as String;
    final replyToId =
        raw['replyToId'] as String? ?? body['replyToId'] as String?;
    final type = raw['type'] as String? ?? body['type'] as String?;
    final existing = _messages[raw['conversationId'] as String];
    final replyText = existing
        ?.where((message) => message.id == replyToId)
        .firstOrNull
        ?.text;
    final kind = switch (type) {
      'image' => MessageContentKind.image,
      'audio' || 'voice' => MessageContentKind.voice,
      'video' => MessageContentKind.video,
      'file' => MessageContentKind.file,
      'contact' => MessageContentKind.contact,
      'location' => MessageContentKind.location,
      'chat_history' => MessageContentKind.chatHistory,
      'sticker' || 'store_sticker' => MessageContentKind.sticker,
      'moment' || 'moment_share' => MessageContentKind.momentShare,
      'live' || 'live_event' => MessageContentKind.liveEvent,
      'system' ||
      'call' ||
      'call_event' ||
      'support' ||
      'support_event' => MessageContentKind.system,
      'screenshot' ||
      'screenshot_notice' => MessageContentKind.screenshotNotice,
      null || 'text' =>
        replyToId == null || replyToId.isEmpty
            ? MessageContentKind.text
            : MessageContentKind.reply,
      _ => MessageContentKind.unsupported,
    };
    return ChatMessage(
      id: raw['id']! as String,
      clientMessageId: raw['clientMsgId'] as String?,
      conversationId: raw['conversationId']! as String,
      senderId: senderId,
      senderName: senderId == currentUser?.id
          ? currentUser?.name ?? '我'
          : '联系人',
      text:
          body['text'] as String? ??
          switch (type) {
            'image' => '[图片]',
            'video' => '[视频]',
            'file' => '[文件] ${body['fileName'] as String? ?? ''}'.trim(),
            'audio' || 'voice' => '[语音]',
            'contact' => '[名片] ${body['name'] as String? ?? ''}'.trim(),
            'location' => '[位置] ${body['name'] as String? ?? ''}'.trim(),
            'chat_history' => _chatHistoryEventSummary(body),
            'sticker' || 'store_sticker' =>
              body['digest'] as String? ?? body['content'] as String? ?? '[表情]',
            'moment' || 'moment_share' =>
              body['content'] as String? ??
                  body['digest'] as String? ??
                  '[朋友圈]',
            'live' || 'live_event' =>
              body['digest'] as String? ??
                  body['content'] as String? ??
                  '[直播互动]',
            'system' =>
              body['digest'] as String? ??
                  body['content'] as String? ??
                  '[系统消息]',
            'call' || 'call_event' => callEventDisplayText(body),
            'support' || 'support_event' => supportEventDisplayText(body),
            'screenshot' || 'screenshot_notice' =>
              senderId == currentUser?.id ? '你截取了聊天界面' : '对方截取了聊天界面',
            null || 'text' => '',
            _ => '[当前版本暂不支持此消息]',
          },
      kind: kind,
      event: body['event'] as String?,
      eventData: body['data'] is Map
          ? Map<String, Object?>.from(body['data']! as Map)
          : const {},
      chatHistoryEntries: chatHistoryEntriesFrom(body['entries']),
      mediaUrl: body['url'] as String? ?? body['downloadUrl'] as String?,
      mediaId: body['mediaId'] as String?,
      fileName: body['fileName'] as String?,
      mimeType: body['mime'] as String? ?? body['mimeType'] as String?,
      durationSeconds: (body['duration'] as num?)?.toInt(),
      replyToId: replyToId?.isEmpty == true ? null : replyToId,
      replyToText: replyText ?? (replyToId == null ? null : '原消息暂不可见'),
      contactUserId: body['userId'] as String?,
      contactName: body['name'] as String?,
      contactHandle: body['handle'] as String?,
      contactAvatarUrl: body['avatarUrl'] as String?,
      latitude: (body['latitude'] as num?)?.toDouble(),
      longitude: (body['longitude'] as num?)?.toDouble(),
      locationName: body['name'] as String?,
      locationAddress: body['address'] as String?,
      mentions: _eventMentions(raw, body),
      reactions:
          (raw['reactions'] as List<Object?>? ??
                  body['reactions'] as List<Object?>? ??
                  const [])
              .whereType<Map<String, Object?>>()
              .where((entry) => entry['emoji'] is String)
              .map(MessageReaction.fromJson)
              .where((reaction) => reaction.count > 0)
              .toList(),
      editedAt: DateTime.tryParse(
        (raw['editedAt'] ?? body['editedAt']) as String? ?? '',
      ),
      isPinned:
          raw['isPinned'] as bool? ??
          body['isPinned'] as bool? ??
          raw['pinnedAt'] != null,
      pinnedAt: DateTime.tryParse(
        (raw['pinnedAt'] ?? body['pinnedAt']) as String? ?? '',
      ),
      pinnedBy: raw['pinnedBy'] as String? ?? body['pinnedBy'] as String?,
      expiresAt: DateTime.tryParse(
        (raw['expiresAt'] ?? body['expiresAt']) as String? ?? '',
      ),
      deliveredCount: (raw['deliveredCount'] as num?)?.toInt() ?? 0,
      readCount: (raw['readCount'] as num?)?.toInt() ?? 0,
      linkPreview: previewRaw == null ? null : LinkPreview.fromJson(previewRaw),
      sentAt: DateTime.parse(raw['createdAt']! as String),
      isMine: senderId == currentUser?.id,
      conversationSeq: (raw['conversationSeq'] as num?)?.toInt() ?? 0,
      status: raw['recalledAt'] != null
          ? MessageStatus.recalled
          : MessageStatus.sent,
    );
  }

  List<MessageMention> _eventMentions(
    Map<String, Object?> raw,
    Map<String, Object?> body,
  ) {
    final items =
        raw['mentions'] as List<Object?>? ??
        body['mentions'] as List<Object?>? ??
        const [];
    final conversationId = raw['conversationId'] as String?;
    final members = conversations
        .where((conversation) => conversation.id == conversationId)
        .firstOrNull
        ?.members;
    final mentions = items
        .map<MessageMention?>((entry) {
          if (entry is String && entry.isNotEmpty) {
            final member = members
                ?.where((candidate) => candidate.id == entry)
                .firstOrNull;
            return MessageMention(userId: entry, name: member?.name ?? entry);
          }
          if (entry is Map<String, Object?> && entry['userId'] is String) {
            return MessageMention.fromJson(entry);
          }
          return null;
        })
        .whereType<MessageMention>()
        .toList();
    final mentionAll =
        raw['mentionAll'] as bool? ?? body['mentionAll'] as bool? ?? false;
    if (mentionAll && !mentions.any((mention) => mention.isEveryone)) {
      mentions.insert(0, const MessageMention(userId: 'all', name: '所有人'));
    }
    return mentions;
  }

  String _chatHistoryEventSummary(Map<String, Object?> body) {
    final entries = body['entries'];
    if (entries is! List<Object?> || entries.isEmpty) return '[聊天记录]';
    final previews = entries
        .whereType<Map<String, Object?>>()
        .map((entry) => entry['summary']?.toString().trim() ?? '')
        .where((summary) => summary.isNotEmpty)
        .take(3)
        .toList();
    return previews.isEmpty ? '[聊天记录]' : '聊天记录\n${previews.join('\n')}';
  }

  void _hydrateLinkPreviews(String conversationId) {
    for (final message in _messages[conversationId] ?? const <ChatMessage>[]) {
      _hydrateLinkPreview(message);
    }
  }

  void _hydrateLinkPreview(ChatMessage message) {
    if (message.linkPreview != null ||
        message.status == MessageStatus.expired ||
        message.status == MessageStatus.recalled ||
        (message.kind != MessageContentKind.text &&
            message.kind != MessageContentKind.reply)) {
      return;
    }
    final match = RegExp(r'https?://[^\s<>]+').firstMatch(message.text);
    final url = match?.group(0);
    if (url == null || !_linkPreviewAttempted.add(message.id)) return;
    unawaited(_fetchLinkPreview(message, url));
  }

  Future<void> _fetchLinkPreview(ChatMessage message, String url) async {
    try {
      final preview = await repository.linkPreview(url);
      if (preview == null) return;
      final list = _messages[message.conversationId];
      final index = list?.indexWhere(
        (item) =>
            item.id == message.id ||
            item.clientMessageId == message.clientMessageId,
      );
      if (list == null || index == null || index < 0) return;
      list[index] = list[index].copyWith(linkPreview: preview);
      await repository.persistMessages(message.conversationId, list);
      notifyListeners();
    } catch (_) {
      // A preview is optional. The original message and URL remain untouched.
    }
  }

  void _replaceMessage(
    String conversationId,
    String clientMessageId,
    ChatMessage replacement,
  ) {
    final list = _messages[conversationId]!;
    final index = list.indexWhere(
      (message) => message.clientMessageId == clientMessageId,
    );
    if (index >= 0) list[index] = replacement;
  }

  void _updateConversation(
    String id,
    String text, {
    bool incrementUnread = false,
    bool incrementMention = false,
  }) {
    final index = conversations.indexWhere(
      (conversation) => conversation.id == id,
    );
    if (index < 0) return;
    final current = conversations[index];
    conversations[index] = current.copyWith(
      subtitle: text,
      updatedAt: DateTime.now(),
      unread: incrementUnread ? current.unread + 1 : current.unread,
      mentionUnreadCount: current.mentionUnreadCount == null
          ? null
          : current.mentionUnreadCount! + (incrementMention ? 1 : 0),
      lastMessageSeq: max(
        current.lastMessageSeq,
        _messages[id]?.lastOrNull?.conversationSeq ?? 0,
      ),
    );
    _sortConversations();
  }

  void _upsertConversation(Conversation conversation) {
    conversations.removeWhere((item) => item.id == conversation.id);
    conversations.add(conversation);
    _sortConversations();
  }

  void _sortConversations() => conversations.sort((a, b) {
    if (a.pinned != b.pinned) return a.pinned ? -1 : 1;
    final byUpdatedAt = b.updatedAt.compareTo(a.updatedAt);
    return byUpdatedAt != 0 ? byUpdatedAt : a.id.compareTo(b.id);
  });

  Map<String, dynamic> _flattenPushPayload(Map<String, dynamic> payload) {
    final normalized = Map<String, dynamic>.from(payload);
    for (final key in const ['payload', 'transmission', 'data']) {
      final nested = payload[key];
      if (nested is Map) {
        normalized.addAll(nested.map((key, value) => MapEntry('$key', value)));
      }
      if (nested is String && nested.trim().startsWith('{')) {
        try {
          final decoded = jsonDecode(nested);
          if (decoded is Map) {
            normalized.addAll(
              decoded.map((key, value) => MapEntry('$key', value)),
            );
          }
        } catch (_) {
          // Invalid optional provider payloads are ignored safely.
        }
      }
    }
    return normalized;
  }

  String _newClientMessageId() {
    return createClientMessageId(_random);
  }

  String _messageFor(Object exception, {required String fallback}) {
    final message = exception.toString();
    if (message.isEmpty ||
        message == 'Exception' ||
        message.contains('ClientException') ||
        message.contains('Failed to fetch') ||
        message.contains('SocketException') ||
        message.contains('XMLHttpRequest error')) {
      return fallback;
    }
    return message
        .replaceFirst('FormatException: ', '')
        .replaceFirst('ImApiException: ', '');
  }

  @override
  void dispose() {
    _disposed = true;
    _conversationRefreshTimer?.cancel();
    for (final timer in _deliveryTimers.values) {
      timer.cancel();
    }
    for (final timer in _typingExpiryTimers.values) {
      timer.cancel();
    }
    for (final timer in _typingStopTimers.values) {
      timer.cancel();
    }
    _connectionSubscription?.cancel();
    _eventSubscription?.cancel();
    unawaited(repository.close());
    callController?.dispose();
    super.dispose();
  }
}
