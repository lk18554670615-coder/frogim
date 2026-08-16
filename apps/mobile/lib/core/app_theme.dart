import 'package:flutter/foundation.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

abstract final class LinliColors {
  static const navy = Color(0xFF123B32);
  static const navySoft = Color(0xFF1B4A3E);
  static const brandGreen = Color(0xFF35CC70);
  static const brandGreenPressed = Color(0xFF28B862);
  static const brandGreenDeep = Color(0xFF178A4A);
  static const brandMint = Color(0xFFEFF9F2);
  static const brandMintStrong = Color(0xFFDCF3E3);
  static const background = Color(0xFFF5F8F6);
  static const surface = Color(0xFFFFFFFF);
  static const pinnedSurface = Color(0xFFF7FAF8);
  static const label = Color(0xFF142B24);
  static const preview = Color(0xFF61756B);
  static const tertiaryLabel = Color(0xFF91A198);
  static const separator = Color(0xFFDDE8E1);
  static const unread = Color(0xFFD92343);
  static const systemGreen = Color(0xFF22C55E);
  static const systemOrange = Color(0xFFD66A23);
  static const systemRed = Color(0xFFD92343);
  static const darkBackground = Color(0xFF0A1511);
  static const darkSurface = Color(0xFF10231B);
  static const darkSurfaceElevated = Color(0xFF193027);
  static const darkPinnedSurface = Color(0xFF14281F);
  static const darkLabel = Color(0xFFF8FAFC);
  static const darkPreview = Color(0xFFC8D8D0);
}

Duration nexaMotionDuration(BuildContext context) =>
    MediaQuery.disableAnimationsOf(context)
    ? Duration.zero
    : const Duration(milliseconds: 180);

ThemeData buildLinliTheme(Brightness brightness, {String? fontFamily}) {
  final dark = brightness == Brightness.dark;
  final label = dark ? LinliColors.darkLabel : LinliColors.label;
  final background = dark ? LinliColors.darkBackground : LinliColors.background;
  final surface = dark ? LinliColors.darkSurface : LinliColors.surface;
  final elevated = dark
      ? LinliColors.darkSurfaceElevated
      : const Color(0xFFEDF4F0);
  final preview = dark ? LinliColors.darkPreview : LinliColors.preview;
  final separator = dark ? const Color(0xFF315044) : LinliColors.separator;
  final scheme = ColorScheme(
    brightness: brightness,
    primary: LinliColors.brandGreen,
    onPrimary: LinliColors.navy,
    secondary: dark ? LinliColors.brandGreen : LinliColors.navy,
    onSecondary: dark ? LinliColors.navy : Colors.white,
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
      primaryColor: dark ? LinliColors.brandGreen : LinliColors.brandGreenDeep,
      scaffoldBackgroundColor: background,
      barBackgroundColor: surface.withValues(alpha: .92),
    ),
    splashFactory: NoSplash.splashFactory,
    highlightColor: LinliColors.brandGreen.withValues(alpha: .12),
    visualDensity: VisualDensity.standard,
    appBarTheme: AppBarTheme(
      elevation: 0,
      scrolledUnderElevation: 0,
      centerTitle: true,
      toolbarHeight: 48,
      backgroundColor: Colors.transparent,
      surfaceTintColor: Colors.transparent,
      foregroundColor: dark ? LinliColors.brandGreen : LinliColors.navy,
      iconTheme: IconThemeData(
        color: dark ? LinliColors.brandGreen : LinliColors.navy,
        size: 22,
      ),
      actionsIconTheme: IconThemeData(
        color: dark ? LinliColors.brandGreen : LinliColors.navy,
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
      iconColor: dark ? LinliColors.brandGreen : LinliColors.navy,
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
            ? LinliColors.brandGreenDeep
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
        borderSide: const BorderSide(
          color: LinliColors.brandGreenDeep,
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
        backgroundColor: LinliColors.brandGreen,
        foregroundColor: LinliColors.navy,
        disabledBackgroundColor: LinliColors.brandGreen.withValues(alpha: .35),
        disabledForegroundColor: LinliColors.navy.withValues(alpha: .45),
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
        foregroundColor: dark ? LinliColors.brandGreen : LinliColors.navy,
        side: BorderSide(color: dark ? separator : const Color(0xFFBFD0C5)),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
        textStyle: textTheme.labelLarge,
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(
        minimumSize: const Size(44, 44),
        foregroundColor: dark
            ? LinliColors.brandGreen
            : LinliColors.brandGreenDeep,
        textStyle: textTheme.bodyLarge,
      ),
    ),
    iconButtonTheme: IconButtonThemeData(
      style: IconButton.styleFrom(
        minimumSize: const Size(44, 44),
        foregroundColor: dark ? LinliColors.brandGreen : LinliColors.navy,
      ),
    ),
    dividerTheme: DividerThemeData(color: separator, thickness: .5, space: .5),
    switchTheme: SwitchThemeData(
      trackColor: WidgetStateProperty.resolveWith(
        (states) => states.contains(WidgetState.selected)
            ? LinliColors.brandGreen
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
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
    ),
  );
}
