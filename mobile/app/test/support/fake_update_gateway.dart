import 'dart:async';

import 'package:work_anywhere/core/core_gateway.dart';

/// CoreGateway double for app-update flows; records calls and lets tests
/// script replies and push `type=update` events.
class FakeUpdateGateway extends CoreGateway {
  FakeUpdateGateway({
    this.updatable = true,
    this.versionCode = 10,
    this.canInstall = true,
  });

  bool updatable;
  int versionCode;
  bool canInstall;
  final _events = StreamController<Map<String, dynamic>>.broadcast();
  final calls = <String>[];
  String? lastMirror;

  /// Next checkUpdate reply; null means up to date.
  Map<String, dynamic>? latest = {
    'versionName': '1.2.0',
    'versionCode': 11,
    'size': 2 * 1024 * 1024,
    'notesUrl': 'https://github.com/vual/work-anywhere/releases/tag/v1.2.0',
  };
  CoreFault? checkFault;
  CoreFault? downloadFault;
  CoreFault? installFault;
  Completer<void>? downloadGate;

  @override
  Stream<Map<String, dynamic>> get events => _events.stream;

  void emit(Map<String, dynamic> event) =>
      _events.add({'type': 'update', ...event});

  Future<void> dispose() => _events.close();

  @override
  Future<Map<String, dynamic>> appVersion() async {
    calls.add('appVersion');
    return {
      'versionName': '1.1.0',
      'versionCode': versionCode,
      'packageName': 'io.workanywhere.work_anywhere',
      'updatable': updatable,
    };
  }

  @override
  Future<Map<String, dynamic>> checkUpdate(String mirrorBase) async {
    calls.add('checkUpdate');
    lastMirror = mirrorBase;
    if (checkFault != null) throw checkFault!;
    final release = latest;
    return release == null
        ? {'state': 'upToDate'}
        : {'state': 'available', 'latest': release};
  }

  @override
  Future<Map<String, dynamic>> downloadUpdate(int versionCode) async {
    calls.add('downloadUpdate:$versionCode');
    emit({'state': 'downloading', 'received': 0, 'total': 100});
    final gate = downloadGate;
    if (gate != null) await gate.future;
    if (downloadFault != null) throw downloadFault!;
    return {'path': '/cache/updates/$versionCode/app.apk'};
  }

  @override
  Future<void> cancelUpdateDownload() async {
    calls.add('cancelUpdateDownload');
    downloadFault = CoreFault('update_cancelled', 'cancelled');
    final gate = downloadGate;
    if (gate != null && !gate.isCompleted) gate.complete();
  }

  @override
  Future<bool> canInstallPackages() async {
    calls.add('canInstallPackages');
    return canInstall;
  }

  @override
  Future<void> openInstallPermissionSettings() async {
    calls.add('openInstallPermissionSettings');
  }

  @override
  Future<void> installUpdate(int versionCode) async {
    calls.add('installUpdate:$versionCode');
    if (installFault != null) throw installFault!;
    emit({'state': 'installing'});
  }
}
