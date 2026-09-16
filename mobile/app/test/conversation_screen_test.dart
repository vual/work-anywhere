import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:work_anywhere/app/theme.dart';
import 'package:work_anywhere/core/core_gateway.dart';
import 'package:work_anywhere/core/models.dart';
import 'package:work_anywhere/features/conversation/conversation_screen.dart';
import 'package:work_anywhere/features/workspace/workspace_controller.dart';
import 'package:work_anywhere/l10n/generated/app_localizations.dart';

class _FakeGateway extends CoreGateway {
  _FakeGateway(
    this.handler, {
    Stream<Map<String, dynamic>>? events,
    this.catalogs,
  }) : _events = events ?? const Stream.empty();
  final Future<Map<String, dynamic>> Function(
    String method,
    Map<String, dynamic> params,
  )
  handler;
  final Stream<Map<String, dynamic>> _events;
  final Map<String, Map<String, dynamic>>? catalogs;

  @override
  Future<Map<String, dynamic>> call(
    String method,
    Map<String, dynamic> params, {
    int timeoutMilliseconds = 30000,
  }) {
    if (method.startsWith('catalog.')) {
      final catalog = catalogs?[method];
      if (catalog != null) return Future.value({'result': catalog});
      return Future.error(CoreFault('catalog_unavailable', 'not configured'));
    }
    return handler(method, params);
  }

  @override
  Stream<Map<String, dynamic>> get events => _events;
}

class _FakeWorkspaceController extends WorkspaceController {
  @override
  Future<WorkspaceState> build() async => const WorkspaceState(
    needsPairing: false,
    agents: [
      AgentSummary(
        id: 'agent-1',
        name: 'Codex',
        kind: 'codex',
        status: 'ready',
      ),
    ],
    projects: [
      ProjectSummary(
        id: 'project-1',
        agentId: 'agent-1',
        name: 'Project Alpha',
        updatedAt: 0,
      ),
    ],
    selectedAgentId: 'agent-1',
    selectedDaemonId: 'daemon-1',
  );
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

final _settingsCatalogs = <String, Map<String, dynamic>>{
  'catalog.models': {
    'items': [
      {
        'id': 'model-a',
        'name': 'GPT A',
        'description': 'Primary model',
        'default': true,
        'selectable': true,
        'defaultReasoningEffort': 'high',
        'reasoningEfforts': [
          {'id': 'low', 'description': 'Fast'},
          {'id': 'high', 'description': 'Deep'},
        ],
      },
      {
        'id': 'model-b',
        'name': 'GPT B',
        'description': 'Alternative model',
        'default': false,
        'selectable': true,
        'defaultReasoningEffort': 'medium',
        'reasoningEfforts': [
          {'id': 'medium', 'description': 'Balanced'},
        ],
      },
    ],
  },
  'catalog.permissionProfiles': {
    'items': [
      {'id': 'default', 'available': true},
      {'id': 'autoReview', 'available': true},
      {'id': 'readOnly', 'available': true},
      {'id': 'fullAccess', 'available': true, 'dangerous': true},
    ],
  },
};

void main() {
  testWidgets('plan confirmation revises and replaces the composer', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(360, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    SharedPreferences.setMockInitialValues({});
    final events = StreamController<Map<String, dynamic>>.broadcast();
    addTearDown(events.close);
    final calls = <Map<String, dynamic>>[];
    var responded = false;
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list') return {'result': <dynamic>[]};
      if (method == 'conversation.subscribe') return {'result': const {}};
      if (method == 'plan.respond') {
        calls.add(Map<String, dynamic>.from(params));
        responded = true;
        return {
          'result': {'started': true, 'turnId': 'turn-2'},
        };
      }
      return {
        'result': {
          'conversation': {
            'id': 'thread-plan',
            'title': 'Plan thread',
            'running': responded,
          },
          'revision': responded ? 8 : 7,
          'timeline': {'items': const []},
          if (!responded)
            'planConfirmation': {
              'planItemId': 'plan-1',
              'turnId': 'turn-1',
              'revision': 7,
            },
        },
      };
    }, events: events.stream);

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(gateway),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: MaterialApp(
          locale: const Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context)
                .copyWith(textScaler: const TextScaler.linear(2)),
            child: child!,
          ),
          home: const ConversationScreen(conversationId: 'thread-plan'),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(
      find.byKey(const ValueKey('plan-confirmation-panel')),
      findsOneWidget,
    );
    expect(find.text('在电脑上继续工作'), findsNothing);
    expect(find.byKey(const ValueKey('skip-plan')), findsOneWidget);
    await tester.enterText(
      find.byKey(const ValueKey('plan-revision-input')),
      '请增加测试',
    );
    await tester.pump();
    expect(find.byKey(const ValueKey('submit-plan-revision')), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('submit-plan-revision')));
    await tester.pumpAndSettle();

    expect(calls, hasLength(1));
    expect(calls.single['decision'], 'revise');
    expect(calls.single['text'], '请增加测试');
    expect(calls.single['expectedRevision'], 7);
    expect(find.byKey(const ValueKey('plan-confirmation-panel')), findsNothing);
  });

  testWidgets('skipping a plan is local and survives re-entry', (tester) async {
    SharedPreferences.setMockInitialValues({});
    final events = StreamController<Map<String, dynamic>>.broadcast();
    addTearDown(events.close);
    var networkResponses = 0;
    var planItemId = 'plan-skip';
    var revision = 3;
    Future<Map<String, dynamic>> handler(
      String method,
      Map<String, dynamic> params,
    ) async {
      if (method == 'plan.respond') networkResponses++;
      if (method == 'action.list') return {'result': <dynamic>[]};
      if (method == 'conversation.subscribe') return {'result': const {}};
      return {
        'result': {
          'conversation': {
            'id': 'thread-skip',
            'title': 'Plan thread',
            'running': false,
          },
          'revision': revision,
          'timeline': {'items': const []},
          'planConfirmation': {
            'planItemId': planItemId,
            'turnId': 'turn-1',
            'revision': revision,
          },
        },
      };
    }

    Widget screen() => ProviderScope(
      overrides: [
        coreGatewayProvider.overrideWithValue(
          _FakeGateway(handler, events: events.stream),
        ),
        workspaceProvider.overrideWith(_FakeWorkspaceController.new),
      ],
      child: const MaterialApp(
        locale: Locale('zh', 'CN'),
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        home: ConversationScreen(conversationId: 'thread-skip'),
      ),
    );

    await tester.pumpWidget(screen());
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('skip-plan')));
    await tester.pumpAndSettle();
    expect(networkResponses, 0);
    expect(find.text('在电脑上继续工作'), findsOneWidget);

    await tester.pumpWidget(const SizedBox());
    await tester.pumpAndSettle();
    await tester.pumpWidget(screen());
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('plan-confirmation-panel')), findsNothing);
    expect(networkResponses, 0);

    planItemId = 'plan-new';
    revision = 4;
    await tester.tap(find.byIcon(Icons.more_vert_rounded));
    await tester.pumpAndSettle();
    await tester.tap(find.byIcon(Icons.refresh_rounded));
    await tester.pumpAndSettle();
    expect(
      find.byKey(const ValueKey('plan-confirmation-panel')),
      findsOneWidget,
    );
  });

  testWidgets('implementing a plan sends the authoritative plan identity', (
    tester,
  ) async {
    SharedPreferences.setMockInitialValues({});
    final events = StreamController<Map<String, dynamic>>.broadcast();
    addTearDown(events.close);
    Map<String, dynamic>? response;
    var completed = false;
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list') return {'result': <dynamic>[]};
      if (method == 'conversation.subscribe') return {'result': const {}};
      if (method == 'plan.respond') {
        response = Map<String, dynamic>.from(params);
        completed = true;
        return {
          'result': {'started': true},
        };
      }
      return {
        'result': {
          'conversation': {'id': 'thread-implement', 'running': completed},
          'revision': completed ? 12 : 11,
          'timeline': {'items': const []},
          if (!completed)
            'planConfirmation': {
              'planItemId': 'plan-11',
              'turnId': 'turn-11',
              'revision': 11,
            },
        },
      };
    }, events: events.stream);
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
          home: ConversationScreen(conversationId: 'thread-implement'),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('implement-plan')));
    await tester.pumpAndSettle();

    expect(response?['decision'], 'implement');
    expect(response?['planItemId'], 'plan-11');
    expect(response?['expectedRevision'], 11);
    expect(response?['text'], 'Implement this plan.');
  });

  testWidgets('pending action takes precedence over plan confirmation', (
    tester,
  ) async {
    SharedPreferences.setMockInitialValues({});
    final events = StreamController<Map<String, dynamic>>.broadcast();
    addTearDown(events.close);
    final gateway = _FakeGateway((method, params) async {
      if (method == 'conversation.subscribe') return {'result': const {}};
      if (method == 'action.list') {
        return {
          'result': [
            {
              'id': 'action-1',
              'conversationId': 'thread-action-plan',
              'method': 'requestApproval',
            },
          ],
        };
      }
      return {
        'result': {
          'conversation': {
            'id': 'thread-action-plan',
            'title': 'Action before plan',
            'running': false,
          },
          'revision': 5,
          'timeline': {'items': const []},
          'planConfirmation': {
            'planItemId': 'plan-5',
            'turnId': 'turn-5',
            'revision': 5,
          },
        },
      };
    }, events: events.stream);

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
          home: ConversationScreen(conversationId: 'thread-action-plan'),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('需要你确认'), findsOneWidget);
    expect(find.byKey(const ValueKey('plan-confirmation-panel')), findsNothing);
  });

  testWidgets('failed plan revision keeps the panel and input', (tester) async {
    SharedPreferences.setMockInitialValues({});
    final events = StreamController<Map<String, dynamic>>.broadcast();
    addTearDown(events.close);
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list') return {'result': <dynamic>[]};
      if (method == 'conversation.subscribe') return {'result': const {}};
      if (method == 'plan.respond') {
        throw CoreFault('peer_offline', 'offline');
      }
      return {
        'result': {
          'conversation': {
            'id': 'thread-plan-error',
            'title': 'Plan error',
            'running': false,
          },
          'revision': 4,
          'timeline': {'items': const []},
          'planConfirmation': {
            'planItemId': 'plan-error',
            'turnId': 'turn-error',
            'revision': 4,
          },
        },
      };
    }, events: events.stream);

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
          home: ConversationScreen(conversationId: 'thread-plan-error'),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const ValueKey('plan-revision-input')),
      '保留这段修改意见',
    );
    await tester.pump();
    await tester.tap(find.byKey(const ValueKey('submit-plan-revision')));
    await tester.pumpAndSettle();

    expect(
      find.byKey(const ValueKey('plan-confirmation-panel')),
      findsOneWidget,
    );
    expect(find.text('保留这段修改意见'), findsOneWidget);
    expect(find.textContaining('计划响应失败'), findsOneWidget);
  });

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

  testWidgets('command activity is localized and expands its detail', (
    tester,
  ) async {
    final gateway = _FakeGateway((method, params) async {
      if (method == 'action.list' || method == 'conversation.subscribe') {
        return {'result': const {}};
      }
      return {
        'result': {
          'conversation': {
            'id': 'thread-command',
            'agentId': 'agent-1',
            'projectId': 'project-1',
            'title': 'Command thread',
            'running': false,
          },
          'timeline': {
            'items': [
              {
                'id': 'command-1',
                'kind': 'commandExecution',
                'status': 'completed',
                'blocks': [
                  {'kind': 'text', 'text': 'echo hello', 'truncated': false},
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
          home: ConversationScreen(conversationId: 'thread-command'),
        ),
      ),
    );
    await tester.pump(const Duration(milliseconds: 200));

    final appBar = tester.widget<AppBar>(find.byType(AppBar));
    final title = tester.widget<Text>(find.text('Command thread'));
    expect(appBar.toolbarHeight, 60);
    expect(title.style?.fontSize, 15);
    expect(title.style?.fontWeight, FontWeight.w400);
    expect(find.text('Project Alpha · Codex'), findsOneWidget);
    expect(find.text('命令执行'), findsOneWidget);
    expect(find.text('commandExecution'), findsNothing);
    expect(find.text('echo hello'), findsNothing);
    expect(find.byIcon(Icons.keyboard_arrow_right_rounded), findsOneWidget);
    expect(find.byIcon(Icons.keyboard_arrow_down_rounded), findsNothing);
    final activityTitle = tester.widget<Text>(find.text('命令执行'));
    expect(activityTitle.style?.fontSize, 14);
    expect(activityTitle.style?.height, 1.15);
    final activityRow = tester.widget<ConstrainedBox>(
      find.ancestor(
        of: find.text('命令执行'),
        matching: find.byType(ConstrainedBox),
      ),
    );
    expect(activityRow.constraints.minHeight, 32);
    await tester.tap(find.text('命令执行'));
    await tester.pumpAndSettle();
    expect(find.text('echo hello'), findsOneWidget);
    expect(find.byIcon(Icons.keyboard_arrow_right_rounded), findsNothing);
    expect(find.byIcon(Icons.keyboard_arrow_down_rounded), findsOneWidget);
    final commandDetail = tester.widget<SelectableText>(
      find.widgetWithText(SelectableText, 'echo hello'),
    );
    expect(commandDetail.style?.fontSize, 13);
    expect(commandDetail.style?.height, 1.25);
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

    await tester.tap(find.byIcon(Icons.more_vert_rounded));
    await tester.pumpAndSettle();
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

    await tester.tap(find.byIcon(Icons.more_vert_rounded));
    await tester.pumpAndSettle();
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
    expect(tester.widget<TextField>(find.byType(TextField)).enabled, isFalse);
    expect(find.text('任务运行中 · 停止'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets(
    'draft sends selected plan model effort and full access settings',
    (tester) async {
      SharedPreferences.setMockInitialValues({});
      Map<String, dynamic>? turnParams;
      final gateway = _FakeGateway((method, params) async {
        switch (method) {
          case 'conversation.create':
            return {
              'result': {'id': 'thread-settings'},
            };
          case 'turn.start':
            turnParams = Map<String, dynamic>.from(params);
            return {'result': const {}};
          case 'conversation.read':
            return {
              'result': {
                'conversation': {
                  'id': 'thread-settings',
                  'title': 'Settings',
                  'running': true,
                },
                'settings': turnParams?['settings'],
                'timeline': {'items': const []},
              },
            };
          case 'action.list':
            return {'result': const <dynamic>[]};
          default:
            return {'result': const {}};
        }
      }, catalogs: _settingsCatalogs);

      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            coreGatewayProvider.overrideWithValue(gateway),
            workspaceProvider.overrideWith(_FakeWorkspaceController.new),
          ],
          child: MaterialApp(
            theme: buildLightTheme(),
            locale: const Locale('zh', 'CN'),
            localizationsDelegates: AppLocalizations.localizationsDelegates,
            supportedLocales: AppLocalizations.supportedLocales,
            home: const ConversationScreen(
              conversationId: null,
              projectId: 'project-1',
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();

      await tester.tap(find.byTooltip('添加'));
      await tester.pumpAndSettle();
      expect(
        tester.widget<BottomSheet>(find.byType(BottomSheet)).backgroundColor,
        Colors.white,
      );
      await tester.tap(find.text('计划模式'));
      await tester.pumpAndSettle();

      await tester.tap(find.byTooltip('权限'));
      await tester.pumpAndSettle();
      expect(
        tester.widget<BottomSheet>(find.byType(BottomSheet)).backgroundColor,
        Colors.white,
      );
      expect(find.text('默认权限'), findsOneWidget);
      expect(find.text('自动审核'), findsOneWidget);
      expect(find.text('只读'), findsOneWidget);
      expect(find.text('完全访问权限'), findsOneWidget);
      await tester.tap(find.text('完全访问权限'));
      await tester.pumpAndSettle();
      expect(find.text('允许完全访问计算机？'), findsOneWidget);
      await tester.tap(find.text('允许完全访问'));
      await tester.pumpAndSettle();

      await tester.tap(find.textContaining('GPT A'));
      await tester.pumpAndSettle();
      expect(
        tester.widget<BottomSheet>(find.byType(BottomSheet)).backgroundColor,
        Colors.white,
      );
      await tester.tap(find.text('模型'));
      await tester.pumpAndSettle();
      expect(
        tester.widget<BottomSheet>(find.byType(BottomSheet)).backgroundColor,
        Colors.white,
      );
      await tester.tap(find.text('GPT B'));
      await tester.pumpAndSettle();

      await tester.enterText(find.byType(TextField), 'Use selected settings');
      await tester.pump();
      await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
      await tester.runAsync(() async {
        for (var index = 0; index < 50 && turnParams == null; index++) {
          await Future<void>.delayed(const Duration(milliseconds: 10));
        }
      });
      await tester.pump();

      final settings = turnParams?['settings'] as Map<String, dynamic>?;
      expect(settings, {
        'mode': 'plan',
        'modelId': 'model-b',
        'reasoningEffort': 'medium',
        'permissionModeId': 'fullAccess',
      });
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets('existing conversation persists plan mode before updating UI', (
    tester,
  ) async {
    SharedPreferences.setMockInitialValues({});
    Map<String, dynamic> settings = {
      'mode': 'default',
      'modelId': 'model-a',
      'reasoningEffort': 'high',
      'permissionModeId': 'default',
    };
    Map<String, dynamic>? updateParams;
    final events = StreamController<Map<String, dynamic>>.broadcast();
    addTearDown(events.close);
    final gateway = _FakeGateway(
      (method, params) async {
        switch (method) {
          case 'conversation.read':
            return {
              'result': {
                'conversation': {
                  'id': 'thread-existing-settings',
                  'title': 'Settings',
                  'running': false,
                },
                'settings': settings,
                'revision': 4,
                'timeline': {'items': const []},
              },
            };
          case 'conversation.settings.update':
            updateParams = Map<String, dynamic>.from(params);
            settings = Map<String, dynamic>.from(
              params['settings'] as Map<String, dynamic>,
            );
            return {'result': settings};
          case 'action.list':
            return {'result': const <dynamic>[]};
          case 'conversation.subscribe':
            return {'result': const {}};
          default:
            return {'result': const {}};
        }
      },
      catalogs: _settingsCatalogs,
      events: events.stream,
    );

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
          home: ConversationScreen(conversationId: 'thread-existing-settings'),
        ),
      ),
    );
    for (var index = 0; index < 8; index++) {
      await tester.pump(const Duration(milliseconds: 50));
    }

    await tester.tap(find.byTooltip('添加'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('计划模式'));
    await tester.runAsync(() async {
      for (var index = 0; index < 50 && updateParams == null; index++) {
        await Future<void>.delayed(const Duration(milliseconds: 10));
      }
    });
    await tester.pump();

    expect(updateParams?['threadId'], 'thread-existing-settings');
    expect(updateParams?['expectedRevision'], 4);
    expect(
      (updateParams?['settings'] as Map<String, dynamic>?)?['mode'],
      'plan',
    );
    expect(find.textContaining('计划 · GPT A'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('image selections append, deduplicate, remove, and scroll', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(360, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));
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

    await tester.tap(find.byTooltip('添加'));
    await _finishFileIo(tester);
    await tester.tap(find.byTooltip('添加'));
    await _finishFileIo(tester);

    expect(picker.requestedLimits, [10, 8]);
    final strip = find.byKey(const ValueKey('attachment-thumbnail-strip'));
    expect(strip, findsOneWidget);
    expect(
      find.descendant(of: strip, matching: find.byType(Image)),
      findsAtLeastNWidgets(4),
    );
    await tester.tap(find.byTooltip('添加'));
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

    scrollable.position.jumpTo(scrollable.position.maxScrollExtent);
    await tester.pump();
    expect(
      find.byKey(ValueKey('attachment-thumbnail-${images.last.path}')),
      findsOneWidget,
    );

    await tester.tap(
      find.byKey(ValueKey('remove-attachment-${images.last.path}')),
    );
    await tester.pump();
    expect(
      find.byKey(ValueKey('attachment-thumbnail-${images.last.path}')),
      findsNothing,
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

    await tester.tap(find.byTooltip('添加'));
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

    await tester.tap(find.byTooltip('添加'));
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
    expect(turnParams?['expectedRevision'], 0);
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

    await tester.tap(find.byTooltip('添加'));
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
