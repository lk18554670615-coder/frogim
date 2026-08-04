import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/web_sync_logic.dart';

void main() {
  test('多标签通知竞争稳定选择同一个标签页', () {
    expect(notificationClaimWinner({'tab-c', 'tab-a', 'tab-b'}), 'tab-a');
    expect(notificationClaimWinner({'tab-b', 'tab-a'}), 'tab-a');
  });

  test('仅会话序列真实变化才触发跨标签同步', () {
    expect(conversationSequencesChanged({'c1': 4}, {'c1': 4}), isFalse);
    expect(conversationSequencesChanged({'c1': 4}, {'c1': 5}), isTrue);
    expect(conversationSequencesChanged({'c1': 4}, {'c1': 4, 'c2': 1}), isTrue);
  });
}
