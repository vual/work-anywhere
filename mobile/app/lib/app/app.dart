import 'package:flutter/material.dart';

import 'router.dart';
import 'theme.dart';
import '../l10n/generated/app_localizations.dart';

class WorkAnywhereApp extends StatelessWidget {
  const WorkAnywhereApp({super.key});

  @override
  Widget build(BuildContext context) => MaterialApp.router(
    onGenerateTitle: (context) => AppLocalizations.of(context).appTitle,
    debugShowCheckedModeBanner: false,
    theme: buildLightTheme(),
    darkTheme: buildDarkTheme(),
    localizationsDelegates: AppLocalizations.localizationsDelegates,
    supportedLocales: AppLocalizations.supportedLocales,
    routerConfig: appRouter,
  );
}
