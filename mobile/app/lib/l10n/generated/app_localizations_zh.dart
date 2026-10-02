// ignore: unused_import
import 'package:intl/intl.dart' as intl;

import 'app_localizations.dart';

// ignore_for_file: type=lint

/// The translations for Chinese (`zh`).
class AppLocalizationsZh extends AppLocalizations {
  AppLocalizationsZh([String locale = 'zh']) : super(locale);

  @override
  String get conversationChangedRetry => '会话状态已变化，请重试';

  @override
  String get queueUnavailableWait => '此会话暂不支持消息排队，请等待当前任务结束';

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
  String pendingCount(int count) {
    return '$count 个待处理';
  }

  @override
  String get needsAttention => '需要人工处理';

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
  String get plan => '计划';

  @override
  String get viewFullPlan => '查看完整计划';

  @override
  String get planLoadFailed => '完整计划加载失败，请重试';

  @override
  String get viewFullContent => '查看完整内容';

  @override
  String get fullContent => '完整内容';

  @override
  String summaryFunctionCall(String value) {
    return '调用函数：$value';
  }

  @override
  String summaryMcpToolCall(String value) {
    return '调用 MCP：$value';
  }

  @override
  String summaryDynamicToolCall(String value) {
    return '调用工具：$value';
  }

  @override
  String summaryCollabAgentToolCall(String value) {
    return '协作调用：$value';
  }

  @override
  String summarySubAgentActivity(String value) {
    return '子 Agent：$value';
  }

  @override
  String summaryWebSearch(String value) {
    return '搜索：$value';
  }

  @override
  String summaryImageView(String value) {
    return '查看图片：$value';
  }

  @override
  String summarySleep(String value) {
    return '等待：$value';
  }

  @override
  String get summaryImageGeneration => '生成图片';

  @override
  String summaryEnteredReviewMode(String value) {
    return '进入审查模式：$value';
  }

  @override
  String summaryExitedReviewMode(String value) {
    return '退出审查模式：$value';
  }

  @override
  String get summaryContextCompaction => '上下文已压缩';

  @override
  String get summaryStatusCompleted => '已完成';

  @override
  String get summaryStatusRunning => '运行中';

  @override
  String get summaryStatusFailed => '失败';

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
  String get answerQuestionsTitle => '需要你的回答';

  @override
  String get answer => '回答';

  @override
  String get answerHint => '输入回答';

  @override
  String get openQuestions => '填写回答';

  @override
  String questionResponseFailed(String error) {
    return '提交回答失败：$error';
  }

  @override
  String get runningStop => '任务运行中 · 停止';

  @override
  String get continueWorking => '在电脑上继续工作';

  @override
  String get addAttachment => '添加附件';

  @override
  String get addMenu => '添加';

  @override
  String get uploadPhoto => '上传照片';

  @override
  String get planMode => '计划模式';

  @override
  String get planModeActive => '计划模式已开启';

  @override
  String get permissions => '权限';

  @override
  String get permissionDefault => '默认权限';

  @override
  String get permissionDefaultDescription => '在工作区沙盒中运行命令';

  @override
  String get permissionAutoReview => '自动审核';

  @override
  String get permissionAutoReviewDescription => '自动审查提权请求';

  @override
  String get permissionReadOnly => '只读';

  @override
  String get permissionReadOnlyDescription => '编辑文件或运行命令需要批准';

  @override
  String get permissionFullAccess => '完全访问权限';

  @override
  String get permissionFullAccessDescription => '完全访问计算机（风险较高）';

  @override
  String get permissionUnavailable => '当前 Desktop 策略不允许使用';

  @override
  String get fullAccessTitle => '允许完全访问计算机？';

  @override
  String get fullAccessBody => 'Codex 将可在工作区沙盒外运行命令且无需批准。仅在你信任此任务时继续。';

  @override
  String get confirmFullAccess => '允许完全访问';

  @override
  String get model => '模型';

  @override
  String get otherModels => '其他模型';

  @override
  String get reasoningEffort => '思考等级';

  @override
  String get settingsUnavailable => '模型和权限设置加载失败，仍可发送普通文字和图片。';

  @override
  String get retrySettings => '重试设置';

  @override
  String settingsUpdateFailed(String error) {
    return '无法更新会话设置：$error';
  }

  @override
  String get settingSmart => '智能';

  @override
  String get settingLow => '低';

  @override
  String get settingMedium => '中';

  @override
  String get settingHigh => '高';

  @override
  String get settingXhigh => '极高';

  @override
  String get settingMax => 'Max';

  @override
  String get settingUltra => 'Ultra';

  @override
  String imageSelectionLimit(int count) {
    return '一次最多选择 $count 张图片';
  }

  @override
  String get imageSelectionTooLarge => '单张图片和图片总大小均不能超过 48 MiB';

  @override
  String selectedImage(String name) {
    return '已选择图片：$name';
  }

  @override
  String removeSelectedImage(String name) {
    return '移除图片：$name';
  }

  @override
  String get queuedMessage => '排队中的消息';

  @override
  String get queuedMessageActions => '排队消息操作';

  @override
  String get editQueuedMessage => '编辑消息';

  @override
  String get guideQueuedMessageNow => '立即引导';

  @override
  String get cancelQueuedMessage => '取消发送';

  @override
  String queuedAttachmentCount(int count) {
    return '$count 个附件';
  }

  @override
  String queueOperationFailed(String error) {
    return '无法更新排队消息：$error';
  }

  @override
  String get currentDraftMustBeEmpty => '请先发送或清空当前输入内容，再编辑排队消息';

  @override
  String get queuedAttachmentsEditUnavailable => '这条排队消息包含已无法从手机恢复的附件';

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

  @override
  String get computer => '电脑';

  @override
  String get back => '返回';

  @override
  String get connectionSection => '连接';

  @override
  String get disabled => '已禁用';

  @override
  String get dragToReorder => '长按拖动手柄，然后拖动以重新排序';

  @override
  String get addComputerHint => '使用二维码与 ChatGPT 桌面版配对';

  @override
  String get editComputer => '编辑电脑';

  @override
  String get deleteComputer => '删除电脑';

  @override
  String deleteComputerConfirm(String name) {
    return '确定从这部手机删除“$name”吗？本机的配对信息会被移除；如需再次连接，请重新配对。电脑端授权仍需在电脑上撤销。';
  }

  @override
  String get computerDeleted => '电脑已删除';

  @override
  String get computerDeleteFailed => '无法删除电脑，请重试';

  @override
  String get save => '保存';

  @override
  String get computerName => '电脑名称';

  @override
  String get relayAddress => 'Relay 地址';

  @override
  String get httpRelayWarning => 'HTTP 连接未加密，请仅在可信网络中使用';

  @override
  String get invalidComputerName => '电脑名称不能为空且不能超过 80 个字符';

  @override
  String get invalidRelayAddress => '请输入有效的 HTTP(S) Relay 地址';

  @override
  String get computerSaved => '电脑设置已保存';

  @override
  String get computerSavedOffline => '设置已保存，但连接失败';

  @override
  String get computerSaveFailed => '无法保存电脑设置';

  @override
  String get noEnabledComputers => '没有启用的电脑';

  @override
  String get connected => '已连接';

  @override
  String get refresh => '刷新';

  @override
  String get moreActions => '更多操作';

  @override
  String get scrollToBottom => '回到底部';

  @override
  String get commandExecution => '命令执行';

  @override
  String get toolActivity => '工具调用';

  @override
  String get reasoningActivity => '思考中';

  @override
  String get fileChange => '文件更改';

  @override
  String get showDetails => '展开详情';

  @override
  String get hideDetails => '收起详情';

  @override
  String get planConfirmationTitle => '是否执行此计划？';

  @override
  String get executePlan => '是，执行此计划';

  @override
  String get executePlanTurnPrompt => '执行此计划。';

  @override
  String get revisePlanHint => '否，请说明修改内容';

  @override
  String get skip => '跳过';

  @override
  String get submit => '提交';

  @override
  String planResponseFailed(String error) {
    return '计划响应失败：$error';
  }
}

/// The translations for Chinese, as used in China (`zh_CN`).
class AppLocalizationsZhCn extends AppLocalizationsZh {
  AppLocalizationsZhCn() : super('zh_CN');
}
