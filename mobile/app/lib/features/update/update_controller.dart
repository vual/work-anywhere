import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../core/core_gateway.dart';
import '../workspace/workspace_controller.dart';

final updateProvider = NotifierProvider<UpdateController, UpdateState>(
  UpdateController.new,
);

/// Minimum spacing between automatic foreground checks.
const updateCheckInterval = Duration(hours: 24);

const _lastCheckedKey = 'update.lastCheckedAt';
const _skippedKey = 'update.skippedVersionCode';
const _mirrorKey = 'update.mirrorBase';

enum UpdatePhase {
  idle,
  checking,
  upToDate,
  available,
  downloading,
  downloaded,
  permissionRequired,
  installing,
  error,
}

class AppVersionInfo {
  const AppVersionInfo({
    required this.versionName,
    required this.versionCode,
    required this.updatable,
  });

  factory AppVersionInfo.fromJson(Map<String, dynamic> json) => AppVersionInfo(
    versionName: json['versionName'] as String? ?? '',
    versionCode: (json['versionCode'] as num?)?.toInt() ?? 0,
    updatable: json['updatable'] == true,
  );

  final String versionName;
  final int versionCode;
  final bool updatable;
}

class UpdateRelease {
  const UpdateRelease({
    required this.versionName,
    required this.versionCode,
    required this.size,
    this.notesUrl,
  });

  factory UpdateRelease.fromJson(Map<String, dynamic> json) => UpdateRelease(
    versionName: json['versionName'] as String? ?? '',
    versionCode: (json['versionCode'] as num?)?.toInt() ?? 0,
    size: (json['size'] as num?)?.toInt() ?? 0,
    notesUrl: json['notesUrl'] as String?,
  );

  final String versionName;
  final int versionCode;
  final int size;
  final String? notesUrl;
}

const _keep = Object();

class UpdateState {
  const UpdateState({
    this.phase = UpdatePhase.idle,
    this.loaded = false,
    this.app,
    this.latest,
    this.received = 0,
    this.total = 0,
    this.artifactReady = false,
    this.errorCode,
    this.errorMessage,
    this.skippedVersionCode,
    this.mirrorBase = '',
  });

  final UpdatePhase phase;
  final bool loaded;
  final AppVersionInfo? app;
  final UpdateRelease? latest;
  final int received;
  final int total;

  /// A verified APK for [latest] is waiting in the app cache.
  final bool artifactReady;
  final String? errorCode;
  final String? errorMessage;
  final int? skippedVersionCode;
  final String mirrorBase;

  bool get updatable => app?.updatable == true;

  bool get busy =>
      phase == UpdatePhase.checking ||
      phase == UpdatePhase.downloading ||
      phase == UpdatePhase.installing;

  /// A newer release the user has not skipped: drives the logo badge.
  UpdateRelease? get pendingRelease {
    final release = latest;
    if (release == null || release.versionCode == skippedVersionCode) {
      return null;
    }
    return release;
  }

  double? get progress => total > 0 ? (received / total).clamp(0, 1) : null;

  UpdateState copyWith({
    UpdatePhase? phase,
    bool? loaded,
    Object? app = _keep,
    Object? latest = _keep,
    int? received,
    int? total,
    bool? artifactReady,
    Object? errorCode = _keep,
    Object? errorMessage = _keep,
    Object? skippedVersionCode = _keep,
    String? mirrorBase,
  }) => UpdateState(
    phase: phase ?? this.phase,
    loaded: loaded ?? this.loaded,
    app: identical(app, _keep) ? this.app : app as AppVersionInfo?,
    latest: identical(latest, _keep) ? this.latest : latest as UpdateRelease?,
    received: received ?? this.received,
    total: total ?? this.total,
    artifactReady: artifactReady ?? this.artifactReady,
    errorCode: identical(errorCode, _keep)
        ? this.errorCode
        : errorCode as String?,
    errorMessage: identical(errorMessage, _keep)
        ? this.errorMessage
        : errorMessage as String?,
    skippedVersionCode: identical(skippedVersionCode, _keep)
        ? this.skippedVersionCode
        : skippedVersionCode as int?,
    mirrorBase: mirrorBase ?? this.mirrorBase,
  );
}

/// App-update state machine. Network, signature and hash checks live in Go
/// Core; APK signer checks and PackageInstaller live in the Kotlin host. This
/// controller only sequences user intent and mirrors progress events.
///
/// [build] performs no I/O; [load] reads preferences and the installed
/// version, so screens can watch the badge without touching platform
/// channels.
class UpdateController extends Notifier<UpdateState> {
  StreamSubscription<Map<String, dynamic>>? _events;
  Future<void>? _loading;
  bool _awaitingPermission = false;

  /// Injected for tests.
  DateTime Function() now = DateTime.now;

  CoreGateway get _gateway => ref.read(coreGatewayProvider);

  @override
  UpdateState build() {
    ref.onDispose(() {
      unawaited(_events?.cancel());
      _events = null;
    });
    return const UpdateState();
  }

  Future<void> load() => _loading ??= _load();

  Future<void> _load() async {
    SharedPreferences? preferences;
    try {
      preferences = await SharedPreferences.getInstance();
    } catch (_) {
      preferences = null;
    }
    AppVersionInfo? app;
    try {
      app = AppVersionInfo.fromJson(await _gateway.appVersion());
    } catch (_) {
      // Hosts without the update API (iOS) behave like a non-updatable build.
      app = null;
    }
    state = state.copyWith(
      loaded: true,
      app: app,
      skippedVersionCode: preferences?.getInt(_skippedKey),
      mirrorBase: preferences?.getString(_mirrorKey) ?? '',
    );
  }

  /// Foreground check, throttled to [updateCheckInterval]. Returns a release
  /// worth prompting for: newer, not skipped, and not already being handled.
  Future<UpdateRelease?> checkAutomatically() async {
    await load();
    if (!state.updatable || state.busy || state.latest != null) return null;
    final preferences = await SharedPreferences.getInstance();
    final last = preferences.getInt(_lastCheckedKey);
    if (last != null &&
        now().difference(DateTime.fromMillisecondsSinceEpoch(last)) <
            updateCheckInterval) {
      return null;
    }
    await check();
    return state.phase == UpdatePhase.available ? state.pendingRelease : null;
  }

  /// Manual check from the About page. A running check or download is left
  /// alone so the page simply shows its progress.
  Future<void> check() async {
    await load();
    if (!state.updatable || state.busy) return;
    if (state.phase == UpdatePhase.downloaded ||
        state.phase == UpdatePhase.permissionRequired) {
      return;
    }
    state = state.copyWith(
      phase: UpdatePhase.checking,
      errorCode: null,
      errorMessage: null,
    );
    try {
      final info = await _gateway.checkUpdate(state.mirrorBase);
      final preferences = await SharedPreferences.getInstance();
      await preferences.setInt(
        _lastCheckedKey,
        now().millisecondsSinceEpoch,
      );
      final latest = info['latest'];
      if (info['state'] == 'available' && latest is Map<String, dynamic>) {
        state = state.copyWith(
          phase: UpdatePhase.available,
          latest: UpdateRelease.fromJson(latest),
          artifactReady: false,
        );
      } else {
        state = state.copyWith(
          phase: UpdatePhase.upToDate,
          latest: null,
          artifactReady: false,
        );
      }
    } on CoreFault catch (fault) {
      _fail(fault.code, fault.message);
    } catch (error) {
      _fail('update_manifest_unavailable', error.toString());
    }
  }

  /// "Update now": download (if needed), then hand the APK to the installer.
  Future<void> startUpdate() async {
    final release = state.latest;
    if (release == null || state.busy) return;
    if (!state.artifactReady) {
      final downloaded = await _download(release);
      if (!downloaded) return;
    }
    await install();
  }

  Future<bool> _download(UpdateRelease release) async {
    _listen();
    state = state.copyWith(
      phase: UpdatePhase.downloading,
      received: 0,
      total: release.size,
      artifactReady: false,
      errorCode: null,
      errorMessage: null,
    );
    try {
      await _gateway.downloadUpdate(release.versionCode);
      state = state.copyWith(
        phase: UpdatePhase.downloaded,
        received: release.size,
        artifactReady: true,
      );
      return true;
    } on CoreFault catch (fault) {
      if (fault.code == 'update_cancelled') {
        final skipped = state.skippedVersionCode == release.versionCode;
        state = state.copyWith(
          phase: skipped ? UpdatePhase.idle : UpdatePhase.available,
          received: 0,
        );
      } else {
        _fail(fault.code, fault.message);
      }
      return false;
    } catch (error) {
      _fail('update_download_failed', error.toString());
      return false;
    }
  }

  Future<void> cancelDownload() async {
    if (state.phase != UpdatePhase.downloading) return;
    await _gateway.cancelUpdateDownload();
  }

  /// Submits the downloaded APK. Without the "install unknown apps" grant it
  /// stops at [UpdatePhase.permissionRequired]; the APK is kept.
  Future<void> install() async {
    final release = state.latest;
    if (release == null || !state.artifactReady) return;
    _listen();
    bool allowed;
    try {
      allowed = await _gateway.canInstallPackages();
    } catch (error) {
      _fail('update_install_failed', error.toString());
      return;
    }
    if (!allowed) {
      state = state.copyWith(
        phase: UpdatePhase.permissionRequired,
        errorCode: 'update_permission_required',
        errorMessage: null,
      );
      return;
    }
    state = state.copyWith(
      phase: UpdatePhase.installing,
      errorCode: null,
      errorMessage: null,
    );
    try {
      await _gateway.installUpdate(release.versionCode);
    } on CoreFault catch (fault) {
      _failInstall(fault.code, fault.message);
    } catch (error) {
      _failInstall('update_install_failed', error.toString());
    }
  }

  Future<void> openPermissionSettings() async {
    _awaitingPermission = true;
    await _gateway.openInstallPermissionSettings();
  }

  /// Called when the app returns to the foreground: continues an install that
  /// was waiting for the user to grant the permission.
  Future<void> onResumed() async {
    if (!_awaitingPermission ||
        state.phase != UpdatePhase.permissionRequired) {
      return;
    }
    _awaitingPermission = false;
    await install();
  }

  /// Error action: re-check, re-download or re-install depending on where the
  /// last attempt failed.
  Future<void> retry() async {
    final release = state.latest;
    if (release == null) return check();
    final redownload =
        !state.artifactReady ||
        state.errorCode == 'update_signer_mismatch' ||
        state.errorCode == 'update_hash_mismatch';
    if (redownload) {
      state = state.copyWith(artifactReady: false);
    }
    await startUpdate();
  }

  Future<void> skip() async {
    final release = state.latest;
    if (release == null) return;
    if (state.phase == UpdatePhase.downloading) {
      await cancelDownload();
    }
    final preferences = await SharedPreferences.getInstance();
    await preferences.setInt(_skippedKey, release.versionCode);
    state = state.copyWith(
      skippedVersionCode: release.versionCode,
      phase: state.busy ? state.phase : UpdatePhase.idle,
    );
  }

  Future<void> setMirrorBase(String value) async {
    final mirror = value.trim();
    final preferences = await SharedPreferences.getInstance();
    if (mirror.isEmpty) {
      await preferences.remove(_mirrorKey);
    } else {
      await preferences.setString(_mirrorKey, mirror);
    }
    state = state.copyWith(mirrorBase: mirror);
  }

  void _listen() {
    _events ??= _gateway.updateEvents.listen(_handleEvent, onError: (_) {});
  }

  void _handleEvent(Map<String, dynamic> event) {
    switch (event['state'] as String? ?? '') {
      case 'downloading':
        if (state.phase != UpdatePhase.downloading) return;
        state = state.copyWith(
          received: (event['received'] as num?)?.toInt() ?? state.received,
          total: (event['total'] as num?)?.toInt() ?? state.total,
        );
      case 'installing':
        if (state.artifactReady) {
          state = state.copyWith(phase: UpdatePhase.installing);
        }
      case 'installed':
        // The process is normally replaced before this arrives.
        state = state.copyWith(phase: UpdatePhase.upToDate, latest: null);
      case 'error':
        // Download failures are reported by the downloadUpdate reply; only
        // installer results arrive solely as events.
        if (state.phase != UpdatePhase.installing) return;
        final error = event['error'] as Map<String, dynamic>? ?? const {};
        _failInstall(
          error['code'] as String? ?? 'update_install_failed',
          error['message'] as String?,
        );
    }
  }

  void _failInstall(String code, String? message) {
    if (code == 'update_install_cancelled') {
      // The APK is still valid; the user can tap install again.
      state = state.copyWith(
        phase: UpdatePhase.downloaded,
        errorCode: code,
        errorMessage: message,
      );
      return;
    }
    if (code == 'update_permission_required') {
      state = state.copyWith(
        phase: UpdatePhase.permissionRequired,
        errorCode: code,
        errorMessage: message,
      );
      return;
    }
    _fail(code, message);
  }

  void _fail(String code, String? message) {
    state = state.copyWith(
      phase: UpdatePhase.error,
      errorCode: code,
      errorMessage: message,
    );
  }
}
