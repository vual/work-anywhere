import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

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

/// Brand mark for an agent kind; unknown kinds fall back to an initial.
class AgentIcon extends StatelessWidget {
  const AgentIcon({
    required this.kind,
    required this.name,
    this.size = 18,
    super.key,
  });

  final String kind;
  final String name;
  final double size;

  static String? assetFor(String kind) => switch (kind) {
    'codex' => 'assets/agents/codex.svg',
    'claude-code' => 'assets/agents/claude-code.svg',
    _ => null,
  };

  @override
  Widget build(BuildContext context) {
    final asset = assetFor(kind);
    if (asset == null) {
      return SizedBox.square(
        dimension: size,
        child: Center(
          child: Text(
            name.isEmpty ? '?' : name.characters.first.toUpperCase(),
            style: TextStyle(fontSize: size * .72, fontWeight: FontWeight.w700),
          ),
        ),
      );
    }
    return SvgPicture.asset(
      asset,
      width: size,
      height: size,
      excludeFromSemantics: true,
    );
  }
}

/// Connection indicator: green when online, grey otherwise.
class WawStatusDot extends StatelessWidget {
  const WawStatusDot({required this.online, this.size = 7, super.key});

  final bool online;
  final double size;

  @override
  Widget build(BuildContext context) => Container(
    width: size,
    height: size,
    decoration: BoxDecoration(
      color: online ? wawGreen : Colors.grey,
      shape: BoxShape.circle,
    ),
  );
}

/// Shared bottom placement for floating inputs (search, composer) and the
/// project FAB: one fixed lift above the system safe area, and a small gap
/// above the keyboard when it is open.
abstract final class WawBottomInset {
  static const restingLift = 16.0;
  static const keyboardGap = 8.0;
  static const inputMinHeight = 52.0;

  /// Distance from the view bottom (or the keyboard top) to the floating
  /// control, including the system safe area. A Scaffold body has the
  /// keyboard inset removed from its MediaQuery, so the view is checked too.
  static double of(BuildContext context) {
    final keyboardOpen =
        MediaQuery.viewInsetsOf(context).bottom > 0 ||
        (View.maybeOf(context)?.viewInsets.bottom ?? 0) > 0;
    return keyboardOpen
        ? keyboardGap
        : MediaQuery.paddingOf(context).bottom + restingLift;
  }
}
