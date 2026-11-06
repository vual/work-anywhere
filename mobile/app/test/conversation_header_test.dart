import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:work_anywhere/app/chrome.dart';
import 'package:work_anywhere/core/core_gateway.dart';
import 'package:work_anywhere/core/models.dart';
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
  }) {
    if (method.startsWith('catalog.')) {
      return Future.error(CoreFault('catalog_unavailable', 'not configured'));
    }
    return handler(method, params);
  }

  @override
  Stream<Map<String, dynamic>> get events => const Stream.empty();
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

Widget _app(CoreGateway gateway, Widget home) => ProviderScope(
  overrides: [
    coreGatewayProvider.overrideWithValue(gateway),
    workspaceProvider.overrideWith(_FakeWorkspaceController.new),
  ],
  child: MaterialApp(
    locale: const Locale('zh', 'CN'),
    localizationsDelegates: AppLocalizations.localizationsDelegates,
    supportedLocales: AppLocalizations.supportedLocales,
    home: home,
  ),
);

Future<void> _settle(WidgetTester tester) async {
  for (var index = 0; index < 10; index++) {
    await tester.pump(const Duration(milliseconds: 50));
  }
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('a draft conversation hides the action pill', (tester) async {
    final gateway = _FakeGateway(
      (method, params) async => {'result': <String, dynamic>{}},
    );
    await tester.pumpWidget(
      _app(
        gateway,
        const ConversationScreen(
          conversationId: null,
          projectId: 'project-1',
          projectName: 'Project Alpha',
        ),
      ),
    );
    await _settle(tester);

    expect(find.byKey(const Key('conversation-title-pill')), findsOneWidget);
    expect(find.byKey(const Key('conversation-rename')), findsNothing);
    expect(find.byIcon(Icons.more_vert_rounded), findsNothing);
    _expectTitlePillReachesMenuSlot(tester);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('menu shows the full title, copies the id and renames', (
    tester,
  ) async {
    const longTitle =
        'A very long conversation title that cannot fit in the header pill';
    var title = longTitle;
    final renames = <Map<String, dynamic>>[];
    final gateway = _FakeGateway((method, params) async {
      switch (method) {
        case 'action.list':
          return {'result': <dynamic>[]};
        case 'conversation.subscribe':
          return {'result': const <String, dynamic>{}};
        case 'conversation.rename':
          renames.add(params);
          title = params['name'] as String;
          return {
            'result': {'threadId': params['threadId'], 'name': title},
          };
        case 'conversation.read':
          return {
            'result': {
              'conversation': {
                'id': 'thread-1',
                'projectId': 'project-1',
                'title': title,
                'running': false,
              },
              'sequence': 1,
              'timeline': {'items': <dynamic>[]},
            },
          };
      }
      return {'result': <String, dynamic>{}};
    });
    String? copied;
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      (call) async {
        if (call.method == 'Clipboard.setData') {
          copied = (call.arguments as Map)['text'] as String?;
        }
        return null;
      },
    );
    addTearDown(
      () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform,
        null,
      ),
    );

    await tester.pumpWidget(
      _app(gateway, const ConversationScreen(conversationId: 'thread-1')),
    );
    await _settle(tester);

    _expectTitlePillReachesMenuSlot(tester);
    await tester.tap(find.byIcon(Icons.more_vert_rounded));
    await tester.pumpAndSettle();
    expect(
      find.descendant(
        of: find.byKey(const Key('conversation-full-title')),
        matching: find.text(longTitle),
      ),
      findsOneWidget,
    );
    await tester.tap(find.text('复制对话串ID'));
    await tester.pumpAndSettle();
    expect(copied, 'thread-1');
    expect(find.text('已复制对话串ID'), findsOneWidget);

    await tester.tap(find.byIcon(Icons.more_vert_rounded));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('conversation-rename')));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const Key('conversation-rename-field')),
      '  Renamed topic  ',
    );
    await tester.tap(find.text('保存'));
    await _settle(tester);

    expect(renames, hasLength(1));
    expect(renames.single['agentId'], 'agent-1');
    expect(renames.single['threadId'], 'thread-1');
    expect(renames.single['name'], 'Renamed topic');
    expect(find.text('Renamed topic'), findsWidgets);
    await tester.pumpWidget(const SizedBox());
  });
}

/// Draft and existing conversations must give the title pill the same width,
/// stopping before the round menu slot at the right edge.
void _expectTitlePillReachesMenuSlot(WidgetTester tester) {
  final screenWidth =
      tester.view.physicalSize.width / tester.view.devicePixelRatio;
  final pill = tester.getRect(find.byKey(const Key('conversation-title-pill')));
  expect(pill.right, closeTo(screenWidth - WawRoundAction.slotWidth - 4, 0.5));
}
