# Invaris brand kit — Invariant Core

A compact set of branding assets for https://github.com/akkaraponph/invaris.

## Design

Segmented geometric boundaries represent constrained execution, a central `I` represents an invariant core, and two teal bars reference guardrail enforcement. All primary SVG artwork is built from editable vector shapes. The INVARIS wordmark is **outlined**, so no custom fonts are required by GitHub or viewers.

**Palette:** Midnight `#0B111A`, Off White `#F4F8FC`, Signal Teal `#20D1B3`, Steel `#476277`.

## Contents

- `logos/` — horizontal lockups on dark/light/transparent backgrounds and vector-only monograms
- `icons/` — padded GitHub avatar (1024px), app icon (512px), favicon sizes 16–256px, plus SVG
- `banners/` — README banner (1600×480), GitHub social preview (1280×640), docs cover (1600×700), launch poster (1600×900), terminal-styled illustration
- `social/` — landscape announcement card and square post
- `concepts/` — an AI-generated alternative exploratory banner, **not** part of the primary vector identity
- `original/` — original selected Invariant Core image reference
- `preview/` — contact sheet to browse assets quickly
- `palette.json` — color tokens

## GitHub usage

1. The [repository README](../README.md) uses [the README banner](banners/github-readme-1600x480.png).
2. [GitHub Pages](../docs/index.html) serves the cover, wordmark, and favicon from [`docs/assets/`](../docs/assets/). When updating one of those SVGs, copy it from this kit to that directory as well.
3. In GitHub repository **Settings → General → Social preview**, upload `banners/github-social-preview-1280x640.png`.
4. The GitHub repository avatar is inherited from the owning user/organization account; `icons/github-avatar-1024.png` is an asset you can use for an organization or another supported profile/avatar context.
5. Use `icons/favicon.svg` or a suitable raster favicon in a website.

Markdown snippet for the repository root:

```md
![Invaris](assets/banners/github-readme-1600x480.png)
```

**Notes:** SVGs are purpose-drawn vector reconstructions based on the selected concept image, not automatically traced pixel-perfect conversions. Promotional banner copy is illustrative. The `terminal-concept` banner represents **conceptual** CLI output, not a verified product screenshot or claimed CLI syntax. Small favicons will simplify some fine detail at 16px; use 32px or larger when possible.
