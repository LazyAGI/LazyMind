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
