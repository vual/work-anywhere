import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../core/core_gateway.dart';
import '../../core/models.dart';

final coreGatewayProvider = Provider<CoreGateway>((ref) => CoreGateway());
final workspaceProvider =
    AsyncNotifierProvider<WorkspaceController, WorkspaceState>(
      WorkspaceController.new,
    );

const _keepValue = Object();

class WorkspaceState {
  const WorkspaceState({
    required this.needsPairing,
    this.daemons = const [],
    this.agents = const [],
    this.projects = const [],
    this.conversations = const [],
    this.selectedDaemonId,
    this.selectedAgentId,
    this.projectCursor,
    this.searchQuery = '',
    this.syncFailed = false,
  });

  final bool needsPairing;
  final List<DaemonSummary> daemons;
  final List<AgentSummary> agents;
  final List<ProjectSummary> projects;
  final List<ConversationSummary> conversations;
  final String? selectedDaemonId;
  final String? selectedAgentId;
  final String? projectCursor;
  final String searchQuery;
  final bool syncFailed;

  AgentSummary? get selectedAgent =>
      agents.where((item) => item.id == selectedAgentId).firstOrNull;
  int get runningCount => conversations.where((item) => item.running).length;

  WorkspaceState copyWith({
    bool? needsPairing,
    List<DaemonSummary>? daemons,
    List<AgentSummary>? agents,
    List<ProjectSummary>? projects,
    List<ConversationSummary>? conversations,
    String? selectedDaemonId,
    String? selectedAgentId,
    Object? projectCursor = _keepValue,
    String? searchQuery,
    bool? syncFailed,
  }) => WorkspaceState(
    needsPairing: needsPairing ?? this.needsPairing,
    daemons: daemons ?? this.daemons,
    agents: agents ?? this.agents,
    projects: projects ?? this.projects,
    conversations: conversations ?? this.conversations,
    selectedDaemonId: selectedDaemonId ?? this.selectedDaemonId,
    selectedAgentId: selectedAgentId ?? this.selectedAgentId,
    projectCursor: identical(projectCursor, _keepValue)
        ? this.projectCursor
        : projectCursor as String?,
    searchQuery: searchQuery ?? this.searchQuery,
    syncFailed: syncFailed ?? this.syncFailed,
  );
}

class WorkspaceController extends AsyncNotifier<WorkspaceState> {
  int _selectionGeneration = 0;

  CoreGateway get _gateway => ref.read(coreGatewayProvider);

  @override
  Future<WorkspaceState> build() async {
    try {
      var statusEnvelope = await _gateway.status();
      var status =
          statusEnvelope['result'] as Map<String, dynamic>? ?? statusEnvelope;
      CoreFault? restoreFault;
      if (status['connected'] != true) {
        try {
          statusEnvelope = await _gateway.restoreStored();
          status =
              statusEnvelope['result'] as Map<String, dynamic>? ??
              statusEnvelope;
        } on CoreFault catch (error) {
          restoreFault = error;
        }
      }
      if (status['connected'] != true) {
        final preferences = await SharedPreferences.getInstance();
        final daemonId = preferences.getString('lastDaemonId');
        if (daemonId != null && restoreFault?.code != 'no_stored_pairing') {
          return WorkspaceState(
            needsPairing: false,
            daemons: [
              DaemonSummary(
                id: daemonId,
                name: preferences.getString('lastDaemonName:$daemonId'),
                phase: ConnectionPhase.offline,
              ),
            ],
            selectedDaemonId: daemonId,
          );
        }
        return const WorkspaceState(needsPairing: true);
      }
      final daemonId = status['daemonId'] as String? ?? 'daemon';
      final preferences = await SharedPreferences.getInstance();
      return await _loadOrKeepPairing(
        _connectedState(
          daemonId,
          preferences.getString('lastDaemonName:$daemonId'),
        ),
      );
    } catch (_) {
      return const WorkspaceState(needsPairing: true);
    }
  }

  Future<void> pair(String uri) async {
    final previous = state.value ?? const WorkspaceState(needsPairing: true);
    Map<String, dynamic> status;
    String? computerName;
    try {
      final envelope = await _gateway.pair(uri.trim());
      status = envelope['result'] as Map<String, dynamic>? ?? envelope;
      final daemonId = status['daemonId'] as String? ?? 'daemon';
      final pairedName = (status['computerName'] as String?)?.trim();
      computerName = pairedName;
      final preferences = await SharedPreferences.getInstance();
      await preferences.setString('lastDaemonId', daemonId);
      await _gateway.persistCredentials();
      if (pairedName != null && pairedName.isNotEmpty) {
        await preferences.setString('lastDaemonName:$daemonId', pairedName);
      }
    } catch (_) {
      var retained = false;
      try {
        final statusEnvelope = await _gateway.status();
        final retainedStatus =
            statusEnvelope['result'] as Map<String, dynamic>? ?? statusEnvelope;
        final daemonId = retainedStatus['daemonId'] as String?;
        if (daemonId != null) {
          await _gateway.persistCredentials();
          final preferences = await SharedPreferences.getInstance();
          await preferences.setString('lastDaemonId', daemonId);
          state = AsyncData(
            WorkspaceState(
              needsPairing: false,
              daemons: [
                DaemonSummary(
                  id: daemonId,
                  name: preferences.getString('lastDaemonName:$daemonId'),
                  phase: ConnectionPhase.offline,
                ),
              ],
              selectedDaemonId: daemonId,
              syncFailed: true,
            ),
          );
          retained = true;
        }
      } catch (_) {
        // The pairing did not reach a validated grant, so there are no new
        // credentials to preserve.
      }
      if (!retained) state = AsyncData(previous);
      rethrow;
    }

    final daemonId = status['daemonId'] as String? ?? 'daemon';
    final preferences = await SharedPreferences.getInstance();
    state = AsyncData(
      await _loadOrKeepPairing(
        _connectedState(
          daemonId,
          computerName ?? preferences.getString('lastDaemonName:$daemonId'),
        ),
      ),
    );
  }

  WorkspaceState _connectedState(String daemonId, [String? name]) =>
      WorkspaceState(
        needsPairing: false,
        daemons: [
          DaemonSummary(
            id: daemonId,
            name: name,
            phase: ConnectionPhase.online,
          ),
        ],
        selectedDaemonId: daemonId,
      );

  Future<WorkspaceState> _loadOrKeepPairing(WorkspaceState base) async {
    try {
      return await _load(base.copyWith(syncFailed: false));
    } catch (_) {
      return base.copyWith(syncFailed: true);
    }
  }

  Future<WorkspaceState> _load(WorkspaceState base) async {
    final preferences = await SharedPreferences.getInstance();
    final agentsResponse = await _gateway.call('agent.list', const {});
    final agentItems =
        ((agentsResponse['result'] as List<dynamic>?) ?? const [])
            .cast<Map<String, dynamic>>()
            .map(AgentSummary.fromJson)
            .toList(growable: false);
    if (agentItems.isEmpty) {
      return base.copyWith(
        agents: const [],
        projects: const [],
        conversations: const [],
      );
    }
    final stored = preferences.getString('lastAgent:${base.selectedDaemonId}');
    final selected = agentItems.any((item) => item.id == stored)
        ? stored!
        : agentItems.first.id;
    final selectedBase = base.copyWith(
      agents: agentItems,
      selectedAgentId: selected,
      projects: const [],
      conversations: const [],
      projectCursor: null,
    );
    return _loadAgentContent(selectedBase);
  }

  Future<WorkspaceState> _loadAgentContent(WorkspaceState base) async {
    final selected = base.selectedAgentId;
    if (selected == null) return base.copyWith(syncFailed: false);
    try {
      final responses = await Future.wait([
        _gateway.call('project.list', {'agentId': selected, 'limit': 10}),
        _gateway.call('conversation.list', {
          'agentId': selected,
          'limit': 20,
          'sortKey': 'updated_at',
          'sortDirection': 'desc',
        }),
      ]);
      final projectResult =
          responses[0]['result'] as Map<String, dynamic>? ?? const {};
      final conversationResult =
          responses[1]['result'] as Map<String, dynamic>? ?? const {};
      return base.copyWith(
        projects: _projects(projectResult),
        conversations: _conversations(conversationResult),
        projectCursor: projectResult['nextCursor'] as String?,
        syncFailed: false,
      );
    } catch (_) {
      return base.copyWith(syncFailed: true);
    }
  }

  Future<void> selectAgent(String id) async {
    final current = state.value;
    if (current == null || current.selectedAgentId == id) return;
    final generation = ++_selectionGeneration;
    final selected = current.copyWith(
      selectedAgentId: id,
      projects: const [],
      conversations: const [],
      projectCursor: null,
      syncFailed: false,
    );
    state = AsyncData(selected);
    final preferences = await SharedPreferences.getInstance();
    await preferences.setString('lastAgent:${current.selectedDaemonId}', id);
    final loaded = await _loadAgentContent(selected);
    if (generation == _selectionGeneration) state = AsyncData(loaded);
  }

  Future<void> refresh() async {
    final current = state.value;
    if (current == null || current.needsPairing) return;
    final generation = ++_selectionGeneration;
    final loaded = await _loadOrKeepPairing(current);
    if (generation == _selectionGeneration) state = AsyncData(loaded);
  }

  Future<void> loadMoreProjects() async {
    final current = state.value;
    if (current == null ||
        current.projectCursor == null ||
        current.selectedAgentId == null) {
      return;
    }
    final response = await _gateway.call('project.list', {
      'agentId': current.selectedAgentId,
      'limit': 10,
      'cursor': current.projectCursor,
    });
    final result = response['result'] as Map<String, dynamic>? ?? const {};
    state = AsyncData(
      current.copyWith(
        projects: [...current.projects, ..._projects(result)],
        projectCursor: result['nextCursor'] as String?,
      ),
    );
  }

  void setSearch(String value) {
    final current = state.value;
    if (current != null) {
      state = AsyncData(current.copyWith(searchQuery: value));
    }
  }

  List<ProjectSummary> _projects(Map<String, dynamic> result) {
    final projects = ((result['items'] as List<dynamic>?) ?? const [])
        .cast<Map<String, dynamic>>()
        .map(ProjectSummary.fromJson)
        .toList();
    projects.sort((a, b) {
      final byUpdatedAt = b.updatedAt.compareTo(a.updatedAt);
      return byUpdatedAt != 0 ? byUpdatedAt : b.id.compareTo(a.id);
    });
    return List.unmodifiable(projects);
  }

  List<ConversationSummary> _conversations(Map<String, dynamic> result) =>
      conversationSummariesFromResult(result);
}

extension _FirstOrNull<T> on Iterable<T> {
  T? get firstOrNull => isEmpty ? null : first;
}
