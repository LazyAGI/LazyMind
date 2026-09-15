# Desktop SQLite Server 设计与使用

## 结论

Desktop / `make local-up` 现在对跨进程共享的 SQLite 文件采用单一存储进程：
`sqlite-server` 是这些文件唯一的物理访问者，Core 和 Python 服务通过本机 HTTP
协议提交 SQL。不同数据库各有独立执行队列，可以并行；同一数据库的操作按顺序
执行，事务从 `begin` 到 `commit` / `rollback` 全程占有该数据库队列。

这与 Milvus Lite 的共同点是“单一进程持有存储，其他模块走服务接口”。Milvus
Lite 实际运行的是 Milvus 协议服务并持有自己的数据目录；它不是 SQLite Server，
也没有复用本文的 SQL 协议。

## 管理范围

服务端只接受预先注册的数据库别名，客户端不能提交任意文件路径。

| 别名 | 物理文件 | 逻辑用途 | 主要访问方 |
| --- | --- | --- | --- |
| `core` | `stores/sqlite/core/core.db` | Core 业务、账号权限、聊天和任务等结构化数据 | Core、迁移、Chat/Review 的只读或业务访问 |
| `lazyllm` | `stores/sqlite/lazyllm/app.db` | 文档处理任务、知识库和 LazyLLM 管理表 | Doc Server、Processor、Worker、Core 只读查询 |
| `segments` | `homes/lazymind/sqlite/segment-store.db` | chunk/line 原文、元数据和 FTS 索引 | Parser、RAG segment store（仅 `SQLiteStore` 模式注册） |

当 `LAZYMIND_SEGMENT_STORE_TYPE=opensearch` 时不注册 `segments` SQLite 别名，
`LAZYMIND_SEGMENT_STORE_URI_OR_PATH` 的 HTTP(S) 地址只交给 OpenSearch 客户端，
不会被 SQLite Server 当成本地文件名打开。

只被一个进程使用的库不进入 SQLite Server，例如 auth-service、channel-gateway、
scan-control-plane、file-watcher 和各模块私有状态库。它们继续直接使用自己的文件，
否则会徒增网络序列化与单点故障，而不能减少跨进程锁竞争。

Docker 模式不受影响，仍按 Docker 配置使用 PostgreSQL、OpenSearch 等服务。

## 并发模型

```text
Core / Python clients
        |
        v
127.0.0.1 sqlite-server + Bearer token
        |
        +-- core queue ------> core.db       （队列内串行）
        +-- lazyllm queue ---> app.db        （队列内串行）
        +-- segments queue --> segment.db    （队列内串行）

三个队列之间并行执行
```

同库读操作也会排队。这比只串行写入更保守，但能保证事务期间不会插入另一个请求，
并彻底消除多个进程各自持有 SQLite 连接造成的锁竞争。事务开始后会一直占用对应
数据库队列；客户端正常结束时提交或回滚，客户端崩溃时服务端在 5 分钟 TTL 到期
后自动回滚并释放队列。

服务端仍为每个文件启用 `WAL`、`synchronous=NORMAL`、`foreign_keys=ON` 和
30 秒 `busy_timeout`。这些配置现在主要防御服务进程内部或旧版本残留访问，而不是
依赖它们解决跨进程并发。

## 接入方式

本地运行管理器自动启动 `sqlite-server`，默认监听 `127.0.0.1:19081`，并在启动
Core 和所有算法服务前检查 `/healthz`。端口参与现有动态端口分配，可通过
`LAZYMIND_LOCAL_SQLITE_SERVER_PORT` 指定。

客户端使用以下运行时配置：

- `LAZYMIND_SQLITE_SERVER_URL`：服务地址；
- `LAZYMIND_SQLITE_SERVER_TOKEN_FILE`：与 process-compose 共用的 `0600` token 文件；
- `sqliteproxy://core`、`sqliteproxy://lazyllm`、`sqliteproxy://segments`：数据库别名 DSN。

Core 通过自定义 `database/sql` 驱动接入，GORM 和 migration 继续使用 SQLite
dialect。Python 侧通过兼容 `sqlite3` DBAPI 的代理接入，LazyLLM `SqlManager`、
SQLAlchemy、`SQLiteStore` 和 `MapStore` 保留原有 SQL 与事务调用方式。

LazyLLM 会为 Parser、Worker 和 Doc Server 再创建 Python 子进程。父进程内执行一次
适配不能覆盖这些子进程，因此本地运行时通过 `algorithm/sitecustomize.py` 在每个
Python 解释器启动时自动安装适配；只有检测到 `sqliteproxy://` 配置时才启用，普通
开发环境和 Docker 模式不会触发。

HTTP 接口只监听回环地址，除健康检查外都校验 Bearer token。服务端支持
`begin`、`execute`、`executemany`、`query`、`commit` 和 `rollback`；参数使用
带类型 JSON 编码，避免 JavaScript 数字精度破坏 SQLite `int64`。客户端的批量
`executemany` 会按实际 JSON 字节数拆分到 16 MiB 服务端上限以内；各拆分请求仍使用
同一个事务 ID，因此批量写的事务边界不变。

Core 的立即事务在直接 SQLite 模式仍使用 `BEGIN IMMEDIATE`；在代理模式必须调用
驱动的 `BeginTx`，取得事务 ID 后再执行 SQL。禁止把裸 `BEGIN IMMEDIATE` 当作普通
`execute` 请求发送，否则单次请求结束时服务端会提前释放数据库队列。

## 设计取舍

- 采用“服务端执行 SQL”，而不是“客户端仍访问文件、服务只发锁”。后者无法阻止
  漏接入的进程绕过锁，也不能保证文件只有一个所有者。
- 采用每库队列，而不是全局队列。全局串行会让 segment 批量写入阻塞 Core 的聊天
  和任务更新，不符合数据库之间没有锁冲突的事实。
- 保留业务层所有权。SQLite Server 只负责存储协议和并发顺序，不理解用户、任务、
  文档等业务规则；业务校验仍在 Core / LazyLLM。
- 不做直接文件访问降级。服务不可用时请求明确失败，避免静默回退后重新产生多个
  文件所有者。process-compose 负责拉起和监控服务。

## 验证

服务端测试覆盖以下行为：

1. `core` 事务未结束时，另一个 `core` 请求会等待；
2. 上述等待期间，`lazyllm` 请求仍可完成；
3. `commit` 后同库等待请求继续执行；
4. 客户端遗留事务超过 TTL 后自动回滚并释放队列；
5. Python DBAPI 的建表、提交、回滚，以及 SQLAlchemy `INSERT ... RETURNING`
   可以通过真实 SQLite Server 工作；
6. 中文大批量 `executemany` 会按编码后字节拆包，OpenSearch 配置不会注册
   `segments` SQLite 文件。

升级后的现有运行实例需要重新执行 `make local-down && make local-up` 才会切换连接；
只替换代码而不重启时，旧进程仍保持旧的直接文件连接。
