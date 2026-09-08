# 文档编写记录

## 当前远端交接状态

- 远端文档分支固定为 `origin/codex/local-workspace-handoff`，从官方 `upstream/main` 的 `TARGET_BASE` 建立；接手 agent 应从该分支读取本目录文档，再按主方案创建自己的功能分支。
- 本轮交付只包含本目录四份方案/证据文档，不包含生产代码或阶段一测试代码。接手 agent 必须从官方 `TARGET_BASE=245bc26dca1f2e8b56b0766cf72fdfcdb49138d9` 建立或复用功能分支，不能在含旧实现的 `feature/newWorkZone` 上继续编码。
- 本机隔离工作树曾创建三份未提交的阶段一测试草稿；它们不属于远端文档交付，不能被远端接手者视为已存在或已 Review。接手 agent 应按主方案任务 1 重新编写/核对测试，再遵守测试 Review 门禁。
- 当前有效边界：算法/LazyLLM 与官方零差异；Local/Desktop 只在任务 2 的单一批次内精简复用旧工作区接入，聚焦验收后记录 hash 并冻结；其余生产实现限 backend/frontend；任务 3A create/append 暂停。
- 代码量约束是验收条件：优先复用；局部小功能原则上新增不超过 1 个生产文件、净增约 200 行，超限先 Review；Local/Desktop 相对旧实现必须实质减量，不带回无关差异。

## 2026-09-08

### 编码执行交接（持续维护）

- 用户已授权按本目录方案开始实现，并再次确认：`algorithm/`、`tests/algorithm/`、`local/`、`desktop/` 必须恢复并保持与官方上游一致；功能只在 `backend/`、`frontend/` 实现。
- 原工作树 `/Users/zouyu/Downloads/LazyMind-main` 已在 `feature/newWorkZone` 拉取至 `b44a440cd938c5a0eb5b3be0ad6dc371e2f3c2df`，拉取前后均无未提交改动。
- 已从真正的 `upstream/main` 建立隔离功能工作树 `/Users/zouyu/Downloads/lazymind-workspace-backend-frontend`，分支 `codex/workspace-backend-frontend`，`TARGET_BASE=245bc26dca1f2e8b56b0766cf72fdfcdb49138d9`。
- 已确认旧 `feature/newWorkZone` 在算法和算法测试目录存在多处相对官方差异；这些差异不会提取到新功能分支。新功能工作树直接以官方目标基线为起点。
- 已确认相同策略适用于 `local/` 与 `desktop/`：不提取旧差异，不在功能实现中修改；Desktop 只复用前端已有 `selectFolder()`，Local 目录选择收敛到 Core。
- 当前正在执行任务 0：初始化官方 LazyLLM 子模块、验证冻结目录与官方零差异、核查原样算法读/改/建/追加能力。尚未开始生产代码实现。
- 本需求涉及 API、权限、数据库迁移与跨部署兼容，遵循仓库两阶段门禁：先编写/调整测试并提交阶段一证据，等待用户 Review 后才进入生产实现。
- 用户要求方案内的小改动也同步修改已有方案文档；已将持续维护规则补入 `IMPLEMENTATION_PLAN.md` 第 0 节。后续不允许出现“代码已变但方案仍旧”的未记录偏差。
- 已完成官方冻结目录校验：新功能工作树的算法、算法测试、Local、Desktop 及 LazyLLM gitlink相对 `upstream/main` 零差异。
- 已完成 macOS 原样算法工具级能力检查：读取和精确替换已有 `.txt` 成功；默认非 trusted 模式下，新建、嵌套新建、追加宿主文件均被 `write_file` 拒绝。证据已同步补入 `IMPLEMENTATION_PLAN.md` 14.1 与 `findings.md`；该能力缺口保持未通过。
- 任务 0 后端聚焦基线通过：`go test ./chat ./subagent ./common -run 'Test(BuildChatRequestBody|BuildLazyChatRequest|ReplaceAskUserToolResult|ApplyLocalFSPathsForChat|InterruptConversationStopsOnlyActiveTasks|ErrorCatalogCodesHaveTranslations)' -count=1`，三个包均成功。功能工作树没有 `frontend/node_modules`，前端既有组件基线尚未运行。
- 已进入阶段一并新增任务 1 首批测试：`backend/core/localworkspace/service_contract_test.go`、`backend/core/chat/local_workspace_schema_test.go`、`backend/core/migrate/local_workspace_migration_contract_test.go`。覆盖现有 runtime 门禁、授权/绑定用户隔离、Work/Chat 区分、撤销不降级、同路径重授权不恢复旧任务、通用错误码加 reason、元数据表结构、一任务一绑定、三组 dev migration 与 v0_3 aggregate 最终形态。
- 测试命令 `go test ./localworkspace ./chat ./migrate -run 'Test.*(Workspace|LocalWorkspace)' -count=1` 按预期失败：官方基线尚无 `orm.LocalWorkspace`、`ConversationWorkspaceBinding`、localworkspace 服务 API、两张表及三组迁移；未发现与本批需求无关的异常失败。生产功能仍未实现。
- 当前需要用户决策：是否接受 macOS/默认非 trusted 模式下“创建/追加宿主文件”保持明确未完成，或批准设计一个仅位于 `backend/` 的业务写入入口。后者会扩大当前方案架构，必须先更新方案、威胁边界与测试矩阵，不能默认实施。
- 用户批准先提供后端受控写入方案、Review 后再编码。已在 `IMPLEMENTATION_PLAN.md` 新增 3.5 与任务 3A：复用官方算法既有 `mcp_config`/MCP client，由 Core 注入系统管理的主任务专用 MCP；仅提供受限 UTF-8 文本 create/append，使用请求级随机 capability token，always_ask 通过服务端绑定操作的现有 AskCard 两阶段确认。方案明确不改算法/Local/Desktop、不启用 trusted、不新增依赖/迁移、不向子任务/Workflow 扩大写能力，并补充 A11 验收矩阵。当前仅完成方案，尚未编写该部分测试或生产代码，等待用户 Review。
- 已按用户要求 Review 3.5。结论：原版存在每请求 MCP header 导致官方算法缓存无界增长、run_id 生成时序不匹配、append replace 改变文件元数据、Lstat TOCTOU、preview 隔离、请求体上限和 MCP 错误合同等不足，暂不应实施。已在主方案 3.5.7 记录 P0/P1/P2 问题及推荐修订方向；任务 3A 继续暂停，等待用户确认修订边界。
- 用户已批准第一轮推荐方向并要求继续 Review。第二轮确认官方算法会把工具参数写入常规日志和可选遥测，且完整参数进入 `<tool_call>` 帧；因此在算法保持官方零差异时，Core MCP 无法同时满足“模型写入正文/授权材料不进算法日志”。同时补充了所有权限模式 prepare/commit、同进程幂等边界、pending 内存配额、随机进程 caller token、always_ask 跨轮消费和 append 风险分级。结果已写入 `IMPLEMENTATION_PLAN.md` 3.5.8 与 `findings.md`；未修改生产代码，任务 3A 仍暂停，等待用户决定暂停 create/append、允许极窄算法脱敏补丁或接受日志风险。
- 用户新增代码量约束：小功能不得膨胀为大量代码或多层抽象。已在主方案第 0 节加入复用优先和规模 Review 门槛：局部小功能原则上最多新增 1 个生产文件、净增约 200 行；必要例外须先报告 diff 规模和不可复用原因并重新 Review。后续每个任务都记录生产代码净增与复用点。
- 用户最新放宽边界：允许一次性沿用旧版工作区相关 Local/Desktop 实现，但接入后不得继续在其基础上修改。已核对旧分支差异为 26 个文件、约 +1029/-214 行且混有 Caddy 升级、assistant bridge 改写、Windows 脚本删除和测试删减，不能整体搬运。主方案 0.2 已改为逐 hunk 提取、验收后记录 hash 并冻结；无关差异及只服务旧算法回调的环境注入明确排除。该放宽不改变算法必须官方零差异，也不自动解决 create/append 缺口。
- 用户进一步要求冻结前优化旧功能并尽量复用已有实现。已明确不照搬旧版约 165 行 Desktop 第二套目录选择和 425 行 Local Proxy 业务/转发合集：Desktop 复用官方 `selectFolder()`/bridge，Local Proxy 复用既有 CORS、AdminSession、route proxy 与 JSON helper，Core 保持 DTO、错误 reason 和授权规则唯一权威；去掉官方算法不消费的 workspace token 注入。优化与接入同批验收后才冻结。

- 开始二次代码核查，使用 writing-plans 和 planning-with-files 工作流。
- 本轮只允许新增方案文档；不修改产品代码、不切分支、不提交、不更新 PR。
- 已记录原 PR 基线和必须废弃的旧跨层授权。
- 已完成请求/询问/停止/子任务生命周期核查，并整理为任务 0–7 的执行顺序。
- 已核查 Desktop selectFolder、AskCard 续问、ContextPrompt、StopChatGeneration、子任务 create/resume、通用错误详情和 OpenAPI 生成流程。
- 已发现并写入明确兼容性检查：默认非 trusted runtime 的 write_file 不能通过新增请求字段改写宿主目录。

- 已编写主交接文档 IMPLEMENTATION_PLAN.md：包含逐文件提取范围、接口合同、测试示例、验证命令、原样算法能力检查与最终边界/冻结检查。
- 已通过当前功能快照的后端聚焦测试（4 个 Go 包）及前端既有组件测试（2 个文件、9 个测试）；结果和局限记录在 findings.md。
- 已完成子任务恢复去重元数据及 OpenAPI cache 路径复核。产品代码、算法和运行配置均未修改。
- 文档复核通过：Markdown 围栏、嵌入 JSON/Python/shell 语法、任务 0–7、未实施状态均检查通过；git 差异只包含本目录四份文档。方案编写已完成，编码与真实平台验收仍未执行。

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

### T1-RED-1 运行矩阵（本机实际结果）

实际新增文件与规模：localworkspace/service_contract_test.go +195；chat/local_workspace_schema_test.go +138；chat/local_workspace_mode_contract_test.go +67；migrate/local_workspace_migration_contract_test.go +66（均位于 backend/core）。生产 +0/-0。四份交接文档同步更新；没有 Local/Desktop 提取或冻结提交。

以下 Go 命令均在 `backend/core`，前端命令在工作树根：

| 检查 / 命令 | 退出码 | 结果 |
|---|---:|---|
| `go test ./chat ./subagent ./common -run 'Test(BuildChatRequestBody|BuildLazyChatRequest|ReplaceAskUserToolResult|ApplyLocalFSPathsForChat|InterruptConversationStopsOnlyActiveTasks|ErrorCatalogCodesHaveTranslations)' -count=1` | 0 | 官方基线三个包通过 |
| `frontend/node_modules/.bin/vitest run --root frontend src/runtime/desktopBridge.test.ts src/modules/chat/components/AskCard/index.test.tsx` | 0 | 2 文件 11 测试通过；复用既有 node_modules 的 gitignored 链接，没有安装或修改锁文件 |
| `go test ./migrate -run 'TestRepository(SQLiteFreshAndUpgradePaths|SQLiteReleaseAndDevPathsMatch|PostgresMigrationPaths)$' -count=1 -v` | 0 | SQLite 两组通过；PostgreSQL 无临时 DSN，SKIP，不能算通过 |
| `go test ./localworkspace ./chat ./migrate -run 'Test.*(Workspace|LocalWorkspace)' -count=1` | 1 | 预期 red：缺失服务/模型编译失败；两张表不存在；六份 migration 不存在；Chat/Cloud 门禁缺失（500、一次派发、一次落库） |
| 原样算法 `/tmp/lazymind-workspace-evidence/check_tools.py` | 0 | 读/替换磁盘断言通过；三个宿主写调用预期拒绝；额外 atexit logger RuntimeError 单独记录 |
| `git diff --exit-code 245bc26d -- algorithm tests/algorithm local desktop evo workflows skills .github .gitmodules LAZYLLM_VERSION` | 0 | 禁区与官方零差异 |
| `git -C algorithm/lazyllm status --porcelain` / `git diff --check` | 0 | 子模块干净、空白检查通过 |

工具复核的可重跑命令（工作树根）：

```sh
PYTHONDONTWRITEBYTECODE=1 \
PYTHONPATH="$PWD/algorithm:$PWD/algorithm/lazyllm" \
LAZYLLM_HOME=/tmp/lazymind-workspace-evidence/lazyllm \
LAZYMIND_AGENTIC_WORKSPACE=/tmp/lazymind-workspace-evidence/internal \
/Users/theone/Downloads/lazymind/.venv/bin/python /tmp/lazymind-workspace-evidence/check_tools.py
```

本机日志：`/tmp/lazymind-workspace-tests.log`、`/tmp/lazymind-workspace-frontend.log`、`/tmp/lazymind-workspace-migration-baseline.log`、`/tmp/lazymind-workspace-evidence/tools.log`。临时日志未加入 Git；其他机器应重跑，不依赖它们存在。

未验证：任务 1 完整 Work 绑定/换绑、权限更新、身份变化、PostgreSQL/aggregate 新增合同；真实 ContextPrompt、模型询问、Local/Desktop/Windows 端到端；任务 2–7。不要以首批 red 代替全阶段完成。下一步等待本批人工 Review，再顺序推进任务 1；3A 保持暂停。


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
