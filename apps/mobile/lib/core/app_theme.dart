import 'package:flutter/material.dart';

abstract final class LinliColors {
  static const navy = Color(0xFF0F172A);
  static const navySoft = Color(0xFF172033);
  static const yellow = Color(0xFFFFC529);
  static const yellowPressed = Color(0xFFF2B600);
  static const background = Color(0xFFF5F7FA);
  static const surface = Color(0xFFFFFFFF);
  static const paleWarm = Color(0xFFFFF9E8);
  static const pinnedSurface = Color(0xFFF8FAFC);
  static const label = Color(0xFF0F172A);
  static const preview = Color(0xFF64748B);
  static const tertiaryLabel = Color(0xFF94A3B8);
  static const separator = Color(0xFFE2E8F0);
  static const unread = Color(0xFFD92343);
  static const systemGreen = Color(0xFF22C55E);
  static const systemRed = Color(0xFFD92343);
  static const darkBackground = Color(0xFF020617);
  static const darkSurface = Color(0xFF0F172A);
  static const darkSurfaceElevated = Color(0xFF1E293B);
  static const darkPinnedSurface = Color(0xFF172033);
  static const darkLabel = Color(0xFFF8FAFC);
  static const darkPreview = Color(0xFFCBD5E1);
}

Duration nexaMotionDuration(BuildContext context) =>
    MediaQuery.disableAnimationsOf(context)
    ? Duration.zero
    : const Duration(milliseconds: 180);

ThemeData buildLinliTheme(Brightness brightness) {
  final dark = brightness == Brightness.dark;
  final label = dark ? LinliColors.darkLabel : LinliColors.label;
  final background = dark ? LinliColors.darkBackground : LinliColors.background;
  final surface = dark ? LinliColors.darkSurface : LinliColors.surface;
  final elevated = dark
      ? LinliColors.darkSurfaceElevated
      : const Color(0xFFEEF2F7);
  final preview = dark ? LinliColors.darkPreview : LinliColors.preview;
  final separator = dark ? const Color(0xFF334155) : LinliColors.separator;
  final scheme = ColorScheme(
    brightness: brightness,
    primary: LinliColors.yellow,
    onPrimary: LinliColors.navy,
    secondary: dark ? LinliColors.yellow : LinliColors.navy,
    onSecondary: dark ? LinliColors.navy : Colors.white,
    error: LinliColors.systemRed,
    onError: Colors.white,
    surface: background,
    onSurface: label,
    surfaceContainer: surface,
    surfaceContainerHigh: elevated,
    outline: separator,
  );

  const baseText = TextStyle(
    fontFamilyFallback: ['PingFang SC', 'SF Pro Text', 'system-ui'],
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
    splashFactory: NoSplash.splashFactory,
    highlightColor: LinliColors.yellow.withValues(alpha: .12),
    visualDensity: VisualDensity.standard,
    appBarTheme: AppBarTheme(
      elevation: 0,
      scrolledUnderElevation: 0,
      centerTitle: true,
      toolbarHeight: 48,
      backgroundColor: Colors.transparent,
      surfaceTintColor: Colors.transparent,
      foregroundColor: dark ? LinliColors.yellow : LinliColors.navy,
      iconTheme: IconThemeData(
        color: dark ? LinliColors.yellow : LinliColors.navy,
        size: 22,
      ),
      actionsIconTheme: IconThemeData(
        color: dark ? LinliColors.yellow : LinliColors.navy,
        size: 22,
      ),
      titleTextStyle: textTheme.titleLarge,
    ),
    cardTheme: CardThemeData(
      elevation: 0,
      shadowColor: Colors.transparent,
      color: surface,
      margin: EdgeInsets.zero,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
    ),
    listTileTheme: ListTileThemeData(
      tileColor: surface,
      iconColor: dark ? LinliColors.yellow : LinliColors.navy,
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
      contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 13),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(12),
        borderSide: BorderSide.none,
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(12),
        borderSide: BorderSide.none,
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(12),
        borderSide: const BorderSide(color: LinliColors.yellow, width: 1.5),
      ),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        backgroundColor: LinliColors.yellow,
        foregroundColor: LinliColors.navy,
        disabledBackgroundColor: LinliColors.yellow.withValues(alpha: .35),
        disabledForegroundColor: LinliColors.navy.withValues(alpha: .45),
        minimumSize: const Size(44, 50),
        padding: const EdgeInsets.symmetric(horizontal: 20),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
        elevation: 0,
        textStyle: textTheme.labelLarge,
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        minimumSize: const Size(44, 50),
        foregroundColor: label,
        side: BorderSide(color: separator),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
        textStyle: textTheme.labelLarge,
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(
        minimumSize: const Size(44, 44),
        foregroundColor: dark ? LinliColors.yellow : LinliColors.navy,
        textStyle: textTheme.bodyLarge,
      ),
    ),
    iconButtonTheme: IconButtonThemeData(
      style: IconButton.styleFrom(
        minimumSize: const Size(44, 44),
        foregroundColor: dark ? LinliColors.yellow : LinliColors.navy,
      ),
    ),
    dividerTheme: DividerThemeData(color: separator, thickness: .5, space: .5),
    switchTheme: SwitchThemeData(
      trackColor: WidgetStateProperty.resolveWith(
        (states) => states.contains(WidgetState.selected)
            ? LinliColors.yellow
            : separator,
      ),
      thumbColor: WidgetStateProperty.resolveWith(
        (states) => states.contains(WidgetState.selected)
            ? LinliColors.navy
            : Colors.white,
      ),
      trackOutlineColor: const WidgetStatePropertyAll(Colors.transparent),
    ),
    snackBarTheme: SnackBarThemeData(
      backgroundColor: dark
          ? LinliColors.darkSurfaceElevated
          : LinliColors.navy,
      contentTextStyle: const TextStyle(color: Colors.white, fontSize: 15),
      behavior: SnackBarBehavior.floating,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
    ),
  );
}
