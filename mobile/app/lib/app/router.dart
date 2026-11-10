import 'package:flutter/widgets.dart';
import 'package:go_router/go_router.dart';

import '../features/about/about_screen.dart';
import '../features/conversation/conversation_screen.dart';
import '../features/pairing/pairing_screen.dart';
import '../features/project/project_screen.dart';
import '../features/workspace/workspace_screen.dart';
import '../features/workspace/computer_management_screen.dart';

/// Root navigator, used by app-level overlays such as the update prompt.
final rootNavigatorKey = GlobalKey<NavigatorState>();

final appRouter = GoRouter(
  navigatorKey: rootNavigatorKey,
  initialLocation: '/',
  routes: [
    GoRoute(path: '/', builder: (context, state) => const WorkspaceGate()),
    GoRoute(path: '/pair', builder: (context, state) => const PairingScreen()),
    GoRoute(
      path: '/about',
      builder: (context, state) =>
          AboutScreen(autoCheck: state.uri.queryParameters['check'] == '1'),
    ),
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
