import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/localization.dart';
import '../../app/chrome.dart';
import '../../app/theme.dart';
import '../../core/models.dart';
import '../workspace/workspace_controller.dart';
import 'project_conversations_controller.dart';

class ProjectScreen extends ConsumerStatefulWidget {
  const ProjectScreen({required this.projectId, super.key});
  final String projectId;

  @override
  ConsumerState<ProjectScreen> createState() => _ProjectScreenState();
}

class _ProjectScreenState extends ConsumerState<ProjectScreen> {
  // Loads the first page once the workspace knows this project's key; the
  // workspace may still be loading when the screen opens (cold start, link).
  void _ensureLoaded(ProjectConversationsKey? key) {
    if (key == null) return;
    Future<void>.microtask(() {
      if (mounted) {
        ref.read(projectConversationsProvider.notifier).ensureLoaded(key);
      }
    });
  }

  ProjectConversationsKey? get _key => projectConversationsKeyFor(
    ref.read(workspaceProvider).value,
    widget.projectId,
  );

  Future<void> _reload() async {
    final key = _key;
    if (key == null) return;
    await ref.read(projectConversationsProvider.notifier).reload(key);
  }

  Future<void> _loadMore() async {
    final key = _key;
    if (key == null) return;
    await ref.read(projectConversationsProvider.notifier).loadMore(key);
  }

  @override
  Widget build(BuildContext context) {
    final workspace = ref.watch(workspaceProvider).value;
    final key = projectConversationsKeyFor(workspace, widget.projectId);
    final page = key == null
        ? null
        : ref.watch(projectConversationsProvider.select((value) => value[key]));
    if (page == null) _ensureLoaded(key);
    final items = page?.items ?? const <ConversationSummary>[];
    final loading = key != null && (page == null || page.loading);
    final project = workspace?.projects
        .where((item) => item.id == widget.projectId)
        .firstOrNull;
    final daemon = workspace?.daemons
        .where((item) => item.id == workspace.selectedDaemonId)
        .firstOrNull;
    final online = daemon?.phase == ConnectionPhase.online;
    final computerName = daemon?.name ?? context.l10n.computer;
    final agentName = workspace?.selectedAgent?.name;
    final subtitleText = agentName == null
        ? computerName
        : '$computerName · $agentName';
    return Scaffold(
      appBar: AppBar(
        automaticallyImplyLeading: false,
        toolbarHeight: WawBackButton.toolbarHeight,
        leadingWidth: WawBackButton.leadingWidth,
        leading: WawBackButton(
          tooltip: context.l10n.back,
          onPressed: () => context.pop(),
        ),
        titleSpacing: 4,
        title: Padding(
          key: const Key('project-title-pill'),
          padding: const EdgeInsets.only(right: WawBackButton.edgePadding),
          child: SizedBox(
            width: double.infinity,
            child: WawTitleBlock(
              title: project?.name ?? context.l10n.project,
              pill: true,
              titleStyle: WawCompact.conversationTitle,
              pillMinHeight: 48,
              pillPadding: const EdgeInsets.symmetric(
                horizontal: 14,
                vertical: 5,
              ),
              subtitle: Semantics(
                label:
                    '${online ? context.l10n.connected : context.l10n.offline}, '
                    '$subtitleText',
                excludeSemantics: true,
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    WawStatusDot(online: online),
                    const SizedBox(width: 5),
                    Flexible(
                      child: Text(
                        subtitleText,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: WawCompact.metadata.copyWith(
                          color: Theme.of(context).mutedInk,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
      body: page?.error != null && items.isEmpty && !loading
          ? Center(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(context.l10n.loadProjectFailed),
                  TextButton(
                    onPressed: _reload,
                    child: Text(context.l10n.retry),
                  ),
                ],
              ),
            )
          : RefreshIndicator(
              onRefresh: _reload,
              child: ListView.builder(
                physics: const AlwaysScrollableScrollPhysics(),
                padding: const EdgeInsets.only(top: 12, bottom: 120),
                itemCount: items.length + 1,
                itemBuilder: (context, index) {
                  if (index == items.length) {
                    if (loading) {
                      return const Padding(
                        padding: EdgeInsets.all(18),
                        child: Center(child: CircularProgressIndicator()),
                      );
                    }
                    if (page?.cursor != null) {
                      return TextButton(
                        onPressed: _loadMore,
                        child: Text(context.l10n.showMore),
                      );
                    }
                    return const SizedBox(height: 18);
                  }
                  final item = items[index];
                  return ListTile(
                    minTileHeight: WawCompact.listTileHeight,
                    contentPadding: const EdgeInsets.symmetric(
                      horizontal: WawCompact.horizontalPadding,
                    ),
                    title: Text(
                      item.title,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: WawCompact.listTitle,
                    ),
                    trailing: item.needsAttention
                        ? Semantics(
                            label: context.l10n.needsAttention,
                            child: const Icon(
                              Icons.error_outline_rounded,
                              color: wawAttention,
                              size: 28,
                            ),
                          )
                        : item.running
                        ? const SizedBox.square(
                            dimension: 19,
                            child: CircularProgressIndicator(
                              color: wawBlue,
                              strokeWidth: 2.5,
                            ),
                          )
                        : item.externallyActive
                        ? const OpenOnComputerIndicator()
                        : Text(
                            _projectRelativeTime(context, item.updatedAt),
                            style: WawCompact.metadata.copyWith(
                              color: Theme.of(context).mutedInk,
                            ),
                          ),
                    onTap: () => context.push(
                      '/conversation/${item.id}',
                      extra: item.projectName ?? project?.name,
                    ),
                  );
                },
              ),
            ),
      floatingActionButtonLocation: FloatingActionButtonLocation.endFloat,
      floatingActionButton:
          workspace?.selectedAgent?.capabilities.createConversation == false
          ? null
          : Padding(
              // Align the FAB's right edge with the list content and lift it
              // to the shared floating-control height.
              padding: const EdgeInsets.only(
                right:
                    WawCompact.horizontalPadding - kFloatingActionButtonMargin,
                bottom: WawBottomInset.restingLift,
              ),
              child: FloatingActionButton(
                onPressed: () => context.push(
                  '/project/${widget.projectId}/new',
                  extra: project?.name ?? context.l10n.project,
                ),
                tooltip: context.l10n.newConversation,
                backgroundColor: wawBlue,
                foregroundColor: Colors.white,
                shape: const CircleBorder(),
                child: const Icon(Icons.edit_outlined),
              ),
            ),
    );
  }
}

String _projectRelativeTime(BuildContext context, int seconds) {
  if (seconds <= 0) return '';
  final difference = DateTime.now().difference(
    DateTime.fromMillisecondsSinceEpoch(seconds * 1000),
  );
  if (difference.inMinutes < 1) return context.l10n.justNow;
  if (difference.inHours < 1) {
    return context.l10n.minutesAgo(difference.inMinutes);
  }
  if (difference.inDays < 1) return context.l10n.hoursAgo(difference.inHours);
  if (difference.inDays < 7) return context.l10n.daysAgo(difference.inDays);
  return context.l10n.weeksAgo((difference.inDays / 7).floor());
}

extension _FirstOrNull<T> on Iterable<T> {
  T? get firstOrNull => isEmpty ? null : first;
}
