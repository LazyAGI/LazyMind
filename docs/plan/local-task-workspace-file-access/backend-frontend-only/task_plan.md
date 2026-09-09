# 工作区工具授权任务与验收

主方案：IMPLEMENTATION_PLAN.md，2026-09-09 Review 稿。用户已允许项目算法及对应测试修改；LazyLLM/gitlink、Local/Desktop 继续冻结。现在只编写方案，生产和测试代码均未修改。

## 当前状态与总门禁

- [x] 原 U1–U7、C1–C2 自动化补齐；历史最近前端矩阵 49/49。
- [ ] 原 T6：真实 Local/打包 Desktop UI、必要数据库及平台验收。
- [x] 算法侧新方案源码核对和修改边界确认。
- [x] 注册、中间件、Core HTTP/state、子任务/Workflow 和旧权限定义审计，设计写入现有方案。
- [ ] 用户 Review 具体设计：删除规则、未接入自定义工具的拒绝、期限与总规模（预计生产净增 870–1350 行、2 个新文件）。
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
- [ ] Review A2 RED 后实现 Core 单一 operation service、批准状态机和真实文件操作；不得先写“临时允许”分支。
