// ignore: unused_import
import 'package:intl/intl.dart' as intl;

import 'app_localizations.dart';

// ignore_for_file: type=lint

/// The translations for English (`en`).
class AppLocalizationsEn extends AppLocalizations {
  AppLocalizationsEn([String locale = 'en']) : super(locale);

  @override
  String get appTitle => 'Work Any Where';

  @override
  String get continueOnPhone => 'Continue your computer work from your phone';

  @override
  String get privacyIntro =>
      'Conversation content is end-to-end encrypted. The Relay cannot read it.';

  @override
  String get scanQr => 'Scan QR code';

  @override
  String get scanComputerQr => 'Scan the QR code on your computer';

  @override
  String get pastePairLink => 'Paste a waw:// pairing link';

  @override
  String get invalidPairLink =>
      'This is not a valid Work Anywhere pairing link';

  @override
  String get pairFailed =>
      'Connection failed. Check that the QR code has not expired and that its Relay address is reachable from this phone (not 127.0.0.1 or localhost).';

  @override
  String get waitingDesktop => 'Waiting for computer approval…';

  @override
  String get projects => 'Projects';

  @override
  String get projectSearchResults => 'Project results';

  @override
  String get recentProjects => 'Recent 10';

  @override
  String resultCount(int count) {
    return '$count';
  }

  @override
  String get noProjects => 'This agent has no projects to display';

  @override
  String get expandProjects => 'Show more projects';

  @override
  String get recent => 'Recent';

  @override
  String get conversationSearchResults => 'Conversation results';

  @override
  String get recentConversations => 'Recent 30';

  @override
  String get noConversations => 'No conversations yet';

  @override
  String get searchProjectsConversations => 'Search projects and conversations';

  @override
  String get secureOnline => 'Online · Secure connection';

  @override
  String get online => 'Online';

  @override
  String get connecting => 'Connecting';

  @override
  String get offline => 'Offline';

  @override
  String daemonSwitchLabel(String name, String status) {
    return '$name, $status. Tap to switch computers';
  }

  @override
  String get addComputer => 'Add computer';

  @override
  String get manageComputers => 'Manage computers';

  @override
  String get running => 'Running';

  @override
  String runningCount(int count) {
    return '$count running';
  }

  @override
  String get loadWorkspaceFailed => 'Unable to load workspace';

  @override
  String get workspaceSyncFailed =>
      'Connected to the computer, but workspace sync failed';

  @override
  String get retry => 'Retry';

  @override
  String get project => 'Project';

  @override
  String get loadProjectFailed => 'Unable to load project conversations';

  @override
  String get showMore => 'Show more';

  @override
  String sendFailed(String error) {
    return 'Send failed: $error';
  }

  @override
  String get stopTitle => 'Stop this task?';

  @override
  String get stopBody => 'Output already produced will be kept.';

  @override
  String get cancel => 'Cancel';

  @override
  String get stop => 'Stop';

  @override
  String get conversation => 'Conversation';

  @override
  String get secureConnection => 'Secure connection';

  @override
  String get syncFailed => 'Content sync failed';

  @override
  String get actionSyncFailed =>
      'Action sync failed; conversation content remains available';

  @override
  String get copyDiagnostics => 'Copy diagnostics';

  @override
  String get diagnosticsCopied => 'Diagnostics copied';

  @override
  String get retryOlder => 'Older content failed to load. Tap to retry';

  @override
  String get viewFullContent => 'View full content';

  @override
  String get fullContent => 'Full content';

  @override
  String get confirmationRequired => 'Your confirmation is required';

  @override
  String get agentContinueRequest =>
      'The agent requested permission to continue';

  @override
  String get deny => 'Deny';

  @override
  String get allow => 'Allow';

  @override
  String get runningStop => 'Task running · Stop';

  @override
  String get continueWorking => 'Continue working on the computer';

  @override
  String get noTimeline => 'No content to display';

  @override
  String get justNow => 'Just now';

  @override
  String minutesAgo(int count) {
    return '$count min';
  }

  @override
  String hoursAgo(int count) {
    return '$count hr';
  }

  @override
  String daysAgo(int count) {
    return '$count d';
  }

  @override
  String weeksAgo(int count) {
    return '$count wk';
  }
}

/// The translations for English, as used in the United States (`en_US`).
class AppLocalizationsEnUs extends AppLocalizationsEn {
  AppLocalizationsEnUs() : super('en_US');
}
