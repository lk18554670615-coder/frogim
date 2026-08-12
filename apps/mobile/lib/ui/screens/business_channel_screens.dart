import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../im/business_features.dart';
import '../widgets/linli_widgets.dart';
import 'chat_screen.dart';

class BusinessChannelHubScreen extends StatefulWidget {
  const BusinessChannelHubScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<BusinessChannelHubScreen> createState() =>
      _BusinessChannelHubScreenState();
}

class _BusinessChannelHubScreenState extends State<BusinessChannelHubScreen> {
  int channelType = 4;
  String parentId = '';
  bool loading = true;
  String error = '';
  List<BusinessChannelSummary> channels = const [];

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    setState(() {
      loading = true;
      error = '';
    });
    try {
      final items = await widget.controller.loadBusinessChannels(
        channelType: channelType,
        parentId: parentId,
      );
      if (mounted) setState(() => channels = items);
    } catch (cause) {
      if (mounted) setState(() => error = '$cause');
    } finally {
      if (mounted) setState(() => loading = false);
    }
  }

  Future<void> _selectType(int value) async {
    channelType = value;
    if (value != 5) parentId = '';
    await _load();
  }

  Future<void> _enter(BusinessChannelSummary channel) async {
    try {
      DateTime? expiresAt;
      if (!channel.subscribed && channel.channelType == 6) {
        final duration = await showModalBottomSheet<Duration?>(
          context: context,
          showDragHandle: true,
          builder: (sheetContext) => SafeArea(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const ListTile(
                  title: Text('选择订阅时长'),
                  subtitle: Text('到期后会自动退出资讯频道'),
                ),
                ListTile(
                  title: const Text('永久订阅'),
                  onTap: () => Navigator.pop(sheetContext, Duration.zero),
                ),
                ListTile(
                  title: const Text('订阅 1 天'),
                  onTap: () =>
                      Navigator.pop(sheetContext, const Duration(days: 1)),
                ),
                ListTile(
                  title: const Text('订阅 7 天'),
                  onTap: () =>
                      Navigator.pop(sheetContext, const Duration(days: 7)),
                ),
              ],
            ),
          ),
        );
        if (duration == null) return;
        expiresAt = duration == Duration.zero
            ? null
            : DateTime.now().add(duration);
      }
      final conversation = await widget.controller.enterBusinessChannel(
        channel,
        expiresAt: expiresAt,
      );
      if (!mounted) return;
      if (conversation == null) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('频道已加入，消息通道正在同步，请稍后重试')));
        await _load();
        return;
      }
      await Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => ChatScreen(
            controller: widget.controller,
            conversation: conversation,
          ),
        ),
      );
      await _load();
    } catch (cause) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('暂时无法进入：$cause')));
      }
    }
  }

  Future<void> _details(BusinessChannelSummary channel) async {
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => BusinessChannelDetailScreen(
          controller: widget.controller,
          channel: channel,
          onOpenChat: _enter,
        ),
      ),
    );
    await _load();
  }

  Future<void> _showCreate() async {
    final name = TextEditingController();
    final description = TextEditingController();
    final parent = TextEditingController(text: parentId);
    final created = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text('创建${_channelTypeLabel(channelType)}'),
        content: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                key: const Key('business-channel-name'),
                controller: name,
                autofocus: true,
                maxLength: 100,
                decoration: const InputDecoration(labelText: '名称'),
              ),
              if (channelType == 5)
                TextField(
                  key: const Key('business-channel-parent'),
                  controller: parent,
                  decoration: const InputDecoration(labelText: '所属社区 ID'),
                ),
              TextField(
                controller: description,
                maxLength: 2000,
                minLines: 2,
                maxLines: 4,
                decoration: const InputDecoration(labelText: '简介'),
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () async {
              if (name.text.trim().isEmpty ||
                  (channelType == 5 && parent.text.trim().isEmpty)) {
                return;
              }
              try {
                await widget.controller.createBusinessChannel(
                  channelType: channelType,
                  name: name.text.trim(),
                  parentId: parent.text.trim(),
                  description: description.text.trim(),
                  postingPolicy: channelType == 6 ? 'operators' : 'members',
                );
                if (dialogContext.mounted) Navigator.pop(dialogContext, true);
              } catch (cause) {
                if (dialogContext.mounted) {
                  ScaffoldMessenger.of(
                    dialogContext,
                  ).showSnackBar(SnackBar(content: Text('创建失败：$cause')));
                }
              }
            },
            child: const Text('创建'),
          ),
        ],
      ),
    );
    name.dispose();
    description.dispose();
    parent.dispose();
    if (created == true) await _load();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: GlassAppBar(
      title: const Text('社区与频道'),
      actions: [
        IconButton(
          tooltip: '创建频道',
          onPressed: _showCreate,
          icon: const Icon(CupertinoIcons.add),
        ),
      ],
    ),
    body: Column(
      children: [
        SingleChildScrollView(
          scrollDirection: Axis.horizontal,
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
          child: SegmentedButton<int>(
            segments: const [
              ButtonSegment(value: 4, label: Text('社区')),
              ButtonSegment(value: 5, label: Text('话题')),
              ButtonSegment(value: 6, label: Text('资讯')),
              ButtonSegment(value: 9, label: Text('直播')),
            ],
            selected: {channelType},
            onSelectionChanged: (values) =>
                unawaited(_selectType(values.first)),
            showSelectedIcon: false,
          ),
        ),
        if (channelType == 5 && parentId.isNotEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Row(
              children: [
                Expanded(child: Text('社区：$parentId')),
                TextButton(
                  onPressed: () {
                    parentId = '';
                    unawaited(_load());
                  },
                  child: const Text('查看全部'),
                ),
              ],
            ),
          ),
        Expanded(child: _body()),
      ],
    ),
  );

  Widget _body() {
    if (loading) return const Center(child: CircularProgressIndicator());
    if (error.isNotEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(CupertinoIcons.exclamationmark_circle, size: 32),
              const SizedBox(height: 12),
              Text(error, textAlign: TextAlign.center),
              const SizedBox(height: 12),
              FilledButton(onPressed: _load, child: const Text('重新加载')),
            ],
          ),
        ),
      );
    }
    if (channels.isEmpty) {
      return Center(child: Text('暂无${_channelTypeLabel(channelType)}'));
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 28),
        itemCount: channels.length,
        separatorBuilder: (_, _) => const SizedBox(height: 10),
        itemBuilder: (context, index) {
          final channel = channels[index];
          return Card(
            margin: EdgeInsets.zero,
            child: ListTile(
              minTileHeight: 76,
              leading: PersonAvatar(
                name: channel.name,
                avatarUrl: channel.avatarUrl,
                size: 44,
              ),
              title: Text(channel.name),
              subtitle: Text(
                channel.description.isEmpty
                    ? '${channel.memberCount} 位成员'
                    : '${channel.description}\n${channel.memberCount} 位成员',
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
              trailing: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  IconButton(
                    tooltip: '频道详情',
                    onPressed: () => _details(channel),
                    icon: const Icon(CupertinoIcons.info_circle),
                  ),
                  FilledButton.tonal(
                    onPressed: () => _enter(channel),
                    child: Text(channel.subscribed ? '进入' : '加入'),
                  ),
                ],
              ),
              onTap: () {
                if (channel.channelType == 4) {
                  setState(() {
                    channelType = 5;
                    parentId = channel.id;
                  });
                  unawaited(_load());
                } else {
                  unawaited(_enter(channel));
                }
              },
            ),
          );
        },
      ),
    );
  }
}

class BusinessChannelDetailScreen extends StatefulWidget {
  const BusinessChannelDetailScreen({
    super.key,
    required this.controller,
    required this.channel,
    required this.onOpenChat,
  });

  final AppController controller;
  final BusinessChannelSummary channel;
  final Future<void> Function(BusinessChannelSummary channel) onOpenChat;

  @override
  State<BusinessChannelDetailScreen> createState() =>
      _BusinessChannelDetailScreenState();
}

class _BusinessChannelDetailScreenState
    extends State<BusinessChannelDetailScreen> {
  late BusinessChannelSummary channel = widget.channel;
  List<BusinessChannelMemberSummary> members = const [];
  List<BusinessChannelAccessSummary> access = const [];
  bool loading = true;
  String error = '';

  bool get operator =>
      channel.role == 'owner' ||
      channel.role == 'admin' ||
      channel.role == 'moderator';

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    setState(() {
      loading = true;
      error = '';
    });
    try {
      final current = await widget.controller.loadBusinessChannel(
        channel.id,
        channel.channelType,
      );
      final loadedMembers = current.subscribed
          ? await widget.controller.loadBusinessChannelMembers(current)
          : <BusinessChannelMemberSummary>[];
      var loadedAccess = <BusinessChannelAccessSummary>[];
      if (current.role == 'owner' ||
          current.role == 'admin' ||
          current.role == 'moderator') {
        loadedAccess = await widget.controller.loadBusinessChannelAccess(
          current,
        );
      }
      if (!mounted) return;
      setState(() {
        channel = current;
        members = loadedMembers;
        access = loadedAccess;
      });
    } catch (cause) {
      if (mounted) setState(() => error = '$cause');
    } finally {
      if (mounted) setState(() => loading = false);
    }
  }

  Future<void> _edit() async {
    final name = TextEditingController(text: channel.name);
    final description = TextEditingController(text: channel.description);
    var visibility = channel.visibility;
    var joinPolicy = channel.joinPolicy;
    var postingPolicy = channel.postingPolicy;
    var slowMode = channel.slowModeSeconds;
    var sendBan = channel.sendBan;
    var allowStranger = channel.allowStranger;
    final accepted = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: const Text('频道设置'),
          content: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  controller: name,
                  maxLength: 100,
                  decoration: const InputDecoration(labelText: '名称'),
                ),
                TextField(
                  controller: description,
                  maxLength: 2000,
                  maxLines: 3,
                  decoration: const InputDecoration(labelText: '简介'),
                ),
                DropdownButtonFormField<String>(
                  initialValue: visibility,
                  decoration: const InputDecoration(labelText: '可见性'),
                  items: const [
                    DropdownMenuItem(value: 'public', child: Text('公开')),
                    DropdownMenuItem(value: 'private', child: Text('私有')),
                  ],
                  onChanged: (value) =>
                      setDialogState(() => visibility = value ?? visibility),
                ),
                DropdownButtonFormField<String>(
                  initialValue: joinPolicy,
                  decoration: const InputDecoration(labelText: '加入方式'),
                  items: const [
                    DropdownMenuItem(value: 'open', child: Text('自由加入')),
                    DropdownMenuItem(value: 'approval', child: Text('需要审核')),
                    DropdownMenuItem(value: 'invite', child: Text('仅邀请')),
                    DropdownMenuItem(value: 'closed', child: Text('关闭加入')),
                  ],
                  onChanged: (value) =>
                      setDialogState(() => joinPolicy = value ?? joinPolicy),
                ),
                DropdownButtonFormField<String>(
                  initialValue: postingPolicy,
                  decoration: const InputDecoration(labelText: '发言权限'),
                  items: const [
                    DropdownMenuItem(value: 'members', child: Text('所有成员')),
                    DropdownMenuItem(value: 'operators', child: Text('仅管理员')),
                  ],
                  onChanged: (value) => setDialogState(
                    () => postingPolicy = value ?? postingPolicy,
                  ),
                ),
                if (channel.channelType == 9)
                  TextFormField(
                    initialValue: '$slowMode',
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(
                      labelText: '慢速模式（秒，0 为关闭）',
                    ),
                    onChanged: (value) => slowMode = int.tryParse(value) ?? 0,
                  ),
                SwitchListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('全员禁言'),
                  value: sendBan,
                  onChanged: (value) => setDialogState(() => sendBan = value),
                ),
                SwitchListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('允许非成员访问'),
                  value: allowStranger,
                  onChanged: (value) =>
                      setDialogState(() => allowStranger = value),
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(dialogContext, true),
              child: const Text('保存'),
            ),
          ],
        ),
      ),
    );
    try {
      if (accepted == true) {
        channel = await widget.controller.updateBusinessChannel(
          channel,
          name: name.text.trim(),
          description: description.text.trim(),
          visibility: visibility,
          joinPolicy: joinPolicy,
          postingPolicy: postingPolicy,
          slowModeSeconds: slowMode.clamp(0, 86400),
          sendBan: sendBan,
          allowStranger: allowStranger,
        );
        await _load();
      }
    } finally {
      name.dispose();
      description.dispose();
    }
  }

  Future<void> _addMember() async {
    final user = TextEditingController();
    var temporaryDays = channel.channelType == 6 ? 7 : 0;
    final accepted = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: const Text('添加成员'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: user,
                autofocus: true,
                decoration: const InputDecoration(labelText: '用户 ID'),
              ),
              if (channel.channelType == 6)
                DropdownButtonFormField<int>(
                  initialValue: temporaryDays,
                  decoration: const InputDecoration(labelText: '成员有效期'),
                  items: const [
                    DropdownMenuItem(value: 0, child: Text('永久')),
                    DropdownMenuItem(value: 1, child: Text('1 天')),
                    DropdownMenuItem(value: 7, child: Text('7 天')),
                    DropdownMenuItem(value: 30, child: Text('30 天')),
                  ],
                  onChanged: (value) => setDialogState(
                    () => temporaryDays = value ?? temporaryDays,
                  ),
                ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(dialogContext, true),
              child: const Text('添加'),
            ),
          ],
        ),
      ),
    );
    try {
      if (accepted == true && user.text.trim().isNotEmpty) {
        await widget.controller.addBusinessChannelMember(
          channel,
          user.text.trim(),
          expiresAt: temporaryDays == 0
              ? null
              : DateTime.now().add(Duration(days: temporaryDays)),
        );
        await _load();
      }
    } finally {
      user.dispose();
    }
  }

  Future<void> _memberAction(
    BusinessChannelMemberSummary member,
    String action,
  ) async {
    switch (action) {
      case 'admin':
        await widget.controller.updateBusinessChannelMember(
          channel,
          member.userId,
          role: 'admin',
        );
        break;
      case 'moderator':
        await widget.controller.updateBusinessChannelMember(
          channel,
          member.userId,
          role: 'moderator',
        );
        break;
      case 'member':
        await widget.controller.updateBusinessChannelMember(
          channel,
          member.userId,
          role: 'member',
        );
        break;
      case 'mute':
        await widget.controller.updateBusinessChannelMember(
          channel,
          member.userId,
          mutedUntil: DateTime.now().add(const Duration(hours: 1)),
        );
        break;
      case 'unmute':
        await widget.controller.updateBusinessChannelMember(
          channel,
          member.userId,
          clearMute: true,
        );
        break;
      case 'temporary':
        await widget.controller.updateBusinessChannelMember(
          channel,
          member.userId,
          expiresAt: DateTime.now().add(const Duration(days: 1)),
        );
        break;
      case 'permanent':
        await widget.controller.updateBusinessChannelMember(
          channel,
          member.userId,
          clearExpiry: true,
        );
        break;
      case 'remove':
        await widget.controller.removeBusinessChannelMember(
          channel,
          member.userId,
        );
        break;
    }
    await _load();
  }

  Future<void> _addAccess() async {
    final user = TextEditingController();
    final reason = TextEditingController();
    var type = 'deny';
    final accepted = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: const Text('添加访问名单'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: user,
                decoration: const InputDecoration(labelText: '用户 ID'),
              ),
              DropdownButtonFormField<String>(
                initialValue: type,
                items: const [
                  DropdownMenuItem(value: 'deny', child: Text('黑名单')),
                  DropdownMenuItem(value: 'allow', child: Text('白名单')),
                ],
                onChanged: (value) =>
                    setDialogState(() => type = value ?? type),
              ),
              TextField(
                controller: reason,
                decoration: const InputDecoration(labelText: '原因'),
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(dialogContext, true),
              child: const Text('添加'),
            ),
          ],
        ),
      ),
    );
    try {
      if (accepted == true && user.text.trim().isNotEmpty) {
        await widget.controller.setBusinessChannelAccess(
          channel,
          user.text.trim(),
          type,
          true,
          reason: reason.text.trim(),
        );
        await _load();
      }
    } finally {
      user.dispose();
      reason.dispose();
    }
  }

  Future<void> _leave() async {
    await widget.controller.leaveBusinessChannel(channel);
    if (mounted) Navigator.pop(context);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: GlassAppBar(
      title: Text(channel.name),
      actions: [
        if (operator)
          IconButton(
            tooltip: '频道设置',
            onPressed: _edit,
            icon: const Icon(CupertinoIcons.settings),
          ),
      ],
    ),
    body: loading
        ? const Center(child: CircularProgressIndicator())
        : error.isNotEmpty
        ? Center(
            child: FilledButton(onPressed: _load, child: Text(error)),
          )
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
              children: [
                Card(
                  child: Padding(
                    padding: const EdgeInsets.all(16),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          channel.name,
                          style: Theme.of(context).textTheme.titleLarge,
                        ),
                        const SizedBox(height: 6),
                        Text(
                          channel.description.isEmpty
                              ? '暂无简介'
                              : channel.description,
                        ),
                        const SizedBox(height: 10),
                        Text(
                          '${channel.memberCount} 位成员 · ${channel.role.isEmpty ? '访客' : channel.role}'
                          '${channel.slowModeSeconds > 0 ? ' · 慢速 ${channel.slowModeSeconds} 秒' : ''}'
                          '${channel.sendBan ? ' · 全员禁言' : ''}',
                        ),
                      ],
                    ),
                  ),
                ),
                const SizedBox(height: 10),
                FilledButton.icon(
                  onPressed: () => widget.onOpenChat(channel),
                  icon: const Icon(CupertinoIcons.chat_bubble_2),
                  label: Text(channel.subscribed ? '进入聊天' : '加入频道'),
                ),
                if (operator) ...[
                  SectionHeader('成员'),
                  Align(
                    alignment: Alignment.centerLeft,
                    child: TextButton.icon(
                      onPressed: _addMember,
                      icon: const Icon(CupertinoIcons.person_add),
                      label: const Text('添加成员'),
                    ),
                  ),
                  ...members.map(
                    (member) => Card(
                      child: ListTile(
                        leading: PersonAvatar(
                          name: member.name,
                          avatarUrl: member.avatarUrl,
                          size: 40,
                        ),
                        title: Text(
                          member.name.isEmpty ? member.userId : member.name,
                        ),
                        subtitle: Text(
                          '${member.role}${member.mutedUntil != null && member.mutedUntil!.isAfter(DateTime.now()) ? ' · 已禁言' : ''}${member.expiresAt == null ? '' : ' · 临时成员'}',
                        ),
                        trailing: member.role == 'owner'
                            ? const Text('群主')
                            : PopupMenuButton<String>(
                                onSelected: (action) =>
                                    _memberAction(member, action),
                                itemBuilder: (_) => [
                                  if (channel.role == 'owner') ...[
                                    const PopupMenuItem(
                                      value: 'admin',
                                      child: Text('设为管理员'),
                                    ),
                                    const PopupMenuItem(
                                      value: 'moderator',
                                      child: Text('设为版主'),
                                    ),
                                    const PopupMenuItem(
                                      value: 'member',
                                      child: Text('设为成员'),
                                    ),
                                  ],
                                  if (member.mutedUntil != null &&
                                      member.mutedUntil!.isAfter(
                                        DateTime.now(),
                                      ))
                                    const PopupMenuItem(
                                      value: 'unmute',
                                      child: Text('解除禁言'),
                                    )
                                  else
                                    const PopupMenuItem(
                                      value: 'mute',
                                      child: Text('禁言 1 小时'),
                                    ),
                                  if (member.expiresAt == null)
                                    const PopupMenuItem(
                                      value: 'temporary',
                                      child: Text('改为临时 1 天'),
                                    )
                                  else
                                    const PopupMenuItem(
                                      value: 'permanent',
                                      child: Text('改为永久成员'),
                                    ),
                                  const PopupMenuItem(
                                    value: 'remove',
                                    child: Text('移除成员'),
                                  ),
                                ],
                              ),
                      ),
                    ),
                  ),
                  SectionHeader('黑白名单'),
                  Align(
                    alignment: Alignment.centerLeft,
                    child: TextButton.icon(
                      onPressed: _addAccess,
                      icon: const Icon(CupertinoIcons.shield),
                      label: const Text('添加名单'),
                    ),
                  ),
                  if (access.isEmpty)
                    const Card(child: ListTile(title: Text('当前没有访问名单')))
                  else
                    ...access.map(
                      (item) => Card(
                        child: ListTile(
                          title: Text(
                            item.name.isEmpty ? item.userId : item.name,
                          ),
                          subtitle: Text(
                            '${item.accessType == 'deny' ? '黑名单' : '白名单'}${item.reason.isEmpty ? '' : ' · ${item.reason}'}',
                          ),
                          trailing: IconButton(
                            tooltip: '移除名单',
                            onPressed: () async {
                              await widget.controller.setBusinessChannelAccess(
                                channel,
                                item.userId,
                                item.accessType,
                                false,
                              );
                              await _load();
                            },
                            icon: const Icon(CupertinoIcons.delete),
                          ),
                        ),
                      ),
                    ),
                ],
                if (channel.subscribed && channel.role != 'owner') ...[
                  const SizedBox(height: 20),
                  OutlinedButton(onPressed: _leave, child: const Text('退出频道')),
                ],
              ],
            ),
          ),
  );
}

class SupportCenterScreen extends StatefulWidget {
  const SupportCenterScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<SupportCenterScreen> createState() => _SupportCenterScreenState();
}

class _SupportCenterScreenState extends State<SupportCenterScreen> {
  bool loading = true;
  String error = '';
  List<SupportSkillGroupSummary> skills = const [];
  List<SupportSessionSummary> sessions = const [];
  List<SupportAgentSummary> agents = const [];
  SupportAgentSummary? meAsAgent;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    setState(() {
      loading = true;
      error = '';
    });
    try {
      final loadedSkills = await widget.controller.loadSupportSkillGroups();
      final loadedSessions = await widget.controller.loadSupportSessions();
      List<SupportAgentSummary> loadedAgents = const [];
      try {
        loadedAgents = await widget.controller.loadSupportAgents();
      } catch (_) {
        // Visitors cannot enumerate support agents.
      }
      if (!mounted) return;
      final currentId = widget.controller.currentUser?.id;
      setState(() {
        skills = loadedSkills;
        sessions = loadedSessions;
        agents = loadedAgents;
        meAsAgent = loadedAgents
            .where((item) => item.userId == currentId)
            .firstOrNull;
      });
    } catch (cause) {
      if (mounted) setState(() => error = '$cause');
    } finally {
      if (mounted) setState(() => loading = false);
    }
  }

  Future<void> _start(SupportSkillGroupSummary skill) async {
    final subject = TextEditingController();
    final accepted = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text('联系${skill.name}'),
        content: TextField(
          controller: subject,
          autofocus: true,
          maxLength: 240,
          maxLines: 3,
          decoration: const InputDecoration(
            labelText: '简单描述问题',
            hintText: '例如：订单退款进度',
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(dialogContext, true),
            child: const Text('开始会话'),
          ),
        ],
      ),
    );
    if (accepted != true) {
      subject.dispose();
      return;
    }
    try {
      final result = await widget.controller.startSupportSession(
        skillGroupId: skill.id,
        subject: subject.text.trim(),
      );
      if (!mounted) return;
      if (result.$2 != null) {
        await Navigator.of(context).push(
          MaterialPageRoute(
            builder: (_) => ChatScreen(
              controller: widget.controller,
              conversation: result.$2!,
            ),
          ),
        );
      }
      await _load();
    } catch (cause) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('创建客服会话失败：$cause')));
      }
    } finally {
      subject.dispose();
    }
  }

  Future<void> _open(SupportSessionSummary session) async {
    try {
      final conversation = await widget.controller.enterSupportSession(session);
      if (!mounted) return;
      if (conversation == null) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('消息通道正在同步，请稍后重试')));
        return;
      }
      await Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => ChatScreen(
            controller: widget.controller,
            conversation: conversation,
          ),
        ),
      );
      await _load();
    } catch (cause) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('暂时无法进入会话：$cause')));
      }
    }
  }

  Future<void> _claim(SupportSessionSummary session) async {
    try {
      final claimed = await widget.controller.claimSupportSession(session.id);
      await _load();
      if (mounted) await _open(claimed);
    } catch (cause) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('接入失败：$cause')));
      }
    }
  }

  Future<void> _setAgentStatus(String status) async {
    try {
      await widget.controller.setSupportAgentStatus(status);
      await _load();
    } catch (cause) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('状态更新失败：$cause')));
      }
    }
  }

  Future<void> _end(SupportSessionSummary session) async {
    try {
      await widget.controller.endSupportSession(session.id);
      await _load();
    } catch (cause) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('结束会话失败：$cause')));
      }
    }
  }

  Future<void> _transfer(SupportSessionSummary session) async {
    final candidates = agents
        .where(
          (agent) =>
              agent.userId != session.assignedAgentId &&
              agent.skillGroupIds.contains(session.skillGroupId) &&
              (agent.status == 'available' || agent.status == 'busy'),
        )
        .toList();
    if (candidates.isEmpty) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('当前没有可转接的客服')));
      return;
    }
    final target = await showModalBottomSheet<SupportAgentSummary>(
      context: context,
      showDragHandle: true,
      builder: (sheetContext) => SafeArea(
        child: ListView(
          shrinkWrap: true,
          children: [
            const ListTile(title: Text('选择转接客服')),
            ...candidates.map(
              (agent) => ListTile(
                leading: PersonAvatar(
                  name: agent.name,
                  avatarUrl: agent.avatarUrl,
                  size: 40,
                ),
                title: Text(agent.name),
                subtitle: Text(
                  '${agent.status == 'available' ? '可接待' : '忙碌'} · '
                  '${agent.activeSessions}/${agent.maxConcurrent}',
                ),
                onTap: () => Navigator.pop(sheetContext, agent),
              ),
            ),
          ],
        ),
      ),
    );
    if (target == null) return;
    try {
      await widget.controller.transferSupportSession(session.id, target.userId);
      await _load();
    } catch (cause) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('转接失败：$cause')));
      }
    }
  }

  Future<void> _rate(SupportSessionSummary session) async {
    var rating = 5;
    final comment = TextEditingController();
    final accepted = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: const Text('评价客服'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: List.generate(
                  5,
                  (index) => IconButton(
                    onPressed: () => setDialogState(() => rating = index + 1),
                    icon: Icon(
                      index < rating
                          ? CupertinoIcons.star_fill
                          : CupertinoIcons.star,
                    ),
                  ),
                ),
              ),
              TextField(
                controller: comment,
                maxLength: 1000,
                maxLines: 3,
                decoration: const InputDecoration(labelText: '补充评价（选填）'),
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(dialogContext, true),
              child: const Text('提交评价'),
            ),
          ],
        ),
      ),
    );
    try {
      if (accepted == true) {
        await widget.controller.rateSupportSession(
          session.id,
          rating,
          comment.text.trim(),
        );
        await _load();
      }
    } catch (cause) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('评价提交失败：$cause')));
      }
    } finally {
      comment.dispose();
    }
  }

  Widget _sessionAction(SupportSessionSummary session) {
    final currentId = widget.controller.currentUser?.id ?? '';
    if (session.status == 'queued' && meAsAgent != null) {
      return FilledButton.tonal(
        onPressed: () => _claim(session),
        child: const Text('接入'),
      );
    }
    if (session.status == 'ended' &&
        session.visitorId == currentId &&
        session.rating == 0) {
      return FilledButton.tonal(
        onPressed: () => _rate(session),
        child: const Text('评价'),
      );
    }
    if (session.status == 'active' || session.status == 'transferring') {
      return PopupMenuButton<String>(
        tooltip: '客服会话操作',
        onSelected: (action) {
          switch (action) {
            case 'open':
              unawaited(_open(session));
              break;
            case 'transfer':
              unawaited(_transfer(session));
              break;
            case 'end':
              unawaited(_end(session));
              break;
          }
        },
        itemBuilder: (_) => [
          const PopupMenuItem(value: 'open', child: Text('进入会话')),
          if (session.assignedAgentId == currentId && agents.length > 1)
            const PopupMenuItem(value: 'transfer', child: Text('转接客服')),
          const PopupMenuItem(value: 'end', child: Text('结束会话')),
        ],
      );
    }
    return const Icon(CupertinoIcons.chevron_forward);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: GlassAppBar(
      title: const Text('在线客服'),
      actions: [
        IconButton(
          tooltip: '刷新',
          onPressed: _load,
          icon: const Icon(CupertinoIcons.refresh),
        ),
      ],
    ),
    body: loading
        ? const Center(child: CircularProgressIndicator())
        : error.isNotEmpty
        ? Center(
            child: FilledButton(onPressed: _load, child: Text('重新加载\n$error')),
          )
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView(
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 28),
              children: [
                if (meAsAgent case final agent?) ...[
                  const SectionHeader('客服工作台'),
                  Card(
                    child: Padding(
                      padding: const EdgeInsets.all(14),
                      child: Row(
                        children: [
                          Expanded(
                            child: Text(
                              '${agent.name} · ${agent.activeSessions}/${agent.maxConcurrent} 个会话',
                            ),
                          ),
                          DropdownButton<String>(
                            value: agent.status,
                            items: const [
                              DropdownMenuItem(
                                value: 'available',
                                child: Text('可接待'),
                              ),
                              DropdownMenuItem(
                                value: 'busy',
                                child: Text('忙碌'),
                              ),
                              DropdownMenuItem(
                                value: 'away',
                                child: Text('离开'),
                              ),
                              DropdownMenuItem(
                                value: 'offline',
                                child: Text('离线'),
                              ),
                            ],
                            onChanged: (value) {
                              if (value != null) {
                                unawaited(_setAgentStatus(value));
                              }
                            },
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
                const SectionHeader('我的会话'),
                if (sessions.isEmpty)
                  const Card(
                    child: Padding(
                      padding: EdgeInsets.all(20),
                      child: Text('当前没有客服会话'),
                    ),
                  )
                else
                  ...sessions.map(
                    (session) => Card(
                      child: ListTile(
                        leading: Icon(_supportStatusIcon(session.status)),
                        title: Text(
                          session.subject.isEmpty
                              ? session.skillGroupName
                              : session.subject,
                        ),
                        subtitle: Text(_supportStatusText(session)),
                        trailing: _sessionAction(session),
                        onTap: session.status == 'queued' && meAsAgent != null
                            ? () => _claim(session)
                            : () => _open(session),
                      ),
                    ),
                  ),
                const SectionHeader('选择服务'),
                ...skills.map(
                  (skill) => Card(
                    child: ListTile(
                      leading: const Icon(CupertinoIcons.chat_bubble_2_fill),
                      title: Text(skill.name),
                      subtitle: Text(
                        '${skill.description.isEmpty ? '在线咨询' : skill.description}\n'
                        '${skill.availableAgents} 位客服在线 · ${skill.queueCount} 人排队',
                      ),
                      isThreeLine: true,
                      trailing: FilledButton.tonal(
                        onPressed: () => _start(skill),
                        child: const Text('咨询'),
                      ),
                    ),
                  ),
                ),
              ],
            ),
          ),
  );
}

String _channelTypeLabel(int channelType) => switch (channelType) {
  4 => '社区',
  5 => '话题',
  6 => '资讯频道',
  9 => '直播间',
  _ => '频道',
};

IconData _supportStatusIcon(String status) => switch (status) {
  'queued' => CupertinoIcons.clock,
  'active' => CupertinoIcons.chat_bubble_2_fill,
  'transferring' => CupertinoIcons.arrow_right_arrow_left,
  _ => CupertinoIcons.check_mark_circled,
};

String _supportStatusText(
  SupportSessionSummary session,
) => switch (session.status) {
  'queued' =>
    '排队中 · 前面 ${session.queuePosition - 1 < 0 ? 0 : session.queuePosition - 1} 人',
  'active' =>
    session.agentName.isEmpty ? '客服会话进行中' : '${session.agentName} 正在为你服务',
  'transferring' => '正在转接客服',
  'ended' => session.rating > 0 ? '已结束 · ${session.rating} 星评价' : '已结束',
  _ => session.status,
};
