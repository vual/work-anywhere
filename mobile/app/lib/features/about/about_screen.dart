import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_svg/flutter_svg.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../app/chrome.dart';
import '../../app/localization.dart';
import '../../app/theme.dart';
import '../update/update_controller.dart';
import '../update/update_views.dart';

const projectHomepageUrl = 'https://github.com/vual/work-anywhere';

/// `/about`: version, update check and project link. With [autoCheck]
/// (`/about?check=1`) one check starts after the first frame; rebuilds,
/// rotation or returning from another page do not repeat it.
class AboutScreen extends ConsumerStatefulWidget {
  const AboutScreen({this.autoCheck = false, super.key});
  final bool autoCheck;

  @override
  ConsumerState<AboutScreen> createState() => _AboutScreenState();
}

class _AboutScreenState extends ConsumerState<AboutScreen> {
  bool _autoChecked = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final controller = ref.read(updateProvider.notifier);
      if (widget.autoCheck && !_autoChecked) {
        _autoChecked = true;
        controller.check();
      } else {
        controller.load();
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final state = ref.watch(updateProvider);
    final app = state.app;
    return Scaffold(
      appBar: AppBar(
        automaticallyImplyLeading: false,
        toolbarHeight: WawBackButton.toolbarHeight,
        leadingWidth: WawBackButton.leadingWidth,
        leading: WawBackButton(
          tooltip: l10n.back,
          onPressed: () => context.canPop() ? context.pop() : context.go('/'),
        ),
        titleSpacing: 4,
        title: Text(l10n.about, style: WawCompact.appBarTitle),
      ),
      body: SafeArea(
        child: ListView(
          padding: const EdgeInsets.fromLTRB(
            WawCompact.horizontalPadding,
            24,
            WawCompact.horizontalPadding,
            24,
          ),
          children: [
            Center(
              child: SvgPicture.asset(
                'assets/waw-app-icon.svg',
                width: 72,
                height: 72,
              ),
            ),
            const SizedBox(height: 12),
            Center(
              child: Text(
                'WAW',
                style: Theme.of(context).textTheme.titleLarge,
              ),
            ),
            const SizedBox(height: 4),
            Center(
              child: Text(
                app == null
                    ? ''
                    : l10n.appVersionLabel(
                        app.versionName,
                        '${app.versionCode}',
                      ),
                key: const Key('about-version'),
                style: WawCompact.metadata,
              ),
            ),
            const SizedBox(height: 24),
            const UpdateStatusSection(),
            const SizedBox(height: 24),
            ListTile(
              contentPadding: EdgeInsets.zero,
              leading: const Icon(Icons.open_in_new),
              title: Text(l10n.projectHomepage),
              subtitle: const Text(projectHomepageUrl),
              onTap: () => launchUrl(
                Uri.parse(projectHomepageUrl),
                mode: LaunchMode.externalApplication,
              ),
            ),
            if (state.updatable) const _AdvancedSection(),
          ],
        ),
      ),
    );
  }
}

class _AdvancedSection extends ConsumerStatefulWidget {
  const _AdvancedSection();

  @override
  ConsumerState<_AdvancedSection> createState() => _AdvancedSectionState();
}

class _AdvancedSectionState extends ConsumerState<_AdvancedSection> {
  late final TextEditingController _mirror = TextEditingController(
    text: ref.read(updateProvider).mirrorBase,
  );

  @override
  void dispose() {
    _mirror.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    await ref.read(updateProvider.notifier).setMirrorBase(_mirror.text);
    if (!mounted) return;
    FocusScope.of(context).unfocus();
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(context.l10n.updateMirrorSaved)));
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return ExpansionTile(
      key: const Key('about-advanced'),
      tilePadding: EdgeInsets.zero,
      childrenPadding: const EdgeInsets.only(bottom: 12),
      title: Text(l10n.updateAdvanced),
      children: [
        TextField(
          key: const Key('update-mirror-field'),
          controller: _mirror,
          keyboardType: TextInputType.url,
          autocorrect: false,
          decoration: InputDecoration(
            labelText: l10n.updateMirrorLabel,
            hintText: l10n.updateMirrorHint,
          ),
          onSubmitted: (_) => _save(),
        ),
        Align(
          alignment: Alignment.centerRight,
          child: TextButton(onPressed: _save, child: Text(l10n.save)),
        ),
      ],
    );
  }
}
