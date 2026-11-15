# Invaris brand kit: Invariant Core

Brand assets for [Invaris](https://github.com/akkaraponph/invaris).

## Design

Segmented geometric boundaries represent constrained execution, a central `I` represents an invariant core, and two teal bars reference guardrail enforcement. All primary SVG artwork is built from editable vector shapes. The INVARIS wordmark is **outlined**, so no custom fonts are required by GitHub or viewers.

**Palette:** Midnight `#0B111A`, Off White `#F4F8FC`, Signal Teal `#20D1B3`, Steel `#476277`.

## Contents

- `logos/`: horizontal lockups on dark/light/transparent backgrounds and vector-only monograms
- `icons/`: padded GitHub avatar (1024px), app icon (512px), favicon sizes 16–256px, plus SVG
- `banners/`: README banner (1600×480), GitHub social preview (1280×640), docs cover (1600×700), launch poster (1600×900), terminal-styled illustration
- `social/`: landscape announcement card and square post
- `original/`: original selected Invariant Core image reference
- `preview/`: contact sheet to browse assets quickly
- `palette.json`: color tokens

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

## Asset notes

The SVGs use editable vector shapes based on the selected concept image. Promotional text and the `terminal-concept` banner illustrate the brand; the terminal image is not a product screenshot or CLI reference. Use 32px or larger favicons when possible to preserve detail.
