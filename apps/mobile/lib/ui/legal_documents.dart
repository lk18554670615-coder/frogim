import 'package:flutter/material.dart';
import 'package:html/dom.dart' as dom;
import 'package:html/parser.dart' as html_parser;
import 'package:http/http.dart' as http;

import '../core/app_config.dart';
import 'widgets/linli_widgets.dart';

enum LegalDocument { terms, privacy }

typedef LegalDocumentLoader = Future<String> Function(Uri uri);

String _legalDocumentTitle(LegalDocument document) =>
    document == LegalDocument.terms ? '用户协议' : '隐私政策';

Future<String> _loadLegalDocument(Uri uri) async {
  final response = await http.get(uri).timeout(const Duration(seconds: 15));
  if (response.statusCode < 200 || response.statusCode >= 300) {
    throw StateError('legal document returned HTTP ${response.statusCode}');
  }
  if (response.body.trim().isEmpty) {
    throw const FormatException('legal document is empty');
  }
  return response.body;
}

Future<void> showLegalDocument(
  BuildContext context,
  LegalDocument document,
) async {
  final isTerms = document == LegalDocument.terms;
  final rawUrl = isTerms ? AppConfig.termsUrl : AppConfig.privacyUrl;
  final uri = Uri.tryParse(rawUrl.trim());
  if (!context.mounted) return;
  await Navigator.of(context).push<void>(
    MaterialPageRoute(
      builder: (_) =>
          uri != null &&
              uri.host.isNotEmpty &&
              (uri.scheme == 'https' || uri.scheme == 'http')
          ? LegalDocumentScreen(document: document, uri: uri)
          : UnavailableLegalDocumentScreen(document: document),
    ),
  );
}

class LegalDocumentScreen extends StatefulWidget {
  const LegalDocumentScreen({
    super.key,
    required this.document,
    required this.uri,
    LegalDocumentLoader? loader,
  }) : loader = loader ?? _loadLegalDocument;

  final LegalDocument document;
  final Uri uri;
  final LegalDocumentLoader loader;

  @override
  State<LegalDocumentScreen> createState() => _LegalDocumentScreenState();
}

class _LegalDocumentScreenState extends State<LegalDocumentScreen> {
  late Future<String> content;

  @override
  void initState() {
    super.initState();
    content = widget.loader(widget.uri);
  }

  void _retry() => setState(() => content = widget.loader(widget.uri));

  @override
  Widget build(BuildContext context) {
    final title = _legalDocumentTitle(widget.document);
    return Scaffold(
      appBar: GlassAppBar(title: Text(title)),
      body: FutureBuilder<String>(
        future: content,
        builder: (context, snapshot) {
          if (snapshot.connectionState != ConnectionState.done) {
            return StatePanel(
              icon: Icons.policy_outlined,
              title: '正在加载$title',
              body: '正在从服务端读取最新版本，请稍候。',
              loading: true,
            );
          }
          if (snapshot.hasError || snapshot.data == null) {
            return StatePanel(
              icon: Icons.cloud_off_outlined,
              title: '$title暂时无法加载',
              body: '请检查网络连接后重试。',
              actionLabel: '重新加载',
              onAction: _retry,
            );
          }
          final body = html_parser.parse(snapshot.data!).body;
          if (body == null || body.text.trim().isEmpty) {
            return StatePanel(
              icon: Icons.description_outlined,
              title: '$title内容为空',
              body: '服务端暂未提供可阅读的正式文本，请稍后重试。',
              actionLabel: '重新加载',
              onAction: _retry,
            );
          }
          return _LegalDocumentBody(body: body);
        },
      ),
    );
  }
}

class _LegalDocumentBody extends StatelessWidget {
  const _LegalDocumentBody({required this.body});

  final dom.Element body;

  @override
  Widget build(BuildContext context) => SelectionArea(
    child: ListView(
      key: const Key('legal-document-content'),
      padding: const EdgeInsets.fromLTRB(20, 20, 20, 40),
      children: _buildBlocks(context, body.children),
    ),
  );

  List<Widget> _buildBlocks(BuildContext context, List<dom.Element> elements) {
    final theme = Theme.of(context);
    final result = <Widget>[];
    for (final element in elements) {
      final text = element.text.trim();
      if (text.isEmpty) continue;
      switch (element.localName) {
        case 'h1':
          result.add(
            Padding(
              padding: const EdgeInsets.only(bottom: 6),
              child: Text(text, style: theme.textTheme.headlineSmall),
            ),
          );
          break;
        case 'h2':
          result.add(
            Padding(
              padding: const EdgeInsets.only(top: 18, bottom: 6),
              child: Text(text, style: theme.textTheme.titleLarge),
            ),
          );
          break;
        case 'h3':
          result.add(
            Padding(
              padding: const EdgeInsets.only(top: 12, bottom: 4),
              child: Text(text, style: theme.textTheme.titleMedium),
            ),
          );
          break;
        case 'p':
          final paragraph = Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: Text.rich(
              TextSpan(
                style: theme.textTheme.bodyMedium?.copyWith(height: 1.75),
                children: _inlineSpans(element.nodes),
              ),
            ),
          );
          if (element.classes.contains('notice')) {
            result.add(
              Container(
                margin: const EdgeInsets.only(bottom: 12),
                padding: const EdgeInsets.fromLTRB(14, 12, 14, 2),
                decoration: BoxDecoration(
                  color: theme.colorScheme.primaryContainer.withValues(
                    alpha: .35,
                  ),
                  borderRadius: BorderRadius.circular(12),
                  border: Border.all(
                    color: theme.colorScheme.primary.withValues(alpha: .16),
                  ),
                ),
                child: paragraph,
              ),
            );
          } else {
            result.add(paragraph);
          }
          break;
        case 'ul':
        case 'ol':
          final ordered = element.localName == 'ol';
          final items = element.children
              .where((child) => child.localName == 'li')
              .toList(growable: false);
          result.add(
            Padding(
              padding: const EdgeInsets.only(bottom: 10),
              child: Column(
                children: [
                  for (var index = 0; index < items.length; index++)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 7),
                      child: Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          SizedBox(
                            width: 24,
                            child: Text(ordered ? '${index + 1}.' : '•'),
                          ),
                          Expanded(
                            child: Text.rich(
                              TextSpan(
                                style: theme.textTheme.bodyMedium?.copyWith(
                                  height: 1.7,
                                ),
                                children: _inlineSpans(items[index].nodes),
                              ),
                            ),
                          ),
                        ],
                      ),
                    ),
                ],
              ),
            ),
          );
          break;
        default:
          result.addAll(_buildBlocks(context, element.children));
          break;
      }
    }
    return result;
  }

  List<InlineSpan> _inlineSpans(List<dom.Node> nodes, {bool bold = false}) {
    final spans = <InlineSpan>[];
    for (final node in nodes) {
      if (node is dom.Text) {
        spans.add(
          TextSpan(
            text: node.data,
            style: bold ? const TextStyle(fontWeight: FontWeight.w600) : null,
          ),
        );
      } else if (node is dom.Element) {
        spans.addAll(
          _inlineSpans(
            node.nodes,
            bold: bold || node.localName == 'strong' || node.localName == 'b',
          ),
        );
      }
    }
    return spans;
  }
}

class UnavailableLegalDocumentScreen extends StatelessWidget {
  const UnavailableLegalDocumentScreen({super.key, required this.document});

  final LegalDocument document;

  @override
  Widget build(BuildContext context) {
    final title = _legalDocumentTitle(document);
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
