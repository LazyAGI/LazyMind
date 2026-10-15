# LazyMind examples

[中文](README.CN.md)

Keep using Codex or DeepSeek Harness (DSH). Connect the LazyMind plugin to use its workflows and knowledge from your existing Agent, with review and editing in the LazyMind panel where required.

These examples reuse existing LazyMind cases. A reference video demonstrates a previous run; it does not certify the current release. Each example records its reproduction status and the evidence still needed.

| Example | What to inspect | Reproduction status |
| --- | --- | --- |
| [Agent → editable PPT](agent-to-editable-ppt/README.md) | Review intermediate material, make a local edit, and deliver an editable file | Codex deck, local edit and Keynote roundtrip checked; final review, DSH run and independent reproduction pending |

Start with [connecting your Agent](shared/agent-setup.md). A running LazyMind instance and its required services are prerequisites; the plugin is not a standalone replacement for LazyMind.

Examples contain reproduction material and records, not another implementation of the workflows. Existing production packages stay in `workflows/`, and existing preview assets stay in `skills/featured/`.
