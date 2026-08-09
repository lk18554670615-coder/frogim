import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:permission_handler/permission_handler.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/browser_notification_permission.dart';
import '../../core/media_opener.dart';
import '../../core/models.dart';
import '../legal_documents.dart';
import '../widgets/linli_widgets.dart';
import 'chat_screen.dart';
import 'settings_preferences.dart';

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
              subtitle: '邻里号、登录设备与账号注销',
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
              title: '关于邻里通讯',
              subtitle: '版本、协议与开源许可',
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
              subtitle: '退出后保留本机缓存',
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
      message: const Text('本机缓存会保留，重新登录后可以继续同步。'),
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

class AccountSecurityScreen extends StatelessWidget {
  const AccountSecurityScreen({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final user = controller.currentUser;
    return Scaffold(
      appBar: const GlassAppBar(title: Text('账号与安全')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 36),
        children: [
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
                subtitle: Text('@${user?.handle ?? '未设置'}'),
                trailing: const Icon(CupertinoIcons.chevron_forward, size: 17),
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
                icon: CupertinoIcons.at,
                title: '邻里号',
                subtitle: user?.handle ?? '未设置',
                status: '只读',
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
  }
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
  Uint8List? avatarBytes;
  String? avatarFileName;
  String? avatarMime;
  String? avatarPath;
  bool saving = false;

  @override
  void initState() {
    super.initState();
    final user = widget.controller.currentUser;
    nameController = TextEditingController(text: user?.name ?? '');
    handleController = TextEditingController(text: user?.handle ?? '');
    signatureController = TextEditingController(
      text: user?.signature ?? user?.presence ?? '',
    );
  }

  @override
  void dispose() {
    nameController.dispose();
    handleController.dispose();
    signatureController.dispose();
    super.dispose();
  }

  Future<void> _pickAvatar() async {
    final file = await ImagePicker().pickImage(
      source: ImageSource.gallery,
      imageQuality: 88,
      maxWidth: 1600,
      maxHeight: 1600,
    );
    if (file == null) return;
    final bytes = await file.readAsBytes();
    if (bytes.length > 8 * 1024 * 1024) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('头像不能超过 8 MB')));
      }
      return;
    }
    final lower = file.name.toLowerCase();
    setState(() {
      avatarBytes = bytes;
      avatarFileName = file.name;
      avatarPath = file.path;
      avatarMime = lower.endsWith('.png') ? 'image/png' : 'image/jpeg';
    });
  }

  Future<void> _save() async {
    if (!(formKey.currentState?.validate() ?? false)) return;
    setState(() => saving = true);
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
      name: nameController.text,
      handle: handleController.text,
      signature: signatureController.text,
      avatar: avatar,
    );
    if (!mounted) return;
    setState(() => saving = false);
    if (success) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('个人资料已更新')));
      Navigator.pop(context);
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '保存失败')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final user = widget.controller.currentUser;
    return Scaffold(
      appBar: GlassAppBar(
        title: const Text('编辑资料'),
        actions: [
          TextButton(
            key: const Key('save-profile'),
            onPressed: saving ? null : _save,
            child: Text(saving ? '保存中' : '保存'),
          ),
        ],
      ),
      body: Form(
        key: formKey,
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 20, 16, 36),
          keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
          children: [
            Center(
              child: Semantics(
                button: true,
                label: '更换头像',
                child: GestureDetector(
                  onTap: saving ? null : _pickAvatar,
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
                                Theme.of(context).brightness == Brightness.dark
                                ? LinliColors.yellow
                                : LinliColors.navy,
                            shape: BoxShape.circle,
                            border: Border.all(
                              color: Theme.of(context).colorScheme.surface,
                              width: 2,
                            ),
                          ),
                          child: Icon(
                            CupertinoIcons.camera_fill,
                            size: 15,
                            color:
                                Theme.of(context).brightness == Brightness.dark
                                ? LinliColors.navy
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
              '头像会先上传到 MinIO，完成校验后再更新个人资料。',
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodySmall,
            ),
            const SizedBox(height: 24),
            TextFormField(
              key: const Key('profile-name'),
              controller: nameController,
              maxLength: 40,
              textInputAction: TextInputAction.next,
              decoration: const InputDecoration(labelText: '昵称'),
              validator: (value) {
                final text = value?.trim() ?? '';
                if (text.isEmpty) return '请输入昵称';
                if (text.characters.length > 40) return '昵称不能超过 40 个字符';
                return null;
              },
            ),
            const SizedBox(height: 12),
            TextFormField(
              key: const Key('profile-handle'),
              controller: handleController,
              maxLength: 24,
              autocorrect: false,
              textCapitalization: TextCapitalization.none,
              textInputAction: TextInputAction.next,
              enabled:
                  (user?.handleChangesRemaining ?? 0) > 0 ||
                  (user?.handle.isEmpty ?? true),
              decoration: InputDecoration(
                labelText: '邻里号 / 用户名',
                prefixText: '@',
                helperText: (user?.handleChangesRemaining ?? 0) > 0
                    ? '还可修改 ${user!.handleChangesRemaining} 次，4–24 位小写字母、数字或下划线'
                    : '修改次数已用完，如需处理请联系平台客服',
              ),
              validator: (value) {
                final text = value?.trim().toLowerCase() ?? '';
                if (!RegExp(r'^[a-z0-9_]{4,24}$').hasMatch(text)) {
                  return '请输入有效的邻里号';
                }
                return null;
              },
            ),
            const SizedBox(height: 12),
            _SettingsRow(
              icon: CupertinoIcons.search,
              title: '通过邻里号找到我',
              subtitle: user?.allowSearchByHandle == true
                  ? '平台当前已开放'
                  : '平台当前已关闭',
              trailing: Icon(
                user?.allowSearchByHandle == true
                    ? CupertinoIcons.checkmark_circle_fill
                    : CupertinoIcons.xmark_circle,
                color: user?.allowSearchByHandle == true
                    ? LinliColors.systemGreen
                    : LinliColors.tertiaryLabel,
              ),
            ),
            _SettingsRow(
              icon: CupertinoIcons.phone,
              title: '通过手机号找到我',
              subtitle: user?.allowSearchByPhone == true
                  ? '平台当前已开放，搜索结果仍不会展示手机号'
                  : '平台当前已关闭，手机号不会用于用户搜索',
              trailing: Icon(
                user?.allowSearchByPhone == true
                    ? CupertinoIcons.checkmark_circle_fill
                    : CupertinoIcons.lock_fill,
                color: user?.allowSearchByPhone == true
                    ? LinliColors.systemGreen
                    : LinliColors.tertiaryLabel,
              ),
            ),
            const SizedBox(height: 12),
            TextFormField(
              key: const Key('profile-signature'),
              controller: signatureController,
              minLines: 2,
              maxLines: 4,
              maxLength: 160,
              decoration: const InputDecoration(labelText: '个性签名'),
            ),
            const SizedBox(height: 12),
            _SettingsRow(
              icon: CupertinoIcons.phone,
              title: '手机号',
              subtitle: _maskedPhone(user?.phone),
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) =>
                      ChangePhoneScreen(controller: widget.controller),
                ),
              ),
            ),
          ],
        ),
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
  final phoneController = TextEditingController();
  final codeController = TextEditingController();
  bool requested = false;
  bool busy = false;

  @override
  void dispose() {
    phoneController.dispose();
    codeController.dispose();
    super.dispose();
  }

  Future<void> _requestCode() async {
    final phone = phoneController.text.trim();
    if (phone.length < 6) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('请输入有效手机号')));
      return;
    }
    setState(() => busy = true);
    final success = await widget.controller.requestPhoneUpdateCode(phone);
    if (!mounted) return;
    setState(() {
      busy = false;
      requested = success;
    });
    if (success) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('验证码已发送')));
    }
  }

  Future<void> _confirm() async {
    if (codeController.text.trim().length < 4) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('请输入验证码')));
      return;
    }
    setState(() => busy = true);
    final success = await widget.controller.updatePhone(
      phoneController.text,
      codeController.text,
    );
    if (!mounted) return;
    setState(() => busy = false);
    if (success) Navigator.pop(context);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('换绑手机号')),
    body: ListView(
      padding: const EdgeInsets.all(16),
      children: [
        _PageIntro(
          '当前绑定：${_maskedPhone(widget.controller.currentUser?.phone)}',
        ),
        const SizedBox(height: 20),
        TextField(
          key: const Key('new-phone'),
          controller: phoneController,
          enabled: !requested && !busy,
          keyboardType: TextInputType.phone,
          autofillHints: const [AutofillHints.telephoneNumber],
          decoration: const InputDecoration(labelText: '新手机号'),
        ),
        if (requested) ...[
          const SizedBox(height: 12),
          TextField(
            key: const Key('phone-change-code'),
            controller: codeController,
            keyboardType: TextInputType.number,
            autofillHints: const [AutofillHints.oneTimeCode],
            decoration: const InputDecoration(labelText: '验证码'),
          ),
        ],
        const SizedBox(height: 20),
        FilledButton(
          key: const Key('phone-change-action'),
          onPressed: busy
              ? null
              : requested
              ? _confirm
              : _requestCode,
          child: Text(requested ? '确认换绑' : '发送验证码'),
        ),
      ],
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
  List<ChatMessage>? items;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final loaded = await widget.controller.loadFavorites();
    if (mounted) setState(() => items = loaded);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('我的收藏')),
    body: items == null
        ? const Center(child: CupertinoActivityIndicator())
        : items!.isEmpty
        ? const StatePanel(
            icon: CupertinoIcons.bookmark,
            title: '还没有收藏',
            body: '在聊天中长按消息并选择收藏，内容会同步显示在这里。',
          )
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
              itemCount: items!.length,
              separatorBuilder: (_, _) => const SizedBox(height: 8),
              itemBuilder: (context, index) {
                final message = items![index];
                return Dismissible(
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
                    child: const Icon(
                      CupertinoIcons.delete,
                      color: Colors.white,
                    ),
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
                        '${message.senderName} · ${message.sentAt.month}/${message.sentAt.day}',
                      ),
                      trailing: const Icon(
                        CupertinoIcons.chevron_forward,
                        size: 16,
                      ),
                      onTap: () => _openConversation(message),
                    ),
                  ),
                );
              },
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
      MaterialPageRoute(
        builder: (_) => ChatScreen(
          controller: widget.controller,
          conversation: conversation!,
          initialMessageId: message.id,
        ),
      ),
    );
  }
}

class DevicesScreen extends StatefulWidget {
  const DevicesScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<DevicesScreen> createState() => _DevicesScreenState();
}

class _DevicesScreenState extends State<DevicesScreen> {
  List<UserDevice>? devices;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final loaded = await widget.controller.loadUserDevices();
    if (mounted) setState(() => devices = loaded);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('登录设备')),
    body: devices == null
        ? const Center(child: CupertinoActivityIndicator())
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
              children: [
                const _PageIntro('设备列表来自服务端，不展示推送令牌。移除后，该设备需要重新完成登录。'),
                const SectionHeader('登录会话'),
                if (devices!.isEmpty)
                  const StatePanel(
                    icon: CupertinoIcons.device_phone_portrait,
                    title: '暂无已注册设备',
                    body: '当前登录尚未注册推送设备，完成 APNs 或 FCM 注册后会显示。',
                  )
                else
                  SectionCard(
                    children: devices!
                        .map(
                          (device) => _SettingsRow(
                            icon: device.platform == 'ios'
                                ? CupertinoIcons.device_phone_portrait
                                : CupertinoIcons.device_phone_portrait,
                            title: _platformName(device.platform),
                            subtitle:
                                '${device.provider.toUpperCase()} · ${_dateText(device.updatedAt)}',
                            status: '移除',
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
        title: const Text('退出这台设备？'),
        content: Text('${_platformName(device.platform)} 将从设备列表移除。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, true),
            child: const Text('移除设备'),
          ),
        ],
      ),
    );
    if (remove != true) return;
    final success = await widget.controller.removeUserDevice(device.id);
    if (success) await _load();
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
  PermissionStatus? permissionStatus;
  BrowserNotificationPermission? browserPermission;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
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
      loaded = true;
    });
    await _refreshPermissionStatus();
  }

  Future<void> _set(String key, bool value, void Function() update) async {
    setState(update);
    await widget.store.writeBool(key, value);
    widget.controller?.refreshPushConfiguration();
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
      if (!mounted || status == BrowserNotificationPermission.granted) return;
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
      ).showSnackBar(const SnackBar(content: Text('请在系统设置中管理邻里通讯的通知权限')));
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('消息通知')),
    body: ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        const _PageIntro('通知偏好会同步到当前推送设备；关闭总开关后，服务端不再向该设备发送离线通知。'),
        const SectionHeader('通知偏好'),
        SectionCard(
          children: [
            _SwitchRow(
              key: const Key('notification-enabled-switch'),
              icon: CupertinoIcons.bell,
              title: '允许消息提醒',
              subtitle: '作为本机通知模块的默认总开关',
              value: enabled,
              enabled: loaded,
              onChanged: (value) => _set(
                LocalSettingsStore.notificationEnabled,
                value,
                () => enabled = value,
              ),
            ),
            _SwitchRow(
              icon: CupertinoIcons.text_bubble,
              title: '显示消息预览',
              subtitle: '关闭后通知仅显示发送者',
              value: preview,
              enabled: loaded && enabled,
              onChanged: (value) => _set(
                LocalSettingsStore.notificationPreview,
                value,
                () => preview = value,
              ),
            ),
            _SwitchRow(
              icon: CupertinoIcons.speaker_2,
              title: '提示音',
              value: sound,
              enabled: loaded && enabled,
              onChanged: (value) => _set(
                LocalSettingsStore.notificationSound,
                value,
                () => sound = value,
              ),
            ),
            _SwitchRow(
              icon: CupertinoIcons.device_phone_portrait,
              title: '振动',
              value: vibration,
              enabled: loaded && enabled,
              onChanged: (value) => _set(
                LocalSettingsStore.notificationVibration,
                value,
                () => vibration = value,
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
                  ? '仅在网页仍打开时提醒，多标签页只显示一次'
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

class PrivacyScreen extends StatelessWidget {
  const PrivacyScreen({
    super.key,
    this.controller,
    this.store = const LocalSettingsStore(),
  });

  final AppController? controller;
  final LocalSettingsStore store;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('隐私与安全')),
    body: ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        const _PageIntro('数据能力只按当前实现展示。敏感操作需要确认，未接入的远程策略不会提供假开关。'),
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
        SectionCard(
          children: [
            _SettingsRow(
              key: const Key('privacy-clear-search-history'),
              icon: CupertinoIcons.clock,
              title: '清除最近搜索',
              subtitle: '删除本机保存的搜索关键词',
              onTap: () => _clearSearchHistory(context),
            ),
            _SettingsRow(
              icon: CupertinoIcons.person_crop_circle_badge_xmark,
              title: '黑名单',
              subtitle: '查看已屏蔽的用户并可随时解除',
              onTap: controller == null
                  ? null
                  : () => Navigator.of(context).push(
                      MaterialPageRoute(
                        builder: (_) =>
                            BlockedUsersScreen(controller: controller!),
                      ),
                    ),
            ),
          ],
        ),
      ],
    ),
  );

  Future<void> _clearSearchHistory(BuildContext context) async {
    await store.clearRecentSearches();
    if (!context.mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('最近搜索已从本机清除')));
  }
}

class BlockedUsersScreen extends StatefulWidget {
  const BlockedUsersScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<BlockedUsersScreen> createState() => _BlockedUsersScreenState();
}

class _BlockedUsersScreenState extends State<BlockedUsersScreen> {
  List<AppUser>? users;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final loaded = await widget.controller.loadBlockedUsers();
    if (mounted) setState(() => users = loaded);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('黑名单')),
    body: users == null
        ? const Center(child: CupertinoActivityIndicator())
        : users!.isEmpty
        ? const StatePanel(
            icon: CupertinoIcons.person_crop_circle_badge_checkmark,
            title: '黑名单为空',
            body: '加入黑名单的用户将无法继续向你发起新消息。',
          )
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
              itemCount: users!.length,
              separatorBuilder: (_, _) => const Divider(height: 1),
              itemBuilder: (context, index) {
                final user = users![index];
                return ListTile(
                  minTileHeight: 68,
                  leading: PersonAvatar(
                    name: user.name,
                    avatarUrl: user.avatarUrl,
                  ),
                  title: Text(user.name),
                  subtitle: Text('@${user.handle}'),
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
      setState(() => users!.removeWhere((item) => item.id == user.id));
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

class StorageScreen extends StatefulWidget {
  const StorageScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<StorageScreen> createState() => _StorageScreenState();
}

class _StorageScreenState extends State<StorageScreen> {
  bool clearing = false;
  int? mediaCacheBytes;

  @override
  void initState() {
    super.initState();
    _loadMediaCache();
  }

  Future<void> _loadMediaCache() async {
    final bytes = await messageMediaCacheBytes();
    if (mounted) setState(() => mediaCacheBytes = bytes);
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
              title: clearing ? '正在清理…' : '清除本机消息缓存',
              subtitle: '覆盖所有当前账号会话的本机缓存',
              destructive: true,
              onTap: clearing ? null : () => _confirmClear(context),
            ),
          ],
        ),
        const SectionHeader('媒体缓存'),
        SectionCard(
          children: [
            _SettingsRow(
              icon: CupertinoIcons.photo,
              title: '已下载媒体',
              subtitle: '聊天中打开过的视频和文件临时缓存',
              status: mediaCacheBytes == null
                  ? '计算中'
                  : _formatBytes(mediaCacheBytes!),
            ),
            _SettingsRow(
              icon: CupertinoIcons.trash,
              title: '清理媒体缓存',
              subtitle: '不会删除云端文件和聊天消息',
              destructive: true,
              onTap: clearing ? null : _clearMediaCache,
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
    setState(() => clearing = true);
    for (final conversation in widget.controller.conversations) {
      await widget.controller.clearLocalMessages(conversation.id);
    }
    if (!mounted) return;
    setState(() => clearing = false);
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('本机消息缓存已清除')));
  }

  Future<void> _clearMediaCache() async {
    setState(() => clearing = true);
    await clearMessageMediaCache();
    if (!mounted) return;
    setState(() {
      clearing = false;
      mediaCacheBytes = 0;
    });
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('媒体缓存已清理')));
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
      loaded = true;
    });
  }

  Future<void> _saveDraft() async {
    final value = controller.text.trim();
    if (value.isEmpty) return;
    setState(() => saving = true);
    await widget.store.writeString(LocalSettingsStore.feedbackDraft, value);
    await widget.store.writeString(
      LocalSettingsStore.feedbackCategory,
      category,
    );
    if (!mounted) return;
    setState(() => saving = false);
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('反馈草稿已保存在本机')));
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
    await widget.store.writeString(LocalSettingsStore.feedbackDraft, '');
    if (!mounted) return;
    setState(() {});
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('反馈已提交')));
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
    appBar: const GlassAppBar(title: Text('关于邻里通讯')),
    body: ListView(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 36),
      children: [
        Center(
          child: Column(
            children: [
              ClipRRect(
                borderRadius: BorderRadius.circular(16),
                child: Image.asset(
                  'assets/brand/linli-im-icon.png',
                  width: 72,
                  height: 72,
                  semanticLabel: '邻里通讯图标',
                ),
              ),
              const SizedBox(height: 12),
              Text('邻里通讯', style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: 4),
              Text(
                '版本 $versionLabel',
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ],
          ),
        ),
        const SectionHeader('法律与许可'),
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
            _SettingsRow(
              key: const Key('open-source-licenses'),
              icon: CupertinoIcons.chevron_left_slash_chevron_right,
              title: '开源软件许可',
              onTap: () => showLicensePage(
                context: context,
                applicationName: '邻里通讯',
                applicationVersion: versionLabel,
              ),
            ),
          ],
        ),
        const SectionHeader('服务状态'),
        SectionCard(
          children: [
            _SettingsRow(
              icon: CupertinoIcons.check_mark_circled,
              title: '客户端版本',
              subtitle: '与 pubspec.yaml 的当前发布版本一致',
              status: packageInfo?.version ?? '读取中',
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
                  if (!context.mounted || !success) return;
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
        ? LinliColors.yellow
        : LinliColors.navy;
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
  Widget build(BuildContext context) => _SettingsRow(
    icon: icon,
    title: title,
    subtitle: subtitle,
    trailing: CupertinoSwitch(
      value: value,
      onChanged: enabled ? onChanged : null,
    ),
    onTap: enabled ? () => onChanged(!value) : null,
  );
}

class _PageIntro extends StatelessWidget {
  const _PageIntro(this.text);

  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
    child: Text(text, style: Theme.of(context).textTheme.bodyMedium),
  );
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
          ? LinliColors.yellow
          : LinliColors.navy,
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

String _dateText(DateTime time) =>
    '${time.year}/${time.month.toString().padLeft(2, '0')}/${time.day.toString().padLeft(2, '0')}';
