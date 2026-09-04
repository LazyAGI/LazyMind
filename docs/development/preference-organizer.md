# Preference Organizer：实现与验收

## 行为和边界

偏好页直接提交整理任务。Core 合并同一用户的 active 任务，手动提交会升级 pending 自动任务的 `force_analysis`；running 任务直接复用。执行顺序为当前 Review 完成、Organizer 执行、恢复其余 Review。Organizer 退避时仍优先于 pending Review，等待和容量不足都不消耗失败次数。

页面可见时，pending / running 每 2 秒读取任务，无任务、结束或查询失败每 15 秒读取；进入页面、恢复可见和手动提交后立即查询，隐藏或卸载停止观察。pending 允许排序和删除；running 冻结这些写入，详情仍可读。完成、无需调整、部分完成和失败都会刷新实际数据和已打开的详情。页面只展示当前总条数和字符预算，不展示可投影条数或行级常驻标记。

Algorithm 使用共享专用线程池，默认 2 个执行槽，可用 `LAZYMIND_MEMORY_MAINTENANCE_WORKERS` 配置。容量满立即返回 `503 maintenance_busy`，Core 延后 2 秒；取消 HTTP 等待不会提前释放槽位。Organizer 总等待上限 30 分钟，Review 10 分钟。每次执行按 task ID / run ID 隔离 LazyLLM 上下文，失效后禁止后续 Agent 轮次和 RemoteFS / Episode 请求。

完整存储不设条目容量上限。唯一容量配置是正整数 `LAZYMIND_PREFERENCE_CONTEXT_MAX_CHARS`，默认 5000，Core 和 Chat 必须一致。投影按索引原顺序保留能完整放入预算的最长前缀，不跳过条目、不截断 summary。Core 与 Algorithm 使用相同的固定 YAML 布局和 JSON 引号转义，按 Unicode 字符（包含格式开销）计数，不按字节、UTF-16 或 token。空 YAML 包装也放不下时投影为空字符串。

完整投影字符需求达到 80% 时自动触发整理，严格低于 40% 才达标。Gate 接收结构化 operations，保存规范化 JSON 的哈希和完整参数。Agent 按 operation ID 调用 Apply；压缩恢复只注入执行游标。没有条目数目标、硬下限或任务总变更次数上限；最多两轮、每轮 60 round，merge 单次仍为 2–10 条。手动任务在非空且已达标时仍分析第一轮，空索引不调用模型；没有安全动作时可以停止，不为达标强制删除。`target_reached` 根据最终验证快照计算，与是否发生改动分离；最终读取失败不推断达标。

`passes[].changes` 从该轮 receipts 汇总，`total_changes` 从各轮结果汇总，仅用于审计，不再限制执行次数。delete / MOVE 按一条计，merge 按源条目数计；幂等零改动不计，部分写入与结果未知保留原计数及状态，不能把总数当作全部已确认成功的变更。写入后验证读取失败也保留 receipt，不重复计数。Algorithm 不再产生 `budget_exhausted`；Core 结果契约和生成客户端保留该枚举以读取历史结果，并接收旧 Algorithm 返回值。历史 JSON 不重写，字符预算不受此兼容状态影响。

每个操作的 receipt 仅保存在 `passes[].receipts`，包括涉及名称、状态、成本、完成/失败步骤、可获得的 ETag 和 Episode ID。保留 `passes_attempted` / `pass_number`，不再提供伪进度 `current_pass`。部分写入和无法确认的结果会停止后续 Apply；最终读取失败仍保留已有记录。此实现不增加崩溃恢复审计或跨 operation lineage 系统。

按去掉 anchor 后的文件路径校验一对一引用，删除偏好仍删除其独占 Markdown。新增和 Merge 在写新 Reference 前校验索引和名称映射碰撞。存量共享文件明确报错，不自动修复。

公共能力采用组合：Core `maintenance.UserTransaction` / `Authorize` 和 Worker 续租包装、Algorithm `common.maintenance.execute`、前端 `usePreferenceOrganizer`。身份在 HTTP 入口统一解析到 `maintenance.Identity`（task Header 优先、query 回退，run ID 仅接受 Header），授权与避免自触发共用身份。MemoryStore 共享 add/merge 的新引用校验、Reference-first 写入和失败补偿，保留各自 receipt 步骤；MOVE 的 Episode-first 顺序独立。Review 的 route/service 共用 schema，前端 Organizer 状态和通知共用映射。共性已收敛到现有组件，不需要新增 Organizer 基类或通用轮询框架。

## 协同升级

本次单字符预算改造不新增数据库迁移。删除旧 `resident_index_usage` 及条目预算配置；列表 `projection_state.max_chars` 与完整投影字符数作为前端唯一预算来源，需要配套升级 Core、Algorithm 和前端。历史任务 JSON 保持原样，读取允许旧额外字段。调序响应直接更新完整统计，乐观排序和冲突期间使预算失效；删除后刷新失败显示“预算待刷新”，不在前端估算。

1. 在维护窗口停止旧 Worker 领取，并结束旧 Core / Chat 上所有维护执行。可通过 `LAZYMIND_RESOURCE_UPDATE_ENABLED=false` 启动暂停调度的 Core；该配置在进程启动时读取。
2. 备份数据库，应用新的 dev migration `20260903044806_add_resource_update_run_id`，或新安装时使用更新后的 v0.3 aggregate。已共享的 dev migrations 不修改。
3. 部署配套 Core 和 Chat，确认双方均传递并校验 run ID，再启用 Worker。不要混用缺少 run ID 的旧维护调用方；这些写入会被明确拒绝。遗留 running 任务在租约过期恢复后，以新的 run ID 重新领取。
4. 发布配套前端。回滚前同样停止并结束维护执行，再执行对应 down migration；aggregate down 以前一 release schema 为基准。

用户锁只覆盖短数据库事务，顺序为用户锁、任务行、数据行。PostgreSQL 使用事务 advisory lock，SQLite 使用封装内的 BEGIN IMMEDIATE。写入时验证 lease / run ID，租约续期和最终确认还匹配 worker 与影响行数。旧执行即使返回成功也不能确认任务；旧 MOVE 也不能在 EpisodeStore 继续产生副作用。

## 取消变更次数上限验证（2026-09-03）

- Algorithm 相关 132 项测试通过：单轮超过 50、两轮累计 110、merge / MOVE / delete 超过 50、幂等和重复记账、部分失败、最终读取失败，以及保留的 Gate / merge / round / 取消边界。变更文件 flake8 通过。
- Core `go test ./...` 通过；`go test -race ./resourceupdate ./maintenance ./remotefs ./currentmemory` 通过。旧 `budget_exhausted` Algorithm 响应仍成功落库，旧任务 JSON 原样读取。
- 前端 hook / 页面 13 项及接口 / view model / 生成客户端 / 双语契约 18 项测试通过；变更文件 ESLint、OpenAPI 新鲜度检查与生产构建通过。本轮未重复全量前端测试和全量 TypeScript 检查；此前基线问题见下文。
- 在一次性 Core 数据库中使用 `Qwen/Qwen3.8-Flash-Next` 真实模型，手动任务 `dc574a5ecc77481aa8c2ab64836e7151` 于 09:46:14–09:48:26 UTC 完成，约 131.5 秒。仅一个任务、一个 attempt、一个 pass，55 次 duplicate DELETE 全部 applied；66 → 11 条，4528 → 758 字符（5000 上限），`organized + target_reached=true`。
- 核对了全部 55 条 receipt、完整 ETag 链、保留的 11 个偏好及其 Reference；无失败步骤、缺失或多余引用，Core 与 Algorithm 最终快照一致。隔离浏览器页面显示 `Total: 11`、`758 / 5000` 和整理完成，操作按钮恢复可用。
- 用户原验收账号保持 19 条、1273 字符，ETag 未变；原开发服务未重启。本次真实数据只验证精确去重，不代表复杂语义 merge / MOVE 的模型验收。

## 单字符预算改造验证（2026-09-03）

- Core 全量 `go test ./...` 通过；currentmemory、remotefs、resourceupdate、maintenance、episode 的 race 检查通过。新增普通无 Lane 任务不得凭零时间插队、Header 优先 / query 回退、历史结果读取及启动清空伪进度测试。
- Algorithm 相关 102 项测试通过：共享 Unicode fixtures、超过 100 条且字符足够、完整前缀、恰好边界、空 / 极小预算、严格低于 40%、手动无改动达标、部分写入 / 补偿和最终读取失败等。
- 前端局部 hook / 页面 12 项及 API / view-model 16 项测试通过，覆盖 2 / 15 秒轮询、后台任务发现、乱序响应、统计失效及恢复。
- OpenAPI 生成检查、错误码检查、变更文件 ESLint、Python lint 和生产构建通过。全仓错误提示检查仍报告既有接口错误文案规则问题，未扩展本次范围去统一改写。
- Node 25 全量测试需 `NODE_OPTIONS=--no-experimental-webstorage` 避免全局 localStorage 覆盖 jsdom。此环境下当前 538 passed / 3 failed，另有 1 个 suite 初始化失败；原 HEAD 为 533 passed / 相同 3 failed 和同一个 suite 初始化失败。失败点均位于 ChatMessageContent 的 i18n mock、Writer Markdown anchor、SkillInstalledView 的 matchMedia mock。完整 TypeScript 检查当前和原 HEAD 均为相同 550 条诊断，没有新增；不把这些检查报告为全绿。

浏览器使用真实偏好组件、隔离 Core 数据库 / Worker、真实 Algorithm 路由及 `Qwen/Qwen3.8-Flash-Next`（OpenAI 兼容接口，skip_auth），Core / Algorithm 均配置 200 字符预算：

- 整理任务 `1e2ff4383eff49ce90413a6f98dbc398`：3 条、194 字符 → 1 条、68 字符，2 个 applied receipts，`target_reached=true`。页面同步显示总条数和新字符占用。
- 再次手动任务 `ae365e4d49834811b250bf5842379e44`：仍分析 1 轮，0 改动，`no_safe_changes + target_reached=true`；浏览器显示“无需调整”。
- 中英文、390px 窄屏均检查通过，没有可投影数量或常驻标签。首次模型请求因宿主机不能解析容器专用地址失败，改用 `127.0.0.1:19001/v1/` 后重试成功，失败时页面也刷新了实际数据。
- `make local-up LAZYMIND_FRONTEND_PORT=8091` 的 Core / 前端就绪，但共享开发数据的 `doc-summary` 注册签名冲突阻断完整算法 / Chat 启动。未重置数据；以上实际模型验证走独立测试入口，不宣称完整应用登录、Chat 注入和 Review 队列链路已重新验收。

## 上一轮实现的验证记录（字符预算改造前）

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

## 上一轮浏览器和模型链路记录

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
- 同时设置 `ORGANIZER_BROWSER_FIXTURE_LARGE=1` 时，初始化 66 条偏好（11 组各 6 份，共 55 个重复项），不创建 Review；以 5000 字符预算手动提交一次 Organizer，核对任务结果、实际存储和 receipts，验证超过旧 50 次限制。默认三条数据与 Lane 场景保持不变。
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
