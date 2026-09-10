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
                name: 'My PC',
                phase: ConnectionPhase.offline,
              ),
            ],
            selectedDaemonId: daemonId,
          );
        }
        return const WorkspaceState(needsPairing: true);
      }
      final daemonId = status['daemonId'] as String? ?? 'daemon';
      return await _loadOrKeepPairing(_connectedState(daemonId));
    } catch (_) {
      return const WorkspaceState(needsPairing: true);
    }
  }

  Future<void> pair(String uri) async {
    final previous = state.value ?? const WorkspaceState(needsPairing: true);
    Map<String, dynamic> status;
    try {
      final envelope = await _gateway.pair(uri.trim());
      status = envelope['result'] as Map<String, dynamic>? ?? envelope;
      final daemonId = status['daemonId'] as String? ?? 'daemon';
      final preferences = await SharedPreferences.getInstance();
      await preferences.setString('lastDaemonId', daemonId);
      await _gateway.persistCredentials();
    } catch (_) {
      state = AsyncData(previous);
      rethrow;
    }

    final daemonId = status['daemonId'] as String? ?? 'daemon';
    state = AsyncData(await _loadOrKeepPairing(_connectedState(daemonId)));
  }

  WorkspaceState _connectedState(String daemonId) => WorkspaceState(
    needsPairing: false,
    daemons: [
      DaemonSummary(id: daemonId, name: 'My PC', phase: ConnectionPhase.online),
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
    final preferences = await SharedPreferences.getInstance();
    final stored = preferences.getString('lastAgent:${base.selectedDaemonId}');
    final selected = agentItems.any((item) => item.id == stored)
        ? stored!
        : agentItems.first.id;
    final responses = await Future.wait([
      _gateway.call('project.list', {'agentId': selected, 'limit': 10}),
      _gateway.call('conversation.list', {
        'agentId': selected,
        'limit': 30,
        'sortKey': 'updated_at',
        'sortDirection': 'desc',
      }),
    ]);
    final projectResult =
        responses[0]['result'] as Map<String, dynamic>? ?? const {};
    final conversationResult =
        responses[1]['result'] as Map<String, dynamic>? ?? const {};
    return base.copyWith(
      agents: agentItems,
      selectedAgentId: selected,
      projects: _projects(projectResult),
      conversations: _conversations(conversationResult),
      projectCursor: projectResult['nextCursor'] as String?,
    );
  }

  Future<void> selectAgent(String id) async {
    final current = state.value;
    if (current == null || current.selectedAgentId == id) return;
    final preferences = await SharedPreferences.getInstance();
    await preferences.setString('lastAgent:${current.selectedDaemonId}', id);
    state = AsyncData(
      await _loadOrKeepPairing(
        current.copyWith(
          selectedAgentId: id,
          projects: const [],
          conversations: const [],
          syncFailed: false,
        ),
      ),
    );
  }

  Future<void> refresh() async {
    final current = state.value;
    if (current == null || current.needsPairing) return;
    state = AsyncData(await _loadOrKeepPairing(current));
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

  List<ProjectSummary> _projects(Map<String, dynamic> result) =>
      ((result['items'] as List<dynamic>?) ?? const [])
          .cast<Map<String, dynamic>>()
          .map(ProjectSummary.fromJson)
          .toList(growable: false);
  List<ConversationSummary> _conversations(Map<String, dynamic> result) =>
      ((result['items'] as List<dynamic>?) ?? const [])
          .cast<Map<String, dynamic>>()
          .map(ConversationSummary.fromJson)
          .toList(growable: false);
}

extension _FirstOrNull<T> on Iterable<T> {
  T? get firstOrNull => isEmpty ? null : first;
}
