import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../widgets/linli_widgets.dart';

enum _LoginMode { code, password }

class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final formKey = GlobalKey<FormState>();
  final phone = TextEditingController();
  final credential = TextEditingController();
  _LoginMode mode = _LoginMode.code;
  bool obscurePassword = true;
  bool agreedToPolicies = false;

  @override
  void dispose() {
    phone.dispose();
    credential.dispose();
    super.dispose();
  }

  Future<void> _requestCode() async {
    if (!_validPhone(phone.text)) {
      formKey.currentState?.validate();
      return;
    }
    await widget.controller.requestCode(phone.text);
  }

  void _submit() {
    if (!(formKey.currentState?.validate() ?? false)) return;
    if (!agreedToPolicies) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('请先阅读并同意用户协议和隐私政策')));
      return;
    }
    if (mode == _LoginMode.code) {
      widget.controller.login(phone.text, credential.text);
    } else {
      widget.controller.passwordLogin(phone.text, credential.text);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    body: AnimatedBuilder(
      animation: widget.controller,
      builder: (context, _) => LayoutBuilder(
        builder: (context, constraints) {
          if (constraints.maxWidth >= 1024) {
            return _buildDesktopLogin(context);
          }
          return SafeArea(
            child: Center(
              child: SingleChildScrollView(
                padding: const EdgeInsets.symmetric(
                  horizontal: 24,
                  vertical: 28,
                ),
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

  Widget _buildDesktopLogin(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    return ColoredBox(
      key: const Key('desktop-login-shell'),
      color: dark ? LinliColors.darkBackground : const Color(0xFFF0F3F8),
      child: SafeArea(
        child: Center(
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(40),
            child: ConstrainedBox(
              key: const Key('desktop-login-card'),
              constraints: const BoxConstraints(maxWidth: 920, minHeight: 680),
              child: DecoratedBox(
                decoration: BoxDecoration(
                  color: Theme.of(context).colorScheme.surfaceContainer,
                  borderRadius: BorderRadius.circular(20),
                  border: Border.all(
                    color: Theme.of(context).colorScheme.outline,
                  ),
                  boxShadow: [
                    BoxShadow(
                      color: LinliColors.navy.withValues(
                        alpha: dark ? .28 : .12,
                      ),
                      blurRadius: 42,
                      offset: const Offset(0, 20),
                    ),
                  ],
                ),
                child: ClipRRect(
                  borderRadius: BorderRadius.circular(20),
                  child: IntrinsicHeight(
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        const Expanded(child: _DesktopLoginBrandPanel()),
                        SizedBox(
                          width: 460,
                          child: Padding(
                            padding: const EdgeInsets.fromLTRB(40, 44, 40, 36),
                            child: _buildLoginForm(context, showBrand: false),
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

  Widget _buildLoginForm(
    BuildContext context, {
    required bool showBrand,
  }) => Form(
    key: formKey,
    child: Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (showBrand) ...[
          Center(
            child: ClipRRect(
              borderRadius: BorderRadius.circular(16),
              child: Image.asset(
                'assets/brand/linli-im-icon.png',
                width: 72,
                height: 72,
                semanticLabel: '邻里通讯应用图标',
              ),
            ),
          ),
          const SizedBox(height: 20),
        ],
        Text(
          '登录邻里通讯',
          textAlign: showBrand ? TextAlign.center : TextAlign.left,
          style: Theme.of(context).textTheme.headlineMedium,
        ),
        const SizedBox(height: 8),
        Text(
          '使用验证码或账号密码继续。',
          textAlign: showBrand ? TextAlign.center : TextAlign.left,
          style: Theme.of(context).textTheme.bodyMedium,
        ),
        const SizedBox(height: 24),
        CupertinoSlidingSegmentedControl<_LoginMode>(
          key: const Key('login-mode-control'),
          groupValue: mode,
          children: const {
            _LoginMode.code: Padding(
              padding: EdgeInsets.symmetric(horizontal: 16, vertical: 8),
              child: Text('验证码登录'),
            ),
            _LoginMode.password: Padding(
              padding: EdgeInsets.symmetric(horizontal: 16, vertical: 8),
              child: Text('密码登录'),
            ),
          },
          onValueChanged: (value) {
            if (value == null) return;
            setState(() {
              mode = value;
              credential.clear();
            });
          },
        ),
        const SizedBox(height: 16),
        TextFormField(
          key: const Key('phone-field'),
          controller: phone,
          keyboardType: TextInputType.phone,
          textInputAction: TextInputAction.next,
          autofillHints: const [AutofillHints.telephoneNumber],
          decoration: const InputDecoration(
            labelText: '手机号',
            prefixIcon: Icon(CupertinoIcons.phone),
          ),
          validator: (value) => _validPhone(value ?? '') ? null : '请输入有效手机号',
        ),
        const SizedBox(height: 12),
        TextFormField(
          key: Key(mode == _LoginMode.code ? 'code-field' : 'password-field'),
          controller: credential,
          keyboardType: mode == _LoginMode.code
              ? TextInputType.number
              : TextInputType.visiblePassword,
          obscureText: mode == _LoginMode.password && obscurePassword,
          autofillHints: mode == _LoginMode.code
              ? const [AutofillHints.oneTimeCode]
              : const [AutofillHints.password],
          onFieldSubmitted: (_) => _submit(),
          decoration: InputDecoration(
            labelText: mode == _LoginMode.code ? '验证码' : '密码',
            prefixIcon: const Icon(CupertinoIcons.lock),
            suffixIcon: mode == _LoginMode.code
                ? TextButton(
                    key: const Key('request-code-button'),
                    onPressed: widget.controller.loading ? null : _requestCode,
                    child: Text(
                      widget.controller.codeRequested ? '重新获取' : '获取验证码',
                    ),
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
            if (mode == _LoginMode.code) {
              return text.trim().length >= 4 ? null : '请输入验证码';
            }
            return text.length >= 8 ? null : '密码至少 8 位';
          },
        ),
        if (mode == _LoginMode.password)
          Align(
            alignment: Alignment.centerRight,
            child: TextButton(
              key: const Key('forgot-password'),
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => ResetPasswordScreen(
                    controller: widget.controller,
                    initialPhone: phone.text,
                  ),
                ),
              ),
              child: const Text('忘记密码？'),
            ),
          ),
        if (widget.controller.error != null) ...[
          const SizedBox(height: 8),
          Text(
            widget.controller.error!,
            key: const Key('login-error'),
            style: Theme.of(
              context,
            ).textTheme.bodySmall?.copyWith(color: LinliColors.systemRed),
          ),
        ],
        if (mode == _LoginMode.code && widget.controller.supportsDemo)
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton(
              key: const Key('dev-fill-code'),
              onPressed: () => setState(() => credential.text = '123456'),
              child: const Text('开发环境：填入 123456'),
            ),
          ),
        const SizedBox(height: 8),
        FilledButton(
          key: const Key('login-button'),
          onPressed: widget.controller.loading ? null : _submit,
          child: widget.controller.loading
              ? const SizedBox.square(
                  key: Key('login-spinner'),
                  dimension: 20,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : Text(mode == _LoginMode.code ? '验证码登录' : '密码登录'),
        ),
        const SizedBox(height: 8),
        OutlinedButton(
          key: const Key('open-register'),
          onPressed: () => Navigator.of(context).push(
            MaterialPageRoute(
              builder: (_) => RegisterScreen(
                controller: widget.controller,
                initialPhone: phone.text,
              ),
            ),
          ),
          child: const Text('注册新账号'),
        ),
        if (widget.controller.supportsDemo)
          CupertinoButton(
            key: const Key('demo-login-button'),
            onPressed: widget.controller.loading
                ? null
                : widget.controller.loginAsDemo,
            child: const Text('预览演示环境'),
          ),
        const SizedBox(height: 10),
        _PolicyConsent(
          key: const Key('login-policy-consent'),
          value: agreedToPolicies,
          onChanged: (value) => setState(() => agreedToPolicies = value),
        ),
      ],
    ),
  );
}

class _DesktopLoginBrandPanel extends StatelessWidget {
  const _DesktopLoginBrandPanel();

  @override
  Widget build(BuildContext context) => ColoredBox(
    color: LinliColors.navy,
    child: Padding(
      padding: const EdgeInsets.all(48),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ClipRRect(
            borderRadius: BorderRadius.circular(16),
            child: Image.asset(
              'assets/brand/linli-im-icon.png',
              width: 68,
              height: 68,
              semanticLabel: '邻里通讯应用图标',
            ),
          ),
          const SizedBox(height: 32),
          const Text(
            '让重要的沟通\n始终清晰、有序。',
            style: TextStyle(
              color: Colors.white,
              fontSize: 30,
              height: 1.28,
              fontWeight: FontWeight.w700,
              letterSpacing: -.4,
            ),
          ),
          const SizedBox(height: 16),
          const Text(
            '单聊、群聊、文件与通话统一在一个安全的工作台。',
            style: TextStyle(
              color: Color(0xFFCBD5E1),
              fontSize: 15,
              height: 1.55,
            ),
          ),
          const SizedBox(height: 36),
          const Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              _DesktopLoginFeature(
                icon: CupertinoIcons.chat_bubble_2_fill,
                label: '即时同步',
              ),
              _DesktopLoginFeature(
                icon: CupertinoIcons.lock_shield_fill,
                label: '可靠安全',
              ),
              _DesktopLoginFeature(
                icon: CupertinoIcons.device_laptop,
                label: '多端协作',
              ),
            ],
          ),
        ],
      ),
    ),
  );
}

class _DesktopLoginFeature extends StatelessWidget {
  const _DesktopLoginFeature({required this.icon, required this.label});

  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) => Container(
    padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
    decoration: BoxDecoration(
      color: Colors.white.withValues(alpha: .08),
      borderRadius: BorderRadius.circular(12),
      border: Border.all(color: Colors.white.withValues(alpha: .1)),
    ),
    child: Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 17, color: LinliColors.yellow),
        const SizedBox(width: 7),
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

class _RegisterScreenState extends State<RegisterScreen> {
  final formKey = GlobalKey<FormState>();
  late final phone = TextEditingController(text: widget.initialPhone);
  final code = TextEditingController();
  final name = TextEditingController();
  final password = TextEditingController();
  final confirmPassword = TextEditingController();
  bool agreedToPolicies = false;

  @override
  void dispose() {
    phone.dispose();
    code.dispose();
    name.dispose();
    password.dispose();
    confirmPassword.dispose();
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
    await widget.controller.registerAccount(
      phone: phone.text,
      code: code.text,
      password: password.text,
      name: name.text,
    );
    if (mounted && widget.controller.authenticated) {
      Navigator.of(context).pop();
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('注册账号')),
    body: AnimatedBuilder(
      animation: widget.controller,
      builder: (context, _) => Form(
        key: formKey,
        child: ListView(
          padding: const EdgeInsets.all(20),
          children: [
            Text('创建邻里通讯账号', style: Theme.of(context).textTheme.headlineMedium),
            const SizedBox(height: 8),
            Text(
              '手机号验证后即可设置密码和昵称。',
              style: Theme.of(context).textTheme.bodyMedium,
            ),
            const SizedBox(height: 24),
            _phoneField(phone),
            const SizedBox(height: 12),
            _codeField(
              code,
              onRequest: () {
                if (_validPhone(phone.text)) {
                  widget.controller.requestCode(phone.text);
                }
              },
            ),
            if (widget.controller.supportsDemo)
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton(
                  key: const Key('register-dev-fill-code'),
                  onPressed: () => setState(() => code.text = '123456'),
                  child: const Text('开发环境：填入 123456'),
                ),
              ),
            TextFormField(
              key: const Key('register-name'),
              controller: name,
              maxLength: 40,
              decoration: const InputDecoration(labelText: '昵称'),
              validator: (value) =>
                  (value?.trim().isNotEmpty ?? false) ? null : '请输入昵称',
            ),
            const SizedBox(height: 12),
            _passwordField(password, key: const Key('register-password')),
            const SizedBox(height: 12),
            TextFormField(
              key: const Key('register-confirm-password'),
              controller: confirmPassword,
              obscureText: true,
              textInputAction: TextInputAction.done,
              onFieldSubmitted: (_) => _register(),
              decoration: const InputDecoration(labelText: '确认密码'),
              validator: (value) =>
                  value == password.text ? null : '两次输入的密码不一致',
            ),
            const SizedBox(height: 8),
            _PolicyConsent(
              key: const Key('register-policy-consent'),
              value: agreedToPolicies,
              onChanged: (value) => setState(() => agreedToPolicies = value),
            ),
            if (widget.controller.error != null) ...[
              const SizedBox(height: 8),
              Text(
                widget.controller.error!,
                style: Theme.of(
                  context,
                ).textTheme.bodySmall?.copyWith(color: LinliColors.systemRed),
              ),
            ],
            const SizedBox(height: 20),
            FilledButton(
              key: const Key('register-submit'),
              onPressed: widget.controller.loading ? null : _register,
              child: const Text('注册并登录'),
            ),
          ],
        ),
      ),
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
  Widget build(BuildContext context) => Semantics(
    checked: value,
    label: '同意用户协议和隐私政策',
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.center,
      children: [
        Checkbox(
          key: const Key('policy-consent-checkbox'),
          value: value,
          onChanged: (next) => onChanged(next ?? false),
        ),
        Expanded(
          child: Wrap(
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              const Text('我已阅读并同意'),
              TextButton(
                onPressed: () => _openPolicy(context, _PolicyDocument.terms),
                child: const Text('《用户协议》'),
              ),
              const Text('和'),
              TextButton(
                onPressed: () => _openPolicy(context, _PolicyDocument.privacy),
                child: const Text('《隐私政策》'),
              ),
            ],
          ),
        ),
      ],
    ),
  );
}

enum _PolicyDocument { terms, privacy }

void _openPolicy(BuildContext context, _PolicyDocument document) {
  final isTerms = document == _PolicyDocument.terms;
  Navigator.of(context).push(
    MaterialPageRoute(
      builder: (_) => Scaffold(
        appBar: GlassAppBar(title: Text(isTerms ? '用户协议' : '隐私政策')),
        body: ListView(
          padding: const EdgeInsets.all(24),
          children: [
            Text(
              isTerms
                  ? '本协议用于说明账号注册、用户行为规范、内容治理、服务变更、账号处置与争议解决。正式发布文本必须由实际运营主体填写主体名称、联系方式、服务地区、争议管辖和生效日期，并完成法律审核。'
                  : '本政策用于说明手机号、账号资料、设备标识、通讯关系、消息与媒体、权限信息的处理目的、保存期限、安全措施、第三方共享、用户权利和账号注销。正式发布文本必须补充实际运营主体、第三方 SDK 清单、服务器区域、联系方式和生效日期，并完成法律审核。',
              style: Theme.of(context).textTheme.bodyLarge,
            ),
            const SizedBox(height: 20),
            const Text('当前为产品验收占位文本，不能替代正式法律文件。'),
          ],
        ),
      ),
    ),
  );
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
  late final phone = TextEditingController(text: widget.initialPhone);
  final code = TextEditingController();
  final password = TextEditingController();
  bool codeRequested = false;

  @override
  void dispose() {
    phone.dispose();
    code.dispose();
    password.dispose();
    super.dispose();
  }

  Future<void> _request() async {
    if (!_validPhone(phone.text)) {
      formKey.currentState?.validate();
      return;
    }
    final success = await widget.controller.requestResetCode(phone.text);
    if (mounted && success) setState(() => codeRequested = true);
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
    appBar: const GlassAppBar(title: Text('重置密码')),
    body: AnimatedBuilder(
      animation: widget.controller,
      builder: (context, _) => Form(
        key: formKey,
        child: ListView(
          padding: const EdgeInsets.all(20),
          children: [
            Text('找回账号', style: Theme.of(context).textTheme.headlineMedium),
            const SizedBox(height: 8),
            Text(
              '验证手机号后设置新密码，成功后其他登录会话将失效。',
              style: Theme.of(context).textTheme.bodyMedium,
            ),
            const SizedBox(height: 24),
            _phoneField(phone),
            const SizedBox(height: 12),
            _codeField(code, onRequest: _request),
            if (widget.controller.supportsDemo)
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton(
                  key: const Key('reset-dev-fill-code'),
                  onPressed: () => setState(() => code.text = '123456'),
                  child: const Text('开发环境：填入 123456'),
                ),
              ),
            _passwordField(password, key: const Key('reset-password')),
            if (widget.controller.error != null) ...[
              const SizedBox(height: 8),
              Text(
                widget.controller.error!,
                style: Theme.of(
                  context,
                ).textTheme.bodySmall?.copyWith(color: LinliColors.systemRed),
              ),
            ],
            const SizedBox(height: 20),
            FilledButton(
              key: const Key('reset-submit'),
              onPressed: widget.controller.loading || !codeRequested
                  ? null
                  : _reset,
              child: const Text('重置密码'),
            ),
          ],
        ),
      ),
    ),
  );
}

TextFormField _phoneField(TextEditingController controller) => TextFormField(
  key: const Key('auth-phone'),
  controller: controller,
  keyboardType: TextInputType.phone,
  autofillHints: const [AutofillHints.telephoneNumber],
  decoration: const InputDecoration(
    labelText: '手机号',
    prefixIcon: Icon(CupertinoIcons.phone),
  ),
  validator: (value) => _validPhone(value ?? '') ? null : '请输入有效手机号',
);

TextFormField _codeField(
  TextEditingController controller, {
  required VoidCallback onRequest,
}) => TextFormField(
  key: const Key('auth-code'),
  controller: controller,
  keyboardType: TextInputType.number,
  autofillHints: const [AutofillHints.oneTimeCode],
  decoration: InputDecoration(
    labelText: '验证码',
    prefixIcon: const Icon(CupertinoIcons.lock),
    suffixIcon: TextButton(onPressed: onRequest, child: const Text('获取验证码')),
  ),
  validator: (value) => (value?.trim().length ?? 0) >= 4 ? null : '请输入验证码',
);

TextFormField _passwordField(
  TextEditingController controller, {
  required Key key,
}) => TextFormField(
  key: key,
  controller: controller,
  obscureText: true,
  autofillHints: const [AutofillHints.newPassword],
  decoration: const InputDecoration(
    labelText: '密码',
    helperText: '至少 8 位，建议同时包含字母、数字和符号',
    prefixIcon: Icon(CupertinoIcons.lock_shield),
  ),
  validator: (value) => (value?.length ?? 0) >= 8 ? null : '密码至少 8 位',
);

bool _validPhone(String value) => value.trim().length >= 6;
