import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/calls/system_call_service_native.dart';

void main() {
  test('系统通话 UUID 对同一服务端 callId 保持稳定', () {
    final first = systemCallIdFor('call-20260801-0001');
    final second = systemCallIdFor('call-20260801-0001');

    expect(first, second);
    expect(
      RegExp(
        r'^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
      ).hasMatch(first),
      isTrue,
    );
  });

  test('不同服务端 callId 不会映射成同一个系统通话 UUID', () {
    expect(
      systemCallIdFor('call-20260801-0001'),
      isNot(systemCallIdFor('call-20260801-0002')),
    );
  });
}
