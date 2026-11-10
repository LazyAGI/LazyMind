# 外部工作流开发接入

本文面向 Codex、其他 Agent 宿主和 LazyMind 开发者。目标是：用户安装并连接 LazyMind 与外部 Agent 后，宿主已有的生成与验证能力可以直接执行工作流，无需在 LazyMind 再配置文本或视觉模型。用户仍可在原生 LazyMind 中运行同一工作流。

## 职责与边界

| 参与方 | 职责 |
| --- | --- |
| 工作流包 | 声明步骤、Skill、约束、输入/输出和可选工具 |
| 外部宿主 | 完成生成、检索、语义/视觉验证和修复；有 subagent 时可分派，没有时由当前 Agent 顺序执行 |
| 根 Controller | 读取状态、领取授权，代理工具调用，发布产物、完成步骤，响应停止 |
| LazyMind Core | 固定 revision，存储输入/产物/状态，检查归属、租约、类型、幂等与版本 |
| LazyMind Chat 工具服务 | 提供明确开放的确定性工具或用户已配置的媒体能力 |
| Host Connector | 将继续、取消等通知送到原绑定的宿主会话 |

“零模型配置”指外部执行不依赖 LazyMind 的文本/视觉模型。图片、视频等能力优先由宿主完成；宿主没有能力时可以调用已声明的 LazyMind 工具。两边都不可用时，说明需要在 LazyMind 配置的功能并等待用户重试，不能假装完成或改成交付别的格式。

LazyMind 不根据模型评分决定外部产物是否合格，不要求语义验证 receipt。外部 Agent 对质量判断负责。文件哈希、槽类型、所属用户、当前 lease 等确定性检查仍在服务端执行。

## 包格式与兼容性

格式定义见 [workflow-format.md](../workflow-format.md#外部-agent-执行)。新工作流采用 `runtime.execution.version: '1'`：`scenario/state.yml` 是步骤目标、输入输出、分支和验收规则的唯一来源；`runtime.execution` 只补充共用的 Skills、约束和工具能力清单。14 个内置包均使用这一声明。

- 同一 revision 的原生和外部执行器收到相同的 `step_contract.prompt`、`execution.skill_files`、`execution.constraints`、验收和必需输出。Skill 按固定 revision 读取，包含 `path`、`content` 和 `sha256`；原生 runner 也实际加载这些内容。
- 编译器拒绝在 `runtime.execution.steps` 覆盖 prompt、acceptance 或 required_outputs，避免形成第二套业务规则。
- 原生工具包装、本地路径、tools-only 和自动保存方式属于执行适配。外部执行器按照公共 `external_execution.adapter_instructions`，用宿主能力或已声明的 MCP 工具完成相同业务要求。它不需要安装或调用原生模型包装。
- 共享契约在线上传输时仅保留一份 Skill 和业务指令。`external_execution` 返回版本、验证归属、适配说明和 `execution_ref: "#/execution"`，引用同一 `execution`；不再重复 Skill 正文、prompt、验收及工具清单。`objective_ref: "#/prompt"` 表示复用顶层 prompt；若另有 objective 文本，将其作为额外运行要求。旧 revision 的独立 `runtime.external_execution` 及未声明外部能力的 executor 路由继续保持，运行中不能替换绑定 revision。
- `launch_request` 和 `user_request` 分别携带启动与本次执行/重试请求，必须连同输入传给 worker。
- 外部验证由宿主执行；服务端继续检查类型、哈希、owner、当前 lease 和幂等。Native 与外部均遵守同一状态机、列表顺序、审核、暂停和条件输出。

更新工作流时应重新发布，再启动新运行测试共享契约。已有运行和已有 PPT 产物仍使用原来固定的 revision；打开历史结果不会触发重跑。

## 根 Controller 的执行循环

正常外部执行默认静默：进度通过已保存产物和工作流面板展示，不额外发送阶段总结、重复计划或非必要用户对话。只有用户主动询问、必须回答/审核/配置、阻断错误或最终交付时才发简短文字；宿主强制通知保持最短。此规则同时传给 worker，worker 返回产物、manifest 和简短验证证据。产物正文、讲稿和检查记录照常保存；不能因此省略 ask、审核、暂停或授权检查。

Controller 按已返回状态调度、代理工具和保存结果，避免重复 worker 的内容分析、重新推导已确认需求或在正常执行时搜索源码。优先等完成通知；只能轮询的宿主使用有边界的状态查询，避免反复查看输出目录并对未变化状态重新规划。静默规则约束宿主输出行为，不是服务端拦截外部模型消息，也不保证缩短模型内部推理时间。

1. 发现并读取工作流；从用户请求和附件满足启动必需项。源文件经 `workflow.input.import` 得到不可变资源绑定。
2. `workflow.start` 使用唯一且稳定的 `idempotency_key`。缺少输入时按返回问题继续收集；不创建重复 run。宿主 Connector 提供真实会话身份，不能编造 `driver_session_id`。
3. 读取 `workflow.state`。已有 execution 用 `workflow.step.claim`，仅在 admission 允许时 `workflow.step.begin`。
4. 取得 `execution_handle` 与固定 `step_contract`。根代理保留 handle，不给 worker。
5. 有 subagent 时可分派执行；没有时由当前外部 Agent 执行该步骤。执行方读取完整固定契约（包括共享 execution Skill、步骤业务说明、启动/补充请求、输入资源、必需/条件输出）与独立输出目录。需要 LazyMind 工具时由 worker 返回调用请求，根代理代为调用。
6. 对实际文件、源事实和预览进行单独验证，修复失败项。可由独立 worker 验证，也可由同一 Agent 进行第二轮检查；后者须注明同 Agent 验证，不能声称独立复核。返回真实 artifact manifest 和验证结论。
7. 根代理逐项 `workflow.artifact.publish`；每个槽使用稳定正整数 `seq`，重试不换序号。文件必须存在于 Connector 可读的机器上。
8. 收到全部发布回执后，调用 `workflow.step.complete`，传原 handle、outcome 和实际执行者的可选 `executor_ref`（真实任务、会话或 trace 引用）。最终文字不能代替产物保存。
9. 按最新状态继续。待人工审核时交还用户；完成时交付 artifact 链接。停止/回退/重领后丢弃旧 worker 结果，不能使用旧 handle 提交。

MCP 不负责替宿主启动原生 subagent。所有宿主遵守相同的 MCP 执行说明；Codex Skill 仅映射原生子代理和问答工具，不维护工作流业务规则。`executor_ref` 用于关联日志，不是服务端可验证的宿主运行证明。其他宿主需实现生成、验证、取消及恢复适配，子 Agent 不是协议前提。单 Agent 仍需遵守授权、暂停、ask 和版本检查。

输入契约可能只含修订引用。根 Controller 用 `workflow.input.get` 读取输入资源、用 `workflow.artifact.get` 读取绑定的 `source_revision_id`，校验内容和版本后把实际文件或文本交给 worker；不能仅凭文件名或旧会话缓存重建内容。下载与发布都复用正常凭证机制，凭证不进入 worker 提示或产物。

## 文件交接与页面并行

控制器保留运行授权、路由/输出元数据、文件引用和简短检查摘要。完整 Skill、论文内容、生成请求、HTML 和讲稿由实际执行方读取，不为转发或导出而反复打印到控制器上下文。

支持共享文件系统的 Connector 提供向后兼容的可选字段，文件路径均指 Connector 所在机器，不传给 Core 作为服务端路径：

| 入口 | 字段 | 行为 |
| --- | --- | --- |
| `workflow.step.begin/claim/resume` | `contract_directory` | 在已有绝对目录下创建私有子目录，保存完整固定契约；返回 `execution.step_contract_file.result_path` 和精简 `step_contract`。授权仍只返回给控制器，不写入 worker 契约文件。 |
| `workflow.tool.call` | `arguments_file` | 读取一个 UTF-8 JSON 对象作为工具参数，与 `arguments` 互斥；沿用文件读取权限、大小限制和既有工具授权。 |
| `workflow.tool.call` | `result_directory` | 在已有绝对目录下创建私有子目录，保存完整工具 JSON，解码并校验输出文件哈希；只返回 `result_path`、文件路径/哈希/大小及状态。错误仍可见。 |
| `workflow.input.get/artifact.get` | `result_directory` | 保存完整已鉴权资源 JSON，返回 `resource_file` / `artifact_file` 中的文件引用，供执行方读取。 |
| `workflow.artifact.list` | `metadata_only: true` | 仅返回 ID、版本、页身份等元数据，不将已保存正文重新带回控制器。 |
| `workflow.artifact.publish` | `output.value_file` | 读取文件作为 text 或 JSON 槽的值，与 `value`、`local_path` 互斥；二进制继续使用 `local_path`。 |

文件模式不改变 command_id、幂等、当前 execution_handle、固定 revision、服务端类型检查及状态机。重试保持 command 和参数内容不变；修改参数文件后不能复用旧 command。文件结果使用私有随机目录，拒绝路径穿越、同名覆盖和错误哈希；不提供在任意已有文件上写入的接口。传输成功不代表视觉/语义检查通过，也不自动发布产物。

固定共享大纲、准确文案、讲稿与风格后，有 subagent 的宿主按可用容量创建独立页面 worker 并行执行，各自读取完整固定业务契约与本页输入；无 subagent 时顺序执行。不得把上一个 worker 的历史带给下一页。检查通过的一页立即按稳定页身份和 seq 发布，不能把完成顺序当页序，也不能为等待较早页面而阻塞其他页面的保存。最终导出参数由工具从已接受文件按视觉页序组装；控制器只交接参数文件路径。整套导出一次，检查实际文件，修复失败页。宿主不支持共享路径时用其等价资源接口或既有内联协议，完整 Skill 和检查要求仍保留。

## 用户追问、暂停与恢复

启动缺少资料时，`workflow.start` 返回 `needs_input` 与精确问题 ID，此时尚未创建运行；宿主收集用户回答到 `clarification_values` 后重新提交。

运行中的追问来自同一 `runtime.input_checkpoint` 和对应 JSON 产物。人工模式（`workflow_mode=dynamic`）在生产步骤成功且存在未回答问题时返回 `control.input_request`，阻止新步骤开始，`continuation=awaiting_user`。自动模式沿用原生策略，不额外追问；未知事实应标注或省略，不能编造。

`input_request` 包含 `artifact_id`、`revision`、`slot`、`title`、`questions`。外部宿主可以使用自己的问答 UI 或普通会话提问；LazyMind 结果页使用同一问题卡。等待用户真实回复后调用：

```json
{
  "session_id": "<run>",
  "input_artifact_id": "<control.input_request.artifact_id>",
  "input_revision": 1,
  "expected_state_version": 12,
  "answer_text": "<用户对这些问题的真实回答>",
  "command_id": "<稳定的回答操作 ID>"
}
```

MCP 工具名为 `workflow.input.answer`；HTTP 复用 `POST /workflow-sessions/{session_id}/control`，增加 `kind: answer_input`。问题版本、状态版本和 owner 均验证，成功重试返回原回执；不同回答不能复用同一个 command。回答保存为 human artifact revision，生成步骤自行填入 `answer_text` 不能绕过提问。该入口只支持文字回答（可包含选择题答案）；附件仍通过启动材料或已有输入资源接口绑定。

保存回答不会批准 review、启动下一步或解除停止。随后读取状态，按 admission 决定是否继续；已暂停仍需用户明确恢复，待审核仍由用户确认。用户可明确回退问题生产步骤或其上游来修正问题；不能借下游重试绕过未回答问题。暂停使用 `workflow.session.stop`，恢复使用 `workflow.session.resume`；旧 execution_handle 的迟到产物会被拒绝。

## 接入其他外部 Agent

基础接入只需配置 LazyMind 通用 MCP 服务 `lazymind mcp proxy` 并沿用当前用户的连接凭证，无需设置 Codex 身份。不同开发者使用相同的工具 schema、执行契约和上述问答/暂停接口。没有控制通道时，用户回到原外部会话发出继续请求后，宿主读取同一个 run 并恢复；不要创建替代运行。

需要 LazyMind 面板自动唤醒原会话时，实现 [workflow-agent-core](../../integrations/workflow-agent-core/README.md) 的 `RuntimeAdapter`：真实会话定位、消息投递、定向取消及可选持久回执。工具识别和控制状态机放在该共享库，宿主插件仅转换自己的 SDK 事件。缺少可靠回执时保持 delivery unknown，不能重复发送。自动唤醒能力与 MCP 执行能力分别验收；配置了插件并不证明控制通道已可用。

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

PPT 的无模型工具包括 `ppt_prepare_page`、`ppt_compute_timing`、`ppt_order_papers`、`pdf_read_text`、`pdf_extract_images`，以及 `ppt_export_html`、`ppt_render_html`、`ppt_inspect_pptx`。后三项分别导出可编辑 PPTX、渲染 HTML 预览、检查实际 PPTX 页数/文本/备注。渲染和检查提供事实，不代替外部 worker 的视觉与语义判断。一页 PPTX 是合法请求，不额外生成封面。

`ppt_prepare_page` 复用原生页面请求组装与 HTML 规范化，模板文字从当前固定 revision 读取。首次调用返回 `generation_request`；外部 Agent 按原样处理其中的 system/user prompt，再以相同页码、brief 和 `generation_responses: [{request_hash, text}]` 回传。返回 `ready` 后得到实际 HTML 文件；哈希不匹配会拒绝。它只完成确定性检查，视觉与语义检查由外部执行者完成。页面可并行生成，但通过检查的一页须立即发布，不能等待整套导出。计时与论文排序工具也复用原生实现。

PDF 工具优先传 `resource_ref: "resource:<source_id>"`，其中 ID 来自当前步骤固定附件输入。Core 校验 owner、绑定、revision 和内容 hash 后内部传字节，避免外部上下文反复携带 base64；也支持显式 `content_base64`，两者不能混用。`pdf_read_text` 使用页范围与字符预算分页，按 `next_page`/`next_page_offset` 继续，不运行 OCR 或模型；扫描件无文本时应由外部能力处理并披露。`pdf_extract_images` 复用原生 PDF 图像/区域提取，返回 PDF 页码与边界，`include_content: true` 才返回图片字节供保存和视觉验证；只提取不调用本地视觉模型。

`workflow.list` 只返回可路由的工作流元数据，不返回全部执行图和实现源码；选定后用 `workflow.get` 获取启动要求，用步骤执行接口获取完整业务契约。每步只读取一次完整契约，控制器保留资源引用和简短执行记录；产物文件、完整 HTML、图片 base64 和已检查的前一步全文不重复打印进模型上下文。按需要解析本步骤的固定输入；不能用裁剪业务 Skill 来换取速度。

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

登录与配对应使用一致的服务地址。当前配对的账户范围包含服务地址，`localhost` 与 `127.0.0.1` 不会自动合并；切换地址后可能出现 `installation_unavailable`。先核对已登录账户与原连接地址，不能通过修改配对范围或复制 token 绕过校验。通用 MCP 可用与宿主控制配对有效需要分别检查。

独立协议调试入口为 `local/build/bin/lazymind mcp codex-workflow`；需使用已安装插件声明的 provider、host control 和 pairing 配置，只有指定进程模式并不会自动启用会话绑定。仍沿用正常凭证机制。不要将 token、provider key 或测试身份写进文档与提交。

其他宿主同事需要完成：真实会话身份注入；可选的 subagent 调度与隔离、单 Agent 顺序执行；根代理工具代理；文件资源在宿主/Connector 之间传输；通知去重、取消与明确恢复；待审核时交还用户。连接控制协议见 [外部 Agent 与 Workflow 交互协议](../architecture/external-agent-workflow-protocol-draft.md)。不能通过新开顶层会话或第二个 Codex 进程替代本会话的 native worker。

## 本地开发与回归入口

遵守所在工作区的 `AGENTS.md`：仅启动本地 runtime。仓库标准构建/启动入口是 `make local-up`，停止使用 `make local-down`；工作区额外提供 `make local-start` 时，可用它复用已有构建。首次检出不要假设存在该快捷目标。不要手动运行 Core/Chat，也不要启动 Desktop。重启后检查前端 8090、Core 18001、Chat 18046，并执行经过认证的实际读取。

关键回归入口：

- Core：`go test ./workflow/graphengine ./workflow/executor ./workflow/hosted ./workflow ./workflowtool ./externalcapability ./modelconfig`。
- CLI：`go test ./internal/workflowmcp ./internal/mcpbridge ./internal/codexplugin`。
- Python：`tests/algorithm` 中 external workflow tools 和 external tools 的入口测试。

验收必须分别覆盖无 LazyMind 模型的外部执行、同 revision 的原生执行、旧 revision 兼容、停止后的迟到结果、工具幂等与权限、缺少媒体能力后的配置提示、实际文件页数与预览检查。声明完成迁移不等于每种外部宿主及每个媒体 provider 已通过端到端测试；对外发布时应区分契约覆盖、回归测试和实际连接验证。

### LazyMind 聊天列表中的外部产物

外部执行器逐项 `workflow.artifact.publish` 后，外部来源、外部控制的运行会在共享产物保存事务内创建专属 Chat 结果会话（包括已绑定宿主原生会话的运行）。用户从聊天侧栏打开即可查看同一工作流的已保存产物、问题卡和暂停/恢复控件；页面不加载本地聊天输入框，不需要本地模型，也不复制外部代理的中间 trace。

结果会话只是展示记录，通过 `external_workflow_session_id` 关联运行，不修改运行原有 `conversation_id`、origin 或宿主绑定。任务中心的展示入口关联结果会话，运行的鉴权范围仍使用原宿主会话；LazyMind 原生发起的运行继续使用原会话。列表和详情返回 `execution_owner: external_agent` 供通用 UI 选择结果视图；任何宿主均使用这一机制。

历史有产物的外部运行在所属用户读取聊天列表时补显。恢复有数量边界和所有者过滤；重复发布不会重复创建，用户重命名保留，归档、删除及永久删除的会话不会被重新创建。任务中心入口继续保留。产物内容仍由原工作流权限和版本接口读取，不复制另一套文件。

外部步骤通过工作流事件和已保存产物展示进度，不订阅 LazyMind 本地子任务的 `/tasks/:id:stream`。步骤投影提供 `executor_host`；旧快照缺少该字段时以 `controller_host` 判断，显式本地执行的步骤仍保留本地活动流。

## Skill 来源与工具补齐

原生与外部执行均以固定版本中的 `step_contract.prompt`、
`execution.skill_files`、约束和验收标准为唯一业务依据。外部执行者必须读取完整
文件内容；分派时须交给生成和验证子代理；不得用宿主自带的 PPT、写作或设计 Skill
替换 LazyMind 的流程、风格、输出结构或质量标准。

先发现该步骤已声明的 LazyMind 工具；适用且已配置时使用它们。缺少操作能力时，
外部 Agent 可以使用自己的工具补齐，但不能改变 Skill 所规定的方法。
两边均无能力时提示用户配置 LazyMind 并重试。Skill 文件缺失应从固定版本补取，
不能作为切换宿主业务 Skill 的理由。执行结果应附使用的 Skill 路径及工具替代说明；
这些执行证据无需显示为聊天 trace。

PPT 工作流直接交付原生 runtime 的大纲、风格目录、页面生成及审查修复提示词，
而非另写外部版提示词。各提示词的中间 JSON 格式按所属操作应用，最终产物仍遵循
步骤声明。历史会话固定旧版本不会自动迁移；验证新 Skill 必须启动新版本会话。

### 外部 Agent 的产物局部润色

外部运行的 `rewrite_selection` 走独立的持久化产物动作，不创建、回退或重跑工作流步骤。工作流包在 `artifact_actions` 声明 `external_generation: true` 后，其原有规划器通过上下文隔离的 generation boundary 产出模型请求；本地执行仍使用原有模型调用，外部执行只接收同一规划器的提示词并返回模型文本。选择器解析、修改范围校验、候选预览和版本提交都属于 LazyMind。

- 原有 `:action-preview` 在外部会话返回 `waiting_agent`、`job_id`、`version`。连接器通过通用 `artifact_edit` host action 通知绑定的原生会话，离线时保留待交付请求。
- `workflow.action.get` 读取当前请求；宿主将其中完整的 `system_prompt`、`user_prompt` 交给原生子代理。`workflow.action.respond` 提交文本及原样的 `version`、`request_hash`。请求哈希防止把其他选区或其他版本的模型回答复用到当前规划过程。
- 若规划器需要下一次生成，返回新的 generation request；`ready` 表示预览就绪，外部 Agent 应结束该轮。MCP 不返回候选 HTML 和确认令牌，避免把大型产物重新灌入模型上下文。
- 页面轮询同一请求并展示原来的差异预览；只有用户确认后才走原有 `:action-execute`。确认使用创建预览时固定的包版本，并检查产物 revision 和人工草稿版本。取消后禁止确认；相同响应重试幂等，不重复调用规划器。
- 当前 PPT 包支持外部润色。存在 `native_pptx` 配套产物时，文本润色只对可唯一定位的原生文本做局部替换，保留其他 ZIP 部件（包括图表和字体），并将 HTML 与 PPTX 版本放在同一数据库事务中保存。无法唯一定位、多文本段结构变更或样式操作应转原生 PPTX 编辑器，不允许只改预览却留下旧下载文件。

新增开发者接入只需实现已有通用 host action 交付及上述 MCP 工具，不需要复制 PPT 业务 Skill。其他工作流可复用 generation boundary，但必须在包内显式声明支持；不支持时返回升级工作流提示，不静默回退到 LazyMind 模型。

### 外部执行的上下文与耗时

`workflow.get` 只提供发现与启动契约，不下发 Python 实现源码；MCP 的状态投影省略完整 compiled graph，保留控制状态、就绪步骤、拓扑投影和执行历史。当前步骤的完整业务 Skill、约束与输入仍由执行授权中的 `step_contract` 提供，不能为节省上下文删减业务规则。

文件产物必须在 `output.local_path` 指定路径；`output.value.local_path` 会被明确拒绝，防止把路径对象误存成已经上传的文件。宿主避免把 HTML 的内嵌字体、图片或整个工具响应直接打印进模型上下文。评估性能时分别记录执行步骤、调度间隔、返工和总墙钟时间；模型未返回 token usage 时记为未知，不能记为零。不同模型、工作流版本和原生 PPTX/HTML 交付要求的耗时不可直接按页数比较。

### Chat edits to completed artifacts

`workflow.edit.context(session_id)` returns the current ordered artifact references,
state version, published package revision, original Skill Markdown and the package's
`artifact_edit` policy. The native and external paths share LazyMind's revision,
publication, linked-list ordering and durable event storage; the host supplies model
work and missing file tools. No Codex-specific business pipeline is introduced.

For user-requested local changes, scoped page regeneration, insertion, deletion or
reordering, delegate the complete contract to a native worker and validate the
current artifact. Submit `workflow.edit.commit` once with `revision_id`,
`state_version` and `slots`. Each included list is the full desired visual order:
`{list_index: N}` preserves an item; adding `content_type` and `value` replaces it;
a new item omits `list_index`; omitting an existing item deletes it. File bytes use
item-level `local_path` in MCP. All nonempty package-declared linked lists must be
included with equal nonzero lengths, together with the updated required file.
Unchanged pages keep their revisions. The transaction checks the session version,
uses the native artifact mutation functions, and rolls back the whole bundle on
conflicts. A retry with an old version fails; reread and reconcile rather than
applying insertion/deletion twice. The session stays completed.

The PPT policy requires preserving editable objects, synchronizing preview HTML,
notes and visible page numbers, and validating native file page count and rendering
before publication. These semantic checks belong to the worker; the server enforces
bundle structure, ownership, allowed slots and optimistic concurrency. This endpoint
does not itself regenerate slides or verify factual content. New user instructions
authorize their edits; a queued AI dialog preview still requires its own user
confirmation and must not be committed through this endpoint.

Package configuration is read at the published head for edit-context discovery and
pinned by revision for commit. Older packages without `artifact_edit` return an
explicit upgrade error. Existing running step contracts remain pinned unchanged.

`workflow.action.cancel` cancels an explicitly discarded preview without changing
published artifacts. Pending or ready AI previews block chat bundle commits until
confirmed or cancelled. Planner replay runs outside database transactions because
it reads its pinned package back through Core; the result is committed with a
version comparison, so cancellation or another response cannot be overwritten.

Editable export prefers the selected native PPTX for the entire current deck when
its publication attempt matches the previews, or when it is newer than all preview
revisions. Mutable/newer preview drafts and partial exports keep the HTML conversion
path. File deliveries remain in the export action's declared tab even though the
page layout renders them outside its page list. This avoids re-converting an
already editable delivery and preserves its native charts and fonts.

Refresh an installed Codex plugin without controlling the desktop lifecycle with
`lazymind internal agent codex refresh`. Existing Codex chats may retain their MCP
process/catalog; a new chat loads the refreshed tools and Skill. Refresh does not
restart Codex or launch LazyMind Desktop.

### Presentation content fidelity

Skill delivery and content adherence are different checks. PPT acceptance is shared
by native and external executors: review source-to-outline coverage, then map each
page's retained claims to planned visible copy and final visible objects. Keep the
map and explicit omissions in validation evidence; hidden notes do not satisfy
visible content coverage. When exporting HTML and native PPTX, compare both against
the same page plan and inspect their renders separately. Do not use file validity,
Skill hashes or a fixed sparse host template as a substitute for this review.
