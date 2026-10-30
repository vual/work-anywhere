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
  for (final unknown in [
    null,
    {'mode': 'default', 'modelId': '', 'permissionModeId': 'custom'},
  ]) {
    test('unknown settings clear previous authority: $unknown', () async {
      var reads = 0;
      final controller = ConversationController(
        gateway: _FakeGateway(
          (method, params) async => {
            'result': {
              'conversation': {'id': 't'},
              'timeline': {'items': []},
              'sequence': ++reads,
              'settings': reads == 1
                  ? {'modelId': 'm', 'permissionModeId': 'readOnly'}
                  : unknown,
            },
          },
        ),
        agentId: 'a',
        threadId: 't',
      );
      addTearDown(controller.dispose);
      await controller.requireFreshSnapshot();
      expect(controller.settings?.modelId, 'm');
      await controller.requireFreshSnapshot();
      expect(controller.settings, isNull);
    });
  }
  test(
    'expired history cursor reloads the latest page without stale items',
    () async {
      var reads = 0;
      final controller = ConversationController(
        gateway: _FakeGateway((method, params) async {
          if (method == 'conversation.timeline.list') {
            throw CoreFault('cursor_expired', 'anchor deleted');
          }
          reads++;
          return {
            'result': {
              'conversation': {'id': 't'},
              'timeline': {
                'items': [_item(reads == 1 ? 'deleted' : 'current')],
                'nextCursor': reads == 1 ? 'old-cursor' : null,
              },
              'sequence': reads,
              'revision': reads,
            },
          };
        }),
        agentId: 'a',
        threadId: 't',
      );
      await controller.requireFreshSnapshot();
      await controller.loadOlder();
      expect(controller.state.items.map((item) => item['id']), ['current']);
      expect(controller.state.hasOlder, false);
      expect(controller.state.loadingOlder, false);
      controller.dispose();
    },
  );

  test('write synchronization requires a successful snapshot and tracks queue capability', () async {
    var fail = true;
    final controller = ConversationController(
      gateway: _FakeGateway((method, params) async {
        if (fail) throw CoreFault('provider_error', 'read failed');
        return {
          'result': {
            'conversation': {'id': 't1'},
            'timeline': {'items': []},
            'revision': 13,
            'sequence': 20,
            'capabilities': {'queue': false},
          },
        };
      }),
      agentId: 'a',
      threadId: 't1',
    );
    expect(controller.state.hasSnapshot, false);
    await expectLater(
      controller.requireFreshSnapshot(),
      throwsA(isA<CoreFault>()),
    );
    expect(controller.state.hasSnapshot, false);
    fail = false;
    await controller.requireFreshSnapshot();
    expect(controller.state.hasSnapshot, true);
    expect(controller.state.revision, 13);
    expect(controller.state.queueAvailable, false);
    controller.dispose();
  });

  test('snapshot finishing after selection changes is discarded', () async {
    final pending = Completer<Map<String, dynamic>>();
    var selected = 'daemon-1';
    final controller = ConversationController(
      gateway: _FakeGateway((method, params) => pending.future),
      agentId: 'a',
      threadId: 't1',
      daemonId: 'daemon-1',
      daemonIdProvider: () => selected,
    );
    final refresh = controller.refreshConversation();
    selected = 'daemon-2';
    pending.complete({
      'result': {
        'conversation': {'id': 't1'},
        'revision': 99,
        'sequence': 99,
      },
    });
    expect(await refresh, false);
    expect(controller.state.hasSnapshot, false);
    controller.dispose();
  });

  test(
    'conversation snapshot restores the authoritative queued messages',
    () async {
      final gateway = _FakeGateway((method, params) async {
        if (method == 'action.list') return {'result': <dynamic>[]};
        return {
          'result': {
            'conversation': {'id': 't1', 'running': true},
            'timeline': {'items': const []},
            'queue': {
              'items': [
                {
                  'id': 'q1',
                  'clientMessageId': 'client-1',
                  'text': 'Queued instruction',
                  'attachmentCount': 2,
                },
              ],
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

      expect(controller.state.queuedSubmissions, hasLength(1));
      expect(controller.state.queuedSubmissions.single.id, 'q1');
      expect(
        controller.state.queuedSubmissions.single.text,
        'Queued instruction',
      );
      expect(controller.state.queuedSubmissions.single.attachmentCount, 2);
      controller.dispose();
    },
  );

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

  test('events from an old computer generation are ignored', () async {
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
      daemonId: 'daemon-new',
      selectionGeneration: 2,
    );
    await controller.load();
    controller.startLiveSync();

    events.add({
      'type': 'event',
      'daemonId': 'daemon-old',
      'generation': 1,
      'conversationId': 't1',
      'sequence': 2,
      'event': 'turn.completed',
    });
    await Future<void>.delayed(const Duration(milliseconds: 200));
    expect(reads, 1);

    events.add({
      'type': 'event',
      'daemonId': 'daemon-new',
      'generation': 2,
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
    'batched delta updates a streaming item without reading a snapshot',
    () async {
      final events = StreamController<Map<String, dynamic>>();
      addTearDown(events.close);
      var reads = 0;
      final controller = ConversationController(
        gateway: _FakeGateway((method, params) async {
          reads++;
          return {
            'result': {
              'conversation': {'id': 't1'},
              'syncEpoch': 'epoch-1',
              'sequence': 1,
              'timeline': {'items': const []},
            },
          };
        }, events: events.stream),
        agentId: 'a1',
        threadId: 't1',
      );
      addTearDown(controller.dispose);
      await controller.refreshConversation();
      controller.startLiveSync();

      events.add({
        'type': 'event',
        'event': 'conversation.delta',
        'conversationId': 't1',
        'sequence': 2,
        'syncEpoch': 'epoch-1',
        'payload': {
          'version': 1,
          'kind': 'assistantText',
          'turnId': 'turn-1',
          'itemId': 'item-1',
          'fromSequence': 2,
          'toSequence': 2,
          'delta': 'Hello from a batch',
        },
      });
      await Future<void>.delayed(const Duration(milliseconds: 100));

      expect(reads, 1);
      expect(controller.state.sequence, 2);
      expect(controller.state.items, hasLength(1));
      expect(
        (controller.state.items.single['blocks'] as List).single['text'],
        'Hello from a batch',
      );
    },
  );

  test('delta sequence gap falls back to an authoritative snapshot', () async {
    final events = StreamController<Map<String, dynamic>>();
    addTearDown(events.close);
    var reads = 0;
    final controller = ConversationController(
      gateway: _FakeGateway((method, params) async {
        reads++;
        return {
          'result': {
            'conversation': {'id': 't1'},
            'syncEpoch': 'epoch-1',
            'sequence': reads == 1 ? 1 : 4,
            'timeline': {
              'items': reads == 1 ? const [] : [_item('official')],
            },
          },
        };
      }, events: events.stream),
      agentId: 'a1',
      threadId: 't1',
    );
    addTearDown(controller.dispose);
    await controller.refreshConversation();
    controller.startLiveSync();
    events.add({
      'type': 'event',
      'event': 'conversation.delta',
      'conversationId': 't1',
      'sequence': 4,
      'syncEpoch': 'epoch-1',
      'payload': {
        'kind': 'assistantText',
        'turnId': 'turn-1',
        'itemId': 'item-1',
        'fromSequence': 3,
        'toSequence': 4,
        'delta': 'gap',
      },
    });
    await Future<void>.delayed(const Duration(milliseconds: 100));

    expect(reads, 2);
    expect(controller.state.items.single['id'], 'official');
  });

  test('oversized live item falls back to an authoritative snapshot', () async {
    final events = StreamController<Map<String, dynamic>>();
    addTearDown(events.close);
    var reads = 0;
    final controller = ConversationController(
      gateway: _FakeGateway((method, params) async {
        reads++;
        return {
          'result': {
            'conversation': {'id': 't1'},
            'syncEpoch': 'epoch-1',
            'sequence': reads == 1 ? 1 : 2,
            'timeline': {
              'items': reads == 1 ? const [] : [_item('official')],
            },
          },
        };
      }, events: events.stream),
      agentId: 'a1',
      threadId: 't1',
    );
    addTearDown(controller.dispose);
    await controller.refreshConversation();
    controller.startLiveSync();
    events.add({
      'type': 'event',
      'event': 'conversation.delta',
      'conversationId': 't1',
      'sequence': 2,
      'syncEpoch': 'epoch-1',
      'payload': {
        'kind': 'assistantText',
        'turnId': 'turn-1',
        'itemId': 'item-1',
        'fromSequence': 2,
        'toSequence': 2,
        'delta': List<String>.filled(512 * 1024 + 1, 'x').join(),
      },
    });
    await Future<void>.delayed(const Duration(milliseconds: 150));

    expect(reads, 2);
    expect(controller.state.items.single['id'], 'official');
  });

  test('live conversation byte budget falls back to a snapshot', () async {
    final events = StreamController<Map<String, dynamic>>();
    addTearDown(events.close);
    var reads = 0;
    final controller = ConversationController(
      gateway: _FakeGateway((method, params) async {
        reads++;
        return {
          'result': {
            'conversation': {'id': 't1'},
            'syncEpoch': 'epoch-1',
            'sequence': reads == 1 ? 1 : 10,
            'timeline': {
              'items': reads == 1 ? const [] : [_item('official')],
            },
          },
        };
      }, events: events.stream),
      agentId: 'a1',
      threadId: 't1',
    );
    addTearDown(controller.dispose);
    await controller.refreshConversation();
    controller.startLiveSync();
    final chunk = List<String>.filled(480 * 1024, 'x').join();
    for (var index = 0; index < 9; index++) {
      events.add({
        'type': 'event',
        'event': 'conversation.delta',
        'conversationId': 't1',
        'sequence': index + 2,
        'syncEpoch': 'epoch-1',
        'payload': {
          'kind': 'assistantText',
          'turnId': 'turn-1',
          'itemId': 'item-$index',
          'fromSequence': index + 2,
          'toSequence': index + 2,
          'delta': chunk,
        },
      });
    }
    await Future<void>.delayed(const Duration(milliseconds: 250));

    expect(reads, 2);
    expect(controller.state.items.single['id'], 'official');
  });

  test(
    'new sync epoch accepts a lower sequence and replaces old projection',
    () async {
      final events = StreamController<Map<String, dynamic>>();
      addTearDown(events.close);
      var reads = 0;
      final controller = ConversationController(
        gateway: _FakeGateway((method, params) async {
          reads++;
          return {
            'result': {
              'conversation': {'id': 't1'},
              'syncEpoch': reads == 1 ? 'epoch-1' : 'epoch-2',
              'sequence': reads == 1 ? 20 : 1,
              'timeline': {
                'items': [_item(reads == 1 ? 'old' : 'new')],
              },
            },
          };
        }, events: events.stream),
        agentId: 'a1',
        threadId: 't1',
      );
      addTearDown(controller.dispose);
      await controller.refreshConversation();
      controller.startLiveSync();
      events.add({
        'type': 'event',
        'event': 'conversation.invalidated',
        'conversationId': 't1',
        'syncEpoch': 'epoch-2',
        'sequence': 1,
      });
      await Future<void>.delayed(const Duration(milliseconds: 100));

      expect(reads, 2);
      expect(controller.state.syncEpoch, 'epoch-2');
      expect(controller.state.sequence, 1);
      expect(controller.state.items.map((item) => item['id']), ['new']);
    },
  );

  test(
    'disconnected transport starts an immediate subscribe fallback',
    () async {
      final events = StreamController<Map<String, dynamic>>();
      addTearDown(events.close);
      var subscriptions = 0;
      final subscribeResult = Completer<Map<String, dynamic>>();
      final gateway = _FakeGateway((method, params) async {
        expect(method, 'conversation.subscribe');
        subscriptions++;
        return subscribeResult.future;
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
      expect(controller.state.online, isFalse);
      events.add({'type': 'transport', 'state': 'online'});
      await Future<void>.delayed(Duration.zero);
      expect(controller.state.online, isTrue);
      subscribeResult.complete({'result': const {}});
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
    'concurrent snapshot callers share a read and later calls read again',
    () async {
      var reads = 0;
      final first = Completer<Map<String, dynamic>>();
      final controller = ConversationController(
        gateway: _FakeGateway((method, params) async {
          reads++;
          if (reads == 1) return first.future;
          return {
            'result': {
              'conversation': {'id': 't1'},
              'revision': 8,
              'sequence': 21,
              'timeline': {
                'items': [_item('new')],
              },
            },
          };
        }),
        agentId: 'a1',
        threadId: 't1',
      );
      addTearDown(controller.dispose);
      final background = controller.refreshConversation();
      final required = controller.requireFreshSnapshot();
      final anotherBackground = controller.refreshConversation();
      expect(reads, 1);
      first.complete({
        'result': {
          'conversation': {'id': 't1'},
          'revision': 7,
          'sequence': 20,
          'timeline': {
            'items': [_item('old')],
          },
        },
      });
      await required;
      expect(await background, true);
      expect(await anotherBackground, true);
      expect(controller.state.revision, 7);
      expect(reads, 1);
      await controller.requireFreshSnapshot();
      expect(reads, 2);
      expect(controller.state.revision, 8);
      expect(controller.state.sequence, 21);
    },
  );

  test('a regressing snapshot cannot overwrite content or settings', () async {
    var reads = 0;
    final controller = ConversationController(
      gateway: _FakeGateway((method, params) async {
        reads++;
        return {
          'result': {
            'conversation': {'id': 't1'},
            'revision': reads == 1 ? 8 : 7,
            'sequence': reads == 1 ? 21 : 20,
            'settings': {'modelId': reads == 1 ? 'current' : 'stale'},
            'timeline': {
              'items': [_item(reads == 1 ? 'new' : 'stale')],
            },
          },
        };
      }),
      agentId: 'a1',
      threadId: 't1',
    );
    addTearDown(controller.dispose);
    expect(await controller.refreshConversation(), true);
    expect(await controller.refreshConversation(), false);
    expect(controller.state.sequence, 21);
    expect(controller.state.revision, 8);
    expect(controller.state.items.map((item) => item['id']), ['new']);
    expect(controller.settings?.modelId, 'current');
  });

  test(
    'an event during a read schedules one snapshot after that read',
    () async {
      final events = StreamController<Map<String, dynamic>>.broadcast();
      addTearDown(events.close);
      final first = Completer<Map<String, dynamic>>();
      final updated = Completer<void>();
      var reads = 0;
      final controller = ConversationController(
        gateway: _FakeGateway((method, params) async {
          expect(method, 'conversation.read');
          reads++;
          if (reads == 1) return first.future;
          return {
            'result': {
              'conversation': {'id': 't1'},
              'revision': 9,
              'sequence': 21,
              'timeline': {
                'items': [_item('event-update')],
              },
            },
          };
        }, events: events.stream),
        agentId: 'a1',
        threadId: 't1',
      );
      addTearDown(controller.dispose);
      controller.addListener(() {
        if (controller.state.sequence == 21 && !updated.isCompleted) {
          updated.complete();
        }
      });
      controller.startLiveSync();
      final required = controller.requireFreshSnapshot();
      events.add({'type': 'event', 'conversationId': 't1', 'sequence': 21});
      await Future<void>.delayed(const Duration(milliseconds: 200));
      expect(reads, 1);
      first.complete({
        'result': {
          'conversation': {'id': 't1'},
          'revision': 8,
          'sequence': 20,
          'timeline': {'items': []},
        },
      });
      await required;
      await updated.future;
      expect(reads, 2);
      expect(controller.state.revision, 9);
      expect(controller.state.sequence, 21);
      expect(controller.state.items.single['id'], 'event-update');
    },
  );

  test(
    'live refresh during required sync propagates the actual provider failure',
    () async {
      final events = StreamController<Map<String, dynamic>>.broadcast();
      addTearDown(events.close);
      final pending = Completer<Map<String, dynamic>>();
      var reads = 0;
      final controller = ConversationController(
        gateway: _FakeGateway((method, params) {
          expect(method, 'conversation.read');
          reads++;
          return pending.future;
        }, events: events.stream),
        agentId: 'a1',
        threadId: 't1',
      );
      addTearDown(controller.dispose);
      controller.startLiveSync();
      final required = controller.requireFreshSnapshot();
      final expectedFailure = expectLater(
        required,
        throwsA(
          isA<CoreFault>().having(
            (fault) => fault.code,
            'code',
            'provider_error',
          ),
        ),
      );
      events.add({'type': 'event', 'conversationId': 't1', 'sequence': 1});
      await Future<void>.delayed(const Duration(milliseconds: 200));
      expect(reads, 1);
      pending.completeError(CoreFault('provider_error', 'read failed'));
      await expectedFailure;
      expect(controller.state.hasSnapshot, false);
      expect(controller.state.bodyError?.code, 'provider_error');
    },
  );

  test(
    'required sync reports selection changes before considering old errors',
    () async {
      var selected = 'daemon-1';
      final pending = Completer<Map<String, dynamic>>();
      final controller = ConversationController(
        gateway: _FakeGateway((method, params) => pending.future),
        agentId: 'a1',
        threadId: 't1',
        daemonId: 'daemon-1',
        daemonIdProvider: () => selected,
      );
      addTearDown(controller.dispose);
      final required = controller.requireFreshSnapshot();
      final expectedFailure = expectLater(
        required,
        throwsA(
          isA<CoreFault>().having(
            (fault) => fault.code,
            'code',
            'selection_changed',
          ),
        ),
      );
      selected = 'daemon-2';
      pending.complete({
        'result': {
          'conversation': {'id': 't1'},
          'revision': 8,
        },
      });
      await expectedFailure;
      expect(controller.state.hasSnapshot, false);
    },
  );
}
