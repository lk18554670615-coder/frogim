import 'dart:math';

String createClientMessageId(Random random, {int? microsecondsSinceEpoch}) {
  // Dart Web bit shifts use JavaScript's 32-bit semantics, so `1 << 32`
  // becomes zero. Two independent 16-bit values retain the original 32-bit
  // entropy and fixed eight-character hexadecimal suffix on every platform.
  final high = random.nextInt(1 << 16).toRadixString(16).padLeft(4, '0');
  final low = random.nextInt(1 << 16).toRadixString(16).padLeft(4, '0');
  final timestamp =
      microsecondsSinceEpoch ?? DateTime.now().microsecondsSinceEpoch;
  return '$timestamp-$high$low';
}
