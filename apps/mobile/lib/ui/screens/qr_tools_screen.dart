import 'dart:ui' as ui;

import 'package:flutter/foundation.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:image_picker/image_picker.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/image_export.dart';
import '../../core/models.dart';
import '../../core/qr_image_decoder.dart';
import '../../core/user_identity.dart';
import '../widgets/linli_widgets.dart';
import 'relationship_screens.dart';

String? qrLoginTokenFrom(String raw) {
  final uri = Uri.tryParse(raw);
  if (uri == null ||
      !const {'qingwaguagua', 'linlitong'}.contains(uri.scheme) ||
      uri.host != 'login') {
    return null;
  }
  final token = uri.pathSegments.firstOrNull?.trim();
  return token == null || !token.startsWith('ql_') || token.length > 128
      ? null
      : token;
}

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
  final imagePicker = ImagePicker();
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
      backgroundColor: LinliColors.brandInk,
      foregroundColor: Colors.white,
      surfaceTintColor: Colors.transparent,
      iconTheme: const IconThemeData(color: Colors.white),
      actionsIconTheme: const IconThemeData(color: Colors.white),
      titleTextStyle: Theme.of(context).textTheme.titleLarge?.copyWith(
        color: Colors.white,
        fontWeight: FontWeight.w700,
      ),
      title: const Text('扫一扫'),
    ),
    body: Stack(
      fit: StackFit.expand,
      children: [
        MobileScanner(controller: scanner, onDetect: _detected),
        const _ScannerMask(),
        SafeArea(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(20, 24, 20, 24),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                AnimatedContainer(
                  duration: const Duration(milliseconds: 180),
                  constraints: const BoxConstraints(maxWidth: 380),
                  padding: const EdgeInsets.symmetric(
                    horizontal: 16,
                    vertical: 12,
                  ),
                  decoration: BoxDecoration(
                    color: error == null
                        ? Colors.black.withValues(alpha: .72)
                        : const Color(0xFF8B1E2D).withValues(alpha: .92),
                    borderRadius: BorderRadius.circular(14),
                    border: Border.all(
                      color: Colors.white.withValues(alpha: .2),
                    ),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(
                        error == null
                            ? CupertinoIcons.qrcode_viewfinder
                            : CupertinoIcons.exclamationmark_triangle_fill,
                        color: Colors.white,
                        size: 20,
                      ),
                      const SizedBox(width: 10),
                      Flexible(
                        child: Text(
                          error ?? '对准二维码，或从相册选择二维码图片',
                          textAlign: TextAlign.center,
                          style: const TextStyle(
                            color: Colors.white,
                            fontSize: 14,
                            height: 1.35,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
                const SizedBox(height: 14),
                Container(
                  constraints: const BoxConstraints(maxWidth: 380),
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 8,
                  ),
                  decoration: BoxDecoration(
                    color: LinliColors.brandInk.withValues(alpha: .94),
                    borderRadius: BorderRadius.circular(20),
                    border: Border.all(
                      color: Colors.white.withValues(alpha: .18),
                    ),
                    boxShadow: const [
                      BoxShadow(
                        color: Colors.black38,
                        blurRadius: 18,
                        offset: Offset(0, 8),
                      ),
                    ],
                  ),
                  child: Row(
                    children: [
                      Expanded(
                        child: _ScannerAction(
                          icon: CupertinoIcons.photo_on_rectangle,
                          label: '相册',
                          onPressed: handling ? null : _pickFromGallery,
                        ),
                      ),
                      const _ScannerActionDivider(),
                      Expanded(
                        child: _ScannerAction(
                          icon: CupertinoIcons.camera_rotate,
                          label: '翻转',
                          onPressed: handling ? null : scanner.switchCamera,
                        ),
                      ),
                      const _ScannerActionDivider(),
                      Expanded(
                        child: _ScannerAction(
                          icon: CupertinoIcons.lightbulb,
                          label: '手电筒',
                          onPressed: handling ? null : scanner.toggleTorch,
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ),
        if (handling)
          const Positioned.fill(
            child: IgnorePointer(
              child: Center(
                child: CircularProgressIndicator(color: Colors.white),
              ),
            ),
          ),
      ],
    ),
  );

  Future<void> _detected(BarcodeCapture capture) async {
    if (handling) return;
    final raw = _rawValue(capture);
    if (raw == null) return;
    await _processRaw(raw);
  }

  Future<void> _pickFromGallery() async {
    if (handling) return;
    handling = true;
    if (mounted) setState(() => error = null);
    await scanner.stop();
    try {
      final image = await imagePicker.pickImage(source: ImageSource.gallery);
      if (image == null) return;
      final length = await image.length();
      if (length > 20 * 1024 * 1024) {
        throw const FormatException('图片不能超过 20 MB，请选择更小的二维码图片');
      }
      final raw = kIsWeb
          ? decodeQrImageBytes(await image.readAsBytes())
          : _rawValue(
              await scanner.analyzeImage(
                    image.path,
                    formats: const [BarcodeFormat.qrCode],
                  ) ??
                  const BarcodeCapture(barcodes: []),
            );
      if (raw == null) {
        throw const FormatException('图片中没有识别到二维码，请换一张清晰图片');
      }
      await _handleRaw(raw);
    } on FormatException catch (exception) {
      if (mounted) setState(() => error = exception.message);
    } on UnsupportedError {
      if (mounted) setState(() => error = '当前设备不支持从相册识别二维码');
    } catch (_) {
      if (mounted) setState(() => error = '无法读取这张图片，请重试');
    } finally {
      handling = false;
      if (mounted) {
        setState(() {});
        await scanner.start();
      }
    }
  }

  String? _rawValue(BarcodeCapture capture) => capture.barcodes
      .map((barcode) => barcode.rawValue?.trim())
      .whereType<String>()
      .where((value) => value.isNotEmpty)
      .firstOrNull;

  Future<void> _processRaw(String raw) async {
    handling = true;
    if (mounted) setState(() => error = null);
    await scanner.stop();
    try {
      await _handleRaw(raw);
    } on FormatException catch (exception) {
      if (mounted) setState(() => error = exception.message);
    } finally {
      handling = false;
      if (mounted) {
        setState(() {});
        await scanner.start();
      }
    }
  }

  Future<void> _handleRaw(String raw) async {
    final loginToken = qrLoginTokenFrom(raw);
    if (loginToken != null) {
      await _confirmWebLogin(loginToken);
      return;
    }
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
    final currentUser = widget.controller.currentUser;
    if (currentUser != null &&
        userIdentityMatchesQuery(
          id: currentUser.id,
          handle: currentUser.handle,
          query: query,
        )) {
      throw const FormatException('这是你自己的二维码');
    }
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
    if (currentUser != null &&
        samePublicUserIdentity(
          firstId: user.id,
          firstHandle: user.handle,
          secondId: currentUser.id,
          secondHandle: currentUser.handle,
        )) {
      throw const FormatException('这是你自己的二维码');
    }
    await _showUser(user);
  }

  Future<void> _confirmWebLogin(String token) async {
    final request = await widget.controller.inspectQrLogin(token);
    if (!mounted) return;
    if (request == null) {
      throw FormatException(widget.controller.error ?? '无法读取这次登录请求');
    }
    final platformLabel = switch (request.clientPlatform) {
      'web' => '网页版',
      'macos' => '桌面端',
      _ => '新设备',
    };
    final confirmed = await showDialog<bool>(
      context: context,
      barrierDismissible: false,
      builder: (dialogContext) => AlertDialog(
        key: const Key('qr-login-confirm-dialog'),
        icon: Container(
          width: 52,
          height: 52,
          decoration: BoxDecoration(
            color: LinliColors.brandYellowStrong,
            borderRadius: BorderRadius.circular(16),
          ),
          child: const Icon(
            CupertinoIcons.device_laptop,
            color: LinliColors.brandInk,
            size: 27,
          ),
        ),
        title: const Text('确认登录青蛙呱呱？'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(14),
              decoration: BoxDecoration(
                color: Theme.of(dialogContext).colorScheme.surfaceContainer,
                borderRadius: BorderRadius.circular(14),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    request.clientName,
                    key: const Key('qr-login-client-name'),
                    style: Theme.of(dialogContext).textTheme.titleMedium,
                  ),
                  const SizedBox(height: 4),
                  Text(
                    '$platformLabel · 登录请求将在两分钟内失效',
                    style: Theme.of(dialogContext).textTheme.bodySmall,
                  ),
                ],
              ),
            ),
            const SizedBox(height: 14),
            const Text('确认后，该设备将获得你的账号登录权限。若不是你本人操作，请选择取消。'),
          ],
        ),
        actions: [
          TextButton(
            key: const Key('cancel-qr-login'),
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          FilledButton(
            key: const Key('confirm-qr-login'),
            onPressed: () => Navigator.pop(dialogContext, true),
            child: const Text('确认登录'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    final success = await widget.controller.confirmQrLogin(token);
    if (!mounted) return;
    if (!success) {
      throw FormatException(widget.controller.error ?? '登录确认失败');
    }
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('已确认登录，新设备正在进入青蛙呱呱')));
    Navigator.pop(context);
  }

  String? _groupToken(String raw) {
    final uri = Uri.tryParse(raw);
    if (uri == null ||
        !const {'qingwaguagua', 'linlitong'}.contains(uri.scheme) ||
        uri.host != 'group') {
      return null;
    }
    final token = uri.pathSegments.firstOrNull?.trim();
    return token == null || token.isEmpty ? null : token;
  }

  String? _userQuery(String raw) {
    final uri = Uri.tryParse(raw);
    if (uri != null &&
        const {'qingwaguagua', 'linlitong'}.contains(uri.scheme) &&
        uri.host == 'user') {
      return uri.pathSegments.firstOrNull;
    }
    if (RegExp(r'^[a-zA-Z0-9_]{4,64}$').hasMatch(raw)) return raw;
    return null;
  }

  Future<void> _showUser(AppUser user) async {
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => FriendProfileScreen(
          controller: widget.controller,
          user: user,
          requestSource: 'qr',
          requestSourceId: user.id,
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
    final identity = publicUserHandle(user?.handle) ?? user?.id ?? 'unknown';
    final data = 'qingwaguagua://user/$identity';
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
                  color: LinliColors.brandInk.withValues(alpha: .08),
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
                            publicUserHandleLabel(user?.handle),
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
                  label: '我的青蛙呱呱二维码',
                  child: QrImageView(
                    data: data,
                    size: 250,
                    padding: const EdgeInsets.all(20),
                    backgroundColor: Colors.white,
                    eyeStyle: const QrEyeStyle(
                      eyeShape: QrEyeShape.square,
                      color: LinliColors.brandInk,
                    ),
                    dataModuleStyle: const QrDataModuleStyle(
                      dataModuleShape: QrDataModuleShape.square,
                      color: LinliColors.brandInk,
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
                  label: Text(kIsWeb ? '下载二维码' : '保存到相册'),
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
      final user = controller.currentUser;
      final image = await _renderQrShareCard(
        data,
        title: user?.name ?? '青蛙呱呱用户',
        subtitle: user == null ? '个人二维码' : publicUserHandleLabel(user.handle),
        footer: '打开青蛙呱呱 · 扫一扫添加我',
        usageNote: '二维码仅用于添加联系人',
      );
      if (image == null) throw const FormatException('二维码生成失败，请重试');
      await exportPngBytes(
        image,
        album: '青蛙呱呱',
        fileName: 'qingwaguagua-${DateTime.now().millisecondsSinceEpoch}',
      );
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(kIsWeb ? '二维码已开始下载' : '二维码已保存到系统相册')),
      );
    } on FormatException catch (error) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(error.message)));
    } on ImageExportException catch (error) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(error.message)));
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
    final data = 'qingwaguagua://group/$token';
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
                    padding: const EdgeInsets.all(20),
                    backgroundColor: Colors.white,
                    eyeStyle: const QrEyeStyle(
                      eyeShape: QrEyeShape.square,
                      color: LinliColors.brandInk,
                    ),
                    dataModuleStyle: const QrDataModuleStyle(
                      dataModuleShape: QrDataModuleShape.square,
                      color: LinliColors.brandInk,
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
                  label: Text(kIsWeb ? '下载二维码' : '保存到相册'),
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
      final image = await _renderQrShareCard(
        data,
        title: groupName,
        subtitle: '青蛙呱呱群聊',
        footer: '打开青蛙呱呱 · 扫一扫加入群聊',
        usageNote: '二维码仅用于加入此群聊',
      );
      if (image == null) throw const FormatException('二维码生成失败');
      await exportPngBytes(
        image,
        album: '青蛙呱呱',
        fileName: 'qingwaguagua-group-${DateTime.now().millisecondsSinceEpoch}',
      );
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(kIsWeb ? '群二维码已开始下载' : '群二维码已保存到系统相册')),
        );
      }
    } catch (error) {
      if (context.mounted) {
        final message = error is FormatException
            ? error.message
            : error is ImageExportException
            ? error.message
            : '保存失败，请检查相册权限';
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text(message)));
      }
    }
  }
}

Future<Uint8List?> _renderQrShareCard(
  String data, {
  required String title,
  required String subtitle,
  required String footer,
  required String usageNote,
}) async {
  const outputWidth = 1080;
  const outputHeight = 1440;
  const cardRect = ui.Rect.fromLTWH(100, 300, 880, 880);
  const qrRect = ui.Rect.fromLTWH(170, 370, 740, 740);
  final recorder = ui.PictureRecorder();
  final canvas = ui.Canvas(recorder);
  canvas.drawColor(LinliColors.background, ui.BlendMode.src);

  final brandBytes = await rootBundle.load(
    'assets/brand/qingwaguagua-mark-transparent.png',
  );
  final codec = await ui.instantiateImageCodec(
    brandBytes.buffer.asUint8List(),
    targetWidth: 84,
    targetHeight: 84,
  );
  final brandFrame = await codec.getNextFrame();
  canvas.drawImageRect(
    brandFrame.image,
    ui.Rect.fromLTWH(
      0,
      0,
      brandFrame.image.width.toDouble(),
      brandFrame.image.height.toDouble(),
    ),
    const ui.Rect.fromLTWH(68, 58, 84, 84),
    ui.Paint()..filterQuality = ui.FilterQuality.high,
  );
  brandFrame.image.dispose();
  codec.dispose();
  _paintShareText(
    canvas,
    '青蛙呱呱',
    const ui.Offset(170, 75),
    maxWidth: 500,
    fontSize: 34,
    fontWeight: FontWeight.w700,
    color: LinliColors.brandInk,
  );
  _paintShareText(
    canvas,
    title,
    const ui.Offset(70, 182),
    maxWidth: 940,
    fontSize: 42,
    fontWeight: FontWeight.w700,
    color: LinliColors.label,
    centered: true,
  );
  _paintShareText(
    canvas,
    subtitle,
    const ui.Offset(70, 240),
    maxWidth: 940,
    fontSize: 27,
    fontWeight: FontWeight.w400,
    color: LinliColors.preview,
    centered: true,
  );

  final card = ui.RRect.fromRectAndRadius(
    cardRect,
    const ui.Radius.circular(34),
  );
  canvas.drawShadow(
    ui.Path()..addRRect(card),
    const ui.Color(0x22000000),
    14,
    false,
  );
  canvas.drawRRect(card, ui.Paint()..color = Colors.white);
  canvas.save();
  canvas.translate(qrRect.left, qrRect.top);
  QrPainter(
    data: data,
    version: QrVersions.auto,
    gapless: true,
    eyeStyle: const QrEyeStyle(
      eyeShape: QrEyeShape.square,
      color: LinliColors.brandInk,
    ),
    dataModuleStyle: const QrDataModuleStyle(
      dataModuleShape: QrDataModuleShape.square,
      color: LinliColors.brandInk,
    ),
  ).paint(canvas, ui.Size.square(qrRect.width));
  canvas.restore();
  _paintShareText(
    canvas,
    footer,
    const ui.Offset(70, 1245),
    maxWidth: 940,
    fontSize: 27,
    fontWeight: FontWeight.w600,
    color: LinliColors.brandInk,
    centered: true,
  );
  _paintShareText(
    canvas,
    usageNote,
    const ui.Offset(70, 1300),
    maxWidth: 940,
    fontSize: 22,
    fontWeight: FontWeight.w400,
    color: LinliColors.tertiaryLabel,
    centered: true,
  );
  final image = await recorder.endRecording().toImage(
    outputWidth,
    outputHeight,
  );
  final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
  image.dispose();
  if (bytes == null) return null;
  return bytes.buffer.asUint8List(bytes.offsetInBytes, bytes.lengthInBytes);
}

void _paintShareText(
  ui.Canvas canvas,
  String text,
  ui.Offset offset, {
  required double maxWidth,
  required double fontSize,
  required FontWeight fontWeight,
  required Color color,
  bool centered = false,
}) {
  final painter = TextPainter(
    text: TextSpan(
      text: text,
      style: TextStyle(
        color: color,
        fontSize: fontSize,
        fontWeight: fontWeight,
        height: 1.2,
      ),
    ),
    textDirection: TextDirection.ltr,
    maxLines: 1,
    ellipsis: '…',
  )..layout(maxWidth: maxWidth);
  painter.paint(
    canvas,
    centered
        ? ui.Offset(offset.dx + (maxWidth - painter.width) / 2, offset.dy)
        : offset,
  );
}

class _ScannerAction extends StatelessWidget {
  const _ScannerAction({
    required this.icon,
    required this.label,
    required this.onPressed,
  });

  final IconData icon;
  final String label;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    enabled: onPressed != null,
    label: label == '相册' ? '从相册选择二维码' : label,
    child: InkWell(
      onTap: onPressed,
      borderRadius: BorderRadius.circular(14),
      child: ConstrainedBox(
        constraints: const BoxConstraints(minHeight: 64),
        child: Opacity(
          opacity: onPressed == null ? .45 : 1,
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(icon, color: Colors.white, size: 24),
              const SizedBox(height: 5),
              Text(
                label,
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
      ),
    ),
  );
}

class _ScannerActionDivider extends StatelessWidget {
  const _ScannerActionDivider();

  @override
  Widget build(BuildContext context) => Container(
    width: 1,
    height: 36,
    color: Colors.white.withValues(alpha: .16),
  );
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
