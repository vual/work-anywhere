import 'dart:async';

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
    this.eventGeneration,
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
  final int? eventGeneration;
  final String? projectCursor;
  final String searchQuery;
  final bool syncFailed;

  AgentSummary? get selectedAgent =>
      agents.where((item) => item.id == selectedAgentId).firstOrNull;
  int get runningCount => conversations.where((item) => item.running).length;
  int get attentionCount =>
      conversations.where((item) => item.needsAttention).length;
  bool get hasEnabledDaemons => daemons.any((item) => item.enabled);

  WorkspaceState copyWith({
    bool? needsPairing,
    List<DaemonSummary>? daemons,
    List<AgentSummary>? agents,
    List<ProjectSummary>? projects,
    List<ConversationSummary>? conversations,
    Object? selectedDaemonId = _keepValue,
    Object? selectedAgentId = _keepValue,
    Object? eventGeneration = _keepValue,
    Object? projectCursor = _keepValue,
    String? searchQuery,
    bool? syncFailed,
  }) => WorkspaceState(
    needsPairing: needsPairing ?? this.needsPairing,
    daemons: daemons ?? this.daemons,
    agents: agents ?? this.agents,
    projects: projects ?? this.projects,
    conversations: conversations ?? this.conversations,
    selectedDaemonId: identical(selectedDaemonId, _keepValue)
        ? this.selectedDaemonId
        : selectedDaemonId as String?,
    selectedAgentId: identical(selectedAgentId, _keepValue)
        ? this.selectedAgentId
        : selectedAgentId as String?,
    eventGeneration: identical(eventGeneration, _keepValue)
        ? this.eventGeneration
        : eventGeneration as int?,
    projectCursor: identical(projectCursor, _keepValue)
        ? this.projectCursor
        : projectCursor as String?,
    searchQuery: searchQuery ?? this.searchQuery,
    syncFailed: syncFailed ?? this.syncFailed,
  );
}

class WorkspaceController extends AsyncNotifier<WorkspaceState> {
  int _selectionGeneration = 0;
  StreamSubscription<Map<String, dynamic>>? _eventSubscription;
  Timer? _eventRefreshDebounce;

  CoreGateway get _gateway => ref.read(coreGatewayProvider);

  @override
  Future<WorkspaceState> build() async {
    _subscribeToEvents();
    try {
      var statusEnvelope = await _gateway.status();
      var status =
          statusEnvelope['result'] as Map<String, dynamic>? ?? statusEnvelope;
      CoreFault? restoreFault;
      var restoredCatalog = false;
      if (_catalog(status).isEmpty && status['connected'] != true) {
        try {
          statusEnvelope = await _gateway.restoreStored();
          status =
              statusEnvelope['result'] as Map<String, dynamic>? ??
              statusEnvelope;
          restoredCatalog = status['pairings'] is List;
        } on CoreFault catch (error) {
          restoreFault = error;
        }
      }
      final storedDaemons = _catalog(status);
      if (storedDaemons.isNotEmpty) {
        final preferences = await SharedPreferences.getInstance();
        final selectedId = _selectedEnabledId(
          storedDaemons,
          preferences.getString('lastDaemonId') ?? status['activeDaemonId'],
        );
        final base = WorkspaceState(
          needsPairing: false,
          daemons: storedDaemons,
          selectedDaemonId: selectedId,
          eventGeneration: (status['generation'] as num?)?.toInt(),
        );
        var loaded = await _loadOrKeepPairing(base);
        loaded = await _probeOthers(loaded);
        return loaded;
      }
      if (status['connected'] != true) {
        final preferences = await SharedPreferences.getInstance();
        final daemonId = preferences.getString('lastDaemonId');
        if (daemonId != null &&
            !restoredCatalog &&
            restoreFault?.code != 'no_stored_pairing') {
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
            eventGeneration: (status['generation'] as num?)?.toInt(),
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
          (status['generation'] as num?)?.toInt(),
        ),
      );
    } catch (_) {
      return const WorkspaceState(needsPairing: true);
    }
  }

  void _subscribeToEvents() {
    if (_eventSubscription != null) return;
    _eventSubscription = _gateway.events.listen(_handleEvent, onError: (_) {});
    ref.onDispose(() {
      _eventRefreshDebounce?.cancel();
      unawaited(_eventSubscription?.cancel());
      _eventSubscription = null;
    });
  }

  void _handleEvent(Map<String, dynamic> event) {
    if (event['type'] != 'event') return;
    final current = state.value;
    if (current == null || current.needsPairing) return;
    final eventDaemonId = event['daemonId'] as String?;
    final eventGeneration = (event['generation'] as num?)?.toInt();
    if (eventDaemonId != null && eventDaemonId != current.selectedDaemonId) {
      return;
    }
    if (current.eventGeneration != null &&
        eventGeneration != current.eventGeneration) {
      return;
    }
    final name = (event['event'] as String? ?? '').toLowerCase();
    if (!name.contains('action')) return;
    _eventRefreshDebounce?.cancel();
    _eventRefreshDebounce = Timer(
      const Duration(milliseconds: 150),
      () => unawaited(refresh()),
    );
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
        final daemonId =
            retainedStatus['daemonId'] as String? ??
            retainedStatus['activeDaemonId'] as String?;
        if (daemonId != null) {
          await _gateway.persistCredentials();
          final preferences = await SharedPreferences.getInstance();
          await preferences.setString('lastDaemonId', daemonId);
          final catalog = _catalog(retainedStatus);
          state = AsyncData(
            WorkspaceState(
              needsPairing: false,
              daemons: catalog.isEmpty
                  ? [
                      DaemonSummary(
                        id: daemonId,
                        name: preferences.getString('lastDaemonName:$daemonId'),
                        phase: ConnectionPhase.offline,
                      ),
                    ]
                  : catalog,
              selectedDaemonId: daemonId,
              eventGeneration: (retainedStatus['generation'] as num?)?.toInt(),
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

    final daemonId =
        status['daemonId'] as String? ??
        status['activeDaemonId'] as String? ??
        'daemon';
    final preferences = await SharedPreferences.getInstance();
    final daemons = _catalog(status);
    state = AsyncData(
      await _loadOrKeepPairing(
        daemons.isEmpty
            ? _connectedState(
                daemonId,
                computerName ??
                    preferences.getString('lastDaemonName:$daemonId'),
                (status['generation'] as num?)?.toInt(),
              )
            : WorkspaceState(
                needsPairing: false,
                daemons: daemons,
                selectedDaemonId: daemonId,
                eventGeneration: (status['generation'] as num?)?.toInt(),
              ),
      ),
    );
  }

  WorkspaceState _connectedState(
    String daemonId, [
    String? name,
    int? eventGeneration,
  ]) => WorkspaceState(
    needsPairing: false,
    daemons: [
      DaemonSummary(id: daemonId, name: name, phase: ConnectionPhase.online),
    ],
    selectedDaemonId: daemonId,
    eventGeneration: eventGeneration,
  );

  Future<WorkspaceState> _loadOrKeepPairing(WorkspaceState base) async {
    try {
      return await _load(base.copyWith(syncFailed: false));
    } catch (_) {
      return base.copyWith(syncFailed: true);
    }
  }

  Future<WorkspaceState> _probeOthers(WorkspaceState base) async {
    var current = base;
    for (final daemon in base.daemons.where(
      (item) => item.enabled && item.id != base.selectedDaemonId,
    )) {
      try {
        final envelope = await _gateway.probePairing(daemon.id);
        final result = envelope['result'] as Map<String, dynamic>? ?? envelope;
        current = _mergeCatalog(current: current, result: result);
      } catch (_) {
        // Probe failures are represented by the daemon's offline phase.
      }
    }
    return current;
  }

  Future<void> selectDaemon(String id) async {
    final current = state.value;
    final daemon = current?.daemons.where((item) => item.id == id).firstOrNull;
    if (current == null ||
        daemon == null ||
        !daemon.enabled ||
        current.selectedDaemonId == id) {
      return;
    }
    final preferences = await SharedPreferences.getInstance();
    final generation = ++_selectionGeneration;
    state = AsyncData(
      current.copyWith(
        selectedDaemonId: id,
        selectedAgentId: null,
        agents: const [],
        projects: const [],
        conversations: const [],
        projectCursor: null,
        syncFailed: false,
        daemons: current.daemons
            .map(
              (item) => item.id == id
                  ? item.copyWith(phase: ConnectionPhase.connecting)
                  : item,
            )
            .toList(growable: false),
      ),
    );
    try {
      final envelope = await _gateway.selectPairing(id);
      final result = envelope['result'] as Map<String, dynamic>? ?? envelope;
      final loaded = await _loadOrKeepPairing(
        _withCatalog(
          current: state.requireValue,
          result: result,
          selectedId: id,
        ),
      );
      if (generation == _selectionGeneration) {
        state = AsyncData(loaded);
        try {
          await preferences.setString('lastDaemonId', id);
        } catch (_) {
          // Selection is already committed by the native catalog. Failure to
          // update this convenience preference must not desynchronize the UI.
        }
      }
    } catch (_) {
      if (generation == _selectionGeneration) {
        state = AsyncData(current.copyWith(syncFailed: true));
      }
    }
  }

  Future<void> setDaemonEnabled(String id, bool enabled) async {
    final current = state.value;
    if (current == null) return;
    final previous = current;
    state = AsyncData(
      current.copyWith(
        daemons: current.daemons
            .map(
              (item) => item.id == id
                  ? item.copyWith(
                      enabled: enabled,
                      phase: enabled
                          ? ConnectionPhase.connecting
                          : ConnectionPhase.disabled,
                    )
                  : item,
            )
            .toList(growable: false),
      ),
    );
    try {
      final envelope = await _gateway.setPairingEnabled(id, enabled);
      final result = envelope['result'] as Map<String, dynamic>? ?? envelope;
      final catalog = _catalog(result);
      state = AsyncData(
        _withCatalog(
          current: state.requireValue,
          result: result,
          selectedId: result['activeDaemonId'] as String?,
        ),
      );
      if (catalog.isNotEmpty && result['activeDaemonId'] != null) {
        await _loadOrKeepPairing(state.requireValue)
            .then((loaded) => state = AsyncData(loaded));
      }
    } catch (_) {
      state = AsyncData(previous);
      rethrow;
    }
  }

  Future<void> reorderDaemons(int oldIndex, int newIndex) async {
    final current = state.value;
    if (current == null) return;
    final reordered = [...current.daemons];
    final item = reordered.removeAt(oldIndex);
    reordered.insert(newIndex, item);
    state = AsyncData(current.copyWith(daemons: reordered));
    try {
      final envelope = await _gateway.reorderPairings(
        reordered.map((item) => item.id).toList(),
      );
      final result = envelope['result'] as Map<String, dynamic>? ?? envelope;
      state = AsyncData(
        _withCatalog(current: state.requireValue, result: result),
      );
    } catch (_) {
      state = AsyncData(current);
      rethrow;
    }
  }

  Future<DaemonSummary?> updateDaemon(
    String id,
    String name,
    String relayUrl,
  ) async {
    final envelope = await _gateway.updatePairing(id, name, relayUrl);
    final result = envelope['result'] as Map<String, dynamic>? ?? envelope;
    state = AsyncData(
      _withCatalog(current: state.requireValue, result: result),
    );
    if (state.requireValue.selectedDaemonId == id) {
      state = AsyncData(await _loadOrKeepPairing(state.requireValue));
    }
    return state.requireValue.daemons
        .where((item) => item.id == id)
        .firstOrNull;
  }

  Future<void> removeDaemon(String id) async {
    final current = state.value;
    if (current == null || !current.daemons.any((item) => item.id == id)) {
      return;
    }
    final envelope = await _gateway.removePairing(id);
    final result = envelope['result'] as Map<String, dynamic>? ?? envelope;
    final daemons = _catalog(result);
    final selectedId = _selectedEnabledId(
      daemons,
      result['activeDaemonId'] as String?,
    );
    ++_selectionGeneration;
    final next = current.copyWith(
      needsPairing: daemons.isEmpty,
      daemons: daemons,
      selectedDaemonId: selectedId,
      eventGeneration:
          (result['generation'] as num?)?.toInt() ?? current.eventGeneration,
      selectedAgentId: selectedId == current.selectedDaemonId
          ? current.selectedAgentId
          : null,
      agents: selectedId == current.selectedDaemonId
          ? current.agents
          : const [],
      projects: selectedId == current.selectedDaemonId
          ? current.projects
          : const [],
      conversations: selectedId == current.selectedDaemonId
          ? current.conversations
          : const [],
      projectCursor: selectedId == current.selectedDaemonId
          ? current.projectCursor
          : null,
      syncFailed: selectedId == current.selectedDaemonId
          ? current.syncFailed
          : false,
    );
    state = AsyncData(
      selectedId != null && selectedId != current.selectedDaemonId
          ? await _loadOrKeepPairing(next)
          : next,
    );
    try {
      final preferences = await SharedPreferences.getInstance();
      await preferences.remove('lastDaemonName:$id');
      await preferences.remove('lastAgent:$id');
      if (selectedId == null) {
        await preferences.remove('lastDaemonId');
      } else {
        await preferences.setString('lastDaemonId', selectedId);
      }
    } catch (_) {
      // The native encrypted catalog is authoritative; these preferences are
      // only conveniences for restoring the most recent selection.
    }
  }

  Future<void> probeDaemon(String id) async {
    final envelope = await _gateway.probePairing(id);
    final result = envelope['result'] as Map<String, dynamic>? ?? envelope;
    state = AsyncData(
      _withCatalog(current: state.requireValue, result: result),
    );
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

List<DaemonSummary> _catalog(Map<String, dynamic> value) {
  final items = value['pairings'];
  if (items is List) {
    return items
        .whereType<Map<String, dynamic>>()
        .map(DaemonSummary.fromJson)
        .toList(growable: false)
      ..sort((a, b) => a.order.compareTo(b.order));
  }
  final daemonId = value['daemonId'] as String?;
  if (daemonId == null) return const [];
  return [
    DaemonSummary(
      id: daemonId,
      name: value['computerName'] as String?,
      phase: connectionPhaseFromString(
        value['connected'] == true ? 'online' : 'offline',
      ),
    ),
  ];
}

String? _selectedEnabledId(List<DaemonSummary> items, String? preferred) {
  if (preferred != null &&
      items.any((item) => item.id == preferred && item.enabled)) {
    return preferred;
  }
  return items.where((item) => item.enabled).firstOrNull?.id;
}

WorkspaceState _mergeCatalog({
  required WorkspaceState current,
  required Map<String, dynamic> result,
}) {
  final daemons = _catalog(result);
  return daemons.isEmpty ? current : current.copyWith(daemons: daemons);
}

WorkspaceState _withCatalog({
  required WorkspaceState current,
  required Map<String, dynamic> result,
  String? selectedId,
}) {
  final daemons = _catalog(result);
  final resolved = selectedId ?? result['activeDaemonId'] as String?;
  final selected =
      resolved != null &&
          daemons.any((item) => item.id == resolved && item.enabled)
      ? resolved
      : _selectedEnabledId(daemons, current.selectedDaemonId);
  return current.copyWith(
    daemons: daemons.isEmpty ? current.daemons : daemons,
    selectedDaemonId: selected,
    eventGeneration:
        (result['generation'] as num?)?.toInt() ?? current.eventGeneration,
    selectedAgentId: null,
    agents: const [],
    projects: const [],
    conversations: const [],
    projectCursor: null,
    syncFailed: false,
  );
}
