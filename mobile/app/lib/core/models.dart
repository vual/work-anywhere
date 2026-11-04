enum ConnectionPhase { connecting, online, offline, disabled }

class DaemonSummary {
  const DaemonSummary({
    required this.id,
    required this.name,
    required this.phase,
    this.relayUrl = '',
    this.enabled = true,
    this.order = 0,
    this.checkedAt,
    this.lastError,
    this.pairingId,
  });
  final String id;
  final String? name;
  final ConnectionPhase phase;
  final String relayUrl;
  final bool enabled;
  final int order;
  final int? checkedAt;
  final String? lastError;
  final String? pairingId;

  factory DaemonSummary.fromJson(Map<String, dynamic> json) => DaemonSummary(
    id: json['daemonId'] as String? ?? json['id'] as String? ?? 'daemon',
    pairingId: json['pairingId'] as String?,
    name: (json['displayName'] as String? ?? json['name'] as String?)?.trim(),
    relayUrl: json['relayUrl'] as String? ?? '',
    enabled: json['enabled'] as bool? ?? true,
    order: (json['order'] as num?)?.toInt() ?? 0,
    checkedAt: (json['checkedAt'] as num?)?.toInt(),
    lastError: json['lastError'] as String?,
    phase: connectionPhaseFromString(
      json['phase'] as String?,
      enabled: json['enabled'] as bool? ?? true,
    ),
  );

  DaemonSummary copyWith({
    String? name,
    String? relayUrl,
    bool? enabled,
    int? order,
    int? checkedAt,
    String? lastError,
    ConnectionPhase? phase,
  }) => DaemonSummary(
    id: id,
    pairingId: pairingId,
    name: name ?? this.name,
    relayUrl: relayUrl ?? this.relayUrl,
    enabled: enabled ?? this.enabled,
    order: order ?? this.order,
    checkedAt: checkedAt ?? this.checkedAt,
    lastError: lastError ?? this.lastError,
    phase: phase ?? this.phase,
  );
}

ConnectionPhase connectionPhaseFromString(
  String? value, {
  bool enabled = true,
}) {
  if (!enabled || value == 'disabled') return ConnectionPhase.disabled;
  return switch (value) {
    'connecting' ||
    'authenticating' ||
    'handshaking' ||
    'syncing' => ConnectionPhase.connecting,
    'online' => ConnectionPhase.online,
    _ => ConnectionPhase.offline,
  };
}

/// Conversation features an agent supports. Agents that omit the field
/// (Codex) support everything the Mobile contract offers.
class AgentCapabilities {
  const AgentCapabilities({
    this.textInput = true,
    this.imageInput = true,
    this.approval = true,
    this.question = true,
    this.interrupt = true,
    this.queue = true,
    this.plan = true,
    this.createConversation = true,
    this.modelSelection = true,
    this.permissionMode = true,
    this.rename = true,
  });

  static const all = AgentCapabilities();

  final bool textInput;
  final bool imageInput;
  final bool approval;
  final bool question;
  final bool interrupt;
  final bool queue;
  final bool plan;
  final bool createConversation;
  final bool modelSelection;
  final bool permissionMode;
  final bool rename;

  factory AgentCapabilities.fromJson(Object? value) {
    if (value is! Map) return all;
    bool flag(String key) => value[key] as bool? ?? false;
    return AgentCapabilities(
      textInput: flag('textInput'),
      imageInput: flag('imageInput'),
      approval: flag('approval'),
      question: flag('question'),
      interrupt: flag('interrupt'),
      queue: flag('queue'),
      plan: flag('plan'),
      createConversation: flag('createConversation'),
      modelSelection: flag('modelSelection'),
      permissionMode: flag('permissionMode'),
      rename: flag('rename'),
    );
  }
}

class AgentSummary {
  const AgentSummary({
    required this.id,
    required this.name,
    required this.kind,
    required this.status,
    this.capabilities = AgentCapabilities.all,
  });
  final String id;
  final String name;
  final String kind;
  final String status;
  final AgentCapabilities capabilities;

  factory AgentSummary.fromJson(Map<String, dynamic> json) {
    final kind = json['kind'] as String? ?? '';
    final name = (json['name'] as String?)?.trim();
    return AgentSummary(
      id: json['id'] as String,
      name: name == null || name.isEmpty ? agentKindName(kind) : name,
      kind: kind,
      status: json['status'] as String? ?? 'unknown',
      capabilities: AgentCapabilities.fromJson(json['capabilities']),
    );
  }
}

/// Product name for a known agent kind, used when Desktop sends no name.
String agentKindName(String kind) => switch (kind) {
  'codex' => 'Codex',
  'claude-code' => 'Claude Code',
  '' => 'Agent',
  _ => kind,
};

class ProjectSummary {
  const ProjectSummary({
    required this.id,
    required this.agentId,
    required this.name,
    required this.updatedAt,
  });
  final String id;
  final String agentId;
  final String name;
  final int updatedAt;

  factory ProjectSummary.fromJson(Map<String, dynamic> json) => ProjectSummary(
    id: json['id'] as String,
    agentId: json['agentId'] as String,
    name: json['name'] as String? ?? 'Untitled project',
    updatedAt: json['updatedAt'] as int? ?? 0,
  );
}

class ConversationSummary {
  const ConversationSummary({
    required this.id,
    required this.agentId,
    required this.projectId,
    this.projectName,
    required this.title,
    required this.status,
    required this.running,
    required this.updatedAt,
    this.needsAttention = false,
    this.externallyActive = false,
  });
  final String id;
  final String agentId;
  final String projectId;
  final String? projectName;
  final String title;
  final String status;
  final bool running;
  final int updatedAt;
  final bool needsAttention;

  /// The conversation is open in another process on the computer; Mobile
  /// shows it read-only.
  final bool externallyActive;

  factory ConversationSummary.fromJson(Map<String, dynamic> json) =>
      ConversationSummary(
        id: json['id'] as String,
        agentId: json['agentId'] as String,
        projectId: json['projectId'] as String? ?? '',
        projectName: json['projectName'] as String?,
        title: json['title'] as String? ?? 'Untitled conversation',
        status: json['status'] as String? ?? 'unknown',
        running: json['running'] as bool? ?? false,
        updatedAt: json['updatedAt'] as int? ?? 0,
        needsAttention: json['needsAttention'] as bool? ?? false,
        externallyActive: json['externallyActive'] as bool? ?? false,
      );

  ConversationSummary withTitle(String value) => ConversationSummary(
    id: id,
    agentId: agentId,
    projectId: projectId,
    projectName: projectName,
    title: value,
    status: status,
    running: running,
    updatedAt: updatedAt,
    needsAttention: needsAttention,
    externallyActive: externallyActive,
  );
}

List<ConversationSummary> conversationSummariesFromResult(
  Map<String, dynamic> result,
) {
  final summaries = <ConversationSummary>[];
  final seenIds = <String>{};
  for (final item in ((result['items'] as List<dynamic>?) ?? const [])) {
    final summary = ConversationSummary.fromJson(item as Map<String, dynamic>);
    if (seenIds.add(summary.id)) summaries.add(summary);
  }
  return List.unmodifiable(summaries);
}

class QueuedSubmission {
  const QueuedSubmission({
    required this.id,
    required this.clientMessageId,
    required this.text,
    required this.attachmentCount,
  });

  final String id;
  final String clientMessageId;
  final String text;
  final int attachmentCount;

  factory QueuedSubmission.fromJson(Map<String, dynamic> json) =>
      QueuedSubmission(
        id: json['id'] as String,
        clientMessageId: json['clientMessageId'] as String? ?? '',
        text: json['text'] as String? ?? '',
        attachmentCount: (json['attachmentCount'] as num?)?.toInt() ?? 0,
      );
}

class PageResult<T> {
  const PageResult(this.items, this.nextCursor);
  final List<T> items;
  final String? nextCursor;
}

class ReasoningEffortOption {
  const ReasoningEffortOption({required this.id, required this.description});
  final String id;
  final String description;

  factory ReasoningEffortOption.fromJson(Map<String, dynamic> json) =>
      ReasoningEffortOption(
        id: json['id'] as String,
        description: json['description'] as String? ?? '',
      );
}

class ModelOption {
  const ModelOption({
    required this.id,
    required this.name,
    required this.description,
    required this.isDefault,
    required this.selectable,
    required this.defaultReasoningEffort,
    required this.reasoningEfforts,
  });
  final String id;
  final String name;
  final String description;
  final bool isDefault;
  final bool selectable;
  final String defaultReasoningEffort;
  final List<ReasoningEffortOption> reasoningEfforts;

  factory ModelOption.fromJson(Map<String, dynamic> json) => ModelOption(
    id: json['id'] as String,
    name: json['name'] as String? ?? json['id'] as String,
    description: json['description'] as String? ?? '',
    isDefault: json['default'] as bool? ?? false,
    selectable: json['selectable'] as bool? ?? false,
    defaultReasoningEffort: json['defaultReasoningEffort'] as String? ?? '',
    reasoningEfforts: (json['reasoningEfforts'] as List? ?? const [])
        .whereType<Map<String, dynamic>>()
        .map(ReasoningEffortOption.fromJson)
        .toList(growable: false),
  );
}

class PermissionModeOption {
  const PermissionModeOption({
    required this.id,
    required this.available,
    required this.reason,
    required this.dangerous,
  });
  final String id;
  final bool available;
  final String reason;
  final bool dangerous;

  factory PermissionModeOption.fromJson(Map<String, dynamic> json) =>
      PermissionModeOption(
        id: json['id'] as String,
        available: json['available'] as bool? ?? false,
        reason: json['reason'] as String? ?? '',
        dangerous: json['dangerous'] as bool? ?? false,
      );
}

class ConversationSettings {
  const ConversationSettings({
    required this.mode,
    required this.modelId,
    required this.reasoningEffort,
    required this.permissionModeId,
  });
  final String mode;
  final String modelId;
  final String reasoningEffort;
  final String permissionModeId;

  factory ConversationSettings.fromJson(Map<String, dynamic> json) =>
      ConversationSettings(
        mode: json['mode'] as String? ?? 'default',
        modelId: json['modelId'] as String? ?? '',
        reasoningEffort: json['reasoningEffort'] as String? ?? '',
        permissionModeId: json['permissionModeId'] as String? ?? 'default',
      );

  Map<String, dynamic> toJson() => {
    'mode': mode,
    'modelId': modelId,
    'reasoningEffort': reasoningEffort,
    'permissionModeId': permissionModeId,
  };

  ConversationSettings copyWith({
    String? mode,
    String? modelId,
    String? reasoningEffort,
    String? permissionModeId,
  }) => ConversationSettings(
    mode: mode ?? this.mode,
    modelId: modelId ?? this.modelId,
    reasoningEffort: reasoningEffort ?? this.reasoningEffort,
    permissionModeId: permissionModeId ?? this.permissionModeId,
  );
}
