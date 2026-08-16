import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/data/demo_repository.dart';

void main() {
  test('退出清理失败时仍立即清空本机可见登录态', () async {
    final repository = _FailingLogoutRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);

    await controller.loginAsDemo();
    expect(controller.authenticated, isTrue);
    expect(controller.currentUser, isNotNull);
    expect(controller.conversations, isNotEmpty);

    await expectLater(controller.logout(), completes);

    expect(controller.authenticated, isFalse);
    expect(controller.connected, isFalse);
    expect(controller.currentUser, isNull);
    expect(controller.conversations, isEmpty);
    expect(controller.contacts, isEmpty);
    expect(controller.loading, isFalse);
  });
}

class _FailingLogoutRepository extends DemoImRepository {
  @override
  Future<void> logout() async {
    throw StateError('secure storage unavailable');
  }
}
