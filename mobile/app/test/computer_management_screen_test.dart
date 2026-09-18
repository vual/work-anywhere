import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:work_anywhere/app/theme.dart';
import 'package:work_anywhere/core/models.dart';
import 'package:work_anywhere/features/workspace/computer_management_screen.dart';
import 'package:work_anywhere/features/workspace/workspace_controller.dart';
import 'package:work_anywhere/l10n/generated/app_localizations.dart';

void main() {
  testWidgets('computer icon is centered against both information lines', (
    tester,
  ) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          workspaceProvider.overrideWith(_FakeWorkspaceController.new),
        ],
        child: MaterialApp(
          locale: const Locale('zh', 'CN'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          theme: buildLightTheme(),
          home: const ComputerManagementScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    final icon = tester.getRect(find.byIcon(Icons.laptop_mac_outlined));
    final name = tester.getRect(find.text('LAPTOP'));
    final status = tester.getRect(
      find.text('在线 · 安全连接 · https://relay.example.com'),
    );
    final informationCenter = (name.top + status.bottom) / 2;

    expect(icon.center.dy, closeTo(informationCenter, 0.01));
    expect(tester.takeException(), isNull);
  });
}

class _FakeWorkspaceController extends WorkspaceController {
  @override
  Future<WorkspaceState> build() async => const WorkspaceState(
    needsPairing: false,
    daemons: [
      DaemonSummary(
        id: 'daemon-1',
        name: 'LAPTOP',
        phase: ConnectionPhase.online,
        relayUrl: 'https://relay.example.com',
      ),
    ],
    selectedDaemonId: 'daemon-1',
  );
}
