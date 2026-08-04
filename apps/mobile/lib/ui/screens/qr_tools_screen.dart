import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:gal/gal.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/models.dart';
import '../widgets/linli_widgets.dart';
import 'chat_screen.dart';

class QrScannerScreen extends StatefulWidget {
  const QrScannerScreen({super.key, required this.controller});
  final AppController controller;

  @override
  State<QrScannerScreen> createState() => _QrScannerScreenState();
}

class _QrScannerScreenState extends State<QrScannerScreen> {
  final scanner = MobileScannerController(
    formats: const [BarcodeFormat.qrCode],
    detectionSpeed: DetectionSpeed.noDuplicates,
  );
  bool handling = false;
  String? error;

  @override
  void dispose() {
    scanner.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: Colors.black,
    appBar: AppBar(
      backgroundColor: Colors.black,
      foregroundColor: Colors.white,
      title: const Text('扫一扫'),
      actions: [
        IconButton(
          tooltip: '切换摄像头',
          onPressed: scanner.switchCamera,
          icon: const Icon(CupertinoIcons.camera_rotate),
        ),
        IconButton(
          tooltip: '手电筒',
          onPressed: scanner.toggleTorch,
          icon: const Icon(CupertinoIcons.lightbulb),
        ),
      ],
    ),
    body: Stack(
      fit: StackFit.expand,
      children: [
        MobileScanner(controller: scanner, onDetect: _detected),
        const _ScannerMask(),
        SafeArea(
          child: Align(
            alignment: Alignment.bottomCenter,
            child: Padding(
              padding: const EdgeInsets.fromLTRB(24, 24, 24, 30),
              child: Text(
                error ?? '对准邻里通讯个人二维码，即可查看资料或发起聊天',
                textAlign: TextAlign.center,
                style: const TextStyle(color: Colors.white, fontSize: 14),
              ),
            ),
          ),
        ),
      ],
    ),
  );

  Future<void> _detected(BarcodeCapture capture) async {
    if (handling) return;
    final raw = capture.barcodes
        .map((barcode) => barcode.rawValue?.trim())
        .whereType<String>()
        .where((value) => value.isNotEmpty)
        .firstOrNull;
    if (raw == null) return;
    handling = true;
    await scanner.stop();
    try {
      final groupToken = _groupToken(raw);
      if (groupToken != null) {
        final joined = await widget.controller.joinGroupByQr(groupToken);
        if (!mounted) return;
        if (!joined) {
          throw FormatException(widget.controller.error ?? '加入群聊失败');
        }
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('已加入群聊，可在消息列表中查看')));
        Navigator.pop(context);
        return;
      }
      final query = _userQuery(raw);
      if (query == null) throw const FormatException('无法识别此二维码');
      final users = await widget.controller.searchUsers(query);
      final user = users
          .where(
            (item) =>
                item.handle.toLowerCase() == query.toLowerCase() ||
                item.id == query,
          )
          .firstOrNull;
      if (!mounted) return;
      if (user == null) throw const FormatException('没有找到对应用户');
      await _showUser(user);
    } on FormatException catch (exception) {
      if (mounted) setState(() => error = exception.message);
    } finally {
      handling = false;
      if (mounted) await scanner.start();
    }
  }

  String? _groupToken(String raw) {
    final uri = Uri.tryParse(raw);
    if (uri == null || uri.scheme != 'linlitong' || uri.host != 'group') {
      return null;
    }
    final token = uri.pathSegments.firstOrNull?.trim();
    return token == null || token.isEmpty ? null : token;
  }

  String? _userQuery(String raw) {
    final uri = Uri.tryParse(raw);
    if (uri != null && uri.scheme == 'linlitong' && uri.host == 'user') {
      return uri.pathSegments.firstOrNull;
    }
    if (RegExp(r'^[a-zA-Z0-9_]{4,64}$').hasMatch(raw)) return raw;
    return null;
  }

  Future<void> _showUser(AppUser user) async {
    await showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      isScrollControlled: true,
      builder: (sheetContext) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 6, 20, 24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              PersonAvatar(
                name: user.name,
                size: 64,
                avatarUrl: user.avatarUrl,
                online: user.isOnline,
              ),
              const SizedBox(height: 12),
              Text(user.name, style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: 4),
              Text(
                '@${user.handle}',
                style: Theme.of(context).textTheme.bodySmall,
              ),
              const SizedBox(height: 20),
              FilledButton.icon(
                onPressed: () async {
                  final conversation = await widget.controller.createDirect(
                    user,
                  );
                  if (!mounted ||
                      !sheetContext.mounted ||
                      conversation == null) {
                    return;
                  }
                  Navigator.pop(sheetContext);
                  await Navigator.of(context).pushReplacement(
                    MaterialPageRoute(
                      builder: (_) => ChatScreen(
                        controller: widget.controller,
                        conversation: conversation,
                      ),
                    ),
                  );
                },
                icon: const Icon(CupertinoIcons.chat_bubble_fill),
                label: const Text('发消息'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class MyQrCodeScreen extends StatelessWidget {
  const MyQrCodeScreen({super.key, required this.controller});
  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final user = controller.currentUser;
    final data = 'linlitong://user/${user?.handle ?? user?.id ?? 'unknown'}';
    return Scaffold(
      appBar: const GlassAppBar(title: Text('我的二维码')),
      body: Center(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(24),
          child: Container(
            constraints: const BoxConstraints(maxWidth: 360),
            padding: const EdgeInsets.all(24),
            decoration: BoxDecoration(
              color: Theme.of(context).colorScheme.surfaceContainer,
              borderRadius: BorderRadius.circular(24),
              boxShadow: [
                BoxShadow(
                  color: LinliColors.navy.withValues(alpha: .08),
                  blurRadius: 30,
                  offset: const Offset(0, 12),
                ),
              ],
            ),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Row(
                  children: [
                    PersonAvatar(
                      name: user?.name ?? '我',
                      size: 52,
                      avatarUrl: user?.avatarUrl,
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            user?.name ?? '我',
                            style: Theme.of(context).textTheme.titleMedium,
                          ),
                          Text(
                            '@${user?.handle ?? ''}',
                            style: Theme.of(context).textTheme.bodySmall,
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 24),
                Semantics(
                  image: true,
                  label: '我的邻里通讯二维码',
                  child: QrImageView(
                    data: data,
                    size: 250,
                    backgroundColor: Colors.white,
                    eyeStyle: const QrEyeStyle(
                      eyeShape: QrEyeShape.square,
                      color: LinliColors.navy,
                    ),
                    dataModuleStyle: const QrDataModuleStyle(
                      dataModuleShape: QrDataModuleShape.square,
                      color: LinliColors.navy,
                    ),
                  ),
                ),
                const SizedBox(height: 14),
                Text(
                  '扫一扫上面的二维码，加我为联系人',
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                const SizedBox(height: 18),
                OutlinedButton.icon(
                  onPressed: () => _saveQrCode(context, data),
                  icon: const Icon(CupertinoIcons.arrow_down_to_line),
                  label: const Text('保存到相册'),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _saveQrCode(BuildContext context, String data) async {
    try {
      var allowed = await Gal.hasAccess();
      if (!allowed) allowed = await Gal.requestAccess();
      if (!allowed) {
        throw const FormatException('请在系统设置中允许邻里通讯添加照片');
      }
      final painter = QrPainter(
        data: data,
        version: QrVersions.auto,
        gapless: true,
        eyeStyle: const QrEyeStyle(
          eyeShape: QrEyeShape.square,
          color: LinliColors.navy,
        ),
        dataModuleStyle: const QrDataModuleStyle(
          dataModuleShape: QrDataModuleShape.square,
          color: LinliColors.navy,
        ),
      );
      final image = await painter.toImageData(1024);
      if (image == null) throw const FormatException('二维码生成失败，请重试');
      await Gal.putImageBytes(
        image.buffer.asUint8List(),
        album: '邻里通讯',
        name: 'linlitong-${DateTime.now().millisecondsSinceEpoch}',
      );
      if (!context.mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('二维码已保存到系统相册')));
    } on FormatException catch (error) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(error.message)));
    } on GalException {
      if (!context.mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('保存失败，请检查相册权限和设备存储空间')));
    }
  }
}

class GroupQrCodeScreen extends StatelessWidget {
  const GroupQrCodeScreen({
    super.key,
    required this.groupName,
    required this.token,
    required this.expiresAt,
  });

  final String groupName;
  final String token;
  final DateTime? expiresAt;

  @override
  Widget build(BuildContext context) {
    final data = 'linlitong://group/$token';
    return Scaffold(
      appBar: const GlassAppBar(title: Text('群二维码')),
      body: Center(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(24),
          child: Container(
            constraints: const BoxConstraints(maxWidth: 360),
            padding: const EdgeInsets.all(24),
            decoration: BoxDecoration(
              color: Theme.of(context).colorScheme.surfaceContainer,
              borderRadius: BorderRadius.circular(24),
            ),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(groupName, style: Theme.of(context).textTheme.titleLarge),
                const SizedBox(height: 20),
                Semantics(
                  image: true,
                  label: '$groupName 的群二维码',
                  child: QrImageView(
                    data: data,
                    size: 250,
                    backgroundColor: Colors.white,
                    eyeStyle: const QrEyeStyle(
                      eyeShape: QrEyeShape.square,
                      color: LinliColors.navy,
                    ),
                    dataModuleStyle: const QrDataModuleStyle(
                      dataModuleShape: QrDataModuleShape.square,
                      color: LinliColors.navy,
                    ),
                  ),
                ),
                const SizedBox(height: 14),
                Text(
                  expiresAt == null
                      ? '群主可随时刷新此二维码'
                      : '有效期至 ${expiresAt!.month} 月 ${expiresAt!.day} 日 ${expiresAt!.hour.toString().padLeft(2, '0')}:${expiresAt!.minute.toString().padLeft(2, '0')}',
                  textAlign: TextAlign.center,
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                const SizedBox(height: 18),
                OutlinedButton.icon(
                  onPressed: () => _saveGroupQr(context, data),
                  icon: const Icon(CupertinoIcons.arrow_down_to_line),
                  label: const Text('保存到相册'),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _saveGroupQr(BuildContext context, String data) async {
    try {
      var allowed = await Gal.hasAccess();
      if (!allowed) allowed = await Gal.requestAccess();
      if (!allowed) throw const FormatException('请在系统设置中允许访问相册');
      final image = await QrPainter(
        data: data,
        version: QrVersions.auto,
        gapless: true,
        eyeStyle: const QrEyeStyle(
          eyeShape: QrEyeShape.square,
          color: LinliColors.navy,
        ),
        dataModuleStyle: const QrDataModuleStyle(
          dataModuleShape: QrDataModuleShape.square,
          color: LinliColors.navy,
        ),
      ).toImageData(1024);
      if (image == null) throw const FormatException('二维码生成失败');
      await Gal.putImageBytes(
        image.buffer.asUint8List(),
        album: '邻里通讯',
        name: 'linlitong-group-${DateTime.now().millisecondsSinceEpoch}',
      );
      if (context.mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('群二维码已保存到系统相册')));
      }
    } catch (error) {
      if (context.mounted) {
        final message = error is FormatException
            ? error.message
            : '保存失败，请检查相册权限';
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text(message)));
      }
    }
  }
}

class _ScannerMask extends StatelessWidget {
  const _ScannerMask();

  @override
  Widget build(BuildContext context) =>
      IgnorePointer(child: CustomPaint(painter: _ScannerMaskPainter()));
}

class _ScannerMaskPainter extends CustomPainter {
  @override
  void paint(Canvas canvas, Size size) {
    final side = size.width.clamp(220.0, 292.0);
    final rect = Rect.fromCenter(
      center: Offset(size.width / 2, size.height * .44),
      width: side,
      height: side,
    );
    final overlay = Path()
      ..addRect(Offset.zero & size)
      ..addRRect(RRect.fromRectAndRadius(rect, const Radius.circular(22)))
      ..fillType = PathFillType.evenOdd;
    canvas.drawPath(overlay, Paint()..color = Colors.black54);
    canvas.drawRRect(
      RRect.fromRectAndRadius(rect, const Radius.circular(22)),
      Paint()
        ..color = Colors.white
        ..style = PaintingStyle.stroke
        ..strokeWidth = 2,
    );
  }

  @override
  bool shouldRepaint(covariant CustomPainter oldDelegate) => false;
}
