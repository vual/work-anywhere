import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
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

class _ClaudeWorkspace extends WorkspaceController {
  _ClaudeWorkspace(this.capabilities);

  final AgentCapabilities capabilities;

  @override
  Future<WorkspaceState> build() async => WorkspaceState(
    needsPairing: false,
    agents: [
      AgentSummary(
        id: 'claude',
        name: 'Claude Code',
        kind: 'claude-code',
        status: 'ready',
        capabilities: capabilities,
      ),
    ],
    selectedAgentId: 'claude',
    selectedDaemonId: 'daemon-1',
  );
}

Future<void> _openConversation(
  WidgetTester tester, {
  required Map<String, dynamic> conversation,
  List<Map<String, dynamic>> actions = const [],
  AgentCapabilities capabilities = AgentCapabilities.all,
}) async {
  SharedPreferences.setMockInitialValues({});
  final gateway = _FakeGateway((method, params) async {
    if (method == 'conversation.subscribe') return {'result': const {}};
    if (method == 'action.list') return {'result': actions};
    return {
      'result': {
        'conversation': conversation,
        'revision': 1,
        'timeline': {'items': const []},
      },
    };
  });
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        coreGatewayProvider.overrideWithValue(gateway),
        workspaceProvider.overrideWith(() => _ClaudeWorkspace(capabilities)),
      ],
      child: MaterialApp(
        locale: const Locale('en'),
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        home: ConversationScreen(
          conversationId: conversation['id'] as String,
          projectId: 'project-1',
        ),
      ),
    ),
  );
  for (var n = 0; n < 6; n++) {
    await tester.pump(const Duration(milliseconds: 100));
  }
}

void main() {
  test('agents without capabilities keep full support and kind names', () {
    final codex = AgentSummary.fromJson({'id': 'a', 'kind': 'codex'});
    expect(codex.name, 'Codex');
    expect(codex.capabilities.queue, isTrue);
    expect(codex.capabilities.textInput, isTrue);

    final claude = AgentSummary.fromJson({
      'id': 'b',
      'kind': 'claude-code',
      'name': '',
      'capabilities': {'history': true, 'textInput': true, 'approval': true},
    });
    expect(claude.name, 'Claude Code');
    expect(claude.capabilities.textInput, isTrue);
    expect(claude.capabilities.queue, isFalse);
    expect(claude.capabilities.createConversation, isFalse);

    expect(AgentSummary.fromJson({'id': 'c'}).name, 'Agent');
  });

  test('conversation summaries carry the computer ownership flag', () {
    final summary = ConversationSummary.fromJson({
      'id': 'c1',
      'agentId': 'claude',
      'externallyActive': true,
    });
    expect(summary.externallyActive, isTrue);
    expect(
      ConversationSummary.fromJson({'id': 'c2', 'agentId': 'claude'})
          .externallyActive,
      isFalse,
    );
  });

  testWidgets('conversation open on the computer replaces the composer', (
    tester,
  ) async {
    await _openConversation(
      tester,
      conversation: {
        'id': 'c1',
        'title': 'Busy elsewhere',
        'running': false,
        'externallyActive': true,
      },
    );
    expect(
      find.byKey(const ValueKey('read-only-composer-notice')),
      findsOneWidget,
    );
    expect(find.textContaining('open on the computer'), findsOneWidget);
    expect(find.byType(TextField), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('agent without text input cannot send', (tester) async {
    await _openConversation(
      tester,
      conversation: {'id': 'c2', 'title': 'History only', 'running': false},
      capabilities: const AgentCapabilities(textInput: false),
    );
    expect(
      find.text(
        "Claude Code can't receive messages from your phone right now.",
      ),
      findsOneWidget,
    );
    expect(find.byType(TextField), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('approval shows the requested command and description', (
    tester,
  ) async {
    await _openConversation(
      tester,
      conversation: {'id': 'c3', 'title': 'Needs approval', 'running': true},
      actions: [
        {
          'id': 'cc-1',
          'kind': 'approval',
          'method': 'claude/canUseTool/Bash',
          'conversationId': 'c3',
          'detail': {
            'toolName': 'Bash',
            'description': 'Run the test suite',
            'command': 'go test ./...',
          },
        },
      ],
    );
    expect(find.text('Run the test suite'), findsOneWidget);
    expect(find.byKey(const ValueKey('action-detail-command')), findsOneWidget);
    expect(find.text('go test ./...'), findsOneWidget);
    expect(find.text('claude/canUseTool/Bash'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });
}
