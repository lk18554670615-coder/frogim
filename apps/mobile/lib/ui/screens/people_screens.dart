import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/models.dart';
import '../widgets/linli_widgets.dart';
import 'chat_screen.dart';
import 'settings_preferences.dart';
import 'relationship_screens.dart';
import 'group_management_screens.dart';

class SearchScreen extends StatefulWidget {
  const SearchScreen({
    super.key,
    required this.controller,
    this.settingsStore = const LocalSettingsStore(),
  });
  final AppController controller;
  final LocalSettingsStore settingsStore;
  @override
  State<SearchScreen> createState() => _SearchScreenState();
}

class _SearchScreenState extends State<SearchScreen> {
  String query = '';
  bool searching = false;
  List<AppUser> remoteResults = const [];
  Timer? debounce;
  List<String> recent = const [];
  UserSearchCapabilities? capabilities;
  String searchBy = 'handle';

  @override
  void initState() {
    super.initState();
    _loadRecent();
    _loadCapabilities();
  }

  @override
  void dispose() {
    debounce?.cancel();
    super.dispose();
  }

  void _search(String value) {
    setState(() => query = value);
    debounce?.cancel();
    final minimumLength = searchBy == 'handle' ? 4 : 5;
    if (value.trim().length < minimumLength || !_searchEnabled) {
      setState(() => remoteResults = const []);
      return;
    }
    debounce = Timer(const Duration(milliseconds: 280), () async {
      setState(() => searching = true);
      final result = await widget.controller.searchUsers(value, by: searchBy);
      if (!mounted || query != value) return;
      setState(() {
        searching = false;
        remoteResults = result;
      });
    });
  }

  bool get _searchEnabled => searchBy == 'handle'
      ? (capabilities?.allowSearchByHandle ?? true)
      : (capabilities?.allowSearchByPhone ?? false);

  Future<void> _loadCapabilities() async {
    final value = await widget.controller.loadSearchCapabilities();
    if (!mounted || value == null) return;
    setState(() {
      capabilities = value;
      if (!value.allowSearchByHandle && value.allowSearchByPhone) {
        searchBy = 'phone';
      }
    });
  }

  Future<void> _loadRecent() async {
    final values = await widget.settingsStore.readRecentSearches();
    if (mounted) setState(() => recent = values);
  }

  Future<void> _clearRecent() async {
    await widget.settingsStore.clearRecentSearches();
    if (mounted) setState(() => recent = const []);
  }

  Future<void> _rememberQuery() async {
    final value = query.trim();
    if (value.isEmpty) return;
    await widget.settingsStore.addRecentSearch(value);
  }

  @override
  Widget build(BuildContext context) {
    final matchingContacts = widget.controller.contacts
        .where(
          (user) => '${user.name}${user.handle}'.toLowerCase().contains(
            query.toLowerCase(),
          ),
        )
        .toList();
    final contacts = <String, AppUser>{
      for (final user in matchingContacts) user.id: user,
      for (final user in remoteResults) user.id: user,
    }.values.toList();
    final conversations = widget.controller.conversations
        .where(
          (item) => '${item.title}${item.subtitle}'.toLowerCase().contains(
            query.toLowerCase(),
          ),
        )
        .toList();
    return Scaffold(
      appBar: const GlassAppBar(title: Text('搜索')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
        children: [
          TextField(
            autofocus: true,
            onChanged: _search,
            decoration: InputDecoration(
              prefixIcon: const Icon(CupertinoIcons.search),
              hintText: searchBy == 'handle' ? '输入完整邻里号' : '输入完整手机号',
            ),
          ),
          if (capabilities?.allowSearchByHandle == true &&
              capabilities?.allowSearchByPhone == true) ...[
            const SizedBox(height: 12),
            CupertinoSlidingSegmentedControl<String>(
              groupValue: searchBy,
              children: const {
                'handle': Padding(
                  padding: EdgeInsets.symmetric(horizontal: 12),
                  child: Text('邻里号'),
                ),
                'phone': Padding(
                  padding: EdgeInsets.symmetric(horizontal: 12),
                  child: Text('手机号'),
                ),
              },
              onValueChanged: (value) {
                if (value == null) return;
                setState(() {
                  searchBy = value;
                  remoteResults = const [];
                });
                _search(query);
              },
            ),
          ],
          if (capabilities != null && !_searchEnabled) ...[
            const SizedBox(height: 12),
            Text(
              searchBy == 'phone'
                  ? '平台当前已关闭手机号搜索，手机号不会展示给其他用户。'
                  : '平台当前已关闭邻里号搜索。',
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ] else if (capabilities?.allowSearchByPhone == false) ...[
            const SizedBox(height: 8),
            Text(
              '为保护隐私，平台当前不支持通过手机号查找用户。',
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ],
          const SizedBox(height: 24),
          if (searching) const LinearProgressIndicator(minHeight: 2),
          if (query.isEmpty) ...[
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('最近搜索', style: Theme.of(context).textTheme.titleMedium),
                TextButton(
                  key: const Key('clear-recent-searches'),
                  onPressed: recent.isEmpty ? null : _clearRecent,
                  child: const Text('清除'),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: recent
                  .map(
                    (item) => ActionChip(
                      avatar: const Icon(CupertinoIcons.time, size: 17),
                      label: Text(item),
                      onPressed: () => _search(item),
                    ),
                  )
                  .toList(),
            ),
            const SizedBox(height: 32),
            const StatePanel(
              icon: CupertinoIcons.search_circle,
              title: '一次搜索，找到所有',
              body: '联系人、群聊和本地消息会在这里统一呈现。',
            ),
          ] else if (contacts.isEmpty && conversations.isEmpty)
            const StatePanel(
              icon: CupertinoIcons.search,
              title: '没有找到结果',
              body: '换个名字、邻里号或关键词试试。',
            )
          else ...[
            if (contacts.isNotEmpty) ...[
              Text('联系人', style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 8),
              ...contacts.map(
                (user) => ListTile(
                  contentPadding: EdgeInsets.zero,
                  leading: PersonAvatar(
                    name: user.name,
                    online: user.isOnline,
                    avatarUrl: user.avatarUrl,
                  ),
                  title: Text(user.name),
                  subtitle: Text('@${user.handle}'),
                  trailing:
                      widget.controller.contacts.any(
                        (contact) => contact.id == user.id,
                      )
                      ? null
                      : const Icon(CupertinoIcons.person_add),
                  onTap: () => _openUser(user),
                ),
              ),
            ],
            if (conversations.isNotEmpty) ...[
              const SizedBox(height: 24),
              Text('会话与消息', style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 8),
              ...conversations.map(
                (item) => ListTile(
                  contentPadding: EdgeInsets.zero,
                  leading: PersonAvatar(name: item.title),
                  title: Text(item.title),
                  subtitle: Text(item.subtitle),
                  onTap: () => _openConversation(item),
                ),
              ),
            ],
          ],
        ],
      ),
    );
  }

  Future<void> _openUser(AppUser user) async {
    await _rememberQuery();
    if (!mounted) return;
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => FriendProfileScreen(
          controller: widget.controller,
          user: user,
          requestSource: 'search',
        ),
      ),
    );
  }

  Future<void> _openConversation(Conversation conversation) async {
    await _rememberQuery();
    if (!mounted) return;
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ChatScreen(
          controller: widget.controller,
          conversation: conversation,
        ),
      ),
    );
  }
}

class FriendRequestsScreen extends StatefulWidget {
  const FriendRequestsScreen({super.key, required this.controller});
  final AppController controller;
  @override
  State<FriendRequestsScreen> createState() => _FriendRequestsScreenState();
}

class _FriendRequestsScreenState extends State<FriendRequestsScreen> {
  final handling = <String>{};
  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) {
      final requests = [...widget.controller.requests]
        ..sort((a, b) {
          if (a.status == 'pending' && b.status != 'pending') return -1;
          if (a.status != 'pending' && b.status == 'pending') return 1;
          return (b.updatedAt ?? b.createdAt ?? DateTime(1970)).compareTo(
            a.updatedAt ?? a.createdAt ?? DateTime(1970),
          );
        });
      return Scaffold(
        appBar: const GlassAppBar(title: Text('新的朋友')),
        body: requests.isEmpty
            ? const StatePanel(
                icon: CupertinoIcons.person_add,
                title: '暂无好友申请',
                body: '通过邻里号、二维码、群聊或名片发起的申请会显示在这里。',
              )
            : ListView.separated(
                padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
                itemCount: requests.length,
                separatorBuilder: (_, _) => const SizedBox(height: 10),
                itemBuilder: (context, index) => _FriendRequestCard(
                  request: requests[index],
                  busy: handling.contains(requests[index].id),
                  onOpen: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => FriendRequestDetailsScreen(
                        controller: widget.controller,
                        request: requests[index],
                      ),
                    ),
                  ),
                  onAction: (action) => _handle(requests[index], action),
                ),
              ),
      );
    },
  );

  Future<void> _handle(FriendRequest request, String action) async {
    setState(() => handling.add(request.id));
    final success = switch (action) {
      'accept' => await widget.controller.acceptRequest(request),
      'reject' => await widget.controller.rejectRequest(request),
      _ => await widget.controller.cancelRequest(request),
    };
    if (!mounted) return;
    setState(() => handling.remove(request.id));
    if (!success) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '操作失败')),
      );
    }
  }
}

class _FriendRequestCard extends StatelessWidget {
  const _FriendRequestCard({
    required this.request,
    required this.busy,
    required this.onOpen,
    required this.onAction,
  });
  final FriendRequest request;
  final bool busy;
  final VoidCallback onOpen;
  final ValueChanged<String> onAction;

  @override
  Widget build(BuildContext context) => Card(
    child: InkWell(
      borderRadius: BorderRadius.circular(16),
      onTap: onOpen,
      child: Padding(
        padding: const EdgeInsets.all(14),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            PersonAvatar(
              name: request.user.name,
              size: 50,
              avatarUrl: request.user.avatarUrl,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          request.user.name,
                          style: Theme.of(context).textTheme.titleMedium,
                        ),
                      ),
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 8,
                          vertical: 4,
                        ),
                        decoration: BoxDecoration(
                          color: Theme.of(
                            context,
                          ).colorScheme.surfaceContainerHigh,
                          borderRadius: BorderRadius.circular(8),
                        ),
                        child: Text(
                          friendRequestStatusLabel(request.status),
                          style: Theme.of(context).textTheme.labelSmall,
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 3),
                  Text(
                    friendRequestSourceLabel(request.source),
                    style: Theme.of(context).textTheme.labelSmall,
                  ),
                  if (request.note.isNotEmpty) ...[
                    const SizedBox(height: 5),
                    Text(
                      request.note,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ],
                  if (request.status == 'pending') ...[
                    const SizedBox(height: 10),
                    if (busy)
                      const CupertinoActivityIndicator(radius: 9)
                    else if (request.outgoing)
                      OutlinedButton(
                        onPressed: () => onAction('cancel'),
                        child: const Text('撤回'),
                      )
                    else
                      Wrap(
                        spacing: 8,
                        runSpacing: 8,
                        children: [
                          OutlinedButton(
                            onPressed: () => onAction('reject'),
                            child: const Text('拒绝'),
                          ),
                          FilledButton(
                            onPressed: () => onAction('accept'),
                            child: const Text('同意'),
                          ),
                        ],
                      ),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    ),
  );
}

class GroupInvitationsScreen extends StatefulWidget {
  const GroupInvitationsScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<GroupInvitationsScreen> createState() => _GroupInvitationsScreenState();
}

class _GroupInvitationsScreenState extends State<GroupInvitationsScreen> {
  final handling = <String>{};

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) {
      final invitations = [...widget.controller.groupInvitations]
        ..sort((a, b) {
          if (a.pending != b.pending) return a.pending ? -1 : 1;
          return (b.updatedAt ?? b.createdAt).compareTo(
            a.updatedAt ?? a.createdAt,
          );
        });
      return Scaffold(
        appBar: const GlassAppBar(title: Text('群聊邀请')),
        body: invitations.isEmpty
            ? const StatePanel(
                icon: CupertinoIcons.person_2,
                title: '暂无群聊邀请',
                body: '好友邀请你加入群聊后，会在这里显示并由你确认。',
              )
            : ListView.separated(
                padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
                itemCount: invitations.length,
                separatorBuilder: (_, _) => const SizedBox(height: 10),
                itemBuilder: (context, index) {
                  final invitation = invitations[index];
                  return Card(
                    child: Padding(
                      padding: const EdgeInsets.all(14),
                      child: Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          PersonAvatar(name: invitation.groupName, size: 50),
                          const SizedBox(width: 12),
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(
                                  invitation.groupName,
                                  style: Theme.of(
                                    context,
                                  ).textTheme.titleMedium,
                                ),
                                const SizedBox(height: 3),
                                Text(
                                  invitation.outgoing
                                      ? '你邀请成员加入该群聊'
                                      : '${invitation.inviter.name} 邀请你加入',
                                  style: Theme.of(context).textTheme.bodySmall,
                                ),
                                const SizedBox(height: 8),
                                if (handling.contains(invitation.id))
                                  const CupertinoActivityIndicator(radius: 9)
                                else if (invitation.pending &&
                                    !invitation.outgoing)
                                  Wrap(
                                    spacing: 8,
                                    children: [
                                      OutlinedButton(
                                        onPressed: () =>
                                            _respond(invitation, 'reject'),
                                        child: const Text('拒绝'),
                                      ),
                                      FilledButton(
                                        onPressed: () =>
                                            _respond(invitation, 'accept'),
                                        child: const Text('加入群聊'),
                                      ),
                                    ],
                                  )
                                else if (invitation.pending &&
                                    invitation.outgoing)
                                  OutlinedButton(
                                    onPressed: () =>
                                        _respond(invitation, 'cancel'),
                                    child: const Text('撤回邀请'),
                                  )
                                else
                                  Text(
                                    _groupInvitationStatus(invitation),
                                    style: Theme.of(
                                      context,
                                    ).textTheme.labelMedium,
                                  ),
                              ],
                            ),
                          ),
                        ],
                      ),
                    ),
                  );
                },
              ),
      );
    },
  );

  Future<void> _respond(GroupInvitation invitation, String action) async {
    setState(() => handling.add(invitation.id));
    final success = await widget.controller.respondGroupInvitation(
      invitation,
      action,
    );
    if (!mounted) return;
    setState(() => handling.remove(invitation.id));
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
          success
              ? switch (action) {
                  'accept' => '已加入群聊',
                  'reject' => '已拒绝邀请',
                  _ => '邀请已撤回',
                }
              : widget.controller.error ?? '群邀请处理失败',
        ),
      ),
    );
  }

  String _groupInvitationStatus(GroupInvitation invitation) {
    if (invitation.status == 'accepted') return '已加入';
    if (invitation.status == 'rejected') return '已拒绝';
    if (invitation.status == 'cancelled') return '已撤回';
    if (!invitation.pending) return '已过期';
    return '待处理';
  }
}

class CreateGroupScreen extends StatefulWidget {
  const CreateGroupScreen({super.key, required this.controller});
  final AppController controller;
  @override
  State<CreateGroupScreen> createState() => _CreateGroupScreenState();
}

class _CreateGroupScreenState extends State<CreateGroupScreen> {
  final selected = <String>{};
  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: GlassAppBar(
      title: const Text('创建群聊'),
      actions: [
        Padding(
          padding: const EdgeInsets.only(right: 12),
          child: TextButton(
            onPressed: selected.isEmpty ? null : _create,
            child: Text('完成 ${selected.length}'),
          ),
        ),
      ],
    ),
    body: Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
          child: TextField(
            decoration: const InputDecoration(
              prefixIcon: Icon(CupertinoIcons.search),
              hintText: '搜索联系人',
            ),
          ),
        ),
        if (selected.isNotEmpty)
          SizedBox(
            height: 72,
            child: ListView(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
              children: widget.controller.contacts
                  .where((user) => selected.contains(user.id))
                  .map(
                    (user) => Padding(
                      padding: const EdgeInsets.only(right: 8),
                      child: PersonAvatar(
                        name: user.name,
                        size: 48,
                        avatarUrl: user.avatarUrl,
                      ),
                    ),
                  )
                  .toList(),
            ),
          ),
        Expanded(
          child: ListView.builder(
            itemCount: widget.controller.contacts.length,
            itemBuilder: (context, index) {
              final user = widget.controller.contacts[index];
              return ListTile(
                minTileHeight: 60,
                contentPadding: const EdgeInsets.symmetric(horizontal: 16),
                leading: PersonAvatar(
                  name: user.name,
                  online: user.isOnline,
                  avatarUrl: user.avatarUrl,
                ),
                title: Text(user.name),
                subtitle: Text(user.presence, softWrap: true),
                trailing: CupertinoCheckbox(
                  value: selected.contains(user.id),
                  onChanged: (value) => setState(
                    () => value!
                        ? selected.add(user.id)
                        : selected.remove(user.id),
                  ),
                ),
                onTap: () => setState(
                  () => selected.contains(user.id)
                      ? selected.remove(user.id)
                      : selected.add(user.id),
                ),
              );
            },
          ),
        ),
      ],
    ),
  );

  Future<void> _create() async {
    final members = widget.controller.contacts
        .where((user) => selected.contains(user.id))
        .toList();
    final name = await showDialog<String>(
      context: context,
      builder: (dialogContext) =>
          _GroupNameDialog(initialName: '${members.first.name}等人的群聊'),
    );
    if (name == null || !mounted) return;
    final conversation = await widget.controller.createGroup(name, members);
    if (conversation == null || !mounted) return;
    await Navigator.of(context).pushReplacement(
      MaterialPageRoute(
        builder: (_) => ChatScreen(
          controller: widget.controller,
          conversation: conversation,
        ),
      ),
    );
  }
}

class _GroupNameDialog extends StatefulWidget {
  const _GroupNameDialog({required this.initialName});
  final String initialName;

  @override
  State<_GroupNameDialog> createState() => _GroupNameDialogState();
}

class _GroupNameDialogState extends State<_GroupNameDialog> {
  late final TextEditingController controller = TextEditingController(
    text: widget.initialName,
  );

  @override
  void dispose() {
    controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('群聊名称'),
    content: TextField(
      controller: controller,
      autofocus: true,
      maxLength: 40,
      decoration: const InputDecoration(hintText: '输入群聊名称'),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      FilledButton(
        onPressed: () {
          final value = controller.text.trim();
          if (value.isNotEmpty) Navigator.pop(context, value);
        },
        child: const Text('创建'),
      ),
    ],
  );
}

class GroupDetailsScreen extends StatelessWidget {
  const GroupDetailsScreen({
    super.key,
    required this.controller,
    required this.conversation,
  });
  final AppController controller;
  final Conversation conversation;
  @override
  Widget build(BuildContext context) =>
      GroupManagementScreen(controller: controller, conversation: conversation);
}

class ContactDetailsScreen extends StatefulWidget {
  const ContactDetailsScreen({
    super.key,
    required this.controller,
    required this.conversation,
  });
  final AppController controller;
  final Conversation conversation;
  @override
  State<ContactDetailsScreen> createState() => _ContactDetailsScreenState();
}

class _ContactDetailsScreenState extends State<ContactDetailsScreen> {
  @override
  Widget build(BuildContext context) {
    final peer = widget.conversation.members.firstOrNull;
    if (peer == null) {
      return const Scaffold(
        appBar: GlassAppBar(title: Text('联系人资料')),
        body: StatePanel(
          icon: CupertinoIcons.person_crop_circle_badge_exclam,
          title: '联系人资料暂不可用',
          body: '当前会话尚未同步联系人信息，请返回后重试。',
        ),
      );
    }
    return FriendProfileScreen(controller: widget.controller, user: peer);
  }
}
