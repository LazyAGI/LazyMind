# Verification / 验收记录

## Current status — 2026-10-15

**Codex deck generated, local title edit and Keynote roundtrip verified; full acceptance pending. / Codex 已生成成片并验证局部改名和 Keynote 编辑保存重开，完整验收尚未通过。**

See [the current review and actual exports](outputs/codex/review-2026-10-15.md). The same run generated four 16:9 slides. The full constrained edit exposed a parser bug; a literal-command workaround succeeded, with pages 2–4 unchanged. Product PDF/PPTX downloads succeeded after installing the existing editable-export dependency. Keynote 14.5 edited a native title, saved/reopened, and exported/reopened PPTX. Fonts differ; footer corrections, deployment verification of the parser fix, DSH's current PPT run, final approval and independent reproduction remain pending.

四页 PDF 已逐页查看；署名已补入讲稿，并确认导出保留。可见页脚修改三次尝试遇到模型 HTTP 503。Keynote 首次打开需要负责人协助；后续由协调者完成编辑与重开，不能算非实现者复现。原始提示词和历史宣传材料仍是参考，不替代当前实跑。

| Check | Codex | DSH | Evidence / remaining work |
| --- | --- | --- | --- |
| Plugin connection | Workflow initiated; private 8091 connector recovery recorded | Installed/preflight passed | Host setup and recovery are explicit costs |
| Materials and outline | Reviewed, unsuitable images replaced | Current PPT run pending | [Review and rewind](outputs/codex/review-2026-10-14.md) |
| Deck | Four pages generated and visually inspected | Pending | [Current review](outputs/codex/review-2026-10-15.md) |
| Review rewind | Live waiting-review click passed | Pending | [State checks](outputs/codex/rewind-live-check.json) |
| Local title edit | Literal workaround passed; constrained command bug fixed in source | Pending | 94 Workflow script tests; pinned-run deployment not claimed |
| PDF/PPTX export | Downloaded and inspected; attribution retained in PPTX notes | Pending | [Exports](outputs/codex/review-2026-10-15.md#export-and-editor-check--导出与编辑器验收) |
| Editor save/reopen | Keynote 14.5 native text edit and PPTX roundtrip passed | Pending | Missing font; no PowerPoint/WPS pass claimed |
| Final review and state | Pending: visible footer corrections blocked by 503 | Pending | Current task not approved as final |
| Independent reproduction | Pending | Pending | Owner arranged colleague; record their result separately |

The records below preserve earlier checkpoints; they describe what was known at that time, not the current status. / 以下保留此前时点的原始记录，当前状态以上表为准。

## Record an actual run / 实跑记录要求

### Current Codex run — 2026-10-14 / 当前 Codex 实跑

The installed Codex plugin started the original Palace Museum task on the isolated 8091 application, with four slides, a general-public audience and AI backgrounds enabled. Workflow session: `mcp-64c7a99d9707aea53a0102ccb383cb8f`; revision: `17436653-d491-4098-88d9-9b871f2e407d`. Application source: `7c8f9ddef8c3b0021986f4f505d1eb8571694794` plus local changes, including the schema compatibility fix below. This is a development checkout, not a clean released build.

需求分析的前两次执行未通过：第一次请求被 Kimi 的工具 schema 校验拒绝；将 `Any` 描述成六类 `anyOf` 后，第二次执行返回 “Model call ended without a complete answer”，未执行工具、未生成成果。第二次失败的具体结束原因没有被保留，不能把它直接归因为第一次的 schema 拒绝。

The first attempt failed Kimi tool-schema validation. A six-type `anyOf` workaround passed request admission, but the second attempt ended without a complete answer and produced no executed tool calls or artifacts. Its exact finish reason was not retained. The two failures must not be reported as the same error.

当前改为仅删除 `ArtifactSaveItem.value` 的 `title` 注释，保留 `Any` 和其他字段校验。完整导出对比确认仅此一处变化；95 项相关回归测试通过。仅重载 8091 Chat，8090 未变更。该结果证明局部兼容修复的测试范围，不代表 PPT 工作流、DSH、导出编辑或独立复现通过。

The replacement removes only the `title` annotation from `ArtifactSaveItem.value`, preserving `Any` and other field validation. The full exported descriptor comparison found exactly that change; 95 related regression tests passed. Only 8091 Chat was reloaded; 8090 was unchanged. Workflow completion, DSH execution, editable export and independent reproduction remain pending.

Attempt 3 successfully saved five artifacts, including the [actual requirement analysis](outputs/codex/requirement-analysis.md), in about 45 seconds. It then failed `MEDIA_CAPABILITY_DEPENDENCY_MISSING`: AI backgrounds were enabled but no image-generation model was selected. This run passed the previously failing schema/tool-call path; it did not complete the step. The existing SiliconFlow `Qwen/Qwen-Image` model was subsequently selected in 8091 settings; actual generation remains unverified.

第三次实跑已成功保存五项成果，需求简报正确包含四页、普通公众、AI 底图和真实检索主体图片。约 45 秒后因未选择文生图模型而失败，属于新的配置阻塞。已在 8091 选择现有 SiliconFlow `Qwen/Qwen-Image`，尚不能把配置成功当作图片生成成功。

After configuration, attempt 4 reused saved requirements and passed. Material collection then completed three image searches and registered four images, but the following model call ended without a complete answer before saving `material_summary`. Three diagnostic API replays reconstructed from the recorded inputs returned valid `save_artifacts(material_summary)` calls; all three passed the production response parser and argument-schema validation. These replays executed no tools and do not count as Workflow recovery. See [issues and fixes](issues.md).

配置补齐后，第 4 次需求分析复用已保存成果并成功。随后素材收集完成三次图片搜索、注册四张图片，但下一轮模型调用未正常结束，`material_summary` 没有保存。按记录重建请求的三次接口重放均返回有效保存参数，现有解析器和参数校验均通过；诊断未执行工具，不算工作流恢复成功。完整问题记录见 [issues.md](issues.md)。

After a subsequent Kimi HTTP 403 explicitly identified its five-hour quota limit, only the 8091 LLM selection was changed to `deepseek-flash`. Material collection attempt 3 (`pss_6052c1df4d454d509ede935cde51d83c`) succeeded in about 155 seconds and left four selected images plus the [raw material summary](outputs/codex/material-summary.md). This is step-execution evidence only: disputed statistics, image-caption accuracy and image rights remain unreviewed. No completed deck or editable-export acceptance is implied.

Kimi 五小时额度耗尽后，仅切换 8091 对话模型为 DeepSeek。素材收集第 3 次执行约 155 秒成功，保存四张选中图片和素材摘要。统计口径、图片内容与说明是否一致、授权条件仍待复核；尚未生成完整 PPT，也未完成导出编辑验收。

### Connector preflight — 2026-10-13 / 接入预检

The owner authorized switching the shared local Agent connection from 8090 to 8091. Existing credentials and connector configuration were backed up privately. Fresh access and refresh credentials were obtained from 8091 through its local session endpoint, then saved with the existing CLI session command. The previous environment's tokens were not reused. This is a single-target connection switch, not simultaneous Agent access to both environments.

用户授权将这套本机 Agent 连接切到 8091。已备份旧配置，从 8091 重新取得并保存完整登录凭据；这次选择单一连接目标，未修改产品代码以支持两个环境同时接入。

- Codex Workflow plugin `0.1.0+codex.c1b5b74cb7b5a351`: installed using the desktop application's bundled CLI after a stale system CLI failed. The installer reports that a new Codex task is needed to load its tools. A subprocess launched from the installed MCP configuration initialized with the 8091 origin and successfully called `knowledge.list` and `workflow.list`. This is a protocol preflight, not a Codex conversation E2E.
- DSH `0.2.0-rc.2`: MCP and Workflow bundle installed in a separate test profile; web host restarted on port 3081. HTTP 401 confirms its login gate is present, not that a signed-in browser session has passed.
- Both hosts' scoped pairing credentials successfully made a read-only Bridge relay request (HTTP 200). No workflow execution, review approval or document-generation result is implied by this check.
- 8090 and 8091 frontend PIDs were unchanged and both returned HTTP 200 after the switch. The application services were not rebuilt by this preflight.

接入阻塞已解除，但上表中的生成、审核、导出、原 Agent 对照及独立复现仍未通过验收；不能把 MCP 工具调用成功写成整篇案例完成。

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

### Page-plan review after maintenance — 2026-10-14

Four page plans were produced, but visual inspection found a watermarked architecture image, a book cover presented as an object close-up, and an exhibition booth described as conservation imagery. Dated collection figures also need correction. See the [review and proposed copy](outputs/codex/review-2026-10-14.md). No rendered deck or review approval is claimed. / 四页方案已产出；逐图检查发现水印、书封面与文物特写混淆、展台与修缮场景混淆，数据时点也待修正，暂不通过。

After maintenance, the chat's existing MCP transport was closed and the global connector targeted 8090. A private 8091-only MCP proxy was initialized using the existing local session mechanism. The global connector and 8090 service were not changed. This recovery is additional setup cost, not seamless plugin continuation. / 维护后原 MCP 连接关闭，全局连接目标回到 8090；本次单独恢复 8091 私有连接，未改全局配置，不宣传无感续跑。


### Rework and notification recovery — 2026-10-14 / 重做与通知恢复

Material rework completed after the review instructions were saved to the existing requirements. All four replacement files were visually inspected; the earlier watermarked image, book-cover and exhibition-booth mismatches are absent. The cup photo is somewhat soft, and final-slide layout and attribution still need review. The [new background prompts](outputs/codex/background-prompts-after-review.md) use restrained graphic decoration rather than simulated documentary photography. These intermediate results do not establish completed-deck acceptance.

审核要求已触发素材重做，四张实际图片已检查；金杯照片略软，最终成片和署名仍需验收。底图提示词重做完成，保留真实照片与 AI 装饰背景的边界。

The global Bridge was left targeting 8090 after maintenance. A private Bridge on loopback port 19092 and the existing dispatcher with its supported `--bridge-url` option were started against the isolated 8091 credentials. The existing matching connector pairing was copied privately with mode 0600; its account scope was verified before reuse. A read-only Bridge relay returned HTTP 200. This is additional recovery setup, not a product fix or proof of automatic notification delivery; actual queue receipt is checked separately. Global credentials, the default Bridge and both application runtimes were unchanged.

维护后的全局连接未改动；本次复用已有参数启动独立 19092 Bridge 与通知进程，只连接 8091，配对文件私有保存。只读转发通过，通知实际到达仍需单独核对，不能把启动成功写成自动续跑完成。
