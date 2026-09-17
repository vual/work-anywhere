import 'package:flutter/material.dart';

import '../../app/localization.dart';
import '../../app/theme.dart';

Future<Map<String, List<String>>?> showUserInputDialog(
  BuildContext context,
  Map<String, dynamic> action,
) => showDialog<Map<String, List<String>>>(
  context: context,
  builder: (_) => UserInputDialog(action: action),
);

class UserInputDialog extends StatefulWidget {
  const UserInputDialog({required this.action, super.key});

  final Map<String, dynamic> action;

  @override
  State<UserInputDialog> createState() => _UserInputDialogState();
}

class _UserInputDialogState extends State<UserInputDialog> {
  late final List<Map<String, dynamic>> _questions;
  final Map<String, TextEditingController> _controllers = {};
  final Map<String, String> _selected = {};

  @override
  void initState() {
    super.initState();
    _questions = (widget.action['questions'] as List? ?? const [])
        .whereType<Map<String, dynamic>>()
        .toList(growable: false);
    for (final question in _questions) {
      final id = question['id'] as String? ?? '';
      final controller = TextEditingController();
      controller.addListener(() {
        if (!mounted) return;
        if (controller.text.trim().isNotEmpty) _selected.remove(id);
        setState(() {});
      });
      _controllers[id] = controller;
    }
  }

  @override
  void dispose() {
    for (final controller in _controllers.values) {
      controller.dispose();
    }
    super.dispose();
  }

  String _answer(String id) {
    final custom = _controllers[id]?.text.trim() ?? '';
    return custom.isNotEmpty ? custom : (_selected[id] ?? '');
  }

  bool get _canSubmit =>
      _questions.isNotEmpty &&
      _questions.every((question) {
        final id = question['id'] as String? ?? '';
        return id.isNotEmpty && _answer(id).isNotEmpty;
      });

  void _choose(String id, String label) {
    _controllers[id]?.clear();
    setState(() => _selected[id] = label);
  }

  void _submit() {
    if (!_canSubmit) return;
    Navigator.of(context).pop({
      for (final question in _questions)
        question['id'] as String: [_answer(question['id'] as String)],
    });
  }

  @override
  Widget build(BuildContext context) {
    final media = MediaQuery.of(context);
    return Dialog(
      insetPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 28),
      backgroundColor: Theme.of(context).floatingSurface,
      surfaceTintColor: Colors.transparent,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(28)),
      clipBehavior: Clip.antiAlias,
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxWidth: 620,
          maxHeight: media.size.height * .86,
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(22, 20, 12, 10),
              child: Row(
                children: [
                  const Icon(Icons.error_outline_rounded, color: wawAttention),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Text(
                      context.l10n.answerQuestionsTitle,
                      style: Theme.of(context).textTheme.titleLarge,
                    ),
                  ),
                  IconButton(
                    onPressed: () => Navigator.of(context).pop(),
                    tooltip: context.l10n.cancel,
                    icon: const Icon(Icons.close_rounded),
                  ),
                ],
              ),
            ),
            const Divider(height: 1),
            Expanded(
              child: ListView.separated(
                padding: const EdgeInsets.fromLTRB(22, 18, 22, 20),
                itemCount: _questions.length,
                separatorBuilder: (_, _) => const Padding(
                  padding: EdgeInsets.symmetric(vertical: 18),
                  child: Divider(height: 1),
                ),
                itemBuilder: (context, index) => _QuestionEditor(
                  question: _questions[index],
                  controller: _controllers[_questions[index]['id'] as String]!,
                  selected: _selected[_questions[index]['id'] as String],
                  onSelected: _choose,
                ),
              ),
            ),
            const Divider(height: 1),
            Padding(
              padding: const EdgeInsets.fromLTRB(22, 12, 22, 18),
              child: Align(
                alignment: Alignment.centerRight,
                child: FilledButton(
                  key: const ValueKey('submit-user-input'),
                  onPressed: _canSubmit ? _submit : null,
                  child: Text(context.l10n.submit),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _QuestionEditor extends StatelessWidget {
  const _QuestionEditor({
    required this.question,
    required this.controller,
    required this.selected,
    required this.onSelected,
  });

  final Map<String, dynamic> question;
  final TextEditingController controller;
  final String? selected;
  final void Function(String id, String label) onSelected;

  @override
  Widget build(BuildContext context) {
    final id = question['id'] as String? ?? '';
    final header = question['header'] as String? ?? '';
    final prompt = question['question'] as String? ?? '';
    final options = (question['options'] as List? ?? const [])
        .whereType<Map<String, dynamic>>()
        .toList(growable: false);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (header.isNotEmpty) ...[
          Text(
            header,
            style: WawCompact.metadata.copyWith(
              color: Theme.of(context).mutedInk,
              fontWeight: FontWeight.w700,
            ),
          ),
          const SizedBox(height: 5),
        ],
        Text(prompt, style: Theme.of(context).textTheme.titleMedium),
        if (options.isNotEmpty) ...[
          const SizedBox(height: 12),
          for (final option in options)
            _OptionRow(
              label: option['label'] as String? ?? '',
              description: option['description'] as String? ?? '',
              selected: selected == option['label'],
              onTap: () => onSelected(id, option['label'] as String? ?? ''),
            ),
        ],
        const SizedBox(height: 12),
        Text(
          context.l10n.answer,
          style: WawCompact.metadata.copyWith(fontWeight: FontWeight.w700),
        ),
        const SizedBox(height: 7),
        TextField(
          key: ValueKey('user-input-$id'),
          controller: controller,
          obscureText: question['isSecret'] == true,
          minLines: 1,
          maxLines: question['isSecret'] == true ? 1 : 4,
          textInputAction: TextInputAction.done,
          decoration: InputDecoration(hintText: context.l10n.answerHint),
        ),
      ],
    );
  }
}

class _OptionRow extends StatelessWidget {
  const _OptionRow({
    required this.label,
    required this.description,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final String description;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => InkWell(
    onTap: onTap,
    borderRadius: BorderRadius.circular(14),
    child: Padding(
      padding: const EdgeInsets.symmetric(vertical: 9),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(top: 1),
            child: Icon(
              selected
                  ? Icons.radio_button_checked_rounded
                  : Icons.radio_button_off_rounded,
              size: 22,
              color: selected
                  ? Theme.of(context).colorScheme.primary
                  : Theme.of(context).mutedInk,
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(label, style: Theme.of(context).textTheme.bodyLarge),
                if (description.isNotEmpty) ...[
                  const SizedBox(height: 3),
                  Text(
                    description,
                    style: Theme.of(context).textTheme.bodyMedium
                        ?.copyWith(color: Theme.of(context).mutedInk),
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    ),
  );
}
