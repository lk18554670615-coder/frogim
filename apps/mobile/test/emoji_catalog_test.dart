import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/emoji_catalog.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('精选目录有320个不同Emoji，分类顺序固定且每项为完整字符', () {
    expect(chatEmojiCategories.map((category) => category.label), [
      '表情',
      '手势',
      '符号',
      '动物与自然',
      '食物与饮品',
      '运动与活动',
      '旅行与交通',
      '物品',
    ]);
    final emojis = chatEmojiCategories
        .expand((category) => category.emojis)
        .toList();
    expect(emojis, hasLength(320));
    expect(emojis.toSet(), hasLength(emojis.length));
    expect(
      chatEmojiCategories.map((category) => category.id).toSet(),
      hasLength(8),
    );
    for (final category in chatEmojiCategories) {
      expect(category.emojis, isNotEmpty);
      for (final emoji in category.emojis) {
        expect(emoji.characters.length, 1, reason: '${category.label}: $emoji');
        expect(emoji.trim(), emoji);
      }
    }
  });

  test('原80个表情及最近默认项全部保留，旧缓存键和24个上限不变', () {
    const previous = [
      '😀',
      '😃',
      '😄',
      '😁',
      '😆',
      '😅',
      '😂',
      '🤣',
      '😊',
      '🙂',
      '🙃',
      '😉',
      '😍',
      '🥰',
      '😘',
      '😋',
      '😎',
      '🤓',
      '🧐',
      '🥳',
      '😏',
      '😔',
      '😢',
      '😭',
      '😤',
      '😡',
      '🤯',
      '😱',
      '😴',
      '🤔',
      '🤗',
      '🫡',
      '👋',
      '🤚',
      '🖐️',
      '✋',
      '🫶',
      '👌',
      '🤌',
      '🤏',
      '✌️',
      '🤞',
      '🫰',
      '🤟',
      '🤘',
      '🤙',
      '👈',
      '👉',
      '👆',
      '👇',
      '☝️',
      '👍',
      '👎',
      '✊',
      '👊',
      '👏',
      '❤️',
      '🧡',
      '💛',
      '💚',
      '💙',
      '💜',
      '🖤',
      '🤍',
      '💔',
      '💕',
      '💞',
      '💓',
      '💗',
      '💖',
      '✨',
      '⭐',
      '🔥',
      '🎉',
      '🎊',
      '✅',
      '❌',
      '💯',
      '❗',
      '❓',
    ];
    final emojis = chatEmojiCategories
        .expand((category) => category.emojis)
        .toSet();
    expect(emojis, containsAll(previous));
    expect(emojis, containsAll(defaultRecentChatEmojis));
    expect(chatRecentEmojisKey, 'chat_recent_emojis');
    expect(chatRecentEmojisLimit, 24);
  });

  test('所有目录字符均有现有NotoColorEmoji字体字形，无需新增字体', () async {
    final font = await rootBundle.load('assets/fonts/NotoColorEmoji.ttf');
    final ranges = _unicodeGlyphRanges(font);
    expect(ranges, isNotEmpty, reason: '现有字体需要包含Unicode全码点cmap表');
    for (final emoji in chatEmojiCategories.expand(
      (category) => category.emojis,
    )) {
      for (final codepoint in emoji.runes) {
        if (codepoint == 0xfe0f || codepoint == 0x200d) continue;
        expect(
          ranges.any(
            (range) =>
                codepoint >= range.$1 &&
                codepoint <= range.$2 &&
                range.$3 + codepoint - range.$1 != 0,
          ),
          isTrue,
          reason: '$emoji 缺少字形 U+${codepoint.toRadixString(16)}',
        );
      }
    }
  });
}

// Read the bundled sfnt's Unicode format-12 cmap, without a test dependency.
List<(int, int, int)> _unicodeGlyphRanges(ByteData font) {
  for (var index = 0; index < font.getUint16(4); index++) {
    final record = 12 + index * 16;
    final tag = String.fromCharCodes(
      List.generate(4, (i) => font.getUint8(record + i)),
    );
    if (tag != 'cmap') continue;
    final cmap = font.getUint32(record + 8);
    for (var i = 0; i < font.getUint16(cmap + 2); i++) {
      final subrecord = cmap + 4 + i * 8;
      final platform = font.getUint16(subrecord);
      final table = cmap + font.getUint32(subrecord + 4);
      if ((platform != 0 && platform != 3) || font.getUint16(table) != 12) {
        continue;
      }
      return List.generate(font.getUint32(table + 12), (group) {
        final offset = table + 16 + group * 12;
        return (
          font.getUint32(offset),
          font.getUint32(offset + 4),
          font.getUint32(offset + 8),
        );
      });
    }
  }
  return const [];
}
