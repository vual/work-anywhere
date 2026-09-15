import 'package:flutter/material.dart';

import 'theme.dart';

class WawCircleButton extends StatelessWidget {
  const WawCircleButton({
    required this.icon,
    required this.onPressed,
    this.tooltip,
    this.size = 56,
    super.key,
  });

  final IconData icon;
  final VoidCallback? onPressed;
  final String? tooltip;
  final double size;

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
      ),
      icon: Icon(icon, size: 28),
    ),
  );
}

class WawTitleBlock extends StatelessWidget {
  const WawTitleBlock({
    required this.title,
    this.subtitle,
    this.pill = false,
    super.key,
  });

  final String title;
  final Widget? subtitle;
  final bool pill;

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
          style: WawCompact.appBarTitle,
        ),
        if (subtitle != null) ...[const SizedBox(height: 2), subtitle!],
      ],
    );
    if (!pill) return content;
    return Container(
      constraints: const BoxConstraints(minHeight: 56),
      padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 8),
      decoration: BoxDecoration(
        color: Theme.of(context).floatingSurface,
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
    margin: const EdgeInsets.only(right: 12),
    decoration: BoxDecoration(
      color: Theme.of(context).floatingSurface,
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
      borderRadius: BorderRadius.circular(28),
      boxShadow: Theme.of(context).floatingShadow,
    ),
    child: child,
  );
}
