import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/settings_screens.dart';

void main() {
  for (final width in [390.0, 1280.0]) {
    testWidgets('$width: 账号入口刷新错误的零次缓存', (tester) async {
      final repository = _ProfileRepository();
      final controller = _controller(repository);
      addTearDown(controller.dispose);
      await _pump(tester, AccountSecurityScreen(controller: controller), width);
      expect(find.text('还可修改 1 次'), findsOneWidget);
      await tester.tap(find.byKey(const Key('account-edit-handle')));
      await tester.pumpAndSettle();
      expect(find.text('编辑资料'), findsOneWidget);
      expect(
        tester
            .widget<TextFormField>(find.byKey(const Key('profile-handle')))
            .enabled,
        isTrue,
      );
    });

    testWidgets('$width: 延迟刷新更新修改权限且保留输入草稿', (tester) async {
      final repository = _ProfileRepository()..pending = Completer<AppUser>();
      final controller = _controller(repository);
      addTearDown(controller.dispose);
      await _pump(tester, EditProfileScreen(controller: controller), width);
      final handle = find.byKey(const Key('profile-handle'));
      expect(tester.widget<TextFormField>(handle).enabled, isFalse);
      expect(find.text('正在更新呱呱号修改状态'), findsOneWidget);
      await tester.enterText(find.byKey(const Key('profile-name')), '尚未保存的昵称');
      repository.pending!.complete(
        repository.user.copyWith(handle: 'new_remote_handle'),
      );
      await tester.pumpAndSettle();
      expect(tester.widget<TextFormField>(handle).enabled, isTrue);
      expect(
        tester.widget<TextFormField>(handle).controller!.text,
        'new_remote_handle',
      );
      expect(
        tester
            .widget<TextFormField>(find.byKey(const Key('profile-name')))
            .controller!
            .text,
        '尚未保存的昵称',
      );
      await tester.tap(find.byKey(const Key('save-profile')));
      await tester.pumpAndSettle();
      expect(repository.updates.single, {'name': '尚未保存的昵称'});
    });
  }

  testWidgets('其他设备耗尽呱呱号次数后仍能保存昵称且不提交旧号', (tester) async {
    final repository = _ProfileRepository();
    final controller = _controller(repository);
    addTearDown(controller.dispose);
    await _pump(tester, EditProfileScreen(controller: controller));
    await tester.enterText(find.byKey(const Key('profile-name')), '本机昵称草稿');
    repository.user = repository.user.copyWith(
      handle: 'new_remote_handle',
      handleChangesRemaining: 0,
    );
    await controller.refreshProfile();
    await tester.pumpAndSettle();
    final handle = tester.widget<TextFormField>(
      find.byKey(const Key('profile-handle')),
    );
    expect(handle.enabled, isFalse);
    expect(handle.controller!.text, 'new_remote_handle');
    await tester.tap(find.byKey(const Key('save-profile')));
    await tester.pumpAndSettle();
    expect(repository.updates.single, {'name': '本机昵称草稿'});
  });

  testWidgets('刷新失败明确提示并支持重试，不清空正在编辑的内容', (tester) async {
    final repository = _ProfileRepository()..failReads = true;
    final controller = _controller(repository);
    addTearDown(controller.dispose);
    await _pump(tester, EditProfileScreen(controller: controller));
    expect(find.byKey(const Key('profile-refresh-error')), findsOneWidget);
    expect(find.textContaining('修改次数已用完'), findsNothing);
    await tester.enterText(find.byKey(const Key('profile-name')), '离线草稿');
    repository.failReads = false;
    await tester.tap(find.text('重试'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('profile-refresh-error')), findsNothing);
    expect(
      tester
          .widget<TextFormField>(find.byKey(const Key('profile-name')))
          .controller!
          .text,
      '离线草稿',
    );
  });

  testWidgets('未修改的旧呱呱号格式不阻止昵称保存', (tester) async {
    final repository = _ProfileRepository();
    repository.user = repository.user.copyWith(
      handle: 'legacy-invalid-handle',
      handleChangesRemaining: 0,
    );
    final controller = _controller(repository);
    addTearDown(controller.dispose);
    await _pump(tester, EditProfileScreen(controller: controller));
    await tester.enterText(find.byKey(const Key('profile-name')), '新昵称');
    await tester.pump();
    await tester.tap(find.byKey(const Key('save-profile')));
    await tester.pumpAndSettle();
    expect(repository.updates.single, {'name': '新昵称'});
  });

  test('切换账号后忽略旧账号的迟到资料', () async {
    final repository = _ProfileRepository()..pending = Completer<AppUser>();
    final controller = _controller(repository);
    addTearDown(controller.dispose);
    final request = controller.refreshProfile();
    controller.currentUser = const AppUser(
      id: 'another',
      name: 'Another',
      handle: 'another_handle',
      presence: '',
    );
    repository.pending!.complete(repository.user);
    expect(await request, isFalse);
    expect(controller.currentUser!.id, 'another');
  });
}

AppController _controller(_ProfileRepository repository) =>
    AppController(repository)
      ..authenticated = true
      ..currentUser = repository.user.copyWith(handleChangesRemaining: 0);

Future<void> _pump(
  WidgetTester tester,
  Widget page, [
  double width = 390,
]) async {
  tester.view.physicalSize = Size(width, 1000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(
    MaterialApp(theme: buildLinliTheme(Brightness.light), home: page),
  );
  await tester.pumpAndSettle();
}

class _ProfileRepository extends DemoImRepository {
  _ProfileRepository() : super(latency: Duration.zero);
  AppUser user = DemoImRepository.demoUser.copyWith(
    handle: 'current_handle',
    handleChangesRemaining: 1,
  );
  Completer<AppUser>? pending;
  bool failReads = false;
  final updates = <Map<String, Object?>>[];

  @override
  Future<AppUser> profile() async {
    if (failReads) throw StateError('offline');
    return pending == null ? user : await pending!.future;
  }

  @override
  Future<AppUser> updateProfile({
    String? name,
    String? handle,
    String? signature,
    String? gender,
    String? avatarMediaId,
    bool? allowSearchByHandle,
    bool? allowSearchByPhone,
  }) async {
    updates.add({
      'name': ?name,
      'handle': ?handle,
      'signature': ?signature,
      'gender': ?gender,
      'avatarMediaId': ?avatarMediaId,
    });
    user = user.copyWith(
      name: name,
      handle: handle,
      signature: signature,
      gender: gender,
      avatarMediaId: avatarMediaId,
    );
    return user;
  }
}
