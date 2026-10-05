import 'dart:convert';

import 'package:flutter/material.dart';

import 'conversation_controller.dart';
import 'markdown_renderer.dart';

/// A timeline block may contain text and inline image data in the same body.
class TimelineMessageBody extends StatefulWidget {
  const TimelineMessageBody({required this.block, this.controller, super.key});

  final Map<String, dynamic> block;
  final ConversationController? controller;

  @override
  State<TimelineMessageBody> createState() => _TimelineMessageBodyState();
}

class _TimelineMessageBodyState extends State<TimelineMessageBody> {
  Future<String>? _fullContent;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(TimelineMessageBody oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.block['text'] != widget.block['text'] ||
        oldWidget.block['contentRef'] != widget.block['contentRef'] ||
        oldWidget.block['truncated'] != widget.block['truncated']) {
      _load();
    }
  }

  void _load() {
    final text = widget.block['text'] as String? ?? '';
    _fullContent =
        widget.block['truncated'] == true &&
            text.contains('data:image/') &&
            widget.controller != null
        ? widget.controller!.readFullContent(widget.block)
        : null;
  }

  @override
  Widget build(BuildContext context) {
    if (_fullContent == null) {
      return InlineImageMessage(data: widget.block['text'] as String? ?? '');
    }
    return FutureBuilder<String>(
      future: _fullContent,
      builder: (context, snapshot) {
        if (snapshot.hasError) {
          return IconButton(
            tooltip: MaterialLocalizations.of(context)
                .refreshIndicatorSemanticLabel,
            onPressed: () => setState(_load),
            icon: const Icon(Icons.refresh),
          );
        }
        if (!snapshot.hasData) {
          return const SizedBox(
            width: 32,
            height: 32,
            child: CircularProgressIndicator(),
          );
        }
        return InlineImageMessage(data: snapshot.data!);
      },
    );
  }
}

class InlineImageMessage extends StatelessWidget {
  const InlineImageMessage({required this.data, super.key});
  final String data;
  static final _images = RegExp(
    r'data:image/[a-zA-Z0-9.+-]+;base64,[a-zA-Z0-9+/=_-]*',
  );

  @override
  Widget build(BuildContext context) {
    final children = <Widget>[];
    var offset = 0;
    for (final match in _images.allMatches(data)) {
      final text = data.substring(offset, match.start).trim();
      if (text.isNotEmpty) children.add(SafeMarkdownMessage(data: text));
      children.add(_image(match.group(0)!));
      offset = match.end;
    }
    if (offset == 0) return SafeMarkdownMessage(data: data);
    final tail = data.substring(offset).trim();
    if (tail.isNotEmpty) children.add(SafeMarkdownMessage(data: tail));
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: children,
    );
  }

  Widget _image(String uri) {
    try {
      final bytes = base64Decode(uri.substring(uri.indexOf(',') + 1));
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 6),
        child: ClipRRect(
          borderRadius: BorderRadius.circular(12),
          child: Image.memory(
            bytes,
            fit: BoxFit.contain,
            cacheWidth: 1024,
            errorBuilder: (_, _, _) => const Icon(Icons.broken_image_outlined),
          ),
        ),
      );
    } on FormatException {
      return const Icon(Icons.broken_image_outlined);
    }
  }
}
