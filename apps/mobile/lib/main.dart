import 'dart:async';
import 'dart:ui';

import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'calls/call_screen.dart';
import 'core/app_controller.dart';
import 'core/app_config.dart';
import 'core/app_theme.dart';
import 'core/client_upgrade.dart';
import 'core/client_diagnostics.dart';
import 'core/bundled_licenses.dart';
import 'core/push_service.dart';
import 'data/im_repository.dart';
import 'data/live_repository.dart';
import 'ui/screens/home_screen.dart';
import 'ui/screens/client_upgrade_screen.dart';
import 'ui/screens/login_screen.dart';

void main() {
  final startup = Stopwatch()..start();
  final diagnostics = ClientDiagnostics.instance;
  runZonedGuarded(() {
    WidgetsFlutterBinding.ensureInitialized();
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
  final navigatorKey = GlobalKey<NavigatorState>();
  late final AppController controller;
  late final PushCoordinator pushCoordinator;
  late final ClientUpgradeService upgradeService;
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
  }

  void _refreshRoot() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    controller.removeListener(_refreshRoot);
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
    title: '邻里通讯',
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
      child: child ?? const SizedBox.shrink(),
    ),
    home: upgradeDecision?.forceUpdate == true
        ? ForcedUpgradeScreen(
            key: const ValueKey('forced-upgrade'),
            decision: upgradeDecision!,
            onRetry: _checkUpgrade,
          )
        : AnimatedSwitcher(
            duration: Duration.zero,
            child: controller.authenticated
                ? HomeScreen(
                    key: const ValueKey('home'),
                    controller: controller,
                    onToggleTheme: _toggleTheme,
                  )
                : controller.initializing
                ? const _LaunchScreen(key: ValueKey('launch'))
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
    backgroundColor: LinliColors.navy,
    body: SafeArea(
      child: Center(
        child: Semantics(
          label: '邻里通讯正在启动',
          child: const SizedBox.square(
            dimension: 26,
            child: CircularProgressIndicator(
              color: LinliColors.yellow,
              strokeWidth: 2.5,
            ),
          ),
        ),
      ),
    ),
  );
}
