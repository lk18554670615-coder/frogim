import 'dart:async';
import 'dart:io';

import 'package:audioplayers/audioplayers.dart';
import 'package:file_picker/file_picker.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';
import 'package:flutter/services.dart';
import 'package:geocoding/geocoding.dart';
import 'package:geolocator/geolocator.dart';
import 'package:image_picker/image_picker.dart';
import 'package:record/record.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../calls/call_models.dart';
import '../../core/app_controller.dart';
import '../../core/app_config.dart';
import '../../core/app_theme.dart';
import '../../core/image_send_editor.dart';
import '../../core/local_media_path.dart';
import '../../core/media_opener.dart';
import '../../core/models.dart';
import '../../core/screenshot_detection.dart';
import '../../core/web_drop_paste.dart';
import '../widgets/linli_widgets.dart';
import '../widgets/media_send_widgets.dart';
import 'moments_screen.dart';
import 'people_screens.dart';
import 'relationship_screens.dart';
import 'settings_screens.dart';
import 'sticker_store_screen.dart';

class ChatScreen extends StatefulWidget {
  const ChatScreen({
    super.key,
    required this.controller,
    required this.conversation,
    this.initialMessageId,
    this.showDesktopDetails = false,
    this.onToggleDesktopDetails,
  });

  final AppController controller;
  final Conversation conversation;
  final String? initialMessageId;
  final bool showDesktopDetails;
  final VoidCallback? onToggleDesktopDetails;

  @override
  State<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScreenState extends State<ChatScreen> {
  final textController = TextEditingController();
  final scrollController = ScrollController();
  final initialMessageKey = GlobalKey();
  bool showAttachments = false;
  bool showEmoji = false;
  bool selecting = false;
  final Set<String> selectedMessageIds = {};
  final Map<String, GlobalKey> _messageKeys = {};
  final Map<String, MessageMention> _pendingMentions = {};
  ChatMessage? replyingTo;
  Timer? _draftTimer;
  bool _draftReady = false;
  late final StreamSubscription<DateTime> _screenshotEvents;
  DateTime? _lastScreenshotNotice;

  AppUser? get peer => widget.conversation.members.firstOrNull;
  String? get conversationAvatarUrl =>
      widget.conversation.avatarUrl ??
      (widget.conversation.kind == ConversationKind.group
          ? 'assets/brand/linli-im-icon.png'
          : peer?.avatarUrl);

  @override
  void initState() {
    super.initState();
    HardwareKeyboard.instance.addHandler(_handleChatHardwareKey);
    _screenshotEvents = ScreenshotDetection.instance.events.listen(
      _handleScreenshot,
    );
    unawaited(_restoreDraft());
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      widget.controller.setActiveConversation(widget.conversation.id);
      unawaited(ScreenshotDetection.instance.start());
      widget.controller
          .loadMessages(widget.conversation.id)
          .then((_) => _scrollToInitialMessage());
    });
  }

  @override
  void dispose() {
    HardwareKeyboard.instance.removeHandler(_handleChatHardwareKey);
    unawaited(_screenshotEvents.cancel());
    unawaited(ScreenshotDetection.instance.stop());
    _draftTimer?.cancel();
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
    widget.controller.setActiveConversation(null);
    textController.dispose();
    scrollController.dispose();
    super.dispose();
  }

  void _handleScreenshot(DateTime occurredAt) {
    if (!mounted) return;
    final previous = _lastScreenshotNotice;
    if (previous != null &&
        occurredAt.difference(previous).abs().inSeconds < 2) {
      return;
    }
    _lastScreenshotNotice = occurredAt;
    unawaited(
      widget.controller
          .sendScreenshotNotice(widget.conversation.id)
          .then((_) => _scrollToEnd()),
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
                PersonAvatar(
                  name: widget.conversation.title,
                  size: 34,
                  avatarUrl: conversationAvatarUrl,
                  online:
                      widget.conversation.kind == ConversationKind.direct &&
                      (peer?.isOnline ?? false),
                ),
                const SizedBox(width: 9),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(
                        widget.conversation.title,
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
              if (!widget.conversation.isBusinessChannel) ...[
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
                        color: Theme.of(context).brightness == Brightness.dark
                            ? LinliColors.darkBackground
                            : const Color(0xFFF8FAFC),
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
                                await widget.controller.deleteMessages(
                                  widget.conversation.id,
                                  Set.of(selectedMessageIds),
                                );
                                if (!mounted) return;
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
                    else
                      ChatComposer(
                        controller: textController,
                        replyingTo: replyingTo,
                        showAttachments: showAttachments,
                        showEmoji: showEmoji,
                        onCancelReply: () => setState(() => replyingTo = null),
                        onToggleAttachments: () => setState(() {
                          showAttachments = !showAttachments;
                          showEmoji = false;
                        }),
                        onToggleEmoji: () => setState(() {
                          showEmoji = !showEmoji;
                          showAttachments = false;
                        }),
                        onAttachment: _pickAttachment,
                        allowLiveInteraction:
                            widget.conversation.channelType == 9,
                        onVoiceReady: _sendVoice,
                        onMention:
                            widget.conversation.kind == ConversationKind.group
                            ? _pickMention
                            : null,
                        onTypingChanged: (typing) => widget.controller
                            .updateTyping(widget.conversation.id, typing),
                        onSend: _send,
                        onSendOptions: _showSendOptions,
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
          _showError('“${file.name}”上传失败，可在消息旁重试');
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
    if (widget.controller.messageLoading.contains(widget.conversation.id)) {
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
        body: '消息会安全保存在本机，并在联网后同步。',
      );
    }
    final latestMineId = messages
        .where((message) => message.isMine)
        .lastOrNull
        ?.id;
    return ListView.builder(
      key: const Key('message-list'),
      controller: scrollController,
      keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
      padding: const EdgeInsets.fromLTRB(12, 12, 12, 16),
      itemCount: messages.length,
      itemBuilder: (context, index) {
        final message = messages[index];
        final previous = index == 0 ? null : messages[index - 1];
        final showTime =
            previous == null ||
            !_sameDay(message.sentAt, previous.sentAt) ||
            message.sentAt.difference(previous.sentAt).inMinutes >= 15;
        final sender = widget.conversation.members
            .where((user) => user.id == message.senderId)
            .firstOrNull;
        return Column(
          key: message.id == widget.initialMessageId
              ? initialMessageKey
              : _messageKeys.putIfAbsent(message.id, GlobalKey.new),
          children: [
            if (showTime) _TimeDivider(date: message.sentAt),
            MessageBubble(
              message: message,
              controller: widget.controller,
              avatarUrl:
                  sender?.avatarUrl ??
                  (widget.conversation.kind == ConversationKind.direct
                      ? peer?.avatarUrl
                      : null),
              showSender: widget.conversation.kind == ConversationKind.group,
              showGroupReceipt:
                  widget.conversation.kind == ConversationKind.group &&
                  message.id == latestMineId,
              onRetry: () => widget.controller.retryMessage(message),
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
          ],
        );
      },
    );
  }

  Future<void> _send([int? expiresInSeconds]) async {
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
    await future;
    _scrollToEnd();
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
        members: widget.conversation.members,
        currentUserId: widget.controller.currentUser?.id,
      ),
    );
    if (!mounted || mention == null) return;
    final token = mention.isEveryone ? '@所有人 ' : '@${mention.name} ';
    final selection = textController.selection;
    final start = selection.isValid
        ? selection.start
        : textController.text.length;
    final end = selection.isValid ? selection.end : textController.text.length;
    textController.text = textController.text.replaceRange(start, end, token);
    textController.selection = TextSelection.collapsed(
      offset: start + token.length,
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

  Future<void> _scrollToMessage(String messageId) async {
    final duration = nexaMotionDuration(context);
    var target = _messageKeys[messageId]?.currentContext;
    if (target == null) {
      await widget.controller.loadMessages(widget.conversation.id, force: true);
      if (!mounted) return;
      await WidgetsBinding.instance.endOfFrame;
      target = _messageKeys[messageId]?.currentContext;
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
      builder: (context) =>
          _ContactPickerSheet(contacts: widget.controller.contacts),
    );
    if (!mounted || contact == null) return;
    final confirmed = await showCupertinoDialog<bool>(
      context: context,
      builder: (context) => CupertinoAlertDialog(
        title: const Text('发送这张名片？'),
        content: Text(
          '\n${contact.name}\n账号：${contact.handle}\n\n仅发送昵称、账号和头像，不会发送手机号、备注或标签。',
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
          content: const Text('\n请在系统设置中允许“邻里通讯”使用你的位置。'),
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
    final reply = replyingTo;
    setState(() {
      replyingTo = null;
      showEmoji = false;
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
      'zip' => 'application/zip',
      'doc' => 'application/msword',
      'docx' =>
        'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      'xls' => 'application/vnd.ms-excel',
      'xlsx' =>
        'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
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

  void _scrollToEnd() => WidgetsBinding.instance.addPostFrameCallback((_) {
    if (!scrollController.hasClients) return;
    scrollController.animateTo(
      scrollController.position.maxScrollExtent,
      duration: nexaMotionDuration(context),
      curve: Curves.easeOutCubic,
    );
  });

  void _scrollToInitialMessage() =>
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final target = initialMessageKey.currentContext;
        if (target == null) {
          _scrollToEnd();
          return;
        }
        Scrollable.ensureVisible(
          target,
          duration: nexaMotionDuration(context),
          alignment: .42,
        );
      });

  Future<void> _showMessageActions(ChatMessage message, Offset anchor) async {
    final canRecall =
        message.isMine &&
        message.status != MessageStatus.sending &&
        message.status != MessageStatus.failed &&
        DateTime.now().difference(message.sentAt) <= const Duration(minutes: 2);
    unawaited(HapticFeedback.mediumImpact());
    final action = await showGeneralDialog<_MessageMenuAction>(
      context: context,
      barrierDismissible: true,
      barrierLabel: '关闭消息操作',
      barrierColor: Colors.black.withValues(alpha: .42),
      transitionDuration: nexaMotionDuration(context),
      pageBuilder: (dialogContext, _, _) => _MessageContextMenu(
        key: const Key('message-context-menu'),
        message: message,
        anchor: anchor,
        canRecall: canRecall,
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
      case _MessageMenuAction.copy:
        await Clipboard.setData(ClipboardData(text: message.text));
      case _MessageMenuAction.forward:
        _showForwardTargets([message], mode: 'separate');
      case _MessageMenuAction.favorite:
        final saved = await widget.controller.favoriteMessage(message);
        if (saved && mounted) {
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(const SnackBar(content: Text('已收藏到本机')));
        }
      case _MessageMenuAction.select:
        setState(() {
          selecting = true;
          selectedMessageIds.add(message.clientMessageId);
        });
      case _MessageMenuAction.recall:
        await widget.controller.recallMessage(message);
      case _MessageMenuAction.pin:
        final success = await widget.controller.toggleMessagePinned(message);
        if (!success && mounted) {
          _showError(widget.controller.error ?? '置顶状态更新失败');
        }
      case _MessageMenuAction.delete:
        await widget.controller.deleteMessage(message);
      case _MessageMenuAction.report:
        await Navigator.of(context).push(
          MaterialPageRoute(
            builder: (_) => ReportScreen(
              controller: widget.controller,
              target: message.senderName,
              targetId: message.id,
              targetType: 'message',
            ),
          ),
        );
    }
  }

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

  void _showMessageSearch() {
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (sheetContext) => _MessageSearchSheet(
        controller: widget.controller,
        conversationId: widget.conversation.id,
        onSelected: (message) {
          Navigator.pop(sheetContext);
          _scrollToMessage(message.id);
        },
      ),
    );
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
    await showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (sheetContext) => SafeArea(
        child: ListView(
          shrinkWrap: true,
          padding: const EdgeInsets.fromLTRB(12, 0, 12, 16),
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 4, 12, 10),
              child: Text(
                '转发到',
                style: Theme.of(context).textTheme.titleMedium,
              ),
            ),
            for (final conversation in widget.controller.conversations)
              ListTile(
                minTileHeight: 56,
                leading: PersonAvatar(
                  name: conversation.title,
                  size: 38,
                  avatarUrl: conversation.avatarUrl,
                ),
                title: Text(conversation.title),
                onTap: () async {
                  Navigator.pop(sheetContext);
                  final sent = await widget.controller.forwardMessages(
                    messages,
                    conversation.id,
                    mode: mode,
                  );
                  if (sent.isNotEmpty && mounted) {
                    setState(() {
                      selecting = false;
                      selectedMessageIds.clear();
                    });
                    ScaffoldMessenger.of(context).showSnackBar(
                      SnackBar(
                        content: Text(
                          mode == 'merged'
                              ? '已合并转发到 ${conversation.title}'
                              : '已转发 ${messages.length} 条到 ${conversation.title}',
                        ),
                      ),
                    );
                  }
                },
              ),
          ],
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
        title: Text('将 ${user.name} 加入黑名单？'),
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
  copy,
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
    required this.message,
    required this.anchor,
    required this.canRecall,
    required this.canPin,
    required this.onSelected,
  });

  final ChatMessage message;
  final Offset anchor;
  final bool canRecall;
  final bool canPin;
  final ValueChanged<_MessageMenuAction> onSelected;

  @override
  Widget build(BuildContext context) {
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
    final canEdit =
        message.isMine &&
        !message.id.startsWith('local-') &&
        message.status != MessageStatus.recalled &&
        canCopy;
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
      if (canCopy)
        const _ContextActionSpec(
          action: _MessageMenuAction.copy,
          icon: CupertinoIcons.doc_on_doc,
          label: '复制',
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
                  _MessageContextPreview(message: message),
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

class _MessageContextPreview extends StatelessWidget {
  const _MessageContextPreview({required this.message});
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
    final time =
        '${message.sentAt.hour.toString().padLeft(2, '0')}:${message.sentAt.minute.toString().padLeft(2, '0')}';
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
                  message.isMine ? '我' : message.senderName,
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
                    ? LinliColors.yellow
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
  Widget build(BuildContext context) {
    final multiUser = conversation.kind == ConversationKind.group;
    final business = conversation.isBusinessChannel;
    final group = multiUser && !business;
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
                        members: conversation.members,
                        fallbackName: conversation.title,
                        fallbackAvatar: conversation.avatarUrl,
                      )
                    : _DirectContactSummary(
                        user: conversation.members.firstOrNull,
                        fallbackName: conversation.title,
                        fallbackAvatar: conversation.avatarUrl,
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
                      target: conversation.title,
                      targetId: conversation.id,
                      targetType: 'conversation',
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
    required this.user,
    required this.fallbackName,
    this.fallbackAvatar,
  });

  final AppUser? user;
  final String fallbackName;
  final String? fallbackAvatar;

  @override
  Widget build(BuildContext context) => Row(
    children: [
      PersonAvatar(
        name: user?.name ?? fallbackName,
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
              user?.name ?? fallbackName,
              style: Theme.of(context).textTheme.titleLarge,
            ),
            const SizedBox(height: 4),
            Text(
              '邻里号：${user?.handle ?? '未设置'}',
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
                style: Theme.of(
                  context,
                ).textTheme.bodySmall?.copyWith(color: LinliColors.preview),
              ),
            ],
          ],
        ),
      ),
    ],
  );
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
    required this.members,
    required this.fallbackName,
    this.fallbackAvatar,
  });

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
        final columns = scale >= 1.6 ? 3 : 5;
        final width = constraints.maxWidth / columns;
        return Wrap(
          runSpacing: 14,
          children: [
            for (final user in people)
              SizedBox(
                width: width,
                child: Semantics(
                  label: '${user.name}，聊天成员',
                  child: Column(
                    children: [
                      PersonAvatar(
                        name: user.name,
                        size: 50,
                        avatarUrl: user.avatarUrl,
                        online: user.isOnline,
                      ),
                      const SizedBox(height: 6),
                      Text(
                        user.name,
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

class MessageBubble extends StatelessWidget {
  const MessageBubble({
    super.key,
    required this.message,
    this.controller,
    this.avatarUrl,
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
  final String? avatarUrl;
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
              color: LinliColors.yellow.withValues(alpha: .16),
              borderRadius: BorderRadius.circular(999),
            ),
            child: Text(
              '${message.isMine ? '我' : message.senderName} ${message.text.replaceFirst(RegExp(r'^[❤️👏⭐]\s*'), '')}',
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
      label: '${mine ? '我' : message.senderName}：${message.text}',
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
                PersonAvatar(
                  name: message.senderName,
                  size: 34,
                  avatarUrl: avatarUrl,
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
                            message.senderName,
                            style: Theme.of(context).textTheme.labelSmall,
                          ),
                        ),
                      _MessageContent(message: message, controller: controller),
                      if (message.status == MessageStatus.sending &&
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
                              color: LinliColors.yellow,
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
                            if (message.status == MessageStatus.failed)
                              GestureDetector(
                                onTap: onRetry,
                                child: const Row(
                                  children: [
                                    Icon(
                                      CupertinoIcons
                                          .exclamationmark_circle_fill,
                                      size: 13,
                                      color: LinliColors.systemRed,
                                    ),
                                    SizedBox(width: 3),
                                    Text(
                                      '发送失败，点此重试',
                                      style: TextStyle(
                                        color: LinliColors.systemRed,
                                        fontSize: 10,
                                      ),
                                    ),
                                  ],
                                ),
                              )
                            else
                              _DeliveryLabel(
                                status: message.status,
                                deliveredCount: showGroupReceipt
                                    ? message.deliveredCount
                                    : null,
                                readCount: showGroupReceipt
                                    ? message.readCount
                                    : null,
                              ),
                        ],
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

  static String _clock(DateTime date) =>
      '${date.hour.toString().padLeft(2, '0')}:${date.minute.toString().padLeft(2, '0')}';

  static String _expiry(DateTime date) =>
      '${date.month}月${date.day}日 ${_clock(date)}';
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
                      ? LinliColors.yellow.withValues(alpha: .18)
                      : Theme.of(context).colorScheme.surfaceContainerHigh,
                  borderRadius: BorderRadius.circular(12),
                  border: reaction.reactedByMe
                      ? Border.all(
                          color: LinliColors.yellow.withValues(alpha: .55),
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
        final image = media.startsWith('assets/')
            ? Image.asset(media, width: 220, height: 164, fit: BoxFit.cover)
            : media.startsWith('http://') ||
                  media.startsWith('https://') ||
                  media.startsWith('data:') ||
                  media.startsWith('blob:')
            ? Image.network(
                media,
                width: 220,
                height: 164,
                fit: BoxFit.cover,
                errorBuilder: (_, _, _) => _MediaUnavailable(
                  color: bubbleColor,
                  textColor: textColor,
                  label: '图片加载失败',
                ),
              )
            : Image.file(
                File(media),
                width: 220,
                height: 164,
                fit: BoxFit.cover,
                errorBuilder: (_, _, _) => _MediaUnavailable(
                  color: bubbleColor,
                  textColor: textColor,
                  label: '本地图片不可用',
                ),
              );
        return Semantics(
          label: '图片消息',
          child: ClipRRect(
            borderRadius: BorderRadius.circular(14),
            child: image,
          ),
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
          child: Image.network(
            media,
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
                      color: mine ? LinliColors.yellow : LinliColors.preview,
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
                    ? Image.network(
                        media,
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
  Widget build(BuildContext context) => Container(
    key: const Key('server-link-preview'),
    constraints: const BoxConstraints(maxWidth: 280),
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
          Image.network(
            imageUrl,
            width: double.infinity,
            height: 112,
            fit: BoxFit.cover,
            errorBuilder: (_, _, _) => const SizedBox.shrink(),
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
  );
}

class _MentionedMessageText extends StatelessWidget {
  const _MentionedMessageText({
    required this.message,
    required this.color,
    required this.mine,
  });

  final ChatMessage message;
  final Color color;
  final bool mine;

  @override
  Widget build(BuildContext context) {
    final base = Theme.of(context).textTheme.bodyLarge?.copyWith(color: color);
    if (message.mentions.isEmpty) return Text(message.text, style: base);
    final tokens = message.mentions
        .map((mention) => mention.isEveryone ? '@所有人' : '@${mention.name}')
        .toSet()
        .toList();
    final pattern = RegExp(tokens.map(RegExp.escape).join('|'));
    final spans = <TextSpan>[];
    var offset = 0;
    for (final match in pattern.allMatches(message.text)) {
      if (match.start > offset) {
        spans.add(TextSpan(text: message.text.substring(offset, match.start)));
      }
      spans.add(
        TextSpan(
          text: match.group(0),
          style: TextStyle(
            color: mine ? LinliColors.yellow : LinliColors.navy,
            fontWeight: FontWeight.w600,
          ),
        ),
      );
      offset = match.end;
    }
    if (offset < message.text.length) {
      spans.add(TextSpan(text: message.text.substring(offset)));
    }
    return Text.rich(TextSpan(style: base, children: spans));
  }
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

  static String _scheduledTime(DateTime date) =>
      '${date.month}月${date.day}日 ${date.hour.toString().padLeft(2, '0')}:${date.minute.toString().padLeft(2, '0')} 发送';
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
    required this.onCancelReply,
    this.onMention,
    this.onTypingChanged,
    this.replyingTo,
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
  final ValueChanged<MediaUpload> onVoiceReady;
  final VoidCallback onCancelReply;
  final VoidCallback? onMention;
  final ValueChanged<bool>? onTypingChanged;
  final ChatMessage? replyingTo;
  final bool showAttachments;
  final bool showEmoji;

  @override
  State<ChatComposer> createState() => _ChatComposerState();
}

class _ChatComposerState extends State<ChatComposer> {
  final AudioRecorder _recorder = AudioRecorder();
  final AudioPlayer _player = AudioPlayer();
  final FocusNode _inputFocusNode = FocusNode();
  Timer? _recordTimer;
  bool _voiceMode = false;
  bool _recording = false;
  bool _cancelRecording = false;
  bool _playing = false;
  bool _pressingVoice = false;
  int _recordSeconds = 0;
  String? _voiceDraftPath;
  int _voiceDraftSeconds = 0;

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_onTextChanged);
    _inputFocusNode.addListener(_onInputFocusChanged);
    HardwareKeyboard.instance.addHandler(_handleHardwareKey);
    _player.onPlayerComplete.listen((_) {
      if (mounted) setState(() => _playing = false);
    });
  }

  @override
  void didUpdateWidget(covariant ChatComposer oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.controller != widget.controller) {
      oldWidget.controller.removeListener(_onTextChanged);
      widget.controller.addListener(_onTextChanged);
    }
  }

  @override
  void dispose() {
    widget.onTypingChanged?.call(false);
    widget.controller.removeListener(_onTextChanged);
    _inputFocusNode.removeListener(_onInputFocusChanged);
    HardwareKeyboard.instance.removeHandler(_handleHardwareKey);
    _recordTimer?.cancel();
    final draftPath = _voiceDraftPath;
    if (draftPath != null) unawaited(_deleteVoiceFile(draftPath));
    unawaited(_recorder.dispose());
    unawaited(_player.dispose());
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
    if (_voiceMode || widget.controller.text.trim().isEmpty) {
      return false;
    }
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
    if (_recording) return;
    widget.onTypingChanged?.call(false);
    if (widget.showEmoji) widget.onToggleEmoji();
    if (widget.showAttachments) widget.onToggleAttachments();
    setState(() => _voiceMode = !_voiceMode);
  }

  Future<void> _startRecording() async {
    if (_recording || _voiceDraftPath != null) return;
    final allowed = await _recorder.hasPermission();
    if (!mounted) return;
    if (!allowed) {
      await showCupertinoDialog<void>(
        context: context,
        builder: (context) => CupertinoAlertDialog(
          title: const Text('无法使用麦克风'),
          content: const Text('请在系统设置中允许邻里通讯访问麦克风后再试。'),
          actions: [
            CupertinoDialogAction(
              onPressed: () => Navigator.pop(context),
              child: const Text('知道了'),
            ),
          ],
        ),
      );
      return;
    }
    if (!_pressingVoice) return;
    final path =
        '${Directory.systemTemp.path}/linli-im-voice-${DateTime.now().microsecondsSinceEpoch}.m4a';
    await _recorder.start(
      const RecordConfig(encoder: AudioEncoder.aacLc, bitRate: 64000),
      path: path,
    );
    if (!mounted) return;
    setState(() {
      _recording = true;
      _cancelRecording = false;
      _recordSeconds = 0;
    });
    _recordTimer?.cancel();
    _recordTimer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (!mounted || !_recording) return;
      setState(() => _recordSeconds++);
      if (_recordSeconds >= 60) unawaited(_finishRecording());
    });
  }

  void _beginVoicePress(LongPressStartDetails _) {
    _pressingVoice = true;
    unawaited(_startRecordingSafely());
  }

  Future<void> _startRecordingSafely() async {
    try {
      await _startRecording();
    } catch (_) {
      try {
        await _recorder.cancel();
      } catch (_) {
        // The recorder may not have reached an active state.
      }
      if (!mounted) return;
      setState(() {
        _recording = false;
        _recordSeconds = 0;
      });
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('录音启动失败，请检查麦克风后重试')));
    }
  }

  void _endVoicePress(LongPressEndDetails _) {
    _pressingVoice = false;
    unawaited(_finishRecording());
  }

  void _updateRecording(LongPressMoveUpdateDetails details) {
    if (!_recording) return;
    final shouldCancel = voiceRecordingShouldCancel(
      details.offsetFromOrigin.dy,
    );
    if (shouldCancel != _cancelRecording) {
      HapticFeedback.selectionClick();
      setState(() => _cancelRecording = shouldCancel);
    }
  }

  Future<void> _finishRecording({bool forceCancel = false}) async {
    if (!_recording) return;
    _recordTimer?.cancel();
    final cancel = forceCancel || _cancelRecording;
    final path = await _recorder.stop();
    if (!mounted) return;
    final seconds = _recordSeconds.clamp(1, 60);
    setState(() {
      _recording = false;
      _cancelRecording = false;
      _recordSeconds = 0;
      if (!cancel && path != null) {
        _voiceDraftPath = path;
        _voiceDraftSeconds = seconds;
      }
    });
    if (cancel && path != null) await _deleteVoiceFile(path);
  }

  Future<void> _togglePreview() async {
    final path = _voiceDraftPath;
    if (path == null) return;
    if (_playing) {
      await _player.stop();
      if (mounted) setState(() => _playing = false);
      return;
    }
    await _player.play(DeviceFileSource(path));
    if (mounted) setState(() => _playing = true);
  }

  Future<void> _discardDraft() async {
    final path = _voiceDraftPath;
    await _player.stop();
    if (mounted) {
      setState(() {
        _playing = false;
        _voiceDraftPath = null;
        _voiceDraftSeconds = 0;
      });
    }
    if (path != null) await _deleteVoiceFile(path);
  }

  Future<void> _sendVoice() async {
    final path = _voiceDraftPath;
    if (path == null) return;
    final file = File(path);
    if (!await file.exists()) {
      await _discardDraft();
      return;
    }
    final upload = MediaUpload(
      bytes: await file.readAsBytes(),
      fileName: path.split(Platform.pathSeparator).last,
      mimeType: 'audio/mp4',
      kind: MessageContentKind.voice,
      localPath: path,
      durationSeconds: _voiceDraftSeconds.clamp(1, 60),
    );
    await _player.stop();
    if (!mounted) return;
    setState(() {
      _playing = false;
      _voiceDraftPath = null;
      _voiceDraftSeconds = 0;
    });
    widget.onVoiceReady(upload);
  }

  Future<void> _deleteVoiceFile(String path) async {
    final file = File(path);
    if (await file.exists()) await file.delete();
  }

  @override
  Widget build(BuildContext context) {
    final canSend = widget.controller.text.trim().isNotEmpty;
    final canSendText = !_voiceMode && canSend;
    return GlassSurface(
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
                        '回复 ${widget.replyingTo!.senderName}：${widget.replyingTo!.text}',
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
                        ? _VoiceRecorderControl(
                            recording: _recording,
                            canceling: _cancelRecording,
                            seconds: _recordSeconds,
                            draftSeconds: _voiceDraftSeconds,
                            hasDraft: _voiceDraftPath != null,
                            playing: _playing,
                            onLongPressStart: _beginVoicePress,
                            onLongPressMoveUpdate: _updateRecording,
                            onLongPressEnd: _endVoicePress,
                            onPreview: _togglePreview,
                            onDiscard: _discardDraft,
                            onSend: _sendVoice,
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
                      onTap: canSendText
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
  }
}

bool voiceRecordingShouldCancel(double verticalOffset) => verticalOffset <= -64;

class _VoiceRecorderControl extends StatelessWidget {
  const _VoiceRecorderControl({
    required this.recording,
    required this.canceling,
    required this.seconds,
    required this.draftSeconds,
    required this.hasDraft,
    required this.playing,
    required this.onLongPressStart,
    required this.onLongPressMoveUpdate,
    required this.onLongPressEnd,
    required this.onPreview,
    required this.onDiscard,
    required this.onSend,
  });

  final bool recording;
  final bool canceling;
  final int seconds;
  final int draftSeconds;
  final bool hasDraft;
  final bool playing;
  final GestureLongPressStartCallback onLongPressStart;
  final GestureLongPressMoveUpdateCallback onLongPressMoveUpdate;
  final GestureLongPressEndCallback onLongPressEnd;
  final VoidCallback onPreview;
  final VoidCallback onDiscard;
  final VoidCallback onSend;

  @override
  Widget build(BuildContext context) {
    if (hasDraft) {
      return Container(
        key: const Key('voice-draft'),
        height: 44,
        padding: const EdgeInsets.only(left: 10),
        decoration: BoxDecoration(
          color: Theme.of(context).colorScheme.surfaceContainerHigh,
          borderRadius: BorderRadius.circular(14),
        ),
        child: Row(
          children: [
            IconButton(
              tooltip: playing ? '暂停试听' : '试听',
              onPressed: onPreview,
              icon: Icon(
                playing ? CupertinoIcons.pause_fill : CupertinoIcons.play_fill,
                size: 18,
              ),
            ),
            Expanded(child: Text('$draftSeconds 秒语音')),
            IconButton(
              tooltip: '丢弃语音',
              onPressed: onDiscard,
              icon: const Icon(CupertinoIcons.xmark, size: 18),
            ),
            IconButton(
              key: const Key('send-voice-button'),
              tooltip: '发送语音',
              onPressed: onSend,
              icon: const Icon(
                CupertinoIcons.arrow_up_circle_fill,
                color: LinliColors.navy,
                size: 27,
              ),
            ),
          ],
        ),
      );
    }
    return Semantics(
      button: true,
      label: recording
          ? canceling
                ? '松开取消录音'
                : '正在录音 $seconds 秒，上滑取消'
          : '按住说话',
      child: GestureDetector(
        key: const Key('hold-to-talk'),
        behavior: HitTestBehavior.opaque,
        onLongPressStart: onLongPressStart,
        onLongPressMoveUpdate: onLongPressMoveUpdate,
        onLongPressEnd: onLongPressEnd,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 140),
          height: 44,
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: canceling
                ? LinliColors.systemRed.withValues(alpha: .12)
                : recording
                ? LinliColors.navy.withValues(alpha: .10)
                : Theme.of(context).colorScheme.surfaceContainerHigh,
            borderRadius: BorderRadius.circular(14),
          ),
          child: Text(
            canceling
                ? '松开取消'
                : recording
                ? '${seconds.clamp(0, 60)}″  上滑取消'
                : '按住说话',
            style: Theme.of(context).textTheme.labelLarge?.copyWith(
              color: canceling ? LinliColors.systemRed : null,
            ),
          ),
        ),
      ),
    );
  }
}

class _ContactPickerSheet extends StatefulWidget {
  const _ContactPickerSheet({required this.contacts});

  final List<AppUser> contacts;

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
  Widget build(BuildContext context) {
    final normalized = query.trim().toLowerCase();
    final contacts = widget.contacts.where((contact) {
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
              placeholder: '搜索联系人或邻里号',
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
                      final name = contact.remark.trim().isEmpty
                          ? contact.name
                          : contact.remark;
                      return ListTile(
                        key: Key('contact-card-option-${contact.id}'),
                        minTileHeight: 64,
                        leading: PersonAvatar(
                          name: name,
                          size: 44,
                          avatarUrl: contact.avatarUrl,
                        ),
                        title: Text(name),
                        subtitle: Text('@${contact.handle}'),
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
  static const _recentKey = 'chat_recent_emojis';
  static const _categories = <(String, IconData, List<String>)>[
    (
      '最近',
      CupertinoIcons.clock,
      ['😊', '😂', '❤️', '👍', '🎉', '🙏', '🥰', '👏'],
    ),
    (
      '表情',
      CupertinoIcons.smiley,
      [
        '😀',
        '😃',
        '😄',
        '😁',
        '😆',
        '😅',
        '😂',
        '🤣',
        '😊',
        '🙂',
        '🙃',
        '😉',
        '😍',
        '🥰',
        '😘',
        '😋',
        '😎',
        '🤓',
        '🧐',
        '🥳',
        '😏',
        '😔',
        '😢',
        '😭',
        '😤',
        '😡',
        '🤯',
        '😱',
        '😴',
        '🤔',
        '🤗',
        '🫡',
      ],
    ),
    (
      '手势',
      CupertinoIcons.hand_raised,
      [
        '👋',
        '🤚',
        '🖐️',
        '✋',
        '🫶',
        '👌',
        '🤌',
        '🤏',
        '✌️',
        '🤞',
        '🫰',
        '🤟',
        '🤘',
        '🤙',
        '👈',
        '👉',
        '👆',
        '👇',
        '☝️',
        '👍',
        '👎',
        '✊',
        '👊',
        '👏',
      ],
    ),
    (
      '符号',
      CupertinoIcons.heart,
      [
        '❤️',
        '🧡',
        '💛',
        '💚',
        '💙',
        '💜',
        '🖤',
        '🤍',
        '💔',
        '💕',
        '💞',
        '💓',
        '💗',
        '💖',
        '✨',
        '⭐',
        '🔥',
        '🎉',
        '🎊',
        '✅',
        '❌',
        '💯',
        '❗',
        '❓',
      ],
    ),
  ];

  int _category = 0;
  List<String> _recent = List.of(_categories.first.$3);

  @override
  void initState() {
    super.initState();
    _loadRecent();
  }

  Future<void> _loadRecent() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final saved = prefs.getStringList(_recentKey);
      if (!mounted || saved == null || saved.isEmpty) return;
      setState(() => _recent = saved.take(24).toList());
    } catch (_) {
      // Emoji input remains fully usable when local preferences are unavailable.
    }
  }

  Future<void> _remember(String emoji) async {
    setState(() {
      _recent.remove(emoji);
      _recent.insert(0, emoji);
      if (_recent.length > 24) _recent.removeRange(24, _recent.length);
    });
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setStringList(_recentKey, _recent);
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
    final emojis = _category == 0 ? _recent : _categories[_category].$3;
    return Container(
      width: double.infinity,
      height: 298,
      color: Theme.of(context).colorScheme.surfaceContainerHigh,
      child: Column(
        children: [
          SizedBox(
            height: 48,
            child: ListView.builder(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 8),
              itemCount: _categories.length,
              itemBuilder: (context, index) => IconButton(
                key: Key('emoji-category-$index'),
                tooltip: _categories[index].$1,
                onPressed: () => setState(() => _category = index),
                icon: Icon(
                  _categories[index].$2,
                  color: _category == index
                      ? LinliColors.navy
                      : LinliColors.tertiaryLabel,
                ),
              ),
            ),
          ),
          const Divider(height: 1),
          Expanded(
            child: GridView.builder(
              key: const Key('emoji-grid'),
              padding: const EdgeInsets.fromLTRB(10, 8, 10, 4),
              gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
                crossAxisCount: 8,
                mainAxisExtent: 44,
              ),
              itemCount: emojis.length,
              itemBuilder: (context, index) => CupertinoButton(
                minimumSize: const Size(40, 40),
                padding: EdgeInsets.zero,
                onPressed: () => _insert(emojis[index]),
                child: Text(
                  emojis[index],
                  style: const TextStyle(fontSize: 25),
                ),
              ),
            ),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(10, 6, 10, 10),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                IconButton.filledTonal(
                  key: const Key('emoji-backspace'),
                  tooltip: '退格',
                  onPressed: _backspace,
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
  const _MentionPickerSheet({required this.members, this.currentUserId});

  final List<AppUser> members;
  final String? currentUserId;

  @override
  State<_MentionPickerSheet> createState() => _MentionPickerSheetState();
}

class _MentionPickerSheetState extends State<_MentionPickerSheet> {
  String query = '';

  @override
  Widget build(BuildContext context) {
    final normalized = query.trim().toLowerCase();
    final members = widget.members
        .where((member) => member.id != widget.currentUserId)
        .where(
          (member) =>
              normalized.isEmpty ||
              member.name.toLowerCase().contains(normalized) ||
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
                if (normalized.isEmpty)
                  ListTile(
                    key: const Key('mention-everyone'),
                    minTileHeight: 60,
                    leading: Container(
                      width: 44,
                      height: 44,
                      alignment: Alignment.center,
                      decoration: BoxDecoration(
                        color: LinliColors.yellow.withValues(alpha: .18),
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
                      name: member.name,
                      size: 44,
                      avatarUrl: member.avatarUrl,
                    ),
                    title: Text(member.name),
                    subtitle: Text('@${member.handle}'),
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
                      body: '换个昵称或邻里号试试。',
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
              final messages = snapshot.data ?? const [];
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
                    subtitle: Text(message.senderName),
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
  String query = '';
  bool loading = false;
  String? error;
  List<ChatMessage> matches = const [];
  Timer? debounce;

  @override
  void dispose() {
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
                            '${matches[index].senderName} · ${_searchDate(matches[index].sentAt)}',
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

  String _searchDate(DateTime value) =>
      '${value.month}月${value.day}日 '
      '${value.hour.toString().padLeft(2, '0')}:'
      '${value.minute.toString().padLeft(2, '0')}';
}
