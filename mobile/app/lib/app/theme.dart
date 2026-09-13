import 'package:flutter/material.dart';

const wawBlue = Color(0xFF2F80ED);
const wawGreen = Color(0xFF34C759);

/// Typography and spacing shared by the information-dense workspace screens.
/// Pairing/onboarding deliberately keeps the default Material sizing.
abstract final class WawCompact {
  static const horizontalPadding = 20.0;
  static const listTileHeight = 52.0;
  static const body = TextStyle(fontSize: 15, height: 1.45);
  static const listTitle = TextStyle(fontSize: 16, height: 1.25);
  static const sectionTitle = TextStyle(
    fontSize: 18,
    height: 1.25,
    fontWeight: FontWeight.w700,
  );
  static const appBarTitle = TextStyle(
    fontSize: 17,
    height: 1.2,
    fontWeight: FontWeight.w700,
  );
  static const metadata = TextStyle(fontSize: 13, height: 1.25);
}

ThemeData buildLightTheme() => _theme(
  Brightness.light,
  Colors.white,
  const Color(0xFF111111),
  const Color(0xFFF2F2F4),
);
ThemeData buildDarkTheme() => _theme(
  Brightness.dark,
  const Color(0xFF0E0F11),
  const Color(0xFFF5F5F5),
  const Color(0xFF202226),
);

ThemeData _theme(Brightness brightness, Color surface, Color ink, Color soft) {
  final scheme = ColorScheme.fromSeed(
    seedColor: wawBlue,
    brightness: brightness,
    surface: surface,
  );
  return ThemeData(
    useMaterial3: true,
    brightness: brightness,
    colorScheme: scheme,
    scaffoldBackgroundColor: surface,
    fontFamilyFallback: const ['PingFang SC', 'Microsoft YaHei', 'sans-serif'],
    textTheme: ThemeData(brightness: brightness).textTheme
        .apply(bodyColor: ink, displayColor: ink),
    dividerColor: Colors.transparent,
    cardColor: soft,
    splashFactory: InkSparkle.splashFactory,
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: soft,
      border: OutlineInputBorder(
        borderSide: BorderSide.none,
        borderRadius: BorderRadius.circular(28),
      ),
      contentPadding: const EdgeInsets.symmetric(horizontal: 20, vertical: 15),
    ),
  );
}
