# UI Safety Skills

This folder contains UI-focused Codex skills designed for security and reliability.

## Included skills
- `skills/.curated/ui-secure-review`
- `skills/.curated/ui-safe-form-patterns`
- `skills/.curated/ui-resilient-accessibility`

## Install from GitHub (example)

After this repository is pushed to GitHub, install one or more skills with:

```bash
scripts/install-skill-from-github.py --repo <owner>/<repo> --path skills/.curated/ui-secure-review
scripts/install-skill-from-github.py --repo <owner>/<repo> --path skills/.curated/ui-safe-form-patterns
scripts/install-skill-from-github.py --repo <owner>/<repo> --path skills/.curated/ui-resilient-accessibility
```

## Offline product Skills

完整的实习生配置与验收手册见 [`PRODUCT_SKILLS_GUIDE.md`](PRODUCT_SKILLS_GUIDE.md)。

- Add ordinary installable Skill URLs to the `skills` list in `builtin-sources.yaml`; keep the platform-maintained `bundled_skills` entries intact.
- Add a curated experience under one self-contained directory:
  - `featured/<id>/featured.yaml` defines the `chat`/`work` type, placement, classification, asset IDs, card/detail text, and task selector/launch/replay/result slots.
  - `featured/<id>/locales/<locale>.yaml` contains the same user-visible slots for each additional locale.
  - `featured/<id>/assets/` contains all covers and optional result images. YAML references logical asset IDs, never frontend paths.
- Run `make featured-check` for strict schema, locale, template, MIME, size, dimension, path, and reference validation.
- Run `make skills-build` to package platform Skills, download/freeze linked Skill ZIPs, and compile `skills/.runtime/builtin-skills` plus `skills/.runtime/featured-skills`. Featured-only Skills stay out of the ordinary Skill marketplace.
- Compiled assets use content-hashed names and the platform-independent URL `/showcase-assets/<id>/<version>/<file>`. Desktop Caddy and Compose Nginx serve the same URL from the generated asset directory.
- Release builds use `builtin-skills.lock.json` in frozen mode. Compose, Native Local, and Desktop discover the generated catalogs and assets from their standard runtime locations automatically.
