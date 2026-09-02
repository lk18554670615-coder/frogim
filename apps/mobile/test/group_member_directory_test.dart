import 'dart:async';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/group_member_directory.dart';
import 'package:linli_im/core/models.dart';

GroupMember member(String id) => GroupMember(
  user: AppUser(id: id, name: '昵称$id', handle: id, presence: ''),
  role: 'member',
  joinedAt: DateTime(2026),
);

void main() {
  test('完整成员去重、缓存及并发请求合并，强制刷新替换快照', () async {
    var calls = 0;
    var changes = 0;
    var clock = DateTime(2026);
    var completer = Completer<List<GroupMember>>();
    final directory = GroupMemberDirectory(
      (_) {
        calls++;
        return completer.future;
      },
      onChanged: () => changes++,
      now: () => clock,
    );
    final first = directory.load('g');
    final second = directory.load('g', force: true);
    expect(identical(first, second), isTrue);
    completer.complete([
      ...List.generate(11, (i) => member('$i')),
      member('10'),
    ]);
    expect((await first).length, 11);
    await second;
    expect((await directory.load('g')).length, 11);
    expect(calls, 1);
    expect(changes, 1);
    completer = Completer<List<GroupMember>>()..complete([member('1')]);
    await directory.load('g', force: true);
    expect(directory.members('g')!.single.user.id, '1');
    clock = clock.add(const Duration(minutes: 2));
    await directory.load('g');
    expect(calls, 3);
    directory.dispose();
  });

  test('群变更后的旧响应不能恢复已移除成员', () async {
    final requests = <Completer<List<GroupMember>>>[];
    final directory = GroupMemberDirectory((_) {
      final next = Completer<List<GroupMember>>();
      requests.add(next);
      return next.future;
    }, onChanged: () {});
    final old = directory.load('g');
    final rejected = expectLater(old, throwsA(isA<GroupMembersInvalidated>()));
    directory.invalidate('g');
    final fresh = directory.load('g');
    requests[1].complete([member('new')]);
    await fresh;
    requests[0].complete([member('old')]);
    await rejected;
    expect(directory.members('g')!.single.user.id, 'new');
    directory.dispose();
  });

  test('注销清空所有群，迟到响应和销毁后的请求均失效', () async {
    final pending = Completer<List<GroupMember>>();
    final directory = GroupMemberDirectory(
      (_) => pending.future,
      onChanged: () {},
    );
    final operation = directory.load('g');
    final rejected = expectLater(
      operation,
      throwsA(isA<GroupMembersInvalidated>()),
    );
    directory.invalidate();
    pending.complete([member('old-account')]);
    await rejected;
    expect(directory.members('g'), isNull);
    directory.dispose();
    await expectLater(
      directory.load('g'),
      throwsA(isA<GroupMembersInvalidated>()),
    );
  });

  test('缺失发送者补查去重、失败可重试，历史离群用户不产生请求风暴', () async {
    var calls = 0;
    var clock = DateTime(2026);
    var fail = true;
    final directory = GroupMemberDirectory(
      (_) async {
        calls++;
        if (fail) throw StateError('offline');
        return [member('late')];
      },
      onChanged: () {},
      now: () => clock,
    );
    await Future.wait(
      List.generate(50, (_) => directory.loadForSender('g', 'late')),
    );
    expect(calls, 1);
    fail = false;
    clock = clock.add(const Duration(seconds: 11));
    await directory.loadForSender('g', 'late');
    expect(calls, 2);
    await directory.loadForSender('g', 'late');
    expect(calls, 2);
    clock = clock.add(const Duration(seconds: 11));
    await Future.wait(
      List.generate(50, (i) => directory.loadForSender('g', 'left$i')),
    );
    expect(calls, 3);
    directory.dispose();
  });

  test('只保留最近访问的32个群，移除会话后清理', () async {
    final directory = GroupMemberDirectory(
      (id) async => [member(id)],
      onChanged: () {},
    );
    for (var i = 0; i < 40; i++) {
      await directory.load('$i');
    }
    expect(directory.members('0'), isNull);
    expect(directory.members('7'), isNull);
    expect(directory.members('8'), isNotNull);
    directory.retainGroups({'39'});
    expect(directory.members('8'), isNull);
    expect(directory.members('39'), isNotNull);
    directory.dispose();
  });
}
