import 'dart:async';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:http/http.dart' as http;
import 'package:open_filex/open_filex.dart';
import 'package:path_provider/path_provider.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:url_launcher/url_launcher.dart';

import 'client_upgrade.dart';
import 'client_upgrade_installer_contract.dart';

const _apkMimeType = 'application/vnd.android.package-archive';
const _maximumApkBytes = 300 * 1024 * 1024;

ClientUpgradeInstaller createClientUpgradeInstaller({http.Client? client}) =>
    _IoClientUpgradeInstaller(client ?? http.Client());

class _IoClientUpgradeInstaller implements ClientUpgradeInstaller {
  _IoClientUpgradeInstaller(this._client);

  final http.Client _client;

  @override
  bool get downloadsInApp => Platform.isAndroid;

  @override
  Future<void> install(
    ClientUpgradeDecision decision, {
    required void Function(ClientUpgradeInstallProgress progress) onProgress,
  }) async {
    final uri = Uri.tryParse(decision.downloadUrl);
    if (uri == null || (uri.scheme != 'https' && uri.scheme != 'http')) {
      throw const ClientUpgradeInstallException('更新地址无效，请联系管理员');
    }
    if (!Platform.isAndroid) {
      final opened = await launchUrl(uri, mode: LaunchMode.platformDefault);
      if (!opened) {
        throw const ClientUpgradeInstallException('更新地址暂时无法打开，请稍后重试');
      }
      return;
    }
    if (uri.scheme != 'https') {
      throw const ClientUpgradeInstallException('应用内更新只接受 HTTPS 下载地址');
    }

    final apk = await _downloadApk(decision, uri, onProgress);
    onProgress(
      const ClientUpgradeInstallProgress(
        phase: ClientUpgradeInstallPhase.requestingPermission,
      ),
    );
    var permission = await Permission.requestInstallPackages.status;
    if (!permission.isGranted) {
      permission = await Permission.requestInstallPackages.request();
    }
    if (!permission.isGranted) {
      throw const ClientUpgradeInstallException('请允许青蛙呱呱安装未知应用，然后再次点击“下载并安装”');
    }

    onProgress(
      const ClientUpgradeInstallProgress(
        phase: ClientUpgradeInstallPhase.openingInstaller,
      ),
    );
    final result = await OpenFilex.open(apk.path, type: _apkMimeType);
    if (result.type != ResultType.done) {
      throw ClientUpgradeInstallException(_openFailureMessage(result.type));
    }
  }

  Future<File> _downloadApk(
    ClientUpgradeDecision decision,
    Uri uri,
    void Function(ClientUpgradeInstallProgress progress) onProgress,
  ) async {
    final cache = await getTemporaryDirectory();
    final directory = Directory(
      '${cache.path}${Platform.pathSeparator}updates',
    );
    await directory.create(recursive: true);
    final version = decision.latestVersion.replaceAll(
      RegExp(r'[^0-9A-Za-z._-]'),
      '_',
    );
    final urlKey = sha256
        .convert(uri.toString().codeUnits)
        .toString()
        .substring(0, 12);
    final target = File(
      '${directory.path}${Platform.pathSeparator}qingwaguagua-$version-$urlKey.apk',
    );
    if (await _isValidApk(target)) {
      onProgress(
        ClientUpgradeInstallProgress(
          phase: ClientUpgradeInstallPhase.preparing,
          receivedBytes: await target.length(),
          totalBytes: await target.length(),
        ),
      );
      return target;
    }

    final partial = File('${target.path}.part');
    await partial.delete().catchError((_) => partial);
    IOSink? sink;
    try {
      final request = http.Request('GET', uri)
        ..headers['Accept'] = _apkMimeType;
      final response = await _client
          .send(request)
          .timeout(const Duration(seconds: 15));
      if (response.statusCode != HttpStatus.ok) {
        throw ClientUpgradeInstallException('安装包下载失败（${response.statusCode}）');
      }
      final finalUri = response.request?.url ?? uri;
      if (finalUri.scheme != 'https') {
        throw const ClientUpgradeInstallException('安装包下载发生了不安全的地址跳转');
      }
      final total = response.contentLength;
      if (total != null && total > _maximumApkBytes) {
        throw const ClientUpgradeInstallException('安装包超过 300 MB，已停止下载');
      }

      sink = partial.openWrite();
      var received = 0;
      onProgress(
        ClientUpgradeInstallProgress(
          phase: ClientUpgradeInstallPhase.downloading,
          totalBytes: total,
        ),
      );
      await for (final chunk in response.stream.timeout(
        const Duration(seconds: 30),
      )) {
        received += chunk.length;
        if (received > _maximumApkBytes) {
          throw const ClientUpgradeInstallException('安装包超过 300 MB，已停止下载');
        }
        sink.add(chunk);
        onProgress(
          ClientUpgradeInstallProgress(
            phase: ClientUpgradeInstallPhase.downloading,
            receivedBytes: received,
            totalBytes: total,
          ),
        );
      }
      await sink.flush();
      await sink.close();
      sink = null;
      if (total != null && received != total) {
        throw const ClientUpgradeInstallException('安装包下载不完整，请重新下载');
      }
      if (!await _isValidApk(partial)) {
        throw const ClientUpgradeInstallException('下载内容不是有效的 APK 安装包');
      }
      if (await target.exists()) await target.delete();
      await partial.rename(target.path);
      for (final entry in directory.listSync()) {
        if (entry is File && entry.path != target.path) {
          await entry.delete().catchError((_) => entry);
        }
      }
      onProgress(
        ClientUpgradeInstallProgress(
          phase: ClientUpgradeInstallPhase.preparing,
          receivedBytes: received,
          totalBytes: total,
        ),
      );
      return target;
    } on TimeoutException {
      throw const ClientUpgradeInstallException('安装包下载超时，请检查网络后重试');
    } on SocketException {
      throw const ClientUpgradeInstallException('安装包下载失败，请检查网络后重试');
    } finally {
      if (sink != null) {
        await sink.flush().catchError((_) {});
        await sink.close().catchError((_) {});
      }
      if (await partial.exists()) {
        await partial.delete().catchError((_) => partial);
      }
    }
  }

  Future<bool> _isValidApk(File file) async {
    if (!await file.exists()) return false;
    final length = await file.length();
    if (length < 4 || length > _maximumApkBytes) return false;
    final handle = await file.open();
    try {
      final magic = await handle.read(4);
      return magic.length == 4 &&
          magic[0] == 0x50 &&
          magic[1] == 0x4b &&
          ((magic[2] == 0x03 && magic[3] == 0x04) ||
              (magic[2] == 0x05 && magic[3] == 0x06) ||
              (magic[2] == 0x07 && magic[3] == 0x08));
    } finally {
      await handle.close();
    }
  }

  String _openFailureMessage(ResultType type) => switch (type) {
    ResultType.permissionDenied => '没有安装应用的权限，请在系统设置中允许后重试',
    ResultType.noAppToOpen => '系统中没有可用的 APK 安装程序',
    ResultType.fileNotFound => '下载的安装包已丢失，请重新下载',
    ResultType.error || ResultType.done => '无法打开系统安装程序，请稍后重试',
  };
}
