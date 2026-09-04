import 'package:flutter/foundation.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

abstract final class LinliColors {
  static const brandYellow = Color(0xFFFFD633);
  static const brandYellowPressed = Color(0xFFE6B900);
  static const brandYellowSoft = Color(0xFFFFF1A6);
  static const brandYellowStrong = Color(0xFFFFE36B);
  static const brandInk = Color(0xFF171714);
  static const brandInkSoft = Color(0xFF2B2A25);
  static const background = Color(0xFFF7F5EE);
  static const surface = Color(0xFFFFFDF8);
  static const surfaceElevated = Color(0xFFF0EDE3);
  static const pinnedSurface = Color(0xFFFFF8D8);
  static const label = Color(0xFF171714);
  static const preview = Color(0xFF68655C);
  static const tertiaryLabel = Color(0xFF8B877C);
  static const separator = Color(0xFFE4DFD1);
  static const unread = Color(0xFFD92343);
  static const systemGreen = Color(0xFF22C55E);
  static const systemOrange = Color(0xFFD66A23);
  static const systemRed = Color(0xFFD92343);
  static const darkBackground = Color(0xFF0F0F0D);
  static const darkSurface = Color(0xFF191815);
  static const darkSurfaceElevated = Color(0xFF24221C);
  static const darkPinnedSurface = Color(0xFF2B281C);
  static const darkLabel = Color(0xFFFFF8DE);
  static const darkPreview = Color(0xFFC8C2AE);
  static const darkSeparator = Color(0xFF3B382E);

}

Duration nexaMotionDuration(BuildContext context) =>
    MediaQuery.disableAnimationsOf(context)
    ? Duration.zero
    : const Duration(milliseconds: 180);

/// 网页在分栏浏览器中仍保持桌面工作台；原生端继续使用更宽的断点。
bool useLinliDesktopLayout(double width) =>
    width >= 1024 || (kIsWeb && width >= 840);

ThemeData buildLinliTheme(Brightness brightness, {String? fontFamily}) {
  final dark = brightness == Brightness.dark;
  final label = dark ? LinliColors.darkLabel : LinliColors.label;
  final background = dark ? LinliColors.darkBackground : LinliColors.background;
  final surface = dark ? LinliColors.darkSurface : LinliColors.surface;
  final elevated = dark
      ? LinliColors.darkSurfaceElevated
      : LinliColors.surfaceElevated;
  final preview = dark ? LinliColors.darkPreview : LinliColors.preview;
  final separator = dark ? LinliColors.darkSeparator : LinliColors.separator;
  final scheme = ColorScheme(
    brightness: brightness,
    primary: LinliColors.brandYellow,
    onPrimary: LinliColors.brandInk,
    secondary: dark ? LinliColors.brandYellow : LinliColors.brandInk,
    onSecondary: dark ? LinliColors.brandInk : LinliColors.brandYellow,
    error: LinliColors.systemRed,
    onError: Colors.white,
    surface: background,
    onSurface: label,
    surfaceContainer: surface,
    surfaceContainerHigh: elevated,
    outline: separator,
  );

  final baseText = TextStyle(
    fontFamily: fontFamily ?? (kIsWeb ? 'NotoSansSC' : null),
    fontFamilyFallback: [
      if (kIsWeb) 'NotoColorEmoji',
      'PingFang SC',
      'Microsoft YaHei',
      'Noto Sans CJK SC',
      'SF Pro Text',
      'system-ui',
    ],
    letterSpacing: 0,
  );
  final textTheme = TextTheme(
    headlineLarge: baseText.copyWith(
      fontSize: 32,
      height: 1.18,
      fontWeight: FontWeight.w700,
      color: label,
      letterSpacing: -.25,
    ),
    headlineMedium: baseText.copyWith(
      fontSize: 28,
      height: 1.18,
      fontWeight: FontWeight.w700,
      color: label,
      letterSpacing: -.25,
    ),
    titleLarge: baseText.copyWith(
      fontSize: 17,
      fontWeight: FontWeight.w600,
      color: label,
      height: 1.29,
    ),
    titleMedium: baseText.copyWith(
      fontSize: 16,
      fontWeight: FontWeight.w600,
      color: label,
      height: 1.3,
    ),
    titleSmall: baseText.copyWith(
      fontSize: 15,
      fontWeight: FontWeight.w600,
      color: label,
      height: 1.33,
    ),
    bodyLarge: baseText.copyWith(fontSize: 17, height: 1.35, color: label),
    bodyMedium: baseText.copyWith(fontSize: 15, height: 1.33, color: preview),
    bodySmall: baseText.copyWith(fontSize: 13, height: 1.38, color: preview),
    labelLarge: baseText.copyWith(
      fontSize: 17,
      fontWeight: FontWeight.w600,
      color: label,
      height: 1.29,
    ),
    labelMedium: baseText.copyWith(fontSize: 13, height: 1.38, color: preview),
    labelSmall: baseText.copyWith(
      fontSize: 12,
      height: 1.33,
      fontWeight: FontWeight.w500,
      color: preview,
    ),
  );

  return ThemeData(
    useMaterial3: true,
    brightness: brightness,
    colorScheme: scheme,
    scaffoldBackgroundColor: background,
    canvasColor: background,
    textTheme: textTheme,
    cupertinoOverrideTheme: CupertinoThemeData(
      brightness: brightness,
      primaryColor: dark ? LinliColors.brandYellow : LinliColors.brandInk,
      scaffoldBackgroundColor: background,
      barBackgroundColor: surface.withValues(alpha: .92),
    ),
    splashFactory: NoSplash.splashFactory,
    highlightColor: LinliColors.brandYellow.withValues(alpha: .2),
    visualDensity: VisualDensity.standard,
    appBarTheme: AppBarTheme(
      elevation: 0,
      scrolledUnderElevation: 0,
      centerTitle: true,
      toolbarHeight: 48,
      backgroundColor: Colors.transparent,
      surfaceTintColor: Colors.transparent,
      foregroundColor: dark ? LinliColors.brandYellow : LinliColors.brandInk,
      iconTheme: IconThemeData(
        color: dark ? LinliColors.brandYellow : LinliColors.brandInk,
        size: 22,
      ),
      actionsIconTheme: IconThemeData(
        color: dark ? LinliColors.brandYellow : LinliColors.brandInk,
        size: 22,
      ),
      titleTextStyle: textTheme.titleLarge,
    ),
    cardTheme: CardThemeData(
      elevation: 0,
      shadowColor: Colors.transparent,
      color: surface,
      margin: EdgeInsets.zero,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(18)),
    ),
    listTileTheme: ListTileThemeData(
      tileColor: surface,
      iconColor: dark ? LinliColors.brandYellow : LinliColors.brandInk,
      textColor: label,
      titleTextStyle: textTheme.bodyLarge,
      subtitleTextStyle: textTheme.bodySmall,
      minLeadingWidth: 32,
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: elevated,
      hintStyle: textTheme.bodyLarge?.copyWith(color: preview),
      labelStyle: textTheme.bodyMedium,
      prefixIconColor: WidgetStateColor.resolveWith(
        (states) => states.contains(WidgetState.focused)
            ? (dark ? LinliColors.brandYellow : LinliColors.brandInk)
            : preview,
      ),
      suffixIconColor: preview,
      contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 14),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: BorderSide.none,
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: BorderSide.none,
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: BorderSide(
          color: dark ? LinliColors.brandYellow : LinliColors.brandInk,
          width: 1.5,
        ),
      ),
      errorBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: const BorderSide(color: LinliColors.systemRed),
      ),
      focusedErrorBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: const BorderSide(color: LinliColors.systemRed, width: 1.5),
      ),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        backgroundColor: dark ? LinliColors.brandYellow : LinliColors.brandInk,
        foregroundColor: dark ? LinliColors.brandInk : LinliColors.brandYellow,
        disabledBackgroundColor: dark
            ? LinliColors.brandYellow.withValues(alpha: .28)
            : LinliColors.brandInk.withValues(alpha: .28),
        disabledForegroundColor: dark
            ? LinliColors.brandInk.withValues(alpha: .58)
            : LinliColors.brandYellow.withValues(alpha: .62),
        minimumSize: const Size(44, 50),
        padding: const EdgeInsets.symmetric(horizontal: 20),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
        elevation: 0,
        textStyle: textTheme.labelLarge,
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        minimumSize: const Size(44, 50),
        foregroundColor: dark ? LinliColors.brandYellow : LinliColors.brandInk,
        side: BorderSide(color: separator),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
        textStyle: textTheme.labelLarge,
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(
        minimumSize: const Size(44, 44),
        foregroundColor: dark ? LinliColors.brandYellow : LinliColors.brandInk,
        textStyle: textTheme.bodyLarge,
      ),
    ),
    iconButtonTheme: IconButtonThemeData(
      style: IconButton.styleFrom(
        minimumSize: const Size(44, 44),
        foregroundColor: dark ? LinliColors.brandYellow : LinliColors.brandInk,
      ),
    ),
    dividerTheme: DividerThemeData(color: separator, thickness: .5, space: .5),
    switchTheme: SwitchThemeData(
      trackColor: WidgetStateProperty.resolveWith(
        (states) => states.contains(WidgetState.selected)
            ? LinliColors.brandYellow
            : separator,
      ),
      thumbColor: WidgetStateProperty.resolveWith(
        (states) => states.contains(WidgetState.selected)
            ? LinliColors.brandInk
            : Colors.white,
      ),
      trackOutlineColor: const WidgetStatePropertyAll(Colors.transparent),
    ),
    snackBarTheme: SnackBarThemeData(
      backgroundColor: dark ? LinliColors.brandYellow : LinliColors.brandInk,
      contentTextStyle: TextStyle(
        color: dark ? LinliColors.brandInk : LinliColors.brandYellow,
        fontSize: 15,
      ),
      behavior: SnackBarBehavior.floating,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
    ),
  );
}
