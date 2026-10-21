# cst/product_prs：公共控制层与工作流业务边界 Review

> 历史审查记录：反映当时提交的问题和测试。后续修复、当前状态及统一测试步骤见 [问题、修复记录与测试总表](product-prs-review-fixes-and-tests.md)。
## 结论与范围

审查基线：`upstream/main@9cbc57c5deb3b5fdded71c6b051041c1bff3aa30`；审查分支：`cst/product_prs@b0962fbbc2e8f6403b1924e191eee99a111d5949`。覆盖增量中的 40 个非测试 Python/Go/前端源文件，并追踪对应工作流脚本、包声明和 main 现有机制。未以 CI 状态作为依据。

**当前分支存在 12 组需要处理的架构边界问题，不能据此前 R1–R7 的行为修复就判断可合入。** 产品工作流的业务规则已经进入通用工具注入、执行策略、产物事务、图输入绑定、编辑存储、会话创建和共享 UI。PPT 也在 manager 中改写控制输入。问题不只是文件名含有 product，而是公共层实际按 workflow ID、业务步骤名、材料名和阶段表执行不同的控制协议。

本轮只做 Review 和行为探针，以下问题尚未完成重构，不对问题分级。不能用“加 if 后其他工作流不受影响”或“把代码挪到 product_policy.py”代替修复；公共层继续导入并解释该业务模块，耦合仍然存在。

前轮由我添加的 `product_tools` 注入限制、产品 UI 条件分支保留、非产品事件流绕过包装，只缩小了影响范围，没有解除架构耦合。本轮将这些保留项一并纳入问题。

## A01：通用 manager 为 PPT 改写当前用户输入

位置：`algorithm/lazymind/chat/workflow/workflow_manager.py:92`，调用点 `:203`、`:444`。

`_ppt_step_user_input` 在普通推进和 handoff 推进前识别 `ppt-workflow`，把“继续”转为空串，利用 Go 的空输入回填启动需求机制。相同包复制为其他 workflow ID 就不再得到同样处理。控制指令与内容修改的区分由通用 manager 内的名称白名单决定。

处理方向：推进协议明确区分执行动作与当前内容要求，或由 PPT 自己消费原始/当前输入；manager 原样转发通用字段。不能把白名单扩展成每种工作流一条分支，也不能简单全局清空“继续”而改变 Writer 现有行为。

## A02：manager 和公共 SDK 承担产品阶段控制及会话重定向

位置：`workflow_manager.py:347`、`:503`、`:519`、`:1633`、`:1659`、`:1670`；`algorithm/lazymind/workflow_sdk/client.py:224`。

manager 定义产品阶段查询、历史阶段产物读取、阶段切换，保存产品专用幂等命令，并写 `product_stage_relay/workflow_session_id`；通用 session 工具和 handoff 再通过 `_relayed_session_id` 隐式替换 session。SDK 同时添加产品专用方法，通用工具组接口增加 `product_tools` 开关。

影响：通用 session 解析不再只由绑定的 session 决定；产品阶段控制的状态保存在公共 agentic_config 中。`_relayed_session_id` 本身只检查 source session 匹配，不检查当前 workflow 类型。探针证实即使配置标为 Writer，只要人为设置同 source session 的 relay，它仍重定向；这说明缺少协议约束，并不等同于已经复现真实用户跨会话串数据。

处理方向：产品阶段工具放入业务扩展，通过正式的通用工具注册和会话切换返回协议调用；公共 manager/SDK 不认识产品阶段名和产品命令字典。业务后端 API 可以存在，但不能以公共 WorkflowClient 内的特设方法和通用 session 隐式重写来承载。

## A03：触发器按产品名称硬编码输入可见性与所有权

位置：`workflow_manager.py:1179`、`:1285`。

`_trigger_input_types` 仅为产品隐藏 `workspace_seed`、`stage_approval`、`upstream_*`；启动时再次按产品 ID 拒绝未公开字段。换一个 ID、保持相同输入契约时，白名单行为立即不同。材料是谁能填写、哪些由 Host 提供，是输入契约/权限问题，不应由 manager 猜材料名称。

处理方向：工作流声明受支持的输入来源和可见范围，编译器校验，Host 与触发器统一执行。不能直接拿 UI 的 `exposed` 替代完整的输入授权语义，也不能删除现在的检查后把可信绑定开放给模型。

## A04：通用 SubAgent runner 按产品步骤名控制轮数、工具和失败

位置：`algorithm/lazymind/chat/engine/subagent/runner.py:955`、`:1384`、`:1487`、`:1546`；`algorithm/lazymind/chat/workflow/product_policy.py:27`、`:33`、`:132`。

公共 runner 导入产品策略，以固定 workflow ID、`workspace_state/stage_manifest` 和 `route_*`、`*_outline`、`finalize_product_delivery` 等步骤名决定 builtin tools、工具列表、调用配额、轮数、重试和终止错误。比如 route 步骤固定 5 轮/120 秒，部分发布工具失败会直接触发产品 fail-fast。

影响：包内改步骤名就改变执行语义；旧 revision 的执行策略随宿主代码发布而变化，策略未完整固定在包 revision 中。工具失败是否终止步骤由宿主硬编码的业务工具名决定，与本轮指出的“工具错误不应任意升级成流程错误”属于同一类边界问题。

处理方向：工具可见性优先复用已有 `tools_only`、`terminal_tools_only` 和声明式工具契约；确需新增执行预算/错误策略时，设计有边界、可验证、随包固定的通用字段。`AgentExecutionOptions.expanded_round_limit` 本身可作为通用执行参数保留，问题是由产品名称白名单赋值。

## A05：远程执行器提前解释产品业务输入并可能直接终止执行

位置：`algorithm/lazymind/chat/workflow/remote_executor.py:173`；`product_policy.py:86`。

`normalize_bound_inputs` 在执行模型/工作流工具之前，翻译 `execution_depth/reference_sample_choice` 别名、决定样例是否提供、校验 `word_target >= 300`，再改写执行参数中的 `remote_inputs`。这些规则工作流脚本也有。探针以产品 PRD `word_target=200` 调用，得到 `PRODUCT_INPUT_INVALID`；外层异常处理把它变成执行错误，工作流自己的工具没有机会处理或解释输入。

处理方向：业务值转换、默认值和业务约束下沉到产品输入工具；公共执行器只负责传输类型、绑定完整性等通用检查。若平台提供声明式输入 schema，错误应按明确协议返回，不按产品字段名分支。

## A06：公共远程执行器为产品改工作目录和错误协议

位置：`remote_executor.py:93`、`:236`、`:295`；`product_policy.py:168`。

产品执行被重定向到 conversation workspace 下 `product-workflow/<task hash>`，其他 workflow 使用 execution spec 原工作目录；恢复还取决于该特殊目录是否存在。HTTP 错误体的提取也只对产品启用，超时则来自产品步骤表。

影响：同一远程执行协议的路径选择、恢复和报错取决于 workflow ID，控制端提供的 workspace 并非所有工作流共同的权威位置。

处理方向：工作目录及允许使用的共享文件能力由通用 execution spec/Host workspace 协议确定；业务不自行覆盖。公共 HTTP 错误解码应按统一错误 envelope 处理。任务哈希和路径边界检查值得保留，但应进入通用能力而非产品专属分支。

## A07：产物写入和 Attempt 完成事务实现第二套产品发布协议

位置：`backend/core/workflow/executor/artifact_sink.go:142`、`:233`、`:268`；`backend/core/workflow/attempt/service.go:410`；`attempt/product_outputs.go:13`、`:33`；`productstate/publication.go:21`、`:134`。

同一 `DBArtifactSink.Save` 对产品暂不替换 selected 产物，暂缓消费者失效；普通工作流走原路径。通用 `Terminal` 固定调用产品完成函数，成功后再选取产物、失效旧消费者、更新版本。遇到 `finalize_product_delivery`，事务进一步校验产品 Workspace/Manifest、七个业务阶段及各阶段正文/HTML 配对，写 `product.published`。

影响：不是普通业务工具保存一个文件，而是业务代码决定通用产物可见性和 Attempt 能否结束。名称或 slot 改动会影响提交时序和成功判定。业务发布校验报错还会回滚通用终态事务。

处理方向：原子输出、不可变 manifest、成功提交/失败回滚若是必要能力，应成为统一产物协议；产品“哪个阶段需要哪些正文”放在产品验证器。main 已有 artifactgraph、artifactfile、controlstore 的 manifest/封存等构件，需核对后复用或扩展，不能声称它们已经完整替代当前产品实现。

## A08：通用图推进在求值后替换产品输入绑定

位置：`backend/core/workflow/transition_handlers.go:812`；`backend/core/workflow/executor/remote_handler.go:195`。

产品 finalizer 从 `productstate.Latest` 取 `workspace_state`，跳过图求值得到的 `workspace_seed` witness，另写一条绑定。远程输入读取又识别 `workspace_seed <- workspace_state`，走产品专用反序列化路径。

影响：仅查看 workflow.yaml/state.yml 和编译图，不能解释该步骤最终读到的输入来源。公共推进器必须知道业务步骤和材料别名，图求值结果不再是唯一依据。

处理方向：将需要的输出到输入关联显式建模在图或通用绑定协议中，编译/求值/冻结使用同一个来源。JSON 文件内联和输入别名解析应通用化；不能在落库前按业务名覆盖 witness。

## A09：人工编辑的存储和 copy-on-write 由产品身份决定

位置：`backend/core/workflow/store.go:1303`、`:1638`；`backend/core/workflow/store/repository.go:359`；`productstate/snapshot.go:13`。

两个通用编辑入口都调用 `productstate.SnapshotEdit`；产品强制文件快照、强制 copy-on-write，即使之前就是 human revision。普通工作流按原规则处理，controlled workflow 又有既有封存规则。

影响：同种文件/同种编辑操作的 revision 和文件生命周期由 workflow ID 决定；平台修复保存/历史版本问题时需要兼顾多套规则。新增 snapshot helper 还让所有调用先查 session，因此不能只凭 helper 内的 ID guard 就断言公共调用链完全不变。

处理方向：通过通用封存/编辑策略决定是否复制，统一使用 artifactfile 和已有 sealed revision 机制；产品不应被通用 Store 特判。迁移时要保留 Writer 当前保存、草稿版本和冲突恢复行为。

## A10：公共 Repository 内重复实现产品领域模型，默认值已经冲突

位置：`backend/core/workflow/store/product_relay.go:28`、`:654`、`:791`；`store/product_project.go:295`、`:345`；`workflows/product_solution_delivery/scripts/tools.py:1865`。

Go Repository 维护产品阶段、slot 对照、阶段偏好、默认字数、确认/暂缓、workspace 与发布历史；产品脚本同时维护阶段、执行计划和业务校验。两边存在实际不一致：

| 阶段 | 工作流直接启动的默认字数 | Go 切换阶段补入的默认字数 |
|---|---:|---:|
| direction | 1,400 | 1,800 |
| design | 3,500 | 5,000 |
| prd | 4,500 | 6,000 |
| review | 1,800 | 3,000 |
| handoff | 3,500 | 5,000 |

Go 在缺少 carried preference 时把这些值导入为输入资源，后续 Python 会把收到的值当作显式输入，而非自身默认值。因此在没有旧偏好的条件下，同样的“默认 PRD”，启动入口不同就会拿到不同篇幅。已实际调用五个阶段的 `validate_product_route` 确认左列，右列核对 Go 函数与其绑定调用链。

处理方向：默认值和产品业务语义必须只有一个权威来源，优先工作流包/领域模块。身份校验、CAS、幂等、可信用户操作来源这些平台职责应保留为通用能力；不能为消除重复而把可信决定交给模型自行批准。

## A11：产品阶段控制自行选最新包并硬编码 Ready 步骤

位置：`backend/core/workflow/store/product_relay.go:740`、`:943`、`:947`；前端 `ProductProject.tsx` 的 relay。

`RelayProductStage` 读取 revision 参数为空的最新 package，创建新 session，然后返回固定 `ready_steps=[route_product_stage]`。当前阶段的固定 revision 和编译器实际算出的初始 Ready 集合没有成为完整的后续阶段契约。

影响：阶段之间发布新包时，下一阶段会隐式切到新 revision；新包若改了入口步骤，Host 仍声称旧步骤 Ready。固定入口这部分可由源码直接确认，尚未运行真实包升级的端到端重现。

处理方向：阶段衔接、revision 选择和升级应有明确通用协议；Ready 集合从所选包的实际 projection 返回。产品业务只提交需要进入哪个阶段的意图，不在通用 Repository 硬编码入口节点。

## A12：共享 UI 按产品 ID 覆盖包声明和任务展示

位置：`frontend/src/modules/chat/components/WorkflowPanel/index.tsx:1852`、`:1937`、`:2313`；`productPresentation.ts:5`；`TaskCenter/index.tsx:1078`、`:1164`、`:1203`、`:1287`；`pages/chatLayout/index.tsx:400`。

公共面板对产品删除 `_outline_report` slot、重置 layout、合并下载入口、挂载 ProductProject；通用 TaskCenter 改用产品进度组件并隐藏普通最终产物；聊天布局将产品任务数量压到最多 1。产品组件还自带阶段列表和具体控制 API。

影响：包声明的 UI 不能独立决定实际展示；复制同一包换 ID 会出现不同界面。仅更新包的 slot、阶段或展示需求不足以更新客户端行为。此前恢复 Writer 编辑器并未消除这些共享 UI 分支。

处理方向：使用正式、受约束的通用 presentation/control adapter 或声明式组件能力，业务组件由扩展注册并在业务边界内处理。仅在 registry 里写另一份 workflow ID 白名单不算解决。任务计数、下载聚合、slot 可见性应由通用展示数据驱动。

## 已核对但不应一概删除的共享改动

- `run_image_model_instance` 接受具体 model 并归一化输出，没有按 workflow ID 改控制行为；供应商选择仍留在图片包工具内。这是可复用工具能力，不属于上述控制层特例。原包装函数提前构造 AutoModel 的兼容性需在后续变更时留意，不能仅据“改了公共文件”判定越界。
- `image_workflow_support.py` 是 V1 搜索/校验/字幕的公共复用实现，调用方向为业务工具调用公共能力，没有让 manager/runner 调用 V1/V2 业务分支。
- `launch_user_input/current_user_input` 是通用附加上下文字段，未按图片 ID 改写原 `user_input`；`image_request.py` 由图片工具调用。与 manager 为 PPT 清空输入是不同的边界。
- PPT renderer 的多元素选择依据已有 `ppt_html` 内容类型，不是 workflow ID。内容类型的专用渲染不等于控制器为某个包改推进语义；仍需维持 Markdown/IR 分支原行为。
- `TempUserFilesRoot`、UUID/CAS、输入资源和文件快照等通用原语可以保留。路由表注册领域 API 本身也不是问题，问题是业务 API 的实现嵌入通用 Repository 并改写公共生命周期。
- main 已有 `RuntimePolicy` 明确要求 host-neutral、避免具体 workflow ID 分支；已有工具可见性、post-step checks、controlled review manifest 等机制。新增能力要先比较这些机制，不能再建同义的产品专用控制链。

## 整改边界与顺序

1. 先确定输入、工具注入、会话衔接、执行预算、发布及编辑需要哪些通用契约；缺少的能力单独设计，不伪装成产品补丁。
2. 将产品阶段/默认值/输入别名/正文配对等业务规则移回包或独立领域扩展，消除多语言重复权威。把确认身份、授权、CAS 和幂等保留在可信 Host。
3. 统一产物发布、输入冻结和编辑语义，再移除公共事务中的 `productstate` 分支；不能直接删除导致现有历史产物或草稿丢失。
4. manager 恢复通用转发/注册职责；共享 UI 通过正式 adapter 获取展示与操作描述。最后检查具体工作流名称和业务步骤名是否仍出现在公共决策中。

## 测试与证据

本轮执行了读取实际源函数的本地探针，没有调用模型、搜索服务或修改业务数据；探针原始结果保存在本机 `/tmp/product-prs-architecture-probes.json`。读取/AST 提取的函数保持原样，只替换外围依赖，不能等同于完整集成测试。

已验证：

- 相同运行声明下，产品 ID 的 route 策略为 5 轮/120 秒；只改 workflow ID 或 route 步骤名，策略返回 None。
- 相同输入字段，产品 ID 只公开 product_goal；换 ID 后 workspace_seed、stage_approval、upstream_design 被保留。
- 同一“继续”输入，PPT ID 返回空串，复制包 ID 返回原文。
- 配置当前 workflow 为 Writer、构造同 source session 的产品 relay 时，通用 session helper 仍重定向；这是函数边界证明，不是实际用户串会话复现。
- `word_target=200` 被公共执行前置规范化拒绝，尚未进入产品工具。
- 实际调用产品路线验证器确认五个阶段的默认字数，与 Go relay 默认表全部不同。

整改后必须补足的行为验收：

| 测试 | 预期 |
|---|---|
| 同包换 workflow ID | 输入权限、工具列表、执行预算、产物可见性和 UI 能力不因名称改变 |
| 同能力步骤改名 | 策略来自声明，不因 route/outline/finalize 字符串失效 |
| 旧 revision + 新宿主 | 保持旧 revision 的约定；升级遵循明确协议 |
| 默认参数直接启动与阶段切换 | 无用户覆盖时篇幅、参考样例和执行深度一致 |
| 多入口推进 | 普通/hand-off/浏览器操作使用同一 session 和控制契约，不读业务专用重定向字典 |
| 工具错误/输入错误 | 按通用契约保留可恢复错误；只有定义明确的终止条件结束步骤 |
| 发布中失败、重试、取消 | 所有声明同一能力的工作流具有一致的原子性、历史版本和消费者失效行为 |
| 图输入冻结 | projection witness、持久化绑定和远程实际读取来源一致 |
| 人工编辑与发布封存 | copy-on-write 依据封存/编辑契约，旧产物不变；Writer 原有保存与冲突恢复保留 |
| UI 与编译包升级 | slot 可见性、任务计数、操作入口来自固定声明/adapter，不依赖产品步骤或 slot 后缀 |

现有行为测试通过只说明已覆盖路径运行正常，不证明这些架构边界已满足。上述 12 组问题均须处理后再复审。
