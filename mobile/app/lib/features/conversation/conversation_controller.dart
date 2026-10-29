import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';

import '../../core/core_gateway.dart';
import '../../core/models.dart';

class ConversationState {
  const ConversationState({
    this.metadata,
    this.items = const [],
    this.queuedSubmissions = const [],
    this.actions = const [],
    this.planConfirmation,
    this.olderCursor,
    this.revision = 0,
    this.hasSnapshot = false,
    this.queueAvailable = true,
    this.sequence = 0,
    this.syncEpoch,
    this.loading = false,
    this.loadingOlder = false,
    this.online = true,
    this.bodyError,
    this.actionError,
    this.olderError,
  });

  final Map<String, dynamic>? metadata;
  final List<Map<String, dynamic>> items;
  final List<QueuedSubmission> queuedSubmissions;
  final List<Map<String, dynamic>> actions;
  final Map<String, dynamic>? planConfirmation;
  final String? olderCursor;
  final int revision;
  final bool hasSnapshot;
  final bool queueAvailable;
  final int sequence;
  final String? syncEpoch;
  final bool loading;
  final bool loadingOlder;
  final bool online;
  final CoreFault? bodyError;
  final CoreFault? actionError;
  final CoreFault? olderError;

  bool get hasOlder => olderCursor != null && olderCursor!.isNotEmpty;
}

class ConversationController extends ChangeNotifier {
  ConversationController({
    required this.gateway,
    required this.agentId,
    required this.threadId,
    this.daemonId,
    this.selectionGeneration,
    this.daemonIdProvider,
    this.selectionGenerationProvider,
  });

  final CoreGateway gateway;
  final String agentId;
  final String threadId;
  final String? daemonId;
  final int? selectionGeneration;
  final String? Function()? daemonIdProvider;
  final int? Function()? selectionGenerationProvider;
  ConversationState _state = const ConversationState();
  ConversationSettings? _settings;
  bool _disposed = false;
  Future<bool>? _snapshotRefresh;
  bool get isCurrent =>
      !_disposed &&
      (daemonId == null ||
          (daemonIdProvider?.call() ?? daemonId) == daemonId) &&
      (selectionGeneration == null ||
          (selectionGenerationProvider?.call() ?? selectionGeneration) ==
              selectionGeneration);
  bool _syncing = false;
  bool _loadedOlder = false;
  bool _resetTimelineOnNextSnapshot = false;
  bool _eventRefreshRunning = false;
  bool _eventRefreshPending = false;
  bool _actionRefreshRunning = false;
  bool _actionRefreshPending = false;
  int _pendingEventSequence = 0;
  final Map<String, _LiveDeltaBuffer> _liveDeltas = {};
  int _liveDeltaBytes = 0;
  Timer? _liveTimer;
  Timer? _eventDebounce;
  Timer? _deltaRenderTimer;
  StreamSubscription<Map<String, dynamic>>? _eventSubscription;

  ConversationState get state => _state;
  ConversationSettings? get settings => _settings;

  void startLiveSync() {
    _liveTimer?.cancel();
    _eventSubscription?.cancel();
    _liveTimer = null;
    _eventSubscription = gateway.events.listen(
      _handleLiveEvent,
      onError: (_, _) {
        _setOnline(false);
        _startPollingFallback();
      },
      onDone: () {
        _setOnline(false);
        _startPollingFallback();
      },
    );
  }

  void _startPollingFallback() {
    if (_disposed || _liveTimer != null) return;
    unawaited(_syncLive());
    _liveTimer = Timer.periodic(const Duration(seconds: 2), (_) => _syncLive());
  }

  void _handleLiveEvent(Map<String, dynamic> event) {
    final expectedDaemonId = daemonIdProvider?.call() ?? daemonId;
    final expectedGeneration =
        selectionGenerationProvider?.call() ?? selectionGeneration;
    final eventDaemonId = event['daemonId'] as String?;
    final eventGeneration = (event['generation'] as num?)?.toInt();
    if (expectedGeneration != null) {
      if (eventDaemonId != expectedDaemonId ||
          eventGeneration != expectedGeneration) {
        return;
      }
    } else if (expectedDaemonId != null &&
        eventDaemonId != null &&
        eventDaemonId != expectedDaemonId) {
      return;
    }
    if (event['type'] == 'transport') {
      switch (event['state'] as String? ?? '') {
        case 'online':
          _setOnline(true);
          _liveTimer?.cancel();
          _liveTimer = null;
          unawaited(_syncLive());
          break;
        case 'disconnected':
          _setOnline(false);
          _startPollingFallback();
          break;
        case 'revoked':
        case 'closed':
          _setOnline(false);
          _liveTimer?.cancel();
          _liveTimer = null;
          break;
      }
      return;
    }
    if (event['type'] != 'event') return;
    final conversationId = event['conversationId'] as String?;
    if (conversationId != null &&
        conversationId.isNotEmpty &&
        conversationId != threadId) {
      return;
    }
    final eventName = (event['event'] as String? ?? '').toLowerCase();
    if (eventName.contains('action')) {
      _queueActionRefresh();
    }
    final eventEpoch = event['syncEpoch'] as String?;
    if (eventEpoch != null &&
        eventEpoch.isNotEmpty &&
        eventEpoch != _state.syncEpoch) {
      _resetForEpoch(eventEpoch);
      _queueConversationRefresh(immediate: true);
      return;
    }
    if (eventName == 'conversation.delta') {
      if (_applyDeltaEvent(event)) return;
      _queueConversationRefresh(immediate: true);
      return;
    }
    final sequence = (event['sequence'] as num?)?.toInt();
    if (sequence == null || sequence > _state.sequence) {
      if (sequence != null && sequence > _pendingEventSequence) {
        _pendingEventSequence = sequence;
      }
      _queueConversationRefresh();
    }
  }

  void _resetForEpoch(String epoch) {
    _liveDeltas.clear();
    _liveDeltaBytes = 0;
    _deltaRenderTimer?.cancel();
    _deltaRenderTimer = null;
    _loadedOlder = false;
    _resetTimelineOnNextSnapshot = true;
    _state = ConversationState(
      queuedSubmissions: _state.queuedSubmissions,
      actions: _state.actions,
      revision: 0,
      sequence: 0,
      syncEpoch: epoch,
      online: _state.online,
    );
    _pendingEventSequence = 0;
    _notify();
  }

  bool _applyDeltaEvent(Map<String, dynamic> event) {
    if (!_state.hasSnapshot) return false;
    final rawPayload = event['payload'];
    if (rawPayload is! Map) return false;
    final payload = Map<String, dynamic>.from(rawPayload);
    final itemId = payload['itemId'] as String? ?? '';
    final kind = payload['kind'] as String? ?? '';
    final delta = payload['delta'] as String? ?? '';
    final from = (payload['fromSequence'] as num?)?.toInt();
    final to = (payload['toSequence'] as num?)?.toInt();
    if (itemId.isEmpty ||
        kind.isEmpty ||
        delta.isEmpty ||
        from == null ||
        to == null ||
        from != _state.sequence + 1 ||
        to < from) {
      return false;
    }
    final bytes = utf8.encode(delta).length;
    final current = _liveDeltas[itemId];
    if ((current?.bytes ?? 0) + bytes > 512 * 1024 ||
        _liveDeltaBytes + bytes > 4 * 1024 * 1024) {
      _liveDeltas.clear();
      _liveDeltaBytes = 0;
      return false;
    }
    final buffer =
        current ?? _LiveDeltaBuffer(kind: kind, baseItem: _itemById(itemId));
    if (buffer.kind != kind) return false;
    buffer.chunks.add(delta);
    buffer.bytes += bytes;
    _liveDeltaBytes += bytes;
    _liveDeltas[itemId] = buffer;
    _state = ConversationState(
      metadata: _state.metadata,
      items: _state.items,
      queuedSubmissions: _state.queuedSubmissions,
      actions: _state.actions,
      planConfirmation: _state.planConfirmation,
      olderCursor: _state.olderCursor,
      revision: _state.revision,
      hasSnapshot: _state.hasSnapshot,
      queueAvailable: _state.queueAvailable,
      sequence: to,
      syncEpoch: _state.syncEpoch,
      loading: _state.loading,
      loadingOlder: _state.loadingOlder,
      online: true,
      bodyError: _state.bodyError,
      actionError: _state.actionError,
      olderError: _state.olderError,
    );
    _deltaRenderTimer ??= Timer(
      const Duration(milliseconds: 75),
      _renderLiveDeltas,
    );
    return true;
  }

  Map<String, dynamic>? _itemById(String id) {
    for (final item in _state.items) {
      if (item['id'] == id) return Map<String, dynamic>.from(item);
    }
    return null;
  }

  void _renderLiveDeltas() {
    _deltaRenderTimer = null;
    if (_disposed || _liveDeltas.isEmpty) return;
    final replacements = <String, Map<String, dynamic>>{
      for (final entry in _liveDeltas.entries)
        entry.key: entry.value.render(entry.key),
    };
    final items = <Map<String, dynamic>>[];
    final seen = <String>{};
    for (final item in _state.items) {
      final id = item['id'] as String? ?? '';
      items.add(replacements[id] ?? item);
      seen.add(id);
    }
    for (final entry in replacements.entries) {
      if (!seen.contains(entry.key)) items.add(entry.value);
    }
    _state = ConversationState(
      metadata: _state.metadata,
      items: items,
      queuedSubmissions: _state.queuedSubmissions,
      actions: _state.actions,
      planConfirmation: _state.planConfirmation,
      olderCursor: _state.olderCursor,
      revision: _state.revision,
      hasSnapshot: _state.hasSnapshot,
      queueAvailable: _state.queueAvailable,
      sequence: _state.sequence,
      syncEpoch: _state.syncEpoch,
      loading: _state.loading,
      loadingOlder: _state.loadingOlder,
      online: _state.online,
      bodyError: _state.bodyError,
      actionError: _state.actionError,
      olderError: _state.olderError,
    );
    _notify();
  }

  void _setOnline(bool online) {
    if (_disposed || _state.online == online) return;
    _state = ConversationState(
      metadata: _state.metadata,
      items: _state.items,
      queuedSubmissions: _state.queuedSubmissions,
      actions: _state.actions,
      planConfirmation: _state.planConfirmation,
      olderCursor: _state.olderCursor,
      revision: _state.revision,
      hasSnapshot: _state.hasSnapshot,
      queueAvailable: _state.queueAvailable,
      sequence: _state.sequence,
      syncEpoch: _state.syncEpoch,
      loading: _state.loading,
      loadingOlder: _state.loadingOlder,
      online: online,
      bodyError: _state.bodyError,
      actionError: _state.actionError,
      olderError: _state.olderError,
    );
    _notify();
  }

  void _queueConversationRefresh({bool immediate = false}) {
    if (_disposed) return;
    _eventRefreshPending = true;
    _eventDebounce?.cancel();
    if (immediate) {
      unawaited(_drainConversationRefresh());
    } else {
      _eventDebounce = Timer(
        const Duration(milliseconds: 150),
        _drainConversationRefresh,
      );
    }
  }

  Future<void> _drainConversationRefresh() async {
    if (_eventRefreshRunning || _disposed) return;
    _eventRefreshRunning = true;
    try {
      // Events may arrive after the in-flight snapshot was assembled. Keep one
      // follow-up read so coalescing cannot lose those updates.
      final inFlight = _snapshotRefresh;
      if (inFlight != null) await inFlight;
      while (_eventRefreshPending && !_disposed) {
        _eventRefreshPending = false;
        await refreshConversation();
        if (_state.sequence >= _pendingEventSequence) {
          _pendingEventSequence = _state.sequence;
        }
      }
    } finally {
      _eventRefreshRunning = false;
    }
  }

  void _queueActionRefresh() {
    if (_disposed) return;
    _actionRefreshPending = true;
    if (!_actionRefreshRunning) unawaited(_drainActionRefresh());
  }

  Future<void> _drainActionRefresh() async {
    if (_actionRefreshRunning || _disposed) return;
    _actionRefreshRunning = true;
    try {
      while (_actionRefreshPending && !_disposed) {
        _actionRefreshPending = false;
        await refreshActions();
      }
    } finally {
      _actionRefreshRunning = false;
    }
  }

  Future<void> _syncLive() async {
    if (_syncing || _disposed || _state.loading) return;
    _syncing = true;
    try {
      final envelope = await gateway.callFor(
        daemonId,
        selectionGeneration,
        'conversation.subscribe',
        {'agentId': agentId, 'threadId': threadId, 'cursor': _state.sequence},
      );
      final result = envelope['result'] as Map<String, dynamic>? ?? const {};
      final syncEpoch = result['syncEpoch'] as String?;
      if (syncEpoch != null &&
          syncEpoch.isNotEmpty &&
          syncEpoch != _state.syncEpoch) {
        _resetForEpoch(syncEpoch);
      }
      final events = result['events'];
      final hasEvents = events is List && events.isNotEmpty;
      if (result['snapshot'] == true || hasEvents) {
        _queueConversationRefresh(immediate: true);
      }
      _setOnline(true);
    } catch (error) {
      // Live sync is best effort. A visible snapshot or paging error remains
      // local to that operation and the next poll/reconnect can recover it.
      if (error is CoreFault &&
          (error.code == 'response_too_large' ||
              error.code == 'payload_too_large')) {
        _queueConversationRefresh(immediate: true);
      }
      if (error is CoreFault && error.code == 'peer_offline') {
        _setOnline(false);
      }
    } finally {
      _syncing = false;
    }
  }

  Future<void> load() async {
    if (!isCurrent) return;
    _state = ConversationState(
      metadata: _state.metadata,
      items: _state.items,
      queuedSubmissions: _state.queuedSubmissions,
      actions: _state.actions,
      planConfirmation: _state.planConfirmation,
      olderCursor: _state.olderCursor,
      revision: _state.revision,
      hasSnapshot: _state.hasSnapshot,
      queueAvailable: _state.queueAvailable,
      sequence: _state.sequence,
      syncEpoch: _state.syncEpoch,
      loading: true,
      online: _state.online,
    );
    _notify();
    await Future.wait([refreshConversation(), refreshActions()]);
    if (!isCurrent) return;
    _state = ConversationState(
      metadata: _state.metadata,
      items: _state.items,
      queuedSubmissions: _state.queuedSubmissions,
      actions: _state.actions,
      planConfirmation: _state.planConfirmation,
      olderCursor: _state.olderCursor,
      revision: _state.revision,
      hasSnapshot: _state.hasSnapshot,
      queueAvailable: _state.queueAvailable,
      sequence: _state.sequence,
      syncEpoch: _state.syncEpoch,
      online: _state.online,
      bodyError: _state.bodyError,
      actionError: _state.actionError,
      olderError: _state.olderError,
    );
    _notify();
  }

  Future<bool> refreshConversation() {
    if (!isCurrent) return Future.value(false);
    // Background events and writes await the same authoritative read. Starting
    // another read must not invalidate a successful send prerequisite.
    return _snapshotRefresh ??= _readConversation().whenComplete(() {
      _snapshotRefresh = null;
    });
  }

  Future<bool> _readConversation() async {
    try {
      final envelope = await gateway.callFor(
        daemonId,
        selectionGeneration,
        'conversation.read',
        {'agentId': agentId, 'threadId': threadId, 'limit': 20},
      );
      if (!isCurrent) return false;
      final result = envelope['result'] as Map<String, dynamic>? ?? const {};
      final timeline = result['timeline'] as Map<String, dynamic>? ?? const {};
      final queue = result['queue'] as Map<String, dynamic>? ?? const {};
      final newestFirst = _maps(timeline['items']);
      final queuedSubmissions = _maps(queue['items'])
          .map(QueuedSubmission.fromJson)
          .toList(growable: false);
      final sequence = (result['sequence'] as num?)?.toInt() ?? 0;
      final incomingEpoch = result['syncEpoch'] as String?;
      final epochChanged =
          incomingEpoch != null &&
          incomingEpoch.isNotEmpty &&
          incomingEpoch != _state.syncEpoch &&
          _state.hasSnapshot;
      if (sequence < _state.sequence && !epochChanged) return false;
      if (epochChanged) {
        _loadedOlder = false;
        _resetTimelineOnNextSnapshot = true;
      }
      final settings = result['settings'];
      if (settings is Map<String, dynamic>) {
        _settings = ConversationSettings.fromJson(settings);
      }
      final latest = _dedupe(newestFirst.reversed);
      _state = ConversationState(
        metadata: result['conversation'] as Map<String, dynamic>?,
        items: _mergeLatest(
          _resetTimelineOnNextSnapshot ? const [] : _state.items,
          latest,
        ),
        queuedSubmissions: queuedSubmissions,
        actions: _state.actions,
        planConfirmation: result['planConfirmation'] as Map<String, dynamic>?,
        olderCursor: _loadedOlder
            ? _state.olderCursor
            : timeline['nextCursor'] as String?,
        revision: (result['revision'] as num?)?.toInt() ?? 0,
        hasSnapshot: true,
        queueAvailable:
            (result['capabilities'] as Map<String, dynamic>?)?['queue'] !=
            false,
        sequence: sequence,
        syncEpoch: incomingEpoch ?? _state.syncEpoch,
        loading: _state.loading,
        online: true,
        actionError: _state.actionError,
      );
      _liveDeltas.clear();
      _liveDeltaBytes = 0;
      _deltaRenderTimer?.cancel();
      _deltaRenderTimer = null;
      _resetTimelineOnNextSnapshot = false;
      _notify();
      return true;
    } catch (error) {
      if (!isCurrent) return false;
      _state = ConversationState(
        metadata: _state.metadata,
        items: _state.items,
        queuedSubmissions: _state.queuedSubmissions,
        actions: _state.actions,
        planConfirmation: _state.planConfirmation,
        olderCursor: _state.olderCursor,
        revision: _state.revision,
        hasSnapshot: _state.hasSnapshot,
        queueAvailable: _state.queueAvailable,
        sequence: _state.sequence,
        syncEpoch: _state.syncEpoch,
        loading: _state.loading,
        online: error is CoreFault && error.code == 'peer_offline'
            ? false
            : _state.online,
        bodyError: _fault(error, 'conversation.read'),
        actionError: _state.actionError,
      );
    }
    _notify();
    return false;
  }

  Future<void> requireFreshSnapshot() async {
    final synchronized = await refreshConversation();
    if (!isCurrent) {
      throw CoreFault('selection_changed', 'The selected conversation changed');
    }
    if (!synchronized) {
      throw _state.bodyError ??
          CoreFault(
            'snapshot_unavailable',
            'Could not synchronize the conversation',
          );
    }
    if (!isCurrent || !_state.hasSnapshot) {
      throw CoreFault('selection_changed', 'The selected conversation changed');
    }
  }

  Future<void> refreshActions() async {
    try {
      final envelope = await gateway.callFor(
        daemonId,
        selectionGeneration,
        'action.list',
        {'agentId': agentId},
      );
      if (!isCurrent) return;
      final actions = _maps(envelope['result'])
          .where(
            (item) =>
                item['conversationId'] == null ||
                item['conversationId'] == threadId,
          )
          .toList();
      _state = ConversationState(
        metadata: _state.metadata,
        items: _state.items,
        queuedSubmissions: _state.queuedSubmissions,
        actions: actions,
        planConfirmation: _state.planConfirmation,
        olderCursor: _state.olderCursor,
        revision: _state.revision,
        hasSnapshot: _state.hasSnapshot,
        queueAvailable: _state.queueAvailable,
        sequence: _state.sequence,
        syncEpoch: _state.syncEpoch,
        loading: _state.loading,
        online: true,
        bodyError: _state.bodyError,
        olderError: _state.olderError,
      );
    } catch (error) {
      if (!isCurrent) return;
      _state = ConversationState(
        metadata: _state.metadata,
        items: _state.items,
        queuedSubmissions: _state.queuedSubmissions,
        actions: _state.actions,
        planConfirmation: _state.planConfirmation,
        olderCursor: _state.olderCursor,
        revision: _state.revision,
        hasSnapshot: _state.hasSnapshot,
        queueAvailable: _state.queueAvailable,
        sequence: _state.sequence,
        syncEpoch: _state.syncEpoch,
        loading: _state.loading,
        online: error is CoreFault && error.code == 'peer_offline'
            ? false
            : _state.online,
        bodyError: _state.bodyError,
        actionError: _fault(error, 'action.list'),
        olderError: _state.olderError,
      );
    }
    _notify();
  }

  Future<void> loadOlder() async {
    final cursor = _state.olderCursor;
    if (_state.loadingOlder || cursor == null || cursor.isEmpty) return;
    _state = ConversationState(
      metadata: _state.metadata,
      items: _state.items,
      queuedSubmissions: _state.queuedSubmissions,
      actions: _state.actions,
      planConfirmation: _state.planConfirmation,
      olderCursor: cursor,
      revision: _state.revision,
      hasSnapshot: _state.hasSnapshot,
      queueAvailable: _state.queueAvailable,
      sequence: _state.sequence,
      syncEpoch: _state.syncEpoch,
      loadingOlder: true,
      online: _state.online,
      bodyError: _state.bodyError,
      actionError: _state.actionError,
    );
    _notify();
    try {
      final envelope = await gateway.callFor(
        daemonId,
        selectionGeneration,
        'conversation.timeline.list',
        {
          'agentId': agentId,
          'threadId': threadId,
          'cursor': cursor,
          'limit': 20,
        },
      );
      if (!isCurrent) return;
      final page = envelope['result'] as Map<String, dynamic>? ?? const {};
      final older = _maps(page['items']).reversed;
      _loadedOlder = true;
      _state = ConversationState(
        metadata: _state.metadata,
        items: _dedupe([...older, ..._state.items]),
        queuedSubmissions: _state.queuedSubmissions,
        actions: _state.actions,
        planConfirmation: _state.planConfirmation,
        olderCursor: page['nextCursor'] as String?,
        revision: _state.revision,
        hasSnapshot: _state.hasSnapshot,
        queueAvailable: _state.queueAvailable,
        sequence: _state.sequence,
        syncEpoch: _state.syncEpoch,
        online: true,
        bodyError: _state.bodyError,
        actionError: _state.actionError,
      );
    } catch (error) {
      if (!isCurrent) return;
      if (error is CoreFault && error.code == 'cursor_expired') {
        _loadedOlder = false;
        _resetTimelineOnNextSnapshot = true;
        await refreshConversation();
        return;
      }
      _state = ConversationState(
        metadata: _state.metadata,
        items: _state.items,
        queuedSubmissions: _state.queuedSubmissions,
        actions: _state.actions,
        planConfirmation: _state.planConfirmation,
        olderCursor: cursor,
        revision: _state.revision,
        hasSnapshot: _state.hasSnapshot,
        queueAvailable: _state.queueAvailable,
        sequence: _state.sequence,
        syncEpoch: _state.syncEpoch,
        online: error is CoreFault && error.code == 'peer_offline'
            ? false
            : _state.online,
        bodyError: _state.bodyError,
        actionError: _state.actionError,
        olderError: _fault(error, 'conversation.timeline.list'),
      );
    }
    _notify();
  }

  Future<String> readFullContent(Map<String, dynamic> block) async {
    try {
      return await _readFullContent(block);
    } on CoreFault catch (error) {
      if (error.code != 'content_ref_expired') rethrow;
      final turnId = block['_turnId'] as String?;
      final itemId = block['_itemId'] as String?;
      final blockIndex = block['_blockIndex'] as int?;
      if (turnId == null ||
          turnId.isEmpty ||
          itemId == null ||
          itemId.isEmpty ||
          blockIndex == null) {
        rethrow;
      }
      final envelope = await gateway.callFor(
        daemonId,
        selectionGeneration,
        'conversation.content.resolve',
        {
          'agentId': agentId,
          'threadId': threadId,
          'turnId': turnId,
          'itemId': itemId,
          'blockIndex': blockIndex,
        },
      );
      final result = envelope['result'] as Map<String, dynamic>? ?? const {};
      final refreshedReference = result['contentRef'] as String?;
      if (refreshedReference == null || refreshedReference.isEmpty) {
        throw CoreFault(
          'invalid_provider_response',
          'Content resolver returned no reference',
          method: 'conversation.content.resolve',
        );
      }
      return _readFullContent({...block, 'contentRef': refreshedReference});
    }
  }

  Future<String> _readFullContent(Map<String, dynamic> block) async {
    final reference = block['contentRef'] as String?;
    if (reference == null || reference.isEmpty) {
      return block['text'] as String? ?? '';
    }
    final buffer = StringBuffer();
    var offset = 0;
    var hasMore = true;
    while (hasMore) {
      final envelope = await gateway.callFor(
        daemonId,
        selectionGeneration,
        'conversation.content.read',
        {
          'agentId': agentId,
          'threadId': threadId,
          'contentRef': reference,
          'offset': offset,
        },
      );
      final result = envelope['result'] as Map<String, dynamic>? ?? const {};
      buffer.write(result['text'] as String? ?? '');
      final next = result['nextOffset'] as int? ?? offset;
      hasMore = result['hasMore'] as bool? ?? false;
      if (hasMore && next <= offset) {
        throw CoreFault(
          'invalid_provider_response',
          'Content cursor did not advance',
          method: 'conversation.content.read',
        );
      }
      offset = next;
    }
    return buffer.toString();
  }

  static List<Map<String, dynamic>> _maps(dynamic value) => value is List
      ? value.whereType<Map<String, dynamic>>().toList()
      : const [];

  static List<Map<String, dynamic>> _dedupe(
    Iterable<Map<String, dynamic>> values,
  ) {
    final seen = <String>{};
    return [
      for (final item in values)
        if (seen.add(item['id'] as String? ?? '')) item,
    ];
  }

  static List<Map<String, dynamic>> _mergeLatest(
    List<Map<String, dynamic>> existing,
    List<Map<String, dynamic>> latest,
  ) {
    final latestById = <String, Map<String, dynamic>>{
      for (final item in latest)
        if ((item['id'] as String? ?? '').isNotEmpty)
          item['id'] as String: item,
    };
    final merged = <Map<String, dynamic>>[];
    final seen = <String>{};
    for (final item in existing) {
      final id = item['id'] as String? ?? '';
      if (id.isEmpty || !seen.add(id)) continue;
      merged.add(latestById.remove(id) ?? item);
    }
    for (final item in latest) {
      final id = item['id'] as String? ?? '';
      if (id.isEmpty || !seen.add(id)) continue;
      merged.add(item);
    }
    return merged;
  }

  static CoreFault _fault(Object error, String method) => error is CoreFault
      ? error
      : CoreFault('unexpected_error', error.toString(), method: method);

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _liveTimer?.cancel();
    _eventDebounce?.cancel();
    _deltaRenderTimer?.cancel();
    _eventSubscription?.cancel();
    super.dispose();
  }
}

class _LiveDeltaBuffer {
  _LiveDeltaBuffer({required this.kind, this.baseItem});

  final String kind;
  final Map<String, dynamic>? baseItem;
  final List<String> chunks = [];
  int bytes = 0;

  Map<String, dynamic> render(String itemId) {
    final item = baseItem == null
        ? <String, dynamic>{
            'id': itemId,
            'kind': _itemKind,
            'status': 'streaming',
            'blocks': <Map<String, dynamic>>[],
          }
        : Map<String, dynamic>.from(baseItem!);
    item['status'] = 'streaming';
    final blocks = <Map<String, dynamic>>[
      for (final value in (item['blocks'] as List? ?? const []))
        if (value is Map) Map<String, dynamic>.from(value),
    ];
    final blockKind = _blockKind;
    var index = blocks.lastIndexWhere((block) => block['kind'] == blockKind);
    if (index < 0) {
      blocks.add({'kind': blockKind, 'text': ''});
      index = blocks.length - 1;
    }
    final base = blocks[index]['text'] as String? ?? '';
    blocks[index] = {...blocks[index], 'text': base + chunks.join()};
    item['blocks'] = blocks;
    return item;
  }

  String get _itemKind => switch (kind) {
    'assistantText' => 'agentMessage',
    'plan' => 'plan',
    'reasoning' || 'reasoningSummary' => 'reasoning',
    'commandOutput' => 'commandExecution',
    'fileChangeOutput' => 'fileChange',
    _ => kind,
  };

  String get _blockKind => switch (kind) {
    'plan' => 'plan',
    'reasoningSummary' => 'summary',
    'commandOutput' || 'fileChangeOutput' => 'output',
    _ => 'text',
  };
}
