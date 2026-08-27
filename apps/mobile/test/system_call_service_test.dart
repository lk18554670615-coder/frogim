import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/calls/system_call_service_native.dart';
import 'package:linli_im/calls/system_call_service_contract.dart';

void main() {
  test('parses an Android cold-start accept action', () {
    final action = systemCallActionFromMap(const {
      'type': 'accept',
      'serverCallId': 'call-cold-start',
      'systemCallId': 'system-call-id',
    });

    expect(action, isNotNull);
    expect(action!.type, SystemCallActionType.accept);
    expect(action.serverCallId, 'call-cold-start');
    expect(action.systemCallId, 'system-call-id');
  });

  test('rejects a corrupt Android cold-start action', () {
    expect(
      systemCallActionFromMap(const {
        'type': 'accept',
        'serverCallId': '',
        'systemCallId': 'system-call-id',
      }),
      isNull,
    );
  });

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
