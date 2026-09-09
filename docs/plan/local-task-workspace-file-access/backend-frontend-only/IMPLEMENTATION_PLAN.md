# 工作区工具授权与文件执行方案

> 当前状态：2026-09-09 A2-R1 行为测试已交付，等待修复 Review。用户已批准原设计和 A1/A2 实现，但 A2 尚有权限与重复执行缺口，不能标记整体完成。测试先行，每个批次经过人工 Review；不创建其他仓库或工作树。当前状态以第 12 节为准，第 7–11 节保留原设计与阶段记录。

## 1. 目标与已确认边界

让 Agent 在用户选定的工作区内读取、创建、修改、追加、删除文件。工具注册声明授权元数据，项目 ToolExecutionMiddleware 在实际执行前联系 Core；需要批准时等待用户决定，随后恢复同一个工具调用。主任务、普通子任务、已声明相应工具的 Workflow 使用同一规则。

- 当前唯一仓库 `/Users/theone/Downloads/lazymind`，分支 `feature/newWorkZone`，本轮起点 `7e04900ee331553829917be1e73c661ff8b0103c`。保留已有改动。
- 用户已确认：允许必要的 `algorithm/lazymind/` 和对应测试修改。算法整体零差异不再是本轮要求；无需再次申请相同范围授权。
- LazyLLM 内容、`algorithm/lazyllm` gitlink 仍与 `245bc26dca1f2e8b56b0766cf72fdfcdb49138d9` 一致；Local/Desktop 与 `ec4676e0d0fb290d81b3160a56e798849ea2d4e4` 一致。
- 不开启 trusted，不做运行时补丁，不重新注入旧 workspace token；本机层仍只证明目录选择并转发授权。
- Core 是 grant、binding、permission、目录状态和 reason 的唯一业务权威，负责受控工作区文件的磁盘访问。算法不能仅凭快照或模型文字批准操作。
- 不新增服务、依赖、数据库表或通用授权框架；不改现有内部产物的存储语义，不恢复通用 shell 删除。
- 原 U1–U7、C1–C2 的自动化补齐已经完成；历史 49/49 不能用作本次新能力验证。T6 实机验收和 F 文件执行仍未完成。

## 2. 路线选择及复用

| 路线 | 判断 |
|---|---|
| 项目 ToolExecutionMiddleware + Core | 推荐。已有 dispatch_selector 在派发前运行，普通子任务也用 AgentExecutor；在项目层完成，不改 LazyLLM |
| 修改 LazyLLM ToolManager | 不采用。已存在所需前置扩展点，修改底层库会扩大范围并破坏冻结要求 |
| 每个工具各写一套批准/AskCard 新轮次 | 不采用。会重复判权、漏掉搜索/子任务，也不能保证恢复原调用 |

直接复用：ToolConfig 注册、AgentExecutor 装配、FailureRetryPolicy/取消检查、`core_api_client.py` 的 get/post 与内部服务认证、Core ResolveForConversation/ResolveActiveForBinding/store.State、现有 API JSON/error helper、LocalWorkspaceControl/Modal/request-id/i18n。

ToolLimitDecisionCoordinator 仅参考同轮等待、取消和超时的实现方式：它按 sid 保存单项决定、动作只有 continue/summarize，不能整体复制或泛化成新框架。RemoteFS 只处理已有虚拟挂载，也不扩成任意宿主路径接口。

## 3. 工具注册与执行合同

### 3.1 元数据

在现有 ToolConfig 增加一个按方法声明的可选字段，建议形式：

```python
authorization: dict[str, Literal['read', 'write', 'delete', 'external']] | None = None
# local_fs 的示例；这是可信代码配置，不是模型参数。
authorization={'ls': 'read', 'glob': 'read', 'grep': 'read', 'read': 'read',
               'info': 'read', 'create': 'write', 'mkdir': 'write',
               'string_replace': 'write', 'overwrite': 'write',
               'append': 'write', 'delete': 'delete'}
```

该字段表达“需要进入授权判定”及操作类别，不直接决定是否弹窗。具体方法从实际选中的 callable 解析，使用现有 Toolkit 展开规则建立每次执行器的索引；不得只匹配字符串前缀，不共享可变全局映射，不把完整 ToolConfig 再复制成第二套 DTO。

在工作区任务中，未知/同名覆盖/未审计的宿主执行工具不能因缺字段自动放行；普通无工作区任务维持现有行为。纯计算工具仅在确认无宿主文件/外部副作用后免此工作区授权。注册声明用于调度，Core 仍验证具体操作，不能信任客户端传入的风险分类。

### 3.2 执行顺序

1. ToolManager 使用现有参数校验准备调用；中间件读取准备后的真实工具、规范化参数及可信任务身份。
2. 对工作区调用先做取消检查、重试筛选，再向 Core prepare；禁止用模型传入的 owner/root/run/approval 覆盖系统字段。纯参数校验必须无磁盘副作用。
3. Core 解析真实任务归属和绑定，检查授权、操作、路径、版本及风险，返回 allow/deny/pending。deny 生成明确失败记录，不能进入工具执行。
4. pending 在原执行线程中有界等待，前端单独向 Core 决定；不结束 ReAct 轮次、不拼接一条用户消息，也不消费 AskCard。
5. 批准后继续原 prepared call；调用参数不变，Core execute 再次检查状态并单次领取操作。LocalFileToolkit 对绑定工作区的文件访问统一转发 Core，不能在失败时回退 Python 本地读写。
6. 返回 Core 的真实结果后继续模型下一步，因此同轮创建→读取→追加/修改→读取成立；拒绝和冲突作为真实结果返回。

授权失败/待批准不算工具故障重试；带副作用的工作区调用不能被现有“相同参数”去重逻辑静默合并。网络重试复用同一 operation_id；模型生成新的 call_id 是新操作，不自动继承批准。

## 4. Core 接口与批准状态

建议接口放在现有 Core localworkspace 包和 routes 中，不创建新服务：

| 接口 | 输入与作用 |
|---|---|
| POST `/internal/conversations/{conversation_id}/workspace-operations:prepare` | 内部服务身份 + run/task/attempt 身份、call_id、tool、operation、相对 path、参数摘要、expected_version；Core 生成 operation_id 与决定 |
| GET `/internal/conversations/{conversation_id}/workspace-operations/{operation_id}` | 原运行者查询 allow/pending/deny/expired；算法短请求轮询并检查取消 |
| POST `/internal/conversations/{conversation_id}/workspace-operations/{operation_id}:execute` | 原参数、内容及相同摘要；重新授权后执行，返回真实结果/version/reason |
| GET `/conversations/{conversation_id}:workspace-approvals` | 登录用户按真实会话 owner 读取待批准摘要，支持刷新后恢复展示 |
| POST `/conversations/{conversation_id}/workspace-approvals/{operation_id}:decide` | 仅 allow_once/reject，前端不提供路径或修改操作内容；Core 核对 owner 和状态 |

Core 的 request/result 结构各定义一次，直接由 handler/service 共用；语言间必要的静态类型不再包一层 facade。内部认证使用已有服务凭据，与选择目录专用 host token 分开；缺少凭据时拒绝，不沿用部分旧端点“token 未设置则放行”的兼容逻辑。

身份由 Core 会话/子任务/Workflow attempt 记录复核：子任务必须属于该 owner 和父会话，Workflow 必须是活跃 attempt、声明工具匹配且 lease 有效。仅传 user_id/conversation_id 不足以证明旧 run 仍有效。可信 workspace 上下文明确接入主请求 schema/组装与子任务私有参数；不能假定 ext 自动透传。

复用 store.State() 的 SQLite/Redis，记录 operation_id、owner、conversation、run/task/attempt、call_id、参数摘要、grant/目录身份/文件版本、状态和期限。文件内容/凭据不进入批准卡片、模型参数、错误日志或公共 Attempt Context。

- 状态：pending → allowed/rejected/expired；allowed → executing → completed/failed/uncertain。每次决定和领取使用 SetNX 等原子原语，不能用 Get+Del 模拟原子消费。
- 建议等待期限 5 分钟、每个会话最多 16 项未完成请求，查询返回有界列表；终态回执保留 24 小时。算法每秒一次短查询，取消时立即终止等待；不持有文件锁等待用户。
- 刷新页面可查询原 pending；终止任务、失去 Workflow lease 或算法 run 已结束后旧批准失效。审批不能启动一个已经结束的 run。
- 权限切换影响后续操作；既有 pending 不因切成 allow_all 自动批准。用户明确批准旧 pending 后仍重新检查 grant 和文件版本，不能绕过新的永久拒绝。
- 同一 operation_id 完成后返回保存的回执；执行中断而提交结果未知时返回 uncertain，禁止自动再次追加。不得声称数据库记录与任意宿主文件存在跨系统事务。期限后旧 ID 不重新创建，同一 call_id 也不重新自动发放批准。

## 5. 权限和产品行为（本次 Review 内容）

沿用历史三档权限定义，删除作为新增的破坏性文件操作明确列出：

| 操作 | always_ask | ask_as_needed | allow_all |
|---|---|---|---|
| 普通列表/搜索/读取 | 允许 | 允许 | 允许 |
| 普通创建/修改/覆盖/追加/mkdir | 逐次批准 | 允许 | 允许 |
| 单文件删除 | 逐次批准 | 逐次批准（新增规则） | 允许 |
| 命中敏感规则的读取 | 逐次批准 | 逐次批准 | 允许（沿用可批准操作免询问语义） |
| 越界、失效授权、敏感文件写入、`.git` 写入 | 拒绝 | 拒绝 | 拒绝 |

敏感规则由 Core 持有一份确定清单和测试：初始覆盖 `.env`/`.env.*`（明确排除 `.env.example`、`.env.sample`、`.env.template`）、`.ssh`/`.aws` 下文件、私钥/凭据文件名（id_rsa/id_ed25519、*.key、*.pem、credentials*、service-account*.json）。这是文件路径规则，不能声称能发现普通文件中的所有秘密。读内容的 grep 也须按匹配文件判权；不能先读出敏感内容再询问，混合搜索需逐文件授权或返回明确跳过原因。

删除只针对工作区内一个普通文件，需 expected_version；不删除目录、不递归、不接受 glob，不用 shell，不默认改成回收站。mkdir 为显式受控动作，不在写文件失败时偷偷创建任意父目录。

自定义 Python、shell、未声明 MCP/其他工具能够在其内部自行访问文件，元数据不是进程沙箱。建议绑定工作区的任务拒绝执行尚未接入的此类工具，返回 `tool_not_authorized` 并列明不兼容点；未绑定工作区的原流程保持原状。Workflow 自定义包同名覆盖官方工具也不能继承官方工具授权。这项兼容性收紧随本方案 Review，未经确认不能静默实施；命令/联网/应用完整授权不是本轮文件操作完成的附带承诺。

## 6. 文件执行与竞态

- Core 在现有 localworkspace 包实现路径访问和文本操作；LocalFileToolkit 保留既有无工作区数据源行为，工作区分支转发 Core。旧 Python 文本替换仍有其他消费者，不能直接删除。
- read/info 返回可用于后续修改的文件 version；create 要求目标不存在；overwrite/string_replace/append/delete 要求 expected_version。版本与内容/文件身份相关，不能用 grant.version 冒充。
- 保留旧方案 20 MiB 写入上限；超过限制明确失败。追加以读旧版本、构造新内容、同目录临时文件、替换的方式执行，不使用无版本 O_APPEND。精确修改保留现有匹配次数和错误语义。只复用与宿主访问兼容的文件处理逻辑，不把 Workflow 内部 artifact helper 直接当授权文件系统。
- 绝对路径、`..`、路径编码歧义、根/中间路径替换、符号链接、非普通文件、Windows 路径别名需具体测试；不以字符串前缀或单次 realpath 作为安全保证。任何既有绝对路径兼容只能由 Core 在复核绑定后规范化，模型不能指定新的根。
- 查询、读取、提交都重新检查 grant/binding、目录实际身份；提交与 Core revoke/权限变化协调。对用户在外部程序中的并发编辑，需要真实冲突测试；“检查后 rename”不能宣称原子的外部 compare-and-swap。
- 在无新增依赖/冻结平台层的条件下，先验证可用的文件句柄与原子替换原语。某平台的符号链接/目录竞态或外部并发保证无法证明时，该验收项保持失败并报告，不能以弱实现、删测试或 trusted 放行。
- 不新增 PDF/Office 解析器；复杂格式沿用已有资源解析能力，但宿主文件读取仍需受控。格式兼容和发现工具输出须逐项对照现有行为。

## 7. 文件范围与规模预算

当前只有四份文档的实际 diff，生产净增 0。下表为整个方案的预估，非已完成 diff；不通过拆批隐藏总量。

| 职责 | 预计生产文件 | 净增预估 |
|---|---|---|
| 注册、等待和主子任务装配 | 现有 algorithm/lazymind/chat/service/component/tool_registry.py；engine/agent_runtime/tool_call_guard.py、executor.py、models.py；service/chat_request.py、chat_service.py；engine/subagent/runner.py | 200–320 |
| 工具转发及发现能力 | 现有 algorithm/lazymind/chat/engine/tools/local_fs.py；复用 infra/core_api_client.py，不另建 HTTP client | 100–180 |
| Core 批准/权限/状态与文件执行 | 新增 backend/core/localworkspace/operations.go、approvals.go；现有 handlers.go、service.go、context.go、subagent_context.go；backend/core/routes.go、chat/local_workspace.go、必要的 chat/chat.go 请求组装 | 450–650 |
| 批准 UI/API 与原因文案 | 现有 frontend/src/modules/chat/components/ChatInput/LocalWorkspaceControl.tsx、utils/localWorkspace.ts、i18n/locales/zh-CN.ts、en-US.ts | 120–200 |

总预算约 870–1350 行生产净增、2 个新生产文件，超过原约 200 行/1 个新文件门槛，必须单独 Review。本轮不能以用户允许算法修改推导出规模自动获批。现有代码缺少 Core 文件执行和逐调用批准状态，这是两份新文件不可省略的职责；不用 manager/facade/重复 DTO 包装，也不把两者硬塞进 service.go 以满足文件数量。

尽量缩小事件/UI接入：复用现有工作区控件，在有绑定的当前会话查询 pending 并用已有 Modal 展示操作、相对路径、来源主/子任务和版本摘要；先查询再决定，切会话关闭窗口并使旧结果失效。隐藏/卸载时停止轮询，恢复显示时重查；已批准未完成不能标“成功”。不改 AskCard/ToolLimitCard 的产品语义，不新增聊天 SSE 字段链或通用弹窗框架。

删除只针对被本批替换且确认无其他调用者的旧提示词授权分支/重复工作区分支；保留普通问答、内部产物、无绑定数据源和其回归测试。不能靠删除拒绝/竞态测试降低代码量。

## 8. 顺序、门禁和验收

任务与命令见 task_plan.md。A0 先交付测试及真实失败分类，不改生产；A1–A3 分别实现注册/可插拔执行阻断、Core文件执行、批准UI与主子Workflow闭环，每批先补该批失败合同再 Review 生产。任何单批也报告新增/删除/净增、真实文件和未验证项。

首次测试重点是阻断位置、注册方法映射、权限矩阵与兼容性，不在设计阶段提交一套可绕过 Core 的临时生产实现。完成的判定必须同时包含真实磁盘、同轮反馈、拒绝零副作用、版本冲突、撤销、批准单次消费、主/子/Workflow 路径、Local/打包 Desktop 平台证据。只跑 mock/组件测试不称文件能力完成。

本稿具体生产设计、两项兼容性选择（删除规则和未接入工具拒绝）、等待/回执期限及总规模等待用户 Review。当前没有修改生产或测试，没有提交/推送。

## 9. Review 后必须修正的设计

独立审查发现 Workflow 自定义脚本在 `runner.py:237` 已于 `AgentExecutor` 安装前执行 `exec(compile(...))`。因此 A3 不能只在工具调用时拒绝未知 callable：绑定工作区的 Workflow 在加载脚本前必须通过 Core/本地静态准入检查，确认包只包含已声明、已审计的受控工具；无法证明时整包拒绝。A0 必须加入顶层 import-time 文件副作用测试。无工作区 Workflow 的既有行为保持不变。

`PreparedToolCall` 只有调用数据和资源访问描述，没有原始 callable 或批准句柄。授权上下文由项目中间件在 selector 内创建，包含原始调用摘要、任务身份和 Core operation_id；批准恢复时只接受匹配的上下文，绝不把批准状态写回模型参数或工具参数。`ResolvedToolAccess` 仅用于调度冲突，不能作为业务授权替代物。

为了减少冗余，Core 可以保留 prepare/status/decide/execute 的四种语义，但由一个 `workspace-operations` service 统一状态与类型，handler 只做认证、解码和回复；不要把状态机复制到多个 handler，也不要为了“少文件”塞入现有 `service.go`。前端登录用户的 pending 列表可以继续使用独立读取端点。这个调整将预算下修为约 700–1100 行生产净增、最多 2 个新生产文件，仍超过原门槛，必须单独 Review。

在 A0 失败合同完成前，不能开始生产实现。未验证的普通子任务全路径、远程 Workflow lease 和 Local/打包 Desktop 文件原语继续列为实测项。


## 10. A1 已交付范围

A1 已在算法项目接入轻量授权扩展点：ToolConfig 可声明方法级操作类别，AgentExecutionOptions/AgentExecutor 可传递授权 gate，ToolExecutionMiddleware 在底层 ToolManager 派发前闭合处理 deny/unknown，Workflow 绑定工作区时在脚本编译前默认拒绝未审计脚本。A1 不创建 Core gate 实例、不等待用户、不执行磁盘操作，故不改变当前真实工作区权限行为。

A1 生产净增约 66 行、5 个既有算法文件；新增 1 个测试文件；52 项可运行矩阵通过。完整服务图仍受本地依赖组合限制，需在 CI/发布环境复核。下一批 A2 只新增 Core operation 状态/文件执行失败合同，先 Review 再实现。


## 11. A2 首轮 RED

A2 测试合同已建立但未实现生产：Core 目前没有 `operations.go`、`approvals.go` 或对应路由，合同运行结果为 4 个预期失败、0 个异常失败。生产实现必须先决定 operation service 与批准状态机边界，再补真实磁盘测试；不得以源码字符串合同通过后宣称文件能力完成。


## 12. A2 当前状态与 A2-R1 修复 Review（2026-09-09）

提交 `a5761633` 已提供 Core 读、创建、追加、替换、删除基础实现，以及算法 read/string_replace/create/append/delete 的 Core 转发分支。按 Git 重新统计，A2 生产新增 894、删除 1、净增 **893 行**：operations.go 502、approvals.go 177、routes.go 4、local_fs.py 210；新增生产文件 2 个。此前的 895 行和“完整安全边界/单次执行已完成”表述不准确。

先完成 A2 遗留修复，再进入 A3 批准 UI 和原调用恢复。A2-R1 仅针对已有合同中的权限决定与相同 operation_id 重复执行，不改变权限产品定义。

| A2-R1 验收 | 本次真实结果 | 拟修复方式 |
|---|---|---|
| `.env` 在 always_ask / ask_as_needed 下须 pending，批准前 execute 拒绝 | 2 项预期失败：直接 allowed | 普通读免询问前排除敏感路径；allow_all 和示例文件规则保留 |
| 普通读取、`.env.example`、allow_all 敏感读保持允许 | 7 项通过 | 保留现有行为 |
| 延迟执行者领取锁时发现操作已完成，只返回已保存回执 | 1 项预期失败：外部撤回的追加被再次执行 | 领取锁后重新加载状态再决定是否执行 |
| failed 是终态，恢复文件旧版本也不能复用旧批准追加 | 1 项预期失败：再次写入 | 仅 allowed 状态可进入 executing，failed 返回现有冲突 reason |
| completed 读取返回保存的无内容回执，不用旧 ID 获取新内容 | 1 项预期失败：返回文件新内容 | 读与写统一处理 completed；新读取必须发起新的调用 |

实际修改：既有 `backend/core/localworkspace/operations_test.go` 净增 **168 行**和本目录四份文档；生产净增 **0**。复用 operationFixture、SQLite state、临时文件和 requireWorkspaceReason；一个仅用于测试的 Store 包装在 SetNX 前确定性插入另一个请求，真实文件执行不 mock，无 sleep、运行时补丁或新增依赖。该用例证明具体交错下的重放缺陷，不替代完整并发/跨进程验收。

拟生产修复仅修改既有 `backend/core/localworkspace/operations.go`，预计净增 **15–40 行**、新增生产文件 **0**。复用同一状态结构、状态读取/保存 helper、permissionDecision、现有错误 reason；不新增服务、表、manager 或 DTO。原子解锁、锁租期、prepare 幂等不混入这一次修复，后续须独立补合同，不能把本批通过称作完整单次执行保证。修改范围若超出上述预算，先报告实际 diff 再 Review。

本次命令（`backend/core`）：基线 `go test ./localworkspace -count=1` 通过；新增四组聚焦测试为 7 通过、5 预期失败；`go test -race ./localworkspace -count=1 -json` 全包叶子用例共 **43 通过、5 预期失败、0 异常失败**，原有 36 项全部通过，未报告 Go data race。失败矩阵是待修复证据，不是验收通过。批准生产后须将新增 12 项转绿，并重新运行 Core 工作区/聊天/子任务回归。

仍未解决：prepare 在决定前计算文件内容哈希；路径校验后重新按路径访问的竞态；根/父目录替换、外部并发提交及提交前撤销协调；执行锁租期/原子解锁、prepare 幂等、uncertain 与崩溃恢复；ls/glob/grep/info 的 Core 接入；mkdir 等方案项；真实 Core gate、原调用批准恢复、批准 UI、主/子任务和 Workflow 生命周期/lease 复核以及 T6 实机验收。这些项目不能因本批的状态修复而勾选完成。
