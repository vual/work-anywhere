import 'dart:io';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
import 'package:work_anywhere/core/core_gateway.dart';
import 'package:work_anywhere/features/conversation/conversation_screen.dart';
import 'package:work_anywhere/features/workspace/workspace_controller.dart';
import 'package:work_anywhere/l10n/generated/app_localizations.dart';

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

  @override
  Stream<Map<String, dynamic>> get events => const Stream.empty();
}

class _FakeWorkspaceController extends WorkspaceController {
  @override
  Future<WorkspaceState> build() async =>
      const WorkspaceState(needsPairing: false, selectedAgentId: 'agent-1');
}

class _FakeImagePicker extends ImagePicker {
  _FakeImagePicker(this.selections);

  final List<List<XFile>> selections;
  final List<int?> requestedLimits = [];

  @override
  Future<List<XFile>> pickMultiImage({
    double? maxWidth,
    double? maxHeight,
    int? imageQuality,
    int? limit,
    bool requestFullMetadata = true,
  }) async {
    requestedLimits.add(limit);
    return selections.isEmpty ? const [] : selections.removeAt(0);
  }
}

class _SizedXFile extends XFile {
  _SizedXFile(super.path, this.byteLength, {super.mimeType});

  final int byteLength;

  @override
  Future<int> length() => Future.value(byteLength);
}

XFile _temporaryImage(Directory directory, String name, {int bytes = 4}) {
  final file = File('${directory.path}${Platform.pathSeparator}$name');
  final content = Uint8List.fromList(List<int>.filled(bytes, 1));
  file.writeAsBytesSync(content);
  return XFile.fromData(
    content,
    path: file.path,
    name: name,
    mimeType: 'image/jpeg',
  );
}

Future<void> _finishFileIo(WidgetTester tester) async {
  await tester.runAsync(
    () => Future<void>.delayed(const Duration(milliseconds: 50)),
  );
  await tester.pump();
}

void _deleteTemporaryDirectory(Directory directory) {
  try {
    directory.deleteSync(recursive: true);
  } on FileSystemException {
    // Flutter's image cache can briefly keep a Windows file handle open.
  }
}

Map<String, dynamic> _item(String id) => {
  'id': id,
  'kind': 'agentMessage',
  'blocks': [
    {
      'kind': 'text',
      'text': List.filled(12, 'content for $id').join('\n'),
      'truncated': false,
    },
  ],
};

void main() {
  testWidgets('timeline summary is a single non-expandable row', (
    tester,
  ) async {
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list' || method == 'conversation.subscribe') {
        return {'result': const {}};
      }
      return {
        'result': {
          'conversation': {
            'id': 'thread-summary',
            'title': 'Summary thread',
            'running': false,
          },
          'timeline': {
            'items': [
              {
                'id': 'mcp-1',
                'kind': 'mcpToolCall',
                'status': 'completed',
                'blocks': [
                  {
                    'kind': 'summary',
                    'text': 'github/list_issues',
                    'summaryKind': 'mcpToolCall',
                    'summaryValue': 'github/list_issues',
                    'truncated': false,
                  },
                ],
              },
            ],
          },
        },
      };
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(gateway),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: const MaterialApp(
          locale: Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConversationScreen(conversationId: 'thread-summary'),
        ),
      ),
    );
    await tester.pump(const Duration(milliseconds: 100));
    await tester.pump(const Duration(milliseconds: 100));

    expect(find.text('调用 MCP：github/list_issues'), findsOneWidget);
    expect(find.byType(ExpansionTile), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('timeline summary is localized in English', (tester) async {
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list' || method == 'conversation.subscribe') {
        return {'result': const {}};
      }
      return {
        'result': {
          'conversation': {
            'id': 'thread-summary-en',
            'title': 'Summary thread',
            'running': false,
          },
          'timeline': {
            'items': [
              {
                'id': 'mcp-1',
                'kind': 'mcpToolCall',
                'status': 'completed',
                'blocks': [
                  {
                    'kind': 'summary',
                    'text': 'github/list_issues',
                    'summaryKind': 'mcpToolCall',
                    'summaryValue': 'github/list_issues',
                    'truncated': false,
                  },
                ],
              },
            ],
          },
        },
      };
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(gateway),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: const MaterialApp(
          locale: Locale('en'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConversationScreen(conversationId: 'thread-summary-en'),
        ),
      ),
    );
    await tester.pump(const Duration(milliseconds: 100));
    await tester.pump(const Duration(milliseconds: 100));

    expect(find.text('Called MCP: github/list_issues'), findsOneWidget);
    expect(find.text('Completed'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('plan timeline card opens full raw markdown in a bottom sheet', (
    tester,
  ) async {
    final fullPlan =
        '# 实施计划\n\n- 第一步\n- 第二步\n- 第三步\n\n```sh\necho done\n```\n\n完整计划末行';
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list' || method == 'conversation.subscribe') {
        return {'result': const {}};
      }
      if (method == 'conversation.content.read') {
        return {
          'result': {
            'text': fullPlan,
            'nextOffset': fullPlan.length,
            'hasMore': false,
          },
        };
      }
      return {
        'result': {
          'conversation': {
            'id': 'thread-plan',
            'title': 'Plan thread',
            'running': false,
          },
          'timeline': {
            'items': [
              {
                'id': 'plan-1',
                'turnId': 'turn-1',
                'kind': 'plan',
                'blocks': [
                  {
                    'kind': 'plan',
                    'text': '# 实施计划\n\n- 第一步\n- 第二步\n- 第三步',
                    'truncated': true,
                    'contentRef': 'plan-ref',
                    'totalBytes': 100,
                  },
                ],
              },
            ],
          },
        },
      };
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(gateway),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: const MaterialApp(
          locale: Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConversationScreen(conversationId: 'thread-plan'),
        ),
      ),
    );
    await tester.pump(const Duration(milliseconds: 100));
    await tester.pump(const Duration(milliseconds: 100));

    expect(find.text('查看完整计划'), findsOneWidget);
    expect(find.text('第四步'), findsNothing);
    await tester.tap(find.text('查看完整计划'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));

    expect(find.text('完整计划末行'), findsOneWidget);
    expect(find.text('echo done'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('conversation opens at the bottom of the timeline', (
    tester,
  ) async {
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list') return {'result': <dynamic>[]};
      if (method == 'conversation.subscribe') return {'result': const {}};
      return {
        'result': {
          'conversation': {
            'id': 'thread-1',
            'title': 'Thread',
            'running': false,
          },
          'timeline': {
            'items': [
              for (var index = 30; index > 0; index--) _item('item-$index'),
            ],
          },
        },
      };
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(gateway),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConversationScreen(conversationId: 'thread-1'),
        ),
      ),
    );

    for (var index = 0; index < 10; index++) {
      await tester.pump();
    }

    final scrollable = tester.state<ScrollableState>(
      find
          .descendant(
            of: find.byType(ListView),
            matching: find.byType(Scrollable),
          )
          .first,
    );
    expect(
      scrollable.position.pixels,
      closeTo(scrollable.position.maxScrollExtent, 0.001),
    );
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('new timeline content follows when already at the bottom', (
    tester,
  ) async {
    var reads = 0;
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list') return {'result': <dynamic>[]};
      if (method == 'conversation.subscribe') return {'result': const {}};
      reads += 1;
      final newest = reads == 1 ? 30 : 31;
      return {
        'result': {
          'conversation': {
            'id': 'thread-follow',
            'title': 'Following thread',
            'running': true,
          },
          'sequence': reads,
          'timeline': {
            'items': [
              for (var index = newest; index > 0; index--) _item('item-$index'),
            ],
          },
        },
      };
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(gateway),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConversationScreen(conversationId: 'thread-follow'),
        ),
      ),
    );
    for (var index = 0; index < 10; index++) {
      await tester.pump();
    }

    final scrollable = tester.state<ScrollableState>(
      find
          .descendant(
            of: find.byType(ListView),
            matching: find.byType(Scrollable),
          )
          .first,
    );
    expect(scrollable.position.extentAfter, closeTo(0, 0.001));

    await tester.tap(find.byIcon(Icons.refresh_rounded));
    for (var index = 0; index < 5; index++) {
      await tester.pump();
    }

    expect(reads, 2);
    expect(scrollable.position.extentAfter, closeTo(0, 0.001));
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('new timeline content preserves position when reading above', (
    tester,
  ) async {
    var reads = 0;
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list') return {'result': <dynamic>[]};
      if (method == 'conversation.subscribe') return {'result': const {}};
      reads += 1;
      final newest = reads == 1 ? 30 : 31;
      return {
        'result': {
          'conversation': {
            'id': 'thread-reading',
            'title': 'Reading thread',
            'running': true,
          },
          'sequence': reads,
          'timeline': {
            'items': [
              for (var index = newest; index > 0; index--) _item('item-$index'),
            ],
          },
        },
      };
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(gateway),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConversationScreen(conversationId: 'thread-reading'),
        ),
      ),
    );
    for (var index = 0; index < 10; index++) {
      await tester.pump();
    }

    final scrollable = tester.state<ScrollableState>(
      find
          .descendant(
            of: find.byType(ListView),
            matching: find.byType(Scrollable),
          )
          .first,
    );
    scrollable.position.jumpTo(scrollable.position.maxScrollExtent - 300);
    final readingOffset = scrollable.position.pixels;
    expect(scrollable.position.extentAfter, greaterThan(24));

    await tester.tap(find.byIcon(Icons.refresh_rounded));
    for (var index = 0; index < 5; index++) {
      await tester.pump();
    }

    expect(reads, 2);
    expect(scrollable.position.pixels, closeTo(readingOffset, 0.001));
    expect(scrollable.position.extentAfter, greaterThan(24));
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('draft conversation is local until the first message is sent', (
    tester,
  ) async {
    final calls = <String>[];
    final gateway = _FakeGateway((method, params) async {
      calls.add(method);
      switch (method) {
        case 'conversation.create':
          return {
            'result': {'id': 'thread-new'},
          };
        case 'turn.start':
          return {'result': const {}};
        case 'conversation.read':
          return {
            'result': {
              'conversation': {
                'id': 'thread-new',
                'title': 'New conversation',
                'running': false,
              },
              'timeline': {'items': const []},
            },
          };
        case 'action.list':
          return {'result': const <dynamic>[]};
        default:
          fail('unexpected method: $method');
      }
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(gateway),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: const MaterialApp(
          locale: Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConversationScreen(
            conversationId: null,
            projectId: 'project-1',
            projectName: 'Work Anywhere',
          ),
        ),
      ),
    );

    for (var index = 0; index < 5; index++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    expect(find.text('Work Anywhere'), findsOneWidget);
    expect(calls, isEmpty);
    final sendButton = find.ancestor(
      of: find.byIcon(Icons.arrow_upward_rounded),
      matching: find.byType(IconButton),
    );
    expect(tester.widget<IconButton>(sendButton.first).onPressed, isNull);

    await tester.enterText(find.byType(TextField), 'Hello from mobile');
    await tester.pump();
    expect(tester.widget<IconButton>(sendButton.first).onPressed, isNotNull);
    await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
    await tester.pump();
    await tester.pump();
    await tester.pump();

    expect(calls, containsAllInOrder(['conversation.create', 'turn.start']));
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('running conversation keeps the composer and uses a stop icon', (
    tester,
  ) async {
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list') return {'result': const <dynamic>[]};
      if (method == 'conversation.subscribe') return {'result': const {}};
      return {
        'result': {
          'conversation': {
            'id': 'thread-running',
            'title': 'Running thread',
            'running': true,
          },
          'timeline': {'items': const []},
        },
      };
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(gateway),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: const MaterialApp(
          locale: Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConversationScreen(conversationId: 'thread-running'),
        ),
      ),
    );

    for (var index = 0; index < 5; index++) {
      await tester.pump(const Duration(milliseconds: 100));
    }

    expect(find.byType(TextField), findsOneWidget);
    expect(find.byIcon(Icons.add_rounded), findsOneWidget);
    expect(find.byIcon(Icons.stop_rounded), findsOneWidget);
    expect(find.text('任务运行中 · 停止'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('image selections append, deduplicate, remove, and scroll', (
    tester,
  ) async {
    final directory = Directory.systemTemp.createTempSync('waw-images-');
    addTearDown(() => _deleteTemporaryDirectory(directory));
    final images = [
      for (var index = 1; index <= 10; index++)
        _temporaryImage(directory, 'photo-$index.jpg'),
    ];
    final picker = _FakeImagePicker([
      images.take(2).toList(),
      [images[1], ...images.skip(2)],
    ]);

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          imagePickerProvider.overrideWithValue(picker),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: const MaterialApp(
          locale: Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConversationScreen(
            conversationId: null,
            projectId: 'project-1',
          ),
        ),
      ),
    );

    await tester.tap(find.byTooltip('添加附件'));
    await _finishFileIo(tester);
    await tester.tap(find.byTooltip('添加附件'));
    await _finishFileIo(tester);

    expect(picker.requestedLimits, [10, 8]);
    final strip = find.byKey(const ValueKey('attachment-thumbnail-strip'));
    expect(strip, findsOneWidget);
    expect(
      find.descendant(of: strip, matching: find.byType(Image)),
      findsNWidgets(10),
    );
    await tester.tap(find.byTooltip('添加附件'));
    await tester.pump();
    expect(find.text('一次最多选择 10 张图片'), findsOneWidget);
    expect(picker.requestedLimits, [10, 8]);
    final scrollable = tester.state<ScrollableState>(
      find.descendant(of: strip, matching: find.byType(Scrollable)),
    );
    expect(scrollable.position.maxScrollExtent, greaterThan(0));
    await tester.drag(strip, const Offset(-240, 0));
    await tester.pump();
    expect(scrollable.position.pixels, greaterThan(0));

    await tester.tap(
      find.byKey(ValueKey('remove-attachment-${images.last.path}')),
    );
    await tester.pump();
    expect(
      find.descendant(of: strip, matching: find.byType(Image)),
      findsNWidgets(9),
    );
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('failed image upload aborts and keeps the draft', (tester) async {
    final directory = Directory.systemTemp.createTempSync('waw-failure-');
    addTearDown(() => _deleteTemporaryDirectory(directory));
    final images = [
      _temporaryImage(directory, 'first.jpg'),
      _temporaryImage(directory, 'second.jpg'),
    ];
    final picker = _FakeImagePicker([images]);
    final aborted = <String>[];
    var nextAttachment = 0;
    var turnStarted = false;
    final gateway = _FakeGateway((method, params) async {
      switch (method) {
        case 'conversation.create':
          return {
            'result': {'id': 'thread-failure'},
          };
        case 'attachment.begin':
          nextAttachment += 1;
          return {
            'result': {'attachmentId': 'attachment-$nextAttachment'},
          };
        case 'attachment.chunk':
          if (params['attachmentId'] == 'attachment-2') {
            throw CoreFault('peer_offline', 'upload failed');
          }
          return {'result': const {}};
        case 'attachment.commit':
          return {'result': const {}};
        case 'attachment.abort':
          aborted.add(params['attachmentId'] as String);
          return {'result': const {}};
        case 'turn.start':
          turnStarted = true;
          return {'result': const {}};
        case 'conversation.subscribe':
          return {'result': const {}};
        default:
          fail('unexpected method: $method');
      }
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(gateway),
          imagePickerProvider.overrideWithValue(picker),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: const MaterialApp(
          locale: Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConversationScreen(
            conversationId: null,
            projectId: 'project-1',
          ),
        ),
      ),
    );

    await tester.tap(find.byTooltip('添加附件'));
    await _finishFileIo(tester);
    await tester.enterText(find.byType(TextField), 'Keep this draft');
    await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
    await tester.runAsync(() async {
      for (var index = 0; index < 50 && aborted.isEmpty; index++) {
        await Future<void>.delayed(const Duration(milliseconds: 10));
      }
    });
    await tester.pump();

    expect(aborted, ['attachment-2']);
    expect(turnStarted, isFalse);
    expect(find.text('Keep this draft'), findsOneWidget);
    expect(
      find.descendant(
        of: find.byKey(const ValueKey('attachment-thumbnail-strip')),
        matching: find.byType(Image),
      ),
      findsNWidgets(2),
    );
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('three images upload in order with one client message id', (
    tester,
  ) async {
    final directory = Directory.systemTemp.createTempSync('waw-upload-');
    addTearDown(() => _deleteTemporaryDirectory(directory));
    final images = [
      _temporaryImage(directory, 'one.jpg'),
      _temporaryImage(directory, 'two.jpg'),
      _temporaryImage(directory, 'three.jpg'),
    ];
    final picker = _FakeImagePicker([images]);
    final beginParams = <Map<String, dynamic>>[];
    Map<String, dynamic>? turnParams;
    var nextAttachment = 0;
    final gateway = _FakeGateway((method, params) async {
      switch (method) {
        case 'conversation.create':
          return {
            'result': {'id': 'thread-images'},
          };
        case 'attachment.begin':
          beginParams.add(Map<String, dynamic>.from(params));
          nextAttachment += 1;
          return {
            'result': {'attachmentId': 'attachment-$nextAttachment'},
          };
        case 'attachment.chunk':
        case 'attachment.commit':
          return {'result': const {}};
        case 'turn.start':
          turnParams = Map<String, dynamic>.from(params);
          return {'result': const {}};
        case 'conversation.read':
          return {
            'result': {
              'conversation': {
                'id': 'thread-images',
                'title': 'Images',
                'running': true,
              },
              'timeline': {'items': const []},
            },
          };
        case 'action.list':
          return {'result': const <dynamic>[]};
        case 'conversation.subscribe':
          return {'result': const {}};
        default:
          fail('unexpected method: $method');
      }
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(gateway),
          imagePickerProvider.overrideWithValue(picker),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: const MaterialApp(
          locale: Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConversationScreen(
            conversationId: null,
            projectId: 'project-1',
          ),
        ),
      ),
    );

    await tester.tap(find.byTooltip('添加附件'));
    await _finishFileIo(tester);
    await tester.enterText(find.byType(TextField), 'Analyze these screenshots');
    await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
    await tester.runAsync(() async {
      for (var index = 0; index < 50 && turnParams == null; index++) {
        await Future<void>.delayed(const Duration(milliseconds: 10));
      }
    });
    await tester.pump();

    expect(beginParams, hasLength(3));
    final clientMessageId = beginParams.first['clientMessageId'];
    expect(beginParams.map((params) => params['clientMessageId']).toSet(), {
      clientMessageId,
    });
    expect(beginParams.map((params) => params['fileName']), [
      'one.jpg',
      'two.jpg',
      'three.jpg',
    ]);
    expect(turnParams?['clientMessageId'], clientMessageId);
    expect(turnParams?['attachmentIds'], [
      'attachment-1',
      'attachment-2',
      'attachment-3',
    ]);
    expect(
      find.byKey(const ValueKey('attachment-thumbnail-strip')),
      findsNothing,
    );
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('oversized image total is rejected before draft creation', (
    tester,
  ) async {
    final picker = _FakeImagePicker([
      [
        _SizedXFile('large-a.jpg', 25 * 1024 * 1024, mimeType: 'image/jpeg'),
        _SizedXFile('large-b.jpg', 25 * 1024 * 1024, mimeType: 'image/jpeg'),
      ],
    ]);
    final calls = <String>[];
    final gateway = _FakeGateway((method, params) async {
      calls.add(method);
      fail('unexpected method: $method');
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(gateway),
          imagePickerProvider.overrideWithValue(picker),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: const MaterialApp(
          locale: Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConversationScreen(
            conversationId: null,
            projectId: 'project-1',
          ),
        ),
      ),
    );

    await tester.tap(find.byTooltip('添加附件'));
    await _finishFileIo(tester);

    expect(find.text('单张图片和图片总大小均不能超过 48 MiB'), findsOneWidget);
    expect(
      find.byKey(const ValueKey('attachment-thumbnail-strip')),
      findsNothing,
    );
    expect(calls, isEmpty);
    await tester.pumpWidget(const SizedBox());
  });
}
