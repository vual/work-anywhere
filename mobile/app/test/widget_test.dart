import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:work_anywhere/core/models.dart';
import 'package:work_anywhere/features/pairing/pairing_screen.dart';
import 'package:work_anywhere/features/workspace/workspace_controller.dart';
import 'package:work_anywhere/features/workspace/workspace_screen.dart';
import 'package:work_anywhere/l10n/generated/app_localizations.dart';

void main() {
  testWidgets('pairing screen offers scan and paste paths', (tester) async {
    await tester.pumpWidget(
      const ProviderScope(
        child: MaterialApp(
          locale: Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: PairingScreen(),
        ),
      ),
    );

    expect(find.text('扫描二维码'), findsOneWidget);
    expect(find.text('粘贴 waw:// 配对链接'), findsOneWidget);
    expect(find.byIcon(Icons.qr_code_scanner_rounded), findsOneWidget);
  });

  testWidgets('pairing screen follows the English locale', (tester) async {
    await tester.pumpWidget(
      const ProviderScope(
        child: MaterialApp(
          locale: Locale('en', 'US'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: PairingScreen(),
        ),
      ),
    );

    expect(find.text('Scan QR code'), findsOneWidget);
    expect(find.text('Paste a waw:// pairing link'), findsOneWidget);
  });

  testWidgets('workspace top bar fits a narrow phone viewport', (tester) async {
    await tester.binding.setSurfaceSize(const Size(360, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    const data = WorkspaceState(
      needsPairing: false,
      daemons: [
        DaemonSummary(
          id: 'daemon-1',
          name: 'My PC',
          phase: ConnectionPhase.online,
        ),
      ],
      selectedDaemonId: 'daemon-1',
    );

    await tester.pumpWidget(
      const ProviderScope(
        child: MaterialApp(
          locale: Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: WorkspaceScreen(data: data),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    expect(find.text('Work Any Where'), findsNothing);
  });

  testWidgets('workspace keeps the running hint visible and daemon at right', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(360, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    const data = WorkspaceState(
      needsPairing: false,
      daemons: [
        DaemonSummary(
          id: 'daemon-1',
          name: 'My PC',
          phase: ConnectionPhase.online,
        ),
      ],
      conversations: [
        ConversationSummary(
          id: 'thread-1',
          agentId: 'agent-1',
          projectId: 'project-1',
          title: 'Running task',
          status: 'running',
          running: true,
          updatedAt: 0,
        ),
      ],
      selectedDaemonId: 'daemon-1',
    );

    await tester.pumpWidget(
      const ProviderScope(
        child: MaterialApp(
          locale: Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: WorkspaceScreen(data: data),
        ),
      ),
    );
    await tester.pump();

    expect(tester.takeException(), isNull);
    expect(find.text('1 个运行中'), findsOneWidget);

    final daemonRect = tester.getRect(
      find.byKey(const Key('workspace-daemon-selector')),
    );
    final runningTextRect = tester.getRect(find.text('1 个运行中'));
    expect(daemonRect.right, closeTo(336, .01));
    expect(runningTextRect.right, lessThanOrEqualTo(daemonRect.left));
  });

  testWidgets('workspace uses compact rows and supports 200 percent text', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(360, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    const data = WorkspaceState(
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
          name: 'Project A',
          updatedAt: 0,
        ),
      ],
      conversations: [
        ConversationSummary(
          id: 'thread-1',
          agentId: 'agent-1',
          projectId: 'project-1',
          title: 'Conversation A',
          status: 'idle',
          running: false,
          updatedAt: 0,
        ),
      ],
      selectedDaemonId: 'daemon-1',
      selectedAgentId: 'agent-1',
    );

    await tester.pumpWidget(
      const ProviderScope(
        child: MaterialApp(
          locale: Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: MediaQuery(
            data: MediaQueryData(textScaler: TextScaler.linear(2)),
            child: WorkspaceScreen(data: data),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    final compactRows = tester
        .widgetList<ListTile>(find.byType(ListTile))
        .where((tile) => tile.minTileHeight == 66);
    expect(compactRows, hasLength(2));
    expect(tester.widget<Text>(find.text('Project A')).style?.fontSize, 17);
    expect(tester.widget<Text>(find.text('项目')).style?.fontSize, 18);
    expect(find.text('最近 10 个'), findsNothing);
    expect(find.text('最近 20 个'), findsNothing);
  });
}
