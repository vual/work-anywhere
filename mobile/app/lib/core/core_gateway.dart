import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

import 'mobile_core.g.dart';

class CoreFault implements Exception {
  CoreFault(this.code, this.message, {this.method, this.requestId});
  final String code;
  final String message;
  final String? method;
  final String? requestId;
  @override
  String toString() => '$code: $message';
}

class DiagnosticEntry {
  const DiagnosticEntry({
    required this.timestamp,
    required this.method,
    required this.durationMilliseconds,
    required this.outcome,
    this.errorCode,
    this.requestId,
  });

  final DateTime timestamp;
  final String method;
  final int durationMilliseconds;
  final String outcome;
  final String? errorCode;
  final String? requestId;

  String format() =>
      '${timestamp.toIso8601String()} method=$method durationMs=$durationMilliseconds outcome=$outcome'
      '${errorCode == null ? '' : ' errorCode=$errorCode'}'
      '${requestId == null ? '' : ' requestId=$requestId'}';
}

class CoreGateway {
  CoreGateway({MobileCoreApi? api}) : _api = api ?? MobileCoreApi();
  final MobileCoreApi _api;
  final List<DiagnosticEntry> _diagnostics = [];
  static const EventChannel _eventChannel = EventChannel(
    'work_anywhere/mobile_events',
  );

  List<DiagnosticEntry> get diagnostics => List.unmodifiable(_diagnostics);

  /// One shared broadcast stream for every listener. Calling
  /// `receiveBroadcastStream()` again would replace the platform message
  /// handler and the native sink, silently starving earlier listeners.
  late final Stream<Map<String, dynamic>> events = _eventChannel
      .receiveBroadcastStream()
      .where((value) => value is String)
      .cast<String>()
      .map((value) => jsonDecode(value) as Map<String, dynamic>);

  /// App-update progress and install results (`type == 'update'`).
  Stream<Map<String, dynamic>> get updateEvents =>
      events.where((event) => event['type'] == 'update');
  String exportDiagnostics() =>
      _diagnostics.map((item) => item.format()).join('\n');

  Future<Map<String, dynamic>> pair(String uri) async {
    final reply = await _api.pair(uri);
    return _decodeReply(reply);
  }

  Future<Map<String, dynamic>> restoreStored() async {
    final reply = await _api.restoreStored();
    return _decodeReply(reply);
  }

  Future<Map<String, dynamic>> selectPairing(String daemonId) async =>
      _decodeReply(await _api.selectPairing(daemonId));

  Future<Map<String, dynamic>> setPairingEnabled(
    String daemonId,
    bool enabled,
  ) async => _decodeReply(await _api.setPairingEnabled(daemonId, enabled));

  Future<Map<String, dynamic>> updatePairing(
    String daemonId,
    String displayName,
    String relayUrl,
  ) async =>
      _decodeReply(await _api.updatePairing(daemonId, displayName, relayUrl));

  Future<Map<String, dynamic>> removePairing(String daemonId) async =>
      _decodeReply(await _api.removePairing(daemonId));

  Future<Map<String, dynamic>> reorderPairings(List<String> daemonIds) async =>
      _decodeReply(await _api.reorderPairings(daemonIds));

  Future<Map<String, dynamic>> probePairing(String daemonId) async =>
      _decodeReply(await _api.probePairing(daemonId));

  Future<void> persistCredentials() async =>
      _decodeReply(await _api.persistCredentials());

  Future<void> deleteStoredCredentials() async =>
      _decodeReply(await _api.deleteStoredCredentials());

  Future<Map<String, dynamic>> callFor(
    String? daemonId,
    int? generation,
    String method,
    Map<String, dynamic> params, {
    int timeoutMilliseconds = 30000,
  }) => call(method, {
    ...params,
    if (daemonId != null && generation != null)
      '_wawTarget': {'daemonId': daemonId, 'generation': generation},
  }, timeoutMilliseconds: timeoutMilliseconds);

  Future<Map<String, dynamic>> call(
    String method,
    Map<String, dynamic> params, {
    int timeoutMilliseconds = 30000,
  }) async {
    final stopwatch = Stopwatch()..start();
    try {
      final reply = await _api.call(
        method,
        jsonEncode(params),
        timeoutMilliseconds,
      );
      final decoded = _decodeReply(
        reply,
        controlEnvelope: true,
        method: method,
      );
      _record(method, stopwatch.elapsedMilliseconds, 'ok', decoded: decoded);
      return decoded;
    } on CoreFault catch (error) {
      _record(
        method,
        stopwatch.elapsedMilliseconds,
        'error',
        errorCode: error.code,
        requestId: error.requestId,
      );
      rethrow;
    } catch (_) {
      _record(
        method,
        stopwatch.elapsedMilliseconds,
        'error',
        errorCode: 'unexpected_error',
      );
      rethrow;
    }
  }

  Future<Map<String, dynamic>> status() async =>
      _decodeReply(await _api.status());

  void close() => _api.close();

  /// `{versionName, versionCode, packageName, updatable}` of the installed app.
  Future<Map<String, dynamic>> appVersion() async =>
      _result(_decodeReply(await _api.appVersion()));

  /// `{state: upToDate}` or `{state: available, latest: {...}}`.
  Future<Map<String, dynamic>> checkUpdate(String mirrorBase) async =>
      _result(_decodeReply(await _api.checkUpdate(mirrorBase)));

  /// Blocks until the APK is downloaded and verified; progress arrives on
  /// [updateEvents].
  Future<Map<String, dynamic>> downloadUpdate(int versionCode) async =>
      _result(_decodeReply(await _api.downloadUpdate(versionCode)));

  Future<void> cancelUpdateDownload() => _api.cancelUpdateDownload();

  Future<bool> canInstallPackages() => _api.canInstallPackages();

  Future<void> openInstallPermissionSettings() =>
      _api.openInstallPermissionSettings();

  /// Submits the downloaded APK to the system installer; the outcome arrives
  /// on [updateEvents] as `installing`, `installed` or `error`.
  Future<void> installUpdate(int versionCode) async =>
      _decodeReply(await _api.installUpdate(versionCode));

  Map<String, dynamic> _result(Map<String, dynamic> decoded) =>
      decoded['result'] as Map<String, dynamic>? ?? decoded;

  Map<String, dynamic> _decodeReply(
    CoreReply reply, {
    bool controlEnvelope = false,
    String? method,
  }) {
    final decoded = jsonDecode(reply.payload) as Map<String, dynamic>;
    if (!reply.ok || decoded['ok'] == false) {
      final error = decoded['error'] as Map<String, dynamic>? ?? decoded;
      throw CoreFault(
        error['code'] as String? ?? 'core_error',
        error['message'] as String? ?? 'Core request failed',
        method: method,
        requestId: decoded['id'] as String?,
      );
    }
    if (controlEnvelope && decoded['error'] != null) {
      final error = decoded['error'] as Map<String, dynamic>;
      throw CoreFault(
        error['code'] as String? ?? 'control_error',
        error['message'] as String? ?? 'Desktop request failed',
        method: method,
        requestId: decoded['id'] as String?,
      );
    }
    return decoded;
  }

  void _record(
    String method,
    int durationMilliseconds,
    String outcome, {
    String? errorCode,
    String? requestId,
    Map<String, dynamic>? decoded,
  }) {
    final entry = DiagnosticEntry(
      timestamp: DateTime.now().toUtc(),
      method: method,
      durationMilliseconds: durationMilliseconds,
      outcome: outcome,
      errorCode: errorCode,
      requestId: requestId ?? decoded?['id'] as String?,
    );
    _diagnostics.add(entry);
    if (_diagnostics.length > 100) {
      _diagnostics.removeRange(0, _diagnostics.length - 100);
    }
    if (kDebugMode) {
      debugPrint('WorkAnywhere ${entry.format()}');
    }
  }
}
