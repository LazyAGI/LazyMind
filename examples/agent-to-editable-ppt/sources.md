# Source material / 原始材料

Inspected on 2026-10-13. Repository baseline: `84a1a5b7f1d1e9ef59022f2c57ab7dfe1fd54ac2`.

The owner supplied the [original Chinese prompt](prompt.zh-CN.txt), two recordings and exported files. Both recordings were inspected using sampled frames and focused review of the review/edit/export stages. These are historical demonstrations, not current-version E2E. Exact historical builds, plugin versions and complete model settings are unknown.

案例负责人提供了原始提示词、两组录屏和导出文件。已抽帧检查录像，并重点检查审核、修改及导出阶段；尚未逐帧审计。历史运行的完整版本和模型配置未知。

## Selected reference: DSH / 主展示：DSH

- Recording: `飞书20261013-171404.mov`, 551.14 seconds, 1920 × 1080, no audio track. Raw video remains outside Git; its duration is not an execution-time benchmark.
- Export: `presentation_2026-09-18T04-06-10.pdf`, four pages, approximately 16:9. The [lossless ZIP](reference/palace-museum-dsh-reference.zip) contains the unchanged original PDF. Extract it with a ZIP utility; verify its SHA-256 against [sources.json](sources.json).
- [Preview](reference/preview.jpg): reduced contact sheet rendered from the PDF, not an editable deliverable.
- Matching PPTX: **not yet supplied**. The other group's PPTX cannot establish this deck's editability.

第二组四页为“北京故宫·紫禁城”“肇建与格局”“珍藏与传承”“守护与新生”。建筑全景、太和殿及文物特写更明确，录屏覆盖来源、审核、第二页局部修改及导出查看，因此选为主展示。局部深色文字与背景对比仍偏弱，第一页底部存在疑似字体符号异常；选择它不代表版式无缺陷。

The recording shows collection of two architecture images and two artifact images, outline/per-slide prompt review, a slide-2 paragraph edit and an exported PDF opened for inspection. AI-generated background imagery is also visible; do not label all imagery as authentic museum photography. The source panel includes Palace Museum website references, but the original collected files and complete attribution inventory were not supplied. The PDF is rasterized: no text was extracted from its pages. Its historical claims have not been independently fact-checked in this audit.

Approximate navigation windows in the original recording, not timing measurements:

| Window | Observed stage / 可见阶段 |
| --- | --- |
| 00:00–00:30 | Agent tool discovery and Workflow panel / 工具发现及面板 |
| 01:15–01:30 | Material summary and source table / 资料及来源 |
| 03:30–04:30 | Outline and per-slide prompts / 大纲与每页提示词 |
| 06:15–07:30 | Generation/loading previews / 生成及等待 |
| 07:45–08:30 | Completed preview and slide-2 paragraph edit / 成果与第二页段落修改 |
| 08:30–09:11 | Export and PDF inspection / 导出及查看 |

The edit textbox is partly clipped. Its exact submitted instruction has not been recovered; do not present a reconstruction as an original transcript. Separate before/after exports are missing.

局部修改输入框存在裁切，尚未取得完整原始修改指令，也没有独立的修改前后两个导出版本。

## Supplementary reference: Codex / 补充展示：Codex

`飞书20261013-170718.mov` (732.381667 seconds, 1920 × 1080, no audio), `presentation_2026-09-24T07-38-27.pdf`, and `presentation_2026-09-24T07-46-51.pptx` form the first group. The recording shows a four-page task and a slide-1 change from “转身面向公众” to “面向公众”. Its opening prompt adds a request to review the outline and per-slide prompts; it is not identical to the owner-supplied prompt.

The PPTX has four 16:9 slides with native text and image objects: 18/15/32/18 text-bearing shapes and 1/1/4/1 picture objects by slide. This establishes editable structure, **not** an editor/save/reopen test. The PDF's slide-2 diagram renders as a large black shape, so it is not the primary visual reference. Its PPTX and PDF are distinct exports, not a verified before/after pair.

最新下载的 `presentation_2026-09-24T07-46-51(1).pptx` 和 `飞书20261013-172003.mov` 与第一组对应文件的 SHA-256 完全相同，属于重复文件，不能补上第二组 PPTX。文件名、大小和校验值见 [sources.json](sources.json)。

## Public references and implementation / 公开参考与实现

- [Codex promotional video](https://www.bilibili.com/video/BV1TppR6xEj7/) and [DSH promotional video](https://www.bilibili.com/video/BV1Xcpt6REjc/) were identified from the supplied promotional index. Byte identity with the downloaded recordings has not been established.
- [PPT Workflow](../../workflows/ppt-workflow/README.md) is the existing implementation.
- The [featured League of Legends deck](../../skills/featured/ppt-workflow/featured.yaml) is a different case, excluded from Palace Museum evidence.

The private promotional index, raw recordings and credentials are not included. Existing museum/image sources retain their own rights; this repository does not establish a new license for third-party imagery.

## Still needed / 待补

- Matching DSH PPTX and exact original local-edit instruction / DSH 对应 PPTX 与完整修改指令。
- Original collected material/image inventory with attribution / 原始采集素材及完整来源清单。
- Current-version runs on both hosts, before/after outputs and actual edit/save/reopen checks / 两宿主当前版本实跑、修改前后成果及编辑保存重开。
- Direct-Agent comparison and independent reproduction / 原 Agent 对照及非实现者复现。
