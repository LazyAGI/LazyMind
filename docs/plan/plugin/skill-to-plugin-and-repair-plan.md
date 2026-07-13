# Skill → Plugin 生成与 Plugin Repair 完整实施计划

## 1. 背景与目标

LazyMind 已具备 AI 辅助创建 Plugin、从 Skill 导入生成 Plugin，以及对 Plugin 状态机、UI、场景进行 AI Repair 的能力。随后 Skill 被重构为支持版本管理和文件夹的 Skill v2；Plugin 也已经增加了基于 revision、blob、文件树和 tree hash 的版本管理。

本文档定义下一阶段的完整改造方案，目标是：

1. 从 Skill 当前生效的固定 revision 读取完整 Skill 包，而不只是 `SKILL.md`。
2. 对文件夹形式、文件很多甚至超过模型上下文的 Skill 进行不遗漏、可追溯的分层分析。
3. 只有 Skill 中确实存在可执行流程时才生成状态机；纯规范、约束、偏好或无序工具集合应拒绝或请求用户确认。
4. 正确处理 Skill 中的脚本、工具函数和命令式脚本。
5. 当框架已有同定位工具时显式替换并告知用户，同时严格区分基础设施能力和供应商绑定的云服务。
6. 让 Plugin Repair 与新的 Plugin revision 体系、Skill 来源 revision、覆盖证据和脚本安全规则保持一致。

本文档不重新设计 Plugin 自身的版本管理；现有 Plugin revision 能力作为既有基础直接复用。

---

## 2. 当前代码基线

### 2.1 已经完成的能力

当前代码已经具备：

- Plugin resource、blob、revision、revision entry、head revision 和 tree hash。
- Plugin draft 的 `base_revision_id`，用于表示 draft 基于哪个已发布 Plugin revision。
- 发布时将 `plugin.yaml`、`scenario/state.yml`、`scenario/scenario.md` 和 `scripts/*` 组成不可变文件树。
- Plugin 版本列表、历史版本查看、用历史版本覆盖 draft、rollback。
- Plugin session 固定 `plugin_revision_id`、`plugin_revision_no`、`plugin_tree_hash` 和 `plugin_remote_root`。
- 算法运行时按 `(plugin_ref, revision_id, tree_hash)` 物化并缓存不可变 Plugin revision，同时校验 tree hash。
- 普通 Skill 来源已经通过 Skill v2 service 的 `head` 读取 `SKILL.md`，不再直接读取旧的 `skill_resources.content`。

这些能力无需重复建设。

### 2.2 仍然存在的缺口

当前 Skill 导入链路仍然只有一个 `skill_content` 字符串：

- 只读取 `SKILL.md`，不读取同一 revision 下的 `references/`、`scripts/`、配置和资源文件。
- 没有保存来源 Skill 的 `head_revision_id`、revision number 和 tree hash。
- 算法请求仍是 `skill_content: string`，无法表达文件树、来源位置和覆盖状态。
- Design Brief、Skeleton 和 State Machine 阶段被要求一定产出步骤，缺少“不可转换”的语义判定。
- Skill 原脚本不会进入生成输入；Phase 3 可能重新生成脚本，而不是审计和复用已有实现。
- 框架工具注册表缺少供导入器使用的能力分类、等价范围和供应商身份。
- Repair 只根据当前 Plugin YAML/state 和用户提示修复，没有来源 Skill revision、证据覆盖和脚本映射上下文。
- 普通用户发布路径当前禁止包含自定义脚本，因此“生成成功但无法发布”的风险需要一并解决。

---

## 3. 核心概念与不变量

### 3.1 两类 revision 不得混淆

- `base_revision_id`：当前 Plugin draft 基于哪个已发布 Plugin revision。
- `source_skill_revision_id`：该 Plugin 生成或重新分析时使用的来源 Skill revision。

二者语义不同，必须分别存储。Plugin rollback 不改变来源 Skill revision；Skill 更新也不能自动改变 Plugin draft 的 base revision。

### 3.2 当前生效 Skill 版本

“当前生效版本”固定定义为请求开始时的 Skill `head_revision_id`：

- 未提交 Skill draft 不参与 Plugin 生成。
- 生成任务入队前固定 revision ID、revision number 和 tree hash。
- 任务排队或执行期间即使 Skill head 更新，当前任务仍使用已固定的 revision。
- 用户若希望使用新的 Skill head，必须显式发起“基于最新 Skill 重新分析/更新 Plugin”。

### 3.3 不可静默遗漏

Skill revision 中的每个文件和每个已切分文本块，最终必须处于以下状态之一：

- `workflow_evidence`：作为工作流步骤、分支或输入输出证据。
- `constraint_applied`：作为提示词、校验或运行约束。
- `tool_replaced`：对应能力被框架工具替换。
- `script_reused`：脚本或函数被 Plugin 复用。
- `reference_only`：只作为执行时参考资料。
- `supporting_or_test_file`：测试、安装、示例或开发辅助文件。
- `binary_metadata_only`：只读取了二进制元数据。
- `excluded_by_user_candidate`：因用户选择其他候选流程而排除。
- `unsupported`：格式或能力不支持，已明确说明。
- `unresolved`：尚未完成判断。

存在可能影响生成正确性的 `unresolved` 项时，生成不得进入 `done`。

### 3.4 不可修改已发布 revision

AI 生成和 AI Repair 都只能修改 Plugin draft：

- 已发布 Plugin revision 永远不可原地修改。
- Repair 成功后仍是 draft 变更；用户发布时创建新的 Plugin revision。
- 运行中的 Plugin session 继续使用它启动时固定的 revision，不受 repair 或新发布影响。

---

## 4. 数据模型

### 4.1 Plugin draft 来源字段

在 `plugin_drafts` 增加：

- `source_skill_revision_id VARCHAR(36) NOT NULL DEFAULT ''`
- `source_skill_revision_no BIGINT NOT NULL DEFAULT 0`
- `source_skill_tree_hash VARCHAR(64) NOT NULL DEFAULT ''`
- `source_analysis_id VARCHAR(36) NOT NULL DEFAULT ''`

保留现有 `source_skill_id` 和 `source_skill_name`。

### 4.2 独立分析记录

新增 `plugin_generation_analyses`，避免把大量中间结果塞入 Plugin revision 文件树：

- `id`
- `draft_id`
- `user_id`
- `source_type`: `skill | description | repair`
- `source_skill_id`
- `source_skill_revision_id`
- `source_skill_revision_no`
- `source_skill_tree_hash`
- `status`: `analyzing | generatable | needs_confirmation | rejected | failed`
- `verdict_code`
- `verdict_message`
- `candidates_json`
- `selected_candidate_id`
- `coverage_report_json`
- `tool_mapping_report_json`
- `script_report_json`
- `created_at`、`updated_at`

### 4.3 分块与缓存

新增可复用的分析缓存，键至少包含：

- `source_skill_revision_id`
- `path`
- `blob_hash`
- `processor_version`

缓存内容包括：

- 文件结构摘要。
- 文本 chunk 及其 path、行号、标题链、chunk hash。
- Python AST 摘要和安全扫描结果。
- JSON/YAML 键路径摘要。
- 文件引用关系。

缓存不替代原文。影响步骤、条件、工具和脚本转换的结论必须能回溯到原始 chunk。

### 4.4 Repair 记录

新增 `plugin_repair_runs`，记录：

- `id`、`draft_id`、`user_id`
- `base_plugin_revision_id`
- `draft_version_before`
- `target`: `statemachine | ui | scenario | scripts | full`
- `mode`: `plugin_local | source_aware`
- `source_analysis_id`
- `source_skill_revision_id`
- `repair_hint`
- `diagnostics_before_json`
- `changes_json`
- `diagnostics_after_json`
- `status`: `queued | repairing | succeeded | rejected | failed | stale`
- `created_at`、`updated_at`

---

## 5. 固定 Skill revision 与完整 SkillPackage

### 5.1 来源快照读取

将现有 `loadPluginSourceSkill` 替换为结构化读取服务：

```text
LoadPluginSourceSkillSnapshot(skillID, userID) -> SkillSnapshot
```

`SkillSnapshot` 至少包含：

- Skill 元数据。
- `revision_id`、`revision_no`、`tree_hash`。
- revision 的完整 entry manifest。
- 文件内容读取引用或 blob hash。

读取步骤必须在同一事务/一致性边界中完成：先读取 Skill head，再确认 revision 属于该 Skill，然后读取该 revision entries。不要先读 head、稍后再按新的 head 读取文件。

### 5.2 SkillPackage 结构

Go → Algorithm 接口由 `skill_content` 扩展为结构化 `skill_package`：

```json
{
  "skill_id": "...",
  "name": "...",
  "revision_id": "...",
  "revision_no": 12,
  "tree_hash": "...",
  "manifest": [
    {
      "path": "scripts/search.py",
      "entry_type": "file",
      "blob_hash": "...",
      "size": 1234,
      "mime": "text/x-python",
      "file_type": "text",
      "binary": false
    }
  ],
  "documents": [],
  "scripts": [],
  "configs": [],
  "binary_assets": []
}
```

大文件内容不应全部直接嵌入单次请求；可传 analysis ID 和本阶段需要的 evidence chunks。

### 5.3 内置 Skill

内置 Skill 也应转换为相同的 `SkillSnapshot/SkillPackage`：

- 若已安装为 Skill v2，直接读取其 head revision。
- 未安装模板可由本地目录构建临时只读 snapshot，包括完整目录而不只是单文件。
- 临时 snapshot 必须计算 manifest hash/tree hash，保证分析缓存和异步任务可重复。

---

## 6. 超大 Skill 与超上下文处理

### 6.1 确定性预处理

在调用 LLM 前由程序完成：

- Markdown：按标题和段落边界切块，保留 path、起止行、标题链和 hash。
- Python：使用 AST 提取 imports、函数、类、签名、docstring、`main()`、CLI 参数、顶层副作用、endpoint、SDK 和环境变量。
- YAML/JSON：提取结构、键路径、命令和路径引用。
- 普通文本：按段落和语义安全边界切块。
- 二进制：只记录 manifest；支持的文档格式可通过独立解析器提取文本。
- 建立引用图：Markdown 链接、脚本 import、配置引用、路径引用和命令示例。

不得按任意 token 位置截断代码或 Markdown 章节。

### 6.2 分层分析流程

1. 使用 `SKILL.md + manifest` 识别 Skill 类型、主要目标和初始候选流程。
2. 对每个文本/代码文件生成带来源引用的局部摘要。
3. 汇总局部摘要，形成候选流程、工具需求、脚本分类和待回读证据清单。
4. 对每个候选流程，通过引用图和语义索引回读相关原始 chunk。
5. 在生成状态机前，再次回读所有会影响步骤、条件、输入输出、工具和安全约束的原文。
6. 执行跨文件冲突检测和全量覆盖检查。

摘要用于检索和规划，不能成为关键行为的唯一证据。

### 6.3 Token 预算

分别为以下阶段设置预算：

- manifest/入口发现。
- 文件局部分析。
- 候选流程验证。
- 工具与脚本分析。
- 最终生成与校验。

读取优先级为：

1. `SKILL.md` 及其直接引用。
2. 候选流程相关文档。
3. 运行时脚本和配置。
4. references。
5. 示例、测试、安装和辅助文件。
6. 二进制资源。

优先级只决定读取顺序，不能让低优先级文件从覆盖账本中消失。

### 6.4 无法完成时的行为

如果达到总轮次、累计 token 或文件数上限后仍无法获得足够证据：

- 有明确候选范围时返回 `needs_confirmation`，展示已分析范围和未解决项。
- 无法形成可靠候选时返回 `rejected`，错误码为 `skill_too_large_or_ambiguous`。
- 禁止“读取前 N 个文件后继续生成完整 Plugin”。

---

## 7. 状态机适用性判定

### 7.1 新增 Analysis Phase

在现有 Design Brief 之前增加 Phase -1：Workflow Suitability Analysis。

输出必须是结构化结果：

- Skill 类型。
- 是否适合状态机。
- 候选流程列表。
- 每个候选的目标、输入、输出、步骤、顺序、分支、重试、等待和完成条件。
- 来源 evidence。
- 将纳入和排除的文件。
- 工具需求和脚本分类。
- verdict 和原因。

### 7.2 Verdict

- `generatable`：存在明确且完整的可执行流程。
- `needs_confirmation`：主体不是单一流程，但存在一个或多个可独立执行的候选子流程。
- `rejected`：纯规范、约束、偏好、知识参考、无序工具集合，或证据不足以构造可靠流程。

### 7.3 判断标准

可生成流程至少需要：

- 明确目标。
- 可识别输入和产出。
- 两个或以上有执行意义的状态；或者一个确有条件分支、重试、等待/人工确认行为的单步骤状态。
- 可判断的完成条件。

以下情况不得通过补 schema 强行生成：

- “阅读并遵守这些规范”。
- 一组互不相关、没有编排关系的工具。
- 只有风格、偏好或安全约束。
- 只有知识说明或参考资料。

### 7.4 用户确认

采用“候选流程确认”策略：

- `needs_confirmation` 时暂停任务。
- 返回候选流程、纳入文件、排除内容、工具替换和未解决项。
- 用户通过 `analysis_id + candidate_id + source_revision_id + draft_version` 确认。
- 确认过期或 draft version/source revision 不一致时返回冲突，要求重新分析。
- Phase 0–3 只能消费用户确认的 candidate，不得重新解释未选内容并补造步骤。

---

## 8. 框架工具同定位判定

### 8.1 工具能力目录

扩展现有 `ToolGroupConfig` 或提供专门的生成期 capability catalog：

- `capability_id`
- `equivalence_scope`: `infrastructure | provider_bound`
- `provider_id`
- `product_id`
- 输入输出 schema/语义。
- 能力限制。
- 所需配置。
- 当前用户是否可用。

初始覆盖至少包括：

- `kb`
- `temp_kb`
- `web_search`
- `academic_search`
- `url_fetch`
- `image_generator`
- `image_editor`
- `multimodal`
- `calculator`
- `external_db`
- `local_fs`

### 8.2 等价规则

基础设施能力按抽象能力判等：

- Skill 提供 `XX KbSearch`，框架提供另一种实现的 `kb`，可视为同定位。
- 通用图片生成需求在输入输出兼容时可映射为 `image_generator`。
- 通用附件检索可映射到对应框架基础设施。

供应商绑定的云服务必须按产品身份判等：

- Skill 明确要求 `XX Search`，框架只有 A Search、B Search，不得替换。
- 框架 `web_search` 即使会在 A/B 中自动选择，也不能替代明确指定的 XX。
- 只有框架支持相同 `provider_id/product_id`，或 Skill 只要求“任意网页搜索”时，才允许映射。

供应商绑定的判断来源包括：

- 文档明确名称。
- Python SDK/import。
- API endpoint。
- 凭据和环境变量名称。
- 产品独有输入输出或行为。

无法可靠判断时进入用户确认，禁止仅靠名称相似度替换。

### 8.3 替换行为

匹配到框架工具时：

- 对应 Skill 脚本标记为 `tool_replaced`，不复制到 Plugin。
- `state.yml.steps[].tools` 使用框架稳定工具名。
- 候选确认页和覆盖报告显示：原工具、替代工具、判定原因、跳过文件和配置要求。
- 框架工具当前不可用时标记为运行阻塞依赖，不能假装生成结果可直接运行。

输入输出不完全一致时，仅允许使用确定性的参数/结果 adapter；需要改变业务语义时必须让用户确认。

---

## 9. Skill 脚本处理

### 9.1 分类

对 `scripts/**/*.py` 分类为：

- `framework_replaced`：已被同定位框架工具替换。
- `importable_tool`：可安全 import 的工具函数。
- `wrappable_command`：具有清晰 `main()`、参数和结果，可安全包装成函数。
- `supporting_script`：安装、测试、迁移或开发辅助脚本。
- `unsupported`：依赖 CLI 交互、任意子进程、危险文件操作、动态执行、隐式环境状态或无法确定输入输出。

### 9.2 工具函数

对于 `importable_tool`：

- 保留原函数实现，避免无依据重写。
- 复制到生成后的 Plugin `scripts/`。
- 在 `plugin.yaml.tool_scripts` 中声明 path 和 functions。
- 在对应 `state.yml.steps[].tools` 中引用函数名。
- 校验函数存在、callable、名称无冲突且引用闭环。

### 9.3 命令式脚本

不新增通用的 `python scripts/xxx.py` 子进程执行器。

只有满足以下条件时才能转换：

- 存在清晰的 `main()` 或参数入口。
- 输入可以转成显式函数参数。
- 输出可以转成显式返回值或 artifact。
- 核心逻辑不依赖交互式终端、任意 cwd 或不可控环境。
- 可以在不改变业务语义的情况下增加薄包装函数。

否则进入 `needs_confirmation` 或拒绝该候选流程。

### 9.4 安全检查

复用并扩展现有 `SecurityVisitor`：

- Python 语法检查。
- AST 安全检查。
- 顶层副作用。
- 临时 import dry-run。
- 相对路径越界。
- 跨脚本 import 和依赖可用性。
- 函数签名与 callable。
- 函数重名。
- endpoint、凭据和供应商绑定识别。
- step tools、`tool_scripts` 和实际函数闭环。

### 9.5 发布策略

调整当前“存在任何自定义脚本即禁止普通发布”的粗粒度规则：

- `framework_replaced` 不产生脚本，不阻塞发布。
- 审计通过的 `importable_tool` 和 `wrappable_command` 可进入普通发布。
- `unsupported`、依赖不明确或需要高权限的脚本必须进入管理员审核。
- 发布时重新验证脚本 hash 与审计记录一致；内容变化则审计结果失效。

---

## 10. Skill → Plugin 生成流水线

### 10.1 状态

扩展生成状态：

- `analyzing`
- `needs_confirmation`
- `generating_brief`
- `brief_done`
- `skeleton_done`
- `state_done`
- `validating`
- `done`
- `rejected`
- `failed`

### 10.2 流程

1. 固定来源 Skill head revision。
2. 构建 manifest、chunk、AST 摘要和引用图。
3. 执行 suitability analysis。
4. 必要时等待用户选择 candidate。
5. 执行工具同定位映射和脚本转换计划。
6. 根据 candidate 和 evidence 生成 Design Brief。
7. 生成 `plugin.yaml` skeleton。
8. 生成 `state.yml`。
9. 生成 `scenario.md`；仅为确实缺少实现的新工具生成脚本。
10. 执行结构、语义、工具、脚本和覆盖校验。
11. 将结果写入 Plugin draft，不自动发布。

### 10.3 各阶段职责

Design Brief 必须包含：

- candidate ID。
- slots 和 steps。
- 数据流和状态流。
- 工具映射。
- evidence references。
- 明确排除项。

Skeleton 阶段不得新增 candidate 中不存在的步骤或工具。

State Machine 阶段必须验证：

- 所有 step ID 与 skeleton 一致。
- `__start__` 和终止路径完整。
- 每个工具引用可解析。
- 输入输出 slot 闭环。
- 分支、重试、等待与 evidence 一致。

Scenario/Scripts 阶段：

- scenario 覆盖所有步骤。
- 优先复用已审计 Skill 脚本。
- 不得重新生成被框架工具替换的脚本。
- 只有 candidate 明确需要且 Skill 无实现时才生成新脚本，并标记为 `generated`。

---

## 11. Plugin Repair

### 11.1 Repair 模式

支持两种模式：

#### `plugin_local`

只根据当前 Plugin draft、校验诊断和用户 repair hint 修复。

适用于：

- 手工创建的 Plugin。
- 用户已经大幅修改、无需再服从原 Skill 的 Plugin。
- UI 布局、YAML 语法、slot 引用等局部问题。

#### `source_aware`

除当前 Plugin draft 外，还读取固定的 `source_analysis_id/source_skill_revision_id` 和相关 evidence。

适用于：

- 从 Skill 生成的 Plugin。
- 修复遗漏步骤、错误工具映射、脚本转换或与来源规范不一致的问题。

`source_aware` 默认使用生成时固定的 Skill revision，不自动读取 Skill 最新 head。若用户要求基于最新 Skill，应走“重新分析/更新 Plugin”，不是普通 repair。

### 11.2 Repair 前置诊断

Repair 不应直接把 YAML 和提示词交给 LLM。先执行确定性诊断：

- PluginSpec 结构校验。
- step/transition 完整性。
- slot 定义、输入输出和 UI 引用。
- 不可达状态、无终止路径和错误分支。
- tool 名称解析和框架工具可用性。
- `tool_scripts`、脚本文件和函数闭环。
- 脚本 AST 安全与依赖检查。
- scenario 对步骤的覆盖。
- source-aware 模式下的 evidence/coverage 差异。

诊断输出稳定 code，不只返回自然语言 warning。

### 11.3 Repair 的并发与版本约束

发起 repair 时记录：

- `draft_version_before`
- 当前 `base_revision_id`
- 当前各文件 hash
- `source_analysis_id/source_skill_revision_id`

Repair 完成写回时使用 optimistic lock：

- draft version 已变化则标记 `stale`，不得覆盖用户的新编辑。
- 可以保留 AI 产出的 patch 供用户查看，但必须重新基于最新 draft 执行或由用户手工合并。
- Repair 只更新目标文件；不能用旧快照覆盖非目标字段。

当前 generate job 绕过 draft version 检查的方式不应继续用于 repair。

### 11.4 Repair 目标

#### State machine repair

- 修复 transitions、route、skipif、steps prompt、inputs/outputs/tools。
- 不得无证据新增业务步骤。
- source-aware 模式下，新增/删除步骤必须给出 evidence 或明确用户 repair hint。

#### UI repair

- 修复 tab、slot widget、layout 和 slot 引用。
- 不修改工作流业务含义。
- `state_layout_content` 继续保持独立 last-write-wins；AI 不应覆盖用户节点位置，除非用户明确要求重新布局。

#### Scenario repair

- 确保每个步骤有说明并与当前 state 一致。
- 不通过 scenario 文本暗中引入 state 中不存在的步骤或工具。

#### Script/tool repair

- 优先把重复能力替换为框架工具。
- 供应商绑定服务不得替换成不同供应商。
- 原脚本修复后必须重新执行完整安全审计。
- 修改函数名时同步更新 `tool_scripts` 和所有 state tool 引用。

#### Full repair

- 先诊断并生成 repair plan。
- 按 `plugin.yaml → state.yml → scenario.md/scripts → cross validation` 顺序执行。
- 所有跨文件 ID 变更必须作为一个原子 draft 更新提交。

### 11.5 Repair 输出

Repair 结果应返回：

- 修改前诊断。
- 修改文件列表。
- 结构化变更摘要。
- 修改后诊断。
- 剩余 warning/blocker。
- source-aware 模式下使用的 evidence。
- 工具替换和脚本安全结果。
- 新 draft version。

Repair 无法安全完成时应 `rejected`，而不是返回“看似有效”的降级 YAML。

---

## 12. API 调整

### 12.1 Skill 生成

`POST /plugin-drafts/{draft_id}:ai-generate`

- description 路径保持兼容。
- skill 路径只接收 `skill_id`，由服务端固定 head snapshot。
- 返回 analysis/job 状态。

新增：

- `GET /plugin-drafts/{draft_id}/generation-analysis`
- `POST /plugin-drafts/{draft_id}:confirm-workflow`
- `POST /plugin-drafts/{draft_id}:reanalyze-source`

`confirm-workflow` 请求必须包含：

- `analysis_id`
- `candidate_id`
- `source_skill_revision_id`
- `draft_version`

### 12.2 Repair

扩展 `POST /plugin-drafts/{draft_id}:ai-repair`：

```json
{
  "target": "statemachine",
  "mode": "source_aware",
  "repair_hint": "...",
  "draft_version": 7,
  "source_analysis_id": "..."
}
```

新增：

- `GET /plugin-drafts/{draft_id}/repair-runs/{repair_id}`
- 可选的 `POST /plugin-drafts/{draft_id}:repair-preview`，只生成诊断和 repair plan，不写 draft。

### 12.3 错误码

至少提供：

- `skill_not_found`
- `skill_head_missing`
- `skill_revision_inconsistent`
- `skill_package_invalid`
- `skill_too_large_or_ambiguous`
- `workflow_not_applicable`
- `workflow_confirmation_required`
- `workflow_confirmation_stale`
- `tool_provider_mismatch`
- `framework_tool_unavailable`
- `script_unsafe`
- `script_unwrappable`
- `generation_coverage_incomplete`
- `repair_stale_draft`
- `repair_not_applicable`
- `repair_validation_failed`

---

## 13. 前端体验

### 13.1 生成分析页

展示：

- 来源 Skill revision。
- 候选流程卡片。
- 每个候选的输入、输出、步骤和 evidence。
- 将纳入/排除的文件。
- 框架工具替换。
- 原脚本处理方式。
- 未解决和不支持项。

### 13.2 状态提示

区分：

- 正在分析 Skill。
- 等待用户选择流程。
- 正在生成 Plugin。
- 已拒绝及原因。
- 已完成但存在非阻塞 warning。

### 13.3 Repair

- 用户选择 repair target 和模式。
- source-aware 仅在 draft 有有效来源 analysis 时可选。
- Repair 前可展示诊断和预期修改范围。
- Repair 后展示文件级 diff、工具替换、脚本安全结果和剩余问题。
- stale repair 不自动覆盖，提示重新运行或手工比较。

---

## 14. 验证与测试

### 14.1 Skill revision

- Skill head 与 draft 不同，只读取 head。
- 入队后 head 更新，任务仍使用固定 revision。
- Skill rollback 后新任务读取 rollback 后的新 head。
- revision 不属于 Skill、tree hash 不一致、blob 缺失时明确失败。
- `source_skill_revision_id` 与 Plugin `base_revision_id` 分别正确维护。

### 14.2 文件夹与大上下文

- `SKILL.md + references + scripts + configs + assets` 全部进入 manifest。
- 关键流程只存在于最后一个 reference 文件时仍能发现。
- 数百文件和单个超大 Markdown 不出现“前 N 文件偏差”。
- 跨文件步骤、脚本 import 和配置引用正确关联。
- 预算耗尽时暂停/拒绝，不生成伪完整状态机。
- manifest 每个文件都能在 coverage report 中找到状态。

### 14.3 Suitability

- 纯规范、偏好和安全约束被拒绝。
- 无序工具集合被拒绝。
- 混合型 Skill 返回候选流程并等待确认。
- 单步骤但真实包含分支/等待的流程可通过。
- 模糊文本不能被 schema patch 强行补成状态机。

### 14.4 工具映射

- `XX KbSearch` 可映射为框架 `kb`，并显式报告。
- 通用图片生成可映射到 `image_generator`。
- 明确 `XX Search` 且框架仅有 A/B 时不得映射。
- SDK、endpoint 或凭据暴露供应商绑定时不得误判为通用搜索。
- 框架支持同一供应商产品时可以映射，并检查配置可用性。
- 输入输出不兼容时不得自动替换。

### 14.5 脚本

- 同定位框架工具对应脚本被跳过且报告可见。
- 安全工具函数原样复用。
- 清晰 `main()` 脚本可包装成函数。
- CLI 交互、`subprocess`、动态执行、危险文件操作被拒绝。
- 缺失依赖、函数重名和工具引用失配阻止完成。
- 脚本审计通过后普通发布可用；脚本变化后旧审计失效。

### 14.6 Repair

- plugin-local repair 不读取来源 Skill。
- source-aware repair 使用固定来源 revision，不跟随新 head。
- 用户编辑发生在 repair 执行期间时，repair 标记 stale 且不覆盖。
- state repair 不无依据新增步骤。
- UI repair 不覆盖用户布局位置。
- script repair 后同步更新所有声明和引用并重新审计。
- full repair 跨文件修改原子提交。
- Repair 不修改已发布 revision，也不影响运行中的 session。

### 14.7 回归

- description 驱动的普通 AI Plugin 生成保持可用。
- 现有 Plugin 发布、版本列表、rollback 和 runtime revision pinning 保持可用。
- 生成/repair 后的 Plugin 可由现有 PluginSpec、编辑器和 runtime 加载。

---

## 15. 分阶段落地

### Phase A：来源快照与完整包

- 扩展 Skill snapshot reader。
- 保存来源 revision 身份。
- 构建 manifest、文件读取和基础 coverage ledger。
- 保持现有生成器兼容，先解决“读对版本和完整文件树”。

### Phase B：Suitability 与候选确认

- 增加 Phase -1。
- 增加 `needs_confirmation/rejected`。
- 增加候选确认 API 和前端。
- 约束 Phase 0–3 只能消费确认 candidate。

### Phase C：大上下文分析

- 文本 chunk、AST、结构摘要和引用图。
- 分层分析、evidence 回读和缓存。
- Token 预算和 coverage gate。

### Phase D：工具和脚本

- 工具 capability catalog。
- 基础设施/供应商绑定判等。
- 脚本分类、复用、包装和安全审计。
- 调整带安全脚本的发布策略。

### Phase E：Repair 重构

- 确定性诊断。
- plugin-local/source-aware 模式。
- optimistic lock 和 stale repair。
- 文件级 patch、跨文件校验和 repair 记录。

### Phase F：观测与灰度

- 记录 verdict、拒绝原因、文件数、token 使用、工具替换和脚本分类指标。
- 先对白名单用户开放 Skill 文件夹导入和 source-aware repair。
- 观察误拒绝、误生成、供应商误替换和 repair stale 比例后扩大范围。

---

## 16. 完成标准

本项目完成必须同时满足：

1. Skill 导入使用固定 head revision，并能追溯 revision ID 和 tree hash。
2. 文件夹 Skill 的所有文件都进入 manifest 和覆盖账本。
3. 超上下文时采用分层分析，不静默截断。
4. 不适合状态机的 Skill 能可靠拒绝；混合型 Skill 由用户确认候选流程。
5. 基础设施工具可同定位替换，供应商绑定云服务不会被错误替换。
6. Skill 脚本能被分类、审计、复用或安全拒绝。
7. 生成结果写入 Plugin draft，发布后进入现有不可变 revision 体系。
8. Repair 支持 plugin-local 和 source-aware，并使用 optimistic lock 防止覆盖并发编辑。
9. Repair 不修改历史 revision，不影响运行中的固定 revision session。
10. 每次生成和 repair 都能解释采用了哪些来源、替换了哪些工具、跳过了哪些文件，以及仍有哪些问题。
