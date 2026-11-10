import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_svg/flutter_svg.dart';
import 'package:go_router/go_router.dart';

import '../../app/localization.dart';
import '../../app/theme.dart';
import '../update/update_controller.dart';

/// App-level sidebar opened from the workspace logo. It holds only app-wide
/// entries; computer and agent actions stay in the computer menu.
class AppDrawer extends ConsumerStatefulWidget {
  const AppDrawer({super.key});

  static const maxWidth = 304.0;

  @override
  ConsumerState<AppDrawer> createState() => _AppDrawerState();
}

class _AppDrawerState extends ConsumerState<AppDrawer> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) ref.read(updateProvider.notifier).load();
    });
  }

  void _open(String location) {
    final router = GoRouter.of(context);
    Navigator.of(context).pop();
    router.push(location);
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final update = ref.watch(updateProvider);
    final app = update.app;
    final pending = update.pendingRelease;
    final width = math.min(
      MediaQuery.sizeOf(context).width * .8,
      AppDrawer.maxWidth,
    );
    return Drawer(
      key: const Key('app-drawer'),
      width: width,
      child: SafeArea(
        child: ListView(
          padding: const EdgeInsets.symmetric(vertical: 12),
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(
                WawCompact.horizontalPadding,
                8,
                WawCompact.horizontalPadding,
                16,
              ),
              child: Row(
                children: [
                  SvgPicture.asset(
                    'assets/waw-app-icon.svg',
                    width: 40,
                    height: 40,
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        const Text('WAW', style: WawCompact.listTitle),
                        if (app != null && app.versionName.isNotEmpty)
                          Text(
                            app.versionName,
                            key: const Key('app-drawer-version'),
                            style: WawCompact.metadata.copyWith(
                              color: Theme.of(
                                context,
                              ).colorScheme.onSurfaceVariant,
                            ),
                          ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
            ListTile(
              key: const Key('drawer-manage-computers'),
              leading: const Icon(Icons.computer_outlined),
              title: Text(l10n.manageComputers),
              onTap: () => _open('/computers'),
            ),
            ListTile(
              key: const Key('drawer-check-updates'),
              leading: const Icon(Icons.system_update_outlined),
              title: Text(l10n.checkForUpdates),
              subtitle: pending == null
                  ? null
                  : Text(l10n.newVersionSubtitle(pending.versionName)),
              onTap: () => _open('/about?check=1'),
            ),
            ListTile(
              key: const Key('drawer-about'),
              leading: const Icon(Icons.info_outline),
              title: Text(l10n.about),
              onTap: () => _open('/about'),
            ),
          ],
        ),
      ),
    );
  }
}

/// Workspace logo: opens the sidebar and carries the new-version dot.
class AppMenuLogo extends ConsumerWidget {
  const AppMenuLogo({required this.size, super.key});
  final double size;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final hasUpdate = ref.watch(
      updateProvider.select((state) => state.pendingRelease != null),
    );
    final hitSize = math.max(48.0, size);
    return Semantics(
      label: hasUpdate ? '${l10n.openMenu}, ${l10n.newVersionAvailable}' : null,
      child: IconButton(
        key: const Key('workspace-menu-button'),
        tooltip: l10n.openMenu,
        padding: EdgeInsets.zero,
        constraints: BoxConstraints.tightFor(width: hitSize, height: hitSize),
        onPressed: () => Scaffold.of(context).openDrawer(),
        icon: Badge(
          key: const Key('workspace-update-badge'),
          isLabelVisible: hasUpdate,
          smallSize: 9,
          child: SvgPicture.asset(
            'assets/waw-app-icon.svg',
            width: size,
            height: size,
          ),
        ),
      ),
    );
  }
}
