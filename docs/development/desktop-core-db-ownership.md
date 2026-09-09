# Desktop 模式下 Core SQLite 的归属设计

> 说明：共享 SQLite 文件的物理连接现已统一交给
> [Desktop SQLite Server](./desktop-sqlite-server.md)。本文描述的 Core 业务 API
> 仍用于保持业务边界；Core 是 `core.db` 的逻辑所有者，SQLite Server 是共享文件
> 的物理连接所有者。

## 设计结论

Desktop 模式现在分成两层所有权：`sqlite-server` 统一持有共享 SQLite 文件的
物理连接并处理同库串行、异库并行；Go Core 仍然是 `core.db` 的逻辑和业务所有者。
SQL 代理解决物理连接与事务冲突，但不代替业务 API，也不允许客户端指定任意文件。

新增或迁移 Core 业务能力时，正确边界仍然是由 Core 提供面向具体业务的内部 API，
而不是让 Algorithm 新增对 Core 表结构的依赖。现存尚未迁移的 SQL 虽通过代理避免
跨进程锁冲突，后续仍应逐步收口到 Core API。

本次首先迁移 SubAgent 的任务执行与任务上下文查询链路。这是本地并发任务
运行期间最频繁的跨进程写入链路，也是 SQLite 锁冲突风险最高的部分。

## 运行流程

1. Core 在 `core.db` 中创建任务。
2. Core 调用 `/api/subagent/run` 前，读取任务、已持久化步骤和 Artifact
   版本信息，生成本次执行所需的任务快照。
3. Algorithm 根据快照在内存中执行，不再接收 SQLite 文件路径或数据库
   DSN。
4. Algorithm 通过 SSE 输出任务状态、步骤和 Artifact 事件。
5. Core 为步骤分配持久化序号并写入数据库，然后继续通过已有的实时任务流
   向前端发布事件。
6. 恢复任务时，Core 使用数据库中的最新状态重新生成快照，Algorithm 从
   快照恢复执行。
7. Chat 侧需要查询任务状态或 Artifact 上下文时，通过带内部令牌的 Core
   API 查询。Artifact 使用批量接口，避免出现 N+1 查询。

整个持久化方向变成单向链路：

`Core 生成快照 → Algorithm 内存执行 → 事件流回传 → Core 写数据库`

## 为什么能够减少 SQLite 锁错误

迁移前，Core 和一个或多个 Python 进程会分别打开同一个 SQLite 文件，
并竞争 SQLite 唯一的写入槽位。增加 busy timeout 和重试只能缓解症状，
无法协调不同进程之间的事务。

迁移后，SubAgent 的数据库写入全部进入 Core 的既有业务路径。Desktop 的所有
`core.db` 物理访问再统一经过 SQLite Server；事务必须通过代理的 `begin` 协议建立，
服务端从开始到提交/回滚持续持有 `core` 队列。Core 内部仍保留事务重试，防御旧进程
残留访问或底层忙状态。

这项修改主要提升稳定性，不保证按相同比例缩短任务总耗时。完整任务的主要
耗时通常仍然来自模型请求和工具执行。

## API 边界

- `POST /api/subagent/run` 接收 `task_spec` 和 `initial_steps`，协议中不再
  包含 `db_dsn`。
- `GET /internal/subagent/conversations/{conversation_id}/tasks` 向可信的
  Algorithm 调用方返回任务 DTO。
- `GET /internal/subagent/artifacts?task_id=...` 使用一次有界查询返回最多
  100 个任务的可见 Artifact。
- `GET /internal/subagent/tasks/{task_id}/artifacts` 用于查询单个任务的
  Artifact。
- 新增的内部查询接口必须携带 `X-LazyMind-Internal-Token`。如果服务令牌
  未配置或者令牌不匹配，接口直接返回 401。

Core 只对外提供业务数据，不暴露表名、原始 SQL、事务控制或者 SQLite
文件路径。这样以后修改数据库结构时，不需要同步修改 Algorithm 中散落的
SQL 语句。

## 本次已经完成的范围

- 普通 SubAgent 启动时，由 Core 下发完整任务快照。
- Algorithm SubAgent 使用内存存储保存本次执行状态，不再创建 Core
  数据库连接。
- text、think、tool call 和 tool result 步骤统一由 Core 持久化。
- tool result 的实时事件保持紧凑；Core 另行持久化最多 16 KiB 的恢复表示，超限内容
  使用工作区文件引用，避免断点恢复使用被 UI 截断的文本。
- Artifact 和任务状态继续通过 Core 的事件处理路径持久化。
- 恢复执行所需的历史步骤和 Artifact 版本由 Core 提供。
- TaskQueryDB 已经改为通过 Core API 查询任务和 Artifact。
- Artifact 上下文使用批量查询，避免逐任务请求和逐任务 SQL 查询。
- `/api/subagent/run` 已删除 `db_dsn` 参数。
- Desktop 和 Docker Core 配置中已经删除废弃的
  `LAZYMIND_SUBAGENT_DB_DSN`。

## 尚未迁移的数据库调用

本次完成的是 SubAgent 高频链路，还没有迁移所有历史上使用
`LAZYMIND_CORE_DATABASE_URL` 的模块。在从 Algorithm 运行环境中彻底删除
该变量之前，还需要迁移以下调用：

- Skill Review：会读取会话历史，并写入 `skill_review_stats`。
- Vocabulary：会读取聊天历史和词汇分组。
- Router：启用后会读写进程和路由元数据；目前 Desktop 默认关闭 Router。

这些模块当前通过 SQLite Server 访问，因此不会再各自打开 `core.db` 文件；但其
授权规则和一致性要求不同，仍应分别设计对应的 Core 业务 API，消除对表结构的耦合。

全部迁移完成后，应执行以下收尾工作：

1. 从 Algorithm 服务环境中删除 `LAZYMIND_CORE_DATABASE_URL`。
2. 从 Algorithm 服务环境中删除指向 Core 的 `LAZYMIND_ACL_DB_DSN`。
3. 增加启动检查：如果 Core 之外的本地进程尝试连接 `core.db`，立即拒绝
   启动并输出明确错误。

## 运行检查方法

执行多个本地并发任务时，应检查以下内容：

- Algorithm 收到的 SubAgent 请求中包含 `task_spec`，不包含 `db_dsn`
  或 `core.db` 路径。
- Core 数据库中的 `sub_agent_steps` 和 `sub_agent_artifacts` 能够正常
  增长。
- 恢复任务时，Artifact 序号从已有最大版本继续递增，而不是重新从 1
  开始。
- 日志中没有 `database is locked`、`SQLITE_BUSY`、步骤序号重复或
  Artifact 序号重复错误。
- 不携带正确内部服务令牌访问新增查询接口时，Core 返回 401。

## 后续建议

建议下一步优先迁移 Skill Review 的写入，因为它仍然可能与正常任务执行
同时写入 `core.db`。随后迁移 Vocabulary 读取，最后处理 Desktop 默认关闭
的 Router。全部迁移完成后，Core 才能成为 `core.db` 真正且可强制验证的
唯一拥有者。
