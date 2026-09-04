import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_theme.dart';
import '../../core/models.dart';
import 'linli_widgets.dart';

bool isOrdinaryGroupConversation(Conversation conversation) =>
    conversation.kind == ConversationKind.group &&
    !conversation.isBusinessChannel;

String conversationTypeLabel(Conversation conversation) =>
    conversation.isBusinessChannel
    ? '频道'
    : isOrdinaryGroupConversation(conversation)
    ? '群聊'
    : '单聊';

/// Conversation-only decoration; personal/message avatars remain unchanged.
class ConversationAvatar extends StatelessWidget {
  const ConversationAvatar({
    super.key,
    required this.conversation,
    required this.name,
    required this.size,
    this.avatarUrl,
    this.online = false,
  });

  final Conversation conversation;
  final String name;
  final double size;
  final String? avatarUrl;
  final bool online;

  @override
  Widget build(BuildContext context) {
    final group = isOrdinaryGroupConversation(conversation);
    final markSize = size >= 40 ? 20.0 : 16.0;
    return SizedBox.square(
      dimension: size,
      child: Stack(
        children: [
          PersonAvatar(
            name: name,
            size: size,
            avatarUrl: avatarUrl,
            online: !group && online,
          ),
          if (group)
            Positioned(
              right: 0,
              bottom: 0,
              child: ExcludeSemantics(
                child: Container(
                  key: ValueKey('conversation-group-mark-${conversation.id}'),
                  width: markSize,
                  height: markSize,
                  decoration: BoxDecoration(
                    color: LinliColors.brandInk,
                    shape: BoxShape.circle,
                    border: Border.all(
                      color: Theme.of(context).colorScheme.surfaceContainer,
                      width: 1.5,
                    ),
                  ),
                  child: Icon(
                    CupertinoIcons.group_solid,
                    color: Colors.white,
                    size: markSize - 6,
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class ConversationTypeBadge extends StatelessWidget {
  const ConversationTypeBadge({super.key});

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    return ExcludeSemantics(
      child: DecoratedBox(
        decoration: BoxDecoration(
          color: dark ? LinliColors.brandInkSoft : LinliColors.brandYellowStrong,
          borderRadius: BorderRadius.circular(4),
        ),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
          child: Text(
            '群聊',
            maxLines: 1,
            softWrap: false,
            style: Theme.of(context).textTheme.labelSmall?.copyWith(
              fontSize: 10,
              height: 1.2,
              fontWeight: FontWeight.w600,
              color: dark
                  ? LinliColors.brandYellowStrong
                  : LinliColors.brandInk,
            ),
          ),
        ),
      ),
    );
  }
}

/// Let long names yield space to the badge, without pushing the timestamp.
class ConversationTitle extends StatelessWidget {
  const ConversationTitle({
    super.key,
    required this.conversation,
    required this.name,
    this.textKey,
    this.announceType = true,
    this.color,
  });

  final Conversation conversation;
  final String name;
  final Key? textKey;
  final bool announceType;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final title = Row(
      children: [
        Flexible(
          child: Text(
            name,
            key: textKey,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: Theme.of(
              context,
            ).textTheme.titleMedium?.copyWith(color: color),
          ),
        ),
        if (isOrdinaryGroupConversation(conversation)) ...[
          const SizedBox(width: 5),
          ConversationTypeBadge(
            key: ValueKey('conversation-group-label-${conversation.id}'),
          ),
        ],
      ],
    );
    return Semantics(
      label: announceType
          ? '${conversationTypeLabel(conversation)}，$name'
          : null,
      excludeSemantics: true,
      child: title,
    );
  }
}
