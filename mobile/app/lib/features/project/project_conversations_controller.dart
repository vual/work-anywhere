import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/models.dart';
import '../workspace/workspace_controller.dart';

/// Identifies one project's conversation list on one computer connection.
typedef ProjectConversationsKey = ({
  String? daemonId,
  int? generation,
  String agentId,
  String projectId,
});

ProjectConversationsKey? projectConversationsKeyFor(
  WorkspaceState? workspace,
  String projectId,
) {
  final agentId = workspace?.selectedAgentId;
  if (workspace == null || agentId == null) return null;
  return (
    daemonId: workspace.selectedDaemonId,
    generation: workspace.eventGeneration,
    agentId: agentId,
    projectId: projectId,
  );
}

class ProjectConversationsPage {
  const ProjectConversationsPage({
    this.items = const [],
    this.cursor,
    this.loaded = false,
    this.loading = false,
    this.error,
  });

  final List<ConversationSummary> items;
  final String? cursor;
  final bool loaded;
  final bool loading;
  final String? error;

  ProjectConversationsPage copyWith({
    List<ConversationSummary>? items,
    Object? cursor = _keep,
    bool? loaded,
    bool? loading,
    Object? error = _keep,
  }) => ProjectConversationsPage(
    items: items ?? this.items,
    cursor: identical(cursor, _keep) ? this.cursor : cursor as String?,
    loaded: loaded ?? this.loaded,
    loading: loading ?? this.loading,
    error: identical(error, _keep) ? this.error : error as String?,
  );
}

const _keep = Object();

final projectConversationsProvider =
    NotifierProvider<
      ProjectConversationsController,
      Map<ProjectConversationsKey, ProjectConversationsPage>
    >(ProjectConversationsController.new);

/// In-memory cache of project conversation lists for this app process.
/// Re-entering a project shows the cached page; only pull-to-refresh, or a
/// conversation created in that project, triggers a new first-page request.
class ProjectConversationsController
    extends Notifier<Map<ProjectConversationsKey, ProjectConversationsPage>> {
  final Map<ProjectConversationsKey, int> _requests = {};

  @override
  Map<ProjectConversationsKey, ProjectConversationsPage> build() {
    // Entries from another computer or connection generation can never be
    // shown again; drop them so the cache stays bounded.
    ref.listen(workspaceProvider, (previous, next) {
      final workspace = next.value;
      if (workspace == null) return;
      final retained = {
        for (final entry in state.entries)
          if (entry.key.daemonId == workspace.selectedDaemonId &&
              entry.key.generation == workspace.eventGeneration)
            entry.key: entry.value,
      };
      if (retained.length != state.length) state = retained;
    });
    return const {};
  }

  Future<void> ensureLoaded(ProjectConversationsKey key) async {
    final page = state[key];
    if (page != null && (page.loaded || page.loading)) return;
    await _fetch(key, reset: true);
  }

  Future<void> reload(ProjectConversationsKey key) => _fetch(key, reset: true);

  Future<void> loadMore(ProjectConversationsKey key) async {
    final page = state[key];
    if (page == null || page.loading || page.cursor == null) return;
    await _fetch(key, reset: false);
  }

  /// Marks every cached list of [projectId] stale so the next visit fetches.
  void invalidateProject(String projectId) {
    state = {
      for (final entry in state.entries)
        if (entry.key.projectId != projectId) entry.key: entry.value,
    };
  }

  void renameLocal(String conversationId, String title) {
    state = {
      for (final entry in state.entries)
        entry.key: entry.value.copyWith(
          items: [
            for (final item in entry.value.items)
              item.id == conversationId ? item.withTitle(title) : item,
          ],
        ),
    };
  }

  Future<void> _fetch(
    ProjectConversationsKey key, {
    required bool reset,
  }) async {
    final request = (_requests[key] ?? 0) + 1;
    _requests[key] = request;
    final previous = state[key] ?? const ProjectConversationsPage();
    final cursor = reset ? null : previous.cursor;
    _put(key, previous.copyWith(loading: true, error: null));
    try {
      final response = await ref.read(coreGatewayProvider).callFor(
        key.daemonId,
        key.generation,
        'conversation.list',
        {
          'agentId': key.agentId,
          'projectId': key.projectId,
          'limit': 15,
          'cursor': ?cursor,
          'sortKey': 'updated_at',
          'sortDirection': 'desc',
        },
      );
      if (_requests[key] != request) return;
      final result = response['result'] as Map<String, dynamic>? ?? const {};
      final next = conversationSummariesFromResult(result);
      final base = reset ? const <ConversationSummary>[] : previous.items;
      final ids = base.map((item) => item.id).toSet();
      _put(
        key,
        ProjectConversationsPage(
          items: [...base, ...next.where((item) => ids.add(item.id))],
          cursor: result['nextCursor'] as String?,
          loaded: true,
        ),
      );
    } catch (error) {
      if (_requests[key] != request) return;
      _put(
        key,
        (state[key] ?? previous).copyWith(loading: false, error: '$error'),
      );
    }
  }

  void _put(ProjectConversationsKey key, ProjectConversationsPage page) {
    state = {...state, key: page};
  }
}
