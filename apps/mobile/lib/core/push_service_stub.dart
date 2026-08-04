import 'app_controller.dart';
import 'push_service_contract.dart';

PlatformPushService createPlatformPushService() => _StubPushService();

class _StubPushService implements PlatformPushService {
  @override
  Future<void> initialize(AppController controller) async {}

  @override
  Future<void> sync(AppController controller) async {}

  @override
  Future<void> dispose() async {}
}
