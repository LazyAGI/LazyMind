# Preference Organizer：实现与验收

## 行为和边界

偏好页直接提交整理任务。Core 合并同一用户的 active 任务，手动提交会升级 pending 自动任务的 `force_analysis`；running 任务直接复用。执行顺序为当前 Review 完成、Organizer 执行、恢复其余 Review。Organizer 退避时仍优先于 pending Review，等待和容量不足都不消耗失败次数。

页面可见时每 2 秒读取 Core 的 active / 最近任务，离开页面只停止观察。pending 允许排序和删除；running 冻结这些写入，详情仍可读。完成、无需调整、部分完成和失败都会刷新实际数据，最新结果在重新进入页面后仍可见。页面不展示内部 Plan、执行身份或日志。

Algorithm 使用共享专用线程池，默认 2 个执行槽，可用 `LAZYMIND_MEMORY_MAINTENANCE_WORKERS` 配置。容量满立即返回 `503 maintenance_busy`，Core 延后 2 秒；取消 HTTP 等待不会提前释放槽位。Organizer 总等待上限 30 分钟，Review 10 分钟。每次执行按 task ID / run ID 隔离 LazyLLM 上下文，失效后禁止后续 Agent 轮次和 RemoteFS / Episode 请求。

Gate 接收结构化 operations，保存规范化 JSON 的哈希和完整参数。Agent 按 operation ID 调用 Apply；压缩恢复只注入执行游标。策略固定为目标最多 30 条、软偏好 20 条、投影预算 40%，没有硬下限；最多两轮、每轮 60 round、总变更预算 50。手动任务在非空且已达标时仍分析第一轮。

每个操作保存 receipt，包括涉及名称、状态、成本、完成/失败步骤、可获得的 ETag 和 Episode ID。部分写入和无法确认的结果会停止后续 Apply；最终读取失败仍保留已有记录。此实现不增加崩溃恢复审计或自动回滚。

按去掉 anchor 后的文件路径校验一对一引用，删除偏好仍删除其独占 Markdown。新增和 Merge 在写新 Reference 前校验索引和名称映射碰撞。存量共享文件明确报错，不自动修复。

公共能力采用组合：Core `maintenance.UserTransaction` / `Authorize` 和 Worker 续租包装、Algorithm `common.maintenance.execute`、前端 `usePreferenceOrganizer`。无需增加 Organizer 专属业务基类。

## 协同升级

1. 在维护窗口停止旧 Worker 领取，并结束旧 Core / Chat 上所有维护执行。可通过 `LAZYMIND_RESOURCE_UPDATE_ENABLED=false` 启动暂停调度的 Core；该配置在进程启动时读取。
2. 备份数据库，应用新的 dev migration `20260903044806_add_resource_update_run_id`，或新安装时使用更新后的 v0.3 aggregate。已共享的 dev migrations 不修改。
3. 部署配套 Core 和 Chat，确认双方均传递并校验 run ID，再启用 Worker。不要混用缺少 run ID 的旧维护调用方；这些写入会被明确拒绝。遗留 running 任务在租约过期恢复后，以新的 run ID 重新领取。
4. 发布配套前端。回滚前同样停止并结束维护执行，再执行对应 down migration；aggregate down 以前一 release schema 为基准。

用户锁只覆盖短数据库事务，顺序为用户锁、任务行、数据行。PostgreSQL 使用事务 advisory lock，SQLite 使用封装内的 BEGIN IMMEDIATE。写入时验证 lease / run ID，租约续期和最终确认还匹配 worker 与影响行数。旧执行即使返回成功也不能确认任务；旧 MOVE 也不能在 EpisodeStore 继续产生副作用。

## 确定性验证

2026-09-03，本地执行：

- Core 全量 `go test ./...` 通过；后续补充检查覆盖 resourceupdate、currentmemory、remotefs、episode、OpenAPI 和 migrate。
- SQLite 与临时 PostgreSQL 均通过相关模块测试；迁移测试验证 dev / aggregate 一致、升级和降级路径；已共享 dev migration 不可变检查通过。
- `go test -race ./resourceupdate ./maintenance` 通过。覆盖用户级入队/领取竞态、长执行续租、租约丢失、过期恢复、旧执行拒写/拒确认、Organizer 退避时 Review 保持 pending。
- Algorithm 四组测试共 65 项通过，覆盖容量/取消/上下文隔离、结构化 Gate、压缩恢复、无硬下限、碰撞、Merge / Episode / Reference 部分失败、unknown receipt、预算只计一次和最终读取失败。
- 前端 hook / 页面 7 项测试通过，覆盖状态恢复、重复提交、可见性轮询、运行冻结、详情访问、排序乐观回滚和无需调整结果恢复。
- 前端生产构建、变更文件 ESLint、OpenAPI / 错误码生成检查通过。完整 TypeScript 检查仍有基线遗留错误；与合并后 HEAD 单独比较，诊断从 112 条减少为 110 条，没有新增。本次 OpenAPI 生成只更新偏好/Organizer 契约，保留其他接口的已提交版本。
- 远端 CI 未运行。上述是本地测试结果，不是 CI 结论。

常用命令（从对应目录执行，使用已安装项目依赖的 Python 环境）：

```sh
# backend/core
 go test ./...
 go test -race ./resourceupdate ./maintenance
 # 指向一次性 PostgreSQL，不能使用生产数据库
 MIGRATION_TEST_POSTGRES_DSN="$DISPOSABLE_POSTGRES_DSN" go test ./migrate
 TEST_DB_DRIVER=postgres TEST_DB_DSN="$DISPOSABLE_POSTGRES_DSN" go test ./resourceupdate ./currentmemory ./remotefs ./episode

# 仓库根目录
 PYTHONPATH=algorithm:algorithm/lazyllm python -m pytest -q tests/algorithm/review/test_memory_store.py tests/algorithm/review/test_preference_organizer.py tests/algorithm/review/test_memory_review.py tests/algorithm/review/test_maintenance_executor.py
 python scripts/check_migration_immutability.py --base origin/main

# frontend
 pnpm test src/modules/memory/hooks/usePreferenceOrganizer.test.tsx src/modules/memory/components/PreferenceMemorySection/index.test.tsx
 pnpm gen:openapi:check
 pnpm check:error-codes
 pnpm build
```

## 实际浏览器和模型链路

本次使用隔离测试数据库、真实 PreferenceMemorySection、生产 Core handlers / Worker、真实 Algorithm 路由与 Qwen/Qwen3.8-Flash-Next。测试入口使用固定测试用户代替登录鉴权，不覆盖完整应用的登录、导航，也不代表历史 38 case 语义评估已经通过。

合成数据包括两条重复的默认中文回答偏好，以及一条明确只对本次排查有效的临时设置。实际观察：

| 顺序 | 结果（UTC） |
| --- | --- |
| 当前 Review | 05:20:33 开始，05:21:09 完成 |
| 主动 Organizer | 05:21:09 开始，05:21:44 完成；3 → 1，删除重复条目和临时条目，保存 2 条 applied receipt |
| 后续 Review | 05:21:44 后开始，最终均完成；每个 Review attempt = 1 |
| 再次手动整理 | 数量为 1 且已达标，仍分析一轮；返回 no_safe_changes，变更数为 0 |

浏览器实际点击后显示“等待当前记忆回顾结束”；pending 刷新恢复该状态，待执行 Review 的 attempt 保持 0。running 时排序/删除禁用、详情可读；同时直接发出删除请求收到 `409 preference_organizing / mutation=none`。完成后列表刷新为 1 条、写入操作恢复，另一个标签页读取相同数据。再次刷新显示“无需调整”。

可复现入口：

- `backend/core/organizer_browser_e2e_test.go`：设置 `ORGANIZER_BROWSER_FIXTURE_ADDR` 才启动，默认测试跳过。使用一次性测试 DB，最多运行 12 分钟。
- `tests/algorithm/review/organizer_browser_fixture.py`：加载真实路由，只在进入 Review / Organizer 前添加 35 / 12 秒等待，便于观察状态。使用自己的有效模型配置和内部测试 token。
- `frontend/tests/fixtures/organizer/index.html`：Vite 下直接加载生产偏好组件。

例如，分别在三个终端运行，使用同一个内部测试 token（仅本机测试）：

```sh
# backend/core
 ORGANIZER_BROWSER_FIXTURE_ADDR=127.0.0.1:18048 LAZYMIND_CHAT_SERVICE_URL=http://127.0.0.1:18049 LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN=organizer-fixture-token go test . -run '^TestOrganizerBrowserFixture$' -count=1 -v -timeout 15m

# 仓库根目录，MODEL_CONFIG_PATH 应指向自己的有效配置
 PYTHONPATH=algorithm:algorithm/lazyllm LAZYMIND_MODEL_CONFIG_PATH="$MODEL_CONFIG_PATH" LAZYMIND_CORE_API_URL=http://127.0.0.1:18048/api/core LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN=organizer-fixture-token python tests/algorithm/review/organizer_browser_fixture.py

# frontend
 VITE_PROXY_TARGET=http://127.0.0.1:18048 pnpm exec vite --host 127.0.0.1 --port 18050
```

打开 `http://127.0.0.1:18050/tests/fixtures/organizer/index.html` 并立即点击整理；`GET /__fixture/tasks` 可核对任务顺序和 receipts，`POST /__fixture/stop` 结束 Core fixture。若 Algorithm 在容器中运行，应按容器网络配置 Core 地址，并只在本机测试环境暴露此固定用户 fixture。
