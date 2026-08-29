# Linli Android screen-share patch

This directory is the unmodified `flutter_webrtc` 1.4.0 release except for
`GetUserMediaImpl.java`.

On Android 14 and newer, the MediaProjection request uses
`MediaProjectionConfig.createConfigForDefaultDisplay()`. The application shares
the current display instead of presenting Android's single-app task picker,
which can otherwise select an obsolete CallKit-created task.

The package version and Dart/native ABI remain 1.4.0 for compatibility with
the pinned LiveKit 2.7.0 client. The upstream MIT license is preserved in
`LICENSE`.
