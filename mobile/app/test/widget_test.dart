import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:work_anywhere/features/pairing/pairing_screen.dart';
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
}
