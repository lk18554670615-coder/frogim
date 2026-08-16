import 'package:flutter/material.dart';

/// Lets a tap on non-interactive page chrome dismiss the software keyboard.
/// Interactive descendants keep their own tap gestures and focus behavior.
class KeyboardDismissRegion extends StatelessWidget {
  const KeyboardDismissRegion({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) => GestureDetector(
    behavior: HitTestBehavior.translucent,
    onTap: () => FocusManager.instance.primaryFocus?.unfocus(),
    child: child,
  );
}
