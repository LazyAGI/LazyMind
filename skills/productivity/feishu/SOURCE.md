# Source and adaptation notice

- Upstream project: [leemysw/feishu-docx](https://github.com/leemysw/feishu-docx)
- Upstream Skill: [`.skills/feishu-docx/SKILL.md`](https://github.com/leemysw/feishu-docx/blob/b12a543399d165929c7f1df2a00d826356d53f39/.skills/feishu-docx/SKILL.md)
- Reviewed commit: `b12a543399d165929c7f1df2a00d826356d53f39`
- Upstream license: MIT; the complete notice is preserved in `LICENSE`.

## LazyMind adaptation

The upstream Skill's Feishu/Lark document capability and operation routing were
used as design input. This package replaces the upstream Python package and CLI
commands with LazyMind's existing authenticated `CloudFileToolkit`, FeishuFS,
AI Writer Workflow, and optional browser-extension tools. No upstream Python
implementation is vendored.

LazyMind-specific additions include provider-first routing for private links,
block-level update guidance, browser/API separation, OAuth failure behavior,
mandatory read-back verification after writes, and a browser-only UI editing
workflow validated against Feishu Wiki's accessibility text-node placeholders.
