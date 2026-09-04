import 'dart:async';
import 'dart:io';

// Only files produced by this process's video compressor may be removed.
// Never delete the original gallery/file-picker source or an arbitrary path.
final _ownedVideoFiles = <String>{};
void registerVideoTemporarySource(String source) =>
    _ownedVideoFiles.add(source);
void releaseVideoSource(String? source) {
  if (source == null || !_ownedVideoFiles.remove(source)) return;
  unawaited(File(source).delete().catchError((Object _) => File(source)));
}
