import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../core/app_config.dart';
import 'widgets/linli_widgets.dart';

enum LegalDocument { terms, privacy }

Future<void> showLegalDocument(
  BuildContext context,
  LegalDocument document,
) async {
  final isTerms = document == LegalDocument.terms;
  final rawUrl = isTerms ? AppConfig.termsUrl : AppConfig.privacyUrl;
  final uri = Uri.tryParse(rawUrl.trim());
  if (uri != null && uri.host.isNotEmpty) {
    final launched = await launchUrl(uri, mode: LaunchMode.externalApplication);
    if (!launched && context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('${isTerms ? '用户协议' : '隐私政策'}暂时无法打开')),
      );
    }
    return;
  }

  if (!context.mounted) return;
  await Navigator.of(context).push<void>(
    MaterialPageRoute(
      builder: (_) => UnavailableLegalDocumentScreen(document: document),
    ),
  );
}

class UnavailableLegalDocumentScreen extends StatelessWidget {
  const UnavailableLegalDocumentScreen({super.key, required this.document});

  final LegalDocument document;

  @override
  Widget build(BuildContext context) {
    final title = document == LegalDocument.terms ? '用户协议' : '隐私政策';
    return Scaffold(
      appBar: GlassAppBar(title: Text(title)),
      body: StatePanel(
        icon: Icons.policy_outlined,
        title: '$title未配置',
        body: '当前安装包没有配置可访问的正式文件。为保护你的权益，请联系管理员获取审核后的文本，再继续注册或登录。',
      ),
    );
  }
}
