import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test(
    'onQueued follows persistence and fires once, 100% upload still waits for ACK',
    () async {
      final repo = _QueueRepository();
      final controller = AppController(repo);
      await controller.loginAsDemo();
      addTearDown(controller.dispose);
      repo.persistGate = Completer<void>();
      final queued = <ChatMessage>[];
      final send = controller.sendMedia(
        'c-linyu',
        _upload(),
        onQueued: queued.add,
      );
      await Future<void>.delayed(Duration.zero);
      expect(queued, isEmpty);
      expect(repo.pending, isNull);
      expect(
        controller
            .messagesFor('c-linyu')
            .where((m) => m.kind == MessageContentKind.voice),
        isEmpty,
      );
      repo.persistGate!.complete();
      await Future<void>.delayed(Duration.zero);
      expect(queued, hasLength(1));
      final id = queued.single.clientMessageId;
      expect(repo.persisted.any((m) => m.clientMessageId == id), isTrue);
      repo.progress!(1);
      expect(
        controller.messagesFor('c-linyu').last.status,
        MessageStatus.sending,
      );
      expect(controller.mediaUploadProgressFor(id), 1);
      repo.ack.complete(
        repo.pending!.copyWith(id: 'server-voice', status: MessageStatus.sent),
      );
      final sent = await send;
      expect(sent.status, MessageStatus.sent);
      expect(sent.clientMessageId, id);
      expect(queued, hasLength(1));
      expect(controller.mediaUploadProgressFor(id), isNull);
    },
  );

  test(
    'failed persistence does not enqueue or consume draft ownership',
    () async {
      final repo = _QueueRepository();
      final controller = AppController(repo);
      await controller.loginAsDemo();
      addTearDown(controller.dispose);
      repo.failPersistence = true;
      var called = false;
      await expectLater(
        controller.sendMedia(
          'c-linyu',
          _upload(),
          onQueued: (_) => called = true,
        ),
        throwsStateError,
      );
      expect(called, isFalse);
      expect(repo.pending, isNull);
      expect(
        controller
            .messagesFor('c-linyu')
            .where((m) => m.kind == MessageContentKind.voice),
        isEmpty,
      );
    },
  );

  test(
    'failed transport keeps existing retry workflow without a second enqueue callback',
    () async {
      final repo = _QueueRepository();
      final controller = AppController(repo);
      await controller.loginAsDemo();
      addTearDown(controller.dispose);
      var callbacks = 0;
      final send = controller.sendMedia(
        'c-linyu',
        _upload(),
        onQueued: (_) => callbacks++,
      );
      await Future<void>.delayed(Duration.zero);
      repo.ack.completeError(StateError('upload rejected'));
      final failed = await send;
      expect(failed.status, MessageStatus.failed);
      repo.ack = Completer<ChatMessage>();
      final retry = controller.retryMessage(failed);
      await Future<void>.delayed(Duration.zero);
      repo.ack.complete(repo.pending!.copyWith(status: MessageStatus.sent));
      await retry;
      expect(callbacks, 1);
      expect(controller.messagesFor('c-linyu').last.status, MessageStatus.sent);
    },
  );
}

MediaUpload _upload() => MediaUpload(
  bytes: Uint8List.fromList([1, 2]),
  fileName: 'voice.m4a',
  mimeType: 'audio/mp4',
  kind: MessageContentKind.voice,
  durationSeconds: 2,
);

class _QueueRepository extends DemoImRepository {
  _QueueRepository() : super(latency: Duration.zero);
  Completer<void>? persistGate;
  bool failPersistence = false;
  List<ChatMessage> persisted = [];
  ChatMessage? pending;
  Completer<ChatMessage> ack = Completer<ChatMessage>();
  void Function(double)? progress;

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {
    if (failPersistence) throw StateError('disk full');
    if (persistGate != null) await persistGate!.future;
    persisted = List.of(messages);
  }

  @override
  Future<ChatMessage> sendMedia(
    ChatMessage pending,
    MediaUpload upload, {
    void Function(double)? onProgress,
  }) {
    this.pending = pending;
    progress = onProgress;
    return ack.future;
  }
}
