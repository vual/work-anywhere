import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

const wawBlue = Color(0xFF2F80ED);
const wawGreen = Color(0xFF34C759);

/// System bar appearance for the app's edge-to-edge surfaces.
///
/// Android 15+ (and Flutter apps targeting Android 16) keeps the status bar
/// transparent. The icon brightness must therefore be explicit; relying on
/// the native theme can leave dark icons disabled after the Activity is
/// restored by an OEM launcher.
SystemUiOverlayStyle wawSystemUiOverlayStyle(Brightness brightness) {
  final style = brightness == Brightness.dark
      ? SystemUiOverlayStyle.light
      : SystemUiOverlayStyle.dark;
  return style.copyWith(
    statusBarColor: Colors.transparent,
    systemNavigationBarColor: Colors.transparent,
    systemStatusBarContrastEnforced: false,
    systemNavigationBarContrastEnforced: false,
  );
}

extension WawThemeColors on ThemeData {
  Color get floatingSurface =>
      brightness == Brightness.dark ? const Color(0xFF202124) : Colors.white;
  Color get softSurface => brightness == Brightness.dark
      ? const Color(0xFF292A2D)
      : const Color(0xFFF1F1F3);
  Color get mutedInk => brightness == Brightness.dark
      ? const Color(0xFFA7A7AA)
      : const Color(0xFF8D8D92);
  Color get userBubble => brightness == Brightness.dark
      ? const Color(0xFF17365C)
      : const Color(0xFFE4F0FF);
  Color get activityRail => brightness == Brightness.dark
      ? const Color(0xFF55565A)
      : const Color(0xFFD1D1D4);
  List<BoxShadow> get floatingShadow => [
    BoxShadow(
      color: Colors.black.withValues(
        alpha: brightness == Brightness.dark ? .28 : .08,
      ),
      blurRadius: 24,
      offset: const Offset(0, 8),
    ),
  ];
}

/// Typography and spacing shared by the information-dense workspace screens.
/// Pairing/onboarding deliberately keeps the default Material sizing.
abstract final class WawCompact {
  static const horizontalPadding = 24.0;
  static const listTileHeight = 54.0;
  static const body = TextStyle(fontSize: 16, height: 1.52);
  static const listTitle = TextStyle(fontSize: 17, height: 1.28);
  static const conversationTitle = TextStyle(
    fontSize: 15,
    height: 1.2,
    fontWeight: FontWeight.w400,
  );
  static const activity = TextStyle(fontSize: 14, height: 1.15);
  static const activityDetail = TextStyle(fontSize: 13, height: 1.25);
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
  final floatingSurface = brightness == Brightness.dark
      ? const Color(0xFF202124)
      : Colors.white;
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
    appBarTheme: AppBarTheme(
      backgroundColor: Colors.transparent,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      scrolledUnderElevation: 0,
      toolbarHeight: 72,
      foregroundColor: ink,
      systemOverlayStyle: wawSystemUiOverlayStyle(brightness),
    ),
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
    bottomSheetTheme: BottomSheetThemeData(
      backgroundColor: floatingSurface,
      modalBackgroundColor: floatingSurface,
      surfaceTintColor: Colors.transparent,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(28)),
      ),
      clipBehavior: Clip.antiAlias,
    ),
  );
}
