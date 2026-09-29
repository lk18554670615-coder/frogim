import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/tenant_password.dart';
import '../widgets/linli_widgets.dart';

/// Managed accounts never send passwords to a tenant-local reset endpoint.
class TenantChangePasswordScreen extends StatefulWidget {
  const TenantChangePasswordScreen({super.key, required this.controller});
  final AppController controller;

  @override
  State<TenantChangePasswordScreen> createState() =>
      _TenantChangePasswordState();
}

class _TenantChangePasswordState extends State<TenantChangePasswordScreen> {
  final _form = GlobalKey<FormState>();
  final _current = TextEditingController();
  final _next = TextEditingController();
  final _confirmation = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) unawaited(widget.controller.refreshAuthPolicy());
    });
  }

  @override
  void dispose() {
    _current.dispose();
    _next.dispose();
    _confirmation.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final controller = widget.controller;
    if (_busy ||
        controller.loading ||
        !controller.supportsTenantPasswordChange ||
        !(_form.currentState?.validate() ?? false)) {
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('确认修改登录密码？'),
        content: const Text(
          '平台将撤销此账号原有登录凭据，并等待企业完成撤权。'
          '本机会返回登录页，确认完成后再使用新密码登录。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            key: const Key('tenant-password-confirm'),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('确认修改'),
          ),
        ],
      ),
    );
    if (!mounted) return;
    if (confirmed != true) {
      setState(() => _busy = false);
      return;
    }
    final success = await controller.changeTenantPassword(
      _current.text,
      _next.text,
    );
    if (!mounted) return;
    setState(() {
      _busy = false;
      _error = success ? null : controller.error ?? '暂时无法修改密码';
    });
    if (success) {
      _current.clear();
      _next.clear();
      _confirmation.clear();
      Navigator.of(context).popUntil((route) => route.isFirst);
    }
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) {
      final enabled = !_busy && !widget.controller.loading;
      return PopScope(
        canPop: !_busy,
        child: Scaffold(
          appBar: const GlassAppBar(title: Text('修改登录密码')),
          body: Align(
            alignment: Alignment.topCenter,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 600),
              child: Form(
                key: _form,
                child: ListView(
                  padding: const EdgeInsets.all(20),
                  children: [
                    const Text(
                      '验证当前密码后更新平台登录密码。只需修改一次，所属企业不变。'
                      '忘记当前密码时，请联系企业管理员重置。',
                    ),
                    const SizedBox(height: 20),
                    TextFormField(
                      key: const Key('tenant-password-current'),
                      controller: _current,
                      enabled: enabled,
                      obscureText: true,
                      autocorrect: false,
                      enableSuggestions: false,
                      textInputAction: TextInputAction.next,
                      autofillHints: const [AutofillHints.password],
                      decoration: const InputDecoration(labelText: '当前密码'),
                      validator: (value) =>
                          (value ?? '').isEmpty ? '请输入当前密码' : null,
                    ),
                    const SizedBox(height: 18),
                    TextFormField(
                      key: const Key('tenant-password-new'),
                      controller: _next,
                      enabled: enabled,
                      obscureText: true,
                      autocorrect: false,
                      enableSuggestions: false,
                      textInputAction: TextInputAction.next,
                      autofillHints: const [AutofillHints.newPassword],
                      decoration: const InputDecoration(
                        labelText: '新密码',
                        helperText: '至少 8 个字符，最多 72 字节；同时遵循企业密码要求',
                        helperMaxLines: 3,
                      ),
                      validator: (value) {
                        final password = value ?? '';
                        if (password.runes.length < 8 ||
                            utf8.encode(password).length > 72) {
                          return '新密码至少 8 个字符且最多 72 字节';
                        }
                        return widget.controller.authPolicy.passwordError(
                          password,
                        );
                      },
                    ),
                    const SizedBox(height: 18),
                    TextFormField(
                      key: const Key('tenant-password-confirmation'),
                      controller: _confirmation,
                      enabled: enabled,
                      obscureText: true,
                      autocorrect: false,
                      enableSuggestions: false,
                      autofillHints: const [AutofillHints.newPassword],
                      decoration: const InputDecoration(labelText: '再次输入新密码'),
                      validator: (value) =>
                          value == _next.text ? null : '两次输入的密码不一致',
                      onFieldSubmitted: (_) => _submit(),
                    ),
                    const SizedBox(height: 20),
                    if (!widget.controller.supportsTenantPasswordChange) ...[
                      const Text('暂时无法确认平台改密能力，请刷新后再试。'),
                      TextButton(
                        onPressed: enabled
                            ? widget.controller.refreshAuthPolicy
                            : null,
                        child: const Text('刷新认证配置'),
                      ),
                    ],
                    if (_error != null) ...[
                      Text(
                        _error!,
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.error,
                        ),
                      ),
                      const SizedBox(height: 12),
                    ],
                    FilledButton(
                      key: const Key('tenant-password-submit'),
                      onPressed:
                          enabled &&
                              widget.controller.supportsTenantPasswordChange
                          ? _submit
                          : null,
                      child: Text(_busy ? '正在处理…' : '修改密码'),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      );
    },
  );
}

/// Manual reconciliation only: no password persistence, polling or auto-replay.
class TenantPasswordTaskNotice extends StatelessWidget {
  const TenantPasswordTaskNotice({super.key, required this.controller});
  final AppController controller;

  Future<void> _dismiss(BuildContext context) async {
    final accepted = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('移除本机进度提示？'),
        content: const Text(
          '这不会取消服务器上的密码任务。移除后不能在本机继续查询该任务，'
          '请确认已记住新密码；有疑问时联系企业管理员。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('保留'),
          ),
          TextButton(
            key: const Key('tenant-password-dismiss-confirm'),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('移除提示'),
          ),
        ],
      ),
    );
    if (accepted == true) {
      await controller.checkTenantPasswordChange(dismiss: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final progress = controller.tenantPasswordProgress;
    if (progress == null || controller.authenticated) {
      return const SizedBox.shrink();
    }
    final (title, detail) = switch (progress) {
      TenantPasswordProgress.unconfirmed => (
        '密码修改结果未确认',
        '请求可能已被受理。请查询进度，不会自动重复提交或保存密码。',
      ),
      TenantPasswordProgress.pending => (
        '密码正在更新',
        '平台已受理，正在等待企业完成撤权。确认完成后使用新密码登录。',
      ),
      TenantPasswordProgress.completed => ('密码已修改', '请使用新密码重新登录。'),
      TenantPasswordProgress.expired => (
        '密码进度查询已过期',
        '请尝试使用新密码登录；无法登录时联系企业管理员。',
      ),
    };
    return Card(
      key: const Key('tenant-password-notice'),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(title, style: Theme.of(context).textTheme.titleSmall),
            const SizedBox(height: 6),
            Text(detail),
            Wrap(
              spacing: 8,
              children: [
                if (progress == TenantPasswordProgress.pending ||
                    progress == TenantPasswordProgress.unconfirmed)
                  TextButton(
                    key: const Key('tenant-password-check'),
                    onPressed: controller.loading
                        ? null
                        : controller.checkTenantPasswordChange,
                    child: const Text('查询密码进度'),
                  ),
                TextButton(
                  key: const Key('tenant-password-dismiss'),
                  onPressed: controller.loading
                      ? null
                      : () => _dismiss(context),
                  child: const Text('移除本机提示'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
