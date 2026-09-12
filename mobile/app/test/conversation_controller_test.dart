import 'package:flutter_test/flutter_test.dart';

import 'dart:async';

import 'package:work_anywhere/core/core_gateway.dart';
import 'package:work_anywhere/features/conversation/conversation_controller.dart';

class _FakeGateway extends CoreGateway {
  _FakeGateway(this.handler, {this._events});
  final Future<Map<String, dynamic>> Function(
    String method,
    Map<String, dynamic> params,
  )
  handler;
  final Stream<Map<String, dynamic>>? _events;

  @override
  Stream<Map<String, dynamic>> get events => _events ?? const Stream.empty();

  @override
  Future<Map<String, dynamic>> call(
    String method,
    Map<String, dynamic> params, {
    int timeoutMilliseconds = 30000,
  }) => handler(method, params);
}

Map<String, dynamic> _item(String id) => {
  'id': id,
  'kind': 'agentMessage',
  'blocks': [
    {'kind': 'text', 'text': id, 'truncated': false},
  ],
};

void main() {
  test('active event refreshes the conversation without polling', () async {
    final events = StreamController<Map<String, dynamic>>();
    addTearDown(events.close);
    var reads = 0;
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list') return {'result': <dynamic>[]};
      reads++;
      return {
        'result': {
          'conversation': {'id': 't1'},
          'sequence': reads,
          'timeline': {'items': const []},
        },
      };
    }, events: events.stream);
    final controller = ConversationController(
      gateway: gateway,
      agentId: 'a1',
      threadId: 't1',
    );
    await controller.load();
    controller.startLiveSync();
    events.add({
      'type': 'event',
      'conversationId': 't1',
      'sequence': 2,
      'event': 'turn.completed',
    });
    await Future<void>.delayed(const Duration(milliseconds: 200));
    expect(reads, 2);
    controller.dispose();
  });

  test('event bursts are debounced into one conversation refresh', () async {
    final events = StreamController<Map<String, dynamic>>();
    addTearDown(events.close);
    var reads = 0;
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list') return {'result': <dynamic>[]};
      reads++;
      return {
        'result': {
          'conversation': {'id': 't1'},
          'sequence': reads == 1 ? 1 : 4,
          'timeline': {'items': const []},
        },
      };
    }, events: events.stream);
    final controller = ConversationController(
      gateway: gateway,
      agentId: 'a1',
      threadId: 't1',
    );
    await controller.load();
    controller.startLiveSync();
    for (var sequence = 2; sequence <= 4; sequence++) {
      events.add({
        'type': 'event',
        'conversationId': 't1',
        'sequence': sequence,
        'event': 'item.updated',
      });
    }
    await Future<void>.delayed(const Duration(milliseconds: 220));

    expect(reads, 2);
    expect(controller.state.sequence, 4);
    controller.dispose();
  });

  test(
    'disconnected transport starts an immediate subscribe fallback',
    () async {
      final events = StreamController<Map<String, dynamic>>();
      addTearDown(events.close);
      var subscriptions = 0;
      final gateway = _FakeGateway((method, params) async {
        expect(method, 'conversation.subscribe');
        subscriptions++;
        return {'result': const {}};
      }, events: events.stream);
      final controller = ConversationController(
        gateway: gateway,
        agentId: 'a1',
        threadId: 't1',
      );
      controller.startLiveSync();
      events.add({'type': 'transport', 'state': 'disconnected'});
      await Future<void>.delayed(Duration.zero);

      expect(subscriptions, 1);
      controller.dispose();
    },
  );

  test('conversation remains visible when action.list fails', () async {
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list') {
        throw CoreFault('peer_offline', 'actions unavailable', method: method);
      }
      return {
        'result': {
          'conversation': {'id': 't1', 'title': 'Thread'},
          'revision': 3,
          'sequence': 4,
          'timeline': {
            'items': [_item('new'), _item('old')],
            'nextCursor': 'older',
          },
        },
      };
    });
    final controller = ConversationController(
      gateway: gateway,
      agentId: 'a1',
      threadId: 't1',
    );

    await controller.load();

    expect(controller.state.items.map((item) => item['id']), ['old', 'new']);
    expect(controller.state.bodyError, isNull);
    expect(controller.state.actionError?.code, 'peer_offline');
  });

  test('older pages are prepended in order and deduplicated', () async {
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list') return {'result': <dynamic>[]};
      if (method == 'conversation.timeline.list') {
        return {
          'result': {
            'items': [_item('old'), _item('oldest')],
          },
        };
      }
      return {
        'result': {
          'conversation': {'id': 't1'},
          'timeline': {
            'items': [_item('new'), _item('old')],
            'nextCursor': 'cursor-1',
          },
        },
      };
    });
    final controller = ConversationController(
      gateway: gateway,
      agentId: 'a1',
      threadId: 't1',
    );
    await controller.load();
    await controller.loadOlder();

    expect(controller.state.items.map((item) => item['id']), [
      'oldest',
      'old',
      'new',
    ]);
  });

  test('full content is assembled from byte-offset chunks', () async {
    final gateway = _FakeGateway((method, params) async {
      final offset = params['offset'] as int;
      return {
        'result': offset == 0
            ? {'text': 'hello ', 'nextOffset': 6, 'hasMore': true}
            : {'text': 'world', 'nextOffset': 11, 'hasMore': false},
      };
    });
    final controller = ConversationController(
      gateway: gateway,
      agentId: 'a1',
      threadId: 't1',
    );

    expect(
      await controller.readFullContent({'contentRef': 'ref-1'}),
      'hello world',
    );
  });

  test(
    'expired content reference is resolved from the stable item locator',
    () async {
      var reads = 0;
      final gateway = _FakeGateway((method, params) async {
        if (method == 'conversation.content.resolve') {
          expect(params['turnId'], 'turn-1');
          expect(params['itemId'], 'plan-1');
          expect(params['blockIndex'], 0);
          return {
            'result': {'contentRef': 'fresh-ref', 'totalBytes': 4},
          };
        }
        if (method == 'conversation.content.read') {
          reads++;
          if (params['contentRef'] == 'expired-ref') {
            throw CoreFault('content_ref_expired', 'expired', method: method);
          }
          expect(params['contentRef'], 'fresh-ref');
          return {
            'result': {'text': 'plan', 'nextOffset': 4, 'hasMore': false},
          };
        }
        throw StateError('unexpected method $method');
      });
      final controller = ConversationController(
        gateway: gateway,
        agentId: 'a1',
        threadId: 't1',
      );

      expect(
        await controller.readFullContent({
          'contentRef': 'expired-ref',
          '_turnId': 'turn-1',
          '_itemId': 'plan-1',
          '_blockIndex': 0,
        }),
        'plan',
      );
      expect(reads, 2);
    },
  );

  test(
    'a stale conversation response cannot overwrite a newer snapshot',
    () async {
      var call = 0;
      final first = Completer<Map<String, dynamic>>();
      final second = Completer<Map<String, dynamic>>();
      final gateway = _FakeGateway((method, params) {
        call++;
        return call == 1 ? first.future : second.future;
      });
      final controller = ConversationController(
        gateway: gateway,
        agentId: 'a1',
        threadId: 't1',
      );
      final olderRequest = controller.refreshConversation();
      final newerRequest = controller.refreshConversation();
      second.complete({
        'result': {
          'conversation': {'id': 't1'},
          'sequence': 2,
          'timeline': {
            'items': [_item('new')],
          },
        },
      });
      await newerRequest;
      first.complete({
        'result': {
          'conversation': {'id': 't1'},
          'sequence': 1,
          'timeline': {
            'items': [_item('stale')],
          },
        },
      });
      await olderRequest;
      expect(controller.state.sequence, 2);
      expect(controller.state.items.map((item) => item['id']), ['new']);
    },
  );
}
