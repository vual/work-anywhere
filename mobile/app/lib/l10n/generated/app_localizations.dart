import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:intl/intl.dart' as intl;

import 'app_localizations_en.dart';
import 'app_localizations_zh.dart';

// ignore_for_file: type=lint

/// Callers can lookup localized strings with an instance of AppLocalizations
/// returned by `AppLocalizations.of(context)`.
///
/// Applications need to include `AppLocalizations.delegate()` in their app's
/// `localizationDelegates` list, and the locales they support in the app's
/// `supportedLocales` list. For example:
///
/// ```dart
/// import 'generated/app_localizations.dart';
///
/// return MaterialApp(
///   localizationsDelegates: AppLocalizations.localizationsDelegates,
///   supportedLocales: AppLocalizations.supportedLocales,
///   home: MyApplicationHome(),
/// );
/// ```
///
/// ## Update pubspec.yaml
///
/// Please make sure to update your pubspec.yaml to include the following
/// packages:
///
/// ```yaml
/// dependencies:
///   # Internationalization support.
///   flutter_localizations:
///     sdk: flutter
///   intl: any # Use the pinned version from flutter_localizations
///
///   # Rest of dependencies
/// ```
///
/// ## iOS Applications
///
/// iOS applications define key application metadata, including supported
/// locales, in an Info.plist file that is built into the application bundle.
/// To configure the locales supported by your app, you’ll need to edit this
/// file.
///
/// First, open your project’s ios/Runner.xcworkspace Xcode workspace file.
/// Then, in the Project Navigator, open the Info.plist file under the Runner
/// project’s Runner folder.
///
/// Next, select the Information Property List item, select Add Item from the
/// Editor menu, then select Localizations from the pop-up menu.
///
/// Select and expand the newly-created Localizations item then, for each
/// locale your application supports, add a new item and select the locale
/// you wish to add from the pop-up menu in the Value field. This list should
/// be consistent with the languages listed in the AppLocalizations.supportedLocales
/// property.
abstract class AppLocalizations {
  AppLocalizations(String locale)
    : localeName = intl.Intl.canonicalizedLocale(locale.toString());

  final String localeName;

  static AppLocalizations of(BuildContext context) {
    return Localizations.of<AppLocalizations>(context, AppLocalizations)!;
  }

  static const LocalizationsDelegate<AppLocalizations> delegate =
      _AppLocalizationsDelegate();

  /// A list of this localizations delegate along with the default localizations
  /// delegates.
  ///
  /// Returns a list of localizations delegates containing this delegate along with
  /// GlobalMaterialLocalizations.delegate, GlobalCupertinoLocalizations.delegate,
  /// and GlobalWidgetsLocalizations.delegate.
  ///
  /// Additional delegates can be added by appending to this list in
  /// MaterialApp. This list does not have to be used at all if a custom list
  /// of delegates is preferred or required.
  static const List<LocalizationsDelegate<dynamic>> localizationsDelegates =
      <LocalizationsDelegate<dynamic>>[
        delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
      ];

  /// A list of this localizations delegate's supported locales.
  static const List<Locale> supportedLocales = <Locale>[
    Locale('en'),
    Locale('en', 'US'),
    Locale('zh'),
    Locale('zh', 'CN'),
  ];

  /// No description provided for @appTitle.
  ///
  /// In zh, this message translates to:
  /// **'Work Any Where'**
  String get appTitle;

  /// No description provided for @continueOnPhone.
  ///
  /// In zh, this message translates to:
  /// **'在手机上继续电脑里的工作'**
  String get continueOnPhone;

  /// No description provided for @privacyIntro.
  ///
  /// In zh, this message translates to:
  /// **'会话内容使用端到端加密，Relay 无法读取正文。'**
  String get privacyIntro;

  /// No description provided for @scanQr.
  ///
  /// In zh, this message translates to:
  /// **'扫描二维码'**
  String get scanQr;

  /// No description provided for @scanComputerQr.
  ///
  /// In zh, this message translates to:
  /// **'扫描电脑上的二维码'**
  String get scanComputerQr;

  /// No description provided for @pastePairLink.
  ///
  /// In zh, this message translates to:
  /// **'粘贴 waw:// 配对链接'**
  String get pastePairLink;

  /// No description provided for @invalidPairLink.
  ///
  /// In zh, this message translates to:
  /// **'这不是有效的 Work Anywhere 配对链接'**
  String get invalidPairLink;

  /// No description provided for @pairFailed.
  ///
  /// In zh, this message translates to:
  /// **'连接失败。请确认二维码未过期，且 Relay 地址可由手机访问（不能使用 127.0.0.1 或 localhost）'**
  String get pairFailed;

  /// No description provided for @waitingDesktop.
  ///
  /// In zh, this message translates to:
  /// **'等待电脑确认…'**
  String get waitingDesktop;

  /// No description provided for @projects.
  ///
  /// In zh, this message translates to:
  /// **'项目'**
  String get projects;

  /// No description provided for @projectSearchResults.
  ///
  /// In zh, this message translates to:
  /// **'项目搜索结果'**
  String get projectSearchResults;

  /// No description provided for @recentProjects.
  ///
  /// In zh, this message translates to:
  /// **'最近 10 个'**
  String get recentProjects;

  /// No description provided for @resultCount.
  ///
  /// In zh, this message translates to:
  /// **'{count} 个'**
  String resultCount(int count);

  /// No description provided for @noProjects.
  ///
  /// In zh, this message translates to:
  /// **'当前 Agent 没有可显示的项目'**
  String get noProjects;

  /// No description provided for @expandProjects.
  ///
  /// In zh, this message translates to:
  /// **'展开更多项目'**
  String get expandProjects;

  /// No description provided for @recent.
  ///
  /// In zh, this message translates to:
  /// **'最近'**
  String get recent;

  /// No description provided for @conversationSearchResults.
  ///
  /// In zh, this message translates to:
  /// **'对话搜索结果'**
  String get conversationSearchResults;

  /// No description provided for @recentConversations.
  ///
  /// In zh, this message translates to:
  /// **'最近 30 个'**
  String get recentConversations;

  /// No description provided for @noConversations.
  ///
  /// In zh, this message translates to:
  /// **'还没有对话'**
  String get noConversations;

  /// No description provided for @searchProjectsConversations.
  ///
  /// In zh, this message translates to:
  /// **'搜索项目和对话'**
  String get searchProjectsConversations;

  /// No description provided for @secureOnline.
  ///
  /// In zh, this message translates to:
  /// **'在线 · 安全连接'**
  String get secureOnline;

  /// No description provided for @online.
  ///
  /// In zh, this message translates to:
  /// **'在线'**
  String get online;

  /// No description provided for @connecting.
  ///
  /// In zh, this message translates to:
  /// **'连接中'**
  String get connecting;

  /// No description provided for @offline.
  ///
  /// In zh, this message translates to:
  /// **'离线'**
  String get offline;

  /// No description provided for @daemonSwitchLabel.
  ///
  /// In zh, this message translates to:
  /// **'{name}，{status}，点击切换电脑'**
  String daemonSwitchLabel(String name, String status);

  /// No description provided for @addComputer.
  ///
  /// In zh, this message translates to:
  /// **'添加电脑'**
  String get addComputer;

  /// No description provided for @manageComputers.
  ///
  /// In zh, this message translates to:
  /// **'管理电脑'**
  String get manageComputers;

  /// No description provided for @running.
  ///
  /// In zh, this message translates to:
  /// **'运行中'**
  String get running;

  /// No description provided for @runningCount.
  ///
  /// In zh, this message translates to:
  /// **'{count} 个运行中'**
  String runningCount(int count);

  /// No description provided for @loadWorkspaceFailed.
  ///
  /// In zh, this message translates to:
  /// **'无法载入工作台'**
  String get loadWorkspaceFailed;

  /// No description provided for @workspaceSyncFailed.
  ///
  /// In zh, this message translates to:
  /// **'已连接电脑，但工作台同步失败'**
  String get workspaceSyncFailed;

  /// No description provided for @retry.
  ///
  /// In zh, this message translates to:
  /// **'重试'**
  String get retry;

  /// No description provided for @project.
  ///
  /// In zh, this message translates to:
  /// **'项目'**
  String get project;

  /// No description provided for @loadProjectFailed.
  ///
  /// In zh, this message translates to:
  /// **'无法载入项目对话'**
  String get loadProjectFailed;

  /// No description provided for @showMore.
  ///
  /// In zh, this message translates to:
  /// **'显示更多'**
  String get showMore;

  /// No description provided for @sendFailed.
  ///
  /// In zh, this message translates to:
  /// **'发送失败：{error}'**
  String sendFailed(String error);

  /// No description provided for @stopTitle.
  ///
  /// In zh, this message translates to:
  /// **'停止当前任务？'**
  String get stopTitle;

  /// No description provided for @stopBody.
  ///
  /// In zh, this message translates to:
  /// **'已经产生的输出会被保留。'**
  String get stopBody;

  /// No description provided for @cancel.
  ///
  /// In zh, this message translates to:
  /// **'取消'**
  String get cancel;

  /// No description provided for @stop.
  ///
  /// In zh, this message translates to:
  /// **'停止'**
  String get stop;

  /// No description provided for @conversation.
  ///
  /// In zh, this message translates to:
  /// **'对话'**
  String get conversation;

  /// No description provided for @secureConnection.
  ///
  /// In zh, this message translates to:
  /// **'安全连接'**
  String get secureConnection;

  /// No description provided for @syncFailed.
  ///
  /// In zh, this message translates to:
  /// **'内容同步失败'**
  String get syncFailed;

  /// No description provided for @actionSyncFailed.
  ///
  /// In zh, this message translates to:
  /// **'待确认操作同步失败，正文仍可查看'**
  String get actionSyncFailed;

  /// No description provided for @copyDiagnostics.
  ///
  /// In zh, this message translates to:
  /// **'复制诊断'**
  String get copyDiagnostics;

  /// No description provided for @diagnosticsCopied.
  ///
  /// In zh, this message translates to:
  /// **'诊断信息已复制'**
  String get diagnosticsCopied;

  /// No description provided for @retryOlder.
  ///
  /// In zh, this message translates to:
  /// **'较早内容加载失败，点击重试'**
  String get retryOlder;

  /// No description provided for @viewFullContent.
  ///
  /// In zh, this message translates to:
  /// **'查看完整内容'**
  String get viewFullContent;

  /// No description provided for @fullContent.
  ///
  /// In zh, this message translates to:
  /// **'完整内容'**
  String get fullContent;

  /// No description provided for @confirmationRequired.
  ///
  /// In zh, this message translates to:
  /// **'需要你确认'**
  String get confirmationRequired;

  /// No description provided for @agentContinueRequest.
  ///
  /// In zh, this message translates to:
  /// **'Agent 请求继续执行'**
  String get agentContinueRequest;

  /// No description provided for @deny.
  ///
  /// In zh, this message translates to:
  /// **'拒绝'**
  String get deny;

  /// No description provided for @allow.
  ///
  /// In zh, this message translates to:
  /// **'允许'**
  String get allow;

  /// No description provided for @runningStop.
  ///
  /// In zh, this message translates to:
  /// **'任务运行中 · 停止'**
  String get runningStop;

  /// No description provided for @continueWorking.
  ///
  /// In zh, this message translates to:
  /// **'在电脑上继续工作'**
  String get continueWorking;

  /// No description provided for @noTimeline.
  ///
  /// In zh, this message translates to:
  /// **'暂无可显示的内容'**
  String get noTimeline;

  /// No description provided for @justNow.
  ///
  /// In zh, this message translates to:
  /// **'刚刚'**
  String get justNow;

  /// No description provided for @minutesAgo.
  ///
  /// In zh, this message translates to:
  /// **'{count} 分钟'**
  String minutesAgo(int count);

  /// No description provided for @hoursAgo.
  ///
  /// In zh, this message translates to:
  /// **'{count} 小时'**
  String hoursAgo(int count);

  /// No description provided for @daysAgo.
  ///
  /// In zh, this message translates to:
  /// **'{count} 天'**
  String daysAgo(int count);

  /// No description provided for @weeksAgo.
  ///
  /// In zh, this message translates to:
  /// **'{count} 周'**
  String weeksAgo(int count);
}

class _AppLocalizationsDelegate
    extends LocalizationsDelegate<AppLocalizations> {
  const _AppLocalizationsDelegate();

  @override
  Future<AppLocalizations> load(Locale locale) {
    return SynchronousFuture<AppLocalizations>(lookupAppLocalizations(locale));
  }

  @override
  bool isSupported(Locale locale) =>
      <String>['en', 'zh'].contains(locale.languageCode);

  @override
  bool shouldReload(_AppLocalizationsDelegate old) => false;
}

AppLocalizations lookupAppLocalizations(Locale locale) {
  // Lookup logic when language+country codes are specified.
  switch (locale.languageCode) {
    case 'en':
      {
        switch (locale.countryCode) {
          case 'US':
            return AppLocalizationsEnUs();
        }
        break;
      }
    case 'zh':
      {
        switch (locale.countryCode) {
          case 'CN':
            return AppLocalizationsZhCn();
        }
        break;
      }
  }

  // Lookup logic when only language code is specified.
  switch (locale.languageCode) {
    case 'en':
      return AppLocalizationsEn();
    case 'zh':
      return AppLocalizationsZh();
  }

  throw FlutterError(
    'AppLocalizations.delegate failed to load unsupported locale "$locale". This is likely '
    'an issue with the localizations generation tool. Please file an issue '
    'on GitHub with a reproducible sample app and the gen-l10n configuration '
    'that was used.',
  );
}
