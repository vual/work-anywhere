import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_svg/flutter_svg.dart';
import 'package:go_router/go_router.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import '../../app/localization.dart';
import '../../app/chrome.dart';
import '../workspace/workspace_controller.dart';

class PairingScreen extends ConsumerStatefulWidget {
  const PairingScreen({super.key});

  @override
  ConsumerState<PairingScreen> createState() => _PairingScreenState();
}

class _PairingScreenState extends ConsumerState<PairingScreen> {
  final _link = TextEditingController();
  bool _scanning = false;
  bool _submitted = false;

  @override
  void dispose() {
    _link.dispose();
    super.dispose();
  }

  Future<void> _pair(String value) async {
    if (_submitted || !value.startsWith('waw://pair?')) {
      if (!value.startsWith('waw://pair?')) {
        _show(context.l10n.invalidPairLink);
      }
      return;
    }
    setState(() {
      _submitted = true;
      _scanning = false;
    });
    try {
      await ref.read(workspaceProvider.notifier).pair(value);
    } catch (_) {
      if (!mounted) return;
      setState(() => _submitted = false);
      _show(context.l10n.pairFailed);
      return;
    }
    if (mounted) {
      final returnTo = GoRouterState.of(context)
          .uri
          .queryParameters['returnTo'];
      if (returnTo == 'computers' && context.canPop()) {
        context.pop();
      } else {
        context.go(returnTo == 'computers' ? '/computers' : '/');
      }
    }
  }

  void _show(String value) =>
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(value)));

  @override
  Widget build(BuildContext context) {
    if (_scanning) {
      return Scaffold(
        appBar: AppBar(
          automaticallyImplyLeading: false,
          toolbarHeight: WawBackButton.toolbarHeight,
          leadingWidth: WawBackButton.leadingWidth,
          leading: WawBackButton(
            tooltip: context.l10n.back,
            onPressed: () => setState(() => _scanning = false),
          ),
        ),
        body: Stack(
          fit: StackFit.expand,
          children: [
            MobileScanner(
              onDetect: (capture) {
                final value = capture.barcodes.firstOrNull?.rawValue;
                if (value != null) _pair(value);
              },
            ),
            Center(
              child: Container(
                width: 248,
                height: 248,
                decoration: BoxDecoration(
                  border: Border.all(color: Colors.white, width: 3),
                  borderRadius: BorderRadius.circular(24),
                ),
              ),
            ),
          ],
        ),
      );
    }
    return Scaffold(
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(28),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const Spacer(),
              Align(
                child: SvgPicture.asset(
                  'assets/waw-app-icon.svg',
                  width: 88,
                  height: 88,
                ),
              ),
              const SizedBox(height: 28),
              Text(
                context.l10n.continueOnPhone,
                textAlign: TextAlign.center,
                style: Theme.of(context).textTheme.headlineMedium
                    ?.copyWith(fontWeight: FontWeight.w700),
              ),
              const SizedBox(height: 12),
              Text(
                context.l10n.privacyIntro,
                textAlign: TextAlign.center,
                style: Theme.of(context).textTheme.bodyLarge
                    ?.copyWith(color: Colors.grey.shade600),
              ),
              const Spacer(),
              FilledButton.icon(
                onPressed: _submitted
                    ? null
                    : () => setState(() => _scanning = true),
                icon: const Icon(Icons.qr_code_scanner_rounded),
                label: Text(context.l10n.scanQr),
                style: FilledButton.styleFrom(
                  backgroundColor: Theme.of(context).colorScheme.onSurface,
                  foregroundColor: Theme.of(context).colorScheme.surface,
                  minimumSize: const Size.fromHeight(56),
                  shape: const StadiumBorder(),
                ),
              ),
              const SizedBox(height: 14),
              WawFloatingSurface(
                child: TextField(
                  controller: _link,
                  enabled: !_submitted,
                  autocorrect: false,
                  decoration: InputDecoration(
                    filled: false,
                    border: InputBorder.none,
                    enabledBorder: InputBorder.none,
                    focusedBorder: InputBorder.none,
                    hintText: context.l10n.pastePairLink,
                    suffixIcon: IconButton(
                      onPressed: () => _pair(_link.text),
                      icon: const Icon(Icons.arrow_forward_rounded),
                    ),
                  ),
                  onSubmitted: _pair,
                ),
              ),
              if (_submitted) ...[
                const SizedBox(height: 20),
                Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    SizedBox.square(
                      dimension: 18,
                      child: CircularProgressIndicator(strokeWidth: 2.5),
                    ),
                    SizedBox(width: 12),
                    Text(context.l10n.waitingDesktop),
                  ],
                ),
              ],
              const SizedBox(height: 20),
            ],
          ),
        ),
      ),
    );
  }
}

extension _FirstOrNull<T> on List<T> {
  T? get firstOrNull => isEmpty ? null : first;
}
