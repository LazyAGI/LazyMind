# 算法跃迁：前端与 Core 任务交付

本轮对应《算法跃迁实现方案》的 A1–A7，仅修改 LazyMind 前端、Core、数据库迁移及接口契约。算法服务和 LazyLLM 子模块不在本轮修改范围。

## 已实现行为

| 任务 | 行为 | 主要验证 |
| --- | --- | --- |
| A1 导航 | 详情返回算法跃迁首页；观察页返回所属任务。离开时取消页面读取、订阅和计时器，不发送任务控制命令。 | 浏览器返回路径与网络请求检查、Hook 卸载测试 |
| A2 恢复 | 首页按 Core 的活动任务记录提供当前任务入口，不受历史首屏分页限制。重入恢复展示与订阅，不自动批准检查点；账号、任务和请求批次隔离迟到结果，报告继续按需加载。 | 列表分页、旧任务迟到响应、浏览器重入无控制请求 |
| A3 终止 | 独立终止入口及二次确认；防连点；同一次意图的重试复用 command_id；独立回读状态，完成竞态以实时终态为准，处理中允许返回。 | Hook 重试、竞态、缓存终态、Core 命令透传与活动任务锁测试 |
| A4 状态来源 | 详情和列表返回 live/cached 与最后成功观测时间。读取缓存不刷新该时间；仅更新步骤时也不伪造状态观测。 | SQLite、PostgreSQL 实时读取后上游失败回退测试 |
| A5 模型候选 | 服务端聚合有权使用且连接、凭证和执行器适配有效的个人模型、共享默认模型；完整流程验收结果独立展示，不影响准入；没有可用默认不自动选第一项。 | 无验收记录仍可选择及创建、权限撤销、配置版本变化、默认不可用、空态及默认展示 |
| A6 本次选择 | 创建时再次解析模型引用，只替换本次 evo_llm，保留其他依赖；不更新默认设置，不依赖被替换默认模型的凭证。 | 真实数据库配合隔离 Evo 上游，核对实际注入配置和默认记录 |
| A7 创建记录 | Core 从实际解析结果保存公开 model_at_creation；详情、历史展示摘要。旧任务明确显示未记录。 | 伪造摘要剔除、公开载荷无凭证、浏览器历史缺失展示 |

## 接口增量

`GET /api/core/agent/evolution-models` 使用现有认证及 `qa.write` 权限，返回标准 Core 响应信封中的以下数据：

- `models`：实际可选的公开模型摘要列表。
- `configured_default`：仍可解析到的配置默认摘要，可能不可选；已删除或不可访问的默认不会泄露其信息。
- `available_default_ref`：配置默认本身可用时才返回。
- `can_select`：是否有可用候选。访问权限不足由已有权限层返回 403。
- `unavailable_reason`：`not_configured`、`unavailable`、`incompatible`、`connection_unverified`、`configuration_incomplete` 或 `credentials_unavailable`；默认可用时省略。

摘要字段为 `model_ref`、`display_name`、`provider_name`、`source`、`validation_status` 和可选 `validation_version`。完整流程验收状态为 `unverified` / `passed` / `failed`，与是否可选择独立；没有可信报告时不返回验证版本。当前来源为 `personal` / `shared`，复用本仓库现有本地模型配置、共享授权与 OpenCode 适配规则。未新增 Cloud 凭证解析能力；Cloud 引用不会被当成本地模型 ID 接受。

`POST /api/core/agent/threads` 增加 `evo_model_ref`。前端只发送引用和原有启动参数；Core 忽略客户端提供的 `llm_config` 与 `model_at_creation`。引用失效返回现有业务码 `2001301` 和安全提示。旧客户端未传引用时，仅可使用经过同样验证的配置默认，不会静默选择其他候选。原有 `llm`、`embed_main` 等依赖检查保留。

任务详情与列表的每个任务增加 `status_source: live | cached`、可选 `observed_at`。列表增加 `current_thread_id`，取自当前用户的活动任务记录。Core 只有确认算法终态后才释放取消任务的活动锁，取消请求受理或状态读取失败均不等于任务已停止。

终止仍使用现有 `POST /api/core/agent/threads/{id}/cancel`。本地等待提示只说明当前页面已提交请求；不会据此推断跨刷新后的真实 `cancelling` 状态。

## 能力验证证据与迁移

新增开发迁移 `20260918033746_evolution_model_selection` 及匹配回退，同时更新已有 v0_3 聚合迁移；支持 SQLite / PostgreSQL：

- `agent_threads.status_observed_at` 可空，旧任务不补造观测时间。
- `evolution_model_validations` 保存受信任离线验证结果：`model_ref`、`validation_version`、`evidence_id`、`passed`、`verified_at`、`expires_at`。

该表只供受控验证流程写入，没有面向浏览器的写入接口，迁移不填充任何“通过”记录。算法验证工具必须针对真实配置执行验证，再保存报告引用。普通连接验证、用户声明、模型品牌均不能替代完整流程验收结果。`expires_at` 仅保留数据库兼容性，新记录不设自动过期时间，读取时不依据该字段限制使用或隐藏验收结果。

模型引用绑定配置身份、所有者、连接组、端点、模型参数、凭证版本、配置更新时间、适配描述及验证版本。当前验证版本为 `evo-opencode-v1`。配置或适配版本变化会使旧引用失效，新配置展示“尚未验证”；报告标识缺失或验证时间不合法也展示“尚未验证”。创建请求会重新检查授权、连接、凭证与执行器适配，不要求完整流程验收记录。

**根据用户确认的修复方案，未执行完整流程验收不再禁用已配置且符合原有准入条件的模型。** 列表和创建接口使用同一准入规则；前端分别展示模型不可用原因与完整流程验收状态。

## 验证命令

在 `frontend/`：

```sh
pnpm exec vitest run src/modules/selfEvolution
pnpm run build
pnpm run gen:openapi:check
```

在 `backend/core/`：

```sh
go test ./agent ./modelconfig -count=1
TEST_DB_DRIVER=postgres TEST_DB_DSN="$TEST_POSTGRES_DSN" go test ./agent ./modelconfig -count=1
MIGRATION_TEST_POSTGRES_DSN="$TEST_POSTGRES_DSN" go test ./migrate -run 'Test(EvolutionSelectionMigrationRoundTrip|RepositoryPostgresMigrationPaths|RepositorySQLiteReleaseAndDevPathsMatch|RepositoryMigrationsSupportPostgresAndSQLite)' -count=1
go test . -run TestOpenAPISpecCoversAllRegisteredRoutes -count=1
```

前端模块 28 项测试、构建、变更文件 ESLint，以及上述双数据库和迁移检查通过。浏览器使用隔离 API 数据，验证返回、只读重入、终止确认及终态、空模型禁用、默认模型展示、创建请求模型引用。它不代表真实算法执行验收。

全量 TypeScript 检查仍受既有 `src/modules/chat/utils/message.test.ts:49` 语法错误阻断，没有将其算作通过。

## 后续算法协作，未在本轮实现

此处记录 A1–A7 交付时的范围。后续 B1–B5 的代码、工具和待完成实测见 [算法实现与验证](evolution-algorithm-validation.md)。

1. B1：暴露真实 `cancelling`，支持跨刷新确认终止中。
2. B2：算法运行配置与产物入口统一约束，保证任务中途不能换模型。
3. B3：交付真实能力验证规范、工具及代表性完整流程证据，并接入受控证据保存流程。
4. B4：联合验证各阶段真实停止、清理与迟到结果处理，发现问题后修复算法。
5. B5：联合验证创建或消息受理过程中离开页面的服务端持久化与恢复边界。
