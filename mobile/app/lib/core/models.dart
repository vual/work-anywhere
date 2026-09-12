enum ConnectionPhase { connecting, online, offline }

class DaemonSummary {
  const DaemonSummary({
    required this.id,
    required this.name,
    required this.phase,
  });
  final String id;
  final String name;
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

class PageResult<T> {
  const PageResult(this.items, this.nextCursor);
  final List<T> items;
  final String? nextCursor;
}
