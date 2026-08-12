import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
import 'package:linli_im/ui/widgets/media_send_widgets.dart';

void main() {
  testWidgets('video source chooser offers recording and gallery', (
    tester,
  ) async {
    ImageSource? selected;
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => ElevatedButton(
            onPressed: () async => selected = await chooseVideoSource(context),
            child: const Text('选择视频'),
          ),
        ),
      ),
    );

    await tester.tap(find.text('选择视频'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('record-short-video')), findsOneWidget);
    expect(find.byKey(const Key('choose-gallery-video')), findsOneWidget);

    await tester.tap(find.byKey(const Key('record-short-video')));
    await tester.pumpAndSettle();
    expect(selected, ImageSource.camera);
  });
}
