# Agent icons

Both icons come from [LobeHub Icons](https://github.com/lobehub/lobe-icons)
(MIT License). The product names and marks belong to their respective owners.

- `codex.svg` is the `Codex.Avatar` variant from `@lobehub/icons@6.15.0`,
  flattened to a static SVG: a white circle with the 5% black inset edge that
  `IconAvatar` draws for white backgrounds, and the gradient `Codex.Inner` mark
  scaled to 70% (`AVATAR_ICON_MULTIPLE`) in the center. The gradient id is fixed.
- `claude-code.svg` is `claudecode-color.svg` from
  `@lobehub/icons-static-svg@1.102.0`, with fixed 24×24 dimensions instead of
  `1em`, no inline `style`, and a stable gradient id.

These changes only exist so `flutter_svg` can parse the files.
