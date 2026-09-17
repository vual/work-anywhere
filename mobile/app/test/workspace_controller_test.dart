import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:work_anywhere/core/core_gateway.dart';
import 'package:work_anywhere/core/models.dart';
import 'package:work_anywhere/features/workspace/workspace_controller.dart';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test(
    'pair failure keeps the unpaired state instead of failing the gate',
    () async {
      final gateway = _FakeCoreGateway(pairFails: true);
      final container = ProviderContainer(
        overrides: [coreGatewayProvider.overrideWithValue(gateway)],
      );
      addTearDown(container.dispose);

      final initial = await container.read(workspaceProvider.future);
      expect(initial.needsPairing, isTrue);

      await expectLater(
        container.read(workspaceProvider.notifier).pair('waw://pair?test'),
        throwsA(isA<CoreFault>()),
      );

      final result = container.read(workspaceProvider);
      expect(result.hasError, isFalse);
      expect(result.value?.needsPairing, isTrue);
    },
  );

  test('approved pair survives an initial workspace sync failure', () async {
    final gateway = _FakeCoreGateway(syncFails: true);
    final container = ProviderContainer(
      overrides: [coreGatewayProvider.overrideWithValue(gateway)],
    );
    addTearDown(container.dispose);
    await container.read(workspaceProvider.future);

    await container.read(workspaceProvider.notifier).pair('waw://pair?test');

    final result = container.read(workspaceProvider).requireValue;
    expect(result.needsPairing, isFalse);
    expect(result.syncFailed, isTrue);
    expect(result.daemons.single.id, 'daemon-1');
    expect(gateway.persisted, isTrue);
  });

  test(
    'approved pair is persisted when its first connection is rate limited',
    () async {
      final gateway = _FakeCoreGateway(retainedPairAfterFailure: true);
      final container = ProviderContainer(
        overrides: [coreGatewayProvider.overrideWithValue(gateway)],
      );
      addTearDown(container.dispose);
      await container.read(workspaceProvider.future);

      await expectLater(
        container.read(workspaceProvider.notifier).pair('waw://pair?test'),
        throwsA(
          isA<CoreFault>().having(
            (error) => error.code,
            'code',
            'rate_limited',
          ),
        ),
      );

      final result = container.read(workspaceProvider).requireValue;
      expect(result.needsPairing, isFalse);
      expect(result.syncFailed, isTrue);
      expect(result.daemons.single.phase, ConnectionPhase.offline);
      expect(gateway.persisted, isTrue);
    },
  );

  test('startup sync failure keeps an already connected daemon', () async {
    final gateway = _FakeCoreGateway(connectedOnStatus: true, syncFails: true);
    final container = ProviderContainer(
      overrides: [coreGatewayProvider.overrideWithValue(gateway)],
    );
    addTearDown(container.dispose);

    final result = await container.read(workspaceProvider.future);

    expect(result.needsPairing, isFalse);
    expect(result.syncFailed, isTrue);
    expect(result.daemons.single.id, 'daemon-1');
  });

  test('failed agent content sync keeps the agent selector usable', () async {
    final gateway = _FakeCoreGateway(
      connectedOnStatus: true,
      agents: const [
        {
          'id': 'agent-broken',
          'name': 'Broken Codex',
          'kind': 'codex',
          'status': 'error',
        },
        {
          'id': 'agent-ready',
          'name': 'Ready Codex',
          'kind': 'codex',
          'status': 'ready',
        },
      ],
      failedAgentIds: const {'agent-broken'},
    );
    final container = ProviderContainer(
      overrides: [coreGatewayProvider.overrideWithValue(gateway)],
    );
    addTearDown(container.dispose);

    var result = await container.read(workspaceProvider.future);

    expect(result.selectedAgentId, 'agent-broken');
    expect(result.syncFailed, isTrue);
    expect(result.agents.map((agent) => agent.id), [
      'agent-broken',
      'agent-ready',
    ]);
    expect(result.projects, isEmpty);
    expect(result.conversations, isEmpty);

    await container.read(workspaceProvider.notifier).selectAgent('agent-ready');
    result = container.read(workspaceProvider).requireValue;

    expect(result.selectedAgentId, 'agent-ready');
    expect(result.syncFailed, isFalse);
    expect(result.projects.single.id, 'project-agent-ready');
    expect(result.conversations.single.id, 'conversation-agent-ready');
  });

  test('duplicate conversation IDs are shown only once', () async {
    final gateway = _FakeCoreGateway(
      connectedOnStatus: true,
      agents: const [
        {
          'id': 'agent-ready',
          'name': 'Ready Codex',
          'kind': 'codex',
          'status': 'ready',
        },
      ],
      duplicateConversations: true,
    );
    final container = ProviderContainer(
      overrides: [coreGatewayProvider.overrideWithValue(gateway)],
    );
    addTearDown(container.dispose);

    final result = await container.read(workspaceProvider.future);

    expect(result.conversations.map((item) => item.id), [
      'conversation-agent-ready',
    ]);
  });

  test(
    'late failure from a previous agent does not replace the current agent',
    () async {
      final gateway = _FakeCoreGateway(
        connectedOnStatus: true,
        agents: const [
          {
            'id': 'agent-initial',
            'name': 'Initial Codex',
            'kind': 'codex',
            'status': 'ready',
          },
          {
            'id': 'agent-slow',
            'name': 'Slow Codex',
            'kind': 'codex',
            'status': 'error',
          },
          {
            'id': 'agent-ready',
            'name': 'Ready Codex',
            'kind': 'codex',
            'status': 'ready',
          },
        ],
        delayedFailureAgentId: 'agent-slow',
      );
      final container = ProviderContainer(
        overrides: [coreGatewayProvider.overrideWithValue(gateway)],
      );
      addTearDown(container.dispose);
      await container.read(workspaceProvider.future);
      final controller = container.read(workspaceProvider.notifier);

      final slowSwitch = controller.selectAgent('agent-slow');
      expect(
        container.read(workspaceProvider).requireValue.selectedAgentId,
        'agent-slow',
      );

      await controller.selectAgent('agent-ready');
      gateway.failDelayedContent();
      await slowSwitch;

      final result = container.read(workspaceProvider).requireValue;
      expect(result.selectedAgentId, 'agent-ready');
      expect(result.syncFailed, isFalse);
      expect(result.projects.single.id, 'project-agent-ready');
      expect(result.conversations.single.id, 'conversation-agent-ready');
    },
  );
}

class _FakeCoreGateway extends CoreGateway {
  _FakeCoreGateway({
    this.pairFails = false,
    this.syncFails = false,
    this.connectedOnStatus = false,
    this.retainedPairAfterFailure = false,
    this.agents = const [],
    this.failedAgentIds = const {},
    this.delayedFailureAgentId,
    this.duplicateConversations = false,
  });

  final bool pairFails;
  final bool syncFails;
  final bool connectedOnStatus;
  final bool retainedPairAfterFailure;
  final List<Map<String, dynamic>> agents;
  final Set<String> failedAgentIds;
  final String? delayedFailureAgentId;
  final bool duplicateConversations;
  Completer<Map<String, dynamic>>? _delayedContent;
  bool persisted = false;

  void failDelayedContent() => _delayedContent?.completeError(
    CoreFault('call_failed', 'delayed content sync failed'),
  );

  @override
  Future<Map<String, dynamic>> status() async => {
    'ok': true,
    'result': {
      'connected': connectedOnStatus,
      if (connectedOnStatus || retainedPairAfterFailure) 'daemonId': 'daemon-1',
    },
  };

  @override
  Future<Map<String, dynamic>> restoreStored() async =>
      throw CoreFault('no_stored_pairing', 'none');

  @override
  Future<Map<String, dynamic>> pair(String uri) async {
    if (retainedPairAfterFailure) {
      throw CoreFault('rate_limited', 'try again later');
    }
    if (pairFails) throw CoreFault('pair_failed', 'unreachable');
    return {
      'ok': true,
      'result': {'connected': true, 'daemonId': 'daemon-1'},
    };
  }

  @override
  Future<void> persistCredentials() async {
    persisted = true;
  }

  @override
  Future<Map<String, dynamic>> call(
    String method,
    Map<String, dynamic> params, {
    int timeoutMilliseconds = 30000,
  }) async {
    if (syncFails) throw CoreFault('call_failed', 'sync failed');
    if (method == 'agent.list') {
      return {'result': agents};
    }
    final agentId = params['agentId'] as String?;
    if (agentId != null && failedAgentIds.contains(agentId)) {
      throw CoreFault('call_failed', 'sync failed for $agentId');
    }
    if (method == 'project.list') {
      if (agentId == delayedFailureAgentId) {
        return (_delayedContent ??= Completer<Map<String, dynamic>>()).future;
      }
      return {
        'result': {
          'items': [
            {
              'id': 'project-$agentId',
              'agentId': agentId,
              'name': 'Project for $agentId',
              'updatedAt': 2,
            },
          ],
        },
      };
    }
    if (method == 'conversation.list') {
      final item = {
        'id': 'conversation-$agentId',
        'agentId': agentId,
        'projectId': 'project-$agentId',
        'title': 'Conversation for $agentId',
        'status': 'idle',
        'running': false,
        'updatedAt': 1,
      };
      return {
        'result': {
          'items': duplicateConversations ? [item, item] : [item],
        },
      };
    }
    return {'result': <String, dynamic>{}};
  }
}
