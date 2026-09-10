import 'package:flutter/widgets.dart';

import '../l10n/generated/app_localizations.dart';

extension LocalizationBuildContext on BuildContext {
  AppLocalizations get l10n => AppLocalizations.of(this);
}
