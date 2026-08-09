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
            const Text('当前为开发环境占位文本，不能替代正式法律文件。'),
          ],
        ),
      ),
    ),
  );
}
