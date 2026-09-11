// ignore: unused_import
import 'package:intl/intl.dart' as intl;

import 'app_localizations.dart';

// ignore_for_file: type=lint

/// The translations for Chinese (`zh`).
class AppLocalizationsZh extends AppLocalizations {
  AppLocalizationsZh([String locale = 'zh']) : super(locale);

  @override
  String get appTitle => 'Work Any Where';

  @override
  String get continueOnPhone => '在手机上继续电脑里的工作';

  @override
  String get privacyIntro => '会话内容使用端到端加密，Relay 无法读取正文。';

  @override
  String get scanQr => '扫描二维码';

  @override
  String get scanComputerQr => '扫描电脑上的二维码';

  @override
  String get pastePairLink => '粘贴 waw:// 配对链接';

  @override
  String get invalidPairLink => '这不是有效的 Work Anywhere 配对链接';

  @override
  String get pairFailed =>
      '连接失败。请确认二维码未过期，且 Relay 地址可由手机访问（不能使用 127.0.0.1 或 localhost）';

  @override
  String get waitingDesktop => '等待电脑确认…';

  @override
  String get projects => '项目';

  @override
  String get projectSearchResults => '项目搜索结果';

  @override
  String get recentProjects => '最近 10 个';

  @override
  String resultCount(int count) {
    return '$count 个';
  }

  @override
  String get noProjects => '当前 Agent 没有可显示的项目';

  @override
  String get expandProjects => '展开更多项目';

  @override
  String get recent => '最近';

  @override
  String get conversationSearchResults => '对话搜索结果';

  @override
  String get recentConversations => '最近 30 个';

  @override
  String get noConversations => '还没有对话';

  @override
  String get searchProjectsConversations => '搜索项目和对话';

  @override
  String get secureOnline => '在线 · 安全连接';

  @override
  String get online => '在线';

  @override
  String get connecting => '连接中';

  @override
  String get offline => '离线';

  @override
  String daemonSwitchLabel(String name, String status) {
    return '$name，$status，点击切换电脑';
  }

  @override
  String get addComputer => '添加电脑';

  @override
  String get manageComputers => '管理电脑';

  @override
  String get running => '运行中';

  @override
  String runningCount(int count) {
    return '$count 个运行中';
  }

  @override
  String get loadWorkspaceFailed => '无法载入工作台';

  @override
  String get workspaceSyncFailed => '已连接电脑，但工作台同步失败';

  @override
  String get retry => '重试';

  @override
  String get project => '项目';

  @override
  String get loadProjectFailed => '无法载入项目对话';

  @override
  String get newConversation => '新建对话';

  @override
  String get createConversationFailed => '无法新建对话，请稍后重试';

  @override
  String get createConversationPermissionDenied => '当前配对没有新建对话权限，请重新配对电脑';

  @override
  String get createConversationDesktopFailed => '电脑端暂时无法新建对话，请检查 Desktop 连接';

  @override
  String get showMore => '显示更多';

  @override
  String sendFailed(String error) {
    return '发送失败：$error';
  }

  @override
  String get stopTitle => '停止当前任务？';

  @override
  String get stopBody => '已经产生的输出会被保留。';

  @override
  String get cancel => '取消';

  @override
  String get stop => '停止';

  @override
  String get send => '发送';

  @override
  String get conversation => '对话';

  @override
  String get secureConnection => '安全连接';

  @override
  String get syncFailed => '内容同步失败';

  @override
  String get actionSyncFailed => '待确认操作同步失败，正文仍可查看';

  @override
  String get copyDiagnostics => '复制诊断';

  @override
  String get diagnosticsCopied => '诊断信息已复制';

  @override
  String get retryOlder => '较早内容加载失败，点击重试';

  @override
  String get viewFullContent => '查看完整内容';

  @override
  String get fullContent => '完整内容';

  @override
  String get externalLinkTitle => '打开外部链接？';

  @override
  String externalLinkBody(String host) {
    return '即将打开 $host。请确认这是你信任的网站。';
  }

  @override
  String get unsupportedLink => '此链接类型不受支持';

  @override
  String get externalLinkFailed => '无法打开此链接';

  @override
  String get open => '打开';

  @override
  String get remoteImageBlocked => '远程图片已隐藏';

  @override
  String get confirmationRequired => '需要你确认';

  @override
  String get agentContinueRequest => 'Agent 请求继续执行';

  @override
  String get deny => '拒绝';

  @override
  String get allow => '允许';

  @override
  String get runningStop => '任务运行中 · 停止';

  @override
  String get continueWorking => '在电脑上继续工作';

  @override
  String get addAttachment => '添加附件';

  @override
  String get noTimeline => '暂无可显示的内容';

  @override
  String get justNow => '刚刚';

  @override
  String minutesAgo(int count) {
    return '$count 分钟';
  }

  @override
  String hoursAgo(int count) {
    return '$count 小时';
  }

  @override
  String daysAgo(int count) {
    return '$count 天';
  }

  @override
  String weeksAgo(int count) {
    return '$count 周';
  }
}

/// The translations for Chinese, as used in China (`zh_CN`).
class AppLocalizationsZhCn extends AppLocalizationsZh {
  AppLocalizationsZhCn() : super('zh_CN');
}
