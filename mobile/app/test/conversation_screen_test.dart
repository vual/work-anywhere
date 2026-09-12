import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
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
}

class _FakeWorkspaceController extends WorkspaceController {
  @override
  Future<WorkspaceState> build() async =>
      const WorkspaceState(needsPairing: false, selectedAgentId: 'agent-1');
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
}
