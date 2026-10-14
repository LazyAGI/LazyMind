# Verification / 验证

**Not accepted yet / 尚未整体验收。** Current-version Codex CLI and DSH headless runs now have real outputs and call traces. Initial source-link defects and independent reproduction remain open; DSH search recovery passed in fresh session H; provenance-ledger corrections are retained separately. See the [2026-10-14 run](runs/2026-10-14/README.md). / 已有真实宿主输出与调用记录；初次来源链接和独立复现仍待处理；DSH 新会话 H 已补齐检索；取证记录修正版单独保留，不宣传效率提升。

输入来源基线 / Source baseline: `84a1a5b7f1d1e9ef59022f2c57ab7dfe1fd54ac2`. Document hashes: [sources.json](sources.json).

## Current record / 当前记录

| Check / 检查 | Codex | DSH |
| --- | --- | --- |
| Plugin connection to intended instance / 插件连接目标实例 | CLI: verified 8091 / 已验证 | Headless: verified 8091 / 已验证 |
| Four files indexed and readable / 四份文件索引及读取 | Search + four exact full-text reads / 检索及四份完整原文 | Search + four reads; metadata says NONE / 检索读取成功，状态元数据异常 |
| A: sourced summary / 带来源的摘要 | Generated; source links need correction / 已生成，来源链接待修正 | Generated; content audit recorded / 已生成并记录核查 |
| B: retrieve and explain R-05 / 检索与解释 R-05 | Generated, actual positions identified / 已生成、定位真实章节 | Core explanation supported; ledger corrected in F / 核心解释有据，取证记录已于 F 轮修正 |
| C: bounded revision proposal / 限定范围修改建议 | Generated; source bytes unchanged / 已生成，原文未变 | Generated; source bytes unchanged; ledger corrected in F / 已生成，原文未变，取证记录已修正 |
| D: fresh-conversation handoff / 新会话交接 | Generated in fresh session; searched and reread all four / 新会话完成检索与四份读取 | D omitted search; H rerun searched three times and read all four / D 漏检索，H 新会话已完成三次检索与四份读取 |
| User opens outputs and finds sources / 取得成果与定位原文 | Files saved; source page verified; generated links fail / 文件已保存，原文页面可用，生成链接错误 | Files saved; IDs/sections available; no automatic source-link pass / 文件可用，有 ID/章节，未判自动链接通过 |
| Direct-Agent comparison / 直接使用资料的对照 | A–D outputs recorded / A–D 真实输出已记录 | A–D outputs recorded / A–D 真实输出已记录 |
| Non-implementer reproduction / 非实现者独立复现 | Pending / 待执行 | Pending / 待执行 |

## Run evidence / 实跑证据

For each host, record / 每个宿主单独记录：

- Run date, OS, host and plugin versions, actual deployed LazyMind revision/build, model/provider, relevant retrieval settings and input hashes. Mark an unverifiable build as unknown, not repository HEAD. / 日期、系统、宿主、插件、实际部署版本、模型、检索设置与输入校验和；未知项如实标记。
- Instance origin, knowledge base ID, four document IDs and index completion. Keep credentials, pairing files and private runtime configuration outside Git. / 实例、库与文档 ID、索引完成证据；凭据和私有配置不入库。
- Prompts as submitted, language, session boundary, actual tool names/arguments/results, source reads and citations, errors, retries and user corrections. / 实际提示词、语言、新会话边界、工具调用与读取、引用、报错、重试和人工修正。
- Setup and recurring actions separately; note pre-existing setup, host project context and memory. / 分开记录首次准备与后续操作，说明预先配置及宿主项目上下文、记忆。
- The four actual output files, source navigation checks, proposal diff, and user-visible file access. Preserve failures instead of selecting only successful attempts. / 保存四份真实输出、来源定位检查、修改差异和成果打开记录，保留失败尝试。
- Independent reproducer's environment, steps followed, help needed and observed outcome. A self-check or second automated run is not independent user reproduction. / 独立复现者的环境、步骤、求助与结果；自检或再跑一次不能代替独立用户复现。

Store sanitized completed evidence under a dated host-specific run directory when a run exists. Do not create an empty “passed” record. / 有真实运行后再建立按日期与宿主区分的脱敏记录，不预建“通过”记录。

## Acceptance references / 验收依据

These references are for the reviewer, **not knowledge base inputs or extra hints to the tested Agent**. Judge equivalent wording by evidence, not exact string matching. / 以下只供评审使用，不上传、不额外提示被测 Agent；允许等价表述。

| Task | Required source-grounded result / 需要出现的有据结论 |
| --- | --- |
| A | Zhice is fictional; proposed public-source research and human review, with role boundaries and immutable confirmed versions. PRD has 12 FR / 18 AC **designs**, not passed tests. Review is returned for revision; handoff remains blocked. / 虚构案例，公开资料与人工复核，角色边界、确认版本保留；12 项 FR / 18 项 AC 不是通过记录；评审退回、交付阻塞。 |
| B | Review R-05 links design §3 wording to PRD §3. Design's reconfirmation wording is ambiguous out of context; PRD requires a new task after starting. R-05 is P2; clarifying it does not close R-01–R-03 P1 gates. / 对应方案 §3、PRD §3；区分启动前后；澄清 P2 不解除三项 P1 门禁。 |
| C | Shows the actual original wording and a localized replacement. Source files remain unchanged. Labels proposal and unconfirmed status, preserving outstanding review items. / 原文与替换片段准确，范围局部，源文件不变，建议待确认，不擅自结案。 |
| D | Reads sources anew. R-01: unaccepted core rules; R-02: retention/deletion/sensitive-data policy; R-03: technical feasibility/nonfunctional constraints; R-04: prototype coverage; R-05: scope wording. All remain unresolved in these source snapshots. Roles are suggestions, people/dates unspecified; handoff blocked. / 新会话重新读取，找回五项未决问题和关闭条件，角色仅为建议，无已指定个人或日期，交付仍阻塞。 |

Check every important citation against the actual source, especially assertions of readiness, authority and completed testing. Opening an HTML fixture does not prove its described tests were performed in this run. Sources mention additional artifacts outside this bundle; claims about them must be attributed as document statements, not direct inspection. / 逐项核对来源；展示稿记载不等于本轮亲自测试，未提供材料只能按文中陈述归因。

## Comparison / 对照

Run the same four tasks with the same host, model, input bytes and output language. For the direct-Agent arm, give it the `inputs/` directory using its normal supported file access or attachment path; replace plugin-specific instructions with direct reading and cite filename/section. Do not force repeated uploads when the host can reuse files. Use separate sessions/workspaces so outputs from one arm do not leak into the other. / 同宿主、模型、输入和语言；对照组正常读取同一目录或附件，替换插件指令，以文件名和章节引用，不故意迫使重复上传；两组会话、成果隔离。

Record for **each arm and each step** / 两组逐步记录：

| Observation / 观察项 | Recording rule / 记录规则 |
| --- | --- |
| First-use preparation / 首次准备 | Installation, permissions, file selection, upload/indexing, wait and failures; identify already-completed setup. / 安装、授权、选文件、上传/索引、等待及失败，注明预先完成部分。 |
| Later manual actions / 后续人工操作 | Material reselection, path/ID entry, context copying, clarification and source checking. / 重选材料、输入路径或 ID、搬运上下文、澄清、核查。 |
| Output quality / 成果质量 | Missing/incorrect citations, lost unresolved items, unsupported approval/test claims, revision scope. / 引用错误与缺失、遗漏问题、无据审批或测试结论、修改范围。 |
| Recovery / 失败恢复 | Retrieval/read errors, missing documents and what the user had to do. / 检索读取错误、材料缺失和人工处理。 |
| Timing, if measured / 如计时 | Include setup and user review; record raw intervals and conditions, no invented averages. / 包括首次成本与审核，保存原始区间及条件。 |

The owner confirmed the scope on 2026-10-14: demonstrate cross-Agent and cross-conversation reuse, not superiority over local files. Acceptance requires both hosts to retrieve the same original sources, preserve citations and unresolved states in fresh conversations, and disclose setup and recovery. Keep the direct-file results as context; do not invent speed or effort gains. / 用户于 2026-10-14 确认以跨 Agent、跨会话复用为主线。验收关注两种宿主使用同一原文、新会话保留依据与未决状态，并如实披露准备和恢复操作；不以优于本地文件作为通过条件，不虚构效率。

## Preparation checks / 准备检查

Run `python3 examples/project-knowledge-reuse/prepare_inputs.py --check` and `git diff --check`. Inspect extracted article text against the HTML, including tables, diagram labels and caveats. These checks validate input packaging only, not parsing in LazyMind, plugin operation, retrieval quality or the claimed advantage. / 生成一致性、差异和原文提取检查仅验证输入整理，不替代产品实跑。

2026-10-13 preparation result / 准备检查结果：

- Regeneration check passed for all four pinned documents. / 四份固定输入的重新生成一致性检查通过。
- A separate HTML text audit matched all article text after whitespace normalization, and checked preservation of every article link and section ID. / 单独提取 HTML 正文核对，忽略空白后内容一致，全部正文链接及章节 ID 保留。
- Local Markdown file targets exist; `git diff --check` passed. / 本地 Markdown 文件链接目标存在，差异空白检查通过。
- Host execution remains pending. Runtime preflight found the Codex Workflow MCP startup and dispatcher default to Bridge port 19091; the existing local Bridge uses credentials for a different instance. Do not replace that connection to claim an isolated run. / 宿主实跑仍待执行；预检发现 Codex Workflow MCP 启动与派发默认使用 19091，现有 Bridge 凭据属于另一实例，不能覆盖该连接来冒充隔离验证。


2026-10-14 update / 更新：The earlier connection blocker above is historical. The user authorized switching the local Agent credentials to 8091. Current runs use that verified instance; no 8090 business service or data was changed. / 上方 10/13 接入阻塞为历史记录；用户已授权切换连接，当前实跑使用 8091。

Maintenance hold / 维护暂停：On 2026-10-14 the owner confirmed that 8091 is intentionally stopped and must not be started. Sixteen initial outputs are saved; DSH recovery E failed to connect and was interrupted, not passed. Recovery F corrected only the local provenance ledgers. / 用户确认 8091 正在维护，禁止启动。16 份初次输出已保存；DSH 补验 E 因连接拒绝被中止，不算通过；F 仅修正本地取证记录。

Post-maintenance update / 维护后更新：DSH G could not resume the migrated workspace. H used a fresh session and private 8091 MCP connection, completed explicit search and four reads, and preserved all unresolved states. Browser checks of all four source pages passed; a reviewed navigation index supplements the original incorrect links. See [recovery evidence](runs/2026-10-14/README.md#post-maintenance-recovery--维护后恢复). / G 续跑失败与 H 新会话成功分别记录；四份浏览器原文导航通过，人工补充索引不冒充初次输出正确。
