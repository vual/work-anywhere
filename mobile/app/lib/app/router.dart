import 'package:go_router/go_router.dart';

import '../features/conversation/conversation_screen.dart';
import '../features/pairing/pairing_screen.dart';
import '../features/project/project_screen.dart';
import '../features/workspace/workspace_screen.dart';
import '../features/workspace/computer_management_screen.dart';

final appRouter = GoRouter(
  initialLocation: '/',
  routes: [
    GoRoute(path: '/', builder: (context, state) => const WorkspaceGate()),
    GoRoute(path: '/pair', builder: (context, state) => const PairingScreen()),
    GoRoute(
      path: '/computers',
      builder: (context, state) => const ComputerManagementScreen(),
    ),
    GoRoute(
      path: '/project/:id/new',
      builder: (context, state) => ConversationScreen(
        conversationId: null,
        projectId: state.pathParameters['id']!,
        projectName: state.extra as String?,
      ),
    ),
    GoRoute(
      path: '/project/:id',
      builder: (context, state) =>
          ProjectScreen(projectId: state.pathParameters['id']!),
    ),
    GoRoute(
      path: '/conversation/:id',
      builder: (context, state) => ConversationScreen(
        conversationId: state.pathParameters['id']!,
        projectName: state.extra as String?,
      ),
    ),
  ],
);
