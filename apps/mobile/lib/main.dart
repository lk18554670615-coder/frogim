import 'dart:async';
import 'dart:ui';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter/semantics.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'calls/call_screen.dart';
import 'core/app_controller.dart';
import 'core/app_config.dart';
import 'core/app_theme.dart';
import 'core/client_upgrade.dart';
import 'core/client_diagnostics.dart';
import 'core/bundled_licenses.dart';
import 'core/push_service.dart';
import 'core/web_context_menu.dart';
import 'data/im_repository.dart';
import 'data/live_repository.dart';
import 'ui/screens/home_screen.dart';
import 'ui/screens/client_upgrade_screen.dart';
import 'ui/screens/login_screen.dart';
import 'ui/widgets/keyboard_dismiss_region.dart';

SemanticsHandle? _webSemanticsHandle;

@visibleForTesting
void enablePersistentWebSemantics({bool? isWebOverride}) {
  if (!(isWebOverride ?? kIsWeb)) return;
  _webSemanticsHandle ??= SemanticsBinding.instance.ensureSemantics();
}

@visibleForTesting
void disposePersistentWebSemanticsForTest() {
  _webSemanticsHandle?.dispose();
  _webSemanticsHandle = null;
}

void main() {
  final startup = Stopwatch()..start();
  final diagnostics = ClientDiagnostics.instance;
  runZonedGuarded(() async {
    WidgetsFlutterBinding.ensureInitialized();
    // Flutter Web otherwise exposes only a one-time "Enable accessibility"
    // gate. Keep the semantic tree available from first paint so keyboard and
    // assistive-technology users can reach the real application UI.
    enablePersistentWebSemantics();
    FlutterError.onError = (details) {
      FlutterError.presentError(details);
      diagnostics.captureError(
        'flutter_error',
        details.exception,
        details.stack ?? StackTrace.current,
      );
    };
    PlatformDispatcher.instance.onError = (error, stack) {
      diagnostics.captureError('async_error', error, stack);
      return true;
    };
    AppConfig.validate();
    registerBundledLicenses();
    await configureWebContextMenu();
    runApp(const LinliApp());
    WidgetsBinding.instance.addPostFrameCallback((_) {
      startup.stop();
      diagnostics.recordStartup(startup.elapsed);
    });
  }, (error, stack) => diagnostics.captureError('zone_error', error, stack));
}

class LinliApp extends StatefulWidget {
  const LinliApp({super.key, this.repository, this.upgradeService});
  final ImRepository? repository;
  final ClientUpgradeService? upgradeService;

  @override
  State<LinliApp> createState() => _LinliAppState();
}

class _LinliAppState extends State<LinliApp> with WidgetsBindingObserver {
  static const _minimumLaunchVisibility = Duration(milliseconds: 650);

  final navigatorKey = GlobalKey<NavigatorState>();
  late final AppController controller;
  late final PushCoordinator pushCoordinator;
  late final ClientUpgradeService upgradeService;
  Timer? _launchReleaseTimer;
  bool _launchVisible = true;
  bool _lastAuthenticated = false;
  ThemeMode themeMode = ThemeMode.system;
  ClientUpgradeDecision? upgradeDecision;
  String? promptedUpgradeKey;

  @override
  void initState() {
    super.initState();
    controller = AppController(
      widget.repository ?? ResilientImRepository.fromEnvironment(),
    );
    ClientDiagnostics.instance.attach(controller.repository);
    pushCoordinator = PushCoordinator();
    upgradeService = widget.upgradeService ?? ClientUpgradeService();
    WidgetsBinding.instance.addObserver(this);
    controller.addListener(_refreshRoot);
    unawaited(pushCoordinator.initialize(controller));
    unawaited(controller.initialize());
    unawaited(_restoreTheme());
    unawaited(_checkUpgrade());
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      _launchReleaseTimer = Timer(_minimumLaunchVisibility, () {
        if (mounted) setState(() => _launchVisible = false);
      });
    });
  }

  void _refreshRoot() {
    if (!mounted) return;
    final sessionEnded = _lastAuthenticated && !controller.authenticated;
    _lastAuthenticated = controller.authenticated;
    setState(() {});
    if (sessionEnded) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        navigatorKey.currentState?.popUntil((route) => route.isFirst);
      });
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    controller.removeListener(_refreshRoot);
    _launchReleaseTimer?.cancel();
    unawaited(pushCoordinator.dispose());
    controller.dispose();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) unawaited(_checkUpgrade());
  }

  @override
  Widget build(BuildContext context) => MaterialApp(
    navigatorKey: navigatorKey,
    title: '青蛙呱呱',
    debugShowCheckedModeBanner: false,
    theme: buildLinliTheme(Brightness.light),
    darkTheme: buildLinliTheme(Brightness.dark),
    themeMode: themeMode,
    locale: const Locale('zh', 'CN'),
    supportedLocales: const [Locale('zh', 'CN')],
    localizationsDelegates: const [
      GlobalMaterialLocalizations.delegate,
      GlobalCupertinoLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
    ],
    themeAnimationDuration: Duration.zero,
    builder: (context, child) => CallUiHost(
      controller: controller.callController,
      child: KeyboardDismissRegion(child: child ?? const SizedBox.shrink()),
    ),
    home: upgradeDecision?.forceUpdate == true
        ? ForcedUpgradeScreen(
            key: const ValueKey('forced-upgrade'),
            decision: upgradeDecision!,
            onRetry: _checkUpgrade,
          )
        : AnimatedSwitcher(
            duration: Duration.zero,
            child: _launchVisible || controller.initializing
                ? const _LaunchScreen(key: ValueKey('launch'))
                : controller.authenticated
                ? HomeScreen(
                    key: const ValueKey('home'),
                    controller: controller,
                    onToggleTheme: _toggleTheme,
                  )
                : LoginScreen(
                    key: const ValueKey('login'),
                    controller: controller,
                  ),
          ),
  );

  Future<void> _checkUpgrade() async {
    try {
      final decision = await upgradeService.check();
      if (!mounted) return;
      setState(() => upgradeDecision = decision);
      if (decision?.updateAvailable == true &&
          decision?.forceUpdate == false &&
          promptedUpgradeKey != decision!.policyKey) {
        WidgetsBinding.instance.addPostFrameCallback((_) {
          final dialogContext = navigatorKey.currentContext;
          if (!mounted || dialogContext == null) return;
          promptedUpgradeKey = decision.policyKey;
          unawaited(showOptionalUpgradeDialog(dialogContext, decision));
        });
      }
    } catch (_) {
      // A transient version-check failure must not lock out a healthy client.
      // Foreground resume retries the policy without disturbing IM recovery.
    }
  }

  Future<void> _restoreTheme() async {
    final saved = (await SharedPreferences.getInstance()).getString(
      'linli_im.theme_mode.v1',
    );
    if (!mounted || saved == null) return;
    setState(() {
      themeMode = ThemeMode.values.firstWhere(
        (mode) => mode.name == saved,
        orElse: () => ThemeMode.system,
      );
    });
  }

  void _toggleTheme() {
    final platformDark =
        MediaQuery.platformBrightnessOf(context) == Brightness.dark;
    final currentlyDark =
        themeMode == ThemeMode.dark ||
        (themeMode == ThemeMode.system && platformDark);
    setState(() {
      themeMode = currentlyDark ? ThemeMode.light : ThemeMode.dark;
    });
    unawaited(
      SharedPreferences.getInstance().then(
        (prefs) => prefs.setString('linli_im.theme_mode.v1', themeMode.name),
      ),
    );
  }
}

class _LaunchScreen extends StatelessWidget {
  const _LaunchScreen({super.key});

  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: Colors.white,
    body: SafeArea(
      child: Center(
        child: Semantics(
          label: '青蛙呱呱正在启动',
          child: SizedBox.square(
            dimension: 300,
            child: Stack(
              alignment: Alignment.center,
              children: [
                ExcludeSemantics(
                  child: Image.asset(
                    'assets/brand/qingwaguagua-mark-transparent.png',
                    width: 160,
                    height: 160,
                  ),
                ),
                Transform.translate(
                  offset: const Offset(0, 116),
                  child: const SizedBox.square(
                    dimension: 22,
                    child: CircularProgressIndicator(
                      color: LinliColors.brandGreenDeep,
                      strokeWidth: 2,
                    ),
                  ),
                ),
                Transform.translate(
                  offset: const Offset(0, 150),
                  child: const ExcludeSemantics(
                    child: Text(
                      '正在启动，请稍候…',
                      style: TextStyle(
                        color: LinliColors.preview,
                        fontSize: 14,
                        fontWeight: FontWeight.w500,
                        letterSpacing: .2,
                      ),
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    ),
  );
}
