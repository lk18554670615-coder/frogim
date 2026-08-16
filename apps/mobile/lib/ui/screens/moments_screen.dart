import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../core/app_controller.dart';
import '../../core/app_config.dart';
import '../../core/image_send_editor.dart';
import '../../core/models.dart';
import '../../im/business_features.dart';
import '../widgets/linli_widgets.dart';
import '../widgets/media_send_widgets.dart';
import 'settings_screens.dart';

Future<MomentSummary?> showMomentPicker(
  BuildContext context,
  AppController controller,
) => showModalBottomSheet<MomentSummary>(
  context: context,
  isScrollControlled: true,
  useSafeArea: true,
  showDragHandle: true,
  builder: (_) => FractionallySizedBox(
    heightFactor: .74,
    child: _MomentPicker(controller: controller),
  ),
);

class MomentsScreen extends StatefulWidget {
  const MomentsScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<MomentsScreen> createState() => _MomentsScreenState();
}

class _MomentsScreenState extends State<MomentsScreen> {
  bool loading = true;
  bool loadingMore = false;
  String error = '';
  String nextCursor = '';
  List<MomentSummary> items = const [];

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load({bool more = false}) async {
    if (more && (loadingMore || nextCursor.isEmpty)) return;
    setState(() {
      if (more) {
        loadingMore = true;
      } else {
        loading = true;
        error = '';
      }
    });
    try {
      final page = await widget.controller.loadMoments(
        cursor: more ? nextCursor : '',
      );
      if (!mounted) return;
      setState(() {
        items = more ? [...items, ...page.items] : page.items;
        nextCursor = page.nextCursor;
      });
    } catch (cause) {
      if (mounted) setState(() => error = cause.toString());
    } finally {
      if (mounted) {
        setState(() {
          loading = false;
          loadingMore = false;
        });
      }
    }
  }

  void _replace(MomentSummary moment) {
    final index = items.indexWhere((item) => item.id == moment.id);
    if (index < 0) return;
    setState(() {
      final updated = [...items];
      updated[index] = moment;
      items = updated;
    });
  }

  Future<void> _publish() async {
    final created = await Navigator.of(context).push<MomentSummary>(
      MaterialPageRoute(
        builder: (_) => MomentComposerScreen(controller: widget.controller),
      ),
    );
    if (created != null && mounted) setState(() => items = [created, ...items]);
  }

  Future<void> _toggleLike(MomentSummary moment) async {
    try {
      _replace(await widget.controller.toggleMomentLike(moment));
    } catch (cause) {
      _snack(cause.toString());
    }
  }

  Future<void> _comments(MomentSummary moment) async {
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      showDragHandle: true,
      builder: (_) => _MomentCommentsSheet(
        controller: widget.controller,
        moment: moment,
        onChanged: () => _load(),
      ),
    );
  }

  Future<void> _share(MomentSummary moment) async {
    final conversation = await _pickConversation();
    if (conversation == null) return;
    final sent = await widget.controller.sendMomentShare(
      conversation.id,
      moment,
    );
    if (sent.status == MessageStatus.failed) {
      _snack('朋友圈分享发送失败');
    } else {
      _snack('已分享到「${conversation.title}」');
    }
  }

  Future<Conversation?> _pickConversation() =>
      showModalBottomSheet<Conversation>(
        context: context,
        useSafeArea: true,
        showDragHandle: true,
        builder: (context) => SafeArea(
          child: ListView(
            shrinkWrap: true,
            children: [
              const ListTile(title: Text('分享到聊天')),
              for (final conversation
                  in widget.controller.conversations
                      .where((item) => !item.archived)
                      .take(50))
                ListTile(
                  leading: PersonAvatar(
                    name: conversation.title,
                    avatarUrl: conversation.avatarUrl,
                    size: 42,
                  ),
                  title: Text(conversation.title),
                  subtitle: Text(conversation.subtitle, maxLines: 1),
                  onTap: () => Navigator.pop(context, conversation),
                ),
            ],
          ),
        ),
      );

  Future<void> _delete(MomentSummary moment) async {
    final confirmed = await showCupertinoDialog<bool>(
      context: context,
      builder: (context) => CupertinoAlertDialog(
        title: const Text('删除这条朋友圈？'),
        content: const Text('删除后无法恢复，相关点赞、评论和提醒也会移除。'),
        actions: [
          CupertinoDialogAction(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          CupertinoDialogAction(
            isDestructiveAction: true,
            onPressed: () => Navigator.pop(context, true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await widget.controller.removeMoment(moment);
      if (mounted) {
        setState(
          () => items = items.where((item) => item.id != moment.id).toList(),
        );
      }
    } catch (cause) {
      _snack(cause.toString());
    }
  }

  Future<void> _reminders() async {
    await showModalBottomSheet<void>(
      context: context,
      useSafeArea: true,
      showDragHandle: true,
      builder: (_) => _MomentRemindersSheet(controller: widget.controller),
    );
  }

  void _snack(String value) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(value.replaceFirst('BusinessApiException: ', ''))),
    );
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: const Text('朋友圈'),
      actions: [
        IconButton(
          tooltip: '互动提醒',
          onPressed: _reminders,
          icon: const Icon(CupertinoIcons.bell),
        ),
        IconButton(
          key: const Key('publish-moment-button'),
          tooltip: '发布朋友圈',
          onPressed: _publish,
          icon: const Icon(CupertinoIcons.camera),
        ),
      ],
    ),
    body: loading
        ? const Center(child: CircularProgressIndicator())
        : error.isNotEmpty
        ? MomentsErrorState(onRefresh: _load)
        : items.isEmpty
        ? MomentsEmptyState(onRefresh: _load, onPublish: _publish)
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView.builder(
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.fromLTRB(12, 10, 12, 28),
              itemCount: items.length + (nextCursor.isEmpty ? 0 : 1),
              itemBuilder: (context, index) {
                if (index == items.length) {
                  return Padding(
                    padding: const EdgeInsets.all(16),
                    child: Center(
                      child: loadingMore
                          ? const CircularProgressIndicator()
                          : OutlinedButton(
                              onPressed: () => _load(more: true),
                              child: const Text('加载更多'),
                            ),
                    ),
                  );
                }
                final moment = items[index];
                return _MomentCard(
                  moment: moment,
                  currentUserId: widget.controller.currentUser?.id ?? '',
                  onLike: () => _toggleLike(moment),
                  onComment: () => _comments(moment),
                  onShare: () => _share(moment),
                  onDelete: () => _delete(moment),
                  onReport: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => ReportScreen(
                        controller: widget.controller,
                        target: '${moment.authorName} 的朋友圈',
                        targetId: moment.id,
                        targetType: 'moment',
                      ),
                    ),
                  ),
                );
              },
            ),
          ),
  );
}

class MomentsErrorState extends StatelessWidget {
  const MomentsErrorState({super.key, required this.onRefresh});

  final Future<void> Function() onRefresh;

  @override
  Widget build(BuildContext context) => RefreshIndicator(
    onRefresh: onRefresh,
    child: CustomScrollView(
      physics: const AlwaysScrollableScrollPhysics(),
      slivers: [
        SliverFillRemaining(
          hasScrollBody: false,
          child: StatePanel(
            icon: CupertinoIcons.exclamationmark_circle,
            title: '朋友圈暂时无法加载',
            body: '请检查网络连接后重试。已经发布的内容仍保存在服务器。',
            actionLabel: '重新加载',
            onAction: onRefresh,
          ),
        ),
      ],
    ),
  );
}

class MomentsEmptyState extends StatelessWidget {
  const MomentsEmptyState({
    super.key,
    required this.onRefresh,
    required this.onPublish,
  });

  final Future<void> Function() onRefresh;
  final VoidCallback onPublish;

  @override
  Widget build(BuildContext context) => RefreshIndicator(
    onRefresh: onRefresh,
    child: CustomScrollView(
      physics: const AlwaysScrollableScrollPhysics(),
      slivers: [
        SliverFillRemaining(
          hasScrollBody: false,
          child: StatePanel(
            icon: CupertinoIcons.person_2_square_stack,
            title: '还没有朋友圈动态',
            body: '发布此刻的想法、照片或视频，好友的互动也会显示在这里。',
            actionLabel: '发布第一条动态',
            onAction: onPublish,
          ),
        ),
      ],
    ),
  );
}

class _MomentCard extends StatelessWidget {
  const _MomentCard({
    required this.moment,
    required this.currentUserId,
    required this.onLike,
    required this.onComment,
    required this.onShare,
    required this.onDelete,
    required this.onReport,
  });

  final MomentSummary moment;
  final String currentUserId;
  final VoidCallback onLike;
  final VoidCallback onComment;
  final VoidCallback onShare;
  final VoidCallback onDelete;
  final VoidCallback onReport;

  @override
  Widget build(BuildContext context) => Card(
    margin: const EdgeInsets.only(bottom: 12),
    child: Padding(
      padding: const EdgeInsets.fromLTRB(14, 14, 14, 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              PersonAvatar(
                name: moment.authorName,
                avatarUrl: moment.authorAvatarUrl,
                size: 44,
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      moment.authorName,
                      style: Theme.of(context).textTheme.titleSmall,
                    ),
                    Text(
                      '${_relativeTime(moment.createdAt)} · ${_visibilityLabel(moment.visibility)}',
                      style: Theme.of(context).textTheme.bodySmall,
                    ),
                  ],
                ),
              ),
              PopupMenuButton<String>(
                onSelected: (value) =>
                    value == 'delete' ? onDelete() : onReport(),
                itemBuilder: (_) => [
                  if (moment.authorId == currentUserId)
                    const PopupMenuItem(value: 'delete', child: Text('删除')),
                  if (moment.authorId != currentUserId)
                    const PopupMenuItem(value: 'report', child: Text('举报')),
                ],
              ),
            ],
          ),
          if (moment.content.isNotEmpty) ...[
            const SizedBox(height: 10),
            Text(moment.content, style: Theme.of(context).textTheme.bodyLarge),
          ],
          if (moment.media.isNotEmpty) ...[
            const SizedBox(height: 10),
            _MomentMediaGrid(moment: moment),
          ],
          if ((moment.location['name'] as String? ?? '').isNotEmpty) ...[
            const SizedBox(height: 8),
            Row(
              children: [
                const Icon(CupertinoIcons.location, size: 14),
                const SizedBox(width: 4),
                Text(
                  moment.location['name']! as String,
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ],
            ),
          ],
          const SizedBox(height: 6),
          Row(
            children: [
              TextButton.icon(
                onPressed: onLike,
                icon: Icon(
                  moment.likedByMe
                      ? CupertinoIcons.heart_fill
                      : CupertinoIcons.heart,
                  size: 18,
                ),
                label: Text('${moment.likeCount}'),
              ),
              TextButton.icon(
                onPressed: onComment,
                icon: const Icon(CupertinoIcons.chat_bubble, size: 18),
                label: Text('${moment.commentCount}'),
              ),
              const Spacer(),
              IconButton(
                tooltip: '分享到聊天',
                onPressed: onShare,
                icon: const Icon(CupertinoIcons.share, size: 20),
              ),
            ],
          ),
          if (moment.comments.isNotEmpty)
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(9),
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.surfaceContainerHigh,
                borderRadius: BorderRadius.circular(10),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  for (final comment in moment.comments.take(3))
                    Padding(
                      padding: const EdgeInsets.symmetric(vertical: 2),
                      child: Text.rich(
                        TextSpan(
                          children: [
                            TextSpan(
                              text: comment.authorName,
                              style: const TextStyle(
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                            if (comment.replyToName.isNotEmpty)
                              TextSpan(text: ' 回复 ${comment.replyToName}'),
                            TextSpan(text: '：${comment.content}'),
                          ],
                        ),
                      ),
                    ),
                  if (moment.comments.length > 3)
                    Text('查看全部 ${moment.comments.length} 条评论'),
                ],
              ),
            ),
        ],
      ),
    ),
  );
}

class _MomentMediaGrid extends StatelessWidget {
  const _MomentMediaGrid({required this.moment});

  final MomentSummary moment;

  @override
  Widget build(BuildContext context) {
    if (moment.mediaKind == 'video') {
      return Semantics(
        button: true,
        label: '播放朋友圈视频',
        child: InkWell(
          onTap: () async {
            final uri = Uri.tryParse(moment.media.first.url);
            if (uri == null || !await launchUrl(uri)) {
              if (context.mounted) {
                ScaffoldMessenger.of(
                  context,
                ).showSnackBar(const SnackBar(content: Text('视频暂时无法打开')));
              }
            }
          },
          borderRadius: BorderRadius.circular(10),
          child: AspectRatio(
            aspectRatio: 16 / 9,
            child: DecoratedBox(
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.surfaceContainerHigh,
                borderRadius: BorderRadius.circular(10),
              ),
              child: const Column(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Icon(CupertinoIcons.play_circle_fill, size: 54),
                  SizedBox(height: 8),
                  Text('点击播放视频'),
                ],
              ),
            ),
          ),
        ),
      );
    }
    final columns = moment.media.length == 1
        ? 1
        : moment.media.length == 2
        ? 2
        : 3;
    return GridView.builder(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: columns,
        mainAxisSpacing: 4,
        crossAxisSpacing: 4,
      ),
      itemCount: moment.media.length,
      itemBuilder: (_, index) => ClipRRect(
        borderRadius: BorderRadius.circular(8),
        child: _MomentNetworkImage(url: moment.media[index].url),
      ),
    );
  }
}

class _MomentNetworkImage extends StatelessWidget {
  const _MomentNetworkImage({required this.url});
  final String url;

  @override
  Widget build(BuildContext context) => LinliNetworkImage(
    url: url,
    cacheKey: url,
    fit: BoxFit.cover,
    placeholderBuilder: (context) =>
        ColoredBox(color: Theme.of(context).colorScheme.surfaceContainerHigh),
    errorBuilder: (context) => ColoredBox(
      color: Theme.of(context).colorScheme.surfaceContainerHigh,
      child: const Center(child: Icon(CupertinoIcons.photo)),
    ),
  );
}

class MomentComposerScreen extends StatefulWidget {
  const MomentComposerScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<MomentComposerScreen> createState() => _MomentComposerScreenState();
}

class _MomentComposerScreenState extends State<MomentComposerScreen> {
  final content = TextEditingController();
  final picker = ImagePicker();
  final uploads = <MediaUpload>[];
  String visibility = 'public';
  final selectedUsers = <String>{};
  bool publishing = false;
  double progress = 0;

  @override
  void dispose() {
    content.dispose();
    super.dispose();
  }

  Future<void> _pickImages() async {
    final files = await picker.pickMultiImage(imageQuality: 88, maxWidth: 2400);
    if (files.isEmpty) return;
    final next = <MediaUpload>[];
    for (final file in files.take(9)) {
      final bytes = await file.readAsBytes();
      _validateMediaSize(bytes.length);
      next.add(
        MediaUpload(
          bytes: bytes,
          fileName: file.name,
          mimeType: _imageMime(file.name),
          kind: MessageContentKind.image,
          localPath: file.path,
        ),
      );
    }
    if (!mounted) return;
    setState(() {
      uploads
        ..clear()
        ..addAll(next);
    });
  }

  Future<void> _editImage(int index) async {
    final current = uploads[index];
    final bytes = await editImageBeforeSending(context, current.bytes);
    if (!mounted || bytes == null) return;
    try {
      _validateMediaSize(bytes.length);
      final original = current.fileName;
      final dot = original.lastIndexOf('.');
      final base = dot > 0 ? original.substring(0, dot) : original;
      setState(() {
        uploads[index] = MediaUpload(
          bytes: bytes,
          fileName: '${base.isEmpty ? 'moment-image' : base}-edited.jpg',
          mimeType: 'image/jpeg',
          kind: MessageContentKind.image,
        );
      });
    } catch (error) {
      _showMediaError(error);
    }
  }

  Future<void> _pickVideo() async {
    try {
      final source = await chooseVideoSource(context);
      if (!mounted || source == null) return;
      final file = await picker.pickVideo(
        source: source,
        maxDuration: const Duration(minutes: 5),
      );
      if (!mounted || file == null) return;
      final preview = await showVideoSendPreview(
        context,
        source: file.path,
        title: file.name,
      );
      if (!mounted || preview == null) return;
      final upload = await prepareVideoUploadWithDialog(
        context,
        file: file,
        maxBytes: AppConfig.mediaMaxBytes,
        previewDurationSeconds: preview.durationSeconds,
      );
      if (!mounted || upload == null) return;
      setState(() {
        uploads
          ..clear()
          ..add(upload);
      });
    } catch (error) {
      _showMediaError(error);
    }
  }

  void _validateMediaSize(int bytes) {
    if (bytes <= AppConfig.mediaMaxBytes) return;
    final maxMB = AppConfig.mediaMaxBytes ~/ (1024 * 1024);
    throw FormatException('文件不能超过 $maxMB MB');
  }

  void _showMediaError(Object error) {
    if (!mounted) return;
    final message = error.toString().replaceFirst('FormatException: ', '');
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _chooseVisibleUsers() async {
    final result = await showModalBottomSheet<Set<String>>(
      context: context,
      useSafeArea: true,
      showDragHandle: true,
      builder: (_) => _VisibleUsersPicker(
        contacts: widget.controller.contacts,
        selected: selectedUsers,
      ),
    );
    if (result != null && mounted) {
      setState(() {
        selectedUsers
          ..clear()
          ..addAll(result);
      });
    }
  }

  Future<void> _publish() async {
    if (content.text.trim().isEmpty && uploads.isEmpty) return;
    if ((visibility == 'selected' || visibility == 'excluded') &&
        selectedUsers.isEmpty) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('请至少选择一位联系人')));
      return;
    }
    setState(() {
      publishing = true;
      progress = 0;
    });
    try {
      final created = await widget.controller.publishMoment(
        content: content.text,
        uploads: uploads,
        visibility: visibility,
        visibleUserIds: selectedUsers.toList(),
        onProgress: (index, value) {
          if (!mounted || uploads.isEmpty) return;
          setState(() => progress = (index + value) / uploads.length);
        },
      );
      if (mounted) Navigator.pop(context, created);
    } catch (cause) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            cause.toString().replaceFirst('BusinessApiException: ', ''),
          ),
        ),
      );
    } finally {
      if (mounted) setState(() => publishing = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: const Text('发布朋友圈'),
      actions: [
        Padding(
          padding: const EdgeInsets.only(right: 10),
          child: FilledButton(
            key: const Key('confirm-publish-moment'),
            onPressed: publishing ? null : _publish,
            child: const Text('发布'),
          ),
        ),
      ],
    ),
    body: ListView(
      padding: const EdgeInsets.all(16),
      children: [
        TextField(
          controller: content,
          minLines: 5,
          maxLines: 10,
          maxLength: 5000,
          decoration: const InputDecoration(
            hintText: '分享此刻的想法…',
            border: InputBorder.none,
          ),
        ),
        if (uploads.isNotEmpty)
          GridView.builder(
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
              crossAxisCount: 3,
              mainAxisSpacing: 6,
              crossAxisSpacing: 6,
            ),
            itemCount: uploads.length,
            itemBuilder: (_, index) => Stack(
              fit: StackFit.expand,
              children: [
                ClipRRect(
                  borderRadius: BorderRadius.circular(10),
                  child: uploads[index].kind == MessageContentKind.video
                      ? InkWell(
                          onTap: uploads[index].localPath == null
                              ? null
                              : () => showMessageVideo(
                                  context,
                                  source: uploads[index].localPath!,
                                  title: uploads[index].fileName,
                                ),
                          child: ColoredBox(
                            color: Theme.of(
                              context,
                            ).colorScheme.surfaceContainerHigh,
                            child: const Icon(
                              CupertinoIcons.play_circle_fill,
                              size: 40,
                            ),
                          ),
                        )
                      : Image.memory(uploads[index].bytes, fit: BoxFit.cover),
                ),
                if (uploads[index].kind == MessageContentKind.image)
                  Positioned(
                    left: 2,
                    bottom: 2,
                    child: IconButton.filledTonal(
                      key: Key('edit-moment-image-$index'),
                      tooltip: '编辑图片',
                      onPressed: publishing ? null : () => _editImage(index),
                      icon: const Icon(CupertinoIcons.pencil, size: 15),
                      constraints: const BoxConstraints.tightFor(
                        width: 30,
                        height: 30,
                      ),
                      padding: EdgeInsets.zero,
                    ),
                  ),
                Positioned(
                  right: 2,
                  top: 2,
                  child: IconButton.filledTonal(
                    onPressed: () => setState(() => uploads.removeAt(index)),
                    icon: const Icon(CupertinoIcons.xmark, size: 15),
                    constraints: const BoxConstraints.tightFor(
                      width: 30,
                      height: 30,
                    ),
                    padding: EdgeInsets.zero,
                  ),
                ),
              ],
            ),
          ),
        const SizedBox(height: 12),
        Row(
          children: [
            OutlinedButton.icon(
              onPressed: publishing ? null : _pickImages,
              icon: const Icon(CupertinoIcons.photo_on_rectangle),
              label: const Text('图片'),
            ),
            const SizedBox(width: 10),
            OutlinedButton.icon(
              onPressed: publishing ? null : _pickVideo,
              icon: const Icon(CupertinoIcons.video_camera),
              label: const Text('视频'),
            ),
          ],
        ),
        const SectionHeader('谁可以看'),
        SectionCard(
          children: [
            DropdownButtonFormField<String>(
              initialValue: visibility,
              decoration: const InputDecoration(
                prefixIcon: Icon(CupertinoIcons.eye),
                border: InputBorder.none,
                contentPadding: EdgeInsets.symmetric(
                  horizontal: 12,
                  vertical: 10,
                ),
              ),
              items: const [
                DropdownMenuItem(value: 'public', child: Text('公开')),
                DropdownMenuItem(value: 'friends', child: Text('仅好友')),
                DropdownMenuItem(value: 'private', child: Text('仅自己')),
                DropdownMenuItem(value: 'selected', child: Text('部分可见')),
                DropdownMenuItem(value: 'excluded', child: Text('不给谁看')),
              ],
              onChanged: publishing
                  ? null
                  : (value) => setState(() => visibility = value ?? 'public'),
            ),
            if (visibility == 'selected' || visibility == 'excluded')
              ListTile(
                leading: const Icon(CupertinoIcons.person_2),
                title: const Text('选择联系人'),
                subtitle: Text('已选择 ${selectedUsers.length} 人'),
                trailing: const Icon(CupertinoIcons.chevron_forward),
                onTap: _chooseVisibleUsers,
              ),
          ],
        ),
        if (publishing) ...[
          const SizedBox(height: 20),
          LinearProgressIndicator(value: uploads.isEmpty ? null : progress),
          const SizedBox(height: 8),
          const Text('正在安全上传并发布…', textAlign: TextAlign.center),
        ],
      ],
    ),
  );
}

class _VisibleUsersPicker extends StatefulWidget {
  const _VisibleUsersPicker({required this.contacts, required this.selected});
  final List<AppUser> contacts;
  final Set<String> selected;

  @override
  State<_VisibleUsersPicker> createState() => _VisibleUsersPickerState();
}

class _VisibleUsersPickerState extends State<_VisibleUsersPicker> {
  late final Set<String> selected = {...widget.selected};

  @override
  Widget build(BuildContext context) => SafeArea(
    child: Column(
      children: [
        ListTile(
          title: const Text('选择联系人'),
          trailing: FilledButton(
            onPressed: () => Navigator.pop(context, selected),
            child: const Text('完成'),
          ),
        ),
        Expanded(
          child: ListView.builder(
            itemCount: widget.contacts.length,
            itemBuilder: (_, index) {
              final user = widget.contacts[index];
              return CheckboxListTile(
                value: selected.contains(user.id),
                secondary: PersonAvatar(
                  name: user.name,
                  avatarUrl: user.avatarUrl,
                  size: 40,
                ),
                title: Text(user.remark.isEmpty ? user.name : user.remark),
                onChanged: (value) => setState(() {
                  if (value == true) {
                    selected.add(user.id);
                  } else {
                    selected.remove(user.id);
                  }
                }),
              );
            },
          ),
        ),
      ],
    ),
  );
}

class _MomentCommentsSheet extends StatefulWidget {
  const _MomentCommentsSheet({
    required this.controller,
    required this.moment,
    required this.onChanged,
  });
  final AppController controller;
  final MomentSummary moment;
  final VoidCallback onChanged;

  @override
  State<_MomentCommentsSheet> createState() => _MomentCommentsSheetState();
}

class _MomentCommentsSheetState extends State<_MomentCommentsSheet> {
  final text = TextEditingController();
  final commentFocus = FocusNode();
  late List<MomentCommentSummary> comments = [...widget.moment.comments];
  MomentCommentSummary? replyingTo;
  bool sending = false;

  @override
  void dispose() {
    text.dispose();
    commentFocus.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    if (text.text.trim().isEmpty || sending) return;
    setState(() => sending = true);
    try {
      final created = await widget.controller.commentMoment(
        widget.moment,
        text.text,
        parentId: replyingTo?.id ?? '',
      );
      text.clear();
      if (mounted) {
        setState(() {
          comments = [...comments, created];
          replyingTo = null;
        });
      }
      widget.onChanged();
    } catch (cause) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              cause.toString().replaceFirst('BusinessApiException: ', ''),
            ),
          ),
        );
      }
    } finally {
      if (mounted) setState(() => sending = false);
    }
  }

  Future<void> _delete(MomentCommentSummary comment) async {
    final confirmed = await showCupertinoDialog<bool>(
      context: context,
      builder: (context) => CupertinoAlertDialog(
        title: const Text('删除这条评论？'),
        actions: [
          CupertinoDialogAction(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          CupertinoDialogAction(
            isDestructiveAction: true,
            onPressed: () => Navigator.pop(context, true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await widget.controller.removeMomentComment(widget.moment, comment);
      if (!mounted) return;
      setState(() {
        comments = comments.where((item) => item.id != comment.id).toList();
        if (replyingTo?.id == comment.id) replyingTo = null;
      });
      widget.onChanged();
    } catch (cause) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text(cause.toString())));
      }
    }
  }

  @override
  Widget build(BuildContext context) => Padding(
    padding: EdgeInsets.only(bottom: MediaQuery.viewInsetsOf(context).bottom),
    child: SizedBox(
      height: MediaQuery.sizeOf(context).height * .65,
      child: Column(
        children: [
          ListTile(title: Text('评论 ${comments.length}')),
          Expanded(
            child: comments.isEmpty
                ? const Center(child: Text('还没有评论，说点什么吧'))
                : ListView.builder(
                    itemCount: comments.length,
                    itemBuilder: (_, index) {
                      final comment = comments[index];
                      final currentUserId =
                          widget.controller.currentUser?.id ?? '';
                      final canDelete =
                          comment.authorId == currentUserId ||
                          widget.moment.authorId == currentUserId;
                      return ListTile(
                        leading: PersonAvatar(
                          name: comment.authorName,
                          avatarUrl: comment.authorAvatarUrl,
                          size: 38,
                        ),
                        title: Text(
                          comment.replyToName.isEmpty
                              ? comment.authorName
                              : '${comment.authorName} 回复 ${comment.replyToName}',
                        ),
                        subtitle: Text(comment.content),
                        trailing: canDelete
                            ? IconButton(
                                tooltip: '删除评论',
                                onPressed: () => _delete(comment),
                                icon: const Icon(
                                  CupertinoIcons.trash,
                                  size: 18,
                                ),
                              )
                            : null,
                        onTap: () {
                          commentFocus.requestFocus();
                          setState(() => replyingTo = comment);
                        },
                      );
                    },
                  ),
          ),
          SafeArea(
            top: false,
            child: Padding(
              padding: const EdgeInsets.fromLTRB(12, 8, 12, 10),
              child: Row(
                children: [
                  Expanded(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        if (replyingTo case final target?)
                          Row(
                            children: [
                              Expanded(
                                child: Text(
                                  '回复 ${target.authorName}',
                                  style: Theme.of(context).textTheme.bodySmall,
                                ),
                              ),
                              IconButton(
                                tooltip: '取消回复',
                                onPressed: () =>
                                    setState(() => replyingTo = null),
                                icon: const Icon(
                                  CupertinoIcons.xmark,
                                  size: 16,
                                ),
                              ),
                            ],
                          ),
                        TextField(
                          controller: text,
                          focusNode: commentFocus,
                          autofocus: true,
                          maxLength: 1000,
                          decoration: InputDecoration(
                            hintText: replyingTo == null
                                ? '写评论…'
                                : '回复 ${replyingTo!.authorName}…',
                            counterText: '',
                          ),
                          onSubmitted: (_) => _send(),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(width: 8),
                  IconButton.filled(
                    onPressed: sending ? null : _send,
                    icon: const Icon(CupertinoIcons.arrow_up),
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

class _MomentRemindersSheet extends StatefulWidget {
  const _MomentRemindersSheet({required this.controller});
  final AppController controller;

  @override
  State<_MomentRemindersSheet> createState() => _MomentRemindersSheetState();
}

class _MomentRemindersSheetState extends State<_MomentRemindersSheet> {
  List<MomentReminderSummary>? items;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    final result = await widget.controller.loadMomentReminders();
    if (!mounted) return;
    setState(() => items = result);
    final unread = result
        .where((item) => item.readAt == null)
        .map((item) => item.id)
        .toList();
    if (unread.isNotEmpty) {
      await widget.controller.markMomentRemindersRead(unread);
    }
  }

  @override
  Widget build(BuildContext context) => SizedBox(
    height: MediaQuery.sizeOf(context).height * .62,
    child: items == null
        ? const Center(child: CircularProgressIndicator())
        : items!.isEmpty
        ? const Center(child: Text('暂无互动提醒'))
        : ListView.builder(
            itemCount: items!.length,
            itemBuilder: (_, index) {
              final item = items![index];
              return ListTile(
                leading: PersonAvatar(
                  name: item.actorName,
                  avatarUrl: item.actorAvatarUrl,
                  size: 42,
                ),
                title: Text(
                  '${item.actorName}${item.type == 'like' ? '赞了你' : '评论了你'}',
                ),
                subtitle: Text(
                  item.momentPreview,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
                trailing: item.readAt == null
                    ? const Icon(CupertinoIcons.circle_fill, size: 8)
                    : null,
              );
            },
          ),
  );
}

class _MomentPicker extends StatefulWidget {
  const _MomentPicker({required this.controller});
  final AppController controller;

  @override
  State<_MomentPicker> createState() => _MomentPickerState();
}

class _MomentPickerState extends State<_MomentPicker> {
  List<MomentSummary>? items;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    final page = await widget.controller.loadMoments();
    if (mounted) setState(() => items = page.items);
  }

  @override
  Widget build(BuildContext context) => items == null
      ? const Center(child: CircularProgressIndicator())
      : Column(
          children: [
            const ListTile(title: Text('选择一条朋友圈分享')),
            Expanded(
              child: ListView.builder(
                itemCount: items!.length,
                itemBuilder: (_, index) {
                  final moment = items![index];
                  return ListTile(
                    leading: moment.media.isEmpty
                        ? const Icon(CupertinoIcons.person_2_square_stack)
                        : ClipRRect(
                            borderRadius: BorderRadius.circular(8),
                            child: LinliNetworkImage(
                              url: moment.media.first.url,
                              cacheKey: moment.media.first.url,
                              width: 48,
                              height: 48,
                              fit: BoxFit.cover,
                              errorBuilder: (_) =>
                                  const Icon(CupertinoIcons.photo),
                            ),
                          ),
                    title: Text(moment.authorName),
                    subtitle: Text(
                      moment.content.isEmpty ? '[媒体朋友圈]' : moment.content,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                    onTap: () => Navigator.pop(context, moment),
                  );
                },
              ),
            ),
          ],
        );
}

String _relativeTime(DateTime value) {
  final difference = DateTime.now().difference(value);
  if (difference.inMinutes < 1) return '刚刚';
  if (difference.inHours < 1) return '${difference.inMinutes} 分钟前';
  if (difference.inDays < 1) return '${difference.inHours} 小时前';
  if (difference.inDays < 7) return '${difference.inDays} 天前';
  return '${value.month}月${value.day}日';
}

String _visibilityLabel(String value) => switch (value) {
  'friends' => '仅好友',
  'private' => '仅自己',
  'selected' => '部分可见',
  'excluded' => '部分不可见',
  _ => '公开',
};

String _imageMime(String name) => switch (name.split('.').last.toLowerCase()) {
  'png' => 'image/png',
  'gif' => 'image/gif',
  'webp' => 'image/webp',
  'heic' => 'image/heic',
  _ => 'image/jpeg',
};
