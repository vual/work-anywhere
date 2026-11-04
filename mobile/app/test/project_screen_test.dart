import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:work_anywhere/core/core_gateway.dart';
import 'package:work_anywhere/core/models.dart';
import 'package:work_anywhere/features/project/project_screen.dart';
import 'package:work_anywhere/features/workspace/workspace_controller.dart';
import 'package:work_anywhere/l10n/generated/app_localizations.dart';

class _FakeGateway extends CoreGateway {
  @override
  Future<Map<String, dynamic>> call(
    String method,
    Map<String, dynamic> params, {
    int timeoutMilliseconds = 30000,
  }) async {
    return {
      'result': {'items': <dynamic>[]},
    };
  }

  @override
  Stream<Map<String, dynamic>> get events => const Stream.empty();
}

class _FakeWorkspaceController extends WorkspaceController {
  @override
  Future<WorkspaceState> build() async => const WorkspaceState(
    needsPairing: false,
    daemons: [
      DaemonSummary(
        id: 'daemon-1',
        name: 'My PC',
        phase: ConnectionPhase.online,
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
    selectedDaemonId: 'daemon-1',
    selectedAgentId: 'agent-1',
  );
}

void main() {
  testWidgets('project new conversation action is a circular icon button', (
    tester,
  ) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          coreGatewayProvider.overrideWithValue(_FakeGateway()),
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: MaterialApp(
          locale: const Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: const ProjectScreen(projectId: 'project-1'),
        ),
      ),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));

    final button = tester.widget<FloatingActionButton>(
      find.byType(FloatingActionButton),
    );
    expect(button.isExtended, isFalse);
    expect(button.shape, isA<CircleBorder>());
    expect(button.tooltip, '新建对话');
    expect(find.byIcon(Icons.edit_outlined), findsOneWidget);
    expect(find.text('新建对话'), findsNothing);
    final back = find.widgetWithIcon(IconButton, Icons.arrow_back_rounded);
    expect(tester.getSize(back), const Size(48, 48));
    expect(tester.getTopLeft(back), const Offset(12, 12));
    expect((tester.widget<IconButton>(back).icon as Icon).size, 24);
  });

  testWidgets(
    'project header pill shows computer and agent; list is cached until pulled',
    (tester) async {
      final gateway = _CountingGateway();
      final visible = ValueNotifier(true);
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            coreGatewayProvider.overrideWithValue(gateway),
            workspaceProvider.overrideWith(_AgentWorkspaceController.new),
          ],
          child: MaterialApp(
            locale: const Locale('zh', 'CN'),
            localizationsDelegates: AppLocalizations.localizationsDelegates,
            supportedLocales: AppLocalizations.supportedLocales,
            home: ValueListenableBuilder<bool>(
              valueListenable: visible,
              builder: (context, show, _) => show
                  ? const ProjectScreen(projectId: 'project-1')
                  : const SizedBox.shrink(),
            ),
          ),
        ),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 100));

      expect(find.text('My PC · Codex'), findsOneWidget);
      expect(find.text('Cached thread'), findsOneWidget);
      expect(gateway.listCalls, 1);

      visible.value = false;
      await tester.pump();
      visible.value = true;
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 100));
      expect(find.text('Cached thread'), findsOneWidget);
      expect(gateway.listCalls, 1);

      await tester.fling(
        find.text('Cached thread'),
        const Offset(0, 400),
        1000,
      );
      await tester.pumpAndSettle();
      expect(gateway.listCalls, 2);
    },
  );
}

class _CountingGateway extends CoreGateway {
  int listCalls = 0;

  @override
  Future<Map<String, dynamic>> call(
    String method,
    Map<String, dynamic> params, {
    int timeoutMilliseconds = 30000,
  }) async {
    if (method == 'conversation.list') {
      listCalls++;
      return {
        'result': {
          'items': [
            {
              'id': 'thread-1',
              'agentId': 'agent-1',
              'projectId': 'project-1',
              'title': 'Cached thread',
              'status': 'idle',
              'updatedAt': 0,
            },
          ],
        },
      };
    }
    return {
      'result': {'items': <dynamic>[]},
    };
  }

  @override
  Stream<Map<String, dynamic>> get events => const Stream.empty();
}

class _AgentWorkspaceController extends WorkspaceController {
  @override
  Future<WorkspaceState> build() async => const WorkspaceState(
    needsPairing: false,
    daemons: [
      DaemonSummary(
        id: 'daemon-1',
        name: 'My PC',
        phase: ConnectionPhase.online,
      ),
    ],
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
    selectedDaemonId: 'daemon-1',
    selectedAgentId: 'agent-1',
  );
}
