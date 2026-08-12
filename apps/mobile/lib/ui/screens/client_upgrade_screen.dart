import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../core/app_theme.dart';
import '../../core/client_upgrade.dart';

Future<bool> openClientUpgrade(ClientUpgradeDecision decision) async {
  final uri = Uri.tryParse(decision.downloadUrl);
  if (uri == null || (uri.scheme != 'https' && uri.scheme != 'http')) {
    return false;
  }
  return launchUrl(uri, mode: LaunchMode.platformDefault);
}

class ForcedUpgradeScreen extends StatefulWidget {
  const ForcedUpgradeScreen({
    super.key,
    required this.decision,
    required this.onRetry,
  });

  final ClientUpgradeDecision decision;
  final Future<void> Function() onRetry;

  @override
  State<ForcedUpgradeScreen> createState() => _ForcedUpgradeScreenState();
}

class _ForcedUpgradeScreenState extends State<ForcedUpgradeScreen> {
  bool opening = false;
  bool retrying = false;
  String error = '';

  Future<void> _open() async {
    if (opening) return;
    setState(() {
      opening = true;
      error = '';
    });
    final opened = await openClientUpgrade(widget.decision);
    if (mounted) {
      setState(() {
        opening = false;
        if (!opened) error = '更新地址暂时无法打开，请稍后重试';
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
                    color: LinliColors.yellow.withValues(alpha: .22),
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
                    label: Text(opening ? '正在打开…' : '立即更新'),
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
  ClientUpgradeDecision decision,
) async {
  var error = '';
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
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('稍后'),
          ),
          FilledButton(
            onPressed: decision.downloadUrl.isEmpty
                ? null
                : () async {
                    if (await openClientUpgrade(decision)) {
                      if (context.mounted) Navigator.pop(context);
                    } else {
                      setState(() => error = '更新地址暂时无法打开');
                    }
                  },
            child: const Text('去更新'),
          ),
        ],
      ),
    ),
  );
}
