enum ConnectionPhase { connecting, online, offline }

class DaemonSummary {
  const DaemonSummary({
    required this.id,
    required this.name,
    required this.phase,
  });
  final String id;
  final String? name;
  final ConnectionPhase phase;
}

class AgentSummary {
  const AgentSummary({
    required this.id,
    required this.name,
    required this.kind,
    required this.status,
  });
  final String id;
  final String name;
  final String kind;
  final String status;

  factory AgentSummary.fromJson(Map<String, dynamic> json) => AgentSummary(
    id: json['id'] as String,
    name: json['name'] as String? ?? 'Codex',
    kind: json['kind'] as String? ?? 'codex',
    status: json['status'] as String? ?? 'unknown',
  );
}

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
    required this.title,
    required this.status,
    required this.running,
    required this.updatedAt,
  });
  final String id;
  final String agentId;
  final String projectId;
  final String title;
  final String status;
  final bool running;
  final int updatedAt;

  factory ConversationSummary.fromJson(Map<String, dynamic> json) =>
      ConversationSummary(
        id: json['id'] as String,
        agentId: json['agentId'] as String,
        projectId: json['projectId'] as String? ?? '',
        title: json['title'] as String? ?? 'Untitled conversation',
        status: json['status'] as String? ?? 'unknown',
        running: json['running'] as bool? ?? false,
        updatedAt: json['updatedAt'] as int? ?? 0,
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
