import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:work_anywhere/features/conversation/markdown_renderer.dart';
import 'package:work_anywhere/l10n/generated/app_localizations.dart';

Widget _app(String markdown) => MaterialApp(
  locale: const Locale('en', 'US'),
  localizationsDelegates: AppLocalizations.localizationsDelegates,
  supportedLocales: AppLocalizations.supportedLocales,
  home: Scaffold(body: SafeMarkdownMessage(data: markdown)),
);

void main() {
  testWidgets('renders common Markdown instead of source markers', (
    tester,
  ) async {
    await tester.pumpWidget(
      _app('## Heading\n\n**bold** and `inline code`\n\n- item'),
    );

    final renderedText = tester
        .widgetList<SelectableText>(find.byType(SelectableText))
        .map((widget) => widget.textSpan?.toPlainText() ?? widget.data ?? '')
        .join('\n');
    expect(renderedText, contains('Heading'));
    expect(renderedText, contains('bold'));
    expect(find.text('**bold**', findRichText: true), findsNothing);
    expect(renderedText, contains('inline code'));
    expect(renderedText, contains('item'));
  });

  testWidgets('does not load Markdown images', (tester) async {
    await tester.pumpWidget(
      _app('![remote screenshot](https://example.com/image.png)'),
    );

    expect(find.text('remote screenshot'), findsOneWidget);
    expect(find.byIcon(Icons.image_not_supported_outlined), findsOneWidget);
  });

  testWidgets('requires confirmation before opening a web link', (
    tester,
  ) async {
    await tester.pumpWidget(_app('[OpenAI](https://openai.com/docs)'));

    await tester.tap(find.text('OpenAI', findRichText: true));
    await tester.pumpAndSettle();

    expect(find.text('Open external link?'), findsOneWidget);
    expect(
      find.text(
        'This will open openai.com. Confirm that you trust this website.',
      ),
      findsOneWidget,
    );
  });
}
