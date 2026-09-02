import 'dart:async';
import 'dart:io';
import 'dart:math' as math;

import 'package:audioplayers/audioplayers.dart';
import 'package:cached_network_image/cached_network_image.dart';
import 'package:file_picker/file_picker.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart'
    show kIsWeb, TargetPlatform, listEquals;
import 'package:flutter/material.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/rendering.dart' show ScrollDirection;
import 'package:flutter/semantics.dart';
import 'package:flutter/services.dart';
import 'package:geocoding/geocoding.dart';
import 'package:geolocator/geolocator.dart';
import 'package:image_picker/image_picker.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../calls/call_models.dart';
import '../../core/app_controller.dart';
import '../../core/app_config.dart';
import '../../core/app_theme.dart';
import '../../core/emoji_catalog.dart';
import '../../core/group_send_policy.dart';
import '../../core/image_export.dart';
import '../../core/image_send_editor.dart';
import '../../core/image_source_bytes.dart';
import '../../core/local_media_path.dart';
import '../../core/media_opener.dart';
import '../../core/models.dart';
import '../../core/screenshot_detection.dart';
import '../../core/user_identity.dart';
import '../../core/web_drop_paste.dart';
import '../../im/business_features.dart';
import '../widgets/linli_widgets.dart';
import '../widgets/forward_conversation_sheet.dart';
import '../widgets/media_send_widgets.dart';
import '../widgets/voice_composer_widgets.dart';
import '../voice_composer_controller.dart';
import 'moments_screen.dart';
import 'people_screens.dart';
import 'relationship_screens.dart';
import 'settings_preferences.dart';
import 'settings_screens.dart';
import 'sticker_store_screen.dart';

export '../voice_composer_controller.dart'
    show
        voiceRecordingShouldCancel,
        voiceRecordingIsTooShort,
        voiceRecordingDurationSeconds;

Route<T> chatScreenRoute<T>(
  BuildContext context, {
  required AppController controller,
  required Conversation conversation,
  String? initialMessageId,
}) {
  // Keep the previous in-memory page visible while every explicit reopen
  // reconciles with the server. loadMessages also hydrates an empty page from
  // disk and de-dupes this with ChatScreen's own initial load, so the route
  // animation never starts two syncs.
  unawaited(controller.loadMessages(conversation.id, force: true));
  final reduceMotion = MediaQuery.maybeOf(context)?.disableAnimations ?? false;
  if (!kIsWeb && Theme.of(context).platform == TargetPlatform.iOS) {
    return _IosChatPageRoute<T>(
      reduceMotion: reduceMotion,
      builder: (_) => ChatScreen(
        controller: controller,
        conversation: conversation,
        initialMessageId: initialMessageId,
      ),
    );
  }
  final duration = reduceMotion ? Duration.zero : nexaMotionDuration(context);
  return PageRouteBuilder<T>(
    opaque: true,
    maintainState: true,
    transitionDuration: duration,
    reverseTransitionDuration: duration,
    pageBuilder: (_, _, _) => ChatScreen(
      controller: controller,
      conversation: conversation,
      initialMessageId: initialMessageId,
    ),
    transitionsBuilder: (_, animation, _, child) {
      if (reduceMotion) return child;
      final curved = CurvedAnimation(
        parent: animation,
        curve: Curves.easeOutCubic,
        reverseCurve: Curves.easeInCubic,
      );
      return FadeTransition(
        opacity: Tween<double>(begin: .985, end: 1).animate(curved),
        child: SlideTransition(
          position: Tween<Offset>(
            begin: const Offset(.035, 0),
            end: Offset.zero,
          ).animate(curved),
          child: child,
        ),
      );
    },
  );
}

/// Retain Cupertino's edge gesture and interactive cancellation. A custom
/// horizontal detector on the whole chat would compete with message gestures.
class _IosChatPageRoute<T> extends CupertinoPageRoute<T> {
  _IosChatPageRoute({required super.builder, required this.reduceMotion});

  final bool reduceMotion;

  @override
  Duration get transitionDuration =>
      reduceMotion ? Duration.zero : super.transitionDuration;

  @override
  Duration get reverseTransitionDuration => transitionDuration;

  @override
  bool canTransitionFrom(TransitionRoute<dynamic> previousRoute) =>
      !reduceMotion && super.canTransitionFrom(previousRoute);

  @override
  Widget buildTransitions(
    BuildContext context,
    Animation<double> animation,
    Animation<double> secondaryAnimation,
    Widget child,
  ) => super.buildTransitions(
    context,
    // Keep the native gesture detector even when accessibility disables motion;
    // returning child directly here would silently disable swipe-to-go-back.
    reduceMotion ? const AlwaysStoppedAnimation<double>(1) : animation,
    reduceMotion ? const AlwaysStoppedAnimation<double>(0) : secondaryAnimation,
    child,
  );
}

class ChatScreen extends StatefulWidget {
  const ChatScreen({
    super.key,
    required this.controller,
    required Conversation conversation,
    this.initialMessageId,
    this.showDesktopDetails = false,
    this.onToggleDesktopDetails,
    this.settingsStore = const LocalSettingsStore(),
    this.chatBackgroundOverride,
  }) : _initialConversation = conversation;

  final AppController controller;
  final Conversation _initialConversation;
  final String? initialMessageId;
  final bool showDesktopDetails;
  final VoidCallback? onToggleDesktopDetails;
  final LocalSettingsStore settingsStore;
  final ChatBackgroundStyle? chatBackgroundOverride;

  Conversation get conversation =>
      controller.conversations
          .where((item) => item.id == _initialConversation.id)
          .firstOrNull ??
      _initialConversation;

  bool get conversationAvailable => controller.conversations.any(
    (item) => item.id == _initialConversation.id,
  );

  @override
  State<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScrollbar extends RawScrollbar {
  const _ChatScrollbar({
    super.key,
    required super.controller,
    required super.child,
    required super.thumbColor,
    required this.onTrackScroll,
  }) : super(
         thumbVisibility: true,
         interactive: true,
         thickness: 6,
         radius: const Radius.circular(3),
       );

  final VoidCallback onTrackScroll;

  @override
  RawScrollbarState<_ChatScrollbar> createState() => _ChatScrollbarState();
}

class _ChatScrollbarState extends RawScrollbarState<_ChatScrollbar> {
  @override
  void handleTrackTapDown(TapDownDetails details) {
    widget.onTrackScroll();
    super.handleTrackTapDown(details);
  }
}

class _ChatScreenState extends State<ChatScreen> {
  final textController = TextEditingController();
  final scrollController = ScrollController();
  final initialMessageKey = GlobalKey();
  final _messageCenterKey = GlobalKey();
  String? _messageListAnchorId;
  double _messageCenterInset = 12;
  bool showAttachments = false;
  bool showEmoji = false;
  bool selecting = false;
  final Set<String> selectedMessageIds = {};
  final Map<String, GlobalKey> _messageKeys = {};
  final Set<String> _pendingVoiceEntrances = {};
  final Map<String, MessageMention> _pendingMentions = {};
  ChatMessage? replyingTo;
  Timer? _draftTimer;
  Timer? _initialScrollTimer;
  Timer? _messageHighlightTimer;
  bool _draftReady = false;
  bool _loadingSendCapability = false;
  bool _sendCapabilityFailed = false;
  bool _messageScrollReady = false;
  bool _initialMessageLoadComplete = false;
  bool _followingLatest = true;
  bool _userScrolling = false;
  double _lastUserScrollDelta = 0;
  int _scrollEpoch = 0;
  int _conversationEpoch = 0;
  int? _queuedBottomEpoch;
  bool _registeredActiveConversation = false;
  String? _previousActiveConversationId;
  bool _loadingOlderFromScroll = false;
  String? _sendRestriction;
  Timer? _sendPolicyExpiryTimer;
  int _sendCapabilityRequest = 0;
  int _observedSendPolicyRevision = 0;
  GroupSendPolicy? _observedSendPolicy;
  String? _highlightedMessageId;
  List<RobotProfile> _robotProfiles = const [];
  bool _robotMenusLoading = false;
  bool _showRobotMenus = false;
  late final StreamSubscription<DateTime> _screenshotEvents;
  DateTime? _lastScreenshotNotice;
  ChatBackgroundStyle _chatBackground = ChatBackgroundStyle.followSystem;
  Conversation? _observedConversation;
  List<AppUser>? _observedContacts;
  bool _closingUnavailableGroup = false;

  AppUser? get peer =>
      widget.conversation.directPeerFor(widget.controller.currentUser?.id);
  String? get conversationAvatarUrl =>
      widget.conversation.avatarUrl ??
      (widget.conversation.kind == ConversationKind.group
          ? 'assets/brand/qingwaguagua-icon.png'
          : peer?.avatarUrl);

  bool get _isOrdinaryGroup =>
      widget.conversation.kind == ConversationKind.group &&
      !widget.conversation.isBusinessChannel;

  String? get _effectiveSendRestriction =>
      (_isOrdinaryGroup
          ? widget.controller
                .groupSendPolicyFor(widget.conversation.id)
                ?.restrictionAt(DateTime.now())
          : null) ??
      supportSessionSendRestriction(
        widget.conversation.channelType,
        widget.controller.messagesFor(widget.conversation.id),
      ) ??
      _sendRestriction;

  @override
  void initState() {
    super.initState();
    _followingLatest = widget.initialMessageId == null;
    _observedConversation = widget.conversation;
    _observedSendPolicyRevision = widget.controller.groupSendPolicyRevision;
    widget.controller.addListener(_handleConversationChanged);
    scrollController.addListener(_handleMessageScroll);
    HardwareKeyboard.instance.addHandler(_handleChatHardwareKey);
    _screenshotEvents = ScreenshotDetection.instance.events.listen(
      _handleScreenshot,
    );
    if (_isOrdinaryGroup ||
        (usesManagedBusinessChannelSendPolicy(widget.conversation) &&
            widget.controller.supportsBusinessFeatures)) {
      _loadingSendCapability = !_isOrdinaryGroup;
      unawaited(_loadSendCapability());
    }
    unawaited(_restoreDraft());
    unawaited(_loadRobotProfiles());
    final backgroundOverride = widget.chatBackgroundOverride;
    if (backgroundOverride != null) {
      _chatBackground = backgroundOverride;
    } else {
      unawaited(_loadChatBackground());
    }
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      _previousActiveConversationId = widget.controller.activeConversationId;
      widget.controller.setActiveConversation(widget.conversation.id);
      _registeredActiveConversation = true;
      unawaited(ScreenshotDetection.instance.start());
      _startInitialScrollPinning(window: const Duration(seconds: 2));
      unawaited(_loadInitialMessages());
    });
  }

  Future<void> _loadInitialMessages() async {
    // ChatScreen is also embedded directly in the desktop split view, where
    // there is no route helper to start the refresh. Always request a
    // reconciliation here; an in-flight route prefetch is de-duplicated and
    // existing in-memory messages stay visible while it completes.
    final conversationEpoch = _conversationEpoch;
    final scrollEpoch = _scrollEpoch;
    await widget.controller.loadMessages(widget.conversation.id, force: true);
    if (!mounted || conversationEpoch != _conversationEpoch) return;
    _initialMessageLoadComplete = true;
    if (widget.initialMessageId != null) {
      _initialScrollTimer?.cancel();
      if (scrollEpoch == _scrollEpoch) _scrollToInitialMessage();
      return;
    }
    _startInitialScrollPinning(window: const Duration(milliseconds: 900));
  }

  void _startInitialScrollPinning({required Duration window}) {
    if (!mounted || !_followingLatest) {
      return;
    }
    _initialScrollTimer?.cancel();
    var ticksRemaining = ((window.inMilliseconds + 49) ~/ 50).clamp(1, 100);
    _pinInitialMessagesToEnd();
    _initialScrollTimer = Timer.periodic(const Duration(milliseconds: 50), (
      timer,
    ) {
      if (!mounted || !_followingLatest) {
        timer.cancel();
        return;
      }
      _pinInitialMessagesToEnd();
      ticksRemaining -= 1;
      if (ticksRemaining > 0) return;
      timer.cancel();
      if (_initialMessageLoadComplete) _messageScrollReady = true;
    });
  }

  void _pinInitialMessagesToEnd() {
    if (!_followingLatest || _queuedBottomEpoch == _scrollEpoch) return;
    final epoch = _scrollEpoch;
    _queuedBottomEpoch = epoch;
    final binding = WidgetsBinding.instance;
    binding.addPostFrameCallback((_) {
      if (_queuedBottomEpoch == epoch) _queuedBottomEpoch = null;
      if (!mounted ||
          epoch != _scrollEpoch ||
          !_followingLatest ||
          !scrollController.hasClients ||
          widget.controller.messagesFor(widget.conversation.id).isEmpty) {
        return;
      }
      scrollController.jumpTo(scrollController.position.maxScrollExtent);
      if (_initialMessageLoadComplete) _messageScrollReady = true;
    });
    // A cache-only reopen may have no network notification or image decode to
    // request another frame. Explicitly schedule one so repeated end anchoring
    // can converge as the lazy message slivers discover their real extent.
    binding.ensureVisualUpdate();
  }

  void _pauseLatestFollowing() {
    _followingLatest = false;
    _initialScrollTimer?.cancel();
    _scrollEpoch += 1;
    _messageScrollReady = true;
  }

  bool _handleMessageScrollNotification(ScrollNotification notification) {
    if (notification.depth != 0) return false;
    // Wheel/trackpad pointerScroll emits a user direction BEFORE changing the
    // offset, but has no dragDetails. Thumb/touch drags also cancel a queued
    // bottom jump as soon as the drag starts. jumpTo/animateTo are not input.
    if ((notification is UserScrollNotification &&
            notification.direction != ScrollDirection.idle) ||
        (notification is ScrollStartNotification &&
            notification.dragDetails != null)) {
      _pauseLatestFollowing();
      _userScrolling = true;
      _lastUserScrollDelta = 0;
    }
    if (_userScrolling && notification is ScrollUpdateNotification) {
      final delta = notification.scrollDelta ?? 0;
      if (delta != 0) _lastUserScrollDelta = delta;
    }
    if (_userScrolling && notification is ScrollEndNotification) {
      _userScrolling = false;
      // Even a sub-2px UPWARD step means reading history. Only deliberately
      // returning towards the end resumes following, never a layout correction.
      if (_lastUserScrollDelta > 0 && notification.metrics.extentAfter <= 2) {
        _followingLatest = true;
        _pinInitialMessagesToEnd();
      }
    }
    return false;
  }

  bool _handleMessageMetrics(ScrollMetricsNotification notification) {
    if (notification.depth == 0 && _followingLatest) {
      _pinInitialMessagesToEnd();
    }
    return false;
  }

  Future<void> _loadChatBackground() async {
    final value = await widget.settingsStore.readChatBackground();
    if (mounted) setState(() => _chatBackground = value);
  }

  Future<void> _loadRobotProfiles() async {
    if (mounted) setState(() => _robotMenusLoading = true);
    try {
      final profiles = await widget.controller.robotProfilesForConversation(
        widget.conversation.id,
      );
      if (!mounted) return;
      setState(() {
        _robotProfiles = profiles;
        if (profiles.isEmpty) _showRobotMenus = false;
      });
    } catch (_) {
      // Ordinary conversations legitimately have no robot metadata. Keep the
      // composer clean if the optional menu endpoint is temporarily unavailable.
    } finally {
      if (mounted) setState(() => _robotMenusLoading = false);
    }
  }

  Future<void> _sendRobotCommand(RobotMenu menu) async {
    if (_effectiveSendRestriction case final restriction?) {
      _showError(restriction);
      return;
    }
    setState(() => _showRobotMenus = false);
    final sent = await widget.controller.sendRobotCommand(
      widget.conversation.id,
      menu,
    );
    if (!mounted) return;
    if (sent == null) {
      _showError(widget.controller.error ?? '机器人指令发送失败');
      return;
    }
    _scrollToEnd();
  }

  Color _chatBackgroundColor(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    return switch (_chatBackground) {
      ChatBackgroundStyle.followSystem =>
        dark ? LinliColors.darkBackground : LinliColors.pinnedSurface,
      ChatBackgroundStyle.softMint =>
        dark ? const Color(0xFF092019) : LinliColors.brandMint,
      ChatBackgroundStyle.cleanPaper =>
        dark ? LinliColors.navySoft : LinliColors.surface,
    };
  }

  Future<void> _loadSendCapability() async {
    if (!_isOrdinaryGroup &&
        (!usesManagedBusinessChannelSendPolicy(widget.conversation) ||
            !widget.controller.supportsBusinessFeatures)) {
      return;
    }
    final request = ++_sendCapabilityRequest;
    final conversationId = widget.conversation.id;
    if (mounted) {
      setState(() {
        _loadingSendCapability = !_isOrdinaryGroup;
        _sendCapabilityFailed = false;
      });
    }
    try {
      if (_isOrdinaryGroup) {
        await widget.controller.loadGroupSendPolicy(conversationId);
        if (!mounted || request != _sendCapabilityRequest) return;
        _scheduleSendPolicyExpiry();
        return;
      }
      final channel = await widget.controller.loadBusinessChannel(
        widget.conversation.channelId ?? widget.conversation.id,
        widget.conversation.channelType,
      );
      if (!mounted || request != _sendCapabilityRequest) return;
      setState(() {
        _sendRestriction = businessChannelSendRestriction(channel);
      });
    } catch (_) {
      if (!mounted || request != _sendCapabilityRequest) return;
      setState(() {
        // Retain known group restrictions on temporary lookup failures. With
        // no snapshot, sending still uses the authoritative server validation.
        _sendCapabilityFailed = !_isOrdinaryGroup;
        _sendRestriction = _isOrdinaryGroup ? null : '暂时无法确认发言权限，请重试后再发送。';
      });
    } finally {
      if (mounted && request == _sendCapabilityRequest) {
        setState(() => _loadingSendCapability = false);
      }
    }
  }

  void _scheduleSendPolicyExpiry() {
    _sendPolicyExpiryTimer?.cancel();
    final now = DateTime.now();
    final next = widget.controller
        .groupSendPolicyFor(widget.conversation.id)
        ?.nextChangeAfter(now);
    if (!_isOrdinaryGroup || next == null) return;
    final delay = next.difference(now) + const Duration(milliseconds: 20);
    // Permanent mute uses a far-future timestamp; bound browser timer delays.
    _sendPolicyExpiryTimer = Timer(
      delay > const Duration(days: 1) ? const Duration(days: 1) : delay,
      () {
        if (!mounted) return;
        setState(() {});
        _scheduleSendPolicyExpiry();
      },
    );
  }

  @override
  void dispose() {
    widget.controller.removeListener(_handleConversationChanged);
    HardwareKeyboard.instance.removeHandler(_handleChatHardwareKey);
    unawaited(_screenshotEvents.cancel());
    unawaited(ScreenshotDetection.instance.stop());
    _draftTimer?.cancel();
    _sendPolicyExpiryTimer?.cancel();
    _sendCapabilityRequest++;
    _initialScrollTimer?.cancel();
    _scrollEpoch += 1;
    _conversationEpoch += 1;
    _messageHighlightTimer?.cancel();
    if (_draftReady) {
      final conversationId = widget.conversation.id;
      final draft = textController.text;
      unawaited(
        Future<void>.microtask(
          () => widget.controller.saveDraft(
            conversationId,
            draft,
            // dispose 发生在框架锁定 widget 树期间，只持久化状态，避免同步触发根组件重建。
            notify: false,
          ),
        ),
      );
    }
    widget.controller.updateTyping(widget.conversation.id, false);
    if (_registeredActiveConversation &&
        widget.controller.activeConversationId == widget.conversation.id) {
      widget.controller.setActiveConversation(_previousActiveConversationId);
    }
    textController.dispose();
    scrollController.removeListener(_handleMessageScroll);
    scrollController.dispose();
    super.dispose();
  }

  @override
  void didUpdateWidget(covariant ChatScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.controller != widget.controller) {
      oldWidget.controller.removeListener(_handleConversationChanged);
      widget.controller.addListener(_handleConversationChanged);
    }
    if (oldWidget.conversation.id != widget.conversation.id ||
        oldWidget.controller != widget.controller) {
      _initialScrollTimer?.cancel();
      _messageHighlightTimer?.cancel();
      _scrollEpoch += 1;
      _conversationEpoch += 1;
      _sendPolicyExpiryTimer?.cancel();
      _sendCapabilityRequest++;
      _sendRestriction = null;
      _loadingSendCapability = false;
      _sendCapabilityFailed = false;
      _observedSendPolicy = null;
      _observedSendPolicyRevision = widget.controller.groupSendPolicyRevision;
      unawaited(_loadSendCapability());
      _followingLatest = widget.initialMessageId == null;
      _userScrolling = false;
      _messageScrollReady = false;
      _initialMessageLoadComplete = false;
      _loadingOlderFromScroll = false;
      _messageKeys.clear();
      _messageListAnchorId = null;
      final epoch = _conversationEpoch;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted || epoch != _conversationEpoch) return;
        widget.controller.setActiveConversation(widget.conversation.id);
        _startInitialScrollPinning(window: const Duration(seconds: 2));
        unawaited(_loadInitialMessages());
      });
    }
    _observedConversation = widget.conversation;
  }

  void _handleConversationChanged() {
    if (replyingTo != null &&
        !widget.controller.canDisplayMessage(replyingTo!)) {
      replyingTo = null;
    }
    final visibleIds = widget.controller
        .messagesFor(widget.conversation.id)
        .map((message) => message.clientMessageId)
        .toSet();
    selectedMessageIds.removeWhere((id) => !visibleIds.contains(id));
    if (!mounted) return;
    if (!widget.conversationAvailable &&
        widget._initialConversation.kind == ConversationKind.group) {
      if (_closingUnavailableGroup) return;
      _closingUnavailableGroup = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        Navigator.of(context).popUntil((route) => route.isFirst);
      });
      return;
    }
    final latest = widget.conversation;
    final policy = widget.controller.groupSendPolicyFor(latest.id);
    final policyChanged = !identical(policy, _observedSendPolicy);
    final revisionChanged =
        _observedSendPolicyRevision !=
        widget.controller.groupSendPolicyRevision;
    _observedSendPolicy = policy;
    _observedSendPolicyRevision = widget.controller.groupSendPolicyRevision;
    if (policyChanged) _scheduleSendPolicyExpiry();
    if (revisionChanged && _isOrdinaryGroup) unawaited(_loadSendCapability());
    if (identical(latest, _observedConversation) &&
        listEquals(widget.controller.contacts, _observedContacts) &&
        !policyChanged &&
        !revisionChanged) {
      return;
    }
    _observedConversation = latest;
    _observedContacts = List.of(widget.controller.contacts);
    setState(() {});
  }

  void _handleScreenshot(DateTime occurredAt) {
    if (!mounted || _effectiveSendRestriction != null) return;
    final previous = _lastScreenshotNotice;
    if (previous != null &&
        occurredAt.difference(previous).abs().inSeconds < 2) {
      return;
    }
    _lastScreenshotNotice = occurredAt;
    unawaited(
      widget.controller.sendScreenshotNotice(widget.conversation.id).then((
        notice,
      ) {
        if (mounted && widget.controller.canDisplayMessage(notice)) {
          _scrollToEnd();
        }
      }),
    );
  }

  bool _handleChatHardwareKey(KeyEvent event) {
    if (event is! KeyDownEvent) return false;
    if (event.logicalKey == LogicalKeyboardKey.escape) {
      _handleEscape();
      return true;
    }
    if (event.logicalKey == LogicalKeyboardKey.keyF &&
        (HardwareKeyboard.instance.isMetaPressed ||
            HardwareKeyboard.instance.isControlPressed)) {
      _showMessageSearch();
      return true;
    }
    return false;
  }

  Future<void> _openUserProfile(AppUser user) => Navigator.of(context).push(
    MaterialPageRoute(
      builder: (_) => FriendProfileScreen(
        controller: widget.controller,
        user: user,
        requestSource: 'conversation',
        requestSourceId: widget.conversation.id,
      ),
    ),
  );

  Widget _buildConversationAvatar() {
    final avatar = PersonAvatar(
      name: widget.controller.displayConversationName(widget.conversation),
      size: 34,
      avatarUrl: conversationAvatarUrl,
      online:
          widget.conversation.kind == ConversationKind.direct &&
          (peer?.isOnline ?? false),
    );
    final target = peer;
    if (widget.conversation.kind != ConversationKind.direct || target == null) {
      return avatar;
    }
    return Semantics(
      button: true,
      label: '查看${widget.controller.displayNameFor(target)}资料',
      child: InkResponse(
        key: const Key('chat-peer-avatar-button'),
        radius: 24,
        onTap: () => unawaited(_openUserProfile(target)),
        child: avatar,
      ),
    );
  }

  @override
  Widget build(BuildContext context) => CallbackShortcuts(
    bindings: {
      const SingleActivator(LogicalKeyboardKey.keyF, meta: true):
          _showMessageSearch,
      const SingleActivator(LogicalKeyboardKey.keyF, control: true):
          _showMessageSearch,
      const SingleActivator(LogicalKeyboardKey.escape): _handleEscape,
    },
    child: Focus(
      autofocus: true,
      child: WebDropPasteRegion(
        onFiles: _sendWebFiles,
        onError: _showError,
        child: Scaffold(
          appBar: AppBar(
            systemOverlayStyle: const SystemUiOverlayStyle(
              statusBarColor: Colors.transparent,
              statusBarIconBrightness: Brightness.dark,
              statusBarBrightness: Brightness.light,
            ),
            titleSpacing: 0,
            centerTitle: false,
            title: Row(
              children: [
                _buildConversationAvatar(),
                const SizedBox(width: 9),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(
                        widget.controller.displayConversationName(
                          widget.conversation,
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                      AnimatedBuilder(
                        animation: widget.controller,
                        builder: (context, _) {
                          final typing = widget.controller.typingLabelFor(
                            widget.conversation.id,
                          );
                          return Text(
                            typing ??
                                (widget.conversation.kind ==
                                        ConversationKind.group
                                    ? '${widget.conversation.memberCount} 位成员'
                                    : (peer?.isOnline ?? false)
                                    ? '在线'
                                    : '稍后回复'),
                            key: Key('chat-presence-${widget.conversation.id}'),
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: Theme.of(context).textTheme.labelSmall
                                ?.copyWith(
                                  color:
                                      typing != null ||
                                          (widget.conversation.kind ==
                                                  ConversationKind.direct &&
                                              (peer?.isOnline ?? false))
                                      ? LinliColors.systemGreen
                                      : LinliColors.preview,
                                ),
                          );
                        },
                      ),
                    ],
                  ),
                ),
              ],
            ),
            actions: [
              if (widget.conversation.kind == ConversationKind.group &&
                  !widget.conversation.isBusinessChannel)
                IconButton(
                  key: const Key('pinned-messages-button'),
                  tooltip: '置顶消息',
                  onPressed: _showPinnedMessages,
                  icon: const Icon(CupertinoIcons.pin),
                ),
              if (widget.conversation.kind == ConversationKind.direct &&
                  !widget.conversation.isBusinessChannel) ...[
                IconButton(
                  key: const Key('start-audio-call'),
                  tooltip: '语音通话',
                  onPressed: () => _startCall(CallMediaType.audio),
                  icon: const Icon(CupertinoIcons.phone),
                ),
                IconButton(
                  key: const Key('start-video-call'),
                  tooltip: '视频通话',
                  onPressed: () => _startCall(CallMediaType.video),
                  icon: const Icon(CupertinoIcons.video_camera),
                ),
              ],
              IconButton(
                key: const Key('chat-more-button'),
                tooltip: widget.showDesktopDetails ? '收起聊天信息' : '聊天信息',
                onPressed: _openChatInfo,
                icon: Icon(
                  widget.showDesktopDetails
                      ? CupertinoIcons.sidebar_right
                      : CupertinoIcons.ellipsis_vertical,
                ),
              ),
            ],
          ),
          body: Row(
            children: [
              Expanded(
                child: Column(
                  children: [
                    Expanded(
                      child: ColoredBox(
                        key: const Key('chat-background-surface'),
                        color: _chatBackgroundColor(context),
                        child: AnimatedBuilder(
                          animation: widget.controller,
                          builder: (context, _) => _buildMessages(),
                        ),
                      ),
                    ),
                    if (selecting)
                      _SelectionBar(
                        count: selectedMessageIds.length,
                        onCancel: () => setState(() {
                          selecting = false;
                          selectedMessageIds.clear();
                        }),
                        onDelete: selectedMessageIds.isEmpty
                            ? null
                            : () async {
                                final deleted = await widget.controller
                                    .deleteMessages(
                                      widget.conversation.id,
                                      Set.of(selectedMessageIds),
                                    );
                                if (!mounted) return;
                                if (!deleted) {
                                  _showError(
                                    widget.controller.error ?? '消息删除失败，本机记录未修改',
                                  );
                                  return;
                                }
                                setState(() {
                                  selecting = false;
                                  selectedMessageIds.clear();
                                });
                              },
                        onForward:
                            _selectedMessages.isEmpty ||
                                _selectedMessages.any(
                                  (message) =>
                                      message.id.startsWith('local-') ||
                                      message.status == MessageStatus.recalled,
                                )
                            ? null
                            : () => _chooseForwardMode(_selectedMessages),
                      )
                    else if (_loadingSendCapability)
                      ChannelSendRestrictionBar(
                        message: _isOrdinaryGroup
                            ? '正在确认群聊发言权限…'
                            : '正在确认频道发言权限…',
                        loading: true,
                      )
                    else if (_effectiveSendRestriction case final restriction?)
                      ChannelSendRestrictionBar(
                        message: restriction,
                        onRetry: _sendCapabilityFailed
                            ? _loadSendCapability
                            : null,
                      )
                    else
                      AnimatedBuilder(
                        animation: widget.controller,
                        builder: (context, _) {
                          if (widget.controller.messagingUnavailable) {
                            return MessagingConnectionBanner(
                              retrying: widget.controller.connectionRetrying,
                              onRetry: widget.controller.retryConnection,
                            );
                          }
                          return Column(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              if (_robotMenusLoading &&
                                  _robotProfiles.isNotEmpty)
                                const LinearProgressIndicator(minHeight: 2),
                              if (_robotProfiles.isNotEmpty)
                                RobotCommandBar(
                                  profiles: _robotProfiles,
                                  expanded: _showRobotMenus,
                                  onToggle: () => setState(() {
                                    _showRobotMenus = !_showRobotMenus;
                                    showAttachments = false;
                                    showEmoji = false;
                                    FocusScope.of(context).unfocus();
                                  }),
                                  onSelected: _sendRobotCommand,
                                ),
                              ChatComposer(
                                controller: textController,
                                replyingTo: replyingTo,
                                replyingToName: replyingTo == null
                                    ? null
                                    : widget.controller.displayNameForId(
                                        replyingTo!.senderId,
                                        fallback: replyingTo!.senderName,
                                      ),
                                showAttachments: showAttachments,
                                showEmoji: showEmoji,
                                onCancelReply: () =>
                                    setState(() => replyingTo = null),
                                onToggleAttachments: () => setState(() {
                                  showAttachments = !showAttachments;
                                  showEmoji = false;
                                  _showRobotMenus = false;
                                }),
                                onToggleEmoji: () => setState(() {
                                  showEmoji = !showEmoji;
                                  showAttachments = false;
                                  _showRobotMenus = false;
                                }),
                                onAttachment: _pickAttachment,
                                allowLiveInteraction:
                                    widget.conversation.channelType == 9,
                                onVoiceReady: _sendVoice,
                                onMention:
                                    widget.conversation.kind ==
                                        ConversationKind.group
                                    ? _pickMention
                                    : null,
                                onTypingChanged: (typing) =>
                                    widget.controller.updateTyping(
                                      widget.conversation.id,
                                      typing,
                                    ),
                                onSend: _send,
                                onSendOptions: _showSendOptions,
                              ),
                            ],
                          );
                        },
                      ),
                  ],
                ),
              ),
              if (widget.showDesktopDetails) ...[
                VerticalDivider(
                  width: 1,
                  color: Theme.of(context).colorScheme.outline,
                ),
                SizedBox(
                  key: const Key('desktop-chat-details-panel'),
                  width: 320,
                  child: _ChatInfoContent(
                    controller: widget.controller,
                    conversation: widget.conversation,
                    onSearch: _showMessageSearch,
                    onClearLocal: _confirmClearLocal,
                    onBlock: _confirmBlock,
                    onScheduledMessages: _showScheduledMessages,
                    onClose: widget.onToggleDesktopDetails,
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    ),
  );

  void _handleEscape() {
    if (selecting || showAttachments || showEmoji || replyingTo != null) {
      setState(() {
        selecting = false;
        selectedMessageIds.clear();
        showAttachments = false;
        showEmoji = false;
        replyingTo = null;
      });
      return;
    }
    final navigator = Navigator.of(context);
    if (navigator.canPop()) navigator.maybePop();
  }

  Future<void> _sendWebFiles(List<WebPickedFile> files) async {
    if (_effectiveSendRestriction case final restriction?) {
      _showError(restriction);
      return;
    }
    if (files.length > 10) {
      _showError('一次最多发送 10 个文件');
      return;
    }
    try {
      for (final file in files) {
        _validateMediaSize(file.bytes.length);
      }
      final reply = replyingTo;
      setState(() {
        replyingTo = null;
        showAttachments = false;
        showEmoji = false;
      });
      for (final file in files) {
        final mime = file.mimeType == 'application/octet-stream'
            ? _mimeFor(file.name)
            : file.mimeType;
        final kind = mime.startsWith('image/')
            ? MessageContentKind.image
            : mime.startsWith('video/')
            ? MessageContentKind.video
            : MessageContentKind.file;
        final sent = await widget.controller.sendMedia(
          widget.conversation.id,
          MediaUpload(
            bytes: file.bytes,
            fileName: file.name,
            mimeType: mime,
            kind: kind,
          ),
          replyTo: reply,
        );
        if (sent.status == MessageStatus.failed) {
          _showError(sent.sendError ?? '“${file.name}”发送失败，可在消息旁重试');
          break;
        }
        _scrollToEnd();
      }
    } on FormatException catch (error) {
      _showError(error.message);
    } catch (_) {
      _showError('文件发送失败，请稍后重试');
    }
  }

  Future<void> _startCall(CallMediaType mediaType) async {
    final calls = widget.controller.callController;
    if (calls == null) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('当前环境未启用音视频通话')));
      return;
    }
    try {
      await calls.startCall(widget.conversation, mediaType);
    } catch (error) {
      if (!mounted) return;
      final message =
          calls.errorMessage ??
          error.toString().replaceFirst('Bad state: ', '');
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(message)));
    }
  }

  Widget _buildMessages() {
    final messages = widget.controller.messagesFor(widget.conversation.id);
    if (messages.isEmpty &&
        widget.controller.messageLoading.contains(widget.conversation.id)) {
      return const Center(child: CupertinoActivityIndicator());
    }
    final loadError = widget.controller.messageErrors[widget.conversation.id];
    if (loadError != null && messages.isEmpty) {
      return StatePanel(
        icon: CupertinoIcons.exclamationmark_bubble,
        title: '消息加载失败',
        body: loadError,
        actionLabel: '重试',
        onAction: () =>
            widget.controller.loadMessages(widget.conversation.id, force: true),
      );
    }
    if (messages.isEmpty) {
      return const StatePanel(
        icon: CupertinoIcons.chat_bubble,
        title: '从一句问候开始',
        body: '发送后的消息会通过服务器在你的设备间同步。',
      );
    }
    final latestMineId = messages
        .where((message) => message.isMine)
        .lastOrNull
        ?.id;
    var anchorIndex = messages.indexWhere(
      (message) => message.stableIdentity == _messageListAnchorId,
    );
    if (anchorIndex < 0) {
      anchorIndex = 0;
      _messageListAnchorId = messages.first.stableIdentity;
      _messageCenterInset =
          widget.controller.messageHistoryHasMore(widget.conversation.id)
          ? 48
          : 12;
    }
    Widget buildMessage(BuildContext context, int messageIndex) {
      final message = messages[messageIndex];
      final previous = messageIndex == 0 ? null : messages[messageIndex - 1];
      final showTime =
          messageIndex == anchorIndex ||
          previous == null ||
          !_sameDay(message.sentAt, previous.sentAt) ||
          message.sentAt.difference(previous.sentAt).inMinutes >= 15;
      final sender = widget.conversation.members
          .where((user) => user.id == message.senderId)
          .firstOrNull;
      final displaySender =
          sender ??
          (message.isMine
              ? widget.controller.currentUser
              : widget.conversation.kind == ConversationKind.direct
              ? peer
              : null);
      final displaySenderName = displaySender?.name.trim().isNotEmpty == true
          ? widget.controller.displayNameFor(displaySender!)
          : widget.controller.displayNameForId(
              message.senderId,
              fallback: message.senderName,
            );
      final stableMessageId = message.stableIdentity;
      final messageKey = message.id == widget.initialMessageId
          ? initialMessageKey
          : _messageKeys.putIfAbsent(stableMessageId, GlobalKey.new);
      if (message.id.isNotEmpty) _messageKeys[message.id] = messageKey;
      return AnimatedContainer(
        key: messageKey,
        duration: const Duration(milliseconds: 220),
        curve: Curves.easeOutCubic,
        decoration: BoxDecoration(
          color: _highlightedMessageId == message.id
              ? LinliColors.brandGreen.withValues(alpha: .16)
              : Colors.transparent,
          borderRadius: BorderRadius.circular(14),
        ),
        child: Column(
          children: [
            if (showTime) _TimeDivider(date: message.sentAt),
            VoiceMessageEntrance(
              key: ValueKey('voice-entrance-${message.clientMessageId}'),
              animate: _pendingVoiceEntrances.remove(message.clientMessageId),
              child: MessageBubble(
                message: message,
                controller: widget.controller,
                senderName: displaySenderName,
                avatarUrl:
                    displaySender?.avatarUrl ??
                    (widget.conversation.kind == ConversationKind.direct
                        ? peer?.avatarUrl
                        : null),
                onAvatarTap: displaySender == null
                    ? null
                    : () => unawaited(_openUserProfile(displaySender)),
                showSender: widget.conversation.kind == ConversationKind.group,
                showGroupReceipt:
                    widget.conversation.kind == ConversationKind.group &&
                    widget.controller.canDisplayMessageReceipts(
                      widget.conversation.id,
                    ) &&
                    message.id == latestMineId,
                onRetry:
                    _effectiveSendRestriction == null && !_loadingSendCapability
                    ? () => widget.controller.retryMessage(message)
                    : null,
                selectionMode: selecting,
                selected: selectedMessageIds.contains(message.clientMessageId),
                onSelect: () => _toggleSelection(message),
                onLongPress: selecting
                    ? (_) => _toggleSelection(message)
                    : (position) => _showMessageActions(message, position),
                onReactionTap: (emoji) =>
                    widget.controller.toggleReaction(message, emoji),
                onAddReaction: () => _showReactionPicker(message),
              ),
            ),
          ],
        ),
      );
    }

    // Older pages grow UP from a stable message, while newer messages grow
    // down. Flutter preserves the viewport's coordinate system even for lazy,
    // variable-height rows; no guessed extent delta or stale saved offset is
    // applied when a history request completes during an active user scroll.
    final list = LayoutBuilder(
      builder: (context, constraints) => CustomScrollView(
        key: const Key('message-list'),
        controller: scrollController,
        center: _messageCenterKey,
        anchor: constraints.maxHeight > 0
            ? (_messageCenterInset / constraints.maxHeight).clamp(0.0, 1.0)
            : 0,
        keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
        slivers: [
          SliverPadding(
            padding: const EdgeInsets.fromLTRB(12, 12, 12, 0),
            sliver: SliverList.builder(
              itemCount: anchorIndex + 1,
              itemBuilder: (context, index) => index == anchorIndex
                  ? _buildMessageHistoryHeader()
                  : buildMessage(context, anchorIndex - index - 1),
            ),
          ),
          SliverPadding(
            key: _messageCenterKey,
            padding: const EdgeInsets.fromLTRB(12, 0, 12, 16),
            sliver: SliverList.builder(
              itemCount: messages.length - anchorIndex,
              itemBuilder: (context, index) =>
                  buildMessage(context, anchorIndex + index),
            ),
          ),
        ],
      ),
    );
    final platform = Theme.of(context).platform;
    final desktop =
        platform == TargetPlatform.windows ||
        platform == TargetPlatform.linux ||
        platform == TargetPlatform.macOS ||
        useLinliDesktopLayout(MediaQuery.sizeOf(context).width);
    return NotificationListener<ScrollMetricsNotification>(
      onNotification: _handleMessageMetrics,
      child: NotificationListener<ScrollNotification>(
        onNotification: _handleMessageScrollNotification,
        child: ScrollConfiguration(
          // Keep the inherited drag devices (no left-mouse list dragging).
          // The explicit thumb shares this list's controller on every desktop.
          behavior: ScrollConfiguration.of(context).copyWith(scrollbars: false),
          child: desktop
              ? _ChatScrollbar(
                  key: const Key('message-scrollbar'),
                  controller: scrollController,
                  thumbColor: Theme.of(
                    context,
                  ).colorScheme.onSurface.withValues(alpha: .3),
                  onTrackScroll: () {
                    // RawScrollbar pages via moveTo for a track click, which
                    // intentionally has no UserScrollNotification of its own.
                    _pauseLatestFollowing();
                    _userScrolling = true;
                    _lastUserScrollDelta = 0;
                  },
                  child: list,
                )
              : list,
        ),
      ),
    );
  }

  Widget _buildMessageHistoryHeader() {
    final conversationId = widget.conversation.id;
    final loading = widget.controller.messageHistoryLoading.contains(
      conversationId,
    );
    final error = widget.controller.messageHistoryErrors[conversationId];
    final hasMore = widget.controller.messageHistoryHasMore(conversationId);
    final textStyle = Theme.of(context).textTheme.labelMedium?.copyWith(
      color: Theme.of(context).colorScheme.onSurfaceVariant,
      fontWeight: FontWeight.w500,
    );

    if (loading) {
      return SizedBox(
        key: const Key('older-messages-loading'),
        height: 36,
        child: Center(
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const CupertinoActivityIndicator(radius: 8),
              const SizedBox(width: 8),
              Text('正在加载更早的消息', style: textStyle),
            ],
          ),
        ),
      );
    }
    if (error != null) {
      return SizedBox(
        key: const Key('older-messages-error'),
        height: 36,
        child: Center(
          child: TextButton(
            onPressed: _loadOlderMessages,
            child: const Text('较早消息加载失败，点此重试'),
          ),
        ),
      );
    }
    if (hasMore) {
      return SizedBox(
        key: const Key('load-older-messages'),
        height: 36,
        child: Center(
          child: TextButton(
            onPressed: _loadOlderMessages,
            child: const Text('加载更早的消息'),
          ),
        ),
      );
    }
    return const SizedBox.shrink(key: Key('no-older-messages'));
  }

  void _handleMessageScroll() {
    if (!_messageScrollReady ||
        !_userScrolling ||
        _loadingOlderFromScroll ||
        !scrollController.hasClients ||
        scrollController.position.pixels -
                scrollController.position.minScrollExtent >
            120) {
      return;
    }
    if (!widget.controller.messageHistoryHasMore(widget.conversation.id)) {
      return;
    }
    unawaited(_loadOlderMessages());
  }

  Future<void> _loadOlderMessages() async {
    if (_loadingOlderFromScroll || !scrollController.hasClients) return;
    _pauseLatestFollowing();
    _loadingOlderFromScroll = true;
    final conversationEpoch = _conversationEpoch;
    await widget.controller.loadOlderMessages(widget.conversation.id);
    if (!mounted || conversationEpoch != _conversationEpoch) return;
    // Keep the in-flight guard until the expanded range has been laid out.
    await WidgetsBinding.instance.endOfFrame;
    if (!mounted || conversationEpoch != _conversationEpoch) return;
    _loadingOlderFromScroll = false;
  }

  Future<void> _send([int? expiresInSeconds]) async {
    if (_effectiveSendRestriction case final restriction?) {
      _showError(restriction);
      return;
    }
    final text = textController.text;
    if (text.trim().isEmpty) return;
    final reply = replyingTo;
    final mentions = _pendingMentions.values
        .where(
          (mention) =>
              text.contains(mention.isEveryone ? '@所有人' : '@${mention.name}'),
        )
        .toList();
    textController.clear();
    await widget.controller.saveDraft(widget.conversation.id, '');
    setState(() {
      replyingTo = null;
      showEmoji = false;
      showAttachments = false;
      _pendingMentions.clear();
    });
    final future = widget.controller.sendMessage(
      widget.conversation.id,
      text,
      replyTo: reply,
      mentions: mentions,
      expiresInSeconds: expiresInSeconds,
    );
    _scrollToEnd();
    final sent = await future;
    if (sent?.status == MessageStatus.failed && mounted) {
      _showError(
        sent?.sendError ??
            (mentions.any((mention) => mention.isEveryone)
                ? '“@所有人”发送失败，请确认当前账号是群主或管理员'
                : '消息发送失败，请检查网络后重试'),
      );
    }
  }

  Future<void> _showSendOptions() async {
    if (textController.text.trim().isEmpty) return;
    final action = await showCupertinoModalPopup<String>(
      context: context,
      builder: (context) => CupertinoActionSheet(
        title: const Text('更多发送方式'),
        message: const Text('定时消息由服务器执行，退出应用后仍会按时发送。'),
        actions: [
          CupertinoActionSheetAction(
            key: const Key('schedule-message-action'),
            onPressed: () => Navigator.pop(context, 'schedule'),
            child: const Text('定时发送'),
          ),
          CupertinoActionSheetAction(
            key: const Key('expire-message-hour-action'),
            onPressed: () => Navigator.pop(context, 'expire-hour'),
            child: const Text('发送并在 1 小时后过期'),
          ),
          CupertinoActionSheetAction(
            key: const Key('expire-message-day-action'),
            onPressed: () => Navigator.pop(context, 'expire-day'),
            child: const Text('发送并在 24 小时后过期'),
          ),
        ],
        cancelButton: CupertinoActionSheetAction(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
      ),
    );
    if (!mounted || action == null) return;
    if (action == 'expire-hour') {
      await _send(3600);
    } else if (action == 'expire-day') {
      await _send(86400);
    } else {
      await _pickScheduleTime();
    }
  }

  Future<void> _pickScheduleTime() async {
    var selected = DateTime.now().add(const Duration(minutes: 10));
    final scheduledAt = await showModalBottomSheet<DateTime>(
      context: context,
      useSafeArea: true,
      isScrollControlled: true,
      builder: (context) => SizedBox(
        height: 380,
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 16, 12, 4),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      '选择发送时间',
                      style: Theme.of(context).textTheme.titleLarge,
                    ),
                  ),
                  TextButton(
                    key: const Key('confirm-schedule-time'),
                    onPressed: () => Navigator.pop(context, selected),
                    child: const Text('确定'),
                  ),
                ],
              ),
            ),
            Expanded(
              child: CupertinoDatePicker(
                key: const Key('schedule-date-picker'),
                mode: CupertinoDatePickerMode.dateAndTime,
                minimumDate: DateTime.now().add(const Duration(minutes: 1)),
                initialDateTime: selected,
                use24hFormat: true,
                onDateTimeChanged: (value) => selected = value,
              ),
            ),
          ],
        ),
      ),
    );
    if (!mounted || scheduledAt == null) return;
    final success = await widget.controller.scheduleMessage(
      widget.conversation.id,
      textController.text,
      scheduledAt,
      replyTo: replyingTo,
    );
    if (!mounted) return;
    if (!success) {
      _showError(
        widget.controller.scheduledMessageErrors[widget.conversation.id] ??
            widget.controller.error ??
            '定时消息创建失败',
      );
      return;
    }
    textController.clear();
    await widget.controller.saveDraft(widget.conversation.id, '');
    if (!mounted) return;
    setState(() {
      replyingTo = null;
      showEmoji = false;
      showAttachments = false;
      _pendingMentions.clear();
    });
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('已交由服务器定时发送')));
  }

  Future<void> _showScheduledMessages() async {
    unawaited(
      widget.controller.loadScheduledMessages(
        widget.conversation.id,
        force: true,
      ),
    );
    await showModalBottomSheet<void>(
      context: context,
      useSafeArea: true,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (_) => _ScheduledMessagesSheet(
        controller: widget.controller,
        conversationId: widget.conversation.id,
      ),
    );
  }

  Future<void> _restoreDraft() async {
    final draft = await widget.controller.loadDraft(widget.conversation.id);
    if (!mounted) return;
    textController.text = draft;
    textController.selection = TextSelection.collapsed(offset: draft.length);
    _draftReady = true;
    textController.addListener(_scheduleDraftSave);
  }

  void _scheduleDraftSave() {
    if (!_draftReady) return;
    _draftTimer?.cancel();
    _draftTimer = Timer(const Duration(milliseconds: 350), () {
      unawaited(
        widget.controller.saveDraft(
          widget.conversation.id,
          textController.text,
        ),
      );
    });
  }

  void _toggleSelection(ChatMessage message) => setState(() {
    selecting = true;
    if (!selectedMessageIds.add(message.clientMessageId)) {
      selectedMessageIds.remove(message.clientMessageId);
    }
  });

  List<ChatMessage> get _selectedMessages => widget.controller
      .messagesFor(widget.conversation.id)
      .where((message) => selectedMessageIds.contains(message.clientMessageId))
      .toList();

  Future<void> _pickMention() async {
    final mention = await showModalBottomSheet<MessageMention>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      showDragHandle: true,
      builder: (context) => _MentionPickerSheet(
        controller: widget.controller,
        members: widget.conversation.members,
        currentUserId: widget.controller.currentUser?.id,
        canMentionEveryone: widget.conversation.canMentionEveryone,
      ),
    );
    if (!mounted || mention == null) return;
    final token = mention.isEveryone ? '@所有人 ' : '@${mention.name} ';
    final selection = textController.selection;
    final start = selection.isValid
        ? selection.start
        : textController.text.length;
    final end = selection.isValid ? selection.end : textController.text.length;
    final updated = textController.text.replaceRange(start, end, token);
    // Update text and selection atomically. Assigning `.text` first briefly
    // resets the selection to -1; Android IMEs can then restore the old cursor
    // inside the inserted nickname when the mention sheet closes.
    textController.value = TextEditingValue(
      text: updated,
      selection: TextSelection.collapsed(offset: start + token.length),
    );
    setState(() {
      _pendingMentions[mention.userId] = mention;
      showAttachments = false;
      showEmoji = false;
    });
  }

  Future<void> _showReactionPicker(ChatMessage message) async {
    const options = ['👍', '❤️', '😂', '🎉', '😮', '🙏'];
    final emoji = await showCupertinoModalPopup<String>(
      context: context,
      builder: (context) => CupertinoActionSheet(
        title: const Text('回应消息'),
        message: const Text('再次选择已有回应可撤销。'),
        actions: [
          for (final option in options)
            CupertinoActionSheetAction(
              key: Key('reaction-option-$option'),
              onPressed: () => Navigator.pop(context, option),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Text(option, style: const TextStyle(fontSize: 24)),
                  if (message.reactions.any(
                    (reaction) =>
                        reaction.emoji == option && reaction.reactedByMe,
                  )) ...[
                    const SizedBox(width: 8),
                    const Icon(CupertinoIcons.checkmark_alt, size: 18),
                  ],
                ],
              ),
            ),
        ],
        cancelButton: CupertinoActionSheetAction(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
      ),
    );
    if (!mounted || emoji == null) return;
    final success = await widget.controller.toggleReaction(message, emoji);
    if (!success && mounted) _showError(widget.controller.error ?? '回应失败');
  }

  Future<void> _editMessage(ChatMessage message) async {
    final controller = TextEditingController(text: message.text);
    final edited = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('编辑消息'),
        content: TextField(
          key: const Key('edit-message-input'),
          controller: controller,
          autofocus: true,
          minLines: 2,
          maxLines: 6,
          maxLength: 5000,
          decoration: const InputDecoration(hintText: '输入消息内容'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('取消'),
          ),
          FilledButton(
            key: const Key('save-message-edit'),
            onPressed: () {
              final value = controller.text.trim();
              if (value.isNotEmpty && value != message.text) {
                Navigator.pop(context, value);
              }
            },
            child: const Text('保存修改'),
          ),
        ],
      ),
    );
    controller.dispose();
    if (!mounted || edited == null) return;
    final success = await widget.controller.editMessage(message, edited);
    if (!success && mounted) _showError(widget.controller.error ?? '编辑失败');
  }

  Future<void> _showPinnedMessages() async {
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      showDragHandle: true,
      builder: (context) => _PinnedMessagesSheet(
        controller: widget.controller,
        conversationId: widget.conversation.id,
        onSelected: (message) {
          Navigator.pop(context);
          _scrollToMessage(message.id);
        },
      ),
    );
  }

  Future<void> _scrollToMessage(
    String messageId, {
    ChatMessage? searchResult,
  }) async {
    // Programmatic navigation is an explicit request to leave the newest
    // message. Stop the startup end-pinning first; otherwise its metrics
    // listener observes this scroll and immediately jumps back to the bottom.
    _pauseLatestFollowing();
    _userScrolling = false;
    final epoch = _scrollEpoch;
    var targetMessageId = messageId;
    if (searchResult != null) {
      final canonical = await widget.controller.revealSearchResult(
        searchResult,
      );
      targetMessageId = canonical.id;
      if (!mounted || epoch != _scrollEpoch) return;
      await WidgetsBinding.instance.endOfFrame;
      if (!mounted || epoch != _scrollEpoch) return;
    }
    final duration = nexaMotionDuration(context);
    var target = _messageKeys[targetMessageId]?.currentContext;
    if (target == null) {
      await widget.controller.loadMessages(widget.conversation.id, force: true);
      if (!mounted || epoch != _scrollEpoch) return;
      await WidgetsBinding.instance.endOfFrame;
      if (!mounted || epoch != _scrollEpoch) return;
      target = _messageKeys[targetMessageId]?.currentContext;
    }
    if (target == null && scrollController.hasClients) {
      final messages = widget.controller.messagesFor(widget.conversation.id);
      final index = messages.indexWhere(
        (message) => message.id == targetMessageId,
      );
      if (index >= 0 && messages.length > 1) {
        final fraction = index / (messages.length - 1);
        final position = scrollController.position;
        scrollController.jumpTo(
          position.minScrollExtent +
              (position.maxScrollExtent - position.minScrollExtent) * fraction,
        );
        await WidgetsBinding.instance.endOfFrame;
        if (!mounted || epoch != _scrollEpoch) return;
        target = _messageKeys[targetMessageId]?.currentContext;
      }
    }
    if (target == null) {
      _showError('该消息不在当前已加载范围内');
      return;
    }
    if (!target.mounted) return;
    await Scrollable.ensureVisible(
      target,
      alignment: .35,
      duration: duration,
      curve: Curves.easeOutCubic,
    );
    if (!mounted) return;
    _messageHighlightTimer?.cancel();
    setState(() => _highlightedMessageId = targetMessageId);
    _messageHighlightTimer = Timer(const Duration(seconds: 2), () {
      if (mounted && _highlightedMessageId == targetMessageId) {
        setState(() => _highlightedMessageId = null);
      }
    });
  }

  Future<void> _pickAttachment(String label) async {
    try {
      if (label == '直播互动') {
        await _showLiveInteraction();
        return;
      }
      if (label == '表情') {
        final sticker = await showStickerPicker(context, widget.controller);
        if (!mounted || sticker == null) return;
        setState(() => showAttachments = false);
        unawaited(
          widget.controller.sendSticker(widget.conversation.id, sticker),
        );
        _scrollToEnd();
        return;
      }
      if (label == '朋友圈') {
        final moment = await showMomentPicker(context, widget.controller);
        if (!mounted || moment == null) return;
        setState(() => showAttachments = false);
        unawaited(
          widget.controller.sendMomentShare(widget.conversation.id, moment),
        );
        _scrollToEnd();
        return;
      }
      MediaUpload? upload;
      if (label == '相册' || label == '拍摄') {
        final source = label == '拍摄' ? ImageSource.camera : ImageSource.gallery;
        final picked = await ImagePicker().pickImage(
          source: source,
          // Android's picker may transcode a gallery GIF to JPEG while
          // retaining the .gif name when resize/quality options are present.
          // Gallery bytes must stay original until we inspect their MIME.
          imageQuality: source == ImageSource.camera ? 88 : null,
          maxWidth: source == ImageSource.camera ? 2400 : null,
        );
        if (picked == null) return;
        final original = await picked.readAsBytes();
        if (!mounted) return;
        final pickedMime = _mimeFor(picked.name);
        if (pickedMime == 'image/gif') {
          // Editing/encoding an animated GIF as JPEG silently destroys its
          // animation. Preserve the validated original and let MessageMapper
          // select WuKongIM's pinned built-in content type 3.
          _validateMediaSize(original.length);
          final localPath = await persistImageBytes(
            original,
            mime: pickedMime,
            extension: '.gif',
          );
          upload = MediaUpload(
            bytes: original,
            fileName: picked.name,
            mimeType: pickedMime,
            kind: MessageContentKind.image,
            localPath: localPath,
          );
        } else {
          final bytes = await editImageBeforeSending(context, original);
          if (!mounted || bytes == null) return;
          _validateMediaSize(bytes.length);
          final localPath = await persistEditedImage(bytes);
          upload = MediaUpload(
            bytes: bytes,
            fileName: _editedImageName(picked.name),
            mimeType: 'image/jpeg',
            kind: MessageContentKind.image,
            localPath: localPath,
          );
        }
      } else if (label == '视频') {
        final source = await chooseVideoSource(context);
        if (!mounted || source == null) return;
        final picked = await ImagePicker().pickVideo(
          source: source,
          maxDuration: const Duration(minutes: 5),
        );
        if (!mounted || picked == null) return;
        final preview = await showVideoSendPreview(
          context,
          source: picked.path,
          title: picked.name,
        );
        if (!mounted || preview == null) return;
        upload = await prepareVideoUploadWithDialog(
          context,
          file: picked,
          maxBytes: AppConfig.mediaMaxBytes,
          previewDurationSeconds: preview.durationSeconds,
        );
      } else if (label == '文件') {
        final result = await FilePicker.platform.pickFiles(
          allowMultiple: false,
          withData: true,
        );
        final file = result?.files.singleOrNull;
        if (file == null) return;
        final bytes =
            file.bytes ??
            (file.path == null ? null : await File(file.path!).readAsBytes());
        if (bytes == null) throw const FileSystemException('无法读取所选文件');
        _validateMediaSize(bytes.length);
        upload = MediaUpload(
          bytes: bytes,
          fileName: file.name,
          mimeType: _mimeFor(file.name),
          kind: MessageContentKind.file,
          localPath: file.path,
        );
      } else if (label == '名片') {
        await _pickContact();
        return;
      } else if (label == '位置') {
        await _pickLocation();
        return;
      }
      if (upload == null) return;
      final reply = replyingTo;
      setState(() {
        replyingTo = null;
        showAttachments = false;
      });
      unawaited(
        widget.controller.sendMedia(
          widget.conversation.id,
          upload,
          replyTo: reply,
        ),
      );
      _scrollToEnd();
    } on PlatformException catch (error) {
      _showError(error.message ?? '无法访问照片或文件，请检查系统权限');
    } catch (error) {
      _showError(error.toString().replaceFirst('FormatException: ', ''));
    }
  }

  Future<void> _showLiveInteraction() async {
    const options = <(String, String, String)>[
      ('live.like', '❤️', '点赞了直播'),
      ('live.applause', '👏', '为直播鼓掌'),
      ('live.follow', '⭐', '关注了直播'),
    ];
    final selected = await showCupertinoModalPopup<(String, String, String)>(
      context: context,
      builder: (context) => CupertinoActionSheet(
        title: const Text('直播互动'),
        message: const Text('互动将实时发送给当前直播频道的成员。'),
        actions: [
          for (final option in options)
            CupertinoActionSheetAction(
              key: Key('live-event-${option.$1}'),
              onPressed: () => Navigator.pop(context, option),
              child: Text('${option.$2}  ${option.$3}'),
            ),
        ],
        cancelButton: CupertinoActionSheetAction(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
      ),
    );
    if (!mounted || selected == null) return;
    setState(() => showAttachments = false);
    try {
      await widget.controller.sendLiveEvent(
        widget.conversation.id,
        event: selected.$1,
        label: '${selected.$2} ${selected.$3}',
      );
      _scrollToEnd();
    } catch (error) {
      _showError(error.toString().replaceFirst('FormatException: ', ''));
    }
  }

  Future<void> _pickContact() async {
    final contact = await showModalBottomSheet<AppUser>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      showDragHandle: true,
      builder: (context) => _ContactPickerSheet(controller: widget.controller),
    );
    if (!mounted || contact == null) return;
    final confirmed = await showCupertinoDialog<bool>(
      context: context,
      builder: (context) => CupertinoAlertDialog(
        title: const Text('发送这张名片？'),
        content: Text(
          '\n${contact.name}\n${publicUserHandleLabel(contact.handle)}\n\n仅发送昵称、呱呱号和头像，不会发送手机号、备注或标签。',
        ),
        actions: [
          CupertinoDialogAction(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          CupertinoDialogAction(
            isDefaultAction: true,
            onPressed: () => Navigator.pop(context, true),
            child: const Text('发送'),
          ),
        ],
      ),
    );
    if (!mounted || confirmed != true) return;
    final reply = replyingTo;
    setState(() {
      replyingTo = null;
      showAttachments = false;
    });
    unawaited(
      widget.controller.sendContact(
        widget.conversation.id,
        contact,
        replyTo: reply,
      ),
    );
    _scrollToEnd();
  }

  Future<void> _pickLocation() async {
    if (!await Geolocator.isLocationServiceEnabled()) {
      _showError('定位服务未开启，请先在系统设置中开启定位服务');
      return;
    }
    var permission = await Geolocator.checkPermission();
    if (permission == LocationPermission.denied) {
      permission = await Geolocator.requestPermission();
    }
    if (permission == LocationPermission.denied) {
      _showError('没有定位权限，无法发送当前位置');
      return;
    }
    if (permission == LocationPermission.deniedForever) {
      if (!mounted) return;
      final openSettings = await showCupertinoDialog<bool>(
        context: context,
        builder: (context) => CupertinoAlertDialog(
          title: const Text('需要定位权限'),
          content: const Text('\n请在系统设置中允许“青蛙呱呱”使用你的位置。'),
          actions: [
            CupertinoDialogAction(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            CupertinoDialogAction(
              isDefaultAction: true,
              onPressed: () => Navigator.pop(context, true),
              child: const Text('前往设置'),
            ),
          ],
        ),
      );
      if (openSettings == true) await Geolocator.openAppSettings();
      return;
    }

    final position = await Geolocator.getCurrentPosition(
      locationSettings: const LocationSettings(
        accuracy: LocationAccuracy.high,
        timeLimit: Duration(seconds: 15),
      ),
    );
    var name = '我的位置';
    String? address;
    try {
      final places = await placemarkFromCoordinates(
        position.latitude,
        position.longitude,
      );
      final place = places.firstOrNull;
      if (place != null) {
        final candidate =
            [place.name, place.street, place.subLocality, place.locality]
                .whereType<String>()
                .map((value) => value.trim())
                .firstWhere((value) => value.isNotEmpty, orElse: () => '我的位置');
        name = candidate;
        address =
            [
                  place.administrativeArea,
                  place.locality,
                  place.subLocality,
                  place.street,
                ]
                .whereType<String>()
                .map((value) => value.trim())
                .where((value) => value.isNotEmpty)
                .toSet()
                .join(' ');
      }
    } catch (_) {
      // Reverse geocoding is optional; the verified coordinates remain valid.
    }
    if (!mounted) return;
    final confirmed = await showCupertinoDialog<bool>(
      context: context,
      builder: (context) => CupertinoAlertDialog(
        title: const Text('发送当前位置？'),
        content: Text(
          '\n$name${address?.isEmpty ?? true ? '' : '\n$address'}\n\n对方将看到此刻的精确位置，不会持续共享或后台追踪。',
        ),
        actions: [
          CupertinoDialogAction(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          CupertinoDialogAction(
            isDefaultAction: true,
            onPressed: () => Navigator.pop(context, true),
            child: const Text('发送'),
          ),
        ],
      ),
    );
    if (!mounted || confirmed != true) return;
    final reply = replyingTo;
    setState(() {
      replyingTo = null;
      showAttachments = false;
    });
    unawaited(
      widget.controller.sendLocation(
        widget.conversation.id,
        latitude: position.latitude,
        longitude: position.longitude,
        name: name,
        address: address,
        replyTo: reply,
      ),
    );
    _scrollToEnd();
  }

  Future<void> _sendVoice(MediaUpload upload) async {
    if (_effectiveSendRestriction case final restriction?) {
      throw FormatException(restriction);
    }
    final conversationId = widget.conversation.id;
    final reply = replyingTo;
    final queued = Completer<void>();
    unawaited(() async {
      try {
        await widget.controller.sendMedia(
          conversationId,
          upload,
          replyTo: reply,
          onQueued: (message) {
            if (mounted && widget.conversation.id == conversationId) {
              setState(() {
                _pendingVoiceEntrances.add(message.clientMessageId);
                replyingTo = null;
                showEmoji = false;
                showAttachments = false;
              });
              _scrollToEnd();
            }
            queued.complete();
          },
        );
      } catch (error, stack) {
        if (!queued.isCompleted) {
          queued.completeError(error, stack);
        } else if (mounted) {
          _showError('语音发送失败，请稍后重试');
        }
      }
    }());
    // The composer releases its draft after durable enqueue, not after ACK.
    await queued.future;
  }

  void _validateMediaSize(int bytes) {
    if (bytes <= AppConfig.mediaMaxBytes) return;
    final maxMB = AppConfig.mediaMaxBytes ~/ (1024 * 1024);
    throw FormatException('文件不能超过 $maxMB MB');
  }

  String _mimeFor(
    String name, {
    bool imageFallback = false,
    bool videoFallback = false,
  }) {
    final extension = name.split('.').last.toLowerCase();
    return switch (extension) {
      'jpg' || 'jpeg' => 'image/jpeg',
      'png' => 'image/png',
      'gif' => 'image/gif',
      'webp' => 'image/webp',
      'heic' => 'image/heic',
      'mp4' => 'video/mp4',
      'mov' => 'video/quicktime',
      'm4v' => 'video/x-m4v',
      'pdf' => 'application/pdf',
      'txt' => 'text/plain',
      'csv' => 'text/csv',
      'rtf' => 'application/rtf',
      'zip' => 'application/zip',
      'doc' => 'application/msword',
      'docx' =>
        'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      'xls' => 'application/vnd.ms-excel',
      'xlsx' =>
        'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      'ppt' => 'application/vnd.ms-powerpoint',
      'pptx' =>
        'application/vnd.openxmlformats-officedocument.presentationml.presentation',
      _ =>
        imageFallback
            ? 'image/jpeg'
            : videoFallback
            ? 'video/mp4'
            : 'application/octet-stream',
    };
  }

  String _editedImageName(String original) {
    final trimmed = original.trim();
    if (trimmed.isEmpty) return 'image-edited.jpg';
    final dot = trimmed.lastIndexOf('.');
    final base = dot > 0 ? trimmed.substring(0, dot) : trimmed;
    return '$base-edited.jpg';
  }

  void _showError(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message.isEmpty ? '操作失败，请重试' : message)),
    );
  }

  void _scrollToEnd() {
    if (!mounted) return;
    _initialScrollTimer?.cancel();
    _scrollEpoch += 1;
    _userScrolling = false;
    _followingLatest = true;
    final epoch = _scrollEpoch;
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      if (!mounted || epoch != _scrollEpoch || !scrollController.hasClients) {
        return;
      }
      final duration = nexaMotionDuration(context);
      if (duration == Duration.zero) {
        scrollController.jumpTo(scrollController.position.maxScrollExtent);
      } else {
        await scrollController.animateTo(
          scrollController.position.maxScrollExtent,
          duration: duration,
          curve: Curves.easeOutCubic,
        );
      }
      if (mounted && epoch == _scrollEpoch) _pinInitialMessagesToEnd();
    });
    WidgetsBinding.instance.ensureVisualUpdate();
  }

  void _scrollToInitialMessage() {
    final epoch = _scrollEpoch;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || epoch != _scrollEpoch) return;
      final target = initialMessageKey.currentContext;
      if (target == null) {
        if (scrollController.hasClients) {
          scrollController.jumpTo(scrollController.position.maxScrollExtent);
        }
        _messageScrollReady = true;
        return;
      }
      Scrollable.ensureVisible(
        target,
        duration: nexaMotionDuration(context),
        alignment: .42,
      );
      _messageScrollReady = true;
    });
    WidgetsBinding.instance.ensureVisualUpdate();
  }

  Future<void> _showMessageActions(ChatMessage message, Offset anchor) async {
    final canRecall = widget.controller.canRecallMessage(message);
    unawaited(HapticFeedback.mediumImpact());
    final action = await showGeneralDialog<_MessageMenuAction>(
      context: context,
      barrierDismissible: true,
      barrierLabel: '关闭消息操作',
      barrierColor: Colors.black.withValues(alpha: .42),
      transitionDuration: nexaMotionDuration(context),
      pageBuilder: (dialogContext, _, _) => _MessageContextMenu(
        controller: widget.controller,
        key: const Key('message-context-menu'),
        message: message,
        anchor: anchor,
        canRecall: canRecall,
        canEdit: widget.controller.isMessageEditable(message),
        canPin:
            widget.conversation.kind == ConversationKind.group &&
            !widget.conversation.isBusinessChannel &&
            !message.id.startsWith('local-') &&
            message.status != MessageStatus.recalled,
        onSelected: (value) => Navigator.pop(dialogContext, value),
      ),
      transitionBuilder: (dialogContext, animation, _, child) {
        final size = MediaQuery.sizeOf(dialogContext);
        final alignment = Alignment(
          (anchor.dx / size.width * 2 - 1).clamp(-1.0, 1.0),
          (anchor.dy / size.height * 2 - 1).clamp(-1.0, 1.0),
        );
        return FadeTransition(
          opacity: CurvedAnimation(parent: animation, curve: Curves.easeOut),
          child: ScaleTransition(
            alignment: alignment,
            scale: Tween(begin: .96, end: 1.0).animate(
              CurvedAnimation(parent: animation, curve: Curves.easeOutCubic),
            ),
            child: child,
          ),
        );
      },
    );
    if (action == null || !mounted) return;
    unawaited(HapticFeedback.selectionClick());
    switch (action) {
      case _MessageMenuAction.reply:
        setState(() => replyingTo = message);
      case _MessageMenuAction.react:
        await _showReactionPicker(message);
      case _MessageMenuAction.edit:
        await _editMessage(message);
      case _MessageMenuAction.editHistory:
        await _showMessageEditHistory(message);
      case _MessageMenuAction.copy:
        await Clipboard.setData(ClipboardData(text: message.text));
        if (mounted) {
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(const SnackBar(content: Text('已复制')));
        }
      case _MessageMenuAction.selectText:
        await _showTextSelection(message);
      case _MessageMenuAction.forward:
        _showForwardTargets([message], mode: 'separate');
      case _MessageMenuAction.favorite:
        final saved = await widget.controller.favoriteMessage(message);
        if (saved && mounted) {
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(const SnackBar(content: Text('已收藏并同步')));
        } else if (mounted) {
          _showError(widget.controller.error ?? '收藏失败，请稍后重试');
        }
      case _MessageMenuAction.select:
        setState(() {
          selecting = true;
          selectedMessageIds.add(message.clientMessageId);
        });
      case _MessageMenuAction.recall:
        final success = await widget.controller.recallMessage(message);
        if (!success && mounted) {
          _showError(widget.controller.error ?? '撤回失败，请稍后重试');
        }
      case _MessageMenuAction.pin:
        final success = await widget.controller.toggleMessagePinned(message);
        if (!success && mounted) {
          _showError(widget.controller.error ?? '置顶状态更新失败');
        }
      case _MessageMenuAction.delete:
        final success = await widget.controller.deleteMessage(message);
        if (!success && mounted) {
          _showError(widget.controller.error ?? '消息删除失败，本机记录未修改');
        }
      case _MessageMenuAction.report:
        await Navigator.of(context).push(
          MaterialPageRoute(
            builder: (_) => ReportScreen(
              controller: widget.controller,
              target: widget.controller.displayNameForId(
                message.senderId,
                fallback: message.senderName,
              ),
              targetId: message.id,
              targetType: 'message',
            ),
          ),
        );
    }
  }

  Future<void> _showTextSelection(
    ChatMessage message,
  ) => showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    useSafeArea: true,
    showDragHandle: true,
    builder: (sheetContext) => FractionallySizedBox(
      heightFactor: .68,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 0, 20, 16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    '选择文字',
                    style: Theme.of(sheetContext).textTheme.titleLarge
                        ?.copyWith(fontWeight: FontWeight.w700),
                  ),
                ),
                TextButton.icon(
                  key: const Key('copy-all-message-text'),
                  onPressed: () async {
                    await Clipboard.setData(ClipboardData(text: message.text));
                    if (!sheetContext.mounted) return;
                    Navigator.pop(sheetContext);
                    if (!mounted) return;
                    ScaffoldMessenger.of(
                      context,
                    ).showSnackBar(const SnackBar(content: Text('已复制全部文字')));
                  },
                  icon: const Icon(CupertinoIcons.doc_on_doc, size: 18),
                  label: const Text('复制全部'),
                ),
              ],
            ),
            const SizedBox(height: 10),
            Text(
              '长按文字后拖动选区，可复制其中一部分。',
              style: Theme.of(sheetContext).textTheme.bodySmall,
            ),
            const SizedBox(height: 14),
            Expanded(
              child: DecoratedBox(
                decoration: BoxDecoration(
                  color: Theme.of(sheetContext).colorScheme.surfaceContainerLow,
                  borderRadius: BorderRadius.circular(16),
                  border: Border.all(
                    color: Theme.of(sheetContext).colorScheme.outline,
                  ),
                ),
                child: SingleChildScrollView(
                  padding: const EdgeInsets.all(16),
                  child: SelectableText(
                    message.text,
                    key: const Key('selectable-message-text'),
                    style: Theme.of(
                      sheetContext,
                    ).textTheme.bodyLarge?.copyWith(height: 1.55),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    ),
  );

  Future<void> _showMessageEditHistory(ChatMessage message) =>
      showModalBottomSheet<void>(
        context: context,
        isScrollControlled: true,
        showDragHandle: false,
        builder: (sheetContext) => FractionallySizedBox(
          heightFactor: .72,
          child: _MessageEditHistorySheet(
            controller: widget.controller,
            message: message,
          ),
        ),
      );

  Future<void> _openChatInfo() async {
    unawaited(HapticFeedback.lightImpact());
    if (widget.onToggleDesktopDetails != null) {
      widget.onToggleDesktopDetails!();
      return;
    }
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ChatInfoScreen(
          controller: widget.controller,
          conversation: widget.conversation,
          onSearch: _showMessageSearch,
          onClearLocal: _confirmClearLocal,
          onBlock: _confirmBlock,
          onScheduledMessages: _showScheduledMessages,
        ),
      ),
    );
  }

  Future<void> _showMessageSearch() async {
    final selected = await showModalBottomSheet<ChatMessage>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (sheetContext) => _MessageSearchSheet(
        controller: widget.controller,
        conversationId: widget.conversation.id,
        onSelected: (message) => Navigator.pop(sheetContext, message),
      ),
    );
    if (!mounted || selected == null) return;
    // On phones the search sheet is opened above ChatInfoScreen. Close that
    // route as well so the message list is actually visible; desktop details
    // are embedded in this route and therefore remain open.
    if (ModalRoute.of(context)?.isCurrent == false) {
      Navigator.of(context).pop();
      // The message list lives on the route underneath ChatInfoScreen. Wait
      // until the Material route has finished uncovering it; attempting
      // ensureVisible while that route is still offstage can leave Android a
      // few rows away from the requested result on long, uneven chat lists.
      await Future<void>.delayed(const Duration(milliseconds: 350));
      await WidgetsBinding.instance.endOfFrame;
      if (!mounted) return;
    }
    await _scrollToMessage(selected.id, searchResult: selected);
  }

  Future<void> _chooseForwardMode(List<ChatMessage> messages) async {
    final mode = messages.length == 1
        ? 'separate'
        : await showCupertinoModalPopup<String>(
            context: context,
            builder: (context) => CupertinoActionSheet(
              title: Text('转发 ${messages.length} 条消息'),
              message: const Text('逐条转发保留原消息类型；合并转发生成一条聊天记录。'),
              actions: [
                CupertinoActionSheetAction(
                  onPressed: () => Navigator.pop(context, 'separate'),
                  child: const Text('逐条转发'),
                ),
                CupertinoActionSheetAction(
                  onPressed: () => Navigator.pop(context, 'merged'),
                  child: const Text('合并转发'),
                ),
              ],
              cancelButton: CupertinoActionSheetAction(
                onPressed: () => Navigator.pop(context),
                child: const Text('取消'),
              ),
            ),
          );
    if (!mounted || mode == null) return;
    await _showForwardTargets(messages, mode: mode);
  }

  Future<void> _showForwardTargets(
    List<ChatMessage> messages, {
    required String mode,
  }) async {
    final result = await showForwardConversations(
      context,
      controller: widget.controller,
      messages: messages,
      mode: mode,
    );
    if (!mounted || result == null) return;
    if (result.allSucceeded) {
      setState(() {
        selecting = false;
        selectedMessageIds.clear();
      });
    }
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
          result.allSucceeded
              ? '已${mode == 'merged' ? '合并' : ''}转发到 ${result.succeeded} 个会话'
              : '转发完成：成功 ${result.succeeded}，失败 ${result.failed}，未发送 ${result.notSent}',
        ),
      ),
    );
  }

  Future<void> _confirmClearLocal() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('清空本地聊天记录？'),
        content: const Text('只会清除当前设备缓存。下次同步时，云端保留的消息可能再次出现。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: LinliColors.systemRed,
              foregroundColor: Colors.white,
            ),
            onPressed: () => Navigator.pop(dialogContext, true),
            child: const Text('仅清空本机'),
          ),
        ],
      ),
    );
    if (confirmed == true) {
      await widget.controller.clearLocalMessages(widget.conversation.id);
    }
  }

  Future<void> _confirmBlock() async {
    final user = peer;
    if (user == null) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text('将 ${widget.controller.displayNameFor(user)} 加入黑名单？'),
        content: const Text('对方将无法继续向你发起新消息。你可以稍后在隐私设置中解除。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: LinliColors.systemRed,
              foregroundColor: Colors.white,
            ),
            onPressed: () => Navigator.pop(dialogContext, true),
            child: const Text('加入黑名单'),
          ),
        ],
      ),
    );
    if (confirmed == true) await widget.controller.blockUser(user, true);
  }

  bool _sameDay(DateTime a, DateTime b) =>
      a.year == b.year && a.month == b.month && a.day == b.day;
}

enum _MessageMenuAction {
  reply,
  react,
  edit,
  editHistory,
  copy,
  selectText,
  forward,
  favorite,
  select,
  recall,
  pin,
  delete,
  report,
}

class _MessageContextMenu extends StatelessWidget {
  const _MessageContextMenu({
    super.key,
    required this.controller,
    required this.message,
    required this.anchor,
    required this.canRecall,
    required this.canEdit,
    required this.canPin,
    required this.onSelected,
  });

  final AppController controller;
  final ChatMessage message;
  final Offset anchor;
  final bool canRecall;
  final bool canEdit;
  final bool canPin;
  final ValueChanged<_MessageMenuAction> onSelected;

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: controller,
    builder: (context, _) => _buildContent(context),
  );

  Widget _buildContent(BuildContext context) {
    final media = MediaQuery.of(context);
    final textScale = media.textScaler.scale(1);
    final canForward =
        !message.id.startsWith('local-') &&
        message.status != MessageStatus.recalled &&
        message.kind != MessageContentKind.system &&
        message.kind != MessageContentKind.screenshotNotice;
    final canCopy =
        message.kind == MessageContentKind.text ||
        message.kind == MessageContentKind.reply;
    final canViewEditHistory =
        message.editedAt != null && !message.id.startsWith('local-');
    final primary = <_ContextActionSpec>[
      const _ContextActionSpec(
        action: _MessageMenuAction.reply,
        icon: CupertinoIcons.reply,
        label: '回复',
      ),
      const _ContextActionSpec(
        action: _MessageMenuAction.react,
        icon: CupertinoIcons.smiley,
        label: '回应',
      ),
      if (canEdit)
        const _ContextActionSpec(
          action: _MessageMenuAction.edit,
          icon: CupertinoIcons.pencil,
          label: '编辑',
        ),
      if (canViewEditHistory)
        const _ContextActionSpec(
          action: _MessageMenuAction.editHistory,
          icon: CupertinoIcons.time,
          label: '编辑记录',
        ),
      if (canCopy)
        const _ContextActionSpec(
          action: _MessageMenuAction.copy,
          icon: CupertinoIcons.doc_on_doc,
          label: '复制',
        ),
      if (canCopy)
        const _ContextActionSpec(
          action: _MessageMenuAction.selectText,
          icon: CupertinoIcons.text_cursor,
          label: '选择文字',
        ),
      if (canForward)
        const _ContextActionSpec(
          action: _MessageMenuAction.forward,
          icon: CupertinoIcons.arrowshape_turn_up_right,
          label: '转发',
        ),
      const _ContextActionSpec(
        action: _MessageMenuAction.favorite,
        icon: CupertinoIcons.bookmark,
        label: '收藏',
      ),
      const _ContextActionSpec(
        action: _MessageMenuAction.select,
        icon: CupertinoIcons.checkmark_circle,
        label: '多选',
      ),
    ];
    final secondary = <_ContextActionSpec>[
      if (canPin)
        _ContextActionSpec(
          action: _MessageMenuAction.pin,
          icon: message.isPinned
              ? CupertinoIcons.pin_slash
              : CupertinoIcons.pin,
          label: message.isPinned ? '取消置顶' : '置顶',
        ),
      if (canRecall)
        const _ContextActionSpec(
          action: _MessageMenuAction.recall,
          icon: CupertinoIcons.arrow_uturn_left,
          label: '撤回',
        ),
      const _ContextActionSpec(
        action: _MessageMenuAction.delete,
        icon: CupertinoIcons.trash,
        label: '删除本机记录',
        destructive: true,
      ),
      if (!message.isMine)
        const _ContextActionSpec(
          action: _MessageMenuAction.report,
          icon: CupertinoIcons.exclamationmark_triangle,
          label: '举报',
          destructive: true,
        ),
    ];
    return CustomSingleChildLayout(
      delegate: _AnchoredMenuLayoutDelegate(
        anchor: anchor,
        safePadding: media.padding,
      ),
      child: Semantics(
        scopesRoute: true,
        namesRoute: true,
        explicitChildNodes: true,
        label: '消息操作',
        child: Material(
          color: Theme.of(context).colorScheme.surfaceContainer,
          elevation: 10,
          shadowColor: LinliColors.navy.withValues(alpha: .22),
          borderRadius: BorderRadius.circular(16),
          clipBehavior: Clip.antiAlias,
          child: ConstrainedBox(
            constraints: BoxConstraints(maxHeight: media.size.height * .72),
            child: SingleChildScrollView(
              padding: const EdgeInsets.fromLTRB(12, 12, 12, 10),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  _MessageContextPreview(
                    message: message,
                    senderName: controller.displayNameForId(
                      message.senderId,
                      fallback: message.senderName,
                    ),
                  ),
                  const SizedBox(height: 8),
                  LayoutBuilder(
                    builder: (context, constraints) {
                      final columns = textScale >= 1.6 ? 3 : 4;
                      final width = constraints.maxWidth / columns;
                      return Wrap(
                        children: [
                          for (final action in primary)
                            SizedBox(
                              width: width,
                              child: _ContextActionButton(
                                spec: action,
                                onTap: () => onSelected(action.action),
                              ),
                            ),
                        ],
                      );
                    },
                  ),
                  Divider(
                    height: 17,
                    color: Theme.of(context).colorScheme.outline,
                  ),
                  Wrap(
                    spacing: 4,
                    runSpacing: 4,
                    children: [
                      for (final action in secondary)
                        _ContextSecondaryButton(
                          spec: action,
                          onTap: () => onSelected(action.action),
                        ),
                    ],
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _MessageEditHistorySheet extends StatefulWidget {
  const _MessageEditHistorySheet({
    required this.controller,
    required this.message,
  });

  final AppController controller;
  final ChatMessage message;

  @override
  State<_MessageEditHistorySheet> createState() =>
      _MessageEditHistorySheetState();
}

class _MessageEditHistorySheetState extends State<_MessageEditHistorySheet> {
  late Future<List<MessageEditRevision>?> revisions;

  @override
  void initState() {
    super.initState();
    revisions = widget.controller.loadMessageEditHistory(widget.message);
  }

  void _retry() {
    setState(() {
      revisions = widget.controller.loadMessageEditHistory(widget.message);
    });
  }

  String _time(DateTime value) {
    final local = value.toLocal();
    String two(int number) => number.toString().padLeft(2, '0');
    return '${local.year}-${two(local.month)}-${two(local.day)} '
        '${two(local.hour)}:${two(local.minute)}';
  }

  @override
  Widget build(BuildContext context) => SafeArea(
    child: Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(20, 14, 12, 10),
          child: Row(
            children: [
              Expanded(
                child: Text(
                  '编辑记录',
                  style: Theme.of(
                    context,
                  ).textTheme.titleLarge?.copyWith(fontWeight: FontWeight.w700),
                ),
              ),
              IconButton(
                tooltip: '关闭',
                onPressed: () => Navigator.pop(context),
                icon: const Icon(CupertinoIcons.xmark_circle_fill),
              ),
            ],
          ),
        ),
        const Divider(height: 1),
        Expanded(
          child: FutureBuilder<List<MessageEditRevision>?>(
            future: revisions,
            builder: (context, snapshot) {
              if (snapshot.connectionState != ConnectionState.done) {
                return const Center(
                  child: CircularProgressIndicator(
                    key: Key('message-edit-history-loading'),
                  ),
                );
              }
              final items = snapshot.data;
              if (items == null) {
                return Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Text('编辑记录加载失败'),
                      const SizedBox(height: 10),
                      OutlinedButton(
                        key: const Key('retry-message-edit-history'),
                        onPressed: _retry,
                        child: const Text('重新加载'),
                      ),
                    ],
                  ),
                );
              }
              if (items.isEmpty) {
                return const Center(child: Text('暂无编辑记录'));
              }
              return ListView.separated(
                key: const Key('message-edit-history-list'),
                padding: const EdgeInsets.fromLTRB(20, 14, 20, 24),
                itemCount: items.length,
                separatorBuilder: (_, _) => const SizedBox(height: 12),
                itemBuilder: (context, index) {
                  final revision = items[index];
                  final title = revision.isOriginal
                      ? '原始内容'
                      : '第 ${revision.version} 次编辑';
                  return Semantics(
                    label:
                        '$title，${revision.text}，${_time(revision.editedAt)}',
                    child: Container(
                      key: Key('message-edit-revision-${revision.version}'),
                      padding: const EdgeInsets.all(14),
                      decoration: BoxDecoration(
                        color: Theme.of(
                          context,
                        ).colorScheme.surfaceContainerLow,
                        borderRadius: BorderRadius.circular(14),
                      ),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Row(
                            children: [
                              Expanded(
                                child: Text(
                                  title,
                                  style: const TextStyle(
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                              ),
                              Text(
                                _time(revision.editedAt),
                                style: Theme.of(context).textTheme.bodySmall,
                              ),
                            ],
                          ),
                          const SizedBox(height: 8),
                          SelectableText(
                            revision.text.isEmpty ? '[非文本内容]' : revision.text,
                          ),
                        ],
                      ),
                    ),
                  );
                },
              );
            },
          ),
        ),
      ],
    ),
  );
}

class _MessageContextPreview extends StatelessWidget {
  const _MessageContextPreview({
    required this.message,
    required this.senderName,
  });
  final String senderName;
  final ChatMessage message;

  @override
  Widget build(BuildContext context) {
    final label = switch (message.kind) {
      MessageContentKind.image => '图片消息',
      MessageContentKind.voice => '${message.durationSeconds ?? 1} 秒语音',
      MessageContentKind.video => message.fileName ?? '视频消息',
      MessageContentKind.file => message.fileName ?? '文件消息',
      MessageContentKind.sticker => message.fileName ?? '表情消息',
      MessageContentKind.momentShare => message.text,
      _ => message.text,
    };
    final localSentAt = message.sentAt.toLocal();
    final time =
        '${localSentAt.hour.toString().padLeft(2, '0')}:${localSentAt.minute.toString().padLeft(2, '0')}';
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerHigh,
        borderRadius: BorderRadius.circular(12),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  message.isMine ? '我' : senderName,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: Theme.of(context).textTheme.labelMedium?.copyWith(
                    color: Theme.of(context).colorScheme.onSurface,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              Text(time, style: Theme.of(context).textTheme.labelSmall),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            label,
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: Theme.of(context).textTheme.bodyMedium?.copyWith(
              color: Theme.of(context).colorScheme.onSurface,
            ),
          ),
        ],
      ),
    );
  }
}

class _ContextActionSpec {
  const _ContextActionSpec({
    required this.action,
    required this.icon,
    required this.label,
    this.destructive = false,
  });

  final _MessageMenuAction action;
  final IconData icon;
  final String label;
  final bool destructive;
}

class _ContextActionButton extends StatelessWidget {
  const _ContextActionButton({required this.spec, required this.onTap});
  final _ContextActionSpec spec;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    label: spec.label,
    child: InkWell(
      borderRadius: BorderRadius.circular(10),
      onTap: onTap,
      child: ConstrainedBox(
        constraints: const BoxConstraints(minHeight: 64),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 2, vertical: 7),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                spec.icon,
                size: 21,
                color: Theme.of(context).brightness == Brightness.dark
                    ? LinliColors.brandGreen
                    : LinliColors.navy,
              ),
              const SizedBox(height: 5),
              Text(
                spec.label,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                textAlign: TextAlign.center,
                style: Theme.of(context).textTheme.labelSmall?.copyWith(
                  color: Theme.of(context).colorScheme.onSurface,
                ),
              ),
            ],
          ),
        ),
      ),
    ),
  );
}

class _ContextSecondaryButton extends StatelessWidget {
  const _ContextSecondaryButton({required this.spec, required this.onTap});
  final _ContextActionSpec spec;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    label: spec.label,
    child: TextButton.icon(
      onPressed: onTap,
      icon: Icon(spec.icon, size: 18),
      label: Text(spec.label),
      style: TextButton.styleFrom(
        minimumSize: const Size(44, 44),
        foregroundColor: spec.destructive
            ? LinliColors.systemRed
            : Theme.of(context).colorScheme.onSurface,
        padding: const EdgeInsets.symmetric(horizontal: 10),
        textStyle: Theme.of(context).textTheme.labelMedium,
      ),
    ),
  );
}

class _AnchoredMenuLayoutDelegate extends SingleChildLayoutDelegate {
  const _AnchoredMenuLayoutDelegate({
    required this.anchor,
    required this.safePadding,
  });

  final Offset anchor;
  final EdgeInsets safePadding;

  @override
  BoxConstraints getConstraintsForChild(BoxConstraints constraints) {
    final width = (constraints.maxWidth - 24).clamp(240.0, 360.0);
    return BoxConstraints(
      minWidth: width,
      maxWidth: width,
      maxHeight:
          constraints.maxHeight - safePadding.top - safePadding.bottom - 16,
    );
  }

  @override
  Offset getPositionForChild(Size size, Size childSize) {
    final left = (anchor.dx - childSize.width / 2).clamp(
      12.0,
      size.width - childSize.width - 12,
    );
    final minimumTop = safePadding.top + 8;
    final maximumTop = size.height - safePadding.bottom - childSize.height - 8;
    final below = anchor.dy + 10;
    final above = anchor.dy - childSize.height - 10;
    final top = below <= maximumTop
        ? below
        : above.clamp(minimumTop, maximumTop);
    return Offset(left, top);
  }

  @override
  bool shouldRelayout(_AnchoredMenuLayoutDelegate oldDelegate) =>
      anchor != oldDelegate.anchor || safePadding != oldDelegate.safePadding;
}

class ChatInfoScreen extends StatelessWidget {
  const ChatInfoScreen({
    super.key,
    required this.controller,
    required this.conversation,
    required this.onSearch,
    required this.onClearLocal,
    required this.onBlock,
    required this.onScheduledMessages,
  });

  final AppController controller;
  final Conversation conversation;
  final VoidCallback onSearch;
  final Future<void> Function() onClearLocal;
  final Future<void> Function() onBlock;
  final VoidCallback onScheduledMessages;

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: controller,
    builder: (context, _) {
      final current = controller.conversations
          .where((item) => item.id == conversation.id)
          .firstOrNull;
      return _buildContent(context, current ?? conversation);
    },
  );

  Widget _buildContent(BuildContext context, Conversation conversation) {
    final multiUser = conversation.kind == ConversationKind.group;
    final business = conversation.isBusinessChannel;
    final group = multiUser && !business;
    final directPeer = conversation.directPeerFor(controller.currentUser?.id);
    return Scaffold(
      appBar: const GlassAppBar(title: Text('聊天信息')),
      body: ListView(
        key: const Key('chat-info-list'),
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 36),
        children: [
          SectionHeader(
            group
                ? '群成员'
                : business
                ? '频道资料'
                : '联系人',
          ),
          SectionCard(
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 16, 12, 14),
                child: multiUser
                    ? _ChatMemberMatrix(
                        controller: controller,
                        members: conversation.members,
                        fallbackName: conversation.title,
                        fallbackAvatar: conversation.avatarUrl,
                      )
                    : _DirectContactSummary(
                        controller: controller,
                        user: directPeer,
                        fallbackName: conversation.title,
                        fallbackAvatar: conversation.avatarUrl,
                        onTap: directPeer == null
                            ? null
                            : () => Navigator.of(context).push(
                                MaterialPageRoute(
                                  builder: (_) => FriendProfileScreen(
                                    controller: controller,
                                    user: directPeer,
                                    requestSource: 'conversation',
                                    requestSourceId: conversation.id,
                                  ),
                                ),
                              ),
                      ),
              ),
            ],
          ),
          const SectionHeader('聊天内容'),
          SectionCard(
            children: [
              SettingTile(
                icon: CupertinoIcons.search,
                title: '查找聊天内容',
                subtitle: '搜索当前设备已同步的消息',
                onTap: onSearch,
              ),
              SettingTile(
                key: const Key('scheduled-messages-button'),
                icon: CupertinoIcons.clock,
                title: '定时消息',
                subtitle: '查看、取消或重试服务端定时任务',
                onTap: onScheduledMessages,
              ),
              if (group)
                SettingTile(
                  icon: CupertinoIcons.person_2,
                  title: '群聊资料与管理',
                  subtitle: '${conversation.memberCount} 位成员',
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => GroupDetailsScreen(
                        controller: controller,
                        conversation: conversation,
                      ),
                    ),
                  ),
                ),
            ],
          ),
          const SectionHeader('会话设置'),
          SectionCard(
            children: [
              SettingTile(
                icon: CupertinoIcons.pin,
                title: '置顶聊天',
                subtitle: '置顶后会话显示在消息列表前部',
                trailing: _AsyncToggle(
                  initialValue: conversation.pinned,
                  onChanged: (_) =>
                      controller.toggleConversationPinned(conversation.id),
                ),
              ),
              SettingTile(
                icon: CupertinoIcons.bell_slash,
                title: '消息免打扰',
                subtitle: '仍会显示未读红点，但不主动提醒',
                trailing: _AsyncToggle(
                  initialValue: conversation.muted,
                  onChanged: (_) =>
                      controller.toggleConversationMuted(conversation.id),
                ),
              ),
            ],
          ),
          const SectionHeader('安全与数据'),
          SectionCard(
            children: [
              SettingTile(
                key: const Key('screenshot-detection-status'),
                icon: CupertinoIcons.device_phone_portrait,
                title: '截屏提示',
                subtitle: ScreenshotDetection.instance.description,
              ),
              SettingTile(
                icon: CupertinoIcons.exclamationmark_triangle,
                title: '举报会话',
                subtitle: '填写原因后提交给平台审核',
                destructive: true,
                onTap: () => Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => ReportScreen(
                      controller: controller,
                      target: controller.displayConversationName(conversation),
                      targetId: group
                          ? conversation.id
                          : directPeer?.id ??
                                conversation.channelId ??
                                conversation.id,
                      targetType: group ? 'group' : 'user',
                    ),
                  ),
                ),
              ),
              if (!multiUser)
                SettingTile(
                  icon: CupertinoIcons.nosign,
                  title: '加入黑名单',
                  subtitle: '确认后，对方将无法继续向你发起新消息',
                  destructive: true,
                  onTap: onBlock,
                ),
              SettingTile(
                key: const Key('clear-local-chat'),
                icon: CupertinoIcons.trash,
                title: '清空本地记录',
                subtitle: '确认后仅删除当前设备缓存',
                destructive: true,
                onTap: onClearLocal,
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _ChatInfoContent extends StatelessWidget {
  const _ChatInfoContent({
    required this.controller,
    required this.conversation,
    required this.onSearch,
    required this.onClearLocal,
    required this.onBlock,
    required this.onScheduledMessages,
    this.onClose,
  });

  final AppController controller;
  final Conversation conversation;
  final VoidCallback onSearch;
  final Future<void> Function() onClearLocal;
  final Future<void> Function() onBlock;
  final VoidCallback onScheduledMessages;
  final VoidCallback? onClose;

  @override
  Widget build(BuildContext context) => Stack(
    children: [
      ChatInfoScreen(
        controller: controller,
        conversation: conversation,
        onSearch: onSearch,
        onClearLocal: onClearLocal,
        onBlock: onBlock,
        onScheduledMessages: onScheduledMessages,
      ),
      if (onClose != null)
        Positioned(
          top: 2,
          right: 4,
          child: IconButton(
            key: const Key('desktop-close-details'),
            tooltip: '收起资料栏',
            onPressed: onClose,
            icon: const Icon(CupertinoIcons.xmark),
          ),
        ),
    ],
  );
}

class _DirectContactSummary extends StatelessWidget {
  const _DirectContactSummary({
    required this.controller,
    required this.user,
    required this.fallbackName,
    this.fallbackAvatar,
    this.onTap,
  });

  final AppController controller;
  final AppUser? user;
  final String fallbackName;
  final String? fallbackAvatar;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final displayName = user == null
        ? fallbackName
        : controller.displayNameFor(user!);
    return Semantics(
      button: onTap != null,
      label: onTap == null ? null : '查看$displayName资料',
      child: InkWell(
        key: const Key('chat-info-contact-profile'),
        borderRadius: BorderRadius.circular(14),
        onTap: onTap,
        child: Row(
          children: [
            PersonAvatar(
              name: displayName,
              size: 60,
              avatarUrl: user?.avatarUrl ?? fallbackAvatar,
              online: user?.isOnline ?? false,
            ),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    displayName,
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                  const SizedBox(height: 4),
                  Text(
                    '呱呱号：${publicUserHandleLabel(user?.handle, fallback: '尚未设置')}',
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                  if ((user?.signature ?? user?.presence ?? '')
                      .trim()
                      .isNotEmpty) ...[
                    const SizedBox(height: 4),
                    Text(
                      (user?.signature ?? user?.presence)!,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: LinliColors.preview,
                      ),
                    ),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _AsyncToggle extends StatefulWidget {
  const _AsyncToggle({required this.initialValue, required this.onChanged});
  final bool initialValue;
  final Future<bool> Function(bool value) onChanged;

  @override
  State<_AsyncToggle> createState() => _AsyncToggleState();
}

class _AsyncToggleState extends State<_AsyncToggle> {
  late bool value = widget.initialValue;
  bool busy = false;

  @override
  void didUpdateWidget(covariant _AsyncToggle oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!busy && oldWidget.initialValue != widget.initialValue) {
      value = widget.initialValue;
    }
  }

  @override
  Widget build(BuildContext context) => CupertinoSwitch(
    value: value,
    onChanged: busy
        ? null
        : (next) async {
            setState(() {
              value = next;
              busy = true;
            });
            final success = await widget.onChanged(next);
            if (!mounted) return;
            setState(() {
              if (!success) value = !next;
              busy = false;
            });
          },
  );
}

class _ChatMemberMatrix extends StatelessWidget {
  const _ChatMemberMatrix({
    required this.controller,
    required this.members,
    required this.fallbackName,
    this.fallbackAvatar,
  });

  final AppController controller;
  final List<AppUser> members;
  final String fallbackName;
  final String? fallbackAvatar;

  @override
  Widget build(BuildContext context) {
    final people = members.isEmpty
        ? [
            AppUser(
              id: 'conversation',
              name: fallbackName,
              handle: '',
              presence: '',
              avatarUrl: fallbackAvatar,
            ),
          ]
        : members.take(10).toList();
    return LayoutBuilder(
      builder: (context, constraints) {
        final scale = MediaQuery.textScalerOf(context).scale(1);
        final minimumCellWidth = 96.0 * math.min(scale, 1.3);
        final fittingColumns = (constraints.maxWidth / minimumCellWidth)
            .floor()
            .clamp(2, scale >= 1.6 ? 3 : 5);
        final columns = math.max(1, math.min(people.length, fittingColumns));
        final width = constraints.maxWidth / columns;
        return Wrap(
          runSpacing: 14,
          children: [
            for (final user in people)
              SizedBox(
                key: ValueKey('chat-info-member-${user.id}'),
                width: width,
                child: Semantics(
                  label: '${controller.displayNameFor(user)}，聊天成员',
                  child: Column(
                    children: [
                      PersonAvatar(
                        name: controller.displayNameFor(user),
                        size: 50,
                        avatarUrl: user.avatarUrl,
                        online: user.isOnline,
                      ),
                      const SizedBox(height: 6),
                      Text(
                        controller.displayNameFor(user),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        textAlign: TextAlign.center,
                        style: Theme.of(context).textTheme.labelSmall,
                      ),
                    ],
                  ),
                ),
              ),
          ],
        );
      },
    );
  }
}

class _TimeDivider extends StatelessWidget {
  const _TimeDivider({required this.date});
  final DateTime date;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 14),
    child: Text(
      _displayTime(date),
      style: Theme.of(
        context,
      ).textTheme.labelSmall?.copyWith(color: LinliColors.tertiaryLabel),
    ),
  );

  static String _displayTime(DateTime date) {
    final now = DateTime.now();
    final clock =
        '${date.hour.toString().padLeft(2, '0')}:${date.minute.toString().padLeft(2, '0')}';
    if (now.year == date.year &&
        now.month == date.month &&
        now.day == date.day) {
      return '今天 $clock';
    }
    return '${date.month}月${date.day}日 $clock';
  }
}

String? supportSessionSendRestriction(
  int channelType,
  Iterable<ChatMessage> messages,
) {
  if (!const {3, 10}.contains(channelType)) return null;
  return messages.any((message) => message.event == 'support.session.ended')
      ? '客服会话已结束，如需帮助请重新发起咨询。'
      : null;
}

class MessageBubble extends StatelessWidget {
  const MessageBubble({
    super.key,
    required this.message,
    this.controller,
    this.senderName,
    this.avatarUrl,
    this.onAvatarTap,
    this.showSender = false,
    this.showGroupReceipt = false,
    this.onRetry,
    this.onLongPress,
    this.onSelect,
    this.selectionMode = false,
    this.selected = false,
    this.onReactionTap,
    this.onAddReaction,
  });

  final ChatMessage message;
  final AppController? controller;
  final String? senderName;
  final String? avatarUrl;
  final VoidCallback? onAvatarTap;
  final bool showSender;
  final bool showGroupReceipt;
  final VoidCallback? onRetry;
  final ValueChanged<Offset>? onLongPress;
  final VoidCallback? onSelect;
  final bool selectionMode;
  final bool selected;
  final ValueChanged<String>? onReactionTap;
  final VoidCallback? onAddReaction;

  @override
  Widget build(BuildContext context) {
    final publicSenderName = message.senderName.trim().isNotEmpty
        ? message.senderName.trim()
        : '对方';
    final resolvedSenderName = senderName?.trim().isNotEmpty == true
        ? senderName!.trim()
        : controller?.displayNameForId(
                message.senderId,
                fallback: publicSenderName,
              ) ??
              publicSenderName;
    // Keep raw ACK/read state in the model. Only the visible label and its
    // transition key are normalized for members without receipt access.
    final canShowReceipts =
        controller?.canDisplayMessageReceipts(message.conversationId) ??
        !showGroupReceipt;
    final displayStatus =
        !canShowReceipts &&
            (message.status == MessageStatus.delivered ||
                message.status == MessageStatus.read)
        ? MessageStatus.sent
        : message.status;
    final showReceiptCounts =
        showGroupReceipt &&
        canShowReceipts &&
        (message.status == MessageStatus.sent ||
            message.status == MessageStatus.delivered ||
            message.status == MessageStatus.read);
    if (message.status == MessageStatus.recalled ||
        message.status == MessageStatus.expired ||
        message.kind == MessageContentKind.system ||
        message.kind == MessageContentKind.screenshotNotice) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 10),
        child: Text(
          message.status == MessageStatus.recalled
              ? '消息已撤回'
              : message.status == MessageStatus.expired
              ? '消息已过期'
              : message.text,
          textAlign: TextAlign.center,
          style: Theme.of(context).textTheme.labelSmall,
        ),
      );
    }
    if (message.kind == MessageContentKind.liveEvent) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 7),
        child: Center(
          child: Container(
            key: Key('live-event-${message.clientMessageId}'),
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
            decoration: BoxDecoration(
              color: LinliColors.brandGreen.withValues(alpha: .16),
              borderRadius: BorderRadius.circular(999),
            ),
            child: Text(
              '${message.isMine ? '我' : resolvedSenderName} ${message.text}',
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.labelMedium?.copyWith(
                color: LinliColors.navy,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        ),
      );
    }
    final mine = message.isMine;
    void openFromCenter() {
      final box = context.findRenderObject() as RenderBox?;
      if (box == null || !box.hasSize) return;
      onLongPress?.call(box.localToGlobal(box.size.center(Offset.zero)));
    }

    return Semantics(
      label: '${mine ? '我' : resolvedSenderName}：${message.text}',
      explicitChildNodes: true,
      onLongPress: onLongPress == null ? null : openFromCenter,
      customSemanticsActions: onLongPress == null
          ? null
          : {const CustomSemanticsAction(label: '显示消息操作'): openFromCenter},
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: selectionMode ? onSelect : null,
        onLongPressStart: onLongPress == null
            ? null
            : (details) => onLongPress!(details.globalPosition),
        onSecondaryTapDown: onLongPress == null
            ? null
            : (details) => onLongPress!(details.globalPosition),
        child: Padding(
          padding: const EdgeInsets.only(bottom: 10),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisAlignment: mine
                ? MainAxisAlignment.end
                : MainAxisAlignment.start,
            children: [
              if (selectionMode) ...[
                SizedBox(
                  width: 40,
                  height: 44,
                  child: Icon(
                    selected
                        ? CupertinoIcons.checkmark_circle_fill
                        : CupertinoIcons.circle,
                    color: selected
                        ? LinliColors.navy
                        : LinliColors.tertiaryLabel,
                  ),
                ),
                const SizedBox(width: 4),
              ],
              if (!mine) ...[
                Semantics(
                  button: onAvatarTap != null,
                  label: onAvatarTap == null
                      ? '$resolvedSenderName头像'
                      : '查看$resolvedSenderName资料',
                  child: InkResponse(
                    key: Key('message-avatar-${message.clientMessageId}'),
                    radius: 22,
                    onTap: selectionMode ? onSelect : onAvatarTap,
                    child: PersonAvatar(
                      name: resolvedSenderName,
                      size: 34,
                      avatarUrl: avatarUrl,
                    ),
                  ),
                ),
                const SizedBox(width: 8),
              ],
              Flexible(
                child: FractionallySizedBox(
                  widthFactor: message.kind == MessageContentKind.image
                      ? .70
                      : .78,
                  alignment: mine
                      ? Alignment.centerRight
                      : Alignment.centerLeft,
                  child: Column(
                    crossAxisAlignment: mine
                        ? CrossAxisAlignment.end
                        : CrossAxisAlignment.start,
                    children: [
                      if (showSender && !mine)
                        Padding(
                          padding: const EdgeInsets.only(left: 4, bottom: 3),
                          child: Text(
                            resolvedSenderName,
                            style: Theme.of(context).textTheme.labelSmall,
                          ),
                        ),
                      _MessageContent(message: message, controller: controller),
                      if (message.kind == MessageContentKind.voice)
                        VoiceUploadProgress(
                          progress: message.status == MessageStatus.sending
                              ? controller?.mediaUploadProgressFor(
                                  message.clientMessageId,
                                )
                              : null,
                          clientMessageId: message.clientMessageId,
                        )
                      else if (message.status == MessageStatus.sending &&
                          controller?.mediaUploadProgressFor(
                                message.clientMessageId,
                              ) !=
                              null) ...[
                        const SizedBox(height: 6),
                        SizedBox(
                          width: 150,
                          child: Semantics(
                            label:
                                '上传 ${(controller!.mediaUploadProgressFor(message.clientMessageId)! * 100).round()}%',
                            child: LinearProgressIndicator(
                              key: Key(
                                'media-upload-progress-${message.clientMessageId}',
                              ),
                              value: controller!.mediaUploadProgressFor(
                                message.clientMessageId,
                              ),
                              minHeight: 3,
                              borderRadius: BorderRadius.circular(999),
                              color: LinliColors.brandGreen,
                              backgroundColor: Theme.of(
                                context,
                              ).colorScheme.surfaceContainerHighest,
                            ),
                          ),
                        ),
                      ],
                      if (message.reactions.isNotEmpty) ...[
                        const SizedBox(height: 4),
                        _MessageReactionBar(
                          reactions: message.reactions,
                          onReactionTap: onReactionTap,
                          onAddReaction: onAddReaction,
                          alignment: mine
                              ? WrapAlignment.end
                              : WrapAlignment.start,
                        ),
                      ],
                      if (message.expiresAt != null) ...[
                        const SizedBox(height: 4),
                        Text(
                          '将在 ${_expiry(message.expiresAt!)} 过期',
                          key: Key('message-expiry-${message.id}'),
                          style: Theme.of(context).textTheme.labelSmall
                              ?.copyWith(
                                color: LinliColors.tertiaryLabel,
                                fontSize: 10,
                              ),
                        ),
                      ],
                      const SizedBox(height: 3),
                      Wrap(
                        alignment: mine
                            ? WrapAlignment.end
                            : WrapAlignment.start,
                        crossAxisAlignment: WrapCrossAlignment.center,
                        spacing: 5,
                        runSpacing: 2,
                        children: [
                          Text(
                            _clock(message.sentAt),
                            style: Theme.of(context).textTheme.labelSmall
                                ?.copyWith(
                                  color: LinliColors.tertiaryLabel,
                                  fontSize: 10,
                                ),
                          ),
                          if (message.editedAt != null)
                            Text(
                              '已编辑',
                              key: Key('edited-label-${message.id}'),
                              style: Theme.of(context).textTheme.labelSmall
                                  ?.copyWith(
                                    color: LinliColors.tertiaryLabel,
                                    fontSize: 10,
                                  ),
                            ),
                          if (message.isPinned)
                            const Tooltip(
                              message: '群置顶消息',
                              child: Icon(
                                CupertinoIcons.pin_fill,
                                size: 11,
                                color: LinliColors.tertiaryLabel,
                              ),
                            ),
                          if (mine)
                            VoiceSendFeedback(
                              // Drop the outgoing animation immediately on a
                              // role change; it must not retain old counts.
                              key: ValueKey(canShowReceipts),
                              animate: message.kind == MessageContentKind.voice,
                              statusKey: displayStatus,
                              child: message.status == MessageStatus.failed
                                  ? Semantics(
                                      container: true,
                                      excludeSemantics: true,
                                      button: onRetry != null,
                                      enabled: onRetry != null,
                                      onTap: onRetry,
                                      label: onRetry == null
                                          ? '消息发送失败'
                                          : '消息发送失败，重新发送',
                                      child: InkWell(
                                        key: const Key('failed-message-retry'),
                                        borderRadius: BorderRadius.circular(8),
                                        onTap: onRetry,
                                        child: ConstrainedBox(
                                          constraints: const BoxConstraints(
                                            minHeight: 44,
                                          ),
                                          child: Padding(
                                            padding: const EdgeInsets.symmetric(
                                              horizontal: 4,
                                            ),
                                            child: Row(
                                              mainAxisSize: MainAxisSize.min,
                                              children: [
                                                const Icon(
                                                  CupertinoIcons
                                                      .exclamationmark_circle_fill,
                                                  size: 14,
                                                  color: LinliColors.systemRed,
                                                ),
                                                const SizedBox(width: 4),
                                                Text(
                                                  onRetry == null
                                                      ? '发送失败'
                                                      : '发送失败，点此重试',
                                                  style: const TextStyle(
                                                    color:
                                                        LinliColors.systemRed,
                                                    fontSize: 11,
                                                  ),
                                                ),
                                              ],
                                            ),
                                          ),
                                        ),
                                      ),
                                    )
                                  : _DeliveryLabel(
                                      status: displayStatus,
                                      deliveredCount: showReceiptCounts
                                          ? message.deliveredCount
                                          : null,
                                      readCount: showReceiptCounts
                                          ? message.readCount
                                          : null,
                                    ),
                            ),
                        ],
                      ),
                      if (mine &&
                          message.status == MessageStatus.failed &&
                          message.sendError != null)
                        Padding(
                          padding: const EdgeInsets.only(top: 2),
                          child: Text(
                            message.sendError!,
                            key: ValueKey(
                              'message-send-error-${message.clientMessageId}',
                            ),
                            style: Theme.of(context).textTheme.labelSmall
                                ?.copyWith(color: LinliColors.systemRed),
                          ),
                        ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  static String _clock(DateTime date) {
    final local = date.toLocal();
    return '${local.hour.toString().padLeft(2, '0')}:${local.minute.toString().padLeft(2, '0')}';
  }

  static String _expiry(DateTime date) {
    final local = date.toLocal();
    return '${local.month}月${local.day}日 ${_clock(local)}';
  }
}

class _MessageReactionBar extends StatelessWidget {
  const _MessageReactionBar({
    required this.reactions,
    required this.alignment,
    this.onReactionTap,
    this.onAddReaction,
  });

  final List<MessageReaction> reactions;
  final WrapAlignment alignment;
  final ValueChanged<String>? onReactionTap;
  final VoidCallback? onAddReaction;

  @override
  Widget build(BuildContext context) => Wrap(
    alignment: alignment,
    spacing: 4,
    runSpacing: 4,
    children: [
      for (final reaction in reactions)
        Semantics(
          button: true,
          selected: reaction.reactedByMe,
          label:
              '${reaction.emoji}，${reaction.count} 个回应${reaction.reactedByMe ? '，我已回应' : ''}',
          child: InkWell(
            key: Key('reaction-${reaction.emoji}'),
            borderRadius: BorderRadius.circular(12),
            onTap: onReactionTap == null
                ? null
                : () => onReactionTap!(reaction.emoji),
            child: ConstrainedBox(
              constraints: const BoxConstraints(minHeight: 44, minWidth: 44),
              child: Container(
                alignment: Alignment.center,
                padding: const EdgeInsets.symmetric(horizontal: 9),
                decoration: BoxDecoration(
                  color: reaction.reactedByMe
                      ? LinliColors.brandGreen.withValues(alpha: .18)
                      : Theme.of(context).colorScheme.surfaceContainerHigh,
                  borderRadius: BorderRadius.circular(12),
                  border: reaction.reactedByMe
                      ? Border.all(
                          color: LinliColors.brandGreen.withValues(alpha: .55),
                        )
                      : null,
                ),
                child: Text('${reaction.emoji} ${reaction.count}'),
              ),
            ),
          ),
        ),
      if (onAddReaction != null)
        IconButton(
          key: const Key('add-message-reaction'),
          tooltip: '添加回应',
          onPressed: onAddReaction,
          icon: const Icon(CupertinoIcons.add, size: 17),
          style: IconButton.styleFrom(
            backgroundColor: Theme.of(context).colorScheme.surfaceContainerHigh,
          ),
        ),
    ],
  );
}

class _MessageContent extends StatelessWidget {
  const _MessageContent({required this.message, this.controller});
  final ChatMessage message;
  final AppController? controller;

  @override
  Widget build(BuildContext context) {
    final mine = message.isMine;
    final dark = Theme.of(context).brightness == Brightness.dark;
    final bubbleColor = mine
        ? LinliColors.navy
        : dark
        ? LinliColors.darkSurfaceElevated
        : const Color(0xFFEEF2F7);
    final textColor = mine
        ? Colors.white
        : Theme.of(context).colorScheme.onSurface;

    if (message.kind == MessageContentKind.image) {
      final media = message.mediaUrl;
      if (media != null) {
        return _MessageImageContent(
          message: message,
          source: media,
          controller: controller,
          bubbleColor: bubbleColor,
          textColor: textColor,
        );
      }
      return _MediaUnavailable(
        color: bubbleColor,
        textColor: textColor,
        label: message.mediaId == null ? '图片不可用' : '暂无法下载此图片',
      );
    }

    if (message.kind == MessageContentKind.sticker) {
      final media = message.mediaUrl;
      if (media == null || media.isEmpty) {
        return _MediaUnavailable(
          color: bubbleColor,
          textColor: textColor,
          label: '表情暂不可用',
        );
      }
      return Semantics(
        label: message.fileName?.isNotEmpty == true
            ? '表情：${message.fileName}'
            : '表情消息',
        child: ClipRRect(
          borderRadius: BorderRadius.circular(14),
          child: _messageImage(
            media,
            cacheKey: message.mediaId,
            width: 132,
            height: 132,
            fit: BoxFit.contain,
            errorBuilder: (_, _, _) => _MediaUnavailable(
              color: bubbleColor,
              textColor: textColor,
              label: '表情加载失败',
            ),
          ),
        ),
      );
    }

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 13, vertical: 10),
      decoration: BoxDecoration(
        color: bubbleColor,
        borderRadius: BorderRadius.only(
          topLeft: const Radius.circular(16),
          topRight: const Radius.circular(16),
          bottomLeft: Radius.circular(mine ? 16 : 5),
          bottomRight: Radius.circular(mine ? 5 : 16),
        ),
      ),
      child: switch (message.kind) {
        MessageContentKind.voice => _VoiceMessageContent(
          message: message,
          color: textColor,
        ),
        MessageContentKind.video => _VideoMessageContent(
          message: message,
          color: textColor,
        ),
        MessageContentKind.reply => Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (message.replyToText != null)
              Container(
                margin: const EdgeInsets.only(bottom: 7),
                padding: const EdgeInsets.fromLTRB(9, 6, 8, 6),
                decoration: BoxDecoration(
                  color: textColor.withValues(alpha: .10),
                  border: Border(
                    left: BorderSide(
                      color: mine
                          ? LinliColors.brandGreen
                          : LinliColors.preview,
                      width: 2,
                    ),
                  ),
                ),
                child: Text(
                  message.replyToText!,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    color: textColor.withValues(alpha: .72),
                    fontSize: 12,
                  ),
                ),
              ),
            _TextWithPreview(message: message, color: textColor, mine: mine),
          ],
        ),
        MessageContentKind.file => _OpenableMediaContent(
          message: message,
          color: textColor,
          icon: CupertinoIcons.doc_fill,
          title: message.fileName ?? message.text.replaceFirst('[文件] ', ''),
          subtitle: message.mediaUrl == null ? '下载地址暂不可用' : '点击打开',
        ),
        MessageContentKind.contact => _ContactMessageCard(
          message: message,
          controller: controller,
          color: textColor,
        ),
        MessageContentKind.location => _LocationMessageCard(
          message: message,
          color: textColor,
        ),
        MessageContentKind.chatHistory => _ChatHistoryMessageCard(
          message: message,
          controller: controller,
          color: textColor,
        ),
        MessageContentKind.momentShare => _MomentShareMessageCard(
          message: message,
          controller: controller,
          color: textColor,
        ),
        _ => _TextWithPreview(message: message, color: textColor, mine: mine),
      },
    );
  }
}

class _MessageImageContent extends StatelessWidget {
  const _MessageImageContent({
    required this.message,
    required this.source,
    required this.controller,
    required this.bubbleColor,
    required this.textColor,
  });

  final ChatMessage message;
  final String source;
  final AppController? controller;
  final Color bubbleColor;
  final Color textColor;

  Size get _displaySize {
    final width = message.mediaWidth;
    final height = message.mediaHeight;
    if (width == null || height == null || width <= 0 || height <= 0) {
      return const Size(220, 180);
    }
    final scale = math.min(220 / width, 280 / height);
    return Size(
      (width * scale).clamp(120, 220).toDouble(),
      (height * scale).clamp(96, 280).toDouble(),
    );
  }

  void _openPreview(BuildContext context) {
    final images = controller
        ?.messagesFor(message.conversationId)
        .where(
          (item) =>
              item.kind == MessageContentKind.image &&
              item.mediaUrl?.trim().isNotEmpty == true,
        )
        .toList();
    final candidates = images?.isNotEmpty == true ? images! : [message];
    final selected = candidates.indexWhere(
      (item) => item.clientMessageId == message.clientMessageId,
    );
    Navigator.of(context).push(
      PageRouteBuilder<void>(
        opaque: true,
        fullscreenDialog: true,
        transitionDuration: nexaMotionDuration(context),
        reverseTransitionDuration: nexaMotionDuration(context),
        pageBuilder: (_, animation, _) => FadeTransition(
          opacity: CurvedAnimation(parent: animation, curve: Curves.easeOut),
          child: _MessageImagePreview(
            controller: controller,
            messages: candidates,
            initialIndex: selected < 0 ? 0 : selected,
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final size = _displaySize;
    return Semantics(
      button: true,
      label: '查看图片',
      child: Material(
        color: Theme.of(context).colorScheme.surfaceContainerHigh,
        borderRadius: BorderRadius.circular(14),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          key: Key('message-image-${message.clientMessageId}'),
          onTap: () => _openPreview(context),
          child: SizedBox(
            width: size.width,
            height: size.height,
            child: _messageImage(
              source,
              key: Key('message-image-render-${message.clientMessageId}'),
              cacheKey: message.mediaId,
              width: size.width,
              height: size.height,
              fit: BoxFit.contain,
              filterQuality: FilterQuality.medium,
              frameBuilder: (context, child, frame, wasSynchronouslyLoaded) {
                if (wasSynchronouslyLoaded || frame != null) return child;
                return Container(
                  key: Key(
                    'message-image-placeholder-${message.clientMessageId}',
                  ),
                  width: size.width,
                  height: size.height,
                  color: Theme.of(context).colorScheme.surfaceContainerHigh,
                  alignment: Alignment.center,
                  child: Icon(
                    CupertinoIcons.photo,
                    size: 24,
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                );
              },
              errorBuilder: (_, _, _) => _MediaUnavailable(
                color: bubbleColor,
                textColor: textColor,
                label: source.startsWith('http') ? '图片加载失败' : '本地图片不可用',
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _MessageImagePreview extends StatefulWidget {
  const _MessageImagePreview({
    required this.controller,
    required this.messages,
    required this.initialIndex,
  });

  final AppController? controller;
  final List<ChatMessage> messages;
  final int initialIndex;

  @override
  State<_MessageImagePreview> createState() => _MessageImagePreviewState();
}

class _MessageImagePreviewState extends State<_MessageImagePreview> {
  late final PageController _pageController;
  late int _currentIndex;
  final bool _controlsVisible = true;
  bool _busy = false;

  ChatMessage get _message => widget.messages[_currentIndex];
  String get _source => _message.mediaUrl!.trim();

  @override
  void initState() {
    super.initState();
    _currentIndex = widget.initialIndex.clamp(0, widget.messages.length - 1);
    _pageController = PageController(initialPage: _currentIndex);
  }

  @override
  void dispose() {
    _pageController.dispose();
    super.dispose();
  }

  void _closePreview() {
    if (_busy) return;
    Navigator.of(context).maybePop();
  }

  void _showMessage(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(message)));
  }

  Future<Uint8List?> _loadBytes() async {
    try {
      return await loadImageSourceBytes(
        _source,
        maxBytes: AppConfig.mediaMaxBytes,
      );
    } on ImageSourceBytesException catch (error) {
      _showMessage(error.message);
    } catch (_) {
      _showMessage('图片读取失败，请检查网络后重试');
    }
    return null;
  }

  Future<void> _save({Uint8List? editedBytes}) async {
    if (_busy) return;
    setState(() => _busy = true);
    final bytes = editedBytes ?? await _loadBytes();
    if (bytes == null) {
      if (mounted) setState(() => _busy = false);
      return;
    }
    final format = editedBytes == null
        ? _previewImageFormat(_message)
        : const _PreviewImageFormat('image/jpeg', '.jpg');
    try {
      await exportImageBytes(
        bytes,
        fileName: 'qingwaguagua-${DateTime.now().millisecondsSinceEpoch}',
        mimeType: format.mimeType,
        extension: format.extension,
      );
      _showMessage('已保存到相册');
    } on ImageExportException catch (error) {
      _showMessage(error.message);
    } catch (_) {
      _showMessage('保存失败，请稍后重试');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _edit() async {
    if (_busy) return;
    final format = _previewImageFormat(_message);
    if (format.mimeType == 'image/gif') {
      _showMessage('动图暂不支持编辑，可以先保存原图');
      return;
    }
    setState(() => _busy = true);
    final original = await _loadBytes();
    if (!mounted) return;
    setState(() => _busy = false);
    if (original == null) return;
    final edited = await editImageBeforeSending(context, original);
    if (!mounted || edited == null) return;
    final action = await showCupertinoModalPopup<String>(
      context: context,
      builder: (context) => CupertinoActionSheet(
        title: const Text('图片已编辑'),
        actions: [
          if (widget.controller != null)
            CupertinoActionSheetAction(
              key: const Key('send-edited-image'),
              onPressed: () => Navigator.pop(context, 'send'),
              child: const Text('发送到当前会话'),
            ),
          CupertinoActionSheetAction(
            key: const Key('save-edited-image'),
            onPressed: () => Navigator.pop(context, 'save'),
            child: const Text('保存到相册'),
          ),
        ],
        cancelButton: CupertinoActionSheetAction(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
      ),
    );
    if (!mounted) return;
    if (action == 'save') {
      await _save(editedBytes: edited);
    } else if (action == 'send') {
      await _sendEdited(edited);
    }
  }

  Future<void> _sendEdited(Uint8List bytes) async {
    final controller = widget.controller;
    if (controller == null || _busy) return;
    setState(() => _busy = true);
    try {
      final path = await persistEditedImage(bytes);
      final result = await controller.sendMedia(
        _message.conversationId,
        MediaUpload(
          bytes: bytes,
          fileName: 'qingwaguagua-edited.jpg',
          mimeType: 'image/jpeg',
          kind: MessageContentKind.image,
          localPath: path,
        ),
      );
      _showMessage(
        result.status == MessageStatus.failed ? '发送失败，请稍后重试' : '已发送',
      );
    } catch (_) {
      _showMessage('发送失败，请稍后重试');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _forward() async {
    final controller = widget.controller;
    if (controller == null) {
      _showMessage('当前页面暂不支持转发');
      return;
    }
    if (_message.id.startsWith('local-') ||
        _message.status == MessageStatus.failed) {
      _showMessage('图片发送成功后才能转发');
      return;
    }
    final result = await showForwardConversations(
      context,
      controller: controller,
      messages: [_message],
      mode: 'separate',
    );
    if (!mounted || result == null) return;
    _showMessage(
      result.allSucceeded
          ? '已转发到 ${result.succeeded} 个会话'
          : '转发完成：成功 ${result.succeeded}，失败 ${result.failed}，未发送 ${result.notSent}',
    );
  }

  Future<void> _showActions() async {
    final action = await showCupertinoModalPopup<String>(
      context: context,
      builder: (context) => CupertinoActionSheet(
        actions: [
          CupertinoActionSheetAction(
            onPressed: () => Navigator.pop(context, 'edit'),
            child: const Text('编辑'),
          ),
          CupertinoActionSheetAction(
            onPressed: () => Navigator.pop(context, 'forward'),
            child: const Text('转发'),
          ),
          CupertinoActionSheetAction(
            onPressed: () => Navigator.pop(context, 'save'),
            child: const Text('保存原图'),
          ),
        ],
        cancelButton: CupertinoActionSheetAction(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
      ),
    );
    if (!mounted) return;
    if (action == 'edit') await _edit();
    if (action == 'forward') await _forward();
    if (action == 'save') await _save();
  }

  @override
  Widget build(BuildContext context) {
    final media = MediaQuery.of(context);
    return Scaffold(
      key: const Key('message-image-preview'),
      backgroundColor: const Color(0xFF080B0A),
      body: Stack(
        fit: StackFit.expand,
        children: [
          PageView.builder(
            key: const Key('message-image-pages'),
            controller: _pageController,
            itemCount: widget.messages.length,
            onPageChanged: (index) => setState(() => _currentIndex = index),
            itemBuilder: (context, index) => _ZoomableMessageImage(
              message: widget.messages[index],
              current: index == _currentIndex,
              width: media.size.width,
              height: media.size.height,
              onTap: _closePreview,
              onLongPress: _showActions,
            ),
          ),
          Positioned.fill(
            child: IgnorePointer(
              ignoring: !_controlsVisible,
              child: AnimatedOpacity(
                duration: nexaMotionDuration(context),
                opacity: _controlsVisible ? 1 : 0,
                child: Stack(
                  children: [
                    Positioned(
                      key: const Key('message-image-preview-top-scrim'),
                      left: 0,
                      right: 0,
                      top: 0,
                      height: media.padding.top + 92,
                      child: const IgnorePointer(
                        child: DecoratedBox(
                          decoration: BoxDecoration(
                            gradient: LinearGradient(
                              begin: Alignment.topCenter,
                              end: Alignment.bottomCenter,
                              colors: [Color(0xB3000000), Color(0x00000000)],
                            ),
                          ),
                        ),
                      ),
                    ),
                    Positioned(
                      key: const Key('message-image-preview-bottom-scrim'),
                      left: 0,
                      right: 0,
                      bottom: 0,
                      height: media.padding.bottom + 106,
                      child: const IgnorePointer(
                        child: DecoratedBox(
                          decoration: BoxDecoration(
                            gradient: LinearGradient(
                              begin: Alignment.topCenter,
                              end: Alignment.bottomCenter,
                              colors: [Color(0x00000000), Color(0x8F000000)],
                            ),
                          ),
                        ),
                      ),
                    ),
                    Positioned(
                      left: 12,
                      right: 12,
                      top: media.padding.top + 8,
                      child: Row(
                        children: [
                          IconButton.filledTonal(
                            key: const Key('close-message-image-preview'),
                            tooltip: '关闭图片',
                            onPressed: () => Navigator.pop(context),
                            style: IconButton.styleFrom(
                              minimumSize: const Size.square(44),
                              backgroundColor: Colors.black.withValues(
                                alpha: .72,
                              ),
                              foregroundColor: Colors.white,
                              side: BorderSide(
                                color: Colors.white.withValues(alpha: .18),
                              ),
                            ),
                            icon: const Icon(CupertinoIcons.xmark, size: 20),
                          ),
                          const Spacer(),
                          if (widget.messages.length > 1)
                            Container(
                              padding: const EdgeInsets.symmetric(
                                horizontal: 11,
                                vertical: 6,
                              ),
                              decoration: BoxDecoration(
                                color: Colors.black.withValues(alpha: .72),
                                borderRadius: BorderRadius.circular(999),
                                border: Border.all(
                                  color: Colors.white.withValues(alpha: .18),
                                ),
                              ),
                              child: Text(
                                '${_currentIndex + 1} / ${widget.messages.length}',
                                style: const TextStyle(
                                  color: Colors.white,
                                  fontSize: 13,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                            ),
                          const Spacer(),
                          IconButton.filledTonal(
                            key: const Key('more-message-image-preview'),
                            tooltip: '更多图片操作',
                            onPressed: _showActions,
                            style: IconButton.styleFrom(
                              minimumSize: const Size.square(44),
                              backgroundColor: Colors.black.withValues(
                                alpha: .72,
                              ),
                              foregroundColor: Colors.white,
                              side: BorderSide(
                                color: Colors.white.withValues(alpha: .18),
                              ),
                            ),
                            icon: const Icon(CupertinoIcons.ellipsis, size: 21),
                          ),
                        ],
                      ),
                    ),
                    Positioned(
                      left: 12,
                      right: 12,
                      bottom: media.padding.bottom + 10,
                      child: _ImagePreviewToolbar(
                        busy: _busy,
                        onEdit: _edit,
                        onForward: _forward,
                        onSave: () => _save(),
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
          if (_busy)
            const Center(
              child: CircularProgressIndicator(
                color: LinliColors.brandGreen,
                strokeWidth: 2.5,
              ),
            ),
        ],
      ),
    );
  }
}

class _ZoomableMessageImage extends StatefulWidget {
  const _ZoomableMessageImage({
    required this.message,
    required this.current,
    required this.width,
    required this.height,
    required this.onTap,
    required this.onLongPress,
  });

  final ChatMessage message;
  final bool current;
  final double width;
  final double height;
  final VoidCallback onTap;
  final VoidCallback onLongPress;

  @override
  State<_ZoomableMessageImage> createState() => _ZoomableMessageImageState();
}

class _ZoomableMessageImageState extends State<_ZoomableMessageImage> {
  final TransformationController _controller = TransformationController();
  TapDownDetails? _doubleTapDetails;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _toggleZoom() {
    final zoomed = _controller.value.getMaxScaleOnAxis() > 1.05;
    if (zoomed) {
      _controller.value = Matrix4.identity();
      return;
    }
    final position =
        _doubleTapDetails?.localPosition ??
        Offset(widget.width / 2, widget.height / 2);
    _controller.value = Matrix4.identity()
      ..translateByDouble(-position.dx * 1.5, -position.dy * 1.5, 0, 1)
      ..scaleByDouble(2.5, 2.5, 1, 1);
  }

  @override
  Widget build(BuildContext context) {
    final id = widget.message.clientMessageId;
    return InteractiveViewer(
      key: widget.current
          ? const Key('message-image-interactive-viewer')
          : Key('message-image-interactive-viewer-$id'),
      transformationController: _controller,
      minScale: 1,
      maxScale: 5,
      clipBehavior: Clip.none,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: widget.onTap,
        onDoubleTapDown: (details) => _doubleTapDetails = details,
        onDoubleTap: _toggleZoom,
        onLongPress: widget.onLongPress,
        child: SizedBox(
          width: widget.width,
          height: widget.height,
          child: _messageImage(
            widget.message.mediaUrl!.trim(),
            key: widget.current
                ? const Key('message-image-preview-render')
                : Key('message-image-preview-render-$id'),
            width: widget.width,
            height: widget.height,
            cacheKey: widget.message.mediaId,
            fit: BoxFit.contain,
            filterQuality: FilterQuality.high,
            errorBuilder: (_, _, _) => const Center(
              child: _MediaUnavailable(
                color: Color(0xFF161B19),
                textColor: Colors.white70,
                label: '图片加载失败',
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _ImagePreviewToolbar extends StatelessWidget {
  const _ImagePreviewToolbar({
    required this.busy,
    required this.onEdit,
    required this.onForward,
    required this.onSave,
  });

  final bool busy;
  final VoidCallback onEdit;
  final VoidCallback onForward;
  final VoidCallback onSave;

  @override
  Widget build(BuildContext context) => DecoratedBox(
    decoration: BoxDecoration(
      color: const Color(0xE61A1D1C),
      borderRadius: BorderRadius.circular(18),
      border: Border.all(color: Colors.white.withValues(alpha: .10)),
    ),
    child: Row(
      children: [
        _ImagePreviewAction(
          key: const Key('edit-message-image-preview'),
          icon: CupertinoIcons.pencil,
          label: '编辑',
          onTap: busy ? null : onEdit,
        ),
        _ImagePreviewAction(
          key: const Key('forward-message-image-preview'),
          icon: CupertinoIcons.arrowshape_turn_up_right,
          label: '转发',
          onTap: busy ? null : onForward,
        ),
        _ImagePreviewAction(
          key: const Key('save-message-image-preview'),
          icon: CupertinoIcons.arrow_down_to_line,
          label: '保存',
          onTap: busy ? null : onSave,
        ),
      ],
    ),
  );
}

class _ImagePreviewAction extends StatelessWidget {
  const _ImagePreviewAction({
    super.key,
    required this.icon,
    required this.label,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) => Expanded(
    child: InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(18),
      child: ConstrainedBox(
        constraints: const BoxConstraints(minHeight: 58),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(icon, color: Colors.white, size: 21),
            const SizedBox(height: 3),
            Text(
              label,
              style: const TextStyle(color: Colors.white, fontSize: 12),
            ),
          ],
        ),
      ),
    ),
  );
}

class _PreviewImageFormat {
  const _PreviewImageFormat(this.mimeType, this.extension);

  final String mimeType;
  final String extension;
}

_PreviewImageFormat _previewImageFormat(ChatMessage message) {
  final mime = message.mimeType?.toLowerCase().trim();
  if (mime == 'image/png') {
    return const _PreviewImageFormat('image/png', '.png');
  }
  if (mime == 'image/gif') {
    return const _PreviewImageFormat('image/gif', '.gif');
  }
  final source = message.mediaUrl?.toLowerCase() ?? '';
  if (source.contains('image/png') || source.endsWith('.png')) {
    return const _PreviewImageFormat('image/png', '.png');
  }
  if (source.contains('image/gif') || source.endsWith('.gif')) {
    return const _PreviewImageFormat('image/gif', '.gif');
  }
  return const _PreviewImageFormat('image/jpeg', '.jpg');
}

Widget _messageImage(
  String source, {
  Key? key,
  String? cacheKey,
  double? width,
  double? height,
  BoxFit fit = BoxFit.contain,
  FilterQuality filterQuality = FilterQuality.medium,
  ImageFrameBuilder? frameBuilder,
  ImageErrorWidgetBuilder? errorBuilder,
}) {
  if (source.startsWith('assets/')) {
    return Image.asset(
      source,
      key: key,
      width: width,
      height: height,
      fit: fit,
      filterQuality: filterQuality,
      frameBuilder: frameBuilder,
      errorBuilder: errorBuilder,
    );
  }
  if (source.startsWith('http://') || source.startsWith('https://')) {
    return CachedNetworkImage(
      key: key,
      imageUrl: source,
      cacheKey: cacheKey,
      width: width,
      height: height,
      fit: fit,
      filterQuality: filterQuality,
      fadeInDuration: const Duration(milliseconds: 90),
      fadeOutDuration: Duration.zero,
      imageBuilder: (context, provider) => Image(
        image: provider,
        width: width,
        height: height,
        fit: fit,
        filterQuality: filterQuality,
      ),
      placeholder: frameBuilder == null
          ? null
          : (context, _) =>
                frameBuilder(context, const SizedBox.shrink(), null, false),
      errorWidget: errorBuilder == null
          ? null
          : (context, _, error) => errorBuilder(context, error, null),
    );
  }
  if (source.startsWith('data:') || source.startsWith('blob:')) {
    return Image.network(
      source,
      key: key,
      width: width,
      height: height,
      fit: fit,
      filterQuality: filterQuality,
      frameBuilder: frameBuilder,
      errorBuilder: errorBuilder,
    );
  }
  return Image.file(
    File(source),
    key: key,
    width: width,
    height: height,
    fit: fit,
    filterQuality: filterQuality,
    frameBuilder: frameBuilder,
    errorBuilder: errorBuilder,
  );
}

class _ChatHistoryMessageCard extends StatelessWidget {
  const _ChatHistoryMessageCard({
    required this.message,
    required this.controller,
    required this.color,
  });

  final ChatMessage message;
  final AppController? controller;
  final Color color;

  @override
  Widget build(BuildContext context) {
    final entries = message.chatHistoryEntries;
    return Semantics(
      button: entries.isNotEmpty,
      label: '合并聊天记录，共 ${entries.length} 条',
      child: InkWell(
        key: Key('chat-history-${message.clientMessageId}'),
        onTap: entries.isEmpty
            ? null
            : () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => _ChatHistoryDetailScreen(
                    message: message,
                    controller: controller,
                  ),
                ),
              ),
        borderRadius: BorderRadius.circular(10),
        child: SizedBox(
          width: 230,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Icon(
                    CupertinoIcons.chat_bubble_2_fill,
                    size: 18,
                    color: color,
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      '聊天记录',
                      style: TextStyle(
                        color: color,
                        fontSize: 15,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                  if (entries.isNotEmpty)
                    Icon(
                      CupertinoIcons.chevron_right,
                      size: 15,
                      color: color.withValues(alpha: .65),
                    ),
                ],
              ),
              const SizedBox(height: 8),
              if (entries.isEmpty)
                Text(
                  '记录明细暂不可用',
                  style: TextStyle(
                    color: color.withValues(alpha: .72),
                    fontSize: 12,
                  ),
                )
              else
                for (final entry in entries.take(3))
                  Padding(
                    padding: const EdgeInsets.only(bottom: 3),
                    child: Text(
                      '${_chatHistorySender(controller, message, entry)}：${entry.summary}',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: color.withValues(alpha: .78),
                        fontSize: 12,
                      ),
                    ),
                  ),
              const SizedBox(height: 7),
              Text(
                entries.isEmpty ? '合并转发' : '共 ${entries.length} 条消息',
                style: TextStyle(
                  color: color.withValues(alpha: .58),
                  fontSize: 11,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _ChatHistoryDetailScreen extends StatelessWidget {
  const _ChatHistoryDetailScreen({required this.message, this.controller});

  final ChatMessage message;
  final AppController? controller;

  @override
  Widget build(BuildContext context) => controller == null
      ? _buildContent(context)
      : AnimatedBuilder(
          animation: controller!,
          builder: (context, _) => _buildContent(context),
        );

  Widget _buildContent(BuildContext context) {
    final entries = message.chatHistoryEntries;
    return Scaffold(
      appBar: AppBar(title: const Text('聊天记录')),
      body: SafeArea(
        child: ListView.separated(
          key: const Key('chat-history-detail-list'),
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 28),
          itemCount: entries.length,
          separatorBuilder: (_, _) => const Divider(height: 1, indent: 48),
          itemBuilder: (context, index) {
            final entry = entries[index];
            return ListTile(
              key: Key('chat-history-entry-${entry.sourceMessageId}'),
              contentPadding: const EdgeInsets.symmetric(
                horizontal: 4,
                vertical: 5,
              ),
              leading: CircleAvatar(
                backgroundColor: LinliColors.brandGreen.withValues(alpha: .22),
                foregroundColor: LinliColors.navy,
                child: Icon(_chatHistoryIcon(entry.type), size: 18),
              ),
              title: Row(
                children: [
                  Expanded(
                    child: Text(
                      _chatHistorySender(controller, message, entry),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: Theme.of(context).textTheme.titleSmall,
                    ),
                  ),
                  Text(
                    _chatHistoryTime(entry.createdAt),
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ],
              ),
              subtitle: Padding(
                padding: const EdgeInsets.only(top: 5),
                child: Text(
                  entry.summary,
                  style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                    color: Theme.of(context).colorScheme.onSurface,
                  ),
                ),
              ),
            );
          },
        ),
      ),
    );
  }
}

String _chatHistorySender(
  AppController? controller,
  ChatMessage message,
  ChatHistoryEntry entry,
) {
  if (entry.senderId.isEmpty) return '未知用户';
  if (entry.senderId == controller?.currentUser?.id) {
    return controller?.currentUser?.name ?? '我';
  }
  for (final contact in controller?.contacts ?? const <AppUser>[]) {
    if (contact.id == entry.senderId) return contact.displayName;
  }
  for (final conversation
      in controller?.conversations ?? const <Conversation>[]) {
    if (conversation.id != message.conversationId) continue;
    for (final member in conversation.members) {
      if (member.id == entry.senderId) return member.name;
    }
  }
  return entry.senderId;
}

IconData _chatHistoryIcon(String type) => switch (type) {
  'image' => CupertinoIcons.photo_fill,
  'audio' || 'voice' => CupertinoIcons.waveform,
  'video' => CupertinoIcons.video_camera_solid,
  'file' => CupertinoIcons.doc_fill,
  'location' => CupertinoIcons.location_solid,
  'contact' => CupertinoIcons.person_crop_rectangle_fill,
  _ => CupertinoIcons.chat_bubble_text_fill,
};

String _chatHistoryTime(DateTime value) {
  final local = value.toLocal();
  String two(int number) => number.toString().padLeft(2, '0');
  return '${local.year}-${two(local.month)}-${two(local.day)} '
      '${two(local.hour)}:${two(local.minute)}';
}

class _MomentShareMessageCard extends StatelessWidget {
  const _MomentShareMessageCard({
    required this.message,
    required this.controller,
    required this.color,
  });

  final ChatMessage message;
  final AppController? controller;
  final Color color;

  @override
  Widget build(BuildContext context) {
    final preview = message.text.replaceFirst('[朋友圈]', '').trim();
    final media = message.mediaUrl;
    final isImage = message.mimeType?.startsWith('image/') == true;
    return Semantics(
      button: controller != null,
      label: '朋友圈分享${preview.isEmpty ? '' : '：$preview'}',
      child: InkWell(
        onTap: controller == null
            ? null
            : () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => MomentsScreen(controller: controller!),
                ),
              ),
        borderRadius: BorderRadius.circular(10),
        child: SizedBox(
          width: 220,
          child: Row(
            children: [
              Container(
                width: 54,
                height: 54,
                clipBehavior: Clip.antiAlias,
                decoration: BoxDecoration(
                  color: color.withValues(alpha: .10),
                  borderRadius: BorderRadius.circular(10),
                ),
                child: isImage && media != null && media.isNotEmpty
                    ? _messageImage(
                        media,
                        cacheKey: message.mediaId,
                        fit: BoxFit.cover,
                        errorBuilder: (_, _, _) =>
                            Icon(CupertinoIcons.person_2_fill, color: color),
                      )
                    : Icon(CupertinoIcons.person_2_fill, color: color),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      '朋友圈',
                      style: TextStyle(
                        color: color,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(height: 3),
                    Text(
                      preview.isEmpty ? '查看分享内容' : preview,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(color: color.withValues(alpha: .78)),
                    ),
                  ],
                ),
              ),
              Icon(CupertinoIcons.chevron_forward, size: 15, color: color),
            ],
          ),
        ),
      ),
    );
  }
}

class _TextWithPreview extends StatelessWidget {
  const _TextWithPreview({
    required this.message,
    required this.color,
    required this.mine,
  });

  final ChatMessage message;
  final Color color;
  final bool mine;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      _MentionedMessageText(message: message, color: color, mine: mine),
      if (message.linkPreview case final preview?) ...[
        const SizedBox(height: 10),
        _ServerLinkPreview(preview: preview, color: color),
      ],
    ],
  );
}

class _ServerLinkPreview extends StatelessWidget {
  const _ServerLinkPreview({required this.preview, required this.color});

  final LinkPreview preview;
  final Color color;

  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    link: true,
    label: '打开链接：${preview.title.isEmpty ? preview.url : preview.title}',
    child: InkWell(
      key: const Key('server-link-preview'),
      borderRadius: BorderRadius.circular(12),
      onTap: () => _openMessageUri(context, Uri.tryParse(preview.url)),
      child: Container(
        constraints: const BoxConstraints(maxWidth: 280, minHeight: 48),
        clipBehavior: Clip.antiAlias,
        decoration: BoxDecoration(
          color: color.withValues(alpha: .09),
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: color.withValues(alpha: .14)),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (preview.imageUrl case final imageUrl?)
              LinliNetworkImage(
                url: imageUrl,
                cacheKey: imageUrl,
                width: double.infinity,
                height: 112,
                fit: BoxFit.cover,
                placeholderBuilder: (context) => ColoredBox(
                  color: Theme.of(context).colorScheme.surfaceContainerHigh,
                ),
                errorBuilder: (_) => const SizedBox.shrink(),
              ),
            Padding(
              padding: const EdgeInsets.all(12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (preview.siteName.isNotEmpty)
                    Text(
                      preview.siteName,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: color.withValues(alpha: .65),
                        fontSize: 11,
                      ),
                    ),
                  if (preview.title.isNotEmpty) ...[
                    const SizedBox(height: 3),
                    Text(
                      preview.title,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: color,
                        fontSize: 14,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ],
                  if (preview.description.isNotEmpty) ...[
                    const SizedBox(height: 4),
                    Text(
                      preview.description,
                      maxLines: 3,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: color.withValues(alpha: .72),
                        fontSize: 12,
                      ),
                    ),
                  ],
                  const SizedBox(height: 5),
                  Text(
                    preview.url,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: color.withValues(alpha: .62),
                      fontSize: 10,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    ),
  );
}

class _MentionedMessageText extends StatefulWidget {
  const _MentionedMessageText({
    required this.message,
    required this.color,
    required this.mine,
  });

  final ChatMessage message;
  final Color color;
  final bool mine;

  @override
  State<_MentionedMessageText> createState() => _MentionedMessageTextState();
}

class _MentionedMessageTextState extends State<_MentionedMessageText> {
  final List<TapGestureRecognizer> _recognizers = [];

  static final RegExp _interactivePattern = RegExp(
    r'https?://[^\s<>]+|[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}|(?<!\d)(?:\+?86[- ]?)?1[3-9]\d{9}(?!\d)',
  );

  @override
  void dispose() {
    for (final recognizer in _recognizers) {
      recognizer.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    for (final recognizer in _recognizers) {
      recognizer.dispose();
    }
    _recognizers.clear();
    final base = Theme.of(
      context,
    ).textTheme.bodyLarge?.copyWith(color: widget.color);
    final mentionTokens = widget.message.mentions
        .map((mention) => mention.isEveryone ? '@所有人' : '@${mention.name}')
        .toSet()
        .toList();
    final patternParts = <String>[
      ...mentionTokens.map(RegExp.escape),
      _interactivePattern.pattern,
    ];
    final pattern = RegExp(patternParts.join('|'));
    final spans = <TextSpan>[];
    var offset = 0;
    for (final match in pattern.allMatches(widget.message.text)) {
      if (match.start > offset) {
        spans.add(
          TextSpan(text: widget.message.text.substring(offset, match.start)),
        );
      }
      final token = match.group(0)!;
      final uri = _messageTokenUri(token);
      if (uri == null) {
        spans.add(
          TextSpan(
            text: token,
            style: TextStyle(
              color: widget.mine ? LinliColors.brandGreen : LinliColors.navy,
              fontWeight: FontWeight.w600,
            ),
          ),
        );
      } else {
        final recognizer = TapGestureRecognizer()
          ..onTap = () => _openMessageUri(context, uri);
        _recognizers.add(recognizer);
        spans.add(
          TextSpan(
            text: token,
            semanticsLabel: '${_messageTokenLabel(uri)}：$token',
            style: TextStyle(
              color: widget.mine
                  ? LinliColors.brandGreen
                  : LinliColors.brandGreenDeep,
              fontWeight: FontWeight.w600,
              decoration: TextDecoration.underline,
              decorationColor: widget.mine
                  ? LinliColors.brandGreen
                  : LinliColors.brandGreenDeep,
            ),
            recognizer: recognizer,
            mouseCursor: SystemMouseCursors.click,
          ),
        );
      }
      offset = match.end;
    }
    if (offset < widget.message.text.length) {
      spans.add(TextSpan(text: widget.message.text.substring(offset)));
    }
    return Text.rich(TextSpan(style: base, children: spans));
  }
}

Uri? _messageTokenUri(String token) {
  if (token.startsWith('http://') || token.startsWith('https://')) {
    return Uri.tryParse(token);
  }
  if (token.contains('@')) return Uri(scheme: 'mailto', path: token);
  final phone = token.replaceAll(RegExp(r'[\s-]'), '');
  if (RegExp(r'^(?:\+?86)?1[3-9]\d{9}$').hasMatch(phone)) {
    return Uri(scheme: 'tel', path: phone);
  }
  return null;
}

String _messageTokenLabel(Uri uri) => switch (uri.scheme) {
  'mailto' => '发送邮件',
  'tel' => '拨打电话',
  _ => '打开链接',
};

Future<void> _openMessageUri(BuildContext context, Uri? uri) async {
  if (uri != null &&
      await launchUrl(uri, mode: LaunchMode.externalApplication)) {
    return;
  }
  if (!context.mounted) return;
  ScaffoldMessenger.maybeOf(
    context,
  )?.showSnackBar(const SnackBar(content: Text('暂时无法打开，请稍后重试')));
}

class _VideoMessageContent extends StatelessWidget {
  const _VideoMessageContent({required this.message, required this.color});

  final ChatMessage message;
  final Color color;

  @override
  Widget build(BuildContext context) {
    final source = message.mediaUrl?.trim();
    final title = message.fileName?.trim().isNotEmpty == true
        ? message.fileName!.trim()
        : '视频消息';
    final duration = message.durationSeconds;
    return Semantics(
      button: source?.isNotEmpty == true,
      label: source?.isNotEmpty == true ? '播放$title' : '视频暂不可播放',
      child: InkWell(
        key: Key('play-video-${message.id}'),
        onTap: source?.isNotEmpty == true
            ? () => showMessageVideo(context, source: source!, title: title)
            : null,
        borderRadius: BorderRadius.circular(12),
        child: ConstrainedBox(
          constraints: const BoxConstraints(minWidth: 178),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                width: 42,
                height: 42,
                decoration: BoxDecoration(
                  color: color.withValues(alpha: .12),
                  shape: BoxShape.circle,
                ),
                child: Icon(CupertinoIcons.play_fill, color: color, size: 22),
              ),
              const SizedBox(width: 10),
              Flexible(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      title,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(color: color, fontSize: 15),
                    ),
                    Text(
                      source?.isNotEmpty == true
                          ? duration == null || duration <= 0
                                ? '点击播放'
                                : '$duration 秒 · 点击播放'
                          : '视频地址暂不可用',
                      style: TextStyle(
                        color: color.withValues(alpha: .65),
                        fontSize: 11,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _OpenableMediaContent extends StatefulWidget {
  const _OpenableMediaContent({
    required this.message,
    required this.color,
    required this.icon,
    required this.title,
    required this.subtitle,
  });

  final ChatMessage message;
  final Color color;
  final IconData icon;
  final String title;
  final String subtitle;

  @override
  State<_OpenableMediaContent> createState() => _OpenableMediaContentState();
}

class _OpenableMediaContentState extends State<_OpenableMediaContent> {
  bool opening = false;

  Future<void> _open() async {
    if (opening) return;
    setState(() => opening = true);
    String? error;
    try {
      error = await openMessageMedia(
        widget.message,
        maxBytes: AppConfig.mediaMaxBytes,
      );
    } catch (_) {
      error = '文件打开失败，请稍后重试';
    }
    if (!mounted) return;
    setState(() => opening = false);
    if (error != null) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(error)));
    }
  }

  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    label: '打开${widget.title}',
    child: InkWell(
      onTap: opening ? null : _open,
      borderRadius: BorderRadius.circular(12),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (opening)
            SizedBox.square(
              dimension: 28,
              child: CircularProgressIndicator(
                strokeWidth: 2,
                color: widget.color,
              ),
            )
          else
            Icon(widget.icon, size: 30, color: widget.color),
          const SizedBox(width: 10),
          Flexible(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  widget.title,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(color: widget.color, fontSize: 15),
                ),
                Text(
                  opening ? '正在准备…' : widget.subtitle,
                  style: TextStyle(
                    color: widget.color.withValues(alpha: .65),
                    fontSize: 11,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    ),
  );
}

class _ContactMessageCard extends StatelessWidget {
  const _ContactMessageCard({
    required this.message,
    required this.controller,
    required this.color,
  });

  final ChatMessage message;
  final AppController? controller;
  final Color color;

  @override
  Widget build(BuildContext context) {
    final name = message.contactName?.trim().isNotEmpty == true
        ? message.contactName!
        : '联系人';
    final handle = message.contactHandle?.trim() ?? '';
    return Semantics(
      button: controller != null && message.contactUserId != null,
      label: '$name 的联系人名片${handle.isEmpty ? '' : '，账号 $handle'}',
      child: InkWell(
        key: Key('contact-message-${message.clientMessageId}'),
        borderRadius: BorderRadius.circular(12),
        onTap: controller == null || message.contactUserId == null
            ? null
            : () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => FriendProfileScreen(
                    controller: controller!,
                    user: AppUser(
                      id: message.contactUserId!,
                      name: name,
                      handle: handle,
                      presence: '来自联系人名片',
                      avatarUrl: message.contactAvatarUrl,
                    ),
                    requestSource: 'card',
                  ),
                ),
              ),
        child: SizedBox(
          width: 210,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  PersonAvatar(
                    name: name,
                    size: 42,
                    avatarUrl: message.contactAvatarUrl,
                  ),
                  const SizedBox(width: 11),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          name,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: color,
                            fontSize: 15,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                        if (handle.isNotEmpty)
                          Text(
                            '@$handle',
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              color: color.withValues(alpha: .68),
                              fontSize: 12,
                            ),
                          ),
                      ],
                    ),
                  ),
                  Icon(CupertinoIcons.chevron_forward, size: 15, color: color),
                ],
              ),
              const SizedBox(height: 9),
              Divider(height: 1, color: color.withValues(alpha: .16)),
              const SizedBox(height: 7),
              Text(
                '联系人名片',
                style: TextStyle(
                  color: color.withValues(alpha: .62),
                  fontSize: 11,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _LocationMessageCard extends StatelessWidget {
  const _LocationMessageCard({required this.message, required this.color});

  final ChatMessage message;
  final Color color;

  @override
  Widget build(BuildContext context) {
    final name = message.locationName?.trim().isNotEmpty == true
        ? message.locationName!
        : '共享位置';
    final address = message.locationAddress?.trim() ?? '';
    final coordinates = message.latitude == null || message.longitude == null
        ? ''
        : '${message.latitude!.toStringAsFixed(6)}, ${message.longitude!.toStringAsFixed(6)}';
    return Semantics(
      button: coordinates.isNotEmpty,
      label: '位置消息，$name${address.isEmpty ? '' : '，$address'}',
      child: InkWell(
        key: Key('location-message-${message.clientMessageId}'),
        borderRadius: BorderRadius.circular(12),
        onTap: coordinates.isEmpty
            ? null
            : () => showCupertinoDialog<void>(
                context: context,
                builder: (context) => CupertinoAlertDialog(
                  title: Text(name),
                  content: Text(
                    '\n${address.isEmpty ? '地址信息不可用' : address}\n$coordinates',
                  ),
                  actions: [
                    CupertinoDialogAction(
                      onPressed: () async {
                        await Clipboard.setData(
                          ClipboardData(text: coordinates),
                        );
                        if (context.mounted) Navigator.pop(context);
                      },
                      child: const Text('复制坐标'),
                    ),
                    CupertinoDialogAction(
                      isDefaultAction: true,
                      onPressed: () => Navigator.pop(context),
                      child: const Text('完成'),
                    ),
                  ],
                ),
              ),
        child: SizedBox(
          width: 220,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Container(
                    width: 42,
                    height: 42,
                    decoration: BoxDecoration(
                      color: color.withValues(alpha: .12),
                      shape: BoxShape.circle,
                    ),
                    child: Icon(
                      CupertinoIcons.location_fill,
                      color: color,
                      size: 22,
                    ),
                  ),
                  const SizedBox(width: 11),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          name,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: color,
                            fontSize: 15,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                        Text(
                          address.isEmpty ? '点按查看坐标' : address,
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: color.withValues(alpha: .68),
                            fontSize: 11,
                          ),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _VoiceMessageContent extends StatefulWidget {
  const _VoiceMessageContent({required this.message, required this.color});

  final ChatMessage message;
  final Color color;

  @override
  State<_VoiceMessageContent> createState() => _VoiceMessageContentState();
}

class _VoiceMessageContentState extends State<_VoiceMessageContent> {
  final AudioPlayer _player = AudioPlayer();
  bool _playing = false;

  @override
  void initState() {
    super.initState();
    _player.onPlayerComplete.listen((_) {
      if (mounted) setState(() => _playing = false);
    });
  }

  @override
  void dispose() {
    unawaited(_player.dispose());
    super.dispose();
  }

  Future<void> _toggle() async {
    if (_playing) {
      await _player.stop();
      if (mounted) setState(() => _playing = false);
      return;
    }
    final source = widget.message.mediaUrl;
    if (source == null || source.isEmpty) {
      _showUnavailable();
      return;
    }
    try {
      if (source.startsWith('http://') || source.startsWith('https://')) {
        await _player.play(
          UrlSource(source, mimeType: widget.message.mimeType),
        );
      } else if (source.startsWith('/')) {
        if (!await File(source).exists()) {
          _showUnavailable();
          return;
        }
        await _player.play(
          DeviceFileSource(source, mimeType: widget.message.mimeType),
        );
      } else {
        _showUnavailable();
        return;
      }
      if (mounted) setState(() => _playing = true);
    } catch (_) {
      _showUnavailable();
    }
  }

  void _showUnavailable() {
    if (!mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('语音暂不可播放，请稍后重试')));
  }

  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    label: _playing ? '停止播放语音' : '播放语音',
    child: InkWell(
      key: Key('voice-message-${widget.message.clientMessageId}'),
      borderRadius: BorderRadius.circular(12),
      onTap: _toggle,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 2, vertical: 1),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              _playing ? CupertinoIcons.stop_fill : CupertinoIcons.waveform,
              size: 22,
              color: widget.color,
            ),
            const SizedBox(width: 10),
            Text(
              '${widget.message.durationSeconds ?? 1}″',
              style: TextStyle(color: widget.color, fontSize: 15),
            ),
          ],
        ),
      ),
    ),
  );
}

class _MediaUnavailable extends StatelessWidget {
  const _MediaUnavailable({
    required this.color,
    required this.textColor,
    required this.label,
  });

  final Color color;
  final Color textColor;
  final String label;

  @override
  Widget build(BuildContext context) => Container(
    width: 220,
    height: 120,
    color: color,
    alignment: Alignment.center,
    child: Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(CupertinoIcons.photo, color: textColor),
        const SizedBox(height: 8),
        Text(label, style: TextStyle(color: textColor, fontSize: 12)),
      ],
    ),
  );
}

class _DeliveryLabel extends StatelessWidget {
  const _DeliveryLabel({
    required this.status,
    this.deliveredCount,
    this.readCount,
  });
  final MessageStatus status;
  final int? deliveredCount;
  final int? readCount;

  @override
  Widget build(BuildContext context) {
    final label = switch (status) {
      MessageStatus.sending => '发送中',
      MessageStatus.sent => '已发送',
      MessageStatus.delivered => '已送达',
      MessageStatus.read => '已读',
      MessageStatus.failed => '发送失败',
      MessageStatus.recalled => '已撤回',
      MessageStatus.expired => '已过期',
    };
    final receiptLabel = deliveredCount == null
        ? label
        : '已送达 $deliveredCount · 已读 ${readCount ?? 0}';
    return Text(
      receiptLabel,
      key: deliveredCount == null ? null : const Key('group-receipt-summary'),
      style: TextStyle(
        color: status == MessageStatus.read
            ? LinliColors.preview
            : LinliColors.tertiaryLabel,
        fontSize: 10,
      ),
    );
  }
}

class _SelectionBar extends StatelessWidget {
  const _SelectionBar({
    required this.count,
    required this.onCancel,
    required this.onDelete,
    required this.onForward,
  });

  final int count;
  final VoidCallback onCancel;
  final VoidCallback? onDelete;
  final VoidCallback? onForward;

  @override
  Widget build(BuildContext context) => GlassSurface(
    border: Border(
      top: BorderSide(color: Theme.of(context).colorScheme.outline),
    ),
    child: SafeArea(
      top: false,
      child: SizedBox(
        height: 58,
        child: Row(
          children: [
            TextButton(onPressed: onCancel, child: const Text('取消')),
            Expanded(
              child: Text(
                '已选择 $count 条',
                textAlign: TextAlign.center,
                style: Theme.of(context).textTheme.titleSmall,
              ),
            ),
            IconButton(
              tooltip: '转发',
              onPressed: onForward,
              icon: const Icon(CupertinoIcons.arrowshape_turn_up_right),
            ),
            TextButton.icon(
              onPressed: onDelete,
              icon: const Icon(CupertinoIcons.trash, size: 18),
              label: const Text('删除'),
              style: TextButton.styleFrom(
                foregroundColor: LinliColors.systemRed,
              ),
            ),
          ],
        ),
      ),
    ),
  );
}

class _ScheduledMessagesSheet extends StatelessWidget {
  const _ScheduledMessagesSheet({
    required this.controller,
    required this.conversationId,
  });

  final AppController controller;
  final String conversationId;

  @override
  Widget build(BuildContext context) => FractionallySizedBox(
    heightFactor: .78,
    child: AnimatedBuilder(
      animation: controller,
      builder: (context, _) {
        final loading = controller.scheduledMessageLoading.contains(
          conversationId,
        );
        final error = controller.scheduledMessageErrors[conversationId];
        final items = controller.scheduledMessagesFor(conversationId);
        return Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 0, 12, 12),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      '定时消息',
                      style: Theme.of(context).textTheme.titleLarge,
                    ),
                  ),
                  IconButton(
                    key: const Key('refresh-scheduled-messages'),
                    tooltip: '刷新',
                    onPressed: loading
                        ? null
                        : () => controller.loadScheduledMessages(
                            conversationId,
                            force: true,
                          ),
                    icon: const Icon(CupertinoIcons.refresh),
                  ),
                ],
              ),
            ),
            Expanded(
              child: loading && items.isEmpty
                  ? const Center(child: CupertinoActivityIndicator())
                  : error != null && items.isEmpty
                  ? StatePanel(
                      icon: CupertinoIcons.wifi_exclamationmark,
                      title: '定时消息加载失败',
                      body: error,
                      actionLabel: '重试',
                      onAction: () => controller.loadScheduledMessages(
                        conversationId,
                        force: true,
                      ),
                    )
                  : items.isEmpty
                  ? const StatePanel(
                      icon: CupertinoIcons.clock,
                      title: '没有待发送消息',
                      body: '在聊天输入框中输入内容，长按发送键即可选择发送时间。',
                    )
                  : ListView.separated(
                      key: const Key('scheduled-message-list'),
                      padding: const EdgeInsets.fromLTRB(12, 0, 12, 28),
                      itemCount: items.length,
                      separatorBuilder: (_, _) => const SizedBox(height: 8),
                      itemBuilder: (context, index) {
                        final item = items[index];
                        return Container(
                          key: Key('scheduled-message-${item.id}'),
                          padding: const EdgeInsets.fromLTRB(16, 12, 8, 12),
                          decoration: BoxDecoration(
                            color: Theme.of(
                              context,
                            ).colorScheme.surfaceContainer,
                            borderRadius: BorderRadius.circular(16),
                            border: Border.all(
                              color: Theme.of(context).colorScheme.outline,
                            ),
                          ),
                          child: Row(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Text(
                                      item.text,
                                      maxLines: 3,
                                      overflow: TextOverflow.ellipsis,
                                    ),
                                    const SizedBox(height: 6),
                                    Text(
                                      item.canRetry
                                          ? item.errorMessage ?? '发送失败，可重新安排'
                                          : _scheduledTime(item.scheduledAt),
                                      style: Theme.of(context)
                                          .textTheme
                                          .labelSmall
                                          ?.copyWith(
                                            color: item.canRetry
                                                ? LinliColors.systemRed
                                                : LinliColors.preview,
                                          ),
                                    ),
                                  ],
                                ),
                              ),
                              if (item.canRetry)
                                IconButton(
                                  key: Key('retry-scheduled-${item.id}'),
                                  tooltip: '一分钟后重试',
                                  onPressed: () =>
                                      controller.retryScheduledMessage(item),
                                  icon: const Icon(CupertinoIcons.refresh),
                                ),
                              IconButton(
                                key: Key('edit-scheduled-${item.id}'),
                                tooltip: '修改定时消息',
                                onPressed: () =>
                                    _editScheduledMessage(context, item),
                                icon: const Icon(CupertinoIcons.pencil),
                              ),
                              IconButton(
                                key: Key('cancel-scheduled-${item.id}'),
                                tooltip: '取消定时消息',
                                onPressed: () =>
                                    controller.cancelScheduledMessage(item),
                                icon: const Icon(
                                  CupertinoIcons.trash,
                                  color: LinliColors.systemRed,
                                ),
                              ),
                            ],
                          ),
                        );
                      },
                    ),
            ),
          ],
        );
      },
    ),
  );

  Future<void> _editScheduledMessage(
    BuildContext context,
    ScheduledMessage message,
  ) async {
    final messenger = ScaffoldMessenger.maybeOf(context);
    final minimumDate = DateTime.now().add(const Duration(minutes: 1));
    final initialDate = message.scheduledAt.isAfter(minimumDate)
        ? message.scheduledAt
        : DateTime.now().add(const Duration(minutes: 10));
    final result =
        await showModalBottomSheet<({String text, DateTime scheduledAt})>(
          context: context,
          useSafeArea: true,
          isScrollControlled: true,
          builder: (_) => _ScheduledMessageEditor(
            message: message,
            minimumDate: minimumDate,
            initialDate: initialDate,
          ),
        );
    if (result == null) return;
    final success = await controller.updateScheduledMessage(
      message,
      text: result.text,
      scheduledAt: result.scheduledAt,
    );
    if (!success) {
      messenger?.showSnackBar(
        SnackBar(
          content: Text(
            controller.scheduledMessageErrors[conversationId] ?? '定时消息修改失败',
          ),
        ),
      );
      return;
    }
    messenger?.showSnackBar(const SnackBar(content: Text('定时消息已更新')));
  }

  static String _scheduledTime(DateTime date) =>
      '${date.month}月${date.day}日 ${date.hour.toString().padLeft(2, '0')}:${date.minute.toString().padLeft(2, '0')} 发送';
}

class _ScheduledMessageEditor extends StatefulWidget {
  const _ScheduledMessageEditor({
    required this.message,
    required this.minimumDate,
    required this.initialDate,
  });

  final ScheduledMessage message;
  final DateTime minimumDate;
  final DateTime initialDate;

  @override
  State<_ScheduledMessageEditor> createState() =>
      _ScheduledMessageEditorState();
}

class _ScheduledMessageEditorState extends State<_ScheduledMessageEditor> {
  late final TextEditingController textController;
  late DateTime selected;

  @override
  void initState() {
    super.initState();
    textController = TextEditingController(text: widget.message.text);
    selected = widget.initialDate;
  }

  @override
  void dispose() {
    textController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Padding(
    padding: EdgeInsets.only(bottom: MediaQuery.viewInsetsOf(context).bottom),
    child: LayoutBuilder(
      builder: (context, constraints) => SizedBox(
        height: constraints.maxHeight > 460 ? 460 : constraints.maxHeight,
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 16, 12, 4),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      '修改定时消息',
                      style: Theme.of(context).textTheme.titleLarge,
                    ),
                  ),
                  TextButton(
                    key: Key('confirm-edit-scheduled-${widget.message.id}'),
                    onPressed: () => Navigator.pop(context, (
                      text: textController.text,
                      scheduledAt: selected,
                    )),
                    child: const Text('保存'),
                  ),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 20, 4),
              child: TextField(
                key: Key('edit-scheduled-text-${widget.message.id}'),
                controller: textController,
                autofocus: true,
                maxLength: 8000,
                maxLines: 3,
                decoration: const InputDecoration(
                  labelText: '消息内容',
                  hintText: '输入定时发送的内容',
                ),
              ),
            ),
            Expanded(
              child: CupertinoDatePicker(
                key: Key('edit-scheduled-time-${widget.message.id}'),
                mode: CupertinoDatePickerMode.dateAndTime,
                minimumDate: widget.minimumDate,
                initialDateTime: selected,
                use24hFormat: true,
                onDateTimeChanged: (value) => selected = value,
              ),
            ),
          ],
        ),
      ),
    ),
  );
}

String? businessChannelSendRestriction(BusinessChannelSummary channel) {
  if (channel.disband) return '频道已解散，历史消息仅供查看。';
  if (channel.ban) return '频道已被封禁，当前不能发送消息。';
  if (!channel.subscribed) return '加入频道后才能发送消息。';
  if (channel.sendBan) return '频道当前为全员禁言，你仍可浏览和接收更新。';
  const operatorRoles = {'owner', 'admin', 'moderator'};
  if (channel.postingPolicy == 'operators' &&
      !operatorRoles.contains(channel.role)) {
    return channel.channelType == 6
        ? '该资讯频道仅管理员可发布，你仍可浏览和接收更新。'
        : '该频道仅管理员可发布，你仍可浏览和接收更新。';
  }
  return null;
}

bool usesManagedBusinessChannelSendPolicy(Conversation conversation) =>
    const {4, 5, 6, 9}.contains(conversation.channelType);

class ChannelSendRestrictionBar extends StatelessWidget {
  const ChannelSendRestrictionBar({
    super.key,
    required this.message,
    this.loading = false,
    this.onRetry,
  });

  final String message;
  final bool loading;
  final Future<void> Function()? onRetry;

  @override
  Widget build(BuildContext context) => SafeArea(
    top: false,
    child: Container(
      key: const Key('channel-send-restriction'),
      width: double.infinity,
      constraints: const BoxConstraints(minHeight: 62),
      padding: const EdgeInsets.fromLTRB(16, 10, 12, 10),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainer,
        border: Border(
          top: BorderSide(color: Theme.of(context).colorScheme.outline),
        ),
      ),
      child: Row(
        children: [
          if (loading)
            const SizedBox.square(
              dimension: 20,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          else
            Icon(
              CupertinoIcons.lock_fill,
              size: 20,
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(message, style: Theme.of(context).textTheme.bodyMedium),
          ),
          if (onRetry != null)
            TextButton(onPressed: onRetry, child: const Text('重试')),
        ],
      ),
    ),
  );
}

class RobotCommandBar extends StatelessWidget {
  const RobotCommandBar({
    super.key,
    required this.profiles,
    required this.expanded,
    required this.onToggle,
    required this.onSelected,
  });

  final List<RobotProfile> profiles;
  final bool expanded;
  final VoidCallback onToggle;
  final ValueChanged<RobotMenu> onSelected;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    final placeholder = profiles.length == 1
        ? profiles.first.placeholder.trim()
        : '';
    return Material(
      key: const Key('robot-command-bar'),
      color: dark ? LinliColors.darkSurface : LinliColors.surface,
      child: Container(
        decoration: BoxDecoration(
          border: Border(
            top: BorderSide(color: Theme.of(context).colorScheme.outline),
          ),
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            SizedBox(
              height: 48,
              child: Row(
                children: [
                  const SizedBox(width: 8),
                  TextButton.icon(
                    key: const Key('robot-menu-toggle'),
                    onPressed: onToggle,
                    icon: Icon(
                      expanded
                          ? CupertinoIcons.chevron_down
                          : CupertinoIcons.list_bullet,
                      size: 18,
                    ),
                    label: const Text('菜单'),
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      placeholder.isEmpty
                          ? '${profiles.length} 个机器人可用'
                          : placeholder,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: Theme.of(context).colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ),
                  const SizedBox(width: 14),
                ],
              ),
            ),
            if (expanded)
              ConstrainedBox(
                constraints: const BoxConstraints(maxHeight: 220),
                child: SingleChildScrollView(
                  padding: const EdgeInsets.fromLTRB(16, 4, 16, 14),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      for (final profile in profiles) ...[
                        if (profiles.length > 1)
                          Padding(
                            padding: const EdgeInsets.only(top: 8, bottom: 8),
                            child: Text(
                              profile.name.trim().isEmpty
                                  ? profile.username
                                  : profile.name,
                              style: Theme.of(context).textTheme.labelMedium,
                            ),
                          ),
                        Wrap(
                          spacing: 8,
                          runSpacing: 8,
                          children: [
                            for (final menu in profile.menus)
                              OutlinedButton(
                                key: ValueKey(
                                  'robot-command-${profile.id}-${menu.command}',
                                ),
                                onPressed: () => onSelected(menu),
                                style: OutlinedButton.styleFrom(
                                  minimumSize: const Size(0, 40),
                                  padding: const EdgeInsets.symmetric(
                                    horizontal: 14,
                                  ),
                                ),
                                child: Text(menu.label),
                              ),
                          ],
                        ),
                      ],
                    ],
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class ChatComposer extends StatefulWidget {
  const ChatComposer({
    super.key,
    required this.controller,
    required this.onSend,
    this.onSendOptions,
    required this.onToggleAttachments,
    required this.onToggleEmoji,
    required this.onAttachment,
    this.allowLiveInteraction = false,
    required this.onVoiceReady,
    this.voiceController,
    required this.onCancelReply,
    this.onMention,
    this.onTypingChanged,
    this.replyingTo,
    this.replyingToName,
    this.showAttachments = false,
    this.showEmoji = false,
  });

  final TextEditingController controller;
  final VoidCallback onSend;
  final VoidCallback? onSendOptions;
  final VoidCallback onToggleAttachments;
  final VoidCallback onToggleEmoji;
  final ValueChanged<String> onAttachment;
  final bool allowLiveInteraction;
  final FutureOr<void> Function(MediaUpload) onVoiceReady;
  final VoiceComposerController? voiceController;
  final VoidCallback onCancelReply;
  final VoidCallback? onMention;
  final ValueChanged<bool>? onTypingChanged;
  final ChatMessage? replyingTo;
  final String? replyingToName;
  final bool showAttachments;
  final bool showEmoji;

  @override
  State<ChatComposer> createState() => _ChatComposerState();
}

class _ChatComposerState extends State<ChatComposer> {
  final FocusNode _inputFocusNode = FocusNode();
  late VoiceComposerController _voice;
  late bool _ownsVoice;
  bool _voiceMode = false;

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_onTextChanged);
    _inputFocusNode.addListener(_onInputFocusChanged);
    HardwareKeyboard.instance.addHandler(_handleHardwareKey);
    _bindVoice();
  }

  void _bindVoice() {
    _ownsVoice = widget.voiceController == null;
    _voice = widget.voiceController ?? VoiceComposerController();
    _voice.addListener(_onVoiceChanged);
  }

  void _onVoiceChanged() {
    if (!mounted) return;
    final notice = _voice.takeNotice();
    setState(() {});
    if (notice == null) return;
    if (notice == VoiceComposerNotice.permissionDenied) {
      unawaited(
        showCupertinoDialog<void>(
          context: context,
          builder: (context) => CupertinoAlertDialog(
            title: const Text('无法使用麦克风'),
            content: const Text('请在系统设置中允许青蛙呱呱访问麦克风后再试。'),
            actions: [
              CupertinoDialogAction(
                onPressed: () => Navigator.pop(context),
                child: const Text('知道了'),
              ),
            ],
          ),
        ),
      );
      return;
    }
    final message = switch (notice) {
      VoiceComposerNotice.startFailed => '录音启动失败，请检查麦克风后重试',
      VoiceComposerNotice.saveFailed => '录音保存失败，请重试',
      VoiceComposerNotice.tooShort => '说话时间太短',
      VoiceComposerNotice.previewFailed => '语音试听失败，请重试',
      VoiceComposerNotice.sendFailed => '语音准备失败，录音已保留，请重试',
      VoiceComposerNotice.permissionDenied => '',
    };
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  void didUpdateWidget(covariant ChatComposer oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.controller != widget.controller) {
      oldWidget.controller.removeListener(_onTextChanged);
      widget.controller.addListener(_onTextChanged);
    }
    if (oldWidget.voiceController != widget.voiceController) {
      _voice.removeListener(_onVoiceChanged);
      if (_ownsVoice) _voice.dispose();
      _bindVoice();
    }
  }

  @override
  void dispose() {
    widget.onTypingChanged?.call(false);
    widget.controller.removeListener(_onTextChanged);
    _inputFocusNode.removeListener(_onInputFocusChanged);
    HardwareKeyboard.instance.removeHandler(_handleHardwareKey);
    _voice.removeListener(_onVoiceChanged);
    if (_ownsVoice) _voice.dispose();
    _inputFocusNode.dispose();
    super.dispose();
  }

  bool _handleHardwareKey(KeyEvent event) {
    if (event is! KeyDownEvent ||
        !_inputFocusNode.hasFocus ||
        event.logicalKey != LogicalKeyboardKey.enter ||
        HardwareKeyboard.instance.isShiftPressed ||
        (widget.controller.value.composing.isValid &&
            !widget.controller.value.composing.isCollapsed)) {
      return false;
    }
    if (_voiceMode || widget.controller.text.trim().isEmpty) return false;
    widget.onSend();
    return true;
  }

  void _onTextChanged() {
    if (!mounted) return;
    setState(() {});
    _notifyTyping();
  }

  void _onInputFocusChanged() => _notifyTyping();

  void _notifyTyping() => widget.onTypingChanged?.call(
    _inputFocusNode.hasFocus && widget.controller.text.trim().isNotEmpty,
  );

  void _toggleVoiceMode() {
    if (_voice.recording || _voice.busy) return;
    widget.onTypingChanged?.call(false);
    if (widget.showEmoji) widget.onToggleEmoji();
    if (widget.showAttachments) widget.onToggleAttachments();
    _inputFocusNode.unfocus();
    setState(() => _voiceMode = !_voiceMode);
  }

  void _updateRecording(LongPressMoveUpdateDetails details) {
    if (_voice.updateDrag(details.offsetFromOrigin.dy)) {
      HapticFeedback.selectionClick();
    }
  }

  @override
  Widget build(BuildContext context) {
    final canSend = widget.controller.text.trim().isNotEmpty;
    final canSendText = !_voiceMode && canSend;
    final composer = GlassSurface(
      border: Border(
        top: BorderSide(color: Theme.of(context).colorScheme.outline),
      ),
      child: SafeArea(
        top: false,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (widget.replyingTo != null)
              Container(
                width: double.infinity,
                padding: const EdgeInsets.fromLTRB(14, 8, 6, 6),
                color: Theme.of(context).colorScheme.surfaceContainerHigh,
                child: Row(
                  children: [
                    const Icon(
                      CupertinoIcons.reply,
                      size: 16,
                      color: LinliColors.preview,
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        '回复 ${widget.replyingToName ?? widget.replyingTo!.senderName}：${widget.replyingTo!.text}',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.bodySmall,
                      ),
                    ),
                    IconButton(
                      tooltip: '取消回复',
                      onPressed: widget.onCancelReply,
                      icon: const Icon(CupertinoIcons.xmark, size: 17),
                    ),
                  ],
                ),
              ),
            Padding(
              padding: const EdgeInsets.fromLTRB(4, 6, 6, 6),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  IconButton(
                    key: const Key('voice-mode-button'),
                    tooltip: _voiceMode ? '切换到键盘' : '切换到语音',
                    onPressed: _toggleVoiceMode,
                    icon: Icon(
                      _voiceMode
                          ? CupertinoIcons.keyboard
                          : CupertinoIcons.mic_circle,
                    ),
                  ),
                  Expanded(
                    child: _voiceMode
                        ? VoiceSizeTransition(
                            alignment: Alignment.bottomCenter,
                            child: AnimatedSwitcher(
                              duration: nexaMotionDuration(context),
                              child: _voice.hasDraft
                                  ? VoiceDraftControl(
                                      key: const ValueKey(
                                        'voice-draft-control',
                                      ),
                                      seconds: _voice.draftSeconds,
                                      playing: _voice.playing,
                                      busy: _voice.busy,
                                      previewBusy: _voice.previewBusy,
                                      onPreview: () =>
                                          unawaited(_voice.togglePreview()),
                                      onDiscard: () =>
                                          unawaited(_voice.discard()),
                                      onSend: () => unawaited(
                                        _voice.send(widget.onVoiceReady),
                                      ),
                                    )
                                  : VoiceRecordingButton(
                                      key: const ValueKey(
                                        'voice-recording-control',
                                      ),
                                      phase: _voice.phase,
                                      onStart: (_) =>
                                          unawaited(_voice.beginPress()),
                                      onMove: _updateRecording,
                                      onEnd: (_) =>
                                          unawaited(_voice.endPress()),
                                      onCancel: () => unawaited(
                                        _voice.endPress(forceCancel: true),
                                      ),
                                    ),
                            ),
                          )
                        : TextField(
                            key: const Key('message-input'),
                            focusNode: _inputFocusNode,
                            controller: widget.controller,
                            minLines: 1,
                            maxLines: 5,
                            textInputAction: TextInputAction.send,
                            textCapitalization: TextCapitalization.sentences,
                            onSubmitted: canSend
                                ? (_) => widget.onSend()
                                : null,
                            style: Theme.of(context).textTheme.bodyLarge,
                            decoration: InputDecoration(
                              hintText: '输入消息',
                              fillColor: Theme.of(
                                context,
                              ).colorScheme.surfaceContainerHigh,
                              contentPadding: const EdgeInsets.symmetric(
                                horizontal: 12,
                                vertical: 9,
                              ),
                            ),
                          ),
                  ),
                  if (widget.onMention != null)
                    IconButton(
                      key: const Key('mention-member-button'),
                      tooltip: '提醒群成员',
                      onPressed: _voiceMode ? null : widget.onMention,
                      icon: const Text(
                        '@',
                        style: TextStyle(
                          fontSize: 20,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                  IconButton(
                    tooltip: '表情',
                    onPressed: _voiceMode ? null : widget.onToggleEmoji,
                    icon: Icon(
                      widget.showEmoji
                          ? CupertinoIcons.keyboard
                          : CupertinoIcons.smiley,
                    ),
                  ),
                  Semantics(
                    button: true,
                    label: canSendText ? '发送，长按选择定时或限时' : '更多附件',
                    child: InkResponse(
                      key: const Key('send-button'),
                      radius: 24,
                      onTap: _voice.recording || _voice.busy
                          ? null
                          : canSendText
                          ? widget.onSend
                          : widget.onToggleAttachments,
                      onLongPress: canSendText ? widget.onSendOptions : null,
                      child: SizedBox.square(
                        dimension: 48,
                        child: Icon(
                          canSendText
                              ? CupertinoIcons.arrow_up_circle_fill
                              : widget.showAttachments
                              ? CupertinoIcons.xmark_circle_fill
                              : CupertinoIcons.add_circled,
                          color: canSendText
                              ? LinliColors.navy
                              : Theme.of(context).colorScheme.onSurface,
                          size: 28,
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
            AnimatedSize(
              duration: nexaMotionDuration(context),
              child: widget.showAttachments
                  ? _AttachmentPanel(
                      onSelected: widget.onAttachment,
                      allowLiveInteraction: widget.allowLiveInteraction,
                    )
                  : widget.showEmoji
                  ? _EmojiPanel(
                      controller: widget.controller,
                      onSend: widget.onSend,
                    )
                  : const SizedBox.shrink(),
            ),
          ],
        ),
      ),
    );
    return Stack(
      clipBehavior: Clip.none,
      children: [
        composer,
        Positioned(
          left: 12,
          right: 12,
          top: -12,
          child: FractionalTranslation(
            translation: const Offset(0, -1),
            child: Align(
              alignment: Alignment.bottomCenter,
              child: VoiceRecordingOverlay(
                phase: _voice.phase,
                seconds: _voice.seconds,
                samples: _voice.samples,
              ),
            ),
          ),
        ),
      ],
    );
  }
}

class _ContactPickerSheet extends StatefulWidget {
  const _ContactPickerSheet({required this.controller});

  final AppController controller;

  @override
  State<_ContactPickerSheet> createState() => _ContactPickerSheetState();
}

class _ContactPickerSheetState extends State<_ContactPickerSheet> {
  final searchController = TextEditingController();
  String query = '';

  @override
  void dispose() {
    searchController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) => _buildContent(context),
  );

  Widget _buildContent(BuildContext context) {
    final normalized = query.trim().toLowerCase();
    final contacts = widget.controller.contacts.where((contact) {
      if (normalized.isEmpty) return true;
      return contact.name.toLowerCase().contains(normalized) ||
          contact.handle.toLowerCase().contains(normalized) ||
          contact.remark.toLowerCase().contains(normalized);
    }).toList();
    return FractionallySizedBox(
      heightFactor: .78,
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(18, 0, 18, 12),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    '发送联系人名片',
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                ),
                Text('${contacts.length} 位'),
              ],
            ),
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: CupertinoSearchTextField(
              key: const Key('contact-card-search'),
              controller: searchController,
              placeholder: '搜索联系人或呱呱号',
              onChanged: (value) => setState(() => query = value),
            ),
          ),
          const SizedBox(height: 8),
          Expanded(
            child: contacts.isEmpty
                ? const StatePanel(
                    icon: CupertinoIcons.person_2,
                    title: '没有可发送的联系人',
                    body: '添加好友后，可以在这里发送联系人名片。',
                  )
                : ListView.separated(
                    keyboardDismissBehavior:
                        ScrollViewKeyboardDismissBehavior.onDrag,
                    padding: const EdgeInsets.fromLTRB(12, 4, 12, 24),
                    itemCount: contacts.length,
                    separatorBuilder: (_, _) => const Divider(indent: 62),
                    itemBuilder: (context, index) {
                      final contact = contacts[index];
                      final name = contact.displayName;
                      return ListTile(
                        key: Key('contact-card-option-${contact.id}'),
                        minTileHeight: 64,
                        leading: PersonAvatar(
                          name: name,
                          size: 44,
                          avatarUrl: contact.avatarUrl,
                        ),
                        title: Text(name),
                        subtitle: Text(publicUserHandleLabel(contact.handle)),
                        trailing: const Icon(
                          CupertinoIcons.chevron_forward,
                          size: 17,
                        ),
                        onTap: () => Navigator.pop(context, contact),
                      );
                    },
                  ),
          ),
        ],
      ),
    );
  }
}

class _AttachmentPanel extends StatelessWidget {
  const _AttachmentPanel({
    required this.onSelected,
    this.allowLiveInteraction = false,
  });
  final ValueChanged<String> onSelected;
  final bool allowLiveInteraction;

  @override
  Widget build(BuildContext context) {
    final items = [
      ('相册', CupertinoIcons.photo_on_rectangle),
      ('拍摄', CupertinoIcons.camera),
      ('视频', CupertinoIcons.video_camera),
      ('文件', CupertinoIcons.doc),
      ('位置', CupertinoIcons.location),
      ('名片', CupertinoIcons.person_crop_rectangle),
      ('表情', CupertinoIcons.smiley),
      ('朋友圈', CupertinoIcons.person_2),
      if (allowLiveInteraction) ('直播互动', CupertinoIcons.heart_circle),
    ];
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.fromLTRB(12, 12, 12, 18),
      color: Theme.of(context).colorScheme.surfaceContainerHigh,
      child: Wrap(
        alignment: WrapAlignment.start,
        spacing: 18,
        runSpacing: 16,
        children: [
          for (final item in items)
            SizedBox(
              width: 64,
              child: CupertinoButton(
                minimumSize: const Size(52, 58),
                padding: EdgeInsets.zero,
                onPressed: () => onSelected(item.$1),
                child: Column(
                  children: [
                    Stack(
                      clipBehavior: Clip.none,
                      children: [
                        Container(
                          width: 48,
                          height: 48,
                          decoration: BoxDecoration(
                            color: Theme.of(
                              context,
                            ).colorScheme.surfaceContainer,
                            borderRadius: BorderRadius.circular(14),
                          ),
                          child: Icon(item.$2, color: LinliColors.navy),
                        ),
                      ],
                    ),
                    const SizedBox(height: 5),
                    Text(
                      item.$1,
                      style: Theme.of(context).textTheme.labelSmall,
                    ),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class _EmojiPanel extends StatefulWidget {
  const _EmojiPanel({required this.controller, required this.onSend});
  final TextEditingController controller;
  final VoidCallback onSend;

  @override
  State<_EmojiPanel> createState() => _EmojiPanelState();
}

class _EmojiPanelState extends State<_EmojiPanel> {
  static const _categoryIcons = [
    CupertinoIcons.clock,
    CupertinoIcons.smiley,
    CupertinoIcons.hand_raised,
    CupertinoIcons.heart,
    CupertinoIcons.leaf_arrow_circlepath,
    CupertinoIcons.cart,
    CupertinoIcons.sportscourt,
    CupertinoIcons.airplane,
    CupertinoIcons.cube_box,
  ];

  final _gridScrollController = ScrollController(keepScrollOffset: false);
  int _category = 0;
  List<String> _recent = List.of(defaultRecentChatEmojis);

  String _categoryLabel(int index) =>
      index == 0 ? '最近' : chatEmojiCategories[index - 1].label;

  @override
  void initState() {
    super.initState();
    _loadRecent();
  }

  @override
  void dispose() {
    _gridScrollController.dispose();
    super.dispose();
  }

  void _selectCategory(int index) {
    if (_category == index) return;
    if (_gridScrollController.hasClients) _gridScrollController.jumpTo(0);
    setState(() => _category = index);
  }

  Future<void> _loadRecent() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final saved = prefs.getStringList(chatRecentEmojisKey);
      if (!mounted || saved == null || saved.isEmpty) return;
      setState(() {
        _recent = saved
            .where((emoji) => emoji.trim().isNotEmpty)
            .toSet()
            .take(chatRecentEmojisLimit)
            .toList();
      });
    } catch (_) {
      // Emoji input remains fully usable when local preferences are unavailable.
    }
  }

  Future<void> _remember(String emoji) async {
    setState(() {
      _recent.remove(emoji);
      _recent.insert(0, emoji);
      if (_recent.length > chatRecentEmojisLimit) {
        _recent.removeRange(chatRecentEmojisLimit, _recent.length);
      }
    });
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setStringList(chatRecentEmojisKey, _recent);
    } catch (_) {
      // Recent history is a convenience; never block input if persistence fails.
    }
  }

  void _insert(String emoji) {
    final value = widget.controller.value;
    final selection = value.selection.isValid
        ? value.selection
        : TextSelection.collapsed(offset: value.text.length);
    final start = selection.start.clamp(0, value.text.length);
    final end = selection.end.clamp(0, value.text.length);
    widget.controller.value = value.copyWith(
      text: value.text.replaceRange(start, end, emoji),
      selection: TextSelection.collapsed(offset: start + emoji.length),
      composing: TextRange.empty,
    );
    unawaited(_remember(emoji));
  }

  void _backspace() {
    final value = widget.controller.value;
    if (value.text.isEmpty) return;
    final selection = value.selection.isValid
        ? value.selection
        : TextSelection.collapsed(offset: value.text.length);
    final start = selection.start.clamp(0, value.text.length);
    final end = selection.end.clamp(0, value.text.length);
    if (start != end) {
      widget.controller.value = value.copyWith(
        text: value.text.replaceRange(start, end, ''),
        selection: TextSelection.collapsed(offset: start),
        composing: TextRange.empty,
      );
      return;
    }
    if (start == 0) return;
    final before = value.text.substring(0, start);
    final lastLength = before.characters.last.length;
    widget.controller.value = value.copyWith(
      text: value.text.replaceRange(start - lastLength, start, ''),
      selection: TextSelection.collapsed(offset: start - lastLength),
      composing: TextRange.empty,
    );
  }

  @override
  Widget build(BuildContext context) {
    final emojis = _category == 0
        ? _recent
        : chatEmojiCategories[_category - 1].emojis;
    final cellExtent = math.max(
      44.0,
      MediaQuery.textScalerOf(context).scale(25) + 16,
    );
    return Container(
      key: const Key('chat-emoji-panel'),
      width: double.infinity,
      height: 298,
      color: Theme.of(context).colorScheme.surfaceContainerHigh,
      child: Column(
        children: [
          SizedBox(
            height: 48,
            child: ListView.builder(
              key: const Key('emoji-categories'),
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 8),
              itemCount: chatEmojiCategories.length + 1,
              itemBuilder: (context, index) => IconButton(
                key: Key('emoji-category-$index'),
                tooltip: _categoryLabel(index),
                isSelected: _category == index,
                onPressed: () => _selectCategory(index),
                icon: Icon(
                  _categoryIcons[index],
                  color: _category == index
                      ? Theme.of(context).colorScheme.secondary
                      : LinliColors.tertiaryLabel,
                ),
              ),
            ),
          ),
          const Divider(height: 1),
          Expanded(
            child: LayoutBuilder(
              builder: (context, constraints) => GridView.builder(
                key: const Key('emoji-grid'),
                controller: _gridScrollController,
                padding: const EdgeInsets.fromLTRB(10, 8, 10, 4),
                gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
                  crossAxisCount: math.max(
                    1,
                    ((constraints.maxWidth - 20) / cellExtent).floor(),
                  ),
                  mainAxisExtent: cellExtent,
                ),
                itemCount: emojis.length,
                itemBuilder: (context, index) => CupertinoButton(
                  key: ValueKey('emoji-item-${emojis[index]}'),
                  minimumSize: const Size(44, 44),
                  padding: EdgeInsets.zero,
                  onPressed: () => _insert(emojis[index]),
                  child: Text(
                    emojis[index],
                    style: const TextStyle(fontSize: 25),
                  ),
                ),
              ),
            ),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(10, 6, 10, 10),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                Expanded(
                  child: Text(
                    _categoryLabel(_category),
                    key: const Key('emoji-category-label'),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ),
                IconButton.filledTonal(
                  key: const Key('emoji-backspace'),
                  tooltip: '退格',
                  onPressed: _backspace,
                  style: IconButton.styleFrom(
                    backgroundColor: Theme.of(context).colorScheme.surface,
                    foregroundColor: Theme.of(context).colorScheme.secondary,
                  ),
                  icon: const Icon(CupertinoIcons.delete_left),
                ),
                const SizedBox(width: 8),
                FilledButton(
                  key: const Key('emoji-send'),
                  onPressed: widget.controller.text.trim().isEmpty
                      ? null
                      : widget.onSend,
                  child: const Text('发送'),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _MentionPickerSheet extends StatefulWidget {
  const _MentionPickerSheet({
    required this.controller,
    required this.members,
    required this.canMentionEveryone,
    this.currentUserId,
  });

  final AppController controller;
  final List<AppUser> members;
  final String? currentUserId;
  final bool canMentionEveryone;

  @override
  State<_MentionPickerSheet> createState() => _MentionPickerSheetState();
}

class _MentionPickerSheetState extends State<_MentionPickerSheet> {
  String query = '';

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) => _buildContent(context),
  );

  Widget _buildContent(BuildContext context) {
    final normalized = query.trim().toLowerCase();
    final members = widget.members
        .where((member) => member.id != widget.currentUserId)
        .where(
          (member) =>
              normalized.isEmpty ||
              member.name.toLowerCase().contains(normalized) ||
              widget.controller
                  .displayNameFor(member)
                  .toLowerCase()
                  .contains(normalized) ||
              member.handle.toLowerCase().contains(normalized),
        )
        .toList();
    return FractionallySizedBox(
      heightFactor: .72,
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    '提醒群成员',
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                ),
                Text('${members.length} 位成员'),
              ],
            ),
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: CupertinoSearchTextField(
              key: const Key('mention-member-search'),
              placeholder: '搜索群成员',
              onChanged: (value) => setState(() => query = value),
            ),
          ),
          const SizedBox(height: 8),
          Expanded(
            child: ListView(
              keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
              children: [
                if (normalized.isEmpty && widget.canMentionEveryone)
                  ListTile(
                    key: const Key('mention-everyone'),
                    minTileHeight: 60,
                    leading: Container(
                      width: 44,
                      height: 44,
                      alignment: Alignment.center,
                      decoration: BoxDecoration(
                        color: LinliColors.brandGreen.withValues(alpha: .18),
                        shape: BoxShape.circle,
                      ),
                      child: const Icon(
                        CupertinoIcons.person_3_fill,
                        color: LinliColors.navy,
                      ),
                    ),
                    title: const Text('所有人'),
                    subtitle: const Text('提醒群内全部成员，服务端会校验权限'),
                    onTap: () => Navigator.pop(
                      context,
                      const MessageMention(userId: 'all', name: '所有人'),
                    ),
                  ),
                for (final member in members)
                  ListTile(
                    key: Key('mention-member-${member.id}'),
                    minTileHeight: 60,
                    leading: PersonAvatar(
                      name: widget.controller.displayNameFor(member),
                      size: 44,
                      avatarUrl: member.avatarUrl,
                    ),
                    title: Text(widget.controller.displayNameFor(member)),
                    subtitle: Text(publicUserHandleLabel(member.handle)),
                    onTap: () => Navigator.pop(
                      context,
                      MessageMention(userId: member.id, name: member.name),
                    ),
                  ),
                if (members.isEmpty && normalized.isNotEmpty)
                  const Padding(
                    padding: EdgeInsets.only(top: 48),
                    child: StatePanel(
                      icon: CupertinoIcons.person_2,
                      title: '没有匹配成员',
                      body: '换个昵称或呱呱号试试。',
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _PinnedMessagesSheet extends StatefulWidget {
  const _PinnedMessagesSheet({
    required this.controller,
    required this.conversationId,
    required this.onSelected,
  });

  final AppController controller;
  final String conversationId;
  final ValueChanged<ChatMessage> onSelected;

  @override
  State<_PinnedMessagesSheet> createState() => _PinnedMessagesSheetState();
}

class _PinnedMessagesSheetState extends State<_PinnedMessagesSheet> {
  late Future<List<ChatMessage>> future = _load();
  late int _historyRevision = widget.controller.groupPresentationRevision;
  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_historyChanged);
  }

  void _historyChanged() {
    if (!mounted) return;
    if (_historyRevision == widget.controller.groupPresentationRevision) {
      setState(() {});
      return;
    }
    _historyRevision = widget.controller.groupPresentationRevision;
    setState(() => future = _load());
  }

  @override
  void dispose() {
    widget.controller.removeListener(_historyChanged);
    super.dispose();
  }

  Future<List<ChatMessage>> _load() =>
      widget.controller.loadPinnedMessages(widget.conversationId);

  @override
  Widget build(BuildContext context) => FractionallySizedBox(
    heightFactor: .68,
    child: Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
          child: Row(
            children: [
              Expanded(
                child: Text(
                  '置顶消息',
                  style: Theme.of(context).textTheme.titleLarge,
                ),
              ),
              IconButton(
                tooltip: '刷新置顶消息',
                onPressed: () => setState(() => future = _load()),
                icon: const Icon(CupertinoIcons.refresh),
              ),
            ],
          ),
        ),
        Expanded(
          child: FutureBuilder<List<ChatMessage>>(
            future: future,
            builder: (context, snapshot) {
              if (snapshot.connectionState != ConnectionState.done) {
                return const Center(child: CupertinoActivityIndicator());
              }
              if (snapshot.hasError) {
                return StatePanel(
                  icon: CupertinoIcons.exclamationmark_triangle,
                  title: '置顶消息加载失败',
                  body: widget.controller.error ?? '请检查网络后重试。',
                  actionLabel: '重新加载',
                  onAction: () => setState(() => future = _load()),
                );
              }
              final messages = (snapshot.data ?? const <ChatMessage>[])
                  .where(widget.controller.canDisplayMessage)
                  .toList();
              if (messages.isEmpty) {
                return const StatePanel(
                  icon: CupertinoIcons.pin,
                  title: '暂无置顶消息',
                  body: '长按群消息，可以将重要内容置顶。',
                );
              }
              return ListView.separated(
                padding: const EdgeInsets.fromLTRB(12, 0, 12, 24),
                itemCount: messages.length,
                separatorBuilder: (_, _) => const Divider(indent: 16),
                itemBuilder: (context, index) {
                  final message = messages[index];
                  return ListTile(
                    key: Key('pinned-message-${message.id}'),
                    minTileHeight: 64,
                    title: Text(
                      message.text,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                    subtitle: Text(
                      widget.controller.displayNameForId(
                        message.senderId,
                        fallback: message.senderName,
                      ),
                    ),
                    trailing: IconButton(
                      tooltip: '取消置顶',
                      onPressed: () async {
                        final success = await widget.controller
                            .toggleMessagePinned(message);
                        if (!mounted) return;
                        if (success) {
                          setState(() => future = _load());
                        } else {
                          ScaffoldMessenger.of(this.context).showSnackBar(
                            SnackBar(
                              content: Text(
                                widget.controller.error ?? '取消置顶失败',
                              ),
                            ),
                          );
                        }
                      },
                      icon: const Icon(CupertinoIcons.pin_slash),
                    ),
                    onTap: () => widget.onSelected(message),
                  );
                },
              );
            },
          ),
        ),
      ],
    ),
  );
}

class _MessageSearchSheet extends StatefulWidget {
  const _MessageSearchSheet({
    required this.controller,
    required this.conversationId,
    required this.onSelected,
  });
  final AppController controller;
  final String conversationId;
  final ValueChanged<ChatMessage> onSelected;

  @override
  State<_MessageSearchSheet> createState() => _MessageSearchSheetState();
}

class _MessageSearchSheetState extends State<_MessageSearchSheet> {
  late int _historyRevision = widget.controller.groupPresentationRevision;
  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_historyChanged);
  }

  void _historyChanged() {
    if (!mounted) return;
    if (_historyRevision == widget.controller.groupPresentationRevision) {
      setState(() {});
      return;
    }
    _historyRevision = widget.controller.groupPresentationRevision;
    setState(
      () =>
          matches = matches.where(widget.controller.canDisplayMessage).toList(),
    );
    if (query.isNotEmpty) _search(query);
  }

  String query = '';
  bool loading = false;
  String? error;
  List<ChatMessage> matches = const [];
  Timer? debounce;

  @override
  void dispose() {
    widget.controller.removeListener(_historyChanged);
    debounce?.cancel();
    super.dispose();
  }

  void _search(String value) {
    setState(() {
      query = value;
      error = null;
      if (value.trim().isEmpty) matches = const [];
    });
    debounce?.cancel();
    if (value.trim().isEmpty) return;
    debounce = Timer(const Duration(milliseconds: 300), () async {
      if (!mounted) return;
      setState(() => loading = true);
      final result = await widget.controller.searchConversationMessages(
        widget.conversationId,
        value,
      );
      if (!mounted || query != value) return;
      setState(() {
        matches = result;
        loading = false;
        if (widget.controller.error?.contains('搜索') == true) {
          error = widget.controller.error;
        }
      });
    });
  }

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Padding(
        padding: EdgeInsets.only(
          left: 16,
          right: 16,
          bottom: MediaQuery.viewInsetsOf(context).bottom + 16,
        ),
        child: SizedBox(
          height: MediaQuery.sizeOf(context).height * .66,
          child: Column(
            children: [
              TextField(
                key: const Key('conversation-message-search'),
                autofocus: true,
                onChanged: _search,
                decoration: const InputDecoration(
                  prefixIcon: Icon(CupertinoIcons.search),
                  hintText: '查找聊天内容',
                ),
              ),
              const SizedBox(height: 12),
              if (loading) const LinearProgressIndicator(minHeight: 2),
              if (error != null)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 8),
                  child: Text(
                    error!,
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                      color: Theme.of(context).colorScheme.error,
                    ),
                  ),
                ),
              Expanded(
                child: query.isEmpty
                    ? const StatePanel(
                        icon: CupertinoIcons.search,
                        title: '输入关键词',
                        body: '同时查找当前设备和云端已保存的消息。',
                      )
                    : matches.isEmpty
                    ? const StatePanel(
                        icon: CupertinoIcons.search,
                        title: '没有匹配内容',
                        body: '换个关键词试试。',
                      )
                    : ListView.separated(
                        itemCount: matches.length,
                        separatorBuilder: (_, _) => const Divider(),
                        itemBuilder: (_, index) => ListTile(
                          key: Key(
                            'message-search-result-${matches[index].id}',
                          ),
                          minTileHeight: 60,
                          title: Text(
                            matches[index].text,
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                          ),
                          subtitle: Text(
                            '${widget.controller.displayNameForId(matches[index].senderId, fallback: matches[index].senderName)} · ${_searchDate(matches[index].sentAt)}',
                          ),
                          trailing: const Icon(
                            CupertinoIcons.chevron_forward,
                            size: 17,
                          ),
                          onTap: () => widget.onSelected(matches[index]),
                        ),
                      ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  String _searchDate(DateTime value) {
    final local = value.toLocal();
    return '${local.month}月${local.day}日 '
        '${local.hour.toString().padLeft(2, '0')}:'
        '${local.minute.toString().padLeft(2, '0')}';
  }
}
