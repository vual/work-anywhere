import 'dart:async';

import 'package:flutter/foundation.dart';

import '../../core/core_gateway.dart';
import '../../core/models.dart';

class ConversationState {
  const ConversationState({
    this.metadata,
    this.items = const [],
    this.actions = const [],
    this.planConfirmation,
    this.olderCursor,
    this.revision = 0,
    this.sequence = 0,
    this.loading = false,
    this.loadingOlder = false,
    this.online = true,
    this.bodyError,
    this.actionError,
    this.olderError,
  });

  final Map<String, dynamic>? metadata;
  final List<Map<String, dynamic>> items;
  final List<Map<String, dynamic>> actions;
  final Map<String, dynamic>? planConfirmation;
  final String? olderCursor;
  final int revision;
  final int sequence;
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
  });

  final CoreGateway gateway;
  final String agentId;
  final String threadId;
  ConversationState _state = const ConversationState();
  ConversationSettings? _settings;
  bool _disposed = false;
  bool _syncing = false;
  bool _loadedOlder = false;
  bool _eventRefreshRunning = false;
  bool _eventRefreshPending = false;
  bool _actionRefreshRunning = false;
  bool _actionRefreshPending = false;
  int _pendingEventSequence = 0;
  Timer? _liveTimer;
  Timer? _eventDebounce;
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
    final sequence = (event['sequence'] as num?)?.toInt();
    if (sequence == null || sequence > _state.sequence) {
      if (sequence != null && sequence > _pendingEventSequence) {
        _pendingEventSequence = sequence;
      }
      _queueConversationRefresh();
    }
  }

  void _setOnline(bool online) {
    if (_disposed || _state.online == online) return;
    _state = ConversationState(
      metadata: _state.metadata,
      items: _state.items,
      actions: _state.actions,
      planConfirmation: _state.planConfirmation,
      olderCursor: _state.olderCursor,
      revision: _state.revision,
      sequence: _state.sequence,
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
      final envelope = await gateway.call('conversation.subscribe', {
        'agentId': agentId,
        'threadId': threadId,
        'cursor': _state.sequence,
      });
      final result = envelope['result'] as Map<String, dynamic>? ?? const {};
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
    _state = ConversationState(
      metadata: _state.metadata,
      items: _state.items,
      actions: _state.actions,
      planConfirmation: _state.planConfirmation,
      olderCursor: _state.olderCursor,
      revision: _state.revision,
      sequence: _state.sequence,
      loading: true,
      online: _state.online,
    );
    _notify();
    await Future.wait([refreshConversation(), refreshActions()]);
    _state = ConversationState(
      metadata: _state.metadata,
      items: _state.items,
      actions: _state.actions,
      planConfirmation: _state.planConfirmation,
      olderCursor: _state.olderCursor,
      revision: _state.revision,
      sequence: _state.sequence,
      online: _state.online,
      bodyError: _state.bodyError,
      actionError: _state.actionError,
      olderError: _state.olderError,
    );
    _notify();
  }

  Future<void> refreshConversation() async {
    try {
      final envelope = await gateway.call('conversation.read', {
        'agentId': agentId,
        'threadId': threadId,
        'limit': 20,
      });
      final result = envelope['result'] as Map<String, dynamic>? ?? const {};
      final settings = result['settings'];
      if (settings is Map<String, dynamic>) {
        _settings = ConversationSettings.fromJson(settings);
      }
      final timeline = result['timeline'] as Map<String, dynamic>? ?? const {};
      final newestFirst = _maps(timeline['items']);
      final sequence = (result['sequence'] as num?)?.toInt() ?? 0;
      if (sequence < _state.sequence) return;
      final latest = _dedupe(newestFirst.reversed);
      _state = ConversationState(
        metadata: result['conversation'] as Map<String, dynamic>?,
        items: _mergeLatest(_state.items, latest),
        actions: _state.actions,
        planConfirmation: result['planConfirmation'] as Map<String, dynamic>?,
        olderCursor: _loadedOlder
            ? _state.olderCursor
            : timeline['nextCursor'] as String?,
        revision: (result['revision'] as num?)?.toInt() ?? 0,
        sequence: sequence,
        loading: _state.loading,
        online: true,
        actionError: _state.actionError,
      );
    } catch (error) {
      _state = ConversationState(
        metadata: _state.metadata,
        items: _state.items,
        actions: _state.actions,
        planConfirmation: _state.planConfirmation,
        olderCursor: _state.olderCursor,
        revision: _state.revision,
        sequence: _state.sequence,
        loading: _state.loading,
        online: error is CoreFault && error.code == 'peer_offline'
            ? false
            : _state.online,
        bodyError: _fault(error, 'conversation.read'),
        actionError: _state.actionError,
      );
    }
    _notify();
  }

  Future<void> refreshActions() async {
    try {
      final envelope = await gateway.call('action.list', {'agentId': agentId});
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
        actions: actions,
        planConfirmation: _state.planConfirmation,
        olderCursor: _state.olderCursor,
        revision: _state.revision,
        sequence: _state.sequence,
        loading: _state.loading,
        online: true,
        bodyError: _state.bodyError,
        olderError: _state.olderError,
      );
    } catch (error) {
      _state = ConversationState(
        metadata: _state.metadata,
        items: _state.items,
        actions: _state.actions,
        planConfirmation: _state.planConfirmation,
        olderCursor: _state.olderCursor,
        revision: _state.revision,
        sequence: _state.sequence,
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
      actions: _state.actions,
      planConfirmation: _state.planConfirmation,
      olderCursor: cursor,
      revision: _state.revision,
      sequence: _state.sequence,
      loadingOlder: true,
      online: _state.online,
      bodyError: _state.bodyError,
      actionError: _state.actionError,
    );
    _notify();
    try {
      final envelope = await gateway.call('conversation.timeline.list', {
        'agentId': agentId,
        'threadId': threadId,
        'cursor': cursor,
        'limit': 20,
      });
      final page = envelope['result'] as Map<String, dynamic>? ?? const {};
      final older = _maps(page['items']).reversed;
      _loadedOlder = true;
      _state = ConversationState(
        metadata: _state.metadata,
        items: _dedupe([...older, ..._state.items]),
        actions: _state.actions,
        planConfirmation: _state.planConfirmation,
        olderCursor: page['nextCursor'] as String?,
        revision: _state.revision,
        sequence: _state.sequence,
        online: true,
        bodyError: _state.bodyError,
        actionError: _state.actionError,
      );
    } catch (error) {
      _state = ConversationState(
        metadata: _state.metadata,
        items: _state.items,
        actions: _state.actions,
        planConfirmation: _state.planConfirmation,
        olderCursor: cursor,
        revision: _state.revision,
        sequence: _state.sequence,
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
      final envelope = await gateway.call('conversation.content.resolve', {
        'agentId': agentId,
        'threadId': threadId,
        'turnId': turnId,
        'itemId': itemId,
        'blockIndex': blockIndex,
      });
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
      final envelope = await gateway.call('conversation.content.read', {
        'agentId': agentId,
        'threadId': threadId,
        'contentRef': reference,
        'offset': offset,
      });
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
    _eventSubscription?.cancel();
    super.dispose();
  }
}
