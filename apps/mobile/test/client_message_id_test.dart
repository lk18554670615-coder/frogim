import 'dart:math';

import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/client_message_id.dart';

void main() {
  test('client message id keeps a 32-bit suffix on every Dart runtime', () {
    final id = createClientMessageId(
      Random(7),
      microsecondsSinceEpoch: 123456789,
    );

    expect(id, matches(RegExp(r'^123456789-[0-9a-f]{8}$')));
  });
}
