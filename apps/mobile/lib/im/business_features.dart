import '../core/models.dart';

class BusinessChannelSummary {
  const BusinessChannelSummary({
    required this.id,
    required this.channelType,
    required this.category,
    required this.name,
    required this.description,
    required this.ownerId,
    required this.visibility,
    required this.joinPolicy,
    required this.postingPolicy,
    required this.memberCount,
    required this.subscribed,
    required this.role,
    this.avatarUrl = '',
    this.parentId = '',
    this.slowModeSeconds = 0,
    this.metadata = const {},
    this.ban = false,
    this.disband = false,
    this.sendBan = false,
    this.allowStranger = false,
  });

  factory BusinessChannelSummary.fromJson(Map<String, Object?> json) =>
      BusinessChannelSummary(
        id: json['channelId'] as String? ?? json['id'] as String? ?? '',
        channelType: (json['channelType'] as num?)?.toInt() ?? 0,
        category: json['category'] as String? ?? '',
        name: json['name'] as String? ?? '',
        description: json['description'] as String? ?? '',
        ownerId: json['ownerId'] as String? ?? '',
        visibility: json['visibility'] as String? ?? 'public',
        joinPolicy: json['joinPolicy'] as String? ?? 'open',
        postingPolicy: json['postingPolicy'] as String? ?? 'members',
        memberCount: (json['memberCount'] as num?)?.toInt() ?? 0,
        subscribed: json['subscribed'] as bool? ?? false,
        role: json['role'] as String? ?? '',
        avatarUrl: json['avatarUrl'] as String? ?? '',
        parentId: json['parentId'] as String? ?? '',
        slowModeSeconds: (json['slowModeSeconds'] as num?)?.toInt() ?? 0,
        metadata: _objectMap(json['metadata']),
        ban: json['ban'] as bool? ?? false,
        disband: json['disband'] as bool? ?? false,
        sendBan: json['sendBan'] as bool? ?? false,
        allowStranger: json['allowStranger'] as bool? ?? false,
      );

  final String id;
  final int channelType;
  final String category;
  final String name;
  final String description;
  final String avatarUrl;
  final String ownerId;
  final String parentId;
  final String visibility;
  final String joinPolicy;
  final String postingPolicy;
  final int slowModeSeconds;
  final int memberCount;
  final bool subscribed;
  final String role;
  final Map<String, Object?> metadata;
  final bool ban;
  final bool disband;
  final bool sendBan;
  final bool allowStranger;
}

class BusinessChannelMemberSummary {
  const BusinessChannelMemberSummary({
    required this.userId,
    required this.name,
    required this.handle,
    required this.avatarUrl,
    required this.role,
    required this.joinedAt,
    required this.updatedAt,
    this.mutedUntil,
    this.expiresAt,
  });

  factory BusinessChannelMemberSummary.fromJson(Map<String, Object?> json) =>
      BusinessChannelMemberSummary(
        userId: json['userId'] as String? ?? '',
        name: json['name'] as String? ?? '',
        handle: json['handle'] as String? ?? '',
        avatarUrl: json['avatarUrl'] as String? ?? '',
        role: json['role'] as String? ?? 'member',
        mutedUntil: _nullableDate(json['mutedUntil']),
        expiresAt: _nullableDate(json['expiresAt']),
        joinedAt: _date(json['joinedAt']),
        updatedAt: _date(json['updatedAt']),
      );

  final String userId;
  final String name;
  final String handle;
  final String avatarUrl;
  final String role;
  final DateTime? mutedUntil;
  final DateTime? expiresAt;
  final DateTime joinedAt;
  final DateTime updatedAt;
}

class BusinessChannelAccessSummary {
  const BusinessChannelAccessSummary({
    required this.userId,
    required this.name,
    required this.handle,
    required this.avatarUrl,
    required this.accessType,
    required this.reason,
    required this.createdAt,
  });

  factory BusinessChannelAccessSummary.fromJson(Map<String, Object?> json) =>
      BusinessChannelAccessSummary(
        userId: json['userId'] as String? ?? '',
        name: json['name'] as String? ?? '',
        handle: json['handle'] as String? ?? '',
        avatarUrl: json['avatarUrl'] as String? ?? '',
        accessType: json['accessType'] as String? ?? '',
        reason: json['reason'] as String? ?? '',
        createdAt: _date(json['createdAt']),
      );

  final String userId;
  final String name;
  final String handle;
  final String avatarUrl;
  final String accessType;
  final String reason;
  final DateTime createdAt;
}

class SupportSkillGroupSummary {
  const SupportSkillGroupSummary({
    required this.id,
    required this.name,
    required this.description,
    required this.queueCount,
    required this.availableAgents,
  });

  factory SupportSkillGroupSummary.fromJson(Map<String, Object?> json) =>
      SupportSkillGroupSummary(
        id: json['id'] as String? ?? '',
        name: json['name'] as String? ?? '',
        description: json['description'] as String? ?? '',
        queueCount: (json['queueCount'] as num?)?.toInt() ?? 0,
        availableAgents: (json['availableAgents'] as num?)?.toInt() ?? 0,
      );

  final String id;
  final String name;
  final String description;
  final int queueCount;
  final int availableAgents;
}

class SupportAgentSummary {
  const SupportAgentSummary({
    required this.userId,
    required this.name,
    required this.status,
    required this.activeSessions,
    required this.maxConcurrent,
    required this.skillGroupIds,
    this.avatarUrl = '',
  });

  factory SupportAgentSummary.fromJson(Map<String, Object?> json) =>
      SupportAgentSummary(
        userId: json['userId'] as String? ?? '',
        name: json['name'] as String? ?? '',
        status: json['status'] as String? ?? 'offline',
        activeSessions: (json['activeSessions'] as num?)?.toInt() ?? 0,
        maxConcurrent: (json['maxConcurrent'] as num?)?.toInt() ?? 0,
        skillGroupIds: (json['skillGroupIds'] as List<Object?>? ?? const [])
            .whereType<String>()
            .toList(),
        avatarUrl: json['avatarUrl'] as String? ?? '',
      );

  final String userId;
  final String name;
  final String avatarUrl;
  final String status;
  final int activeSessions;
  final int maxConcurrent;
  final List<String> skillGroupIds;
}

class SupportSessionSummary {
  const SupportSessionSummary({
    required this.id,
    required this.visitorId,
    required this.visitorName,
    required this.skillGroupId,
    required this.skillGroupName,
    required this.channelId,
    required this.channelType,
    required this.subject,
    required this.status,
    required this.queuePosition,
    required this.assignedAgentId,
    required this.agentName,
    required this.rating,
    required this.ratingComment,
  });

  factory SupportSessionSummary.fromJson(Map<String, Object?> json) =>
      SupportSessionSummary(
        id: json['id'] as String? ?? '',
        visitorId: json['visitorId'] as String? ?? '',
        visitorName: json['visitorName'] as String? ?? '',
        skillGroupId: json['skillGroupId'] as String? ?? '',
        skillGroupName: json['skillGroupName'] as String? ?? '',
        channelId: json['channelId'] as String? ?? '',
        channelType: (json['channelType'] as num?)?.toInt() ?? 0,
        subject: json['subject'] as String? ?? '',
        status: json['status'] as String? ?? 'queued',
        queuePosition: (json['queuePosition'] as num?)?.toInt() ?? 0,
        assignedAgentId: json['assignedAgentId'] as String? ?? '',
        agentName: json['agentName'] as String? ?? '',
        rating: (json['rating'] as num?)?.toInt() ?? 0,
        ratingComment: json['ratingComment'] as String? ?? '',
      );

  final String id;
  final String visitorId;
  final String visitorName;
  final String skillGroupId;
  final String skillGroupName;
  final String channelId;
  final int channelType;
  final String subject;
  final String status;
  final int queuePosition;
  final String assignedAgentId;
  final String agentName;
  final int rating;
  final String ratingComment;
}

class BusinessMedia {
  const BusinessMedia({
    required this.id,
    required this.mime,
    required this.url,
  });

  final String id;
  final String mime;
  final String url;
}

class MomentMediaSummary {
  const MomentMediaSummary({
    required this.id,
    required this.mime,
    required this.url,
  });

  factory MomentMediaSummary.fromJson(Map<String, Object?> json) =>
      MomentMediaSummary(
        id: json['id'] as String? ?? '',
        mime: json['mime'] as String? ?? '',
        url: json['url'] as String? ?? '',
      );

  final String id;
  final String mime;
  final String url;
}

class MomentCommentSummary {
  const MomentCommentSummary({
    required this.id,
    required this.momentId,
    required this.authorId,
    required this.authorName,
    required this.parentId,
    required this.replyToUserId,
    required this.replyToName,
    required this.content,
    required this.createdAt,
    this.authorAvatarUrl = '',
  });

  factory MomentCommentSummary.fromJson(Map<String, Object?> json) =>
      MomentCommentSummary(
        id: json['id'] as String? ?? '',
        momentId: json['momentId'] as String? ?? '',
        authorId: json['authorId'] as String? ?? '',
        authorName: json['authorName'] as String? ?? '',
        authorAvatarUrl: json['authorAvatarUrl'] as String? ?? '',
        parentId: json['parentId'] as String? ?? '',
        replyToUserId: json['replyToUserId'] as String? ?? '',
        replyToName: json['replyToName'] as String? ?? '',
        content: json['content'] as String? ?? '',
        createdAt: _date(json['createdAt']),
      );

  final String id;
  final String momentId;
  final String authorId;
  final String authorName;
  final String authorAvatarUrl;
  final String parentId;
  final String replyToUserId;
  final String replyToName;
  final String content;
  final DateTime createdAt;
}

class MomentSummary {
  const MomentSummary({
    required this.id,
    required this.authorId,
    required this.authorName,
    required this.content,
    required this.mediaKind,
    required this.media,
    required this.visibility,
    required this.visibleUserIds,
    required this.location,
    required this.likeCount,
    required this.commentCount,
    required this.likedByMe,
    required this.comments,
    required this.status,
    required this.createdAt,
    required this.updatedAt,
    this.authorAvatarUrl = '',
  });

  factory MomentSummary.fromJson(Map<String, Object?> json) => MomentSummary(
    id: json['id'] as String? ?? '',
    authorId: json['authorId'] as String? ?? '',
    authorName: json['authorName'] as String? ?? '',
    authorAvatarUrl: json['authorAvatarUrl'] as String? ?? '',
    content: json['content'] as String? ?? '',
    mediaKind: json['mediaKind'] as String? ?? 'none',
    media: (json['media'] as List<Object?>? ?? const [])
        .map((item) => MomentMediaSummary.fromJson(_objectMap(item)))
        .where((item) => item.id.isNotEmpty)
        .toList(),
    visibility: json['visibility'] as String? ?? 'public',
    visibleUserIds: (json['visibleUserIds'] as List<Object?>? ?? const [])
        .whereType<String>()
        .toList(),
    location: _objectMap(json['location']),
    likeCount: (json['likeCount'] as num?)?.toInt() ?? 0,
    commentCount: (json['commentCount'] as num?)?.toInt() ?? 0,
    likedByMe: json['likedByMe'] as bool? ?? false,
    comments: (json['comments'] as List<Object?>? ?? const [])
        .map((item) => MomentCommentSummary.fromJson(_objectMap(item)))
        .where((item) => item.id.isNotEmpty)
        .toList(),
    status: json['status'] as String? ?? 'published',
    createdAt: _date(json['createdAt']),
    updatedAt: _date(json['updatedAt']),
  );

  final String id;
  final String authorId;
  final String authorName;
  final String authorAvatarUrl;
  final String content;
  final String mediaKind;
  final List<MomentMediaSummary> media;
  final String visibility;
  final List<String> visibleUserIds;
  final Map<String, Object?> location;
  final int likeCount;
  final int commentCount;
  final bool likedByMe;
  final List<MomentCommentSummary> comments;
  final String status;
  final DateTime createdAt;
  final DateTime updatedAt;
}

class MomentPage {
  const MomentPage({required this.items, required this.nextCursor});

  final List<MomentSummary> items;
  final String nextCursor;
}

class MomentReminderSummary {
  const MomentReminderSummary({
    required this.id,
    required this.momentId,
    required this.actorId,
    required this.actorName,
    required this.type,
    required this.commentId,
    required this.momentPreview,
    required this.createdAt,
    this.actorAvatarUrl = '',
    this.readAt,
  });

  factory MomentReminderSummary.fromJson(Map<String, Object?> json) =>
      MomentReminderSummary(
        id: (json['id'] as num?)?.toInt() ?? 0,
        momentId: json['momentId'] as String? ?? '',
        actorId: json['actorId'] as String? ?? '',
        actorName: json['actorName'] as String? ?? '',
        actorAvatarUrl: json['actorAvatarUrl'] as String? ?? '',
        type: json['type'] as String? ?? '',
        commentId: json['commentId'] as String? ?? '',
        momentPreview: json['momentPreview'] as String? ?? '',
        readAt: _nullableDate(json['readAt']),
        createdAt: _date(json['createdAt']),
      );

  final int id;
  final String momentId;
  final String actorId;
  final String actorName;
  final String actorAvatarUrl;
  final String type;
  final String commentId;
  final String momentPreview;
  final DateTime? readAt;
  final DateTime createdAt;
}

class StickerCategorySummary {
  const StickerCategorySummary({
    required this.id,
    required this.name,
    required this.sortOrder,
  });

  factory StickerCategorySummary.fromJson(Map<String, Object?> json) =>
      StickerCategorySummary(
        id: json['id'] as String? ?? '',
        name: json['name'] as String? ?? '',
        sortOrder: (json['sortOrder'] as num?)?.toInt() ?? 0,
      );

  final String id;
  final String name;
  final int sortOrder;
}

class StickerItemSummary {
  const StickerItemSummary({
    required this.id,
    required this.packId,
    required this.name,
    required this.mediaId,
    required this.mime,
    required this.url,
    required this.emoji,
    required this.sortOrder,
    required this.status,
    required this.metadata,
    required this.favorite,
    required this.useCount,
    this.usedAt,
  });

  factory StickerItemSummary.fromJson(Map<String, Object?> json) =>
      StickerItemSummary(
        id: json['id'] as String? ?? '',
        packId: json['packId'] as String? ?? '',
        name: json['name'] as String? ?? '',
        mediaId: json['mediaId'] as String? ?? '',
        mime: json['mime'] as String? ?? '',
        url: json['url'] as String? ?? '',
        emoji: json['emoji'] as String? ?? '',
        sortOrder: (json['sortOrder'] as num?)?.toInt() ?? 0,
        status: json['status'] as String? ?? 'published',
        metadata: _objectMap(json['metadata']),
        favorite: json['favorite'] as bool? ?? false,
        useCount: (json['useCount'] as num?)?.toInt() ?? 0,
        usedAt: _nullableDate(json['usedAt']),
      );

  final String id;
  final String packId;
  final String name;
  final String mediaId;
  final String mime;
  final String url;
  final String emoji;
  final int sortOrder;
  final String status;
  final Map<String, Object?> metadata;
  final bool favorite;
  final int useCount;
  final DateTime? usedAt;
}

class StickerPackSummary {
  const StickerPackSummary({
    required this.id,
    required this.categoryId,
    required this.categoryName,
    required this.name,
    required this.description,
    required this.coverMediaId,
    required this.coverMime,
    required this.coverUrl,
    required this.status,
    required this.sortOrder,
    required this.favorite,
    required this.items,
  });

  factory StickerPackSummary.fromJson(Map<String, Object?> json) =>
      StickerPackSummary(
        id: json['id'] as String? ?? '',
        categoryId: json['categoryId'] as String? ?? '',
        categoryName: json['categoryName'] as String? ?? '',
        name: json['name'] as String? ?? '',
        description: json['description'] as String? ?? '',
        coverMediaId: json['coverMediaId'] as String? ?? '',
        coverMime: json['coverMime'] as String? ?? '',
        coverUrl: json['coverUrl'] as String? ?? '',
        status: json['status'] as String? ?? 'published',
        sortOrder: (json['sortOrder'] as num?)?.toInt() ?? 0,
        favorite: json['favorite'] as bool? ?? false,
        items: (json['items'] as List<Object?>? ?? const [])
            .map((item) => StickerItemSummary.fromJson(_objectMap(item)))
            .where((item) => item.id.isNotEmpty)
            .toList(),
      );

  final String id;
  final String categoryId;
  final String categoryName;
  final String name;
  final String description;
  final String coverMediaId;
  final String coverMime;
  final String coverUrl;
  final String status;
  final int sortOrder;
  final bool favorite;
  final List<StickerItemSummary> items;
}

abstract interface class BusinessFeatureRepository {
  Future<List<BusinessChannelSummary>> businessChannels({
    int channelType = 0,
    String category = '',
    String parentId = '',
    int limit = 100,
  });
  Future<BusinessChannelSummary> createBusinessChannel({
    required int channelType,
    required String name,
    String parentId = '',
    String description = '',
    String visibility = 'public',
    String joinPolicy = 'open',
    String postingPolicy = 'members',
    int slowModeSeconds = 0,
  });
  Future<BusinessChannelSummary> businessChannel(
    String channelId,
    int channelType,
  );
  Future<BusinessChannelSummary> updateBusinessChannel(
    String channelId,
    int channelType, {
    String? name,
    String? description,
    String? visibility,
    String? joinPolicy,
    String? postingPolicy,
    int? slowModeSeconds,
    bool? sendBan,
    bool? allowStranger,
  });
  Future<List<BusinessChannelMemberSummary>> businessChannelMembers(
    String channelId,
    int channelType,
  );
  Future<void> addBusinessChannelMember(
    String channelId,
    int channelType,
    String userId, {
    DateTime? expiresAt,
  });
  Future<void> removeBusinessChannelMember(
    String channelId,
    int channelType,
    String userId,
  );
  Future<void> updateBusinessChannelMember(
    String channelId,
    int channelType,
    String userId, {
    String? role,
    DateTime? mutedUntil,
    bool clearMute = false,
    DateTime? expiresAt,
    bool clearExpiry = false,
  });
  Future<void> setBusinessChannelAccess(
    String channelId,
    int channelType,
    String userId,
    String accessType,
    bool enabled, {
    String reason = '',
  });
  Future<List<BusinessChannelAccessSummary>> businessChannelAccess(
    String channelId,
    int channelType, {
    String accessType = '',
  });
  Future<void> subscribeBusinessChannel(
    String channelId,
    int channelType, {
    DateTime? expiresAt,
  });
  Future<void> unsubscribeBusinessChannel(String channelId, int channelType);
  Future<List<SupportSkillGroupSummary>> supportSkillGroups();
  Future<List<SupportAgentSummary>> supportAgents({String skillGroupId = ''});
  Future<SupportAgentSummary> setSupportAgentStatus(String status);
  Future<List<SupportSessionSummary>> supportSessions({
    String status = '',
    String skillGroupId = '',
  });
  Future<SupportSessionSummary> createSupportSession({
    required String skillGroupId,
    String subject = '',
    int channelType = 10,
  });
  Future<SupportSessionSummary> claimSupportSession(String sessionId);
  Future<SupportSessionSummary> transferSupportSession(
    String sessionId,
    String targetAgentId,
  );
  Future<SupportSessionSummary> endSupportSession(String sessionId);
  Future<SupportSessionSummary> rateSupportSession(
    String sessionId,
    int rating,
    String comment,
  );
  Future<BusinessMedia> uploadBusinessMedia(
    MediaUpload upload, {
    void Function(double progress)? onProgress,
  });
  Future<MomentPage> moments({
    String authorId = '',
    String cursor = '',
    int limit = 20,
  });
  Future<MomentSummary> createMoment({
    required String content,
    required String mediaKind,
    required List<String> mediaIds,
    required String visibility,
    List<String> visibleUserIds = const [],
    Map<String, Object?> location = const {},
  });
  Future<MomentSummary> setMomentLike(String momentId, bool active);
  Future<MomentCommentSummary> createMomentComment(
    String momentId,
    String content, {
    String parentId = '',
  });
  Future<void> deleteMoment(String momentId);
  Future<void> deleteMomentComment(String momentId, String commentId);
  Future<List<MomentReminderSummary>> momentReminders({int limit = 100});
  Future<void> markMomentRemindersRead(List<int> reminderIds);
  Future<List<StickerCategorySummary>> stickerCategories();
  Future<List<StickerPackSummary>> stickerPacks({String categoryId = ''});
  Future<StickerPackSummary> stickerPack(String packId);
  Future<void> setStickerPackFavorite(String packId, bool active);
  Future<void> setStickerFavorite(String stickerId, bool active);
  Future<void> recordStickerUse(String stickerId);
  Future<List<StickerItemSummary>> recentStickers({int limit = 50});
  Future<List<StickerItemSummary>> favoriteStickers({int limit = 50});
}

Map<String, Object?> _objectMap(Object? value) {
  if (value is! Map) return const {};
  return value.map((key, item) => MapEntry(key.toString(), item));
}

DateTime _date(Object? value) =>
    _nullableDate(value) ?? DateTime.fromMillisecondsSinceEpoch(0, isUtc: true);

DateTime? _nullableDate(Object? value) => value is String
    ? DateTime.tryParse(value)?.toLocal()
    : value is num
    ? DateTime.fromMillisecondsSinceEpoch(value.toInt(), isUtc: true).toLocal()
    : null;
