import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/models.dart';
import '../../core/user_identity.dart';
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
  final TextEditingController queryController = TextEditingController();
  String query = '';
  bool searching = false;
  String? searchError;
  List<AppUser> remoteResults = const [];
  Timer? debounce;
  List<String> recent = const [];
  bool recentFailed = false;
  bool clearingRecent = false;
  UserSearchCapabilities? capabilities;
  bool capabilitiesLoading = true;
  bool capabilitiesFailed = false;
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
    queryController.dispose();
    super.dispose();
  }

  void _search(String value) {
    setState(() => query = value);
    debounce?.cancel();
    final minimumLength = searchBy == 'handle' ? 4 : 5;
    if (value.trim().length < minimumLength || !_searchEnabled) {
      setState(() {
        searching = false;
        searchError = null;
        remoteResults = const [];
      });
      return;
    }
    final requestedBy = searchBy;
    debounce = Timer(const Duration(milliseconds: 280), () async {
      setState(() => searching = true);
      final result = await widget.controller.searchUsers(
        value,
        by: requestedBy,
      );
      if (!mounted || query != value || searchBy != requestedBy) return;
      setState(() {
        searching = false;
        searchError = widget.controller.error;
        remoteResults = result;
      });
    });
  }

  bool get _searchEnabled {
    if (capabilitiesLoading || capabilitiesFailed || capabilities == null) {
      return false;
    }
    return searchBy == 'handle'
        ? capabilities!.allowSearchByHandle
        : capabilities!.allowSearchByPhone;
  }

  Future<void> _loadCapabilities() async {
    if (mounted) {
      setState(() {
        capabilitiesLoading = true;
        capabilitiesFailed = false;
      });
    }
    final value = await widget.controller.loadSearchCapabilities();
    if (!mounted) return;
    if (value == null) {
      setState(() {
        capabilitiesLoading = false;
        capabilitiesFailed = true;
      });
      return;
    }
    setState(() {
      capabilities = value;
      capabilitiesLoading = false;
      capabilitiesFailed = false;
      if (!value.allowSearchByHandle && value.allowSearchByPhone) {
        searchBy = 'phone';
      }
    });
    if (query.trim().isNotEmpty) _search(query);
  }

  Future<void> _loadRecent() async {
    try {
      final values = await widget.settingsStore.readRecentSearches();
      if (!mounted) return;
      setState(() {
        recent = values;
        recentFailed = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        recent = const [];
        recentFailed = true;
      });
    }
  }

  Future<void> _clearRecent() async {
    if (clearingRecent) return;
    setState(() => clearingRecent = true);
    try {
      await widget.settingsStore.clearRecentSearches();
      if (!mounted) return;
      setState(() {
        recent = const [];
        recentFailed = false;
      });
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('最近搜索清除失败，请稍后重试')));
    } finally {
      if (mounted) setState(() => clearingRecent = false);
    }
  }

  Future<void> _rememberQuery() async {
    final value = query.trim();
    if (value.isEmpty) return;
    try {
      await widget.settingsStore.addRecentSearch(value);
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('最近搜索未能保存，不影响继续查看')));
    }
  }

  void _useRecent(String value) {
    queryController
      ..text = value
      ..selection = TextSelection.collapsed(offset: value.length);
    _search(value);
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) => _buildContent(context),
  );

  Widget _buildContent(BuildContext context) {
    final matchingContacts = widget.controller.contacts
        .where(
          (user) => '${user.displayName}${user.name}${user.handle}'
              .toLowerCase()
              .contains(query.toLowerCase()),
        )
        .toList();
    final contacts = <String, AppUser>{
      for (final user in matchingContacts) user.id: user,
      for (final user in remoteResults) user.id: user,
    }.values.toList();
    final conversations = widget.controller.conversations
        .where(
          (item) =>
              '${widget.controller.displayConversationName(item)}${item.title}${item.subtitle}'
                  .toLowerCase()
                  .contains(query.toLowerCase()),
        )
        .toList();
    return Scaffold(
      appBar: const GlassAppBar(title: Text('搜索')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
        children: [
          TextField(
            controller: queryController,
            autofocus: true,
            onChanged: _search,
            decoration: InputDecoration(
              prefixIcon: const Icon(CupertinoIcons.search),
              hintText: capabilitiesLoading || capabilitiesFailed
                  ? '搜索本地联系人或会话'
                  : searchBy == 'handle'
                  ? '搜索联系人、会话或完整呱呱号'
                  : '输入完整手机号',
            ),
          ),
          if (capabilitiesLoading) ...[
            const SizedBox(height: 10),
            const LinearProgressIndicator(minHeight: 2),
            const SizedBox(height: 6),
            Text(
              '正在读取平台搜索权限，本地联系人和会话仍可搜索。',
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ] else if (capabilitiesFailed) ...[
            const SizedBox(height: 10),
            _SearchNotice(
              message: widget.controller.error ?? '平台搜索权限读取失败',
              actionLabel: '重试',
              onAction: _loadCapabilities,
            ),
          ],
          if (capabilities?.allowSearchByHandle == true &&
              capabilities?.allowSearchByPhone == true) ...[
            const SizedBox(height: 12),
            CupertinoSlidingSegmentedControl<String>(
              groupValue: searchBy,
              children: const {
                'handle': Padding(
                  padding: EdgeInsets.symmetric(horizontal: 12),
                  child: Text('呱呱号'),
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
                  : '平台当前已关闭呱呱号搜索。',
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
          if (searchError != null) ...[
            const SizedBox(height: 10),
            _SearchNotice(
              message: searchError!,
              actionLabel: '重试',
              onAction: () => _search(query),
            ),
          ],
          if (query.isEmpty) ...[
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('最近搜索', style: Theme.of(context).textTheme.titleMedium),
                TextButton(
                  key: const Key('clear-recent-searches'),
                  onPressed: recent.isEmpty || clearingRecent
                      ? null
                      : _clearRecent,
                  child: Text(clearingRecent ? '清除中…' : '清除'),
                ),
              ],
            ),
            if (recentFailed)
              _SearchNotice(
                message: '最近搜索读取失败，本地搜索功能不受影响。',
                actionLabel: '重试',
                onAction: _loadRecent,
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
                      onPressed: () => _useRecent(item),
                    ),
                  )
                  .toList(),
            ),
            const SizedBox(height: 32),
            const StatePanel(
              icon: CupertinoIcons.search_circle,
              title: '搜索联系人和会话',
              body: '输入昵称、关键词或完整呱呱号开始查找。',
            ),
          ] else if (contacts.isEmpty && conversations.isEmpty)
            const StatePanel(
              icon: CupertinoIcons.search,
              title: '没有找到结果',
              body: '换个名字、呱呱号或关键词试试。',
            )
          else ...[
            if (contacts.isNotEmpty) ...[
              Text('联系人', style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 8),
              ...contacts.map(
                (user) => ListTile(
                  contentPadding: EdgeInsets.zero,
                  leading: PersonAvatar(
                    name: widget.controller.displayNameFor(user),
                    avatarUrl: user.avatarUrl,
                  ),
                  title: Text(widget.controller.displayNameFor(user)),
                  subtitle: Text(publicUserHandleLabel(user.handle)),
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
                  leading: PersonAvatar(
                    name: widget.controller.displayConversationName(item),
                  ),
                  title: Text(widget.controller.displayConversationName(item)),
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
      chatScreenRoute(
        context,
        controller: widget.controller,
        conversation: conversation,
      ),
    );
  }
}

class _SearchNotice extends StatelessWidget {
  const _SearchNotice({
    required this.message,
    required this.actionLabel,
    required this.onAction,
  });

  final String message;
  final String actionLabel;
  final VoidCallback onAction;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    return Container(
      padding: const EdgeInsets.fromLTRB(12, 9, 8, 9),
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
          TextButton(onPressed: onAction, child: Text(actionLabel)),
        ],
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
      final loadError = widget.controller.friendRequestsLoadError;
      return Scaffold(
        appBar: const GlassAppBar(title: Text('新的朋友')),
        body: requests.isEmpty && loadError != null
            ? StatePanel(
                icon: CupertinoIcons.exclamationmark_circle,
                title: '好友申请暂时无法加载',
                body: loadError,
                actionLabel: '重新加载',
                onAction: widget.controller.refreshFriendRequests,
              )
            : requests.isEmpty
            ? const StatePanel(
                icon: CupertinoIcons.person_add,
                title: '暂无好友申请',
                body: '通过呱呱号、二维码、群聊或名片发起的申请会显示在这里。',
              )
            : Column(
                children: [
                  if (loadError != null)
                    Padding(
                      padding: const EdgeInsets.fromLTRB(16, 12, 16, 0),
                      child: _SearchNotice(
                        message: '$loadError，当前显示上次同步的申请。',
                        actionLabel: '重试',
                        onAction: widget.controller.refreshFriendRequests,
                      ),
                    ),
                  Expanded(
                    child: ListView.separated(
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
                  ),
                ],
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
    final message = success
        ? switch (action) {
            'accept' => '已添加 ${request.user.name} 为好友',
            'reject' => '已拒绝 ${request.user.name} 的申请',
            _ => '好友申请已撤回',
          }
        : widget.controller.error ?? '操作失败';
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(message)));
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
      final loadError = widget.controller.groupInvitationsLoadError;
      return Scaffold(
        appBar: const GlassAppBar(title: Text('群聊邀请')),
        body: invitations.isEmpty && widget.controller.loading
            ? const StatePanel(
                icon: CupertinoIcons.person_2,
                title: '正在同步群聊邀请',
                body: '请稍候，最新邀请会自动显示。',
                loading: true,
              )
            : invitations.isEmpty && loadError != null
            ? StatePanel(
                icon: CupertinoIcons.exclamationmark_circle,
                title: '群聊邀请暂时无法加载',
                body: loadError,
                actionLabel: '重新加载',
                onAction: widget.controller.refreshGroupInvitations,
              )
            : invitations.isEmpty
            ? const StatePanel(
                icon: CupertinoIcons.person_2,
                title: '暂无群聊邀请',
                body: '好友邀请你加入群聊后，会在这里显示并由你确认。',
              )
            : Column(
                children: [
                  if (loadError != null)
                    Padding(
                      padding: const EdgeInsets.fromLTRB(16, 12, 16, 0),
                      child: _SearchNotice(
                        message: '$loadError，当前显示上次同步的邀请。',
                        actionLabel: '重试',
                        onAction: widget.controller.refreshGroupInvitations,
                      ),
                    ),
                  Expanded(
                    child: ListView.separated(
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
                                PersonAvatar(
                                  name: invitation.groupName,
                                  size: 50,
                                ),
                                const SizedBox(width: 12),
                                Expanded(
                                  child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
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
                                        style: Theme.of(
                                          context,
                                        ).textTheme.bodySmall,
                                      ),
                                      const SizedBox(height: 8),
                                      if (handling.contains(invitation.id))
                                        const CupertinoActivityIndicator(
                                          radius: 9,
                                        )
                                      else if (invitation.pending &&
                                          !invitation.outgoing)
                                        Wrap(
                                          spacing: 8,
                                          children: [
                                            OutlinedButton(
                                              onPressed: () => _respond(
                                                invitation,
                                                'reject',
                                              ),
                                              child: const Text('拒绝'),
                                            ),
                                            FilledButton(
                                              onPressed: () => _respond(
                                                invitation,
                                                'accept',
                                              ),
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
                  ),
                ],
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
  final searchController = TextEditingController();
  String query = '';
  bool creating = false;

  @override
  void dispose() {
    searchController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) => _buildContent(context),
  );

  Widget _buildContent(BuildContext context) {
    final normalizedQuery = query.trim().toLowerCase();
    final contacts = widget.controller.contacts.where((user) {
      if (normalizedQuery.isEmpty) return true;
      return user.name.toLowerCase().contains(normalizedQuery) ||
          user.remark.toLowerCase().contains(normalizedQuery) ||
          user.handle.toLowerCase().contains(normalizedQuery) ||
          user.id.toLowerCase().contains(normalizedQuery);
    }).toList();
    return Scaffold(
      appBar: GlassAppBar(
        title: const Text('创建群聊'),
        actions: [
          Padding(
            padding: const EdgeInsets.only(right: 12),
            child: TextButton(
              key: const Key('create-group-submit'),
              onPressed: selected.isEmpty || creating ? null : _create,
              child: creating
                  ? const SizedBox.square(
                      dimension: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : Text('完成 ${selected.length}'),
            ),
          ),
        ],
      ),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
            child: TextField(
              key: const Key('create-group-search'),
              controller: searchController,
              textInputAction: TextInputAction.search,
              onChanged: (value) => setState(() => query = value),
              decoration: InputDecoration(
                prefixIcon: const Icon(CupertinoIcons.search),
                hintText: '搜索昵称、备注或呱呱号',
                suffixIcon: query.isEmpty
                    ? null
                    : IconButton(
                        tooltip: '清除搜索',
                        onPressed: () {
                          searchController.clear();
                          setState(() => query = '');
                        },
                        icon: const Icon(CupertinoIcons.clear_circled_solid),
                      ),
              ),
            ),
          ),
          if (selected.isNotEmpty)
            SizedBox(
              height: 76,
              child: ListView(
                scrollDirection: Axis.horizontal,
                padding: const EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: 8,
                ),
                children: widget.controller.contacts
                    .where((user) => selected.contains(user.id))
                    .map(
                      (user) => Padding(
                        padding: const EdgeInsets.only(right: 10),
                        child: Tooltip(
                          message: '移除 ${user.displayName}',
                          child: InkWell(
                            key: Key('create-group-selected-${user.id}'),
                            borderRadius: BorderRadius.circular(28),
                            onTap: creating
                                ? null
                                : () =>
                                      setState(() => selected.remove(user.id)),
                            child: Stack(
                              clipBehavior: Clip.none,
                              children: [
                                PersonAvatar(
                                  name: user.displayName,
                                  size: 48,
                                  avatarUrl: user.avatarUrl,
                                ),
                                const Positioned(
                                  right: -3,
                                  top: -3,
                                  child: Icon(
                                    CupertinoIcons.minus_circle_fill,
                                    size: 18,
                                    color: LinliColors.systemRed,
                                  ),
                                ),
                              ],
                            ),
                          ),
                        ),
                      ),
                    )
                    .toList(),
              ),
            ),
          Expanded(
            child: widget.controller.contacts.isEmpty
                ? const StatePanel(
                    icon: CupertinoIcons.person_2,
                    title: '还没有可邀请的联系人',
                    body: '请先添加好友，再回来创建群聊。',
                  )
                : contacts.isEmpty
                ? StatePanel(
                    icon: CupertinoIcons.search,
                    title: '没有匹配的联系人',
                    body: '没有找到“${query.trim()}”，请检查昵称、备注或呱呱号。',
                    actionLabel: '清除搜索',
                    onAction: () {
                      searchController.clear();
                      setState(() => query = '');
                    },
                  )
                : ListView.builder(
                    keyboardDismissBehavior:
                        ScrollViewKeyboardDismissBehavior.onDrag,
                    itemCount: contacts.length,
                    itemBuilder: (context, index) {
                      final user = contacts[index];
                      final isSelected = selected.contains(user.id);
                      final publicIdentity = publicUserHandleLabel(user.handle);
                      return ListTile(
                        key: Key('create-group-contact-${user.id}'),
                        minTileHeight: 64,
                        enabled: !creating,
                        contentPadding: const EdgeInsets.symmetric(
                          horizontal: 16,
                        ),
                        leading: PersonAvatar(
                          name: user.displayName,
                          avatarUrl: user.avatarUrl,
                        ),
                        title: Text(user.displayName),
                        subtitle: Text(
                          user.remark.isEmpty
                              ? publicIdentity
                              : '${user.name} · $publicIdentity',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                        trailing: CupertinoCheckbox(
                          value: isSelected,
                          onChanged: creating
                              ? null
                              : (value) => setState(() {
                                  if (value == true) {
                                    selected.add(user.id);
                                  } else {
                                    selected.remove(user.id);
                                  }
                                }),
                        ),
                        onTap: creating
                            ? null
                            : () => setState(
                                () => isSelected
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
  }

  Future<void> _create() async {
    if (creating) return;
    final members = widget.controller.contacts
        .where((user) => selected.contains(user.id))
        .toList();
    if (members.isEmpty) return;
    final name = await showDialog<String>(
      context: context,
      builder: (dialogContext) =>
          _GroupNameDialog(initialName: '${members.first.name}等人的群聊'),
    );
    if (name == null || !mounted) return;
    setState(() => creating = true);
    widget.controller.clearError();
    final conversation = await widget.controller.createGroup(name, members);
    if (!mounted) return;
    setState(() => creating = false);
    if (conversation == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '群聊创建失败，请稍后重试')),
      );
      return;
    }
    await Navigator.of(context).pushReplacement(
      chatScreenRoute(
        context,
        controller: widget.controller,
        conversation: conversation,
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

class SavedGroupsScreen extends StatelessWidget {
  const SavedGroupsScreen({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final groups = controller.conversations
        .where(
          (conversation) =>
              conversation.kind == ConversationKind.group &&
              !conversation.isBusinessChannel &&
              conversation.saved,
        )
        .toList();
    return Scaffold(
      appBar: const GlassAppBar(title: Text('保存的群聊')),
      body: groups.isEmpty
          ? const StatePanel(
              icon: CupertinoIcons.book,
              title: '还没有保存群聊',
              body: '进入群聊资料，打开“保存到通讯录”后会显示在这里。',
            )
          : ListView.separated(
              key: const Key('saved-groups-list'),
              padding: const EdgeInsets.symmetric(vertical: 8),
              itemCount: groups.length,
              separatorBuilder: (_, _) => const Divider(indent: 80),
              itemBuilder: (context, index) {
                final group = groups[index];
                return ListTile(
                  key: ValueKey('saved-group-${group.id}'),
                  minTileHeight: 72,
                  contentPadding: const EdgeInsets.symmetric(horizontal: 16),
                  leading: PersonAvatar(
                    name: group.title,
                    avatarUrl:
                        group.avatarUrl ?? 'assets/brand/qingwaguagua-icon.png',
                    size: 50,
                  ),
                  title: Text(group.title),
                  subtitle: Text(
                    '${group.memberCount} 位成员${group.muted ? ' · 已免打扰' : ''}',
                  ),
                  trailing: const Icon(
                    CupertinoIcons.chevron_forward,
                    size: 17,
                  ),
                  onTap: () {
                    controller.markRead(group.id);
                    Navigator.of(context).push(
                      chatScreenRoute(
                        context,
                        controller: controller,
                        conversation: group,
                      ),
                    );
                  },
                );
              },
            ),
    );
  }
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
    final peer = widget.conversation.directPeerFor(
      widget.controller.currentUser?.id,
    );
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
