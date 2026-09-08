# 前后端限定方案编写进度

- [x] 确认用户约束：算法零改动；通过后端数据和任务规则组织现有算法能力；本轮只写文档。
- [x] 核查上游已有接口、当前 PR 的可提取实现和跨层依赖。
- [x] 明确目录选择、请求上下文、权限规则、撤销/停止、子任务、接口生成的实现路径。
- [x] 编写独立可执行的 IMPLEMENTATION_PLAN.md，并记录可复用证据。
- [x] 检查文档中的接口、文件路径、验证命令、范围限制和未验证项。
- [x] 任务 0 实测确认官方非 trusted 模式的宿主创建/追加缺口，并将证据同步到主方案与 findings。
- [x] 根据用户授权补充待 Review 的 Core 受控写入方案：系统 MCP、请求级 capability token、受限 create/append、always_ask 单次确认和新增测试矩阵。
- [x] Review `IMPLEMENTATION_PLAN.md` 3.5，识别 MCP cache、run 生命周期、always_ask、append/TOCTOU、preview 与错误合同不足，并写入 3.5.7。
- [x] 用户确认 3.5.7 的总体推荐方向；继续第二轮 Review，核查工具参数日志、流式持久化、幂等、资源上限、跨轮确认和替代产物通道。
- [x] 将第二轮发现写入 3.5.8：官方算法参数日志与“正文不进日志”形成硬冲突；补充全模式 prepare/commit、同进程幂等、pending 配额及 append 语义。
- [x] 将用户的代码量控制要求加入权威约束：复用既有实现，局部小功能设置新增文件/净增行数 Review 门槛，每任务报告实际规模。
- [x] 核对旧 Local/Desktop 差异并按用户最新授权更新边界：只逐 hunk 提取工作区接入，排除 26 文件差异中的无关升级/删减；完成后记录 hash 并冻结。
- [x] 增加冻结前代码优化约束：Desktop/Local Proxy/Core 分别复用现有选择、会话代理和授权能力，移除重复业务层与旧算法 token 注入；优化、接入、测试同批完成。
- [x] 本次交接将 create/append 明确标为暂停项，不阻塞任务 1–7；接手 agent 禁止实施任务 3A。未来只有用户重新明确批准完整合同才能重开。
- [x] 整理远端文档交接状态：明确远端不含本机测试草稿、接手基线、Local/Desktop 单批冻结、代码量门槛和可直接复制的执行指令。

历史核查快照：旧功能分支 e7ed8a4189bb627e96814fc2f34818693cbc2050；原 PR 上游基线 2823ebe17b3cbbb944dca411176ebee6845b64a2。
当前实际编码基线：隔离工作树 `/Users/zouyu/Downloads/lazymind-workspace-backend-frontend`、分支 `codex/workspace-backend-frontend`、官方 `TARGET_BASE=245bc26dca1f2e8b56b0766cf72fdfcdb49138d9`。下个 agent 必须复用并先检查该工作树，不得从包含旧功能的 fork main 开始，也不得覆盖现有阶段一测试。

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

### 当前执行进度：T1-RED-1 等待人工 Review

- [x] 拉取并精确确认 aa53e969 文档分支；完整阅读四份文档和适用规则。
- [x] 从用户指定 245bc26d 建立 codex/local-workspace-core，保留原工作树。
- [x] 官方 LazyLLM 检出并确认禁区零差异。
- [x] 本机工具级读/改/创建/追加能力复核，创建/追加保持未通过，单独记录 logger 清理异常。
- [x] 后端聚焦基线、前端 11 项与 SQLite 既有迁移基线验证。
- [x] 任务 1 首批四文件 466 行测试与 red 分类；生产净增 0，复用点/命令/结果已同步主方案、findings、progress。
- [ ] 人工 Review T1-RED-1；服务测试被缺失 API 编译阻断，尚未运行内部断言。
- [ ] 完成任务 1 后续测试（Work 绑定/换绑、权限更新 CAS、身份变化、aggregate/dev 等价与 PostgreSQL）及经 Review 的小批实现。
- [ ] 任务 0 的真实 ContextPrompt 与完整部署场景验收（工具级结果不能替代）。
- [ ] 任务 2 精确接口与规模 Review，Local/Desktop 优化接入测试单批完成后记录 SHA-256/冻结提交/净增。
- [ ] 任务 3、4、5、6、7 按顺序测试、Review、实现及交接；3A 暂停。

当前没有 Local/Desktop 冻结清单或冻结点；代码量阈值未触发（无生产改动）。没有推送或修改 PR。


### 2026-09-08 T1-SCHEMA-1：ORM 与迁移生产批次

- 用户明确批准直接进入生产，并批准超出原 297 行 SQL 的最小修复批次；共享的六份旧工作区 migration 从文档快照逐文件提取并通过 `scripts/check_migration_immutability.py --base 245bc26d...`，未改写历史 SQL。
- 生产修改：新增 `common/orm/local_workspace_models.go`，在 `all_models.go` 注册两表；新增三组历史 dev migration 和一组 `20260908065108_fix_workspace_binding_timestamp` 修正 migration；更新既有 v0_3 aggregate；`migrate/run.go` 将声明 `PRAGMA foreign_keys=OFF` 的 SQLite migration 固定到单连接，在事务前暂停外键、事务内执行 `foreign_key_check`、所有出口恢复原设置。
- 清理：新事务 helper 替代 apply up/down 与 migration 测试 helper 的重复 Begin/Rollback/Commit，共删除 52 行旧事务代码；没有删除仍被 dialect 容错测试使用的 `execMigrationSQL`/column-change helper。
- 规模：本批生产约 +554/-42，净增约 512 行。其中不可压缩部分为八份获批 SQL（历史三对 214 行、修正一对 46 行）及 aggregate 83 行；runner +84/-42；ORM +44。未新增 manager、facade、DTO 或依赖。
- 双数据库验证：`MIGRATION_TEST_POSTGRES_DSN=... go test ./migrate -count=1` 通过；SQLite/PostgreSQL 独立 ORM schema 测试通过；`go test ./common/orm -count=1` 通过。测试覆盖 FK 原始 ON/OFF、SQL/历史/integrity 失败回滚、up/down、绑定数据保留、修正 migration 时间戳保留、约束与索引、aggregate/dev 语义等价。
- 禁区：algorithm、tests/algorithm、LazyLLM、Local/Desktop 与官方基线零差异。任务 2 尚未开始。
- 下一步：继续任务 1 service/errors/context 小批次，使首批服务测试从 compile-red 进入行为验证，再单独接 handler/chat 绑定。


### 2026-09-08 T1-CORE-2：Core 授权、绑定与 API 生产批次

- 用户在迁移批次后再次指示继续生产。本批完成任务 1 的 Core service/errors/context、Unix/Windows 目录身份、四个公开 handler/route，以及 Chat 请求前门禁和会话+binding 原子事务；没有实现任务 2 的本机 picker/token，也没有内部算法执行接口。
- 实际生产文件：新增 `backend/core/localworkspace/{service.go,errors.go,context.go,directory_identity.go,directory_identity_unix.go,directory_identity_windows.go,handlers.go}`、`backend/core/chat/local_workspace.go`；调整 `chat/conversation.go` 与 `routes.go`。生产 +706/-1，净增 705 行。测试增加 `chat/local_workspace_binding_contract_test.go`、`localworkspace/handlers_contract_test.go` 并扩展 service contract，共 +281。
- 估算偏差：服务层先估 380–450 行，最终增加到 705 行。不可复用部分为四个公开 API（249）、会话原子绑定/门禁（130）、三平台身份实现（84）、grant/snapshot/error（238）和路由（5）。现有 `store.DB`、`common.AppError/ReplyAppErr`、GORM transaction、`systemdeps.IsLocalRuntime`、Conversation ORM 和 mux route 均已复用；没有 manager/facade、通用框架、重复业务 DTO 或新依赖。
- 业务权威：Core 计算并保存目录身份；Register 的 source 仅为 local/desktop 展示元数据，不与 runtime 字符串比较。Resolve 每次检查 owner、Work、binding、grant status/version 和目录身份；目录被替换时旧 grant 单向变为 path_unavailable。撤销和权限变更使用乐观版本，权限响应明确 `effective_at=next_request`。
- API 行为：Chat/Cloud 带 workspace 参数时在任何下游调用和会话落库前拒绝；Local Work 首次创建时 Conversation 与 binding 同事务；已有任务不能新增/切换 binding。List、Binding、Revoke、Permission 仅访问 owner 数据；Binding API仍可显示 revoked 状态，执行 Resolve 则拒绝。
- 清理：List 从逐 workspace Count 改为单次 group 查询；新事务/错误 helper 仅在确有复用时使用。未删除仍被旧业务调用的代码。两次工具命令因在 `backend/core` 工作目录仍带仓库前缀而在写入前失败，已改用正确路径；一次 Windows `go test` 尝试执行 PE 文件导致 exec format error，改为 `go test -c` 验证编译。这些均未造成产品文件部分覆盖。
- 验证：`go test ./localworkspace ./chat ./migrate . -count=1` 全通过；PostgreSQL `go test ./localworkspace` 与 `go test ./migrate` 全通过；`GOOS=windows GOARCH=amd64 go test -c ./localworkspace` 生成有效 PE32+；迁移不可变、diff check、禁区零差异和 LazyLLM 子模块干净均通过。
- 未验证：真实 Local/Desktop 选择与授权转发、前端、ContextPrompt/请求增强、询问/停止、子任务/Workflow、OpenAPI。任务 3A 仍暂停。下一步严格进入任务 2，一次性完成 Local/Desktop 优化、接入、测试、hash 和冻结。


### 2026-09-08 T2-NATIVE-FREEZE：Local/Desktop 一次性接入完成并冻结

- 冻结提交：`ec4676e0d0fb290d81b3160a56e798849ea2d4e4`。此提交之后禁止修改下列 Local/Desktop 文件；发现问题先报告用户并等待重新授权。Backend/Frontend 后续任务不受此冻结影响。
- 实际生产规模：任务 2 全批生产 +756/-11，净增 745；测试 +498。Local/Desktop 生产部分约净增 686：Desktop main/preload、Local Proxy workspace/picker/server/CORS、Runtime Manager Core/Proxy caller token；其余为 Core内部入口和 Frontend bridge 类型。旧 Local Proxy workspace.go 为 425 行，最终 398 行左右；删除两份重复 directory identity 平台文件并由 Core统一计算 identity，合并 Core HTTP helper，复用 transport/AdminSession/writeJSON/requestFromLoopback/CORS/route 配置。
- 选择证明：Desktop candidate 绑定 webContents、AdminSession user、五分钟 TTL 和本机文件对象 proof；Local candidate 绑定 AdminSession user、五分钟 TTL 和 `os.SameFile` proof。authorize 只接受 selection token，消费后不可重放，注册前重新 realpath/stat。重新授权必须再次弹系统 picker并选择 Core记录的同一 canonical path；取消不创建 candidate/grant。
- Core继续是 grant、binding、permission、reason 和目录 identity 唯一业务权威。本机层只证明选择并转发 path/source；Core内部入口使用 runtime caller token、重新计算目录 identity，不向算法注入 token。
- 明确排除并由 diff 审计确认：Caddy版本、assistant bridge、Windows脚本删除、build测试删减、config.env、algorithm_service/token注入、旧 InternalResolve执行通道、Local Proxy重复 identity 文件均未带入。
- 冻结前验证：Local Proxy `go test ./...` 与 Windows test binary编译通过；Runtime Manager `go test ./...`（约65秒）与 Windows编译通过；Desktop node测试49项通过；Frontend Desktop bridge Vitest 11项和 TypeScript检查通过；Core localworkspace及根包通过；algorithm/tests/algorithm/LazyLLM零差异。
- SHA-256 清单：

```text
ada4ee40a6ec1edde0fc41f7a86d36157b70fcd4a359f19193a920b20119a47d  desktop/electron/src/main.js
bff62d69cffe94ac07302d48c7d5725030971b1b25269fed9565dddd05e986e1  desktop/electron/src/preload.js
ca42379a87872f70f2e6ea5d4a31caa35c0b570bf619741c90ae303b10a72ac1  desktop/scripts/local-workspace-contract.test.mjs
4febe148728ae644bb7db91cf59bba82b0ed201172f7d002de1789b07753c57e  desktop/scripts/preload-bridge.test.mjs
a30dac12f9f0e27f71cf68cb08e3d96f5435c77f8ff872bb5cb13f70262f6abe  local/local-proxy/internal/server/cors.go
886c459122cf348603981518fc640578efcb0464a059cc836d49f12b242302ef  local/local-proxy/internal/server/server.go
f0c048689f1b76b62bf34e6f5e30bef074cf3775bbbb4363fd39250339187774  local/local-proxy/internal/server/workspace.go
12f304f9073e2539fee6a79efd7be119f2726871735c80c814c0357a1585f11a  local/local-proxy/internal/server/workspace_picker_darwin.go
9d47b5c650e966441990273afb52cfd184820154c8ba7045c5d890069173be42  local/local-proxy/internal/server/workspace_picker_other.go
7f6858e5db2d2ceebf6aec4c279481bcbceeaa5516faec72c50cc6c540d88e75  local/local-proxy/internal/server/workspace_picker_windows.go
8e86a5697abb906395c4ccba9e79d74156d57513e80ade826e9d94c46b3c89e4  local/local-proxy/internal/server/workspace_selection_contract_test.go
292e3dfa12ab35448792092205df8b5e7925cbf85c05b2f2a50184734ffbc379  local/local-runtime-manager/core_service.go
94299dfda753f9b77eb4ca27964438ae0f2714b8707101576cf953a378850a12  local/local-runtime-manager/local_proxy.go
f9f7d345ee718b7783a0eda75b6727bd053ad04d505df109d8c3dd4e3f8d1822  local/local-runtime-manager/local_workspace_env.go
11f7f4a1918fcd24c43134a3afa9c2ed85fb31e359a48ac19273eebc001dca02  local/local-runtime-manager/local_workspace_env_test.go
```

- 实施异常：三次从子目录执行却带仓库前缀的写命令在首个重定向/读取前失败，没有部分写入；之后固定从工作树根执行。首次 hash脚本因 zsh未按换行拆分路径失败，改用 NUL分隔后生成以上清单。
- 下一步：任务 3 只修改 backend/frontend；Local/Desktop 文件冻结，不再触碰。任务 3A仍暂停。


### 2026-09-08 T3-CONTEXT：统一工作区请求快照

- 生产修改净增约 135 行，没有新增生产文件：扩展 `localworkspace/context.go`、`chat/localfs_paths.go`、真实 Chat/ContextPrompt/ContextUsage 最终组装点、history ext 和 `common/text_file.go`。
- `TextFileExtensions()` 返回现有扩展 map 的排序副本；保留既有 `IsTextFileExtension` 测试，因为它覆盖大小写、点前缀和二进制拒绝，新排序测试不能替代。
- 已绑定 Work 在 scan 前解析 Core snapshot并直接设置唯一 `local_fs_sources`，不调用全局 Scan Control Plane；无绑定才沿用原 scan。撤销、目录替换或跨用户错误保留 Core HTTP/code/reason，不降级。
- 新 Work preview用 workspace_id/permission_mode/run_in_background解析已有 grant，不落库。真实 Chat、regenerate/retry和 ContextPrompt/Usage均在附件转换后再次读取 snapshot，增强最终 query但保持 user_query/files/history；history ext只存 workspace/version/permission元数据，不存 root。
- 修正两个既有引用会话组装点的 history类型为 `[]map[string]any`，不重建或丢弃历史。
- 规模：总 +262/-13；测试增加纯函数、排序副本、绑定跳过scan、草稿不落库和 ext无根路径。期间误覆盖既有 text_file_test，审计时恢复并仅追加新用例；无有效旧测试被删除。
- 验证：`go test ./chat ./localworkspace ./common -count=1`通过；冻结 Local/Desktop SHA全通过且与 `ec4676e0` 零差异；algorithm/tests/algorithm/LazyLLM零差异。
- 未验证：真实模型 ContextPrompt内容、普通子任务/Workflow私有上下文、权限询问和撤销停止；由任务4–6继续。任务3A仍暂停。


### 2026-09-08 T4-SUBAGENT-PARTIAL：子任务上下文接入，专项验收待补

- 新增 `localworkspace/subagent_context.go`：无绑定保持原 params；绑定任务从 DB snapshot重建 parent_agentic_config的 owner/conversation/local_fs_sources，保存 Core私有原始 instruction元数据，从原文生成一次 subagent ModelNotice，避免 resume累加；忽略模型伪造的 Core metadata。
- `handleTaskCreated` create在落库前重建 params；resume校验 task owner/conversation，读取 DB中已有 params重建并先更新数据库再启动 runner。InternalGetExecutionSpec在 executor/lease认证后重建 private params，并把同一 JSON用于 task DTO和顶层 params；workspace_path仍为内部产物目录。
- 普通无工作区精简 fixture最初因缺少 conversations/workspace表返回503；修正为先检查 binding表和实际 binding，无 schema/无绑定原样返回，不隐藏真实绑定错误。
- 验证命令 `go test ./chat ./subagent ./localworkspace ./workflow/executor -run 'Test.*(Workspace|ExecutionSpec|Subagent|SubAgent|InterruptConversation|Context)' -count=1` 四包通过；冻结 SHA和禁区零差异通过。
- 本批未标记任务4完成：还需专用测试覆盖 create/resume DB Params、跨用户/跨会话 resume、revoked和permission version更新、private响应两份 params一致、公共 AttemptContext不含root。任务5–7未开始，3A暂停。


### 2026-09-08 T4-SUBAGENT：普通子任务与 Workflow 私有上下文验收完成

- 普通子任务 create 在落库前调用 `RebuildSubagentParams`；resume 校验 task owner/conversation，从数据库 Params中的 Core元数据恢复原始 instruction，重新读取最新 permission/grant并先更新数据库 Params再启动。模型本轮提交的 `_core_workspace_context`、伪造 Sources和 instruction不能覆盖数据库权威值。
- private execution-spec在 executor/lease认证后重建同一快照，顶层 `params` 与 `task.params` 深度一致；已撤销 grant返回409。内部 `workspace_path`保持原子任务产物目录，公共 AttemptContext未新增 canonical path/local_fs_sources。
- 无 binding或旧精简数据库 schema保持原行为；绑定任务保留 files/Skill等非工作区参数，notice在多次resume后只出现一次，并明确普通子任务不能直接ask_user。
- 专项测试覆盖 create/resume DB Params、permission version更新、伪造参数、跨用户恢复、revoked spec、private params一致和公共AttemptContext字段边界。
- 验证：`go test ./chat ./subagent ./localworkspace ./workflow/executor -run 'Test.*(Workspace|ExecutionSpec|Subagent|SubAgent|InterruptConversation|Context)' -count=1` 四包通过；冻结SHA全部通过，Local/Desktop未修改。
- 下一步：任务5询问与撤销停止。


### 2026-09-08 T5-LIFECYCLE：询问、权限与撤销停止完成

- 从 StopChatGeneration提取 `StopConversationExecution`，保持所有权、run decision/cancel signal、external chat、Workflow、普通子任务和 Python cancel链路；原 handler改为解析/回复，删除已无用的 log import和重复停止代码。
- localworkspace通过启动时注入的 StopConversationFunc调用 chat service，避免包循环。Revoke事务提交后使用独立15秒context停止所有绑定会话；停止失败不回滚授权，响应返回 affected_task_count、stop_requested、stop_failed_count。事务冲突不调用停止。
- bound workspace提交 ask_answers_structured时校验 ask_id必须匹配当前会话最后一个未回答AskCard；错卡片/无pending返回400。未提交、部分保存和权限模式切换不调用此提交路径；普通非工作区问答保持旧行为。
- 验证：`go test ./chat ./localworkspace ./subagent ./workflow . -run 'Test.*(Ask|Workspace|Stop|Interrupt|Cancel|Permission)' -count=1` 五包通过；冻结/算法边界通过。
- 下一步：任务6前端UI/API。


### 2026-09-08 T6-FRONTEND：工作区选择、权限与撤销 UI完成

- 新增 `utils/localWorkspace.ts`（72行）复用 axiosInstance和冻结的 Desktop token bridge，统一Local/Desktop选择、授权、重新授权、列表、binding、permission、revoke及Core reason读取；新增 `LocalWorkspaceControl.tsx`（141行）复用Ant Design Button/Select/Modal/Tag，不提取旧643行控件或504行样式。
- 新Work和existing Work均显示控件：ChatLayout从detail.is_task_conv识别已有任务。真实SSE、SendMessageParams和ContextUsage buildRequest携带workspace_id/permission_mode；staleKey包含工作区与权限变化。请求从不发送root。
- 授权必须先得到本机candidate token再由用户确认；取消不授权。allow_all显示四类副作用风险摘要；existing Work权限以后端返回version为准并提示下次执行生效。撤销显示影响任务数，stop_failed_count>0显示“授权已撤销，部分任务停止请求失败”。已撤销目录重新授权只产生新grant，不替换旧binding。
- 中英文locale均增加工作区文案。生产+260，测试+46；新增两个生产文件是UI与runtime/API职责隔离所需，未增加manager/facade/生成DTO/样式文件。
- 验证：工作区/desktop bridge合同17项通过；chatLayout/newChatContainer/AskCard 11项通过；修改文件ESLint通过；tsconfig.mcp通过；Vite production build通过。全量tsconfig.json仍有大量基线生成代码与implicit-any错误，本批不修复。
- 下一步：任务7最终回归、真实能力矩阵、范围和分支收敛。
