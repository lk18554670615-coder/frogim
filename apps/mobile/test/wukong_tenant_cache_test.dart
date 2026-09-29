import 'package:flutter_test/flutter_test.dart';
import 'package:wukongimfluttersdk/entity/channel.dart';
import 'package:wukongimfluttersdk/manager/channel_manager.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test(
    'account boundary clears channel cache and rejects late provider',
    () async {
      final manager = WKChannelManager.shared;
      manager.resetLocalCache();
      addTearDown(manager.resetLocalCache);
      manager.addOrUpdateChannel(WKChannel('same-id', 2)..channelName = '旧企业');
      expect((await manager.getChannel('same-id', 2))?.channelName, '旧企业');
      late void Function(WKChannel) delayed;
      manager.addOnGetChannelListener(
        (id, type, complete) => delayed = complete,
      );
      manager.fetchChannelInfo('same-id', 2);
      manager.resetLocalCache();
      expect(await manager.getChannel('same-id', 2), isNull);
      manager.addOrUpdateChannel(WKChannel('same-id', 2)..channelName = '新企业');
      delayed(WKChannel('same-id', 2)..channelName = '迟到的旧企业资料');
      expect((await manager.getChannel('same-id', 2))?.channelName, '新企业');
    },
  );
}
