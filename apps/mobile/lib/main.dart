import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'calls/call_screen.dart';
import 'core/app_controller.dart';
import 'core/app_config.dart';
import 'core/app_theme.dart';
import 'core/push_service.dart';
import 'data/im_repository.dart';
import 'data/live_repository.dart';
import 'ui/screens/home_screen.dart';
import 'ui/screens/login_screen.dart';

void main() {
  AppConfig.validate();
  runApp(const LinliApp());
}

class LinliApp extends StatefulWidget {
  const LinliApp({super.key, this.repository});
  final ImRepository? repository;

  @override
  State<LinliApp> createState() => _LinliAppState();
}

class _LinliAppState extends State<LinliApp> {
  late final AppController controller;
  late final PushCoordinator pushCoordinator;
  ThemeMode themeMode = ThemeMode.system;

  @override
  void initState() {
    super.initState();
    controller = AppController(
      widget.repository ?? ResilientImRepository.fromEnvironment(),
    );
    pushCoordinator = PushCoordinator();
    controller.addListener(_refreshRoot);
    unawaited(pushCoordinator.initialize(controller));
    unawaited(controller.initialize());
    unawaited(_restoreTheme());
  }

  void _refreshRoot() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    controller.removeListener(_refreshRoot);
    unawaited(pushCoordinator.dispose());
    controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => MaterialApp(
    title: '邻里通讯',
    debugShowCheckedModeBanner: false,
    theme: buildLinliTheme(Brightness.light),
    darkTheme: buildLinliTheme(Brightness.dark),
    themeMode: themeMode,
    locale: const Locale('zh', 'CN'),
    supportedLocales: const [Locale('zh', 'CN'), Locale('en')],
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
    home: AnimatedSwitcher(
      duration: Duration.zero,
      child: controller.authenticated
          ? HomeScreen(
              key: const ValueKey('home'),
              controller: controller,
              onToggleTheme: _toggleTheme,
            )
          : controller.initializing
          ? const _LaunchScreen(key: ValueKey('launch'))
          : LoginScreen(key: const ValueKey('login'), controller: controller),
    ),
  );

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
