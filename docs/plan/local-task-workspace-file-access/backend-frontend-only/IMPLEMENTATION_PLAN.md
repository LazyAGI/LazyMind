# 工作区工具授权与文件执行方案

> 2026-09-09 最新：用户确认的R3–R7生产及统一自动化测试已完成，结果/实际代码量/兼容限制见末尾“最终生产/自动化结果”；实际运行验收仍未完成。开发起点7bc81ffa，本轮相关代码与文档同批提交，未推送远端。

> 当前状态：2026-09-09 用户已要求剩余功能连续实施生产、之后统一测试，替代原逐批测试先行及测试后 Review 顺序。A2-R2 已提交 `69e4809e`。本轮已完成全链路源码核对，剩余整体范围与超量预算见第 14 节；原代码量门槛仍须 Review，尚未实施本轮生产或运行测试。LazyLLM/gitlink、Local/Desktop 冻结不变。

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

## 7. 原设计文件范围与规模预算（历史，现以第 14 节为准）

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

## 8. 原测试先行顺序（历史，2026-09-09 已由用户改为生产后统一测试）

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


## 12. A2 当前状态与 A2-R1 修复结果（2026-09-09）

提交 `a5761633` 已提供 Core 读、创建、追加、替换、删除基础实现，以及算法 read/string_replace/create/append/delete 的 Core 转发分支。按 Git 重新统计，A2 生产新增 894、删除 1、净增 **893 行**：operations.go 502、approvals.go 177、routes.go 4、local_fs.py 210；新增生产文件 2 个。此前的 895 行和“完整安全边界/单次执行已完成”表述不准确。

先完成 A2 遗留修复，再进入 A3 批准 UI 和原调用恢复。A2-R1 仅针对已有合同中的权限决定与相同 operation_id 重复执行，不改变权限产品定义。

| A2-R1 验收 | RED → 本次结果 | 实际修复方式 |
|---|---|---|
| `.env` 在 always_ask / ask_as_needed 下须 pending，批准前 execute 拒绝 | 2 项预期失败 → 通过 | 普通读免询问前排除敏感路径；allow_all 和示例文件规则保留 |
| 普通读取、`.env.example`、allow_all 敏感读保持允许 | 7 项通过 | 保留现有行为 |
| 延迟执行者领取锁时发现操作已完成，只返回已保存回执 | 1 项预期失败 → 通过 | 将唯一一次状态读取移到领取锁之后，避免重复校验和旧快照 |
| failed 是终态，恢复文件旧版本也不能复用旧批准追加 | 1 项预期失败 → 通过 | 仅 allowed 状态可进入 executing，failed 返回现有冲突 reason |
| completed 读取返回保存的无内容回执，不用旧 ID 获取新内容 | 1 项预期失败 → 通过 | 读与写统一处理 completed；新读取必须发起新的调用 |

测试批次 `cff178a1`：既有 `backend/core/localworkspace/operations_test.go` 净增 **168 行**，生产净增 0。复用 operationFixture、SQLite state、临时文件和 requireWorkspaceReason；一个仅用于测试的 Store 包装在 SetNX 前确定性插入另一个请求，真实文件执行不 mock，无 sleep、运行时补丁或新增依赖。该用例证明具体交错下的重放缺陷，不替代完整并发/跨进程验收。本次生产批次仅调整该测试的一行注释以符合新的读取顺序，没有删改断言。

用户“直接生产吧”已批准该范围。实际只改既有 `backend/core/localworkspace/operations.go`，生产新增 **13 行**、删除 **11 行**、净增 **2 行**，低于原预估 15–40 行，无新增生产文件。通过调整执行顺序替代新增两套状态校验，复用同一状态结构、状态读取/保存 helper、permissionDecision、现有错误 reason；没有新增服务、表、manager 或 DTO。A2 基础实现加本次修复的生产累计净增为 895 行。原子解锁、锁租期、prepare 幂等不混入这一次修复，后续须独立补合同，不能把本批通过称作完整单次执行保证。

验证（`backend/core`）：`cff178a1` 的 RED 矩阵为 43 通过、5 预期失败、0 异常失败；本次新增四组 **12 项全部通过**，`go test -race ./localworkspace -count=1`、`go test ./chat ./subagent -count=1`、`go vet ./localworkspace` 均通过，无 data race 报告。范围检查通过；Local/Desktop、LazyLLM/gitlink 均未改动。未运行完整算法/前端矩阵或实机验收，不用历史结果替代本次证据。

仍未解决：prepare 在决定前计算文件内容哈希；路径校验后重新按路径访问的竞态；根/父目录替换、外部并发提交及提交前撤销协调；执行锁租期/原子解锁、prepare 幂等、uncertain 与崩溃恢复；ls/glob/grep/info 的 Core 接入；mkdir 等方案项；真实 Core gate、原调用批准恢复、批准 UI、主/子任务和 Workflow 生命周期/lease 复核以及 T6 实机验收。这些项目不能因本批的状态修复而勾选完成。

## 13. A2-R2：一次性领取实现与验证（2026-09-09）

测试批次起点 `b76d18f4`，失败合同提交 `1542434e`；用户已批准本批生产范围。本批只处理已有 operation_id 的批准决定和执行领取。R1 的锁后读取不能防止“读完状态后停顿，锁先到期”的交错；现在直接复用 SetNX 保存一次性消费记录，删除短期锁的领取/释放 helper。

| 行为合同 | RED → 本次结果 |
|---|---|
| 领取后读取 allowed 快照，原 2 分钟锁过期，其他请求不能导致同一操作追加两次 | 1 项预期失败 → 通过；完成回执不计为执行 |
| 领取后读取 pending 快照，锁过期不能使 allow_once 与 reject 均写入成功 | 1 项预期失败 → 通过 |
| execute / decide 不使用 Get+Del 释放领取 | 2 项预期失败 → 通过 |
| 错误 owner/call_id/content 的执行请求不能消耗有效批准 | 3 项通过 |
| 错误 owner/action 的决定请求不能消耗有效批准 | 2 项通过 |
| execute / decide 写入状态失败后，存储恢复也不能自动接管同一操作 | 补充 2 项预期失败 → 通过；创建文件的副作用为零 |

测试阶段扩展现有 `operations_test.go`（+156）和 `approvals_test.go`（+63），共 **219 行**，测试提交无生产修改。本次生产批次先补充状态写入失败的 2 项真实行为合同（既有 operations_test.go +56 行），复现 RED 后再实施。复用 operationFixture、真实 SQLite state、临时目录、requireWorkspaceReason。测试包装器捕获状态快照，并仅将领取记录的时间推进到第 3 分钟；操作本身仍在原 5 分钟有效期内。SetNX/状态持久化与磁盘操作仍调用真实实现，不等待真实分钟数。另一个包装器只暴露必需的 state.Store 接口，观察是否使用非原子 Del；没有修改状态接口或后端实现。该模拟证明具体交错，不能冒充 Redis/跨进程/长时间实测。

实际生产范围仅既有 `backend/core/localworkspace/operations.go`、`approvals.go`：合计新增 **30 行**、删除 **35 行**、净减少 **5 行**，**0 个新生产文件**，低于获批预估净增 30–60 行。operations.go +18/-25，approvals.go +12/-10；复用状态结构、校验、错误 reason 与 SetNX，不新增框架。A2 基础实现加 R1/R2 的生产累计净增为 **890 行**（893 + 2 - 5）。实际行为：

1. 领取前核对 owner、调用摘要、状态与批准 action，避免错误请求永久占用标记。completed 返回已保存的无内容回执，pending/failed/不匹配请求保持明确拒绝。
2. 决定和执行各使用一个既有 SetNX 标记，保留 24 小时且不主动删除；它是一次性消费记录，不是到期后可接管的互斥锁。24 小时是本批标记保留时间，不代表原 5 分钟批准有效期延长，也不代表终态回执 24 小时保留已经完成。
3. 成功领取后重读当前状态/有效期，不能执行旧快照。未能领取时只查询已有状态：completed 可返回回执，其他状态拒绝接管；不得因为文件版本又匹配或旧请求失联而重放。
4. 删除不再调用的 releaseOperation 和 Get+Del 兜底，替换旧 2 分钟锁常量；不新增锁 manager、续期任务、依赖、表、状态接口或 DTO。无效 action 在消费前拒绝，已有身份和状态校验继续复用。
5. 状态存储异常时不执行、不清除消费标记以强行重试；查询仍不能证明磁盘已成功。完整 uncertain/结果未知展示和崩溃恢复属于后续合同，不能在本批标记完成。

生产批次已删除 `operations_contract_test.go` 中只检查状态名/SetNX/CompareAndDelete 字面出现的旧源码合同（-14 行，1 个测试）；它不能证明单次执行，且强制保留 CompareAndDelete 与一次性消费不再相符。本批行为合同替代其领取/释放检查，未完成的过期/uncertain 行为验收继续保留，其他测试不删除。

验证命令（`backend/core`）：基线 `go test ./localworkspace -count=1` 通过；`go test ./localworkspace -run '^TestWorkspaceClaim' -count=1 -v` 为 **5 通过、4 预期失败**；`go test -race ./localworkspace -count=1 -json` 按叶子用例统计 **53 通过、4 预期失败、0 异常失败**，原有 48 项通过，无 data race 报告。首次测试收集有一个漏导入 state 的编译错误，已补回测试 import；没有作为产品 RED。上述为测试阶段 RED 证据。本次原 9 项及补充 2 项共 **11 项全部转绿**；`go test -race ./localworkspace -count=1 -json`、`go test ./chat ./subagent -count=1`、`go vet ./localworkspace` 全部通过。删除的 1 项仅为字面源码检查，其余已有行为合同保留并通过。Local/Desktop、LazyLLM/gitlink 冻结检查通过。未运行算法/前端、Redis/跨进程或打包实机验收，不以历史通过结果替代。

后续边界：prepare 当前每次产生新 operation_id；算法 call_id 是参数哈希，相同参数的独立调用会相同，且请求未传递完整 run/task/attempt 身份。只给 Core prepare 加缓存会把独立调用合并，还不能证明过期 run 无法重新授权，故需在下一批按真实调用身份、存储保留/清理、取消/运行结束整体设计。本批不接入该缓存，不宣称跨 operation_id 幂等。目录/撤销竞态、混合版本 Core 并行运行、崩溃/uncertain、Redis 与 Local/打包 Desktop 实测均仍未完成。

本批只读 agent 审查结论：A2-R2 范围无 critical/important 问题。已精简 failedOperationWriteStore 的无用 CompareAndDelete 接口依赖；未借审查扩大实现。新增错误用例只注入执行前/决定时的状态 Set 错误，不注入领取后的 Get 或完成回执 Set，后两者及完整 uncertain/崩溃恢复没有本批行为验收证据。


## 14. 剩余功能整合：生产完成后统一测试（2026-09-09）

### 14.1 新执行授权与本次实际规模

用户连续要求“直接生产后面统一测试”“直接将功能都开发完再测试”，已明确变更执行顺序：不再为小功能先写 RED、等待确认、再生产。整体范围获批后连续实现下述 R3–R7，再统一补齐/运行测试、处理缺陷和代码 Review；实现途中持续更新本目录四份文档。统一测试后的真实 Local/打包 Desktop 验收仍须列明实际环境，不能以代码完成替代验证。

本轮起点 `69e4809e`，`feature/newWorkZone` 工作区起始干净，领先远端跟踪分支 7 个提交。已读源码和派生调用链，未运行本轮测试，生产/测试代码实际 diff 为 **0**，本次只更新四份文档。不创建仓库/worktree，不推送。

用户原始要求第 7 条的超量 Review 仍适用。按 `git diff --numstat 7e04900e HEAD -- backend frontend algorithm/lazymind` 排除测试，整个授权阶段至今生产新增 **957**、删除 **7**、净增 **950** 行（包括 A1，不能只报 A2 的 890）。剩余五组预计还需净增 **900–1,400 行**，阶段累计预计 **1,850–2,350 行**；低于既往报价的假设已不成立。本表是源码核对后的估计，不是实际完成 diff，不承诺靠删测试/压缩可读性满足估计。

| 连续实施项 | 必需的既有文件范围（相对仓库根） | 预计净增 |
|---|---|---:|
| R3 真实运行身份、prepare 幂等和记录期限 | backend/core/localworkspace/{operations.go,approvals.go,lifecycle.go,subagent_context.go}；backend/core/chat/{chat.go,conversation_logic.go,redis_cache.go,run_decision.go}；backend/core/subagent/{runner.go,handlers.go}；backend/core/main.go；algorithm/lazymind/chat/{api/subagent_routes.py,engine/subagent/runner.py,workflow/remote_executor.py,service/chat_service.py,service/chat_request.py} | 250–400 |
| R4 精确工具派发、原调用批准等待/恢复 | algorithm/lazymind/chat/service/component/tool_registry.py；engine/agent_runtime/{tool_call_guard.py,executor.py,models.py}；engine/tools/local_fs.py（共享修改在各项只计一次） | 180–300 |
| R5 Core 文件/目录、搜索与版本/路径保护 | backend/core/localworkspace/{operations.go,approvals.go,context.go,directory_identity.go,directory_identity_unix.go,directory_identity_windows.go,handlers.go}；algorithm/lazymind/chat/engine/tools/local_fs.py | 300–450 |
| R6 待批准入口与状态展示 | backend/core/routes.go；frontend/src/modules/chat/{utils/localWorkspace.ts,components/ChatInput/LocalWorkspaceControl.tsx}；frontend/src/i18n/locales/{zh-CN.ts,en-US.ts} | 100–150 |
| R7 Workflow 加载准入、错误与兼容收尾 | algorithm/lazymind/chat/engine/subagent/runner.py；backend/core/localworkspace/{approvals.go,context.go}；上述既有路由/文案落点 | 70–100 |

各组文件可重叠，总计约 30 个既有文件；目标 **0 个新生产文件**，不新增服务、依赖、数据库表、manager、facade 或通用框架。准确文件列表及新增/删除/净增在每个实际实现节点记录，若需超出总预算或改变产品/安全规则，再一次性说明新增范围，不重新为既已批准的同类实现请求许可。

不可直接复用的原因已查明：现有 main gate 为空；工具内部没有原 prepared call 身份；普通子任务没有执行代次；Core prepare 每次 newID；不存在 pending 列表；发现工具仍在 Python 本地读盘。复用现有工具/状态/弹窗只能减少重复基础设施，不能替代上述缺失业务连接。

### 14.2 R3：真实身份和一次调用

**主任务：** Core 在 `conversation_logic.go` 创建 run_id 并在派发前写 ChatStatus，ChatHistory 对新任务可能尚无记录。复用 chat 包的 `getChatStatus`、`runDecisionKey`、取消和终态判定；通过现有 lifecycle callback 模式向 localworkspace 提供只读校验，避免 localworkspace 导入 chat 形成循环，不复制 cache key/JSON DTO。prepare/status/decide/execute 检查 owner/conversation、history/run 对应关系、generating 和不存在取消/终态决定。非流式和双回复入口也必须设置/清理同一真实状态，不能生成算法侧 UUID 伪造已注册 run。

**普通子任务：**复用 SubAgentTask 和 Params/现有 state，不新增表。Core 每次真实启动/恢复生成独立执行代次，经 RunRequest → FastAPI 参数 → runner 的私有执行上下文传递。只取本次请求携带的代次，不能从持久化 task.Params 重新读到后来启动的代次而冒充新 runner。Core 校验自己的代次记录和任务 owner/conversation/status；结束/中断/再次恢复使旧代次不可用。子任务是脱离主轮上下文运行的独立任务，主任务正常完成不能直接使仍运行的子任务失效；显式会话停止继续复用已有中断子任务路径。

**Workflow：**复用 `attempt.Service.ValidateLease`、WorkflowSessionStep/Session 的 task/owner/conversation 关系和 FencingGeneration；remote_executor 持有的真实 attempt_id/lease 通过私有执行上下文传入 runner。授权绑定 attempt 加代次，lease 变更即失效。不复用允许终态上报的 authorizeWorkflowExecutorTask 来授权文件访问，不把 lease 放入模型参数、提示词、普通事件或待批准列表。

**原工具调用：**移除 local_fs 的参数哈希 call_id。ToolExecutionMiddleware 获取真实 PreparedToolCall，建立每次执行器内部生成的调用句柄，关联 run/调用序号/实际 tool call id；不同调用即使参数相同也使用不同句柄，同一调用的网络重试复用句柄。模型传入的 owner/root/run/approval 等字段不得覆盖私有上下文。read 预检查加 append 等复合方法使用明确子操作序号，不能把不同子操作混成同一个 Core operation。

**Core 幂等：**在现有 state.Store 中按 owner/conversation/执行代次/调用句柄/子操作组成规范化身份，利用 SetNX 创建一次 prepare 记录；独立保存参数摘要，重用 ID 改参数返回冲突。重试优先返回原 pending/终态记录，不能先按当前文件存在性拒绝已完成 create 重试。5 分钟批准期不因重试延长；无内容回执及去重记录保留 24 小时，过期或运行结束不再创建新批准。不存在/损坏/不可读的状态不能通过 newID 自动兜底执行。

### 14.3 R4：原调用等待，保留官方执行基础设施

采用项目 ToolExecutionMiddleware 已有 dispatch_selector 和结果记录扩展点；通过 manager.tools_info 解析实际 ModuleTool 的实例/方法，与 ToolConfig 注册元数据形成每个执行器内索引。不能只看工具名前缀，不能 monkeypatch LazyLLM、替换其方法、设置 trusted，也不能用进程全局可变参数队列给并行工具“猜测”原 call_id。

对于准确匹配已登记 LocalFileToolkit 的受控调用，在项目层用已有 prepared validated_arguments 驱动受控文件调用并构造原索引的 ToolExecutionRecord，不再把同一调用交给底层重复执行；普通工具继续现有 ToolManager/引用处理路径。注册元数据仍是准入标志，具体权限由 Core 判定。实现前后保留实际调用者身份与允许/拒绝/执行结果映射，不能把真实执行写成 SKIPPED 或把批准拒绝算作普通失败重试。

Core pending 后在原调用内以短 HTTP 查询有界等待；每轮检查取消与运行有效性，批准后继续原参数，不结束 ReAct 轮次、不追加模拟用户消息、不复用 AskCard，不在等待时持有文件句柄/提交锁。模型看到真实执行结果后才进行下一步。权限切换不会自动允许旧 pending；拒绝/超时/取消明确结束对应调用。

未知自定义宿主代码不能因没有 metadata 自动放行。对内置无工作区访问的工具保留已查证路径，逐项记录兼容结果；任意脚本/MCP/shell 的隔离不是 metadata 能提供的，沿用原已确认的未接入代码拒绝边界，不声称完整沙箱。

### 14.4 R5：文件能力和提交边界

- 将 read/create/overwrite/string_replace/append/delete、mkdir 及 ls/glob/grep/info 的工作区访问统一转发 Core；非工作区数据源与内部产物行为保持原有实现，不删除其他使用者代码。
- Core 在读取文件内容前完成授权判定；敏感读取未批准不计算内容哈希，grep 对未获准文件明确跳过或单独申请，禁止先搜出内容再检查。只保留一份敏感路径规则。
- 将路径字符串的重复 realpath/ReadFile/Rename 访问改为固定授权根及父目录句柄内的操作，复用 Go 1.25 文件原语和已存在平台目录身份 helper；校验中间路径、symlink/别名、普通文件与根身份，不把句柄能力夸大为任意进程沙箱。
- create 不覆盖并发出现的文件；修改保留权限，限定文本编码/二进制与 20 MiB 上限，版本来源必须是 Agent 实际观察值，不能在修改前自动读取新版本来绕过冲突；精确替换保留 expected_replacements 行为，失败不部分写入。
- grant/binding/permission、运行资格与提交点协调使用 Core 现有存储事务/相关生命周期逻辑，避免新增通用 manager；拒绝或冲突不能回退本地执行。状态保存失败保留一次性标记，提交结果不明进入 uncertain、禁止自动重放；UI 显示需核对实际文件。
- 外部编辑器不会参与 Core 事务。版本核验加 rename 不能宣称任意外部并发写的原子 compare-and-swap。若某平台保证无法证明，按原第 6 节保留该验收未完成/报告，不通过弱化测试或 trusted 使其“通过”。该限制不会因本次改为后测而消失。

### 14.5 R6/R7：前端和 Workflow

复用 LocalWorkspaceControl 的 Modal、request-id、可见性 effect、现有 axios/error/i18n；补登录用户的 pending 列表，Core 过滤 owner/conversation，仅提供操作、相对路径、调用来源、版本摘要、期限及状态。会话最多 16 个未完成请求的容量控制必须由 Core 原子维护，超限返回明确 reason；不能让并发子任务用无界数组绕过。

切会话关闭旧操作并拒收旧响应；隐藏/卸载停止查询，恢复后重查；允许一次/拒绝只提交 operation_id 和动作，不能把 UI 的路径/权限当权威。允许后显示待执行/执行中，只有 Core completed 才表示完成。原工具调用仍在等待时刷新页面能继续处理；旧运行结束的批准不可重新激活。

Workflow 自定义脚本须在 materialize/import/exec 前完成准入；当前捕获所有异常后回退同名内置工具的路径需要显式拒绝，不能把阻断偷偷变成备用执行。已声明并受控的内置工具和主/普通子任务共用文件规则。移除被真实授权替代的 ModelNotice “ask_user 授权/子任务只能转述”旧分支，并保留一般安全说明，避免代码生效后模型仍遵循过时行为。

### 14.6 统一测试与验收矩阵（实现完成后执行）

| 组 | 必须观察的结果 |
|---|---|
| 身份/幂等 | 同调用多次 prepare 同 ID；同参数不同调用不同 ID；改参数冲突；旧 run/子任务恢复旧代次/过期或被替换 lease 均拒绝 |
| 原调用恢复 | pending 时零副作用；刷新批准后同轮 create→read→append/replace→read→delete；拒绝/超时/取消不执行；传输重试不重复追加 |
| 权限/目录/版本 | 三档权限、敏感内容未批不读、搜索不旁路、.git/符号链接/目录替换/版本冲突/撤销与提交、无覆盖 create、大小和替换计数 |
| 状态故障 | SetNX/Get/写执行态/写终态/进程中断分别注入；保留有界回执/去重记录，uncertain 不自动重放，容量并发不超限 |
| 实际任务入口 | 主流式/非流式/双回复，普通子任务创建/恢复/中断，Workflow 声明内置工具、lease 更替与脚本加载前拒绝 |
| UI/兼容 | 切会话/刷新/隐藏恢复/重复点击、允许不等于成功；普通未绑定工具、AskCard、内部产物和既有文件格式结果回归 |
| 运行环境 | SQLite/必要 Redis、Core/算法/前端回归与构建、Local/打包 Desktop 实机/平台分别记录；缺环境不写通过 |

统一测试阶段保留既有行为合同，删除仅限被替换且无使用者的生产分支/字面测试；不得为了“全部通过”删除未解决能力测试。每组报告真实通过/失败/环境异常，最终完整功能状态以实际矩阵为准，不把方案或生产 diff 当成完成证据。

## 2026-09-09 R3–R7 生产整合进行中

- 已获整体预算确认；当前复用现有文件，不新增生产文件/依赖/表。算法最小增量要求持续执行。
- 当前生产实际 +1568/-482，净增 1086；算法净增 204。统计为未提交整合快照，会随审查变化。
- 前端复用 LocalWorkspaceControl/Modal/axios/i18n 完成 pending 列表与允许一次/拒绝、可见性轮询、切会话隔离。Core 增加真实调用幂等、16 原子槽、24h 回执、运行身份及目录句柄访问；算法接入实际工具注册对象、私有调用身份与 Core 等待，仍在审查。
- 身份/目录/文件生产整合期间执行 `go build ./...` 通过（backend/core）；`go test ./localworkspace -run '^$'` 编译通过。提前运行一次包回归失败：旧 fixture 不含新增真实运行身份，旧文本合同仍要求 ask_user/filepath.Rel/os.Rename；未作为最终验证。DecideOperation 必须接收 DB 复核生命周期，已有测试仅先适配签名，禁止增加绕过验证的 nil-db 兼容层。
- 操作文件首轮批量草稿因缺失 helper 无法编译，已回退该未完成草稿后逐步整合；未提交该草稿。多次命令因 cwd 相对路径误用失败，后续使用绝对路径/明确 backend/core 工作目录，不计作产品 RED。
- 冻结检查 Local/Desktop 对 ec4676e0、LazyLLM gitlink 对245bc26d 无差异。尚未统一测试、端到端或实机验收；未提交/推送。下一步完成只读 Review 修复并开始统一行为/构建矩阵。

实际生产文件：

- `algorithm/lazymind/chat/api/subagent_routes.py`：+2/-0（净 +2）
- `algorithm/lazymind/chat/engine/agent_runtime/executor.py`：+2/-0（净 +2）
- `algorithm/lazymind/chat/engine/agent_runtime/models.py`：+1/-0（净 +1）
- `algorithm/lazymind/chat/engine/agent_runtime/tool_call_guard.py`：+62/-31（净 +31）
- `algorithm/lazymind/chat/engine/subagent/runner.py`：+11/-3（净 +8）
- `algorithm/lazymind/chat/engine/tools/local_fs.py`：+199/-111（净 +88）
- `algorithm/lazymind/chat/service/chat_request.py`：+1/-0（净 +1）
- `algorithm/lazymind/chat/service/chat_service.py`：+4/-0（净 +4）
- `algorithm/lazymind/chat/service/component/tool_registry.py`：+63/-1（净 +62）
- `algorithm/lazymind/chat/workflow/remote_executor.py`：+5/-0（净 +5）
- `backend/core/chat/chat.go`：+4/-0（净 +4）
- `backend/core/chat/conversation_logic.go`：+48/-10（净 +38）
- `backend/core/chat/redis_cache.go`：+7/-4（净 +3）
- `backend/core/chat/run_decision.go`：+65/-15（净 +50）
- `backend/core/localworkspace/approvals.go`：+91/-33（净 +58）
- `backend/core/localworkspace/context.go`：+7/-13（净 -6）
- `backend/core/localworkspace/directory_identity_unix.go`：+5/-0（净 +5）
- `backend/core/localworkspace/directory_identity_windows.go`：+5/-0（净 +5）
- `backend/core/localworkspace/lifecycle.go`：+104/-0（净 +104）
- `backend/core/localworkspace/operations.go`：+637/-243（净 +394）
- `backend/core/main.go`：+6/-0（净 +6）
- `backend/core/routes.go`：+1/-0（净 +1）
- `backend/core/subagent/runner.go`：+101/-15（净 +86）
- `frontend/src/i18n/locales/en-US.ts`：+11/-1（净 +10）
- `frontend/src/i18n/locales/zh-CN.ts`：+11/-1（净 +10）
- `frontend/src/modules/chat/components/ChatInput/LocalWorkspaceControl.tsx`：+90/-1（净 +89）
- `frontend/src/modules/chat/utils/localWorkspace.ts`：+25/-0（净 +25）

## 2026-09-09 统一验证进行中（二）

- 当前生产28个既有文件净增约1181行，算法约217行；未新增生产文件/依赖/表。额外必要的 `backend/core/workflow/store.go` 5行会话锁统一 dismissal 锁顺序；总预算仍在900–1400内。
- 只读Review发现并修复：打开根句柄与目录身份分离校验；过期index阻断列表；撤销后回执不可见；执行/决定已消费后的非可操作状态投影；Workflow session/task/attempt锁顺序；子任务旧代次事件覆盖；提交临界点重新核对有效期/运行资格。后两项仍由身份测试验证。
- Core新增真实SQLite/磁盘行为：同调用幂等和不同调用并发16槽、失效记录不续期、敏感grep跳过与二进制拒绝、根替换拒绝、create不覆盖并发文件、replace计数与权限、撤销后回执/过期索引/跨owner、完成回执Set失败进入uncertain不重放、提交点到期零修改。原R2一次性领取合同保留。
- `go test -race ./localworkspace -count=1 -json`：67叶子用例通过；`go vet ./localworkspace`、Windows amd64与Linux amd64交叉编译通过。交叉编译不是实机验证。
- 原 filepath.Rel/os.Rename 源码字面合同已删除9行（1项），实际目录/版本/并发创建行为保留/增加；ask_user及子任务只能转述旧提示断言改为Core真实批准路径。未删除失败能力合同。
- 前端六文件矩阵68/68，修改文件ESLint、定向tsc与生产构建通过；完整tsc失败662行诊断，修改文件零诊断，未扩大范围修复。相关测试新增19项，覆盖批准不等于完成、拒绝/错误、切会话/隐藏/卸载/恢复和axios边界。不是浏览器+真实Core联调。
- 算法当前聚焦矩阵92通过（真实ToolManager/middleware+HTTP边界mock），更广Workflow/子任务矩阵运行中。算法待补显式receipt标志，禁止把无内容回执当空文件读取。
- 全Core `go test ./...` 首次失败在common的2个错误目录合同：本轮新错误文字没有复用catalog。已定位为本次回归并复用既有store/conflict消息，待重跑。其余包包括chat/subagent/workflow已通过该次运行。不是环境RED。
- 未提交/推送；无Redis服务实测、真实模型同轮闭环或Local/打包Desktop平台验收。下一步完成剩余Review和统一测试重跑，再记录最终兼容边界与准确diff。

## 2026-09-09 R3–R7 最终生产/自动化结果

用户已确认整体范围并要求算法尽量少增加。已按连续生产→统一测试完成本轮实现及已发现缺陷修复，尚未提交/推送。下列结果替代前面“等待整体Review/未执行本轮测试”的历史快照；不代表实机验收或旧版全部工具完全等价。

### 实际范围与代码量

| 范围 | 生产文件 | 新增 | 删除 | 净增 |
|---|---:|---:|---:|---:|
| Backend | 14 | 1304 | 400 | 904 |
| Frontend | 4 | 141 | 6 | 135 |
| Algorithm | 10 | 462 | 150 | 312 |
| 总计 | 28 | 1907 | 556 | 1351 |

0 新生产文件/服务/依赖/数据库表；预算900–1400内。算法身份传递本身净增20行，其余主要是实际工具派发、批准等待/句柄、Core转发和防止普通数据源/内部产物旁路的兼容校验。整个授权阶段相对7e04900e累计净增2301行，非本轮单独新增。

- 复用现有ToolManager dispatch_selector、ToolConfig、真实实例索引、Core HTTP client；不改LazyLLM/不开trusted/不动态打补丁。真实调用nonce+索引+provider摘要，Core请求加不可变发起时间，记录到期后旧句柄不能重新授权。
- Core保持业务权威；主/普通子/Workflow身份与lease复核，grant/binding/运行状态使用既有行锁协调；文件提交前再复核期限和资格。既有槽位SetNX限制16未完成请求，回执24h且无内容，失败未知不重放。
- 文件操作包含读取、新建、覆盖、精确修改、追加、单文件删除、mkdir、ls/glob/grep/info；Core授权后读内容，敏感grep显式跳过。使用打开目录句柄身份、拒绝链接/别名、20MiB文本限制、保留文件权限、无覆盖create、精确计数及CRLF规范化；**不宣称对外部编辑器提供原子CAS**。
- 旧提示词ask_user权限分支/子任务只能转述已由真实批准通路替代。前端批准≠完成，支持刷新、切会话、隐藏/卸载、uncertain与Core回执。

### 验证结果与限制

| 检查 | 本次结果 |
|---|---|
| Core `go test ./... -count=1` | 83包通过，5包无测试，退出0 |
| Core `go vet ./...` | 通过 |
| Core `go test -race ./localworkspace -count=1 -json` | 69叶子用例通过、0失败 |
| 主/子/Workflow身份及交错race矩阵 | chat/subagent/workflow通过 |
| Windows amd64 / Linux amd64 `go build ./localworkspace` | 交叉编译通过；不是平台执行证据 |
| 算法7文件标准pytest矩阵 | 最终129通过；13个既有HTTPX弃用警告 |
| Workflow remote executor既有20项 | 临时协程适配下20通过；标准pytest因缺pytest-asyncio有17环境失败，未改依赖 |
| 前端6文件Vitest | 68/68通过 |
| 修改前端文件ESLint、定向tsc、生产构建 | 通过；保留原资源/dynamic import/chunk等警告 |
| 全前端tsc | 未通过，662行诊断在未修改文件；修改文件零诊断，未顺手修复 |
| 冻结检查 | local/desktop对ec4676e0无差异，LazyLLM/gitlink对245bc26d无差异且子模块干净 |

首次全Core的catalog失败是本次新错误文字回归，已复用既有错误并重跑通过；新增测试fixture签名/时间戳/对话框选择器引起的运行异常已纠正。旧filepath.Rel/os.Rename字面合同删除1项9行，保留并增强真实路径/版本/磁盘/并发测试。未把旧历史通过数算作本次通过。

未验证：真实模型同轮读建改追加删、浏览器+真实Core登录批准联调、Redis/PostgreSQL跨进程并发、Local与打包Desktop实机、Windows/Linux实际文件执行和任意外部并发编辑。测试边界包括Core真实SQLite/磁盘及算法真实manager+HTTP mock，不能替代以上验收。

### 仍然明确受限的工具兼容性

下列工具只在绑定工作区的运行中拒绝，普通未绑定任务保持原行为。原因是现有工具存在尚未证明受控的宿主路径/额外副作用，**不是宣称这些工具均不安全**：

- Writer profile_resources、generate_draft_section(_markdown)、generate_draft_blocks(_markdown)、generate_draft_document_markdown；结构化generate_draft_document及已核查内部Writer方法已接入。
- ExternalDatabaseToolkit三项数据库工具；vision_extractor/image_generator/image_editor/video_generator/video_to_gif。
- vocab_learn、MemoryTools读写个性化；SkillManagementToolkit安装/编辑/删除；全部MailToolkit方法；schedule创建/读取/更新/触发；FeishuWikiFS/NotionFS/GoogleDriveFS供应商方法。
- 未登记/未知自定义工具、绑定工作区的Workflow脚本包在加载前拒绝，不以trusted或同名内置回退绕过。

已保留已核查检索工具、普通计算/编排、内部文本/JSON产物，以及task/upload范围内文件/图像产物；整批路径先按实际saver规范化后校验。混合普通源grep在打开文件前排除工作区，未绑定源继续原rg路径。不能把这些限制描述为旧版功能完全恢复。

### 下一步

最后独立Review已确认具体修复，本轮准备相关提交；保留以上兼容限制和实机待验收清单。如需恢复被拒绝的额外工具，应先按具体工具的路径与副作用核查方案，再由用户确认产品边界，不自动扩大本轮。

最终实际生产文件（相对仓库根）：

- `algorithm/lazymind/chat/api/subagent_routes.py`：+2/-0，净+2
- `algorithm/lazymind/chat/engine/agent_runtime/executor.py`：+2/-0，净+2
- `algorithm/lazymind/chat/engine/agent_runtime/models.py`：+1/-0，净+1
- `algorithm/lazymind/chat/engine/agent_runtime/tool_call_guard.py`：+71/-31，净+40
- `algorithm/lazymind/chat/engine/subagent/runner.py`：+11/-3，净+8
- `algorithm/lazymind/chat/engine/tools/local_fs.py`：+215/-115，净+100
- `algorithm/lazymind/chat/service/chat_request.py`：+1/-0，净+1
- `algorithm/lazymind/chat/service/chat_service.py`：+4/-0，净+4
- `algorithm/lazymind/chat/service/component/tool_registry.py`：+150/-1，净+149
- `algorithm/lazymind/chat/workflow/remote_executor.py`：+5/-0，净+5
- `backend/core/chat/chat.go`：+4/-0，净+4
- `backend/core/chat/conversation_logic.go`：+48/-10，净+38
- `backend/core/chat/redis_cache.go`：+7/-4，净+3
- `backend/core/chat/run_decision.go`：+65/-15，净+50
- `backend/core/localworkspace/approvals.go`：+134/-33，净+101
- `backend/core/localworkspace/context.go`：+7/-13，净-6
- `backend/core/localworkspace/directory_identity_unix.go`：+13/-0，净+13
- `backend/core/localworkspace/directory_identity_windows.go`：+14/-0，净+14
- `backend/core/localworkspace/lifecycle.go`：+124/-0，净+124
- `backend/core/localworkspace/operations.go`：+707/-242，净+465
- `backend/core/main.go`：+6/-0，净+6
- `backend/core/routes.go`：+1/-0，净+1
- `backend/core/subagent/runner.go`：+169/-83，净+86
- `backend/core/workflow/store.go`：+5/-0，净+5
- `frontend/src/i18n/locales/en-US.ts`：+11/-1，净+10
- `frontend/src/i18n/locales/zh-CN.ts`：+11/-1，净+10
- `frontend/src/modules/chat/components/ChatInput/LocalWorkspaceControl.tsx`：+94/-4，净+90
- `frontend/src/modules/chat/utils/localWorkspace.ts`：+25/-0，净+25

### 本次可复现命令（仓库根）

```sh
PYTHONPATH="$PWD/algorithm:$PWD/algorithm/lazyllm" .venv/bin/python -m pytest \
  tests/algorithm/chat/test_local_fs_tool.py tests/algorithm/chat/test_tool_call_guard.py \
  tests/algorithm/chat/test_agent_executor.py tests/algorithm/chat/test_tool_registry.py \
  tests/algorithm/chat/test_core_api_client.py tests/algorithm/chat/test_workspace_authorization_contract.py \
  tests/algorithm/chat/test_subagent_runner.py -q
```

当前环境没有pytest-asyncio；以下命令只为执行仓库既有协程测试，无新增仓库探针/依赖，不能假定其他机器存在历史临时文件：

```sh
PYTHONPATH="$PWD/algorithm:$PWD/algorithm/lazyllm" .venv/bin/python - <<'PYTEST'
import asyncio
import inspect
import lazyllm
import pytest
lazyllm.LOG.info('workflow coroutine regression')
class ExistingCoroutineTests:
    def pytest_configure(self, config):
        config.addinivalue_line('markers', 'asyncio: coroutine test run with asyncio.run')
    @pytest.hookimpl(tryfirst=True)
    def pytest_pyfunc_call(self, pyfuncitem):
        if inspect.iscoroutinefunction(pyfuncitem.obj):
            arguments = {name: pyfuncitem.funcargs[name]
                         for name in inspect.signature(pyfuncitem.obj).parameters}
            asyncio.run(pyfuncitem.obj(**arguments))
            return True
raise SystemExit(pytest.main(
    ['algorithm/tests/chat/workflows/test_remote_executor.py', '-q', '--disable-warnings'],
    plugins=[ExistingCoroutineTests()]))
PYTEST
```

Core命令（backend/core工作目录）：

```sh
go test ./... -count=1
go vet ./...
go test -race ./localworkspace -count=1 -json
go test -race ./chat ./subagent ./workflow -run '^Test(Workspace(Main|Workflow|Subagent)|DismissSessionWaitsForWorkspace)' -count=1
GOOS=windows GOARCH=amd64 go build ./localworkspace
GOOS=linux GOARCH=amd64 go build ./localworkspace
```

前端命令（frontend工作目录）：

```sh
NODE_OPTIONS=--no-experimental-webstorage pnpm exec vitest run \
 src/modules/chat/components/ChatInput/LocalWorkspaceControl.test.tsx \
 src/modules/chat/components/ChatInput/LocalWorkspace.contract.test.ts \
 src/modules/chat/components/ChatInput/index.test.tsx \
 src/modules/chat/utils/localWorkspace.test.ts \
 src/modules/chat/components/AskCard/index.test.tsx src/runtime/desktopBridge.test.ts
pnpm exec tsc -p tsconfig.mcp.json --noEmit
NODE_OPTIONS=--no-experimental-webstorage pnpm run build
```

最终独立Review：此前三个具体问题（混合源grep先读后过滤、产物路径空白规范化、递归glob）修复并回看通过；Core句柄/时效/回执/提交校验专项无critical/important发现。该Review范围不表示上方限制的工具或真实平台已验收。

提交前最后验收对照：Core补充Workflow固定版本compiled_graph当前step必须声明local_fs，未声明/无版本则拒绝；新增真实版本fixture和未声明拒绝合同通过。该增量纳入上表，仍无新生产文件；执行时的Python声明检查不再是唯一约束。

最终提交检查（2026-09-09）：Workflow声明校验独立Review无新增问题；全Core83包/5无测试与全vet最新重跑通过；根agent再次执行算法标准129项与前端六文件68项通过，diff检查/冻结检查通过。最终生产28文件+1907/-556净1351（算法312）；所有变更均本功能相关。提交后继续保留实机与兼容限制，不把本批提交称为完整产品验收。

## 2026-09-10 非人工工作收尾：执行中

用户要求完成除人工验收外的其他工作，覆盖自动联调、可自动验证的数据库环境、受限工具兼容核查/补齐、工程检查及远端交付。起点752a6a76、工作区干净；fetch确认远端无新提交，本地领先9个。只用现有仓库，不改Local/Desktop或LazyLLM。沿用每批约200净增/新文件门槛，发现超量或产品/安全规则改变时先报告具体差异再Review；旧1400预算不能作为任意工具恢复的无限授权。

- [ ] 核对前端完整tsc真实错误及根因，制定最小正确修复范围。
- [ ] 核对剩余受限工具，区分可复用安全路径与必须改变授权/文件访问设计的部分。
- [ ] 补现有pytest-asyncio测试依赖到本机venv，用标准命令运行Workflow协程测试，取消临时适配依赖。
- [ ] 启动隔离临时Redis/PostgreSQL验证现有Core行为，测试落现有测试文件，可复现且不碰用户库。
- [ ] 自动Core↔算法HTTP/批准链路联调，测试资源隔离；实际模型/运行态需要核实可用配置，不能用mock冒充。
- [ ] 汇总验证/限制/实际文件代码量，提交相关改动并安全同步远端。

本机已有Local服务进程启动于9月7日（旧二进制），不能直接当作752a6a76运行证据。存在redis-server/PostgreSQL命令；.venv有pytest9.1.1但没有pip和pytest-asyncio，使用已有uv安装仓库已声明测试依赖，不修改生产依赖。当前派发独立只读兼容/类型排查和数据库自动验证；主任务处理环境与跨语言联调。


## 2026-09-10 继续执行记录（非人工部分）

本次继续工作完成了邮件附件边界收紧、测试夹具隔离和自动化回归。新增生产改动仅在算法既有文件：

- `algorithm/lazymind/chat/engine/tools/local_file/workspace.py`：绑定 Core 工作区时，即使历史 `trusted_local_mode` 已开启，内部产物与附件路径仍使用工作区根；兼容子任务的 `parent_agentic_config` 身份。
- `algorithm/lazymind/chat/engine/tools/mail.py`：草稿/附件输出先经过同一工作区 resolver；发送旧草稿前重新校验持久化附件，避免历史绝对路径越界；不改变邮箱确认卡和发送流程。
- `tests/algorithm/chat/test_mail_toolkit.py`：新增绑定工作区、历史附件、已有外部符号链接的边界覆盖。
- `tests/algorithm/chat/test_workspace_authorization_contract.py`：HTTP 联调无 fixture 时先跳过，异常时先取消后台调用，避免测试退出等待挂死。
- `backend/core/chat/run_decision_test.go`：HTTP fixture 的子任务、Workflow revision/session/attempt 使用会话唯一前缀并清理专属 Redis key；并发审批仅接受成功或预期冲突。

本批生产增量为 25 行净增（`workspace.py` 13、`mail.py` 5、既有 registry 13/-1；按文件实际 diff 统计），测试增量 174 行左右；没有新增生产文件、依赖、服务或表，也未修改 Local/Desktop、`algorithm/lazyllm` 或 gitlink。

本次新鲜验证：

- Python 重点集合：`174 passed, 1 skipped, 13 warnings`；skip 是未提供真实 HTTP fixture 的可选测试。
- Core 全量：`go test ./... -count=1` 通过；重点包 `chat/localworkspace/subagent/workflow` 通过。
- Core↔算法真实 HTTP：`TestWorkspacePythonCoreHTTP` 的 main、subagent、workflow 三个叶子均通过（SQLite 临时数据库、临时目录，不调用模型或登录服务）。
- 前端工作区用例：6 个文件、68 tests 通过；生产构建通过。
- 前端完整 `tsc --noEmit` 仍有仓库既有错误（canonical 依赖后约 573 项，集中在旧生成客户端和非工作区页面）；`check:error-prompts` 仍有既有全局违规。没有用排除配置掩盖。
- PostgreSQL+Redis 专属临时实例验证已重新运行并通过：localworkspace 全套通过，chat 的 main identity、workflow identity、并发 prepare/approve/execute、16 槽容量、撤销围栏和跨进程单次消费均通过；实例和数据目录已销毁，未连接用户数据库。

仍未宣称完成的自动化/运行态边界：真实登录、模型驱动的主流式/非流式/双回复身份注册、真实 FastAPI 子任务启动、Windows/打包 Desktop、用户目录选择和跨平台符号链接/外部编辑器并发。这些属于人工或平台验收，不能由当前 HTTP fixture 代替。工作区文件二进制传输、任意脚本/shell/MCP 宿主执行以及旧 Writer/media 全量注册仍保持拒绝或待专项设计；没有通过提示词或 trusted 绕过。


本批冻结记录（提交 `a2f1d558c3d70253450a1fa7b299f660e31dc966`，冻结基线：Local/Desktop `ec4676e0d0fb290d81b3160a56e798849ea2d4e4`，LazyLLM `2e3d00ac3ae4ae983cb7d0ca4bd231c1a56ebfc0`）：涉及文件 SHA-256 已核对为 `workspace.py=056b0f112c3e5f11528baa9cc5bca141a08fa5b4cf5aa60cddf8db100aa027c8`、`mail.py=5217a4e8de2e5599a1477e83e4b6ab0b34b8c56321642073c34f668e2d326da2`、`tool_registry.py=cab5e28bf6d11511a6a6079bb9264e41a6fbfcc59d470fb5aa6d8994db82d4f5`、`run_decision_test.go=cb9b186804243e49383c5289424910260bd9a110a42129ad4330bddd52566619`、`test_mail_toolkit.py=5be3d5c188d391a0ecc0b5de37c84039ec9defc96ca29cb4a7893bc92160c732`、`test_tool_registry.py=4726ca2171481bae9c89bf3a33181472eff75f753b2ebee618dd1e0c0e3d0295`、`test_workspace_authorization_contract.py=03600fc7a0033a6691231624d07145e78b63e59c04e3ea94254ef755f62a8b0d`。
