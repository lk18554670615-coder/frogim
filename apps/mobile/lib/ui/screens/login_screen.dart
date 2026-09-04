import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/auth_validation.dart';
import '../../core/models.dart';
import '../legal_documents.dart';
import '../widgets/linli_widgets.dart';

enum _LoginMode { code, password, qr }

String? inviteCodeFromQrPayload(String raw) {
  final value = raw.trim();
  final uri = Uri.tryParse(value);
  if (uri != null &&
      const {'qingwaguagua', 'linlitong'}.contains(uri.scheme) &&
      uri.host == 'register') {
    final code = uri.queryParameters['invite']?.trim();
    if (code != null && code.isNotEmpty) return code.toUpperCase();
  }
  if (RegExp(r'^[A-Za-z0-9_-]{6,20}$').hasMatch(value)) {
    return value.toUpperCase();
  }
  return null;
}

class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final formKey = GlobalKey<FormState>();
  final phoneFieldKey = GlobalKey<FormFieldState<String>>();
  final phone = TextEditingController();
  final credential = TextEditingController();
  final inviteCode = TextEditingController();
  _LoginMode mode = _LoginMode.code;
  bool obscurePassword = true;
  bool agreedToPolicies = false;
  bool codeRequested = false;
  String? codeRequestedPhone;
  bool inviteChecking = false;
  bool? inviteValid;
  QrLoginTicket? qrTicket;
  Timer? qrPollTimer;
  bool qrPolling = false;
  String? qrStatus;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      widget.controller.clearError(preserveAuthenticationFailure: true);
      if (!widget.controller.authPolicyLoaded) {
        unawaited(widget.controller.refreshAuthPolicy());
      }
    });
  }

  @override
  void dispose() {
    qrPollTimer?.cancel();
    phone.dispose();
    credential.dispose();
    inviteCode.dispose();
    super.dispose();
  }

  Future<void> _requestCode() async {
    if (!(phoneFieldKey.currentState?.validate() ?? false)) return;
    final requested = await widget.controller.requestCode(phone.text);
    if (!mounted || !requested) return;
    setState(() {
      codeRequested = true;
      codeRequestedPhone = phone.text.trim();
    });
  }

  void _phoneChanged(String value) {
    if (codeRequested && value.trim() != codeRequestedPhone) {
      setState(() {
        codeRequested = false;
        codeRequestedPhone = null;
      });
    }
    widget.controller.clearError();
  }

  Future<void> _validateLoginInviteCode() async {
    if (inviteCode.text.trim().isEmpty) {
      setState(() => inviteValid = null);
      return;
    }
    setState(() => inviteChecking = true);
    final valid = await widget.controller.validateInviteCode(inviteCode.text);
    if (!mounted) return;
    setState(() {
      inviteChecking = false;
      inviteValid = valid;
    });
  }

  Future<void> _scanLoginInviteCode() async {
    final result = await Navigator.of(context).push<String>(
      MaterialPageRoute(builder: (_) => const InviteCodeScannerScreen()),
    );
    if (!mounted || result == null) return;
    inviteCode.text = result;
    inviteValid = null;
    await _validateLoginInviteCode();
  }

  void _submit() {
    if (mode == _LoginMode.qr) return;
    if (!(formKey.currentState?.validate() ?? false)) return;
    if (!agreedToPolicies) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('请先阅读并同意用户协议和隐私政策')));
      return;
    }
    if (mode == _LoginMode.code) {
      widget.controller.login(
        phone.text,
        credential.text,
        inviteCode: inviteCode.text,
      );
    } else {
      widget.controller.passwordLogin(phone.text, credential.text);
    }
  }

  void _changeMode(_LoginMode value) {
    if (value == mode) return;
    setState(() {
      mode = value;
      credential.clear();
      obscurePassword = true;
      qrStatus = null;
    });
    widget.controller.clearError();
    if (value == _LoginMode.qr) {
      _createQrLogin();
    } else {
      qrPollTimer?.cancel();
    }
  }

  Future<void> _createQrLogin() async {
    qrPollTimer?.cancel();
    setState(() {
      qrTicket = null;
      qrStatus = '正在生成安全登录码…';
    });
    final ticket = await widget.controller.createQrLoginTicket(
      clientName: kIsWeb ? '青蛙呱呱网页版' : '青蛙呱呱桌面端',
    );
    if (!mounted || mode != _LoginMode.qr) return;
    if (ticket == null) {
      setState(() => qrStatus = widget.controller.error ?? '二维码生成失败');
      return;
    }
    setState(() {
      qrTicket = ticket;
      qrStatus = '等待手机确认';
    });
    qrPollTimer = Timer.periodic(
      const Duration(seconds: 2),
      (_) => _pollQrLogin(),
    );
  }

  Future<void> _pollQrLogin() async {
    final ticket = qrTicket;
    if (!mounted || ticket == null || qrPolling || mode != _LoginMode.qr) {
      return;
    }
    if (ticket.expired) {
      qrPollTimer?.cancel();
      setState(() => qrStatus = '二维码已过期，请刷新');
      return;
    }
    qrPolling = true;
    final loggedIn = await widget.controller.pollQrLoginTicket(ticket);
    qrPolling = false;
    if (!mounted || loggedIn) return;
    if (widget.controller.error != null) {
      qrPollTimer?.cancel();
      setState(() => qrStatus = widget.controller.error);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    body: AnimatedBuilder(
      animation: widget.controller,
      builder: (context, _) => LayoutBuilder(
        builder: (context, constraints) {
          if (useLinliDesktopLayout(constraints.maxWidth)) {
            return _buildDesktopLogin(context, constraints);
          }
          final horizontalPadding = constraints.maxWidth < 360 ? 16.0 : 24.0;
          return SafeArea(
            child: SingleChildScrollView(
              keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
              padding: EdgeInsets.fromLTRB(
                horizontalPadding,
                constraints.maxHeight >= 760 ? 44 : 24,
                horizontalPadding,
                28,
              ),
              child: Align(
                alignment: Alignment.topCenter,
                child: ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 390),
                  child: _buildLoginForm(context, showBrand: true),
                ),
              ),
            ),
          );
        },
      ),
    ),
  );

  Widget _buildDesktopLogin(BuildContext context, BoxConstraints viewport) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    final compactHeight = viewport.maxHeight < 820;
    final outerPadding = compactHeight ? 20.0 : 40.0;
    return ColoredBox(
      key: const Key('desktop-login-shell'),
      color: dark ? const Color(0xFF101613) : const Color(0xFFE9EDF1),
      child: SafeArea(
        child: Center(
          child: SingleChildScrollView(
            padding: EdgeInsets.all(outerPadding),
            child: ConstrainedBox(
              key: const Key('desktop-login-card'),
              constraints: BoxConstraints(
                maxWidth: 820,
                minHeight: compactHeight ? 0 : 620,
              ),
              child: DecoratedBox(
                decoration: BoxDecoration(
                  color: Theme.of(context).colorScheme.surfaceContainer,
                  borderRadius: BorderRadius.circular(14),
                  border: Border.all(
                    color: Theme.of(context).colorScheme.outline,
                  ),
                  boxShadow: [
                    BoxShadow(
                      color: Colors.black.withValues(alpha: dark ? .28 : .14),
                      blurRadius: 36,
                      offset: const Offset(0, 16),
                    ),
                  ],
                ),
                child: ClipRRect(
                  borderRadius: BorderRadius.circular(14),
                  child: IntrinsicHeight(
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        const SizedBox(
                          width: 280,
                          child: _DesktopLoginBrandPanel(),
                        ),
                        Expanded(
                          child: Padding(
                            padding: EdgeInsets.fromLTRB(
                              44,
                              compactHeight ? 32 : 44,
                              44,
                              compactHeight ? 28 : 36,
                            ),
                            child: Center(
                              child: _buildLoginForm(context, showBrand: false),
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildLoginForm(BuildContext context, {required bool showBrand}) {
    final effectiveMode = showBrand && mode == _LoginMode.qr
        ? _LoginMode.code
        : mode;
    final qrMode = effectiveMode == _LoginMode.qr;
    return Form(
      key: formKey,
      autovalidateMode: AutovalidateMode.disabled,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (showBrand) ...[
            Center(
              child: Image.asset(
                'assets/brand/qingwaguagua-mark-transparent.png',
                width: 88,
                height: 88,
                semanticLabel: '青蛙呱呱应用图标',
              ),
            ),
            const SizedBox(height: 18),
          ],
          Text(
            showBrand ? '登录青蛙呱呱' : '登录',
            textAlign: showBrand ? TextAlign.center : TextAlign.left,
            style: Theme.of(context).textTheme.headlineMedium?.copyWith(
              fontSize: showBrand ? 26 : null,
              fontWeight: FontWeight.w700,
              letterSpacing: -.3,
            ),
          ),
          if (showBrand) ...[
            const SizedBox(height: 8),
            Text(
              qrMode ? '使用手机确认后安全登录' : '使用手机号继续',
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodyMedium,
            ),
          ],
          SizedBox(height: showBrand ? 26 : 24),
          CupertinoSlidingSegmentedControl<_LoginMode>(
            key: const Key('login-mode-control'),
            groupValue: effectiveMode,
            backgroundColor: Theme.of(context).brightness == Brightness.dark
                ? LinliColors.darkSurfaceElevated
                : LinliColors.brandMintStrong,
            thumbColor: Theme.of(context).colorScheme.surfaceContainer,
            children: {
              _LoginMode.code: _LoginModeLabel(
                label: showBrand ? '验证码登录' : '验证码',
                selected: effectiveMode == _LoginMode.code,
                compact: !showBrand,
              ),
              _LoginMode.password: _LoginModeLabel(
                label: showBrand ? '密码登录' : '密码',
                selected: effectiveMode == _LoginMode.password,
                compact: !showBrand,
              ),
              if (!showBrand)
                _LoginMode.qr: _LoginModeLabel(
                  label: '扫码登录',
                  selected: effectiveMode == _LoginMode.qr,
                  compact: true,
                ),
            },
            onValueChanged: (value) {
              if (value != null) _changeMode(value);
            },
          ),
          const SizedBox(height: 16),
          if (qrMode)
            _buildQrLoginPanel(context)
          else ...[
            TextFormField(
              key: phoneFieldKey,
              autovalidateMode: AutovalidateMode.onUnfocus,
              controller: phone,
              keyboardType: TextInputType.phone,
              textInputAction: TextInputAction.next,
              autofillHints: const [AutofillHints.telephoneNumber],
              onChanged: _phoneChanged,
              onTapOutside: (_) =>
                  FocusManager.instance.primaryFocus?.unfocus(),
              decoration: const InputDecoration(
                labelText: '手机号',
                prefixIcon: Icon(CupertinoIcons.phone),
              ),
              validator: (value) =>
                  _validPhone(value ?? '') ? null : '请输入有效手机号',
            ),
            const SizedBox(height: 12),
            TextFormField(
              key: Key(
                effectiveMode == _LoginMode.code
                    ? 'code-field'
                    : 'password-field',
              ),
              autovalidateMode: AutovalidateMode.onUnfocus,
              controller: credential,
              keyboardType: effectiveMode == _LoginMode.code
                  ? TextInputType.number
                  : TextInputType.visiblePassword,
              obscureText:
                  effectiveMode == _LoginMode.password && obscurePassword,
              autofillHints: effectiveMode == _LoginMode.code
                  ? const [AutofillHints.oneTimeCode]
                  : const [AutofillHints.password],
              onChanged: (_) => widget.controller.clearError(),
              onTapOutside: (_) =>
                  FocusManager.instance.primaryFocus?.unfocus(),
              onFieldSubmitted: (_) => _submit(),
              decoration: InputDecoration(
                labelText: effectiveMode == _LoginMode.code ? '验证码' : '密码',
                prefixIcon: const Icon(CupertinoIcons.lock),
                suffixIcon: effectiveMode == _LoginMode.code
                    ? TextButton(
                        key: const Key('request-code-button'),
                        onPressed: widget.controller.loading
                            ? null
                            : _requestCode,
                        child: Text(codeRequested ? '重新获取' : '获取验证码'),
                      )
                    : IconButton(
                        tooltip: obscurePassword ? '显示密码' : '隐藏密码',
                        onPressed: () =>
                            setState(() => obscurePassword = !obscurePassword),
                        icon: Icon(
                          obscurePassword
                              ? CupertinoIcons.eye
                              : CupertinoIcons.eye_slash,
                        ),
                      ),
              ),
              validator: (value) {
                final text = value ?? '';
                if (effectiveMode == _LoginMode.code) {
                  return text.trim().length >= 4 ? null : '请输入验证码';
                }
                return text.isNotEmpty ? null : '请输入密码';
              },
            ),
            if (effectiveMode == _LoginMode.code &&
                widget.controller.authPolicy.invitationEnabled) ...[
              const SizedBox(height: 12),
              TextFormField(
                key: const Key('login-invite-code'),
                controller: inviteCode,
                textCapitalization: TextCapitalization.characters,
                onChanged: (_) {
                  setState(() => inviteValid = null);
                  widget.controller.clearError();
                },
                decoration: InputDecoration(
                  labelText: widget.controller.authPolicy.invitationRequired
                      ? '邀请码（新用户必填）'
                      : '邀请码（选填）',
                  helperText: inviteValid == true
                      ? '邀请码有效'
                      : inviteValid == false
                      ? '邀请码无效、已停用或已失效'
                      : '仅首次登录创建账号时使用，已有账号可留空',
                  errorText: inviteValid == false ? '请检查邀请码' : null,
                  prefixIcon: const Icon(CupertinoIcons.ticket),
                  suffixIcon: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      IconButton(
                        tooltip: '校验邀请码',
                        onPressed: inviteChecking
                            ? null
                            : _validateLoginInviteCode,
                        icon: inviteChecking
                            ? const SizedBox.square(
                                dimension: 18,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                ),
                              )
                            : const Icon(CupertinoIcons.check_mark_circled),
                      ),
                      IconButton(
                        tooltip: '扫描邀请码',
                        onPressed: _scanLoginInviteCode,
                        icon: const Icon(CupertinoIcons.qrcode_viewfinder),
                      ),
                    ],
                  ),
                ),
              ),
            ],
            if (effectiveMode == _LoginMode.password)
              Align(
                alignment: Alignment.centerRight,
                child: TextButton(
                  key: const Key('forgot-password'),
                  onPressed: () {
                    widget.controller.clearError();
                    Navigator.of(context).push(
                      MaterialPageRoute(
                        builder: (_) => ResetPasswordScreen(
                          controller: widget.controller,
                          initialPhone: phone.text,
                        ),
                      ),
                    );
                  },
                  child: const Text('忘记密码？'),
                ),
              ),
            if (widget.controller.error != null) ...[
              const SizedBox(height: 8),
              _InlineAuthError(
                key: const Key('login-error'),
                message: widget.controller.error!,
              ),
            ],
            const SizedBox(height: 10),
            _PolicyConsent(
              key: const Key('login-policy-consent'),
              value: agreedToPolicies,
              onChanged: (value) => setState(() => agreedToPolicies = value),
            ),
            const SizedBox(height: 12),
            FilledButton(
              key: const Key('login-button'),
              onPressed: widget.controller.loading ? null : _submit,
              child: widget.controller.loading
                  ? const SizedBox.square(
                      key: Key('login-spinner'),
                      dimension: 20,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : Text(effectiveMode == _LoginMode.code ? '验证码登录' : '密码登录'),
            ),
            const SizedBox(height: 8),
            if (!widget.controller.authPolicyAvailable ||
                widget.controller.authPolicy.registrationEnabled)
              OutlinedButton(
                key: const Key('open-register'),
                onPressed: () {
                  widget.controller.clearError();
                  Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => RegisterScreen(
                        controller: widget.controller,
                        initialPhone: phone.text,
                      ),
                    ),
                  );
                },
                child: const Text('注册新账号'),
              )
            else
              const _RegistrationClosedNotice(),
          ],
        ],
      ),
    );
  }

  Widget _buildQrLoginPanel(BuildContext context) {
    final ticket = qrTicket;
    final canRefresh =
        ticket?.expired == true ||
        (ticket == null && !widget.controller.loading);
    return Column(
      key: const Key('qr-login-panel'),
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 214,
          height: 214,
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: Colors.white,
            borderRadius: BorderRadius.circular(18),
            border: Border.all(color: const Color(0xFFDCE6E1)),
            boxShadow: const [
              BoxShadow(
                color: Color(0x160F172A),
                blurRadius: 24,
                offset: Offset(0, 10),
              ),
            ],
          ),
          child: ticket == null
              ? const Center(child: CupertinoActivityIndicator(radius: 14))
              : Semantics(
                  label: '青蛙呱呱安全登录二维码',
                  image: true,
                  child: QrImageView(
                    key: const Key('qr-login-code'),
                    data: ticket.qrPayload,
                    version: QrVersions.auto,
                    eyeStyle: const QrEyeStyle(
                      eyeShape: QrEyeShape.square,
                      color: LinliColors.navy,
                    ),
                    dataModuleStyle: const QrDataModuleStyle(
                      dataModuleShape: QrDataModuleShape.square,
                      color: LinliColors.navy,
                    ),
                    padding: EdgeInsets.zero,
                  ),
                ),
        ),
        const SizedBox(height: 18),
        Row(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(
              canRefresh
                  ? CupertinoIcons.exclamationmark_circle
                  : CupertinoIcons.shield_lefthalf_fill,
              size: 17,
              color: canRefresh
                  ? LinliColors.systemRed
                  : LinliColors.brandGreenDeep,
            ),
            const SizedBox(width: 7),
            Flexible(
              child: Text(
                qrStatus ?? '等待手机确认',
                key: const Key('qr-login-status'),
                textAlign: TextAlign.center,
                style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                  fontWeight: FontWeight.w600,
                  color: canRefresh ? LinliColors.systemRed : null,
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: 10),
        Text(
          '手机打开青蛙呱呱，点击右上角“+”后选择“扫一扫”',
          textAlign: TextAlign.center,
          style: Theme.of(context).textTheme.bodySmall?.copyWith(height: 1.5),
        ),
        if (canRefresh) ...[
          const SizedBox(height: 8),
          TextButton.icon(
            key: const Key('refresh-qr-login'),
            onPressed: widget.controller.loading ? null : _createQrLogin,
            icon: const Icon(CupertinoIcons.refresh, size: 17),
            label: const Text('刷新二维码'),
          ),
        ],
      ],
    );
  }
}

class _LoginModeLabel extends StatelessWidget {
  const _LoginModeLabel({
    required this.label,
    required this.selected,
    required this.compact,
  });

  final String label;
  final bool selected;
  final bool compact;

  @override
  Widget build(BuildContext context) => Padding(
    padding: EdgeInsets.symmetric(
      horizontal: compact ? 12 : 16,
      vertical: compact ? 13 : 14,
    ),
    child: Text(
      label,
      style: TextStyle(
        color: selected
            ? Theme.of(context).colorScheme.onSurface
            : LinliColors.preview,
        fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
      ),
    ),
  );
}

class _InlineAuthError extends StatelessWidget {
  const _InlineAuthError({super.key, required this.message});

  final String message;

  @override
  Widget build(BuildContext context) => Semantics(
    liveRegion: true,
    label: '操作提示：$message',
    child: Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: LinliColors.systemRed.withValues(alpha: .08),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: LinliColors.systemRed.withValues(alpha: .18)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Padding(
            padding: EdgeInsets.only(top: 1),
            child: Icon(
              CupertinoIcons.exclamationmark_circle_fill,
              size: 16,
              color: LinliColors.systemRed,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              message,
              style: Theme.of(context).textTheme.bodySmall?.copyWith(
                color: LinliColors.systemRed,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
        ],
      ),
    ),
  );
}

class _DesktopLoginBrandPanel extends StatelessWidget {
  const _DesktopLoginBrandPanel();

  @override
  Widget build(BuildContext context) => ColoredBox(
    key: const Key('desktop-login-brand-panel'),
    color: LinliColors.navy,
    child: Padding(
      padding: const EdgeInsets.all(36),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          Image.asset(
            'assets/brand/qingwaguagua-mark-transparent.png',
            width: 96,
            height: 96,
            semanticLabel: '青蛙呱呱应用图标',
          ),
          const SizedBox(height: 24),
          const Text(
            '青蛙呱呱',
            style: TextStyle(
              color: Colors.white,
              fontSize: 24,
              height: 1.2,
              fontWeight: FontWeight.w700,
              letterSpacing: -.2,
            ),
          ),
          const SizedBox(height: 8),
          const Text(
            '桌面网页版',
            style: TextStyle(
              color: Color(0xFFAEC4BA),
              fontSize: 13,
              height: 1.4,
              letterSpacing: .6,
            ),
          ),
        ],
      ),
    ),
  );
}

class RegisterScreen extends StatefulWidget {
  const RegisterScreen({
    super.key,
    required this.controller,
    this.initialPhone = '',
  });

  final AppController controller;
  final String initialPhone;

  @override
  State<RegisterScreen> createState() => _RegisterScreenState();
}

class InviteCodeScannerScreen extends StatefulWidget {
  const InviteCodeScannerScreen({super.key});

  @override
  State<InviteCodeScannerScreen> createState() =>
      _InviteCodeScannerScreenState();
}

class _InviteCodeScannerScreenState extends State<InviteCodeScannerScreen> {
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

  void _detected(BarcodeCapture capture) {
    if (handling) return;
    for (final barcode in capture.barcodes) {
      final code = inviteCodeFromQrPayload(barcode.rawValue ?? '');
      if (code == null) continue;
      handling = true;
      Navigator.of(context).pop(code);
      return;
    }
    if (mounted) setState(() => error = '这不是有效的邀请码二维码');
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: Colors.black,
    appBar: AppBar(
      title: const Text('扫描邀请码'),
      backgroundColor: Colors.black,
      foregroundColor: Colors.white,
    ),
    body: Stack(
      fit: StackFit.expand,
      children: [
        MobileScanner(controller: scanner, onDetect: _detected),
        Center(
          child: Container(
            width: 250,
            height: 250,
            decoration: BoxDecoration(
              border: Border.all(color: LinliColors.brandGreen, width: 3),
              borderRadius: BorderRadius.circular(24),
            ),
          ),
        ),
        Positioned(
          left: 24,
          right: 24,
          bottom: 42,
          child: Text(
            error ?? '将邀请码二维码放入框内',
            textAlign: TextAlign.center,
            style: TextStyle(
              color: error == null ? Colors.white : Colors.redAccent,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
      ],
    ),
  );
}

class _RegisterScreenState extends State<RegisterScreen> {
  final formKey = GlobalKey<FormState>();
  final phoneFieldKey = GlobalKey<FormFieldState<String>>();
  late final phone = TextEditingController(text: widget.initialPhone);
  final code = TextEditingController();
  final name = TextEditingController();
  final password = TextEditingController();
  final confirmPassword = TextEditingController();
  final inviteCode = TextEditingController();
  bool? inviteValid;
  bool inviteChecking = false;
  bool agreedToPolicies = false;
  bool codeRequested = false;
  String? codeRequestedPhone;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      widget.controller.clearError();
      if (!widget.controller.authPolicyLoaded) {
        unawaited(widget.controller.refreshAuthPolicy());
      }
    });
  }

  @override
  void dispose() {
    phone.dispose();
    code.dispose();
    name.dispose();
    password.dispose();
    confirmPassword.dispose();
    inviteCode.dispose();
    super.dispose();
  }

  Future<void> _register() async {
    if (!(formKey.currentState?.validate() ?? false)) return;
    if (!agreedToPolicies) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('请先阅读并同意用户协议和隐私政策')));
      return;
    }
    if (inviteCode.text.trim().isNotEmpty && inviteValid != true) {
      final valid = await _validateInviteCode();
      if (!valid) return;
    }
    await widget.controller.registerAccount(
      phone: phone.text,
      code: code.text,
      password: password.text,
      name: name.text,
      inviteCode: inviteCode.text,
    );
    if (mounted && widget.controller.authenticated) {
      Navigator.of(context).pop();
    }
  }

  Future<bool> _validateInviteCode() async {
    if (inviteCode.text.trim().isEmpty) {
      setState(() => inviteValid = null);
      return !widget.controller.authPolicy.invitationRequired;
    }
    setState(() => inviteChecking = true);
    final valid = await widget.controller.validateInviteCode(inviteCode.text);
    if (mounted) {
      setState(() {
        inviteChecking = false;
        inviteValid = valid;
      });
    }
    return valid;
  }

  Future<void> _scanInviteCode() async {
    final result = await Navigator.of(context).push<String>(
      MaterialPageRoute(builder: (_) => const InviteCodeScannerScreen()),
    );
    if (!mounted || result == null) return;
    inviteCode.text = result;
    setState(() => inviteValid = null);
    await _validateInviteCode();
  }

  Future<void> _requestCode() async {
    if (!(phoneFieldKey.currentState?.validate() ?? false)) return;
    final requested = await widget.controller.requestCode(phone.text);
    if (!mounted || !requested) return;
    setState(() {
      codeRequested = true;
      codeRequestedPhone = phone.text.trim();
    });
  }

  void _phoneChanged(String value) {
    if (codeRequested && value.trim() != codeRequestedPhone) {
      setState(() {
        codeRequested = false;
        codeRequestedPhone = null;
      });
    }
    widget.controller.clearError();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: _AuthBrandAppBarTitle()),
    body: AnimatedBuilder(
      animation: widget.controller,
      builder: (context, _) {
        if (widget.controller.authPolicyAvailable &&
            !widget.controller.authPolicy.registrationEnabled) {
          return const _RegistrationClosedView();
        }
        return Form(
          key: formKey,
          autovalidateMode: AutovalidateMode.disabled,
          child: ListView(
            keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
            padding: EdgeInsets.fromLTRB(
              MediaQuery.sizeOf(context).width > 528
                  ? (MediaQuery.sizeOf(context).width - 480) / 2
                  : 24,
              24,
              MediaQuery.sizeOf(context).width > 528
                  ? (MediaQuery.sizeOf(context).width - 480) / 2
                  : 24,
              36,
            ),
            children: [
              const _AuthFlowHeader(
                title: '创建账号',
                subtitle: '验证手机号并设置昵称与登录密码。',
              ),
              const SizedBox(height: 28),
              const _AuthSectionLabel(label: '验证手机号'),
              const SizedBox(height: 10),
              _phoneField(
                phone,
                fieldKey: phoneFieldKey,
                onChanged: _phoneChanged,
              ),
              const SizedBox(height: 14),
              _codeField(
                code,
                onRequest: _requestCode,
                requestLabel: codeRequested ? '重新获取' : '获取验证码',
                requestEnabled: !widget.controller.loading,
                onChanged: (_) => widget.controller.clearError(),
              ),
              if (codeRequested) const _AuthCodeSentNotice(),
              const SizedBox(height: 24),
              const _AuthSectionLabel(label: '完善账号资料'),
              const SizedBox(height: 10),
              TextFormField(
                key: const Key('register-name'),
                autovalidateMode: AutovalidateMode.onUnfocus,
                controller: name,
                maxLength: 40,
                textInputAction: TextInputAction.next,
                onTapOutside: (_) =>
                    FocusManager.instance.primaryFocus?.unfocus(),
                onChanged: (_) => widget.controller.clearError(),
                decoration: const InputDecoration(
                  labelText: '昵称',
                  hintText: '例如：小青蛙',
                  helperText: '最多 40 个字符，之后可在个人资料中修改',
                  counterText: '',
                ),
                validator: (value) =>
                    (value?.trim().isNotEmpty ?? false) ? null : '请输入昵称',
              ),
              if (widget.controller.authPolicy.invitationEnabled) ...[
                const SizedBox(height: 14),
                TextFormField(
                  key: const Key('register-invite-code'),
                  controller: inviteCode,
                  textCapitalization: TextCapitalization.characters,
                  onChanged: (_) => setState(() => inviteValid = null),
                  decoration: InputDecoration(
                    labelText: widget.controller.authPolicy.invitationRequired
                        ? '邀请码'
                        : '邀请码（选填）',
                    helperText: inviteValid == true
                        ? '邀请码有效'
                        : inviteValid == false
                        ? '邀请码无效、已停用或已失效'
                        : '邀请码不区分大小写',
                    errorText: inviteValid == false ? '请检查邀请码' : null,
                    prefixIcon: const Icon(CupertinoIcons.ticket),
                    suffixIcon: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        IconButton(
                          tooltip: '校验邀请码',
                          onPressed: inviteChecking
                              ? null
                              : _validateInviteCode,
                          icon: inviteChecking
                              ? const SizedBox.square(
                                  dimension: 18,
                                  child: CircularProgressIndicator(
                                    strokeWidth: 2,
                                  ),
                                )
                              : const Icon(CupertinoIcons.check_mark_circled),
                        ),
                        IconButton(
                          tooltip: '扫描邀请码',
                          onPressed: _scanInviteCode,
                          icon: const Icon(CupertinoIcons.qrcode_viewfinder),
                        ),
                      ],
                    ),
                  ),
                  validator: (value) =>
                      widget.controller.authPolicy.invitationRequired &&
                          (value?.trim().isEmpty ?? true)
                      ? '请输入邀请码'
                      : null,
                ),
              ],
              const SizedBox(height: 14),
              _AuthPasswordField(
                fieldKey: const Key('register-password'),
                controller: password,
                label: '登录密码',
                helperText: widget.controller.authPolicy.passwordHelperText,
                onChanged: (_) => widget.controller.clearError(),
                validator: (value) =>
                    widget.controller.authPolicy.passwordError(value ?? ''),
              ),
              const SizedBox(height: 14),
              _AuthPasswordField(
                fieldKey: const Key('register-confirm-password'),
                controller: confirmPassword,
                label: '确认密码',
                textInputAction: TextInputAction.done,
                onChanged: (_) => widget.controller.clearError(),
                onFieldSubmitted: (_) => _register(),
                validator: (value) {
                  if (value?.isEmpty ?? true) return '请再次输入登录密码';
                  return value == password.text ? null : '两次输入的密码不一致';
                },
              ),
              const SizedBox(height: 18),
              _PolicyConsent(
                key: const Key('register-policy-consent'),
                value: agreedToPolicies,
                onChanged: (value) => setState(() => agreedToPolicies = value),
              ),
              if (widget.controller.error != null) ...[
                const SizedBox(height: 12),
                _InlineAuthError(message: widget.controller.error!),
              ],
              const SizedBox(height: 18),
              FilledButton(
                key: const Key('register-submit'),
                onPressed: widget.controller.loading ? null : _register,
                child: widget.controller.loading
                    ? const SizedBox.square(
                        dimension: 20,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('注册并登录'),
              ),
              const SizedBox(height: 8),
              TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: const Text('已有账号，返回登录'),
              ),
            ],
          ),
        );
      },
    ),
  );
}

class _PolicyConsent extends StatelessWidget {
  const _PolicyConsent({
    super.key,
    required this.value,
    required this.onChanged,
  });

  final bool value;
  final ValueChanged<bool> onChanged;

  @override
  Widget build(BuildContext context) {
    final policyTextStyle =
        Theme.of(context).textTheme.bodySmall ??
        DefaultTextStyle.of(context).style;
    final linkStyle = TextButton.styleFrom(
      minimumSize: const Size(0, 48),
      padding: const EdgeInsets.symmetric(horizontal: 1),
      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
      textStyle: policyTextStyle.copyWith(fontWeight: FontWeight.w600),
    );

    return Semantics(
      checked: value,
      label: '同意用户协议和隐私政策',
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          SizedBox.square(
            dimension: 48,
            child: Checkbox(
              key: const Key('policy-consent-checkbox'),
              value: value,
              visualDensity: VisualDensity.compact,
              materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
              onChanged: (next) => onChanged(next ?? false),
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: DefaultTextStyle(
              style: policyTextStyle,
              child: Wrap(
                spacing: 0,
                runSpacing: 4,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  const SizedBox(
                    height: 48,
                    child: Align(
                      widthFactor: 1,
                      alignment: Alignment.center,
                      child: Text('我已阅读并同意'),
                    ),
                  ),
                  TextButton(
                    style: linkStyle,
                    onPressed: () =>
                        showLegalDocument(context, LegalDocument.terms),
                    child: const Text('用户协议'),
                  ),
                  const SizedBox(
                    height: 48,
                    child: Align(
                      widthFactor: 1,
                      alignment: Alignment.center,
                      child: Text('和'),
                    ),
                  ),
                  TextButton(
                    style: linkStyle,
                    onPressed: () =>
                        showLegalDocument(context, LegalDocument.privacy),
                    child: const Text('隐私政策'),
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class ResetPasswordScreen extends StatefulWidget {
  const ResetPasswordScreen({
    super.key,
    required this.controller,
    this.initialPhone = '',
  });

  final AppController controller;
  final String initialPhone;

  @override
  State<ResetPasswordScreen> createState() => _ResetPasswordScreenState();
}

class _ResetPasswordScreenState extends State<ResetPasswordScreen> {
  final formKey = GlobalKey<FormState>();
  final phoneFieldKey = GlobalKey<FormFieldState<String>>();
  late final phone = TextEditingController(text: widget.initialPhone);
  final code = TextEditingController();
  final password = TextEditingController();
  bool codeRequested = false;
  String? codeRequestedPhone;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      widget.controller.clearError();
      if (!widget.controller.authPolicyLoaded) {
        unawaited(widget.controller.refreshAuthPolicy());
      }
    });
  }

  @override
  void dispose() {
    phone.dispose();
    code.dispose();
    password.dispose();
    super.dispose();
  }

  Future<void> _request() async {
    if (!(phoneFieldKey.currentState?.validate() ?? false)) return;
    final success = await widget.controller.requestResetCode(phone.text);
    if (!mounted || !success) return;
    setState(() {
      codeRequested = true;
      codeRequestedPhone = phone.text.trim();
    });
  }

  void _phoneChanged(String value) {
    if (codeRequested && value.trim() != codeRequestedPhone) {
      setState(() {
        codeRequested = false;
        codeRequestedPhone = null;
      });
    }
    widget.controller.clearError();
  }

  Future<void> _reset() async {
    if (!(formKey.currentState?.validate() ?? false)) return;
    final success = await widget.controller.resetPassword(
      phone: phone.text,
      code: code.text,
      password: password.text,
    );
    if (!mounted || !success) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('密码已重置，请使用新密码登录')));
    Navigator.pop(context);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: _AuthBrandAppBarTitle()),
    body: AnimatedBuilder(
      animation: widget.controller,
      builder: (context, _) {
        return Form(
          key: formKey,
          autovalidateMode: AutovalidateMode.disabled,
          child: ListView(
            keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
            padding: EdgeInsets.fromLTRB(
              MediaQuery.sizeOf(context).width > 528
                  ? (MediaQuery.sizeOf(context).width - 480) / 2
                  : 24,
              24,
              MediaQuery.sizeOf(context).width > 528
                  ? (MediaQuery.sizeOf(context).width - 480) / 2
                  : 24,
              36,
            ),
            children: [
              const _AuthFlowHeader(title: '找回密码', subtitle: '验证绑定手机号后设置新密码。'),
              const SizedBox(height: 28),
              const _AuthSectionLabel(label: '验证账号归属'),
              const SizedBox(height: 10),
              _phoneField(
                phone,
                fieldKey: phoneFieldKey,
                onChanged: _phoneChanged,
              ),
              const SizedBox(height: 14),
              _codeField(
                code,
                onRequest: _request,
                requestLabel: codeRequested ? '重新获取' : '获取验证码',
                requestEnabled: !widget.controller.loading,
                onChanged: (_) => widget.controller.clearError(),
              ),
              if (codeRequested) const _AuthCodeSentNotice(),
              const SizedBox(height: 24),
              const _AuthSectionLabel(label: '设置新密码'),
              const SizedBox(height: 10),
              _AuthPasswordField(
                fieldKey: const Key('reset-password'),
                controller: password,
                label: '新密码',
                helperText: widget.controller.authPolicy.passwordHelperText,
                textInputAction: TextInputAction.done,
                onChanged: (_) => widget.controller.clearError(),
                validator: (value) =>
                    widget.controller.authPolicy.passwordError(value ?? ''),
                onFieldSubmitted: (_) => _reset(),
              ),
              const SizedBox(height: 14),
              const _AuthSecurityNotice(),
              if (widget.controller.error != null) ...[
                const SizedBox(height: 12),
                _InlineAuthError(message: widget.controller.error!),
              ],
              const SizedBox(height: 18),
              FilledButton(
                key: const Key('reset-submit'),
                onPressed: widget.controller.loading ? null : _reset,
                child: widget.controller.loading
                    ? const SizedBox.square(
                        dimension: 20,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('确认重置密码'),
              ),
              const SizedBox(height: 8),
              TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: const Text('返回登录'),
              ),
            ],
          ),
        );
      },
    ),
  );
}

TextFormField _phoneField(
  TextEditingController controller, {
  Key? fieldKey,
  ValueChanged<String>? onChanged,
}) => TextFormField(
  key: fieldKey ?? const Key('auth-phone'),
  autovalidateMode: AutovalidateMode.onUnfocus,
  controller: controller,
  keyboardType: TextInputType.phone,
  textInputAction: TextInputAction.next,
  autofillHints: const [AutofillHints.telephoneNumber],
  onChanged: onChanged,
  onTapOutside: (_) => FocusManager.instance.primaryFocus?.unfocus(),
  decoration: const InputDecoration(labelText: '手机号'),
  validator: (value) => _validPhone(value ?? '') ? null : '请输入有效手机号',
);

TextFormField _codeField(
  TextEditingController controller, {
  required VoidCallback onRequest,
  String requestLabel = '获取验证码',
  bool requestEnabled = true,
  ValueChanged<String>? onChanged,
}) => TextFormField(
  key: const Key('auth-code'),
  autovalidateMode: AutovalidateMode.onUnfocus,
  controller: controller,
  keyboardType: TextInputType.number,
  textInputAction: TextInputAction.next,
  autofillHints: const [AutofillHints.oneTimeCode],
  onChanged: onChanged,
  onTapOutside: (_) => FocusManager.instance.primaryFocus?.unfocus(),
  decoration: InputDecoration(
    labelText: '验证码',
    suffixIcon: TextButton(
      onPressed: requestEnabled ? onRequest : null,
      child: Text(requestLabel),
    ),
  ),
  validator: (value) => (value?.trim().length ?? 0) >= 4 ? null : '请输入验证码',
);

class _AuthPasswordField extends StatefulWidget {
  const _AuthPasswordField({
    required this.fieldKey,
    required this.controller,
    required this.label,
    this.helperText,
    this.textInputAction = TextInputAction.next,
    this.onFieldSubmitted,
    this.onChanged,
    this.validator,
  });

  final Key fieldKey;
  final TextEditingController controller;
  final String label;
  final String? helperText;
  final TextInputAction textInputAction;
  final ValueChanged<String>? onFieldSubmitted;
  final ValueChanged<String>? onChanged;
  final FormFieldValidator<String>? validator;

  @override
  State<_AuthPasswordField> createState() => _AuthPasswordFieldState();
}

class _AuthPasswordFieldState extends State<_AuthPasswordField> {
  bool obscure = true;

  @override
  Widget build(BuildContext context) => TextFormField(
    key: widget.fieldKey,
    autovalidateMode: AutovalidateMode.onUnfocus,
    controller: widget.controller,
    obscureText: obscure,
    keyboardType: TextInputType.visiblePassword,
    textInputAction: widget.textInputAction,
    autofillHints: const [AutofillHints.newPassword],
    onFieldSubmitted: widget.onFieldSubmitted,
    onChanged: widget.onChanged,
    onTapOutside: (_) => FocusManager.instance.primaryFocus?.unfocus(),
    decoration: InputDecoration(
      labelText: widget.label,
      helperText: widget.helperText,
      suffixIcon: IconButton(
        tooltip: obscure ? '显示密码' : '隐藏密码',
        onPressed: () => setState(() => obscure = !obscure),
        icon: Icon(obscure ? CupertinoIcons.eye : CupertinoIcons.eye_slash),
      ),
    ),
    validator:
        widget.validator ??
        (value) => (value?.length ?? 0) >= 8 ? null : '密码至少 8 位',
  );
}

class _AuthFlowHeader extends StatelessWidget {
  const _AuthFlowHeader({required this.title, required this.subtitle});

  final String title;
  final String subtitle;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text(
        title,
        style: Theme.of(context).textTheme.headlineSmall?.copyWith(
          fontWeight: FontWeight.w700,
          letterSpacing: -.25,
        ),
      ),
      const SizedBox(height: 7),
      Text(
        subtitle,
        style: Theme.of(context).textTheme.bodyMedium?.copyWith(
          height: 1.5,
          color: Theme.of(context).colorScheme.onSurfaceVariant,
        ),
      ),
    ],
  );
}

class _AuthBrandAppBarTitle extends StatelessWidget {
  const _AuthBrandAppBarTitle();

  @override
  Widget build(BuildContext context) => Image.asset(
    'assets/brand/qingwaguagua-mark-transparent.png',
    width: 28,
    height: 28,
    semanticLabel: '青蛙呱呱',
  );
}

class _AuthSectionLabel extends StatelessWidget {
  const _AuthSectionLabel({required this.label});

  final String label;

  @override
  Widget build(BuildContext context) => Text(
    label,
    style: Theme.of(context).textTheme.labelLarge?.copyWith(
      color: Theme.of(context).colorScheme.onSurface,
      fontWeight: FontWeight.w700,
    ),
  );
}

class _AuthSecurityNotice extends StatelessWidget {
  const _AuthSecurityNotice();

  @override
  Widget build(BuildContext context) => Text(
    '重置后，其他设备上的登录会话将自动失效。',
    style: Theme.of(context).textTheme.bodySmall?.copyWith(
      height: 1.45,
      color: Theme.of(context).colorScheme.onSurfaceVariant,
    ),
  );
}

class _AuthCodeSentNotice extends StatelessWidget {
  const _AuthCodeSentNotice();

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(top: 8, left: 4),
    child: Text(
      '验证码已发送，5 分钟内有效',
      style: Theme.of(context).textTheme.bodySmall?.copyWith(
        color: LinliColors.brandGreenDeep,
        fontWeight: FontWeight.w500,
      ),
    ),
  );
}

bool _validPhone(String value) => validAuthPhone(value);

class _RegistrationClosedNotice extends StatelessWidget {
  const _RegistrationClosedNotice();

  @override
  Widget build(BuildContext context) => Container(
    key: const Key('registration-disabled-notice'),
    padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
    decoration: BoxDecoration(
      color: Theme.of(context).brightness == Brightness.dark
          ? LinliColors.darkSurfaceElevated
          : LinliColors.brandMint,
      borderRadius: BorderRadius.circular(14),
      border: Border.all(color: Theme.of(context).colorScheme.outline),
    ),
    child: Row(
      children: [
        const Icon(
          CupertinoIcons.info_circle,
          size: 18,
          color: LinliColors.brandGreenDeep,
        ),
        const SizedBox(width: 10),
        Expanded(
          child: Text(
            '当前暂未开放新账号注册',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ),
      ],
    ),
  );
}

class _RegistrationClosedView extends StatelessWidget {
  const _RegistrationClosedView();

  @override
  Widget build(BuildContext context) => ListView(
    key: const Key('registration-disabled-view'),
    padding: EdgeInsets.fromLTRB(
      MediaQuery.sizeOf(context).width > 528
          ? (MediaQuery.sizeOf(context).width - 480) / 2
          : 24,
      32,
      MediaQuery.sizeOf(context).width > 528
          ? (MediaQuery.sizeOf(context).width - 480) / 2
          : 24,
      36,
    ),
    children: [
      const _AuthFlowHeader(
        title: '暂未开放注册',
        subtitle: '当前仅支持已有账号登录，注册恢复后会在此处显示。',
      ),
      const SizedBox(height: 22),
      const _RegistrationClosedNotice(),
      const SizedBox(height: 22),
      OutlinedButton(
        onPressed: () => Navigator.of(context).maybePop(),
        child: const Text('返回登录'),
      ),
    ],
  );
}
