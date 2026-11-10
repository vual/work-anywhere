import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../app/localization.dart';
import '../../app/theme.dart';
import '../../l10n/generated/app_localizations.dart';
import 'update_controller.dart';

String formatUpdateBytes(int bytes) {
  if (bytes >= 1024 * 1024) {
    return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
  }
  if (bytes >= 1024) return '${(bytes / 1024).toStringAsFixed(0)} KB';
  return '$bytes B';
}

/// User-facing text for a stable update error code.
String updateErrorText(AppLocalizations l10n, String? code) =>
    switch (code) {
      'update_manifest_unavailable' ||
      'update_manifest_invalid' => l10n.updateErrorManifest,
      'update_signature_invalid' => l10n.updateErrorSignature,
      'update_hash_mismatch' => l10n.updateErrorHash,
      'update_download_failed' => l10n.updateErrorDownload,
      'update_no_asset' => l10n.updateErrorNoAsset,
      'update_signer_mismatch' => l10n.updateErrorSigner,
      'update_install_failed' => l10n.updateErrorInstall,
      'update_install_cancelled' => l10n.updateInstallCancelled,
      'update_unsupported_build' => l10n.updateUnsupportedBuild,
      _ => l10n.updateErrorGeneric,
    };

Future<void> openReleaseNotes(String? url) async {
  final uri = url == null ? null : Uri.tryParse(url);
  if (uri == null || uri.scheme != 'https') return;
  await launchUrl(uri, mode: LaunchMode.externalApplication);
}

enum UpdatePromptChoice { update, later, skip }

/// Foreground auto-check prompt: version, size, notes link and three actions.
Future<UpdatePromptChoice?> showUpdatePrompt(
  BuildContext context,
  UpdateRelease release,
) => showDialog<UpdatePromptChoice>(
  context: context,
  builder: (context) {
    final l10n = context.l10n;
    return AlertDialog(
      key: const Key('update-prompt'),
      title: Text(l10n.updateAvailableTitle(release.versionName)),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (release.size > 0)
            Text(l10n.updateDownloadSize(formatUpdateBytes(release.size))),
          if (release.notesUrl != null)
            TextButton(
              style: TextButton.styleFrom(padding: EdgeInsets.zero),
              onPressed: () => openReleaseNotes(release.notesUrl),
              child: Text(l10n.updateReleaseNotes),
            ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(UpdatePromptChoice.skip),
          child: Text(l10n.updateSkipVersion),
        ),
        TextButton(
          onPressed: () => Navigator.of(context).pop(UpdatePromptChoice.later),
          child: Text(l10n.updateLater),
        ),
        FilledButton(
          onPressed: () =>
              Navigator.of(context).pop(UpdatePromptChoice.update),
          child: Text(l10n.updateNow),
        ),
      ],
    );
  },
);

/// Update status block of the About page: check button plus the current
/// phase (progress, release card, permission guide or error with retry).
class UpdateStatusSection extends ConsumerWidget {
  const UpdateStatusSection({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(updateProvider);
    final controller = ref.read(updateProvider.notifier);
    final l10n = context.l10n;
    final checkButton = FilledButton.tonal(
      key: const Key('update-check-button'),
      onPressed: state.loaded && state.updatable && !state.busy
          ? controller.check
          : null,
      child: Text(l10n.checkForUpdates),
    );
    if (state.loaded && !state.updatable) {
      return Column(
        children: [
          checkButton,
          const SizedBox(height: 8),
          Text(
            l10n.updateUnsupportedBuild,
            key: const Key('update-unsupported'),
            textAlign: TextAlign.center,
            style: WawCompact.metadata,
          ),
        ],
      );
    }
    final release = state.latest;
    final Widget status = switch (state.phase) {
      UpdatePhase.idle => const SizedBox.shrink(),
      UpdatePhase.checking => Row(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const SizedBox.square(
            dimension: 16,
            child: CircularProgressIndicator(strokeWidth: 2),
          ),
          const SizedBox(width: 10),
          Text(l10n.updateChecking),
        ],
      ),
      UpdatePhase.upToDate => Text(
        l10n.updateUpToDate,
        key: const Key('update-up-to-date'),
      ),
      UpdatePhase.available when release != null => _ReleaseCard(
        release: release,
        actions: [
          TextButton(
            onPressed: controller.skip,
            child: Text(l10n.updateSkipVersion),
          ),
          FilledButton(
            key: const Key('update-now-button'),
            onPressed: controller.startUpdate,
            child: Text(l10n.updateNow),
          ),
        ],
      ),
      UpdatePhase.downloading when release != null => _ReleaseCard(
        release: release,
        body: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            LinearProgressIndicator(
              key: const Key('update-progress'),
              value: state.progress,
            ),
            const SizedBox(height: 6),
            Text(
              l10n.updateDownloading(
                state.total > 0
                    ? '${formatUpdateBytes(state.received)} / ${formatUpdateBytes(state.total)}'
                    : formatUpdateBytes(state.received),
              ),
              style: WawCompact.metadata,
            ),
          ],
        ),
        actions: [
          TextButton(
            key: const Key('update-cancel-button'),
            onPressed: controller.cancelDownload,
            child: Text(l10n.cancel),
          ),
        ],
      ),
      UpdatePhase.downloaded when release != null => _ReleaseCard(
        release: release,
        body: state.errorCode == null
            ? null
            : Text(updateErrorText(l10n, state.errorCode)),
        actions: [
          FilledButton(
            key: const Key('update-install-button'),
            onPressed: controller.install,
            child: Text(l10n.updateInstall),
          ),
        ],
      ),
      UpdatePhase.permissionRequired when release != null => _ReleaseCard(
        release: release,
        title: l10n.updatePermissionTitle,
        body: Text(l10n.updatePermissionBody),
        actions: [
          FilledButton(
            key: const Key('update-open-settings-button'),
            onPressed: controller.openPermissionSettings,
            child: Text(l10n.updateOpenSettings),
          ),
        ],
      ),
      UpdatePhase.installing => Text(l10n.updateInstalling),
      UpdatePhase.error => Column(
        key: const Key('update-error'),
        children: [
          Text(
            updateErrorText(l10n, state.errorCode),
            textAlign: TextAlign.center,
            style: TextStyle(color: Theme.of(context).colorScheme.error),
          ),
          TextButton(
            onPressed: controller.retry,
            child: Text(
              state.errorCode == 'update_signer_mismatch' ||
                      state.errorCode == 'update_hash_mismatch'
                  ? l10n.updateRedownload
                  : l10n.retry,
            ),
          ),
        ],
      ),
      _ => const SizedBox.shrink(),
    };
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Center(child: checkButton),
        const SizedBox(height: 12),
        // Release cards span the page; short status lines are centred.
        status is _ReleaseCard ? status : Center(child: status),
      ],
    );
  }
}

class _ReleaseCard extends StatelessWidget {
  const _ReleaseCard({
    required this.release,
    required this.actions,
    this.title,
    this.body,
  });

  final UpdateRelease release;
  final List<Widget> actions;
  final String? title;
  final Widget? body;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return Card(
      key: const Key('update-release-card'),
      margin: EdgeInsets.zero,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 14, 16, 8),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              title ?? l10n.updateAvailableTitle(release.versionName),
              style: WawCompact.listTitle,
            ),
            if (release.size > 0) ...[
              const SizedBox(height: 4),
              Text(
                l10n.updateDownloadSize(formatUpdateBytes(release.size)),
                style: WawCompact.metadata,
              ),
            ],
            if (body != null) ...[const SizedBox(height: 10), body!],
            if (release.notesUrl != null)
              TextButton(
                style: TextButton.styleFrom(padding: EdgeInsets.zero),
                onPressed: () => openReleaseNotes(release.notesUrl),
                child: Text(l10n.updateReleaseNotes),
              ),
            // OverflowBar stacks the buttons instead of overflowing on
            // narrow screens or large text scales.
            OverflowBar(
              alignment: MainAxisAlignment.end,
              overflowAlignment: OverflowBarAlignment.end,
              spacing: 8,
              children: actions,
            ),
          ],
        ),
      ),
    );
  }
}
