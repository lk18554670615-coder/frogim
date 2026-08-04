import 'dart:ui';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_config.dart';
import 'package:flutter/services.dart';

import '../../core/app_theme.dart';
import '../../core/models.dart';

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
    final color = Theme.of(context).colorScheme.primary;
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
                      color: Colors.white,
                      fontSize: size * .38,
                      fontWeight: FontWeight.w600,
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
                : Image.network(
                    resolvedAvatarUrl,
                    width: size,
                    height: size,
                    fit: BoxFit.cover,
                    semanticLabel: '$name 的头像',
                    errorBuilder: (_, _, _) => Text(
                      name.characters.take(1).toString(),
                      style: TextStyle(
                        color: Colors.white,
                        fontSize: size * .38,
                        fontWeight: FontWeight.w600,
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
        height: 36,
        padding: const EdgeInsets.symmetric(horizontal: 10),
        decoration: BoxDecoration(
          color: Theme.of(context).brightness == Brightness.dark
              ? LinliColors.darkSurfaceElevated
              : const Color(0xFFEEF2F7),
          borderRadius: BorderRadius.circular(12),
        ),
        child: Row(
          children: [
            Icon(
              CupertinoIcons.search,
              size: 17,
              color: Theme.of(
                context,
              ).colorScheme.onSurface.withValues(alpha: .42),
            ),
            const SizedBox(width: 6),
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

class SectionCard extends StatelessWidget {
  const SectionCard({super.key, required this.children});
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => ClipRRect(
    borderRadius: BorderRadius.circular(16),
    child: Material(
      color: Theme.of(context).colorScheme.surfaceContainer,
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
    ),
  );
}

class SectionHeader extends StatelessWidget {
  const SectionHeader(this.text, {super.key});
  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(16, 24, 16, 7),
    child: Text(
      text,
      style: Theme.of(context).textTheme.labelMedium?.copyWith(
        color: Theme.of(context).colorScheme.onSurface.withValues(alpha: .5),
        fontWeight: FontWeight.w500,
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
    minTileHeight: subtitle == null ? 52 : 60,
    contentPadding: const EdgeInsets.symmetric(horizontal: 12),
    leading: showIconBackground
        ? Container(
            width: 30,
            height: 30,
            decoration: BoxDecoration(
              color: destructive
                  ? LinliColors.systemRed
                  : Theme.of(context).colorScheme.primary,
              borderRadius: BorderRadius.circular(7),
            ),
            alignment: Alignment.center,
            child: Icon(icon, size: 18, color: Colors.white),
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
      ),
    ),
    subtitle: subtitle == null
        ? null
        : Text(
            subtitle!,
            softWrap: true,
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
  });
  final IconData icon;
  final String title;
  final String body;
  final String? actionLabel;
  final VoidCallback? onAction;

  @override
  Widget build(BuildContext context) => Center(
    child: Padding(
      padding: const EdgeInsets.symmetric(horizontal: 40, vertical: 32),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(
            icon,
            color: Theme.of(
              context,
            ).colorScheme.onSurface.withValues(alpha: .22),
            size: 50,
          ),
          const SizedBox(height: 16),
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
            const SizedBox(height: 14),
            CupertinoButton(onPressed: onAction, child: Text(actionLabel!)),
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
