import 'dart:async';

import 'package:flutter/foundation.dart';

import '../../core/core_gateway.dart';

class ConversationState {
  const ConversationState({
    this.metadata,
    this.items = const [],
    this.actions = const [],
    this.olderCursor,
    this.revision = 0,
    this.sequence = 0,
    this.loading = false,
    this.loadingOlder = false,
    this.bodyError,
    this.actionError,
    this.olderError,
  });

  final Map<String, dynamic>? metadata;
  final List<Map<String, dynamic>> items;
  final List<Map<String, dynamic>> actions;
  final String? olderCursor;
  final int revision;
  final int sequence;
  final bool loading;
  final bool loadingOlder;
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
  bool _disposed = false;
  bool _syncing = false;
  Timer? _liveTimer;

  ConversationState get state => _state;

  void startLiveSync() {
    _liveTimer?.cancel();
    _liveTimer = Timer.periodic(const Duration(seconds: 2), (_) => _syncLive());
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
        await refreshConversation();
      }
    } catch (error) {
      // Live sync is best effort. A visible snapshot or paging error remains
      // local to that operation and the next poll/reconnect can recover it.
      if (error is CoreFault &&
          (error.code == 'response_too_large' ||
              error.code == 'payload_too_large')) {
        await refreshConversation();
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
      olderCursor: _state.olderCursor,
      revision: _state.revision,
      sequence: _state.sequence,
      loading: true,
    );
    _notify();
    await Future.wait([refreshConversation(), refreshActions()]);
    _state = ConversationState(
      metadata: _state.metadata,
      items: _state.items,
      actions: _state.actions,
      olderCursor: _state.olderCursor,
      revision: _state.revision,
      sequence: _state.sequence,
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
        'limit': 30,
      });
      final result = envelope['result'] as Map<String, dynamic>? ?? const {};
      final timeline = result['timeline'] as Map<String, dynamic>? ?? const {};
      final newestFirst = _maps(timeline['items']);
      _state = ConversationState(
        metadata: result['conversation'] as Map<String, dynamic>?,
        items: _dedupe(newestFirst.reversed),
        actions: _state.actions,
        olderCursor: timeline['nextCursor'] as String?,
        revision: result['revision'] as int? ?? 0,
        sequence: result['sequence'] as int? ?? 0,
        loading: _state.loading,
        actionError: _state.actionError,
      );
    } catch (error) {
      _state = ConversationState(
        metadata: _state.metadata,
        items: _state.items,
        actions: _state.actions,
        olderCursor: _state.olderCursor,
        revision: _state.revision,
        sequence: _state.sequence,
        loading: _state.loading,
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
        olderCursor: _state.olderCursor,
        revision: _state.revision,
        sequence: _state.sequence,
        loading: _state.loading,
        bodyError: _state.bodyError,
        olderError: _state.olderError,
      );
    } catch (error) {
      _state = ConversationState(
        metadata: _state.metadata,
        items: _state.items,
        actions: _state.actions,
        olderCursor: _state.olderCursor,
        revision: _state.revision,
        sequence: _state.sequence,
        loading: _state.loading,
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
      olderCursor: cursor,
      revision: _state.revision,
      sequence: _state.sequence,
      loadingOlder: true,
      bodyError: _state.bodyError,
      actionError: _state.actionError,
    );
    _notify();
    try {
      final envelope = await gateway.call('conversation.timeline.list', {
        'agentId': agentId,
        'threadId': threadId,
        'cursor': cursor,
        'limit': 30,
      });
      final page = envelope['result'] as Map<String, dynamic>? ?? const {};
      final older = _maps(page['items']).reversed;
      _state = ConversationState(
        metadata: _state.metadata,
        items: _dedupe([...older, ..._state.items]),
        actions: _state.actions,
        olderCursor: page['nextCursor'] as String?,
        revision: _state.revision,
        sequence: _state.sequence,
        bodyError: _state.bodyError,
        actionError: _state.actionError,
      );
    } catch (error) {
      _state = ConversationState(
        metadata: _state.metadata,
        items: _state.items,
        actions: _state.actions,
        olderCursor: cursor,
        revision: _state.revision,
        sequence: _state.sequence,
        bodyError: _state.bodyError,
        actionError: _state.actionError,
        olderError: _fault(error, 'conversation.timeline.list'),
      );
    }
    _notify();
  }

  Future<String> readFullContent(Map<String, dynamic> block) async {
    final reference = block['contentRef'] as String?;
    if (reference == null || reference.isEmpty) {
      return block['text'] as String? ?? '';
    }
    final buffer = StringBuffer();
    var offset = 0;
    var hasMore = true;
    try {
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
    } on CoreFault catch (error) {
      if (error.code == 'content_ref_expired') {
        await refreshConversation();
      }
      rethrow;
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
    super.dispose();
  }
}
