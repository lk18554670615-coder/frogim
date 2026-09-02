import 'auth_validation.dart';
import 'models.dart';

const groupManagementNoticeEvents = <String>{
  'group.invite.accepted',
  'group.invite.rejected',
  'group.invite.cancelled',
  'group.member.joined',
  'group.members.added',
  'group.member_added',
  'group.member.leave',
  'group.member.remove',
  'group.blacklist.added',
  'screenshot.taken',
};

bool isManagedGroup(Conversation? conversation) =>
    conversation?.kind == ConversationKind.group &&
    !conversation!.isBusinessChannel;

bool isGroupManager(String? role) => role == 'owner' || role == 'admin';

/// Receipt data still syncs normally; this only controls client presentation.
/// Unknown or stale group roles cannot reveal another member's read state.
bool canPresentMessageReceipts(
  Conversation? conversation, {
  bool roleTrusted = true,
}) {
  if (conversation == null) return false;
  if (conversation.kind == ConversationKind.direct) return true;
  return roleTrusted && isGroupManager(conversation.currentUserRole);
}

bool isGroupManagementNotice(ChatMessage message) =>
    message.status == MessageStatus.recalled ||
    message.kind == MessageContentKind.screenshotNotice ||
    (message.kind == MessageContentKind.system &&
        groupManagementNoticeEvents.contains(message.event));

/// Shared by history, search, previews and notification presentation. Raw
/// messages stay cached; becoming a manager must not require recovering deletes.
bool canPresentGroupMessage(
  ChatMessage message,
  Conversation? conversation, {
  bool roleTrusted = true,
}) {
  if (!isGroupManagementNotice(message)) return true;
  if (conversation == null) return false;
  if (!isManagedGroup(conversation)) return true;
  return roleTrusted && isGroupManager(conversation.currentUserRole);
}

String messagePreviewText(ChatMessage message) =>
    message.status == MessageStatus.recalled ? '消息已撤回' : message.text;

bool canRecallChatMessage(
  ChatMessage message,
  Conversation? conversation,
  AuthPolicy policy, {
  DateTime? now,
  bool roleTrusted = true,
}) {
  if (message.id.isEmpty ||
      message.id.startsWith('local-') ||
      message.status == MessageStatus.sending ||
      message.status == MessageStatus.failed ||
      message.status == MessageStatus.recalled ||
      message.status == MessageStatus.expired ||
      message.kind == MessageContentKind.system) {
    return false;
  }
  final at = now ?? DateTime.now();
  if (message.expiresAt != null && !message.expiresAt!.isAfter(at)) {
    return false;
  }
  final group = isManagedGroup(conversation);
  if (group &&
      (!roleTrusted ||
          !{
            'owner',
            'admin',
            'member',
          }.contains(conversation!.currentUserRole))) {
    return false;
  }
  if (!message.isMine &&
      !(group && isGroupManager(conversation!.currentUserRole))) {
    return false;
  }
  final window = group ? policy.groupRecallWindow : policy.directRecallWindow;
  return at.difference(message.sentAt) <= window;
}
