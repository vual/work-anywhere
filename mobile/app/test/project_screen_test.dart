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
    expect(button.tooltip, '新建对话');
    expect(find.byIcon(Icons.edit_outlined), findsOneWidget);
    expect(find.text('新建对话'), findsNothing);
  });
}
