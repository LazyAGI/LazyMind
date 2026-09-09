# 当前差异证据

> 2026-09-09 最新：用户确认的R3–R7生产及统一自动化测试已完成，结果/实际代码量/兼容限制见末尾“最终生产/自动化结果”；实际运行验收仍未完成。开发起点7bc81ffa，本轮相关代码与文档同批提交，未推送远端。

## 最新：剩余整体接入源码证据（2026-09-09）

- 用户已改为完整生产后统一测试；本轮只读代码和更新四份文档，不继承旧通过数。起点 69e4809e，工作区干净；本轮生产/测试代码净增 0。
- ToolManager 的 PreparedToolCall 在 dispatch_selector 可读真实 call_id/validated_arguments，但下层 callable 只收到参数，且 diverter 会并行执行；不能用共享参数哈希队列回填调用身份。tools_info 可读实际 MethodModuleTool 的实例/方法，现有 CitationResultMiddleware 已使用同一路径识别工具，项目层可以复用，无需改 LazyLLM。
- Core prepare 当前每次 newID；local_fs call_id 是参数哈希，run/task/attempt 没有完整传递。仅缓存哈希会合并独立调用，必须同时补真实执行代次/调用句柄与参数摘要。
- 主流式 run_id 由 conversation_logic.go 在发算法请求前生成，ChatStatus 先写 generating/run_id；新 ChatHistory 可能在流式中途或终态才创建。Core runDecision 是比状态终结更早的取消屏障；非流式目前未登记同样的活动状态。用 ChatHistory 存在性判定会误拒绝正常新任务。
- 普通子任务 SubAgentTask 没有 run/generation/lease 字段，恢复复用同 task_id；状态 running 不能拒绝恢复前旧 runner。现有 RunRequest → API → runner 可传 Core 生成的执行代次，持久化复用既有 Params/state。子任务 detached background 启动，不能把主轮正常结束当作子任务自动失效条件。
- WorkflowSessionStep 已有 lease_token/fencing_generation/lease_expires_at，attempt.Service.ValidateLease 检查活跃租约；remote_executor._run_claim 已持有真实 lease，但没有传到 run_subagent_stream。authorizeWorkflowExecutorTask 为了终态回传允许 terminal，不能复用成文件操作授权。
- localworkspace 被 chat/subagent 导入，反向 import 会循环；主运行校验应复用现有 lifecycle callback 风格在 main 装配，避免复制 chat 的缓存 DTO/key。
- 工具现有 ls/glob/grep/info 仍直接本地访问；Core resolveOperation 在权限决定前计算文件哈希；原字符串路径解析后重新按路径访问存在竞态。前端已有控件/API/request-id，但无 pending 列表；不可把现有基础设施当成缺失业务已经可用。
- 整个授权阶段按 Git 重算生产 +957/-7，净增 950（含 A1）；剩余预计净增 900–1,400，累计 1,850–2,350，约 30 既有文件、目标 0 新生产文件，触发用户原超量 Review，详细不可复用原因见方案第 14 节。
- 只读 agent 给出了 main/ordinary subagent/Workflow 具体调用链和上述身份缺口；不是完整方案验收或测试通过。检索中三个候选路径不存在（subagent/dispatch.go、chat/routers/*、infra/citation_middleware.py），已用 rg 找到 runner.go、chat/api/subagent_routes.py、infra/tool_result_citations.py；没有执行失败的产品测试。

## 最新：A2-R2 实现与验证（2026-09-09）

- 用户已批准一次性消费方案。两份既有生产文件实际 +30/-35，净减少 5 行：24 小时 SetNX 标记替代 2 分钟互斥锁；有效期仍为 5 分钟，标记不会在状态错误时删除；错误身份/参数/action 在消费前拒绝，领取后重读状态。completed 重复请求返回保存回执，不重复读写。
- 删除无调用者的 claimOperation/releaseOperation 与其中 Get+Del 分支；删除 14 行、1 项只检查字面的旧源码测试，其余已有测试保留。无新增生产文件/依赖/表/服务。
- 本次在生产修改前补充状态 Set 失败的 2 项合同（+56 行）：原实现决定/执行均可再次领取，执行重试实际创建了文件；已复现 RED 后修复。原 9 项加补充 2 项全部通过。
- 本次 `go test -race ./localworkspace -count=1 -json`、`go test ./chat ./subagent -count=1`、`go vet ./localworkspace` 通过，Local/Desktop、LazyLLM/gitlink 未变化。读状态后的完整过期/崩溃/uncertain、prepare 幂等、运行身份及实机仍未覆盖，保留原未完成项。
- 一次 gofmt 使用了相对仓库根的路径但工作目录为 backend/core，命令报路径不存在；改为 localworkspace/operations_test.go 后成功，不是产品失败。

- 本批只读 agent 审查无 critical/important 问题，已移除新测试包装器无用的 CompareAndDelete 接口依赖。新增错误测试只覆盖执行前/决定时状态 Set 失败；领取后 Get 和完成回执 Set 失败尚无注入测试，不能以本批结果宣称完整崩溃恢复。

### 修复前领取过期行为证据（1542434e）

- `operationLockTTL` 是 2 分钟，小于 operation 的 5 分钟有效期。执行/决定在锁后读取状态，但普通状态 Set 不受锁持有权约束；读取快照后停顿超过锁期限，后来的请求能够重新领取并写入，原请求仍可写回旧快照。
- 新增 claimSnapshotStore 仅在测试的 State 边界模拟领取记录过去 3 分钟，并在已读取快照与返回之间插入第二请求。执行合同实测两次追加成功（排除仅返回 completed 回执的情况）；决定合同实测 allow_once/reject 两次成功。二者都是产品断言失败，不是 Go 内存 data race。
- releaseOperation 在不提供可选 CompareAndDelete 能力时调用 Get+Del。只暴露必需 Store 接口的测试包装器记录到了 execute 和 decide 的非原子 Del，各产生一项失败。SQLite/Redis 实际实现均有原子 CompareAndDelete，不能将此测试说成已复现真实 Redis 后端的 Get+Del。
- 5 项保护用例通过：执行的 owner/call_id/content 不匹配，以及决定的 owner/action 不合法，均不消耗随后合法请求的批准。一次性领取方案必须保留这些行为，不能简单删除 defer release 就结束。
- 基线通过；新合同 9 项为 5 通过/4 预期失败；完整 `go test -race ./localworkspace -count=1 -json` 为 53 通过/4 预期失败，原有 48 项通过，无 data race 报告。首次漏导入 state 导致一次测试编译异常，修正后最终异常为 0。
- 实际修改两个既有测试文件，共 +219 行；生产净增 0。拟复用 SetNX 的一次性标记代替短期互斥锁，删除释放分支，范围/期限/错误请求不消费/过期重查合同见 IMPLEMENTATION_PLAN.md 第 13 节，尚未获本批生产 Review。
- 顺带核查未修改的 prepare：每次 newID，无 call_id 对应记录复用。算法 local_fs.py 的 call_id 为操作参数哈希，而 payload 没有完整 run/task/attempt 字段。下一批必须连同真实调用身份和过期运行拒绝设计，不能把增加短期缓存当作完整幂等。

## A2-R1 修复与验证（2026-09-09）

- 用户已批准从 `cff178a1` 直接实施生产。仅 operations.go 新增 13、删除 11、净增 2 行：将状态读取移至领取锁之后；completed 统一返回回执；只有 allowed 状态可执行；敏感读取不走普通读取免询问分支。复用已有函数和类型，没有新增生产文件或重复校验。
- 本次新增四组 12 项测试全部通过，之前 5 项 RED 转绿；`go test -race ./localworkspace -count=1`、`go test ./chat ./subagent -count=1`、`go vet ./localworkspace` 全部通过。测试只调整一行顺序说明注释，断言未改。
- 范围检查确认 Local/Desktop、LazyLLM/gitlink 未变化。状态读取顺序修复仅覆盖已复现的交错；锁租期、原子释放、prepare 幂等、目录竞态及运行身份仍按主方案保持未解决。算法/前端和实机本轮未测。

### 修复前行为复现（测试提交 cff178a1）

以下为基于 `a5761633` 建立失败合同的历史记录，更正下方历史“A2 完成”判断，不继承旧测试成功作为本次验收。

- `permissionDecision` 首个分支对所有 read 返回 allowed，导致 `.env` 在 always_ask、ask_as_needed 下绕过 pending。通过实际 PrepareOperation + 临时假数据文件复现 2 项失败；普通读、示例文件和 allow_all 敏感读的 7 项兼容用例通过。
- ExecuteOperation 在 SetNX 前读取 value，领取后未重新读取。测试在 Store 边界确定性插入另一请求完成追加，再让外部编辑器恢复原内容；延迟请求重用旧快照，把追加再次写入，实际磁盘断言失败。包装器只安排交错，授权、SQLite 与文件操作均为真实实现。
- failed 状态仍保留 DecisionAllowed，而 execute 未限制只能从 allowed 状态进入。临时文件先产生版本冲突，再恢复旧内容，同 operation_id 重试成功追加，违反批准单次执行合同。
- completed 的读操作被专门排除在回执复用之外。首次读取后更改临时文件，再提交同一 ID，返回了新的文件内容。测试要求返回原有无内容回执；新读取须发起新调用。
- 新增测试复用已有 requireWorkspaceReason，对 pending 拒绝和版本/失败重试检查既有 HTTP 状态及 reason，避免把数据库或其他异常当作期望拒绝。
- 本次全包 `go test -race ./localworkspace -count=1 -json`：按叶子用例统计 43 通过、5 预期失败、0 异常失败；其中新增 12 项为 7 通过/5 失败，原有 36 项全部通过，无 data race 报告。测试代码净增 168 行，生产净增 0。
- 拟最小修复：仅 operations.go 预计净增 15–40 行，无新生产文件，使用既有状态/helper；详细验收与范围见 IMPLEMENTATION_PLAN.md 第 12 节。目录竞态、锁租期/原子解锁、prepare 幂等、运行身份与 unknown/uncertain 等仍未覆盖，不能以这 12 项代替完整安全验收。
- 本轮误读过两个不存在的候选路径（state/state.go、common/app_error.go），已用 rg 定位真实 store.go/error_catalog.go；是源码检索错误，未导致测试收集或编译失败。

## 本次接手源码复核（69d4b603）

- `LocalWorkspaceControl.tsx` 在有 `conversationId` 时仍保留可点击的目录按钮；Core 的 `ensureConversationWithWorkspace` 会对新增或不同 binding 返回 `binding_locked`，因此 U1 是前端状态约束缺口，不需要修改 Core 绑定规则。
- 组件加载 effect 只用局部 `active` 防止旧绑定查询回写，但不会在 `conversationId` 变化时立即清空 `selected/items` 或通知父组件；`choose` 和 `allow` 的异步结果也不受该 `active` 标记保护。
- 目录按钮接收 `disabled`，最近目录 Select 没有接收；权限 Select 使用同一个 `disabled`，与 `next_request` 权限更新语义冲突。
- `listWorkspaces` 尚未暴露 Core 已实现的 `query/include_inactive` 参数；Core 的 `InternalPrepareReauthorization`、权限版本冲突和结构化 reason 均已存在，可直接复用，无需新增服务、依赖或数据库表。
- `validateWorkspaceAskSubmission` 仅比对最近未回答卡片的 `ask_id`；`RebuildSubagentParams` 仅写入 `parent_agentic_config.user_id`，而官方 `runner.py` 构建 agentic config 时依次读取 `attachment_context.user_id`、顶层 `params.user_id`、parent 值。C1/C2 的文档判断与当前源码一致。
- 当前 `LocalWorkspace.contract.test.ts` 的三项检查均读取源码并匹配字符串，不能覆盖 U1–U3 的交互和异步竞态；第一批应使用 React Testing Library 的 render/rerender/fireEvent 与可控 Promise 建立失败合同。

## T1 阶段一测试证据

- 新增 `LocalWorkspaceControl.test.tsx`，直接渲染真实组件和 Ant Design 控件；只 mock 运行模式、HTTP/bridge 边界、消息提示，使用可控 Promise 重现竞态。
- 单文件运行结果为 13 项中 7 项预期失败、6 项通过。预期失败准确命中：绑定任务目录按钮未锁定、已有未绑定任务仍可选择、切会话残留旧状态、旧 picker 结果显示授权框、旧 authorize 结果回写、disabled 草稿 Select 可用、运行中权限 Select 不可用。
- 通过项确认：旧绑定查询的 `active` 防护有效；原生 picker 取消、Modal Cancel、关闭按钮和 Esc 均不调用授权或 `onChange`；确认授权时只传 runtime 和 selection token。
- 初次增加 Modal 关闭测试时，jsdom 不执行 CSS leave 动画，导致以“节点移除”为标准的三项异常失败；测试改为检查 Modal 已进入 leave 状态，并将 Esc 发到实际键盘容器。调整后异常失败归零。
- 最终聚焦矩阵共 30 项：7 项预期失败、23 项通过；现有三个 workspace/bridge 文件的 17 项在加入新文件前后均保持通过。新测试 ESLint、diff 检查及冻结边界检查通过。阶段一不把预期 RED 计为产品验证通过。

## T2 实现与验证证据

- 仅修改既有 `LocalWorkspaceControl.tsx`：会话变化时立即清空目录、候选、权限和父状态；复用 request-id 模式为查询、picker、authorize、权限和撤销建立统一代次校验。
- 已有任务目录入口锁定：有 `conversationId` 时，已绑定任务不能换绑，未绑定任务不能新增绑定；失效绑定仍保留后续重授权入口。Core 的 binding 权威规则未改变。
- `disabled` 只禁用草稿目录选择入口；已绑定任务的权限 Select 保持可用，继续调用现有 `updateWorkspacePermission` 并沿用 `next_request` 语义。
- 对静态风险确认框捕获打开时的会话代次；切会话后确认旧 allow-all 不再发请求。权限更新和撤销已经提交到后端时不伪造取消，只丢弃不属于当前会话的返回值和消息。
- 阶段二新增 4 项合同后，组件测试 17/17 通过；其中额外确认取消 picker 不会作废同一草稿仍在加载的最近目录列表。连同 ChatInput 装配、workspace utility 和 Desktop bridge 的聚焦矩阵为 39/39。ESLint、`tsc -p tsconfig.mcp.json --noEmit` 和 `pnpm run build` 通过。
- 本机 Node 26 默认暴露实验性 `localStorage`，使 `ChatInput/index.test.tsx` 收集前失败；使用 `NODE_OPTIONS=--no-experimental-webstorage` 后通过。发布工作流为 Node 20，本批未修改测试基础设施。

## T3–T5 拉通证据

- `listWorkspaces` 现直接传递 Core 已支持的 `query/include_inactive`；管理 Modal 展示 active/revoked/path_unavailable，复用同一 token 重授权流程和撤销 API。
- 权限、授权和撤销遇到 binding_conflict/workspace_not_found/revoked/path_unavailable 时重新读取 Core 状态，不保留失败的乐观值；reason 使用中英文固定字典，未知值有明确兜底。
- AskCard 新记录逐题校验问题文本、类型、原 choices、自定义 choices 数量及答案值类型；null 答案仍表示省略。无 questions 的旧历史仅按 ask_id 校验以保持兼容。
- 官方 runner 优先读取 `attachment_context.user_id`；Core 现在对真实绑定同时归一化顶层、附件上下文和 parent 的 user/conversation，且保留附件内其他字段。
- 自动化结果为前端 43/43、Core 三包通过、静态检查与生产构建通过；这不是打包 Desktop 的人工 UI 证据，也不证明 F 类文件执行闭环。

## 2026-09-09 后续稳定性审计

- Local Proxy 的 workspace 端点用 `{code: "LOCAL_WORKSPACE_SELECTION_EXPIRED"}` 等响应错误；Axios 将其放在 `error.response.data.code`。当前 `workspaceReason` 只读取 Core 的嵌套 reason 和错误对象顶层 `code`，因此主机选择过期、选择禁止等错误会落入 `unknown`。
- 当前中英文 reason 字典没有选择过期和选择禁止文案。已有 mode/path/invalid 文案可以在归一化后继续复用，不需要修改 Local Proxy 或 Core 协议。
- `loadManagedItems` 只用独立的 `listRequestRef` 处理查询先后顺序。会话改变仅递增 `requestRef`，不会关闭 `manageOpen`、清空 `managedItems` 或作废在途管理查询；草稿切换到已有任务时，管理窗口及旧授权结果仍可能显示。
- 修复可限制在 4 个既有前端生产文件，预计净增 30–50 行。算法、Backend、Local/Desktop 均无需修改；最终人工验收和文件执行能力不属于本批。
- S1 首次运行时管理窗口用例被前序 Ant Design confirm 的离场动画节点干扰，`findByRole("dialog")` 因多个匹配而异常失败。改为按 `chat.workspace.manageTitle` 定位所属 dialog 后，结果稳定为 5 个错误码断言收到 `unknown`、管理 dialog 未进入 leave 状态；这是 6 项预期产品失败，不再包含测试设施异常。
- S2 以固定 map 归一化 5 个现有 Local Proxy code，保持 Core detail reason 的读取优先级，并对非字符串 code 返回 unknown；没有把任意大写文本动态转换为翻译键。会话 effect 递增既有 `listRequestRef` 并清理管理窗口状态，迟到列表因此不能回写。
- S3 六文件矩阵为 49/49；相关 ESLint、`tsc -p tsconfig.mcp.json --noEmit` 和 Vite 生产构建均通过。构建仍只有既有的资源解析、动态导入和 chunk 大小警告。本批相对 `4e837c43` 没有 Backend、算法、Local 或 Desktop 生产差异。

## 基线

审计提交 bb46abd64ca5fc431f4f7748fb9e085099990d5e，旧对照 e7ed8a4189bb627e96814fc2f34818693cbc2050。旧 spec/checklist/代码在 Git 历史中可查；不继承其勾选为当前验收结果。

截至原审计，算法相对 245bc26d 零差异，Local/Desktop 相对 ec4676e0 零差异。该阶段的算法冻结已由 2026-09-09 用户主动确认调整：允许项目算法及对应测试必要修改，LazyLLM/gitlink、Local/Desktop 仍冻结。

## 本机实际检查（2026-09-08）

| 检查 | 结果与限制 |
|---|---|
| backend/core：go test ./localworkspace ./chat ./subagent -run 'Test.*(Workspace\|LocalFS)' -count=1 | 3 包通过 |
| 前端现有 workspace/bridge 聚焦测试 | Node 24.19.0 下 3 文件 17 项通过；不等于完整 UI 验收 |
| 默认 Node 20.20.2 运行相同 Vitest | jsdom/undici 报 webidl.util.markAsUncloneable 不存在，收集前失败；换已有 Node 24 后通过，未改依赖 |
| 官方文件工具临时探针 | 实际导入当前 LocalFileToolkit/write_file，没有替换工具方法；所有文件为临时假数据 |
| 前端组件临时探针 | 实际组件+React/testing-library/jsdom，API/展示组件为替身；不冒充真实浏览器 E2E |

前端测试文件：src/modules/chat/components/ChatInput/LocalWorkspace.contract.test.ts、src/modules/chat/utils/localWorkspace.test.ts、src/runtime/desktopBridge.test.ts。LocalWorkspace.contract.test.ts 的 3 项为源码字符串断言，不能覆盖交互缺陷。

## 文件工具级复现

临时脚本 /tmp/lazymind-parity-review.fOKiMM/probe.py 使用已有 local/build/deps/python/algorithm/bin/python；配置、日志和 fixture 均在同一临时目录，未修改算法。

| 操作 | 当前实际结果 |
|---|---|
| 检查工具列表 | ls/glob/grep/read/string_replace/info；trusted=false |
| 从非工作区 cwd 读取 existing.txt | 相对路径失败；绝对路径成功、返回绝对路径且无 version |
| write_file 创建/追加宿主文件 | 两者均 ToolExecutionError |
| 配置 always_ask 后直接 string_replace | 文件修改成功；说明工具层无该批准检查，不表示模型一定忽略提示词 |
| read 后外部增加一行，再 replace | 接受修改；接口无 expected_version |
| service-account-fixture.json（只有 marker 假数据） | read/replace 均成功；旧规则要求敏感读批准、敏感写禁止 |
| .git/fixture.py（临时假文件） | replace 成功；旧规则禁止 .git 内写入 |
| 同一工具上下文中将根移走、原路径改为指向 sibling 临时目录的链接 | read 读取替代目录；没有复核旧授权的文件系统身份 |

可迁移的复现方法：在临时 root 创建 existing.txt=alpha；设置 agentic_config.local_fs_sources 为 source_id、paths=[root]、file_extensions=[txt,json,py]，并设置 workspace_permission_mode=always_ask。直接调用实际 read/string_replace 并断言磁盘；用 sibling 临时目录测试根替换。不要使用真实敏感文件或用户工作目录。

这些证据不是撤销端到端测试；Core 的取消通知仍存在，但不能由通知存在推导出逐次访问/提交校验已经恢复。

## 组件级复现

临时脚本 /tmp/lazymind-parity-review.fOKiMM/ui-probe.cjs 转译实际 LocalWorkspaceControl.tsx，未替换其状态和事件逻辑。

1. 已绑定 conv-a/grant-a 的目录按钮仍可用；选择并确认 grant-b 后 onChange 实际发出 grant-b。Core 绑定锁会拒绝随后的请求。
2. 同一组件从 conv-a rerender 到无绑定会话，旧 Alpha 仍显示，onChange 没有发出清空值。
3. 新草稿 disabled=true 时，最近目录 Select 仍可用。

正式回归需用 render/rerender/fireEvent 和可控 Promise 覆盖同样步骤，以及 picker/authorize 迟到、取消、历史重授权、运行中权限和错误反馈。临时脚本不是远端测试交付，其他机器按上述步骤重新构造 fixture。

## 源码证据落点

- backend/core/localworkspace/context.go：sources 及 ModelNotice，权限表达为文本。
- algorithm/lazymind/chat/engine/tools/local_fs.py：实际工具列表、路径解析、read/string_replace。
- algorithm/lazymind/chat/service/chat_service.py：MCP 只在非 Workflow 主轮加载；缓存 key 包含完整 server 配置。
- algorithm/lazymind/chat/engine/subagent/runner.py：工具从 DEFAULT_TOOLS/Workflow package 解析；身份优先读取 attachment_context/顶层 params。
- backend/core/localworkspace/subagent_context.go：当前只重建 parent 等字段，需要核对实际消费者优先级。
- frontend/src/modules/chat/components/ChatInput/LocalWorkspaceControl.tsx：绑定状态/异步回调/Select 缺陷。
- frontend/src/modules/chat/utils/localWorkspace.ts：列表未带 include_inactive/search，reason 未完成本地化映射。

未验证：真实模型同轮完整文件闭环、Local/打包 Desktop 全 UI、真实子任务批准、Windows 文件操作。不得以已有测试通过替代这些验收。

## 2026-09-09 新授权提案源码复核

本次 HEAD 为 7e04900ee331553829917be1e73c661ff8b0103c，核查开始工作区干净。完整读取四份交接文档；以下仅是本次源码证据，不是运行测试通过。

- algorithm/lazymind/chat/service/component/tool_registry.py 的 ToolConfig 尚无授权字段；还需核查 Toolkit 展开后到具体方法的元数据传播，不能只标记整个 Toolkit。
- algorithm/lazymind/chat/engine/agent_runtime/tool_call_guard.py 的 ToolExecutionMiddleware.execute_with_records 已通过 dispatch_selector 在派发前处理重试/去重，可作为授权接入候选。
- algorithm/lazyllm/lazyllm/tools/agent/toolsManager.py 的现有 execute_with_records 先调用 selector 再执行选中的调用，支持复用，不需为获得此前置入口修改 gitlink。
- executor.py 安装上述中间件；subagent/runner.py 使用 AgentExecutor，具备复用基础，但尚未证明所有 Workflow/工具旁路受控。
- backend/core/localworkspace/context.go 已提供 ResolveForConversation 和权限快照，目前 ModelNotice 仍为提示词规则，并不提供逐调用批准执行保证。
- chat_service.py 仍在 workflow_turn_is_bound 时排除通用 MCP；不能以主任务 MCP 代替完整覆盖。

本批只运行 git status、git rev-parse、rg、源码及文档读取、git diff 检查；未运行测试或临时探针。

## 2026-09-09 授权边界确认后的设计审计

- 用户已确认允许 algorithm/lazymind/ 与对应测试的必要修改，要求修改前先明确方案、控制冗余。LazyLLM/gitlink、Local/Desktop 仍冻结。
- 发现直接复用的算法 HTTP helper：chat/engine/tools/infra/core_api_client.py 的 post_core_api/get_core_api，已带现有内部服务 token 和可信运行时用户头，并关闭环境代理继承；无须增加 HTTP client 或复用选择目录专用 host token。
- Core store.State() 已提供 SQLite/Redis state.Store；SetNX 和两种实现的 CompareAndDelete 可用于有界待批准状态与单次领取，不能把普通 Get+Del 当成原子消费。
- tool_limit_control.py 已有同轮等待、取消、超时示例，但活动记录按 sid 单项保存、仅支持 continue/summarize，且 executor 仅为主 Chat 安装 on_max_retries；整套复制/改名不能解决多子任务并发批准。
- LocalFileToolkit 同时包含 ls/glob/grep/read/string_replace/info，搜索也会读内容；只检查 read/string_replace 会留下路径和敏感读取旁路。
- 主请求在 chat_service.py 显式组装 agentic_config，不能假设 Core ext.workspace_context 自动传入。子任务从 parent_agentic_config 复制再归一化身份；Workflow legacy_tools 来自不可变 Attempt Context，package 工具优先于同名 DEFAULT_TOOLS，必须处理同名覆盖和自定义代码旁路。
- 已核对历史 e7ed8a41 的 spec：always_ask 普通读免询问、写与外部副作用询问；ask_as_needed 普通工作区文件操作允许，风险操作询问；allow_all 跳过可批准询问但不能绕过永久禁止项。权限改变不自动批准既有 pending。删除为本轮新增语义，需明确验收。
- 自定义 Python/命令可以在工具内部直接访问文件，注册字段不构成进程沙箱。方案必须明确未接入工具的兼容性策略，不能宣称任何 Workflow 代码天然受控。
- 读取时遇到两个不存在的候选文件名和 zsh 未匹配 glob，已改用 rg --files/实际路径；这些是检索错误，未运行测试，不计为产品失败。

### 设计结论与验证限制

- Core 已有 store.State()，但没有现成逐工具批准业务；批准记录和磁盘执行是必须补充的两项职责，方案预算为 2 个新生产文件，不机械复制 ToolLimitDecisionCoordinator。
- 现有 Local runtime 已向 Core/算法传递内部服务 token 的配置；本次只读取对应源码，未读运行时秘密或修改冻结文件。新的内部文件操作必须验证 token 非空，不照搬 RemoteFS 未设置 token 时放行的行为。
- 原子替换不自动提供数据库/磁盘跨系统事务，也不自动解决外部程序并发写；方案显式保留 uncertain 与真实竞态验收，不承诺未验证的 exactly-once 或沙箱隔离。
- 当前设计未运行算法/Core/UI 新测试；预算、接口、策略表及新增文件属于待 Review 方案，不是实现完成事实。

## 2026-09-09 独立 Agent Review 结论（进行中）

- **P1，已证实：Workflow 自定义脚本存在加载期旁路。** `algorithm/lazymind/chat/engine/subagent/runner.py:219-237` 会在工具调用和 `AgentExecutor` 安装前对发布脚本执行 `exec(compile(...))`。因此仅在 `ToolExecutionMiddleware` 中拒绝未授权 callable，无法阻止脚本顶层代码在加载时访问宿主文件或产生副作用。绑定工作区的 Workflow 必须在加载前验证为受控工具包，或明确拒绝该类脚本；必须加入 import-time 副作用测试。
- **P1，已证实：现有 `PreparedToolCall` 只保存调用数据和 `ResolvedToolAccess`，没有原始 callable、授权句柄或操作状态。** `algorithm/lazyllm/lazyllm/tools/agent/tool_runtime.py` 与 `toolsManager.py:1043-1058` 表明授权信息不能靠模型参数或工具名自然传递；中间件需要在自身内维护一次调用的不可伪造上下文，并在批准恢复时核对 call_id、参数摘要和运行身份。
- **P1，已证实：`ToolExecutionMiddleware` 的 selector 适合作为执行前阻断点，但它不是批准状态机。** `tool_call_guard.py:345-374` 只负责失败策略、日志和派发索引；Core `state.Store` 的 `SetNX`/`CompareAndDelete` 是原子原语，不会自动实现状态转换、过期、单次领取和 uncertain 回执。A1 必须先定义并测试这些转换。
- **P2，已证实：当前方案的 5 个 HTTP 接口可以合并为一个受限的操作资源接口，减少 handler/DTO 重复。** prepare/status/decide/execute 仍需保留语义，但可以由同一个 `workspace-operations` handler 按动作路由到一个 service 类型；前端用户列表继续使用独立登录端点。是否合并应以可读性和测试覆盖为准，不能把并发状态逻辑塞进 `handlers.go`。
- **P2，已证实：`ToolRuntimeMetadata.read_keys/write_keys` 只能表达工具资源冲突，不能替代 Core 的 workspace grant/permission 判定。** 可复用其访问索引避免再造“文件资源 DTO”，但不能把本地绝对路径 key 当成授权证明。
- **P2，需实测：普通子任务是否始终经过 `AgentExecutor` 中间件、Workflow 是否存在远程 executor 之外的本地脚本入口，以及 Local/打包 Desktop 的实际文件句柄语义。当前源码显示存在复用路径，但还没有端到端证据。

Agent Review 还未形成完整终稿；上述结论已足以阻止直接进入生产，先修正设计再做 A0。

## 2026-09-09 A0 首轮测试结果

- 基线 `go test ./localworkspace -count=1` 通过。
- 算法完整命令在系统 Python 下因未加载 LazyLLM 失败；使用仓库 `.venv` 并补充 `PYTHONPATH` 后，工具注册和 Workflow 收集又因 `.venv` FastAPI 与系统 Pydantic 版本不兼容（`ImportError: pydantic.main.IncEx`）阻塞。这是测试环境异常，不计入产品 RED；未改环境文件或仓库依赖。
- 可运行的 A0 命令：`PYTHONPATH=algorithm/lazymind:algorithm/lazyllm .venv/bin/python -m pytest tests/algorithm/chat/test_tool_call_guard.py tests/algorithm/chat/test_workspace_authorization_contract.py -q`，结果 **17 passed, 4 failed, 0 abnormal**。
- 4 项失败均为预期合同：`authorization_gate` 尚未接入中间件；`ToolConfig` 尚无 `authorization` 字段；中间件无授权 gate；Workflow 脚本无加载前 `_validate_workflow_workspace_package` 准入。此前静态合同选择器导致的异常已修正并重跑归零。
- 当前只新增 A0 测试文件和测试合同，生产代码净增 0；未进入 A1 实现。Workflow 加载期副作用问题仍是 P1 阻断。

## 2026-09-09 A1 实现与验证结果

- A0 四项 RED 已转绿：`ToolConfig.authorization` 字段及 local_fs 方法映射、`ToolExecutionMiddleware.authorization_gate`、拒绝零副作用、Workflow 绑定工作区时加载前拒绝。
- A1 可运行矩阵：`test_tool_call_guard.py`、`test_agent_executor.py`、`test_workspace_authorization_contract.py`、`test_local_fs_tool.py`、`test_core_api_client.py` 共 **52 passed**；`py_compile` 和 `git diff --check` 通过。
- 生产修改 5 个既有 `algorithm/lazymind` 文件，净增约 **66 行**；新增测试文件 1 个，未新增生产文件。没有修改 LazyLLM 内容/gitlink、Local/Desktop、Backend 或依赖。
- 授权 gate 只接受明确 `True/'allow'/'allowed'`；拒绝返回 `authorization_denied`，异常/未知状态闭合为 `authorization_unavailable`。没有 Core gate 实例注入，因此 A1 不改变现有运行时的真实授权行为。
- Workflow 的 `runner.py:237 exec(compile(...))` 之前增加加载准入；绑定工作区且存在声明脚本时默认拒绝，避免把模型可写的 `workflow_package_authorized` 当成安全凭据。无工作区 Workflow 保持旧路径。
- 发现并修复一次测试环境异常：`.venv` 的 FastAPI/Pydantic 版本冲突；未修改仓库依赖。最终可运行子集使用 Pydantic 2，52 项通过；完整服务图仍需在正式 CI/发布环境复核。

## 2026-09-09 A2 首轮 RED

- 新增 Core 合同测试：`backend/core/localworkspace/operations_contract_test.go`、`approval_routes_contract_test.go`；只读源码合同，不引入生产接口替身。
- 命令 `go test ./localworkspace -run 'Workspace(Operations|Approval|OperationRoutes)' -count=1` 结果为 **4 个测试失败、0 个异常失败**。
- 预期失败准确命中：`operations.go` 尚不存在；`approvals.go` 尚不存在；`routes.go` 尚未登记 workspace-operations prepare/status/execute 和用户 decide 路由。
- 初次合同路径读取错误已修正：Go 测试工作目录是 `backend/core/localworkspace`；缺失生产文件现在报告字段缺口而不调用 `t.Fatalf`，保证 RED/异常失败可区分。
- 本批尚未修改生产代码；A2 生产范围、状态机实现和文件原语仍待人工 Review。已有 `go test ./localworkspace -count=1` 基线在 A1 期间通过，A2 RED 仅是新增合同失败。


## 2026-09-09 A2 实现与验证

- Core 新增 `operations.go` 502 行、`approvals.go` 177 行；算法既有 `local_fs.py` 净增 211 行；新增 Core/算法测试约 414 行。生产净增约 895 行，超过规模门槛，已按两份职责拆分并记录不可复用原因。
- Core 实现：ResolveForConversation 复核 owner/binding/status/directory identity；prepare 保存 operation_id、调用摘要、权限版本和内容摘要；execute 重新复核绑定/权限版本/文件版本，采用临时文件+rename 写入，拒绝绝对路径、越界、.git、symlink、特殊文件和敏感写入；批准用 SetNX 决定锁，执行用 SetNX 单次锁。
- 算法实现：绑定工作区 source 或可信上下文时，LocalFileToolkit 的 read/string_replace/create/append/delete 转发 Core；Core pending 转成 `ToolExecutionError.approval_required`，Core 错误不会回退 Python 本地读写；无绑定 source 保留旧行为。
- 验证：算法相关矩阵 103/103；Core `go test ./... -count=1`、`go vet ./...`、`go test -race ./localworkspace -count=1` 通过；本地 Python 警告不影响结果。
- A2 未完成项：没有注入真实 Core authorization gate；pending 没有在同一 Agent 轮次轮询/恢复；UI pending 列表/决定尚未接入；Workflow 自定义包和普通子任务仅有源码路径证据，未端到端；uncertain/崩溃恢复和打包 Desktop 未验证。

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
