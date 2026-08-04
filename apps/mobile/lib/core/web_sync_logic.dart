bool conversationSequencesChanged(
  Map<String, int> previous,
  Map<String, int> current,
) =>
    previous.length != current.length ||
    current.entries.any((entry) => previous[entry.key] != entry.value);

String notificationClaimWinner(Iterable<String> tabIds) {
  final ordered = tabIds.toSet().toList()..sort();
  if (ordered.isEmpty) throw ArgumentError.value(tabIds, 'tabIds');
  return ordered.first;
}
