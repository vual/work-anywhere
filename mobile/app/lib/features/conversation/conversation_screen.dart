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

const _maxImageCount = 10;
const _maxImageBytes = 48 * 1024 * 1024;
const _bottomFollowThreshold = 24.0;

@visibleForTesting
final imagePickerProvider = Provider<ImagePicker>((ref) => ImagePicker());

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
  final List<XFile> _images = [];
  bool _submitting = false;
  bool _selectingImages = false;
  bool _didAutoScrollToBottom = false;
  bool _followScrollScheduled = false;
  int _timelineTailSignature = 0;
  late String? _activeConversationId = widget.conversationId;

  bool get _isDraft => _activeConversationId == null;
  bool get _canSend =>
      !_submitting &&
      !_selectingImages &&
      (_composer.text.trim().isNotEmpty || _images.isNotEmpty);

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
    if (!mounted) return;
    final signature = _tailSignature(_controller?.state.items ?? const []);
    final timelineChanged = signature != _timelineTailSignature;
    _timelineTailSignature = signature;
    final shouldFollow =
        timelineChanged &&
        _didAutoScrollToBottom &&
        _scroll.hasClients &&
        _scroll.position.extentAfter <= _bottomFollowThreshold;

    setState(() {});
    if (shouldFollow) _followTimelineToBottom();
  }

  int _tailSignature(List<Map<String, dynamic>> items) {
    if (items.isEmpty) return 0;
    final tail = items.last;
    final blocks = (tail['blocks'] as List? ?? const [])
        .whereType<Map<String, dynamic>>();
    return Object.hash(
      items.length,
      tail['id'],
      tail['kind'],
      tail['status'],
      Object.hashAll(
        blocks.map(
          (block) => Object.hash(
            block['kind'],
            block['text'],
            block['summaryKind'],
            block['summaryValue'],
            block['truncated'],
            block['contentRef'],
            block['totalBytes'],
          ),
        ),
      ),
    );
  }

  void _followTimelineToBottom() {
    if (_followScrollScheduled) return;
    _followScrollScheduled = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _followScrollScheduled = false;
      if (!mounted || !_scroll.hasClients) return;
      _scroll.jumpTo(_scroll.position.maxScrollExtent);
    });
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

  Future<List<String>> _uploadImages(String clientMessageId) async {
    final conversationId = _activeConversationId;
    if (_images.isEmpty || _agentId == null || conversationId == null) {
      return const [];
    }
    final projectId =
        _controller?.state.metadata?['projectId'] as String? ??
        widget.projectId;
    final attachmentIds = <String>[];
    for (final image in _images) {
      final bytes = await image.readAsBytes();
      String? attachmentId;
      try {
        final begin = await ref.read(coreGatewayProvider).call(
          'attachment.begin',
          {
            'agentId': _agentId,
            'conversationId': conversationId,
            'clientMessageId': clientMessageId,
            if (projectId != null && projectId.isNotEmpty)
              'projectId': projectId,
            'mimeType': _imageMimeType(image),
            'fileName': image.name,
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
        attachmentIds.add(attachmentId);
      } catch (_) {
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
    return attachmentIds;
  }

  String _imageMimeType(XFile image) {
    final supplied = image.mimeType;
    if (supplied != null && supplied.startsWith('image/')) return supplied;
    final lowerName = image.name.toLowerCase();
    if (lowerName.endsWith('.png')) return 'image/png';
    if (lowerName.endsWith('.webp')) return 'image/webp';
    if (lowerName.endsWith('.gif')) return 'image/gif';
    if (lowerName.endsWith('.heic') || lowerName.endsWith('.heif')) {
      return 'image/heic';
    }
    return 'image/jpeg';
  }

  Future<bool> _imagesWithinSizeLimit(List<XFile> images) async {
    var total = 0;
    for (final image in images) {
      final length = await image.length();
      if (length > _maxImageBytes) return false;
      total += length;
      if (total > _maxImageBytes) return false;
    }
    return true;
  }

  void _showComposerMessage(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _pickImages() async {
    if (_submitting || _selectingImages) return;
    final remaining = _maxImageCount - _images.length;
    if (remaining <= 0) {
      _showComposerMessage(context.l10n.imageSelectionLimit(_maxImageCount));
      return;
    }
    setState(() => _selectingImages = true);
    try {
      final selected = await ref
          .read(imagePickerProvider)
          .pickMultiImage(imageQuality: 88, limit: remaining);
      if (!mounted || selected.isEmpty) return;

      final existingPaths = _images.map((image) => image.path).toSet();
      final additions = <XFile>[];
      for (final image in selected) {
        if (existingPaths.add(image.path)) additions.add(image);
      }
      final accepted = additions.take(remaining).toList();
      final candidates = [..._images, ...accepted];
      if (!await _imagesWithinSizeLimit(candidates)) {
        if (mounted) _showComposerMessage(context.l10n.imageSelectionTooLarge);
        return;
      }
      if (!mounted) return;
      setState(() => _images.addAll(accepted));
      if (additions.length > remaining) {
        _showComposerMessage(context.l10n.imageSelectionLimit(_maxImageCount));
      }
    } finally {
      if (mounted) setState(() => _selectingImages = false);
    }
  }

  void _removeImage(XFile image) {
    if (_submitting) return;
    setState(() => _images.removeWhere((item) => item.path == image.path));
  }

  Future<void> _send() async {
    final text = _composer.text.trim();
    if (_submitting || (text.isEmpty && _images.isEmpty)) {
      return;
    }
    setState(() => _submitting = true);
    var turnStartSent = false;
    try {
      if (!await _imagesWithinSizeLimit(_images)) {
        if (mounted) _showComposerMessage(context.l10n.imageSelectionTooLarge);
        return;
      }
      final workspace = await ref.read(workspaceProvider.future);
      final agentId = workspace.selectedAgentId;
      if (agentId == null) {
        throw CoreFault('invalid_params', 'An agent is required');
      }
      final conversationId = await _ensureConversation();
      final clientMessageId = '${DateTime.now().microsecondsSinceEpoch}';
      final attachmentIds = await _uploadImages(clientMessageId);
      turnStartSent = true;
      await ref.read(coreGatewayProvider).call('turn.start', {
        'agentId': agentId,
        'threadId': conversationId,
        'text': text,
        if (attachmentIds.isNotEmpty) 'attachmentIds': attachmentIds,
        'clientMessageId': clientMessageId,
      }, timeoutMilliseconds: 60000);
      _composer.clear();
      setState(_images.clear);
      await _refresh();
    } catch (error) {
      final ambiguous =
          turnStartSent &&
          error is CoreFault &&
          (error.code == 'ambiguous_write' || error.code == 'peer_offline');
      if (ambiguous && await _reconcileAmbiguousTurn()) {
        _composer.clear();
        if (mounted) setState(_images.clear);
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
              style: WawCompact.appBarTitle,
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
                  style: WawCompact.metadata.copyWith(color: Colors.grey),
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
                margin: const EdgeInsets.fromLTRB(14, 6, 14, 0),
                child: Padding(
                  padding: const EdgeInsets.all(12),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Text(
                        context.l10n.confirmationRequired,
                        style: const TextStyle(fontWeight: FontWeight.w700),
                      ),
                      const SizedBox(height: 6),
                      Text(
                        action['method'] as String? ??
                            context.l10n.agentContinueRequest,
                      ),
                      const SizedBox(height: 8),
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
            minimum: const EdgeInsets.fromLTRB(12, 6, 12, 10),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Expanded(
                  child: Container(
                    decoration: BoxDecoration(
                      color: Theme.of(context).cardColor,
                      borderRadius: BorderRadius.circular(24),
                    ),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        if (_images.isNotEmpty)
                          _AttachmentStrip(
                            images: _images,
                            enabled: !_submitting,
                            onRemove: _removeImage,
                          ),
                        TextField(
                          controller: _composer,
                          enabled: !_submitting,
                          minLines: 1,
                          maxLines: 5,
                          style: WawCompact.body,
                          decoration: InputDecoration(
                            filled: false,
                            border: InputBorder.none,
                            enabledBorder: InputBorder.none,
                            focusedBorder: InputBorder.none,
                            contentPadding: const EdgeInsets.symmetric(
                              horizontal: 12,
                              vertical: 10,
                            ),
                            prefixIcon: IconButton(
                              onPressed: _submitting || _selectingImages
                                  ? null
                                  : _pickImages,
                              icon: const Icon(Icons.add_rounded, size: 24),
                              tooltip: context.l10n.addAttachment,
                            ),
                            hintText: context.l10n.continueWorking,
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                IconButton.filled(
                  onPressed: running ? _interrupt : (_canSend ? _send : null),
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
          ),
        ],
      ),
    );
  }
}

class _AttachmentStrip extends StatelessWidget {
  const _AttachmentStrip({
    required this.images,
    required this.enabled,
    required this.onRemove,
  });

  final List<XFile> images;
  final bool enabled;
  final ValueChanged<XFile> onRemove;

  @override
  Widget build(BuildContext context) => SizedBox(
    key: const ValueKey('attachment-thumbnail-strip'),
    height: 72,
    child: ListView.separated(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 0),
      itemCount: images.length,
      separatorBuilder: (_, _) => const SizedBox(width: 8),
      itemBuilder: (context, index) {
        final image = images[index];
        return Semantics(
          label: context.l10n.selectedImage(image.name),
          image: true,
          child: SizedBox.square(
            dimension: 64,
            child: Stack(
              clipBehavior: Clip.none,
              children: [
                Positioned.fill(
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(10),
                    child: Image.file(
                      File(image.path),
                      key: ValueKey('attachment-thumbnail-${image.path}'),
                      fit: BoxFit.cover,
                      errorBuilder: (_, _, _) => ColoredBox(
                        color: Theme.of(context)
                            .colorScheme
                            .surfaceContainerHighest,
                        child: const Icon(Icons.broken_image_outlined),
                      ),
                    ),
                  ),
                ),
                Positioned(
                  top: 2,
                  right: 2,
                  child: Semantics(
                    button: true,
                    label: context.l10n.removeSelectedImage(image.name),
                    child: IconButton.filled(
                      key: ValueKey('remove-attachment-${image.path}'),
                      onPressed: enabled ? () => onRemove(image) : null,
                      padding: EdgeInsets.zero,
                      constraints: const BoxConstraints.tightFor(
                        width: 28,
                        height: 28,
                      ),
                      style: IconButton.styleFrom(
                        backgroundColor: Colors.black87,
                        foregroundColor: Colors.white,
                        disabledBackgroundColor: Colors.black38,
                        disabledForegroundColor: Colors.white70,
                      ),
                      icon: const Icon(Icons.close_rounded, size: 17),
                    ),
                  ),
                ),
              ],
            ),
          ),
        );
      },
    ),
  );
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
      padding: const EdgeInsets.fromLTRB(
        WawCompact.horizontalPadding,
        10,
        WawCompact.horizontalPadding,
        6,
      ),
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
        SvgPicture.asset('assets/waw-app-icon.svg', width: 64, height: 64),
        const SizedBox(height: 14),
        Text(
          projectName,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: WawCompact.appBarTitle,
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
    final locatedBlocks = [
      for (var index = 0; index < blocks.length; index++)
        <String, dynamic>{
          ...blocks[index],
          '_turnId': item['turnId'],
          '_itemId': item['id'],
          '_blockIndex': index,
        },
    ];
    if (kind.toLowerCase() == 'plan') {
      return _PlanTimelineCard(
        block: locatedBlocks.isEmpty
            ? <String, dynamic>{}
            : locatedBlocks.first,
        controller: controller,
      );
    }
    if (locatedBlocks.length == 1 && locatedBlocks.first['kind'] == 'summary') {
      return _TimelineSummaryItem(item: item, block: locatedBlocks.first);
    }
    final user = kind.toLowerCase().contains('user');
    final tool =
        kind.toLowerCase().contains('tool') ||
        kind.toLowerCase().contains('command') ||
        kind.toLowerCase().contains('file');
    final content = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (final block in locatedBlocks) ...[
          SafeMarkdownMessage(data: block['text'] as String? ?? ''),
          if (block['truncated'] == true)
            TextButton.icon(
              onPressed: controller == null
                  ? null
                  : () => _showFullContent(context, controller!, block),
              icon: const Icon(Icons.open_in_full_rounded),
              label: Text(context.l10n.viewFullContent),
            ),
          const SizedBox(height: 6),
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
        margin: const EdgeInsets.only(bottom: 10),
        padding: user
            ? const EdgeInsets.symmetric(horizontal: 14, vertical: 9)
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

class _TimelineSummaryItem extends StatelessWidget {
  const _TimelineSummaryItem({required this.item, required this.block});

  final Map<String, dynamic> item;
  final Map<String, dynamic> block;

  @override
  Widget build(BuildContext context) {
    final status = item['status'] as String?;
    final hasStatus = status != null && status.isNotEmpty;
    final statusColor = _statusColor(status);
    return Padding(
      padding: const EdgeInsets.only(bottom: 10),
      child: Row(
        children: [
          Icon(_statusIcon(status), size: 17, color: statusColor),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              _summaryText(context, block),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: Theme.of(context).textTheme.bodyMedium,
            ),
          ),
          if (hasStatus) ...[
            const SizedBox(width: 8),
            Text(
              _statusText(context, status),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: Theme.of(context).textTheme.bodySmall
                  ?.copyWith(color: statusColor),
            ),
          ],
        ],
      ),
    );
  }

  static String _summaryText(BuildContext context, Map<String, dynamic> block) {
    final value = block['summaryValue'] as String? ?? '';
    switch (block['summaryKind'] as String? ?? '') {
      case 'functionCallOutput':
        return context.l10n.summaryFunctionCall(value);
      case 'mcpToolCall':
        return context.l10n.summaryMcpToolCall(value);
      case 'dynamicToolCall':
        return context.l10n.summaryDynamicToolCall(value);
      case 'collabAgentToolCall':
        return context.l10n.summaryCollabAgentToolCall(value);
      case 'subAgentActivity':
        return context.l10n.summarySubAgentActivity(value);
      case 'webSearch':
        return context.l10n.summaryWebSearch(value);
      case 'imageView':
        return context.l10n.summaryImageView(value);
      case 'sleep':
        return context.l10n.summarySleep(value);
      case 'imageGeneration':
        return context.l10n.summaryImageGeneration;
      case 'enteredReviewMode':
        return context.l10n.summaryEnteredReviewMode(value);
      case 'exitedReviewMode':
        return context.l10n.summaryExitedReviewMode(value);
      case 'contextCompaction':
        return context.l10n.summaryContextCompaction;
      default:
        return block['text'] as String? ?? value;
    }
  }

  static String _statusText(BuildContext context, String status) {
    switch (status.toLowerCase()) {
      case 'completed':
        return context.l10n.summaryStatusCompleted;
      case 'inprogress':
      case 'running':
        return context.l10n.summaryStatusRunning;
      case 'failed':
      case 'error':
      case 'errored':
        return context.l10n.summaryStatusFailed;
      default:
        return status;
    }
  }

  static IconData _statusIcon(String? status) {
    switch (status?.toLowerCase()) {
      case 'failed':
      case 'error':
      case 'errored':
        return Icons.error_outline_rounded;
      case 'inprogress':
      case 'running':
        return Icons.sync_rounded;
      case 'completed':
        return Icons.check_circle_outline_rounded;
      default:
        return Icons.bolt_outlined;
    }
  }

  static Color _statusColor(String? status) {
    switch (status?.toLowerCase()) {
      case 'failed':
      case 'error':
      case 'errored':
        return Colors.redAccent;
      case 'completed':
        return Colors.green;
      default:
        return Colors.grey;
    }
  }
}

class _PlanTimelineCard extends StatelessWidget {
  const _PlanTimelineCard({required this.block, required this.controller});

  final Map<String, dynamic> block;
  final ConversationController? controller;

  @override
  Widget build(BuildContext context) {
    final truncated = block['truncated'] == true;
    return Card(
      margin: const EdgeInsets.only(bottom: 10),
      clipBehavior: Clip.antiAlias,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(14, 10, 14, 6),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.checklist_rounded, size: 20),
                const SizedBox(width: 8),
                Text(
                  context.l10n.plan,
                  style: const TextStyle(fontWeight: FontWeight.w700),
                ),
              ],
            ),
            const SizedBox(height: 6),
            SafeMarkdownMessage(data: block['text'] as String? ?? ''),
            if (truncated)
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton.icon(
                  onPressed: controller == null
                      ? null
                      : () => _showFullPlan(context, controller!, block),
                  icon: const Icon(Icons.expand_more_rounded),
                  label: Text(context.l10n.viewFullPlan),
                ),
              ),
          ],
        ),
      ),
    );
  }

  Future<void> _showFullPlan(
    BuildContext context,
    ConversationController controller,
    Map<String, dynamic> block,
  ) {
    return showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (_) => _PlanContentSheet(controller: controller, block: block),
    );
  }
}

class _PlanContentSheet extends StatefulWidget {
  const _PlanContentSheet({required this.controller, required this.block});

  final ConversationController controller;
  final Map<String, dynamic> block;

  @override
  State<_PlanContentSheet> createState() => _PlanContentSheetState();
}

class _PlanContentSheetState extends State<_PlanContentSheet> {
  late Future<String> _content;

  @override
  void initState() {
    super.initState();
    _content = widget.controller.readFullContent(widget.block);
  }

  void _retry() {
    setState(() {
      _content = widget.controller.readFullContent(widget.block);
    });
  }

  @override
  Widget build(BuildContext context) => SafeArea(
    child: SizedBox(
      height: MediaQuery.sizeOf(context).height * .82,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 0, 20, 16),
        child: FutureBuilder<String>(
          future: _content,
          builder: (context, snapshot) {
            if (snapshot.hasError) {
              return Column(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Text(
                    context.l10n.planLoadFailed,
                    textAlign: TextAlign.center,
                  ),
                  const SizedBox(height: 12),
                  FilledButton.tonal(
                    onPressed: _retry,
                    child: Text(context.l10n.retry),
                  ),
                ],
              );
            }
            if (!snapshot.hasData) {
              return const Center(child: CircularProgressIndicator());
            }
            return SingleChildScrollView(
              padding: const EdgeInsets.only(bottom: 24),
              child: SafeMarkdownMessage(data: snapshot.data!),
            );
          },
        ),
      ),
    ),
  );
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
