import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/image_export_contract.dart';

void main() {
  test('gallery name is a stem so the native plugin adds one extension', () {
    expect(imageFileStem('qingwaguagua-1', '.jpg'), 'qingwaguagua-1');
    expect(imageFileStem('qingwaguagua-1.jpg', '.jpg'), 'qingwaguagua-1');
    expect(imageFileStem('qingwaguagua-1.JPG', 'jpg'), 'qingwaguagua-1');
  });

  test('browser download name keeps exactly one extension', () {
    expect(imageFileName('qingwaguagua-1', '.png'), 'qingwaguagua-1.png');
    expect(imageFileName('qingwaguagua-1.png', '.png'), 'qingwaguagua-1.png');
  });
}
