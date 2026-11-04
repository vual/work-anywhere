// ignore: unused_import
import 'package:intl/intl.dart' as intl;

import 'app_localizations.dart';

// ignore_for_file: type=lint

/// The translations for English (`en`).
class AppLocalizationsEn extends AppLocalizations {
  AppLocalizationsEn([String locale = 'en']) : super(locale);

  @override
  String get permissionCustom => 'Custom permissions';

  @override
  String get messageSyncing => 'Syncing with computer…';

  @override
  String messageUploading(int index, int count, int percent) {
    return 'Uploading image $index/$count · $percent%';
  }

  @override
  String get messageSending => 'Sending…';

  @override
  String get messageConfirming => 'Waiting for computer confirmation';

  @override
  String get messageFailed => 'Send failed';

  @override
  String get messageUnknown => 'Send result unknown';

  @override
  String get messageEdit => 'Edit';

  @override
  String get messageCheck => 'Check status';

  @override
  String get messageCreationUnknown =>
      'Conversation creation could not be confirmed. Check on your computer before starting another conversation.';

  @override
  String get conversationChangedRetry =>
      'The conversation changed. Please retry.';

  @override
  String get queueUnavailableWait =>
      'Queued messages are unavailable for this conversation. Wait for the current task to finish.';

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
  String pendingCount(int count) {
    return '$count pending';
  }

  @override
  String get needsAttention => 'Needs your attention';

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
  String get newConversation => 'New conversation';

  @override
  String get createConversationFailed =>
      'Unable to create a conversation. Try again later.';

  @override
  String get createConversationPermissionDenied =>
      'This pairing cannot create conversations. Pair the computer again.';

  @override
  String get createConversationDesktopFailed =>
      'The computer cannot create a conversation right now. Check the Desktop connection.';

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
  String get send => 'Send';

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
  String get plan => 'Plan';

  @override
  String get viewFullPlan => 'View full plan';

  @override
  String get planLoadFailed => 'Unable to load the full plan. Try again.';

  @override
  String get viewFullContent => 'View full content';

  @override
  String get fullContent => 'Full content';

  @override
  String summaryFunctionCall(String value) {
    return 'Called function: $value';
  }

  @override
  String summaryMcpToolCall(String value) {
    return 'Called MCP: $value';
  }

  @override
  String summaryDynamicToolCall(String value) {
    return 'Called tool: $value';
  }

  @override
  String summaryCollabAgentToolCall(String value) {
    return 'Collaboration call: $value';
  }

  @override
  String summarySubAgentActivity(String value) {
    return 'Sub-agent: $value';
  }

  @override
  String summaryWebSearch(String value) {
    return 'Searched: $value';
  }

  @override
  String summaryImageView(String value) {
    return 'Viewed image: $value';
  }

  @override
  String summarySleep(String value) {
    return 'Waited: $value';
  }

  @override
  String get summaryImageGeneration => 'Generated an image';

  @override
  String summaryEnteredReviewMode(String value) {
    return 'Entered review mode: $value';
  }

  @override
  String summaryExitedReviewMode(String value) {
    return 'Exited review mode: $value';
  }

  @override
  String get summaryContextCompaction => 'Context compacted';

  @override
  String get summaryStatusCompleted => 'Completed';

  @override
  String get summaryStatusRunning => 'Running';

  @override
  String get summaryStatusFailed => 'Failed';

  @override
  String get externalLinkTitle => 'Open external link?';

  @override
  String externalLinkBody(String host) {
    return 'This will open $host. Confirm that you trust this website.';
  }

  @override
  String get unsupportedLink => 'This link type is not supported';

  @override
  String get externalLinkFailed => 'Unable to open this link';

  @override
  String get open => 'Open';

  @override
  String get remoteImageBlocked => 'Remote image hidden';

  @override
  String get confirmationRequired => 'Your confirmation is required';

  @override
  String pendingActionsRemaining(int count) {
    return '$count more pending';
  }

  @override
  String get agentContinueRequest =>
      'The agent requested permission to continue';

  @override
  String get deny => 'Deny';

  @override
  String get allow => 'Allow';

  @override
  String get answerQuestionsTitle => 'Your answer is needed';

  @override
  String get answer => 'Answer';

  @override
  String get answerHint => 'Enter an answer';

  @override
  String get openQuestions => 'Answer questions';

  @override
  String questionResponseFailed(String error) {
    return 'Could not submit answers: $error';
  }

  @override
  String get runningStop => 'Task running · Stop';

  @override
  String get continueWorking => 'Continue working on the computer';

  @override
  String get addAttachment => 'Add attachment';

  @override
  String get addMenu => 'Add';

  @override
  String get uploadPhoto => 'Upload photo';

  @override
  String get planMode => 'Plan mode';

  @override
  String get planModeActive => 'Plan mode on';

  @override
  String get permissions => 'Permissions';

  @override
  String get permissionDefault => 'Default permissions';

  @override
  String get permissionDefaultDescription =>
      'Run commands in the workspace sandbox';

  @override
  String get permissionAutoReview => 'Auto review';

  @override
  String get permissionAutoReviewDescription =>
      'Automatically review elevation requests';

  @override
  String get permissionReadOnly => 'Read only';

  @override
  String get permissionReadOnlyDescription =>
      'File edits and commands require approval';

  @override
  String get permissionFullAccess => 'Full access';

  @override
  String get permissionFullAccessDescription =>
      'Full access to this computer (higher risk)';

  @override
  String get permissionUnavailable =>
      'Unavailable under the current Desktop policy';

  @override
  String get fullAccessTitle => 'Allow full computer access?';

  @override
  String get fullAccessBody =>
      'Full access lets the agent run commands and change files without approval and outside the workspace. Only continue if you trust this task.';

  @override
  String get confirmFullAccess => 'Allow full access';

  @override
  String get model => 'Model';

  @override
  String get otherModels => 'Other models';

  @override
  String get reasoningEffort => 'Thinking level';

  @override
  String get settingsUnavailable =>
      'Model and permission settings could not be loaded. Basic sending is still available.';

  @override
  String get settingsSelectionUnavailable =>
      'The selected model, reasoning effort or permission is unavailable. Choose again before retrying.';

  @override
  String get retrySettings => 'Retry settings';

  @override
  String settingsUpdateFailed(String error) {
    return 'Could not update conversation settings: $error';
  }

  @override
  String get settingSmart => 'Smart';

  @override
  String get settingLow => 'Low';

  @override
  String get settingMedium => 'Medium';

  @override
  String get settingHigh => 'High';

  @override
  String get settingXhigh => 'Extra high';

  @override
  String get settingMax => 'Max';

  @override
  String get settingUltra => 'Ultra';

  @override
  String imageSelectionLimit(int count) {
    return 'Select up to $count images per message';
  }

  @override
  String get imageSelectionTooLarge =>
      'Each image and all selected images together must not exceed 48 MiB';

  @override
  String selectedImage(String name) {
    return 'Selected image: $name';
  }

  @override
  String removeSelectedImage(String name) {
    return 'Remove image: $name';
  }

  @override
  String get queuedMessage => 'Queued message';

  @override
  String get queuedMessageActions => 'Queued message actions';

  @override
  String get editQueuedMessage => 'Edit message';

  @override
  String get guideQueuedMessageNow => 'Guide now';

  @override
  String get cancelQueuedMessage => 'Cancel sending';

  @override
  String queuedAttachmentCount(int count) {
    return '$count attachment(s)';
  }

  @override
  String queueOperationFailed(String error) {
    return 'Could not update the queued message: $error';
  }

  @override
  String get currentDraftMustBeEmpty =>
      'Send or clear the current draft before editing a queued message';

  @override
  String get queuedAttachmentsEditUnavailable =>
      'This queued message contains attachments that are no longer available on this phone';

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

  @override
  String get computer => 'Computer';

  @override
  String get back => 'Back';

  @override
  String get connectionSection => 'Connections';

  @override
  String get disabled => 'Disabled';

  @override
  String get dragToReorder => 'Long-press the handle, then drag to reorder';

  @override
  String get addComputerHint =>
      'Pair with the ChatGPT desktop app using a QR code';

  @override
  String get editComputer => 'Edit computer';

  @override
  String get deleteComputer => 'Delete computer';

  @override
  String deleteComputerConfirm(String name) {
    return 'Remove “$name” from this phone? Its local pairing information will be deleted. Pair it again to reconnect. Revoke the computer\'s authorization separately on the computer.';
  }

  @override
  String get computerDeleted => 'Computer deleted';

  @override
  String get computerDeleteFailed => 'Unable to delete computer. Try again.';

  @override
  String get save => 'Save';

  @override
  String get computerName => 'Computer name';

  @override
  String get relayAddress => 'Relay address';

  @override
  String get httpRelayWarning =>
      'HTTP is unencrypted; use it only on a trusted network';

  @override
  String get invalidComputerName =>
      'Computer name is required and must be 80 characters or fewer';

  @override
  String get invalidRelayAddress => 'Enter a valid HTTP(S) Relay address';

  @override
  String get computerSaved => 'Computer settings saved';

  @override
  String get computerSavedOffline =>
      'Settings saved, but the connection failed';

  @override
  String get computerSaveFailed => 'Unable to save computer settings';

  @override
  String get noEnabledComputers => 'No computers are enabled';

  @override
  String get connected => 'Connected';

  @override
  String get refresh => 'Refresh';

  @override
  String get moreActions => 'More actions';

  @override
  String get renameConversation => 'Rename conversation';

  @override
  String get conversationName => 'Conversation name';

  @override
  String get invalidConversationName =>
      'Name must be 1-200 characters on a single line';

  @override
  String renameFailed(String error) {
    return 'Could not rename: $error';
  }

  @override
  String get copyConversationId => 'Copy thread ID';

  @override
  String get conversationIdCopied => 'Thread ID copied';

  @override
  String get scrollToBottom => 'Scroll to bottom';

  @override
  String get commandExecution => 'Command execution';

  @override
  String get toolActivity => 'Tool activity';

  @override
  String get reasoningActivity => 'Thinking';

  @override
  String get fileChange => 'File changes';

  @override
  String get showDetails => 'Show details';

  @override
  String get hideDetails => 'Hide details';

  @override
  String get planConfirmationTitle => 'Implement this plan?';

  @override
  String get executePlan => 'Yes, implement this plan';

  @override
  String get executePlanTurnPrompt => 'Implement this plan.';

  @override
  String get revisePlanHint => 'No, describe what to change';

  @override
  String get skip => 'Skip';

  @override
  String get submit => 'Submit';

  @override
  String planResponseFailed(String error) {
    return 'Could not respond to the plan: $error';
  }

  @override
  String get openOnComputer => 'Open on computer';

  @override
  String get conversationOpenOnComputer =>
      'This conversation is open on the computer, so it is read-only here. Close it there to continue from your phone.';

  @override
  String agentCannotSend(String agent) {
    return '$agent can\'t receive messages from your phone right now.';
  }
}

/// The translations for English, as used in the United States (`en_US`).
class AppLocalizationsEnUs extends AppLocalizationsEn {
  AppLocalizationsEnUs() : super('en_US');

  @override
  String get permissionCustom => 'Custom permissions';
}
