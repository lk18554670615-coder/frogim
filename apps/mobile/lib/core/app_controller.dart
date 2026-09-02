import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';

import '../calls/call_controller.dart';
import '../calls/call_repository.dart';
import '../data/im_repository.dart';
import '../data/live_repository.dart' show ImApiException;
import '../im/business_features.dart';
import '../im/structured_event_text.dart';
import 'auth_validation.dart';
import 'client_message_id.dart';
import 'client_diagnostics.dart';
import 'client_device.dart';
import 'forward_batch.dart';
import 'image_dimensions.dart';
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
  bool connectionAttempted = false;
  bool connectionRetrying = false;
  String? error;
  AuthPolicy authPolicy = const AuthPolicy();
  bool authPolicyLoaded = false;
  bool authPolicyAvailable = false;
  bool authPolicyLoading = false;
  String? authPolicyLoadError;
  AppUser? currentUser;
  List<Conversation> conversations = [];
  List<AppUser> contacts = [];
  List<FriendRequest> requests = [];
  List<GroupInvitation> groupInvitations = [];
  List<AppAnnouncement> announcements = [];
  String? contactsLoadError;
  String? friendRequestsLoadError;
  String? groupInvitationsLoadError;
  String? announcementsLoadError;
  final Map<String, List<ChatMessage>> _messages = {};
  final Map<String, Future<void>> _messageLoadOperations = {};
  final Map<String, Future<List<ChatMessage>>> _messageCacheLoadOperations = {};
  final Map<String, String> _drafts = {};
  final Map<String, MediaUpload> _pendingMedia = {};
  final Map<String, double> _mediaUploadProgress = {};
  final Set<String> _sendingMediaMessageIds = {};
  final Set<String> _mediaAwaitingReconnect = {};
  final Set<String> _mediaAutomaticallyRetried = {};
  String? _pendingProfileAvatarFingerprint;
  String? _pendingProfileAvatarMediaId;
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
  final Set<String> _pendingOutgoingFriendUserIds = {};
  Timer? _conversationRefreshTimer;
  bool _conversationRefreshRunning = false;
  bool _conversationRefreshQueued = false;
  final Set<String> _linkPreviewAttempted = {};
  final Set<String> messageLoading = {};
  final Map<String, String> messageErrors = {};
  final Set<String> messageHistoryLoading = {};
  final Map<String, String> messageHistoryErrors = {};
  final Map<String, bool> _messageHistoryHasMore = {};
  StreamSubscription<bool>? _connectionSubscription;
  StreamSubscription<ImEvent>? _eventSubscription;
  final Random _random = Random.secure();
  String? _activeConversationId;
  String? _pendingConversationId;
  String? _pushDeviceId;
  String? _voipPushDeviceId;
  bool _disposed = false;
  Future<void>? _authenticationBootstrap;
  bool _stickyAuthenticationError = false;
  int _forwardSessionEpoch = 0;
  final Set<ForwardBatchTask> _forwardTasks = {};

  bool get messagingUnavailable =>
      authenticated && connectionAttempted && !connected;
  int get conversationUnreadCount => conversations
      .where((conversation) => !conversation.muted)
      .fold(0, (total, conversation) => total + conversation.unread);
  int get systemNotificationUnreadCount =>
      announcements.where((announcement) => announcement.unread).length;
  int get notificationUnreadCount =>
      conversationUnreadCount + systemNotificationUnreadCount;
  int get pendingIncomingFriendRequestCount => requests
      .where((request) => request.status == 'pending' && !request.outgoing)
      .length;
  int get pendingIncomingGroupInvitationCount => groupInvitations
      .where((invitation) => invitation.pending && !invitation.outgoing)
      .length;
  int get contactNotificationCount =>
      pendingIncomingFriendRequestCount + pendingIncomingGroupInvitationCount;
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
  int groupHistoryRevision = 0;
  FriendRequest? pendingFriendRequestFor(String userId) => requests
      .where(
        (request) => request.user.id == userId && request.status == 'pending',
      )
      .firstOrNull;
  bool awaitingFriendApprovalFor(String userId) =>
      _pendingOutgoingFriendUserIds.contains(userId) ||
      requests.any(
        (request) =>
            request.user.id == userId &&
            request.status == 'pending' &&
            request.outgoing,
      );
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

  List<ChatMessage> messagesFor(String conversationId) => List.unmodifiable(
    (_messages[conversationId] ?? const <ChatMessage>[]).where(canReadMessage),
  );
  bool canReadMessage(ChatMessage message) =>
      repository is! GroupHistoryRepository ||
      (repository as GroupHistoryRepository).canReadCachedMessage(message);
  bool messageHistoryHasMore(String conversationId) =>
      _messageHistoryHasMore[conversationId] ?? false;
  String draftFor(String conversationId) => _drafts[conversationId] ?? '';
  double? mediaUploadProgressFor(String clientMessageId) =>
      _mediaUploadProgress[clientMessageId];
  List<ScheduledMessage> scheduledMessagesFor(String conversationId) =>
      List.unmodifiable(_scheduledMessages[conversationId] ?? const []);
  int get archivedConversationCount =>
      conversations.where((conversation) => conversation.archived).length;
  bool get supportsBusinessFeatures => repository is BusinessFeatureRepository;

  void clearError({bool preserveAuthenticationFailure = false}) {
    if (preserveAuthenticationFailure && _stickyAuthenticationError) return;
    _stickyAuthenticationError = false;
    if (error == null) return;
    error = null;
    notifyListeners();
  }

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
    // Authentication policy controls registration and password-reset forms,
    // but it must not hold the entire login screen behind the launch splash.
    // Load it in parallel and let the login page show a compact syncing state.
    final authPolicyFuture = _loadAuthPolicy(notify: true);
    var restored = false;
    try {
      if (await repository.restoreSession()) {
        final restoredUser = repository.currentUser;
        if (restoredUser == null) {
          throw StateError('本机会话缺少账号资料');
        }
        restored = true;
        initializing = false;
        _enterAuthenticatedShell(restoredUser, refreshProfile: true);
      }
    } catch (exception) {
      // 恢复会话后的短暂网络错误不应清除登录态。
      _stickyAuthenticationError = true;
      error = _messageFor(exception, fallback: '已恢复账号，消息正在重连');
    } finally {
      if (!restored) {
        initializing = false;
        notifyListeners();
      }
      unawaited(authPolicyFuture);
    }
  }

  void _enterAuthenticatedShell(AppUser user, {bool refreshProfile = false}) {
    _invalidateForwardTasks();
    _stickyAuthenticationError = false;
    currentUser = user;
    authenticated = true;
    loading = true;
    error = null;
    _subscribe();
    notifyListeners();
    unawaited(_reportClientDevice());
    unawaited(_connectSafely());
    final bootstrap = _bootstrapAuthenticatedSession(
      refreshProfile: refreshProfile,
    );
    _authenticationBootstrap = bootstrap;
    unawaited(
      bootstrap.whenComplete(() {
        if (identical(_authenticationBootstrap, bootstrap)) {
          _authenticationBootstrap = null;
        }
      }),
    );
  }

  Future<void> _reportClientDevice() async {
    try {
      final report = await ClientDeviceReporter.collect();
      await repository.registerClientDevice(
        installationId: report.installationId,
        platform: report.platform,
        deviceName: report.deviceName,
        deviceModel: report.deviceModel,
        osVersion: report.osVersion,
        appVersion: report.appVersion,
      );
    } catch (exception) {
      if (kDebugMode || kProfileMode) {
        debugPrint(
          '[client-device] report skipped: type=${exception.runtimeType}',
        );
      }
    }
  }

  Future<void> _bootstrapAuthenticatedSession({
    required bool refreshProfile,
  }) async {
    final profileCheck = refreshProfile
        ? _refreshRestoredProfile()
        : Future<bool>.value(false);
    Object? coreError;
    try {
      await _loadCore();
    } catch (exception) {
      coreError = exception;
    }
    final sessionExpired = await profileCheck;
    if (_disposed || sessionExpired || !authenticated) return;
    error = coreError == null
        ? null
        : _messageFor(coreError, fallback: '账号已登录，消息列表暂时无法同步');
    loading = false;
    notifyListeners();
  }

  Future<void> _expireRestoredSession() async {
    _stickyAuthenticationError = true;
    error = '登录状态已失效，请重新登录';
    final connectionSubscription = _connectionSubscription;
    final eventSubscription = _eventSubscription;
    _connectionSubscription = null;
    _eventSubscription = null;
    await _clearAuthenticatedState();
    try {
      await Future.wait<void>([
        if (connectionSubscription != null) connectionSubscription.cancel(),
        if (eventSubscription != null) eventSubscription.cancel(),
      ]);
    } catch (exception, stackTrace) {
      ClientDiagnostics.instance.captureError(
        'expired_session_subscription_cleanup',
        exception,
        stackTrace,
      );
    }
  }

  Future<bool> _refreshRestoredProfile() async {
    try {
      currentUser = await repository.profile();
      if (!_disposed) notifyListeners();
      return false;
    } catch (_) {
      // Refresh-token rejection clears the repository identity. Distinguish it
      // from a transient outage so a revoked session never leaves a stale,
      // apparently authenticated shell on screen.
      if (repository.currentUser == null) {
        await _expireRestoredSession();
        return true;
      }
      return false;
    }
  }

  Future<void> _loadAuthPolicy({bool notify = false}) async {
    if (authPolicyLoading) return;
    authPolicyLoading = true;
    if (notify) notifyListeners();
    try {
      authPolicy = await repository.authPolicy().timeout(
        const Duration(seconds: 3),
      );
      authPolicyAvailable = true;
      authPolicyLoadError = null;
    } catch (_) {
      authPolicyAvailable = false;
      // Older production servers do not expose /v2/config/auth yet. Keep the
      // server-enforced legacy registration/reset flow available and use the
      // conservative built-in password limits until the optional policy can
      // be fetched. Only an explicit policy response may disable registration.
      authPolicy = const AuthPolicy();
      authPolicyLoadError = '认证策略接口暂不可用，已使用兼容规则';
    } finally {
      authPolicyLoaded = true;
      authPolicyLoading = false;
      if (notify) notifyListeners();
    }
  }

  Future<void> refreshAuthPolicy() => _loadAuthPolicy(notify: true);

  /// Waits for the first authenticated data sync when a flow must open a
  /// data-dependent destination immediately after login. The main shell does
  /// not await this future, so ordinary sign-in remains responsive.
  Future<void> waitForInitialAuthenticatedSync() async {
    await _authenticationBootstrap;
  }

  Future<bool> requestCode(String phone) async {
    final normalized = phone.trim();
    if (!validAuthPhone(normalized)) {
      error = '请输入有效手机号';
      notifyListeners();
      return false;
    }
    loading = true;
    error = null;
    notifyListeners();
    try {
      await repository.requestCode(normalized);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '验证码发送失败，请稍后重试');
      return false;
    } finally {
      loading = false;
      notifyListeners();
    }
  }

  Future<void> login(String phone, String code) async {
    if (!validAuthPhone(phone) || code.trim().isEmpty) {
      error = '请输入有效手机号和验证码';
      notifyListeners();
      return;
    }
    loading = true;
    error = null;
    notifyListeners();
    var enteredShell = false;
    try {
      final user = await repository.login(phone.trim(), code.trim());
      enteredShell = true;
      _enterAuthenticatedShell(user);
    } catch (exception) {
      authenticated = false;
      error = _messageFor(exception, fallback: '登录失败，请检查信息后重试');
    } finally {
      if (!enteredShell) {
        loading = false;
        notifyListeners();
      }
    }
  }

  Future<void> passwordLogin(String phone, String password) async {
    if (!validAuthPhone(phone) || password.isEmpty) {
      error = '请输入手机号和密码';
      notifyListeners();
      return;
    }
    loading = true;
    error = null;
    notifyListeners();
    var enteredShell = false;
    try {
      final user = await repository.passwordLogin(phone.trim(), password);
      enteredShell = true;
      _enterAuthenticatedShell(user);
    } catch (exception) {
      authenticated = false;
      error = _messageFor(exception, fallback: '手机号或密码错误');
    } finally {
      if (!enteredShell) {
        loading = false;
        notifyListeners();
      }
    }
  }

  Future<QrLoginTicket?> createQrLoginTicket({
    required String clientName,
  }) async {
    loading = true;
    error = null;
    notifyListeners();
    try {
      return await repository.createQrLoginTicket(clientName: clientName);
    } catch (exception) {
      error = _messageFor(exception, fallback: '登录二维码生成失败，请重试');
      return null;
    } finally {
      loading = false;
      notifyListeners();
    }
  }

  Future<bool> pollQrLoginTicket(QrLoginTicket ticket) async {
    try {
      final user = await repository.pollQrLoginTicket(ticket);
      if (user == null) return false;
      _enterAuthenticatedShell(user);
      return true;
    } catch (exception) {
      authenticated = false;
      error = _messageFor(exception, fallback: '扫码登录失败，请刷新二维码重试');
      notifyListeners();
      return false;
    }
  }

  Future<QrLoginRequest?> inspectQrLogin(String token) async {
    error = null;
    notifyListeners();
    try {
      return await repository.inspectQrLogin(token);
    } catch (exception) {
      error = _messageFor(exception, fallback: '无法读取这次登录请求');
      notifyListeners();
      return null;
    }
  }

  Future<bool> confirmQrLogin(String token) async {
    error = null;
    notifyListeners();
    try {
      await repository.confirmQrLogin(token);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '登录确认失败，请重试');
      notifyListeners();
      return false;
    }
  }

  Future<bool> registerAccount({
    required String phone,
    required String code,
    required String password,
    required String name,
  }) async {
    if (authPolicyAvailable && !authPolicy.registrationEnabled) {
      error = '当前暂未开放新账号注册';
      notifyListeners();
      return false;
    }
    if (!validAuthPhone(phone) || code.trim().isEmpty || name.trim().isEmpty) {
      error = '请完整填写注册信息';
      notifyListeners();
      return false;
    }
    final passwordError = authPolicy.passwordError(password);
    if (passwordError != null) {
      error = passwordError;
      notifyListeners();
      return false;
    }
    loading = true;
    error = null;
    notifyListeners();
    var enteredShell = false;
    try {
      final user = await repository.register(
        phone: phone.trim(),
        code: code.trim(),
        password: password,
        name: name.trim(),
      );
      enteredShell = true;
      _enterAuthenticatedShell(user);
      return true;
    } catch (exception) {
      authenticated = false;
      error = _messageFor(exception, fallback: '注册失败');
      return false;
    } finally {
      if (!enteredShell) {
        loading = false;
        notifyListeners();
      }
    }
  }

  Future<bool> requestResetCode(String phone) async {
    if (!validAuthPhone(phone)) {
      error = '请输入有效手机号';
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
    if (!validAuthPhone(phone) || code.trim().isEmpty) {
      error = '请完整填写重置信息';
      notifyListeners();
      return false;
    }
    final passwordError = authPolicy.passwordError(password);
    if (passwordError != null) {
      error = passwordError;
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
      await _authenticationBootstrap;
    } catch (exception) {
      error = _messageFor(exception, fallback: '当前构建未启用演示模式');
      notifyListeners();
    }
  }

  void _subscribe() {
    _connectionSubscription ??= repository.connectionChanges.listen((value) {
      if (_disposed) return;
      connected = value;
      connectionAttempted = true;
      if (value) {
        connectionRetrying = false;
        unawaited(_flushPendingDeliveries());
        unawaited(_resumeMediaAfterReconnect());
      } else if (_sendingMediaMessageIds.isNotEmpty) {
        _mediaAwaitingReconnect.addAll(_sendingMediaMessageIds);
      }
      notifyListeners();
    });
    _eventSubscription ??= repository.events.listen(_handleEvent);
  }

  Future<void> _connectSafely() async {
    final started = Stopwatch()..start();
    try {
      await repository.connect();
    } catch (_) {
      connected = false;
      ClientDiagnostics.instance.captureOperational(
        kind: 'connection',
        name: 'wukong_connect',
        duration: started.elapsed,
      );
    } finally {
      if (!_disposed) {
        connectionAttempted = true;
        connectionRetrying = false;
        notifyListeners();
      }
    }
  }

  Future<void> retryConnection() async {
    if (!authenticated || connected || connectionRetrying) return;
    connectionRetrying = true;
    notifyListeners();
    await _connectSafely();
  }

  Future<void> _loadCore() async {
    final loadedConversations = await repository.conversations();
    if (!authenticated) return;
    conversations = loadedConversations;
    _sortConversations();
    _prewarmRecentMessageCaches();
    final results = await Future.wait<Object>([
      _loadOptional(
        repository.contacts(),
        contacts,
        fallbackMessage: '联系人加载失败，请稍后重试',
      ),
      _loadOptional(
        repository.friendRequests(),
        requests,
        fallbackMessage: '好友申请加载失败，请稍后重试',
      ),
      _loadOptional(
        repository.groupInvitations(),
        groupInvitations,
        fallbackMessage: '群聊邀请加载失败，请稍后重试',
      ),
      _loadOptional(
        repository.announcements(),
        announcements,
        fallbackMessage: '平台公告加载失败，请稍后重试',
      ),
    ]);
    final contactsResult = results[0] as _OptionalLoadResult<List<AppUser>>;
    final requestsResult =
        results[1] as _OptionalLoadResult<List<FriendRequest>>;
    final invitationsResult =
        results[2] as _OptionalLoadResult<List<GroupInvitation>>;
    final announcementsResult =
        results[3] as _OptionalLoadResult<List<AppAnnouncement>>;
    if (!authenticated) return;
    contacts = contactsResult.value;
    contactsLoadError = contactsResult.error;
    requests = requestsResult.value;
    friendRequestsLoadError = requestsResult.error;
    groupInvitations = invitationsResult.value;
    groupInvitationsLoadError = invitationsResult.error;
    announcements = announcementsResult.value;
    announcementsLoadError = announcementsResult.error;
    unawaited(ClientDiagnostics.instance.flush(repository));
  }

  Future<_OptionalLoadResult<T>> _loadOptional<T>(
    Future<T> operation,
    T fallback, {
    required String fallbackMessage,
  }) async {
    try {
      return _OptionalLoadResult(await operation);
    } catch (exception) {
      return _OptionalLoadResult(
        fallback,
        _messageFor(exception, fallback: fallbackMessage),
      );
    }
  }

  Future<bool> refreshContacts() async {
    contactsLoadError = null;
    notifyListeners();
    try {
      contacts = await repository.contacts();
      return true;
    } catch (exception) {
      contactsLoadError = _messageFor(exception, fallback: '联系人加载失败，请稍后重试');
      return false;
    } finally {
      notifyListeners();
    }
  }

  Future<bool> refreshFriendRequests() async {
    friendRequestsLoadError = null;
    notifyListeners();
    try {
      requests = await repository.friendRequests();
      _reconcilePendingOutgoingFriendUsers();
      return true;
    } catch (exception) {
      friendRequestsLoadError = _messageFor(
        exception,
        fallback: '好友申请加载失败，请稍后重试',
      );
      return false;
    } finally {
      notifyListeners();
    }
  }

  Future<bool> refreshGroupInvitations() async {
    groupInvitationsLoadError = null;
    notifyListeners();
    try {
      groupInvitations = await repository.groupInvitations();
      return true;
    } catch (exception) {
      groupInvitationsLoadError = _messageFor(
        exception,
        fallback: '群聊邀请加载失败，请稍后重试',
      );
      return false;
    } finally {
      notifyListeners();
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
    error = null;
    try {
      return await repository.searchUsers(query, by: by);
    } catch (exception) {
      error = _messageFor(exception, fallback: '搜索失败，请稍后重试');
      notifyListeners();
      return const [];
    }
  }

  Future<UserSearchCapabilities?> loadSearchCapabilities() async {
    error = null;
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
    announcementsLoadError = null;
    notifyListeners();
    try {
      announcements = await repository.announcements();
    } catch (exception) {
      announcementsLoadError = _messageFor(
        exception,
        fallback: '平台公告加载失败，请稍后重试',
      );
    } finally {
      notifyListeners();
    }
  }

  Future<void> loadMessages(String conversationId, {bool force = false}) {
    final active = _messageLoadOperations[conversationId];
    if (active != null) return active;
    if (!force && _messages.containsKey(conversationId)) {
      return Future<void>.value();
    }
    final operation = _loadMessages(conversationId, force: force);
    _messageLoadOperations[conversationId] = operation;
    return operation.whenComplete(() {
      if (identical(_messageLoadOperations[conversationId], operation)) {
        _messageLoadOperations.remove(conversationId);
      }
    });
  }

  Future<void> _reloadHistoryAfterPolicy(String cid) async {
    final userId = currentUser?.id;
    final active = _messageLoadOperations[cid];
    if (active != null) await active;
    if (_disposed || currentUser?.id != userId || activeConversationId != cid) {
      return;
    }
    await loadMessages(cid, force: true);
  }

  Future<List<ChatMessage>> _readCachedMessages(String conversationId) {
    final active = _messageCacheLoadOperations[conversationId];
    if (active != null) return active;
    if (repository is! CachedMessageRepository) {
      return Future<List<ChatMessage>>.value(const []);
    }
    final cachedRepository = repository as CachedMessageRepository;
    final operation = cachedRepository
        .cachedMessages(conversationId)
        .catchError((_) => <ChatMessage>[]);
    _messageCacheLoadOperations[conversationId] = operation;
    return operation.whenComplete(() {
      if (identical(_messageCacheLoadOperations[conversationId], operation)) {
        _messageCacheLoadOperations.remove(conversationId);
      }
    });
  }

  void _prewarmRecentMessageCaches() {
    if (repository is! CachedMessageRepository || currentUser == null) return;
    final sessionUserId = currentUser!.id;
    final recent = conversations
        .where((conversation) => !conversation.archived)
        .take(5)
        .toList(growable: false);
    for (final conversation in recent) {
      unawaited(_prewarmMessageCache(conversation.id, sessionUserId));
    }
  }

  Future<void> _prewarmMessageCache(
    String conversationId,
    String sessionUserId,
  ) async {
    final cached = await _readCachedMessages(conversationId);
    if (_disposed ||
        !authenticated ||
        currentUser?.id != sessionUserId ||
        cached.isEmpty) {
      return;
    }
    final existing = _messages[conversationId] ?? const <ChatMessage>[];
    _messages[conversationId] = _mergeMessageLists(existing, cached);
    final oldestSequence = _oldestServerSequence(_messages[conversationId]!);
    _messageHistoryHasMore[conversationId] =
        repository is PaginatedMessageRepository && oldestSequence > 1;
    notifyListeners();
  }

  Future<void> _loadMessages(
    String conversationId, {
    required bool force,
  }) async {
    messageLoading.add(conversationId);
    messageErrors.remove(conversationId);
    notifyListeners();
    // Start the authoritative sync before touching encrypted local storage.
    // Some Android keystores take noticeable time on the first decrypt after
    // process start; serializing the network request behind that read makes a
    // cold chat open needlessly slower. The cache can still paint first while
    // both operations are in flight.
    final remoteMessages = repository
        .messages(conversationId)
        .then<_MessageLoadResult>(
          _MessageLoadResult.success,
          onError: (Object error, StackTrace stackTrace) =>
              _MessageLoadResult.failure(error, stackTrace),
        );
    if (!_messages.containsKey(conversationId) &&
        repository is CachedMessageRepository) {
      final cached = await _readCachedMessages(conversationId);
      if (cached.isNotEmpty) {
        _messages[conversationId] = _mergeMessageLists(const [], cached);
        notifyListeners();
      }
    }
    try {
      final remoteResult = await remoteMessages;
      if (remoteResult.error case final error?) {
        Error.throwWithStackTrace(error, remoteResult.stackTrace!);
      }
      final messages = remoteResult.messages!;
      final existing = _messages[conversationId] ?? const <ChatMessage>[];
      final merged = force && existing.isNotEmpty
          ? _mergeMessageLists(existing, messages)
          : _mergeMessageLists(const [], messages);
      _messages[conversationId] = merged;
      final oldestSequence = _oldestServerSequence(merged);
      _messageHistoryHasMore[conversationId] =
          repository is PaginatedMessageRepository && oldestSequence > 1;
      messageHistoryErrors.remove(conversationId);
      _scheduleDelivered(
        conversationId,
        merged.fold<int>(
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

  Future<bool> loadOlderMessages(
    String conversationId, {
    int limit = 50,
  }) async {
    if (messageHistoryLoading.contains(conversationId) ||
        !messageHistoryHasMore(conversationId)) {
      return false;
    }
    final paginated = repository;
    if (paginated is! PaginatedMessageRepository) {
      _messageHistoryHasMore[conversationId] = false;
      notifyListeners();
      return false;
    }
    final historyRepository = paginated as PaginatedMessageRepository;
    final current = _messages[conversationId] ?? const <ChatMessage>[];
    final beforeSequence = _oldestServerSequence(current);
    if (beforeSequence <= 1) {
      _messageHistoryHasMore[conversationId] = false;
      notifyListeners();
      return false;
    }

    messageHistoryLoading.add(conversationId);
    messageHistoryErrors.remove(conversationId);
    notifyListeners();
    try {
      final older = await historyRepository.olderMessages(
        conversationId,
        beforeSequence: beforeSequence,
        limit: limit,
      );
      final merged = _mergeMessageLists(current, older);
      final nextOldest = _oldestServerSequence(merged);
      final advanced = nextOldest > 0 && nextOldest < beforeSequence;
      _messages[conversationId] = merged;
      _messageHistoryHasMore[conversationId] =
          advanced && nextOldest > 1 && older.isNotEmpty;
      if (advanced) {
        _hydrateLinkPreviews(conversationId);
        // The production repository has already committed its bounded WuKong
        // hot cache. Persist the page-model snapshot off the scroll path so a
        // slow local store cannot delay viewport anchoring.
        unawaited(
          repository.persistMessages(conversationId, merged).catchError((_) {}),
        );
      }
      return advanced;
    } catch (exception) {
      messageHistoryErrors[conversationId] = _messageFor(
        exception,
        fallback: '较早的消息加载失败，请稍后重试',
      );
      return false;
    } finally {
      messageHistoryLoading.remove(conversationId);
      notifyListeners();
    }
  }

  int _oldestServerSequence(Iterable<ChatMessage> messages) {
    var oldest = 0;
    for (final message in messages) {
      final sequence = message.conversationSeq;
      if (sequence <= 0) continue;
      if (oldest == 0 || sequence < oldest) oldest = sequence;
    }
    return oldest;
  }

  List<ChatMessage> _mergeMessageLists(
    Iterable<ChatMessage> current,
    Iterable<ChatMessage> incoming,
  ) {
    final merged = <ChatMessage>[];
    final indexByIdentity = <String, int>{};

    void upsert(ChatMessage message) {
      if (!canReadMessage(message)) return;
      final identities = <String>{
        if (message.id.isNotEmpty) 'id:${message.id}',
        if (message.clientMessageId.isNotEmpty)
          'client:${message.clientMessageId}',
      };
      int? index;
      for (final identity in identities) {
        index ??= indexByIdentity[identity];
      }
      if (index == null) {
        index = merged.length;
        merged.add(message);
      } else {
        merged[index] = message;
      }
      for (final identity in identities) {
        indexByIdentity[identity] = index;
      }
    }

    for (final message in current) {
      upsert(message);
    }
    for (final message in incoming) {
      upsert(message);
    }
    merged.sort((a, b) {
      final aSequence = a.conversationSeq;
      final bSequence = b.conversationSeq;
      if (aSequence > 0 && bSequence > 0) {
        final bySequence = aSequence.compareTo(bSequence);
        if (bySequence != 0) return bySequence;
      } else if (aSequence > 0) {
        return -1;
      } else if (bSequence > 0) {
        return 1;
      }
      return a.sentAt.compareTo(b.sentAt);
    });
    return merged;
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
      final sorted = List<ScheduledMessage>.of(items)
        ..sort((a, b) => a.scheduledAt.compareTo(b.scheduledAt));
      _scheduledMessages[conversationId] = sorted;
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
    String? robotId,
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
      replyToSeq: replyTo?.conversationSeq ?? 0,
      replyToSenderId: replyTo?.senderId,
      replyToSenderName: replyTo?.senderName,
      mentions: mentions,
      robotId: robotId,
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

  Future<ChatMessage?> sendRobotCommand(
    String conversationId,
    RobotMenu menu,
  ) => sendMessage(conversationId, menu.command, robotId: menu.robotId);

  Future<List<RobotProfile>> robotProfilesForConversation(
    String conversationId,
  ) => repository.robotProfiles(conversationId);

  Future<ChatMessage> sendMedia(
    String conversationId,
    MediaUpload upload, {
    ChatMessage? replyTo,
    ValueChanged<ChatMessage>? onQueued,
  }) async {
    if (upload.kind == MessageContentKind.image &&
        (upload.width == null || upload.height == null)) {
      final dimensions = await decodeImagePixelSize(upload.bytes);
      if (dimensions != null) {
        upload = upload.copyWith(
          width: dimensions.width,
          height: dimensions.height,
        );
      }
    }
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
      mediaWidth: upload.width,
      mediaHeight: upload.height,
      fileName: upload.fileName,
      mimeType: upload.mimeType,
      durationSeconds: upload.durationSeconds,
      replyToId: replyTo?.id,
      replyToText: replyTo?.text,
      replyToSeq: replyTo?.conversationSeq ?? 0,
      replyToSenderId: replyTo?.senderId,
      replyToSenderName: replyTo?.senderName,
      status: MessageStatus.sending,
    );
    _pendingMedia[clientId] = upload;
    _mediaUploadProgress[clientId] = 0;
    final list = _messages.putIfAbsent(conversationId, () => []);
    try {
      await repository.persistMessages(conversationId, [...list, pending]);
    } catch (_) {
      _pendingMedia.remove(clientId);
      _mediaUploadProgress.remove(clientId);
      rethrow;
    }
    // Keep the draft out of reactive message lists until it is durable. An
    // unrelated realtime event must not mount its entrance before onQueued.
    _messages.putIfAbsent(conversationId, () => []).add(pending);
    _updateConversation(conversationId, label);
    onQueued?.call(pending);
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
      replyToSeq: replyTo?.conversationSeq ?? 0,
      replyToSenderId: replyTo?.senderId,
      replyToSenderName: replyTo?.senderName,
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
      replyToSeq: replyTo?.conversationSeq ?? 0,
      replyToSenderId: replyTo?.senderId,
      replyToSenderName: replyTo?.senderName,
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
    final isMediaMessage =
        pending.kind == MessageContentKind.image ||
        pending.kind == MessageContentKind.file ||
        pending.kind == MessageContentKind.video ||
        pending.kind == MessageContentKind.voice;
    if (isMediaMessage) {
      _sendingMediaMessageIds.add(pending.clientMessageId);
    }
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
      _sendingMediaMessageIds.remove(pending.clientMessageId);
      _mediaAwaitingReconnect.remove(pending.clientMessageId);
      _mediaAutomaticallyRetried.remove(pending.clientMessageId);
      await repository.persistMessages(pending.conversationId, list);
      notifyListeners();
      _hydrateLinkPreview(sent);
      return sent;
    } catch (exception) {
      _sendingMediaMessageIds.remove(pending.clientMessageId);
      if (isMediaMessage &&
          (!connected ||
              _mediaAwaitingReconnect.contains(pending.clientMessageId) ||
              _isTransientMediaFailure(exception))) {
        _mediaAwaitingReconnect.add(pending.clientMessageId);
      }
      _mediaUploadProgress.remove(pending.clientMessageId);
      final failed = pending.copyWith(status: MessageStatus.failed);
      _replaceMessage(pending.conversationId, pending.clientMessageId, failed);
      await repository.persistMessages(pending.conversationId, list);
      notifyListeners();
      if (connected &&
          _mediaAwaitingReconnect.contains(pending.clientMessageId)) {
        unawaited(_resumeMediaAfterReconnect());
      }
      return failed;
    }
  }

  bool _isTransientMediaFailure(Object exception) {
    if (exception is SocketException ||
        exception is TimeoutException ||
        exception is HttpException) {
      return true;
    }
    final message = exception.toString().toLowerCase();
    return message.contains('socket') ||
        message.contains('connection abort') ||
        message.contains('connection closed') ||
        message.contains('network is unreachable') ||
        message.contains('software caused connection abort');
  }

  Future<void> _resumeMediaAfterReconnect() async {
    if (!connected || _mediaAwaitingReconnect.isEmpty) return;
    for (final clientMessageId in _mediaAwaitingReconnect.toList()) {
      if (!connected) return;
      if (_mediaAutomaticallyRetried.contains(clientMessageId)) {
        _mediaAwaitingReconnect.remove(clientMessageId);
        continue;
      }
      ChatMessage? failed;
      for (final messages in _messages.values) {
        failed = messages
            .where((message) => message.clientMessageId == clientMessageId)
            .firstOrNull;
        if (failed != null) break;
      }
      if (failed == null) {
        _mediaAwaitingReconnect.remove(clientMessageId);
        continue;
      }
      // A reconnect event can arrive just before the original send future
      // reports its failure. Leave it queued; the catch path invokes us again.
      if (failed.status == MessageStatus.sending) continue;
      if (failed.status != MessageStatus.failed) {
        _mediaAwaitingReconnect.remove(clientMessageId);
        continue;
      }
      _mediaAutomaticallyRetried.add(clientMessageId);
      _mediaAwaitingReconnect.remove(clientMessageId);
      final sending = failed.copyWith(status: MessageStatus.sending);
      _replaceMessage(failed.conversationId, failed.clientMessageId, sending);
      notifyListeners();
      await _sendPending(sending);
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
    if (_isMessageEditWindowExpired(message)) {
      error = '消息已超过 ${authPolicy.messageRecallMinutes} 分钟编辑时限';
      notifyListeners();
      return false;
    }
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

  bool isMessageEditable(ChatMessage message) =>
      message.isMine &&
      !message.id.startsWith('local-') &&
      message.status != MessageStatus.recalled &&
      message.status != MessageStatus.expired &&
      (message.kind == MessageContentKind.text ||
          message.kind == MessageContentKind.reply) &&
      !_isMessageEditWindowExpired(message);

  bool _isMessageEditWindowExpired(ChatMessage message) =>
      DateTime.now().difference(message.sentAt) >
      authPolicy.messageMutationWindow;

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
      return (await repository.pinnedMessages(
        conversationId,
      )).where(canReadMessage).toList();
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
    final local = messagesFor(
      conversationId,
    ).where((message) => message.text.toLowerCase().contains(normalized));
    try {
      final remote = await repository.searchMessages(
        conversationId,
        query.trim(),
      );
      final merged = <String, ChatMessage>{};
      for (final message in [...remote, ...local]) {
        if (!canReadMessage(message)) continue;
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

  Future<ChatMessage> revealSearchResult(ChatMessage message) async {
    final current = _messages[message.conversationId] ?? const <ChatMessage>[];
    for (final item in current) {
      if (item.id == message.id ||
          (message.clientMessageId.isNotEmpty &&
              item.clientMessageId == message.clientMessageId)) {
        // Search and recent-conversation synchronization can expose different
        // server ID representations for the same client message. The widget
        // tree is keyed by the already loaded canonical message, so return it
        // to the caller instead of trying to locate the search DTO's ID.
        return item;
      }
    }
    final merged = _mergeMessageLists(current, [message]);
    _messages[message.conversationId] = merged;
    notifyListeners();
    try {
      await repository.persistMessages(message.conversationId, merged);
    } catch (_) {
      // The authoritative search result stays visible in memory even if the
      // encrypted page snapshot cannot be updated at this moment.
    }
    return message;
  }

  Future<bool> recallMessage(ChatMessage message) async {
    if (!message.isMine || message.id.startsWith('local-')) {
      error = '这条消息当前不能撤回';
      notifyListeners();
      return false;
    }
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
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '撤回失败，可能已超过可撤回时间');
      notifyListeners();
      return false;
    }
  }

  Future<bool> deleteMessage(ChatMessage message) async {
    final list = _messages[message.conversationId];
    if (list == null) {
      error = '本机没有找到这条消息';
      notifyListeners();
      return false;
    }
    final updated = List<ChatMessage>.of(list)
      ..removeWhere((item) => item.clientMessageId == message.clientMessageId);
    try {
      await repository.persistMessages(message.conversationId, updated);
      _messages[message.conversationId] = updated;
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '消息删除失败，本机记录未修改');
      notifyListeners();
      return false;
    }
  }

  Future<bool> deleteMessages(
    String conversationId,
    Set<String> clientMessageIds,
  ) async {
    final list = _messages[conversationId];
    if (list == null || clientMessageIds.isEmpty) return false;
    final updated = List<ChatMessage>.of(list)
      ..removeWhere(
        (message) => clientMessageIds.contains(message.clientMessageId),
      );
    try {
      await repository.persistMessages(conversationId, updated);
      _messages[conversationId] = updated;
      for (final id in clientMessageIds) {
        _pendingMedia.remove(id);
      }
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '消息删除失败，本机记录未修改');
      notifyListeners();
      return false;
    }
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
    String? clientBatchId,
  }) async {
    final result = await _forwardAttempt(
      List.unmodifiable(messages),
      conversationId,
      mode: mode,
      clientBatchId: clientBatchId ?? _newClientMessageId(),
      sessionEpoch: _forwardSessionEpoch,
      sessionUserId: currentUser?.id,
    );
    if (!result.succeeded && !_disposed) {
      error = result.error;
      notifyListeners();
    }
    return result.messages;
  }

  ForwardBatchTask createForwardBatch(
    List<ChatMessage> messages,
    List<Conversation> targets, {
    required String mode,
  }) {
    final frozenMessages = List<ChatMessage>.unmodifiable(messages);
    final validation = validateForwardMessages(frozenMessages);
    if (validation != null) throw ArgumentError(validation);
    final sessionEpoch = _forwardSessionEpoch;
    final sessionUserId = currentUser?.id;
    late final ForwardBatchTask task;
    task = ForwardBatchTask(
      conversations: targets,
      createBatchId: _newClientMessageId,
      canContinue: () => _forwardSessionValid(sessionEpoch, sessionUserId),
      send: (targetId, batchId) => _forwardAttempt(
        frozenMessages,
        targetId,
        mode: mode,
        clientBatchId: batchId,
        sessionEpoch: sessionEpoch,
        sessionUserId: sessionUserId,
      ),
      onDispose: () => _forwardTasks.remove(task),
    );
    _forwardTasks.add(task);
    return task;
  }

  bool _forwardSessionValid(int epoch, String? userId) =>
      !_disposed &&
      authenticated &&
      userId != null &&
      currentUser?.id == userId &&
      _forwardSessionEpoch == epoch;

  void _invalidateForwardTasks() {
    _forwardSessionEpoch++;
    for (final task in _forwardTasks.toList()) {
      task.invalidateSession();
    }
  }

  Future<ForwardSendResult> _forwardAttempt(
    List<ChatMessage> messages,
    String conversationId, {
    required String mode,
    required String clientBatchId,
    required int sessionEpoch,
    required String? sessionUserId,
  }) async {
    final validation = validateForwardMessages(messages);
    if (validation != null) return ForwardSendResult.failure(validation);
    if (!_forwardSessionValid(sessionEpoch, sessionUserId)) {
      return const ForwardSendResult.failure(
        '登录状态已失效，请重新登录',
        sessionExpired: true,
      );
    }
    List<ChatMessage> forwarded;
    try {
      forwarded = await repository.forwardMessages(
        conversationId,
        messages.map((message) => message.id).toList(growable: false),
        mode: mode,
        clientBatchId: clientBatchId,
      );
    } catch (exception) {
      if (exception is ImApiException && exception.statusCode == 401) {
        if (_forwardSessionValid(sessionEpoch, sessionUserId)) {
          await _expireRestoredSession();
        }
        return const ForwardSendResult.failure(
          '登录状态已失效，请重新登录',
          sessionExpired: true,
        );
      }
      return ForwardSendResult.failure(
        _messageFor(exception, fallback: '转发失败，请稍后重试'),
      );
    }
    if (forwarded.isEmpty) {
      return const ForwardSendResult.failure('服务器未确认转发结果，请重试');
    }
    // An accepted send remains successful even if local cache persistence fails.
    // Never apply its response to a replacement login session.
    if (_forwardSessionValid(sessionEpoch, sessionUserId)) {
      final list = _messages[conversationId];
      if (list != null) {
        for (final item in forwarded) {
          if (!list.any((existing) => existing.id == item.id)) list.add(item);
        }
      }
      _updateConversation(conversationId, forwarded.last.text);
      notifyListeners();
      if (list != null) {
        try {
          await repository.persistMessages(conversationId, List.of(list));
        } catch (exception, stackTrace) {
          ClientDiagnostics.instance.captureError(
            'forward_cache_persistence',
            exception,
            stackTrace,
          );
        }
      }
    }
    return ForwardSendResult.success(forwarded);
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
    await repository.persistMessages(conversationId, const []);
    _messages[conversationId] = [];
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

  Future<void> registerWebPushDevice({
    required String deviceId,
    required String subscription,
    required bool notificationsEnabled,
    required bool previewEnabled,
    required bool soundEnabled,
    required bool vibrationEnabled,
  }) async {
    if (!authenticated || subscription.trim().isEmpty) return;
    await repository.registerDevice(
      deviceId: deviceId,
      platform: 'web',
      provider: 'webpush',
      pushToken: subscription.trim(),
      notificationsEnabled: notificationsEnabled,
      previewEnabled: previewEnabled,
      soundEnabled: soundEnabled,
      vibrationEnabled: vibrationEnabled,
    );
    _pushDeviceId = deviceId;
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

  Future<bool> toggleConversationSaved(String id) {
    final conversation = conversations.firstWhere(
      (item) => item.id == id,
      orElse: () => throw StateError('conversation not found'),
    );
    return _updateConversationPreferences(
      id,
      saved: !conversation.saved,
      localUpdate: (item) => item.copyWith(saved: !item.saved),
      fallback: conversation.saved ? '移出通讯录失败，请稍后重试' : '保存到通讯录失败，请稍后重试',
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
    bool? saved,
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
        saved: saved,
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
      _pendingOutgoingFriendUserIds.remove(request.user.id);
      try {
        final conversation = await repository.createDirect(request.user);
        _upsertConversation(conversation);
      } catch (_) {
        // New servers provision this conversation as part of accepting. This
        // compatibility call keeps the same real-data behavior on older
        // deployments where acceptance only created the friendship.
      }
      final contactIndex = contacts.indexWhere(
        (contact) => contact.id == request.user.id,
      );
      if (contactIndex >= 0) {
        contacts[contactIndex] = request.user;
      } else {
        contacts.add(request.user);
      }
      notifyListeners();
      try {
        contacts = await repository.contacts();
        notifyListeners();
      } catch (_) {
        // The accept mutation already succeeded. Keep the optimistic contact
        // usable and let the realtime event refresh the authoritative list.
      }
      try {
        conversations = await repository.conversations();
        _sortConversations();
        notifyListeners();
      } catch (_) {
        // The server has already accepted the request and provisioned the
        // direct conversation. Realtime invalidation or the next refresh will
        // reconcile a transient conversation-list failure.
      }
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
      _pendingOutgoingFriendUserIds.add(user.id);
      try {
        requests = await repository.friendRequests();
        _reconcilePendingOutgoingFriendUsers();
      } catch (_) {
        // Sending succeeded even if the immediate follow-up refresh did not.
        // Keep the profile in a waiting state until the realtime refresh lands.
        unawaited(_refreshSocial());
      }
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

  Future<List<AppUser>?> loadBlockedUsers() async {
    try {
      return await repository.blockedUsers();
    } catch (exception) {
      error = _messageFor(exception, fallback: '黑名单加载失败');
      notifyListeners();
      return null;
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
    bool? historyVisibleToNewMembers,
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
        historyVisibleToNewMembers: historyVisibleToNewMembers,
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
    required String gender,
    MediaUpload? avatar,
  }) async {
    final normalizedName = name.trim();
    final normalizedHandle = handle.trim().toLowerCase();
    final normalizedSignature = signature.trim();
    if (normalizedName.isEmpty) {
      error = '请输入昵称';
      notifyListeners();
      return false;
    }
    if (normalizedName.runes.length > 40) {
      error = '昵称不能超过 40 个字符';
      notifyListeners();
      return false;
    }
    if (!RegExp(r'^[a-z0-9_]{4,24}$').hasMatch(normalizedHandle)) {
      error = '呱呱号需为 4–24 位小写字母、数字或下划线';
      notifyListeners();
      return false;
    }
    if (normalizedSignature.runes.length > 160) {
      error = '个性签名不能超过 160 个字符';
      notifyListeners();
      return false;
    }
    if (gender != 'unspecified' && gender != 'male' && gender != 'female') {
      error = '请选择有效的性别展示方式';
      notifyListeners();
      return false;
    }
    error = null;
    try {
      String? avatarMediaId;
      String? avatarFingerprint;
      if (avatar == null) {
        _pendingProfileAvatarFingerprint = null;
        _pendingProfileAvatarMediaId = null;
      } else {
        avatarFingerprint = sha256.convert(avatar.bytes).toString();
        if (_pendingProfileAvatarFingerprint != avatarFingerprint) {
          _pendingProfileAvatarFingerprint = avatarFingerprint;
          _pendingProfileAvatarMediaId = null;
        }
        avatarMediaId = _pendingProfileAvatarMediaId;
        if (avatarMediaId == null) {
          avatarMediaId = await repository.uploadAvatar(avatar);
          _pendingProfileAvatarMediaId = avatarMediaId;
        }
      }
      currentUser = await repository.updateProfile(
        name: normalizedName,
        handle: normalizedHandle,
        signature: normalizedSignature,
        gender: gender,
        avatarMediaId: avatarMediaId,
      );
      if (avatarFingerprint == _pendingProfileAvatarFingerprint) {
        _pendingProfileAvatarFingerprint = null;
        _pendingProfileAvatarMediaId = null;
      }
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '个人资料保存失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> updatePrivacySettings({
    bool? allowSearchByHandle,
    bool? allowSearchByPhone,
  }) async {
    if (allowSearchByHandle == null && allowSearchByPhone == null) return true;
    error = null;
    try {
      currentUser = await repository.updateProfile(
        allowSearchByHandle: allowSearchByHandle,
        allowSearchByPhone: allowSearchByPhone,
      );
      notifyListeners();
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '隐私设置保存失败，请稍后重试');
      notifyListeners();
      return false;
    }
  }

  Future<bool> requestPhoneUpdateCode(String phone) async {
    final normalized = phone.trim();
    if (!RegExp(r'^\+?[0-9]{6,32}$').hasMatch(normalized)) {
      error = '请输入有效手机号';
      notifyListeners();
      return false;
    }
    if (normalized == currentUser?.phone?.trim()) {
      error = '新手机号不能与当前绑定相同';
      notifyListeners();
      return false;
    }
    try {
      await repository.requestPhoneChangeCode(normalized);
      return true;
    } catch (exception) {
      error = _messageFor(exception, fallback: '验证码发送失败');
      notifyListeners();
      return false;
    }
  }

  Future<bool> updatePhone(String phone, String code) async {
    final normalized = phone.trim();
    if (!RegExp(r'^\+?[0-9]{6,32}$').hasMatch(normalized) ||
        code.trim().length < 4) {
      error = '请输入有效手机号和验证码';
      notifyListeners();
      return false;
    }
    try {
      currentUser = await repository.updatePhone(normalized, code.trim());
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

  Future<List<UserDevice>?> loadUserDevices() async {
    try {
      return await repository.userDevices();
    } catch (exception) {
      error = _messageFor(exception, fallback: '登录设备加载失败');
      notifyListeners();
      return null;
    }
  }

  Future<List<ImDeviceSession>?> loadImDeviceSessions() async {
    try {
      return await repository.imDeviceSessions();
    } catch (exception) {
      error = _messageFor(exception, fallback: '登录会话加载失败');
      notifyListeners();
      return null;
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
      return (await repository.favorites()).where(canReadMessage).toList();
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
    _invalidateForwardTasks();
    loading = true;
    notifyListeners();
    final connectionSubscription = _connectionSubscription;
    final eventSubscription = _eventSubscription;
    _connectionSubscription = null;
    _eventSubscription = null;
    for (final subscription in [connectionSubscription, eventSubscription]) {
      if (subscription == null) continue;
      try {
        await subscription.cancel();
      } catch (exception, stackTrace) {
        ClientDiagnostics.instance.captureError(
          'logout_subscription_cleanup',
          exception,
          stackTrace,
        );
      }
    }
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
    try {
      await repository.logout();
    } catch (exception, stackTrace) {
      // Logging out is a local security boundary. Never leave the visible app
      // authenticated merely because server revocation or secure-store cleanup
      // failed; record the failure so it can be reconciled on the next launch.
      ClientDiagnostics.instance.captureError(
        'logout_repository_cleanup',
        exception,
        stackTrace,
      );
    }
    await _clearAuthenticatedState();
  }

  Future<void> _clearAuthenticatedState() async {
    _invalidateForwardTasks();
    authenticated = false;
    connected = false;
    connectionAttempted = false;
    connectionRetrying = false;
    currentUser = null;
    conversations = [];
    contacts = [];
    requests = [];
    groupInvitations = [];
    announcements = [];
    contactsLoadError = null;
    friendRequestsLoadError = null;
    groupInvitationsLoadError = null;
    announcementsLoadError = null;
    _messages.clear();
    _messageLoadOperations.clear();
    _messageCacheLoadOperations.clear();
    _pendingMedia.clear();
    _mediaUploadProgress.clear();
    _sendingMediaMessageIds.clear();
    _mediaAwaitingReconnect.clear();
    _mediaAutomaticallyRetried.clear();
    _pendingProfileAvatarFingerprint = null;
    _pendingProfileAvatarMediaId = null;
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
    _pendingOutgoingFriendUserIds.clear();
    _activeConversationId = null;
    _conversationRefreshTimer?.cancel();
    _conversationRefreshTimer = null;
    _conversationRefreshQueued = false;
    _linkPreviewAttempted.clear();
    messageErrors.clear();
    messageLoading.clear();
    messageHistoryErrors.clear();
    messageHistoryLoading.clear();
    _messageHistoryHasMore.clear();
    loading = false;
    if (!_disposed) notifyListeners();
  }

  void _handleEvent(ImEvent event) {
    switch (event.type) {
      case ImEventType.sessionExpired:
        unawaited(_expireRestoredSession());
        break;
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
      case ImEventType.groupHistoryChanged:
        groupHistoryRevision++;
        final cid = event.payload['conversationId']?.toString();
        if (cid != null) {
          final index = conversations.indexWhere((item) => item.id == cid);
          if (index >= 0) {
            conversations[index] = conversations[index].copyWith(
              subtitle: '打开会话查看消息',
              unread: 0,
              mentionUnreadCount: 0,
            );
          }
          _messages[cid]?.removeWhere((message) => !canReadMessage(message));
          _messageHistoryHasMore.remove(cid);
          // Cancel neither drafts nor optimistic sends. Fresh loads are gated by
          // the repository even when an earlier request completes afterwards.
          if (activeConversationId == cid) {
            unawaited(_reloadHistoryAfterPolicy(cid));
          }
        }
        _scheduleConversationRefresh();
      case ImEventType.conversationChanged:
        _scheduleConversationRefresh();
      case ImEventType.friendChanged:
        unawaited(_refreshSocial());
        _scheduleConversationRefresh();
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
    await Future.wait([refreshContacts(), refreshFriendRequests()]);
  }

  void _reconcilePendingOutgoingFriendUsers() {
    _pendingOutgoingFriendUserIds.removeWhere(
      (userId) =>
          contacts.any((contact) => contact.id == userId) ||
          requests.any((request) => request.user.id == userId),
    );
  }

  Future<void> _refreshGroupInvitations() async {
    await refreshGroupInvitations();
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
      robotId:
          body['robot_id'] as String? ??
          body['robotId'] as String? ??
          raw['robot_id'] as String? ??
          raw['robotId'] as String?,
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
      editedAt: tryParseLocalDateTime(raw['editedAt'] ?? body['editedAt']),
      isPinned:
          raw['isPinned'] as bool? ??
          body['isPinned'] as bool? ??
          raw['pinnedAt'] != null,
      pinnedAt: tryParseLocalDateTime(raw['pinnedAt'] ?? body['pinnedAt']),
      pinnedBy: raw['pinnedBy'] as String? ?? body['pinnedBy'] as String?,
      expiresAt: tryParseLocalDateTime(raw['expiresAt'] ?? body['expiresAt']),
      deliveredCount: (raw['deliveredCount'] as num?)?.toInt() ?? 0,
      readCount: (raw['readCount'] as num?)?.toInt() ?? 0,
      linkPreview: previewRaw == null ? null : LinkPreview.fromJson(previewRaw),
      sentAt: parseLocalDateTime(raw['createdAt']! as String),
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
    final message = exception.toString().trim();
    if (message.contains('TimeoutException')) {
      return '连接服务器超时，请稍后重试';
    }
    if (message.contains('HandshakeException') ||
        message.contains('CERTIFICATE_VERIFY_FAILED')) {
      return '安全连接失败，请检查手机时间或网络';
    }
    if (message.contains('ClientException') ||
        message.contains('Failed to fetch') ||
        message.contains('SocketException') ||
        message.contains('XMLHttpRequest error')) {
      return '无法连接服务器，请检查网络后重试';
    }
    if (message.isEmpty || message == 'Exception') {
      return fallback;
    }
    final normalized = message
        .replaceFirst('FormatException: ', '')
        .replaceFirst('ImApiException: ', '')
        .trim();
    final lower = normalized.toLowerCase();
    if (lower.contains('account already exists') ||
        lower.contains('already registered') ||
        lower.contains('user already exists')) {
      return '该手机号已注册，请直接登录';
    }
    if (lower.contains('invalid credentials') ||
        lower.contains('incorrect password') ||
        lower.contains('invalid password')) {
      return '手机号、密码或验证码不正确';
    }
    if (lower.contains('user not found') ||
        lower.contains('account not found')) {
      return '账号不存在，请先注册';
    }
    if (lower.contains('verification code') ||
        lower.contains('invalid code') ||
        lower.contains('code expired')) {
      return '验证码无效或已过期，请重新获取';
    }
    if (lower.contains('too many requests') || lower.contains('rate limit')) {
      return '操作过于频繁，请稍后再试';
    }
    if (lower.contains('handle is already in use')) {
      return '这个呱呱号已被使用，请换一个';
    }
    if (lower.contains('handle change limit reached')) {
      return '呱呱号修改次数已用完';
    }
    if (lower.contains('instant messaging service') ||
        lower.contains('im unavailable')) {
      return '即时通讯服务暂时不可用，请稍后重试';
    }
    if (lower.contains('verification provider') ||
        lower.contains('sms unavailable') ||
        lower.contains('sms not configured')) {
      return '短信验证码服务暂时不可用，请稍后重试';
    }
    if (lower.contains('group ownership') ||
        lower.contains('transfer ownership')) {
      return '请先转让群主或解散所管理的群聊';
    }
    if (RegExp(r'[\u3400-\u9fff]').hasMatch(normalized)) return normalized;
    return fallback;
  }

  @override
  void dispose() {
    _invalidateForwardTasks();
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

class _OptionalLoadResult<T> {
  const _OptionalLoadResult(this.value, [this.error]);

  final T value;
  final String? error;
}

class _MessageLoadResult {
  const _MessageLoadResult.success(this.messages)
    : error = null,
      stackTrace = null;

  const _MessageLoadResult.failure(this.error, this.stackTrace)
    : messages = null;

  final List<ChatMessage>? messages;
  final Object? error;
  final StackTrace? stackTrace;
}
