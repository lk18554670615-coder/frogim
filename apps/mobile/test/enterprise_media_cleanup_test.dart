import 'dart:async';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/calls/call_media_engine.dart';
import 'package:linli_im/calls/call_models.dart';
import 'package:livekit_client/livekit_client.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const configuration = CallConfiguration(
    provider: 'livekit',
    url: 'wss://enterprise.test/livekit',
    inviteTimeout: Duration(seconds: 30),
    tokenTtl: Duration(minutes: 5),
    maxParticipants: 9,
    supportsScreenShare: true,
  );
  for (final lateVideo in [false, true]) {
    test(
      'disposing while ${lateVideo ? "camera" : "microphone"} permission waits releases late tracks',
      () async {
        final audio = _Audio();
        final video = _Video();
        final audioGate = Completer<LocalAudioTrack>();
        final videoGate = Completer<LocalVideoTrack>();
        final enteredVideo = Completer<void>();
        final engine = LiveKitCallMediaEngine(
          captureAudio: (_) => audioGate.future,
          captureVideo: (_) {
            enteredVideo.complete();
            return videoGate.future;
          },
        );
        final initialization = engine.initialize(
          configuration: configuration,
          mediaType: lateVideo ? CallMediaType.video : CallMediaType.audio,
        );
        final failure = expectLater(initialization, throwsStateError);
        if (lateVideo) {
          audioGate.complete(audio);
          await enteredVideo.future;
        }
        await engine.dispose();
        if (lateVideo) {
          videoGate.complete(video);
        } else {
          audioGate.complete(audio);
        }
        await failure;
        expect(audio.stopped, 1);
        expect(audio.disposed, 1);
        if (lateVideo) {
          expect(video.stopped, 1);
          expect(video.disposed, 1);
        }
      },
    );
  }
}

class _Audio implements LocalAudioTrack {
  int stopped = 0, disposed = 0;
  @override
  Future<bool> stop() async {
    stopped++;
    return true;
  }

  @override
  Future<bool> dispose() async {
    disposed++;
    return true;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _Video implements LocalVideoTrack {
  int stopped = 0, disposed = 0;
  @override
  Future<bool> stop() async {
    stopped++;
    return true;
  }

  @override
  Future<bool> dispose() async {
    disposed++;
    return true;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
