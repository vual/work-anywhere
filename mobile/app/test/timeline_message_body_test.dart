import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:work_anywhere/features/conversation/timeline_message_body.dart';

const pixel =
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aD1sAAAAASUVORK5CYII=';

void main() {
  testWidgets('renders inline attachments and preserves surrounding text', (
    tester,
  ) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: Scaffold(
          body: InlineImageMessage(
            data:
                'Caption\n\ndata:image/png;base64,$pixel\n\nSecond caption\n\ndata:image/png;base64,$pixel',
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.byType(Image), findsNWidgets(2));
    expect(find.text('Caption', findRichText: true), findsOneWidget);
    expect(find.text('Second caption', findRichText: true), findsOneWidget);
    expect(find.textContaining('base64', findRichText: true), findsNothing);
  });

  testWidgets('invalid base64 is replaced by an image placeholder', (
    tester,
  ) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: Scaffold(
          body: InlineImageMessage(data: 'data:image/jpeg;base64,a'),
        ),
      ),
    );
    expect(find.byIcon(Icons.broken_image_outlined), findsOneWidget);
    expect(find.textContaining('base64', findRichText: true), findsNothing);
  });
}
