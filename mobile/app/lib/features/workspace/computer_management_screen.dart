import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/chrome.dart';
import '../../app/localization.dart';
import '../../app/theme.dart';
import '../../core/models.dart';
import '../../core/core_gateway.dart';
import 'workspace_controller.dart';

class ComputerManagementScreen extends ConsumerWidget {
  const ComputerManagementScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(workspaceProvider);
    return state.when(
      loading: () =>
          const Scaffold(body: Center(child: CircularProgressIndicator())),
      error: (_, _) =>
          Scaffold(body: Center(child: Text(context.l10n.loadWorkspaceFailed))),
      data: (data) => Scaffold(
        appBar: AppBar(
          leading: WawCircleButton(
            icon: Icons.arrow_back_rounded,
            tooltip: context.l10n.back,
            onPressed: () => context.canPop() ? context.pop() : context.go('/'),
          ),
          title: Text(
            context.l10n.manageComputers,
            style: WawCompact.appBarTitle,
          ),
        ),
        body: SafeArea(
          child: CustomScrollView(
            slivers: [
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(24, 8, 24, 4),
                sliver: SliverToBoxAdapter(
                  child: Text(
                    context.l10n.connectionSection,
                    style: WawCompact.sectionTitle,
                  ),
                ),
              ),
              SliverPadding(
                padding: const EdgeInsets.symmetric(horizontal: 24),
                sliver: SliverReorderableList(
                  itemCount: data.daemons.length,
                  onReorderItem: (oldIndex, newIndex) async {
                    try {
                      await ref
                          .read(workspaceProvider.notifier)
                          .reorderDaemons(oldIndex, newIndex);
                    } catch (_) {
                      if (context.mounted) {
                        ScaffoldMessenger.of(context).showSnackBar(
                          SnackBar(
                            content: Text(context.l10n.computerSaveFailed),
                          ),
                        );
                      }
                    }
                  },
                  itemBuilder: (context, index) => _ComputerRow(
                    key: ValueKey(data.daemons[index].id),
                    index: index,
                    computer: data.daemons[index],
                    onEnabledChanged: (enabled) async {
                      try {
                        await ref
                            .read(workspaceProvider.notifier)
                            .setDaemonEnabled(data.daemons[index].id, enabled);
                      } catch (_) {
                        if (context.mounted) {
                          ScaffoldMessenger.of(context).showSnackBar(
                            SnackBar(
                              content: Text(context.l10n.computerSaveFailed),
                            ),
                          );
                        }
                      }
                    },
                    onEdit: () =>
                        _editComputer(context, ref, data.daemons[index]),
                  ),
                ),
              ),
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(24, 8, 24, 0),
                sliver: SliverToBoxAdapter(
                  child: _AddComputerCard(
                    onTap: () => context.push('/pair?returnTo=computers'),
                  ),
                ),
              ),
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(24, 12, 24, 24),
                sliver: SliverToBoxAdapter(
                  child: Text(
                    context.l10n.dragToReorder,
                    style: WawCompact.body.copyWith(
                      color: Theme.of(context).mutedInk,
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _editComputer(
    BuildContext context,
    WidgetRef ref,
    DaemonSummary computer,
  ) async {
    final result = await showDialog<_ComputerEditResult>(
      context: context,
      builder: (_) => _ComputerEditDialog(computer: computer),
    );
    if (result == null || !context.mounted) return;
    try {
      final updated = await ref
          .read(workspaceProvider.notifier)
          .updateDaemon(computer.id, result.name, result.relayUrl);
      if (context.mounted) {
        final message =
            updated?.enabled == true &&
                updated?.phase == ConnectionPhase.offline
            ? context.l10n.computerSavedOffline
            : context.l10n.computerSaved;
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(message)));
      }
    } catch (error) {
      if (context.mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(_editError(context, error))));
      }
    }
  }
}

class _ComputerRow extends StatelessWidget {
  const _ComputerRow({
    required this.index,
    required this.computer,
    required this.onEnabledChanged,
    required this.onEdit,
    super.key,
  });
  final int index;
  final DaemonSummary computer;
  final ValueChanged<bool> onEnabledChanged;
  final VoidCallback onEdit;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(bottom: 3),
    child: Material(
      color: Theme.of(context).softSurface,
      borderRadius: BorderRadius.circular(18),
      child: ListTile(
        minTileHeight: 76,
        leading: ReorderableDragStartListener(
          index: index,
          child: const Icon(Icons.drag_handle_rounded, size: 28),
        ),
        title: Row(
          children: [
            const Icon(Icons.laptop_mac_outlined, size: 26),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    computer.name?.isNotEmpty == true
                        ? computer.name!
                        : context.l10n.computer,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 3),
                  Text(
                    '${_phaseLabel(context, computer.phase)}${computer.relayUrl.isEmpty ? '' : ' · ${computer.relayUrl}'}',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: WawCompact.metadata.copyWith(
                      color: Theme.of(context).mutedInk,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
        trailing: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            PopupMenuButton<String>(
              onSelected: (_) => onEdit(),
              itemBuilder: (context) => [
                PopupMenuItem(
                  value: 'edit',
                  child: Text(context.l10n.editComputer),
                ),
              ],
            ),
            Switch(value: computer.enabled, onChanged: onEnabledChanged),
          ],
        ),
      ),
    ),
  );
}

class _AddComputerCard extends StatelessWidget {
  const _AddComputerCard({required this.onTap});
  final VoidCallback onTap;
  @override
  Widget build(BuildContext context) => Material(
    color: Theme.of(context).softSurface,
    borderRadius: BorderRadius.circular(18),
    child: ListTile(
      minTileHeight: 76,
      leading: const Icon(Icons.add_link_rounded, size: 28),
      title: Text(context.l10n.addComputer),
      subtitle: Text(context.l10n.addComputerHint),
      onTap: onTap,
    ),
  );
}

class _ComputerEditResult {
  const _ComputerEditResult(this.name, this.relayUrl);
  final String name;
  final String relayUrl;
}

class _ComputerEditDialog extends StatefulWidget {
  const _ComputerEditDialog({required this.computer});
  final DaemonSummary computer;
  @override
  State<_ComputerEditDialog> createState() => _ComputerEditDialogState();
}

class _ComputerEditDialogState extends State<_ComputerEditDialog> {
  late final TextEditingController _name = TextEditingController(
    text: widget.computer.name ?? '',
  );
  late final TextEditingController _relay = TextEditingController(
    text: widget.computer.relayUrl,
  );
  String? _error;

  @override
  void dispose() {
    _name.dispose();
    _relay.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text(context.l10n.editComputer),
    content: SingleChildScrollView(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          TextField(
            controller: _name,
            autofocus: true,
            maxLength: 80,
            decoration: InputDecoration(labelText: context.l10n.computerName),
          ),
          const SizedBox(height: 8),
          TextField(
            controller: _relay,
            keyboardType: TextInputType.url,
            onChanged: (_) => setState(() {}),
            decoration: InputDecoration(
              labelText: context.l10n.relayAddress,
              hintText: 'https://relay.example.com',
            ),
          ),
          if (_relay.text.trimLeft().startsWith('http://'))
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(
                context.l10n.httpRelayWarning,
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
            ),
          if (_error != null)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(
                _error!,
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
            ),
        ],
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: Text(context.l10n.cancel),
      ),
      FilledButton(onPressed: _save, child: Text(context.l10n.save)),
    ],
  );

  void _save() {
    final name = _name.text.trim();
    final relay = _relay.text.trim();
    final uri = Uri.tryParse(relay);
    if (name.isEmpty || name.runes.length > 80) {
      setState(() => _error = context.l10n.invalidComputerName);
      return;
    }
    if (uri == null ||
        (uri.scheme != 'http' && uri.scheme != 'https') ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.hasQuery ||
        uri.hasFragment ||
        (uri.path.isNotEmpty && uri.path != '/')) {
      setState(() => _error = context.l10n.invalidRelayAddress);
      return;
    }
    Navigator.pop(context, _ComputerEditResult(name, relay));
  }
}

String _phaseLabel(BuildContext context, ConnectionPhase phase) =>
    switch (phase) {
      ConnectionPhase.online => context.l10n.secureOnline,
      ConnectionPhase.connecting => context.l10n.connecting,
      ConnectionPhase.disabled => context.l10n.disabled,
      ConnectionPhase.offline => context.l10n.offline,
    };

String _editError(BuildContext context, Object error) =>
    error is CoreFault && error.code == 'invalid_relay_origin'
    ? context.l10n.invalidRelayAddress
    : context.l10n.computerSaveFailed;
