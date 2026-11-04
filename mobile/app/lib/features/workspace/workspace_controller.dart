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

/// In-memory projection of one agent's project and recent lists. It lives only
/// for this app process and is dropped when the computer or connection
/// generation changes; Desktop stays authoritative.
class AgentContent {
  const AgentContent({
    this.projects = const [],
    this.conversations = const [],
    this.projectCursor,
  });

  final List<ProjectSummary> projects;
  final List<ConversationSummary> conversations;
  final String? projectCursor;
}

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
    this.agentCache = const {},
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
  final Map<String, AgentContent> agentCache;

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
    Map<String, AgentContent>? agentCache,
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
    agentCache: agentCache ?? this.agentCache,
  );

  WorkspaceState withAgentContent(String agentId, AgentContent content) =>
      copyWith(agentCache: {...agentCache, agentId: content});
}

class WorkspaceController extends AsyncNotifier<WorkspaceState> {
  int _selectionGeneration = 0;
  int _catalogChanges = 0;
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
        final selectedId =
            (status.containsKey('pairings')
                    ? status['activeDaemonId']
                    : status['daemonId'])
                as String?;
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

  // Native catalog writes and rollback are authoritative. A later operation
  // invalidates every pending load from an earlier selection.
  Future<void> _mutateCatalog(
    Future<Map<String, dynamic>> Function() action,
  ) async {
    final generation = ++_selectionGeneration;
    _catalogChanges++;
    try {
      final envelope = await action();
      if (generation != _selectionGeneration) return;
      final result = envelope['result'] as Map<String, dynamic>? ?? envelope;
      final next = _withCatalog(current: state.requireValue, result: result);
      state = AsyncData(next);
      final loaded = await _loadOrKeepPairing(next);
      if (generation == _selectionGeneration) state = AsyncData(loaded);
    } catch (_) {
      if (generation == _selectionGeneration) {
        try {
          final envelope = await _gateway.status();
          if (generation == _selectionGeneration) {
            final result =
                envelope['result'] as Map<String, dynamic>? ?? envelope;
            final next = _withCatalog(
              current: state.requireValue,
              result: result,
            );
            state = AsyncData(next.copyWith(syncFailed: true));
            final loaded = await _loadOrKeepPairing(next);
            if (generation == _selectionGeneration) {
              state = AsyncData(loaded.copyWith(syncFailed: true));
            }
          }
        } catch (_) {
          if (generation == _selectionGeneration) {
            state = AsyncData(state.requireValue.copyWith(syncFailed: true));
          }
        }
      }
      rethrow;
    } finally {
      _catalogChanges--;
      // Native operations can complete in a different order. Reconcile the
      // final Core selection after the last outstanding catalog operation.
      if (_catalogChanges == 0 && generation != _selectionGeneration) {
        await _reconcileCatalog();
      }
    }
  }

  Future<void> _reconcileCatalog() async {
    final generation = ++_selectionGeneration;
    try {
      final envelope = await _gateway.status();
      if (generation != _selectionGeneration) return;
      final result = envelope['result'] as Map<String, dynamic>? ?? envelope;
      final next = _withCatalog(current: state.requireValue, result: result);
      state = AsyncData(next);
      final loaded = await _loadOrKeepPairing(next);
      if (generation == _selectionGeneration) state = AsyncData(loaded);
    } catch (_) {
      if (generation == _selectionGeneration) {
        state = AsyncData(state.requireValue.copyWith(syncFailed: true));
      }
    }
  }

  Future<void> selectDaemon(String id) async {
    final current = state.value;
    if (current == null ||
        current.selectedDaemonId == id ||
        !current.daemons.any((item) => item.id == id && item.enabled)) {
      return;
    }
    try {
      await _mutateCatalog(() => _gateway.selectPairing(id));
    } catch (_) {
      // The authoritative catalog has been re-read; expose sync state in UI.
    }
  }

  Future<void> setDaemonEnabled(String id, bool enabled) =>
      _mutateCatalog(() => _gateway.setPairingEnabled(id, enabled));

  Future<void> reorderDaemons(int oldIndex, int newIndex) async {
    final current = state.value;
    if (current == null) return;
    final reordered = [...current.daemons];
    final item = reordered.removeAt(oldIndex);
    reordered.insert(newIndex, item);
    await _mutateCatalog(
      () => _gateway.reorderPairings(reordered.map((item) => item.id).toList()),
    );
  }

  Future<DaemonSummary?> updateDaemon(
    String id,
    String name,
    String relayUrl,
  ) async {
    await _mutateCatalog(() => _gateway.updatePairing(id, name, relayUrl));
    return state.requireValue.daemons
        .where((item) => item.id == id)
        .firstOrNull;
  }

  Future<void> removeDaemon(String id) async {
    await _mutateCatalog(() => _gateway.removePairing(id));
    try {
      final preferences = await SharedPreferences.getInstance();
      await preferences.remove('lastDaemonName:$id');
      await preferences.remove('lastAgent:$id');
      final selected = state.requireValue.selectedDaemonId;
      if (selected == null) {
        await preferences.remove('lastDaemonId');
      } else {
        await preferences.setString('lastDaemonId', selected);
      }
    } catch (_) {
      /* The catalog remains authoritative. */
    }
  }

  Future<void> probeDaemon(String id) =>
      _mutateCatalog(() => _gateway.probePairing(id));

  Future<Map<String, dynamic>> _callFor(
    WorkspaceState base,
    String method,
    Map<String, dynamic> params,
  ) => _gateway.callFor(
    base.selectedDaemonId,
    base.eventGeneration,
    method,
    params,
  );

  Future<WorkspaceState> _load(WorkspaceState base) async {
    if (base.selectedDaemonId == null) return base;
    final preferences = await SharedPreferences.getInstance();
    final agentsResponse = await _callFor(base, 'agent.list', const {});
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
        agentCache: const {},
      );
    }
    final stored = preferences.getString('lastAgent:${base.selectedDaemonId}');
    final selected = agentItems.any((item) => item.id == stored)
        ? stored!
        : agentItems.first.id;
    final agentIds = agentItems.map((item) => item.id).toSet();
    final selectedBase = base.copyWith(
      agents: agentItems,
      selectedAgentId: selected,
      projects: const [],
      conversations: const [],
      projectCursor: null,
      agentCache: {
        for (final entry in base.agentCache.entries)
          if (agentIds.contains(entry.key)) entry.key: entry.value,
      },
    );
    return _loadAgentContent(selectedBase);
  }

  Future<WorkspaceState> _loadAgentContent(WorkspaceState base) async {
    final selected = base.selectedAgentId;
    if (selected == null) return base.copyWith(syncFailed: false);
    try {
      final responses = await Future.wait([
        _callFor(base, 'project.list', {'agentId': selected, 'limit': 10}),
        _callFor(base, 'conversation.list', {
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
      final content = AgentContent(
        projects: _projects(projectResult),
        conversations: _conversations(conversationResult),
        projectCursor: projectResult['nextCursor'] as String?,
      );
      return base
          .copyWith(
            projects: content.projects,
            conversations: content.conversations,
            projectCursor: content.projectCursor,
            syncFailed: false,
          )
          .withAgentContent(selected, content);
    } catch (_) {
      return base.copyWith(syncFailed: true);
    }
  }

  /// Switches agents. A previously loaded agent is shown from the in-memory
  /// cache without a request; pull-to-refresh reloads it explicitly.
  Future<void> selectAgent(String id) async {
    if (_catalogChanges > 0) return;
    final current = state.value;
    if (current == null || current.selectedAgentId == id) return;
    final generation = ++_selectionGeneration;
    final cached = current.agentCache[id];
    final selected = current.copyWith(
      selectedAgentId: id,
      projects: cached?.projects ?? const [],
      conversations: cached?.conversations ?? const [],
      projectCursor: cached?.projectCursor,
      syncFailed: false,
    );
    state = AsyncData(selected);
    final preferences = await SharedPreferences.getInstance();
    await preferences.setString('lastAgent:${current.selectedDaemonId}', id);
    if (cached != null) return;
    final loaded = await _loadAgentContent(selected);
    if (generation == _selectionGeneration) state = AsyncData(loaded);
  }

  /// Applies a confirmed rename to the visible list and every cached agent.
  void renameConversationLocal(String id, String title) {
    final current = state.value;
    if (current == null) return;
    List<ConversationSummary> rename(List<ConversationSummary> items) => [
      for (final item in items) item.id == id ? item.withTitle(title) : item,
    ];
    state = AsyncData(
      current.copyWith(
        conversations: rename(current.conversations),
        agentCache: {
          for (final entry in current.agentCache.entries)
            entry.key: AgentContent(
              projects: entry.value.projects,
              conversations: rename(entry.value.conversations),
              projectCursor: entry.value.projectCursor,
            ),
        },
      ),
    );
  }

  Future<void> refresh() async {
    if (_catalogChanges > 0) return;
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
    final generation = _selectionGeneration;
    final response = await _callFor(current, 'project.list', {
      'agentId': current.selectedAgentId,
      'limit': 10,
      'cursor': current.projectCursor,
    });
    if (generation != _selectionGeneration) return;
    final result = response['result'] as Map<String, dynamic>? ?? const {};
    final projects = [...current.projects, ..._projects(result)];
    final cursor = result['nextCursor'] as String?;
    state = AsyncData(
      current
          .copyWith(projects: projects, projectCursor: cursor)
          .withAgentContent(
            current.selectedAgentId!,
            AgentContent(
              projects: projects,
              conversations: current.conversations,
              projectCursor: cursor,
            ),
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
}) {
  final daemons = _catalog(result);
  final active = result['activeDaemonId'] as String?;
  final selected = active == null || active.isEmpty ? null : active;
  final generation = (result['generation'] as num?)?.toInt();
  final same =
      selected == current.selectedDaemonId &&
      generation == current.eventGeneration;
  return current.copyWith(
    needsPairing: daemons.isEmpty,
    daemons: daemons,
    selectedDaemonId: selected,
    eventGeneration: generation,
    selectedAgentId: same ? current.selectedAgentId : null,
    agents: same ? current.agents : const [],
    projects: same ? current.projects : const [],
    conversations: same ? current.conversations : const [],
    projectCursor: same ? current.projectCursor : null,
    agentCache: same ? current.agentCache : const {},
    syncFailed: false,
  );
}
