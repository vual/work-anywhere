import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:work_anywhere/core/core_gateway.dart';
import 'package:work_anywhere/features/update/update_controller.dart';
import 'package:work_anywhere/features/workspace/workspace_controller.dart';

import 'support/fake_update_gateway.dart';

void main() {
  late FakeUpdateGateway gateway;
  late ProviderContainer container;
  final start = DateTime.utc(2026, 11, 10, 8);

  UpdateController controller() => container.read(updateProvider.notifier);
  UpdateState state() => container.read(updateProvider);

  setUp(() {
    SharedPreferences.setMockInitialValues({});
    gateway = FakeUpdateGateway();
    container = ProviderContainer(
      overrides: [coreGatewayProvider.overrideWithValue(gateway)],
    );
    addTearDown(container.dispose);
    // Keep the provider subscribed and pin the clock.
    container.listen(updateProvider, (_, _) {});
    controller().now = () => start;
  });

  test('build performs no platform calls', () {
    expect(state().phase, UpdatePhase.idle);
    expect(gateway.calls, isEmpty);
  });

  test('manual check reports an available release and sets the badge', () async {
    await controller().check();
    expect(state().phase, UpdatePhase.available);
    expect(state().latest!.versionCode, 11);
    expect(state().pendingRelease, isNotNull);
    final preferences = await SharedPreferences.getInstance();
    expect(
      preferences.getInt('update.lastCheckedAt'),
      start.millisecondsSinceEpoch,
    );
  });

  test('up-to-date check clears the release', () async {
    gateway.latest = null;
    await controller().check();
    expect(state().phase, UpdatePhase.upToDate);
    expect(state().pendingRelease, isNull);
  });

  test('automatic check is throttled to 24 hours', () async {
    SharedPreferences.setMockInitialValues({
      'update.lastCheckedAt': start
          .subtract(const Duration(hours: 23))
          .millisecondsSinceEpoch,
    });
    expect(await controller().checkAutomatically(), isNull);
    expect(gateway.calls, isNot(contains('checkUpdate')));

    controller().now = () => start.add(const Duration(hours: 2));
    final release = await controller().checkAutomatically();
    expect(release?.versionCode, 11);
  });

  test('automatic check does not prompt for a skipped version', () async {
    SharedPreferences.setMockInitialValues({'update.skippedVersionCode': 11});
    expect(await controller().checkAutomatically(), isNull);
    expect(gateway.calls, contains('checkUpdate'));
    expect(state().phase, UpdatePhase.available);
    expect(state().pendingRelease, isNull);
  });

  test('debug builds never check', () async {
    gateway.updatable = false;
    await controller().check();
    expect(await controller().checkAutomatically(), isNull);
    expect(gateway.calls, isNot(contains('checkUpdate')));
    expect(state().updatable, isFalse);
  });

  test('skip persists the version and clears the badge', () async {
    await controller().check();
    await controller().skip();
    expect(state().pendingRelease, isNull);
    expect(state().phase, UpdatePhase.idle);
    final preferences = await SharedPreferences.getInstance();
    expect(preferences.getInt('update.skippedVersionCode'), 11);
  });

  test('update downloads then submits the installer', () async {
    await controller().check();
    await controller().startUpdate();
    expect(gateway.calls, containsAllInOrder([
      'downloadUpdate:11',
      'canInstallPackages',
      'installUpdate:11',
    ]));
    await pumpEventQueue();
    expect(state().phase, UpdatePhase.installing);
    expect(state().artifactReady, isTrue);
  });

  test('download progress events update the state', () async {
    await controller().check();
    gateway.downloadGate = Completer<void>();
    final running = controller().startUpdate();
    await pumpEventQueue();
    gateway.emit({'state': 'downloading', 'received': 40, 'total': 100});
    await pumpEventQueue();
    expect(state().phase, UpdatePhase.downloading);
    expect(state().progress, closeTo(.4, .001));
    gateway.downloadGate!.complete();
    await running;
  });

  test('cancel returns to available without installing', () async {
    await controller().check();
    gateway.downloadGate = Completer<void>();
    final running = controller().startUpdate();
    await pumpEventQueue();
    await controller().cancelDownload();
    await running;
    expect(state().phase, UpdatePhase.available);
    expect(gateway.calls, isNot(contains('installUpdate:11')));
  });

  test('missing install permission waits and resumes on return', () async {
    gateway.canInstall = false;
    await controller().check();
    await controller().startUpdate();
    expect(state().phase, UpdatePhase.permissionRequired);
    expect(state().artifactReady, isTrue);

    await controller().openPermissionSettings();
    gateway.canInstall = true;
    await controller().onResumed();
    expect(gateway.calls.where((c) => c == 'downloadUpdate:11'), hasLength(1));
    expect(gateway.calls, contains('installUpdate:11'));
  });

  test('installer cancel keeps the APK for another attempt', () async {
    await controller().check();
    await controller().startUpdate();
    await pumpEventQueue();
    gateway.emit({
      'state': 'error',
      'error': {'code': 'update_install_cancelled', 'message': 'cancelled'},
    });
    await pumpEventQueue();
    expect(state().phase, UpdatePhase.downloaded);
    expect(state().errorCode, 'update_install_cancelled');
  });

  test('signer mismatch retries with a fresh download', () async {
    await controller().check();
    gateway.installFault = CoreFault('update_signer_mismatch', 'bad cert');
    await controller().startUpdate();
    expect(state().phase, UpdatePhase.error);
    gateway.installFault = null;
    await controller().retry();
    expect(gateway.calls.where((c) => c == 'downloadUpdate:11'), hasLength(2));
  });

  test('check failures surface the stable error code', () async {
    gateway.checkFault = CoreFault('update_signature_invalid', 'bad');
    await controller().check();
    expect(state().phase, UpdatePhase.error);
    expect(state().errorCode, 'update_signature_invalid');
    gateway.checkFault = null;
    await controller().retry();
    expect(state().phase, UpdatePhase.available);
  });

  test('mirror base is persisted and passed to the check', () async {
    await controller().setMirrorBase('  https://mirror.example/waw/  ');
    await controller().check();
    expect(gateway.lastMirror, 'https://mirror.example/waw/');
    final preferences = await SharedPreferences.getInstance();
    expect(preferences.getString('update.mirrorBase'), 'https://mirror.example/waw/');
  });
}
