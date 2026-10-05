import 'dart:convert';
import 'dart:io';
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_svg/flutter_svg.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../app/chrome.dart';
import '../../app/localization.dart';
import '../../app/theme.dart';
import '../../core/core_gateway.dart';
import '../../core/models.dart';
import '../workspace/workspace_controller.dart';
import 'conversation_controller.dart';
import 'markdown_renderer.dart';
import 'local_submissions.dart';
import 'timeline_message_body.dart';
import 'user_input_dialog.dart';

const _maxImageCount = 10;
const _maxImageBytes = 48 * 1024 * 1024;
const _bottomFollowThreshold = 24.0;
const _showJumpToBottomThreshold = 96.0;
const _conversationToolbarHeight = WawBackButton.toolbarHeight;
const _dismissedPlanConfirmationsKey = 'dismissedPlanConfirmations';

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
  final _composerPanelKey = GlobalKey<_ComposerPanelState>();
  final _planRevision = TextEditingController();
  final _scroll = ScrollController();
  ConversationController? _controller;
  final List<XFile> _images = [];
  late final LocalSubmissions _submissions;
  String? _boundDaemonId;
  String? _boundAgentId;
  int? _boundGeneration;
  String get _draftKey => 'draft:${widget.projectId}';
  SubmissionScope get _submissionScope => (
    _boundDaemonId,
    _boundAgentId ?? _agentId ?? '',
    _activeConversationId ?? _draftKey,
  );
  Map<String, List<XFile>> get _queuedImages =>
      _submissions.queueImages(_submissionScope);
  bool get _sendBusy => _submitting || _submissions.busy(_submissionScope);
  final Set<String> _queueUpdating = {};
  bool _submitting = false;
  bool _selectingImages = false;
  bool _didAutoScrollToBottom = false;
  bool _followScrollScheduled = false;
  bool _showJumpToBottom = false;
  bool _planSubmitting = false;
  bool _catalogLoading = false;
  bool _settingsUpdating = false;
  bool _catalogFailed = false;
  bool _lastOnline = true;
  List<ModelOption> _models = const [];
  List<PermissionModeOption> _permissionModes = const [];
  String _permissionCatalogModelId = '';
  ConversationSettings? _settings;
  bool _planPreferencesLoaded = false;
  final Set<String> _dismissedPlanConfirmations = {};
  final Set<String> _handledPlanConfirmations = {};
  final Set<String> _presentedUserInputActions = {};
  bool _userInputDialogOpen = false;
  int _timelineTailSignature = 0;
  late String? _activeConversationId = widget.conversationId;

  bool get _isDraft => _activeConversationId == null;
  bool get _connectionOnline {
    final controller = _controller;
    if (controller != null) return controller.state.online;
    final workspace = ref.read(workspaceProvider).value;
    final selectedDaemonId = workspace?.selectedDaemonId;
    final daemon = workspace?.daemons
        .where((item) => item.id == selectedDaemonId)
        .firstOrNull;
    return daemon == null || daemon.phase == ConnectionPhase.online;
  }

  bool get _canSend =>
      !_sendBusy &&
      !_selectingImages &&
      !_settingsUpdating &&
      _connectionOnline &&
      (_controller?.state.actions.isEmpty ?? true) &&
      _selectionCurrent &&
      !_submissions
          .records(_submissionScope)
          .any((record) => record.creationUnknown) &&
      (_composer.text.trim().isNotEmpty || _images.isNotEmpty);

  String? get _agentId =>
      _controller?.agentId ??
      ref.read(workspaceProvider).value?.selectedAgentId;

  @override
  void initState() {
    super.initState();
    _submissions = ref.read(localSubmissionsProvider);
    _submissions.addListener(_localSubmissionsChanged);
    _composer.addListener(_composerChanged);
    _planRevision.addListener(_composerChanged);
    _scroll.addListener(_onScroll);
    Future<void>.microtask(_loadDismissedPlanConfirmations);
    Future<void>.microtask(_initializeConversation);
  }

  Future<void> _initializeConversation() async {
    try {
      final workspace = await ref.read(workspaceProvider.future);
      if (!mounted) return;
      final agentId = workspace.selectedAgentId;
      if (agentId == null) return;
      _boundDaemonId = workspace.selectedDaemonId;
      _boundAgentId = agentId;
      _boundGeneration = workspace.eventGeneration;
      _activeConversationId ??= _submissions.draftThreads[_submissionScope];
      await _loadCatalogs(agentId);
      final conversationId = _activeConversationId;
      if (conversationId == null || !mounted) return;
      final controller = ConversationController(
        gateway: ref.read(coreGatewayProvider),
        agentId: agentId,
        threadId: conversationId,
        daemonId: workspace.selectedDaemonId,
        selectionGeneration: workspace.eventGeneration,
        daemonIdProvider: () =>
            ref.read(workspaceProvider).value?.selectedDaemonId,
        selectionGenerationProvider: () =>
            ref.read(workspaceProvider).value?.eventGeneration,
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
    _submissions.removeListener(_localSubmissionsChanged);
    _composer
      ..removeListener(_composerChanged)
      ..dispose();
    _planRevision
      ..removeListener(_composerChanged)
      ..dispose();
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
    _submissions.reconcile(
      _submissionScope,
      _controller?.state.items ?? const [],
      _controller?.state.queuedSubmissions ?? const [],
    );
    final queuedClientIds = _controller?.state.queuedSubmissions
        .map((item) => item.clientMessageId)
        .toSet();
    if (queuedClientIds != null) {
      _queuedImages.removeWhere(
        (clientMessageId, _) => !queuedClientIds.contains(clientMessageId),
      );
    }
    final authoritative = _controller?.settings;
    if (authoritative != null && !_settingsUpdating) {
      _settings = _normalizeSettings(authoritative);
      if (_settings?.modelId.isNotEmpty == true &&
          _permissionCatalogModelId != _settings!.modelId &&
          !_catalogLoading) {
        final agentId = _agentId;
        if (agentId != null) {
          unawaited(_reloadPermissions(agentId, _settings!.modelId));
        }
      }
    }
    final online = _controller?.state.online ?? true;
    if (online && !_lastOnline && !_catalogLoading) {
      final agentId = _agentId;
      if (agentId != null) unawaited(_loadCatalogs(agentId));
    }
    _lastOnline = online;
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
    _scheduleUserInputDialog();
  }

  void _scheduleUserInputDialog() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || _userInputDialogOpen) return;
      final action = (_controller?.state.actions ?? const [])
          .where(
            (item) =>
                item['kind'] == 'userInput' &&
                (item['questions'] as List? ?? const []).isNotEmpty &&
                !_presentedUserInputActions.contains('${item['id']}'),
          )
          .firstOrNull;
      if (action != null) unawaited(_presentUserInputAction(action));
    });
  }

  Future<void> _presentUserInputAction(Map<String, dynamic> action) async {
    if (!mounted || _userInputDialogOpen) return;
    _presentedUserInputActions.add('${action['id']}');
    _userInputDialogOpen = true;
    try {
      final answers = await showUserInputDialog(context, action);
      if (answers != null && mounted) {
        await _resolveUserInput(action, answers);
      }
    } finally {
      _userInputDialogOpen = false;
      _scheduleUserInputDialog();
    }
  }

  ConversationSettings? _normalizeSettings(ConversationSettings? value) {
    if (_models.isEmpty || _permissionModes.isEmpty) return value;
    var model = _models.where((item) => item.id == value?.modelId).firstOrNull;
    model ??= _models
        .where((item) => item.isDefault && item.selectable)
        .firstOrNull;
    model ??= _models.where((item) => item.selectable).firstOrNull;
    if (model == null) return value;
    var effort = value?.reasoningEffort ?? '';
    if (!model.reasoningEfforts.any((item) => item.id == effort)) {
      effort = model.defaultReasoningEffort;
    }
    if (effort.isEmpty && model.reasoningEfforts.isNotEmpty) {
      effort = model.reasoningEfforts.first.id;
    }
    final permission = _permissionModes
        .where((item) => item.id == value?.permissionModeId)
        .firstOrNull;
    final fallbackPermission =
        _permissionModes
            .where((item) => item.id == 'default' && item.available)
            .firstOrNull ??
        _permissionModes.where((item) => item.available).firstOrNull;
    return ConversationSettings(
      mode: value?.mode == 'plan' ? 'plan' : 'default',
      modelId: model.id,
      reasoningEffort: effort,
      permissionModeId: (permission ?? fallbackPermission)?.id ?? 'default',
    );
  }

  Future<void> _loadCatalogs(String agentId) async {
    if (_catalogLoading) return;
    if (mounted) setState(() => _catalogLoading = true);
    try {
      final gateway = ref.read(coreGatewayProvider);
      final modelResponse = await gateway.call('catalog.models', {
        'agentId': agentId,
      });
      final modelResult =
          modelResponse['result'] as Map<String, dynamic>? ?? const {};
      final models = (modelResult['items'] as List? ?? const [])
          .whereType<Map<String, dynamic>>()
          .map(ModelOption.fromJson)
          .toList(growable: false);
      if (models.isEmpty) {
        throw const FormatException('empty catalog');
      }
      final requestedModelId = _controller?.settings?.modelId.isNotEmpty == true
          ? _controller!.settings!.modelId
          : (_settings?.modelId.isNotEmpty == true
                ? _settings!.modelId
                : (models.where((item) => item.isDefault).firstOrNull ??
                          models.first)
                      .id);
      final permissions = await _fetchPermissions(agentId, requestedModelId);
      if (permissions.isEmpty) throw const FormatException('empty catalog');
      if (!mounted) return;
      setState(() {
        _models = models;
        _permissionModes = permissions;
        _permissionCatalogModelId = requestedModelId;
        _catalogFailed = false;
        _settings = _normalizeSettings(_controller?.settings ?? _settings);
      });
    } catch (_) {
      if (mounted) setState(() => _catalogFailed = true);
    } finally {
      if (mounted) setState(() => _catalogLoading = false);
    }
  }

  Future<List<PermissionModeOption>> _fetchPermissions(
    String agentId,
    String modelId,
  ) async {
    final response = await ref.read(coreGatewayProvider).call(
      'catalog.permissionProfiles',
      {'agentId': agentId, 'modelId': modelId},
    );
    final result = response['result'] as Map<String, dynamic>? ?? const {};
    return (result['items'] as List? ?? const [])
        .whereType<Map<String, dynamic>>()
        .map(PermissionModeOption.fromJson)
        .toList(growable: false);
  }

  Future<void> _reloadPermissions(String agentId, String modelId) async {
    if (_catalogLoading) return;
    setState(() => _catalogLoading = true);
    try {
      final permissions = await _fetchPermissions(agentId, modelId);
      if (permissions.isEmpty) throw const FormatException('empty catalog');
      if (!mounted) return;
      setState(() {
        _permissionModes = permissions;
        _permissionCatalogModelId = modelId;
        _catalogFailed = false;
        _settings = _normalizeSettings(_controller?.settings ?? _settings);
      });
    } catch (_) {
      if (mounted) setState(() => _catalogFailed = true);
    } finally {
      if (mounted) setState(() => _catalogLoading = false);
    }
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

  void _localSubmissionsChanged() {
    if (mounted) setState(() {});
  }

  bool get _selectionCurrent {
    final workspace = ref.read(workspaceProvider).value;
    return mounted &&
        workspace?.selectedDaemonId == _boundDaemonId &&
        workspace?.selectedAgentId == _boundAgentId &&
        workspace?.eventGeneration == _boundGeneration;
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
    final selectedWorkspace = ref.read(workspaceProvider).value;
    final selectedDaemon = selectedWorkspace?.selectedDaemonId;
    final selectedGeneration = selectedWorkspace?.eventGeneration;
    final draftScope = _submissionScope;
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
    _submissions.bindThread(draftScope, conversationId);
    if (!mounted ||
        ref.read(workspaceProvider).value?.selectedDaemonId != selectedDaemon ||
        ref.read(workspaceProvider).value?.eventGeneration !=
            selectedGeneration) {
      throw CoreFault('selection_changed', 'The selected conversation changed');
    }
    _activeConversationId = conversationId;
    final controller = ConversationController(
      gateway: ref.read(coreGatewayProvider),
      agentId: agentId,
      threadId: conversationId,
      daemonId: selectedDaemon,
      selectionGeneration: selectedGeneration,
      daemonIdProvider: () =>
          ref.read(workspaceProvider).value?.selectedDaemonId,
      selectionGenerationProvider: () =>
          ref.read(workspaceProvider).value?.eventGeneration,
    )..addListener(_changed);
    if (mounted) setState(() => _controller = controller);
    controller.startLiveSync();
    return conversationId;
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
    final shouldShow =
        _scroll.hasClients &&
        _scroll.position.extentAfter > _showJumpToBottomThreshold;
    if (shouldShow != _showJumpToBottom && mounted) {
      setState(() => _showJumpToBottom = shouldShow);
    }
    if (_scroll.hasClients && _scroll.position.pixels <= 80) {
      _loadOlder();
    }
  }

  Future<void> _loadDismissedPlanConfirmations() async {
    final preferences = await SharedPreferences.getInstance();
    if (!mounted) return;
    setState(() {
      _dismissedPlanConfirmations.addAll(
        preferences.getStringList(_dismissedPlanConfirmationsKey) ?? const [],
      );
      _planPreferencesLoaded = true;
    });
  }

  String? _planDismissalKey(Map<String, dynamic>? confirmation) {
    final planItemId = confirmation?['planItemId'] as String?;
    final threadId = _activeConversationId;
    final workspace = ref.read(workspaceProvider).value;
    final daemonId = workspace?.selectedDaemonId;
    if (planItemId == null || threadId == null || daemonId == null) return null;
    return '$daemonId:$threadId:$planItemId';
  }

  bool _showPlanConfirmation(ConversationState state) {
    if (!_planPreferencesLoaded ||
        state.actions.isNotEmpty ||
        state.planConfirmation == null) {
      return false;
    }
    final key = _planDismissalKey(state.planConfirmation);
    return key != null &&
        !_dismissedPlanConfirmations.contains(key) &&
        !_handledPlanConfirmations.contains(key);
  }

  Future<void> _skipPlan(Map<String, dynamic> confirmation) async {
    final key = _planDismissalKey(confirmation);
    if (key == null) return;
    setState(() {
      _dismissedPlanConfirmations.add(key);
      _planRevision.clear();
    });
    final preferences = await SharedPreferences.getInstance();
    await preferences.setStringList(
      _dismissedPlanConfirmationsKey,
      _dismissedPlanConfirmations.toList(growable: false),
    );
  }

  Future<void> _respondToPlan(
    Map<String, dynamic> confirmation,
    String decision,
  ) async {
    if (_planSubmitting || _agentId == null || _activeConversationId == null) {
      return;
    }
    final text = decision == 'implement'
        ? context.l10n.executePlanTurnPrompt
        : _planRevision.text.trim();
    if (text.isEmpty) return;
    final dismissalKey = _planDismissalKey(confirmation);
    final expectedRevision = (confirmation['revision'] as num?)?.toInt();
    setState(() => _planSubmitting = true);
    try {
      await ref.read(coreGatewayProvider).call('plan.respond', {
        'agentId': _agentId,
        'threadId': _activeConversationId,
        'planItemId': confirmation['planItemId'],
        'expectedRevision': expectedRevision,
        'decision': decision,
        'text': text,
      }, timeoutMilliseconds: 60000);
      _planRevision.clear();
      if (dismissalKey != null) {
        _handledPlanConfirmations.add(dismissalKey);
      }
      await _refresh();
    } catch (error) {
      await _controller?.refreshConversation();
      final refreshed = _controller?.state;
      final currentConfirmation = refreshed?.planConfirmation;
      final accepted =
          error is CoreFault &&
          (error.code == 'ambiguous_write' || error.code == 'peer_offline') &&
          refreshed != null &&
          (refreshed.revision != expectedRevision ||
              currentConfirmation == null ||
              currentConfirmation['planItemId'] != confirmation['planItemId'] ||
              currentConfirmation['turnId'] != confirmation['turnId']);
      if (accepted && dismissalKey != null) {
        _handledPlanConfirmations.add(dismissalKey);
      } else if (mounted) {
        _showComposerMessage(
          error is CoreFault && error.code == 'revision_conflict'
              ? context.l10n.conversationChangedRetry
              : context.l10n.planResponseFailed(error.toString()),
        );
      }
    } finally {
      if (mounted) setState(() => _planSubmitting = false);
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

  Future<List<String>> _uploadImages(LocalSubmission record) async {
    final conversationId = _activeConversationId;
    if (record.images.isEmpty || _agentId == null || conversationId == null) {
      return const [];
    }
    final projectId =
        _controller?.state.metadata?['projectId'] as String? ??
        widget.projectId;
    final gateway = ref.read(coreGatewayProvider);
    final attachmentIds = <String>[];
    record.totalBytes = 0;
    for (final image in record.images) {
      record.totalBytes += await image.length();
    }
    record.uploadedBytes = 0;
    for (var index = 0; index < record.images.length; index++) {
      _assertSubmissionCurrent();
      final image = record.images[index];
      record.imageIndex = index + 1;
      if (record.attachmentIds.containsKey(index)) {
        attachmentIds.add(record.attachmentIds[index]!);
        record.uploadedBytes += await image.length();
        continue;
      }
      final bytes = await image.readAsBytes();
      _assertSubmissionCurrent();
      String? attachmentId;
      try {
        final begin = await gateway.call('attachment.begin', {
          'agentId': _agentId,
          'conversationId': conversationId,
          'clientMessageId': record.clientMessageId,
          if (projectId != null && projectId.isNotEmpty) 'projectId': projectId,
          'mimeType': _imageMimeType(image),
          'fileName': image.name,
          'size': bytes.length,
        });
        attachmentId =
            (begin['result'] as Map<String, dynamic>)['attachmentId'] as String;
        const chunkSize = 256 * 1024;
        for (var offset = 0; offset < bytes.length; offset += chunkSize) {
          _assertSubmissionCurrent();
          final end = (offset + chunkSize).clamp(0, bytes.length);
          await gateway.call('attachment.chunk', {
            'attachmentId': attachmentId,
            'data': base64Encode(bytes.sublist(offset, end)),
          });
          record.uploadedBytes += end - offset;
          _submissions.changed();
        }
        _assertSubmissionCurrent();
        await gateway.call('attachment.commit', {'attachmentId': attachmentId});
        record.attachmentIds[index] = attachmentId;
        attachmentIds.add(attachmentId);
      } catch (_) {
        if (attachmentId != null) {
          try {
            await gateway.call('attachment.abort', {
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
    if (_selectingImages) return;
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
    setState(() => _images.removeWhere((item) => item.path == image.path));
  }

  bool get _canChangeSettings {
    final state = _controller?.state;
    return !_sendBusy &&
        !_settingsUpdating &&
        !_catalogLoading &&
        !_catalogFailed &&
        _connectionOnline &&
        (state?.actions.isEmpty ?? true) &&
        !_showPlanConfirmation(state ?? const ConversationState());
  }

  Future<void> _applySettings(ConversationSettings next) async {
    if (!_canChangeSettings) return;
    if (_isDraft) {
      setState(() => _settings = _normalizeSettings(next));
      return;
    }
    final agentId = _agentId;
    final threadId = _activeConversationId;
    final controller = _controller;
    if (agentId == null || threadId == null || controller == null) return;
    setState(() => _settingsUpdating = true);
    try {
      await controller.requireFreshSnapshot();
      if (!mounted || !controller.isCurrent) return;
      await ref.read(coreGatewayProvider).call('conversation.settings.update', {
        'agentId': agentId,
        'threadId': threadId,
        'expectedRevision': controller.state.revision,
        'settings': next.toJson(),
      });
      await controller.refreshConversation();
      if (mounted && controller.isCurrent) {
        setState(() => _settings = _normalizeSettings(controller.settings));
      }
    } catch (error) {
      if (!mounted || !controller.isCurrent) return;
      await controller.refreshConversation();
      if (mounted && controller.isCurrent) {
        setState(() => _settings = _normalizeSettings(controller.settings));
        _showComposerMessage(
          error is CoreFault && error.code == 'revision_conflict'
              ? context.l10n.conversationChangedRetry
              : context.l10n.settingsUpdateFailed(error.toString()),
        );
      }
    } finally {
      if (mounted) setState(() => _settingsUpdating = false);
    }
  }

  Future<void> _showAddMenu() async {
    if (_selectingImages || !_connectionOnline) {
      return;
    }
    if (_catalogFailed || _settings == null) {
      await _pickImages();
      return;
    }
    await showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      constraints: const BoxConstraints(maxWidth: 520),
      builder: (sheetContext) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(12, 0, 12, 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              ListTile(
                minTileHeight: 56,
                leading: const Icon(Icons.photo_library_outlined),
                title: Text(context.l10n.uploadPhoto),
                onTap: () {
                  Navigator.pop(sheetContext);
                  unawaited(_pickImages());
                },
              ),
              if (_settings != null && !_catalogFailed)
                ListTile(
                  minTileHeight: 56,
                  enabled: _canChangeSettings,
                  leading: const Icon(Icons.rule_rounded),
                  title: Text(context.l10n.planMode),
                  trailing: _settings?.mode == 'plan'
                      ? const Icon(Icons.check_rounded)
                      : null,
                  onTap: !_canChangeSettings
                      ? null
                      : () {
                          Navigator.pop(sheetContext);
                          final current = _settings;
                          if (current != null) {
                            unawaited(
                              _applySettings(
                                current.copyWith(
                                  mode: current.mode == 'plan'
                                      ? 'default'
                                      : 'plan',
                                ),
                              ),
                            );
                          }
                        },
                ),
            ],
          ),
        ),
      ),
    );
  }

  String _permissionTitle(String id) => switch (id) {
    'autoReview' => context.l10n.permissionAutoReview,
    'readOnly' => context.l10n.permissionReadOnly,
    'fullAccess' => context.l10n.permissionFullAccess,
    _ => context.l10n.permissionDefault,
  };

  String _permissionDescription(String id) => switch (id) {
    'autoReview' => context.l10n.permissionAutoReviewDescription,
    'readOnly' => context.l10n.permissionReadOnlyDescription,
    'fullAccess' => context.l10n.permissionFullAccessDescription,
    _ => context.l10n.permissionDefaultDescription,
  };

  IconData _permissionIcon(String id) => switch (id) {
    'autoReview' => Icons.verified_user_outlined,
    'readOnly' => Icons.lock_outline_rounded,
    'fullAccess' => Icons.warning_amber_rounded,
    _ => Icons.pan_tool_alt_outlined,
  };

  Future<void> _showPermissionMenu() async {
    if (!_canChangeSettings) return;
    final selected = await showModalBottomSheet<PermissionModeOption>(
      context: context,
      showDragHandle: true,
      constraints: const BoxConstraints(maxWidth: 520),
      builder: (sheetContext) => SafeArea(
        child: ListView(
          shrinkWrap: true,
          padding: const EdgeInsets.fromLTRB(12, 0, 12, 16),
          children: [
            for (final mode in _permissionModes)
              ListTile(
                minTileHeight: 68,
                enabled: mode.available,
                leading: Icon(
                  _permissionIcon(mode.id),
                  color: mode.dangerous ? Colors.deepOrange : null,
                ),
                title: Text(_permissionTitle(mode.id)),
                subtitle: Text(
                  mode.available
                      ? _permissionDescription(mode.id)
                      : context.l10n.permissionUnavailable,
                ),
                trailing: _settings?.permissionModeId == mode.id
                    ? const Icon(Icons.check_rounded)
                    : null,
                onTap: mode.available
                    ? () => Navigator.pop(sheetContext, mode)
                    : null,
              ),
          ],
        ),
      ),
    );
    if (selected == null ||
        !mounted ||
        selected.id == _settings?.permissionModeId) {
      return;
    }
    if (selected.dangerous) {
      final confirmed =
          await showDialog<bool>(
            context: context,
            builder: (dialogContext) => AlertDialog(
              title: Text(context.l10n.fullAccessTitle),
              content: Text(context.l10n.fullAccessBody),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(dialogContext, false),
                  child: Text(context.l10n.cancel),
                ),
                FilledButton(
                  onPressed: () => Navigator.pop(dialogContext, true),
                  child: Text(context.l10n.confirmFullAccess),
                ),
              ],
            ),
          ) ??
          false;
      if (!confirmed) return;
    }
    final current = _settings;
    if (current != null) {
      await _applySettings(current.copyWith(permissionModeId: selected.id));
    }
  }

  String _effortLabel(String id) => switch (id) {
    'minimal' || 'low' => context.l10n.settingLow,
    'medium' => context.l10n.settingMedium,
    'high' => context.l10n.settingHigh,
    'xhigh' => context.l10n.settingXhigh,
    'max' => context.l10n.settingMax,
    'ultra' => context.l10n.settingUltra,
    _ => context.l10n.settingSmart,
  };

  ModelOption? get _selectedModel =>
      _models.where((item) => item.id == _settings?.modelId).firstOrNull;

  Future<void> _showReasoningMenu() async {
    if (!_canChangeSettings || _settings == null || _selectedModel == null) {
      return;
    }
    final model = _selectedModel!;
    final selected = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      constraints: const BoxConstraints(maxWidth: 520),
      builder: (sheetContext) => SafeArea(
        child: ListView(
          shrinkWrap: true,
          padding: const EdgeInsets.fromLTRB(12, 0, 12, 16),
          children: [
            for (final effort in model.reasoningEfforts)
              ListTile(
                minTileHeight: 52,
                title: Text(_effortLabel(effort.id)),
                trailing: effort.id == _settings!.reasoningEffort
                    ? const Icon(Icons.check_rounded)
                    : null,
                onTap: () => Navigator.pop(sheetContext, effort.id),
              ),
            const Divider(),
            ListTile(
              minTileHeight: 64,
              title: Text(context.l10n.model),
              subtitle: Text(model.name),
              trailing: const Icon(Icons.chevron_right_rounded),
              onTap: () {
                Navigator.pop(sheetContext, '__model__');
              },
            ),
          ],
        ),
      ),
    );
    if (!mounted || selected == null) return;
    if (selected == '__model__') {
      await _showModelMenu();
    } else if (selected != _settings?.reasoningEffort) {
      await _applySettings(_settings!.copyWith(reasoningEffort: selected));
    }
  }

  Future<void> _showModelMenu({bool showAll = false}) async {
    final selectable = _models.where((item) => item.selectable).toList();
    final visible = showAll ? selectable : selectable.take(3).toList();
    final currentModel = _selectedModel;
    if (currentModel != null &&
        !visible.any((item) => item.id == currentModel.id)) {
      visible.add(currentModel);
    }
    final selected = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      constraints: const BoxConstraints(maxWidth: 520),
      builder: (sheetContext) => SafeArea(
        child: ListView(
          shrinkWrap: true,
          padding: const EdgeInsets.fromLTRB(12, 0, 12, 16),
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
              child: Text(
                context.l10n.model,
                style: Theme.of(context).textTheme.titleMedium,
              ),
            ),
            for (final model in visible)
              ListTile(
                minTileHeight: 68,
                enabled: model.selectable,
                title: Text(model.name),
                subtitle: model.description.isEmpty
                    ? null
                    : Text(
                        model.description,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                trailing: model.id == _settings?.modelId
                    ? const Icon(Icons.check_rounded)
                    : null,
                onTap: model.selectable
                    ? () => Navigator.pop(sheetContext, model.id)
                    : null,
              ),
            if (!showAll && selectable.length > 3)
              ListTile(
                minTileHeight: 56,
                title: Text(context.l10n.otherModels),
                trailing: const Icon(Icons.chevron_right_rounded),
                onTap: () => Navigator.pop(sheetContext, '__all__'),
              ),
          ],
        ),
      ),
    );
    if (!mounted || selected == null) return;
    if (selected == '__all__') {
      await _showModelMenu(showAll: true);
      return;
    }
    final model = _models.where((item) => item.id == selected).firstOrNull;
    final current = _settings;
    if (model == null ||
        current == null ||
        !model.selectable ||
        model.id == current.modelId) {
      return;
    }
    var effort = current.reasoningEffort;
    if (!model.reasoningEfforts.any((item) => item.id == effort)) {
      effort = model.defaultReasoningEffort;
    }
    if (effort.isEmpty && model.reasoningEfforts.isNotEmpty) {
      effort = model.reasoningEfforts.first.id;
    }
    final agentId = _agentId;
    if (agentId != null && _permissionCatalogModelId != model.id) {
      await _reloadPermissions(agentId, model.id);
      if (!mounted || _catalogFailed) return;
    }
    var permissionModeId = current.permissionModeId;
    if (!_permissionModes.any(
      (item) => item.id == permissionModeId && item.available,
    )) {
      permissionModeId =
          _permissionModes
              .where((item) => item.id == 'autoReview' && item.available)
              .firstOrNull
              ?.id ??
          _permissionModes.where((item) => item.available).first.id;
    }
    await _applySettings(
      current.copyWith(
        modelId: model.id,
        reasoningEffort: effort,
        permissionModeId: permissionModeId,
      ),
    );
  }

  Future<void> _send() async {
    if (!_canSend) return;
    final record = LocalSubmission(
      scope: _submissionScope,
      text: _composer.text.trim(),
      images: _images,
    );
    _submissions.add(record);
    _composer.clear();
    setState(_images.clear);
    _didAutoScrollToBottom = true;
    _followTimelineToBottom();
    await _submit(record);
  }

  void _assertSubmissionCurrent() {
    if (!_selectionCurrent ||
        (_controller != null && !_controller!.isCurrent)) {
      throw CoreFault('selection_changed', 'The selected conversation changed');
    }
  }

  Future<void> _submit(LocalSubmission record) async {
    if (_sendBusy || !_selectionCurrent || !_connectionOnline) return;
    final imageTooLargeMessage = context.l10n.imageSelectionTooLarge;
    final queueUnavailableMessage = context.l10n.queueUnavailableWait;
    record.busy = true;
    record.phase = SubmissionPhase.syncing;
    record.error = null;
    record.errorCode = null;
    setState(() => _submitting = true);
    _submissions.changed();
    var writeSent = false;
    var createSent = false;
    var accepted = false;
    try {
      if (!await _imagesWithinSizeLimit(record.images)) {
        throw CoreFault('payload_too_large', imageTooLargeMessage);
      }
      _assertSubmissionCurrent();
      createSent = _isDraft;
      final conversationId = await _ensureConversation();
      record.scope = (_boundDaemonId, _boundAgentId!, conversationId);
      createSent = false;
      _assertSubmissionCurrent();
      final controller = _controller!;
      await controller.requireFreshSnapshot();
      _assertSubmissionCurrent();
      if (controller.state.metadata?['running'] == true &&
          !controller.state.queueAvailable) {
        throw CoreFault('queue_unavailable', queueUnavailableMessage);
      }
      record.phase = record.images.isEmpty
          ? SubmissionPhase.sending
          : SubmissionPhase.uploading;
      _submissions.changed();
      final attachmentIds = await _uploadImages(record);
      _assertSubmissionCurrent();
      record.queueWrite = controller.state.metadata?['running'] == true;
      record.phase = SubmissionPhase.sending;
      _submissions.changed();
      writeSent = true;
      final envelope = await ref.read(coreGatewayProvider).call(
        record.queueWrite ? 'conversation.queue.add' : 'turn.start',
        {
          'agentId': _boundAgentId,
          'threadId': conversationId,
          'text': record.text,
          if (attachmentIds.isNotEmpty) 'attachmentIds': attachmentIds,
          'clientMessageId': record.clientMessageId,
          if (!record.queueWrite && _settings != null && !_catalogFailed)
            'settings': _settings!.toJson(),
          if (!record.queueWrite) 'expectedRevision': controller.state.revision,
        },
        timeoutMilliseconds: 60000,
      );
      accepted = true;
      final result = envelope['result'] as Map<String, dynamic>? ?? const {};
      final turn = result['turn'] as Map<String, dynamic>?;
      record.turnId = result['turnId'] as String? ?? turn?['id'] as String?;
      record.queueId = record.queueWrite ? result['id'] as String? : null;
      record.phase = SubmissionPhase.confirming;
      if (record.queueWrite && record.images.isNotEmpty) {
        _submissions.queueImages(record.scope)[record.clientMessageId] =
            record.images;
      }
      _submissions.changed();
      if (_selectionCurrent) {
        // Reconcile even if an event delivered the user item before the response.
        _submissions.reconcile(
          record.scope,
          controller.state.items,
          controller.state.queuedSubmissions,
        );
        await controller.refreshConversation();
        _submissions.reconcile(
          record.scope,
          controller.state.items,
          controller.state.queuedSubmissions,
        );
      }
    } catch (error) {
      if (!record.reconciled) {
        final uncertain =
            (writeSent || createSent) &&
            !accepted &&
            !_definiteRejection(error);
        record.phase = accepted
            ? SubmissionPhase.confirming
            : uncertain
            ? SubmissionPhase.unknown
            : SubmissionPhase.failed;
        record.creationUnknown = uncertain && createSent;
        record.error = error.toString();
        record.errorCode = error is CoreFault ? error.code : null;
        _submissions.changed();
        if (uncertain && _selectionCurrent) await _confirmSubmission(record);
      }
    } finally {
      record.busy = false;
      // Leaving a page may interrupt upload, but never leaves an endless spinner.
      _submissions.changed();
      if (mounted) setState(() => _submitting = false);
    }
  }

  bool _definiteRejection(Object error) =>
      error is CoreFault &&
      {
        'revision_conflict',
        'invalid_params',
        'invalid_selection',
        'selection_unavailable',
        'conversation_busy',
        'queue_unavailable',
        'queue_full',
        'forbidden',
        'scope_missing',
        'payload_too_large',
        'project_not_resolved',
        'unsupported_attachment_type',
      }.contains(error.code);

  Future<void> _confirmSubmission(LocalSubmission record) async {
    if (!_selectionCurrent || _controller == null || record.busy) return;
    record.busy = true;
    _submissions.changed();
    try {
      await _controller!.refreshConversation();
      _submissions.reconcile(
        record.scope,
        _controller!.state.items,
        _controller!.state.queuedSubmissions,
      );
    } catch (error) {
      record.error = error.toString();
      record.errorCode = error is CoreFault ? error.code : null;
      _submissions.changed();
    } finally {
      record.busy = false;
      _submissions.changed();
    }
  }

  Future<void> _retrySubmission(LocalSubmission record) async {
    if (record.phase != SubmissionPhase.failed || _sendBusy) return;
    record.clientMessageId = LocalSubmission.newMessageId();
    record.turnId = null;
    record.queueId = null;
    await _submit(record);
  }

  void _editSubmission(LocalSubmission record) {
    if (record.phase != SubmissionPhase.failed || _sendBusy) return;
    if (_composer.text.isNotEmpty || _images.isNotEmpty) {
      _showComposerMessage(context.l10n.currentDraftMustBeEmpty);
      return;
    }
    _composer.text = record.text;
    setState(() => _images.addAll(record.images));
    _submissions.remove(record);
    _composerPanelKey.currentState?.requestInputFocus();
  }

  Future<void> _editQueuedSubmission(QueuedSubmission item) async {
    if (_composer.text.trim().isNotEmpty || _images.isNotEmpty) {
      _showComposerMessage(context.l10n.currentDraftMustBeEmpty);
      return;
    }
    final cachedImages = _queuedImages[item.clientMessageId];
    if (item.attachmentCount > 0 && cachedImages == null) {
      _showComposerMessage(context.l10n.queuedAttachmentsEditUnavailable);
      return;
    }
    final deleted = await _runQueueOperation(item, 'conversation.queue.delete');
    if (!deleted || !mounted) return;
    _queuedImages.remove(item.clientMessageId);
    _composer.text = item.text;
    _composer.selection = TextSelection.collapsed(offset: item.text.length);
    setState(() {
      _images
        ..clear()
        ..addAll(cachedImages ?? const []);
    });
    _composerPanelKey.currentState?.requestInputFocus();
  }

  Future<void> _guideQueuedSubmission(QueuedSubmission item) async {
    final started = await _runQueueOperation(item, 'conversation.queue.start');
    if (started) _queuedImages.remove(item.clientMessageId);
  }

  Future<void> _cancelQueuedSubmission(QueuedSubmission item) async {
    final deleted = await _runQueueOperation(item, 'conversation.queue.delete');
    if (deleted) _queuedImages.remove(item.clientMessageId);
  }

  Future<bool> _runQueueOperation(QueuedSubmission item, String method) async {
    final agentId = _agentId;
    final threadId = _activeConversationId;
    if (agentId == null ||
        threadId == null ||
        _queueUpdating.contains(item.id)) {
      return false;
    }
    setState(() => _queueUpdating.add(item.id));
    try {
      await ref.read(coreGatewayProvider).call(method, {
        'agentId': agentId,
        'threadId': threadId,
        'queuedSubmissionId': item.id,
      }, timeoutMilliseconds: 60000);
      await _controller?.refreshConversation();
      return true;
    } catch (error) {
      await _controller?.refreshConversation();
      if (mounted) {
        _showComposerMessage(context.l10n.queueOperationFailed('$error'));
      }
      return false;
    } finally {
      if (mounted) setState(() => _queueUpdating.remove(item.id));
    }
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

  Future<void> _showComputerStatus() async {
    final workspace = ref.read(workspaceProvider).value;
    final selectedId = workspace?.selectedDaemonId;
    final daemon = workspace?.daemons
        .where((item) => item.id == selectedId)
        .firstOrNull;
    final daemonOnline =
        daemon?.phase == ConnectionPhase.online ||
        (daemon == null && _stateOnline(_controller?.state));
    await showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (sheetContext) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(24, 0, 24, 24),
          child: ListTile(
            contentPadding: EdgeInsets.zero,
            leading: const Icon(Icons.computer_rounded, size: 28),
            title: Text(daemon?.name ?? context.l10n.computer),
            subtitle: Text(
              daemon == null
                  ? (_stateOnline(_controller?.state)
                        ? context.l10n.online
                        : context.l10n.offline)
                  : _daemonPhaseLabel(context, daemon.phase),
            ),
            trailing: Container(
              width: 10,
              height: 10,
              decoration: BoxDecoration(
                color: daemonOnline ? wawGreen : Colors.grey,
                shape: BoxShape.circle,
              ),
            ),
          ),
        ),
      ),
    );
  }

  bool _stateOnline(ConversationState? state) => state?.online ?? false;

  String _daemonPhaseLabel(BuildContext context, ConnectionPhase phase) =>
      switch (phase) {
        ConnectionPhase.connecting => context.l10n.connecting,
        ConnectionPhase.online => context.l10n.online,
        ConnectionPhase.offline => context.l10n.offline,
        ConnectionPhase.disabled => context.l10n.disabled,
      };

  Future<void> _resolve(Map<String, dynamic> action, bool approved) async {
    final result = approved ? {'decision': 'accept'} : {'decision': 'decline'};
    try {
      await ref.read(coreGatewayProvider).call('action.resolve', {
        'agentId': _agentId,
        'id': action['id'],
        'result': result,
      });
      await _refresh();
      await ref.read(workspaceProvider.notifier).refresh();
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('$error')));
      }
    }
  }

  Future<void> _resolveUserInput(
    Map<String, dynamic> action,
    Map<String, List<String>> answers,
  ) async {
    try {
      await ref.read(coreGatewayProvider).call('action.resolve', {
        'agentId': _agentId,
        'id': action['id'],
        'result': {
          'answers': {
            for (final entry in answers.entries)
              entry.key: {'answers': entry.value},
          },
        },
      });
      await _refresh();
      await ref.read(workspaceProvider.notifier).refresh();
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(context.l10n.questionResponseFailed('$error')),
          ),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final state = _isDraft
        ? const ConversationState()
        : (_controller?.state ?? const ConversationState(loading: true));
    final localRecords = _submissions.records(_submissionScope);
    final conversation = state.metadata;
    final title =
        conversation?['title'] as String? ??
        (_isDraft ? context.l10n.newConversation : context.l10n.conversation);
    final workspace = ref.watch(workspaceProvider).value;
    final summary = workspace?.conversations
        .where((item) => item.id == _activeConversationId)
        .firstOrNull;
    final projectId =
        conversation?['projectId'] as String? ??
        summary?.projectId ??
        widget.projectId;
    final agentId =
        conversation?['agentId'] as String? ?? summary?.agentId ?? _agentId;
    final project = workspace?.projects
        .where((item) => item.id == projectId)
        .firstOrNull;
    final agent = workspace?.agents
        .where((item) => item.id == agentId)
        .firstOrNull;
    final projectName =
        widget.projectName ??
        summary?.projectName ??
        project?.name ??
        context.l10n.project;
    final agentName = agent?.name ?? workspace?.selectedAgent?.name ?? 'Codex';
    final running = conversation?['running'] as bool? ?? false;
    final online = _isDraft ? _connectionOnline : state.online;
    final planConfirmation = _showPlanConfirmation(state)
        ? state.planConfirmation
        : null;
    final timelineTopPadding =
        MediaQuery.paddingOf(context).top + _conversationToolbarHeight + 10;
    return Scaffold(
      extendBodyBehindAppBar: true,
      appBar: AppBar(
        toolbarHeight: _conversationToolbarHeight,
        backgroundColor: Theme.of(context).scaffoldBackgroundColor
            .withValues(alpha: .72),
        leadingWidth: WawBackButton.leadingWidth,
        leading: WawBackButton(
          tooltip: context.l10n.back,
          onPressed: () => Navigator.maybePop(context),
        ),
        titleSpacing: 4,
        title: WawTitleBlock(
          title: title,
          pill: true,
          titleStyle: WawCompact.conversationTitle,
          pillMinHeight: 48,
          pillPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 5),
          subtitle: Semantics(
            label:
                '${online ? context.l10n.secureConnection : context.l10n.offline}, '
                '$projectName, $agentName',
            excludeSemantics: true,
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Container(
                  width: 7,
                  height: 7,
                  decoration: BoxDecoration(
                    color: online ? wawGreen : Colors.grey,
                    shape: BoxShape.circle,
                  ),
                ),
                const SizedBox(width: 5),
                Flexible(
                  child: Text(
                    '$projectName · $agentName',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: WawCompact.metadata.copyWith(
                      color: Theme.of(context).mutedInk,
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
        actions: [
          WawActionPill(
            children: [
              IconButton(
                onPressed: _showComputerStatus,
                tooltip: context.l10n.computer,
                icon: const Icon(Icons.computer_rounded),
              ),
              PopupMenuButton<String>(
                tooltip: context.l10n.moreActions,
                onSelected: (value) {
                  if (value == 'refresh') _refresh();
                },
                itemBuilder: (context) => [
                  PopupMenuItem(
                    value: 'refresh',
                    enabled: !_isDraft,
                    child: Row(
                      children: [
                        const Icon(Icons.refresh_rounded),
                        const SizedBox(width: 10),
                        Text(context.l10n.refresh),
                      ],
                    ),
                  ),
                ],
                icon: const Icon(Icons.more_vert_rounded),
              ),
            ],
          ),
        ],
      ),
      body: Stack(
        children: [
          Column(
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
              if (_catalogFailed)
                Material(
                  color: Theme.of(context).colorScheme.surfaceContainerHigh,
                  child: ListTile(
                    dense: true,
                    title: Text(context.l10n.settingsUnavailable),
                    trailing: TextButton(
                      onPressed: _agentId == null
                          ? null
                          : () => _loadCatalogs(_agentId!),
                      child: Text(context.l10n.retrySettings),
                    ),
                  ),
                ),
              if (state.actions.isNotEmpty)
                for (final action in state.actions)
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 6, 16, 0),
                    child: _ActionConfirmation(
                      action: action,
                      onResolve: (approved) => _resolve(action, approved),
                      onAnswer: () => _presentUserInputAction(action),
                    ),
                  ),
              Expanded(
                child: _Timeline(
                  items: state.items,
                  localRecords: localRecords,
                  localActionsEnabled:
                      online && !_sendBusy && state.actions.isEmpty,
                  onRetry: _retrySubmission,
                  onEdit: _editSubmission,
                  onConfirm: _confirmSubmission,
                  controller: _controller,
                  emptyState: _isDraft
                      ? _DraftConversationEmpty(
                          projectName:
                              widget.projectName ?? context.l10n.project,
                        )
                      : null,
                  scrollController: _scroll,
                  loadingOlder: state.loadingOlder,
                  olderError: state.olderError,
                  onRetryOlder: _loadOlder,
                  topPadding: timelineTopPadding,
                ),
              ),
              if (planConfirmation != null)
                _PlanConfirmationPanel(
                  controller: _planRevision,
                  submitting: _planSubmitting,
                  online: online,
                  onImplement: () =>
                      _respondToPlan(planConfirmation, 'implement'),
                  onRevise: () => _respondToPlan(planConfirmation, 'revise'),
                  onSkip: () => _skipPlan(planConfirmation),
                )
              else ...[
                if (state.queueAvailable && state.queuedSubmissions.isNotEmpty)
                  _QueuedSubmissionList(
                    items: state.queuedSubmissions,
                    online: online,
                    blocked: state.actions.isNotEmpty || _sendBusy,
                    updatingIds: _queueUpdating,
                    onEdit: _editQueuedSubmission,
                    onGuideNow: _guideQueuedSubmission,
                    onCancel: _cancelQueuedSubmission,
                  ),
                _ComposerPanel(
                  key: _composerPanelKey,
                  composer: _composer,
                  images: _images,
                  enabled:
                      online && state.actions.isEmpty && !_settingsUpdating,
                  selectingImages: _selectingImages,
                  running: running,
                  online: online,
                  canSend: _canSend,
                  submitting: _sendBusy,
                  settings: _settings,
                  permissionIcon: _permissionIcon(
                    _settings?.permissionModeId ?? 'default',
                  ),
                  model: _selectedModel,
                  settingsVisible: !_catalogFailed && _settings != null,
                  settingsEnabled: _canChangeSettings,
                  settingsUpdating: _settingsUpdating,
                  onAdd: _showAddMenu,
                  onPermissions: _showPermissionMenu,
                  onModelAndEffort: _showReasoningMenu,
                  onRemoveImage: _removeImage,
                  onSend: _send,
                  onInterrupt: _interrupt,
                ),
              ],
            ],
          ),
          if (_showJumpToBottom)
            Positioned(
              right: 0,
              left: 0,
              bottom: planConfirmation != null ? 190 : 88,
              child: Center(
                child: WawCircleButton(
                  size: 48,
                  icon: Icons.arrow_downward_rounded,
                  tooltip: context.l10n.scrollToBottom,
                  onPressed: () {
                    if (!_scroll.hasClients) return;
                    _scroll.animateTo(
                      _scroll.position.maxScrollExtent,
                      duration: const Duration(milliseconds: 280),
                      curve: Curves.easeOutCubic,
                    );
                  },
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class _ActionConfirmation extends StatelessWidget {
  const _ActionConfirmation({
    required this.action,
    required this.onResolve,
    required this.onAnswer,
  });

  final Map<String, dynamic> action;
  final ValueChanged<bool> onResolve;
  final VoidCallback onAnswer;

  @override
  Widget build(BuildContext context) => WawFloatingSurface(
    padding: const EdgeInsets.all(16),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          action['kind'] == 'userInput'
              ? context.l10n.answerQuestionsTitle
              : context.l10n.confirmationRequired,
          style: const TextStyle(fontWeight: FontWeight.w700),
        ),
        const SizedBox(height: 6),
        Text(
          action['kind'] == 'userInput'
              ? ((action['questions'] as List? ?? const [])
                            .whereType<Map<String, dynamic>>()
                            .firstOrNull?['question']
                        as String? ??
                    context.l10n.agentContinueRequest)
              : action['method'] as String? ??
                    context.l10n.agentContinueRequest,
        ),
        const SizedBox(height: 8),
        if (action['kind'] == 'userInput')
          Align(
            alignment: Alignment.centerRight,
            child: FilledButton(
              onPressed: (action['questions'] as List? ?? const []).isNotEmpty
                  ? onAnswer
                  : null,
              child: Text(context.l10n.openQuestions),
            ),
          )
        else
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              TextButton(
                onPressed: () => onResolve(false),
                child: Text(context.l10n.deny),
              ),
              const SizedBox(width: 8),
              FilledButton.icon(
                onPressed: () => onResolve(true),
                icon: const Icon(Icons.check_rounded),
                label: Text(context.l10n.allow),
              ),
            ],
          ),
      ],
    ),
  );
}

class _PlanConfirmationPanel extends StatelessWidget {
  const _PlanConfirmationPanel({
    required this.controller,
    required this.submitting,
    required this.online,
    required this.onImplement,
    required this.onRevise,
    required this.onSkip,
  });

  final TextEditingController controller;
  final bool submitting;
  final bool online;
  final VoidCallback onImplement;
  final VoidCallback onRevise;
  final VoidCallback onSkip;

  @override
  Widget build(BuildContext context) {
    final hasText = controller.text.trim().isNotEmpty;
    return SafeArea(
      top: false,
      minimum: const EdgeInsets.fromLTRB(16, 6, 16, 10),
      child: WawFloatingSurface(
        padding: const EdgeInsets.fromLTRB(16, 16, 12, 10),
        child: Column(
          key: const ValueKey('plan-confirmation-panel'),
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              context.l10n.planConfirmationTitle,
              style: WawCompact.sectionTitle,
            ),
            const SizedBox(height: 8),
            TextButton(
              key: const ValueKey('implement-plan'),
              onPressed: online && !submitting ? onImplement : null,
              style: TextButton.styleFrom(
                minimumSize: const Size.fromHeight(48),
                alignment: Alignment.centerLeft,
              ),
              child: submitting
                  ? const SizedBox.square(
                      dimension: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : Text(context.l10n.executePlan),
            ),
            Divider(color: Theme.of(context).activityRail, height: 1),
            Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                const Padding(
                  padding: EdgeInsets.only(left: 8, bottom: 14),
                  child: Icon(Icons.edit_outlined, size: 18),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: TextField(
                    key: const ValueKey('plan-revision-input'),
                    controller: controller,
                    enabled: !submitting,
                    minLines: 1,
                    maxLines: 4,
                    decoration: InputDecoration(
                      filled: false,
                      border: InputBorder.none,
                      enabledBorder: InputBorder.none,
                      focusedBorder: InputBorder.none,
                      hintText: context.l10n.revisePlanHint,
                      contentPadding: const EdgeInsets.symmetric(vertical: 12),
                    ),
                  ),
                ),
                TextButton(
                  key: ValueKey(hasText ? 'submit-plan-revision' : 'skip-plan'),
                  onPressed: submitting
                      ? null
                      : hasText
                      ? (online ? onRevise : null)
                      : onSkip,
                  child: Text(
                    hasText ? context.l10n.submit : context.l10n.skip,
                    style: const TextStyle(fontWeight: FontWeight.w700),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _QueuedSubmissionList extends StatelessWidget {
  const _QueuedSubmissionList({
    required this.items,
    required this.online,
    required this.blocked,
    required this.updatingIds,
    required this.onEdit,
    required this.onGuideNow,
    required this.onCancel,
  });

  final List<QueuedSubmission> items;
  final bool online;
  final bool blocked;
  final Set<String> updatingIds;
  final ValueChanged<QueuedSubmission> onEdit;
  final ValueChanged<QueuedSubmission> onGuideNow;
  final ValueChanged<QueuedSubmission> onCancel;

  @override
  Widget build(BuildContext context) => ConstrainedBox(
    constraints: const BoxConstraints(maxHeight: 176),
    child: ListView.separated(
      key: const ValueKey('queued-submission-list'),
      shrinkWrap: true,
      reverse: true,
      padding: const EdgeInsets.fromLTRB(72, 4, 16, 2),
      itemCount: items.length,
      separatorBuilder: (_, _) => const SizedBox(height: 6),
      itemBuilder: (context, index) {
        final item = items[items.length - 1 - index];
        final updating = updatingIds.contains(item.id);
        final menuEnabled = online && !blocked && !updating;
        final attachmentLabel = item.attachmentCount > 0
            ? context.l10n.queuedAttachmentCount(item.attachmentCount)
            : '';
        return Align(
          alignment: Alignment.centerRight,
          child: Material(
            color: Theme.of(context).colorScheme.surface,
            elevation: 1,
            shadowColor: Colors.black26,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(24),
              side: BorderSide(
                color: Theme.of(context).colorScheme.outlineVariant,
              ),
            ),
            child: Semantics(
              label: context.l10n.queuedMessage,
              child: Padding(
                padding: const EdgeInsets.only(left: 16),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(Icons.format_list_bulleted_rounded, size: 18),
                    const SizedBox(width: 8),
                    Flexible(
                      child: Text(
                        item.text.isNotEmpty ? item.text : attachmentLabel,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    if (updating)
                      const Padding(
                        padding: EdgeInsets.symmetric(horizontal: 15),
                        child: SizedBox.square(
                          dimension: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        ),
                      )
                    else
                      PopupMenuButton<String>(
                        key: ValueKey('queued-message-menu-${item.id}'),
                        enabled: menuEnabled,
                        tooltip: context.l10n.queuedMessageActions,
                        constraints: const BoxConstraints(minWidth: 220),
                        onSelected: (value) {
                          switch (value) {
                            case 'edit':
                              onEdit(item);
                              break;
                            case 'guide':
                              onGuideNow(item);
                              break;
                            case 'cancel':
                              onCancel(item);
                              break;
                          }
                        },
                        itemBuilder: (context) => [
                          PopupMenuItem(
                            value: 'edit',
                            child: ListTile(
                              contentPadding: EdgeInsets.zero,
                              leading: const Icon(Icons.edit_outlined),
                              title: Text(context.l10n.editQueuedMessage),
                            ),
                          ),
                          PopupMenuItem(
                            value: 'guide',
                            child: ListTile(
                              contentPadding: EdgeInsets.zero,
                              leading: const Icon(
                                Icons.subdirectory_arrow_left,
                              ),
                              title: Text(context.l10n.guideQueuedMessageNow),
                            ),
                          ),
                          PopupMenuItem(
                            value: 'cancel',
                            child: ListTile(
                              contentPadding: EdgeInsets.zero,
                              leading: Icon(
                                Icons.delete_outline_rounded,
                                color: Theme.of(context).colorScheme.error,
                              ),
                              title: Text(
                                context.l10n.cancelQueuedMessage,
                                style: TextStyle(
                                  color: Theme.of(context).colorScheme.error,
                                ),
                              ),
                            ),
                          ),
                        ],
                        icon: const Icon(Icons.more_horiz_rounded),
                      ),
                  ],
                ),
              ),
            ),
          ),
        );
      },
    ),
  );
}

class _ComposerPanel extends StatefulWidget {
  const _ComposerPanel({
    super.key,
    required this.composer,
    required this.images,
    required this.enabled,
    required this.selectingImages,
    required this.running,
    required this.online,
    required this.canSend,
    required this.submitting,
    required this.settings,
    required this.permissionIcon,
    required this.model,
    required this.settingsVisible,
    required this.settingsEnabled,
    required this.settingsUpdating,
    required this.onAdd,
    required this.onPermissions,
    required this.onModelAndEffort,
    required this.onRemoveImage,
    required this.onSend,
    required this.onInterrupt,
  });

  final TextEditingController composer;
  final List<XFile> images;
  final bool enabled;
  final bool selectingImages;
  final bool running;
  final bool online;
  final bool canSend;
  final bool submitting;
  final ConversationSettings? settings;
  final IconData permissionIcon;
  final ModelOption? model;
  final bool settingsVisible;
  final bool settingsEnabled;
  final bool settingsUpdating;
  final VoidCallback onAdd;
  final VoidCallback onPermissions;
  final VoidCallback onModelAndEffort;
  final ValueChanged<XFile> onRemoveImage;
  final VoidCallback onSend;
  final VoidCallback onInterrupt;

  @override
  State<_ComposerPanel> createState() => _ComposerPanelState();
}

class _ComposerPanelState extends State<_ComposerPanel> {
  final FocusNode _focusNode = FocusNode();
  final GlobalKey _inputKey = GlobalKey();

  @override
  void initState() {
    super.initState();
    _focusNode.addListener(_handleFocusChange);
  }

  @override
  void dispose() {
    _focusNode
      ..removeListener(_handleFocusChange)
      ..dispose();
    super.dispose();
  }

  void _handleFocusChange() {
    if (mounted) setState(() {});
  }

  void requestInputFocus() {
    _focusNode.requestFocus();
  }

  Widget _input(BuildContext context, {required bool compact}) => TextField(
    key: _inputKey,
    controller: widget.composer,
    focusNode: _focusNode,
    enabled: widget.enabled,
    minLines: 1,
    maxLines: compact ? 1 : 5,
    textAlignVertical: TextAlignVertical.center,
    style: WawCompact.body,
    decoration: InputDecoration(
      filled: false,
      border: InputBorder.none,
      enabledBorder: InputBorder.none,
      focusedBorder: InputBorder.none,
      contentPadding: compact
          ? const EdgeInsets.symmetric(horizontal: 4, vertical: 12)
          : const EdgeInsets.fromLTRB(16, 12, 16, 4),
      hintText: context.l10n.continueWorking,
    ),
  );

  Widget _addButton(BuildContext context) => SizedBox(
    width: 48,
    height: 48,
    child: Center(
      child: IconButton(
        constraints: const BoxConstraints.tightFor(width: 32, height: 32),
        padding: EdgeInsets.zero,
        iconSize: 24,
        onPressed: widget.enabled && !widget.selectingImages
            ? widget.onAdd
            : null,
        tooltip: context.l10n.addMenu,
        icon: const Icon(Icons.add_rounded),
      ),
    ),
  );

  Widget _actionButton(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(4, 2, 8, 2),
    child: _CompactActionButton(
      enabled:
          widget.online &&
          !widget.submitting &&
          (widget.canSend || widget.running),
      tooltip: widget.running && !widget.canSend
          ? context.l10n.stop
          : context.l10n.send,
      color: Theme.of(context).colorScheme.onSurface,
      keepEnabledColors: widget.running && !widget.canSend,
      onPressed: widget.canSend
          ? widget.onSend
          : widget.running
          ? widget.onInterrupt
          : null,
      child: widget.submitting
          ? const SizedBox.square(
              dimension: 14,
              child: CircularProgressIndicator(
                strokeWidth: 2,
                color: Colors.white,
              ),
            )
          : widget.running && !widget.canSend
          ? const Icon(Icons.stop_rounded, size: 18)
          : const Icon(Icons.arrow_upward_rounded, size: 18),
    ),
  );

  @override
  Widget build(BuildContext context) {
    final expanded = _focusNode.hasFocus && widget.enabled;
    return SafeArea(
      top: false,
      minimum: const EdgeInsets.fromLTRB(16, 6, 16, 10),
      child: WawFloatingSurface(
        child: AnimatedSize(
          duration: const Duration(milliseconds: 180),
          curve: Curves.easeOutCubic,
          alignment: Alignment.bottomCenter,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (widget.images.isNotEmpty)
                _AttachmentStrip(
                  images: widget.images,
                  enabled: widget.enabled,
                  onRemove: widget.onRemoveImage,
                ),
              if (!expanded)
                Row(
                  key: const ValueKey('composer-collapsed'),
                  children: [
                    _addButton(context),
                    Expanded(child: _input(context, compact: true)),
                    _actionButton(context),
                  ],
                )
              else ...[
                _input(context, compact: false),
                Row(
                  key: const ValueKey('composer-expanded-controls'),
                  children: [
                    _addButton(context),
                    if (widget.settingsVisible)
                      IconButton(
                        constraints: const BoxConstraints(
                          minWidth: 48,
                          minHeight: 48,
                        ),
                        onPressed: widget.settingsEnabled
                            ? widget.onPermissions
                            : null,
                        tooltip: context.l10n.permissions,
                        icon: Icon(widget.permissionIcon, size: 24),
                      ),
                    const Spacer(),
                    if (widget.settingsVisible)
                      Flexible(
                        child: TextButton(
                          onPressed: widget.settingsEnabled
                              ? widget.onModelAndEffort
                              : null,
                          style: TextButton.styleFrom(
                            minimumSize: const Size(48, 48),
                            padding: const EdgeInsets.symmetric(horizontal: 8),
                          ),
                          child: widget.settingsUpdating
                              ? const SizedBox.square(
                                  dimension: 18,
                                  child: CircularProgressIndicator(
                                    strokeWidth: 2,
                                  ),
                                )
                              : Text(
                                  '${widget.settings?.mode == 'plan' ? '${context.l10n.plan} · ' : ''}'
                                  '${_composerModelLabel(widget.model?.name)} · '
                                  '${_composerEffortLabel(context, widget.settings?.reasoningEffort ?? '')}',
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                ),
                        ),
                      ),
                    _actionButton(context),
                  ],
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

String _composerModelLabel(String? name) {
  final value = name?.trim() ?? '';
  return value.replaceFirst(RegExp(r'^GPT-', caseSensitive: false), '');
}

class _CompactActionButton extends StatelessWidget {
  const _CompactActionButton({
    required this.enabled,
    required this.tooltip,
    required this.color,
    required this.onPressed,
    required this.child,
    this.keepEnabledColors = false,
  });

  final bool enabled;
  final String tooltip;
  final Color color;
  final VoidCallback? onPressed;
  final Widget child;
  final bool keepEnabledColors;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final background = enabled || keepEnabledColors
        ? color
        : theme.colorScheme.surfaceContainerHighest;
    return Semantics(
      button: true,
      enabled: enabled,
      label: tooltip,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: enabled ? onPressed : null,
        child: SizedBox(
          width: 48,
          height: 48,
          child: Center(
            child: Tooltip(
              message: tooltip,
              child: IconButton.filled(
                onPressed: enabled ? onPressed : null,
                tooltip: tooltip,
                style: IconButton.styleFrom(
                  minimumSize: const Size.square(32),
                  maximumSize: const Size.square(32),
                  padding: EdgeInsets.zero,
                  backgroundColor: background,
                  foregroundColor: theme.colorScheme.surface,
                  disabledBackgroundColor: background,
                  disabledForegroundColor: keepEnabledColors
                      ? theme.colorScheme.surface
                      : theme.colorScheme.onSurfaceVariant,
                ),
                icon: child,
              ),
            ),
          ),
        ),
      ),
    );
  }
}

String _composerEffortLabel(BuildContext context, String id) => switch (id) {
  'minimal' || 'low' => context.l10n.settingLow,
  'medium' => context.l10n.settingMedium,
  'high' => context.l10n.settingHigh,
  'xhigh' => context.l10n.settingXhigh,
  'max' => context.l10n.settingMax,
  'ultra' => context.l10n.settingUltra,
  _ => context.l10n.settingSmart,
};

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
    required this.localRecords,
    required this.localActionsEnabled,
    required this.onRetry,
    required this.onEdit,
    required this.onConfirm,
    required this.controller,
    this.emptyState,
    required this.scrollController,
    required this.loadingOlder,
    required this.olderError,
    required this.onRetryOlder,
    required this.topPadding,
  });

  final List<Map<String, dynamic>> items;
  final List<LocalSubmission> localRecords;
  final bool localActionsEnabled;
  final ValueChanged<LocalSubmission> onRetry;
  final ValueChanged<LocalSubmission> onEdit;
  final ValueChanged<LocalSubmission> onConfirm;
  final ConversationController? controller;
  final Widget? emptyState;
  final ScrollController scrollController;
  final bool loadingOlder;
  final CoreFault? olderError;
  final VoidCallback onRetryOlder;
  final double topPadding;

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty && localRecords.isEmpty) {
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
        0,
        WawCompact.horizontalPadding,
        6,
      ).copyWith(top: topPadding),
      itemCount: items.length + localRecords.length + 1,
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
        if (index > items.length) {
          final record = localRecords[index - items.length - 1];
          return _LocalSubmissionBubble(
            record: record,
            enabled: localActionsEnabled,
            onRetry: () => onRetry(record),
            onEdit: () => onEdit(record),
            onConfirm: () => onConfirm(record),
          );
        }
        return _TimelineItem(
          key: ValueKey(items[index - 1]['id']),
          item: items[index - 1],
          controller: controller,
        );
      },
    );
  }
}

class _LocalSubmissionBubble extends StatelessWidget {
  const _LocalSubmissionBubble({
    required this.record,
    required this.enabled,
    required this.onRetry,
    required this.onEdit,
    required this.onConfirm,
  });
  final LocalSubmission record;
  final bool enabled;
  final VoidCallback onRetry;
  final VoidCallback onEdit;
  final VoidCallback onConfirm;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final status = switch (record.phase) {
      SubmissionPhase.syncing => l10n.messageSyncing,
      SubmissionPhase.uploading => l10n.messageUploading(
        record.imageIndex,
        record.images.length,
        record.totalBytes == 0
            ? 0
            : (100 * record.uploadedBytes / record.totalBytes).round(),
      ),
      SubmissionPhase.sending => l10n.messageSending,
      SubmissionPhase.confirming => l10n.messageConfirming,
      SubmissionPhase.failed => l10n.messageFailed,
      SubmissionPhase.unknown => l10n.messageUnknown,
    };
    return Align(
      key: ValueKey('local-message-${record.clientMessageId}'),
      alignment: Alignment.centerRight,
      child: FractionallySizedBox(
        widthFactor: .82,
        alignment: Alignment.centerRight,
        child: Container(
          margin: const EdgeInsets.only(bottom: 14),
          padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 12),
          decoration: BoxDecoration(
            color: Theme.of(context).userBubble,
            borderRadius: BorderRadius.circular(24),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (record.text.isNotEmpty)
                SafeMarkdownMessage(data: record.text),
              for (final image in record.images)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 6),
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(12),
                    child: Image.file(
                      File(image.path),
                      fit: BoxFit.contain,
                      cacheWidth: 1024,
                      errorBuilder: (_, _, _) =>
                          const Icon(Icons.broken_image_outlined),
                    ),
                  ),
                ),
              const SizedBox(height: 6),
              Semantics(
                liveRegion: true,
                child: Text(
                  status,
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ),
              if (record.errorCode == 'revision_conflict')
                Text(l10n.conversationChangedRetry),
              if (record.errorCode == 'queue_unavailable')
                Text(l10n.queueUnavailableWait),
              if (record.phase == SubmissionPhase.failed)
                Wrap(
                  children: [
                    TextButton(
                      onPressed: enabled ? onRetry : null,
                      child: Text(l10n.retry),
                    ),
                    TextButton(
                      onPressed: enabled ? onEdit : null,
                      child: Text(l10n.messageEdit),
                    ),
                  ],
                ),
              if (record.phase == SubmissionPhase.unknown ||
                  record.phase == SubmissionPhase.confirming)
                TextButton(
                  onPressed: enabled && !record.creationUnknown
                      ? onConfirm
                      : null,
                  child: Text(l10n.messageCheck),
                ),
              if (record.creationUnknown) Text(l10n.messageCreationUnknown),
            ],
          ),
        ),
      ),
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
  const _TimelineItem({
    required this.item,
    required this.controller,
    super.key,
  });
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
    final user = kind.toLowerCase().contains('user');
    final activity =
        locatedBlocks.any((block) => block['kind'] == 'summary') ||
        kind.toLowerCase().contains('reason') ||
        kind.toLowerCase().contains('tool') ||
        kind.toLowerCase().contains('command') ||
        kind.toLowerCase().contains('file');
    if (activity) {
      return _ActivityTimelineItem(
        item: item,
        blocks: locatedBlocks,
        controller: controller,
      );
    }
    final content = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (final block in locatedBlocks) ...[
          TimelineMessageBody(block: block, controller: controller),
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
    return Align(
      alignment: user ? Alignment.centerRight : Alignment.centerLeft,
      child: FractionallySizedBox(
        widthFactor: user ? .82 : 1,
        alignment: user ? Alignment.centerRight : Alignment.centerLeft,
        child: Container(
          constraints: const BoxConstraints(maxWidth: 680),
          margin: const EdgeInsets.only(bottom: 14),
          padding: user
              ? const EdgeInsets.symmetric(horizontal: 18, vertical: 12)
              : EdgeInsets.zero,
          decoration: user
              ? BoxDecoration(
                  color: Theme.of(context).userBubble,
                  borderRadius: BorderRadius.circular(24),
                )
              : null,
          child: content,
        ),
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
                    child: InlineImageMessage(data: snapshot.data!),
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

class _ActivityTimelineItem extends StatefulWidget {
  const _ActivityTimelineItem({
    required this.item,
    required this.blocks,
    required this.controller,
  });

  final Map<String, dynamic> item;
  final List<Map<String, dynamic>> blocks;
  final ConversationController? controller;

  @override
  State<_ActivityTimelineItem> createState() => _ActivityTimelineItemState();
}

class _ActivityTimelineItemState extends State<_ActivityTimelineItem> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final kind = (widget.item['kind'] as String? ?? '').toLowerCase();
    final summary = widget.blocks
        .where((block) => block['kind'] == 'summary')
        .firstOrNull;
    final detailBlocks = widget.blocks
        .where((block) => block['kind'] != 'summary')
        .toList(growable: false);
    final command = kind.contains('command');
    final reasoning = kind.contains('reason');
    final file = kind.contains('file');
    final tool = kind.contains('tool');
    final expandable =
        (command || reasoning) &&
        detailBlocks.any(
          (block) => (block['text'] as String? ?? '').trim().isNotEmpty,
        );
    final title = summary != null
        ? _TimelineSummaryItem._summaryText(context, summary)
        : command
        ? context.l10n.commandExecution
        : reasoning
        ? context.l10n.reasoningActivity
        : file
        ? context.l10n.fileChange
        : tool
        ? context.l10n.toolActivity
        : context.l10n.reasoningActivity;
    final status = widget.item['status'] as String?;
    final statusColor = _TimelineSummaryItem._statusColor(status);
    final icon = command
        ? Icons.terminal_rounded
        : file
        ? Icons.description_outlined
        : reasoning
        ? Icons.cloud_outlined
        : _TimelineSummaryItem._statusIcon(status);

    return IntrinsicHeight(
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Container(
            width: 3,
            margin: const EdgeInsets.only(right: 12),
            decoration: BoxDecoration(
              color: Theme.of(context).activityRail,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
          Expanded(
            child: Padding(
              padding: EdgeInsets.zero,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  InkWell(
                    borderRadius: BorderRadius.circular(12),
                    onTap: expandable
                        ? () => setState(() => _expanded = !_expanded)
                        : null,
                    child: ConstrainedBox(
                      constraints: const BoxConstraints(minHeight: 32),
                      child: Row(
                        children: [
                          Icon(icon, size: 18, color: statusColor),
                          const SizedBox(width: 8),
                          Expanded(
                            child: Text(
                              title,
                              maxLines: 2,
                              overflow: TextOverflow.ellipsis,
                              style: WawCompact.activity.copyWith(
                                color: Theme.of(context).mutedInk,
                              ),
                            ),
                          ),
                          if (status != null && status.isNotEmpty) ...[
                            const SizedBox(width: 8),
                            Text(
                              _TimelineSummaryItem._statusText(context, status),
                              style: WawCompact.metadata.copyWith(
                                color: statusColor,
                              ),
                            ),
                          ],
                          if (expandable)
                            Tooltip(
                              message: _expanded
                                  ? context.l10n.hideDetails
                                  : context.l10n.showDetails,
                              child: Icon(
                                _expanded
                                    ? Icons.keyboard_arrow_down_rounded
                                    : Icons.keyboard_arrow_right_rounded,
                                size: 20,
                                color: Theme.of(context).mutedInk,
                              ),
                            ),
                        ],
                      ),
                    ),
                  ),
                  if (_expanded)
                    Container(
                      margin: const EdgeInsets.only(top: 2),
                      padding: const EdgeInsets.all(10),
                      decoration: BoxDecoration(
                        color: Theme.of(context).softSurface,
                        borderRadius: BorderRadius.circular(14),
                      ),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          for (final block in detailBlocks) ...[
                            SelectableText(
                              block['text'] as String? ?? '',
                              style: command
                                  ? WawCompact.activityDetail.copyWith(
                                      fontFamily: 'monospace',
                                    )
                                  : WawCompact.activityDetail,
                            ),
                            if (block['truncated'] == true &&
                                widget.controller != null)
                              TextButton.icon(
                                onPressed: () =>
                                    _TimelineItem(
                                      item: widget.item,
                                      controller: widget.controller,
                                    )._showFullContent(
                                      context,
                                      widget.controller!,
                                      block,
                                    ),
                                icon: const Icon(Icons.open_in_full_rounded),
                                label: Text(context.l10n.viewFullContent),
                              ),
                          ],
                        ],
                      ),
                    ),
                ],
              ),
            ),
          ),
        ],
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
