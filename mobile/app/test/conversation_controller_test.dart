import 'package:flutter_test/flutter_test.dart';
import 'package:work_anywhere/core/core_gateway.dart';
import 'package:work_anywhere/features/conversation/conversation_controller.dart';

class _FakeGateway extends CoreGateway {
  _FakeGateway(this.handler);
  final Future<Map<String, dynamic>> Function(
    String method,
    Map<String, dynamic> params,
  )
  handler;

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
}
