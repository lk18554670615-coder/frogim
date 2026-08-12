import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_callkit_incoming/entities/android_params.dart';
import 'package:flutter_callkit_incoming/entities/call_event.dart';
import 'package:flutter_callkit_incoming/entities/call_kit_params.dart';
import 'package:flutter_callkit_incoming/entities/ios_params.dart';
import 'package:flutter_callkit_incoming/entities/notification_params.dart';
import 'package:flutter_callkit_incoming/flutter_callkit_incoming.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'call_models.dart';
import 'system_call_service_contract.dart';

const _pendingActionsKey = 'calls.pending_system_actions.v1';
const _permissionPromptedKey = 'calls.system_permission_prompted.v1';
const _systemCallChannel = MethodChannel('com.linlitong.imapp/system_calls');

SystemCallService createSystemCallService() => NativeSystemCallService();

/// 由 Android 插件的无头 FlutterEngine 调用，只保存最小动作，主引擎恢复后再鉴权请求服务端。
@pragma('vm:entry-point')
Future<void> linliSystemCallBackgroundHandler(CallEvent event) async {
  WidgetsFlutterBinding.ensureInitialized();
  final action = _serializedAction(event);
  if (action == null) return;
  final preferences = await SharedPreferences.getInstance();
  final pending = preferences.getStringList(_pendingActionsKey) ?? <String>[];
  final encoded = jsonEncode(action);
  if (!pending.contains(encoded)) {
    pending.add(encoded);
    await preferences.setStringList(
      _pendingActionsKey,
      pending.length > 12 ? pending.sublist(pending.length - 12) : pending,
    );
  }
}

class NativeSystemCallService implements SystemCallService {
  final _actions = StreamController<SystemCallAction>.broadcast();
  final Map<String, String> _systemToServer = {};
  final Map<String, String> _serverToSystem = {};
  StreamSubscription<CallEvent?>? _subscription;
  Future<void>? _initialization;
  bool _disposed = false;
  final Set<String> _emittedActionKeys = {};

  @override
  Stream<SystemCallAction> get actions => _actions.stream;

  @override
  Future<void> initialize() => _initialization ??= _initialize();

  Future<void> _initialize() async {
    try {
      _subscription = FlutterCallkitIncoming.onEvent.listen(_handleEvent);
    } on MissingPluginException {
      // 桌面与纯 Dart 测试没有原生通话插件。
      return;
    }
    if (Platform.isAndroid) {
      try {
        await FlutterCallkitIncoming.onBackgroundMessage(
          linliSystemCallBackgroundHandler,
        );
      } on MissingPluginException {
        // 测试环境没有 Android 原生插件。
      } on PlatformException catch (error) {
        if (kDebugMode) {
          if (kDebugMode) {
            debugPrint('Background call actions unavailable: ${error.code}');
          }
        }
      }
    }
    try {
      await _drainAndroidLaunchActions();
      await _restoreActiveCalls();
      await _drainBackgroundActions();
    } on MissingPluginException {
      // 桌面与纯 Dart 测试没有原生通话插件。
    } on PlatformException catch (error) {
      if (kDebugMode) {
        if (kDebugMode) {
          debugPrint('System call integration unavailable: ${error.code}');
        }
      }
    }
  }

  @override
  Future<void> preparePermissions() async {
    if (!Platform.isAndroid) {
      return;
    }
    await initialize();
    final preferences = await SharedPreferences.getInstance();
    if (preferences.getBool(_permissionPromptedKey) ?? false) {
      return;
    }
    try {
      await FlutterCallkitIncoming.requestNotificationPermission({
        'title': '允许来电通知',
        'rationaleMessagePermission': '用于在锁屏或后台及时显示语音和视频来电。',
        'postNotificationMessageRequired': '请在系统设置中开启通知，才能接收来电提醒。',
      });
      if (!await FlutterCallkitIncoming.canUseFullScreenIntent()) {
        await FlutterCallkitIncoming.requestFullIntentPermission();
      }
      await preferences.setBool(_permissionPromptedKey, true);
    } on MissingPluginException {
      // 纯 Dart 测试没有原生插件。
    } on PlatformException catch (error) {
      if (kDebugMode) {
        if (kDebugMode) {
          debugPrint('System call permission deferred: ${error.code}');
        }
      }
    }
  }

  @override
  Future<bool> showIncoming({
    required CallSession session,
    required String callerName,
    String? callerHandle,
    String? avatarUrl,
  }) async {
    await initialize();
    final systemId = systemCallIdFor(session.id);
    _systemToServer[systemId] = session.id;
    _serverToSystem[session.id] = systemId;
    try {
      await FlutterCallkitIncoming.showCallkitIncoming(
        _params(
          session: session,
          systemId: systemId,
          displayName: callerName,
          handle: callerHandle,
          avatarUrl: avatarUrl,
        ),
      );
      return true;
    } on MissingPluginException {
      return false;
    } on PlatformException catch (error) {
      if (kDebugMode) debugPrint('Show incoming call failed: ${error.code}');
      return false;
    }
  }

  @override
  Future<void> showOutgoing({
    required CallSession session,
    required String calleeName,
    String? calleeHandle,
    String? avatarUrl,
  }) async {
    await initialize();
    final systemId = systemCallIdFor(session.id);
    _systemToServer[systemId] = session.id;
    _serverToSystem[session.id] = systemId;
    try {
      await FlutterCallkitIncoming.startCall(
        _params(
          session: session,
          systemId: systemId,
          displayName: calleeName,
          handle: calleeHandle,
          avatarUrl: avatarUrl,
        ),
      );
    } on MissingPluginException {
      // 测试与非移动平台忽略系统通话展示。
    }
  }

  @override
  Future<void> setConnected(String serverCallId) async {
    try {
      await FlutterCallkitIncoming.setCallConnected(
        _serverToSystem[serverCallId] ?? systemCallIdFor(serverCallId),
      );
    } on MissingPluginException {
      // 纯 Dart 测试没有原生插件。
    }
  }

  @override
  Future<void> setMuted(String serverCallId, bool muted) async {
    try {
      await FlutterCallkitIncoming.muteCall(
        _serverToSystem[serverCallId] ?? systemCallIdFor(serverCallId),
        isMuted: muted,
      );
    } on MissingPluginException {
      // 纯 Dart 测试没有原生插件。
    }
  }

  @override
  Future<void> end(String serverCallId) async {
    final systemId =
        _serverToSystem.remove(serverCallId) ?? systemCallIdFor(serverCallId);
    _systemToServer.remove(systemId);
    try {
      await FlutterCallkitIncoming.endCall(systemId);
    } on MissingPluginException {
      // 纯 Dart 测试没有原生插件。
    }
  }

  @override
  Future<String?> voipPushToken() async {
    if (!Platform.isIOS) {
      return null;
    }
    try {
      final value = (await FlutterCallkitIncoming.getDevicePushTokenVoIP())
          ?.trim();
      return value == null || value.isEmpty ? null : value;
    } on MissingPluginException {
      return null;
    }
  }

  CallKitParams _params({
    required CallSession session,
    required String systemId,
    required String displayName,
    required String? handle,
    required String? avatarUrl,
  }) {
    final seconds = session.expiresAt.difference(DateTime.now()).inSeconds;
    return CallKitParams(
      id: systemId,
      nameCaller: displayName.isEmpty ? '邻里联系人' : displayName,
      appName: '邻里通讯',
      avatar: avatarUrl,
      handle: handle?.isNotEmpty == true ? handle : '邻里通讯',
      type: session.mediaType == CallMediaType.video ? 1 : 0,
      duration: (seconds.clamp(1, 120)) * 1000,
      extra: {
        'serverCallId': session.id,
        'conversationId': session.conversationId,
        'mediaType': session.mediaType.name,
      },
      missedCallNotification: const NotificationParams(
        showNotification: true,
        isShowCallback: false,
        subtitle: '未接来电',
      ),
      callingNotification: const NotificationParams(
        showNotification: true,
        isShowCallback: true,
        subtitle: '通话进行中',
        callbackText: '挂断',
      ),
      android: const AndroidParams(
        isCustomNotification: true,
        isShowLogo: false,
        isShowCallID: false,
        ringtonePath: 'system_ringtone_default',
        backgroundColor: '#07101F',
        actionColor: '#34C759',
        textColor: '#FFFFFF',
        incomingCallNotificationChannelName: '音视频来电',
        missedCallNotificationChannelName: '未接来电',
        isShowFullLockedScreen: true,
        isImportant: true,
        isFullScreen: true,
        textAccept: '接听',
        textDecline: '拒绝',
      ),
      ios: IOSParams(
        handleType: 'generic',
        normalHandle: 1,
        supportsVideo: true,
        maximumCallGroups: 1,
        maximumCallsPerCallGroup: 1,
        supportsDTMF: false,
        supportsHolding: false,
        supportsGrouping: false,
        supportsUngrouping: false,
        includesCallsInRecents: false,
        configureAudioSession: true,
        audioSessionMode: 'voiceChat',
        audioSessionActive: true,
        audioSessionPreferredSampleRate: 48000,
        audioSessionPreferredIOBufferDuration: 0.005,
        ringtonePath: 'system_ringtone_default',
      ),
    );
  }

  void _handleEvent(CallEvent? event) {
    if (event == null || _disposed) return;
    final action = _actionFromEvent(event, _systemToServer);
    if (action != null) _emitAction(action);
  }

  Future<void> _drainAndroidLaunchActions() async {
    if (!Platform.isAndroid) return;
    final pending = await _systemCallChannel.invokeListMethod<Object?>(
      'drainLaunchActions',
    );
    for (final raw in pending ?? const <Object?>[]) {
      if (raw is! Map) continue;
      final action = systemCallActionFromMap(raw);
      if (action == null) continue;
      _systemToServer[action.systemCallId] = action.serverCallId;
      _serverToSystem[action.serverCallId] = action.systemCallId;
      _emitAction(action);
    }
  }

  Future<void> _restoreActiveCalls() async {
    final active = await FlutterCallkitIncoming.activeCalls();
    for (final params in active) {
      final serverId = _serverId(params);
      if (serverId == null) continue;
      _systemToServer[params.id] = serverId;
      _serverToSystem[serverId] = params.id;
      if (params.isAccepted) {
        _emitAction(
          SystemCallAction(
            type: SystemCallActionType.accept,
            serverCallId: serverId,
            systemCallId: params.id,
          ),
        );
      }
    }
  }

  Future<void> _drainBackgroundActions() async {
    final preferences = await SharedPreferences.getInstance();
    final pending = preferences.getStringList(_pendingActionsKey) ?? const [];
    if (pending.isEmpty) return;
    await preferences.remove(_pendingActionsKey);
    for (final raw in pending) {
      try {
        final json = jsonDecode(raw) as Map<String, Object?>;
        final type = SystemCallActionType.values.byName(
          json['type']! as String,
        );
        final serverId = json['serverCallId']! as String;
        final systemId = json['systemCallId']! as String;
        _systemToServer[systemId] = serverId;
        _serverToSystem[serverId] = systemId;
        _emitAction(
          SystemCallAction(
            type: type,
            serverCallId: serverId,
            systemCallId: systemId,
            muted: json['muted'] as bool?,
          ),
        );
      } catch (_) {
        // 损坏或旧版本动作直接丢弃，避免误操作其他通话。
      }
    }
  }

  void _emitAction(SystemCallAction action) {
    if (_disposed) return;
    final key =
        '${action.type.name}|${action.serverCallId}|'
        '${action.systemCallId}|${action.muted}';
    if (!_emittedActionKeys.add(key)) return;
    if (_emittedActionKeys.length > 64) {
      _emittedActionKeys.remove(_emittedActionKeys.first);
    }
    _actions.add(action);
  }

  @override
  Future<void> dispose() async {
    _disposed = true;
    await _subscription?.cancel();
    await _actions.close();
  }
}

String systemCallIdFor(String serverCallId) {
  final bytes = sha256.convert(utf8.encode(serverCallId)).bytes.toList();
  bytes[6] = (bytes[6] & 0x0f) | 0x50;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  final hex = bytes
      .take(16)
      .map((value) => value.toRadixString(16).padLeft(2, '0'))
      .join();
  return '${hex.substring(0, 8)}-${hex.substring(8, 12)}-${hex.substring(12, 16)}-'
      '${hex.substring(16, 20)}-${hex.substring(20, 32)}';
}

SystemCallAction? _actionFromEvent(
  CallEvent event,
  Map<String, String> systemToServer,
) {
  final params = switch (event) {
    CallEventActionCallAccept(:final callKitParams) => callKitParams,
    CallEventActionCallDecline(:final callKitParams) => callKitParams,
    CallEventActionCallEnded(:final callKitParams) => callKitParams,
    CallEventActionCallToggleMute() => null,
    _ => null,
  };
  final systemId = switch (event) {
    CallEventActionCallTimeout(:final id) => id,
    CallEventActionCallToggleMute(:final id) => id,
    _ => params?.id,
  };
  if (systemId == null) return null;
  final serverId = _serverId(params) ?? systemToServer[systemId];
  if (serverId == null || serverId.isEmpty) return null;
  final (type, muted) = switch (event) {
    CallEventActionCallAccept() => (SystemCallActionType.accept, null),
    CallEventActionCallDecline() => (SystemCallActionType.decline, null),
    CallEventActionCallEnded() => (SystemCallActionType.end, null),
    CallEventActionCallTimeout() => (SystemCallActionType.timeout, null),
    CallEventActionCallToggleMute(:final isMuted) => (
      SystemCallActionType.mute,
      isMuted,
    ),
    _ => (null, null),
  };
  if (type == null) return null;
  return SystemCallAction(
    type: type,
    serverCallId: serverId,
    systemCallId: systemId,
    muted: muted,
  );
}

String? _serverId(CallKitParams? params) =>
    params?.extra?['serverCallId']?.toString();

Map<String, Object?>? _serializedAction(CallEvent event) {
  final action = _actionFromEvent(event, const {});
  if (action == null) return null;
  return {
    'type': action.type.name,
    'serverCallId': action.serverCallId,
    'systemCallId': action.systemCallId,
    if (action.muted != null) 'muted': action.muted,
  };
}

SystemCallAction? systemCallActionFromMap(Map<Object?, Object?> json) {
  final typeName = json['type']?.toString();
  final serverCallId = json['serverCallId']?.toString();
  final systemCallId = json['systemCallId']?.toString();
  if (typeName == null ||
      serverCallId == null ||
      serverCallId.isEmpty ||
      systemCallId == null ||
      systemCallId.isEmpty) {
    return null;
  }
  final type = SystemCallActionType.values
      .where((value) => value.name == typeName)
      .firstOrNull;
  if (type == null) return null;
  return SystemCallAction(
    type: type,
    serverCallId: serverCallId,
    systemCallId: systemCallId,
    muted: json['muted'] as bool?,
  );
}
