import 'dart:io';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/emoji_catalog.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _surfaceKey = Key('emoji-test-surface');
const _gridKey = Key('emoji-grid');

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUpAll(() async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(
          const MethodChannel('com.llfbandit.record/messages'),
          (_) async => null,
        );
    // Render actual CJK text and emoji rather than the test binding's Ahem font.
    for (final (family, asset) in [
      ('NotoSansSC', 'assets/fonts/NotoSansSC-Regular.otf'),
      ('NotoColorEmoji', 'assets/fonts/NotoColorEmoji.ttf'),
      ('MaterialIcons', 'fonts/MaterialIcons-Regular.otf'),
      (
        'packages/cupertino_icons/CupertinoIcons',
        'packages/cupertino_icons/assets/CupertinoIcons.ttf',
      ),
    ]) {
      await (FontLoader(family)..addFont(rootBundle.load(asset))).load();
    }
  });

  tearDownAll(() {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(
          const MethodChannel('com.llfbandit.record/messages'),
          null,
        );
  });

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('全部分类可横向访问，目录中的新表情可插入但不立即发送', (tester) async {
    var sends = 0;
    final text = await _openComposer(tester, onSend: () => sends++);
    for (var index = 0; index < chatEmojiCategories.length; index++) {
      await _selectCategory(tester, index + 1);
      final category = chatEmojiCategories[index];
      expect(
        tester.widget<Text>(find.byKey(const Key('emoji-category-label'))).data,
        category.label,
      );
      await tester.tap(
        find.byKey(ValueKey('emoji-item-${category.emojis.first}')),
      );
      await tester.pumpAndSettle();
    }
    expect(
      text.text,
      chatEmojiCategories.map((category) => category.emojis.first).join(),
    );
    expect(sends, 0);
    await tester.tap(find.byKey(const Key('emoji-send')));
    expect(sends, 1);
    expect(tester.takeException(), isNull);
  });

  testWidgets('切换分类网格回到顶部，不继承上一分类的滚动位置', (tester) async {
    await _openComposer(tester);
    await _selectCategory(tester, 1);
    await tester.drag(find.byKey(_gridKey), const Offset(0, -240));
    await tester.pumpAndSettle();
    expect(
      tester.widget<GridView>(find.byKey(_gridKey)).controller!.offset,
      greaterThan(0),
    );
    await _selectCategory(tester, 4);
    expect(tester.widget<GridView>(find.byKey(_gridKey)).controller!.offset, 0);
    expect(find.byKey(const ValueKey('emoji-item-🐶')), findsOneWidget);
    await _selectCategory(tester, 1);
    expect(tester.widget<GridView>(find.byKey(_gridKey)).controller!.offset, 0);
  });

  testWidgets('光标插入、替换选择和退格保留混合文本与正确光标位置', (tester) async {
    final text = await _openComposer(tester);
    text.value = const TextEditingValue(
      text: '你好世界',
      selection: TextSelection.collapsed(offset: 2),
    );
    await tester.pump();
    await tester.tap(find.byKey(const ValueKey('emoji-item-❤️')));
    await tester.pumpAndSettle();
    expect(text.text, '你好❤️世界');
    expect(text.selection.baseOffset, '你好❤️'.length);
    text.selection = TextSelection(baseOffset: 2, extentOffset: '你好❤️'.length);
    await tester.pump();
    await tester.tap(find.byKey(const ValueKey('emoji-item-😊')));
    await tester.pumpAndSettle();
    expect(text.text, '你好😊世界');
    expect(text.selection.baseOffset, '你好😊'.length);
    await tester.tap(find.byKey(const Key('emoji-backspace')));
    await tester.pump();
    expect(text.text, '你好世界');
    expect(text.selection.baseOffset, 2);
    text.selection = const TextSelection(baseOffset: 4, extentOffset: 2);
    await tester.tap(find.byKey(const Key('emoji-backspace')));
    await tester.pump();
    expect(text.text, '你好');
  });

  testWidgets('复合Emoji、肤色、家庭和旗帜退格一次完整删除', (tester) async {
    final text = await _openComposer(tester);
    for (final emoji in ['❤️', '👍🏽', '👨‍👩‍👧‍👦', '🇨🇳', '🏳️‍🌈']) {
      text.value = TextEditingValue(
        text: '前$emoji后',
        selection: TextSelection.collapsed(offset: '前$emoji'.length),
      );
      await tester.pump();
      await tester.tap(find.byKey(const Key('emoji-backspace')));
      await tester.pump();
      expect(text.text, '前后', reason: emoji);
      expect(text.selection.baseOffset, 1);
    }
    text.selection = const TextSelection.collapsed(offset: 0);
    await tester.tap(find.byKey(const Key('emoji-backspace')));
    expect(text.text, '前后');
  });

  testWidgets('升级读取旧最近记录，包括目录外的有效Emoji，不覆盖旧存储', (tester) async {
    const saved = ['👨‍👩‍👧‍👦', '🍕', '🐶', '❤️'];
    SharedPreferences.setMockInitialValues({chatRecentEmojisKey: saved});
    await _openComposer(tester);
    expect(_visibleRecent(tester), saved);
    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getStringList(chatRecentEmojisKey), saved);
    expect(find.byKey(const ValueKey('emoji-item-😊')), findsNothing);
  });

  testWidgets('最近最多24项，重复使用移至首位，关闭再开后顺序不变', (tester) async {
    final text = await _openComposer(tester, size: const Size(1280, 900));
    await _selectCategory(tester, 1);
    final used = chatEmojiCategories.first.emojis.take(30).toList();
    for (final emoji in used) {
      await tester.tap(find.byKey(ValueKey('emoji-item-$emoji')));
      await tester.pumpAndSettle();
    }
    await tester.tap(find.byKey(ValueKey('emoji-item-${used[20]}')));
    await tester.pumpAndSettle();
    final expected = [
      used[20],
      ...used.reversed.where((emoji) => emoji != used[20]),
    ].take(24).toList();
    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getStringList(chatRecentEmojisKey), expected);
    await tester.pumpWidget(const SizedBox.shrink());
    await _pumpComposer(tester, text);
    expect(_visibleRecent(tester), expected);
  });

  testWidgets('无效光标追加到末尾，删除为空后发送禁用', (tester) async {
    final text = await _openComposer(tester);
    text.text = '文字';
    await tester.pump();
    await tester.tap(find.byKey(const ValueKey('emoji-item-😊')));
    await tester.pumpAndSettle();
    expect(text.text, '文字😊');
    text.selection = TextSelection(
      baseOffset: 0,
      extentOffset: text.text.length,
    );
    await tester.tap(find.byKey(const Key('emoji-backspace')));
    await tester.pump();
    expect(text.text, isEmpty);
    expect(
      tester
          .widget<FilledButton>(find.byKey(const Key('emoji-send')))
          .onPressed,
      isNull,
    );
  });

  for (final size in [const Size(320, 568), const Size(1280, 900)]) {
    for (final brightness in Brightness.values) {
      for (final scale in [1.0, 2.0]) {
        testWidgets('${size.width} ${brightness.name} 字体$scale布局、点击范围及混合文本', (
          tester,
        ) async {
          final text = await _openComposer(
            tester,
            size: size,
            brightness: brightness,
            textScale: scale,
          );
          text.text = '你好 Hello ';
          await tester.pump();
          await _selectCategory(tester, 4);
          await tester.tap(find.byKey(const ValueKey('emoji-item-🐶')));
          await tester.pumpAndSettle();
          final rect = tester.getRect(
            find.byKey(const ValueKey('emoji-item-🐶')),
          );
          expect(rect.width, greaterThanOrEqualTo(44));
          expect(rect.height, greaterThanOrEqualTo(44));
          final styleSize = 25 * scale;
          expect(rect.height, greaterThan(styleSize));
          final backspace = tester.widget<IconButton>(
            find.byKey(const Key('emoji-backspace')),
          );
          expect(
            backspace.style!.foregroundColor!.resolve({}),
            isNot(backspace.style!.backgroundColor!.resolve({})),
          );
          expect(tester.takeException(), isNull);
          if (Platform.isWindows &&
              ((size.width == 320 && scale == 2) ||
                  (size.width == 1280 && scale == 1))) {
            await expectLater(
              find.byKey(_surfaceKey),
              matchesGoldenFile(
                'goldens/windows/emoji-${size.width == 320 ? 'mobile' : 'desktop'}-${brightness.name}.png',
              ),
            );
          }
        });
      }
    }
  }
}

Future<void> _selectCategory(WidgetTester tester, int index) async {
  final target = find.byKey(Key('emoji-category-$index'));
  await tester.ensureVisible(target);
  await tester.pumpAndSettle();
  await tester.tap(target);
  await tester.pumpAndSettle();
}

List<String> _visibleRecent(WidgetTester tester) => tester
    .widgetList<CupertinoButton>(
      find.descendant(
        of: find.byKey(_gridKey),
        matching: find.byType(CupertinoButton),
      ),
    )
    .map((button) => (button.child as Text).data!)
    .toList();

Future<TextEditingController> _openComposer(
  WidgetTester tester, {
  Size size = const Size(375, 812),
  Brightness brightness = Brightness.light,
  double textScale = 1,
  VoidCallback? onSend,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  final text = TextEditingController();
  addTearDown(() async {
    await tester.pumpWidget(const SizedBox.shrink());
    text.dispose();
  });
  await _pumpComposer(
    tester,
    text,
    brightness: brightness,
    textScale: textScale,
    onSend: onSend,
  );
  return text;
}

Future<void> _pumpComposer(
  WidgetTester tester,
  TextEditingController text, {
  Brightness brightness = Brightness.light,
  double textScale = 1,
  VoidCallback? onSend,
}) async {
  final theme = buildLinliTheme(brightness, fontFamily: 'NotoSansSC');
  await tester.pumpWidget(
    MaterialApp(
      theme: theme.copyWith(
        textTheme: theme.textTheme.apply(
          fontFamilyFallback: ['NotoColorEmoji'],
        ),
        cupertinoOverrideTheme: const CupertinoThemeData(
          textTheme: CupertinoTextThemeData(
            textStyle: TextStyle(
              fontFamily: 'NotoSansSC',
              fontFamilyFallback: ['NotoColorEmoji'],
            ),
            actionTextStyle: TextStyle(
              fontFamily: 'NotoSansSC',
              fontFamilyFallback: ['NotoColorEmoji'],
            ),
          ),
        ),
      ),
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(
          context,
        ).copyWith(textScaler: TextScaler.linear(textScale)),
        child: child!,
      ),
      home: Scaffold(
        body: Align(
          alignment: Alignment.bottomCenter,
          child: RepaintBoundary(
            key: _surfaceKey,
            child: ChatComposer(
              controller: text,
              showEmoji: true,
              onSend: onSend ?? () {},
              onToggleAttachments: () {},
              onToggleEmoji: () {},
              onAttachment: (_) {},
              onVoiceReady: (_) {},
              onCancelReply: () {},
            ),
          ),
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
}
