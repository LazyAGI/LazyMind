# 工作区工具授权任务与验收

> 2026-09-10 状态更正：尚未完成全部非人工工作。最新全量前端检查为 134 项类型错误/64 文件，错误提示检查失败；状态存储 Get/SetNX 故障、提交后进程中断与实际任务入口仍有自动化缺口。已经通过的局部测试不等于完整验收，具体分项见末尾“限制分类与完成状态更正”。

> 2026-09-09 最新：用户确认的R3–R7生产及统一自动化测试已完成，结果/实际代码量/兼容限制见末尾“最终生产/自动化结果”；实际运行验收仍未完成。开发起点7bc81ffa，本轮相关代码与文档同批提交，未推送远端。

主方案：IMPLEMENTATION_PLAN.md，第 14 节为最新执行范围。用户已改为剩余功能全部生产完成后统一测试，原逐批 RED/人工 Review 顺序为历史记录；原超量 Review 和冻结边界继续有效。A2-R2 已提交 69e4809e。本轮生产/测试 diff 为 0，等待整体规模 Review。

## 当前状态与总门禁

- [x] 原 U1–U7、C1–C2 自动化补齐；历史最近前端矩阵 49/49。
- [ ] 原 T6：真实 Local/打包 Desktop UI、必要数据库及平台验收。
- [x] 算法侧新方案源码核对和修改边界确认。
- [x] 注册、中间件、Core HTTP/state、子任务/Workflow 和旧权限定义审计，设计写入现有方案。
- [x] 用户已 Review 原具体设计及 A1/A2 生产范围；本批 A2-R1 修复范围见下方，继续保留逐批 Review 门禁。
- [ ] 每个实施批次先建立失败合同，报告预期失败/异常失败，Review 后再写该批生产代码。

全流程在本会话执行；不创建子任务、worktree、第二仓库或新分支。下面是验收和修改步骤，不是预先批准的生产 diff。

## A0：首批测试合同（生产净增 0）

文件使用已有 `tests/algorithm/chat/test_tool_registry.py`、`test_agent_executor.py`、`test_tool_call_guard.py`、`test_subagent_runner.py`，以及 `backend/core/localworkspace/service_contract_test.go`。按下表逐项添加；测试只替换 HTTP/身份/等待边界，工具执行器和实际副作用计数使用真实路径，不 mock 掉中间件。

| 合同组 | 输入与可观察断言 | 预期基线结果 |
|---|---|---|
| 注册传播 | local_fs 各方法映射 read/write/delete；Toolkit 延迟激活后映射仍正确 | 新元数据合同失败 |
| 无旁路 | 同名自定义工具不继承内置授权；未知宿主工具在绑定任务中不执行 | 新阻断合同失败 |
| 执行前判定 | allow 执行一次；deny/pending 未决定时副作用计数为 0 | 新授权合同失败 |
| 原调用恢复 | 批准只恢复原 call_id/参数；拒绝、超时、取消均不执行 | 新恢复合同失败 |
| 重试/批次 | 混合允许/拒绝保持原索引；不同追加调用不按相同参数合并；传输重试不重复副作用 | 新工作区合同失败；现有普通工具重试用例应通过 |
| Core 决策 | 方案第 5 节权限表；allow_all 仍拒绝越界/失效/敏感写/.git 写 | 新具体操作决策合同失败 |
| 身份与权限变化 | 跨 owner/conversation/旧 run/失效 attempt 拒绝；切权限不自动批准 pending | 新合同失败 |
| 兼容 | 无绑定普通工具、AskCard、内部产物、Workflow 未声明工具的原限制保持 | 既有回归应通过 |

关键失败断言范式（在已有 ToolManager fixture 中增量实现，不新增生产替身）：

```python
# 单次调用正在等待决定时，由测试中的可控 Event 检查：
assert entered_authorization.wait(timeout=1)
assert disk_effects == []
# 测试随后令 Core 边界返回拒绝，等待真实中间件结束：
assert batch.records[0].disposition is ToolExecutionDisposition.SKIPPED
assert batch.records[0].reason == 'approval_rejected'
assert disk_effects == []
```

- [ ] 核对本机 Python/pytest 依赖，使用仓库要求的现有环境；不能假定历史 `/tmp` 探针仍存在。
- [ ] 先运行现有矩阵，记录本次基线，再加入首批合同。
- [ ] 重跑，逐项记录断言失败及缺失接口；导入/依赖/fixture 异常单列并先修复测试运行条件。
- [ ] 用有界 Event/超时同步，禁止靠长 sleep 测试“尚未执行”。
- [ ] 报告真实 RED 数量和原有通过数量；用户 Review 前生产代码保持零增量。

命令（仓库根目录，python 为本次核实的项目解释器）：

```sh
python -m pytest tests/algorithm/chat/test_tool_registry.py tests/algorithm/chat/test_agent_executor.py tests/algorithm/chat/test_tool_call_guard.py tests/algorithm/chat/test_subagent_runner.py -q
```

Core 目录：

```sh
go test ./localworkspace -count=1
```

## A1：授权判定和受控派发

接口遵循主方案第 3–4 节；实现范围为 ToolConfig/每个执行器的元数据索引、主/子任务可信上下文、Core prepare/状态/decide 和中间件阻断及等待。复用 core_api_client 与 store.State，不新建 client、manager、通用 coordinator。

- [ ] A0 Review 后实现最小判定和原调用控制；没有生产 fake allow、内存兜底批准或“暂时先执行”分支。
- [ ] 新增的 Core 状态只保存本功能必要字段，handler/service 共用一份类型，短期限与条数有界。
- [ ] 初始 allow、明确批准、拒绝、取消、过期形成可测试状态转换；状态后端失败一律不执行。
- [ ] 将相同参数副作用调用的执行与普通工具 FailureRetryPolicy/重复观察区分；不改动无绑定行为。
- [ ] 执行原矩阵和 Core 包回归，报告实际新增/删除/净增及未连通项；批准 UI 未接入前不宣称可交付用户使用。
- [ ] 更新四份文档、提交本批相关文件，进入下一批 Review。

## A2：Core 文件操作与路径/版本保证

新增生产文件限制为主方案报告的 `backend/core/localworkspace/operations.go`、`approvals.go`；前者负责磁盘访问，后者负责操作批准/回执，两者不再增加 facade。算法只修改既有 local_fs.py 转发；现有普通数据源路径与内部产物行为保持。

测试优先落点：新增 `backend/core/localworkspace/operations_test.go`、`approvals_test.go`；扩展既有 `tests/algorithm/chat/test_local_fs_tool.py`、`test_core_api_client.py`。

- [ ] 先建立临时真实文件树合同：create→read→append→read→replace→read→delete，输出及磁盘逐步一致；mkdir 显式执行。
- [ ] create 已存在、修改/删除缺失或旧版本、追加重复请求、大小上限、精确替换匹配错误分别失败且内容保持。
- [ ] 列表/搜索/info/read 均回查绑定和路径；敏感 grep 不先读取再批准，失败无敏感内容泄露。
- [ ] 建立根与父目录替换、symlink、特殊文件、Windows 别名、外部并发写、提交前撤销、审批期间文件变化的屏障同步测试。
- [ ] 建立 SQLite 状态下双消费者、重复批准、旧 ID、缓存到期、执行崩溃后不重放追加测试；使用现有 Redis 环境时补同等合同，不临时引入新服务。
- [ ] 先报告无法证明的平台原语/外部并发项；不实施削弱版“realpath 后直接写”以通过其余用例。
- [ ] RED Review 后实现受控操作，原调用携带 Core 操作句柄；结果包含 version/reason，内容不进入批准状态。
- [ ] 移除本批实际替代的工作区提示词授权分支；保留普通问答及非工作区工具实现和测试。
- [ ] 运行下列矩阵并报告真实代码量，更新四文档后提交。

```sh
# 仓库根目录
python -m pytest tests/algorithm/chat/test_local_fs_tool.py tests/algorithm/chat/test_core_api_client.py tests/algorithm/chat/test_tool_call_guard.py -q
# backend/core
 go test ./localworkspace ./chat ./subagent -count=1
 go test -race ./localworkspace -count=1
```

## A3：批准界面与主/子/Workflow 拉通

修改现有 LocalWorkspaceControl、workspace utility 和中英文 locale，复用 Modal/请求代次；不添加新的聊天 SSE 协议、审批消息模型或 AskCard 工具批准分支。

- [ ] 先扩展现有 LocalWorkspaceControl.test.tsx/localWorkspace.test.ts：待批准摘要、允许一次/拒绝、超时、重复点击、刷新恢复、切会话/迟到结果隔离。
- [ ] 列表只显示当前 owner/conversation 的活跃操作，展示来源主任务/子任务/Workflow 步骤；批准按钮不把“请求成功”当作“文件执行成功”。
- [ ] RED Review 后接入 Core 查询和决定；有界轮询，隐藏/卸载停止，恢复显示重查。
- [ ] 真实主任务与普通子任务批准后都继续原轮次；并发子任务批准不会串 call_id。
- [ ] Workflow 使用真实声明工具、活跃 attempt/lease；等待时 heartbeat 继续，lease 丢失时旧批准不能执行。
- [ ] 未声明工具、自定义包同名覆盖、未受控 Python/shell/MCP 的拒绝有明确产品结果；无绑定 Workflow 原行为不变。
- [ ] 重新运行前端 6 文件矩阵、相关 ESLint、TypeScript、构建，Core 和算法新旧矩阵；不沿用历史通过数。
- [ ] Local/打包 Desktop 各自实测同轮读建改追加删、拒绝、冲突和撤销；不能用开发服务器或组件 mock 替代打包验收。
- [ ] 统计总生产净增和新增文件，核对仅相关 backend/frontend/algorithm 项目文件及四文档，更新进度后提交。

```sh
# frontend
NODE_OPTIONS=--no-experimental-webstorage pnpm exec vitest run src/modules/chat/components/ChatInput/LocalWorkspaceControl.test.tsx src/modules/chat/components/ChatInput/LocalWorkspace.contract.test.ts src/modules/chat/components/ChatInput/index.test.tsx src/modules/chat/components/AskCard/index.test.tsx src/modules/chat/utils/localWorkspace.test.ts src/runtime/desktopBridge.test.ts
pnpm exec tsc -p tsconfig.mcp.json --noEmit
NODE_OPTIONS=--no-experimental-webstorage pnpm run build
```

边界检查：`git diff --check`；Local/Desktop 对 `ec4676e0` 的 diff 必须为空；LazyLLM gitlink 对 `245bc26d` 相同且子模块内部无改动。项目算法/对应测试的变化逐文件审查，不能再用“algorithm 全目录零差异”阻止已批准范围。

## 最终验收状态

- [ ] 读、创建、覆盖/精确修改、追加、单文件删除及发现工具在同轮返回真实结果。
- [ ] 三档权限、单次批准、取消/超时、目录/敏感/.git/版本/撤销、重复请求和未知提交结果均有可复现证据。
- [ ] 主任务、普通子任务、已声明工具的 Workflow 均通过；自定义代码受限范围如实说明。
- [ ] Local/打包 Desktop、SQLite、必要 Redis/数据库与平台证据按实际环境分别列出。
- [ ] 只移除被替代且无其他使用者的旧代码，保留其他产品行为和回归。
- [ ] 四份文档记录实际文件、规模、复用、命令、结果和未验证项，满足交接。

## Review 修正项（2026-09-09）

- [ ] 在 A0 增加 Workflow 发布脚本 `exec(compile(...))` 之前的准入失败合同：绑定工作区时，未审计脚本顶层文件访问必须在加载前被阻止；补 import-time 副作用测试。
- [ ] 在 A1 明确中间件创建不可伪造的原调用上下文（call_id、参数摘要、任务身份、operation_id），批准恢复不得通过模型参数或工具名猜测。
- [ ] 在 A1 以状态转换测试证明 pending/allowed/rejected/expired/executing/completed/failed/uncertain、单次消费和并发消费者；不能把 `SetNX` 当成完整状态机。
- [ ] 评估 Core 四种操作语义是否由一个 service 统一实现，减少重复 DTO/handler；保留语义和可读性，不为少文件删除并发测试。
- [ ] A0 报告普通子任务和 Workflow 实际执行路径的源码/测试证据；未证实前不写“全路径已接入”。

## A0 执行记录（2026-09-09）

- [x] Core 基线：`go test ./localworkspace -count=1` 通过。
- [x] 算法可运行子集：中间件既有合同 17 项通过；A0 新合同 4 项预期失败，异常失败 0。
- [x] 已修正 A0 静态测试自身异常（Workflow 缺失 helper 改为断言缺失，而不是抛 `ValueError`）。
- [ ] 完整算法注册/子任务矩阵：当前被 `.venv` FastAPI 与系统 Pydantic 版本冲突阻塞，需在不改仓库依赖的前提下选择正确项目运行环境后再补跑。
- [ ] 人工 Review A0 RED 与设计修正；Review 前不修改生产代码。

## A1 执行记录（2026-09-09）

- [x] 实现最小 ToolConfig 授权字段，local_fs 方法映射 read/write/delete。
- [x] 实现中间件可插拔 gate；只允许明确 allow，deny/异常/未知状态均不进入底层 ToolManager。
- [x] AgentExecutor 将 gate 传入中间件；补传播测试。
- [x] Workflow 绑定工作区时在脚本 `exec(compile(...))` 前执行加载准入；无工作区路径保持兼容。
- [x] A1 矩阵 52/52 通过，`py_compile`、`git diff --check` 通过，冻结边界未变化。
- [x] 实际生产净增约 66 行、5 个既有算法文件；新增 1 个测试文件；无生产新文件。
- [ ] Core gate 尚未注入；未实现 pending 等待、Core 状态机、磁盘文件执行、批准 UI，因此不能宣称工作区文件授权闭环。

A2 开始前继续遵守测试先行和人工 Review；Core 文件执行与批准状态仍按方案分开实现。

## A2 首轮测试记录（2026-09-09）

- [x] 新增 `operations_contract_test.go`：锁定读、创建、追加、替换、删除、expected_version、路径/撤销/版本/符号链接/.git/原子替换合同。
- [x] 新增 `approval_routes_contract_test.go`：锁定 Core 内部操作路由和用户决定路由。
- [x] 运行 `go test ./localworkspace -run 'Workspace(Operations|Approval|OperationRoutes)' -count=1`：4 个测试失败、0 个异常失败，全部为预期 RED。
- [x] Review A2 RED 后实现 Core 单一 operation service、批准状态机和真实文件操作；不得先写“临时允许”分支。


## A2 基础实现与待修复项（2026-09-09，更正原完成记录）

- [x] Core 基础文件操作、operation 状态结构及四条操作/决定路由已提交；历史测试只证明当时覆盖的场景。
- [x] LocalFileToolkit 的 read/string_replace/create/append/delete 有 Core 转发分支；不代表发现工具也已接入，call_id 幂等未证明。
- [ ] 完整权限表、目录竞态、撤销协调、单次执行、HTTP 身份边界与失败恢复验收；本次仅覆盖下列 12 项行为用例。
- [x] A2-R1 测试批次 cff178a1：在既有 operations_test.go 增加 12 项行为用例，净增 168 行，生产净增 0；RED 为 43 通过、5 预期失败、0 异常失败，原有 36 项通过。
- [x] 用户“直接生产吧”批准 A2-R1 最小修复；仅 operations.go，生产新增 13/删除 11/净增 2 行，无新生产文件。
- [x] 修复敏感读决定、锁后状态复核、failed 禁止重试、completed 读取返回无内容回执；新增 12 项全部转绿，未删除或改弱断言。
- [x] `go test -race ./localworkspace -count=1`、`go test ./chat ./subagent -count=1`、`go vet ./localworkspace` 通过；四份文档同步实际 diff 和未验证项。
- [ ] A2 后续：批准前内容读取、路径/外部编辑/撤销竞态、原子解锁及租期、prepare 幂等与 uncertain，分别先补真实合同；本批不宣称这些问题已解决。
- [ ] A3：把真实 Core gate 注入 middleware，pending 在原 Agent 调用中等待/恢复，批准 UI 和刷新/切会话隔离。
- [ ] A3：证明普通子任务、已声明 Workflow、lease/取消/重启、uncertain 语义；完成 Local/打包 Desktop 实测。

A2 基础实现生产净增 893 行、2 个新生产文件；A2-R1 修复生产净增 2 行，合计 895 行。测试批次净增 168 行，本次只调整一行测试注释，不以拆批隐藏累计规模。

## A2-R2：领取过期与非原子释放

- [x] 基于 b76d18f4 重新运行工作区基线，通过；新增既有 operations_test.go 156 行、approvals_test.go 63 行，无生产修改。
- [x] 新增 9 个行为用例：5 通过、4 预期失败；完整包 race 矩阵 53 通过/4 失败，原有 48 项通过，最终异常 0。首轮漏导入 state 已修正并记录。
- [x] 用户已批准第 13 节一次性消费方案；仅 operations.go/approvals.go，实际生产 +30/-35、净减少 5 行，无新生产文件；领取前验身份/动作，领取后复核状态和有效期，不按互斥锁释放或接管。
- [x] 删除无调用者的 claimOperation/releaseOperation，保留底层通用 state 接口；删除仅检查状态/原语字面的旧源码合同 14 行，实际过期/uncertain 完整验收仍未完成。
- [x] 补充状态 Set 失败后的两项合同（+56 行），先复现 RED；本次 11 项全部通过，工作区 race、聊天/子任务回归和 vet 通过。
- [x] 本批只读代码审查无 critical/important 问题；采纳测试包装器移除无用可选接口的建议，核对文档/冻结边界/diff 后提交，不推送。
- [ ] 下一批整体制定真实 call_id/run/task/attempt 与 prepare 幂等、过期/运行结束拒绝合同；不得只加参数哈希缓存而合并独立调用。
- [ ] R2 不覆盖目录竞态、运行生命周期、混合版本部署、Redis/跨进程/实机与完整崩溃恢复；这些项目继续保留，不能因新增用例转绿自动完成。


## 剩余功能整合（替代逐小批测试门禁）

- [x] 核对唯一仓库状态；读取实际 ToolManager/项目中间件、Core 主/子任务与 Workflow 状态及前端工作区入口。
- [x] 只读 agent 核对三种运行身份，确认主任务缓存先于 ChatHistory、子任务无代次、Workflow lease 未传递；没有修改生产或运行测试。
- [x] 汇总实际授权阶段生产净增 950 行；剩余预计净增 900–1,400 行、约 30 既有文件、0 新生产文件，完整范围/复用/验收写入方案第 14 节。
- [ ] 原第 7 条整体规模 Review；获批后连续生产，不再为 R3–R7 各自测试请求重复确认。
- [ ] R3：主运行及取消权威、子任务独立执行代次、Workflow lease/代次私有传递；原调用句柄、prepare 幂等、5 分钟批准与 24 小时回执/去重。
- [ ] R4：ToolConfig 的真实实例/方法匹配、同调用批准等待/恢复、取消与结果记录；不修改 LazyLLM 或运行时 monkeypatch。
- [ ] R5：发现/读写/目录全部 Core 转发、授权前不读敏感内容、目录句柄、版本/提交/uncertain 收尾；外部竞态不能证明的项目如实保留。
- [ ] R6/R7：会话有界 pending 查询、原控件批准交互/状态；Workflow 加载前拒绝和旧提示词清理。
- [ ] 所有生产修改完成后统一编写/运行方案第 14.6 节测试矩阵，修复实际缺陷并做最终代码 Review；实机/平台无证据的项目保持未验收。
- [ ] 每个实际实现节点更新四份文档和累计规模；仅提交相关文件，不推送或新建 checkout。

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

本批生产增量为 31 行净增（`workspace.py` 13、`mail.py` 5、`tool_registry.py` 13；按实际 diff 统计），测试四文件净增579行；没有新增生产文件、依赖、服务或表，也未修改 Local/Desktop、`algorithm/lazyllm` 或 gitlink。

本次新鲜验证：

- Python 重点集合：`174 passed, 1 skipped, 13 warnings`；skip 是未提供真实 HTTP fixture 的可选测试。
- Core 全量：`go test ./... -count=1` 通过；重点包 `chat/localworkspace/subagent/workflow` 通过。
- Core↔算法真实 HTTP：`TestWorkspacePythonCoreHTTP` 的 main、subagent、workflow 三个叶子均通过（SQLite 临时数据库、临时目录，不调用模型或登录服务）。
- 前端工作区用例：6 个文件、68 tests 通过；生产构建通过。
- 前端完整 `tsc --noEmit` 仍有仓库既有错误（2026-09-10重新执行为134项/64文件，涉及生成客户端和多个页面；成因与影响待逐项核实）；`check:error-prompts` 仍有既有全局违规。没有用排除配置掩盖。
- PostgreSQL+Redis 专属临时实例验证已重新运行并通过：localworkspace 全套通过，chat 的 main identity、workflow identity、并发 prepare/approve/execute、16 槽容量、撤销围栏和跨进程单次消费均通过；实例和数据目录已销毁，未连接用户数据库。

仍未宣称完成的自动化/运行态边界：真实登录、模型驱动的主流式/非流式/双回复身份注册、真实 FastAPI 子任务启动、Windows/打包 Desktop、用户目录选择和跨平台符号链接/外部编辑器并发。其中实际任务入口可继续补自动验证；目录选择及平台行为须按真实环境验收，均不能由当前HTTP fixture代替。工作区文件二进制传输、任意脚本/shell/MCP 宿主执行以及旧 Writer/media 全量注册仍保持拒绝或待专项设计；没有通过提示词或 trusted 绕过。


本批冻结记录（提交 `a2f1d558c3d70253450a1fa7b299f660e31dc966`，冻结基线：Local/Desktop `ec4676e0d0fb290d81b3160a56e798849ea2d4e4`，LazyLLM `2e3d00ac3ae4ae983cb7d0ca4bd231c1a56ebfc0`）：涉及文件 SHA-256 已核对为 `workspace.py=056b0f112c3e5f11528baa9cc5bca141a08fa5b4cf5aa60cddf8db100aa027c8`、`mail.py=5217a4e8de2e5599a1477e83e4b6ab0b34b8c56321642073c34f668e2d326da2`、`tool_registry.py=cab5e28bf6d11511a6a6079bb9264e41a6fbfcc59d470fb5aa6d8994db82d4f5`、`run_decision_test.go=cb9b186804243e49383c5289424910260bd9a110a42129ad4330bddd52566619`、`test_mail_toolkit.py=5be3d5c188d391a0ecc0b5de37c84039ec9defc96ca29cb4a7893bc92160c732`、`test_tool_registry.py=4726ca2171481bae9c89bf3a33181472eff75f753b2ebee618dd1e0c0e3d0295`、`test_workspace_authorization_contract.py=03600fc7a0033a6691231624d07145e78b63e59c04e3ea94254ef755f62a8b0d`。


## 2026-09-10 限制分类与完成状态更正

用户询问这些限制能否修复。重新核对后，前次“非人工工作已完成”的结论不成立，不能把可自动化补齐的项目移交为人工验收。

| 类别 | 当前事实 | 后续处理 |
|---|---|---|
| 工程检查问题 | 本次实际运行 `pnpm exec tsc --noEmit`：退出2，134项/64文件；`pnpm run check:error-prompts`：退出1。此前573项数字已过时 | 核实成因和影响，功能相关问题继续修复；跨模块修复先报告范围，不削弱检查或删除失败覆盖 |
| 自动验证缺口 | 已有真实HTTP使用fixture播种身份；缺少部分实际主流式/非流式/双回复和子任务入口的完整贯通验证；Get/SetNX故障与提交后进程退出测试尚未补齐 | 应在既有测试文件补齐；多进程正常消费成功不能代替进程中断恢复验证 |
| 兼容性未完成 | 部分Writer/media/skill读取方法在绑定工作区后仍受限 | 优先复用原工具并逐项校验路径，不能直接扩大准入；需要额外代码量时遵守已批准预算和Review门槛 |
| 新权限/能力设计 | 本机二进制文件经Core传递到图片/PDF等工具、任意脚本/shell/custom MCP宿主执行不属于当前已验证文本文件通路 | 可研究扩展，但不能声称仅加注册字段就安全；不修改冻结层、不依赖trusted或注入绕过 |
| 平台和人工验收 | 真实目录选择、打包Desktop、Windows行为、跨平台外部编辑器竞争未验证 | 按实际环境验证；未验证不等于已知无法实现，也不等于测试通过 |

本次只核对、纠正文档，未实施新生产修复。此前功能提交 `a2f1d558` 实际生产三文件 +39/-8，净增31行；测试四文件 +584/-5，净增579行（其中Python净增173行），更正旧记录的25/174统计。Core/算法HTTP及数据库测试原通过结果保留其明确范围；不将有限路径检查表述为已经解决外部进程竞争。


## 2026-09-10 一次性补齐非人工工作：进行中

用户明确要求剩余非人工工作连续做完；本轮起点 `3a5181e4`、工作区干净，只使用现有仓库和分支。持续推进代码/测试，不重复请求测试顺序批准；不改变Local/Desktop/LazyLLM冻结，也不放开未设计的任意宿主执行。

- [x] 错误提示扫描修复：`frontend/scripts/i18n/check-error-prompts.mjs` 使用现有TypeScript解析器识别真实catch范围，保留失败提示规则；对应新测试 `frontend/scripts/i18n/check-error-prompts.test.mjs` 3项通过，覆盖嵌套catch、字符串括号及目录错误/客户端校验提示。
- [x] 前端真实请求catch改用现有 `components/request.ts#getLocalizedErrorMessage`；fetch失败保留response结构供HTTP状态映射，不把状态拼进用户错误文字；Channel错误列和Workflow生成失败说明改用错误目录，保留阶段/结构化路径。`node frontend/scripts/i18n/check-error-prompts.mjs` 通过。
- [ ] 类型错误134项/64文件正在处理；优先修类型、删除已核实未使用代码，不加any、排除路径或生产依赖。与错误提示修改按hunk协调。
- [ ] Core Get/SetNX故障、提交后进程中断防重放、多进程启动屏障及实际任务入口测试正在补齐，未将编译或skip计作验收。
- [ ] Writer/media/skill兼容性最小方案只读核查中，先确认原内部资源路径可复用，再统计精确增量。
- [ ] 完成整合后重新运行数据库/HTTP/Python/前端/构建与边界检查，独立Review、提交与推送。

当前修改尚未提交，规模随类型收尾变化；本轮无算法生产修改、无新生产文件/依赖/服务/表，测试新增文件1个。扫描器修复不等于全前端验收，后续需完整tsc及页面回归。历史临时日志不是交付依赖，复现命令已按仓库路径记载。


### 本轮Core与算法启动验证完成节点

- `backend/core/localworkspace/operations_test.go`：4类Get/SetNX故障注入已验证；使用唯一29字符会话ID，修正首次PG运行因测试ID超出varchar(36)而失败的问题（测试夹具问题，不修改数据库schema）。
- `backend/core/chat/run_decision_test.go`：真实handleNonStreamChat/handleStreamChat单流/双回复入口验证上游收到请求前运行身份已经注册；结束后prepared、receipt、新prepare均失效。`TestWorkspaceExecutionProcesses` 的六进程增加ready/start屏障和有界超时，另注入completed回执保存前进程退出73；先确认文件已append，再恢复临时文件到原期望版本后重放仍被拒绝，避免版本冲突掩盖消费保护。
- 独立Review提出的“旧版本可掩盖重放保护”缺口已修复并回看通过。SQLite定向9子场景三遍通过；root使用专属临时PG/Redis执行 `go test -race ./localworkspace -count=1 -timeout=180s` 及 `go test -race ./chat -run '^TestWorkspace(ChatEntrypointsRegisterAndFinishRuns|MainIdentityRequiresRegisteredLiveRun|WorkflowIdentityRequiresCurrentOwnedLease|BackendIntegration|ExecutionProcesses)$' -count=1 -timeout=180s -v` 均通过。实例和临时目录已删除，无用户库改动。
- `tests/algorithm/chat/test_subagent_runner.py` 新增FastAPI ASGI请求实际进入runner，校验初次/恢复/缺身份请求以本次私有身份为准，不继承parent/已持久化后代次；私有字段不进入提示词或SSE。使用已有FakeDB和确定性executor，不调用远端模型，不称为真实模型验收。
- `algorithm/tests/chat/workflows/test_remote_executor.py` 增补断言Core claim的task/attempt/generation/lease原样私下传给runner，未放入模型params。标准 `.venv/bin/python -m pytest tests/algorithm/chat/test_subagent_runner.py algorithm/tests/chat/workflows/test_remote_executor.py -q`：43通过。
- 初次FastAPI导入因现有本机环境缺少仓库已声明的RAG测试依赖失败；已用uv补齐 `tests/algorithm/requirements-test.txt` 与runtime声明的pandas/openpyxl/docx/pptx等，遵守冻结LazyLLM既有spacy/bm25s约束及setuptools<80。仅测试venv变更，没有新增生产依赖声明或修改冻结层。

剩余类型/兼容整合与全量前端测试仍在进行；当前通过的本节点不代表全目标完成。


### 类型、兼容与广回归节点（仍在执行）

- 全量前端 `pnpm exec tsc --noEmit` 已退出0，原134项已清除；未关闭strict/排除检查、未添加生产依赖。清理真实未用MarkdownEditor、批量上传旧类/store及其专属test alias/mock；消息事件使用收到的e而非浏览器全局event；侧栏复用SidebarConversationNode保持缺父节点占位行为。
- OpenAPI源头补齐：Core既有技能list/detail实际返回的auto_evo、is_enabled两项；通过 `go run . --export-openapi-to <repo>/frontend/scripts/openapi/specs/core.yaml` 与现有generator更新client。Scan复用已有Compensation响应schema并补源码缺失的JobError schema；新增schema引用解析与技能字段类型契约通过。四套OpenAPI fresh、1908错误码同步检查通过。
- 错误提示Review提出的Promise.catch漏检及Workflow误用通用字典已修复：catch扫描覆盖arrow/function/expression，4项Node测试通过；Workflow共用专用diagnostic翻译helper，保留结构化标识并补齐中英文30码，与当前compiler集合匹配。6项语言回归中首次用UNKNOWN作未知码夹具失败（UNKNOWN实际已在目录中），已改为真正不存在的码并6项通过。
- 算法兼容初稿9个生产文件净+124、无新生产文件：纯生成/媒体输入/Writer/资源profile/skill reader复用原实现，manifest路径及绑定身份收敛。独立安全Review又发现data包装规范化和factory捕获对象两项P1，正在修复；未将初稿通过测试作为安全完成结论。
- root广回归 `pytest tests/algorithm/chat algorithm/tests/chat/workflows` 首轮：1961通过、21失败、1skip、1error。新增executor测试首次建立globals键后pytest删除键的清理不兼容已修；pipeline FakeAgent补真实manager/skill属性，文本合并测试固定其模块时钟排除调度抖动；相关51项通过，保留2秒启动门槛。其余Workflow失败正在区分旧合同与平台字体夹具，不删除有效失败断言。
- 全前端首轮7套失败/126通过；修复模拟浏览器API、partial i18n mock和Writer现行参数/响应夹具后，第二轮133套通过，仅RecordList错误翻译mock重置问题失败。该问题正在收尾，未宣称全量通过。
- Core全量83包通过、5无测试、vet通过；Scan全量通过。生产构建通过。整合/安全Review未结束，尚未提交。


### 前端完整回归通过节点

完整 `NODE_OPTIONS=--no-experimental-webstorage pnpm exec vitest run --maxWorkers=4 --minWorkers=1`：134套、856项全部通过，无未处理异常。`pnpm exec tsc --noEmit`、项目 `pnpm run typecheck`、4项Node错误扫描回归、OpenAPI fresh、1908错误目录检查、错误提示检查与生产构建均通过。降低测试进程并发仅用于本机整合验证，不修改仓库性能/类型/行为门槛。

RecordList错误目录mock在每次mockReset后恢复返回值；新增成功置顶不误报失败断言，并复用测试环境补齐DOM scrollTo。SkillInstalledView装饰图标加aria-hidden，使按钮可访问名称保持其文字而不是加入图标名字；保留原“至少两项才能提交”的断言。

Core最终全量83包通过（另5包无测试），算法P1修复与Workflow广回归仍未完成。此处仅标记前端节点，不作为全目标完成。

### 2026-09-10 最终自动化验收记录

- [x] Python 广回归：`1999 passed, 1 skipped, 5 warnings`。
- [x] Core 全量、`go vet`、工作区与 Chat 关键 race、Scan 全量。
- [x] Core↔算法 HTTP `main/subagent/workflow`。
- [x] 前端 `tsc --noEmit`、`typecheck`、OpenAPI fresh、错误码/错误提示、Node 检查、134 文件 856 测试、生产构建。
- [x] `localworkspace` Windows/Linux 交叉编译。
- [x] Local/Desktop、官方 LazyLLM 基线和 gitlink 冻结边界复核。
- [x] 独立只读安全 Review：无 Critical/Important。
- [ ] 人工/真实环境验收：目录选择、真实登录/模型、打包 Desktop、跨平台实机和外部编辑器竞争。

本次不再进行生产代码修改；剩余能力若要扩大到任意 shell/custom MCP、工作区二进制宿主传输或完整旧 Writer/media 注册，需要单独的授权与产品设计评审。
