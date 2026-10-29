import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/localization.dart';
import '../../app/chrome.dart';
import '../../app/theme.dart';
import '../../core/models.dart';
import '../workspace/workspace_controller.dart';

class ProjectScreen extends ConsumerStatefulWidget {
  const ProjectScreen({required this.projectId, super.key});
  final String projectId;

  @override
  ConsumerState<ProjectScreen> createState() => _ProjectScreenState();
}

class _ProjectScreenState extends ConsumerState<ProjectScreen> {
  final List<ConversationSummary> _items = [];
  String? _cursor;
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    Future<void>.microtask(_load);
  }

  Future<void> _load() async {
    final workspace = ref.read(workspaceProvider).value;
    if (workspace == null || workspace.selectedAgentId == null) return;
    bool current() {
      if (!mounted) return false;
      final latest = ref.read(workspaceProvider).value;
      return mounted &&
          latest?.selectedDaemonId == workspace.selectedDaemonId &&
          latest?.eventGeneration == workspace.eventGeneration &&
          latest?.selectedAgentId == workspace.selectedAgentId;
    }

    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final response = await ref.read(coreGatewayProvider).callFor(
        workspace.selectedDaemonId,
        workspace.eventGeneration,
        'conversation.list',
        {
          'agentId': workspace.selectedAgentId,
          'projectId': widget.projectId,
          'limit': 15,
          if (_cursor != null) 'cursor': _cursor,
          'sortKey': 'updated_at',
          'sortDirection': 'desc',
        },
      );
      if (!current()) return;
      final result = response['result'] as Map<String, dynamic>? ?? const {};
      final next = conversationSummariesFromResult(result);
      setState(() {
        final existingIds = _items.map((item) => item.id).toSet();
        _items.addAll(next.where((item) => existingIds.add(item.id)));
        _cursor = result['nextCursor'] as String?;
      });
    } catch (error) {
      if (current()) setState(() => _error = error.toString());
    } finally {
      if (current()) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final workspace = ref.watch(workspaceProvider).value;
    final project = workspace?.projects
        .where((item) => item.id == widget.projectId)
        .firstOrNull;
    final daemon = workspace?.daemons
        .where((item) => item.id == workspace.selectedDaemonId)
        .firstOrNull;
    return Scaffold(
      appBar: AppBar(
        automaticallyImplyLeading: false,
        toolbarHeight: WawBackButton.toolbarHeight,
        leadingWidth: WawBackButton.leadingWidth,
        leading: WawBackButton(
          tooltip: context.l10n.back,
          onPressed: () => context.pop(),
        ),
        titleSpacing: 12,
        title: WawTitleBlock(
          title: project?.name ?? context.l10n.project,
          subtitle: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.computer_rounded, size: 14),
              const SizedBox(width: 5),
              Flexible(
                child: Text(
                  daemon?.name ?? context.l10n.computer,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: WawCompact.metadata.copyWith(
                    color: Theme.of(context).mutedInk,
                  ),
                ),
              ),
              const SizedBox(width: 7),
              Text(
                '• ${daemon?.phase == ConnectionPhase.online ? context.l10n.connected : context.l10n.offline}',
                style: WawCompact.metadata.copyWith(
                  color: Theme.of(context).mutedInk,
                ),
              ),
            ],
          ),
        ),
      ),
      body: _error != null && _items.isEmpty
          ? Center(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(context.l10n.loadProjectFailed),
                  TextButton(onPressed: _load, child: Text(context.l10n.retry)),
                ],
              ),
            )
          : ListView.builder(
              padding: const EdgeInsets.only(top: 12, bottom: 104),
              itemCount: _items.length + 1,
              itemBuilder: (context, index) {
                if (index == _items.length) {
                  if (_loading) {
                    return const Padding(
                      padding: EdgeInsets.all(18),
                      child: Center(child: CircularProgressIndicator()),
                    );
                  }
                  if (_cursor != null) {
                    return TextButton(
                      onPressed: _load,
                      child: Text(context.l10n.showMore),
                    );
                  }
                  return const SizedBox(height: 18);
                }
                final item = _items[index];
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
      floatingActionButtonLocation: FloatingActionButtonLocation.endFloat,
      floatingActionButton: FloatingActionButton(
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
