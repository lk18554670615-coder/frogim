import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart' as rtc;
import 'package:livekit_client/livekit_client.dart';

import 'call_models.dart';

enum CallConnectionState {
  connecting,
  connected,
  reconnecting,
  disconnected,
  failed,
  closed,
}

class CallRemoteVideo {
  const CallRemoteVideo({
    required this.participantId,
    required this.track,
    required this.isScreenShare,
    required this.isActiveSpeaker,
  });

  final String participantId;
  final VideoTrack track;
  final bool isScreenShare;
  final bool isActiveSpeaker;
}

abstract interface class CallMediaEngine {
  Stream<CallConnectionState> get connectionChanges;
  Stream<void> get mediaChanges;
  VideoTrack? get localVideoTrack;
  List<CallRemoteVideo> get remoteVideos;
  List<String> get activeSpeakerIds;
  int get participantCount;
  bool get screenShareEnabled;

  Future<void> initialize({
    required CallConfiguration configuration,
    required CallMediaType mediaType,
  });
  Future<void> connect(CallMediaSession session);
  Future<void> setMuted(bool value);
  Future<void> setCameraEnabled(bool value);
  Future<void> setSpeakerEnabled(bool value);
  Future<void> setScreenShareEnabled(bool value);
  Future<void> switchCamera();
  Future<void> dispose();
}

class LiveKitCallMediaEngine implements CallMediaEngine {
  static const _screenShareChannel = MethodChannel(
    'com.fd.kuailiao/screen_share',
  );
  final _connections = StreamController<CallConnectionState>.broadcast();
  final _media = StreamController<void>.broadcast();
  Room? _room;
  EventsListener<RoomEvent>? _listener;
  LocalAudioTrack? _localAudio;
  LocalVideoTrack? _localVideo;
  CallMediaType? _mediaType;
  bool _muted = false;
  bool _cameraEnabled = true;
  bool _speakerEnabled = true;
  bool _screenShareEnabled = false;
  bool _connected = false;
  bool _disposing = false;

  static const _videoParameters = VideoParameters(
    dimensions: VideoDimensionsPresets.h360_169,
    encoding: VideoEncoding(maxBitrate: 450000, maxFramerate: 15),
  );

  @override
  Stream<CallConnectionState> get connectionChanges => _connections.stream;

  @override
  Stream<void> get mediaChanges => _media.stream;

  @override
  VideoTrack? get localVideoTrack {
    final publication = _room?.localParticipant?.getTrackPublicationBySource(
      TrackSource.camera,
    );
    return publication?.track is VideoTrack
        ? publication!.track! as VideoTrack
        : _localVideo;
  }

  @override
  List<CallRemoteVideo> get remoteVideos {
    final room = _room;
    if (room == null) return const [];
    final active = room.activeSpeakers.map((value) => value.identity).toSet();
    final tracks = <CallRemoteVideo>[];
    for (final participant in room.remoteParticipants.values) {
      for (final publication in participant.videoTrackPublications) {
        final track = publication.track;
        if (track == null || publication.muted) continue;
        tracks.add(
          CallRemoteVideo(
            participantId: participant.identity,
            track: track,
            isScreenShare: publication.source == TrackSource.screenShareVideo,
            isActiveSpeaker: active.contains(participant.identity),
          ),
        );
      }
    }
    tracks.sort((left, right) {
      if (left.isScreenShare != right.isScreenShare) {
        return left.isScreenShare ? -1 : 1;
      }
      if (left.isActiveSpeaker != right.isActiveSpeaker) {
        return left.isActiveSpeaker ? -1 : 1;
      }
      return left.participantId.compareTo(right.participantId);
    });
    return tracks;
  }

  @override
  List<String> get activeSpeakerIds =>
      _room?.activeSpeakers.map((value) => value.identity).toList() ?? const [];

  @override
  int get participantCount {
    final room = _room;
    if (room == null) return 1;
    return room.remoteParticipants.length +
        (room.localParticipant == null ? 0 : 1);
  }

  @override
  bool get screenShareEnabled => _screenShareEnabled;

  @override
  Future<void> initialize({
    required CallConfiguration configuration,
    required CallMediaType mediaType,
  }) async {
    if (_room != null || _localAudio != null || _localVideo != null) return;
    if (configuration.provider != 'livekit') {
      throw StateError('不支持的通话媒体服务');
    }
    _mediaType = mediaType;
    _localAudio = await LocalAudioTrack.create(
      const AudioCaptureOptions(
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
      ),
    );
    if (mediaType == CallMediaType.video) {
      _localVideo = await LocalVideoTrack.createCameraTrack(
        const CameraCaptureOptions(
          cameraPosition: CameraPosition.front,
          maxFrameRate: 15,
          params: _videoParameters,
        ),
      );
    }
    _emitMedia();
  }

  @override
  Future<void> connect(CallMediaSession session) async {
    if (_connected) return;
    if (_localAudio == null) {
      throw StateError('通话媒体尚未初始化');
    }
    if (session.expiresAt.isBefore(DateTime.now())) {
      throw StateError('LiveKit 入会凭证已过期');
    }
    final room = Room(
      roomOptions: const RoomOptions(
        adaptiveStream: true,
        dynacast: true,
        defaultCameraCaptureOptions: CameraCaptureOptions(
          cameraPosition: CameraPosition.front,
          maxFrameRate: 15,
          params: _videoParameters,
        ),
        defaultAudioOutputOptions: AudioOutputOptions(speakerOn: true),
      ),
    );
    _room = room;
    final listener = room.createListener();
    _listener = listener;
    listener
      ..on<RoomConnectedEvent>((_) {
        _connections.add(CallConnectionState.connected);
        _emitMedia();
      })
      ..on<ReconnectingEvent>((_) {
        _connections.add(CallConnectionState.reconnecting);
      })
      ..on<RoomReconnectedEvent>((_) {
        _connections.add(CallConnectionState.connected);
        _emitMedia();
      })
      ..on<RoomDisconnectedEvent>((_) {
        if (!_disposing) _connections.add(CallConnectionState.failed);
      })
      ..on<ParticipantConnectedEvent>((_) => _emitMedia())
      ..on<ParticipantDisconnectedEvent>((_) => _emitMedia())
      ..on<TrackSubscribedEvent>((_) => _emitMedia())
      ..on<TrackUnsubscribedEvent>((_) => _emitMedia())
      ..on<TrackMutedEvent>((_) => _emitMedia())
      ..on<TrackUnmutedEvent>((_) => _emitMedia())
      ..on<ActiveSpeakersChangedEvent>((_) => _emitMedia());
    _connections.add(CallConnectionState.connecting);
    try {
      await room.connect(
        session.url,
        session.token,
        fastConnectOptions: FastConnectOptions(
          microphone: TrackOption(enabled: true, track: _localAudio),
          camera: TrackOption(
            enabled: _mediaType == CallMediaType.video,
            track: _localVideo,
          ),
        ),
      );
      _connected = true;
      await room.setSpeakerOn(_speakerEnabled);
      await room.localParticipant?.setMicrophoneEnabled(!_muted);
      if (_mediaType == CallMediaType.video) {
        await room.localParticipant?.setCameraEnabled(_cameraEnabled);
      }
      _connections.add(CallConnectionState.connected);
      _emitMedia();
    } catch (_) {
      _connections.add(CallConnectionState.failed);
      rethrow;
    }
  }

  @override
  Future<void> setMuted(bool value) async {
    _muted = value;
    if (_connected) {
      await _room?.localParticipant?.setMicrophoneEnabled(!value);
    } else {
      _localAudio?.mediaStreamTrack.enabled = !value;
    }
  }

  @override
  Future<void> setCameraEnabled(bool value) async {
    _cameraEnabled = value;
    if (_connected) {
      await _room?.localParticipant?.setCameraEnabled(value);
    } else {
      _localVideo?.mediaStreamTrack.enabled = value;
    }
    _emitMedia();
  }

  @override
  Future<void> setSpeakerEnabled(bool value) async {
    _speakerEnabled = value;
    await _room?.setSpeakerOn(value);
  }

  @override
  Future<void> setScreenShareEnabled(bool value) async {
    final participant = _room?.localParticipant;
    if (!_connected || participant == null) {
      throw StateError('加入通话后才能共享屏幕');
    }
    final isAndroid =
        !kIsWeb && defaultTargetPlatform == TargetPlatform.android;
    if (value && isAndroid) {
      final allowed = await rtc.Helper.requestCapturePermission();
      if (!allowed) throw StateError('未获得屏幕录制权限');
      // Android 14+ requires this after the user grants capture consent but
      // before WebRTC obtains the MediaProjection instance.
      await _screenShareChannel.invokeMethod<void>('startForegroundService');
    }
    try {
      await participant.setScreenShareEnabled(value, captureScreenAudio: true);
      _screenShareEnabled = value;
      _emitMedia();
    } catch (_) {
      if (value && isAndroid) await _stopAndroidScreenShareService();
      rethrow;
    } finally {
      if (!value && isAndroid) await _stopAndroidScreenShareService();
    }
  }

  Future<void> _stopAndroidScreenShareService() async {
    try {
      await _screenShareChannel.invokeMethod<void>('stopForegroundService');
    } on MissingPluginException {
      // Tests and non-Android embedders do not register this channel.
    }
  }

  @override
  Future<void> switchCamera() async {
    final publication = _room?.localParticipant?.getTrackPublicationBySource(
      TrackSource.camera,
    );
    final track = publication?.track;
    if (track is! LocalVideoTrack) return;
    final options = track.currentOptions;
    if (options is CameraCaptureOptions) {
      await track.setCameraPosition(options.cameraPosition.switched());
    }
  }

  void _emitMedia() {
    if (!_media.isClosed) _media.add(null);
  }

  @override
  Future<void> dispose() async {
    if (_disposing) return;
    _disposing = true;
    if (!kIsWeb && defaultTargetPlatform == TargetPlatform.android) {
      await _stopAndroidScreenShareService();
    }
    final room = _room;
    _room = null;
    await _listener?.dispose();
    _listener = null;
    if (room != null) {
      try {
        await room.disconnect().timeout(const Duration(seconds: 3));
      } catch (_) {
        // A failed signal handshake has no remote session to disconnect. Media
        // cleanup must still finish and preserve the original connection error.
      }
      try {
        await room.dispose();
      } catch (_) {
        // Native WebRTC resources are best-effort after a failed handshake.
      }
    } else {
      await _localAudio?.stop();
      await _localAudio?.dispose();
      await _localVideo?.stop();
      await _localVideo?.dispose();
    }
    _localAudio = null;
    _localVideo = null;
    if (!_connections.isClosed) {
      _connections.add(CallConnectionState.closed);
      await _connections.close();
    }
    if (!_media.isClosed) await _media.close();
  }
}
