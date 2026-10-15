# LazyMind examples

[中文](README.CN.md)

Keep using Codex or DeepSeek Harness (DSH). Connect the LazyMind plugin to use its workflows and knowledge from your existing Agent, with review and editing in the LazyMind panel where required.

These examples reuse existing LazyMind cases. A reference video demonstrates a previous run; it does not certify the current release. Each example records its reproduction status and the evidence still needed.

## Video showcases

Each video has a dedicated case directory with an introduction and the original link. The Codex and DeepSeek Harness cases share the detailed Palace Museum PPT reproduction material below.

| Case | Platform |
| --- | --- |
| [DeepSeek Harness × LazyMind: from a request to a slide deck](deepseek-harness-ppt/README.md) | Bilibili |
| [From a product idea to a proposal, PRD, and prototype](product-idea-to-prototype/README.md) | Bilibili |
| [Codex × LazyMind: create and refine a presentation](codex-ppt/README.md) | Bilibili |
| [Refining AI presentation layouts and content](refine-ai-ppt/README.md) | Bilibili |
| [Creating your own animated stickers](animated-stickers/README.md) | WeChat Channels |
| [Bringing AI agents into your workflow](agents-in-workflow/README.md) | WeChat Channels |

## Reproduction material and status

| Example | What to inspect | Reproduction status |
| --- | --- | --- |
| [Agent → editable PPT](agent-to-editable-ppt/README.md) | Review intermediate material, make a local edit, and deliver an editable file | Codex deck, local edit and Keynote roundtrip checked; final review, DSH run and independent reproduction pending |

Start with [connecting your Agent](shared/agent-setup.md). A running LazyMind instance and its required services are prerequisites; the plugin is not a standalone replacement for LazyMind.

Examples contain reproduction material and records, not another implementation of the workflows. Existing production packages stay in `workflows/`, and existing preview assets stay in `skills/featured/`.
