import 'package:flutter_test/flutter_test.dart';
import 'package:work_anywhere/core/models.dart';
import 'package:work_anywhere/features/conversation/local_submissions.dart';

void main() {
  const SubmissionScope scope = ('daemon-1', 'agent-1', 'thread-1');

  test('authoritative client message id removes only its local bubble', () {
    final store = LocalSubmissions();
    final first = LocalSubmission(scope: scope, text: 'same', images: const []);
    final second = LocalSubmission(
      scope: scope,
      text: 'same',
      images: const [],
    );
    store
      ..add(first)
      ..add(second)
      ..reconcile(scope, [
        {
          'id': 'user-1',
          'kind': 'userMessage',
          'turnId': 'turn-1',
          'clientMessageId': first.clientMessageId,
        },
      ], const []);

    expect(store.records(scope), [second]);
    store.reconcile(scope, [
      {
        'id': 'user-1',
        'kind': 'userMessage',
        'turnId': 'turn-1',
        'clientMessageId': first.clientMessageId,
      },
    ], const []);
    expect(store.records(scope), [second]);
  });

  test('authoritative queue item replaces its local bubble', () {
    final store = LocalSubmissions();
    final record = LocalSubmission(
      scope: scope,
      text: 'queued',
      images: const [],
    );
    store.add(record);
    store.reconcile(scope, const [], [
      QueuedSubmission(
        id: 'queue-1',
        clientMessageId: 'placeholder',
        text: 'queued',
        attachmentCount: 0,
      ),
    ]);
    expect(store.records(scope), [record]);

    store.reconcile(scope, const [], [
      QueuedSubmission(
        id: 'queue-1',
        clientMessageId: record.clientMessageId,
        text: 'queued',
        attachmentCount: 0,
      ),
    ]);
    expect(store.records(scope), isEmpty);
  });

  test(
    'draft binding keeps pending records addressable after page re-entry',
    () {
      final store = LocalSubmissions();
      const draftScope = ('daemon-1', 'agent-1', 'draft:project-1');
      final record = LocalSubmission(
        scope: draftScope,
        text: 'first',
        images: const [],
      );
      store.add(record);
      store.bindThread(draftScope, 'thread-created');

      expect(store.draftThreads[draftScope], 'thread-created');
      expect(store.records(('daemon-1', 'agent-1', 'thread-created')), [
        record,
      ]);
    },
  );

  test('legacy fallback requires one user message in the accepted turn', () {
    final store = LocalSubmissions();
    final record = LocalSubmission(
      scope: scope,
      text: 'hello',
      images: const [],
    )..turnId = 'turn-1';
    store.add(record);
    store.reconcile(scope, [
      {'id': 'one', 'kind': 'userMessage', 'turnId': 'turn-1'},
      {'id': 'two', 'kind': 'userMessage', 'turnId': 'turn-1'},
    ], const []);
    expect(store.records(scope), [record]);

    store.reconcile(scope, [
      {'id': 'one', 'kind': 'userMessage', 'turnId': 'turn-1'},
    ], const []);
    expect(store.records(scope), isEmpty);
  });
}
