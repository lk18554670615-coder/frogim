import 'package:shared_preferences/shared_preferences.dart';

enum ChatBackgroundStyle { followSystem, softMint, cleanPaper }

extension ChatBackgroundStyleDetails on ChatBackgroundStyle {
  String get title => switch (this) {
    ChatBackgroundStyle.followSystem => '跟随系统',
    ChatBackgroundStyle.softMint => '柔和薄荷',
    ChatBackgroundStyle.cleanPaper => '纯净纸面',
  };

  String get subtitle => switch (this) {
    ChatBackgroundStyle.followSystem => '自动适配浅色与深色外观',
    ChatBackgroundStyle.softMint => '与青蛙 Logo 呼应的低饱和浅绿',
    ChatBackgroundStyle.cleanPaper => '更简洁、对比更清晰的中性背景',
  };

  static ChatBackgroundStyle parse(String value) =>
      ChatBackgroundStyle.values.firstWhere(
        (style) => style.name == value,
        orElse: () => ChatBackgroundStyle.followSystem,
      );
}

/// Local-only preferences used by settings surfaces.
///
/// Server-backed capabilities remain in [AppController]. Keeping these values
/// here prevents a settings switch from pretending to change remote policy.
class LocalSettingsStore {
  const LocalSettingsStore();

  static const notificationEnabled = 'settings.notification.enabled';
  static const notificationPreview = 'settings.notification.preview';
  static const notificationSound = 'settings.notification.sound';
  static const notificationVibration = 'settings.notification.vibration';
  static const feedbackDraft = 'settings.support.feedback_draft';
  static const feedbackCategory = 'settings.support.feedback_category';
  static const recentSearches = 'search.recent.v1';
  static const chatBackground = 'settings.chat.background';

  Future<bool> readBool(String key, {required bool fallback}) async {
    final preferences = await SharedPreferences.getInstance();
    return preferences.getBool(key) ?? fallback;
  }

  Future<void> writeBool(String key, bool value) async {
    final preferences = await SharedPreferences.getInstance();
    final saved = await preferences.setBool(key, value);
    if (!saved) throw StateError('本机设置保存失败');
  }

  Future<String> readString(String key, {String fallback = ''}) async {
    final preferences = await SharedPreferences.getInstance();
    return preferences.getString(key) ?? fallback;
  }

  Future<void> writeString(String key, String value) async {
    final preferences = await SharedPreferences.getInstance();
    final saved = await preferences.setString(key, value);
    if (!saved) throw StateError('本机设置保存失败');
  }

  Future<List<String>> readRecentSearches() async {
    final preferences = await SharedPreferences.getInstance();
    return preferences.getStringList(recentSearches) ?? const [];
  }

  Future<void> addRecentSearch(String value) async {
    final normalized = value.trim();
    if (normalized.isEmpty) return;
    final preferences = await SharedPreferences.getInstance();
    final values = preferences.getStringList(recentSearches) ?? <String>[];
    values.remove(normalized);
    values.insert(0, normalized);
    final saved = await preferences.setStringList(
      recentSearches,
      values.take(8).toList(),
    );
    if (!saved) throw StateError('最近搜索保存失败');
  }

  Future<void> clearRecentSearches() async {
    final preferences = await SharedPreferences.getInstance();
    final cleared = await preferences.remove(recentSearches);
    if (!cleared) throw StateError('最近搜索清除失败');
  }

  Future<ChatBackgroundStyle> readChatBackground() async {
    final value = await readString(
      chatBackground,
      fallback: ChatBackgroundStyle.followSystem.name,
    );
    return ChatBackgroundStyleDetails.parse(value);
  }

  Future<void> writeChatBackground(ChatBackgroundStyle value) =>
      writeString(chatBackground, value.name);
}
