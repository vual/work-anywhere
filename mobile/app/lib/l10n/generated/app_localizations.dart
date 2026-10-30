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

  /// No description provided for @permissionCustom.
  ///
  /// In zh, this message translates to:
  /// **'自定义权限'**
  String get permissionCustom;

  /// No description provided for @messageSyncing.
  ///
  /// In zh, this message translates to:
  /// **'正在同步电脑状态…'**
  String get messageSyncing;

  /// No description provided for @messageUploading.
  ///
  /// In zh, this message translates to:
  /// **'正在上传图片 {index}/{count} · {percent}%'**
  String messageUploading(int index, int count, int percent);

  /// No description provided for @messageSending.
  ///
  /// In zh, this message translates to:
  /// **'正在发送…'**
  String get messageSending;

  /// No description provided for @messageConfirming.
  ///
  /// In zh, this message translates to:
  /// **'等待电脑确认'**
  String get messageConfirming;

  /// No description provided for @messageFailed.
  ///
  /// In zh, this message translates to:
  /// **'发送失败'**
  String get messageFailed;

  /// No description provided for @messageUnknown.
  ///
  /// In zh, this message translates to:
  /// **'发送结果未知'**
  String get messageUnknown;

  /// No description provided for @messageEdit.
  ///
  /// In zh, this message translates to:
  /// **'编辑'**
  String get messageEdit;

  /// No description provided for @messageCheck.
  ///
  /// In zh, this message translates to:
  /// **'同步确认'**
  String get messageCheck;

  /// No description provided for @messageCreationUnknown.
  ///
  /// In zh, this message translates to:
  /// **'新会话创建结果未知，请先在电脑端确认，再创建其他会话。'**
  String get messageCreationUnknown;

  /// No description provided for @conversationChangedRetry.
  ///
  /// In zh, this message translates to:
  /// **'会话状态已变化，请重试'**
  String get conversationChangedRetry;

  /// No description provided for @queueUnavailableWait.
  ///
  /// In zh, this message translates to:
  /// **'此会话暂不支持消息排队，请等待当前任务结束'**
  String get queueUnavailableWait;

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

  /// No description provided for @pendingCount.
  ///
  /// In zh, this message translates to:
  /// **'{count} 个待处理'**
  String pendingCount(int count);

  /// No description provided for @needsAttention.
  ///
  /// In zh, this message translates to:
  /// **'需要人工处理'**
  String get needsAttention;

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

  /// No description provided for @newConversation.
  ///
  /// In zh, this message translates to:
  /// **'新建对话'**
  String get newConversation;

  /// No description provided for @createConversationFailed.
  ///
  /// In zh, this message translates to:
  /// **'无法新建对话，请稍后重试'**
  String get createConversationFailed;

  /// No description provided for @createConversationPermissionDenied.
  ///
  /// In zh, this message translates to:
  /// **'当前配对没有新建对话权限，请重新配对电脑'**
  String get createConversationPermissionDenied;

  /// No description provided for @createConversationDesktopFailed.
  ///
  /// In zh, this message translates to:
  /// **'电脑端暂时无法新建对话，请检查 Desktop 连接'**
  String get createConversationDesktopFailed;

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

  /// No description provided for @send.
  ///
  /// In zh, this message translates to:
  /// **'发送'**
  String get send;

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

  /// No description provided for @plan.
  ///
  /// In zh, this message translates to:
  /// **'计划'**
  String get plan;

  /// No description provided for @viewFullPlan.
  ///
  /// In zh, this message translates to:
  /// **'查看完整计划'**
  String get viewFullPlan;

  /// No description provided for @planLoadFailed.
  ///
  /// In zh, this message translates to:
  /// **'完整计划加载失败，请重试'**
  String get planLoadFailed;

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

  /// No description provided for @summaryFunctionCall.
  ///
  /// In zh, this message translates to:
  /// **'调用函数：{value}'**
  String summaryFunctionCall(String value);

  /// No description provided for @summaryMcpToolCall.
  ///
  /// In zh, this message translates to:
  /// **'调用 MCP：{value}'**
  String summaryMcpToolCall(String value);

  /// No description provided for @summaryDynamicToolCall.
  ///
  /// In zh, this message translates to:
  /// **'调用工具：{value}'**
  String summaryDynamicToolCall(String value);

  /// No description provided for @summaryCollabAgentToolCall.
  ///
  /// In zh, this message translates to:
  /// **'协作调用：{value}'**
  String summaryCollabAgentToolCall(String value);

  /// No description provided for @summarySubAgentActivity.
  ///
  /// In zh, this message translates to:
  /// **'子 Agent：{value}'**
  String summarySubAgentActivity(String value);

  /// No description provided for @summaryWebSearch.
  ///
  /// In zh, this message translates to:
  /// **'搜索：{value}'**
  String summaryWebSearch(String value);

  /// No description provided for @summaryImageView.
  ///
  /// In zh, this message translates to:
  /// **'查看图片：{value}'**
  String summaryImageView(String value);

  /// No description provided for @summarySleep.
  ///
  /// In zh, this message translates to:
  /// **'等待：{value}'**
  String summarySleep(String value);

  /// No description provided for @summaryImageGeneration.
  ///
  /// In zh, this message translates to:
  /// **'生成图片'**
  String get summaryImageGeneration;

  /// No description provided for @summaryEnteredReviewMode.
  ///
  /// In zh, this message translates to:
  /// **'进入审查模式：{value}'**
  String summaryEnteredReviewMode(String value);

  /// No description provided for @summaryExitedReviewMode.
  ///
  /// In zh, this message translates to:
  /// **'退出审查模式：{value}'**
  String summaryExitedReviewMode(String value);

  /// No description provided for @summaryContextCompaction.
  ///
  /// In zh, this message translates to:
  /// **'上下文已压缩'**
  String get summaryContextCompaction;

  /// No description provided for @summaryStatusCompleted.
  ///
  /// In zh, this message translates to:
  /// **'已完成'**
  String get summaryStatusCompleted;

  /// No description provided for @summaryStatusRunning.
  ///
  /// In zh, this message translates to:
  /// **'运行中'**
  String get summaryStatusRunning;

  /// No description provided for @summaryStatusFailed.
  ///
  /// In zh, this message translates to:
  /// **'失败'**
  String get summaryStatusFailed;

  /// No description provided for @externalLinkTitle.
  ///
  /// In zh, this message translates to:
  /// **'打开外部链接？'**
  String get externalLinkTitle;

  /// No description provided for @externalLinkBody.
  ///
  /// In zh, this message translates to:
  /// **'即将打开 {host}。请确认这是你信任的网站。'**
  String externalLinkBody(String host);

  /// No description provided for @unsupportedLink.
  ///
  /// In zh, this message translates to:
  /// **'此链接类型不受支持'**
  String get unsupportedLink;

  /// No description provided for @externalLinkFailed.
  ///
  /// In zh, this message translates to:
  /// **'无法打开此链接'**
  String get externalLinkFailed;

  /// No description provided for @open.
  ///
  /// In zh, this message translates to:
  /// **'打开'**
  String get open;

  /// No description provided for @remoteImageBlocked.
  ///
  /// In zh, this message translates to:
  /// **'远程图片已隐藏'**
  String get remoteImageBlocked;

  /// No description provided for @confirmationRequired.
  ///
  /// In zh, this message translates to:
  /// **'需要你确认'**
  String get confirmationRequired;

  /// No description provided for @pendingActionsRemaining.
  ///
  /// In zh, this message translates to:
  /// **'还有 {count} 个待处理'**
  String pendingActionsRemaining(int count);

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

  /// No description provided for @answerQuestionsTitle.
  ///
  /// In zh, this message translates to:
  /// **'需要你的回答'**
  String get answerQuestionsTitle;

  /// No description provided for @answer.
  ///
  /// In zh, this message translates to:
  /// **'回答'**
  String get answer;

  /// No description provided for @answerHint.
  ///
  /// In zh, this message translates to:
  /// **'输入回答'**
  String get answerHint;

  /// No description provided for @openQuestions.
  ///
  /// In zh, this message translates to:
  /// **'填写回答'**
  String get openQuestions;

  /// No description provided for @questionResponseFailed.
  ///
  /// In zh, this message translates to:
  /// **'提交回答失败：{error}'**
  String questionResponseFailed(String error);

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

  /// No description provided for @addAttachment.
  ///
  /// In zh, this message translates to:
  /// **'添加附件'**
  String get addAttachment;

  /// No description provided for @addMenu.
  ///
  /// In zh, this message translates to:
  /// **'添加'**
  String get addMenu;

  /// No description provided for @uploadPhoto.
  ///
  /// In zh, this message translates to:
  /// **'上传照片'**
  String get uploadPhoto;

  /// No description provided for @planMode.
  ///
  /// In zh, this message translates to:
  /// **'计划模式'**
  String get planMode;

  /// No description provided for @planModeActive.
  ///
  /// In zh, this message translates to:
  /// **'计划模式已开启'**
  String get planModeActive;

  /// No description provided for @permissions.
  ///
  /// In zh, this message translates to:
  /// **'权限'**
  String get permissions;

  /// No description provided for @permissionDefault.
  ///
  /// In zh, this message translates to:
  /// **'默认权限'**
  String get permissionDefault;

  /// No description provided for @permissionDefaultDescription.
  ///
  /// In zh, this message translates to:
  /// **'在工作区沙盒中运行命令'**
  String get permissionDefaultDescription;

  /// No description provided for @permissionAutoReview.
  ///
  /// In zh, this message translates to:
  /// **'自动审核'**
  String get permissionAutoReview;

  /// No description provided for @permissionAutoReviewDescription.
  ///
  /// In zh, this message translates to:
  /// **'自动审查提权请求'**
  String get permissionAutoReviewDescription;

  /// No description provided for @permissionReadOnly.
  ///
  /// In zh, this message translates to:
  /// **'只读'**
  String get permissionReadOnly;

  /// No description provided for @permissionReadOnlyDescription.
  ///
  /// In zh, this message translates to:
  /// **'编辑文件或运行命令需要批准'**
  String get permissionReadOnlyDescription;

  /// No description provided for @permissionFullAccess.
  ///
  /// In zh, this message translates to:
  /// **'完全访问权限'**
  String get permissionFullAccess;

  /// No description provided for @permissionFullAccessDescription.
  ///
  /// In zh, this message translates to:
  /// **'完全访问计算机（风险较高）'**
  String get permissionFullAccessDescription;

  /// No description provided for @permissionUnavailable.
  ///
  /// In zh, this message translates to:
  /// **'当前 Desktop 策略不允许使用'**
  String get permissionUnavailable;

  /// No description provided for @fullAccessTitle.
  ///
  /// In zh, this message translates to:
  /// **'允许完全访问计算机？'**
  String get fullAccessTitle;

  /// No description provided for @fullAccessBody.
  ///
  /// In zh, this message translates to:
  /// **'完全访问会让 Agent 无需批准即可运行命令和修改文件，且不受工作区限制。仅在你信任此任务时继续。'**
  String get fullAccessBody;

  /// No description provided for @confirmFullAccess.
  ///
  /// In zh, this message translates to:
  /// **'允许完全访问'**
  String get confirmFullAccess;

  /// No description provided for @model.
  ///
  /// In zh, this message translates to:
  /// **'模型'**
  String get model;

  /// No description provided for @otherModels.
  ///
  /// In zh, this message translates to:
  /// **'其他模型'**
  String get otherModels;

  /// No description provided for @reasoningEffort.
  ///
  /// In zh, this message translates to:
  /// **'思考等级'**
  String get reasoningEffort;

  /// No description provided for @settingsUnavailable.
  ///
  /// In zh, this message translates to:
  /// **'模型和权限设置加载失败，仍可发送普通文字和图片。'**
  String get settingsUnavailable;

  /// No description provided for @settingsSelectionUnavailable.
  ///
  /// In zh, this message translates to:
  /// **'所选模型、思考等级或权限已不可用，请重新选择后重试。'**
  String get settingsSelectionUnavailable;

  /// No description provided for @retrySettings.
  ///
  /// In zh, this message translates to:
  /// **'重试设置'**
  String get retrySettings;

  /// No description provided for @settingsUpdateFailed.
  ///
  /// In zh, this message translates to:
  /// **'无法更新会话设置：{error}'**
  String settingsUpdateFailed(String error);

  /// No description provided for @settingSmart.
  ///
  /// In zh, this message translates to:
  /// **'智能'**
  String get settingSmart;

  /// No description provided for @settingLow.
  ///
  /// In zh, this message translates to:
  /// **'低'**
  String get settingLow;

  /// No description provided for @settingMedium.
  ///
  /// In zh, this message translates to:
  /// **'中'**
  String get settingMedium;

  /// No description provided for @settingHigh.
  ///
  /// In zh, this message translates to:
  /// **'高'**
  String get settingHigh;

  /// No description provided for @settingXhigh.
  ///
  /// In zh, this message translates to:
  /// **'极高'**
  String get settingXhigh;

  /// No description provided for @settingMax.
  ///
  /// In zh, this message translates to:
  /// **'Max'**
  String get settingMax;

  /// No description provided for @settingUltra.
  ///
  /// In zh, this message translates to:
  /// **'Ultra'**
  String get settingUltra;

  /// No description provided for @imageSelectionLimit.
  ///
  /// In zh, this message translates to:
  /// **'一次最多选择 {count} 张图片'**
  String imageSelectionLimit(int count);

  /// No description provided for @imageSelectionTooLarge.
  ///
  /// In zh, this message translates to:
  /// **'单张图片和图片总大小均不能超过 48 MiB'**
  String get imageSelectionTooLarge;

  /// No description provided for @selectedImage.
  ///
  /// In zh, this message translates to:
  /// **'已选择图片：{name}'**
  String selectedImage(String name);

  /// No description provided for @removeSelectedImage.
  ///
  /// In zh, this message translates to:
  /// **'移除图片：{name}'**
  String removeSelectedImage(String name);

  /// No description provided for @queuedMessage.
  ///
  /// In zh, this message translates to:
  /// **'排队中的消息'**
  String get queuedMessage;

  /// No description provided for @queuedMessageActions.
  ///
  /// In zh, this message translates to:
  /// **'排队消息操作'**
  String get queuedMessageActions;

  /// No description provided for @editQueuedMessage.
  ///
  /// In zh, this message translates to:
  /// **'编辑消息'**
  String get editQueuedMessage;

  /// No description provided for @guideQueuedMessageNow.
  ///
  /// In zh, this message translates to:
  /// **'立即引导'**
  String get guideQueuedMessageNow;

  /// No description provided for @cancelQueuedMessage.
  ///
  /// In zh, this message translates to:
  /// **'取消发送'**
  String get cancelQueuedMessage;

  /// No description provided for @queuedAttachmentCount.
  ///
  /// In zh, this message translates to:
  /// **'{count} 个附件'**
  String queuedAttachmentCount(int count);

  /// No description provided for @queueOperationFailed.
  ///
  /// In zh, this message translates to:
  /// **'无法更新排队消息：{error}'**
  String queueOperationFailed(String error);

  /// No description provided for @currentDraftMustBeEmpty.
  ///
  /// In zh, this message translates to:
  /// **'请先发送或清空当前输入内容，再编辑排队消息'**
  String get currentDraftMustBeEmpty;

  /// No description provided for @queuedAttachmentsEditUnavailable.
  ///
  /// In zh, this message translates to:
  /// **'这条排队消息包含已无法从手机恢复的附件'**
  String get queuedAttachmentsEditUnavailable;

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

  /// No description provided for @computer.
  ///
  /// In zh, this message translates to:
  /// **'电脑'**
  String get computer;

  /// No description provided for @back.
  ///
  /// In zh, this message translates to:
  /// **'返回'**
  String get back;

  /// No description provided for @connectionSection.
  ///
  /// In zh, this message translates to:
  /// **'连接'**
  String get connectionSection;

  /// No description provided for @disabled.
  ///
  /// In zh, this message translates to:
  /// **'已禁用'**
  String get disabled;

  /// No description provided for @dragToReorder.
  ///
  /// In zh, this message translates to:
  /// **'长按拖动手柄，然后拖动以重新排序'**
  String get dragToReorder;

  /// No description provided for @addComputerHint.
  ///
  /// In zh, this message translates to:
  /// **'使用二维码与 ChatGPT 桌面版配对'**
  String get addComputerHint;

  /// No description provided for @editComputer.
  ///
  /// In zh, this message translates to:
  /// **'编辑电脑'**
  String get editComputer;

  /// No description provided for @deleteComputer.
  ///
  /// In zh, this message translates to:
  /// **'删除电脑'**
  String get deleteComputer;

  /// No description provided for @deleteComputerConfirm.
  ///
  /// In zh, this message translates to:
  /// **'确定从这部手机删除“{name}”吗？本机的配对信息会被移除；如需再次连接，请重新配对。电脑端授权仍需在电脑上撤销。'**
  String deleteComputerConfirm(String name);

  /// No description provided for @computerDeleted.
  ///
  /// In zh, this message translates to:
  /// **'电脑已删除'**
  String get computerDeleted;

  /// No description provided for @computerDeleteFailed.
  ///
  /// In zh, this message translates to:
  /// **'无法删除电脑，请重试'**
  String get computerDeleteFailed;

  /// No description provided for @save.
  ///
  /// In zh, this message translates to:
  /// **'保存'**
  String get save;

  /// No description provided for @computerName.
  ///
  /// In zh, this message translates to:
  /// **'电脑名称'**
  String get computerName;

  /// No description provided for @relayAddress.
  ///
  /// In zh, this message translates to:
  /// **'Relay 地址'**
  String get relayAddress;

  /// No description provided for @httpRelayWarning.
  ///
  /// In zh, this message translates to:
  /// **'HTTP 连接未加密，请仅在可信网络中使用'**
  String get httpRelayWarning;

  /// No description provided for @invalidComputerName.
  ///
  /// In zh, this message translates to:
  /// **'电脑名称不能为空且不能超过 80 个字符'**
  String get invalidComputerName;

  /// No description provided for @invalidRelayAddress.
  ///
  /// In zh, this message translates to:
  /// **'请输入有效的 HTTP(S) Relay 地址'**
  String get invalidRelayAddress;

  /// No description provided for @computerSaved.
  ///
  /// In zh, this message translates to:
  /// **'电脑设置已保存'**
  String get computerSaved;

  /// No description provided for @computerSavedOffline.
  ///
  /// In zh, this message translates to:
  /// **'设置已保存，但连接失败'**
  String get computerSavedOffline;

  /// No description provided for @computerSaveFailed.
  ///
  /// In zh, this message translates to:
  /// **'无法保存电脑设置'**
  String get computerSaveFailed;

  /// No description provided for @noEnabledComputers.
  ///
  /// In zh, this message translates to:
  /// **'没有启用的电脑'**
  String get noEnabledComputers;

  /// No description provided for @connected.
  ///
  /// In zh, this message translates to:
  /// **'已连接'**
  String get connected;

  /// No description provided for @refresh.
  ///
  /// In zh, this message translates to:
  /// **'刷新'**
  String get refresh;

  /// No description provided for @moreActions.
  ///
  /// In zh, this message translates to:
  /// **'更多操作'**
  String get moreActions;

  /// No description provided for @scrollToBottom.
  ///
  /// In zh, this message translates to:
  /// **'回到底部'**
  String get scrollToBottom;

  /// No description provided for @commandExecution.
  ///
  /// In zh, this message translates to:
  /// **'命令执行'**
  String get commandExecution;

  /// No description provided for @toolActivity.
  ///
  /// In zh, this message translates to:
  /// **'工具调用'**
  String get toolActivity;

  /// No description provided for @reasoningActivity.
  ///
  /// In zh, this message translates to:
  /// **'思考中'**
  String get reasoningActivity;

  /// No description provided for @fileChange.
  ///
  /// In zh, this message translates to:
  /// **'文件更改'**
  String get fileChange;

  /// No description provided for @showDetails.
  ///
  /// In zh, this message translates to:
  /// **'展开详情'**
  String get showDetails;

  /// No description provided for @hideDetails.
  ///
  /// In zh, this message translates to:
  /// **'收起详情'**
  String get hideDetails;

  /// No description provided for @planConfirmationTitle.
  ///
  /// In zh, this message translates to:
  /// **'是否执行此计划？'**
  String get planConfirmationTitle;

  /// No description provided for @executePlan.
  ///
  /// In zh, this message translates to:
  /// **'是，执行此计划'**
  String get executePlan;

  /// No description provided for @executePlanTurnPrompt.
  ///
  /// In zh, this message translates to:
  /// **'执行此计划。'**
  String get executePlanTurnPrompt;

  /// No description provided for @revisePlanHint.
  ///
  /// In zh, this message translates to:
  /// **'否，请说明修改内容'**
  String get revisePlanHint;

  /// No description provided for @skip.
  ///
  /// In zh, this message translates to:
  /// **'跳过'**
  String get skip;

  /// No description provided for @submit.
  ///
  /// In zh, this message translates to:
  /// **'提交'**
  String get submit;

  /// No description provided for @planResponseFailed.
  ///
  /// In zh, this message translates to:
  /// **'计划响应失败：{error}'**
  String planResponseFailed(String error);

  /// No description provided for @openOnComputer.
  ///
  /// In zh, this message translates to:
  /// **'电脑端使用中'**
  String get openOnComputer;

  /// No description provided for @conversationOpenOnComputer.
  ///
  /// In zh, this message translates to:
  /// **'此会话正在电脑上打开，手机端只能查看。在电脑上关闭后即可继续发送。'**
  String get conversationOpenOnComputer;

  /// No description provided for @agentCannotSend.
  ///
  /// In zh, this message translates to:
  /// **'{agent} 当前不支持从手机发送消息。'**
  String agentCannotSend(String agent);
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
