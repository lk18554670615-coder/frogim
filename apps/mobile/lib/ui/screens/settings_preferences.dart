import 'package:shared_preferences/shared_preferences.dart';

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

  Future<bool> readBool(String key, {required bool fallback}) async {
    final preferences = await SharedPreferences.getInstance();
    return preferences.getBool(key) ?? fallback;
  }

  Future<void> writeBool(String key, bool value) async {
    final preferences = await SharedPreferences.getInstance();
    await preferences.setBool(key, value);
  }

  Future<String> readString(String key, {String fallback = ''}) async {
    final preferences = await SharedPreferences.getInstance();
    return preferences.getString(key) ?? fallback;
  }

  Future<void> writeString(String key, String value) async {
    final preferences = await SharedPreferences.getInstance();
    await preferences.setString(key, value);
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
    await preferences.setStringList(recentSearches, values.take(8).toList());
  }

  Future<void> clearRecentSearches() async {
    final preferences = await SharedPreferences.getInstance();
    await preferences.remove(recentSearches);
  }
}
