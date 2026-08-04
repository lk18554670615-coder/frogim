import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:getuiflut/getuiflut.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'app_config.dart';
import 'app_controller.dart';
import 'push_service_contract.dart';

PlatformPushService createPlatformPushService() => _GetuiPushService();

class _GetuiPushService implements PlatformPushService {
  final Getuiflut _plugin = Getuiflut();
  String? _cid;
  String? _lastRegistrationFingerprint;
  String? _lastVoipRegistrationFingerprint;
  int? _lastBadge;
  bool _syncing = false;
  bool _disposed = false;
  bool _permissionRequested = false;

  @override
  Future<void> initialize(AppController controller) async {
    if (!AppConfig.getuiEnabled) return;
    _plugin.addEventHandler(
      onReceiveClientId: (value) async {
        _cid = value.trim();
        await sync(controller);
      },
      onNotificationMessageArrived: (message) async {
        controller.handlePushPayload(message);
      },
      onNotificationMessageClicked: (message) async {
        controller.handlePushPayload(message);
      },
      onTransmitUserMessageReceive: (message) async {
        controller.handlePushPayload(message);
      },
      onReceiveOnlineState: (_) async {},
      onRegisterDeviceToken: (_) async {},
      onReceivePayload: (message) async {
        controller.handlePushPayload(message);
      },
      onReceiveNotificationResponse: (message) async {
        controller.handlePushPayload(message);
      },
      onAppLinkPayload: (value) async {
        final payload = _tryDecode(value);
        if (payload != null) controller.handlePushPayload(payload);
      },
      onPushModeResult: (_) async {},
      onSetTagResult: (_) async {},
      onAliasResult: (_) async {},
      onQueryTagResult: (_) async {},
      onWillPresentNotification: (message) async {
        controller.handlePushPayload(message);
      },
      onOpenSettingsForNotification: (_) async {},
      onGrantAuthorization: (_) async {},
      onLiveActivityResult: (_) async {},
      onRegisterPushToStartTokenResult: (_) async {},
    );
    try {
      if (Platform.isIOS) {
        if (AppConfig.getuiAppId.isEmpty ||
            AppConfig.getuiAppKey.isEmpty ||
            AppConfig.getuiAppSecret.isEmpty) {
          return;
        }
        _plugin.startSdk(
          appId: AppConfig.getuiAppId,
          appKey: AppConfig.getuiAppKey,
          appSecret: AppConfig.getuiAppSecret,
        );
      } else if (Platform.isAndroid) {
        _plugin.initGetuiSdk;
      }
      unawaited(_loadInitialState(controller));
    } on MissingPluginException {
      // Native push is intentionally unavailable in widget/desktop tests.
    }
  }

  Future<void> _loadInitialState(AppController controller) async {
    await Future<void>.delayed(const Duration(milliseconds: 800));
    if (_disposed) return;
    try {
      final cid = (await _plugin.getClientId).trim();
      if (cid.isNotEmpty) _cid = cid;
      if (Platform.isIOS) {
        final launch = await _plugin.getLaunchNotification;
        if (launch.isNotEmpty) {
          controller.handlePushPayload(
            launch.map((key, value) => MapEntry('$key', value)),
          );
        }
      }
      await sync(controller);
    } on PlatformException catch (error) {
      if (kDebugMode) {
        debugPrint('Getui initialization deferred: ${error.code}');
      }
    }
  }

  @override
  Future<void> sync(AppController controller) async {
    if (!AppConfig.getuiEnabled || _disposed || _syncing) return;
    _syncing = true;
    try {
      if (controller.authenticated && !_permissionRequested) {
        _permissionRequested = true;
        await Permission.notification.request();
        await controller.callController?.prepareSystemCallPermissions();
      }
      final badge = controller.authenticated
          ? controller.notificationUnreadCount
          : 0;
      if (_lastBadge != badge) {
        _plugin.setBadge(badge);
        _lastBadge = badge;
      }
      final cid = _cid;
      final userId = controller.currentUser?.id;
      final preferences = await SharedPreferences.getInstance();
      final notificationsEnabled =
          preferences.getBool('settings.notification.enabled') ?? true;
      final previewEnabled =
          preferences.getBool('settings.notification.preview') ?? true;
      final soundEnabled =
          preferences.getBool('settings.notification.sound') ?? true;
      final vibrationEnabled =
          preferences.getBool('settings.notification.vibration') ?? true;
      final registrationFingerprint = [
        userId,
        cid,
        notificationsEnabled,
        previewEnabled,
        soundEnabled,
        vibrationEnabled,
      ].join('|');
      if (controller.authenticated && Platform.isIOS && userId != null) {
        final voipToken = await controller.callController?.voipPushToken();
        if (voipToken != null && voipToken.isNotEmpty) {
          final voipFingerprint = [
            userId,
            voipToken,
            notificationsEnabled,
            previewEnabled,
            soundEnabled,
            vibrationEnabled,
          ].join('|');
          if (_lastVoipRegistrationFingerprint != voipFingerprint) {
            final digest = sha256.convert(utf8.encode(voipToken)).toString();
            await controller.registerVoipPushDevice(
              deviceId: 'apns-voip-${digest.substring(0, 24)}',
              token: voipToken,
              notificationsEnabled: notificationsEnabled,
              previewEnabled: previewEnabled,
              soundEnabled: soundEnabled,
              vibrationEnabled: vibrationEnabled,
            );
            _lastVoipRegistrationFingerprint = voipFingerprint;
          }
        }
      }
      if (controller.authenticated &&
          cid != null &&
          cid.isNotEmpty &&
          userId != null &&
          _lastRegistrationFingerprint != registrationFingerprint) {
        final platform = Platform.isIOS ? 'ios' : 'android';
        final digest = sha256.convert(utf8.encode(cid)).toString();
        await controller.registerPushDevice(
          deviceId: 'getui-$platform-${digest.substring(0, 24)}',
          platform: platform,
          cid: cid,
          notificationsEnabled: notificationsEnabled,
          previewEnabled: previewEnabled,
          soundEnabled: soundEnabled,
          vibrationEnabled: vibrationEnabled,
        );
        _lastRegistrationFingerprint = registrationFingerprint;
      }
      if (!controller.authenticated) {
        _lastRegistrationFingerprint = null;
        _lastVoipRegistrationFingerprint = null;
      }
    } on PlatformException catch (error) {
      if (kDebugMode) debugPrint('Getui sync deferred: ${error.code}');
    } catch (error) {
      if (kDebugMode) debugPrint('Getui device registration deferred: $error');
    } finally {
      _syncing = false;
    }
  }

  Map<String, dynamic>? _tryDecode(String value) {
    try {
      final decoded = jsonDecode(value);
      return decoded is Map
          ? decoded.map((key, value) => MapEntry('$key', value))
          : null;
    } catch (_) {
      return null;
    }
  }

  @override
  Future<void> dispose() async {
    _disposed = true;
  }
}
