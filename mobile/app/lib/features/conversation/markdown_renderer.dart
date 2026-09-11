import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_markdown_plus/flutter_markdown_plus.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../app/localization.dart';

/// Renders conversation text as safe GitHub-Flavored Markdown.
///
/// The renderer deliberately does not load images. Links are restricted to
/// http(s), show the destination host, and require an explicit confirmation
/// before leaving the app.
class SafeMarkdownMessage extends StatelessWidget {
  const SafeMarkdownMessage({required this.data, super.key});

  final String data;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final body = theme.textTheme.bodyMedium;
    final styleSheet = MarkdownStyleSheet.fromTheme(theme).copyWith(
      p: body,
      code: body?.copyWith(
        fontFamily: 'monospace',
        backgroundColor: theme.colorScheme.surfaceContainerHighest,
      ),
      codeblockDecoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(8),
      ),
      blockquoteDecoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(6),
        border: Border(
          left: BorderSide(color: theme.colorScheme.primary, width: 3),
        ),
      ),
    );

    return MarkdownBody(
      data: data,
      selectable: true,
      styleSheet: styleSheet,
      onTapLink: (_, href, _) => unawaited(_openLink(context, href)),
      imageBuilder: (_, _, alt) => _BlockedImage(alt: alt),
    );
  }

  Future<void> _openLink(BuildContext context, String? href) async {
    final uri = href == null ? null : Uri.tryParse(href);
    if (uri == null || !_isWebUri(uri)) {
      if (context.mounted) {
        _showMessage(context, context.l10n.unsupportedLink);
      }
      return;
    }

    final confirmed =
        await showDialog<bool>(
          context: context,
          builder: (dialogContext) => AlertDialog(
            title: Text(context.l10n.externalLinkTitle),
            content: Text(context.l10n.externalLinkBody(uri.host)),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(dialogContext, false),
                child: Text(context.l10n.cancel),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(dialogContext, true),
                child: Text(context.l10n.open),
              ),
            ],
          ),
        ) ??
        false;
    if (!confirmed || !context.mounted) return;

    final launched = await launchUrl(uri, mode: LaunchMode.externalApplication);
    if (!launched && context.mounted) {
      _showMessage(context, context.l10n.externalLinkFailed);
    }
  }

  bool _isWebUri(Uri uri) =>
      (uri.scheme == 'http' || uri.scheme == 'https') && uri.host.isNotEmpty;

  void _showMessage(BuildContext context, String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }
}

class _BlockedImage extends StatelessWidget {
  const _BlockedImage({this.alt});

  final String? alt;

  @override
  Widget build(BuildContext context) => DecoratedBox(
    decoration: BoxDecoration(
      color: Theme.of(context).colorScheme.surfaceContainerHighest,
      borderRadius: BorderRadius.circular(8),
    ),
    child: Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.image_not_supported_outlined, size: 18),
          const SizedBox(width: 8),
          Text(
            alt?.isNotEmpty == true ? alt! : context.l10n.remoteImageBlocked,
          ),
        ],
      ),
    ),
  );
}
