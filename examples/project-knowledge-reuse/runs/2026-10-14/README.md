# Current-version run / 当前版本实跑

**Partial acceptance, not a published success claim / 部分验收，不是成功宣传结论。**

The same four fixed TXT files were imported once into the isolated LazyMind instance at `http://127.0.0.1:8091`, knowledge base `showcase-zhice-v1` (`ds_b4cbf46595d98dd2a0e872c1d96bf03d`). Codex and DSH use that same knowledge base through LazyMind's installed MCP integration. The Agent writes the outputs; LazyMind retrieves and reads source documents. No Workflow is invoked for this case.

四份固定输入只导入一次。Codex、DSH 使用同一知识库，LazyMind 负责检索和读取，宿主负责写作。实际输入的提示词、工具调用摘要和原始输出分别保存在下列目录；不得把调用成功或文件生成当作整体验收通过。

| Arm / 分组 | Host / 宿主 | Model / 模型 | Evidence / 证据 |
| --- | --- | --- | --- |
| Codex + LazyMind | Codex CLI `0.162.0-alpha.17.2`; installed plugin `0.1.0+codex.c1b5b74cb7b5a351` | `gpt-6-astra`, high; existing user selection | [codex-plugin](codex-plugin/evidence.json) |
| Codex direct files | Same CLI/model; LazyMind plugin disabled | Same as plugin arm | [codex-direct](codex-direct/evidence.json) |
| DSH + LazyMind | DSH `0.2.0-rc.2`, headless; existing integration's MCP configuration | `deepseek-official / deepseek-flash` | [dsh-plugin](dsh-plugin/evidence.json) |
| DSH direct files | Same headless host/model; LazyMind MCP omitted | Same as plugin arm | [dsh-direct](dsh-direct/evidence.json) |

This verifies CLI/headless execution, not the DSH browser panel or a new installation by an independent user. The two hosts use different models; compare plugin vs direct files **within** each host, not Codex vs DSH speed. Recorded durations cover one invocation only, exclude prior setup/review and include variable provider latency. No averages or efficiency percentages are claimed.

这是 CLI/headless 实跑，不等于 DSH 浏览器面板或非实现者从零安装通过。比较仅限同宿主同模型的插件组和直接文件组；单次调用时长不含首次配置、索引和人工审核，不能宣传为完整任务耗时或效率提升。

## Session and source boundaries / 会话与来源边界

- A starts a blank session; B and C resume its exact ID. D starts another blank session in a separate empty workspace, with only the original D prompt and knowledge ID (or input directory in the direct arm). A–C outputs are not supplied to D. Session IDs are retained in each `evidence.json`.
- Codex memory and multi-agent features are disabled for these invocations. DSH uses its normal headless profile with the installed MCP entry supplied as a temporary overlay; the web profile is not edited. Logs are checked for unintended source access; a fresh session is not an independent human reproduction.
- The direct arm reads the same persistent input directory. It is not forced to re-upload or copy files at every step or when creating D.
- Output text is retained as generated, including mistakes. Only private absolute path prefixes are redacted; raw and published hashes are recorded. Model reasoning, credentials and full private session logs are excluded.

A 新会话、B/C 续用同一会话、D 独立新会话；D 未获得 A–C 成果。对照组正常复用同一个资料目录，不人为增加重复上传成本。原始输出中的问题保留，仅脱敏私有路径；完整日志留在仓库外，仓库记录可核查的工具参数与结果摘要。

## Findings requiring attention / 实跑发现

1. **Source navigation is not automatic acceptance.** Codex's initial outputs prepend the frontend origin to the tool's API-relative `original_file.download_url`. A real request to that resulting `/datasets/...:download` URL returns HTTP 200 **HTML for the app**, not the source. The corresponding `/api/core/datasets/...:download` request with authentication returns the correct TXT bytes. Do not use HTTP 200 alone as proof. The existing logged-in document page `/lib/knowledge/knowledge/<KB_ID>/<DOC_ID>` was opened for `design.txt`; it displays the original text and the R-05 sentence. These are different route/authentication contracts. The generated download links remain a known output defect; no source-link pass is claimed.
2. **Parsing metadata is inconsistent with successful reads.** `knowledge.document.list/get` reports `parse_status: NONE` for these TXT documents while searches return chunks and full text reads succeed. Codex A's four returned texts were byte-identical to the fixed UTF-8 inputs and were not truncated. This is not evidence that indexing failed; it is also not a reason to silently relabel the returned status.
3. **The fixture has shifted section references.** Review R-05 says design §3 / PRD §3; the actual ambiguous phrase is in design `[section-4]`, and the explicit PRD rule is in `[section-4]` and `[section-5]` FR-002. Both hosts noticed the mismatch. Preserve the original fixtures and cite the actual section and item.
4. **DSH's provenance wording needs review.** B reads the review again and searches the other documents, but its generated read ledger can imply all three full documents were re-read in that turn. C also repeats search queries from B in its run ledger. The actual call trace, rather than the model's self-report, defines what happened. Core R-05 reasoning is source-supported; these ledger statements are not silently repaired.
5. **Harness setup failures are retained privately.** The desktop's app-managed MCP transport could not load in standalone Codex CLI; a subsequent override incorrectly quoted plugin keys. The accepted run uses per-invocation configuration and the installed plugin, without changing global configuration. These are setup costs, not LazyMind retrieval failures.
6. **No extra convenience over four local files has been demonstrated yet.** Plugin runs show that two hosts can reuse one knowledge base without attaching the documents again. Direct runs can also reuse one local directory. This fixture alone does not establish fewer manual actions, better answer quality, or faster completion. First-use plugin/KB preparation adds cost. Do not turn cross-host access into a superiority claim without a user scenario that needs it.

已记录：下载路径被误当网页链接、解析状态元数据不一致、原始案例章节编号偏移、DSH 自述的逐轮读取记录不够准确，以及 CLI 接入准备失败。实际源文可通过已有知识库文档页面查看；但未经修正和重验，不将生成报告的链接标为通过。

## Pending / 尚待完成

- Correct initial generated source links at the product/host boundary; a reviewed navigation index is available below. Both hosts and direct-file arms completed A–D; DSH search was rerun in fresh session H. / 初次生成链接仍需产品或宿主层修正，已有审核补充的原文导航；DSH 已在新会话 H 补齐检索。
- Obtain a non-implementer's reproduction record. / 非实现者独立复现。
- The owner confirmed cross-Agent/cross-conversation reuse as the showcase focus; no superiority claim over local files is required. / 用户已确认聚焦跨 Agent、跨会话复用，不要求证明优于本地文件。
- Create the independent PR after the PPT PR, as requested. / 按计划在 PPT PR 之后提交独立 PR。


## Recorded outcome / 已记录结果

The owner confirmed on 2026-10-14 that this case should focus on cross-Agent and cross-conversation reuse. Skill-to-Workflow remains deferred; this example does not verify Workflow step enforcement.

用户已确认本例聚焦跨 Agent、跨会话复用，Skill 转 Workflow 等补充材料。本例不验证 Workflow 强制步骤执行。

| Deliverable / 成果 | Codex + LazyMind | DSH + LazyMind |
| --- | --- | --- |
| Summary / 摘要 | [A original](codex-plugin/01-summary.md), [I corrected links](codex-plugin/01-summary-reviewed.md) | [A](dsh-plugin/01-summary.md) |
| R-05 evidence / 有据追问 | [B original](codex-plugin/02-scope-evidence.md), [I corrected links](codex-plugin/02-scope-evidence-reviewed.md) | [B original](dsh-plugin/02-scope-evidence.md), [F corrected ledger](dsh-plugin/02-scope-evidence-reviewed.md) |
| Local proposal / 局部修改建议 | [C](codex-plugin/03-design-revision.md) | [C original](dsh-plugin/03-design-revision.md), [F corrected ledger](dsh-plugin/03-design-revision-reviewed.md) |
| Fresh-session handoff / 新会话交接 | [D](codex-plugin/04-handoff.md) | [D original](dsh-plugin/04-handoff.md), [H fresh-session rerun](dsh-plugin/04-handoff-post-maintenance.md) |

- All four arms produced A–D, sixteen initial files in total. A/B/C share a session within each arm; D has a different session ID and an empty output workspace. The four arms have distinct session IDs. Plugin calls remain scoped to the same knowledge base and source document IDs.
- Both plugin handoffs retain R-01–R-05, three P1/two P2, the unapproved state, and `blocked`. Their local C proposals do not become KB facts. A subsequent full read confirmed all four source texts still match the fixed inputs byte for byte: [source integrity](source-integrity.json).
- Codex D executed search plus four full reads. DSH D read all four originals but omitted the requested search. Recovery E was attempted after the initial run; the instance refused connections. It was interrupted and is not accepted as retrieval or handoff recovery. No failed recovery was substituted for the successful original D.
- DSH F corrected B/C's per-turn read ledger in separate files, keeping the original output. It used existing conversation evidence/local output files, not new source retrieval. DSH's edit guard rejected edits before a fresh file read; the host reread and retried. Those errors remain in the trace.
- At 16:35 the 8091 process record changed and the application subsequently refused connections. The owner confirmed intentional maintenance and instructed us not to start it. PPT execution and remaining KB network checks are on hold; 8090 was not changed.

这 16 份成果证明两种宿主确实能使用同一知识库，并在新的会话中重新取得原文、保留未决问题。它们不证明自动遵循每一项提示词、所有引用链接可点击或无需人工审核。DSH 漏检索和取证记录修正均计入恢复成本。

## Environment limits / 环境边界

The run used macOS, a local development instance on 8091, the CLI/plugin versions listed above and the existing fixed input revision. The application was a development build with local fixes, not a clean release. A fresh complete build fingerprint was not captured before maintenance; its exact deployed revision is therefore **unverified for this record**, not inferred from branch HEAD. The installation/pairing and KB import were already prepared; this is not a from-zero setup timing. DSH used headless MCP, not its graphical Workflow panel. The JSON event projection can truncate large DSH tool results; full private logs are retained, and truncated projections are not claimed as byte-equality evidence.

服务是带本地修复的开发构建；维护前未补齐完整构建指纹，因此本记录不把仓库 HEAD 当成已验证部署版本。首次安装、配对和入库已预先完成；不宣传从零完成时间。DSH 大结果的事件投影可能截断，只有实际完整返回并核对的内容才算逐字验证。

## Post-maintenance recovery / 维护后恢复

- G could not resume D: DSH rejected the migrated workspace because its recorded working directory no longer existed. Exit 1; no model output or knowledge retrieval. Original session metadata was preserved. / G 因工作目录迁移被 DSH 拒绝续跑，未改历史会话。
- H started an empty workspace and a new session (`session-b6faa0c1-0eb8-4515-b151-58c635f6d1dd`), with the D task plus explicit search/ledger/link instructions, no previous outputs or summaries. It executed three `knowledge.search` calls followed by four document reads and generated a [new handoff](dsh-plugin/04-handoff-post-maintenance.md), exit 0, 71.22 seconds for this invocation. This is a fresh-session rerun after feedback, not proof that the original D prompt always works. / H 新会话已实做三次检索及四份读取，保留全部五项未决评审和 blocked 状态；补充提示与恢复成本不隐去。
- The existing global connector pointed to 8090 after maintenance. H used a private 8091 credential directory with the existing `lazymind mcp proxy` command in a temporary DSH configuration. No global connector or 8090 service was changed. This adds setup and does not validate automatic desktop notification recovery. / 单独恢复 8091 MCP 连接，没有切换全局连接；不算桌面通知无感恢复。
- All four browser source pages were opened and the relevant original passages checked; [reviewed navigation](source-navigation.md) provides the links. Coordinator MCP reads after H reconfirmed unchanged complete texts: [checks](post-maintenance-checks.json). Original generated API-relative links remain an output defect; this manual index is the disclosed workaround. / 四份原文导航均实测，并再次核对源文未变；初次生成的错误链接保留，导航索引是明确披露的补救。
- The repository now retains sixteen initial outputs, two provenance-ledger corrections and one fresh-session rerun. No independent human reproduction or graphical DSH-host acceptance is claimed. / 共保留 16 份初次输出、2 份取证修正版和 1 份新会话重跑成果；独立复现与 DSH 图形宿主验收仍未完成。

### Codex citation recovery I / Codex 引用修正 I

Codex resumed its A–C session after migration, listed the four knowledge documents and saved separate reviewed A/B files. It replaced 46 + 23 incorrect links with the four browser-verified document-page routes, preserving the original outputs and body claims. The reviewed copies disclose that the coordinator checked navigation and that the original retrieval ledger belongs to the earlier turns. No new search, full-text read or source write is claimed for I. / 同一 Codex 会话完成两份来源链接修正版，保留初次输出；本轮只列文档核对身份，不冒充重新检索或全文读取。

I used a per-invocation isolated MCP proxy configuration because the installed global connection targets 8090 after maintenance. This recovery is not proof of seamless installed-plugin reconnection. Exit 0; invocation duration 61.37 seconds. The underlying API-relative download-path ambiguity is still an open product issue; generated reports are recovered with a disclosed navigation instruction. / 本轮使用临时隔离连接，未改全局配置；这是已披露的提示词与连接补救，底层链接语义问题仍记录为未修复。
