import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:wukongimfluttersdk/manager/event_manager.dart';
import 'package:wukongimfluttersdk/proto/packet.dart';
import 'package:wukongimfluttersdk/proto/proto.dart';
import 'package:wukongimfluttersdk/proto/write_read.dart';
import 'package:wukongimfluttersdk/wkim.dart';

void main() {
  test('pinned Flutter SDK decodes and dispatches WuKong EventPacket', () {
    final body = WriteData()
      ..writeString('evt-1')
      ..writeString('stream.delta')
      ..writeUint64(BigInt.from(1710000000123))
      ..writeBytes(
        utf8.encode(
          jsonEncode({
            'client_msg_no': 'stream-1',
            'event_key': 'main',
            'msg_event_seq': 3,
            'payload': {'kind': 'text', 'delta': '你好'},
          }),
        ),
      );
    final bodyBytes = body.toUint8List() as Uint8List;
    final frame = Uint8List.fromList([
      12 << 4,
      ..._variableLength(bodyBytes.length),
      ...bodyBytes,
    ]);

    final packet = Proto().decode(frame) as EventPacket;
    expect(PacketType.event.index, 12);
    expect(packet.id, 'evt-1');
    expect(packet.type, 'stream.delta');
    expect(packet.timestamp, 1710000000123);

    WKEvent? received;
    WKEventManager.shared.addEventListener('test', (event) {
      received = event;
    });
    addTearDown(() => WKEventManager.shared.removeEventListener('test'));
    WKEventManager.shared.notifyEventListeners(WKEvent(packet));

    expect(received?.dataJson['client_msg_no'], 'stream-1');
    expect(
      (received?.dataJson['payload'] as Map<Object?, Object?>)['delta'],
      '你好',
    );
  });

  test('pinned Flutter SDK decodes v4 stream anchor wire fields', () {
    final previousVersion = WKIM.shared.options.protoVersion;
    WKIM.shared.options.protoVersion = 4;
    addTearDown(() => WKIM.shared.options.protoVersion = previousVersion);

    final setting = Setting()..stream = 1;
    expect(setting.encode(), 2);
    expect(Setting().decode(2).stream, 1);

    final streamId = BigInt.parse('2087258383470989312');
    final messageId = BigInt.parse('2087258383470989313');
    final body = WriteData()
      ..writeUint8(2)
      ..writeString('msg-key')
      ..writeString('usr_bob')
      ..writeString('usr_bob')
      ..writeUint8(1)
      ..writeUint32(0)
      ..writeString('stream-anchor-1')
      ..writeUint8(0)
      ..writeString('')
      ..writeUint64(streamId)
      ..writeUint64(messageId)
      ..writeUint32(9)
      ..writeUint32(1786476152)
      ..writeBytes(utf8.encode('{"type":1,"content":"placeholder"}'));
    final bodyBytes = body.toUint8List() as Uint8List;
    final frame = Uint8List.fromList([
      PacketType.recv.index << 4,
      ..._variableLength(bodyBytes.length),
      ...bodyBytes,
    ]);

    final packet = Proto().decode(frame) as RecvPacket;
    expect(packet.setting.stream, 1);
    expect(packet.clientMsgNO, 'stream-anchor-1');
    expect(packet.streamFlag, 0);
    expect(packet.streamNo, '');
    expect(packet.streamSeq, streamId.toInt());
    expect(packet.messageID, messageId);
    expect(packet.messageSeq, 9);
    expect(packet.messageTime, 1786476152);
    expect(packet.payload, '{"type":1,"content":"placeholder"}');
  });
}

List<int> _variableLength(int value) {
  final output = <int>[];
  do {
    var byte = value & 0x7f;
    value >>= 7;
    if (value > 0) byte |= 0x80;
    output.add(byte);
  } while (value > 0);
  return output;
}
