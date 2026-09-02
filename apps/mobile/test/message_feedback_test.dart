import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/message_feedback.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/im_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('iOS notification tone is packaged as a short PCM WAV asset', () async {
    final bytes = await rootBundle.load('assets/sounds/message.wav');
    expect(String.fromCharCodes(bytes.buffer.asUint8List(0, 4)), 'RIFF');
    expect(String.fromCharCodes(bytes.buffer.asUint8List(8, 4)), 'WAVE');
    expect(bytes.getUint16(20, Endian.little), 1);
    expect(bytes.getUint32(24, Endian.little), 22050);
    expect(bytes.lengthInBytes, 10628);
  });

  test(
    'notification preferences default on and reuse the existing keys',
    () async {
      var prefs = await MessageFeedbackPreferences.load();
      expect([prefs.enabled, prefs.sound, prefs.vibration], [true, true, true]);
      SharedPreferences.setMockInitialValues({
        'settings.notification.enabled': false,
        'settings.notification.sound': false,
        'settings.notification.vibration': false,
      });
      prefs = await MessageFeedbackPreferences.load();
      expect(
        [prefs.enabled, prefs.sound, prefs.vibration],
        [false, false, false],
      );
    },
  );

  for (final flags in [
    (true, true),
    (true, false),
    (false, true),
    (false, false),
  ]) {
    test('sound/vibration switches ${flags.$1}/${flags.$2}', () async {
      final calls = <(bool, bool)>[];
      final feedback = MessageFeedback(
        preferences: () async =>
            MessageFeedbackPreferences(sound: flags.$1, vibration: flags.$2),
        play: ({required sound, required vibration}) async {
          calls.add((sound, vibration));
        },
      )..setAccount('me');
      await feedback.received(_message('1'), eligible: () => true);
      expect(calls, flags == (false, false) ? isEmpty : [flags]);
      feedback.dispose();
    });
  }

  test('master switch suppresses both sound and vibration', () async {
    var plays = 0;
    final feedback = MessageFeedback(
      preferences: () async => const MessageFeedbackPreferences(enabled: false),
      play: ({required sound, required vibration}) async {
        plays++;
      },
    )..setAccount('me');
    await feedback.received(_message('1'), eligible: () => true);
    expect(plays, 0);
    feedback.dispose();
  });

  for (final invalidate in [
    'background',
    'logout',
    'switch account',
    'dispose',
    'mute',
  ]) {
    test('pending alert invalidated by $invalidate', () async {
      var plays = 0;
      var eligible = true;
      final prefs = Completer<MessageFeedbackPreferences>();
      final feedback = MessageFeedback(
        preferences: () => prefs.future,
        play: ({required sound, required vibration}) async {
          plays++;
        },
      )..setAccount('me');
      final pending = feedback.received(
        _message('1'),
        eligible: () => eligible,
      );
      switch (invalidate) {
        case 'background':
          feedback.setForeground(false);
        case 'logout':
          feedback.setAccount(null);
        case 'switch account':
          feedback.setAccount('other');
        case 'dispose':
          feedback.dispose();
        case 'mute':
          eligible = false;
      }
      prefs.complete(const MessageFeedbackPreferences());
      await pending;
      expect(plays, 0);
      feedback.dispose();
    });
  }

  test(
    'background arrivals do not replay on resume; duplicates and bursts coalesce',
    () async {
      var plays = 0;
      var now = DateTime(2026);
      final feedback = MessageFeedback(
        preferences: () async => const MessageFeedbackPreferences(),
        now: () => now,
        play: ({required sound, required vibration}) async {
          plays++;
        },
      )..setAccount('me');
      feedback.setForeground(false);
      await feedback.received(_message('background'), eligible: () => true);
      feedback.setForeground(true);
      await feedback.received(_message('background'), eligible: () => true);
      expect(plays, 0);
      await Future.wait([
        for (var i = 0; i < 10; i++)
          feedback.received(_message('$i'), eligible: () => true),
      ]);
      expect(plays, 1);
      now = now.add(const Duration(seconds: 1));
      await feedback.received(_message('0'), eligible: () => true);
      expect(plays, 1);
      await feedback.received(_message('next'), eligible: () => true);
      expect(plays, 2);
      feedback.dispose();
    },
  );

  test('native bridge sends independent flags on Android and iOS', () async {
    const channel = MethodChannel('com.fd.kuailiao/message_feedback');
    final calls = <MethodCall>[];
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      return null;
    });
    addTearDown(() {
      debugDefaultTargetPlatformOverride = null;
      messenger.setMockMethodCallHandler(channel, null);
    });
    for (final platform in [TargetPlatform.android, TargetPlatform.iOS]) {
      debugDefaultTargetPlatformOverride = platform;
      final feedback = MessageFeedback(
        preferences: () async => const MessageFeedbackPreferences(sound: false),
      )..setAccount('me');
      await feedback.received(_message('native'), eligible: () => true);
      feedback.dispose();
    }
    expect(calls.length, 2);
    expect(calls.every((c) => c.method == 'play'), isTrue);
    expect(calls.first.arguments, {'sound': false, 'vibration': true});
  });

  test(
    'current chat alerts only on new real-time incoming user messages',
    () async {
      var plays = 0;
      var now = DateTime(2026);
      final feedback = MessageFeedback(
        preferences: () async => const MessageFeedbackPreferences(),
        now: () => now,
        play: ({required sound, required vibration}) async {
          plays++;
        },
      );
      final repository = _Repository();
      final controller = AppController(repository, messageFeedback: feedback);
      addTearDown(() async {
        controller.dispose();
        await repository.updates.close();
      });
      await controller.loginAsDemo();
      controller.setActiveConversation('c-linyu');
      Future<void> emit(
        ChatMessage message, {
        bool realtime = true,
        ImEventType type = ImEventType.messageCreated,
      }) async {
        now = now.add(const Duration(seconds: 1));
        repository.updates.add(
          ImEvent(
            type: type,
            payload: {'message': message.toJson(), 'realtime': realtime},
          ),
        );
        await Future<void>.delayed(const Duration(milliseconds: 5));
      }

      await emit(_message('new'));
      expect(plays, 1);
      await emit(_message('new'));
      await emit(
        _message('new').copyWith(status: MessageStatus.read),
        type: ImEventType.messageChanged,
      );
      await emit(_message('history'), realtime: false);
      await emit(_message('mine', mine: true));
      await emit(_message('system').copyWith(kind: MessageContentKind.system));
      await emit(
        _message(
          'screenshot',
        ).copyWith(kind: MessageContentKind.screenshotNotice),
      );
      for (final status in [
        MessageStatus.sending,
        MessageStatus.failed,
        MessageStatus.recalled,
        MessageStatus.expired,
      ]) {
        await emit(_message(status.name).copyWith(status: status));
      }
      await emit(_message('past-expiry').copyWith(expiresAt: DateTime(2020)));
      expect(plays, 1);
      await controller.toggleConversationMuted('c-linyu');
      await emit(_message('muted'));
      expect(plays, 1);
      await controller.toggleConversationMuted('c-linyu');
      await emit(_message('media').copyWith(kind: MessageContentKind.voice));
      expect(plays, 2);
      await emit(_message('group', conversationId: 'c-team'));
      expect(plays, 3);
      repository.hiddenIds.add('outside-history');
      await emit(_message('outside-history', conversationId: 'c-team'));
      expect(plays, 3);
      await controller.toggleConversationMuted('c-team');
      await emit(_message('muted-group', conversationId: 'c-team'));
      expect(plays, 3);
      expect(
        controller.messagesFor('c-linyu').any((m) => m.id == 'media'),
        isTrue,
      );
    },
  );

  test(
    'playback failure does not break message persistence or the next alert',
    () async {
      var attempts = 0;
      var now = DateTime(2026);
      final feedback = MessageFeedback(
        now: () => now,
        play: ({required sound, required vibration}) async {
          attempts++;
          throw PlatformException(code: 'unavailable');
        },
      );
      final repository = _Repository();
      final controller = AppController(repository, messageFeedback: feedback);
      addTearDown(() async {
        controller.dispose();
        await repository.updates.close();
      });
      await controller.loginAsDemo();
      for (final id in ['first', 'second']) {
        now = now.add(const Duration(seconds: 1));
        repository.updates.add(
          ImEvent(
            type: ImEventType.messageCreated,
            payload: {'message': _message(id).toJson(), 'realtime': true},
          ),
        );
        await Future<void>.delayed(const Duration(milliseconds: 5));
        expect(
          controller.messagesFor('c-linyu').any((m) => m.id == id),
          isTrue,
        );
        expect(repository.persisted.any((m) => m.id == id), isTrue);
      }
      expect(attempts, 2);
      expect(controller.error, isNull);
    },
  );
}

ChatMessage _message(
  String id, {
  bool mine = false,
  String conversationId = 'c-linyu',
}) => ChatMessage(
  id: id,
  conversationId: conversationId,
  senderId: mine ? 'me' : 'u1',
  senderName: '测试好友',
  text: '消息',
  sentAt: DateTime.now(),
  isMine: mine,
);

class _Repository extends DemoImRepository implements GroupHistoryRepository {
  _Repository() : super(latency: Duration.zero, store: _MemoryStore());
  final updates = StreamController<ImEvent>.broadcast();
  List<ChatMessage> persisted = [];
  final hiddenIds = <String>{};
  @override
  bool canReadCachedMessage(ChatMessage message) =>
      !hiddenIds.contains(message.id);
  @override
  Stream<ImEvent> get events => updates.stream;
  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {
    persisted = List.of(messages);
    await super.persistMessages(conversationId, messages);
  }
}

class _MemoryStore extends SecureLocalStore {
  final values = <String, Object>{};
  @override
  Future<void> writeJson(String key, Object value) async {
    values[key] = value;
  }

  @override
  Future<Object?> readJson(String key) async => values[key];
  @override
  Future<void> remove(String key) async {
    values.remove(key);
  }
}
