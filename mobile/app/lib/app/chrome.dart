import 'package:flutter/material.dart';

import 'localization.dart';
import 'theme.dart';

class WawBackButton extends StatelessWidget {
  const WawBackButton({
    required this.onPressed,
    required this.tooltip,
    super.key,
  });

  static const edgePadding = 12.0;
  static const leadingWidth = edgePadding + 48;
  static const toolbarHeight = 72.0;

  final VoidCallback onPressed;
  final String tooltip;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(left: edgePadding),
    child: Center(
      child: WawCircleButton(
        icon: Icons.arrow_back_rounded,
        size: 48,
        iconSize: 24,
        tooltip: tooltip,
        onPressed: onPressed,
      ),
    ),
  );
}

class WawCircleButton extends StatelessWidget {
  const WawCircleButton({
    required this.icon,
    required this.onPressed,
    this.tooltip,
    this.size = 56,
    this.iconSize = 28,
    super.key,
  });

  final IconData icon;
  final VoidCallback? onPressed;
  final String? tooltip;
  final double size;
  final double iconSize;

  @override
  Widget build(BuildContext context) => SizedBox.square(
    dimension: size,
    child: IconButton(
      onPressed: onPressed,
      tooltip: tooltip,
      style: IconButton.styleFrom(
        backgroundColor: Theme.of(context).floatingSurface,
        foregroundColor: Theme.of(context).colorScheme.onSurface,
        shadowColor: Colors.black12,
        elevation: 1,
        shape: const CircleBorder(),
      ),
      icon: Icon(icon, size: iconSize),
    ),
  );
}

class WawTitleBlock extends StatelessWidget {
  const WawTitleBlock({
    required this.title,
    this.subtitle,
    this.pill = false,
    this.titleStyle,
    this.pillMinHeight = 56,
    this.pillPadding = const EdgeInsets.symmetric(horizontal: 18, vertical: 8),
    super.key,
  });

  final String title;
  final Widget? subtitle;
  final bool pill;
  final TextStyle? titleStyle;
  final double pillMinHeight;
  final EdgeInsetsGeometry pillPadding;

  @override
  Widget build(BuildContext context) {
    final content = Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          title,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: titleStyle ?? WawCompact.appBarTitle,
        ),
        if (subtitle != null) ...[const SizedBox(height: 2), subtitle!],
      ],
    );
    if (!pill) return content;
    return Container(
      constraints: BoxConstraints(minHeight: pillMinHeight),
      padding: pillPadding,
      decoration: BoxDecoration(
        color: Theme.of(context).floatingSurface,
        border: Border.all(color: Theme.of(context).colorScheme.outlineVariant),
        borderRadius: BorderRadius.circular(28),
        boxShadow: Theme.of(context).floatingShadow,
      ),
      child: content,
    );
  }
}

class WawActionPill extends StatelessWidget {
  const WawActionPill({required this.children, super.key});

  final List<Widget> children;

  @override
  Widget build(BuildContext context) => Container(
    margin: const EdgeInsets.only(right: WawBackButton.edgePadding),
    decoration: BoxDecoration(
      color: Theme.of(context).floatingSurface,
      border: Border.all(color: Theme.of(context).colorScheme.outlineVariant),
      borderRadius: BorderRadius.circular(28),
      boxShadow: Theme.of(context).floatingShadow,
    ),
    child: Row(mainAxisSize: MainAxisSize.min, children: children),
  );
}

class WawFloatingSurface extends StatelessWidget {
  const WawFloatingSurface({required this.child, this.padding, super.key});

  final Widget child;
  final EdgeInsetsGeometry? padding;

  @override
  Widget build(BuildContext context) => Container(
    padding: padding,
    decoration: BoxDecoration(
      color: Theme.of(context).floatingSurface,
      border: Border.all(color: Theme.of(context).colorScheme.outlineVariant),
      borderRadius: BorderRadius.circular(28),
      boxShadow: Theme.of(context).floatingShadow,
    ),
    child: child,
  );
}

/// Marks a conversation that is open in another process on the computer.
class OpenOnComputerIndicator extends StatelessWidget {
  const OpenOnComputerIndicator({super.key});

  @override
  Widget build(BuildContext context) => Semantics(
    label: context.l10n.openOnComputer,
    child: Tooltip(
      message: context.l10n.openOnComputer,
      child: Icon(
        Icons.computer_rounded,
        size: 20,
        color: Theme.of(context).mutedInk,
      ),
    ),
  );
}
