import 'dart:async';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/models.dart';
import 'support/forward_fakes.dart';

class DelayedSessionRepository extends RecordingForwardRepository {
  final typing = <Completer<void>>[];
  Completer<List<ChatMessage>>? delayedMessages;
  List<ChatMessage> immediateMessages = [];
  @override
  Future<void> setTyping(String conversationId, bool value) {
    final result = Completer<void>();
    typing.add(result);
    return result.future;
  }

  @override
  Future<List<ChatMessage>> messages(String id) async =>
      delayedMessages?.future ?? immediateMessages;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));
  test(
    'late history from an earlier login cannot merge into a new session',
    () async {
      final repository = DelayedSessionRepository();
      final controller = forwardTestController(repository);
      addTearDown(controller.dispose);
      final delayed = Completer<List<ChatMessage>>();
      repository.delayedMessages = delayed;
      final oldLoad = controller.loadMessages('source-conversation');
      await controller.logout();
      // Same local user ID still needs a distinct session generation.
      controller.authenticated = true;
      controller.currentUser = forwardTestUser;
      repository.delayedMessages = null;
      repository.immediateMessages = [forwardSource(2)];
      await controller.loadMessages('source-conversation');
      delayed.complete([forwardSource(1)]);
      await oldLoad;
      expect(controller.messagesFor('source-conversation').map((m) => m.id), [
        'source-2',
      ]);
      expect(controller.messageErrors, isEmpty);
    },
  );
  test(
    'old typing failure cannot clear a new login typing announcement',
    () async {
      final repository = DelayedSessionRepository();
      final controller = forwardTestController(repository);
      controller.updateTyping('same-chat', true);
      final old = repository.typing.single;
      await controller.logout();
      controller.authenticated = true;
      controller.currentUser = forwardTestUser;
      controller.updateTyping('same-chat', true);
      final count = repository.typing.length;
      old.completeError(StateError('old connection failed'));
      await Future<void>.delayed(Duration.zero);
      controller.updateTyping('same-chat', true);
      expect(repository.typing.length, count);
      for (final pending in repository.typing) {
        if (!pending.isCompleted) pending.complete();
      }
      controller.dispose();
    },
  );
}
