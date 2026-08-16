import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/ui/screens/qr_tools_screen.dart';

void main() {
  test('扫码器只接受独立的青蛙呱呱登录票据', () {
    expect(
      qrLoginTokenFrom('qingwaguagua://login/ql_secure_token'),
      'ql_secure_token',
    );
    expect(
      qrLoginTokenFrom('linlitong://login/ql_legacy_compatible'),
      'ql_legacy_compatible',
    );
    expect(qrLoginTokenFrom('qingwaguagua://user/ql_secure_token'), isNull);
    expect(qrLoginTokenFrom('qingwaguagua://login/not-a-ticket'), isNull);
    expect(qrLoginTokenFrom('https://example.com/ql_secure_token'), isNull);
  });
}
