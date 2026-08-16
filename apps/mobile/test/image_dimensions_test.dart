import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:image/image.dart' as image_lib;
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/image_dimensions.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('发送图片前读取像素宽高并写入待发送消息和上传参数', () async {
    final bytes = Uint8List.fromList(
      image_lib.encodePng(image_lib.Image(width: 40, height: 80)),
    );
    final decoded = await decodeImagePixelSize(bytes);
    expect(decoded, isNotNull);
    expect(decoded!.width, 40);
    expect(decoded.height, 80);

    final repository = _ImageDimensionRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await controller.loginAsDemo();
    final conversation = controller.conversations.first;

    final sent = await controller.sendMedia(
      conversation.id,
      MediaUpload(
        bytes: bytes,
        fileName: 'portrait.png',
        mimeType: 'image/png',
        kind: MessageContentKind.image,
      ),
    );

    expect(repository.upload?.width, 40);
    expect(repository.upload?.height, 80);
    expect(repository.pending?.mediaWidth, 40);
    expect(repository.pending?.mediaHeight, 80);
    expect(sent.mediaWidth, 40);
    expect(sent.mediaHeight, 80);
  });
}

class _ImageDimensionRepository extends DemoImRepository {
  _ImageDimensionRepository() : super(latency: Duration.zero);

  ChatMessage? pending;
  MediaUpload? upload;

  @override
  Future<ChatMessage> sendMedia(
    ChatMessage message,
    MediaUpload mediaUpload, {
    void Function(double progress)? onProgress,
  }) async {
    pending = message;
    upload = mediaUpload;
    onProgress?.call(1);
    return message.copyWith(id: 'image-sent', status: MessageStatus.sent);
  }
}
