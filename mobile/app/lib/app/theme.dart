import 'package:flutter/material.dart';

const wawBlue = Color(0xFF2F80ED);
const wawGreen = Color(0xFF34C759);

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
