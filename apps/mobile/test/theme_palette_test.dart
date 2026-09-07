import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_theme.dart';

double contrast(Color a, Color b) {
  final x = a.computeLuminance();
  final y = b.computeLuminance();
  return ((x > y ? x : y) + .05) / ((x > y ? y : x) + .05);
}

void main() {
  for (final brightness in Brightness.values) {
    test('$brightness text remains readable across actual theme surfaces', () {
      final theme = buildLinliTheme(brightness);
      final colors = LinliPalette(brightness == Brightness.dark);
      for (final surface in [
        colors.background,
        colors.surface,
        colors.elevated,
        colors.selected,
        colors.incomingBubble,
        colors.outgoingBubble,
      ]) {
        for (final text in [colors.text, colors.secondaryText, colors.link]) {
          expect(
            contrast(text, surface),
            greaterThanOrEqualTo(4.5),
            reason: '$text on $surface',
          );
        }
      }
      for (final states in <Set<WidgetState>>[
        {},
        {WidgetState.hovered},
        {WidgetState.pressed},
      ]) {
        final style = theme.filledButtonTheme.style!;
        expect(
          contrast(
            style.foregroundColor!.resolve(states)!,
            style.backgroundColor!.resolve(states)!,
          ),
          greaterThanOrEqualTo(4.5),
        );
      }
      expect(contrast(colors.primary, colors.surface), greaterThanOrEqualTo(3));
      expect(
        contrast(colors.controlOutline, colors.elevated),
        greaterThanOrEqualTo(3),
      );
      expect(
        contrast(colors.successText, colors.navigation),
        greaterThanOrEqualTo(4.5),
      );
      expect(theme.appBarTheme.backgroundColor, colors.navigation);
      expect(theme.cupertinoOverrideTheme!.applyThemeToAll, isTrue);
      expect(theme.cupertinoOverrideTheme!.primaryColor, colors.primary);
      expect(theme.textSelectionTheme.cursorColor, colors.primary);
      expect(colors.outgoingBubble, isNot(colors.incomingBubble));
      expect(
        theme.filledButtonTheme.style!.backgroundColor!.resolve({
          WidgetState.disabled,
          WidgetState.pressed,
        }),
        colors.elevated,
      );
    });
  }
}
