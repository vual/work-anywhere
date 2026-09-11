import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_svg/flutter_svg.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

import '../../app/localization.dart';
import '../../app/theme.dart';
import '../../core/core_gateway.dart';
import '../workspace/workspace_controller.dart';
import 'conversation_controller.dart';
import 'markdown_renderer.dart';

class ConversationScreen extends ConsumerStatefulWidget {
  const ConversationScreen({
    required this.conversationId,
    this.projectId,
    this.projectName,
    super.key,
  });

  final String? conversationId;
  final String? projectId;
  final String? projectName;

  @override
  ConsumerState<ConversationScreen> createState() => _ConversationScreenState();
}

class _ConversationScreenState extends ConsumerState<ConversationScreen> {
  final _composer = TextEditingController();
  final _scroll = ScrollController();
  ConversationController? _controller;
  XFile? _image;
  bool _submitting = false;
  bool _didAutoScrollToBottom = false;
  late String? _activeConversationId = widget.conversationId;

  bool get _isDraft => _activeConversationId == null;
  bool get _canSend =>
      !_submitting && (_composer.text.trim().isNotEmpty || _image != null);

  String? get _agentId => ref.read(workspaceProvider).value?.selectedAgentId;

  @override
  void initState() {
    super.initState();
    _composer.addListener(_composerChanged);
    _scroll.addListener(_onScroll);
    Future<void>.microtask(_initializeConversation);
  }

  Future<void> _initializeConversation() async {
    final conversationId = _activeConversationId;
    if (conversationId == null) return;
    try {
      final workspace = await ref.read(workspaceProvider.future);
      if (!mounted) return;
      final agentId = workspace.selectedAgentId;
      if (agentId == null) return;
      final controller = ConversationController(
        gateway: ref.read(coreGatewayProvider),
        agentId: agentId,
        threadId: conversationId,
      )..addListener(_changed);
      setState(() => _controller = controller);
      controller.load().then((_) {
        _scrollToBottomAfterInitialLoad();
        if (mounted) controller.startLiveSync();
      });
    } catch (_) {
      // Workspace errors are surfaced by the workspace screen; leave this
      // view available for a later retry or navigation back to the list.
    }
  }

  @override
  void dispose() {
    _composer.dispose();
    _composer.removeListener(_composerChanged);
    _scroll
      ..removeListener(_onScroll)
      ..dispose();
    _controller
      ?..removeListener(_changed)
      ..dispose();
    super.dispose();
  }

  Future<void> _copyDiagnostics() async {
    final text = ref.read(coreGatewayProvider).exportDiagnostics();
    await Clipboard.setData(ClipboardData(text: text));
    if (mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(context.l10n.diagnosticsCopied)));
    }
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  void _composerChanged() {
    if (mounted) setState(() {});
  }

  Future<void> _refresh() async => _controller?.load();

  Future<String> _ensureConversation() async {
    final existing = _activeConversationId;
    if (existing != null) return existing;
    final agentId = _agentId;
    final projectId = widget.projectId;
    if (agentId == null || projectId == null) {
      throw CoreFault('invalid_params', 'A project and agent are required');
    }
    final response = await ref.read(coreGatewayProvider).call(
      'conversation.create',
      {'agentId': agentId, 'projectId': projectId},
    );
    final result = response['result'] as Map<String, dynamic>? ?? const {};
    final conversationId = result['id'] as String?;
    if (conversationId == null || conversationId.isEmpty) {
      throw CoreFault(
        'invalid_provider_response',
        'The new conversation did not include an id',
      );
    }
    _activeConversationId = conversationId;
    final controller = ConversationController(
      gateway: ref.read(coreGatewayProvider),
      agentId: agentId,
      threadId: conversationId,
    )..addListener(_changed);
    if (mounted) setState(() => _controller = controller);
    controller.startLiveSync();
    return conversationId;
  }

  Future<bool> _reconcileAmbiguousTurn() async {
    final controller = _controller;
    if (controller == null) return false;
    for (final delay in [
      Duration.zero,
      const Duration(milliseconds: 300),
      const Duration(milliseconds: 700),
    ]) {
      if (delay > Duration.zero) await Future<void>.delayed(delay);
      await controller.refreshConversation();
      if (controller.state.metadata?['running'] == true) return true;
    }
    return false;
  }

  void _scrollToBottomAfterInitialLoad() {
    if (_didAutoScrollToBottom || !mounted) return;
    if (_controller?.state.items.isEmpty ?? true) return;

    var framesRemaining = 4;
    void settleScroll(Duration _) {
      if (!mounted || _didAutoScrollToBottom || !_scroll.hasClients) return;
      _scroll.jumpTo(_scroll.position.maxScrollExtent);
      framesRemaining -= 1;
      if (framesRemaining == 0) {
        _didAutoScrollToBottom = true;
      } else {
        WidgetsBinding.instance.addPostFrameCallback(settleScroll);
      }
    }

    WidgetsBinding.instance.addPostFrameCallback(settleScroll);
  }

  void _onScroll() {
    if (_scroll.hasClients && _scroll.position.pixels <= 80) {
      _loadOlder();
    }
  }

  Future<void> _loadOlder() async {
    final controller = _controller;
    if (controller == null ||
        controller.state.loadingOlder ||
        !controller.state.hasOlder) {
      return;
    }
    final beforeExtent = _scroll.hasClients
        ? _scroll.position.maxScrollExtent
        : 0.0;
    final beforeOffset = _scroll.hasClients ? _scroll.offset : 0.0;
    await controller.loadOlder();
    if (!mounted) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scroll.hasClients) {
        final added = _scroll.position.maxScrollExtent - beforeExtent;
        _scroll.jumpTo(
          (beforeOffset + added).clamp(0, _scroll.position.maxScrollExtent),
        );
      }
    });
  }

  Future<String?> _uploadImage(String clientMessageId) async {
    final conversationId = _activeConversationId;
    if (_image == null || _agentId == null || conversationId == null) {
      return null;
    }
    final bytes = await File(_image!.path).readAsBytes();
    final projectId =
        _controller?.state.metadata?['projectId'] as String? ??
        widget.projectId;
    String? attachmentId;
    try {
      final begin = await ref.read(coreGatewayProvider).call(
        'attachment.begin',
        {
          'agentId': _agentId,
          'conversationId': conversationId,
          'clientMessageId': clientMessageId,
          if (projectId != null && projectId.isNotEmpty) 'projectId': projectId,
          'mimeType': _image!.mimeType ?? 'image/jpeg',
          'fileName': _image!.name,
          'size': bytes.length,
        },
      );
      attachmentId =
          (begin['result'] as Map<String, dynamic>)['attachmentId'] as String;
      const chunkSize = 256 * 1024;
      for (var offset = 0; offset < bytes.length; offset += chunkSize) {
        final end = (offset + chunkSize).clamp(0, bytes.length);
        await ref.read(coreGatewayProvider).call('attachment.chunk', {
          'attachmentId': attachmentId,
          'data': base64Encode(bytes.sublist(offset, end)),
        });
      }
      await ref.read(coreGatewayProvider).call('attachment.commit', {
        'attachmentId': attachmentId,
      });
      return attachmentId;
    } catch (error) {
      if (attachmentId != null) {
        try {
          await ref.read(coreGatewayProvider).call('attachment.abort', {
            'attachmentId': attachmentId,
          });
        } catch (_) {
          // The original upload error is more useful to the user.
        }
      }
      rethrow;
    }
  }

  Future<void> _pickImage() async {
    if (_submitting) return;
    final selected = await ImagePicker().pickImage(
      source: ImageSource.gallery,
      imageQuality: 88,
    );
    if (mounted && selected != null) setState(() => _image = selected);
  }

  Future<void> _send() async {
    final text = _composer.text.trim();
    if (_submitting || (text.isEmpty && _image == null)) {
      return;
    }
    setState(() => _submitting = true);
    var turnStartSent = false;
    try {
      final workspace = await ref.read(workspaceProvider.future);
      final agentId = workspace.selectedAgentId;
      if (agentId == null) {
        throw CoreFault('invalid_params', 'An agent is required');
      }
      final conversationId = await _ensureConversation();
      final clientMessageId = '${DateTime.now().microsecondsSinceEpoch}';
      final attachment = await _uploadImage(clientMessageId);
      turnStartSent = true;
      await ref.read(coreGatewayProvider).call('turn.start', {
        'agentId': agentId,
        'threadId': conversationId,
        'text': text,
        if (attachment != null) 'attachmentIds': [attachment],
        'clientMessageId': clientMessageId,
      }, timeoutMilliseconds: 60000);
      _composer.clear();
      setState(() => _image = null);
      await _refresh();
    } catch (error) {
      final ambiguous =
          turnStartSent &&
          error is CoreFault &&
          (error.code == 'ambiguous_write' || error.code == 'peer_offline');
      if (ambiguous && await _reconcileAmbiguousTurn()) {
        _composer.clear();
        if (mounted) setState(() => _image = null);
        return;
      }
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              _isDraft
                  ? _createConversationError(context, error)
                  : context.l10n.sendFailed(error.toString()),
            ),
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  String _createConversationError(BuildContext context, Object error) {
    if (error is CoreFault) {
      switch (error.code) {
        case 'forbidden':
          return context.l10n.createConversationPermissionDenied;
        case 'provider_error':
          return context.l10n.createConversationDesktopFailed;
      }
    }
    return context.l10n.createConversationFailed;
  }

  Future<void> _interrupt() async {
    if (_agentId == null) return;
    final confirmed =
        await showDialog<bool>(
          context: context,
          builder: (context) => AlertDialog(
            title: Text(context.l10n.stopTitle),
            content: Text(context.l10n.stopBody),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: Text(context.l10n.cancel),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: Text(context.l10n.stop),
              ),
            ],
          ),
        ) ??
        false;
    if (!confirmed) return;
    await ref.read(coreGatewayProvider).call('turn.interrupt', {
      'agentId': _agentId,
      'threadId': _activeConversationId,
    });
    await _refresh();
  }

  Future<void> _resolve(Map<String, dynamic> action, bool approved) async {
    final result = approved ? {'decision': 'accept'} : {'decision': 'decline'};
    try {
      await ref.read(coreGatewayProvider).call('action.resolve', {
        'agentId': _agentId,
        'id': action['id'],
        'result': result,
      });
      await _refresh();
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('$error')));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final state = _isDraft
        ? const ConversationState()
        : (_controller?.state ?? const ConversationState(loading: true));
    final conversation = state.metadata;
    final title =
        conversation?['title'] as String? ??
        (_isDraft ? context.l10n.newConversation : context.l10n.conversation);
    final running = conversation?['running'] as bool? ?? false;
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              title,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w700),
            ),
            Row(
              children: [
                Container(
                  width: 8,
                  height: 8,
                  decoration: const BoxDecoration(
                    color: wawGreen,
                    shape: BoxShape.circle,
                  ),
                ),
                const SizedBox(width: 6),
                Text(
                  context.l10n.secureConnection,
                  style: const TextStyle(fontSize: 12, color: Colors.grey),
                ),
              ],
            ),
          ],
        ),
        actions: [
          if (!_isDraft)
            IconButton(
              onPressed: _refresh,
              icon: const Icon(Icons.refresh_rounded),
            ),
        ],
      ),
      body: Column(
        children: [
          if (state.loading) const LinearProgressIndicator(minHeight: 2),
          if (state.bodyError != null)
            _ErrorBanner(
              title: context.l10n.syncFailed,
              error: state.bodyError!,
              onRetry: _controller!.refreshConversation,
              onCopyDiagnostics: _copyDiagnostics,
            ),
          if (state.actionError != null)
            _ErrorBanner(
              title: context.l10n.actionSyncFailed,
              error: state.actionError!,
              onRetry: _controller!.refreshActions,
              onCopyDiagnostics: _copyDiagnostics,
            ),
          if (state.actions.isNotEmpty)
            for (final action in state.actions)
              Card(
                margin: const EdgeInsets.fromLTRB(16, 8, 16, 0),
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Text(
                        context.l10n.confirmationRequired,
                        style: const TextStyle(fontWeight: FontWeight.w700),
                      ),
                      const SizedBox(height: 8),
                      Text(
                        action['method'] as String? ??
                            context.l10n.agentContinueRequest,
                      ),
                      const SizedBox(height: 12),
                      Row(
                        mainAxisAlignment: MainAxisAlignment.end,
                        children: [
                          TextButton(
                            onPressed: () => _resolve(action, false),
                            child: Text(context.l10n.deny),
                          ),
                          const SizedBox(width: 8),
                          FilledButton(
                            onPressed: () => _resolve(action, true),
                            child: Text(context.l10n.allow),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ),
          Expanded(
            child: _Timeline(
              items: state.items,
              controller: _controller,
              emptyState: _isDraft
                  ? _DraftConversationEmpty(
                      projectName: widget.projectName ?? context.l10n.project,
                    )
                  : null,
              scrollController: _scroll,
              loadingOlder: state.loadingOlder,
              olderError: state.olderError,
              onRetryOlder: _loadOlder,
            ),
          ),
          SafeArea(
            top: false,
            minimum: const EdgeInsets.fromLTRB(16, 8, 16, 14),
            child: Column(
              children: [
                if (_image != null)
                  ListTile(
                    contentPadding: EdgeInsets.zero,
                    leading: const Icon(Icons.image_outlined),
                    title: Text(_image!.name),
                    trailing: IconButton(
                      onPressed: () => setState(() => _image = null),
                      icon: const Icon(Icons.close_rounded),
                    ),
                  ),
                Row(
                  children: [
                    Expanded(
                      child: TextField(
                        controller: _composer,
                        enabled: !_submitting,
                        minLines: 1,
                        maxLines: 5,
                        decoration: InputDecoration(
                          prefixIcon: IconButton(
                            onPressed: _submitting ? null : _pickImage,
                            icon: const Icon(Icons.add_rounded),
                            tooltip: context.l10n.addAttachment,
                          ),
                          hintText: context.l10n.continueWorking,
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    IconButton.filled(
                      onPressed: running
                          ? _interrupt
                          : (_canSend ? _send : null),
                      tooltip: running ? context.l10n.stop : context.l10n.send,
                      style: IconButton.styleFrom(
                        backgroundColor: running ? Colors.black : wawBlue,
                        foregroundColor: Colors.white,
                      ),
                      icon: running
                          ? const Icon(Icons.stop_rounded)
                          : _submitting
                          ? const SizedBox.square(
                              dimension: 18,
                              child: CircularProgressIndicator(
                                strokeWidth: 2,
                                color: Colors.white,
                              ),
                            )
                          : const Icon(Icons.arrow_upward_rounded),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _Timeline extends StatelessWidget {
  const _Timeline({
    required this.items,
    required this.controller,
    this.emptyState,
    required this.scrollController,
    required this.loadingOlder,
    required this.olderError,
    required this.onRetryOlder,
  });

  final List<Map<String, dynamic>> items;
  final ConversationController? controller;
  final Widget? emptyState;
  final ScrollController scrollController;
  final bool loadingOlder;
  final CoreFault? olderError;
  final VoidCallback onRetryOlder;

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty) {
      return emptyState ??
          Center(
            child: Text(
              context.l10n.noTimeline,
              style: const TextStyle(color: Colors.grey),
            ),
          );
    }
    return ListView.builder(
      controller: scrollController,
      padding: const EdgeInsets.all(16),
      itemCount: items.length + 1,
      itemBuilder: (context, index) {
        if (index == 0) {
          if (loadingOlder) {
            return const Padding(
              padding: EdgeInsets.all(12),
              child: Center(child: CircularProgressIndicator()),
            );
          }
          if (olderError != null) {
            return TextButton.icon(
              onPressed: onRetryOlder,
              icon: const Icon(Icons.refresh_rounded),
              label: Text(context.l10n.retryOlder),
            );
          }
          return const SizedBox.shrink();
        }
        return _TimelineItem(item: items[index - 1], controller: controller);
      },
    );
  }
}

class _DraftConversationEmpty extends StatelessWidget {
  const _DraftConversationEmpty({required this.projectName});

  final String projectName;

  @override
  Widget build(BuildContext context) => Center(
    child: Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        SvgPicture.asset('assets/waw-app-icon.svg', width: 72, height: 72),
        const SizedBox(height: 18),
        Text(
          projectName,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700),
        ),
      ],
    ),
  );
}

class _TimelineItem extends StatelessWidget {
  const _TimelineItem({required this.item, required this.controller});
  final Map<String, dynamic> item;
  final ConversationController? controller;

  @override
  Widget build(BuildContext context) {
    final kind = item['kind'] as String? ?? 'item';
    final blocks = (item['blocks'] as List? ?? const [])
        .whereType<Map<String, dynamic>>()
        .toList();
    final user = kind.toLowerCase().contains('user');
    final tool =
        kind.toLowerCase().contains('tool') ||
        kind.toLowerCase().contains('command') ||
        kind.toLowerCase().contains('reasoning') ||
        kind.toLowerCase().contains('file');
    final content = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (final block in blocks) ...[
          SafeMarkdownMessage(data: block['text'] as String? ?? ''),
          if (block['truncated'] == true)
            TextButton.icon(
              onPressed: controller == null
                  ? null
                  : () => _showFullContent(context, controller!, block),
              icon: const Icon(Icons.open_in_full_rounded),
              label: Text(context.l10n.viewFullContent),
            ),
          const SizedBox(height: 8),
        ],
      ],
    );
    if (tool) {
      return ExpansionTile(
        tilePadding: EdgeInsets.zero,
        leading: const Icon(Icons.settings_outlined),
        title: Text(kind),
        children: [content],
      );
    }
    return Align(
      alignment: user ? Alignment.centerRight : Alignment.centerLeft,
      child: Container(
        constraints: const BoxConstraints(maxWidth: 560),
        margin: const EdgeInsets.only(bottom: 14),
        padding: user
            ? const EdgeInsets.symmetric(horizontal: 16, vertical: 12)
            : EdgeInsets.zero,
        decoration: user
            ? BoxDecoration(
                color: wawBlue.withValues(alpha: .12),
                borderRadius: BorderRadius.circular(22),
              )
            : null,
        child: content,
      ),
    );
  }

  Future<void> _showFullContent(
    BuildContext context,
    ConversationController controller,
    Map<String, dynamic> block,
  ) async {
    showDialog<void>(
      context: context,
      builder: (dialogContext) => FutureBuilder<String>(
        future: controller.readFullContent(block),
        builder: (context, snapshot) => AlertDialog(
          title: Text(context.l10n.fullContent),
          content: SizedBox(
            width: double.maxFinite,
            child: snapshot.hasError
                ? SelectableText(snapshot.error.toString())
                : snapshot.hasData
                ? SingleChildScrollView(
                    child: SafeMarkdownMessage(data: snapshot.data!),
                  )
                : const Center(child: CircularProgressIndicator()),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext),
              child: Text(MaterialLocalizations.of(context).closeButtonLabel),
            ),
          ],
        ),
      ),
    );
  }
}

class _ErrorBanner extends StatelessWidget {
  const _ErrorBanner({
    required this.title,
    required this.error,
    required this.onRetry,
    required this.onCopyDiagnostics,
  });

  final String title;
  final CoreFault error;
  final VoidCallback onRetry;
  final VoidCallback onCopyDiagnostics;

  @override
  Widget build(BuildContext context) => MaterialBanner(
    content: ExpansionTile(
      tilePadding: EdgeInsets.zero,
      title: Text(title),
      children: [
        Align(
          alignment: Alignment.centerLeft,
          child: SelectableText(
            'method: ${error.method ?? '-'}\n'
            'code: ${error.code}\n'
            'requestId: ${error.requestId ?? '-'}',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ),
      ],
    ),
    actions: [
      TextButton(
        onPressed: onCopyDiagnostics,
        child: Text(context.l10n.copyDiagnostics),
      ),
      TextButton(onPressed: onRetry, child: Text(context.l10n.retry)),
    ],
  );
}
