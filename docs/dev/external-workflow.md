# 外部工作流开发接入

本文面向 Codex、其他 Agent 宿主和 LazyMind 开发者。目标是：用户安装并连接 LazyMind 与外部 Agent 后，宿主已有的生成与验证能力可以直接执行工作流，无需在 LazyMind 再配置文本或视觉模型。用户仍可在原生 LazyMind 中运行同一工作流。

## 职责与边界

| 参与方 | 职责 |
| --- | --- |
| 工作流包 | 声明步骤、Skill、约束、输入/输出和可选工具 |
| 外部宿主 | 创建原生 subagent，完成生成、检索、语义/视觉验证和修复 |
| 根 Controller | 读取状态、领取授权，代理工具调用，发布产物、完成步骤，响应停止 |
| LazyMind Core | 固定 revision，存储输入/产物/状态，检查归属、租约、类型、幂等与版本 |
| LazyMind Chat 工具服务 | 提供明确开放的确定性工具或用户已配置的媒体能力 |
| Host Connector | 将继续、取消等通知送到原绑定的宿主会话 |

“零模型配置”指外部执行不依赖 LazyMind 的文本/视觉模型。图片、视频等能力优先由宿主完成；宿主没有能力时可以调用已声明的 LazyMind 工具。两边都不可用时，说明需要在 LazyMind 配置的功能并等待用户重试，不能假装完成或改成交付别的格式。

LazyMind 不根据模型评分决定外部产物是否合格，不要求语义验证 receipt。外部 Agent 对质量判断负责。文件哈希、槽类型、所属用户、当前 lease 等确定性检查仍在服务端执行。

## 包格式与兼容性

格式定义见 [workflow-format.md](../workflow-format.md#外部-agent-执行)。`runtime.external_execution.version: '1'` 为每个编译节点声明外部 prompt、skills、constraints、tools 与验收标准。原生 `state.yml`/`driver.md` 不变。

- 新声明只有重新发布后才生效；运行绑定的 revision 不随 HEAD 更新。
- 旧 revision 没有声明时，保留原 executor 路由；不能把旧 run 强制迁移为外部执行。
- 外部步骤即使声明了工具，仍由外部 Agent 执行；工具声明本身不启动 LazyMind 子代理。
- 固定 Skill 正文随 `step_contract.external_execution.skill_files` 返回；包含 `path`、`content` 和 `sha256`。宿主必须将正文传给 worker。
- `launch_request` 保留启动请求，`user_request` 保留本次执行或重试补充；两者和固定输入一并交给 worker，不能只传通用步骤 prompt。
- `external_execution.task_specification` 保留同 revision 的业务说明，供读取精确枚举、JSON 形状和分支条件。执行方式以外部 prompt、constraints 和 Skill 为准；其中原生模型、包装工具、自动发布和页数下限等指令不转移到外部执行。
- `validation_owner` 为 `external-agent`。输出类型、列表顺序、条件分支、审核与暂停规则仍来自同一编译图。

## 根 Controller 的执行循环

1. 发现并读取工作流；从用户请求和附件满足启动必需项。源文件经 `workflow.input.import` 得到不可变资源绑定。
2. `workflow.start` 使用唯一且稳定的 `idempotency_key`。缺少输入时按返回问题继续收集；不创建重复 run。宿主 Connector 提供真实会话身份，不能编造 `driver_session_id`。
3. 读取 `workflow.state`。已有 execution 用 `workflow.step.claim`，仅在 admission 允许时 `workflow.step.begin`。
4. 取得 `execution_handle` 与固定 `step_contract`。根代理保留 handle，不给 worker。
5. 用宿主原生 subagent 执行该步骤；传入完整固定契约（包括 Skill、业务说明、启动/补充请求、输入资源、必需/条件输出）与独立输出目录。需要 LazyMind 工具时由 worker 返回调用请求，根代理代为调用。
6. 用宿主验证 worker 检查实际文件、源事实和预览；修复失败项。worker 返回真实 artifact manifest 和验证结论。
7. 根代理逐项 `workflow.artifact.publish`；每个槽使用稳定正整数 `seq`，重试不换序号。文件必须存在于 Connector 可读的机器上。
8. 收到全部发布回执后，调用 `workflow.step.complete`，传原 handle、outcome 和实际 worker 的 `executor_ref`。最终文字不能代替产物保存。
9. 按最新状态继续。待人工审核时交还用户；完成时交付 artifact 链接。停止/回退/重领后丢弃旧 worker 结果，不能使用旧 handle 提交。

MCP 不负责替宿主启动原生 subagent。Codex 集成的 workflow Skill 负责要求这种调度；`executor_ref` 用于关联日志，不是服务端可验证的宿主运行证明。其他宿主需自行实现生成和验证 worker、取消及恢复适配。

输入契约可能只含修订引用。根 Controller 用 `workflow.input.get` 读取输入资源、用 `workflow.artifact.get` 读取绑定的 `source_revision_id`，校验内容和版本后把实际文件或文本交给 worker；不能仅凭文件名或旧会话缓存重建内容。下载与发布都复用正常凭证机制，凭证不进入 worker 提示或产物。

## 步骤工具 MCP

### 发现

```json
{
  "session_id": "<run>",
  "execution_id": "<attempt>",
  "execution_handle": "<current handle>"
}
```

调用 `workflow.tool.list`。返回当前步骤允许的 `tools`，包括 `name`、`source`、`input_schema`、`available` 和不可用原因。不得根据别的步骤或本地包猜测权限。

### 执行

```json
{
  "session_id": "<run>",
  "execution_id": "<attempt>",
  "execution_handle": "<current handle>",
  "tool_name": "ppt_export_html",
  "source": "package",
  "arguments": {
    "pages": [{"html": "<self-contained slide HTML>", "notes": "Speaker notes"}],
    "filename": "summary.pptx"
  },
  "command_id": "<stable unique operation id>"
}
```

调用 `workflow.tool.call`。`source` 可以省略；提供时必须与固定契约一致。相同 command 和参数重试返回原回执；换参数必须换 command。单次工具操作最多执行 5 分钟；`TOOL_CALL_IN_PROGRESS` 表示仍在执行或结果暂时未知，可以取同一回执，不另建付费调用。若待完成回执超过 6 分钟，返回不可自动重试的 `TOOL_OUTCOME_UNKNOWN`：停止轮询，检查提供方已有结果，只有用户确认后才能以新 command 或新 attempt 重试；原 command 绝不再次执行，后续若原回执补全仍可读取。明确失败后按公共错误提示修正参数、授权或配置，再使用新 command 或重试 attempt；`INVALID_ARGUMENT` 和 `PERMISSION_DENIED` 会保留具体指导。

工具结果不自动发布产物、不自动完成步骤。包工具返回的文件是 `{name,mime_type,content_base64,sha256}`；根代理将字节保存到自己的输出目录，检查哈希，再通过 artifact publish 上传。当前传输上限为包内容 64 MiB、工具结果 32 MiB；大文件需要独立的资源传输扩展，不能塞入文本回执。

Core 路径（前缀 `/api/core`）：

| 操作 | 路径 |
| --- | --- |
| 工具列表 | `POST /workflow-sessions/{session_id}/hosted-attempts/{attempt_id}/tools:list` |
| 工具调用 | `POST /workflow-sessions/{session_id}/hosted-attempts/{attempt_id}/tools:call` |

HTTP body 对应 MCP 参数，路径中的 execution 使用 `attempt_id`；body 不重复 session/execution ID。沿用登录凭证、`Workflow-Contract-Version: workflow.v1` 与 `{contract_version,ok,result,error}` envelope。不能用调用方传入的 header 冒充 Agent 类型；global capability identity 来自已持久化的 host binding。

### 工具来源

`package` 只运行服务明确注册的适配器，不动态执行来自 Workflow 包的 Python/JS。PPT 适配器通过固定 revision 的完整文件快照和 tree hash 调用内部 `/api/workflow/external-tools/list|call`；Core 校验 blob hash，Chat 再校验快照树与工具绑定。知识库和学术适配器在 Core 内复用已有数据服务，并校验固定 revision 中的审计标记文件。新增工具须增加适配器、严格参数 schema、资源限制和入口行为测试。

PPT 的无模型工具是 `ppt_export_html`、`ppt_render_html`、`ppt_inspect_pptx`。分别导出可编辑 PPTX、渲染 HTML 预览、检查实际 PPTX 页数/文本/备注。渲染和检查提供事实，不代替外部 worker 的视觉与语义判断。一页 PPTX 是合法请求，不额外生成封面。

HTML 导出器会把表格转换为可编辑文本和线条；要求原生 PowerPoint 表格或图表时，宿主须补建对应原生对象，再检查最终 PPTX。HTML 预览正确也不能替代最终文件渲染：字体替换、表格结构和备注都应检查实际文件，渲染环境须具备相应语言的字体。

知识库工具 `knowledge_list_documents`、`knowledge_read_document` 只访问领取步骤时冻结在 `knowledge_scope` 中的选中范围。单文档输入不会扩大为整个数据集；读取仍检查用户权限、文档版本和现有解析结果，不触发 embedding、解析或生成模型。没有选择知识库时返回 `no_selected_knowledge` 与空列表，不能扫描用户全部文档。

学术工具包括 `academic_context`、`academic_expansion`、`academic_search_papers`、`academic_read_paper`、`academic_resolve_papers`。扩展、搜索与解析 ID 必须使用固定输入中的 `expansion_id`；全文读取使用固定 `handoff_id`，或当前会话唯一可确定的 handoff。多个候选时要求明确选择，不猜测历史任务。文本输入资源中的 ID 也必须通过 owner、revision、内容 hash 检查；工具参数不能传入用户、租户或其他会话身份。这里的“解析”是既有论文记录的定位，不调用论文生成模型。

PPT HTML 导出只接受自包含内容，禁止网络资源、脚本和任意本地路径；依赖须由 LazyMind 运行环境预先安装。外部路径不会在渲染期间下载依赖、字体或浏览器。原生 PPT 的配置和运行方式继续保留。

`global` 复用 [capability gateway](../architecture/external-agent-capability-gateway.md)，支持已授权的 MCP 工具与已配置的内置工具。步骤声明与用户授权同时生效。媒体输入使用 `resource:<workflow.input.import 返回的 ID>`；Core 校验 owner/hash 后内部传字节，外部不能传任意服务端路径。

## Codex 安装与外部宿主适配

LazyMind 的 Codex 连接入口安装内置 `lazymind-workflow` 插件与 workflow Skill。源码位于 `local/lazymind-cli/internal/codexplugin/assets/lazymind-workflow/skills/workflow/SKILL.md`。Connector 位于 `integrations/codex-workflow`，继续复用现有会话绑定、通知投递、恢复与面板能力。

本地开发中，已有连接更新 CLI 后使用不重启宿主的刷新入口：

```sh
make lazymind-cli-build
local/build/bin/lazymind internal agent codex refresh
```

`refresh` 保留配对和启用偏好，不关闭或启动宿主。首次连接仍通过设置中的连接入口；`internal agent codex connect` 可能为建立宿主控制而重启应用，不能在正在执行的会话中当作普通刷新命令使用。已运行的 MCP 进程需要客户端重新连接才能加载新工具 schema；刷新成功不代表当前会话已经热加载。

独立协议调试入口为 `local/build/bin/lazymind mcp codex-workflow`；需使用已安装插件声明的 provider、host control 和 pairing 配置，只有指定进程模式并不会自动启用会话绑定。仍沿用正常凭证机制。不要将 token、provider key 或测试身份写进文档与提交。

其他宿主同事需要完成：真实会话身份注入；原生 subagent 调度与隔离；根代理工具代理；文件资源在宿主/Connector 之间传输；通知去重、取消与明确恢复；待审核时交还用户。连接控制协议见 [外部 Agent 与 Workflow 交互协议](../architecture/external-agent-workflow-protocol-draft.md)。不能通过新开顶层会话或第二个 Codex 进程替代本会话的 native worker。

## 本地开发与回归入口

遵守所在工作区的 `AGENTS.md`：仅启动本地 runtime。仓库标准构建/启动入口是 `make local-up`，停止使用 `make local-down`；工作区额外提供 `make local-start` 时，可用它复用已有构建。首次检出不要假设存在该快捷目标。不要手动运行 Core/Chat，也不要启动 Desktop。重启后检查前端 8090、Core 18001、Chat 18046，并执行经过认证的实际读取。

关键回归入口：

- Core：`go test ./workflow/graphengine ./workflow/executor ./workflow/hosted ./workflow ./workflowtool ./externalcapability ./modelconfig`。
- CLI：`go test ./internal/workflowmcp ./internal/mcpbridge ./internal/codexplugin`。
- Python：`tests/algorithm` 中 external workflow tools 和 external tools 的入口测试。

验收必须分别覆盖无 LazyMind 模型的外部执行、同 revision 的原生执行、旧 revision 兼容、停止后的迟到结果、工具幂等与权限、缺少媒体能力后的配置提示、实际文件页数与预览检查。声明完成迁移不等于每种外部宿主及每个媒体 provider 已通过端到端测试；对外发布时应区分契约覆盖、回归测试和实际连接验证。

### LazyMind 中查看外部运行与中间产物

外部执行器通过 `workflow.artifact.publish` 发布阶段产物后，LazyMind 持久化产物并发送工作流事件。任务中心会登记未绑定 LazyMind 对话的外部运行；用户可通过“查看过程与产物”打开同一个工作流面板，查看需求、素材、大纲、逐页预览与最终文件。面板订阅运行事件并重新读取服务端快照，不需要配置 LazyMind 生成模型，也不依赖宿主自动唤醒通道。

已存储但尚无任务记录的独立外部运行，在所属用户读取任务列表时补登。补登不会连接无关对话、重复创建已有任务或恢复已归档任务；产物读取继续使用既有的所有者权限检查。外部执行器应在每个产物生成后立即发布，才能展示执行过程，而非只在全部完成后提交。
