import 'package:wukongimfluttersdk/db/const.dart';
import 'package:wukongimfluttersdk/db/wk_db_helper.dart';
import 'history_access.dart';

Future<void> invalidateNativeGroupHistory(
  String channelId,
  GroupHistoryAccess? access,
) async {
  final db = WKDBHelper.shared.getDB();
  if (db == null) return;
  await db.transaction((tx) async {
    final rows = await tx.query(
      WKDBConst.tableMessage,
      columns: ['message_id', 'message_seq', 'timestamp', 'client_msg_no'],
      where: 'channel_id=? AND channel_type=2 AND message_seq>0',
      whereArgs: [channelId],
    );
    for (final row in rows) {
      if (access?.allows(
            (row['message_seq'] as num).toInt(),
            DateTime.fromMillisecondsSinceEpoch(
              (row['timestamp'] as num).toInt() * 1000,
            ),
          ) ??
          false) {
        continue;
      }
      final id = row['message_id'];
      // SDK deleteWithMessageIDs is a tombstone operation: DO NOT use it here.
      await tx.delete(
        WKDBConst.tableMessageReaction,
        where: 'message_id=?',
        whereArgs: [id],
      );
      await tx.delete(
        WKDBConst.tableReminders,
        where: 'message_id=?',
        whereArgs: [id],
      );
      await tx.delete(
        WKDBConst.tableMessage,
        where:
            'message_id=? AND channel_id=? AND channel_type=2 AND message_seq>0',
        whereArgs: [id, channelId],
      );
      await tx.update(
        WKDBConst.tableConversation,
        {'last_client_msg_no': '', 'unread_count': 0},
        where: 'channel_id=? AND channel_type=2 AND last_client_msg_no=?',
        whereArgs: [channelId, row['client_msg_no']],
      );
    }
    // Reminders can outlive their message-cache rows. Do not leave an orphaned
    // pre-join reminder/link just because the message was already evicted.
    if (access?.visibleAll != true) {
      final String predicate;
      final List<Object?> args = [channelId];
      if (access?.afterSeq != null) {
        predicate = 'message_seq<=?';
        args.add(access!.afterSeq);
      } else if (access?.afterTimestamp != null) {
        predicate =
            'message_id NOT IN (SELECT message_id FROM ${WKDBConst.tableMessage} WHERE channel_id=? AND channel_type=2 AND timestamp>?)';
        args.addAll([channelId, access!.afterTimestamp]);
      } else {
        predicate = '1=1';
      }
      await tx.delete(
        WKDBConst.tableReminders,
        where: 'channel_id=? AND channel_type=2 AND ($predicate)',
        whereArgs: args,
      );
      await tx.update(
        WKDBConst.tableConversation,
        {'last_client_msg_no': '', 'unread_count': 0},
        where:
            'channel_id=? AND channel_type=2 AND last_client_msg_no NOT IN (SELECT client_msg_no FROM ${WKDBConst.tableMessage} WHERE channel_id=? AND channel_type=2)',
        whereArgs: [channelId, channelId],
      );
    }
    // Extensions carry edited bodies. Reset this channel's sync cursor as well,
    // allowing a later open policy to hydrate the originals and extensions.
    await tx.delete(
      WKDBConst.tableMessageExtra,
      where: 'channel_id=? AND channel_type=2',
      whereArgs: [channelId],
    );
  });
}
