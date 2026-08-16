import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../im/business_features.dart';
import '../widgets/linli_widgets.dart';

Future<StickerItemSummary?> showStickerPicker(
  BuildContext context,
  AppController controller,
) => showModalBottomSheet<StickerItemSummary>(
  context: context,
  isScrollControlled: true,
  useSafeArea: true,
  showDragHandle: true,
  builder: (_) => FractionallySizedBox(
    heightFactor: .72,
    child: StickerPicker(controller: controller),
  ),
);

class StickerStoreScreen extends StatelessWidget {
  const StickerStoreScreen({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('表情商店')),
    body: StickerPicker(controller: controller, storeMode: true),
  );
}

class StickerPicker extends StatefulWidget {
  const StickerPicker({
    super.key,
    required this.controller,
    this.storeMode = false,
  });

  final AppController controller;
  final bool storeMode;

  @override
  State<StickerPicker> createState() => _StickerPickerState();
}

class _StickerPickerState extends State<StickerPicker> {
  bool loading = true;
  String error = '';
  String selected = 'recent';
  List<StickerCategorySummary> categories = const [];
  List<StickerPackSummary> packs = const [];
  List<StickerItemSummary> recent = const [];
  List<StickerItemSummary> favorites = const [];

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    setState(() {
      loading = true;
      error = '';
    });
    try {
      final values = await Future.wait<Object>([
        widget.controller.loadStickerCategories(),
        widget.controller.loadStickerPacks(),
        widget.controller.loadRecentStickers(),
        widget.controller.loadFavoriteStickers(),
      ]);
      if (!mounted) return;
      setState(() {
        categories = values[0] as List<StickerCategorySummary>;
        packs = values[1] as List<StickerPackSummary>;
        recent = values[2] as List<StickerItemSummary>;
        favorites = values[3] as List<StickerItemSummary>;
        if (recent.isEmpty && categories.isNotEmpty) {
          selected = categories.first.id;
        }
      });
    } catch (cause) {
      debugPrint('Sticker store load failed: $cause');
      if (mounted) setState(() => error = 'failed');
    } finally {
      if (mounted) setState(() => loading = false);
    }
  }

  List<StickerItemSummary> get _visibleItems => switch (selected) {
    'recent' => recent,
    'favorites' => favorites,
    _ =>
      packs
          .where((pack) => pack.categoryId == selected)
          .expand((pack) => pack.items)
          .toList(),
  };

  List<StickerPackSummary> get _visiblePacks =>
      selected == 'recent' || selected == 'favorites'
      ? const []
      : packs.where((pack) => pack.categoryId == selected).toList();

  Future<void> _favoriteSticker(StickerItemSummary sticker) async {
    try {
      await widget.controller.toggleStickerFavorite(sticker);
      await _load();
    } catch (cause) {
      debugPrint('Sticker favorite update failed: $cause');
      _snack('暂时无法更新收藏，请稍后重试');
    }
  }

  Future<void> _favoritePack(StickerPackSummary pack) async {
    try {
      await widget.controller.toggleStickerPackFavorite(pack);
      await _load();
    } catch (cause) {
      debugPrint('Sticker pack favorite update failed: $cause');
      _snack('暂时无法更新表情包收藏，请稍后重试');
    }
  }

  void _select(StickerItemSummary sticker) {
    if (!widget.storeMode) Navigator.pop(context, sticker);
  }

  void _snack(String value) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(value.replaceFirst('BusinessApiException: ', ''))),
    );
  }

  @override
  Widget build(BuildContext context) {
    if (loading) return const Center(child: CircularProgressIndicator());
    if (error.isNotEmpty) {
      return StatePanel(
        icon: CupertinoIcons.exclamationmark_circle,
        title: '表情商店暂时无法加载',
        body: '请检查网络连接后重试。已经收藏的表情不会丢失。',
        actionLabel: '重新加载',
        onAction: _load,
      );
    }
    return Column(
      children: [
        SizedBox(
          height: 48,
          child: ListView(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(horizontal: 12),
            children: [
              _CategoryChip(
                label: '最近',
                selected: selected == 'recent',
                onTap: () => setState(() => selected = 'recent'),
              ),
              _CategoryChip(
                label: '收藏',
                selected: selected == 'favorites',
                onTap: () => setState(() => selected = 'favorites'),
              ),
              for (final category in categories)
                _CategoryChip(
                  label: category.name,
                  selected: selected == category.id,
                  onTap: () => setState(() => selected = category.id),
                ),
            ],
          ),
        ),
        const Divider(height: 1),
        Expanded(
          child: RefreshIndicator(
            onRefresh: _load,
            child: CustomScrollView(
              physics: const AlwaysScrollableScrollPhysics(),
              slivers: [
                if (_visiblePacks.isNotEmpty)
                  SliverList.builder(
                    itemCount: _visiblePacks.length,
                    itemBuilder: (context, index) {
                      final pack = _visiblePacks[index];
                      return ListTile(
                        leading: ClipRRect(
                          borderRadius: BorderRadius.circular(10),
                          child: LinliNetworkImage(
                            url: pack.coverUrl,
                            cacheKey: pack.coverUrl,
                            width: 42,
                            height: 42,
                            fit: BoxFit.cover,
                            errorBuilder: (_) => const Icon(
                              CupertinoIcons.square_grid_2x2,
                              size: 32,
                            ),
                          ),
                        ),
                        title: Text(pack.name),
                        subtitle: Text(
                          '${pack.items.length} 个表情${pack.description.isEmpty ? '' : ' · ${pack.description}'}',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                        trailing: TextButton.icon(
                          onPressed: () => _favoritePack(pack),
                          icon: Icon(
                            pack.favorite
                                ? CupertinoIcons.heart_fill
                                : CupertinoIcons.heart,
                            size: 18,
                          ),
                          label: Text(pack.favorite ? '已收藏' : '收藏'),
                        ),
                      );
                    },
                  ),
                if (_visibleItems.isEmpty)
                  SliverFillRemaining(
                    hasScrollBody: false,
                    child: StatePanel(
                      icon: CupertinoIcons.smiley,
                      title: selected == 'recent'
                          ? '最近还没有使用记录'
                          : selected == 'favorites'
                          ? '还没有收藏表情'
                          : '当前分类没有表情',
                      body: selected == 'recent'
                          ? '发送过的表情会出现在这里，方便下次快速使用。'
                          : selected == 'favorites'
                          ? '长按表情即可收藏，常用内容会在这里集中显示。'
                          : '可以下拉刷新，或切换到其他分类继续浏览。',
                    ),
                  )
                else
                  SliverPadding(
                    padding: const EdgeInsets.fromLTRB(12, 12, 12, 28),
                    sliver: SliverGrid.builder(
                      gridDelegate:
                          const SliverGridDelegateWithMaxCrossAxisExtent(
                            maxCrossAxisExtent: 116,
                            mainAxisSpacing: 10,
                            crossAxisSpacing: 10,
                            childAspectRatio: .86,
                          ),
                      itemCount: _visibleItems.length,
                      itemBuilder: (context, index) {
                        final sticker = _visibleItems[index];
                        return _StickerTile(
                          sticker: sticker,
                          storeMode: widget.storeMode,
                          onTap: () => _select(sticker),
                          onFavorite: () => _favoriteSticker(sticker),
                        );
                      },
                    ),
                  ),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

class _CategoryChip extends StatelessWidget {
  const _CategoryChip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(right: 8, top: 7, bottom: 7),
    child: ChoiceChip(
      label: Text(label),
      selected: selected,
      onSelected: (_) => onTap(),
    ),
  );
}

class _StickerTile extends StatelessWidget {
  const _StickerTile({
    required this.sticker,
    required this.storeMode,
    required this.onTap,
    required this.onFavorite,
  });

  final StickerItemSummary sticker;
  final bool storeMode;
  final VoidCallback onTap;
  final VoidCallback onFavorite;

  @override
  Widget build(BuildContext context) => Material(
    color: Theme.of(context).colorScheme.surfaceContainer,
    borderRadius: BorderRadius.circular(14),
    clipBehavior: Clip.antiAlias,
    child: InkWell(
      onTap: onTap,
      onLongPress: onFavorite,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(8, 8, 8, 6),
        child: Column(
          children: [
            Expanded(
              child: LinliNetworkImage(
                url: sticker.url,
                cacheKey: sticker.mediaId,
                fit: BoxFit.contain,
                errorBuilder: (_) => Center(
                  child: Text(sticker.emoji.isEmpty ? '🙂' : sticker.emoji),
                ),
              ),
            ),
            Row(
              children: [
                Expanded(
                  child: Text(
                    sticker.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.labelSmall,
                  ),
                ),
                if (storeMode)
                  InkResponse(
                    onTap: onFavorite,
                    child: Padding(
                      padding: const EdgeInsets.all(3),
                      child: Icon(
                        sticker.favorite
                            ? CupertinoIcons.heart_fill
                            : CupertinoIcons.heart,
                        size: 15,
                      ),
                    ),
                  ),
              ],
            ),
          ],
        ),
      ),
    ),
  );
}
