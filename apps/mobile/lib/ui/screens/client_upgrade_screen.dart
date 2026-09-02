import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_theme.dart';
import '../../core/client_upgrade.dart';
import '../../core/client_upgrade_installer.dart';

class ForcedUpgradeScreen extends StatefulWidget {
  const ForcedUpgradeScreen({
    super.key,
    required this.decision,
    required this.onRetry,
    this.installer,
  });

  final ClientUpgradeDecision decision;
  final Future<void> Function() onRetry;
  final ClientUpgradeInstaller? installer;

  @override
  State<ForcedUpgradeScreen> createState() => _ForcedUpgradeScreenState();
}

class _ForcedUpgradeScreenState extends State<ForcedUpgradeScreen> {
  late final ClientUpgradeInstaller installer;
  bool opening = false;
  bool retrying = false;
  String error = '';
  ClientUpgradeInstallProgress? progress;

  @override
  void initState() {
    super.initState();
    installer = widget.installer ?? createClientUpgradeInstaller();
  }

  Future<void> _open() async {
    if (opening) return;
    setState(() {
      opening = true;
      error = '';
      progress = null;
    });
    try {
      await installer.install(
        widget.decision,
        onProgress: (value) {
          if (mounted) setState(() => progress = value);
        },
      );
      if (!mounted) return;
      setState(() {
        opening = false;
      });
    } on ClientUpgradeInstallException catch (exception) {
      if (!mounted) return;
      setState(() {
        opening = false;
        error = exception.message;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        opening = false;
        error = '更新失败，请检查网络后重试';
      });
    }
  }

  Future<void> _retry() async {
    if (retrying) return;
    setState(() {
      retrying = true;
      error = '';
    });
    await widget.onRetry();
    if (mounted) setState(() => retrying = false);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: Theme.of(context).colorScheme.surface,
    body: SafeArea(
      child: Center(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(28),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 460),
            child: Column(
              children: [
                Container(
                  width: 78,
                  height: 78,
                  decoration: BoxDecoration(
                    color: LinliColors.brandGreen.withValues(alpha: .22),
                    borderRadius: BorderRadius.circular(24),
                  ),
                  child: const Icon(
                    CupertinoIcons.arrow_down_circle_fill,
                    color: LinliColors.navy,
                    size: 42,
                  ),
                ),
                const SizedBox(height: 24),
                Text(
                  '需要更新后继续使用',
                  textAlign: TextAlign.center,
                  style: Theme.of(context).textTheme.headlineSmall,
                ),
                const SizedBox(height: 10),
                Text(
                  '当前版本 ${widget.decision.currentVersion}，最低支持版本 ${widget.decision.minimumVersion}，最新版本 ${widget.decision.latestVersion}。',
                  textAlign: TextAlign.center,
                  style: Theme.of(context).textTheme.bodyMedium,
                ),
                if (widget.decision.releaseNotes.isNotEmpty) ...[
                  const SizedBox(height: 20),
                  Container(
                    width: double.infinity,
                    padding: const EdgeInsets.all(16),
                    decoration: BoxDecoration(
                      color: Theme.of(context).colorScheme.surfaceContainerHigh,
                      borderRadius: BorderRadius.circular(16),
                    ),
                    child: Text(widget.decision.releaseNotes),
                  ),
                ],
                if (error.isNotEmpty) ...[
                  const SizedBox(height: 14),
                  Text(
                    error,
                    textAlign: TextAlign.center,
                    style: const TextStyle(color: LinliColors.systemRed),
                  ),
                ],
                if (opening && installer.downloadsInApp) ...[
                  const SizedBox(height: 16),
                  LinearProgressIndicator(value: progress?.fraction),
                  const SizedBox(height: 8),
                  Text(
                    _upgradeActionLabel(
                      installer,
                      progress,
                      working: true,
                      externalIdleLabel: '立即更新',
                    ),
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ],
                const SizedBox(height: 24),
                SizedBox(
                  width: double.infinity,
                  child: FilledButton.icon(
                    onPressed: widget.decision.downloadUrl.isEmpty || opening
                        ? null
                        : _open,
                    icon: opening
                        ? const SizedBox.square(
                            dimension: 18,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(CupertinoIcons.arrow_down_circle),
                    label: Text(
                      _upgradeActionLabel(
                        installer,
                        progress,
                        working: opening,
                        externalIdleLabel: '立即更新',
                      ),
                    ),
                  ),
                ),
                const SizedBox(height: 8),
                TextButton.icon(
                  onPressed: retrying ? null : _retry,
                  icon: retrying
                      ? const SizedBox.square(
                          dimension: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(CupertinoIcons.refresh),
                  label: const Text('更新完成后重新检查'),
                ),
              ],
            ),
          ),
        ),
      ),
    ),
  );
}

Future<void> showOptionalUpgradeDialog(
  BuildContext context,
  ClientUpgradeDecision decision, {
  ClientUpgradeInstaller? upgradeInstaller,
}) async {
  final installer = upgradeInstaller ?? createClientUpgradeInstaller();
  var error = '';
  var working = false;
  ClientUpgradeInstallProgress? progress;
  await showDialog<void>(
    context: context,
    builder: (context) => StatefulBuilder(
      builder: (context, setState) => AlertDialog(
        title: const Text('发现新版本'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('最新版本 ${decision.latestVersion}'),
            if (decision.releaseNotes.isNotEmpty) ...[
              const SizedBox(height: 12),
              Text(decision.releaseNotes),
            ],
            if (error.isNotEmpty) ...[
              const SizedBox(height: 10),
              Text(error, style: const TextStyle(color: LinliColors.systemRed)),
            ],
            if (working && installer.downloadsInApp) ...[
              const SizedBox(height: 14),
              LinearProgressIndicator(value: progress?.fraction),
              const SizedBox(height: 8),
              Text(_upgradeActionLabel(installer, progress, working: true)),
            ],
          ],
        ),
        actions: [
          TextButton(
            onPressed: working ? null : () => Navigator.pop(context),
            child: const Text('稍后'),
          ),
          FilledButton(
            onPressed: decision.downloadUrl.isEmpty || working
                ? null
                : () async {
                    setState(() {
                      working = true;
                      error = '';
                      progress = null;
                    });
                    try {
                      await installer.install(
                        decision,
                        onProgress: (value) {
                          if (context.mounted) {
                            setState(() => progress = value);
                          }
                        },
                      );
                      if (context.mounted) Navigator.pop(context);
                    } on ClientUpgradeInstallException catch (exception) {
                      if (context.mounted) {
                        setState(() {
                          working = false;
                          error = exception.message;
                        });
                      }
                    } catch (_) {
                      if (context.mounted) {
                        setState(() {
                          working = false;
                          error = '更新失败，请检查网络后重试';
                        });
                      }
                    }
                  },
            child: Text(
              _upgradeActionLabel(installer, progress, working: working),
            ),
          ),
        ],
      ),
    ),
  );
}

String _upgradeActionLabel(
  ClientUpgradeInstaller installer,
  ClientUpgradeInstallProgress? progress, {
  required bool working,
  String externalIdleLabel = '去更新',
}) {
  if (!working) {
    return installer.downloadsInApp ? '下载并安装' : externalIdleLabel;
  }
  final fraction = progress?.fraction;
  return switch (progress?.phase) {
    ClientUpgradeInstallPhase.downloading =>
      fraction == null ? '正在下载…' : '正在下载 ${(fraction * 100).round()}%',
    ClientUpgradeInstallPhase.preparing => '正在校验安装包…',
    ClientUpgradeInstallPhase.requestingPermission => '等待安装授权…',
    ClientUpgradeInstallPhase.openingInstaller => '正在打开安装程序…',
    null => installer.downloadsInApp ? '准备下载…' : '正在打开…',
  };
}
