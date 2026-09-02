import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/group_send_policy.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/im/message_mapper.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

const personalMute = '你已被禁言，暂时无法在该群发送消息';
const allMute = '群聊已开启全员禁言，仅群主和管理员可发言';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  final audioEvents = <EventChannel>[];
  setUpAll(() {
    for (final name in [
      'com.llfbandit.record/messages',
      'xyz.luan/audioplayers.global',
    ]) {
      messenger.setMockMethodCallHandler(
        MethodChannel(name),
        (call) async => null,
      );
    }
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers'),
      (call) async {
        if (call.method == 'create') {
          final args = call.arguments as Map<Object?, Object?>;
          final channel = EventChannel(
            'xyz.luan/audioplayers/events/${args['playerId']}',
          );
          audioEvents.add(channel);
          messenger.setMockStreamHandler(
            channel,
            MockStreamHandler.inline(onListen: (_, _) {}),
          );
        }
        return null;
      },
    );
    messenger.setMockStreamHandler(
      const EventChannel('xyz.luan/audioplayers.global/events'),
      MockStreamHandler.inline(onListen: (_, _) {}),
    );
  });
  tearDownAll(() {
    for (final name in [
      'com.llfbandit.record/messages',
      'xyz.luan/audioplayers.global',
      'xyz.luan/audioplayers',
    ]) {
      messenger.setMockMethodCallHandler(MethodChannel(name), null);
    }
    for (final channel in [
      ...audioEvents,
      const EventChannel('xyz.luan/audioplayers.global/events'),
    ]) {
      messenger.setMockStreamHandler(channel, null);
    }
  });
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('个人禁言优先，全员禁言只豁免群主和管理员，到期自动允许发言', () async {
    final repository = _MuteRepository();
    final now = DateTime.now();
    repository.allMutedUntil = now.add(const Duration(hours: 1));
    for (final role in ['member', 'admin', 'owner']) {
      repository.role = role;
      var policy = await repository.policy();
      expect(policy.restrictionAt(now), role == 'member' ? allMute : null);
      repository.mutedUntil = now.add(const Duration(minutes: 1));
      policy = await repository.policy();
      expect(policy.restrictionAt(now), personalMute);
      expect(policy.restrictionAt(now.add(const Duration(hours: 1))), isNull);
      repository.mutedUntil = null;
    }
    await repository.close();
  });

  for (final thrown in [false, true]) {
    test('发送${thrown ? '异常' : '失败 ACK'}保留具体禁言原因和本地记录，解禁后可重试', () async {
      final repository = _MuteRepository()
        ..mutedUntil = DateTime.now().add(const Duration(hours: 1))
        ..throwOnSend = thrown;
      final controller = AppController(repository);
      await controller.loginAsDemo();
      addTearDown(controller.dispose);
      final message = (await controller.sendMessage('c-team', '测试禁言反馈'))!;
      expect(message.status, MessageStatus.failed);
      expect(message.sendError, personalMute);
      expect(ChatMessage.fromJson(message.toJson()).sendError, personalMute);
      expect(controller.messagesFor('c-team').last.sendError, personalMute);

      repository.mutedUntil = null;
      repository.throwOnSend = false;
      repository.sendStatus = MessageStatus.sent;
      await controller.retryMessage(message);
      final retried = controller.messagesFor('c-team').last;
      expect(retried.status, MessageStatus.sent);
      expect(retried.sendError, isNull);
      expect(retried.clientMessageId, message.clientMessageId);
    });
  }

  test('语音/附件失败使用相同禁言提示；网络故障不能冒充禁言', () async {
    final repository = _MuteRepository()
      ..allMutedUntil = DateTime.now().add(const Duration(hours: 1));
    final controller = AppController(repository);
    await controller.loginAsDemo();
    addTearDown(controller.dispose);
    final message = await controller.sendMedia(
      'c-team',
      MediaUpload(
        bytes: Uint8List.fromList([1, 2, 3]),
        fileName: 'voice.m4a',
        mimeType: 'audio/mp4',
        kind: MessageContentKind.voice,
      ),
    );
    expect(message.sendError, allMute);
    repository.failPolicy = true;
    repository.allMutedUntil = null;
    final failed = (await controller.sendMessage('c-team', '服务暂不可用'))!;
    expect(failed.sendError, '消息发送失败，请稍后重试');
    expect(failed.sendError, isNot(contains('禁言')));
  });

  test('延迟失败回执也补齐禁言原因，而非只变成红色重试按钮', () async {
    final repository = _MuteRepository()..sendStatus = MessageStatus.sending;
    final controller = AppController(repository);
    await controller.loginAsDemo();
    addTearDown(controller.dispose);
    final pending = (await controller.sendMessage('c-team', '延迟拒绝'))!;
    repository.mutedUntil = DateTime.now().add(const Duration(hours: 1));
    final completed = Completer<void>();
    void observe() {
      if (controller.messagesFor('c-team').last.sendError == personalMute &&
          !completed.isCompleted) {
        completed.complete();
      }
    }

    controller.addListener(observe);
    repository.bus.add(
      ImEvent(
        type: ImEventType.messageChanged,
        payload: {
          'message': pending.copyWith(status: MessageStatus.failed).toJson(),
          'reasonCode': 11,
        },
      ),
    );
    await completed.future.timeout(const Duration(seconds: 3));
    controller.removeListener(observe);
  });

  test('旧失败对象连续点击只重发一次，成功后旧入口失效', () async {
    final repository = _MuteRepository();
    final controller = AppController(repository);
    await controller.loginAsDemo();
    addTearDown(controller.dispose);
    final failed = (await controller.sendMessage('c-team', '重发测试'))!;
    final barrier = Completer<ChatMessage>();
    repository.sendBarrier = barrier;
    final retry = controller.retryMessage(failed);
    await controller.retryMessage(failed);
    expect(repository.sendCalls, 2);
    expect(controller.canRetryMessage(failed), isFalse);
    expect(
      controller.messagesFor('c-team').single.status,
      MessageStatus.sending,
    );
    barrier.complete(failed.copyWith(status: MessageStatus.sent));
    await retry;
    await controller.retryMessage(failed);
    expect(repository.sendCalls, 2);
    expect(controller.messagesFor('c-team').single.status, MessageStatus.sent);
    expect(controller.messagesFor('c-team').single.sendError, isNull);
  });

  for (final kind in [
    MessageContentKind.image,
    MessageContentKind.voice,
    MessageContentKind.video,
    MessageContentKind.file,
  ]) {
    test('$kind 无本地路径的失败媒体仍保留原文件供重发', () async {
      final repository = _MuteRepository();
      final controller = AppController(repository);
      await controller.loginAsDemo();
      addTearDown(controller.dispose);
      final failed = await controller.sendMedia(
        'c-team',
        MediaUpload(
          bytes: Uint8List.fromList([1, 2, 3]),
          fileName: 'retry.bin',
          mimeType: 'application/octet-stream',
          kind: kind,
          width: 80,
          height: 60,
        ),
      );
      repository.sendStatus = MessageStatus.sent;
      await controller.retryMessage(failed);
      expect(repository.mediaCalls, 2);
      expect(repository.lastUpload?.bytes, [1, 2, 3]);
      expect(
        controller.messagesFor('c-team').single.status,
        MessageStatus.sent,
      );
    });
  }

  test('上传后延迟失败只重发消息，不依赖已删除的本地文件', () async {
    final repository = _MuteRepository()
      ..sendStatus = MessageStatus.sending
      ..uploadedMediaId = 'uploaded-media';
    final controller = AppController(repository);
    await controller.loginAsDemo();
    addTearDown(controller.dispose);
    final sent = await controller.sendMedia(
      'c-team',
      MediaUpload(
        bytes: Uint8List.fromList([1, 2, 3]),
        fileName: 'voice.m4a',
        mimeType: 'audio/mp4',
        kind: MessageContentKind.voice,
      ),
    );
    repository.bus.add(
      ImEvent(
        type: ImEventType.messageChanged,
        payload: {
          'message': sent.copyWith(status: MessageStatus.failed).toJson(),
        },
      ),
    );
    await Future<void>.delayed(const Duration(milliseconds: 20));
    repository.sendStatus = MessageStatus.sent;
    await controller.retryMessage(controller.messagesFor('c-team').single);
    expect(repository.mediaCalls, 1);
    expect(repository.sendCalls, 2);
    expect(controller.messagesFor('c-team').single.status, MessageStatus.sent);
  });

  test('重复失败事件不反复查禁言原因，成功后不再退回失败', () async {
    final repository = _MuteRepository()..sendStatus = MessageStatus.sending;
    final controller = AppController(repository);
    await controller.loginAsDemo();
    addTearDown(controller.dispose);
    final pending = (await controller.sendMessage('c-team', '延迟回执'))!;
    final failedEvent = ImEvent(
      type: ImEventType.messageChanged,
      payload: {
        'message': pending.copyWith(status: MessageStatus.failed).toJson(),
      },
    );
    final before = repository.policyLoads;
    for (var i = 0; i < 10; i++) {
      repository.bus.add(failedEvent);
    }
    await Future<void>.delayed(const Duration(milliseconds: 20));
    expect(repository.policyLoads, before + 1);
    repository.sendStatus = MessageStatus.sent;
    await controller.retryMessage(controller.messagesFor('c-team').single);
    repository.bus.add(failedEvent);
    expect(controller.messagesFor('c-team').single.status, MessageStatus.sent);
  });

  test('发送中 ACK 成功优先于稍晚返回的发送中快照，退出后不恢复旧消息', () async {
    final repository = _MuteRepository();
    final controller = AppController(repository);
    await controller.loginAsDemo();
    addTearDown(controller.dispose);
    final failed = (await controller.sendMessage('c-team', 'ACK 抢先'))!;
    final barrier = Completer<ChatMessage>();
    repository.sendBarrier = barrier;
    final retry = controller.retryMessage(failed);
    repository.bus.add(
      ImEvent(
        type: ImEventType.messageChanged,
        payload: {
          'message': failed
              .copyWith(id: 'confirmed', status: MessageStatus.sent)
              .toJson(),
        },
      ),
    );
    barrier.complete(failed.copyWith(status: MessageStatus.sending));
    await retry;
    expect(controller.messagesFor('c-team').single.id, 'confirmed');
    expect(controller.messagesFor('c-team').single.status, MessageStatus.sent);
    repository.sendBarrier = null;
    final second = (await controller.sendMessage('c-team', '退出中的重发'))!;
    final secondBarrier = Completer<ChatMessage>();
    repository.sendBarrier = secondBarrier;
    final secondRetry = controller.retryMessage(second);
    await controller.logout();
    secondBarrier.complete(second.copyWith(status: MessageStatus.sent));
    await secondRetry;
    expect(controller.messagesFor('c-team'), isEmpty);
    expect(controller.canRetryMessage(second), isFalse);
  });

  test('失败 ACK 早于发送返回时保留重发入口，刷新旧缓存不回退成功状态', () async {
    final repository = _MuteRepository();
    final controller = AppController(repository);
    await controller.loginAsDemo();
    addTearDown(controller.dispose);
    final failed = (await controller.sendMessage('c-team', '乱序状态'))!;
    final barrier = Completer<ChatMessage>();
    repository.sendBarrier = barrier;
    final retry = controller.retryMessage(failed);
    repository.bus.add(
      ImEvent(
        type: ImEventType.messageChanged,
        payload: {'message': failed.toJson()},
      ),
    );
    barrier.complete(failed.copyWith(status: MessageStatus.sending));
    await retry;
    expect(
      controller.messagesFor('c-team').single.status,
      MessageStatus.failed,
    );
    expect(controller.canRetryMessage(failed), isTrue);
    repository.sendBarrier = null;
    repository.sendStatus = MessageStatus.sent;
    await controller.retryMessage(failed);
    repository.history = [failed];
    await controller.loadMessages('c-team', force: true);
    expect(controller.messagesFor('c-team').single.status, MessageStatus.sent);
  });

  for (final width in [390.0, 1280.0]) {
    testWidgets('$width 失败消息旁直接重发，不重复弹出失败提醒', (tester) async {
      final repository = _MuteRepository();
      final controller = AppController(repository);
      await tester.runAsync(controller.loginAsDemo);
      addTearDown(controller.dispose);
      tester.view.physicalSize = Size(width, 844);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await tester.pumpWidget(
        MaterialApp(
          theme: buildLinliTheme(
            width > 600 ? Brightness.dark : Brightness.light,
          ),
          home: ChatScreen(
            controller: controller,
            conversation: controller.conversations.firstWhere(
              (item) => item.id == 'c-team',
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.enterText(find.byKey(const Key('message-input')), '失败后手动重发');
      await tester.pump();
      await tester.tap(find.byKey(const Key('send-button')));
      await tester.pumpAndSettle();
      expect(find.text('重新发送'), findsOneWidget);
      expect(find.byType(SnackBar), findsNothing);
      final failed = controller.messagesFor('c-team').single;
      final barrier = Completer<ChatMessage>();
      repository.sendBarrier = barrier;
      await tester.tap(find.byKey(const Key('failed-message-retry')));
      await tester.pump();
      expect(find.text('重新发送'), findsNothing);
      expect(repository.sendCalls, 2);
      barrier.complete(failed.copyWith(status: MessageStatus.sent));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('failed-message-retry')), findsNothing);
      expect(find.byType(SnackBar), findsNothing);
      expect(find.text('失败后手动重发'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    });
  }

  test('普通权限错误码不伪称禁言，明确禁言和限流错误码有独立提示', () {
    expect(wukongSendFailureMessage(11), isNot(contains('禁言')));
    expect(wukongSendFailureMessage(13), isNot(contains('禁言')));
    expect(wukongSendFailureMessage(25), contains('禁言'));
    expect(wukongSendFailureMessage(22), contains('频繁'));
  });

  for (final width in [390.0, 1280.0]) {
    testWidgets('$width 群禁言提示、实时解禁和草稿恢复', (tester) async {
      final repository = _MuteRepository()
        ..mutedUntil = DateTime.now().add(const Duration(hours: 1));
      final controller = AppController(repository);
      await tester.runAsync(controller.loginAsDemo);
      addTearDown(controller.dispose);
      await controller.saveDraft('c-team', '未发送的草稿');
      tester.view.physicalSize = Size(width, 844);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await tester.pumpWidget(
        MaterialApp(
          theme: buildLinliTheme(
            width > 600 ? Brightness.dark : Brightness.light,
          ),
          home: ChatScreen(
            controller: controller,
            conversation: controller.conversations.firstWhere(
              (item) => item.id == 'c-team',
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text(personalMute), findsOneWidget);
      expect(find.byKey(const Key('message-input')), findsNothing);
      repository.mutedUntil = null;
      await tester.runAsync(() async {
        final refreshed = Completer<void>();
        void observePolicy() {
          if (controller.groupSendPolicyFor('c-team')?.member?.mutedUntil ==
                  null &&
              !refreshed.isCompleted) {
            refreshed.complete();
          }
        }

        controller.addListener(observePolicy);
        repository.bus.add(
          const ImEvent(
            type: ImEventType.conversationChanged,
            payload: {
              'conversationId': 'c-team',
              'groupSendPolicyChanged': true,
            },
          ),
        );
        try {
          await refreshed.future.timeout(const Duration(seconds: 3));
        } finally {
          controller.removeListener(observePolicy);
        }
      });
      await tester.pumpAndSettle();
      expect(find.text(personalMute), findsNothing);
      expect(find.byKey(const Key('message-input')), findsOneWidget);
      expect(find.text('未发送的草稿'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    });
  }

  testWidgets('失败原因在窄屏大字体下可见且不溢出', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: MediaQuery(
            data: const MediaQueryData(textScaler: TextScaler.linear(2)),
            child: SizedBox(
              width: 320,
              child: MessageBubble(
                message: ChatMessage(
                  id: 'failed',
                  conversationId: 'c-team',
                  senderId: 'me',
                  senderName: '我',
                  text: '未发送的内容',
                  sentAt: DateTime.now(),
                  isMine: true,
                  status: MessageStatus.failed,
                  sendError: allMute,
                ),
                onRetry: () {},
              ),
            ),
          ),
        ),
      ),
    );
    expect(find.text(allMute), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

class _MuteRepository extends DemoImRepository {
  _MuteRepository() : super(latency: Duration.zero, store: _MemoryStore());
  String role = 'member';
  DateTime? mutedUntil;
  DateTime? allMutedUntil;
  bool failPolicy = false;
  bool throwOnSend = false;
  MessageStatus sendStatus = MessageStatus.failed;
  int sendCalls = 0;
  int mediaCalls = 0;
  int policyLoads = 0;
  MediaUpload? lastUpload;
  String? uploadedMediaId;
  Completer<ChatMessage>? sendBarrier;
  List<ChatMessage> history = [];
  final bus = StreamController<ImEvent>.broadcast(sync: true);
  @override
  Stream<ImEvent> get events => bus.stream;
  @override
  Future<GroupProfile> groupProfile(String conversationId) async {
    policyLoads++;
    if (failPolicy) throw StateError('offline');
    return GroupProfile(
      conversationId: conversationId,
      ownerId: 'owner',
      name: '测试群',
      announcement: '',
      announcementVersion: 0,
      joinPolicy: 'invite',
      allowMemberAddFriend: true,
      updatedAt: DateTime.now(),
      allMutedUntil: allMutedUntil,
    );
  }

  @override
  Future<List<GroupMember>> groupMembers(String conversationId) async => [
    GroupMember(
      user: DemoImRepository.demoUser,
      role: role,
      joinedAt: DateTime(2026),
      mutedUntil: mutedUntil,
    ),
  ];
  Future<GroupSendPolicy> policy() async => GroupSendPolicy(
    profile: await groupProfile('c-team'),
    member: (await groupMembers('c-team')).first,
  );
  @override
  Future<List<ChatMessage>> messages(String conversationId) async => history;
  @override
  Future<ChatMessage> send(ChatMessage pending) async {
    sendCalls++;
    if (sendBarrier != null) return sendBarrier!.future;
    if (throwOnSend) throw const FormatException('forbidden');
    return pending.copyWith(status: sendStatus);
  }

  @override
  Future<ChatMessage> sendMedia(
    ChatMessage pending,
    MediaUpload upload, {
    void Function(double)? onProgress,
  }) {
    mediaCalls++;
    lastUpload = upload;
    return send(pending.copyWith(mediaId: uploadedMediaId));
  }

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {}
  @override
  Future<void> close() async {
    await bus.close();
    await super.close();
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
