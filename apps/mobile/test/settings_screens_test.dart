import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/auth_validation.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/screens/people_screens.dart';
import 'package:linli_im/ui/screens/relationship_screens.dart';
import 'package:linli_im/ui/screens/settings_preferences.dart';
import 'package:linli_im/ui/screens/settings_screens.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('设置中心覆盖账号、通知、隐私、通用、存储与支持入口', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(
      tester,
      SettingsScreen(controller: controller, onToggleTheme: () {}),
    );

    expect(find.text('账号与安全'), findsOneWidget);
    expect(find.text('我的收藏'), findsOneWidget);
    expect(find.text('登录设备'), findsOneWidget);
    expect(find.text('消息通知'), findsOneWidget);
    expect(find.text('隐私与安全'), findsOneWidget);
    expect(find.text('通用'), findsOneWidget);
    expect(find.text('聊天背景'), findsOneWidget);
    expect(find.text('存储空间'), findsOneWidget);
    expect(find.text('帮助与反馈'), findsOneWidget);
    expect(find.text('关于青蛙呱呱'), findsOneWidget);

    await tester.tap(find.byKey(const Key('settings-notifications')));
    await _settle(tester);
    expect(find.text('通知偏好'), findsOneWidget);
    expect(find.text('系统通知权限'), findsOneWidget);
  });

  testWidgets('退出登录说明与本机缓存清理行为一致', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(
      tester,
      SettingsScreen(controller: controller, onToggleTheme: () {}),
    );

    expect(find.text('清除本机登录凭据和账号缓存'), findsOneWidget);
    await tester.ensureVisible(find.byKey(const Key('settings-logout')));
    await tester.drag(find.byType(ListView), const Offset(0, -120));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('settings-logout')));
    await _settle(tester);

    expect(find.text('退出登录？'), findsOneWidget);
    expect(
      find.text('本机登录凭据与账号缓存将清除；云端消息和联系人不会删除，重新登录后可以再次同步。'),
      findsOneWidget,
    );
    await tester.tap(find.text('取消'));
    await _settle(tester);
    expect(controller.authenticated, isTrue);
  });

  testWidgets('编辑资料仅在有效内容发生变化后允许保存', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(tester, EditProfileScreen(controller: controller));

    final saveFinder = find.byKey(const Key('save-profile'));
    expect(tester.widget<TextButton>(saveFinder).onPressed, isNull);
    expect(find.byKey(const Key('profile-discovery-card')), findsNothing);
    expect(find.byKey(const Key('profile-phone-card')), findsOneWidget);
    expect(find.text('点击头像更换照片'), findsOneWidget);
    expect(find.byKey(const Key('profile-gender')), findsOneWidget);
    expect(find.text('不展示'), findsOneWidget);
    expect(find.text('呱呱号'), findsOneWidget);
    expect(find.text('2/40'), findsOneWidget);
    expect(find.text('8/24'), findsOneWidget);
    expect(find.text('10/160'), findsOneWidget);
    expect(find.textContaining('还可修改 1 次 · 4–24 位'), findsOneWidget);
    expect(find.textContaining('MinIO'), findsNothing);

    await tester.tap(find.byKey(const Key('profile-gender')));
    await _settle(tester);
    expect(find.text('选择性别'), findsOneWidget);
    await tester.tap(find.byKey(const Key('profile-gender-female')));
    await _settle(tester);
    expect(find.text('女'), findsOneWidget);
    expect(tester.widget<TextButton>(saveFinder).onPressed, isNotNull);

    await tester.enterText(find.byKey(const Key('profile-name')), '');
    await tester.pump();
    expect(tester.widget<TextButton>(saveFinder).onPressed, isNull);
    expect(find.text('请输入昵称'), findsNothing);

    await tester.enterText(find.byKey(const Key('profile-name')), '青蛙用户');
    await tester.pump();
    expect(tester.widget<TextButton>(saveFinder).onPressed, isNotNull);
  });

  testWidgets('编辑资料按服务端 Unicode 字符规则计数并阻止超长提交', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(tester, EditProfileScreen(controller: controller));

    final multiRuneEmoji = List.filled(6, '👨‍👩‍👧‍👦').join();
    await tester.enterText(
      find.byKey(const Key('profile-name')),
      multiRuneEmoji,
    );
    await tester.pump();

    expect(find.text('42/40'), findsOneWidget);
    expect(
      tester
          .widget<TextButton>(find.byKey(const Key('save-profile')))
          .onPressed,
      isNull,
    );
    expect(
      await controller.saveProfile(
        name: multiRuneEmoji,
        handle: controller.currentUser!.handle,
        signature: controller.currentUser!.signature ?? '',
        gender: controller.currentUser!.gender,
      ),
      isFalse,
    );
    expect(controller.error, '昵称不能超过 40 个字符');
  });

  testWidgets('个人资料保存失败保留草稿并显示可重试的明确错误', (tester) async {
    final repository = _RetryProfileRepository();
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser
      ..connected = true;
    addTearDown(controller.dispose);
    await _pump(tester, EditProfileScreen(controller: controller));

    final nameField = find.byKey(const Key('profile-name'));
    final saveButton = find.byKey(const Key('save-profile'));
    await tester.enterText(nameField, '新的青蛙昵称');
    await tester.pump();
    await tester.tap(saveButton);
    await _settle(tester);

    expect(repository.updateProfileCalls, 1);
    expect(find.byKey(const Key('profile-save-error')), findsOneWidget);
    expect(find.text('这个呱呱号已被使用，请换一个'), findsOneWidget);
    expect(tester.widget<TextFormField>(nameField).controller?.text, '新的青蛙昵称');
    expect(tester.widget<TextButton>(saveButton).onPressed, isNotNull);

    await tester.tap(find.text('重试'));
    await _settle(tester);

    expect(repository.updateProfileCalls, 2);
    expect(controller.currentUser?.name, '新的青蛙昵称');
    expect(find.byKey(const Key('profile-save-error')), findsNothing);
  });

  testWidgets('个人资料失败提示会在继续编辑时清除', (tester) async {
    final repository = _RetryProfileRepository(alwaysFail: true);
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser
      ..connected = true;
    addTearDown(controller.dispose);
    await _pump(tester, EditProfileScreen(controller: controller));

    final nameField = find.byKey(const Key('profile-name'));
    await tester.enterText(nameField, '第一次修改');
    await tester.pump();
    await tester.tap(find.byKey(const Key('save-profile')));
    await _settle(tester);
    expect(find.byKey(const Key('profile-save-error')), findsOneWidget);

    await tester.enterText(nameField, '第二次修改');
    await tester.pump();
    expect(find.byKey(const Key('profile-save-error')), findsNothing);
    expect(find.text('第二次修改'), findsOneWidget);
  });

  testWidgets('个人资料在小屏键盘弹出时仍可滚动查看全部字段', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    tester.view.physicalSize = const Size(360, 640);
    tester.view.devicePixelRatio = 1;
    tester.view.viewInsets = const FakeViewPadding(bottom: 280);
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.view.resetViewInsets);
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: EditProfileScreen(controller: controller),
      ),
    );
    await _settle(tester);

    final signatureField = find.byKey(const Key('profile-signature'));
    await tester.scrollUntilVisible(
      signatureField,
      160,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.tap(signatureField);
    await tester.pump();
    await tester.ensureVisible(find.byKey(const Key('profile-phone-card')));
    await tester.pump();

    expect(
      find.byKey(const Key('profile-phone-card')).hitTestable(),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('账号页的呱呱号入口与可修改状态保持一致', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(tester, AccountSecurityScreen(controller: controller));

    expect(find.text('还可修改 1 次'), findsOneWidget);
    await tester.tap(find.byKey(const Key('account-edit-handle')));
    await _settle(tester);

    expect(find.text('编辑资料'), findsOneWidget);
    expect(find.textContaining('还可修改 1 次 · 4–24 位'), findsOneWidget);
  });

  testWidgets('系统内部账号不会暴露给用户且不阻止其他资料保存', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    controller.currentUser = controller.currentUser!.copyWith(
      handle: 'll_0a72db642113767333c3',
      handleChangesRemaining: 2,
    );
    await _pump(tester, AccountSecurityScreen(controller: controller));

    expect(find.text('ll_0a72db642113767333c3'), findsNothing);
    expect(find.text('呱呱号未设置'), findsOneWidget);
    expect(find.text('由账号服务自动生成'), findsOneWidget);
    expect(find.text('等待生成'), findsOneWidget);

    await tester.tap(find.byType(ListTile).first);
    await _settle(tester);

    final handleField = tester.widget<TextFormField>(
      find.byKey(const Key('profile-handle')),
    );
    expect(handleField.controller?.text, isEmpty);
    expect(handleField.enabled, isFalse);
    expect(find.text('账号服务升级后会自动生成，生成后可修改'), findsOneWidget);

    await tester.enterText(find.byKey(const Key('profile-name')), '新的昵称');
    await tester.pump();
    expect(
      tester
          .widget<TextButton>(find.byKey(const Key('save-profile')))
          .onPressed,
      isNotNull,
    );
    await tester.tap(find.byKey(const Key('save-profile')));
    await _settle(tester);
    expect(controller.currentUser?.name, '新的昵称');
    expect(controller.currentUser?.handle, 'll_0a72db642113767333c3');
  });

  testWidgets('编辑资料存在未保存内容时返回会先确认', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => EditProfileScreen(controller: controller),
                ),
              ),
              child: const Text('打开编辑资料'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('打开编辑资料'));
    await _settle(tester);
    await tester.enterText(find.byKey(const Key('profile-name')), '新的昵称');
    await tester.pump();

    await tester.binding.handlePopRoute();
    await _settle(tester);
    expect(find.text('放弃修改？'), findsOneWidget);
    expect(find.text('你尚未保存本页修改，离开后将无法恢复。'), findsOneWidget);
    await tester.tap(find.text('继续编辑'));
    await _settle(tester);
    expect(find.text('编辑资料'), findsOneWidget);

    await tester.binding.handlePopRoute();
    await _settle(tester);
    await tester.tap(find.text('放弃修改'));
    await _settle(tester);
    expect(find.text('编辑资料'), findsNothing);
    expect(find.text('打开编辑资料'), findsOneWidget);
  });

  testWidgets('登录设备页使用面向用户的提示文字', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(tester, DevicesScreen(controller: controller));
    await _settle(tester);

    expect(find.text('在这里管理各端的登录状态和新消息通知。'), findsOneWidget);
    expect(find.text('登录设备'), findsWidgets);
    expect(find.text('消息通知设备'), findsWidgets);
    expect(find.textContaining('WuKongIM'), findsNothing);
    expect(find.textContaining('APNs'), findsNothing);
    expect(find.textContaining('FCM'), findsNothing);
  });

  testWidgets('登录设备接口失败时显示可重试错误而不是伪装空列表', (tester) async {
    final repository = _FailingSettingsRepository();
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser;
    addTearDown(controller.dispose);

    await _pump(tester, DevicesScreen(controller: controller));
    await _settle(tester);

    expect(find.text('登录设备暂时无法加载'), findsOneWidget);
    expect(find.text('通知设备暂时无法加载'), findsOneWidget);
    expect(find.text('暂无其他登录设备'), findsNothing);
    expect(find.text('暂无消息通知设备'), findsNothing);
    expect(find.text('重新加载'), findsNWidgets(2));
  });

  testWidgets('账号安全提供登录态密码修改且首屏不提前报错', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(tester, AccountSecurityScreen(controller: controller));

    expect(find.text('修改登录密码'), findsOneWidget);
    await tester.tap(find.byKey(const Key('account-change-password')));
    await _settle(tester);

    expect(find.text('验证绑定手机号'), findsOneWidget);
    expect(find.text('请输入收到的验证码'), findsNothing);
    expect(find.text('密码至少 8 个字符'), findsNothing);
    expect(find.text('两次输入的密码不一致'), findsNothing);
    expect(
      tester
          .widget<FilledButton>(find.byKey(const Key('password-change-submit')))
          .onPressed,
      isNull,
    );

    await tester.tap(find.byKey(const Key('password-change-request-code')));
    await _settle(tester);
    expect(
      tester
          .widget<FilledButton>(find.byKey(const Key('password-change-submit')))
          .onPressed,
      isNotNull,
    );
    await tester.enterText(
      find.byKey(const Key('password-change-code')),
      '123456',
    );
    await tester.enterText(
      find.byKey(const Key('password-change-new')),
      'StrongPass456!',
    );
    await tester.enterText(
      find.byKey(const Key('password-change-confirmation')),
      'different',
    );
    await tester.tap(find.byKey(const Key('password-change-submit')));
    await _settle(tester);
    expect(find.text('两次输入的密码不一致'), findsOneWidget);

    await tester.enterText(
      find.byKey(const Key('password-change-confirmation')),
      'StrongPass456!',
    );
    await tester.tap(find.byKey(const Key('password-change-submit')));
    await _settle(tester);
    expect(find.text('密码已修改'), findsOneWidget);
    expect(find.text('重新登录'), findsOneWidget);
    await tester.tap(find.byKey(const Key('password-change-relogin')));
    await _settle(tester);
    expect(controller.authenticated, isFalse);
  });

  testWidgets('修改密码使用服务端策略并按 UTF-8 字节数拦截过长密码', (tester) async {
    final controller = (await tester.runAsync(_controller))!
      ..authPolicy = const AuthPolicy(passwordMinLength: 12);
    addTearDown(controller.dispose);

    await _pump(tester, ChangePasswordScreen(controller: controller));
    expect(find.text('至少 12 个字符，最多 72 字节'), findsOneWidget);

    await tester.tap(find.byKey(const Key('password-change-request-code')));
    await _settle(tester);
    await tester.enterText(
      find.byKey(const Key('password-change-code')),
      '123456',
    );
    await tester.enterText(
      find.byKey(const Key('password-change-new')),
      '12345678901',
    );
    await tester.enterText(
      find.byKey(const Key('password-change-confirmation')),
      '12345678901',
    );
    await tester.tap(find.byKey(const Key('password-change-submit')));
    await _settle(tester);
    expect(find.text('密码至少 12 个字符'), findsOneWidget);

    final passwordOver72Bytes = List.filled(25, '蛙').join();
    await tester.enterText(
      find.byKey(const Key('password-change-new')),
      passwordOver72Bytes,
    );
    await tester.enterText(
      find.byKey(const Key('password-change-confirmation')),
      passwordOver72Bytes,
    );
    await tester.tap(find.byKey(const Key('password-change-submit')));
    await _settle(tester);
    expect(find.text('密码过长，请缩短后重试'), findsOneWidget);
    expect(find.text('密码已修改'), findsNothing);
  });

  testWidgets('密码策略不可用时使用兼容规则保留修改流程', (tester) async {
    final controller = AppController(_FailingAuthPolicyRepository())
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser
      ..authPolicyLoaded = true
      ..authPolicyAvailable = false;
    addTearDown(controller.dispose);

    await _pump(tester, ChangePasswordScreen(controller: controller));

    expect(find.byKey(const Key('password-change-new')), findsOneWidget);
    expect(find.textContaining('至少 8'), findsOneWidget);
    expect(find.byKey(const Key('password-policy-unavailable')), findsNothing);
  });

  testWidgets('换绑手机号首屏不提前报错并完整验证新号码', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(tester, ChangePhoneScreen(controller: controller));

    final action = find.byKey(const Key('phone-change-action'));
    expect(find.text('当前绑定手机号'), findsOneWidget);
    expect(find.text('请输入有效手机号'), findsNothing);
    expect(tester.widget<FilledButton>(action).onPressed, isNull);

    await tester.enterText(find.byKey(const Key('new-phone')), '13900139000');
    await tester.pump();
    expect(tester.widget<FilledButton>(action).onPressed, isNotNull);
    await tester.tap(action);
    await _settle(tester);

    expect(find.byKey(const Key('phone-change-code')), findsOneWidget);
    expect(find.text('修改'), findsOneWidget);
    expect(find.textContaining('后重发'), findsOneWidget);
    expect(tester.widget<FilledButton>(action).onPressed, isNull);

    await tester.enterText(
      find.byKey(const Key('phone-change-code')),
      '123456',
    );
    await tester.pump();
    expect(tester.widget<FilledButton>(action).onPressed, isNotNull);
    await tester.tap(action);
    await _settle(tester);

    expect(find.text('手机号已换绑'), findsOneWidget);
    expect(find.text('新的登录手机号为 139****9000。以后请使用新手机号登录。'), findsOneWidget);
    expect(find.text('新手机号不能与当前绑定相同'), findsNothing);
    await tester.tap(find.byKey(const Key('phone-change-done')));
    await _settle(tester);
    expect(controller.currentUser?.phone, '13900139000');
  });

  testWidgets('换绑手机号拒绝当前号码且不显示无关字段错误', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(tester, ChangePhoneScreen(controller: controller));

    await tester.enterText(find.byKey(const Key('new-phone')), '13800138000');
    await tester.pump();

    expect(
      tester
          .widget<FilledButton>(find.byKey(const Key('phone-change-action')))
          .onPressed,
      isNull,
    );
    expect(find.text('新手机号不能与当前绑定相同'), findsNothing);
    expect(find.text('请输入收到的验证码'), findsNothing);
  });

  testWidgets('密码与手机号验证码请求失败时给出明确提示', (tester) async {
    final repository = _FailingSettingsRepository();
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser
      ..authPolicyLoaded = true
      ..authPolicyAvailable = true;
    addTearDown(controller.dispose);

    await _pump(tester, ChangePasswordScreen(controller: controller));
    await tester.tap(find.byKey(const Key('password-change-request-code')));
    await _settle(tester);
    expect(find.text('验证码发送失败'), findsOneWidget);

    await _pump(tester, ChangePhoneScreen(controller: controller));
    await tester.enterText(find.byKey(const Key('new-phone')), '13900139000');
    await tester.pump();
    await tester.tap(find.byKey(const Key('phone-change-action')));
    await _settle(tester);
    expect(find.text('验证码发送失败'), findsOneWidget);
  });

  testWidgets('注销验证码请求失败时给出明确提示', (tester) async {
    final repository = _FailingSettingsRepository();
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser;
    addTearDown(controller.dispose);

    await _pump(tester, AccountDeletionScreen(controller: controller));
    await tester.tap(find.text('我已理解并确认上述影响'));
    await tester.pump();
    final deletionAction = find.byKey(const Key('account-deletion-action'));
    await tester.ensureVisible(deletionAction);
    await tester.pump();
    expect(tester.widget<FilledButton>(deletionAction).onPressed, isNotNull);
    await tester.tap(deletionAction);
    await _settle(tester);
    expect(controller.error, '注销验证码发送失败');
    expect(find.text('注销验证码发送失败'), findsOneWidget);
  });

  testWidgets('聊天背景选择只写入本机偏好', (tester) async {
    await _pump(tester, const ChatBackgroundSettingsScreen());
    await _settle(tester);

    await tester.tap(find.byKey(const Key('chat-background-softMint')));
    await _settle(tester);

    final preferences = await SharedPreferences.getInstance();
    expect(
      preferences.getString(LocalSettingsStore.chatBackground),
      ChatBackgroundStyle.softMint.name,
    );
    expect(find.text('聊天背景已切换为“柔和薄荷”'), findsOneWidget);
  });

  testWidgets('聊天页读取并应用已保存的本机背景', (tester) async {
    final preferences = await SharedPreferences.getInstance();
    await preferences.setString(
      LocalSettingsStore.chatBackground,
      ChatBackgroundStyle.softMint.name,
    );
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);

    await _pump(
      tester,
      ChatScreen(
        controller: controller,
        conversation: controller.conversations.first,
      ),
    );
    await _settle(tester);

    final surface = tester.widget<ColoredBox>(
      find.byKey(const Key('chat-background-surface')),
    );
    expect(surface.color, LinliColors.brandMint);
  });

  testWidgets('通知偏好写入本机且通用外观操作生效', (tester) async {
    await _pump(tester, const NotificationSettingsScreen());
    await _settle(tester);
    await tester.tap(find.byKey(const Key('notification-enabled-switch')));
    await _settle(tester);

    final preferences = await SharedPreferences.getInstance();
    expect(
      preferences.getBool(LocalSettingsStore.notificationEnabled),
      isFalse,
    );

    var toggled = false;
    await _pump(
      tester,
      GeneralSettingsScreen(onToggleTheme: () => toggled = true),
    );
    await _settle(tester);
    await tester.tap(find.byKey(const Key('general-toggle-theme')));
    expect(toggled, isTrue);
  });

  testWidgets('通知偏好保存失败会回滚开关并给出真实提示', (tester) async {
    await _pump(
      tester,
      const NotificationSettingsScreen(
        store: _FailingLocalSettingsStore(failWrites: true),
      ),
    );

    final row = find.byKey(const Key('notification-enabled-switch'));
    final toggle = find.descendant(
      of: row,
      matching: find.byType(CupertinoSwitch),
    );
    expect(tester.widget<CupertinoSwitch>(toggle).value, isTrue);
    await tester.tap(row);
    await _settle(tester);

    expect(tester.widget<CupertinoSwitch>(toggle).value, isTrue);
    expect(find.text('通知偏好保存失败，已恢复原设置'), findsOneWidget);
  });

  testWidgets('通知偏好读取失败会展示默认值来源并允许重试', (tester) async {
    await _pump(
      tester,
      const NotificationSettingsScreen(
        store: _FailingLocalSettingsStore(failReads: true),
      ),
    );

    expect(find.text('通知偏好读取失败，当前显示默认设置。'), findsOneWidget);
    expect(find.text('重新读取'), findsOneWidget);
  });

  testWidgets('聊天背景写入失败不会显示假成功', (tester) async {
    const store = _FailingLocalSettingsStore(failWrites: true);
    await _pump(tester, const ChatBackgroundSettingsScreen(store: store));
    await tester.tap(find.byKey(const Key('chat-background-softMint')));
    await _settle(tester);
    expect(find.text('聊天背景保存失败，原设置未改变'), findsOneWidget);
    expect(find.text('聊天背景已切换为“柔和薄荷”'), findsNothing);
  });

  testWidgets('反馈草稿写入失败会保留当前内容且不显示假成功', (tester) async {
    final appController = (await tester.runAsync(_controller))!;
    addTearDown(appController.dispose);
    await _pump(
      tester,
      HelpFeedbackScreen(
        controller: appController,
        store: const _FailingLocalSettingsStore(failWrites: true),
      ),
    );
    await tester.enterText(
      find.byKey(const Key('feedback-field')),
      '草稿写入失败时仍要保留当前内容。',
    );
    await tester.pump();
    final saveDraft = find.byKey(const Key('save-feedback-draft'));
    await tester.ensureVisible(saveDraft);
    final saveAction = tester.widget<OutlinedButton>(saveDraft).onPressed;
    expect(saveAction, isNotNull);
    saveAction!();
    await _settle(tester);
    expect(find.text('反馈草稿保存失败，填写内容仍保留在当前页面'), findsOneWidget);
    expect(find.text('草稿写入失败时仍要保留当前内容。'), findsOneWidget);
  });

  testWidgets('隐私页清除持久化的最近搜索', (tester) async {
    final preferences = await SharedPreferences.getInstance();
    await preferences.setStringList(LocalSettingsStore.recentSearches, [
      '林屿',
      '产品小组',
    ]);

    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(tester, PrivacyScreen(controller: controller));
    expect(find.byKey(const Key('privacy-discovery-card')), findsOneWidget);
    expect(find.text('2 条'), findsOneWidget);
    await tester.tap(find.byKey(const Key('privacy-clear-search-history')));
    await _settle(tester);
    expect(find.text('清除最近搜索？'), findsOneWidget);
    await tester.tap(
      find.byKey(const Key('privacy-confirm-clear-search-history')),
    );
    await _settle(tester);

    expect(
      preferences.getStringList(LocalSettingsStore.recentSearches),
      isNull,
    );
    expect(find.text('最近搜索已从本机清除'), findsOneWidget);
    expect(find.text('无记录'), findsOneWidget);
  });

  testWidgets('隐私页可修改呱呱号搜索权限并保留平台禁用状态', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(tester, PrivacyScreen(controller: controller));
    await _settle(tester);

    final handleRow = find.byKey(const Key('privacy-search-by-handle'));
    final phoneRow = find.byKey(const Key('privacy-search-by-phone'));
    expect(handleRow, findsOneWidget);
    expect(phoneRow, findsOneWidget);
    expect(
      tester
          .widget<CupertinoSwitch>(
            find.descendant(
              of: handleRow,
              matching: find.byType(CupertinoSwitch),
            ),
          )
          .onChanged,
      isNotNull,
    );
    expect(
      tester
          .widget<CupertinoSwitch>(
            find.descendant(
              of: phoneRow,
              matching: find.byType(CupertinoSwitch),
            ),
          )
          .onChanged,
      isNull,
    );

    await tester.tap(handleRow);
    await _settle(tester);
    expect(controller.currentUser?.allowSearchByHandle, isFalse);
    expect(find.text('已关闭呱呱号搜索'), findsOneWidget);
  });

  testWidgets('隐私设置保存期间锁定另一开关，避免并发响应覆盖', (tester) async {
    final repository = _DelayedPrivacyRepository();
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser
      ..connected = true;
    addTearDown(controller.dispose);
    await _pump(tester, PrivacyScreen(controller: controller));
    await _settle(tester);

    final handleRow = find.byKey(const Key('privacy-search-by-handle'));
    final phoneRow = find.byKey(const Key('privacy-search-by-phone'));
    await tester.tap(handleRow);
    await tester.pump();

    expect(
      find.descendant(
        of: handleRow,
        matching: find.byType(CupertinoActivityIndicator),
      ),
      findsOneWidget,
    );
    expect(
      tester
          .widget<CupertinoSwitch>(
            find.descendant(
              of: phoneRow,
              matching: find.byType(CupertinoSwitch),
            ),
          )
          .onChanged,
      isNull,
    );

    repository.releaseSave();
    await _settle(tester);
    expect(controller.currentUser?.allowSearchByHandle, isFalse);
  });

  testWidgets('旧服务器未声明隐私写入能力时禁用开关且不发起无效保存', (tester) async {
    final repository = _LegacyPrivacyRepository();
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser
      ..connected = true;
    addTearDown(controller.dispose);
    await _pump(tester, PrivacyScreen(controller: controller));
    await _settle(tester);

    final handleRow = find.byKey(const Key('privacy-search-by-handle'));
    final handleSwitch = tester.widget<CupertinoSwitch>(
      find.descendant(of: handleRow, matching: find.byType(CupertinoSwitch)),
    );
    expect(handleSwitch.onChanged, isNull);
    expect(handleSwitch.activeTrackColor, isNotNull);
    expect(handleSwitch.inactiveTrackColor, handleSwitch.activeTrackColor);
    expect(find.text('当前服务器版本暂不支持修改'), findsNWidgets(2));

    await tester.tap(handleRow);
    await _settle(tester);
    expect(repository.updateProfileCalls, 0);
    expect(controller.currentUser?.allowSearchByHandle, isTrue);
  });

  testWidgets('最近搜索清除失败会保留计数并提示重试', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(
      tester,
      PrivacyScreen(
        controller: controller,
        store: const _FailingLocalSettingsStore(
          failClearRecent: true,
          recentValues: ['林屿', '产品小组'],
        ),
      ),
    );

    expect(find.text('2 条'), findsOneWidget);
    await tester.tap(find.byKey(const Key('privacy-clear-search-history')));
    await _settle(tester);
    await tester.tap(
      find.byKey(const Key('privacy-confirm-clear-search-history')),
    );
    await _settle(tester);

    expect(find.text('2 条'), findsOneWidget);
    expect(find.text('最近搜索清除失败，请稍后重试'), findsOneWidget);
  });

  testWidgets('平台搜索权限失败时本地搜索仍可打开结果', (tester) async {
    final repository = _FailingSearchCapabilitiesRepository();
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser
      ..connected = true
      ..contacts = DemoImRepository.people;
    addTearDown(controller.dispose);
    await _pump(
      tester,
      SearchScreen(
        controller: controller,
        settingsStore: const _FailingLocalSettingsStore(failAddRecent: true),
      ),
    );

    expect(find.text('搜索能力加载失败'), findsOneWidget);
    await tester.enterText(find.byType(TextField).first, '林屿');
    await _settle(tester);
    expect(find.text('林屿'), findsNWidgets(2));
    await tester.tap(find.text('林屿').last);
    await _settle(tester);

    expect(find.byType(FriendProfileScreen), findsOneWidget);
    expect(find.text('最近搜索未能保存，不影响继续查看'), findsOneWidget);
  });

  testWidgets('黑名单加载失败与真实空态严格区分且可重试', (tester) async {
    final repository = _FailingBlockedRepository();
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser;
    addTearDown(controller.dispose);
    await _pump(tester, BlockedUsersScreen(controller: controller));
    await _settle(tester);

    expect(find.text('黑名单暂时无法加载'), findsOneWidget);
    expect(find.text('黑名单为空'), findsNothing);
    expect(find.text('重新加载'), findsOneWidget);
  });

  testWidgets('收藏页按消息类型筛选已同步内容', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    final conversation = controller.conversations.first;
    final now = DateTime(2026, 8, 13, 14);
    await tester.runAsync(() async {
      await controller.favoriteMessage(
        ChatMessage(
          id: 'favorite-text',
          conversationId: conversation.id,
          senderId: controller.currentUser!.id,
          senderName: controller.currentUser!.name,
          text: '收藏文字内容',
          sentAt: now,
          isMine: true,
        ),
      );
      await controller.favoriteMessage(
        ChatMessage(
          id: 'favorite-file',
          conversationId: conversation.id,
          senderId: controller.currentUser!.id,
          senderName: controller.currentUser!.name,
          text: '[文件] 发布说明.pdf',
          sentAt: now,
          isMine: true,
          kind: MessageContentKind.file,
          fileName: '发布说明.pdf',
        ),
      );
    });

    await _pump(tester, FavoritesScreen(controller: controller));
    expect(find.text('收藏文字内容'), findsOneWidget);
    expect(find.text('[文件] 发布说明.pdf'), findsOneWidget);

    await tester.tap(find.byKey(const Key('favorite-filter-file')));
    await _settle(tester);
    expect(find.text('收藏文字内容'), findsNothing);
    expect(find.text('[文件] 发布说明.pdf'), findsOneWidget);

    await tester.tap(find.byKey(const Key('favorite-filter-media')));
    await _settle(tester);
    expect(find.text('没有媒体收藏'), findsOneWidget);
  });

  testWidgets('存储页经二次确认清除所有会话本机消息', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    final conversation = controller.conversations.first;
    await tester.runAsync(() => controller.loadMessages(conversation.id));
    expect(controller.messagesFor(conversation.id), isNotEmpty);

    await _pump(tester, StorageScreen(controller: controller));
    await tester.tap(find.byKey(const Key('storage-clear-local-messages')));
    await _settle(tester);
    expect(find.text('清除本机消息缓存？'), findsOneWidget);
    await tester.tap(find.text('清除本机缓存'));
    await _pumpUntilText(tester, '本机消息缓存已清除');

    for (final item in controller.conversations) {
      expect(controller.messagesFor(item.id), isEmpty);
    }
  });

  testWidgets('本机消息持久化清理失败时保留内存记录且不显示假成功', (tester) async {
    final repository = _FailingPersistRepository();
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser
      ..connected = true;
    controller.conversations = (await tester.runAsync(
      repository.conversations,
    ))!;
    controller.contacts = (await tester.runAsync(repository.contacts))!;
    addTearDown(controller.dispose);
    final conversation = controller.conversations.first;
    await tester.runAsync(() => controller.loadMessages(conversation.id));
    final before = controller.messagesFor(conversation.id).length;
    expect(before, greaterThan(0));

    await _pump(tester, StorageScreen(controller: controller));
    await tester.tap(find.byKey(const Key('storage-clear-local-messages')));
    await _settle(tester);
    await tester.tap(find.text('清除本机缓存'));
    await _pumpUntilText(tester, '本机消息缓存清理失败，聊天记录未被修改');

    expect(controller.messagesFor(conversation.id), hasLength(before));
    expect(find.text('本机消息缓存清理失败，聊天记录未被修改'), findsOneWidget);
  });

  testWidgets('帮助反馈明确保存草稿而不伪装提交', (tester) async {
    final appController = (await tester.runAsync(_controller))!;
    addTearDown(appController.dispose);
    await _pump(tester, HelpFeedbackScreen(controller: appController));
    await _settle(tester);
    await tester.enterText(
      find.byKey(const Key('feedback-field')),
      '在弱网下发送图片时希望展示更清晰的重试原因。',
    );
    await tester.pump();
    expect(find.byKey(const Key('submit-feedback')), findsOneWidget);
    expect(find.byKey(const Key('save-feedback-draft')), findsOneWidget);
    expect(
      tester
          .widget<FilledButton>(find.byKey(const Key('submit-feedback')))
          .onPressed,
      isNotNull,
    );
  });

  testWidgets('设置中心支持深色与 200% 字体且无布局异常', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MediaQuery(
        data: const MediaQueryData(textScaler: TextScaler.linear(2)),
        child: MaterialApp(
          theme: buildLinliTheme(Brightness.dark),
          home: SettingsScreen(controller: controller, onToggleTheme: () {}),
        ),
      ),
    );
    await _settle(tester);

    expect(find.text('账号与安全'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

Future<AppController> _controller() async {
  final repository = DemoImRepository(
    latency: Duration.zero,
    store: _MemorySecureLocalStore(),
  );
  final controller = AppController(repository);
  controller.authenticated = true;
  controller.currentUser = DemoImRepository.demoUser;
  controller.connected = true;
  controller.authPolicyLoaded = true;
  controller.authPolicyAvailable = true;
  controller.conversations = await repository.conversations();
  controller.contacts = await repository.contacts();
  return controller;
}

class _MemorySecureLocalStore extends SecureLocalStore {
  final Map<String, Object> values = <String, Object>{};

  @override
  Future<void> writeJson(String key, Object value) async {
    values[key] = value;
  }

  @override
  Future<Object?> readJson(String key) async => values[key];

  @override
  Future<void> remove(String key) async {
    values.remove(key);
  }

  @override
  Future<void> clearAccountData() async {
    values.clear();
  }
}

class _FailingBlockedRepository extends DemoImRepository {
  _FailingBlockedRepository() : super(latency: Duration.zero);

  @override
  Future<List<AppUser>> blockedUsers() =>
      Future<List<AppUser>>.error(StateError('network unavailable'));
}

class _RetryProfileRepository extends DemoImRepository {
  _RetryProfileRepository({this.alwaysFail = false})
    : super(latency: Duration.zero);

  final bool alwaysFail;
  int updateProfileCalls = 0;

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
    updateProfileCalls += 1;
    if (alwaysFail || updateProfileCalls == 1) {
      throw const ImApiException(
        statusCode: 409,
        code: 'HANDLE_TAKEN',
        message: 'handle is already in use',
      );
    }
    return super.updateProfile(
      name: name,
      handle: handle,
      signature: signature,
      gender: gender,
      avatarMediaId: avatarMediaId,
      allowSearchByHandle: allowSearchByHandle,
      allowSearchByPhone: allowSearchByPhone,
    );
  }
}

class _FailingSearchCapabilitiesRepository extends DemoImRepository {
  _FailingSearchCapabilitiesRepository() : super(latency: Duration.zero);

  @override
  Future<UserSearchCapabilities> searchCapabilities() =>
      Future<UserSearchCapabilities>.error(StateError('network unavailable'));
}

class _LegacyPrivacyRepository extends DemoImRepository {
  _LegacyPrivacyRepository() : super(latency: Duration.zero);

  int updateProfileCalls = 0;

  @override
  Future<UserSearchCapabilities> searchCapabilities() async =>
      const UserSearchCapabilities(
        allowSearchByHandle: true,
        allowSearchByPhone: false,
      );

  @override
  Future<AppUser> updateProfile({
    String? name,
    String? handle,
    String? signature,
    String? gender,
    String? avatarMediaId,
    bool? allowSearchByHandle,
    bool? allowSearchByPhone,
  }) {
    updateProfileCalls += 1;
    return super.updateProfile(
      name: name,
      handle: handle,
      signature: signature,
      gender: gender,
      avatarMediaId: avatarMediaId,
      allowSearchByHandle: allowSearchByHandle,
      allowSearchByPhone: allowSearchByPhone,
    );
  }
}

class _DelayedPrivacyRepository extends DemoImRepository {
  _DelayedPrivacyRepository() : super(latency: Duration.zero);

  final Completer<void> _saveGate = Completer<void>();

  void releaseSave() => _saveGate.complete();

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
    await _saveGate.future;
    return super.updateProfile(
      name: name,
      handle: handle,
      signature: signature,
      gender: gender,
      avatarMediaId: avatarMediaId,
      allowSearchByHandle: allowSearchByHandle,
      allowSearchByPhone: allowSearchByPhone,
    );
  }
}

class _FailingPersistRepository extends DemoImRepository {
  _FailingPersistRepository()
    : super(latency: Duration.zero, store: _MemorySecureLocalStore());

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) => Future<void>.error(StateError('storage unavailable'));
}

class _FailingLocalSettingsStore extends LocalSettingsStore {
  const _FailingLocalSettingsStore({
    this.failReads = false,
    this.failWrites = false,
    this.failClearRecent = false,
    this.failAddRecent = false,
    this.recentValues = const [],
  });

  final bool failReads;
  final bool failWrites;
  final bool failClearRecent;
  final bool failAddRecent;
  final List<String> recentValues;

  @override
  Future<bool> readBool(String key, {required bool fallback}) {
    if (failReads) return Future<bool>.error(StateError('read failed'));
    return Future<bool>.value(fallback);
  }

  @override
  Future<String> readString(String key, {String fallback = ''}) {
    if (failReads) return Future<String>.error(StateError('read failed'));
    return Future<String>.value(fallback);
  }

  @override
  Future<List<String>> readRecentSearches() {
    if (failReads) {
      return Future<List<String>>.error(StateError('read failed'));
    }
    return Future<List<String>>.value(recentValues);
  }

  @override
  Future<void> writeBool(String key, bool value) {
    if (failWrites) return Future<void>.error(StateError('write failed'));
    return Future<void>.value();
  }

  @override
  Future<void> writeString(String key, String value) {
    if (failWrites) return Future<void>.error(StateError('write failed'));
    return Future<void>.value();
  }

  @override
  Future<void> clearRecentSearches() {
    if (failClearRecent) {
      return Future<void>.error(StateError('clear failed'));
    }
    return Future<void>.value();
  }

  @override
  Future<void> addRecentSearch(String value) {
    if (failAddRecent) {
      return Future<void>.error(StateError('write failed'));
    }
    return Future<void>.value();
  }
}

class _FailingSettingsRepository extends DemoImRepository {
  _FailingSettingsRepository() : super(latency: Duration.zero);

  @override
  Future<List<UserDevice>> userDevices() =>
      Future<List<UserDevice>>.error(StateError('network unavailable'));

  @override
  Future<List<ImDeviceSession>> imDeviceSessions() =>
      Future<List<ImDeviceSession>>.error(StateError('network unavailable'));

  @override
  Future<void> requestPasswordResetCode(String phone) =>
      Future<void>.error(StateError('provider unavailable'));

  @override
  Future<void> requestPhoneChangeCode(String phone) =>
      Future<void>.error(StateError('provider unavailable'));

  @override
  Future<void> requestAccountDeletionCode() =>
      Future<void>.error(StateError('provider unavailable'));
}

class _FailingAuthPolicyRepository extends DemoImRepository {
  _FailingAuthPolicyRepository() : super(latency: Duration.zero);

  @override
  Future<AuthPolicy> authPolicy() =>
      Future<AuthPolicy>.error(StateError('network unavailable'));
}

Future<void> _pump(WidgetTester tester, Widget home) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(
    MaterialApp(theme: buildLinliTheme(Brightness.light), home: home),
  );
  await _settle(tester);
}

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 6; i++) {
    await tester.pump(const Duration(milliseconds: 100));
  }
}

Future<void> _pumpUntilText(
  WidgetTester tester,
  String text, {
  int attempts = 60,
}) async {
  for (var i = 0; i < attempts && find.text(text).evaluate().isEmpty; i++) {
    await tester.pump(const Duration(milliseconds: 100));
  }
  expect(find.text(text), findsOneWidget);
}
