import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/core/media_access.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/widgets/linli_widgets.dart';

void main() {
  testWidgets('旧签名图片在气泡和预览中改用固定鉴权地址', (tester) async {
    final owner = Object();
    mediaAccess.configure(
      owner: owner,
      apiBaseUrl: 'https://media.example.com',
      userId: 'me',
      token: 'media-only',
    );
    addTearDown(() => mediaAccess.clear(owner));
    final message = ChatMessage(
      id: 'image-fixed',
      conversationId: 'c',
      senderId: 'peer',
      senderName: 'Peer',
      text: '[图片]',
      sentAt: DateTime(2026),
      isMine: false,
      kind: MessageContentKind.image,
      mediaId: 'med_image',
      mediaUrl: 'https://old-storage.example/expired.jpg?X-Amz-Expires=900',
    );
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(body: MessageBubble(message: message)),
      ),
    );
    final image = tester.widget<CachedNetworkImage>(
      find.byType(CachedNetworkImage),
    );
    expect(
      image.imageUrl,
      'https://media.example.com/v2/media/med_image/content?viewer=me',
    );
    expect(image.httpHeaders, {'Authorization': 'Media media-only'});
    expect(image.cacheKey, image.imageUrl);
    await tester.tap(
      find.byKey(Key('message-image-${message.clientMessageId}')),
    );
    await tester.pumpAndSettle();
    final preview = tester.widget<CachedNetworkImage>(
      find.byKey(const Key('message-image-preview-render')),
    );
    expect(preview.imageUrl, image.imageUrl);
    expect(preview.httpHeaders, image.httpHeaders);
    await tester.pumpWidget(const SizedBox());
  });
  testWidgets('通用远程图片使用磁盘缓存并保留稳定缓存键', (tester) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: LinliNetworkImage(
          url: 'https://media.example.com/avatar/u-1.png',
          cacheKey: 'avatar-u-1-v2',
          width: 48,
          height: 48,
        ),
      ),
    );

    final image = tester.widget<CachedNetworkImage>(
      find.byType(CachedNetworkImage),
    );
    expect(image.imageUrl, 'https://media.example.com/avatar/u-1.png');
    expect(image.cacheKey, 'avatar-u-1-v2');
    expect(image.fadeInDuration, const Duration(milliseconds: 90));
    expect(image.fadeOutDuration, Duration.zero);
  });

  testWidgets('聊天表情缩略图复用消息媒体缓存键', (tester) async {
    final message = ChatMessage(
      id: 'sticker-cache-message',
      clientMessageId: 'sticker-cache-client',
      conversationId: 'sticker-cache-conversation',
      senderId: 'peer',
      senderName: '林屿',
      text: '[表情]',
      sentAt: DateTime(2026, 8, 16, 22),
      isMine: false,
      kind: MessageContentKind.sticker,
      mediaId: 'media-sticker-7',
      mediaUrl: 'https://media.example.com/stickers/7.webp',
    );

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(body: MessageBubble(message: message)),
      ),
    );

    final image = tester.widget<CachedNetworkImage>(
      find.byType(CachedNetworkImage),
    );
    expect(image.imageUrl, 'https://media.example.com/stickers/7.webp');
    expect(image.cacheKey, 'media-sticker-7');
    expect(image.width, 132);
    expect(image.height, 132);
  });
}
