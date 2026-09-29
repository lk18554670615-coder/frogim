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
import 'native_push_startup.dart';
import 'push_service_contract.dart';

PlatformPushService createPlatformPushService() => _GetuiPushService();

class _GetuiPushService implements PlatformPushService {
  final Getuiflut _plugin = Getuiflut();
  final NativePushStartup _startup = NativePushStartup();
  String? _cid;
  String? _lastRegistrationFingerprint;
  String? _lastVoipRegistrationFingerprint;
  int? _lastBadge;
  bool _syncing = false;
  bool _syncAgain = false;
  Object? _session;
  bool _disposed = false;
  bool _permissionRequested = false;
  Timer? _voipRetry;

  @override
  Future<void> initialize(AppController controller) async {
    if ((!AppConfig.getuiEnabled && !Platform.isIOS) ||
        (!Platform.isAndroid && !Platform.isIOS)) {
      return;
    }
    if (AppConfig.getuiEnabled) {
      _plugin.addEventHandler(
        onVoipRegistrationChanged: (_) async => sync(controller),
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
    }
    unawaited(
      _startup.run(
        getuiConfigured:
            AppConfig.getuiEnabled &&
            (!Platform.isIOS ||
                AppConfig.getuiAppId.isNotEmpty &&
                    AppConfig.getuiAppKey.isNotEmpty &&
                    AppConfig.getuiAppSecret.isNotEmpty),
        active: () => !_disposed,
        startGetui: () async {
          if (Platform.isIOS) {
            await _plugin.startSdk(
              appId: AppConfig.getuiAppId,
              appKey: AppConfig.getuiAppKey,
              appSecret: AppConfig.getuiAppSecret,
            );
          } else if (Platform.isAndroid) {
            await _plugin.initGetuiSdk;
          }
        },
        restoreGetui: () => _loadInitialState(controller),
        syncProviders: () => sync(controller),
      ),
    );
  }

  Future<void> _loadInitialState(AppController controller) async {
    if (_disposed) return;
    if (_startup.getuiReady) {
      final cid = (await _plugin.getClientId).trim();
      if (cid.isNotEmpty) _cid = cid;
    }
    if (_startup.getuiReady && Platform.isIOS) {
      final launch = await _plugin.getLaunchNotification;
      if (launch.isNotEmpty) {
        controller.handlePushPayload(
          launch.map((key, value) => MapEntry('$key', value)),
        );
      }
    }
  }

  @override
  Future<void> sync(AppController controller) async {
    if ((!AppConfig.getuiEnabled && !Platform.isIOS) ||
        _disposed ||
        (!Platform.isAndroid && !Platform.isIOS)) {
      return;
    }
    if (_syncing) {
      _syncAgain = true;
      return;
    }
    final session = controller.pushSession;
    if (!identical(session, _session)) {
      _session = session;
      _lastRegistrationFingerprint = null;
      _lastVoipRegistrationFingerprint = null;
    }
    bool current() =>
        !_disposed &&
        session != null &&
        identical(session, controller.pushSession);
    _syncing = true;
    try {
      if (session == null) {
        if (_startup.getuiReady && _lastBadge != 0) {
          _plugin.setBadge(0);
          _lastBadge = 0;
        }
        return;
      }
      final providers = await controller.allowedPushProviders();
      if (current() &&
          Platform.isIOS &&
          controller.usesTenantAuthentication &&
          (!providers.contains('getui_voip') || !_startup.getuiReady)) {
        await controller.revokeVoipPushDevice(session);
        if (!current()) return;
        controller.setVoipPushStatus('来电推送未就绪：等待平台和推送服务启用', session);
      }
      if (!current() ||
          !(_startup.getuiReady && providers.contains('getui') ||
              _startup.getuiReady &&
                  Platform.isIOS &&
                  providers.contains('getui_voip') ||
              Platform.isIOS && providers.contains('apns_voip'))) {
        return;
      }
      if (!_permissionRequested) {
        _permissionRequested = true;
        await Permission.notification.request();
        if (!current()) return;
        await controller.callController?.prepareSystemCallPermissions();
        if (!current()) return;
      }
      final badge = controller.authenticated
          ? controller.notificationUnreadCount
          : 0;
      if (_startup.getuiReady && _lastBadge != badge) {
        _plugin.setBadge(badge);
        _lastBadge = badge;
      }
      final cid = _cid;
      final userId = controller.currentUser?.id;
      final preferences = await SharedPreferences.getInstance();
      if (!current()) return;
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
      // Native identity acknowledgement is part of managed login; a binding
      // becomes usable only after the platform returns its exact lease.
      if (controller.usesTenantAuthentication &&
          Platform.isIOS &&
          providers.contains('getui_voip') &&
          _startup.getuiReady &&
          userId != null) {
        final registration = await _plugin.voipRegistration;
        if (!current()) return;
        final voipCID = registration['cid'];
        final voipDevice = registration['deviceId'];
        if (registration['ready'] != true ||
            voipCID is! String ||
            voipCID.isEmpty ||
            voipDevice is! String ||
            voipDevice.isEmpty) {
          await controller.revokeVoipPushDevice(session);
          if (!current()) return;
          _lastVoipRegistrationFingerprint = null;
          controller.setVoipPushStatus('来电推送未就绪：等待系统令牌登记', session);
        } else {
          final fingerprint = '$voipDevice|$voipCID|$registrationFingerprint';
          if (_lastVoipRegistrationFingerprint != fingerprint) {
            await controller.revokeVoipPushDevice(session);
            if (!current()) return;
            await controller.registerVoipPushDevice(
              deviceId: voipDevice,
              token: voipCID,
              notificationsEnabled: notificationsEnabled,
              previewEnabled: previewEnabled,
              soundEnabled: soundEnabled,
              vibrationEnabled: vibrationEnabled,
              expectedPushSession: session,
            );
            if (!current()) return;
            _lastVoipRegistrationFingerprint = fingerprint;
          }
          controller.setVoipPushStatus(
            notificationsEnabled ? '来电推送已登记' : '来电推送已关闭',
            session,
          );
        }
      }
      if (!controller.usesTenantAuthentication &&
          providers.contains('apns_voip') &&
          Platform.isIOS &&
          userId != null) {
        final voipToken = await controller.callController?.voipPushToken();
        if (!current()) return;
        if (voipToken == null || voipToken.isEmpty) {
          _voipRetry?.cancel();
          _voipRetry = Timer(const Duration(seconds: 5), () {
            if (current()) unawaited(sync(controller));
          });
        } else {
          _voipRetry?.cancel();
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
              expectedPushSession: session,
            );
            if (!current()) return;
            _lastVoipRegistrationFingerprint = voipFingerprint;
          }
        }
      }
      if (_startup.getuiReady &&
          providers.contains('getui') &&
          current() &&
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
          expectedPushSession: session,
        );
        if (!current()) return;
        _lastRegistrationFingerprint = registrationFingerprint;
      }
      if (!controller.authenticated) {
        _lastRegistrationFingerprint = null;
        _lastVoipRegistrationFingerprint = null;
      }
    } on PlatformException {
      if (current() && Platform.isIOS && controller.usesTenantAuthentication) {
        controller.setVoipPushStatus('来电推送未就绪：登记失败，将重试', session!);
      }
      if (kDebugMode) debugPrint('Getui sync deferred');
    } catch (_) {
      if (current() && Platform.isIOS && controller.usesTenantAuthentication) {
        controller.setVoipPushStatus('来电推送未就绪：登记失败，将重试', session!);
      }
      if (kDebugMode) debugPrint('Getui device registration deferred');
    } finally {
      _syncing = false;
      if (current() && Platform.isIOS && controller.usesTenantAuthentication) {
        _voipRetry?.cancel();
        _voipRetry = Timer(const Duration(seconds: 5), () {
          if (current()) unawaited(sync(controller));
        });
      }
      if (_syncAgain && !_disposed) {
        _syncAgain = false;
        unawaited(sync(controller));
      }
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
    _voipRetry?.cancel();
  }
}
