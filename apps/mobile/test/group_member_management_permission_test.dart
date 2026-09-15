import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/group_send_policy.dart';
import 'package:linli_im/core/models.dart';

void main() {
  test('群主可管理其他成员，管理员只能管理普通成员', () {
    final owner = _member('owner', 'owner');
    final admin = _member('admin', 'admin');
    final otherAdmin = _member('other-admin', 'admin');
    final member = _member('member', 'member');

    expect(canManageGroupMember(owner, member), isTrue);
    expect(canManageGroupMember(owner, admin), isTrue);
    expect(canManageGroupMember(owner, owner), isFalse);
    expect(canManageGroupMember(admin, member), isTrue);
    expect(canManageGroupMember(admin, owner), isFalse);
    expect(canManageGroupMember(admin, otherAdmin), isFalse);
    expect(canManageGroupMember(member, admin), isFalse);
    expect(canManageGroupMember(null, member), isFalse);
  });
}

GroupMember _member(String id, String role) => GroupMember(
  user: AppUser(id: id, name: id, handle: '@$id', presence: ''),
  role: role,
  joinedAt: DateTime(2026),
);
