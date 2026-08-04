import 'app_controller.dart';
import 'push_service_contract.dart';
import 'push_service_stub.dart'
    if (dart.library.js_interop) 'push_service_web.dart'
    if (dart.library.io) 'push_service_native.dart'
    as platform;

class PushCoordinator {
  PushCoordinator() : _delegate = platform.createPlatformPushService();

  final PlatformPushService _delegate;
  AppController? _controller;
  bool _initialized = false;

  Future<void> initialize(AppController controller) async {
    if (_initialized) return;
    _initialized = true;
    _controller = controller;
    controller.addListener(_syncFromController);
    await _delegate.initialize(controller);
    await _delegate.sync(controller);
  }

  void _syncFromController() {
    final controller = _controller;
    if (controller != null) _delegate.sync(controller);
  }

  Future<void> dispose() async {
    final controller = _controller;
    if (controller != null) controller.removeListener(_syncFromController);
    _controller = null;
    await _delegate.dispose();
  }
}
