import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:work_anywhere/app/theme.dart';
import 'package:work_anywhere/core/models.dart';
import 'package:work_anywhere/features/pairing/pairing_screen.dart';
import 'package:work_anywhere/features/workspace/workspace_controller.dart';
import 'package:work_anywhere/features/workspace/workspace_screen.dart';
import 'package:work_anywhere/l10n/generated/app_localizations.dart';

void main() {
  testWidgets(
    'workspace search stays above the keyboard and returns on dismissal',
    (tester) async {
      tester.view.physicalSize = const Size(360, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.view.resetViewInsets);
      await tester.pumpWidget(
        ProviderScope(
          child: MaterialApp(
            localizationsDelegates: AppLocalizations.localizationsDelegates,
            supportedLocales: AppLocalizations.supportedLocales,
            theme: buildLightTheme(),
            home: const WorkspaceScreen(
              data: WorkspaceState(needsPairing: false),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      final search = find.byType(TextField);
      final initial = tester.getRect(search);
      await tester.tap(search);
      tester.view.viewInsets = const FakeViewPadding(bottom: 300);
      await tester.pumpAndSettle();
      expect(tester.getRect(search).bottom, lessThanOrEqualTo(500));
      expect(tester.getRect(search).top, closeTo(initial.top - 300, 1));
      expect(tester.takeException(), isNull);
      tester.view.resetViewInsets();
      await tester.pumpAndSettle();
      expect(tester.getRect(search).top, closeTo(initial.top, 1));
    },
  );

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

  testWidgets('daemon selector opens an anchored menu below the selector', (
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
          phase: ConnectionPhase.offline,
        ),
      ],
      selectedDaemonId: 'daemon-1',
    );

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          locale: const Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          theme: buildLightTheme(),
          home: const WorkspaceScreen(data: data),
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('workspace-daemon-selector')));
    await tester.pumpAndSettle();

    expect(find.byType(MenuItemButton), findsNWidgets(3));
    final selector = tester.getRect(
      find.byKey(const Key('workspace-daemon-selector')),
    );
    final menu = tester.getRect(find.byType(MenuItemButton).first);
    expect(menu.top, greaterThanOrEqualTo(selector.bottom));
    expect(tester.takeException(), isNull);
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
    expect(find.text('在线 · 安全连接'), findsNothing);

    final daemonRect = tester.getRect(
      find.byKey(const Key('workspace-daemon-selector')),
    );
    final runningTextRect = tester.getRect(find.text('1 个运行中'));
    expect(daemonRect.right, closeTo(340, .01));
    expect(runningTextRect.right, lessThanOrEqualTo(daemonRect.left));
  });

  testWidgets('workspace scroll keeps daemon and agent rows fixed', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(360, 640));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    final data = WorkspaceState(
      needsPairing: false,
      daemons: const [
        DaemonSummary(
          id: 'daemon-1',
          name: 'My PC',
          phase: ConnectionPhase.online,
        ),
      ],
      agents: const [
        AgentSummary(
          id: 'agent-1',
          name: 'Codex',
          kind: 'codex',
          status: 'ready',
        ),
      ],
      projects: List.generate(
        20,
        (index) => ProjectSummary(
          id: 'project-$index',
          agentId: 'agent-1',
          name: 'Project $index',
          updatedAt: index,
        ),
      ),
      selectedDaemonId: 'daemon-1',
      selectedAgentId: 'agent-1',
    );

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          locale: const Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: WorkspaceScreen(data: data),
        ),
      ),
    );
    await tester.pumpAndSettle();

    final topBar = find.byKey(const Key('workspace-top-bar'));
    final agentRail = find.byKey(const Key('workspace-agent-rail'));
    final contentScroll = find.byKey(const Key('workspace-content-scroll'));
    final topBarBefore = tester.getRect(topBar);
    final agentRailBefore = tester.getRect(agentRail);

    await tester.drag(contentScroll, const Offset(0, -420));
    await tester.pumpAndSettle();

    expect(tester.getRect(topBar), topBarBefore);
    expect(tester.getRect(agentRail), agentRailBefore);
    final scrollable = find.descendant(
      of: contentScroll,
      matching: find.byType(Scrollable),
    );
    expect(
      tester.state<ScrollableState>(scrollable).position.pixels,
      greaterThan(0),
    );
  });

  testWidgets('conversation needing attention shows an orange alert icon', (
    tester,
  ) async {
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
          title: 'Waiting for an answer',
          status: 'waiting_action',
          running: true,
          needsAttention: true,
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

    expect(find.text('1 个待处理'), findsOneWidget);
    final icons = tester.widgetList<Icon>(
      find.byIcon(Icons.error_outline_rounded),
    );
    expect(icons, hasLength(2));
    expect(icons.every((icon) => icon.color == wawAttention), isTrue);
    expect(find.byType(CircularProgressIndicator), findsNothing);
  });

  testWidgets('workspace uses 54dp rows and supports 200 percent text', (
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
        .where((tile) => tile.minTileHeight == 54);
    expect(compactRows, hasLength(2));
    expect(tester.widget<Text>(find.text('Project A')).style?.fontSize, 17);
    expect(tester.widget<Text>(find.text('项目')).style?.fontSize, 18);
    expect(find.text('最近 10 个'), findsNothing);
    expect(find.text('最近 20 个'), findsNothing);
  });

  testWidgets('workspace explains when every computer is disabled', (
    tester,
  ) async {
    const data = WorkspaceState(
      needsPairing: false,
      daemons: [
        DaemonSummary(
          id: 'daemon-1',
          name: 'Offline PC',
          phase: ConnectionPhase.disabled,
          enabled: false,
        ),
      ],
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
    expect(find.text('没有启用的电脑'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
