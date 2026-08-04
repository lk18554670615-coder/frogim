import 'app_controller.dart';

abstract interface class PlatformPushService {
  Future<void> initialize(AppController controller);
  Future<void> sync(AppController controller);
  Future<void> dispose();
}
