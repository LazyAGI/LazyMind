# Verification / 验收记录

## Current status — 2026-10-13

**Historical materials inspected; current-version E2E remains pending. / 已检查历史材料，当前版本宿主 E2E 仍待验收。**

The owner-supplied original prompt, two recordings, two PDFs and one distinct PPTX have been inspected. The selected DSH PDF is included as a lossless ZIP with a rendered preview; its corresponding PPTX is missing. The latest PPTX and recording downloads are byte-identical duplicates of the first Codex group. See [sources](sources.md) and [checksums](sources.json). No new workflow output has been produced in this preparation.

已核对原始提示词、两段录像、两份 PDF 和一份不同内容的 PPTX。主展示选 DSH，另附 Codex 参考；二者不能拼成同一次运行。新下载文件只是第一组的重复件。本轮尚未新生成工作流成果。

Repository baseline inspected: `84a1a5b7f1d1e9ef59022f2c57ab7dfe1fd54ac2`. Integration docs and source were read. An isolated 8091 runtime reported `ready`, but its existing build has not been verified against this baseline; that check is not an example pass. No services were restarted for this preparation.

| Check | Codex | DSH | Required evidence |
| --- | --- | --- | --- |
| Plugin installation and correct instance | Not run | Not run | Host/plugin/build versions; successful capability discovery |
| Original material read in current run | Not run | Not run | Historical exports inventoried; collected source-image bundle still missing |
| Outline/material review | Not run | Not run | Before/after adjustment and user approval |
| Deck V1 | Not run | Not run | Actual run, artifact revision and opened export |
| Local edit and preserved pages | Not run | Not run | V1/V2 plus page comparison |
| Editable export | Not run | Not run | Editor version, edited element, saved/reopened file |
| Final state agrees with output | Not run | Not run | Completed state and matching artifact revision |
| Direct-Agent comparison | Not run | Not run | Same-input runs; setup and recurring effort separated |
| Independent reproduction | Not run | Not run | Reviewer/date, steps followed, help required and outcome |

## Record an actual run / 实跑记录要求

### Historical artifact inspection / 历史文件检查

| Check | Observed result / 实际结果 | Limit / 边界 |
| --- | --- | --- |
| Selected DSH PDF | Four pages, approximately 16:9; visual review and contact sheet available / 四页，约 16:9，已查看及制作预览 | Rasterized pages; not editability evidence / 栅格页面，不证明可编辑 |
| First-group Codex PPTX | Four 16:9 slides, native text and image objects / 四页，含原生文本及图片对象 | Not the selected DSH deck; no actual editor/save/reopen test / 非 DSH 成果，未实测编辑保存重开 |
| First-group PDF | Slide-2 diagram has a large black shape / 第二页图示有明显黑块 | Not chosen as primary showcase / 不作为主展示 |
| Selected PDF packaging | ZIP extraction hash matches the original PDF / ZIP 解压后与原 PDF 哈希一致 | Compression does not repair or change the source / 压缩没有修复或改变源文件 |
| Recordings | Review, local edit and export stages observed / 可见审核、局部修改、导出 | Sampled review; versions unknown; duration is not benchmark time / 抽帧检查，版本未知，时长不是效率指标 |

Keep these observations separate from the host acceptance table above. Existing recordings cannot pass the current-version checks.

For each host, record: date; LazyMind deployed build and dirty-code status; host/plugin/model versions; workflow revision; source hashes; run/artifact identifiers; final state; output paths; user review actions; retries and manual corrections. Record only values actually observed; unknown values remain unknown. Redact credentials and private host paths.

每个宿主分别记录上述信息。源码版本、服务健康、工具调用成功、产物完成和独立复现是不同层次，不合并成一个“通过”。

## Pass criteria / 通过条件

- Original material is used correctly; no fabricated claims or references / 正确使用原材料，不虚构内容或来源。
- User can inspect and approve the actual intermediate result / 审核针对真实中间成果。
- Requested local change is present; untouched pages retain content and layout / 指定修改生效，其余页面保留。
- An exported file can actually be edited, saved and reopened / 导出可实际编辑、保存及重开。
- Extra setup and panel actions are visible, and the claimed benefit follows from observed work / 配置和面板操作如实呈现，优势有操作证据。
- Another person can follow the published instructions without undocumented assistance / 非实现者无需隐藏步骤即可复现。

When a check fails, retain the failure and narrow the claim. Do not use an old promotional recording to mark a current check passed.
