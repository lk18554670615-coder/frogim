import 'dart:async';
import 'dart:js_interop';
import 'dart:js_interop_unsafe';
import 'dart:math';

import 'message_content_registry.dart';
import 'history_access.dart';
import 'wukong_gateway_contract.dart';

@JS('globalThis.wk')
external JSObject? get _wkGlobal;

WukongGateway createWukongGateway({WukongDataSource? dataSource}) =>
    WebWukongGateway(dataSource: dataSource);

class WebWukongGateway implements WukongGateway, WukongHistoryCache {
  @override
  Future<void> invalidateGroupHistory(
    String channelId,
    GroupHistoryAccess? access,
  ) async {
    // Official JS 1.3.5 keeps no history DB: only conversation previews and
    // reminders. Never touch its outgoing send queues or conversation drafts.
    final manager = _conversationManager;
    if (manager != null) {
      final conversations = manager
          .getProperty<JSArray<JSObject>>('conversations'.toJS)
          .toDart;
      for (final conversation in conversations) {
        final channel = _object(conversation, 'channel');
        if (_string(channel, 'channelID') != channelId ||
            _integer(channel, 'channelType') != 2) {
          continue;
        }
        final message = conversation.getProperty<JSObject?>('lastMessage'.toJS);
        if (message != null &&
            !(access?.allows(
                  _integer(message, 'messageSeq').toInt(),
                  DateTime.fromMillisecondsSinceEpoch(
                    _integer(message, 'timestamp').toInt() * 1000,
                  ),
                ) ??
                false)) {
          conversation.setProperty('lastMessage'.toJS, null);
          conversation.setProperty('unread'.toJS, 0.toJS);
        }
      }
    }
    final reminders = _reminderManager;
    if (reminders != null) {
      final items = reminders
          .getProperty<JSArray<JSObject>>('reminders'.toJS)
          .toDart;
      reminders.setProperty(
        'reminders'.toJS,
        items
            .where((item) {
              final channel = _object(item, 'channel');
              return _string(channel, 'channelID') != channelId ||
                  _integer(channel, 'channelType') != 2 ||
                  (access?.visibleAll == true ||
                      (access?.afterSeq != null &&
                          _integer(item, 'messageSeq') > access!.afterSeq!));
            })
            .toList()
            .toJS,
      );
    }
  }

  factory WebWukongGateway({WukongDataSource? dataSource}) =>
      WebWukongGateway._(dataSource);

  WebWukongGateway._(this._dataSource);

  final WukongDataSource? _dataSource;
  final _states = StreamController<WukongConnectionState>.broadcast();
  final _events = StreamController<WukongGatewayEvent>.broadcast();
  final _sendResults = StreamController<WukongSendResult>.broadcast();
  final _callbacks = <JSFunction>[];
  final _clientMsgBySeq = <int, String>{};

  JSObject? _sdk;
  JSObject? _connectManager;
  JSObject? _chatManager;
  JSObject? _conversationManager;
  JSObject? _reminderManager;
  JSObject? _eventManager;
  JSFunction? _eventCallback;
  WukongSession? _session;
  WukongConnectionState _state = WukongConnectionState.disconnected;
  Completer<void>? _connectCompleter;
  bool _disposed = false;

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
    if (session.sdk != 'wukongimjssdk' || session.deviceFlag != 1) {
      throw const FormatException(
        'Web requires the official WuKong JavaScript SDK session',
      );
    }
    await disconnect();
    final previousEventManager = _eventManager;
    final previousEventCallback = _eventCallback;
    if (previousEventManager != null && previousEventCallback != null) {
      previousEventManager.callMethod<JSAny?>(
        'removeEventListener'.toJS,
        previousEventCallback,
      );
    }
    final wk = _wkGlobal;
    if (wk == null) {
      throw StateError('wukongimjssdk 1.3.5 is not loaded');
    }
    final sdkClass = wk.getProperty<JSFunction>('WKSDK'.toJS);
    final sdk = sdkClass.callMethod<JSObject>('shared'.toJS);
    final config = _object(sdk, 'config');
    config
      ..setProperty('uid'.toJS, session.uid.toJS)
      ..setProperty('token'.toJS, session.token.toJS)
      ..setProperty('addr'.toJS, session.wsUrl.toJS)
      ..setProperty('deviceFlag'.toJS, session.deviceFlag.toJS)
      ..setProperty('debug'.toJS, false.toJS);
    final sdkVersion = _string(config, 'sdkVersion');
    if (sdkVersion.isNotEmpty && sdkVersion != '1.3.5') {
      throw StateError('expected wukongimjssdk 1.3.5, loaded $sdkVersion');
    }

    _session = session;
    _sdk = sdk;
    _connectManager = _object(sdk, 'connectManager');
    _chatManager = _object(sdk, 'chatManager');
    _conversationManager = _object(sdk, 'conversationManager');
    _reminderManager = _object(sdk, 'reminderManager');
    _eventManager = _object(sdk, 'eventManager');
    _eventCallback = null;
    _callbacks.clear();
    _configureProvider(_object(config, 'provider'));
    _configureListeners();
    for (final type in WukongContentType.custom) {
      final callback = ((JSNumber? value) => _newRawContent(
        value?.toDartInt ?? type,
      )).toJS;
      _callbacks.add(callback);
      sdk.callMethodVarArgs<JSAny?>('register'.toJS, [type.toJS, callback]);
    }
  }

  void _configureProvider(JSObject provider) {
    final connectAddress = ((JSFunction complete) {
      complete.callAsFunction(null, _session!.wsUrl.toJS);
    }).toJS;
    final syncConversations = ((JSAny? _) => _syncWebConversations().toJS).toJS;
    final syncMessages =
        ((JSObject channel, JSObject options) => _syncWebMessages(
          channel,
          options,
        ).toJS).toJS;
    final channelInfo = ((JSObject channel) => _loadWebChannelInfo(
      channel,
    ).toJS).toJS;
    final syncSubscribers =
        ((JSObject channel, JSNumber version) => _syncWebMembers(
          channel,
          version.toDartInt,
        ).toJS).toJS;
    final syncMessageExtras =
        ((JSObject channel, JSNumber version, JSNumber limit) =>
                _syncWebMessageExtras(
                  channel,
                  version.toDartInt,
                  limit.toDartInt,
                ).toJS)
            .toJS;
    final syncReminders = ((JSNumber version) => _loadWebReminders(
      version.toDartInt,
    ).toJS).toJS;
    final reminderDone = ((JSArray<JSNumber> ids) => _doneWebReminders(
      ids.toDart.map((id) => id.toDartInt).toList(),
    ).toJS).toJS;
    _callbacks.addAll([
      connectAddress,
      syncConversations,
      syncMessages,
      channelInfo,
      syncSubscribers,
      syncMessageExtras,
      syncReminders,
      reminderDone,
    ]);
    provider
      ..setProperty('connectAddrCallback'.toJS, connectAddress)
      ..setProperty('syncConversationsCallback'.toJS, syncConversations)
      ..setProperty('syncMessagesCallback'.toJS, syncMessages)
      ..setProperty('channelInfoCallback'.toJS, channelInfo)
      ..setProperty('syncSubscribersCallback'.toJS, syncSubscribers)
      ..setProperty('syncMessageExtraCallback'.toJS, syncMessageExtras)
      ..setProperty('syncRemindersCallback'.toJS, syncReminders)
      ..setProperty('reminderDoneCallback'.toJS, reminderDone);
  }

  Future<JSObject> _loadWebChannelInfo(JSObject channel) async {
    final raw = await _dataSource?.channelInfo(
      WukongChannel(
        id: _string(channel, 'channelID'),
        type: _integer(channel, 'channelType'),
      ),
    );
    final item = raw ?? const <String, Object?>{};
    final info = _sdk!.callMethod<JSObject>('newChannelInfo'.toJS);
    info
      ..setProperty('channel'.toJS, channel)
      ..setProperty(
        'title'.toJS,
        (_stringValue(item['channel_name']).isEmpty
                ? _string(channel, 'channelID')
                : _stringValue(item['channel_name']))
            .toJS,
      )
      ..setProperty('logo'.toJS, _stringValue(item['avatar']).toJS)
      ..setProperty('mute'.toJS, (_int(item['mute']) == 1).toJS)
      ..setProperty('top'.toJS, (_int(item['top']) == 1).toJS)
      ..setProperty('online'.toJS, (_int(item['online']) == 1).toJS)
      ..setProperty('lastOffline'.toJS, _int(item['last_offline']).toJS)
      ..setProperty('orgData'.toJS, _map(item['remote_extra']).jsify());
    return info;
  }

  Future<JSArray<JSObject>> _syncWebMembers(
    JSObject rawChannel,
    int version,
  ) async {
    final channel = WukongChannel(
      id: _string(rawChannel, 'channelID'),
      type: _integer(rawChannel, 'channelType'),
    );
    final items = await _dataSource?.syncChannelMembers(
      channel: channel,
      version: version,
      limit: 500,
    );
    return (items ?? const <Map<String, Object?>>[])
        .map((item) {
          final subscriber = _constructor(
            'Subscriber',
          ).callAsConstructor<JSObject>();
          subscriber
            ..setProperty('uid'.toJS, _stringValue(item['member_uid']).toJS)
            ..setProperty('name'.toJS, _stringValue(item['member_name']).toJS)
            ..setProperty(
              'remark'.toJS,
              _stringValue(item['member_remark']).toJS,
            )
            ..setProperty(
              'avatar'.toJS,
              _stringValue(item['member_avatar']).toJS,
            )
            ..setProperty('role'.toJS, _int(item['role']).toJS)
            ..setProperty('channel'.toJS, rawChannel)
            ..setProperty('version'.toJS, _int(item['version']).toJS)
            ..setProperty(
              'isDeleted'.toJS,
              (_int(item['is_deleted']) == 1).toJS,
            )
            ..setProperty('status'.toJS, _int(item['status']).toJS)
            ..setProperty('orgData'.toJS, _map(item['extra']).jsify());
          return subscriber;
        })
        .toList()
        .toJS;
  }

  Future<JSArray<JSObject>> _syncWebMessageExtras(
    JSObject rawChannel,
    int version,
    int limit,
  ) async {
    final items = await _dataSource?.syncMessageExtras(
      channel: WukongChannel(
        id: _string(rawChannel, 'channelID'),
        type: _integer(rawChannel, 'channelType'),
      ),
      version: version,
      limit: limit.clamp(1, 500),
    );
    return (items ?? const <Map<String, Object?>>[])
        .map((item) {
          final extra = _constructor(
            'MessageExtra',
          ).callAsConstructor<JSObject>();
          extra
            ..setProperty(
              'messageID'.toJS,
              _stringValue(item['message_idstr']).toJS,
            )
            ..setProperty('channel'.toJS, rawChannel)
            ..setProperty('messageSeq'.toJS, _int(item['message_seq']).toJS)
            ..setProperty('readed'.toJS, (_int(item['readed']) == 1).toJS)
            ..setProperty('readedCount'.toJS, _int(item['readed_count']).toJS)
            ..setProperty('unreadCount'.toJS, _int(item['unread_count']).toJS)
            ..setProperty('revoke'.toJS, (_int(item['revoke']) == 1).toJS)
            ..setProperty('revoker'.toJS, _stringValue(item['revoker']).toJS)
            ..setProperty('editedAt'.toJS, _int(item['edited_at']).toJS)
            ..setProperty('extra'.toJS, _map(item['extra']).jsify())
            ..setProperty(
              'extraVersion'.toJS,
              _int(item['extra_version']).toJS,
            );
          final edited = _map(item['content_edit']);
          if (edited.isNotEmpty) {
            extra.setProperty(
              'contentEdit'.toJS,
              _newRawContent(_int(edited['type']), edited),
            );
          }
          return extra;
        })
        .toList()
        .toJS;
  }

  Future<JSArray<JSObject>> _loadWebReminders(int initialVersion) async {
    final source = _dataSource;
    if (source == null) return <JSObject>[].toJS;
    var version = initialVersion;
    final result = <JSObject>[];
    for (var page = 0; page < 50; page++) {
      final items = await source.syncReminders(version: version, limit: 500);
      for (final item in items) {
        final reminder = _constructor('Reminder').callAsConstructor<JSObject>();
        final channel = _sdk!.callMethodVarArgs<JSObject>('newChannel'.toJS, [
          _stringValue(item['channel_id']).toJS,
          _int(item['channel_type']).toJS,
        ]);
        reminder
          ..setProperty('channel'.toJS, channel)
          ..setProperty('reminderID'.toJS, _int(item['reminder_id']).toJS)
          ..setProperty('messageID'.toJS, _stringValue(item['message_id']).toJS)
          ..setProperty('messageSeq'.toJS, _int(item['message_seq']).toJS)
          ..setProperty('reminderType'.toJS, _int(item['type']).toJS)
          ..setProperty('text'.toJS, _stringValue(item['text']).toJS)
          ..setProperty('data'.toJS, _map(item['data']).jsify())
          ..setProperty('isLocate'.toJS, (_int(item['is_locate']) == 1).toJS)
          ..setProperty('version'.toJS, _int(item['version']).toJS)
          ..setProperty('done'.toJS, (_int(item['done']) == 1).toJS);
        version = max(version, _int(item['version']));
        result.add(reminder);
      }
      if (items.length < 500) return result.toJS;
    }
    throw StateError('WuKong reminder sync exceeded 50 pages');
  }

  Future<void> _doneWebReminders(List<int> reminderIds) async {
    if (reminderIds.isEmpty) return;
    await _dataSource?.doneReminders(reminderIds);
  }

  void _refreshWebReminders() {
    final manager = _reminderManager;
    if (manager == null) return;
    final promise = manager.callMethod<JSPromise<JSAny?>>('sync'.toJS);
    unawaited(promise.toDart.catchError((_) => null));
  }

  void _configureListeners() {
    final connection = ((JSNumber status, JSNumber? reason, JSObject? _) {
      final value = status.toDartInt;
      final state = switch (value) {
        1 => WukongConnectionState.connected,
        2 => WukongConnectionState.connecting,
        4 => WukongConnectionState.kicked,
        _ => WukongConnectionState.disconnected,
      };
      _setState(state);
      final completer = _connectCompleter;
      if (value == 1 && completer != null && !completer.isCompleted) {
        completer.complete();
      } else if ((value == 3 || value == 4) &&
          completer != null &&
          !completer.isCompleted) {
        completer.completeError(
          StateError(
            'WuKongIM Web connection rejected: ${reason?.toDartInt ?? value}',
          ),
        );
      }
      if (value == 1) _refreshWebReminders();
    }).toJS;
    final message = ((JSObject raw) {
      final mapped = _fromWebMessage(raw);
      final isSending = mapped.state == WukongMessageState.sending;
      if (isSending && mapped.clientSeq > 0) {
        _clientMsgBySeq[mapped.clientSeq] = mapped.clientMsgNo;
      }
      _events.add(
        WukongGatewayEvent(
          kind: isSending
              ? WukongGatewayEventKind.inserted
              : WukongGatewayEventKind.received,
          message: mapped,
          channel: mapped.channel,
        ),
      );
    }).toJS;
    final sendStatus = ((JSObject ack) {
      final clientSeq = _integer(ack, 'clientSeq');
      final clientMsgNo = _clientMsgBySeq.remove(clientSeq) ?? '';
      _sendResults.add(
        WukongSendResult(
          clientMsgNo: clientMsgNo,
          messageId: _valueString(ack.getProperty<JSAny?>('messageID'.toJS)),
          messageSeq: _integer(ack, 'messageSeq'),
          reasonCode: _integer(ack, 'reasonCode'),
          clientSeq: clientSeq,
        ),
      );
    }).toJS;
    final command = ((JSObject raw) {
      final mapped = _fromWebMessage(raw);
      _events.add(
        WukongGatewayEvent(
          kind: WukongGatewayEventKind.command,
          message: mapped,
          channel: mapped.channel,
          data: mapped.payload,
        ),
      );
      _refreshWebReminders();
    }).toJS;
    final conversation = ((JSObject raw, JSNumber action) {
      final channel = _object(raw, 'channel');
      _events.add(
        WukongGatewayEvent(
          kind: WukongGatewayEventKind.conversationChanged,
          channel: WukongChannel(
            id: _string(channel, 'channelID'),
            type: _integer(channel, 'channelType'),
          ),
          data: {
            'action': action.toDartInt,
            'unread': _integer(raw, 'unread'),
            'timestamp': _integer(raw, 'timestamp'),
          },
        ),
      );
    }).toJS;
    final messageEvent = ((JSObject raw) {
      final data = _map(raw.getProperty<JSAny?>('dataJson'.toJS)?.dartify());
      _events.add(
        WukongGatewayEvent(
          kind: WukongGatewayEventKind.messageEvent,
          data: {
            'id': _string(raw, 'id'),
            'type': _string(raw, 'type'),
            'timestamp': _integer(raw, 'timestamp'),
            'data': data,
          },
        ),
      );
    }).toJS;
    _eventCallback = messageEvent;
    _callbacks.addAll([
      connection,
      message,
      sendStatus,
      command,
      conversation,
      messageEvent,
    ]);
    _connectManager!.callMethod<JSAny?>(
      'addConnectStatusListener'.toJS,
      connection,
    );
    _chatManager!
      ..callMethod<JSAny?>('addMessageListener'.toJS, message)
      ..callMethod<JSAny?>('addMessageStatusListener'.toJS, sendStatus)
      ..callMethod<JSAny?>('addCMDListener'.toJS, command);
    _conversationManager!.callMethod<JSAny?>(
      'addConversationListener'.toJS,
      conversation,
    );
    _eventManager!.callMethod<JSAny?>('addEventListener'.toJS, messageEvent);
  }

  @override
  Future<void> connect() {
    _checkNotDisposed();
    if (_sdk == null) throw StateError('initialize must be called first');
    if (_state == WukongConnectionState.connected) return Future.value();
    _setState(WukongConnectionState.connecting);
    final completer = _connectCompleter = Completer<void>();
    _sdk!.callMethod<JSAny?>('connect'.toJS);
    return completer.future.timeout(const Duration(seconds: 10));
  }

  @override
  Future<void> disconnect({bool logout = false}) async {
    _sdk?.callMethod<JSAny?>('disconnect'.toJS);
    final completer = _connectCompleter;
    if (completer != null && !completer.isCompleted) {
      completer.completeError(StateError('connection closed'));
    }
    _connectCompleter = null;
    _setState(WukongConnectionState.disconnected);
  }

  @override
  Future<void> markRead(WukongChannel channel) async {
    final sdk = _sdk;
    final manager = _conversationManager;
    if (sdk == null || manager == null) return;
    final rawChannel = sdk.callMethodVarArgs<JSObject>('newChannel'.toJS, [
      channel.id.toJS,
      channel.type.toJS,
    ]);
    final conversation = manager.callMethodVarArgs<JSObject?>(
      'findConversation'.toJS,
      [rawChannel],
    );
    if (conversation == null) return;
    conversation.setProperty('unread'.toJS, 0.toJS);
    manager.callMethodVarArgs<JSAny?>('notifyConversationListeners'.toJS, [
      conversation,
      1.toJS,
    ]);
    final reminderManager = _reminderManager;
    if (reminderManager == null) return;
    final reminders = reminderManager.callMethodVarArgs<JSArray<JSObject>>(
      'getWaitDoneReminders'.toJS,
      [rawChannel],
    );
    final ids = reminders.toDart
        .map((item) => _integer(item, 'reminderID'))
        .where((id) => id > 0)
        .toSet()
        .toList();
    if (ids.isEmpty) return;
    final rawIDs = ids.map((id) => id.toJS).toList().toJS;
    await reminderManager.callMethodVarArgs<JSPromise<JSAny?>>('done'.toJS, [
      rawIDs,
    ]).toDart;
  }

  @override
  Future<WukongMessage> send(WukongOutgoingMessage outgoing) async {
    _checkNotDisposed();
    if (_state != WukongConnectionState.connected) {
      throw StateError('WuKongIM is not connected');
    }
    final content = _newRawContent(
      (outgoing.payload['type'] as num?)?.toInt() ?? -1,
      outgoing.payload,
    );
    final channel = _sdk!.callMethodVarArgs<JSObject>('newChannel'.toJS, [
      outgoing.channel.id.toJS,
      outgoing.channel.type.toJS,
    ]);
    final options = _constructor('SendOptions').callAsConstructor<JSObject>();
    final setting = _constructor('Setting').callAsConstructor<JSObject>();
    final topic = outgoing.topic?.trim() ?? '';
    setting.setProperty('topic'.toJS, topic.isNotEmpty.toJS);
    options
      ..setProperty('setting'.toJS, setting)
      ..setProperty('noPersist'.toJS, outgoing.noPersist.toJS)
      ..setProperty('reddot'.toJS, outgoing.redDot.toJS);
    final packet = _chatManager!.callMethodVarArgs<JSObject>(
      'getSendPacketWithOptions'.toJS,
      [content, channel, options],
    );
    packet
      ..setProperty('noPersist'.toJS, outgoing.noPersist.toJS)
      ..setProperty('reddot'.toJS, outgoing.redDot.toJS)
      ..setProperty('syncOnce'.toJS, outgoing.syncOnce.toJS)
      ..setProperty('expire'.toJS, outgoing.expireSeconds.toJS)
      ..setProperty('topic'.toJS, topic.toJS);
    final requestedClientMsgNo = outgoing.clientMsgNo?.trim() ?? '';
    if (requestedClientMsgNo.isNotEmpty) {
      packet.setProperty('clientMsgNo'.toJS, requestedClientMsgNo.toJS);
    }
    final clientSeq = _integer(packet, 'clientSeq');
    _object(
      _chatManager!,
      'sendingQueues',
    ).callMethodVarArgs<JSAny?>('set'.toJS, [clientSeq.toJS, packet]);
    final raw = _constructor(
      'Message',
    ).callMethodVarArgs<JSObject>('fromSendPacket'.toJS, [packet, content]);
    _chatManager!
      ..callMethodVarArgs<JSAny?>('sendSendPacket'.toJS, [packet])
      ..callMethodVarArgs<JSAny?>('notifyMessageListeners'.toJS, [raw]);
    final mapped = _fromWebMessage(raw);
    _clientMsgBySeq[mapped.clientSeq] = mapped.clientMsgNo;
    return mapped;
  }

  JSObject _newRawContent(int type, [Map<String, Object?>? source]) {
    final payload = Map<String, Object?>.from(
      source ?? <String, Object?>{'type': type},
    );
    payload['type'] = type;
    final content = _sdk!.callMethod<JSObject>('newMessageContent'.toJS);
    final encode = (() => payload.jsify() as JSObject).toJS;
    final decode = ((JSObject value) {
      final decoded = value.dartify();
      if (decoded is Map) {
        payload
          ..clear()
          ..addAll(wukongObjectMap(decoded));
      }
    }).toJS;
    _callbacks.addAll([encode, decode]);
    content
      ..setProperty('contentType'.toJS, type.toJS)
      ..setProperty('contentObj'.toJS, payload.jsify())
      ..setProperty('encodeJSON'.toJS, encode)
      ..setProperty('decodeJSON'.toJS, decode);
    return content;
  }

  Future<JSArray<JSObject>> _syncWebConversations() async {
    final items = await _dataSource?.syncConversations(
      version: 0,
      lastMsgSeqs: '',
      messageCount: 1,
    );
    return (items ?? const <Map<String, Object?>>[])
        .map(_webConversation)
        .toList()
        .toJS;
  }

  Future<JSArray<JSObject>> _syncWebMessages(
    JSObject channel,
    JSObject options,
  ) async {
    final response = await _dataSource?.syncMessages(
      channel: WukongChannel(
        id: _string(channel, 'channelID'),
        type: _integer(channel, 'channelType'),
      ),
      startMessageSeq: _integer(options, 'startMessageSeq'),
      endMessageSeq: _integer(options, 'endMessageSeq'),
      limit: _integer(options, 'limit').clamp(1, 500),
      pullMode: _integer(options, 'pullMode'),
    );
    final messages = response?['messages'] as List<Object?>? ?? const [];
    return messages
        .map((value) => _webSyncedMessage(_map(value)))
        .toList()
        .toJS;
  }

  JSObject _webConversation(Map<String, Object?> value) {
    final conversation = _constructor(
      'Conversation',
    ).callAsConstructor<JSObject>();
    final channel = _channelFromMap(value);
    conversation
      ..setProperty('channel'.toJS, channel)
      ..setProperty('unread'.toJS, _int(value['unread']).toJS)
      ..setProperty('timestamp'.toJS, _int(value['timestamp']).toJS)
      ..setProperty('extra'.toJS, <String, Object?>{}.jsify());
    final recents = value['recents'] as List<Object?>? ?? const [];
    if (recents.isNotEmpty) {
      conversation.setProperty(
        'lastMessage'.toJS,
        _webSyncedMessage(_map(recents.first)),
      );
    }
    return conversation;
  }

  JSObject _webSyncedMessage(Map<String, Object?> value) {
    final message = _constructor('Message').callAsConstructor<JSObject>();
    final payload = projectWukongStreamPayload(value);
    final content = _newRawContent(_int(payload['type']), payload);
    message
      ..setProperty(
        'messageID'.toJS,
        (_stringValue(value['message_idstr']).isNotEmpty
                ? _stringValue(value['message_idstr'])
                : _stringValue(value['message_id']))
            .toJS,
      )
      ..setProperty('messageSeq'.toJS, _int(value['message_seq']).toJS)
      ..setProperty(
        'clientMsgNo'.toJS,
        _stringValue(value['client_msg_no']).toJS,
      )
      ..setProperty('fromUID'.toJS, _stringValue(value['from_uid']).toJS)
      ..setProperty('channel'.toJS, _channelFromMap(value))
      ..setProperty('timestamp'.toJS, _int(value['timestamp']).toJS)
      ..setProperty('streamNo'.toJS, _stringValue(value['stream_no']).toJS)
      ..setProperty('streamSeq'.toJS, _int(value['stream_seq']).toJS)
      ..setProperty('streamFlag'.toJS, _int(value['stream_flag']).toJS)
      ..setProperty('status'.toJS, 1.toJS)
      ..setProperty('content'.toJS, content);
    return message;
  }

  JSObject _channelFromMap(Map<String, Object?> value) =>
      _constructor('Channel').callAsConstructorVarArgs<JSObject>([
        _stringValue(value['channel_id']).toJS,
        _int(value['channel_type']).toJS,
      ]);

  WukongMessage _fromWebMessage(JSObject raw) {
    final channel = _object(raw, 'channel');
    final content = _object(raw, 'content');
    final payload = _map(
      content.getProperty<JSAny?>('contentObj'.toJS)?.dartify(),
    );
    final status = _integer(raw, 'status');
    return WukongMessage(
      messageId: _valueString(raw.getProperty<JSAny?>('messageID'.toJS)),
      messageSeq: _integer(raw, 'messageSeq'),
      clientMsgNo: _string(raw, 'clientMsgNo'),
      clientSeq: _integer(raw, 'clientSeq'),
      fromUid: _string(raw, 'fromUID'),
      channel: WukongChannel(
        id: _string(channel, 'channelID'),
        type: _integer(channel, 'channelType'),
      ),
      timestamp: _fromSeconds(_integer(raw, 'timestamp')),
      payload: payload,
      state: status == 0
          ? WukongMessageState.sending
          : status == 1
          ? WukongMessageState.sent
          : WukongMessageState.failed,
      reasonCode: status,
      streamNo: _string(raw, 'streamNo'),
      streamSeq: _integer(raw, 'streamSeq'),
      streamFlag: _integer(raw, 'streamFlag'),
    );
  }

  JSFunction _constructor(String name) =>
      _wkGlobal!.getProperty<JSFunction>(name.toJS);

  void _setState(WukongConnectionState value) {
    if (_state == value || _disposed) return;
    _state = value;
    _states.add(value);
  }

  @override
  Future<void> dispose() async {
    if (_disposed) return;
    await disconnect(logout: true);
    final eventManager = _eventManager;
    final eventCallback = _eventCallback;
    if (eventManager != null && eventCallback != null) {
      eventManager.callMethod<JSAny?>(
        'removeEventListener'.toJS,
        eventCallback,
      );
    }
    _eventCallback = null;
    _disposed = true;
    _callbacks.clear();
    await _states.close();
    await _events.close();
    await _sendResults.close();
  }

  void _checkNotDisposed() {
    if (_disposed) throw StateError('WuKongIM gateway is disposed');
  }
}

JSObject _object(JSObject source, String name) =>
    source.getProperty<JSObject>(name.toJS);

int _integer(JSObject source, String name) =>
    _int(source.getProperty<JSAny?>(name.toJS)?.dartify());

String _string(JSObject source, String name) =>
    _valueString(source.getProperty<JSAny?>(name.toJS));

String _valueString(JSAny? value) {
  if (value == null) return '';
  final dart = value.dartify();
  if (dart is String || dart is num) return dart.toString();
  if (value.isA<JSObject>()) {
    return (value as JSObject).callMethod<JSString>('toString'.toJS).toDart;
  }
  return '';
}

Map<String, Object?> _map(Object? value) => wukongObjectMap(value);

int _int(Object? value) =>
    value is num ? value.toInt() : int.tryParse(value?.toString() ?? '') ?? 0;

String _stringValue(Object? value) => value?.toString() ?? '';

DateTime _fromSeconds(int value) =>
    DateTime.fromMillisecondsSinceEpoch(value * 1000, isUtc: true);
