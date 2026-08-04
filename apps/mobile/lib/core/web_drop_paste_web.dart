import 'dart:async';
import 'dart:js_interop';
import 'dart:typed_data';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:web/web.dart' as web;

import 'app_theme.dart';

class WebPickedFile {
  const WebPickedFile({
    required this.bytes,
    required this.name,
    required this.mimeType,
  });

  final Uint8List bytes;
  final String name;
  final String mimeType;
}

class WebDropPasteRegion extends StatefulWidget {
  const WebDropPasteRegion({
    super.key,
    required this.child,
    required this.onFiles,
    required this.onError,
  });

  final Widget child;
  final Future<void> Function(List<WebPickedFile> files) onFiles;
  final ValueChanged<String> onError;

  @override
  State<WebDropPasteRegion> createState() => _WebDropPasteRegionState();
}

class _WebDropPasteRegionState extends State<WebDropPasteRegion> {
  final List<StreamSubscription<dynamic>> _subscriptions = [];
  var _dragDepth = 0;
  var _dragging = false;
  var _reading = false;

  @override
  void initState() {
    super.initState();
    final target = web.document.body;
    if (target == null) return;
    _subscriptions
      ..add(target.onDragEnter.listen(_onDragEnter))
      ..add(target.onDragOver.listen(_onDragOver))
      ..add(target.onDragLeave.listen(_onDragLeave))
      ..add(target.onDrop.listen(_onDrop))
      ..add(target.onPaste.listen(_onPaste));
  }

  bool _containsFiles(web.DataTransfer? data) {
    if (data == null) return false;
    for (var index = 0; index < data.items.length; index++) {
      if (data.items[index].kind == 'file') return true;
    }
    return data.files.length > 0;
  }

  void _onDragEnter(web.Event raw) {
    final event = raw as web.DragEvent;
    if (!_containsFiles(event.dataTransfer)) return;
    event.preventDefault();
    _dragDepth++;
    if (!_dragging && mounted) setState(() => _dragging = true);
  }

  void _onDragOver(web.Event raw) {
    final event = raw as web.DragEvent;
    if (!_containsFiles(event.dataTransfer)) return;
    event.preventDefault();
    event.dataTransfer?.dropEffect = 'copy';
  }

  void _onDragLeave(web.Event raw) {
    if (!_dragging) return;
    _dragDepth = (_dragDepth - 1).clamp(0, 1000);
    if (_dragDepth == 0 && mounted) setState(() => _dragging = false);
  }

  void _onDrop(web.Event raw) {
    final event = raw as web.DragEvent;
    final data = event.dataTransfer;
    if (!_containsFiles(data)) return;
    event.preventDefault();
    _dragDepth = 0;
    if (mounted) setState(() => _dragging = false);
    unawaited(_readFiles(data!.files));
  }

  void _onPaste(web.ClipboardEvent event) {
    final data = event.clipboardData;
    if (!_containsFiles(data)) return;
    event.preventDefault();
    unawaited(_readFiles(data!.files));
  }

  Future<void> _readFiles(web.FileList files) async {
    if (_reading || files.length == 0) return;
    if (mounted) setState(() => _reading = true);
    try {
      final picked = <WebPickedFile>[];
      for (var index = 0; index < files.length; index++) {
        final file = files.item(index);
        if (file == null) continue;
        final buffer = await file.arrayBuffer().toDart;
        picked.add(
          WebPickedFile(
            bytes: Uint8List.view(buffer.toDart),
            name: file.name,
            mimeType: file.type.isEmpty
                ? 'application/octet-stream'
                : file.type,
          ),
        );
      }
      if (picked.isNotEmpty) await widget.onFiles(picked);
    } catch (_) {
      widget.onError('浏览器无法读取该文件，请重新选择');
    } finally {
      if (mounted) setState(() => _reading = false);
    }
  }

  @override
  void dispose() {
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Stack(
    fit: StackFit.expand,
    children: [
      widget.child,
      IgnorePointer(
        child: AnimatedOpacity(
          duration: const Duration(milliseconds: 160),
          opacity: _dragging || _reading ? 1 : 0,
          child: ColoredBox(
            color: LinliColors.navy.withValues(alpha: .88),
            child: SafeArea(
              child: Center(
                child: Container(
                  margin: const EdgeInsets.all(24),
                  padding: const EdgeInsets.symmetric(
                    horizontal: 32,
                    vertical: 28,
                  ),
                  decoration: BoxDecoration(
                    color: Colors.white.withValues(alpha: .10),
                    borderRadius: BorderRadius.circular(16),
                    border: Border.all(
                      color: LinliColors.yellow.withValues(alpha: .75),
                      width: 2,
                    ),
                  ),
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (_reading)
                        const CupertinoActivityIndicator(color: Colors.white)
                      else
                        const Icon(
                          CupertinoIcons.cloud_upload,
                          color: LinliColors.yellow,
                          size: 34,
                        ),
                      const SizedBox(height: 12),
                      Text(
                        _reading ? '正在读取文件' : '松开发送到当前会话',
                        style: Theme.of(
                          context,
                        ).textTheme.titleMedium?.copyWith(color: Colors.white),
                      ),
                      const SizedBox(height: 4),
                      const Text(
                        '支持图片和文件，发送前会检查大小与类型',
                        style: TextStyle(color: Color(0xFFD5DCE7)),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    ],
  );
}
