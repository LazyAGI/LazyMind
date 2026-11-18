# Interactive paper guides

Open a PDF in the paper reading workspace and switch between **Guide / Original**. New guides use a Flashpapers-style article: Overview, Why This Matters, The Big Idea, How It Works, paper-specific chapters, Results, Limitations and Glossary. Titles follow the chosen language. The directory is planned for the paper, usually with 7–9 chapters (up to 12 for broader papers); it is not constrained to four content roles.

## Fast generation

The `paper-guide-v24` production pipeline has four steps:

1. **Read the paper.** Use its existing parsed PDF snapshot. For large-context models, allow up to 128 KB of source text so typical full papers and primary result tables remain available; smaller models retain their context-based limit. Include full evidence paragraphs when they fit; otherwise retain whole paragraphs spread across the paper within the model input budget. Page markers and compact source references accompany the text. There is no model extraction call.
2. **Plan chapters and their contents.** One `paper.explainer.plan` call returns the directory, learning objectives, outlines, source references and designs for paper-specific visual interactions. Every chapter receives the same plan so chapters can be written independently.
3. **Generate chapters in parallel.** `paper.explainer.chapter` writes each chapter's prose, formula explanations and any assigned HTML/SVG/canvas/JavaScript teaching scene. Four chapter requests run concurrently by default. A completed chapter is saved immediately, and the reader presents chapters in planned order regardless of completion order.
4. **Light check.** Validate the directory, chapter identity, nonempty content and renderable payload shape. This is a local check; it adds no model call. There is no scientific review pass, whole-guide model audit or automatic content-repair loop.

Normal model-call count is one planning call plus one call per chapter. Source preparation and provider latency still affect wall time. New runs have the existing total request/time budget and authorization/lease protections. A failed chapter does not discard chapters that have already finished. Resuming an eligible interrupted run reuses saved work and generates only missing chapters; each retry still counts against its durable budget.

The light check does not certify every scientific statement or execute generated JavaScript on the server. Citations let readers inspect sources, and teaching examples should distinguish illustrative scenarios from measurements reported in the paper.

## Article and paper-specific interactions

The article has a chapter directory, a concise overview, substantial mechanism explanations, worked examples and paper-specific topics. Examples include LoRA's parameter and subspace explorations, a noise-process demonstration, or a search-tree walkthrough. Interaction designs are selected for the learning question, rather than from a fixed set of four widgets. The planner assigns distinct learning goals to avoid duplicate scenes and reserves a compact HTML table or chart for primary reported results when available. Headline variants keep their configuration labels. Plain prose uses text and math; richer formatting belongs inside the HTML module.

A chapter's playground is a self-contained document with inline styles, SVG/canvas and ordinary JavaScript. It can manipulate geometry, matrices, paths, plots and calculations locally. The host supplies the theme and places it in an opaque-origin iframe with `sandbox="allow-scripts"`, without same-origin or top-navigation permissions. A restrictive CSP blocks external resource loading and connections. Generated content is never inserted as executable markup into the application DOM. See the [iframe srcdoc isolation guidance](https://developer.mozilla.org/en-US/docs/Web/API/HTMLIFrameElement/srcdoc).

The explanatory text outside the scene remains readable when an interaction is unavailable. Source controls belong to the article and open the source preview/PDF; generated scenes do not receive application credentials or document API access. A module's runtime failure can show its static explanation and a reload control rather than blocking the rest of the article.

## Models, source versions and history

Generation details identify the selected model and run state. A per-run model override applies to the new guide without changing global reading/chat preferences. Existing revisions retain their original model identity. A changed source or model cannot silently continue an incompatible run.

Old published guides remain readable using their original presentation. New generation uses the fast article layout; an older unfinished contract needs regeneration. Source material limitations remain available as context, rather than requiring scientific audit before the article can be read.

## API

Routes are relative to `/api/core/datasets/{dataset}/documents/{document}` and use the Core `data` envelope. Use `language=zh` or `language=en` on reads; generation accepts language in JSON. Access remains scoped to the authenticated document owner.

| Method and route | Purpose |
| --- | --- |
| `GET /paper-guide` | Availability, source hash, published/candidate revision and reading progress; supports private ETag revalidation. |
| `POST /paper-guide:generate` | `language`, `expected_source_hash`, `idempotency_key`, `regenerate` and optional `model_ref`; normally returns 202. |
| `GET /paper-guide/revisions/{revision}` | Run, plan and published chapters; `after_version` supports incremental updates. Fast plans have `layout: "flashpapers"`, `title`, `subtitle`, and ordered `sections`. Section `role` is the stable chapter slug, not a four-role enum. |
| `GET /paper-guide/revisions/{revision}/evidence/{evidence}` | Source preview and currentness; locators use zero-based physical pages. |
| `POST /paper-guide/runs/{run}:cancel` | Stops remaining work and preserves completed chapters. |
| `POST /paper-guide/runs/{run}:resume` | Requeues eligible missing work with the same model, source and remaining budget. |
| `PUT /paper-guide/progress` | Saves revision, section key, block, Unicode offset and reading mode. |
| `POST /paper-guide/revisions/{revision}:selection-context` | Resolves a selected passage against stored text and source references. |

Fast chapter blocks use `paragraph`, `heading`, `formula` or `playground`. A playground carries `playground.html` and static explanatory `text`; its references use the same source-preview path as ordinary prose. Internal task names, planner data and generated code are not exposed as reading-flow instructions.

### v26：解释问题与领域背景

仍使用读取论文、一次目录规划、并行生成章节、轻量检查四步，不增加审查模型调用。内部规划协议增加 `reader_profile`、`shared_examples`，章节携带 `knowledge_gaps`、`reasoning_questions` 和 `example_ids`；完整规划转交每个章节，旧记录缺省为空仍可读取。

规划区分“这个概念是什么”与“这一步为什么成立”。共享算例固定场景、输入、单位和约束；交互用同一配置与计算函数产生所有派生显示值，正文不另行硬编码一套交互默认结果。交互必须展示能够回答教学问题的对照。概览标题由本地轻量处理移除，例子引用与控制字符由协议校验，不尝试自动科学审查自由文本。

术语表以领域问题、核心概念、方法类别及其与本文的关系为主，不再主要罗列公式变量；符号在正文首次使用时解释。排队显示“开始后约 2 分钟”，排队耗时与开始后的生成估计分开。
