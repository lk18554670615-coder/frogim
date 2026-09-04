import 'dart:ui';

import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_config.dart';
import 'package:flutter/services.dart';

import '../../core/app_theme.dart';
import '../../core/models.dart';
import '../../core/media_access.dart';

class LinliNetworkImage extends StatelessWidget {
  const LinliNetworkImage({
    super.key,
    required this.url,
    this.cacheKey,
    this.width,
    this.height,
    this.fit = BoxFit.cover,
    this.filterQuality = FilterQuality.medium,
    this.semanticLabel,
    this.placeholderBuilder,
    this.errorBuilder,
  });

  final String url;
  final String? cacheKey;
  final double? width;
  final double? height;
  final BoxFit fit;
  final FilterQuality filterQuality;
  final String? semanticLabel;
  final WidgetBuilder? placeholderBuilder;
  final WidgetBuilder? errorBuilder;

  @override
  Widget build(BuildContext context) {
    final trimmed = url.trim();
    final resolved = trimmed.startsWith('/') && AppConfig.apiBaseUrl.isNotEmpty
        ? '${AppConfig.apiBaseUrl}$trimmed'
        : trimmed;
    return CachedNetworkImage(
      imageUrl: resolved,
      httpHeaders: mediaAccess.headersFor(resolved),
      cacheKey: mediaAccess.owns(resolved) ? resolved : cacheKey,
      width: width,
      height: height,
      fit: fit,
      filterQuality: filterQuality,
      fadeInDuration: const Duration(milliseconds: 90),
      fadeOutDuration: Duration.zero,
      imageBuilder: semanticLabel == null
          ? null
          : (context, provider) => Image(
              image: provider,
              width: width,
              height: height,
              fit: fit,
              filterQuality: filterQuality,
              semanticLabel: semanticLabel,
            ),
      placeholder: placeholderBuilder == null
          ? null
          : (context, _) => placeholderBuilder!(context),
      errorWidget: (context, _, _) =>
          errorBuilder?.call(context) ?? const SizedBox.shrink(),
    );
  }
}

class GlassSurface extends StatelessWidget {
  const GlassSurface({
    super.key,
    required this.child,
    this.border,
    this.padding,
  });

  final Widget child;
  final Border? border;
  final EdgeInsetsGeometry? padding;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    final highContrast = MediaQuery.highContrastOf(context);
    final color = highContrast
        ? (dark ? LinliColors.darkSurface : Colors.white)
        : (dark
              ? LinliColors.darkSurface.withValues(alpha: .86)
              : Colors.white.withValues(alpha: .78));
    final content = Container(
      padding: padding,
      decoration: BoxDecoration(color: color, border: border),
      child: child,
    );
    if (highContrast) return content;
    return ClipRect(
      child: BackdropFilter(
        filter: ImageFilter.blur(sigmaX: 18, sigmaY: 18),
        child: content,
      ),
    );
  }
}

class GlassAppBar extends StatelessWidget implements PreferredSizeWidget {
  const GlassAppBar({
    super.key,
    required this.title,
    this.actions,
    this.titleSpacing,
    this.centerTitle = true,
  });

  final Widget title;
  final List<Widget>? actions;
  final double? titleSpacing;
  final bool centerTitle;

  @override
  Size get preferredSize => const Size.fromHeight(44);

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    return AppBar(
      systemOverlayStyle: SystemUiOverlayStyle(
        statusBarColor: Colors.transparent,
        statusBarIconBrightness: dark ? Brightness.light : Brightness.dark,
        statusBarBrightness: dark ? Brightness.dark : Brightness.light,
      ),
      title: title,
      actions: actions,
      titleSpacing: titleSpacing,
      centerTitle: centerTitle,
      flexibleSpace: GlassSurface(
        border: Border(
          bottom: BorderSide(color: Theme.of(context).colorScheme.outline),
        ),
        child: const SizedBox.expand(),
      ),
    );
  }
}

class PersonAvatar extends StatelessWidget {
  const PersonAvatar({
    super.key,
    required this.name,
    this.size = 48,
    this.online = false,
    this.avatarUrl,
  });
  final String name;
  final double size;
  final bool online;
  final String? avatarUrl;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final color = scheme.primary;
    final foreground = scheme.onPrimary;
    final resolvedAvatarUrl =
        avatarUrl != null &&
            avatarUrl!.startsWith('/') &&
            AppConfig.apiBaseUrl.isNotEmpty
        ? '${AppConfig.apiBaseUrl}$avatarUrl'
        : avatarUrl;
    return SizedBox.square(
      dimension: size,
      child: Stack(
        children: [
          Container(
            width: size,
            height: size,
            clipBehavior: Clip.antiAlias,
            decoration: BoxDecoration(color: color, shape: BoxShape.circle),
            alignment: Alignment.center,
            child: resolvedAvatarUrl == null
                ? Text(
                    name.characters.take(1).toString(),
                    textScaler: MediaQuery.textScalerOf(
                      context,
                    ).clamp(maxScaleFactor: 1.25),
                    style: TextStyle(
                      color: foreground,
                      fontSize: size * .38,
                      fontWeight: FontWeight.w700,
                    ),
                  )
                : resolvedAvatarUrl.startsWith('assets/')
                ? Image.asset(
                    resolvedAvatarUrl,
                    width: size,
                    height: size,
                    fit: BoxFit.cover,
                    semanticLabel: '$name 的头像',
                  )
                : LinliNetworkImage(
                    url: resolvedAvatarUrl,
                    cacheKey: resolvedAvatarUrl,
                    width: size,
                    height: size,
                    fit: BoxFit.cover,
                    semanticLabel: '$name 的头像',
                    placeholderBuilder: (_) => ColoredBox(
                      color: color,
                      child: const SizedBox.expand(),
                    ),
                    errorBuilder: (_) => Center(
                      child: Text(
                        name.characters.take(1).toString(),
                        style: TextStyle(
                          color: foreground,
                          fontSize: size * .38,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                    ),
                  ),
          ),
          if (online)
            Positioned(
              right: 0,
              bottom: 0,
              child: Container(
                width: 13,
                height: 13,
                decoration: BoxDecoration(
                  color: LinliColors.systemGreen,
                  shape: BoxShape.circle,
                  border: Border.all(
                    color: Theme.of(context).colorScheme.surfaceContainer,
                    width: 2,
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class LinliSearchBar extends StatelessWidget {
  const LinliSearchBar({
    super.key,
    required this.onTap,
    this.hint = '搜索联系人、群组或消息',
  });
  final VoidCallback onTap;
  final String hint;

  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    label: hint,
    child: CupertinoButton(
      padding: EdgeInsets.zero,
      minimumSize: const Size.fromHeight(44),
      onPressed: onTap,
      child: Container(
        height: 42,
        padding: const EdgeInsets.symmetric(horizontal: 12),
        decoration: BoxDecoration(
          color: Theme.of(context).brightness == Brightness.dark
              ? LinliColors.darkSurfaceElevated
              : Theme.of(context).colorScheme.surfaceContainerHigh,
          borderRadius: BorderRadius.circular(13),
        ),
        child: Row(
          children: [
            Icon(
              CupertinoIcons.search,
              size: 18,
              color: Theme.of(
                context,
              ).colorScheme.onSurface.withValues(alpha: .42),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                hint,
                style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                  color: Theme.of(
                    context,
                  ).colorScheme.onSurface.withValues(alpha: .42),
                ),
              ),
            ),
          ],
        ),
      ),
    ),
  );
}

class MessagingConnectionBanner extends StatelessWidget {
  const MessagingConnectionBanner({
    super.key,
    required this.retrying,
    required this.onRetry,
  });

  final bool retrying;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    final message = retrying ? '正在重新连接消息服务…' : '消息服务未连接，发送暂不可用';
    return Semantics(
      container: true,
      liveRegion: true,
      label: message,
      child: Container(
        key: const Key('messaging-connection-banner'),
        constraints: const BoxConstraints(minHeight: 48),
        padding: const EdgeInsets.only(left: 14, right: 4),
        color: dark ? const Color(0xFF3A2A1D) : const Color(0xFFFFF3E8),
        child: Row(
          children: [
            const Icon(
              CupertinoIcons.exclamationmark_circle_fill,
              size: 17,
              color: LinliColors.systemOrange,
            ),
            const SizedBox(width: 9),
            Expanded(
              child: Text(
                message,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: dark
                      ? const Color(0xFFFFD8B8)
                      : const Color(0xFF70401F),
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
            if (retrying)
              const Padding(
                padding: EdgeInsets.symmetric(horizontal: 13),
                child: CupertinoActivityIndicator(radius: 8),
              )
            else
              CupertinoButton(
                key: const Key('retry-messaging-connection'),
                minimumSize: const Size(48, 44),
                padding: const EdgeInsets.symmetric(horizontal: 10),
                onPressed: onRetry,
                child: const Text(
                  '重试',
                  style: TextStyle(
                    color: LinliColors.systemOrange,
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class SectionCard extends StatelessWidget {
  const SectionCard({super.key, required this.children});
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => Material(
    color: Theme.of(context).colorScheme.surfaceContainer,
    clipBehavior: Clip.antiAlias,
    shape: RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(16),
      side: BorderSide(
        color: Theme.of(context).colorScheme.outline.withValues(alpha: .6),
        width: .5,
      ),
    ),
    child: Column(
      children: [
        for (var i = 0; i < children.length; i++) ...[
          children[i],
          if (i != children.length - 1)
            Padding(
              padding: const EdgeInsets.only(left: 56),
              child: Divider(
                color: Theme.of(context).colorScheme.outline,
                height: .5,
              ),
            ),
        ],
      ],
    ),
  );
}

class SectionHeader extends StatelessWidget {
  const SectionHeader(this.text, {super.key, this.horizontalInset = 0});
  final String text;
  final double horizontalInset;

  @override
  Widget build(BuildContext context) => Padding(
    padding: EdgeInsets.fromLTRB(horizontalInset, 24, horizontalInset, 8),
    child: Text(
      text,
      style: Theme.of(context).textTheme.labelMedium?.copyWith(
        color: Theme.of(context).colorScheme.onSurface.withValues(alpha: .5),
        fontWeight: FontWeight.w600,
        letterSpacing: .1,
      ),
    ),
  );
}

class SettingTile extends StatelessWidget {
  const SettingTile({
    super.key,
    required this.icon,
    required this.title,
    this.subtitle,
    this.trailing,
    this.onTap,
    this.destructive = false,
    this.showIconBackground = true,
  });
  final IconData icon;
  final String title;
  final String? subtitle;
  final Widget? trailing;
  final VoidCallback? onTap;
  final bool destructive;
  final bool showIconBackground;

  @override
  Widget build(BuildContext context) => ListTile(
    minTileHeight: subtitle == null ? 56 : 64,
    contentPadding: const EdgeInsets.symmetric(horizontal: 14),
    leading: showIconBackground
        ? Container(
            width: 34,
            height: 34,
            decoration: BoxDecoration(
              color: destructive
                  ? LinliColors.systemRed.withValues(alpha: .1)
                  : Theme.of(context).brightness == Brightness.dark
                  ? LinliColors.brandGreen.withValues(alpha: .14)
                  : LinliColors.brandMintStrong,
              borderRadius: BorderRadius.circular(10),
            ),
            alignment: Alignment.center,
            child: Icon(
              icon,
              size: 18,
              color: destructive
                  ? LinliColors.systemRed
                  : Theme.of(context).brightness == Brightness.dark
                  ? LinliColors.brandGreen
                  : LinliColors.navy,
            ),
          )
        : Icon(
            icon,
            size: 21,
            color: destructive
                ? LinliColors.systemRed
                : Theme.of(context).colorScheme.primary,
          ),
    title: Text(
      title,
      style: Theme.of(context).textTheme.bodyLarge?.copyWith(
        color: destructive ? LinliColors.systemRed : null,
        fontWeight: FontWeight.w500,
      ),
    ),
    subtitle: subtitle == null
        ? null
        : Text(
            subtitle!,
            softWrap: true,
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: Theme.of(context).textTheme.bodySmall,
          ),
    trailing:
        trailing ??
        Icon(
          CupertinoIcons.chevron_forward,
          size: 17,
          color: Theme.of(context).colorScheme.onSurface.withValues(alpha: .25),
        ),
    onTap: onTap,
  );
}

class StatePanel extends StatelessWidget {
  const StatePanel({
    super.key,
    required this.icon,
    required this.title,
    required this.body,
    this.actionLabel,
    this.onAction,
    this.loading = false,
  });
  final IconData icon;
  final String title;
  final String body;
  final String? actionLabel;
  final VoidCallback? onAction;
  final bool loading;

  @override
  Widget build(BuildContext context) => Center(
    child: Padding(
      padding: const EdgeInsets.symmetric(horizontal: 40, vertical: 32),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 56,
            height: 56,
            decoration: BoxDecoration(
              color: Theme.of(context).brightness == Brightness.dark
                  ? LinliColors.brandGreen.withValues(alpha: .1)
                  : LinliColors.brandMint,
              shape: BoxShape.circle,
            ),
            alignment: Alignment.center,
            child: loading
                ? CupertinoActivityIndicator(
                    color: Theme.of(context).brightness == Brightness.dark
                        ? LinliColors.brandGreen
                        : LinliColors.brandGreenDeep,
                  )
                : Icon(
                    icon,
                    color: Theme.of(context).brightness == Brightness.dark
                        ? LinliColors.brandGreen
                        : LinliColors.brandGreenDeep,
                    size: 26,
                  ),
          ),
          const SizedBox(height: 18),
          Text(
            title,
            style: Theme.of(context).textTheme.titleLarge,
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 6),
          Text(
            body,
            style: Theme.of(context).textTheme.bodyMedium?.copyWith(
              color: Theme.of(
                context,
              ).colorScheme.onSurface.withValues(alpha: .5),
            ),
            textAlign: TextAlign.center,
          ),
          if (actionLabel != null) ...[
            const SizedBox(height: 18),
            FilledButton.tonal(onPressed: onAction, child: Text(actionLabel!)),
          ],
        ],
      ),
    ),
  );
}

class MessageStatusIcon extends StatelessWidget {
  const MessageStatusIcon({super.key, required this.status});
  final MessageStatus status;

  @override
  Widget build(BuildContext context) {
    final color = Theme.of(context).brightness == Brightness.dark
        ? Colors.black.withValues(alpha: .72)
        : Colors.white.withValues(alpha: .8);
    return switch (status) {
      MessageStatus.sending => SizedBox.square(
        dimension: 11,
        child: CircularProgressIndicator(strokeWidth: 1.25, color: color),
      ),
      MessageStatus.sent => Icon(
        CupertinoIcons.check_mark,
        size: 12,
        color: color,
      ),
      MessageStatus.delivered || MessageStatus.read => Icon(
        CupertinoIcons.check_mark_circled_solid,
        size: 12,
        color: color,
      ),
      MessageStatus.failed => const Icon(
        CupertinoIcons.exclamationmark_circle_fill,
        size: 13,
        color: LinliColors.systemRed,
      ),
      MessageStatus.recalled ||
      MessageStatus.expired => const SizedBox.shrink(),
    };
  }
}
