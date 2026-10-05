import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

import '../../core/models.dart';

typedef SubmissionScope = (String?, String, String);

enum SubmissionPhase {
  syncing,
  uploading,
  sending,
  confirming,
  failed,
  unknown,
}

/// Separate from the Desktop projection. This store never writes to disk.
final localSubmissionsProvider = Provider<LocalSubmissions>((ref) {
  final store = LocalSubmissions();
  ref.onDispose(store.dispose);
  return store;
});

class LocalSubmission {
  LocalSubmission({
    required this.scope,
    required this.text,
    required List<XFile> images,
  }) : images = List.unmodifiable(images),
       clientMessageId = newMessageId();

  static String newMessageId() {
    final random = Random.secure();
    return List.generate(
      16,
      (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
    ).join();
  }

  SubmissionScope scope;
  final String text;
  final List<XFile> images;
  String clientMessageId;
  SubmissionPhase phase = SubmissionPhase.syncing;
  String? turnId;
  String? queueId;
  bool queueWrite = false;
  bool busy = false;
  bool reconciled = false;
  bool creationUnknown = false;
  String? error;
  String? errorCode;
  int uploadedBytes = 0;
  int totalBytes = 0;
  int imageIndex = 0;
  // Commit IDs survive a definite failure and may be reused in this thread.
  final Map<int, String> attachmentIds = {};
}

class LocalSubmissions extends ChangeNotifier {
  final List<LocalSubmission> _records = [];
  final Map<SubmissionScope, String> draftThreads = {};

  void bindThread(SubmissionScope draftScope, String threadId) {
    draftThreads[draftScope] = threadId;
    for (final record in records(draftScope)) {
      record.scope = (draftScope.$1, draftScope.$2, threadId);
    }
    changed();
  }

  final Map<SubmissionScope, Map<String, List<XFile>>> _queueImages = {};
  bool _disposed = false;

  List<LocalSubmission> records(SubmissionScope scope) =>
      _records.where((record) => record.scope == scope).toList(growable: false);
  bool busy(SubmissionScope scope) =>
      records(scope).any((record) => record.busy);
  Map<String, List<XFile>> queueImages(SubmissionScope scope) =>
      _queueImages.putIfAbsent(scope, () => {});

  void add(LocalSubmission record) {
    _records.add(record);
    changed();
  }

  void remove(LocalSubmission record) {
    _records.remove(record);
    changed();
  }

  void changed() {
    if (!_disposed) notifyListeners();
  }

  void reconcile(
    SubmissionScope scope,
    List<Map<String, dynamic>> items,
    List<QueuedSubmission> queue,
  ) {
    var changedState = false;
    for (final record in records(scope)) {
      final users = items
          .where((item) => item['kind'] == 'userMessage')
          .toList();
      final matchingClient = users.any(
        (item) => item['clientMessageId'] == record.clientMessageId,
      );
      final sameTurn = record.turnId == null
          ? <Map<String, dynamic>>[]
          : users.where((item) => item['turnId'] == record.turnId).toList();
      final legacyMatch =
          sameTurn.length == 1 &&
          (sameTurn.single['clientMessageId'] == null ||
              sameTurn.single['clientMessageId'] == '');
      final queued = queue
          .where((item) => item.clientMessageId == record.clientMessageId)
          .firstOrNull;
      if (matchingClient || legacyMatch || queued != null) {
        if (queued != null && record.images.isNotEmpty) {
          queueImages(scope)[record.clientMessageId] = record.images;
        }
        record.reconciled = true;
        _records.remove(record);
        changedState = true;
      }
    }
    if (changedState) {
      if (records(scope).isEmpty) {
        draftThreads.removeWhere((_, threadId) => threadId == scope.$3);
      }
      changed();
    }
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }
}
