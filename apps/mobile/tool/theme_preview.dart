// Local-only visual QA with synthetic accounts. Never imported by main.dart.
// flutter build web -t tool/theme_preview.dart --output build/theme-preview
import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:linli_im/ui/screens/login_screen.dart';

SemanticsHandle? _semantics;

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  _semantics ??= SemanticsBinding.instance.ensureSemantics();
  final controller = AppController(DemoImRepository(latency: Duration.zero));
  await controller.loginAsDemo();
  runApp(_ThemePreview(controller: controller));
}

class _ThemePreview extends StatefulWidget {
  const _ThemePreview({required this.controller});
  final AppController controller;
  @override
  State<_ThemePreview> createState() => _ThemePreviewState();
}

class _ThemePreviewState extends State<_ThemePreview> {
  bool dark = Uri.base.queryParameters['dark'] == '1';
  @override
  void dispose() {
    widget.controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final width = double.tryParse(Uri.base.queryParameters['width'] ?? '');
    final scale = double.tryParse(Uri.base.queryParameters['scale'] ?? '') ?? 1;
    return MaterialApp(
      title: '蓝白主题本地预览（模拟数据）',
      debugShowCheckedModeBanner: false,
      theme: buildLinliTheme(dark ? Brightness.dark : Brightness.light),
      builder: (context, child) => Center(
        child: SizedBox(
          width: width,
          child: MediaQuery(
            data: MediaQuery.of(context).copyWith(
              size: Size(
                width ?? MediaQuery.sizeOf(context).width,
                MediaQuery.sizeOf(context).height,
              ),
              textScaler: TextScaler.linear(scale),
            ),
            child: child!,
          ),
        ),
      ),
      home: Uri.base.queryParameters['page'] == 'login'
          ? LoginScreen(controller: widget.controller)
          : HomeScreen(
              controller: widget.controller,
              onToggleTheme: () => setState(() => dark = !dark),
            ),
    );
  }
}
