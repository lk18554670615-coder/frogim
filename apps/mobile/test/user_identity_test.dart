import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/user_identity.dart';

void main() {
  test('公开呱呱号统一显示且不泄露内部账号标识', () {
    expect(publicUserHandleLabel('qingwa_2026'), '@qingwa_2026');
    expect(publicUserHandleLabel(''), '尚未设置呱呱号');
    expect(publicUserHandleLabel('usr_f6db4cf93b1efd2e4bf05dc1'), '尚未设置呱呱号');
  });

  test('两个未设置呱呱号的不同用户不会被误判为同一人', () {
    expect(
      samePublicUserIdentity(
        firstId: 'user-a',
        firstHandle: '',
        secondId: 'user-b',
        secondHandle: '',
      ),
      isFalse,
    );
    expect(
      samePublicUserIdentity(
        firstId: 'user-a',
        firstHandle: 'qingwa_a',
        secondId: 'user-b',
        secondHandle: 'QINGWA_A',
      ),
      isTrue,
    );
  });

  test('二维码查询只匹配真实用户 ID 或公开呱呱号', () {
    expect(
      userIdentityMatchesQuery(id: 'user-a', handle: '', query: 'user-a'),
      isTrue,
    );
    expect(
      userIdentityMatchesQuery(id: 'user-a', handle: '', query: 'user-b'),
      isFalse,
    );
    expect(
      userIdentityMatchesQuery(
        id: 'user-a',
        handle: 'qingwa_a',
        query: 'QINGWA_A',
      ),
      isTrue,
    );
  });
}
