import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'support/forward_fakes.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('超过100个目标全部发送，并发不超过3且批次ID各不相同', () async {
    final repository = RecordingForwardRepository();
    final controller = forwardTestController(repository, count: 125);
    final gate = Completer<void>();
    repository.onForward = (request) async {
      await gate.future;
      return repository.responseFor(request);
    };
    final task = controller.createForwardBatch(
      [forwardSource(1)],
      controller.conversations,
      mode: 'separate',
    );
    final running = task.start();
    expect(repository.requests, hasLength(3));
    await task.start();
    expect(repository.requests, hasLength(3));
    gate.complete();
    await running;
    expect(repository.requests, hasLength(125));
    expect(
      repository.requests.map((request) => request.batchId).toSet(),
      hasLength(125),
    );
    expect(repository.maximumActive, 3);
    expect(task.allSucceeded, isTrue);
    expect(
      controller.conversations.every((item) => item.subtitle == '转发测试消息 0'),
      isTrue,
    );
    task.dispose();
    controller.dispose();
  });

  for (final mode in ['separate', 'merged']) {
    test('$mode 部分失败不阻塞其他目标，重试保留ID和源顺序、不重复成功对象', () async {
      final repository = RecordingForwardRepository();
      final controller = forwardTestController(repository);
      var fail = true;
      repository.onForward = (request) async {
        if (fail && request.targetId == 'target-1') {
          throw TimeoutException('response lost');
        }
        return repository.responseFor(request);
      };
      final messages = [forwardSource(2), forwardSource(1)];
      final task = controller.createForwardBatch(
        messages,
        controller.conversations,
        mode: mode,
      );
      messages.clear();
      await task.start();
      expect(task.succeededCount, 4);
      expect(task.failedCount, 1);
      expect(task.targets[1].error, contains('超时'));
      expect(controller.error, isNull);
      final original = repository.requests.firstWhere(
        (item) => item.targetId == 'target-1',
      );
      fail = false;
      await task.start();
      expect(task.allSucceeded, isTrue);
      expect(repository.requests, hasLength(6));
      expect(repository.requests.last.batchId, original.batchId);
      expect(repository.requests.last.mode, mode);
      expect(repository.requests.last.sourceIds, ['source-2', 'source-1']);
      task.dispose();
      controller.dispose();
    });
  }

  test('逐条转发部分完成及响应丢失后，沿用相同ID可由服务端去重', () async {
    final repository = RecordingForwardRepository();
    final controller = forwardTestController(repository, count: 1);
    final accepted = <String>{};
    var firstAttempt = true;
    repository.onForward = (request) async {
      for (var index = 0; index < request.sourceIds.length; index++) {
        accepted.add('${request.batchId}:$index');
        if (firstAttempt && index == 0) {
          firstAttempt = false;
          throw TimeoutException('partially accepted');
        }
      }
      return repository.responseFor(request);
    };
    final task = controller.createForwardBatch(
      [forwardSource(0), forwardSource(1)],
      controller.conversations,
      mode: 'separate',
    );
    await task.start();
    expect(accepted, hasLength(1));
    await task.start();
    expect(task.allSucceeded, isTrue);
    expect(accepted, hasLength(2));
    task.dispose();
    controller.dispose();
  });

  test('停止仅取消排队目标，在途完成后可继续且不会重发成功目标', () async {
    final repository = RecordingForwardRepository();
    final controller = forwardTestController(repository, count: 8);
    final gate = Completer<void>();
    repository.onForward = (request) async {
      await gate.future;
      return repository.responseFor(request);
    };
    final task = controller.createForwardBatch(
      [forwardSource(1)],
      controller.conversations,
      mode: 'separate',
    );
    final running = task.start();
    task.stop();
    expect(task.running, isTrue);
    gate.complete();
    await running;
    expect(task.succeededCount, 3);
    expect(task.notSentCount, 5);
    expect(repository.requests, hasLength(3));
    await task.start();
    expect(repository.requests, hasLength(8));
    expect(task.allSucceeded, isTrue);
    task.dispose();
    controller.dispose();
  });

  test('开始退出登录时立即停止队列，同账号重新登录也不能恢复旧任务', () async {
    final repository = RecordingForwardRepository();
    final controller = forwardTestController(repository, count: 8);
    final gate = Completer<void>();
    final logoutGate = Completer<void>();
    repository.onLogout = () => logoutGate.future;
    repository.onForward = (request) async {
      await gate.future;
      return repository.responseFor(request);
    };
    final task = controller.createForwardBatch(
      [forwardSource(1)],
      controller.conversations,
      mode: 'separate',
    );
    final running = task.start();
    final logout = controller.logout();
    expect(task.sessionExpired, isTrue);
    gate.complete();
    await running;
    expect(repository.requests, hasLength(3));
    logoutGate.complete();
    await logout;
    controller.authenticated = true;
    controller.currentUser = forwardTestUser;
    await task.start();
    expect(repository.requests, hasLength(3));
    task.dispose();
    controller.dispose();
  });

  test('401停止所有排队请求，403只失败当前对象', () async {
    for (final code in [401, 403]) {
      final repository = RecordingForwardRepository();
      final controller = forwardTestController(repository, count: 9);
      repository.onForward = (request) async {
        if (request.targetId == 'target-0') {
          throw ImApiException(
            statusCode: code,
            code: code == 401 ? 'UNAUTHENTICATED' : 'FORBIDDEN',
            message: '无权发送',
          );
        }
        return repository.responseFor(request);
      };
      final task = controller.createForwardBatch(
        [forwardSource(1)],
        controller.conversations,
        mode: 'separate',
      );
      await task.start();
      expect(task.sessionExpired, code == 401);
      expect(repository.requests.length, code == 401 ? 3 : 9);
      task.dispose();
      controller.dispose();
    }
  });

  test('销毁任务或控制器会停止排队，不向已销毁监听器回调', () async {
    for (final disposeController in [false, true]) {
      final repository = RecordingForwardRepository();
      final controller = forwardTestController(repository, count: 9);
      final gate = Completer<void>();
      repository.onForward = (request) async {
        await gate.future;
        return repository.responseFor(request);
      };
      final task = controller.createForwardBatch(
        [forwardSource(1)],
        controller.conversations,
        mode: 'separate',
      );
      final running = task.start();
      if (disposeController) {
        controller.dispose();
      } else {
        task.dispose();
      }
      gate.complete();
      await running;
      expect(repository.requests, hasLength(3));
      if (disposeController) {
        task.dispose();
      } else {
        controller.dispose();
      }
    }
  });

  test('服务器成功但缓存失败仍标记成功，重试不重新发请求', () async {
    final repository = RecordingForwardRepository();
    final controller = forwardTestController(repository, count: 1);
    await controller.loadMessages('target-0');
    repository.failPersistence = true;
    final task = controller.createForwardBatch(
      [forwardSource(1)],
      controller.conversations,
      mode: 'separate',
    );
    await task.start();
    expect(task.allSucceeded, isTrue);
    expect(controller.messagesFor('target-0'), hasLength(1));
    await task.start();
    expect(repository.requests, hasLength(1));
    task.dispose();
    controller.dispose();
  });

  test('100条源消息可转发，101条、无效和重复消息均不发送或截断', () async {
    final repository = RecordingForwardRepository();
    final controller = forwardTestController(repository, count: 1);
    final hundred = List.generate(100, forwardSource);
    final task = controller.createForwardBatch(
      hundred,
      controller.conversations,
      mode: 'merged',
    );
    await task.start();
    expect(repository.requests.single.sourceIds, hasLength(100));
    for (final messages in [
      List.generate(101, forwardSource),
      <ChatMessage>[],
      [forwardSource(1), forwardSource(1)],
      [forwardSource(1).copyWith(status: MessageStatus.expired)],
      [forwardSource(1).copyWith(status: MessageStatus.recalled)],
      [forwardSource(1).copyWith(status: MessageStatus.failed)],
    ]) {
      expect(
        () => controller.createForwardBatch(
          messages,
          controller.conversations,
          mode: 'separate',
        ),
        throwsArgumentError,
      );
    }
    expect(repository.requests, hasLength(1));
    task.dispose();
    controller.dispose();
  });

  test('单目标接口保留可选批次ID及原返回值', () async {
    final repository = RecordingForwardRepository();
    final controller = forwardTestController(repository);
    final sent = await controller.forwardMessages(
      [forwardSource(1)],
      'target-0',
      mode: 'separate',
      clientBatchId: 'stable-id',
    );
    expect(sent, hasLength(1));
    expect(repository.requests.single.batchId, 'stable-id');
    expect(
      await controller.forwardMessage(forwardSource(2), 'target-1'),
      isNotNull,
    );
    controller.dispose();
  });
}
