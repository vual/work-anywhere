import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:work_anywhere/features/about/about_screen.dart';
import 'package:work_anywhere/features/update/update_controller.dart';
import 'package:work_anywhere/features/workspace/workspace_controller.dart';
import 'package:work_anywhere/features/workspace/workspace_screen.dart';
import 'package:work_anywhere/l10n/generated/app_localizations.dart';

import 'support/fake_update_gateway.dart';

void main() {
  late FakeUpdateGateway gateway;

  setUp(() {
    SharedPreferences.setMockInitialValues({});
    gateway = FakeUpdateGateway();
  });

  Future<ProviderContainer> pumpApp(
    WidgetTester tester,
    String initialLocation,
  ) async {
    final router = GoRouter(
      initialLocation: initialLocation,
      routes: [
        GoRoute(
          path: '/',
          builder: (context, state) =>
              const WorkspaceScreen(data: WorkspaceState(needsPairing: false)),
        ),
        GoRoute(
          path: '/about',
          builder: (context, state) => AboutScreen(
            autoCheck: state.uri.queryParameters['check'] == '1',
          ),
        ),
        GoRoute(
          path: '/computers',
          builder: (context, state) =>
              const Scaffold(body: Text('computers-page')),
        ),
      ],
    );
    addTearDown(router.dispose);
    final container = ProviderContainer(
      overrides: [coreGatewayProvider.overrideWithValue(gateway)],
    );
    addTearDown(container.dispose);
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: MaterialApp.router(
          locale: const Locale('zh'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          routerConfig: router,
        ),
      ),
    );
    await tester.pumpAndSettle();
    return container;
  }

  int checks() => gateway.calls.where((call) => call == 'checkUpdate').length;

  testWidgets('about?check=1 checks exactly once', (tester) async {
    await pumpApp(tester, '/about?check=1');
    expect(checks(), 1);
    expect(find.byKey(const Key('update-release-card')), findsOneWidget);
    expect(find.text('发现新版本 1.2.0'), findsOneWidget);
    expect(find.text('版本 1.1.0 (10)'), findsOneWidget);

    // Rotation and rebuilds must not trigger another check.
    tester.view.physicalSize = const Size(1600, 900);
    addTearDown(tester.view.resetPhysicalSize);
    await tester.pumpAndSettle();
    expect(checks(), 1);
  });

  testWidgets('plain about page does not check', (tester) async {
    await pumpApp(tester, '/about');
    expect(checks(), 0);
    expect(find.text('版本 1.1.0 (10)'), findsOneWidget);
    await tester.tap(find.byKey(const Key('update-check-button')));
    await tester.pumpAndSettle();
    expect(checks(), 1);
    expect(find.byKey(const Key('update-now-button')), findsOneWidget);
  });

  testWidgets('debug builds disable the check button', (tester) async {
    gateway.updatable = false;
    await pumpApp(tester, '/about?check=1');
    expect(checks(), 0);
    expect(find.byKey(const Key('update-unsupported')), findsOneWidget);
    final button = tester.widget<FilledButton>(
      find.byKey(const Key('update-check-button')),
    );
    expect(button.onPressed, isNull);
  });

  testWidgets('logo opens the sidebar and check updates auto-checks', (
    tester,
  ) async {
    await pumpApp(tester, '/');
    await tester.tap(find.byKey(const Key('workspace-menu-button')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('app-drawer')), findsOneWidget);
    expect(find.byKey(const Key('drawer-manage-computers')), findsOneWidget);
    expect(find.byKey(const Key('drawer-about')), findsOneWidget);

    await tester.tap(find.byKey(const Key('drawer-check-updates')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('app-drawer')), findsNothing);
    expect(find.byType(AboutScreen), findsOneWidget);
    expect(checks(), 1);
  });

  testWidgets('sidebar manage computers opens the computer list', (
    tester,
  ) async {
    await pumpApp(tester, '/');
    await tester.tap(find.byKey(const Key('workspace-menu-button')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('drawer-manage-computers')));
    await tester.pumpAndSettle();
    expect(find.text('computers-page'), findsOneWidget);
  });

  testWidgets('new version shows the badge until skipped', (tester) async {
    final container = await pumpApp(tester, '/');
    Badge badge() =>
        tester.widget<Badge>(find.byKey(const Key('workspace-update-badge')));
    expect(badge().isLabelVisible, isFalse);

    await container.read(updateProvider.notifier).check();
    await tester.pumpAndSettle();
    expect(badge().isLabelVisible, isTrue);

    await tester.tap(find.byKey(const Key('workspace-menu-button')));
    await tester.pumpAndSettle();
    expect(find.text('有新版本 1.2.0'), findsOneWidget);
    Navigator.of(tester.element(find.byKey(const Key('app-drawer')))).pop();
    await tester.pumpAndSettle();

    await container.read(updateProvider.notifier).skip();
    await tester.pumpAndSettle();
    expect(badge().isLabelVisible, isFalse);
  });
}
