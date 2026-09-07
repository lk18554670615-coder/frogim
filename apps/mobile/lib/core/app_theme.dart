import 'package:flutter/foundation.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

abstract final class LinliColors {
  static const primary = Color(0xFF1976B9);
  static const primaryPressed = Color(0xFF14659F);
  static const selectedSurface = Color(0xFFE7F2FC);
  static const link = Color(0xFF12649E);
  static const background = Color(0xFFF2F5F8);
  static const surface = Color(0xFFFFFFFF);
  static const surfaceElevated = Color(0xFFE9EEF3);
  static const chatBackground = Color(0xFFE7EFF5);
  static const outgoingBubble = Color(0xFFDCEEFF);
  static const pinnedSurface = selectedSurface;
  static const label = Color(0xFF182533);
  static const preview = Color(0xFF5B6B7A);
  static const tertiaryLabel = preview;
  static const separator = Color(0xFFDCE3EA);
  static const controlOutline = Color(0xFF7A8997);
  static const unread = Color(0xFFD92343);
  static const systemGreen = Color(0xFF22C55E);
  static const successText = Color(0xFF13763F);
  static const darkSuccessText = Color(0xFF63D78D);
  static const systemOrange = Color(0xFFD66A23);
  static const systemRed = Color(0xFFD92343);
  static const darkPrimary = Color(0xFF64B5EF);
  static const darkPrimaryPressed = Color(0xFF4A9FD9);
  static const darkLink = Color(0xFF8ACCF8);
  static const darkBackground = Color(0xFF0F1923);
  static const darkSurface = Color(0xFF172533);
  static const darkSurfaceElevated = Color(0xFF223445);
  static const darkOutgoingBubble = Color(0xFF244D6B);
  static const darkSelectedSurface = Color(0xFF203F57);
  static const darkPinnedSurface = darkSelectedSurface;
  static const darkLabel = Color(0xFFE8F0F7);
  static const darkPreview = Color(0xFFADBDCC);
  static const darkSeparator = Color(0xFF304454);
  static const darkControlOutline = Color(0xFF8296A8);
  static const darkError = Color(0xFFFF758B);
  static const mediaBackground = Color(0xFF0B121A);
}

/// Role-based colors shared by custom widgets and the Material theme.
/// Navigation, selection and message surfaces must not inherit button fills.
@immutable
class LinliPalette {
  const LinliPalette(this.dark);
  final bool dark;

  Color get primary => dark ? LinliColors.darkPrimary : LinliColors.primary;
  Color get onPrimary => dark ? LinliColors.darkBackground : Colors.white;
  Color get primaryPressed =>
      dark ? LinliColors.darkPrimaryPressed : LinliColors.primaryPressed;
  Color get background =>
      dark ? LinliColors.darkBackground : LinliColors.background;
  Color get surface => dark ? LinliColors.darkSurface : LinliColors.surface;
  Color get navigation => surface;
  Color get elevated =>
      dark ? LinliColors.darkSurfaceElevated : LinliColors.surfaceElevated;
  Color get text => dark ? LinliColors.darkLabel : LinliColors.label;
  Color get secondaryText =>
      dark ? LinliColors.darkPreview : LinliColors.preview;
  Color get separator =>
      dark ? LinliColors.darkSeparator : LinliColors.separator;
  Color get controlOutline =>
      dark ? LinliColors.darkControlOutline : LinliColors.controlOutline;
  Color get selected =>
      dark ? LinliColors.darkSelectedSurface : LinliColors.selectedSurface;
  Color get onSelected => link;
  Color get link => dark ? LinliColors.darkLink : LinliColors.link;
  Color get chatBackground =>
      dark ? LinliColors.darkBackground : LinliColors.chatBackground;
  Color get incomingBubble =>
      dark ? LinliColors.darkSurfaceElevated : LinliColors.surface;
  Color get outgoingBubble =>
      dark ? LinliColors.darkOutgoingBubble : LinliColors.outgoingBubble;
  Color get error => dark ? LinliColors.darkError : LinliColors.systemRed;
  Color get successText =>
      dark ? LinliColors.darkSuccessText : LinliColors.successText;
  SystemUiOverlayStyle get systemOverlay => SystemUiOverlayStyle(
    statusBarColor: Colors.transparent,
    statusBarIconBrightness: dark ? Brightness.light : Brightness.dark,
    statusBarBrightness: dark ? Brightness.dark : Brightness.light,
    systemNavigationBarColor: navigation,
    systemNavigationBarIconBrightness: dark
        ? Brightness.light
        : Brightness.dark,
  );
}

extension LinliPaletteContext on BuildContext {
  LinliPalette get linli =>
      LinliPalette(Theme.of(this).brightness == Brightness.dark);
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
  final palette = LinliPalette(dark);
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
    primary: palette.primary,
    onPrimary: palette.onPrimary,
    primaryContainer: palette.selected,
    onPrimaryContainer: palette.onSelected,
    secondary: palette.link,
    onSecondary: palette.onPrimary,
    secondaryContainer: palette.selected,
    onSecondaryContainer: palette.onSelected,
    error: palette.error,
    onError: dark ? LinliColors.darkBackground : Colors.white,
    surface: background,
    onSurface: label,
    surfaceContainer: surface,
    surfaceContainerLowest: background,
    surfaceContainerLow: surface,
    surfaceContainerHigh: elevated,
    surfaceContainerHighest: elevated,
    surfaceDim: elevated,
    surfaceBright: surface,
    inverseSurface: dark ? LinliColors.surface : LinliColors.darkSurface,
    onInverseSurface: dark ? LinliColors.label : LinliColors.darkLabel,
    inversePrimary: dark ? LinliColors.primary : LinliColors.darkPrimary,
    onSurfaceVariant: preview,
    outline: separator,
    outlineVariant: palette.controlOutline,
    surfaceTint: Colors.transparent,
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
    textSelectionTheme: TextSelectionThemeData(
      cursorColor: palette.primary,
      selectionColor: palette.primary.withValues(alpha: .24),
      selectionHandleColor: palette.primary,
    ),
    dialogTheme: DialogThemeData(
      backgroundColor: surface,
      surfaceTintColor: Colors.transparent,
    ),
    bottomSheetTheme: BottomSheetThemeData(
      backgroundColor: surface,
      surfaceTintColor: Colors.transparent,
    ),
    popupMenuTheme: PopupMenuThemeData(
      color: surface,
      surfaceTintColor: Colors.transparent,
    ),
    cupertinoOverrideTheme: CupertinoThemeData(
      applyThemeToAll: true,
      brightness: brightness,
      primaryColor: palette.primary,
      scaffoldBackgroundColor: background,
      barBackgroundColor: surface.withValues(alpha: .92),
    ),
    splashFactory: NoSplash.splashFactory,
    highlightColor: palette.primary.withValues(alpha: .12),
    visualDensity: VisualDensity.standard,
    appBarTheme: AppBarTheme(
      elevation: 0,
      scrolledUnderElevation: 0,
      centerTitle: true,
      toolbarHeight: 48,
      backgroundColor: palette.navigation,
      systemOverlayStyle: palette.systemOverlay,
      surfaceTintColor: Colors.transparent,
      foregroundColor: palette.primary,
      iconTheme: IconThemeData(color: palette.primary, size: 22),
      actionsIconTheme: IconThemeData(color: palette.primary, size: 22),
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
      iconColor: palette.primary,
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
        (states) =>
            states.contains(WidgetState.focused) ? (palette.primary) : preview,
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
        borderSide: BorderSide(color: palette.primary, width: 1.5),
      ),
      errorBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: BorderSide(color: palette.error),
      ),
      focusedErrorBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: BorderSide(color: palette.error, width: 1.5),
      ),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style:
          FilledButton.styleFrom(
            backgroundColor: palette.primary,
            foregroundColor: palette.onPrimary,
            disabledBackgroundColor: elevated,
            disabledForegroundColor: preview,
            minimumSize: const Size(44, 50),
            padding: const EdgeInsets.symmetric(horizontal: 20),
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(14),
            ),
            elevation: 0,
            textStyle: textTheme.labelLarge,
          ).copyWith(
            backgroundColor: WidgetStateProperty.resolveWith((states) {
              if (states.contains(WidgetState.disabled)) return elevated;
              if (states.contains(WidgetState.pressed) ||
                  states.contains(WidgetState.hovered)) {
                return palette.primaryPressed;
              }
              return palette.primary;
            }),
          ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        minimumSize: const Size(44, 50),
        foregroundColor: palette.link,
        side: BorderSide(color: palette.controlOutline),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
        textStyle: textTheme.labelLarge,
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(
        minimumSize: const Size(44, 44),
        foregroundColor: palette.link,
        textStyle: textTheme.bodyLarge,
      ),
    ),
    iconButtonTheme: IconButtonThemeData(
      style: IconButton.styleFrom(
        minimumSize: const Size(44, 44),
        foregroundColor: palette.primary,
      ),
    ),
    dividerTheme: DividerThemeData(color: separator, thickness: .5, space: .5),
    switchTheme: SwitchThemeData(
      trackColor: WidgetStateProperty.resolveWith(
        (states) =>
            states.contains(WidgetState.selected) ? palette.primary : elevated,
      ),
      thumbColor: WidgetStateProperty.resolveWith(
        (states) =>
            states.contains(WidgetState.selected) ? palette.onPrimary : preview,
      ),
      trackOutlineColor: WidgetStateProperty.resolveWith(
        (states) => states.contains(WidgetState.selected)
            ? Colors.transparent
            : palette.controlOutline,
      ),
    ),
    snackBarTheme: SnackBarThemeData(
      backgroundColor: dark ? elevated : LinliColors.label,
      contentTextStyle: TextStyle(color: LinliColors.darkLabel, fontSize: 15),
      behavior: SnackBarBehavior.floating,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
    ),
  );
}
