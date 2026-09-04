import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:image_picker/image_picker.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/avatar_image.dart';
import '../../core/browser_notification_permission.dart';
import '../../core/image_send_editor.dart';
import '../../core/media_opener.dart';
import '../../core/models.dart';
import '../../core/user_identity.dart';
import '../legal_documents.dart';
import '../widgets/linli_widgets.dart';
import 'chat_screen.dart';
import 'settings_preferences.dart';

class MyInviteCodeScreen extends StatefulWidget {
  const MyInviteCodeScreen({super.key, required this.controller});
  final AppController controller;

  @override
  State<MyInviteCodeScreen> createState() => _MyInviteCodeScreenState();
}

class _MyInviteCodeScreenState extends State<MyInviteCodeScreen> {
  InviteCodeProfile? profile;
  Object? loadError;
  bool loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      loading = true;
      loadError = null;
    });
    try {
      final value = await widget.controller.loadInviteCode();
      if (mounted) setState(() => profile = value);
    } catch (error) {
      if (mounted) setState(() => loadError = error);
    } finally {
      if (mounted) setState(() => loading = false);
    }
  }

  Future<void> _copy() async {
    final code = profile?.code;
    if (code == null) return;
    await Clipboard.setData(ClipboardData(text: code));
    if (mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('邀请码已复制')));
    }
  }

  Future<void> _change() async {
    final current = profile;
    if (current == null || current.selfChangesRemaining <= 0) return;
    final input = TextEditingController();
    final value = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('修改邀请码'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('仅能自行修改一次。修改后旧邀请码立即失效且不能恢复。'),
            const SizedBox(height: 14),
            TextField(
              key: const Key('custom-invite-code'),
              controller: input,
              autofocus: true,
              maxLength: 20,
              textCapitalization: TextCapitalization.characters,
              decoration: const InputDecoration(
                labelText: '新邀请码',
                helperText: '6–20 位字母、数字、下划线或短横线',
              ),
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, input.text),
            child: const Text('确认修改'),
          ),
        ],
      ),
    );
    input.dispose();
    if (value == null) return;
    final normalized = value.trim().toUpperCase();
    if (!RegExp(
      r'^[A-Z0-9](?:[A-Z0-9_-]{4,18})[A-Z0-9]$',
    ).hasMatch(normalized)) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('邀请码格式不正确')));
      }
      return;
    }
    final updated = await widget.controller.changeInviteCode(normalized);
    if (!mounted) return;
    if (updated == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '邀请码修改失败')),
      );
      return;
    }
    setState(() => profile = updated);
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('邀请码已修改，旧邀请码已失效')));
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('我的邀请码')),
    body: loading
        ? const Center(child: CircularProgressIndicator())
        : loadError != null || profile == null
        ? Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Text('邀请码暂时无法加载'),
                const SizedBox(height: 12),
                OutlinedButton(onPressed: _load, child: const Text('重试')),
              ],
            ),
          )
        : ListView(
            padding: const EdgeInsets.all(24),
            children: [
              Center(
                child: Container(
                  padding: const EdgeInsets.all(18),
                  decoration: BoxDecoration(
                    color: Colors.white,
                    borderRadius: BorderRadius.circular(22),
                  ),
                  child: QrImageView(
                    data: profile!.qrPayload,
                    size: 230,
                    backgroundColor: Colors.white,
                  ),
                ),
              ),
              const SizedBox(height: 22),
              Text(
                profile!.code,
                key: const Key('my-invite-code'),
                textAlign: TextAlign.center,
                style: Theme.of(context).textTheme.headlineMedium?.copyWith(
                  fontWeight: FontWeight.w800,
                  letterSpacing: 2,
                ),
              ),
              const SizedBox(height: 6),
              Text(
                profile!.status == 'active' ? '邀请码有效' : '邀请码已被停用',
                textAlign: TextAlign.center,
                style: TextStyle(
                  color: profile!.status == 'active'
                      ? LinliColors.systemGreen
                      : Theme.of(context).colorScheme.error,
                ),
              ),
              const SizedBox(height: 22),
              FilledButton.icon(
                onPressed: _copy,
                icon: const Icon(CupertinoIcons.doc_on_doc),
                label: const Text('复制邀请码'),
              ),
              const SizedBox(height: 10),
              OutlinedButton.icon(
                key: const Key('change-invite-code'),
                onPressed: profile!.selfChangesRemaining > 0 ? _change : null,
                icon: const Icon(CupertinoIcons.pencil),
                label: Text(
                  profile!.selfChangesRemaining > 0
                      ? '修改邀请码（剩余 1 次）'
                      : '邀请码修改次数已用完',
                ),
              ),
              const SizedBox(height: 18),
              Text(
                '对方注册时填写或扫描此邀请码即可记录邀请关系。不会自动添加好友，也不会展示对方账号信息。',
                textAlign: TextAlign.center,
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ],
          ),
  );
}

class SettingsScreen extends StatelessWidget {
  const SettingsScreen({
    super.key,
    required this.controller,
    required this.onToggleTheme,
    this.settingsStore = const LocalSettingsStore(),
  });

  final AppController controller;
  final VoidCallback onToggleTheme;
  final LocalSettingsStore settingsStore;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('设置')),
    body: ListView(
      key: const Key('settings-list'),
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 36),
      children: [
        const SectionHeader('账号'),
        SectionCard(
          children: [
            _SettingsRow(
              key: const Key('settings-account'),
              icon: CupertinoIcons.person_crop_circle,
              title: '账号与安全',
              subtitle: '密码、手机号、登录设备与账号注销',
              onTap: () =>
                  _push(context, AccountSecurityScreen(controller: controller)),
            ),
            _SettingsRow(
              key: const Key('settings-favorites'),
              icon: CupertinoIcons.bookmark,
              title: '我的收藏',
              subtitle: '查看已收藏的消息与文件',
              onTap: () =>
                  _push(context, FavoritesScreen(controller: controller)),
            ),
            _SettingsRow(
              key: const Key('settings-devices'),
              icon: CupertinoIcons.device_phone_portrait,
              title: '登录设备',
              subtitle: '当前设备与其他登录会话',
              onTap: () =>
                  _push(context, DevicesScreen(controller: controller)),
            ),
          ],
        ),
        const SectionHeader('偏好与隐私'),
        SectionCard(
          children: [
            _SettingsRow(
              key: const Key('settings-notifications'),
              icon: CupertinoIcons.bell,
              title: '消息通知',
              subtitle: '预览、声音与振动偏好',
              onTap: () => _push(
                context,
                NotificationSettingsScreen(
                  controller: controller,
                  store: settingsStore,
                ),
              ),
            ),
            _SettingsRow(
              key: const Key('settings-privacy'),
              icon: CupertinoIcons.shield,
              title: '隐私与安全',
              subtitle: '数据保护、搜索记录与黑名单说明',
              onTap: () => _push(
                context,
                PrivacyScreen(controller: controller, store: settingsStore),
              ),
            ),
            _SettingsRow(
              key: const Key('settings-general'),
              icon: CupertinoIcons.slider_horizontal_3,
              title: '通用',
              subtitle: '外观与语言',
              onTap: () => _push(
                context,
                GeneralSettingsScreen(onToggleTheme: onToggleTheme),
              ),
            ),
            _SettingsRow(
              key: const Key('settings-chat-background'),
              icon: CupertinoIcons.paintbrush,
              title: '聊天背景',
              subtitle: '选择舒适、统一的会话底色',
              onTap: () => _push(
                context,
                ChatBackgroundSettingsScreen(store: settingsStore),
              ),
            ),
            _SettingsRow(
              key: const Key('settings-storage'),
              icon: CupertinoIcons.archivebox,
              title: '存储空间',
              subtitle: '查看并清理本机消息缓存',
              onTap: () =>
                  _push(context, StorageScreen(controller: controller)),
            ),
          ],
        ),
        const SectionHeader('支持'),
        SectionCard(
          children: [
            _SettingsRow(
              key: const Key('settings-help'),
              icon: CupertinoIcons.question_circle,
              title: '帮助与反馈',
              subtitle: '常见问题与反馈草稿',
              onTap: () => _push(
                context,
                HelpFeedbackScreen(
                  controller: controller,
                  store: settingsStore,
                ),
              ),
            ),
            _SettingsRow(
              key: const Key('settings-about'),
              icon: CupertinoIcons.info_circle,
              title: '关于青蛙呱呱',
              subtitle: '版本、用户协议与隐私政策',
              onTap: () => _push(context, const AboutScreen()),
            ),
          ],
        ),
        const SectionHeader('登录'),
        SectionCard(
          children: [
            _SettingsRow(
              key: const Key('settings-logout'),
              icon: CupertinoIcons.square_arrow_right,
              title: '退出登录',
              subtitle: '清除本机登录凭据和账号缓存',
              destructive: true,
              onTap: () => _confirmLogout(context),
            ),
          ],
        ),
      ],
    ),
  );

  void _push(BuildContext context, Widget screen) {
    Navigator.of(context).push(MaterialPageRoute(builder: (_) => screen));
  }

  void _confirmLogout(BuildContext context) => showCupertinoModalPopup<void>(
    context: context,
    builder: (sheetContext) => CupertinoActionSheet(
      title: const Text('退出登录？'),
      message: const Text('本机登录凭据与账号缓存将清除；云端消息和联系人不会删除，重新登录后可以再次同步。'),
      actions: [
        CupertinoActionSheetAction(
          isDestructiveAction: true,
          onPressed: () async {
            Navigator.pop(sheetContext);
            await controller.logout();
            if (context.mounted) {
              Navigator.of(context).popUntil((route) => route.isFirst);
            }
          },
          child: const Text('退出登录'),
        ),
      ],
      cancelButton: CupertinoActionSheetAction(
        onPressed: () => Navigator.pop(sheetContext),
        child: const Text('取消'),
      ),
    ),
  );
}

class AccountSecurityScreen extends StatefulWidget {
  const AccountSecurityScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<AccountSecurityScreen> createState() => _AccountSecurityScreenState();
}

class _AccountSecurityScreenState extends State<AccountSecurityScreen> {
  AppController get controller => widget.controller;
  bool profileLoading = true;
  bool profileUnavailable = false;

  @override
  void initState() {
    super.initState();
    unawaited(_refreshProfile());
  }

  Future<void> _refreshProfile() async {
    setState(() {
      profileLoading = true;
      profileUnavailable = false;
    });
    final loaded = await controller.refreshProfile(reportError: false);
    if (!mounted) return;
    setState(() {
      profileLoading = false;
      profileUnavailable = !loaded;
    });
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: controller,
    builder: (context, _) {
      final user = controller.currentUser;
      final publicHandle = publicUserHandle(user?.handle);
      final handleIncomplete = publicHandle == null;
      final canEditHandle =
          !profileLoading &&
          !profileUnavailable &&
          !handleIncomplete &&
          (user?.handleChangesRemaining ?? 0) > 0;
      return Scaffold(
        appBar: const GlassAppBar(title: Text('账号与安全')),
        body: ListView(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 36),
          children: [
            if (profileUnavailable)
              _SettingsNotice(
                key: const Key('account-profile-refresh-error'),
                message: '个人资料暂时无法更新，请重试',
                actionLabel: '重试',
                onAction: _refreshProfile,
              ),
            SectionCard(
              children: [
                ListTile(
                  minTileHeight: 84,
                  contentPadding: const EdgeInsets.symmetric(horizontal: 16),
                  leading: PersonAvatar(
                    name: user?.name ?? '我',
                    size: 56,
                    avatarUrl: user?.avatarUrl,
                    online: controller.connected,
                  ),
                  title: Text(
                    user?.name ?? '我的账号',
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                  subtitle: Text(
                    publicHandle == null ? '呱呱号未设置' : '@$publicHandle',
                  ),
                  trailing: const Icon(
                    CupertinoIcons.chevron_forward,
                    size: 17,
                  ),
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => EditProfileScreen(controller: controller),
                    ),
                  ),
                ),
              ],
            ),
            const SectionHeader('账号标识'),
            SectionCard(
              children: [
                _SettingsRow(
                  key: const Key('account-edit-handle'),
                  icon: CupertinoIcons.at,
                  title: '呱呱号',
                  subtitle: publicHandle ?? '由账号服务自动生成',
                  status: profileLoading
                      ? '正在更新'
                      : profileUnavailable
                      ? '状态待确认'
                      : handleIncomplete
                      ? '等待生成'
                      : (user?.handleChangesRemaining ?? 0) > 0
                      ? '还可修改 ${user!.handleChangesRemaining} 次'
                      : '不可修改',
                  onTap: canEditHandle
                      ? () => Navigator.of(context).push(
                          MaterialPageRoute(
                            builder: (_) =>
                                EditProfileScreen(controller: controller),
                          ),
                        )
                      : null,
                ),
                _SettingsRow(
                  icon: CupertinoIcons.phone,
                  title: '绑定手机号',
                  subtitle: _maskedPhone(user?.phone),
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => ChangePhoneScreen(controller: controller),
                    ),
                  ),
                ),
              ],
            ),
            const SectionHeader('安全'),
            SectionCard(
              children: [
                _SettingsRow(
                  key: const Key('account-change-password'),
                  icon: CupertinoIcons.lock,
                  title: '修改登录密码',
                  subtitle: '验证绑定手机号后更新，完成后重新登录',
                  onTap: user?.phone?.trim().isNotEmpty == true
                      ? () => Navigator.of(context).push(
                          MaterialPageRoute(
                            builder: (_) =>
                                ChangePasswordScreen(controller: controller),
                          ),
                        )
                      : null,
                ),
                _SettingsRow(
                  icon: CupertinoIcons.device_phone_portrait,
                  title: '登录设备',
                  subtitle: '检查当前设备与在线状态',
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => DevicesScreen(controller: controller),
                    ),
                  ),
                ),
                _SettingsRow(
                  icon: CupertinoIcons.delete,
                  title: '注销账号',
                  subtitle: '永久删除资料、关系与云端消息',
                  destructive: true,
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) =>
                          AccountDeletionScreen(controller: controller),
                    ),
                  ),
                ),
              ],
            ),
          ],
        ),
      );
    },
  );
}

class ChangePasswordScreen extends StatefulWidget {
  const ChangePasswordScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<ChangePasswordScreen> createState() => _ChangePasswordScreenState();
}

class _ChangePasswordScreenState extends State<ChangePasswordScreen> {
  final formKey = GlobalKey<FormState>();
  final codeController = TextEditingController();
  final passwordController = TextEditingController();
  final confirmationController = TextEditingController();
  bool codeRequested = false;
  bool busy = false;
  bool submitted = false;
  bool obscurePassword = true;
  bool obscureConfirmation = true;

  String get phone => widget.controller.currentUser?.phone?.trim() ?? '';

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || widget.controller.authPolicyLoaded) return;
      unawaited(widget.controller.refreshAuthPolicy());
    });
  }

  @override
  void dispose() {
    codeController.dispose();
    passwordController.dispose();
    confirmationController.dispose();
    super.dispose();
  }

  Future<void> _requestCode() async {
    if (phone.isEmpty || busy) return;
    setState(() => busy = true);
    final success = await widget.controller.requestResetCode(phone);
    if (!mounted) return;
    setState(() {
      busy = false;
      if (success) codeRequested = true;
    });
    if (success) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('验证码已发送至 ${_maskedPhone(phone)}')));
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '验证码发送失败，请稍后重试')),
      );
    }
  }

  Future<void> _submit() async {
    if (busy) return;
    setState(() => submitted = true);
    if (!codeRequested) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('请先获取短信验证码')));
      return;
    }
    if (!(formKey.currentState?.validate() ?? false)) return;
    setState(() => busy = true);
    final success = await widget.controller.resetPassword(
      phone: phone,
      code: codeController.text,
      password: passwordController.text,
    );
    if (!mounted) return;
    setState(() => busy = false);
    if (!success) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '密码修改失败，请稍后重试')),
      );
      return;
    }
    await showCupertinoDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (dialogContext) => CupertinoAlertDialog(
        title: const Text('密码已修改'),
        content: const Text('本机将退出登录；其他设备的登录凭据到期后也需要重新验证。'),
        actions: [
          CupertinoDialogAction(
            key: const Key('password-change-relogin'),
            onPressed: () => Navigator.of(dialogContext).pop(),
            child: const Text('重新登录'),
          ),
        ],
      ),
    );
    if (mounted) await widget.controller.logout();
  }

  Widget _buildForm(BuildContext context) => Form(
    key: formKey,
    autovalidateMode: submitted
        ? AutovalidateMode.onUserInteraction
        : AutovalidateMode.disabled,
    child: ListView(
      keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 36),
      children: [
        SectionCard(
          children: [
            _SettingsRow(
              icon: CupertinoIcons.phone,
              title: '验证绑定手机号',
              subtitle: _maskedPhone(phone),
              status: '当前账号',
            ),
          ],
        ),
        const SizedBox(height: 12),
        _PageIntro('为保护账号安全，修改成功后本机会退出登录。请使用新密码重新登录。'),
        const SectionHeader('短信验证'),
        TextFormField(
          key: const Key('password-change-code'),
          controller: codeController,
          enabled: !busy,
          keyboardType: TextInputType.number,
          textInputAction: TextInputAction.next,
          autofillHints: const [AutofillHints.oneTimeCode],
          decoration: InputDecoration(
            labelText: '验证码',
            helperText: codeRequested ? '验证码 5 分钟内有效' : '验证码将发送到当前绑定手机号',
            suffixIcon: TextButton(
              key: const Key('password-change-request-code'),
              onPressed: busy || phone.isEmpty ? null : _requestCode,
              child: Text(codeRequested ? '重新获取' : '获取验证码'),
            ),
          ),
          validator: (value) =>
              (value?.trim().length ?? 0) >= 4 ? null : '请输入收到的验证码',
        ),
        const SectionHeader('设置新密码'),
        TextFormField(
          key: const Key('password-change-new'),
          controller: passwordController,
          enabled: !busy,
          obscureText: obscurePassword,
          textInputAction: TextInputAction.next,
          autofillHints: const [AutofillHints.newPassword],
          decoration: InputDecoration(
            labelText: '新密码',
            helperText: widget.controller.authPolicy.passwordHelperText,
            suffixIcon: IconButton(
              tooltip: obscurePassword ? '显示密码' : '隐藏密码',
              onPressed: () =>
                  setState(() => obscurePassword = !obscurePassword),
              icon: Icon(
                obscurePassword ? CupertinoIcons.eye : CupertinoIcons.eye_slash,
              ),
            ),
          ),
          validator: (value) =>
              widget.controller.authPolicy.passwordError(value ?? ''),
        ),
        const SizedBox(height: 14),
        TextFormField(
          key: const Key('password-change-confirmation'),
          controller: confirmationController,
          enabled: !busy,
          obscureText: obscureConfirmation,
          textInputAction: TextInputAction.done,
          autofillHints: const [AutofillHints.newPassword],
          decoration: InputDecoration(
            labelText: '再次输入新密码',
            suffixIcon: IconButton(
              tooltip: obscureConfirmation ? '显示密码' : '隐藏密码',
              onPressed: () =>
                  setState(() => obscureConfirmation = !obscureConfirmation),
              icon: Icon(
                obscureConfirmation
                    ? CupertinoIcons.eye
                    : CupertinoIcons.eye_slash,
              ),
            ),
          ),
          validator: (value) =>
              value == passwordController.text ? null : '两次输入的密码不一致',
          onFieldSubmitted: (_) => _submit(),
        ),
        const SizedBox(height: 24),
        FilledButton(
          key: const Key('password-change-submit'),
          onPressed: busy || !codeRequested ? null : _submit,
          child: busy
              ? const SizedBox.square(
                  dimension: 20,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('确认修改密码'),
        ),
      ],
    ),
  );

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('修改登录密码')),
    body: AnimatedBuilder(
      animation: widget.controller,
      builder: (context, _) => _buildForm(context),
    ),
  );
}

class EditProfileScreen extends StatefulWidget {
  const EditProfileScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<EditProfileScreen> createState() => _EditProfileScreenState();
}

class _EditProfileScreenState extends State<EditProfileScreen> {
  final formKey = GlobalKey<FormState>();
  late final TextEditingController nameController;
  late final TextEditingController handleController;
  late final TextEditingController signatureController;
  late String initialName;
  late String initialHandle;
  late bool initialHandleIsInternal;
  late String initialSignature;
  late String initialGender;
  AppUser? _lastProfile;
  bool _syncingDraft = false;
  bool _profileLoading = true;
  bool _profileUnavailable = false;
  late String selectedGender;
  Uint8List? avatarBytes;
  String? avatarFileName;
  String? avatarMime;
  String? avatarPath;
  String? saveError;
  bool saving = false;
  bool allowExit = false;

  @override
  void initState() {
    super.initState();
    final user = widget.controller.currentUser;
    _lastProfile = user;
    initialName = user?.name ?? '';
    final submittedInitialHandle = user?.handle.trim() ?? '';
    initialHandleIsInternal = isInternalUserHandle(submittedInitialHandle);
    initialHandle = publicUserHandle(submittedInitialHandle) ?? '';
    initialSignature = user?.signature ?? user?.presence ?? '';
    initialGender = switch (user?.gender) {
      'male' => 'male',
      'female' => 'female',
      _ => 'unspecified',
    };
    selectedGender = initialGender;
    nameController = TextEditingController(text: initialName)
      ..addListener(_onDraftChanged);
    handleController = TextEditingController(text: initialHandle)
      ..addListener(_onDraftChanged);
    signatureController = TextEditingController(text: initialSignature)
      ..addListener(_onDraftChanged);
    widget.controller.addListener(_onProfileChanged);
    unawaited(_refreshProfile());
  }

  void _onDraftChanged() {
    if (!mounted || _syncingDraft) return;
    setState(() => saveError = null);
  }

  Future<void> _refreshProfile() async {
    setState(() {
      _profileLoading = true;
      _profileUnavailable = false;
    });
    final loaded = await widget.controller.refreshProfile(reportError: false);
    if (!mounted) return;
    setState(() {
      _profileLoading = false;
      _profileUnavailable = !loaded;
    });
  }

  void _onProfileChanged() {
    final user = widget.controller.currentUser;
    if (!mounted ||
        user == null ||
        user.id != _lastProfile?.id ||
        identical(user, _lastProfile)) {
      return;
    }
    final keepName = _nameChanged;
    final keepHandle = _handleChanged;
    final keepSignature = _signatureChanged;
    final keepGender = selectedGender != initialGender;
    setState(() {
      _syncingDraft = true;
      initialName = user.name;
      initialHandle = publicUserHandle(user.handle) ?? '';
      initialHandleIsInternal = isInternalUserHandle(user.handle);
      initialSignature = user.signature ?? user.presence;
      initialGender = switch (user.gender) {
        'male' => 'male',
        'female' => 'female',
        _ => 'unspecified',
      };
      // Refresh untouched fields, but never overwrite a draft already typed.
      if (!keepName) nameController.text = initialName;
      if (!keepHandle) handleController.text = initialHandle;
      if (!keepSignature) signatureController.text = initialSignature;
      if (!keepGender) selectedGender = initialGender;
      _lastProfile = user;
      _syncingDraft = false;
    });
  }

  bool get _nameChanged => nameController.text.trim() != initialName.trim();
  bool get _handleChanged =>
      handleController.text.trim().toLowerCase() != initialHandle.toLowerCase();
  bool get _signatureChanged =>
      signatureController.text.trim() != initialSignature.trim();

  bool get _hasChanges =>
      avatarBytes != null ||
      _nameChanged ||
      _handleChanged ||
      _signatureChanged ||
      selectedGender != initialGender;

  bool get _draftLooksValid {
    final name = nameController.text.trim();
    final handle = handleController.text.trim().toLowerCase();
    final signature = signatureController.text.trim();
    final handleIsValid = RegExp(r'^[a-z0-9_]{4,24}$').hasMatch(handle);
    return (!_nameChanged || (name.isNotEmpty && name.runes.length <= 40)) &&
        (!_handleChanged || handleIsValid) &&
        (!_signatureChanged || signature.runes.length <= 160);
  }

  @override
  void dispose() {
    widget.controller.removeListener(_onProfileChanged);
    nameController.dispose();
    handleController.dispose();
    signatureController.dispose();
    super.dispose();
  }

  Future<void> _chooseAvatarSource() async {
    if (saving) return;
    final source = await showCupertinoModalPopup<ImageSource>(
      context: context,
      builder: (sheetContext) => CupertinoActionSheet(
        title: const Text('更换头像'),
        message: Text(
          kIsWeb
              ? '选择 JPG、PNG 或 WebP 图片，保存后会作为新头像。'
              : '选择照片后可以裁剪和旋转，头像会按正方形保存。',
        ),
        actions: [
          if (!kIsWeb)
            CupertinoActionSheetAction(
              key: const Key('profile-avatar-camera'),
              onPressed: () => Navigator.pop(sheetContext, ImageSource.camera),
              child: const Text('拍照'),
            ),
          CupertinoActionSheetAction(
            key: const Key('profile-avatar-gallery'),
            onPressed: () => Navigator.pop(sheetContext, ImageSource.gallery),
            child: Text(kIsWeb ? '从电脑选择图片' : '从手机相册选择'),
          ),
        ],
        cancelButton: CupertinoActionSheetAction(
          onPressed: () => Navigator.pop(sheetContext),
          child: const Text('取消'),
        ),
      ),
    );
    if (source != null && mounted) await _pickAvatar(source);
  }

  Future<void> _pickAvatar(ImageSource source) async {
    XFile? file;
    try {
      file = await ImagePicker().pickImage(
        source: source,
        imageQuality: 88,
        maxWidth: 1600,
        maxHeight: 1600,
      );
      if (file == null) return;
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('无法读取头像图片，请检查相册权限后重试')));
      return;
    }
    final selectedFile = file;
    Uint8List bytes;
    try {
      bytes = await selectedFile.readAsBytes();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(kIsWeb ? '无法读取所选图片，请重新选择' : '无法读取头像图片，请检查相册权限后重试'),
        ),
      );
      return;
    }
    if (!mounted) return;
    if (bytes.length > 8 * 1024 * 1024) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('头像不能超过 8 MB')));
      return;
    }
    final mimeType = avatarImageMimeType(bytes);
    if (mimeType == null) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('请选择 JPG、PNG 或 WebP 格式的图片')));
      return;
    }
    Uint8List? editedBytes = bytes;
    if (!kIsWeb) {
      try {
        editedBytes = await editAvatarImage(context, bytes);
      } catch (_) {
        if (!mounted) return;
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('头像处理失败，请重新选择图片')));
        return;
      }
    }
    if (!mounted || editedBytes == null) return;
    final editedMime = avatarImageMimeType(editedBytes);
    if (editedMime == null || editedBytes.length > 8 * 1024 * 1024) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('头像处理失败，请重新选择图片')));
      return;
    }
    setState(() {
      avatarBytes = editedBytes;
      avatarFileName = editedMime == 'image/png'
          ? 'avatar.png'
          : editedMime == 'image/webp'
          ? 'avatar.webp'
          : 'avatar.jpg';
      avatarPath = null;
      avatarMime = editedMime;
      saveError = null;
    });
  }

  Future<void> _chooseGender() async {
    if (saving) return;
    final value = await showCupertinoModalPopup<String>(
      context: context,
      builder: (sheetContext) => CupertinoActionSheet(
        title: const Text('选择性别'),
        message: const Text('选择“不展示”时，其他用户不会在个人资料中看到这一项。'),
        actions: [
          for (final option in const [
            ('unspecified', '不展示'),
            ('male', '男'),
            ('female', '女'),
          ])
            CupertinoActionSheetAction(
              key: Key('profile-gender-${option.$1}'),
              onPressed: () => Navigator.pop(sheetContext, option.$1),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Text(option.$2),
                  if (selectedGender == option.$1) ...[
                    const SizedBox(width: 8),
                    const Icon(CupertinoIcons.check_mark, size: 18),
                  ],
                ],
              ),
            ),
        ],
        cancelButton: CupertinoActionSheetAction(
          onPressed: () => Navigator.pop(sheetContext),
          child: const Text('取消'),
        ),
      ),
    );
    if (value != null && mounted && value != selectedGender) {
      setState(() {
        selectedGender = value;
        saveError = null;
      });
    }
  }

  Future<void> _save() async {
    if (saving || _profileLoading || !_hasChanges || !_draftLooksValid) return;
    if (!(formKey.currentState?.validate() ?? false)) return;
    setState(() {
      saving = true;
      saveError = null;
    });
    final avatar = avatarBytes == null
        ? null
        : MediaUpload(
            bytes: avatarBytes!,
            fileName: avatarFileName!,
            mimeType: avatarMime!,
            kind: MessageContentKind.image,
            localPath: avatarPath,
          );
    final success = await widget.controller.saveProfile(
      name: _nameChanged ? nameController.text : null,
      handle: _handleChanged ? handleController.text : null,
      signature: _signatureChanged ? signatureController.text : null,
      gender: selectedGender != initialGender ? selectedGender : null,
      avatar: avatar,
    );
    if (!mounted) return;
    if (success) {
      setState(() => saving = false);
      allowExit = true;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('个人资料已更新')));
      Navigator.pop(context);
    } else {
      setState(() {
        saving = false;
        saveError = widget.controller.error ?? '个人资料保存失败，请稍后重试';
      });
    }
  }

  Future<void> _confirmDiscard() async {
    if (saving || !_hasChanges) return;
    final discard = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('放弃修改？'),
        content: const Text('你尚未保存本页修改，离开后将无法恢复。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('继续编辑'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, true),
            style: TextButton.styleFrom(
              foregroundColor: Theme.of(context).colorScheme.error,
            ),
            child: const Text('放弃修改'),
          ),
        ],
      ),
    );
    if (discard != true || !mounted) return;
    setState(() => allowExit = true);
    Navigator.pop(context);
  }

  @override
  Widget build(BuildContext context) {
    final user = widget.controller.currentUser;
    final canSave =
        !saving && !_profileLoading && _hasChanges && _draftLooksValid;
    return PopScope(
      canPop: allowExit || !_hasChanges,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) _confirmDiscard();
      },
      child: Scaffold(
        appBar: GlassAppBar(
          title: const Text('编辑资料'),
          actions: [
            TextButton(
              key: const Key('save-profile'),
              onPressed: canSave ? _save : null,
              child: Text(saving ? '保存中' : '保存'),
            ),
          ],
        ),
        body: Column(
          children: [
            if (_profileUnavailable)
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: _SettingsNotice(
                  key: const Key('profile-refresh-error'),
                  message: '个人资料暂时无法更新，可保留草稿后重试',
                  actionLabel: '重试',
                  onAction: saving ? null : _refreshProfile,
                ),
              ),
            if (saveError != null)
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Semantics(
                  key: const Key('profile-save-error'),
                  container: true,
                  liveRegion: true,
                  label: saveError,
                  child: _SettingsNotice(
                    message: saveError!,
                    actionLabel: '重试',
                    onAction: canSave ? _save : null,
                  ),
                ),
              ),
            Expanded(
              child: Form(
                key: formKey,
                child: ListView(
                  padding: const EdgeInsets.fromLTRB(16, 20, 16, 36),
                  keyboardDismissBehavior:
                      ScrollViewKeyboardDismissBehavior.onDrag,
                  children: [
                    Center(
                      child: Semantics(
                        button: true,
                        label: '更换头像',
                        child: GestureDetector(
                          onTap: saving ? null : _chooseAvatarSource,
                          child: Stack(
                            children: [
                              ClipOval(
                                child: avatarBytes != null
                                    ? Image.memory(
                                        avatarBytes!,
                                        width: 88,
                                        height: 88,
                                        fit: BoxFit.cover,
                                      )
                                    : SizedBox(
                                        width: 88,
                                        height: 88,
                                        child: PersonAvatar(
                                          name: user?.name ?? '我',
                                          size: 88,
                                          avatarUrl: user?.avatarUrl,
                                        ),
                                      ),
                              ),
                              Positioned(
                                right: 0,
                                bottom: 0,
                                child: Container(
                                  width: 30,
                                  height: 30,
                                  decoration: BoxDecoration(
                                    color:
                                        Theme.of(context).brightness ==
                                            Brightness.dark
                                        ? LinliColors.brandYellow
                                        : LinliColors.brandInk,
                                    shape: BoxShape.circle,
                                    border: Border.all(
                                      color: Theme.of(
                                        context,
                                      ).colorScheme.surface,
                                      width: 2,
                                    ),
                                  ),
                                  child: Icon(
                                    CupertinoIcons.camera_fill,
                                    size: 15,
                                    color:
                                        Theme.of(context).brightness ==
                                            Brightness.dark
                                        ? LinliColors.brandInk
                                        : Colors.white,
                                  ),
                                ),
                              ),
                            ],
                          ),
                        ),
                      ),
                    ),
                    const SizedBox(height: 8),
                    Text(
                      '点击头像更换照片',
                      textAlign: TextAlign.center,
                      style: Theme.of(context).textTheme.bodySmall,
                    ),
                    const SizedBox(height: 24),
                    TextFormField(
                      key: const Key('profile-name'),
                      controller: nameController,
                      maxLength: 40,
                      buildCounter: _hideTextFieldCounter,
                      textInputAction: TextInputAction.next,
                      decoration: const InputDecoration(labelText: '昵称'),
                      validator: (value) {
                        if (!_nameChanged) return null;
                        final text = value?.trim() ?? '';
                        if (text.isEmpty) return '请输入昵称';
                        if (text.runes.length > 40) return '昵称不能超过 40 个字符';
                        return null;
                      },
                    ),
                    _ProfileFieldMeta(
                      count: '${nameController.text.runes.length}/40',
                    ),
                    const SizedBox(height: 16),
                    TextFormField(
                      key: const Key('profile-handle'),
                      controller: handleController,
                      maxLength: 24,
                      buildCounter: _hideTextFieldCounter,
                      autocorrect: false,
                      textCapitalization: TextCapitalization.none,
                      textInputAction: TextInputAction.next,
                      enabled:
                          !_profileLoading &&
                          !_profileUnavailable &&
                          !initialHandleIsInternal &&
                          ((user?.handleChangesRemaining ?? 0) > 0 ||
                              initialHandle.isEmpty ||
                              _handleChanged),
                      decoration: const InputDecoration(
                        labelText: '呱呱号',
                        hintText: '请设置 4–24 位呱呱号',
                        prefixText: '@',
                      ),
                      validator: (value) {
                        if (!_handleChanged) return null;
                        final text = value?.trim().toLowerCase() ?? '';
                        if (initialHandleIsInternal && text.isEmpty) {
                          return null;
                        }
                        if (!RegExp(r'^[a-z0-9_]{4,24}$').hasMatch(text)) {
                          return '请输入有效的呱呱号';
                        }
                        return null;
                      },
                    ),
                    _ProfileFieldMeta(
                      helper: _profileLoading
                          ? '正在更新呱呱号修改状态'
                          : _profileUnavailable
                          ? '修改状态待确认，请重试加载资料'
                          : initialHandleIsInternal
                          ? '账号服务升级后会自动生成，生成后可修改'
                          : (user?.handleChangesRemaining ?? 0) > 0
                          ? '还可修改 ${user!.handleChangesRemaining} 次 · 4–24 位小写字母、数字或下划线'
                          : '修改次数已用完，如需处理请联系平台客服',
                      count: '${handleController.text.characters.length}/24',
                    ),
                    const SizedBox(height: 16),
                    TextFormField(
                      key: const Key('profile-signature'),
                      controller: signatureController,
                      minLines: 2,
                      maxLines: 4,
                      maxLength: 160,
                      buildCounter: _hideTextFieldCounter,
                      decoration: const InputDecoration(labelText: '个性签名'),
                    ),
                    _ProfileFieldMeta(
                      count: '${signatureController.text.runes.length}/160',
                    ),
                    const SectionHeader('资料展示'),
                    SectionCard(
                      key: const Key('profile-gender-card'),
                      children: [
                        _SettingsRow(
                          key: const Key('profile-gender'),
                          icon: CupertinoIcons.person_crop_circle,
                          title: '性别',
                          subtitle: _profileGenderLabel(selectedGender),
                          status: selectedGender == 'unspecified'
                              ? '仅自己可见'
                              : null,
                          onTap: _chooseGender,
                        ),
                      ],
                    ),
                    const SectionHeader('绑定信息'),
                    SectionCard(
                      key: const Key('profile-phone-card'),
                      children: [
                        _SettingsRow(
                          icon: CupertinoIcons.phone,
                          title: '手机号',
                          subtitle: _maskedPhone(user?.phone),
                          onTap: () => Navigator.of(context).push(
                            MaterialPageRoute(
                              builder: (_) => ChangePhoneScreen(
                                controller: widget.controller,
                              ),
                            ),
                          ),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

Widget? _hideTextFieldCounter(
  BuildContext context, {
  required int currentLength,
  required bool isFocused,
  required int? maxLength,
}) => null;

String _profileGenderLabel(String value) => switch (value) {
  'male' => '男',
  'female' => '女',
  _ => '不展示',
};

class _ProfileFieldMeta extends StatelessWidget {
  const _ProfileFieldMeta({this.helper, required this.count});

  final String? helper;
  final String count;

  @override
  Widget build(BuildContext context) {
    final style = Theme.of(context).textTheme.bodySmall?.copyWith(
      color: Theme.of(context).colorScheme.onSurfaceVariant,
    );
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 6, 12, 0),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (helper != null)
            Expanded(
              child: Text(
                helper!,
                style: style,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
            )
          else
            const Spacer(),
          const SizedBox(width: 12),
          Text(count, style: style),
        ],
      ),
    );
  }
}

class ChangePhoneScreen extends StatefulWidget {
  const ChangePhoneScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<ChangePhoneScreen> createState() => _ChangePhoneScreenState();
}

class _ChangePhoneScreenState extends State<ChangePhoneScreen> {
  final formKey = GlobalKey<FormState>();
  final phoneController = TextEditingController();
  final codeController = TextEditingController();
  bool requested = false;
  bool busy = false;
  bool submitted = false;
  int resendSeconds = 0;
  Timer? resendTimer;

  String get currentPhone => widget.controller.currentUser?.phone?.trim() ?? '';

  String get newPhone => phoneController.text.trim();

  bool get phoneLooksValid =>
      RegExp(r'^\+?[0-9]{6,32}$').hasMatch(newPhone) &&
      newPhone != currentPhone;

  bool get codeLooksValid => codeController.text.trim().length >= 4;

  @override
  void dispose() {
    resendTimer?.cancel();
    phoneController.dispose();
    codeController.dispose();
    super.dispose();
  }

  void _startResendCountdown() {
    resendTimer?.cancel();
    setState(() => resendSeconds = 60);
    resendTimer = Timer.periodic(const Duration(seconds: 1), (timer) {
      if (!mounted || resendSeconds <= 1) {
        timer.cancel();
        if (mounted) setState(() => resendSeconds = 0);
        return;
      }
      setState(() => resendSeconds -= 1);
    });
  }

  void _editPhone() {
    resendTimer?.cancel();
    setState(() {
      requested = false;
      submitted = false;
      resendSeconds = 0;
      codeController.clear();
    });
  }

  Future<void> _requestCode() async {
    if (busy || resendSeconds > 0) return;
    setState(() => submitted = true);
    if (!(formKey.currentState?.validate() ?? false)) return;
    setState(() => busy = true);
    final success = await widget.controller.requestPhoneUpdateCode(newPhone);
    if (!mounted) return;
    setState(() {
      busy = false;
      if (success) requested = true;
    });
    if (success) {
      _startResendCountdown();
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('验证码已发送至 ${_maskedPhone(newPhone)}')),
      );
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '验证码发送失败，请稍后重试')),
      );
    }
  }

  Future<void> _confirm() async {
    if (busy || !requested) return;
    setState(() => submitted = true);
    if (!(formKey.currentState?.validate() ?? false)) return;
    final confirmedPhone = newPhone;
    setState(() => busy = true);
    final success = await widget.controller.updatePhone(
      confirmedPhone,
      codeController.text,
    );
    if (!mounted) return;
    setState(() {
      busy = false;
      if (success) submitted = false;
    });
    if (!success) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '手机号换绑失败，请稍后重试')),
      );
      return;
    }
    resendTimer?.cancel();
    await showCupertinoDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (dialogContext) => CupertinoAlertDialog(
        title: const Text('手机号已换绑'),
        content: Text('新的登录手机号为 ${_maskedPhone(confirmedPhone)}。以后请使用新手机号登录。'),
        actions: [
          CupertinoDialogAction(
            key: const Key('phone-change-done'),
            onPressed: () => Navigator.of(dialogContext).pop(),
            child: const Text('完成'),
          ),
        ],
      ),
    );
    if (mounted) Navigator.pop(context, true);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('换绑手机号')),
    body: Form(
      key: formKey,
      autovalidateMode: submitted
          ? AutovalidateMode.onUserInteraction
          : AutovalidateMode.disabled,
      child: ListView(
        keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 36),
        children: [
          SectionCard(
            children: [
              _SettingsRow(
                icon: CupertinoIcons.phone_fill,
                title: '当前绑定手机号',
                subtitle: _maskedPhone(currentPhone),
                status: '已验证',
              ),
            ],
          ),
          const SizedBox(height: 12),
          const _PageIntro('验证码将发送到新手机号。换绑成功后，手机号登录和找回密码都会使用新号码。'),
          const SectionHeader('验证新手机号'),
          TextFormField(
            key: const Key('new-phone'),
            controller: phoneController,
            enabled: !requested && !busy,
            keyboardType: TextInputType.phone,
            textInputAction: requested
                ? TextInputAction.next
                : TextInputAction.done,
            autofillHints: const [AutofillHints.telephoneNumber],
            onChanged: (_) => setState(() {}),
            onFieldSubmitted: (_) {
              if (!requested) _requestCode();
            },
            decoration: InputDecoration(
              labelText: '新手机号',
              helperText: requested
                  ? '验证码已发送至 ${_maskedPhone(newPhone)}'
                  : '请输入可正常接收短信的手机号',
              suffixIcon: requested
                  ? TextButton(
                      key: const Key('phone-change-edit-number'),
                      onPressed: busy ? null : _editPhone,
                      child: const Text('修改'),
                    )
                  : null,
            ),
            validator: (value) {
              final phone = value?.trim() ?? '';
              if (!RegExp(r'^\+?[0-9]{6,32}$').hasMatch(phone)) {
                return '请输入有效手机号';
              }
              if (phone == currentPhone) return '新手机号不能与当前绑定相同';
              return null;
            },
          ),
          if (requested) ...[
            const SizedBox(height: 14),
            TextFormField(
              key: const Key('phone-change-code'),
              controller: codeController,
              enabled: !busy,
              keyboardType: TextInputType.number,
              textInputAction: TextInputAction.done,
              autofillHints: const [AutofillHints.oneTimeCode],
              onChanged: (_) => setState(() {}),
              onFieldSubmitted: (_) => _confirm(),
              decoration: InputDecoration(
                labelText: '短信验证码',
                helperText: '验证码 5 分钟内有效',
                suffixIcon: TextButton(
                  key: const Key('phone-change-resend'),
                  onPressed: busy || resendSeconds > 0 ? null : _requestCode,
                  child: Text(
                    resendSeconds > 0 ? '${resendSeconds}s 后重发' : '重新获取',
                  ),
                ),
              ),
              validator: (value) =>
                  (value?.trim().length ?? 0) >= 4 ? null : '请输入收到的验证码',
            ),
          ],
          const SizedBox(height: 24),
          FilledButton(
            key: const Key('phone-change-action'),
            onPressed: busy
                ? null
                : requested
                ? codeLooksValid
                      ? _confirm
                      : null
                : phoneLooksValid
                ? _requestCode
                : null,
            child: busy
                ? const SizedBox.square(
                    dimension: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : Text(requested ? '确认换绑手机号' : '获取短信验证码'),
          ),
        ],
      ),
    ),
  );
}

class FavoritesScreen extends StatefulWidget {
  const FavoritesScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<FavoritesScreen> createState() => _FavoritesScreenState();
}

class _FavoritesScreenState extends State<FavoritesScreen> {
  late int _historyRevision = widget.controller.groupPresentationRevision;
  List<ChatMessage>? items;
  _FavoriteFilter filter = _FavoriteFilter.all;

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_historyChanged);
    _load();
  }

  void _historyChanged() {
    if (!mounted) return;
    if (_historyRevision == widget.controller.groupPresentationRevision) {
      setState(() {});
      return;
    }
    _historyRevision = widget.controller.groupPresentationRevision;
    setState(
      () => items = items?.where(widget.controller.canDisplayMessage).toList(),
    );
    unawaited(_load());
  }

  @override
  void dispose() {
    widget.controller.removeListener(_historyChanged);
    super.dispose();
  }

  Future<void> _load() async {
    final loaded = await widget.controller.loadFavorites();
    if (mounted) setState(() => items = loaded);
  }

  @override
  Widget build(BuildContext context) {
    final allItems = items?.where(widget.controller.canDisplayMessage).toList();
    final filteredItems = allItems
        ?.where((message) => filter.matches(message.kind))
        .toList(growable: false);
    return Scaffold(
      appBar: const GlassAppBar(title: Text('我的收藏')),
      body: allItems == null
          ? const Center(child: CupertinoActivityIndicator())
          : allItems.isEmpty
          ? const StatePanel(
              icon: CupertinoIcons.bookmark,
              title: '还没有收藏',
              body: '在聊天中长按消息并选择收藏，内容会同步显示在这里。',
            )
          : Column(
              children: [
                SizedBox(
                  height: 52,
                  child: ListView.separated(
                    key: const Key('favorite-filters'),
                    scrollDirection: Axis.horizontal,
                    padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
                    itemCount: _FavoriteFilter.values.length,
                    separatorBuilder: (_, _) => const SizedBox(width: 8),
                    itemBuilder: (context, index) {
                      final option = _FavoriteFilter.values[index];
                      return ChoiceChip(
                        key: Key('favorite-filter-${option.name}'),
                        label: Text(option.label),
                        selected: filter == option,
                        onSelected: (_) => setState(() => filter = option),
                        showCheckmark: false,
                      );
                    },
                  ),
                ),
                Expanded(
                  child: filteredItems!.isEmpty
                      ? StatePanel(
                          icon: CupertinoIcons.bookmark,
                          title: '没有${filter.label}收藏',
                          body: '切换分类可查看其他已收藏内容。',
                        )
                      : RefreshIndicator(
                          onRefresh: _load,
                          child: ListView.separated(
                            padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
                            itemCount: filteredItems.length,
                            separatorBuilder: (_, _) =>
                                const SizedBox(height: 8),
                            itemBuilder: (context, index) =>
                                _favoriteTile(filteredItems[index]),
                          ),
                        ),
                ),
              ],
            ),
    );
  }

  Widget _favoriteTile(ChatMessage message) => Dismissible(
    key: ValueKey('favorite-${message.id}'),
    direction: DismissDirection.endToStart,
    confirmDismiss: (_) => _confirmRemove(message),
    background: Container(
      alignment: Alignment.centerRight,
      padding: const EdgeInsets.only(right: 20),
      decoration: BoxDecoration(
        color: LinliColors.systemRed,
        borderRadius: BorderRadius.circular(12),
      ),
      child: const Icon(CupertinoIcons.delete, color: Colors.white),
    ),
    child: Card(
      child: ListTile(
        minTileHeight: 72,
        leading: const Icon(CupertinoIcons.bookmark_fill),
        title: Text(
          message.text.isEmpty ? '[非文本消息]' : message.text,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
        ),
        subtitle: Text(
          '${widget.controller.displayNameForId(message.senderId, fallback: message.senderName)} · ${_monthDay(message.sentAt)}',
        ),
        trailing: const Icon(CupertinoIcons.chevron_forward, size: 16),
        onTap: () => _openConversation(message),
      ),
    ),
  );

  Future<bool> _confirmRemove(ChatMessage message) async {
    final confirmed = await showCupertinoDialog<bool>(
      context: context,
      builder: (dialogContext) => CupertinoAlertDialog(
        title: const Text('取消收藏？'),
        content: const Text('只会从收藏列表移除，不会删除聊天中的原消息。'),
        actions: [
          CupertinoDialogAction(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          CupertinoDialogAction(
            isDestructiveAction: true,
            onPressed: () => Navigator.pop(dialogContext, true),
            child: const Text('取消收藏'),
          ),
        ],
      ),
    );
    if (confirmed != true) return false;
    final removed = await widget.controller.removeFavorite(message);
    if (removed && mounted) {
      setState(() => items!.removeWhere((item) => item.id == message.id));
    }
    return removed;
  }

  Future<void> _openConversation(ChatMessage message) async {
    var conversation = widget.controller.conversations
        .where((item) => item.id == message.conversationId)
        .firstOrNull;
    if (conversation == null) {
      await widget.controller.refresh();
      conversation = widget.controller.conversations
          .where((item) => item.id == message.conversationId)
          .firstOrNull;
    }
    if (!mounted) return;
    if (conversation == null) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('原会话已不可访问，收藏内容仍可在本页查看')));
      return;
    }
    await Navigator.of(context).push(
      chatScreenRoute(
        context,
        controller: widget.controller,
        conversation: conversation,
        initialMessageId: message.id,
      ),
    );
  }
}

enum _FavoriteFilter {
  all('全部'),
  text('文字'),
  media('媒体'),
  file('文件'),
  other('其他');

  const _FavoriteFilter(this.label);

  final String label;

  bool matches(MessageContentKind kind) => switch (this) {
    _FavoriteFilter.all => true,
    _FavoriteFilter.text =>
      kind == MessageContentKind.text || kind == MessageContentKind.reply,
    _FavoriteFilter.media =>
      kind == MessageContentKind.image ||
          kind == MessageContentKind.voice ||
          kind == MessageContentKind.video ||
          kind == MessageContentKind.sticker ||
          kind == MessageContentKind.momentShare,
    _FavoriteFilter.file => kind == MessageContentKind.file,
    _FavoriteFilter.other =>
      kind != MessageContentKind.text &&
          kind != MessageContentKind.reply &&
          kind != MessageContentKind.image &&
          kind != MessageContentKind.voice &&
          kind != MessageContentKind.video &&
          kind != MessageContentKind.sticker &&
          kind != MessageContentKind.momentShare &&
          kind != MessageContentKind.file,
  };
}

class DevicesScreen extends StatefulWidget {
  const DevicesScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<DevicesScreen> createState() => _DevicesScreenState();
}

class _DevicesScreenState extends State<DevicesScreen> {
  List<UserDevice>? devices;
  List<ImDeviceSession>? imSessions;
  bool loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    if (mounted) setState(() => loading = true);
    final results = await Future.wait<Object?>([
      widget.controller.loadImDeviceSessions(),
      widget.controller.loadUserDevices(),
    ]);
    if (mounted) {
      setState(() {
        imSessions = results[0] as List<ImDeviceSession>?;
        devices = results[1] as List<UserDevice>?;
        loading = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('登录设备')),
    body: loading
        ? const Center(child: CupertinoActivityIndicator())
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
              children: [
                const _PageIntro('在这里管理各端的登录状态和新消息通知。'),
                const SectionHeader('登录设备'),
                if (imSessions == null)
                  StatePanel(
                    icon: CupertinoIcons.exclamationmark_triangle,
                    title: '登录设备暂时无法加载',
                    body: '请检查网络连接后重试，现有登录状态不会受到影响。',
                    actionLabel: '重新加载',
                    onAction: _load,
                  )
                else if (imSessions!.isEmpty)
                  const StatePanel(
                    icon: CupertinoIcons.device_phone_portrait,
                    title: '暂无其他登录设备',
                    body: '在其他手机、网页或电脑端登录后，会显示在这里。',
                  )
                else
                  SectionCard(
                    children: imSessions!
                        .map(
                          (session) => _SettingsRow(
                            icon: _imDeviceIcon(session.deviceFlag),
                            title: _imDeviceName(session.deviceFlag),
                            subtitle:
                                '${session.isOnline ? '${session.connectionCount} 个连接在线' : '当前离线'} · ${_dateText(session.updatedAt)}',
                            status: '下线',
                            destructive: true,
                            onTap: () => _confirmQuitSession(session),
                          ),
                        )
                        .toList(),
                  ),
                const SectionHeader('消息通知设备'),
                if (devices == null)
                  StatePanel(
                    icon: CupertinoIcons.exclamationmark_triangle,
                    title: '通知设备暂时无法加载',
                    body: '请检查网络连接后重试，当前设备的通知设置不会改变。',
                    actionLabel: '重新加载',
                    onAction: _load,
                  )
                else if (devices!.isEmpty)
                  const StatePanel(
                    icon: CupertinoIcons.bell_slash,
                    title: '暂无消息通知设备',
                    body: '设备允许接收新消息通知后，会显示在这里。',
                  )
                else
                  SectionCard(
                    children: devices!
                        .map(
                          (device) => _SettingsRow(
                            icon: CupertinoIcons.bell,
                            title: _platformName(device.platform),
                            subtitle:
                                '${device.provider.toUpperCase()} · ${_dateText(device.updatedAt)}',
                            status: '停止通知',
                            destructive: true,
                            onTap: () => _confirmRemove(device),
                          ),
                        )
                        .toList(),
                  ),
              ],
            ),
          ),
  );

  Future<void> _confirmRemove(UserDevice device) async {
    final remove = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('停止这台设备的通知？'),
        content: Text('${_platformName(device.platform)} 将不再接收推送通知，不影响登录会话。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, true),
            child: const Text('停止通知'),
          ),
        ],
      ),
    );
    if (remove != true) return;
    final success = await widget.controller.removeUserDevice(device.id);
    if (!mounted) return;
    if (success) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('已停止该设备的消息通知')));
      await _load();
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '停止通知失败，请稍后重试')),
      );
    }
  }

  Future<void> _confirmQuitSession(ImDeviceSession session) async {
    final quit = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text('下线${_imDeviceName(session.deviceFlag)}？'),
        content: const Text('该端的当前连接会立即断开，再次使用时需要重新登录。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, true),
            child: const Text('确认下线'),
          ),
        ],
      ),
    );
    if (quit != true) return;
    final success = await widget.controller.quitImDeviceSession(
      session.deviceFlag,
    );
    if (!mounted) return;
    if (success) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('该设备已下线')));
      await _load();
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '设备下线失败，请稍后重试')),
      );
    }
  }
}

class NotificationSettingsScreen extends StatefulWidget {
  const NotificationSettingsScreen({
    super.key,
    this.controller,
    this.store = const LocalSettingsStore(),
  });

  final AppController? controller;
  final LocalSettingsStore store;

  @override
  State<NotificationSettingsScreen> createState() =>
      _NotificationSettingsScreenState();
}

class _NotificationSettingsScreenState
    extends State<NotificationSettingsScreen> {
  bool loaded = false;
  bool enabled = true;
  bool preview = true;
  bool sound = true;
  bool vibration = true;
  String? loadError;
  final Set<String> savingKeys = <String>{};
  PermissionStatus? permissionStatus;
  BrowserNotificationPermission? browserPermission;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final values = await Future.wait<bool>([
        widget.store.readBool(
          LocalSettingsStore.notificationEnabled,
          fallback: true,
        ),
        widget.store.readBool(
          LocalSettingsStore.notificationPreview,
          fallback: true,
        ),
        widget.store.readBool(
          LocalSettingsStore.notificationSound,
          fallback: true,
        ),
        widget.store.readBool(
          LocalSettingsStore.notificationVibration,
          fallback: true,
        ),
      ]);
      if (!mounted) return;
      setState(() {
        enabled = values[0];
        preview = values[1];
        sound = values[2];
        vibration = values[3];
        loadError = null;
        loaded = true;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        loadError = '通知偏好读取失败，当前显示默认设置。';
        loaded = true;
      });
    }
    await _refreshPermissionStatus();
  }

  Future<void> _set(
    String key,
    bool value,
    bool previous,
    void Function(bool value) update,
  ) async {
    if (savingKeys.contains(key)) return;
    setState(() {
      savingKeys.add(key);
      update(value);
    });
    try {
      await widget.store.writeBool(key, value);
      widget.controller?.refreshPushConfiguration();
    } catch (_) {
      if (!mounted) return;
      setState(() => update(previous));
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('通知偏好保存失败，已恢复原设置')));
    } finally {
      if (mounted) setState(() => savingKeys.remove(key));
    }
  }

  Future<void> _refreshPermissionStatus() async {
    if (kIsWeb) {
      final status = await browserNotificationPermission();
      if (mounted) setState(() => browserPermission = status);
      return;
    }
    try {
      final status = await Permission.notification.status;
      if (mounted) setState(() => permissionStatus = status);
    } catch (_) {
      // Widget tests and desktop platforms may not provide a native bridge.
    }
  }

  Future<void> _manageSystemPermission() async {
    if (kIsWeb) {
      final status = await requestBrowserNotificationPermission();
      if (mounted) setState(() => browserPermission = status);
      if (!mounted) return;
      if (status == BrowserNotificationPermission.granted) {
        widget.controller?.refreshPushConfiguration();
        return;
      }
      final message = status == BrowserNotificationPermission.unsupported
          ? '浏览器通知需要 HTTPS 或本机安全环境'
          : '浏览器未允许通知，请在地址栏的网站权限中开启';
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(message)));
      return;
    }
    try {
      final status = await Permission.notification.request();
      if (mounted) setState(() => permissionStatus = status);
      if (status.isPermanentlyDenied) await openAppSettings();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('请在系统设置中管理青蛙呱呱的通知权限')));
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('消息通知')),
    body: ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        const _PageIntro(
          '通知偏好用于手机前台消息提醒，并同步到当前推送设备。免打扰会话不响铃；最终效果仍受系统通知、静音和勿扰设置控制。',
        ),
        if (loadError != null)
          _SettingsNotice(
            message: loadError!,
            actionLabel: '重新读取',
            onAction: () {
              setState(() {
                loaded = false;
                loadError = null;
              });
              _load();
            },
          ),
        const SectionHeader('通知偏好'),
        SectionCard(
          children: [
            _SwitchRow(
              key: const Key('notification-enabled-switch'),
              icon: CupertinoIcons.bell,
              title: '允许消息提醒',
              subtitle: '作为本机通知模块的默认总开关',
              value: enabled,
              enabled:
                  loaded &&
                  !savingKeys.contains(LocalSettingsStore.notificationEnabled),
              onChanged: (value) => _set(
                LocalSettingsStore.notificationEnabled,
                value,
                enabled,
                (next) => enabled = next,
              ),
            ),
            _SwitchRow(
              icon: CupertinoIcons.text_bubble,
              title: '显示消息预览',
              subtitle: '关闭后通知仅显示发送者',
              value: preview,
              enabled:
                  loaded &&
                  enabled &&
                  !savingKeys.contains(LocalSettingsStore.notificationPreview),
              onChanged: (value) => _set(
                LocalSettingsStore.notificationPreview,
                value,
                preview,
                (next) => preview = next,
              ),
            ),
            _SwitchRow(
              icon: CupertinoIcons.speaker_2,
              title: '提示音',
              value: sound,
              enabled:
                  loaded &&
                  enabled &&
                  !savingKeys.contains(LocalSettingsStore.notificationSound),
              onChanged: (value) => _set(
                LocalSettingsStore.notificationSound,
                value,
                sound,
                (next) => sound = next,
              ),
            ),
            _SwitchRow(
              icon: CupertinoIcons.device_phone_portrait,
              title: '振动',
              value: vibration,
              enabled:
                  loaded &&
                  enabled &&
                  !savingKeys.contains(
                    LocalSettingsStore.notificationVibration,
                  ),
              onChanged: (value) => _set(
                LocalSettingsStore.notificationVibration,
                value,
                vibration,
                (next) => vibration = next,
              ),
            ),
          ],
        ),
        const SectionHeader('系统能力'),
        SectionCard(
          children: [
            _SettingsRow(
              icon: CupertinoIcons.app_badge,
              title: kIsWeb ? '浏览器通知权限' : '系统通知权限',
              subtitle: kIsWeb
                  ? '允许后即使关闭网页也可接收提醒，多标签页只显示一次'
                  : '由 iOS 或 Android 系统控制最终展示权限',
              status: kIsWeb
                  ? switch (browserPermission) {
                      BrowserNotificationPermission.granted => '已允许',
                      BrowserNotificationPermission.denied => '未允许',
                      BrowserNotificationPermission.unsupported => '不可用',
                      _ => '检查权限',
                    }
                  : switch (permissionStatus) {
                      null => '检查权限',
                      PermissionStatus.granted ||
                      PermissionStatus.provisional => '已允许',
                      PermissionStatus.denied => '未允许',
                      PermissionStatus.permanentlyDenied ||
                      PermissionStatus.restricted => '去设置',
                      _ => '检查权限',
                    },
              onTap: _manageSystemPermission,
            ),
          ],
        ),
      ],
    ),
  );
}

class PrivacyScreen extends StatefulWidget {
  const PrivacyScreen({
    super.key,
    this.controller,
    this.store = const LocalSettingsStore(),
  });

  final AppController? controller;
  final LocalSettingsStore store;

  @override
  State<PrivacyScreen> createState() => _PrivacyScreenState();
}

class _PrivacyScreenState extends State<PrivacyScreen> {
  int? recentSearchCount;
  bool recentSearchFailed = false;
  bool clearingRecentSearches = false;
  UserSearchCapabilities? searchCapabilities;
  bool searchCapabilitiesLoading = false;
  String? searchCapabilitiesError;
  final Set<String> savingDiscoverySettings = {};

  @override
  void initState() {
    super.initState();
    _loadRecentSearchCount();
    _loadSearchCapabilities();
  }

  Future<void> _loadSearchCapabilities() async {
    final controller = widget.controller;
    if (controller == null) return;
    setState(() {
      searchCapabilitiesLoading = true;
      searchCapabilitiesError = null;
    });
    try {
      final capabilities = await controller.repository.searchCapabilities();
      if (!mounted) return;
      setState(() => searchCapabilities = capabilities);
    } catch (_) {
      if (!mounted) return;
      setState(() => searchCapabilitiesError = '搜索权限状态加载失败');
    } finally {
      if (mounted) setState(() => searchCapabilitiesLoading = false);
    }
  }

  Future<void> _loadRecentSearchCount() async {
    try {
      final values = await widget.store.readRecentSearches();
      if (!mounted) return;
      setState(() {
        recentSearchCount = values.length;
        recentSearchFailed = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        recentSearchCount = null;
        recentSearchFailed = true;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final user = widget.controller?.currentUser;
    return Scaffold(
      appBar: const GlassAppBar(title: Text('隐私与安全')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
        children: [
          const _PageIntro('管理账号的可发现方式、本机隐私数据和已屏蔽用户。敏感操作会在执行前再次确认。'),
          if (user != null) ...[
            const SectionHeader('找到我的方式'),
            SectionCard(
              key: const Key('privacy-discovery-card'),
              children: [
                _PrivacySwitchRow(
                  key: const Key('privacy-search-by-handle'),
                  icon: CupertinoIcons.at,
                  title: '通过呱呱号找到我',
                  subtitle:
                      searchCapabilities?.canUpdatePrivacySettings == false
                      ? '当前服务器版本暂不支持修改'
                      : searchCapabilities?.allowSearchByHandle == false
                      ? '当前平台未开放呱呱号搜索'
                      : '其他用户输入完整呱呱号后可以找到你',
                  value: user.allowSearchByHandle,
                  saving: savingDiscoverySettings.contains('handle'),
                  enabled:
                      !searchCapabilitiesLoading &&
                      searchCapabilitiesError == null &&
                      savingDiscoverySettings.isEmpty &&
                      searchCapabilities?.canUpdatePrivacySettings == true &&
                      searchCapabilities?.allowSearchByHandle != false,
                  onChanged: (value) =>
                      _saveDiscoverySetting(key: 'handle', value: value),
                ),
                _PrivacySwitchRow(
                  key: const Key('privacy-search-by-phone'),
                  icon: CupertinoIcons.phone,
                  title: '通过手机号找到我',
                  subtitle:
                      searchCapabilities?.canUpdatePrivacySettings == false
                      ? '当前服务器版本暂不支持修改'
                      : searchCapabilities?.allowSearchByPhone == false
                      ? '当前平台未开放手机号搜索'
                      : '搜索结果只展示公开资料，不会展示手机号',
                  value: user.allowSearchByPhone,
                  saving: savingDiscoverySettings.contains('phone'),
                  enabled:
                      !searchCapabilitiesLoading &&
                      searchCapabilitiesError == null &&
                      savingDiscoverySettings.isEmpty &&
                      searchCapabilities?.canUpdatePrivacySettings == true &&
                      searchCapabilities?.allowSearchByPhone != false,
                  onChanged: (value) =>
                      _saveDiscoverySetting(key: 'phone', value: value),
                ),
              ],
            ),
            if (searchCapabilitiesError != null)
              _SettingsNotice(
                message: '$searchCapabilitiesError，当前设置尚未修改。',
                actionLabel: '重试',
                onAction: _loadSearchCapabilities,
              ),
            const _PageIntro('关闭后，其他用户不能再通过对应信息搜索到你；青蛙呱呱不会在搜索结果中公开手机号。'),
          ],
          const SectionHeader('数据保护'),
          SectionCard(
            children: const [
              _SettingsRow(
                icon: CupertinoIcons.lock_shield,
                title: '本地消息加密',
                subtitle: '缓存由设备密钥加密后保存',
                status: '已启用',
              ),
              _SettingsRow(
                icon: CupertinoIcons.lock_fill,
                title: '系统安全存储',
                subtitle: '登录凭据由系统钥匙串保护',
                status: '已启用',
              ),
            ],
          ),
          const SectionHeader('本机数据'),
          if (recentSearchFailed)
            _SettingsNotice(
              message: '最近搜索记录读取失败，本机记录尚未被修改。',
              actionLabel: '重试',
              onAction: _loadRecentSearchCount,
            ),
          SectionCard(
            children: [
              _SettingsRow(
                key: const Key('privacy-clear-search-history'),
                icon: CupertinoIcons.clock,
                title: '清除最近搜索',
                subtitle: recentSearchCount == 0
                    ? '本机当前没有保存搜索关键词'
                    : '删除本机保存的搜索关键词',
                status: switch (recentSearchCount) {
                  _ when recentSearchFailed => '读取失败',
                  null => '读取中',
                  0 => '无记录',
                  final count => '$count 条',
                },
                onTap: !clearingRecentSearches && (recentSearchCount ?? 0) > 0
                    ? () => _confirmClearSearchHistory(context)
                    : null,
              ),
              _SettingsRow(
                icon: CupertinoIcons.person_crop_circle_badge_xmark,
                title: '黑名单',
                subtitle: '查看已屏蔽的用户并可随时解除',
                onTap: widget.controller == null
                    ? null
                    : () => Navigator.of(context).push(
                        MaterialPageRoute(
                          builder: (_) => BlockedUsersScreen(
                            controller: widget.controller!,
                          ),
                        ),
                      ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Future<void> _saveDiscoverySetting({
    required String key,
    required bool value,
  }) async {
    final controller = widget.controller;
    if (controller == null || savingDiscoverySettings.contains(key)) return;
    setState(() => savingDiscoverySettings.add(key));
    final success = await controller.updatePrivacySettings(
      allowSearchByHandle: key == 'handle' ? value : null,
      allowSearchByPhone: key == 'phone' ? value : null,
    );
    if (!mounted) return;
    setState(() => savingDiscoverySettings.remove(key));
    final label = key == 'handle' ? '呱呱号搜索' : '手机号搜索';
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
          success
              ? (value ? '已允许通过$label找到你' : '已关闭$label')
              : (controller.error ?? '隐私设置保存失败，请稍后重试'),
        ),
      ),
    );
  }

  Future<void> _confirmClearSearchHistory(BuildContext context) async {
    final confirmed = await showCupertinoModalPopup<bool>(
      context: context,
      builder: (sheetContext) => CupertinoActionSheet(
        title: const Text('清除最近搜索？'),
        message: Text('将删除本机保存的 $recentSearchCount 条搜索关键词，此操作不会影响聊天记录。'),
        actions: [
          CupertinoActionSheetAction(
            key: const Key('privacy-confirm-clear-search-history'),
            isDestructiveAction: true,
            onPressed: () => Navigator.pop(sheetContext, true),
            child: const Text('清除搜索记录'),
          ),
        ],
        cancelButton: CupertinoActionSheetAction(
          onPressed: () => Navigator.pop(sheetContext, false),
          child: const Text('取消'),
        ),
      ),
    );
    if (confirmed != true) return;
    setState(() => clearingRecentSearches = true);
    try {
      await widget.store.clearRecentSearches();
      if (!context.mounted) return;
      setState(() {
        recentSearchCount = 0;
        recentSearchFailed = false;
      });
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('最近搜索已从本机清除')));
    } catch (_) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('最近搜索清除失败，请稍后重试')));
    } finally {
      if (mounted) setState(() => clearingRecentSearches = false);
    }
  }
}

class _PrivacySwitchRow extends StatelessWidget {
  const _PrivacySwitchRow({
    super.key,
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.value,
    required this.enabled,
    required this.saving,
    required this.onChanged,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final bool value;
  final bool enabled;
  final bool saving;
  final ValueChanged<bool> onChanged;

  @override
  Widget build(BuildContext context) {
    final disabledTrack = Theme.of(
      context,
    ).colorScheme.onSurface.withValues(alpha: .18);
    return _SettingsRow(
      icon: icon,
      title: title,
      subtitle: subtitle,
      trailing: SizedBox(
        width: 52,
        child: Align(
          alignment: Alignment.centerRight,
          child: saving
              ? const CupertinoActivityIndicator(radius: 9)
              : CupertinoSwitch(
                  value: value,
                  onChanged: enabled ? onChanged : null,
                  activeTrackColor: enabled ? null : disabledTrack,
                  inactiveTrackColor: enabled ? null : disabledTrack,
                ),
        ),
      ),
      onTap: enabled && !saving ? () => onChanged(!value) : null,
    );
  }
}

class BlockedUsersScreen extends StatefulWidget {
  const BlockedUsersScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<BlockedUsersScreen> createState() => _BlockedUsersScreenState();
}

class _BlockedUsersScreenState extends State<BlockedUsersScreen> {
  List<AppUser> users = const [];
  bool loading = true;
  bool failed = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    if (mounted) {
      setState(() {
        loading = true;
        failed = false;
      });
    }
    final loaded = await widget.controller.loadBlockedUsers();
    if (!mounted) return;
    setState(() {
      loading = false;
      failed = loaded == null;
      users = loaded ?? const [];
    });
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('黑名单')),
    body: loading
        ? const Center(child: CupertinoActivityIndicator())
        : failed
        ? StatePanel(
            icon: CupertinoIcons.exclamationmark_shield,
            title: '黑名单暂时无法加载',
            body: '请检查网络连接后重试。已屏蔽关系不会因此改变。',
            actionLabel: '重新加载',
            onAction: _load,
          )
        : users.isEmpty
        ? StatePanel(
            icon: CupertinoIcons.person_crop_circle_badge_checkmark,
            title: '黑名单为空',
            body: '加入黑名单的用户将无法继续向你发起新消息。',
            actionLabel: '刷新',
            onAction: _load,
          )
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
              itemCount: users.length,
              separatorBuilder: (_, _) => const Divider(height: 1),
              itemBuilder: (context, index) {
                final user = users[index];
                return ListTile(
                  minTileHeight: 68,
                  leading: PersonAvatar(
                    name: user.name,
                    avatarUrl: user.avatarUrl,
                  ),
                  title: Text(user.name),
                  subtitle: Text(publicUserHandleLabel(user.handle)),
                  trailing: TextButton(
                    onPressed: () => _unblock(user),
                    child: const Text('移出'),
                  ),
                );
              },
            ),
          ),
  );

  Future<void> _unblock(AppUser user) async {
    final confirmed = await showCupertinoDialog<bool>(
      context: context,
      builder: (dialogContext) => CupertinoAlertDialog(
        title: Text('将 ${user.name} 移出黑名单？'),
        content: const Text('解除后，对方可以再次申请添加你或向已有会话发送消息。'),
        actions: [
          CupertinoDialogAction(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          CupertinoDialogAction(
            onPressed: () => Navigator.pop(dialogContext, true),
            child: const Text('确认移出'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    final success = await widget.controller.blockUser(user, false);
    if (!mounted) return;
    if (success) {
      setState(() => users.removeWhere((item) => item.id == user.id));
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '解除失败')),
      );
    }
  }
}

class GeneralSettingsScreen extends StatelessWidget {
  const GeneralSettingsScreen({super.key, required this.onToggleTheme});

  final VoidCallback onToggleTheme;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('通用')),
    body: ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        const SectionHeader('显示'),
        SectionCard(
          children: [
            _SettingsRow(
              key: const Key('general-toggle-theme'),
              icon: CupertinoIcons.moon,
              title: '切换深浅外观',
              subtitle: Theme.of(context).brightness == Brightness.dark
                  ? '当前为深色'
                  : '当前为浅色',
              onTap: onToggleTheme,
            ),
            const _SettingsRow(
              icon: CupertinoIcons.globe,
              title: '语言',
              subtitle: '当前版本仅提供简体中文',
              status: '简体中文',
            ),
          ],
        ),
      ],
    ),
  );
}

class ChatBackgroundSettingsScreen extends StatefulWidget {
  const ChatBackgroundSettingsScreen({
    super.key,
    this.store = const LocalSettingsStore(),
  });

  final LocalSettingsStore store;

  @override
  State<ChatBackgroundSettingsScreen> createState() =>
      _ChatBackgroundSettingsScreenState();
}

class _ChatBackgroundSettingsScreenState
    extends State<ChatBackgroundSettingsScreen> {
  ChatBackgroundStyle selected = ChatBackgroundStyle.followSystem;
  bool loading = true;
  bool saving = false;
  String? loadError;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final value = await widget.store.readChatBackground();
      if (!mounted) return;
      setState(() {
        selected = value;
        loadError = null;
        loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        loadError = '聊天背景读取失败，当前使用跟随系统。';
        loading = false;
      });
    }
  }

  Future<void> _select(ChatBackgroundStyle value) async {
    if (value == selected || saving) return;
    setState(() => saving = true);
    try {
      await widget.store.writeChatBackground(value);
      if (!mounted) return;
      setState(() {
        selected = value;
        loadError = null;
      });
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('聊天背景已切换为“${value.title}”')));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('聊天背景保存失败，原设置未改变')));
    } finally {
      if (mounted) setState(() => saving = false);
    }
  }

  Color _previewColor(BuildContext context, ChatBackgroundStyle style) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    return switch (style) {
      ChatBackgroundStyle.followSystem =>
        dark ? LinliColors.darkBackground : LinliColors.background,
      ChatBackgroundStyle.softMint =>
        dark ? LinliColors.darkSurfaceElevated : LinliColors.brandYellowSoft,
      ChatBackgroundStyle.cleanPaper =>
        dark ? LinliColors.brandInkSoft : LinliColors.surface,
    };
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('聊天背景')),
    body: ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        const _PageIntro('背景仅保存在本机，不会改变聊天内容，也不会同步给对方。'),
        if (loadError != null)
          _SettingsNotice(
            message: loadError!,
            actionLabel: '重新读取',
            onAction: () {
              setState(() {
                loading = true;
                loadError = null;
              });
              _load();
            },
          ),
        const SectionHeader('背景样式'),
        if (loading)
          const Padding(
            padding: EdgeInsets.all(32),
            child: Center(child: CupertinoActivityIndicator()),
          )
        else
          SectionCard(
            children: [
              for (final style in ChatBackgroundStyle.values)
                ListTile(
                  key: Key('chat-background-${style.name}'),
                  minTileHeight: 64,
                  contentPadding: const EdgeInsets.symmetric(horizontal: 14),
                  leading: Container(
                    width: 38,
                    height: 38,
                    decoration: BoxDecoration(
                      color: _previewColor(context, style),
                      borderRadius: BorderRadius.circular(12),
                      border: Border.all(
                        color: Theme.of(context).colorScheme.outline,
                      ),
                    ),
                  ),
                  title: Text(style.title),
                  subtitle: Text(style.subtitle),
                  trailing: Icon(
                    selected == style
                        ? CupertinoIcons.check_mark_circled_solid
                        : CupertinoIcons.circle,
                    color: selected == style
                        ? LinliColors.brandInk
                        : Theme.of(
                            context,
                          ).colorScheme.onSurface.withValues(alpha: .25),
                  ),
                  enabled: !saving,
                  onTap: saving ? null : () => _select(style),
                ),
            ],
          ),
      ],
    ),
  );
}

class StorageScreen extends StatefulWidget {
  const StorageScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<StorageScreen> createState() => _StorageScreenState();
}

class _StorageScreenState extends State<StorageScreen> {
  bool clearingMessages = false;
  bool clearingMedia = false;
  int? mediaCacheBytes;
  bool mediaCacheFailed = false;

  @override
  void initState() {
    super.initState();
    _loadMediaCache();
  }

  Future<void> _loadMediaCache() async {
    try {
      final bytes = await messageMediaCacheBytes();
      if (!mounted) return;
      setState(() {
        mediaCacheBytes = bytes;
        mediaCacheFailed = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        mediaCacheBytes = null;
        mediaCacheFailed = true;
      });
    }
  }

  int get loadedMessageCount => widget.controller.conversations.fold(
    0,
    (total, conversation) =>
        total + widget.controller.messagesFor(conversation.id).length,
  );

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('存储空间')),
    body: ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        const _PageIntro('本页只清理当前账号的本机消息索引，不删除云端记录。重新同步后，云端消息可能再次出现。'),
        const SectionHeader('本机缓存'),
        SectionCard(
          children: [
            _SettingsRow(
              key: const Key('storage-loaded-messages'),
              icon: CupertinoIcons.chat_bubble_2,
              title: '已载入消息',
              subtitle: '当前内存中可核验的消息数量',
              status: '$loadedMessageCount 条',
            ),
            _SettingsRow(
              key: const Key('storage-clear-local-messages'),
              icon: CupertinoIcons.delete,
              title: clearingMessages ? '正在清理…' : '清除本机消息缓存',
              subtitle: '覆盖所有当前账号会话的本机缓存',
              destructive: true,
              onTap: clearingMessages || loadedMessageCount == 0
                  ? null
                  : () => _confirmClear(context),
            ),
          ],
        ),
        const SectionHeader('媒体缓存'),
        if (mediaCacheFailed)
          _SettingsNotice(
            message: '媒体缓存大小读取失败，本机文件尚未被修改。',
            actionLabel: '重试',
            onAction: _loadMediaCache,
          ),
        SectionCard(
          children: [
            _SettingsRow(
              icon: CupertinoIcons.photo,
              title: '已下载媒体',
              subtitle: '聊天中打开过的视频和文件临时缓存',
              status: mediaCacheFailed
                  ? '读取失败'
                  : mediaCacheBytes == null
                  ? '计算中'
                  : _formatBytes(mediaCacheBytes!),
            ),
            _SettingsRow(
              key: const Key('storage-clear-media-cache'),
              icon: CupertinoIcons.trash,
              title: clearingMedia ? '正在清理…' : '清理媒体缓存',
              subtitle: mediaCacheBytes == 0 ? '当前没有可清理的媒体缓存' : '不会删除云端文件和聊天消息',
              destructive: true,
              onTap:
                  clearingMedia ||
                      mediaCacheFailed ||
                      mediaCacheBytes == null ||
                      mediaCacheBytes == 0
                  ? null
                  : () => _confirmClearMedia(context),
            ),
          ],
        ),
      ],
    ),
  );

  void _confirmClear(BuildContext context) => showCupertinoModalPopup<void>(
    context: context,
    builder: (sheetContext) => CupertinoActionSheet(
      title: const Text('清除本机消息缓存？'),
      message: const Text('云端消息不会删除，后续同步可能重新下载。'),
      actions: [
        CupertinoActionSheetAction(
          isDestructiveAction: true,
          onPressed: () {
            Navigator.pop(sheetContext);
            _clear();
          },
          child: const Text('清除本机缓存'),
        ),
      ],
      cancelButton: CupertinoActionSheetAction(
        onPressed: () => Navigator.pop(sheetContext),
        child: const Text('取消'),
      ),
    ),
  );

  Future<void> _clear() async {
    setState(() => clearingMessages = true);
    var cleared = 0;
    var failed = 0;
    for (final conversation in widget.controller.conversations) {
      try {
        await widget.controller.clearLocalMessages(conversation.id);
        cleared++;
      } catch (_) {
        failed++;
      }
    }
    if (!mounted) return;
    setState(() => clearingMessages = false);
    final message = failed == 0
        ? '本机消息缓存已清除'
        : cleared == 0
        ? '本机消息缓存清理失败，聊天记录未被修改'
        : '已清理 $cleared 个会话，另有 $failed 个会话清理失败';
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(message)));
  }

  void _confirmClearMedia(
    BuildContext context,
  ) => showCupertinoModalPopup<void>(
    context: context,
    builder: (sheetContext) => CupertinoActionSheet(
      title: const Text('清理媒体缓存？'),
      message: Text(
        '将删除本机已下载的 ${_formatBytes(mediaCacheBytes ?? 0)} 媒体临时文件，不会删除云端文件和聊天消息。',
      ),
      actions: [
        CupertinoActionSheetAction(
          isDestructiveAction: true,
          onPressed: () {
            Navigator.pop(sheetContext);
            _clearMediaCache();
          },
          child: const Text('清理媒体缓存'),
        ),
      ],
      cancelButton: CupertinoActionSheetAction(
        onPressed: () => Navigator.pop(sheetContext),
        child: const Text('取消'),
      ),
    ),
  );

  Future<void> _clearMediaCache() async {
    setState(() => clearingMedia = true);
    try {
      await clearMessageMediaCache();
      if (!mounted) return;
      setState(() {
        mediaCacheBytes = 0;
        mediaCacheFailed = false;
      });
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('媒体缓存已清理')));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('媒体缓存清理失败，请稍后重试')));
    } finally {
      if (mounted) setState(() => clearingMedia = false);
    }
  }

  String _formatBytes(int value) {
    if (value < 1024) return '$value B';
    if (value < 1024 * 1024) return '${(value / 1024).toStringAsFixed(1)} KB';
    return '${(value / (1024 * 1024)).toStringAsFixed(1)} MB';
  }
}

class HelpFeedbackScreen extends StatefulWidget {
  const HelpFeedbackScreen({
    super.key,
    required this.controller,
    this.store = const LocalSettingsStore(),
  });

  final AppController controller;
  final LocalSettingsStore store;

  @override
  State<HelpFeedbackScreen> createState() => _HelpFeedbackScreenState();
}

class _HelpFeedbackScreenState extends State<HelpFeedbackScreen> {
  final controller = TextEditingController();
  String category = '功能建议';
  bool loaded = false;
  bool saving = false;
  String? draftLoadError;

  @override
  void initState() {
    super.initState();
    _loadDraft();
  }

  @override
  void dispose() {
    controller.dispose();
    super.dispose();
  }

  Future<void> _loadDraft() async {
    try {
      final values = await Future.wait<String>([
        widget.store.readString(LocalSettingsStore.feedbackDraft),
        widget.store.readString(
          LocalSettingsStore.feedbackCategory,
          fallback: '功能建议',
        ),
      ]);
      if (!mounted) return;
      controller.text = values[0];
      setState(() {
        category = values[1];
        draftLoadError = null;
        loaded = true;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        draftLoadError = '本机反馈草稿读取失败，你仍可以直接填写并提交。';
        loaded = true;
      });
    }
  }

  Future<void> _saveDraft() async {
    final value = controller.text.trim();
    if (value.isEmpty) return;
    setState(() => saving = true);
    try {
      await widget.store.writeString(LocalSettingsStore.feedbackDraft, value);
      await widget.store.writeString(
        LocalSettingsStore.feedbackCategory,
        category,
      );
      if (!mounted) return;
      setState(() => draftLoadError = null);
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('反馈草稿已保存在本机')));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('反馈草稿保存失败，填写内容仍保留在当前页面')));
    } finally {
      if (mounted) setState(() => saving = false);
    }
  }

  Future<void> _submit() async {
    final value = controller.text.trim();
    if (value.isEmpty) return;
    setState(() => saving = true);
    final categoryValue = switch (category) {
      '功能建议' => 'product',
      '使用问题' => 'support',
      '稳定性问题' => 'stability',
      '隐私与安全' => 'privacy',
      _ => 'other',
    };
    final success = await widget.controller.submitFeedback(
      category: categoryValue,
      content: value,
    );
    if (!mounted) return;
    setState(() => saving = false);
    if (!success) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '提交失败')),
      );
      return;
    }
    controller.clear();
    var draftCleared = true;
    try {
      await widget.store.writeString(LocalSettingsStore.feedbackDraft, '');
    } catch (_) {
      draftCleared = false;
    }
    if (!mounted) return;
    setState(() {});
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(draftCleared ? '反馈已提交' : '反馈已提交，但本机草稿未能清除')),
    );
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('帮助与反馈')),
    body: ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
      children: [
        const SectionHeader('常见问题'),
        SectionCard(
          children: const [
            _FaqRow(
              question: '为什么消息会重新出现？',
              answer: '清除本机缓存不会删除云端消息，重新同步后云端记录可能再次下载。',
            ),
            _FaqRow(
              question: '如何屏蔽或举报联系人？',
              answer: '打开联系人资料，在安全与数据区域选择加入黑名单或举报。',
            ),
            _FaqRow(
              question: '如何管理收藏？',
              answer: '在聊天中长按消息选择收藏；打开“我的收藏”可查看原会话，向左滑动可取消收藏。',
            ),
          ],
        ),
        const SectionHeader('提交反馈'),
        if (draftLoadError != null)
          _SettingsNotice(
            message: draftLoadError!,
            actionLabel: '重新读取',
            onAction: () {
              setState(() {
                loaded = false;
                draftLoadError = null;
              });
              _loadDraft();
            },
          ),
        const _PageIntro('反馈会通过已认证接口提交；未完成时可先保存本机草稿。'),
        DropdownButtonFormField<String>(
          key: const Key('feedback-category'),
          initialValue: category,
          decoration: const InputDecoration(labelText: '问题类型'),
          items: const ['功能建议', '使用问题', '稳定性问题', '隐私与安全']
              .map((item) => DropdownMenuItem(value: item, child: Text(item)))
              .toList(),
          onChanged: loaded
              ? (value) => setState(() => category = value ?? category)
              : null,
        ),
        const SizedBox(height: 12),
        TextField(
          key: const Key('feedback-field'),
          controller: controller,
          minLines: 4,
          maxLines: 7,
          maxLength: 600,
          decoration: const InputDecoration(
            labelText: '描述问题或建议',
            alignLabelWithHint: true,
            hintText: '请写明发生场景、预期结果和实际结果',
          ),
          onChanged: (_) => setState(() {}),
        ),
        const SizedBox(height: 8),
        FilledButton(
          key: const Key('submit-feedback'),
          onPressed: loaded && !saving && controller.text.trim().isNotEmpty
              ? _submit
              : null,
          child: Text(saving ? '正在提交…' : '提交反馈'),
        ),
        const SizedBox(height: 8),
        OutlinedButton(
          key: const Key('save-feedback-draft'),
          onPressed: loaded && !saving && controller.text.trim().isNotEmpty
              ? _saveDraft
              : null,
          child: const Text('保存反馈草稿'),
        ),
      ],
    ),
  );
}

class AboutScreen extends StatefulWidget {
  const AboutScreen({super.key});

  @override
  State<AboutScreen> createState() => _AboutScreenState();
}

class _AboutScreenState extends State<AboutScreen> {
  PackageInfo? packageInfo;

  String get versionLabel => switch (packageInfo) {
    final info? => '${info.version} (${info.buildNumber})',
    _ => '读取中…',
  };

  @override
  void initState() {
    super.initState();
    _loadPackageInfo();
  }

  Future<void> _loadPackageInfo() async {
    try {
      final loaded = await PackageInfo.fromPlatform();
      if (mounted) setState(() => packageInfo = loaded);
    } catch (_) {
      // 测试宿主或不支持的平台可能没有原生插件；正式平台会读取安装包元数据。
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('关于青蛙呱呱')),
    body: ListView(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 36),
      children: [
        Center(
          child: Column(
            children: [
              ClipRRect(
                borderRadius: BorderRadius.circular(16),
                child: Image.asset(
                  'assets/brand/qingwaguagua-icon.png',
                  width: 72,
                  height: 72,
                  semanticLabel: '青蛙呱呱图标',
                ),
              ),
              const SizedBox(height: 12),
              Text('青蛙呱呱', style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: 4),
              Text(
                '版本 $versionLabel',
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ],
          ),
        ),
        const SectionHeader('法律信息'),
        SectionCard(
          children: [
            _SettingsRow(
              icon: CupertinoIcons.doc_text,
              title: '用户协议',
              onTap: () => showLegalDocument(context, LegalDocument.terms),
            ),
            _SettingsRow(
              icon: CupertinoIcons.hand_raised,
              title: '隐私政策',
              onTap: () => showLegalDocument(context, LegalDocument.privacy),
            ),
          ],
        ),
      ],
    ),
  );
}

class AccountDeletionScreen extends StatefulWidget {
  const AccountDeletionScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<AccountDeletionScreen> createState() => _AccountDeletionScreenState();
}

class _AccountDeletionScreenState extends State<AccountDeletionScreen> {
  final codeController = TextEditingController();
  bool understood = false;
  bool codeRequested = false;
  bool busy = false;

  @override
  void dispose() {
    codeController.dispose();
    super.dispose();
  }

  Future<void> _requestCode() async {
    setState(() => busy = true);
    final success = await widget.controller.requestAccountDeletionCode();
    if (!mounted) return;
    setState(() {
      busy = false;
      codeRequested = success;
    });
    if (success) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('验证码已发送到当前绑定手机号')));
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '验证码发送失败，请稍后重试')),
      );
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('注销账号')),
    body: ListView(
      padding: const EdgeInsets.all(16),
      children: [
        const StatePanel(
          icon: CupertinoIcons.exclamationmark_triangle,
          title: '注销操作不可恢复',
          body: '完成手机号验证后，账号会立即停用并匿名化，登录设备、好友关系和群成员关系会被清理；历史消息仅保留“已注销用户”引用。',
        ),
        SectionCard(
          children: [
            ListTile(
              minTileHeight: 52,
              title: const Text('我已理解并确认上述影响'),
              trailing: CupertinoCheckbox(
                value: understood,
                onChanged: (value) =>
                    setState(() => understood = value ?? false),
              ),
              onTap: () => setState(() => understood = !understood),
            ),
          ],
        ),
        const SizedBox(height: 20),
        if (codeRequested) ...[
          TextField(
            key: const Key('account-deletion-code'),
            controller: codeController,
            enabled: !busy,
            keyboardType: TextInputType.number,
            autofillHints: const [AutofillHints.oneTimeCode],
            decoration: InputDecoration(
              labelText: '手机号验证码',
              helperText: '验证码 5 分钟内有效',
              suffixIcon: TextButton(
                onPressed: busy ? null : _requestCode,
                child: const Text('重新发送'),
              ),
            ),
          ),
          const SizedBox(height: 20),
        ],
        FilledButton(
          key: const Key('account-deletion-action'),
          style: FilledButton.styleFrom(
            backgroundColor: LinliColors.systemRed,
            foregroundColor: Colors.white,
          ),
          onPressed: !understood || busy
              ? null
              : codeRequested
              ? _confirmDeletion
              : _requestCode,
          child: busy
              ? const CupertinoActivityIndicator(color: Colors.white)
              : Text(codeRequested ? '验证并永久注销' : '发送验证码'),
        ),
      ],
    ),
  );

  void _confirmDeletion() => showCupertinoModalPopup<void>(
    context: context,
    builder: (sheetContext) => CupertinoActionSheet(
      title: const Text('提交注销申请？'),
      message: const Text('注销后将退出所有设备，且无法恢复。若你仍是群主，请先转让群主或解散群聊。'),
      actions: [
        CupertinoActionSheetAction(
          isDestructiveAction: true,
          onPressed: () async {
            Navigator.pop(sheetContext);
            final code = codeController.text.trim();
            if (code.length < 4) {
              ScaffoldMessenger.of(
                context,
              ).showSnackBar(const SnackBar(content: Text('请输入收到的验证码')));
              return;
            }
            setState(() => busy = true);
            final success = await widget.controller.deleteAccount(code);
            if (!mounted) return;
            setState(() => busy = false);
            if (success) {
              Navigator.of(context).popUntil((route) => route.isFirst);
            } else {
              ScaffoldMessenger.of(context).showSnackBar(
                SnackBar(
                  content: Text(widget.controller.error ?? '账号注销失败，请稍后重试'),
                ),
              );
            }
          },
          child: const Text('确认永久注销'),
        ),
      ],
      cancelButton: CupertinoActionSheetAction(
        onPressed: () => Navigator.pop(sheetContext),
        child: const Text('取消'),
      ),
    ),
  );
}

class ReportScreen extends StatefulWidget {
  const ReportScreen({
    super.key,
    required this.target,
    this.controller,
    this.targetId = '',
    this.targetType = 'user',
  });

  final String target;
  final AppController? controller;
  final String targetId;
  final String targetType;

  @override
  State<ReportScreen> createState() => _ReportScreenState();
}

class _ReportScreenState extends State<ReportScreen> {
  String? reason;
  static const reasons = ['垃圾广告', '欺诈或冒充', '骚扰或仇恨言论', '色情或不适内容', '其他'];

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('提交举报')),
    body: ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        Padding(
          padding: const EdgeInsets.all(16),
          child: Text(
            '举报 ${widget.target}\n举报内容将被安全审核，对方不会知道你的身份。',
            style: Theme.of(context).textTheme.bodyMedium,
          ),
        ),
        SectionCard(
          children: reasons
              .map(
                (item) => ListTile(
                  minTileHeight: 52,
                  title: Text(item),
                  trailing: reason == item
                      ? Icon(
                          CupertinoIcons.check_mark,
                          color: Theme.of(context).colorScheme.primary,
                        )
                      : const SizedBox(width: 20),
                  onTap: () => setState(() => reason = item),
                ),
              )
              .toList(),
        ),
        const SizedBox(height: 24),
        FilledButton(
          onPressed: reason == null
              ? null
              : () async {
                  final success =
                      await widget.controller?.reportTarget(
                        targetType: widget.targetType,
                        targetId: widget.targetId,
                        reason: reason!,
                      ) ??
                      true;
                  if (!context.mounted) return;
                  if (!success) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      SnackBar(
                        content: Text(
                          widget.controller?.error ?? '举报提交失败，请稍后重试',
                        ),
                      ),
                    );
                    return;
                  }
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(content: Text('举报已提交，我们会尽快审核')),
                  );
                  Navigator.pop(context);
                },
          child: const Text('提交举报'),
        ),
      ],
    ),
  );
}

class _SettingsRow extends StatelessWidget {
  const _SettingsRow({
    super.key,
    required this.icon,
    required this.title,
    this.subtitle,
    this.status,
    this.trailing,
    this.onTap,
    this.destructive = false,
  });

  final IconData icon;
  final String title;
  final String? subtitle;
  final String? status;
  final Widget? trailing;
  final VoidCallback? onTap;
  final bool destructive;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    final color = destructive
        ? LinliColors.systemRed
        : dark
        ? LinliColors.brandYellow
        : LinliColors.brandInk;
    return Semantics(
      button: onTap != null,
      enabled: onTap != null,
      child: ListTile(
        minTileHeight: subtitle == null ? 56 : 64,
        contentPadding: const EdgeInsets.symmetric(horizontal: 14),
        leading: Container(
          width: 32,
          height: 32,
          decoration: BoxDecoration(
            color: color.withValues(alpha: dark ? .16 : .08),
            borderRadius: BorderRadius.circular(9),
          ),
          alignment: Alignment.center,
          child: Icon(icon, size: 18, color: color),
        ),
        title: Text(
          title,
          style: Theme.of(context).textTheme.bodyLarge?.copyWith(
            color: destructive ? LinliColors.systemRed : null,
            fontWeight: FontWeight.w500,
          ),
        ),
        subtitle: subtitle == null
            ? null
            : Text(subtitle!, style: Theme.of(context).textTheme.bodySmall),
        trailing:
            trailing ??
            (status != null
                ? ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 92),
                    child: Text(
                      status!,
                      textAlign: TextAlign.end,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: Theme.of(context).textTheme.labelMedium,
                    ),
                  )
                : Icon(
                    CupertinoIcons.chevron_forward,
                    size: 17,
                    color: Theme.of(
                      context,
                    ).colorScheme.onSurface.withValues(alpha: .25),
                  )),
        onTap: onTap,
      ),
    );
  }
}

class _SwitchRow extends StatelessWidget {
  const _SwitchRow({
    super.key,
    required this.icon,
    required this.title,
    required this.value,
    required this.enabled,
    required this.onChanged,
    this.subtitle,
  });

  final IconData icon;
  final String title;
  final String? subtitle;
  final bool value;
  final bool enabled;
  final ValueChanged<bool> onChanged;

  @override
  Widget build(BuildContext context) {
    final disabledTrack = Theme.of(
      context,
    ).colorScheme.onSurface.withValues(alpha: .18);
    return _SettingsRow(
      icon: icon,
      title: title,
      subtitle: subtitle,
      trailing: CupertinoSwitch(
        value: value,
        onChanged: enabled ? onChanged : null,
        activeTrackColor: enabled ? null : disabledTrack,
        inactiveTrackColor: enabled ? null : disabledTrack,
      ),
      onTap: enabled ? () => onChanged(!value) : null,
    );
  }
}

class _PageIntro extends StatelessWidget {
  const _PageIntro(this.text);

  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(top: 8),
    child: Text(text, style: Theme.of(context).textTheme.bodyMedium),
  );
}

class _SettingsNotice extends StatelessWidget {
  const _SettingsNotice({
    super.key,
    required this.message,
    this.actionLabel,
    this.onAction,
  });

  final String message;
  final String? actionLabel;
  final VoidCallback? onAction;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    return Container(
      margin: const EdgeInsets.only(top: 12),
      padding: const EdgeInsets.fromLTRB(12, 10, 8, 10),
      decoration: BoxDecoration(
        color: LinliColors.systemRed.withValues(alpha: dark ? .14 : .07),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(
          color: LinliColors.systemRed.withValues(alpha: dark ? .28 : .16),
        ),
      ),
      child: Row(
        children: [
          const Icon(
            CupertinoIcons.exclamationmark_circle,
            size: 18,
            color: LinliColors.systemRed,
          ),
          const SizedBox(width: 9),
          Expanded(
            child: Text(message, style: Theme.of(context).textTheme.bodySmall),
          ),
          if (actionLabel != null)
            TextButton(onPressed: onAction, child: Text(actionLabel!)),
        ],
      ),
    );
  }
}

class _FaqRow extends StatelessWidget {
  const _FaqRow({required this.question, required this.answer});

  final String question;
  final String answer;

  @override
  Widget build(BuildContext context) => ExpansionTile(
    tilePadding: const EdgeInsets.symmetric(horizontal: 14),
    childrenPadding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
    leading: Icon(
      CupertinoIcons.question_circle,
      color: Theme.of(context).brightness == Brightness.dark
          ? LinliColors.brandYellow
          : LinliColors.brandInk,
    ),
    title: Text(question, style: Theme.of(context).textTheme.bodyLarge),
    children: [
      Align(
        alignment: Alignment.centerLeft,
        child: Text(answer, style: Theme.of(context).textTheme.bodyMedium),
      ),
    ],
  );
}

String _maskedPhone(String? phone) {
  final value = phone?.trim() ?? '';
  if (value.length < 7) return value.isEmpty ? '未绑定' : value;
  return '${value.substring(0, 3)}****${value.substring(value.length - 4)}';
}

String _platformName(String platform) => switch (platform.toLowerCase()) {
  'ios' => 'iPhone / iPad',
  'android' => 'Android 设备',
  'macos' => 'Mac',
  'windows' => 'Windows 设备',
  _ => '登录设备',
};

String _imDeviceName(int deviceFlag) => switch (deviceFlag) {
  0 => 'App（Android / iOS）',
  1 => 'Web',
  2 => '桌面端（macOS）',
  _ => '未知平台',
};

IconData _imDeviceIcon(int deviceFlag) => switch (deviceFlag) {
  0 => CupertinoIcons.device_phone_portrait,
  1 => CupertinoIcons.globe,
  2 => CupertinoIcons.desktopcomputer,
  _ => CupertinoIcons.device_phone_portrait,
};

String _dateText(DateTime time) {
  final local = time.toLocal();
  return '${local.year}/${local.month.toString().padLeft(2, '0')}/${local.day.toString().padLeft(2, '0')}';
}

String _monthDay(DateTime time) {
  final local = time.toLocal();
  return '${local.month}/${local.day}';
}
