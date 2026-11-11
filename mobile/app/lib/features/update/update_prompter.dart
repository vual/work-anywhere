import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'update_controller.dart';
import 'update_views.dart';

/// App-level foreground hook: runs the throttled automatic check on launch
/// and on resume, shows the update prompt, and resumes an install waiting for
/// the "install unknown apps" grant. It works on every page, including the
/// pairing screen, which has no sidebar.
class UpdatePrompter extends ConsumerStatefulWidget {
  const UpdatePrompter({
    required this.navigatorKey,
    required this.onOpenAbout,
    required this.child,
    super.key,
  });

  final GlobalKey<NavigatorState> navigatorKey;

  /// Shows download progress after the user picks "Update now".
  final VoidCallback onOpenAbout;
  final Widget child;

  @override
  ConsumerState<UpdatePrompter> createState() => _UpdatePrompterState();
}

class _UpdatePrompterState extends ConsumerState<UpdatePrompter> {
  late final AppLifecycleListener _lifecycle;
  bool _prompting = false;

  @override
  void initState() {
    super.initState();
    _lifecycle = AppLifecycleListener(onResume: _onResume);
    WidgetsBinding.instance.addPostFrameCallback((_) => _checkAutomatically());
  }

  @override
  void dispose() {
    _lifecycle.dispose();
    super.dispose();
  }

  void _onResume() {
    unawaited(ref.read(updateProvider.notifier).onResumed());
    unawaited(_checkAutomatically());
  }

  Future<void> _checkAutomatically() async {
    if (_prompting || !mounted) return;
    final controller = ref.read(updateProvider.notifier);
    final release = await controller.checkAutomatically();
    if (release == null || !mounted) return;
    final navigatorContext = widget.navigatorKey.currentContext;
    if (navigatorContext == null || !navigatorContext.mounted) return;
    _prompting = true;
    try {
      final choice = await showUpdatePrompt(navigatorContext, release);
      switch (choice) {
        case UpdatePromptChoice.update:
          widget.onOpenAbout();
          unawaited(controller.startUpdate());
        case UpdatePromptChoice.skip:
          await controller.skip();
        case UpdatePromptChoice.later:
        case null:
          break;
      }
    } finally {
      _prompting = false;
    }
  }

  @override
  Widget build(BuildContext context) => widget.child;
}
