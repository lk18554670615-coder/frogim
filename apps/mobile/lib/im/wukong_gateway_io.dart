import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:wukongimfluttersdk/common/options.dart';
import 'package:wukongimfluttersdk/entity/channel.dart' as full;
import 'package:wukongimfluttersdk/entity/channel_member.dart' as full;
import 'package:wukongimfluttersdk/entity/conversation.dart' as full;
import 'package:wukongimfluttersdk/entity/msg.dart' as full;
import 'package:wukongimfluttersdk/entity/reminder.dart' as full;
import 'package:wukongimfluttersdk/model/wk_message_content.dart';
import 'package:wukongimfluttersdk/type/const.dart' as full;
import 'package:wukongimfluttersdk/wkim.dart';

import 'message_content_registry.dart';
import 'history_access.dart';
import 'native_history_cache.dart';
import 'wukong_gateway_contract.dart';
import 'wukong_gateway_macos_easy.dart';

WukongGateway createWukongGateway({WukongDataSource? dataSource}) =>
    Platform.isMacOS
    ? MacOSWukongGateway(dataSource: dataSource)
    : IoWukongGateway(dataSource: dataSource);

class IoWukongGateway implements WukongGateway, WukongHistoryCache {
  @override
  Future<void> invalidateGroupHistory(
    String channelId,
    GroupHistoryAccess? access,
  ) => invalidateNativeGroupHistory(channelId, access);
  factory IoWukongGateway({WukongDataSource? dataSource}) =>
      IoWukongGateway._(dataSource);

  IoWukongGateway._(this._dataSource);

  static const _listenerKey = 'linli_wukong_gateway';
  final WukongDataSource? _dataSource;
  final _states = StreamController<WukongConnectionState>.broadcast();
  final _events = StreamController<WukongGatewayEvent>.broadcast();
  final _sendResults = StreamController<WukongSendResult>.broadcast();
  final _outgoingClientSeqs = <int>{};

  WukongConnectionState _state = WukongConnectionState.disconnected;
  WukongSession? _session;
  bool _disposed = false;
  Completer<void>? _fullConnectCompleter;
  Future<void> _fullSendQueue = Future<void>.value();
  bool _reminderSyncing = false;
  bool _reminderSyncAgain = false;

  @override
  Stream<WukongConnectionState> get connectionStates => _states.stream;

  @override
  Stream<WukongGatewayEvent> get events => _events.stream;

  @override
  Stream<WukongSendResult> get sendResults => _sendResults.stream;

  @override
  WukongConnectionState get connectionState => _state;

  @override
  WukongSession? get session => _session;

  @override
  Future<void> initialize(WukongSession session) async {
    _checkNotDisposed();
    session.validate();
    await disconnect();
    _session = session;
    if (!Platform.isAndroid && !Platform.isIOS) {
      throw UnsupportedError('the full SDK gateway supports Android and iOS');
    }
    if (session.sdk != 'wukongimfluttersdk' || session.deviceFlag != 0) {
      throw const FormatException(
        'this platform requires a full WuKong Flutter SDK session',
      );
    }
    await _initializeFull(session);
  }

  Future<void> _initializeFull(WukongSession session) async {
    final options = Options.newDefault(
      session.uid,
      session.token,
      addr: session.tcpAddress,
    )..deviceFlag = session.deviceFlag;
    final ready = await WKIM.shared.setup(options);
    if (!ready) {
      throw StateError(
        'WuKong Flutter SDK local database initialization failed',
      );
    }
    final connection = WKIM.shared.connectionManager;
    connection.removeOnConnectionStatus(_listenerKey);
    connection.addOnConnectionStatus(_listenerKey, _onFullConnection);

    final eventManager = WKIM.shared.eventManager;
    eventManager.removeEventListener(_listenerKey);
    eventManager.addEventListener(_listenerKey, (event) {
      _events.add(
        WukongGatewayEvent(
          kind: WukongGatewayEventKind.messageEvent,
          data: {
            'id': event.id,
            'type': event.type,
            'timestamp': event.timestamp,
            'data': wukongObjectMap(event.dataJson),
          },
        ),
      );
    });

    final messages = WKIM.shared.messageManager;
    messages.removeNewMsgListener(_listenerKey);
    messages.removeOnRefreshMsgListener(_listenerKey);
    messages.addOnNewMsgListener(_listenerKey, (items) {
      for (final item in items) {
        _emitFullMessage(WukongGatewayEventKind.received, item);
      }
    });
    messages.addOnRefreshMsgListener(_listenerKey, (item) {
      final mapped = _mapFullMessage(item);
      _events.add(
        WukongGatewayEvent(
          kind: WukongGatewayEventKind.refreshed,
          message: mapped,
          channel: mapped.channel,
        ),
      );
      if (item.status != full.WKSendMsgResult.sendLoading &&
          (_outgoingClientSeqs.remove(item.clientSeq) ||
              item.fromUID == _session?.uid)) {
        _sendResults.add(
          WukongSendResult(
            clientMsgNo: item.clientMsgNO,
            messageId: item.messageID,
            messageSeq: item.messageSeq,
            reasonCode: item.status,
            clientSeq: item.clientSeq,
          ),
        );
      }
    });
    messages.addOnMsgInsertedListener((item) {
      final mapped = _mapFullMessage(item);
      _events.add(
        WukongGatewayEvent(
          kind: WukongGatewayEventKind.inserted,
          message: mapped,
          channel: mapped.channel,
        ),
      );
    });
    for (final type in WukongContentType.custom) {
      messages.registerMsgContent(type, (data) => _RawFullContent.from(data));
    }
    messages.addOnSyncChannelMsgListener(_syncFullMessages);

    final channels = WKIM.shared.channelManager;
    channels.removeOnRefreshListener(_listenerKey);
    channels.addOnRefreshListener(_listenerKey, (item) {
      _events.add(
        WukongGatewayEvent(
          kind: WukongGatewayEventKind.conversationChanged,
          channel: WukongChannel(id: item.channelID, type: item.channelType),
          data: {'channelInfoChanged': true},
        ),
      );
    });
    channels.addOnGetChannelListener(_loadFullChannel);

    final members = WKIM.shared.channelMemberManager;
    members.removeNewMemberListener(_listenerKey);
    members.removeRefreshMemberListener(_listenerKey);
    members.removeDeleteMemberListener(_listenerKey);
    members.addOnNewMemberListener(
      _listenerKey,
      (items) => _emitFullMemberChange(items.firstOrNull),
    );
    members.addOnRefreshMemberListener(
      _listenerKey,
      (item, _) => _emitFullMemberChange(item),
    );
    members.addOnDeleteMemberListener(
      _listenerKey,
      (items) => _emitFullMemberChange(items.firstOrNull),
    );

    final conversations = WKIM.shared.conversationManager;
    conversations.removeOnRefreshMsgListListener(_listenerKey);
    conversations.addOnRefreshMsgListListener(_listenerKey, (items) {
      for (final item in items) {
        _events.add(
          WukongGatewayEvent(
            kind: WukongGatewayEventKind.conversationChanged,
            channel: WukongChannel(id: item.channelID, type: item.channelType),
            data: {
              'unread': item.unreadCount,
              'lastMessageSeq': item.lastMsgSeq,
              'lastClientMsgNo': item.clientMsgNo,
              'timestamp': item.lastMsgTimestamp,
            },
          ),
        );
      }
    });
    conversations.addOnSyncConversationListener(_syncFullConversations);

    final reminders = WKIM.shared.reminderManager;
    reminders.removeOnNewReminderListener(_listenerKey);
    reminders.addOnNewReminderListener(_listenerKey, (items) {
      for (final item in items) {
        _events.add(
          WukongGatewayEvent(
            kind: WukongGatewayEventKind.conversationChanged,
            channel: WukongChannel(id: item.channelID, type: item.channelType),
            data: {'remindersChanged': true},
          ),
        );
      }
    });

    WKIM.shared.cmdManager.removeCmdListener(_listenerKey);
    WKIM.shared.cmdManager.addOnCmdListener(_listenerKey, (command) {
      _events.add(
        WukongGatewayEvent(
          kind: WukongGatewayEventKind.command,
          data: {'cmd': command.cmd, 'param': _map(command.param)},
        ),
      );
      _scheduleReminderSync();
    });
  }

  @override
  Future<void> connect() async {
    _checkNotDisposed();
    if (_session == null) throw StateError('initialize must be called first');
    if (_state == WukongConnectionState.connected) return;
    final pending = _fullConnectCompleter;
    if (_state == WukongConnectionState.connecting &&
        pending != null &&
        !pending.isCompleted) {
      return pending.future.timeout(const Duration(seconds: 10));
    }
    _setState(WukongConnectionState.connecting);
    _fullConnectCompleter = Completer<void>();
    WKIM.shared.connectionManager.connect();
    return _fullConnectCompleter!.future.timeout(const Duration(seconds: 10));
  }

  @override
  Future<void> disconnect({bool logout = false}) async {
    if (_session != null) {
      WKIM.shared.connectionManager.disconnect(logout);
    }
    if (logout) {
      // The full SDK clears its uid/token on logout. Invalidate the matching
      // gateway session as well so a later login with the same stable WuKong
      // token cannot skip SDK setup and try to connect with empty options.
      _session = null;
      _outgoingClientSeqs.clear();
    }
    final completer = _fullConnectCompleter;
    if (completer != null && !completer.isCompleted) {
      completer.completeError(StateError('connection closed'));
    }
    _fullConnectCompleter = null;
    _setState(WukongConnectionState.disconnected);
  }

  @override
  Future<void> markRead(WukongChannel channel) async {
    if (_session != null) {
      await WKIM.shared.conversationManager.updateRedDot(
        channel.id,
        channel.type,
        0,
      );
      final reminders = await WKIM.shared.reminderManager.getWithChannel(
        channel.id,
        channel.type,
        0,
      );
      await _doneReminderIDs(reminders.map((item) => item.reminderID));
    }
  }

  @override
  Future<WukongMessage> send(WukongOutgoingMessage message) {
    _checkNotDisposed();
    if (_state != WukongConnectionState.connected) {
      throw StateError('WuKongIM is not connected');
    }
    final result = Completer<WukongMessage>();
    _fullSendQueue = _fullSendQueue.catchError((_) {}).then((_) async {
      try {
        result.complete(await _sendFull(message));
      } catch (error, stackTrace) {
        result.completeError(error, stackTrace);
      }
    });
    return result.future;
  }

  Future<WukongMessage> _sendFull(WukongOutgoingMessage outgoing) async {
    final manager = WKIM.shared.messageManager;
    final requestedClientMsgNo = outgoing.clientMsgNo?.trim() ?? '';
    if (!outgoing.noPersist && requestedClientMsgNo.isNotEmpty) {
      final existing = await manager.getWithClientMsgNo(requestedClientMsgNo);
      if (existing != null) {
        if (existing.status != full.WKSendMsgResult.sendSuccess) {
          existing.status = full.WKSendMsgResult.sendLoading;
          await manager.saveMsg(existing);
          manager.setRefreshMsg(existing);
          _outgoingClientSeqs.add(existing.clientSeq);
          WKIM.shared.connectionManager.sendMessage(existing);
        }
        return _mapFullMessage(existing);
      }
    }

    // The pinned SDK's high-level send method always creates a new
    // clientMsgNo. Build the same public WKMsg model here so an app retry can
    // reuse its durable idempotency key without patching the official SDK.
    final content = _RawFullContent(outgoing.payload);
    final item = full.WKMsg()
      ..messageContent = content
      ..contentType = content.contentType
      ..channelID = outgoing.channel.id
      ..channelType = outgoing.channel.type
      ..fromUID = _session!.uid
      ..topicID = outgoing.topic ?? ''
      ..expireTime = outgoing.expireSeconds;
    if (requestedClientMsgNo.isNotEmpty) {
      item.clientMsgNO = requestedClientMsgNo;
    }
    item.header
      ..noPersist = outgoing.noPersist
      ..redDot = outgoing.redDot
      ..syncOnce = outgoing.syncOnce;
    if (item.expireTime > 0) {
      item.expireTimestamp = item.timestamp + item.expireTime;
    }
    final encodedContent = content.encodeJson()..['type'] = content.contentType;
    item.content = jsonEncode(encodedContent);
    item.setChannelInfo(
      full.WKChannel(outgoing.channel.id, outgoing.channel.type),
    );
    final from = await WKIM.shared.channelManager.getChannel(
      item.fromUID,
      full.WKChannelType.personal,
    );
    if (from != null) item.setFrom(from);

    if (!outgoing.noPersist) {
      item.orderSeq = await manager.getMessageOrderSeq(
        0,
        item.channelID,
        item.channelType,
      );
      item.clientSeq = await manager.saveMsg(item);
      if (item.clientSeq <= 0) {
        throw StateError(
          'WuKong Flutter SDK failed to persist outgoing message',
        );
      }
      final conversation = await WKIM.shared.conversationManager.saveWithLiMMsg(
        item,
        0,
      );
      manager.setOnMsgInserted(item);
      if (conversation != null) {
        WKIM.shared.conversationManager.setRefreshUIMsgs([conversation]);
      }
    } else {
      // The official high-level path leaves all non-persistent messages at
      // clientSeq=0, which makes simultaneous SENDACK correlation ambiguous.
      item.clientSeq = 0x40000000 + Random.secure().nextInt(0x3fffffff);
    }
    _outgoingClientSeqs.add(item.clientSeq);
    WKIM.shared.connectionManager.sendMessage(item);
    return _mapFullMessage(item);
  }

  void _onFullConnection(int status, int? reasonCode, Object? info) {
    if (status == full.WKConnectStatus.kicked) {
      // WuKongIM handles a server DISCONNECT as logout and clears the SDK
      // credentials before publishing `kicked`. Mirror that invalidation in
      // this adapter; otherwise the next login can incorrectly reuse this
      // session and never call WKIM.setup again.
      _session = null;
      _outgoingClientSeqs.clear();
    }
    final mapped = switch (status) {
      full.WKConnectStatus.success => WukongConnectionState.connected,
      full.WKConnectStatus.syncMsg => WukongConnectionState.syncing,
      full.WKConnectStatus.syncCompleted => WukongConnectionState.connected,
      full.WKConnectStatus.connecting => WukongConnectionState.connecting,
      full.WKConnectStatus.kicked => WukongConnectionState.kicked,
      full.WKConnectStatus.noNetwork =>
        WukongConnectionState.networkUnavailable,
      _ => WukongConnectionState.disconnected,
    };
    _setState(mapped);
    if (status == full.WKConnectStatus.success ||
        status == full.WKConnectStatus.syncCompleted) {
      _scheduleReminderSync();
    }
    final completer = _fullConnectCompleter;
    if (status == full.WKConnectStatus.success &&
        completer != null &&
        !completer.isCompleted) {
      completer.complete();
    } else if ((status == full.WKConnectStatus.kicked ||
            (status == full.WKConnectStatus.fail && reasonCode != null)) &&
        completer != null &&
        !completer.isCompleted) {
      completer.completeError(
        StateError('WuKongIM connection rejected: ${reasonCode ?? status}'),
      );
    }
  }

  void _emitFullMessage(WukongGatewayEventKind kind, full.WKMsg item) {
    final mapped = _mapFullMessage(item);
    _events.add(
      WukongGatewayEvent(kind: kind, message: mapped, channel: mapped.channel),
    );
  }

  WukongMessage _mapFullMessage(full.WKMsg item) {
    var channelId = item.channelID;
    if (item.channelType == full.WKChannelType.personal &&
        channelId == _session?.uid) {
      channelId = item.fromUID;
    }
    return WukongMessage(
      messageId: item.messageID,
      messageSeq: item.messageSeq,
      clientMsgNo: item.clientMsgNO,
      clientSeq: item.clientSeq,
      fromUid: item.fromUID,
      channel: WukongChannel(id: channelId, type: item.channelType),
      timestamp: _fromSeconds(item.timestamp),
      payload: _payload(item.content),
      state: item.status == full.WKSendMsgResult.sendLoading
          ? WukongMessageState.sending
          : item.status == full.WKSendMsgResult.sendSuccess
          ? WukongMessageState.sent
          : WukongMessageState.failed,
      reasonCode: item.status,
      streamNo: item.streamNo,
      streamSeq: item.streamSeq,
      streamFlag: item.streamFlag,
    );
  }

  void _syncFullConversations(
    String lastMsgSeqs,
    int msgCount,
    int version,
    Function(full.WKSyncConversation) complete,
  ) {
    () async {
      final result = full.WKSyncConversation()
        ..uid = _session?.uid ?? ''
        ..conversations = [];
      try {
        final items = await _dataSource?.syncConversations(
          version: version,
          lastMsgSeqs: lastMsgSeqs,
          messageCount: msgCount,
        );
        for (final item in items ?? const <Map<String, Object?>>[]) {
          final conversation = full.WKSyncConvMsg()
            ..channelID = _string(item['channel_id'])
            ..channelType = _int(item['channel_type'])
            ..unread = _int(item['unread'])
            ..timestamp = _int(item['timestamp'])
            ..lastMsgSeq = _int(item['last_msg_seq'])
            ..lastClientMsgNO = _string(item['last_client_msg_no'])
            ..offsetMsgSeq = _int(item['offset_msg_seq'])
            ..version = _int(item['version'])
            ..recents = (item['recents'] as List<Object?>? ?? const [])
                .map(_syncMessage)
                .toList();
          result.conversations!.add(conversation);
        }
      } finally {
        complete(result);
      }
    }();
  }

  void _syncFullMessages(
    String channelId,
    int channelType,
    int startMessageSeq,
    int endMessageSeq,
    int limit,
    int pullMode,
    Function(full.WKSyncChannelMsg?) complete,
  ) {
    () async {
      final result = full.WKSyncChannelMsg()..messages = [];
      try {
        final response = await _dataSource?.syncMessages(
          channel: WukongChannel(id: channelId, type: channelType),
          startMessageSeq: startMessageSeq,
          endMessageSeq: endMessageSeq,
          limit: limit,
          pullMode: pullMode,
        );
        if (response != null) {
          result
            ..startMessageSeq = _int(response['start_message_seq'])
            ..endMessageSeq = _int(response['end_message_seq'])
            ..more = _int(response['more'])
            ..messages = (response['messages'] as List<Object?>? ?? const [])
                .map(_syncMessage)
                .toList();
        }
      } finally {
        complete(result);
      }
    }();
  }

  void _loadFullChannel(
    String channelId,
    int channelType,
    Function(full.WKChannel) complete,
  ) {
    () async {
      final channel = WukongChannel(id: channelId, type: channelType);
      try {
        final raw = await _dataSource?.channelInfo(channel);
        complete(_fullChannel(raw ?? const {}, channel));
      } catch (_) {
        // The SDK callback has no error channel. Complete with a minimally
        // identified channel so message insertion cannot stall; a later fetch
        // replaces it with authoritative business data.
        complete(full.WKChannel(channelId, channelType));
        return;
      }
      try {
        await _syncFullChannelMembers(channel);
      } catch (_) {
        // A channel refresh remains valid even if a member delta is
        // temporarily unavailable; the next channel fetch resumes by version.
      }
    }();
  }

  full.WKChannel _fullChannel(
    Map<String, Object?> raw,
    WukongChannel fallback,
  ) =>
      full.WKChannel(
          _string(raw['channel_id']).isEmpty
              ? fallback.id
              : _string(raw['channel_id']),
          _int(raw['channel_type']) == 0
              ? fallback.type
              : _int(raw['channel_type']),
        )
        ..channelName = _string(raw['channel_name'])
        ..channelRemark = _string(raw['channel_remark'])
        ..avatar = _string(raw['avatar'])
        ..showNick = _int(raw['show_nick'])
        ..top = _int(raw['top'])
        ..save = _int(raw['save'])
        ..mute = _int(raw['mute'])
        ..forbidden = _int(raw['forbidden'])
        ..invite = _int(raw['invite'])
        ..status = _int(raw['status'])
        ..follow = _int(raw['follow'])
        ..createdAt = _string(raw['created_at'])
        ..updatedAt = _string(raw['updated_at'])
        ..version = _int(raw['version'])
        ..online = _int(raw['online'])
        ..lastOffline = _int(raw['last_offline'])
        ..receipt = _int(raw['receipt'])
        ..category = _string(raw['category'])
        ..remoteExtraMap = _map(raw['remote_extra']);

  Future<void> _syncFullChannelMembers(WukongChannel channel) async {
    final source = _dataSource;
    if (source == null) return;
    var version = await WKIM.shared.channelMemberManager.getMaxVersion(
      channel.id,
      channel.type,
    );
    for (var page = 0; page < 50; page++) {
      final items = await source.syncChannelMembers(
        channel: channel,
        version: version,
        limit: 200,
      );
      if (items.isEmpty) return;
      final mapped = items.map((raw) {
        final item = full.WKChannelMember()
          ..channelID = channel.id
          ..channelType = channel.type
          ..memberUID = _string(raw['member_uid'])
          ..memberName = _string(raw['member_name'])
          ..memberRemark = _string(raw['member_remark'])
          ..memberAvatar = _string(raw['member_avatar'])
          ..role = _int(raw['role'])
          ..status = _int(raw['status'])
          ..isDeleted = _int(raw['is_deleted'])
          ..createdAt = _string(raw['created_at'])
          ..updatedAt = _string(raw['updated_at'])
          ..version = _int(raw['version'])
          ..extraMap = _map(raw['extra']);
        version = max(version, item.version);
        return item;
      }).toList();
      await WKIM.shared.channelMemberManager.saveOrUpdateList(mapped);
      if (items.length < 200) return;
    }
    throw StateError('WuKong channel member sync exceeded 50 pages');
  }

  void _emitFullMemberChange(full.WKChannelMember? item) {
    if (item == null) return;
    _events.add(
      WukongGatewayEvent(
        kind: WukongGatewayEventKind.conversationChanged,
        channel: WukongChannel(id: item.channelID, type: item.channelType),
        data: {'channelMembersChanged': true, 'memberId': item.memberUID},
      ),
    );
  }

  void _scheduleReminderSync() {
    unawaited(_syncPlatformReminders().catchError((_) {}));
  }

  Future<void> _syncPlatformReminders() async {
    if (_reminderSyncing) {
      _reminderSyncAgain = true;
      return;
    }
    final source = _dataSource;
    if (source == null) return;
    _reminderSyncing = true;
    try {
      do {
        _reminderSyncAgain = false;
        var version = await WKIM.shared.reminderManager.getMaxVersion();
        for (var page = 0; page < 50; page++) {
          final items = await source.syncReminders(
            version: version,
            limit: 500,
          );
          if (items.isEmpty) break;
          for (final item in items) {
            version = max(version, _int(item['version']));
          }
          final mapped = items.map((item) {
            return full.WKReminder()
              ..reminderID = _int(item['reminder_id'])
              ..messageID = _string(item['message_id'])
              ..channelID = _string(item['channel_id'])
              ..channelType = _int(item['channel_type'])
              ..messageSeq = _int(item['message_seq'])
              ..type = _int(item['type'])
              ..isLocate = _int(item['is_locate'])
              ..uid = _string(item['uid'])
              ..text = _string(item['text'])
              ..data = _map(item['data'])
              ..version = _int(item['version'])
              ..done = _int(item['done'])
              ..needUpload = _int(item['need_upload'])
              ..publisher = _string(item['publisher']);
          }).toList();
          await WKIM.shared.reminderManager.saveOrUpdateReminders(mapped);
          if (items.length < 500) break;
          if (page == 49) {
            throw StateError('WuKong reminder sync exceeded 50 pages');
          }
        }
      } while (_reminderSyncAgain);
    } finally {
      _reminderSyncing = false;
    }
  }

  Future<void> _doneReminderIDs(Iterable<int> rawIDs) async {
    final source = _dataSource;
    if (source == null) return;
    final ids = rawIDs.where((id) => id > 0).toSet().toList()..sort();
    for (var offset = 0; offset < ids.length; offset += 500) {
      await source.doneReminders(
        ids.sublist(offset, min(offset + 500, ids.length)),
      );
    }
    if (ids.isNotEmpty) await _syncPlatformReminders();
  }

  full.WKSyncMsg _syncMessage(Object? source) {
    final item = _map(source);
    final message = full.WKSyncMsg()
      ..channelID = _string(item['channel_id'])
      ..channelType = _int(item['channel_type'])
      ..messageID = _string(item['message_idstr']).isNotEmpty
          ? _string(item['message_idstr'])
          : _string(item['message_id'])
      ..clientMsgNO = _string(item['client_msg_no'])
      ..messageSeq = _int(item['message_seq'])
      ..fromUID = _string(item['from_uid'])
      ..timestamp = _int(item['timestamp'])
      ..setting = _int(item['setting'])
      ..streamNo = _string(item['stream_no'])
      ..streamSeq = _int(item['stream_seq'])
      ..streamFlag = _int(item['stream_flag'])
      ..payload = projectWukongStreamPayload(item);
    final extra = _map(item['message_extra']);
    if (extra.isNotEmpty) {
      message.messageExtra = full.WKSyncExtraMsg()
        ..messageIdStr = _string(extra['message_idstr'])
        ..revoke = _int(extra['revoke'])
        ..revoker = _string(extra['revoker'])
        ..extraVersion = _int(extra['extra_version'])
        ..unreadCount = _int(extra['unread_count'])
        ..readedCount = _int(extra['readed_count'])
        ..readed = _int(extra['readed'])
        ..contentEdit = _map(extra['content_edit'])
        ..editedAt = _int(extra['edited_at']);
    }
    return message;
  }

  void _setState(WukongConnectionState value) {
    if (_state == value || _disposed) return;
    _state = value;
    _states.add(value);
  }

  @override
  Future<void> dispose() async {
    if (_disposed) return;
    await disconnect(logout: true);
    WKIM.shared.connectionManager.removeOnConnectionStatus(_listenerKey);
    WKIM.shared.eventManager.removeEventListener(_listenerKey);
    WKIM.shared.messageManager.removeNewMsgListener(_listenerKey);
    WKIM.shared.messageManager.removeOnRefreshMsgListener(_listenerKey);
    WKIM.shared.channelManager.removeOnRefreshListener(_listenerKey);
    WKIM.shared.channelMemberManager.removeNewMemberListener(_listenerKey);
    WKIM.shared.channelMemberManager.removeRefreshMemberListener(_listenerKey);
    WKIM.shared.channelMemberManager.removeDeleteMemberListener(_listenerKey);
    WKIM.shared.conversationManager.removeOnRefreshMsgListListener(
      _listenerKey,
    );
    WKIM.shared.reminderManager.removeOnNewReminderListener(_listenerKey);
    WKIM.shared.cmdManager.removeCmdListener(_listenerKey);
    _disposed = true;
    await _states.close();
    await _events.close();
    await _sendResults.close();
  }

  void _checkNotDisposed() {
    if (_disposed) throw StateError('WuKongIM gateway is disposed');
  }
}

class _RawFullContent extends WKMessageContent {
  _RawFullContent(Map<String, Object?> payload)
    : _payload = Map<String, Object?>.from(payload) {
    contentType = _int(_payload['type']);
  }

  factory _RawFullContent.from(Object? value) => _RawFullContent(_map(value));

  final Map<String, Object?> _payload;

  @override
  Map<String, dynamic> encodeJson() => {
    for (final entry in _payload.entries)
      if (entry.key != 'type') entry.key: entry.value,
  };

  @override
  WKMessageContent decodeJson(Map<String, dynamic> json) =>
      _RawFullContent(Map<String, Object?>.from(json));

  @override
  String displayText() => _string(_payload['content']);
}

Map<String, Object?> _map(Object? value) => wukongObjectMap(value);

Map<String, Object?> _payload(Object? value) {
  if (value is Map) return _map(value);
  if (value is String && value.isNotEmpty) {
    try {
      final decoded = jsonDecode(value);
      if (decoded is Map) return _map(decoded);
    } catch (_) {
      try {
        final decoded = jsonDecode(utf8.decode(base64Decode(value)));
        if (decoded is Map) return _map(decoded);
      } catch (_) {
        return {'type': -1, 'content': value};
      }
    }
  }
  return const {'type': -1};
}

int _int(Object? value) =>
    value is num ? value.toInt() : int.tryParse(value?.toString() ?? '') ?? 0;

String _string(Object? value) => value?.toString() ?? '';

DateTime _fromSeconds(int value) =>
    DateTime.fromMillisecondsSinceEpoch(value * 1000, isUtc: true);
