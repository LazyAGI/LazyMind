# 复用核查记录

本目录遵循用户最新约束，优先于父目录所有旧设计和历史验收结论。

## 已确认

- 当前功能分支干净，HEAD=e7ed8a4189bb627e96814fc2f34818693cbc2050。
- 原 PR #693 共 132 个文件，algorithm/ 与 tests/algorithm/ 共 23 个；禁止直接整体搬运旧 PR。
- 上游请求已支持 local_fs_sources、files、history、query/user_query、disabled_tools、available_skills、enable_subagent、enable_workflow。
- 上游 LocalFileToolkit 已支持目录、glob、grep、读取、精确替换、info；读取 source_id/paths/file_extensions。它不识别旧 PR 新增的 workspace_id/relative_paths/权限字段；扩展名采用精确匹配，不能传 ["*"] 表示所有文件。
- 主任务已有 write_file、list_dir、read_file；不为本需求重写算法工具。
- environment_context 在上游提示词主要消费 locale/time，不能假设增加工作区字段就会自动进入提示词。
- 当前 backend/core/localworkspace/ 和 frontend 的工作区控件可提取，但原生选择仍依赖旧 PR 的 local/desktop 改动，需要替换接入。
- 已有后端 MCP 认证/通信聚焦测试通过（前轮）；MCP 不作为本方案默认架构。

## 本轮新增证据

- Desktop 基线 main/preload 已有 selectFolder；frontend/src/runtime/desktopBridge.ts:selectFolder 可直接调用，不必添加 selectLocalWorkspace/authorizeLocalWorkspace 等桥。
- buildChatRequestBody 同时保留 query/user_query；上游 chat_service 把扩展后的 query 放入 backend.resources 上下文，真实用户输入和语言选择仍由 user_query 决定。已有 ContextPrompt 导出可验证是否真正可见。
- AskCard 已提交 ask_answers_structured；buildHistoryMessages/replaceAskUserToolResult 会重建询问结果。局部填写和忽略卡片有特殊行为，不能把任何新消息都当批准；应仅给工作区权限询问补充范围内的解释规则。
- ContextUsage/ContextPrompt 有独立请求组装路径，必须复用同一工作区上下文函数，不能仅修改 conversations:chat。
- StopChatGeneration 已覆盖主会话取消信号、外部执行器、Workflow 停止、普通子任务 InterruptConversation/CancelRuns 和算法 NotifyChatCancel；可提取服务函数，禁止 handler 间伪造 HTTP 请求。
- 普通子任务 handleTaskCreated 有 create/resume 两路；当前 PR 的 authoritativeSubagentParams 可收敛为现有 parent_agentic_config.local_fs_sources 等字段。上游 runner 已消费 parent_agentic_config，且 runtime_instruction 是现有提示词入口。
- Workflow Step 是另一套公共 Attempt Context 路径，不能把用户目录写入其内部 WorkspacePath 或自动重写不可变产物路径。
- Workflow 进一步核查发现已有 Host-private `subagent.InternalGetExecutionSpec`：原样 remote_executor 读取 spec.params 和 spec.task，runner 消费 parent_agentic_config/runtime_instruction。可在这个后端私有输出边界重建工作区上下文，无需给公共 AttemptContext 加主机路径，也无需修改算法；必须同步 spec.params 与 task.params，防止一个覆盖另一个。
- common.AppError.WithDetail 和 ReplyAppErr 可用已有通用错误码 + detail.reason 表达工作区业务原因；无需改根目录 i18n/errors/core.json。
- 原生目录选择等待可能超出代理读取超时；Local 的 Core 选择器应采用短请求启动 + 状态查询 + 一次性候选 token，复用原候选存储逻辑，不能加到冻结的 Local Proxy。
- 上游 Desktop 构建默认 trustedLocalMode=false；write_file 创建/追加宿主目录依赖已有 trusted_local_mode。local_fs.string_replace 只覆盖已有文件。因此“仅传 paths 即可创建任意新文件”不能作为已验证事实，必须将真实读/建/改列为开始实现时的首个兼容性检查。不能为使检查通过修改算法、运行模式或打包配置。

## 调查过程说明

少数猜测文件名的 rg/git show 查询未命中（如 chat/subagent.go）；已通过 rg --files 和真实调用点定位到 conversation_logic.go、subagent/store.go、runner.go。它们不是测试失败，也不影响代码状态。

## 本轮运行验证

在 FEATURE_SNAPSHOT 上运行既有聚焦测试：

```sh
cd backend/core
go test ./chat ./localworkspace ./subagent ./common -run 'Test(BuildChatRequestBody|BuildLazyChatRequest|ReplaceAskUserToolResult|ApplyLocalFSPathsForChat|InterruptConversationStopsOnlyActiveTasks|ErrorCatalogCodesHaveTranslations|.*DirectoryIdentity.*)' -count=1
```

四个包均通过。它证明当前可复用组件的已有测试通过，不证明新方案已实现，也不证明上游原样算法的完整读建改闭环已通过。


前端既有复用组件验证：

```sh
cd frontend
node_modules/.bin/vitest run src/runtime/desktopBridge.test.ts src/modules/chat/components/AskCard/index.test.tsx
```

2 个文件、9 个测试全部通过，退出码 0。运行输出含 Node localStorage 实验性提示和 Sass legacy API 弃用提示；没有测试失败。本轮未升级依赖。测试只验证现有组件，未验证新选择流程。

- 最终复核了原样 subagent runner：parent_agentic_config 是已排除直接提示词展示的结构化参数。计划将 Core 的去重元数据放在它的嵌套字段中，并明确 create/resume 的信任来源，避免恢复时反复叠加旧权限文本。
- 明确 OpenAPI cache 的实际路径为 frontend/scripts/openapi/.openapi-cache.json，避免下一位 agent 误改根目录缓存。

## 编码启动后的任务 0 证据

- 最新官方目标基线为 `245bc26dca1f2e8b56b0766cf72fdfcdb49138d9`；新功能工作树的 `algorithm/`、`tests/algorithm/`、`local/`、`desktop/`、`.gitmodules`、`LAZYLLM_VERSION` 相对 `upstream/main` 零差异。
- 官方 LazyLLM gitlink为 `2e3d00ac3ae4ae983cb7d0ca4bd231c1a56ebfc0`。仅为本机临时运行环境安装缺失 Python 包，没有修改仓库依赖、锁文件或算法文件。
- macOS 工具级临时目录实测：`LocalFileToolkit.read` 读取 `alpha\n` 成功，`LocalFileToolkit.string_replace` 替换后磁盘为 `beta\n`。
- 默认 `trusted_local_mode=false` 时，主任务 `write_file` 对该宿主目录的新建、嵌套新建和追加都返回 `ToolExecutionError: path must stay inside the current main-Agent workspace`；磁盘确认没有创建目标文件。
- 这证明“创建/追加宿主文件”是当前官方能力缺口，不能由新的 `local_fs_sources` 或提示文本补足。后续前后端实现不得把该项标记为通过。

## 受控写入通道核查

- 官方算法请求已经支持 `mcp_config`；`chat_service.py::_build_mcp_tools` 使用现有 MCP client 加载并注册允许的工具。因此 Core 注入系统 MCP 可以在不改算法的前提下给主 Agent 增加窄工具。
- Core 已依赖 `github.com/modelcontextprotocol/go-sdk`，并已有 stateless Streamable HTTP MCP handler，可复用服务构造、请求体上限和测试方式，无需新增生产依赖。
- 用户配置的 MCP 由数据库和设置总开关加载，不适合承载任务工作区能力；系统工作区 MCP 应只在有效绑定的最终请求内追加，不写用户 MCP 表，也不出现在设置 UI。
- 直接转发用户 Authorization/Cookie 会把长期凭据送入算法进程且无法稳定支持后台执行。方案改用 Core 生成、仅内存保存摘要、绑定单次 run/workspace 的随机 capability token，并在每次写入时重新核对数据库和目录身份。
- 官方 ask_user 提供 ask_id 和结构化 boolean 回答，但不会天然绑定某次 MCP 操作。因此 always_ask 必须先暂存不可变操作，再由 Core 根据 confirmation marker 用服务端数据规范化 AskCard；commit 只接收 confirmation_id，防止确认后替换 path/content/mode。
- Workflow-bound turn 当前不加载通用 MCP，普通子任务也没有主 Agent AskCard 通道。第一版将写入能力明确限定为主 Agent；子任务与 Workflow 的创建/追加不作虚假承诺。

## 受控写入方案 Review 发现

- 官方算法 `_mcp_server_cache_key` 会把 headers 纳入 key，`_mcp_tool_cache` 没有过期项清理。每请求更换 Bearer token 会导致 cache/client/token 在算法进程内无界累计，是当前方案的 P0 阻断项。
- Core 当前在 `handleStreamChat` 内才生成 `run_id`，此时最终请求已经组装；请求级 token 无法按计划绑定真实 run。SSE 断开后 Core 还会继续 drain，不能用浏览器 request context 代表执行生命周期。
- always_ask 可以通过服务端规范化防止“确认后换参数”，但第一次 MCP 返回后仍依赖模型主动调用 ask_user。模型漏掉 marker 时会安全失败但流程卡住，必须真实模型验收。
- temp+rename 追加会改变 inode 并可能丢 ACL/xattr/硬链接和 watcher 语义；逐段 Lstat 也无法完全消除外部进程 TOCTOU。必须明确威胁模型并选择平台安全句柄或接受残余风险。
- 推荐使用稳定的 Core 进程 caller token控制 MCP cache，再以短期 invocation_handle 绑定本轮 run；handle 单独无法通过 MCP caller 认证。preview 不激活 handle。该设计会把短期 handle 放进模型可见增强 query，但不进入用户原文/history/数据库，需在最终安全说明中明确。

## 受控写入方案第二轮 Review 发现

- 官方算法的常规 `ToolCall` info 日志会记录工具参数预览，Agent Lab/上下文压缩遥测启用时还会把参数预览写入 JSONL；仅 `set_session_env` 有现成脱敏。冻结算法时，MCP 的正文和 invocation handle 无法在进入这些日志前由 Core 清洗。
- 官方工具渲染还会把完整 arguments 放入 `<tool_call>` 帧。若未来继续写入方案，Core 必须在缓存、ChatHistory、重连和外部聊天投影前结构化移除正文/句柄，但该措施只保护 Core 数据，不保护算法日志。
- MCP SDK handler 没有可直接复用的 JSON-RPC 请求 id，所有权限模式都需 prepare/commit 才能抵抗网络/模型重试导致的重复 append；不新增持久化 ledger 时，只能保证同一 Core 生命周期幂等，崩溃后必须禁止自动重放。
- 512 KiB pending 内容要求按 conversation/user/全局限制数量与总字节，并在完成、拒绝、撤销、run 结束和 TTL 到期时释放。
- 既有 Local 内部 token 有固定开发默认值，不能作为可暴露 MCP route 的唯一 caller 凭据；仍应使用 Core 启动时随机且进程内稳定的 token，接受每次 Core 重启最多新增一个算法 cache key 的残余增长。
- 通过官方 `write_file` 或 `save_chat_artifact` 先做内部产物再复制也不能避开正文参数日志，因此不是安全替代方案。
- 当前约束形成硬冲突：算法保持官方零差异时，无法同时保证模型生成的写入正文不进入算法工具日志。推荐暂停 create/append；若必须交付，需要用户明确批准极窄算法脱敏补丁，或明确接受日志泄露风险。

## Local/Desktop 一次性复用核查

- 旧分支的 Local/Desktop 差异共 26 个文件、约 +1029/-214 行，不是纯工作区补丁；包含 Caddy 升级、assistant bridge 改写、Windows 脚本删除和构建测试删减，不能整体提取。
- Desktop 官方基线已经有 `selectFolder()`、dialog、runtime 状态和 preload bridge，可复用其基础设施；但普通 `selectFolder()` 只返回可由 renderer 伪造的 path，不能替代旧工作区 IPC 的短期 token + webContents 选择证明。保留窄 select/authorize IPC，同时避免复制通用 helper，是安全与代码量之间的最小边界。
- Local Proxy 已有 CORS、AdminSession、route matching/reverse proxy 和 `writeJSON`。旧 `workspace.go` 再实现一套错误、envelope、Core client 和身份逻辑共约 425 行；一次性提取时应只保留操作系统 picker 与短期候选状态，复用现有代理/会话能力，不能复制 Core 业务规则。
- 旧 Local Runtime Manager 向 Core、Local Proxy 和算法同时注入 workspace host token；官方算法不消费该旧合同，算法注入必须删除。只有最终选定的本机 Core/Proxy 边界所需配置才允许保留。
- 用户要求在冻结前优化旧功能实现并尽量复用已有代码；优化、接入和测试必须作为同一批完成，冻结后不得以普通修复名义继续修改 Local/Desktop。

## 2026-09-08 当前接手执行记录（优先于历史状态）

- 文档远端已 fetch 并核对：`aa53e9699ba9f6b06383d3fe2eb94adf538a1a83`。
- 当前工作树 `/Users/theone/Downloads/lazymind-workspace-core`，新分支 `codex/local-workspace-core`，直接建立于用户指定 `245bc26dca1f2e8b56b0766cf72fdfcdb49138d9`。原工作树仍在 `feature/newWorkZone`，开始时 status 干净，没有覆盖改动。
- 官方 main 只读查询为 `bac0dc102775488df19908dbcf4f5fe4e47a084c`；本轮遵循用户指定基线，不自动升级。
- 已完整阅读四份文档、`.cursor/rules/coding-standards.mdc`、迁移 AGENTS.md；父目录没有 AGENTS.md。遵循用户明确要求及本文历史交接的测试人工 Review 门禁，先交付任务 1 首批测试，不写生产功能。
- 当前只导入四份文档，生产净增 0。复用现有 ORM 测试数据库、迁移 runner 和聚焦测试，不引入框架。
- 已运行官方基线 `go test ./chat ./subagent ./common -run 'Test(BuildChatRequestBody|BuildLazyChatRequest|ReplaceAskUserToolResult|ApplyLocalFSPathsForChat|InterruptConversationStopsOnlyActiveTasks|ErrorCatalogCodesHaveTranslations)' -count=1`：三个包通过，退出码 0。
- 文档冲突解释：0.1 与任务 2 及用户最新要求优先；2.3、3.1、任务 6 中直接提交 renderer path / Core picker / 删除窄 workspace IPC 的旧描述不再是实现合同。任务 2 前应形成精确的本机选择证明接口测试并 Review；不得按旧条款绕开选择证明。
- 任务 3A 继续暂停。上述历史机器实测不当成本机验收；本机工具能力、前端及 PostgreSQL 尚待验证。
- 下一步：完成任务 0 可运行的本机检查、编写任务 1 测试并报告预期失败/异常失败，然后等待人工 Review。

### T1-RED-1 本机新增证据与限制

- 新增测试共四文件 466 行，生产净增 0，文件与命令见主方案当前批次及 progress 矩阵。
- 基线 Chat/Cloud 收到 `workspace_id` 后没有先拒绝：两个 HTTP 用例均实际调用一次 fake scan 服务并持久化一个 conversation，随后返回 500/code 2001407；预期为 403/既有 forbidden code/detail.reason=mode_forbidden，且零派发/零落库。测试替身不连接真实服务。
- localworkspace 包不存在，测试编译报 Enabled/PublicWorkspace/Register/ResolveForConversation 等 undefined；这是缺失实现证据，尚非所有服务断言运行后的 red。模型不存在也阻止了服务用例执行。
- schema 用例在既有 ORM 全模型 fixture 中发现 local_workspaces 和 conversation_workspace_bindings 均不存在。迁移用例发现六份 up/down 文件均不存在，SQLite 升级用例在第一份缺失 migration 停止。
- macOS 工具测试使用原工作树既有 Python venv 的依赖，正常 PYTHONPATH 仅指向新工作树官方源码及官方子模块；打印模块 __file__ 已核对。没有动态替换/monkey patch/site-packages 修改；PYTHONDONTWRITEBYTECODE=1，日志与内部产物均在 /tmp。
- 实测临时宿主目录 `/private/tmp/lazymind-workspace-evidence/host-_b7g5_u3`（测试结束删除），existing.txt 从 alpha\n 变为 beta\n；三个被拒绝的写调用后磁盘仍为 beta\n，created.txt/nested/result.txt 不存在。工具检查不等于完整 Local 服务、打包 Desktop 或真实模型验收。
- 独立异常：Python 工具断言全部执行且退出 0，随后官方 LazyLLM atexit 的 cleanup 延迟初始化 LOG，打印 `RuntimeError: can't create new thread at interpreter shutdown`。未修复冻结算法；不把这段输出归入预期 create/append 拒绝。另有官方 Python escape SyntaxWarning、前端 Sass 弃用提示。
- 测试开发期间异常已处理：一次 Go 命令误在仓库根运行而报缺少 go.mod，已在 backend/core 重跑；首次 HTTP fixture 用 map 解码上游 string detail，已改为 any 后显式断言 reason 类型，最终可观察到真实缺失门禁。查询中猜测文件不存在不属于产品失败。
- 没有 PostgreSQL 临时 DSN，因此其既有迁移测试明确 SKIP；SQLite 两组既有迁移测试通过。ContextPrompt/真实模型询问、完整 Local/打包 Desktop/Windows、权限修改、停止及任务 2–7 未验收。
- 后续实现风险：主方案任务 1 一次列出多份生产文件，不能视为豁免 200 行/1 文件门槛；需拆成可审查小批次或事先报告预计 diff 和不可复用原因。


### 2026-09-08 T1-SCHEMA-1：ORM 与迁移生产批次

- 用户明确批准直接进入生产，并批准超出原 297 行 SQL 的最小修复批次；共享的六份旧工作区 migration 从文档快照逐文件提取并通过 `scripts/check_migration_immutability.py --base 245bc26d...`，未改写历史 SQL。
- 生产修改：新增 `common/orm/local_workspace_models.go`，在 `all_models.go` 注册两表；新增三组历史 dev migration 和一组 `20260908065108_fix_workspace_binding_timestamp` 修正 migration；更新既有 v0_3 aggregate；`migrate/run.go` 将声明 `PRAGMA foreign_keys=OFF` 的 SQLite migration 固定到单连接，在事务前暂停外键、事务内执行 `foreign_key_check`、所有出口恢复原设置。
- 清理：新事务 helper 替代 apply up/down 与 migration 测试 helper 的重复 Begin/Rollback/Commit，共删除 52 行旧事务代码；没有删除仍被 dialect 容错测试使用的 `execMigrationSQL`/column-change helper。
- 规模：本批生产约 +554/-42，净增约 512 行。其中不可压缩部分为八份获批 SQL（历史三对 214 行、修正一对 46 行）及 aggregate 83 行；runner +84/-42；ORM +44。未新增 manager、facade、DTO 或依赖。
- 双数据库验证：`MIGRATION_TEST_POSTGRES_DSN=... go test ./migrate -count=1` 通过；SQLite/PostgreSQL 独立 ORM schema 测试通过；`go test ./common/orm -count=1` 通过。测试覆盖 FK 原始 ON/OFF、SQL/历史/integrity 失败回滚、up/down、绑定数据保留、修正 migration 时间戳保留、约束与索引、aggregate/dev 语义等价。
- 禁区：algorithm、tests/algorithm、LazyLLM、Local/Desktop 与官方基线零差异。任务 2 尚未开始。
- 下一步：继续任务 1 service/errors/context 小批次，使首批服务测试从 compile-red 进入行为验证，再单独接 handler/chat 绑定。
