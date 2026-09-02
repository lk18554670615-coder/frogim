import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/voice_composer_controller.dart';
import 'package:linli_im/ui/widgets/voice_composer_widgets.dart';

import 'support/voice_fakes.dart';

void main() {
  testWidgets(
    'long press keeps its gesture target, overlay stays above composer, slide back previews',
    (tester) async {
      final recorder = FakeVoiceRecorder();
      var now = DateTime.now();
      final voice = VoiceComposerController(
        recorder: recorder,
        player: FakeVoicePlayer(),
        now: () => now,
      );
      final text = TextEditingController();
      await tester.pumpWidget(_composer(text, voice));
      await tester.tap(find.byKey(const Key('voice-mode-button')));
      await tester.pump();
      final before = tester.getRect(find.byKey(const Key('hold-to-talk')));
      final gesture = await tester.startGesture(before.center);
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump(const Duration(milliseconds: 200));
      expect(voice.phase, VoiceComposerPhase.recording);
      expect(tester.getRect(find.byKey(const Key('hold-to-talk'))), before);
      final overlay = tester.getRect(
        find.byKey(const Key('voice-recording-overlay')),
      );
      expect(overlay.bottom, lessThan(before.top));
      await gesture.moveBy(const Offset(0, -80));
      await tester.pump(const Duration(milliseconds: 200));
      expect(voice.phase, VoiceComposerPhase.canceling);
      expect(find.text('松开取消'), findsNWidgets(2));
      await gesture.moveBy(const Offset(0, 70));
      await tester.pump();
      expect(voice.phase, VoiceComposerPhase.recording);
      now = now.add(const Duration(seconds: 2));
      await gesture.up();
      await _flushFiles(tester);
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('voice-draft')), findsOneWidget);
      expect(find.byKey(const Key('voice-recording-overlay')), findsNothing);
      await tester.tap(find.byKey(const Key('preview-voice-button')));
      await tester.pumpAndSettle();
      expect(voice.playing, isTrue);
      await tester.tap(find.byKey(const Key('discard-voice-button')));
      await _flushFiles(tester);
      await tester.pumpAndSettle();
      expect(find.text('按住说话'), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
      voice.dispose();
      text.dispose();
    },
  );

  testWidgets(
    'permission wait has no waveform or recording label and refusal shows guidance',
    (tester) async {
      final recorder = FakeVoiceRecorder()..permissionGate = Completer<bool>();
      final voice = VoiceComposerController(
        recorder: recorder,
        player: FakeVoicePlayer(),
      );
      final text = TextEditingController();
      await tester.pumpWidget(_composer(text, voice));
      await tester.tap(find.byKey(const Key('voice-mode-button')));
      await tester.pump();
      final gesture = await tester.startGesture(
        tester.getCenter(find.byKey(const Key('hold-to-talk'))),
      );
      await tester.pump(const Duration(milliseconds: 600));
      expect(find.text('正在准备录音'), findsOneWidget);
      expect(find.byKey(const Key('voice-waveform')), findsNothing);
      expect(find.text('正在录音'), findsNothing);
      await gesture.up();
      recorder.permissionGate!.complete(false);
      await tester.pumpAndSettle();
      expect(find.text('无法使用麦克风'), findsOneWidget);
      expect(recorder.starts, 0);
      await tester.pumpWidget(const SizedBox.shrink());
      voice.dispose();
      text.dispose();
    },
  );

  for (final width in [320.0, 1280.0]) {
    for (final brightness in Brightness.values) {
      testWidgets('voice states fit $width / $brightness / 200% text', (
        tester,
      ) async {
        tester.view.physicalSize = Size(width, 844);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        final samples = ValueNotifier<List<double>>([0, .2, .4, .7, .3]);
        for (final phase in [
          VoiceComposerPhase.preparing,
          VoiceComposerPhase.recording,
          VoiceComposerPhase.canceling,
          VoiceComposerPhase.processing,
        ]) {
          await tester.pumpWidget(
            MaterialApp(
              theme: buildLinliTheme(brightness),
              home: MediaQuery(
                data: MediaQueryData(
                  size: Size(width, 844),
                  textScaler: const TextScaler.linear(2),
                ),
                child: Scaffold(
                  body: Align(
                    alignment: Alignment.bottomRight,
                    child: SizedBox(
                      width: width > 600 ? 520 : width,
                      child: VoiceRecordingOverlay(
                        phase: phase,
                        seconds: 12,
                        samples: samples,
                      ),
                    ),
                  ),
                ),
              ),
            ),
          );
          await tester.pump(const Duration(milliseconds: 200));
          expect(tester.takeException(), isNull);
        }
        await tester.pumpWidget(
          MaterialApp(
            theme: buildLinliTheme(brightness),
            home: MediaQuery(
              data: const MediaQueryData(textScaler: TextScaler.linear(2)),
              child: Scaffold(
                body: Center(
                  child: SizedBox(
                    width: width > 600 ? 380 : 170,
                    child: VoiceDraftControl(
                      seconds: 60,
                      playing: false,
                      busy: false,
                      previewBusy: false,
                      onPreview: () {},
                      onDiscard: () {},
                      onSend: () {},
                    ),
                  ),
                ),
              ),
            ),
          ),
        );
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox.shrink());
        samples.dispose();
      });
    }
  }

  testWidgets(
    'draft buttons lock during file preparation and reduced motion is static',
    (tester) async {
      final samples = ValueNotifier<List<double>>([1]);
      var sends = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: MediaQuery(
            data: const MediaQueryData(disableAnimations: true),
            child: Scaffold(
              body: Column(
                children: [
                  VoiceWaveform(samples: samples, color: Colors.green),
                  VoiceDraftControl(
                    seconds: 2,
                    playing: false,
                    busy: true,
                    previewBusy: false,
                    onPreview: () {},
                    onDiscard: () {},
                    onSend: () => sends++,
                  ),
                  const VoiceMessageEntrance(
                    animate: true,
                    child: Text('new voice'),
                  ),
                ],
              ),
            ),
          ),
        ),
      );
      for (final key in [
        'preview-voice-button',
        'discard-voice-button',
        'send-voice-button',
      ]) {
        expect(
          tester.widget<IconButton>(find.byKey(Key(key))).onPressed,
          isNull,
        );
      }
      await tester.tap(find.byKey(const Key('send-voice-button')));
      expect(sends, 0);
      final bar = tester.widget<AnimatedContainer>(
        find.byKey(const ValueKey('voice-level-23')),
      );
      expect(bar.constraints!.maxHeight, 3);
      expect(bar.duration, Duration.zero);
      expect(
        tester
            .widget<Opacity>(
              find.descendant(
                of: find.byType(VoiceMessageEntrance),
                matching: find.byType(Opacity),
              ),
            )
            .opacity,
        1,
      );
      await tester.pumpWidget(const SizedBox.shrink());
      samples.dispose();
    },
  );

  testWidgets(
    'new voice entrance runs once, ACK and retry do not restart it, history stays visible',
    (tester) async {
      Widget view(bool animate, String text) => MaterialApp(
        home: VoiceMessageEntrance(
          key: const ValueKey('client-id'),
          animate: animate,
          child: Text(text),
        ),
      );
      await tester.pumpWidget(view(true, 'sending'));
      final opacity = find.descendant(
        of: find.byType(VoiceMessageEntrance),
        matching: find.byType(Opacity),
      );
      expect(tester.widget<Opacity>(opacity).opacity, 0);
      await tester.pump(const Duration(milliseconds: 110));
      expect(tester.widget<Opacity>(opacity).opacity, inExclusiveRange(0, 1));
      await tester.pumpWidget(view(false, 'ACK'));
      await tester.pump(const Duration(milliseconds: 110));
      expect(tester.widget<Opacity>(opacity).opacity, 1);
      await tester.pumpWidget(view(true, 'retry'));
      expect(tester.widget<Opacity>(opacity).opacity, 1);
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpWidget(view(false, 'history'));
      expect(tester.widget<Opacity>(opacity).opacity, 1);
    },
  );

  testWidgets(
    'voice progress uses reported percentage and fades away only when cleared',
    (tester) async {
      Widget view(double? progress) => MaterialApp(
        home: Scaffold(
          body: VoiceUploadProgress(
            progress: progress,
            clientMessageId: 'voice',
          ),
        ),
      );
      await tester.pumpWidget(view(.37));
      expect(
        tester
            .widget<LinearProgressIndicator>(
              find.byType(LinearProgressIndicator),
            )
            .value,
        .37,
      );
      await tester.pumpWidget(view(1));
      expect(
        tester
            .widget<LinearProgressIndicator>(
              find.byType(LinearProgressIndicator),
            )
            .value,
        1,
      );
      await tester.pumpWidget(view(null));
      await tester.pumpAndSettle();
      expect(find.byType(LinearProgressIndicator), findsNothing);
    },
  );
}

Future<void> _flushFiles(WidgetTester tester) async {
  // Let real IO complete between fake-zone microtasks before settling spinners.
  for (var index = 0; index < 5; index++) {
    await tester.pump();
    await tester.runAsync(
      () async => Future<void>.delayed(const Duration(milliseconds: 20)),
    );
  }
}

Widget _composer(TextEditingController text, VoiceComposerController voice) =>
    MaterialApp(
      theme: buildLinliTheme(Brightness.light),
      home: Scaffold(
        body: Column(
          children: [
            const Expanded(child: Center(child: Text('聊天内容'))),
            ChatComposer(
              controller: text,
              voiceController: voice,
              onSend: () {},
              onToggleAttachments: () {},
              onToggleEmoji: () {},
              onAttachment: (_) {},
              onVoiceReady: (MediaUpload _) {},
              onCancelReply: () {},
            ),
          ],
        ),
      ),
    );
