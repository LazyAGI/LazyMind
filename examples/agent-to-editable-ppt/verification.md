# Verification / 验收记录

## Current status — 2026-10-13

**Blocked by missing original case materials. No current-version host E2E or exported deck has been produced. / 缺少原始案例材料，未完成当前版本宿主 E2E，未生成导出成果。**

Repository baseline inspected: `84a1a5b7f1d1e9ef59022f2c57ab7dfe1fd54ac2`. Integration docs and source were read. An isolated 8091 runtime reported `ready`, but its existing build has not been verified against this baseline; that check is not an example pass. No services were restarted for this preparation.

| Check | Codex | DSH | Required evidence |
| --- | --- | --- | --- |
| Plugin installation and correct instance | Not run | Not run | Host/plugin/build versions; successful capability discovery |
| Original material read | Blocked | Blocked | Input inventory and checksums |
| Outline/material review | Not run | Not run | Before/after adjustment and user approval |
| Deck V1 | Not run | Not run | Actual run, artifact revision and opened export |
| Local edit and preserved pages | Not run | Not run | V1/V2 plus page comparison |
| Editable export | Not run | Not run | Editor version, edited element, saved/reopened file |
| Final state agrees with output | Not run | Not run | Completed state and matching artifact revision |
| Direct-Agent comparison | Not run | Not run | Same-input runs; setup and recurring effort separated |
| Independent reproduction | Not run | Not run | Reviewer/date, steps followed, help required and outcome |

## Record an actual run / 实跑记录要求

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
