import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_svg/flutter_svg.dart';
import 'package:go_router/go_router.dart';

import '../../app/theme.dart';
import '../../app/localization.dart';
import '../../core/models.dart';
import '../pairing/pairing_screen.dart';
import 'workspace_controller.dart';

class WorkspaceGate extends ConsumerWidget {
  const WorkspaceGate({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) => ref
      .watch(workspaceProvider)
      .when(
        loading: () =>
            const Scaffold(body: Center(child: CircularProgressIndicator())),
        error: (error, stack) => Scaffold(
          body: _Failure(onRetry: () => ref.invalidate(workspaceProvider)),
        ),
        data: (data) => data.needsPairing
            ? const PairingScreen()
            : WorkspaceScreen(data: data),
      );
}

class WorkspaceScreen extends ConsumerWidget {
  const WorkspaceScreen({required this.data, super.key});
  final WorkspaceState data;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final query = data.searchQuery.trim().toLowerCase();
    final projects = query.isEmpty
        ? data.projects
        : data.projects
              .where(
                (item) =>
                    '${item.name} ${item.root}'.toLowerCase().contains(query),
              )
              .toList();
    final conversations = query.isEmpty
        ? data.conversations
        : data.conversations
              .where(
                (item) => '${item.title} ${item.preview}'
                    .toLowerCase()
                    .contains(query),
              )
              .toList();
    return Scaffold(
      body: SafeArea(
        child: RefreshIndicator(
          onRefresh: () => ref.read(workspaceProvider.notifier).refresh(),
          child: CustomScrollView(
            slivers: [
              SliverToBoxAdapter(child: _TopBar(data: data)),
              if (data.syncFailed)
                SliverToBoxAdapter(
                  child: _SyncFailure(
                    onRetry: () =>
                        ref.read(workspaceProvider.notifier).refresh(),
                  ),
                ),
              SliverToBoxAdapter(child: _AgentRail(data: data)),
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(22, 22, 22, 0),
                sliver: SliverToBoxAdapter(
                  child: _SectionTitle(
                    title: query.isEmpty
                        ? context.l10n.projects
                        : context.l10n.projectSearchResults,
                    trailing: query.isEmpty
                        ? context.l10n.recentProjects
                        : context.l10n.resultCount(projects.length),
                  ),
                ),
              ),
              if (projects.isEmpty)
                SliverToBoxAdapter(
                  child: _Empty(label: context.l10n.noProjects),
                )
              else
                SliverList.builder(
                  itemCount: projects.length,
                  itemBuilder: (context, index) =>
                      _ProjectRow(project: projects[index]),
                ),
              if (query.isEmpty && data.projectCursor != null)
                SliverToBoxAdapter(
                  child: TextButton(
                    onPressed: () =>
                        ref.read(workspaceProvider.notifier).loadMoreProjects(),
                    child: Text(context.l10n.expandProjects),
                  ),
                ),
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(22, 26, 22, 0),
                sliver: SliverToBoxAdapter(
                  child: _SectionTitle(
                    title: query.isEmpty
                        ? context.l10n.recent
                        : context.l10n.conversationSearchResults,
                    trailing: query.isEmpty
                        ? context.l10n.recentConversations
                        : context.l10n.resultCount(conversations.length),
                  ),
                ),
              ),
              if (conversations.isEmpty)
                SliverToBoxAdapter(
                  child: _Empty(label: context.l10n.noConversations),
                )
              else
                SliverList.builder(
                  itemCount: conversations.length,
                  itemBuilder: (context, index) =>
                      _ConversationRow(conversation: conversations[index]),
                ),
              const SliverToBoxAdapter(child: SizedBox(height: 104)),
            ],
          ),
        ),
      ),
      bottomSheet: SafeArea(
        minimum: const EdgeInsets.fromLTRB(22, 8, 22, 18),
        child: TextField(
          onChanged: ref.read(workspaceProvider.notifier).setSearch,
          decoration: InputDecoration(
            prefixIcon: const Icon(Icons.search_rounded),
            hintText: context.l10n.searchProjectsConversations,
          ),
        ),
      ),
    );
  }
}

class _TopBar extends StatelessWidget {
  const _TopBar({required this.data});
  final WorkspaceState data;

  @override
  Widget build(BuildContext context) {
    final daemon = data.daemons.first;
    return Padding(
      padding: const EdgeInsets.fromLTRB(22, 16, 22, 8),
      child: SizedBox(
        height: 72,
        child: Row(
          children: [
            SvgPicture.asset('assets/waw-app-icon.svg', width: 50, height: 50),
            if (data.runningCount == 0) ...[
              const SizedBox(width: 12),
              const Text(
                'Work Any Where',
                style: TextStyle(fontWeight: FontWeight.w700, fontSize: 16),
              ),
            ],
            const Spacer(),
            if (data.runningCount > 0) ...[
              InkWell(
                borderRadius: BorderRadius.circular(22),
                onTap: () {
                  final item = data.conversations.firstWhere(
                    (item) => item.running,
                  );
                  context.push('/conversation/${item.id}');
                },
                child: Padding(
                  padding: const EdgeInsets.all(10),
                  child: Row(
                    children: [
                      const SizedBox.square(
                        dimension: 16,
                        child: CircularProgressIndicator(
                          strokeWidth: 2.4,
                          color: wawBlue,
                        ),
                      ),
                      const SizedBox(width: 8),
                      Text(context.l10n.runningCount(data.runningCount)),
                    ],
                  ),
                ),
              ),
              const Spacer(),
            ],
            Semantics(
              button: true,
              label: context.l10n.daemonSwitchLabel(
                daemon.name,
                _statusLabel(context, daemon.phase),
              ),
              child: InkWell(
                borderRadius: BorderRadius.circular(28),
                onTap: () => _showDaemons(context, data.daemons),
                child: Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 17,
                    vertical: 10,
                  ),
                  decoration: BoxDecoration(
                    color: Theme.of(context).cardColor,
                    borderRadius: BorderRadius.circular(28),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      _StatusDot(daemon.phase),
                      const SizedBox(width: 10),
                      Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            daemon.name,
                            style: const TextStyle(fontWeight: FontWeight.w700),
                          ),
                          Text(
                            _statusLabel(context, daemon.phase),
                            style: TextStyle(fontSize: 12, color: Colors.grey),
                          ),
                        ],
                      ),
                      const SizedBox(width: 10),
                      const Icon(Icons.keyboard_arrow_down_rounded, size: 18),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _showDaemons(BuildContext context, List<DaemonSummary> items) {
    showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final item in items)
              ListTile(
                leading: _StatusDot(item.phase),
                title: Text(item.name),
                subtitle: Text(_statusLabel(context, item.phase)),
                trailing: item.id == data.selectedDaemonId
                    ? const Icon(Icons.check_rounded)
                    : null,
              ),
            const Divider(),
            ListTile(
              leading: const Icon(Icons.add_rounded),
              title: Text(context.l10n.addComputer),
              onTap: () => context.push('/pair'),
            ),
            ListTile(
              leading: const Icon(Icons.settings_outlined),
              title: Text(context.l10n.manageComputers),
            ),
          ],
        ),
      ),
    );
  }
}

class _AgentRail extends ConsumerWidget {
  const _AgentRail({required this.data});
  final WorkspaceState data;

  @override
  Widget build(BuildContext context, WidgetRef ref) => SizedBox(
    height: 62,
    child: ListView.separated(
      padding: const EdgeInsets.symmetric(horizontal: 22, vertical: 7),
      scrollDirection: Axis.horizontal,
      itemCount: data.agents.length,
      separatorBuilder: (_, _) => const SizedBox(width: 10),
      itemBuilder: (context, index) {
        final agent = data.agents[index];
        final selected = agent.id == data.selectedAgentId;
        return ChoiceChip(
          selected: selected,
          onSelected: (_) =>
              ref.read(workspaceProvider.notifier).selectAgent(agent.id),
          label: Text(agent.name),
          avatar: Text(
            agent.kind == 'codex'
                ? 'C'
                : agent.kind.characters.first.toUpperCase(),
          ),
          showCheckmark: false,
          selectedColor: Colors.black,
          labelStyle: TextStyle(
            color: selected ? Colors.white : null,
            fontWeight: FontWeight.w600,
          ),
          shape: const StadiumBorder(side: BorderSide.none),
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 12),
        );
      },
    ),
  );
}

class _ProjectRow extends StatelessWidget {
  const _ProjectRow({required this.project});
  final ProjectSummary project;
  @override
  Widget build(BuildContext context) => ListTile(
    minTileHeight: 74,
    contentPadding: const EdgeInsets.symmetric(horizontal: 22),
    leading: const Icon(Icons.folder_outlined, size: 30),
    title: Text(
      project.name,
      maxLines: 1,
      overflow: TextOverflow.ellipsis,
      style: const TextStyle(fontWeight: FontWeight.w600),
    ),
    subtitle: project.root.isEmpty
        ? null
        : Text(project.root, maxLines: 1, overflow: TextOverflow.ellipsis),
    trailing: Text(
      _relativeTime(context, project.updatedAt),
      style: const TextStyle(color: Colors.grey),
    ),
    onTap: () => context.push('/project/${project.id}'),
  );
}

class _ConversationRow extends StatelessWidget {
  const _ConversationRow({required this.conversation});
  final ConversationSummary conversation;
  @override
  Widget build(BuildContext context) => ListTile(
    minTileHeight: 72,
    contentPadding: const EdgeInsets.symmetric(horizontal: 22),
    title: Text(
      conversation.title,
      maxLines: 1,
      overflow: TextOverflow.ellipsis,
    ),
    subtitle: conversation.preview.isEmpty
        ? null
        : Text(
            conversation.preview,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
    trailing: conversation.running
        ? Semantics(
            label: context.l10n.running,
            child: SizedBox.square(
              dimension: 19,
              child: CircularProgressIndicator(
                strokeWidth: 2.5,
                color: wawBlue,
              ),
            ),
          )
        : Text(
            _relativeTime(context, conversation.updatedAt),
            style: const TextStyle(color: Colors.grey),
          ),
    onTap: () => context.push('/conversation/${conversation.id}'),
  );
}

class _SectionTitle extends StatelessWidget {
  const _SectionTitle({required this.title, required this.trailing});
  final String title;
  final String trailing;
  @override
  Widget build(BuildContext context) => Row(
    children: [
      Expanded(
        child: Text(
          title,
          style: Theme.of(context).textTheme.titleLarge
              ?.copyWith(fontWeight: FontWeight.w700),
        ),
      ),
      Text(trailing, style: const TextStyle(color: Colors.grey)),
    ],
  );
}

class _StatusDot extends StatelessWidget {
  const _StatusDot(this.phase);
  final ConnectionPhase phase;
  @override
  Widget build(BuildContext context) {
    if (phase == ConnectionPhase.connecting) {
      return const SizedBox.square(
        dimension: 12,
        child: CircularProgressIndicator(strokeWidth: 2),
      );
    }
    return Container(
      width: 11,
      height: 11,
      decoration: BoxDecoration(
        color: phase == ConnectionPhase.online ? wawGreen : Colors.grey,
        shape: BoxShape.circle,
      ),
    );
  }
}

class _Empty extends StatelessWidget {
  const _Empty({required this.label});
  final String label;
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.all(28),
    child: Text(label, style: const TextStyle(color: Colors.grey)),
  );
}

class _SyncFailure extends StatelessWidget {
  const _SyncFailure({required this.onRetry});
  final Future<void> Function() onRetry;

  @override
  Widget build(BuildContext context) => Container(
    margin: const EdgeInsets.fromLTRB(22, 4, 22, 8),
    padding: const EdgeInsets.fromLTRB(16, 12, 8, 12),
    decoration: BoxDecoration(
      color: Theme.of(context).colorScheme.errorContainer,
      borderRadius: BorderRadius.circular(16),
    ),
    child: Row(
      children: [
        Expanded(child: Text(context.l10n.workspaceSyncFailed)),
        TextButton(onPressed: onRetry, child: Text(context.l10n.retry)),
      ],
    ),
  );
}

class _Failure extends StatelessWidget {
  const _Failure({required this.onRetry});
  final VoidCallback onRetry;
  @override
  Widget build(BuildContext context) => Center(
    child: Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(context.l10n.loadWorkspaceFailed),
        const SizedBox(height: 12),
        OutlinedButton(onPressed: onRetry, child: Text(context.l10n.retry)),
      ],
    ),
  );
}

String _relativeTime(BuildContext context, int seconds) {
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

String _statusLabel(BuildContext context, ConnectionPhase phase) =>
    switch (phase) {
      ConnectionPhase.connecting => context.l10n.connecting,
      ConnectionPhase.online => context.l10n.secureOnline,
      ConnectionPhase.offline => context.l10n.offline,
    };
