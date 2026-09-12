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
}

class _FakeCoreGateway extends CoreGateway {
  _FakeCoreGateway({
    this.pairFails = false,
    this.syncFails = false,
    this.connectedOnStatus = false,
    this.retainedPairAfterFailure = false,
  });

  final bool pairFails;
  final bool syncFails;
  final bool connectedOnStatus;
  final bool retainedPairAfterFailure;
  bool persisted = false;

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
      return {'result': <Map<String, dynamic>>[]};
    }
    return {'result': <String, dynamic>{}};
  }
}
