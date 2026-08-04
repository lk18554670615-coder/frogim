import 'dart:async';

import 'package:flutter_webrtc/flutter_webrtc.dart';

import 'call_models.dart';

enum CallConnectionState { connecting, connected, disconnected, failed, closed }

abstract interface class CallMediaEngine {
  Stream<CallConnectionState> get connectionChanges;
  Stream<Map<String, Object?>> get localCandidates;
  RTCVideoRenderer? get localRenderer;
  RTCVideoRenderer? get remoteRenderer;

  Future<void> initialize({
    required CallConfiguration configuration,
    required CallMediaType mediaType,
  });
  Future<Map<String, String>> createOffer();
  Future<Map<String, String>> createAnswer();
  Future<void> setRemoteDescription({
    required String sdp,
    required String type,
  });
  Future<void> addRemoteCandidate(Map<String, Object?> candidate);
  Future<void> setMuted(bool value);
  Future<void> setCameraEnabled(bool value);
  Future<void> setSpeakerEnabled(bool value);
  Future<void> switchCamera();
  Future<void> dispose();
}

class WebRtcCallMediaEngine implements CallMediaEngine {
  final _connections = StreamController<CallConnectionState>.broadcast();
  final _candidates = StreamController<Map<String, Object?>>.broadcast();
  final _queuedCandidates = <RTCIceCandidate>[];
  final _localRenderer = RTCVideoRenderer();
  final _remoteRenderer = RTCVideoRenderer();
  RTCPeerConnection? _peer;
  MediaStream? _localStream;
  bool _remoteDescriptionSet = false;

  @override
  Stream<CallConnectionState> get connectionChanges => _connections.stream;

  @override
  Stream<Map<String, Object?>> get localCandidates => _candidates.stream;

  @override
  RTCVideoRenderer get localRenderer => _localRenderer;

  @override
  RTCVideoRenderer get remoteRenderer => _remoteRenderer;

  @override
  Future<void> initialize({
    required CallConfiguration configuration,
    required CallMediaType mediaType,
  }) async {
    await _localRenderer.initialize();
    await _remoteRenderer.initialize();
    final video = mediaType == CallMediaType.video;
    _localStream = await navigator.mediaDevices.getUserMedia({
      'audio': {
        'echoCancellation': true,
        'noiseSuppression': true,
        'autoGainControl': true,
      },
      'video': video
          ? {
              'facingMode': 'user',
              'width': {'ideal': 1280},
              'height': {'ideal': 720},
              'frameRate': {'ideal': 24, 'max': 30},
            }
          : false,
    });
    _localRenderer.srcObject = _localStream;
    _peer = await createPeerConnection({
      'iceServers': configuration.iceServers
          .map((server) => server.toRtcMap())
          .toList(),
      'sdpSemantics': 'unified-plan',
      'iceCandidatePoolSize': 2,
    });
    for (final track in _localStream!.getTracks()) {
      await _peer!.addTrack(track, _localStream!);
    }
    _peer!.onTrack = (event) {
      if (event.streams.isNotEmpty) {
        _remoteRenderer.srcObject = event.streams[0];
      }
    };
    _peer!.onAddStream = (stream) => _remoteRenderer.srcObject = stream;
    _peer!.onIceCandidate = (candidate) {
      final value = candidate.candidate;
      if (value == null || value.isEmpty) return;
      _candidates.add({
        'candidate': value,
        'sdpMid': candidate.sdpMid,
        'sdpMLineIndex': candidate.sdpMLineIndex,
      });
    };
    _peer!.onConnectionState = (state) {
      _connections.add(switch (state) {
        RTCPeerConnectionState.RTCPeerConnectionStateConnected =>
          CallConnectionState.connected,
        RTCPeerConnectionState.RTCPeerConnectionStateDisconnected =>
          CallConnectionState.disconnected,
        RTCPeerConnectionState.RTCPeerConnectionStateFailed =>
          CallConnectionState.failed,
        RTCPeerConnectionState.RTCPeerConnectionStateClosed =>
          CallConnectionState.closed,
        _ => CallConnectionState.connecting,
      });
    };
    _peer!.onIceConnectionState = (state) {
      if (state == RTCIceConnectionState.RTCIceConnectionStateFailed) {
        _connections.add(CallConnectionState.failed);
      } else if (state ==
          RTCIceConnectionState.RTCIceConnectionStateDisconnected) {
        _connections.add(CallConnectionState.disconnected);
      }
    };
  }

  @override
  Future<Map<String, String>> createOffer() async {
    final description = await _peer!.createOffer({
      'offerToReceiveAudio': true,
      'offerToReceiveVideo': true,
    });
    await _peer!.setLocalDescription(description);
    return {'sdp': description.sdp ?? '', 'type': description.type ?? 'offer'};
  }

  @override
  Future<Map<String, String>> createAnswer() async {
    final description = await _peer!.createAnswer({
      'offerToReceiveAudio': true,
      'offerToReceiveVideo': true,
    });
    await _peer!.setLocalDescription(description);
    return {'sdp': description.sdp ?? '', 'type': description.type ?? 'answer'};
  }

  @override
  Future<void> setRemoteDescription({
    required String sdp,
    required String type,
  }) async {
    await _peer!.setRemoteDescription(RTCSessionDescription(sdp, type));
    _remoteDescriptionSet = true;
    for (final candidate in _queuedCandidates) {
      await _peer!.addCandidate(candidate);
    }
    _queuedCandidates.clear();
  }

  @override
  Future<void> addRemoteCandidate(Map<String, Object?> candidate) async {
    final value = candidate['candidate'] as String?;
    if (value == null || value.isEmpty) return;
    final parsed = RTCIceCandidate(
      value,
      candidate['sdpMid'] as String?,
      (candidate['sdpMLineIndex'] as num?)?.toInt(),
    );
    if (_remoteDescriptionSet) {
      await _peer!.addCandidate(parsed);
    } else {
      _queuedCandidates.add(parsed);
    }
  }

  @override
  Future<void> setMuted(bool value) async {
    for (final track in _localStream?.getAudioTracks() ?? const []) {
      track.enabled = !value;
    }
  }

  @override
  Future<void> setCameraEnabled(bool value) async {
    for (final track in _localStream?.getVideoTracks() ?? const []) {
      track.enabled = value;
    }
  }

  @override
  Future<void> setSpeakerEnabled(bool value) => Helper.setSpeakerphoneOn(value);

  @override
  Future<void> switchCamera() async {
    final tracks = _localStream?.getVideoTracks() ?? const [];
    if (tracks.isNotEmpty) await Helper.switchCamera(tracks.first);
  }

  @override
  Future<void> dispose() async {
    for (final track in _localStream?.getTracks() ?? const []) {
      await track.stop();
    }
    await _localStream?.dispose();
    await _peer?.close();
    _localRenderer.srcObject = null;
    _remoteRenderer.srcObject = null;
    await _localRenderer.dispose();
    await _remoteRenderer.dispose();
    await _connections.close();
    await _candidates.close();
  }
}
