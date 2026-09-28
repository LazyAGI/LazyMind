# KB 上传阻塞与解析存储优化（2026-09-23）

## 当前落地范围（2026-09-24）

`cst/fix_0923` 的必要修复全部位于 LazyMind，LazyLLM 子模块保持 `373b206d`。
SQLite 代理适配层通过 CAST 读取原始时间，并将显式时区转换为本地无时区时间，
兼容原版回调调度器；LazyMind processor 的分块预览只读取正文存储，保留原版分页行为。
原版 LazyLLM 下的代理和预览回归测试 10 项通过，本次未重启服务。

下文为此前排查与优化分支的历史记录。FTS 批量删除、避免重复正文写入及通用向量
冷读取优化未包含在当前分支中；本次也不新增性能日志，无需合并 LazyLLM `cst/opt_doc`。

## 原因与修复

上传绑定任务在 `createTaskFromUploadedFile` 的事务内，通过使用根数据库的
`datasetAlgoIDByID` 查询 dataset。本地 SQLite 代理的事务独占同库执行队列，
导致事务等待查询、查询等待事务结束。现场查询耗时 60.037 秒，接着插入 document
报 `context canceled`；上传文件存在，但解析任务尚未创建。

现在直接使用事务 `tx` 查询 dataset，并传播查询错误。新增单连接回归测试验证
上传绑定、算法 ID、document/task/processing state 原子创建，以及 dataset 缺失时回滚。
通过 Go overlay 使用修改前的 `task.go` 运行同一回归测试，成功复现超时失败；
修复后 `go test ./doc -count=1 -timeout=180s` 通过。

解析存储还有两处不必要开销：

1. 正文先落盘、embedding 完成后调用 HybridStore.upsert，又重写一次正文与 FTS。
   现在第二阶段只写向量；复制和其他完整 upsert 保持原有行为。模型失败时已保存的
   正文与成功向量仍保留，向量写失败仍向上传播错误。
2. SQLite FTS 表的 uid 是 UNINDEXED，逐节点 DELETE 会反复扫描集合。
   改为每 500 个 uid 批量删除，再插入更新内容。不改变表结构或已有数据格式。

日志新增/拆分 `phase=transform`、`segment_store`、`embed`、`vector_store`；
原 `phase=store` 保留为整体时间。原来 embed 时间包含向量写入，不能直接视为模型耗时。

## 隔离存储基准

本机临时目录运行真实 local-runtime-manager SQLite HTTP server，使用生产 Python
sqlite_proxy 和 SQLiteStore；没有写入用户数据库。对比 LazyLLM `373b206d` 的
原 upsert 与修改后实现。每节点正文为 `knowledge retrieval embedding measurement `
重复 20 次，每个规模创建独立集合，首次写入后完整重写一次，并校验节点数量。
单次测量包含序列化、代理请求、正文和 FTS 写入，不能当作整份文件的解析耗时。

| SQLite 代理节点数 | 首次写入：修改前/后 | 重写：修改前/后 |
| --- | --- | --- |
| 1,000 | 0.063 / 0.053 秒 | 0.244 / 0.068 秒 |
| 3,000 | 0.179 / 0.169 秒 | 1.702 / 0.234 秒 |
| 10,000 | 12.321 / 0.631 秒 | 29.329 / 0.821 秒 |

直接 SQLite（无 HTTP 代理）10,000 节点首次写入从 8.197 降到 0.213 秒，
重写从 19.650 降到 0.294 秒。这说明反复扫描 FTS 是主要瓶颈之一，并非全由代理通信造成。
上述基准只比较 FTS 批量更新；消除 embedding 后正文重复写入的收益另计。

独立临时 Milvus Lite + SQLiteStore 测试：1,000 个节点、1,024 维合成向量、
FLAT/COSINE 索引，正文写入 0.014 秒，合成 embedding 0.040 秒，
向量写入（含首次建集合）0.701 秒。整体调用 0.940 秒。
读回全部 1,000 个节点，验证每个向量维数为 1,024。
合成 embedding 不调用远程模型，这个数字不代表实际模型响应时间。

## 验证与限制

- Core doc 包测试通过。
- LazyLLM 定向回归 19 项通过：`test_processing_levels.py`、
  `test_sqlite_store_bulk.py`、`test_document_store_upsert_failure.py`。
  覆盖跨批次 FTS 更新、重复写幂等、其他文档保留、部分 embedding 失败、
  向量写失败和复制路径。
- 扩展运行旧 `test_document_store.py` 时 19 项均在初始化阶段失败：旧夹具通过
  `mkstemp` 预建普通文件，当前安装的 Milvus Lite 需要目录路径，报 FileExistsError /
  Open local milvus failed。本次未修改该夹具；上面的独立 Milvus 验证使用新目录成功运行。
- 没有测用户原文件的 OCR 或远程 embedding。分组转换按父节点处理，复杂文档、
  LLM 转换和模型限流仍可能影响总时间；默认单 worker 的多文件排队也仍存在。
- 初次验证后，按用户要求于 11:14 完成本地服务重启，Core 重新构建，解析服务加载修改。
  LazyLLM 改动位于子模块内，提交时需一并处理。

## 实际上传后的回调修复

11:16:52—11:16:58 的实际上传解析耗时 6.091 秒；embedding 合计约 4.59 秒，
向量写入约 1.32 秒。Worker 已成功，但任务状态仍为 WAITING。
原因是 finished queue 的无时区本地时间经 SQLite 代理读回后被附加 UTC，
回调判断与 `datetime.now()` 比较产生 naive/aware TypeError，阻塞成功通知。

- SQLite 队列 peek 通过 CAST 读取原始 finished_at 文本，保留原有墙上时间及显式偏移，
  避免代理凭空加 UTC，也避免单纯修改比较方式后额外等待 8 小时。
- 回调判断使用与输入相同的时区；无时区时间继续按本地时间处理。
- 正文预览明确不读取向量；需要向量的冷读取先初始化向量存储，修复 `_client_pool` 缺失。
- 时区、队列、冷读取、分块接口及处理阶段相关回归共 29 项通过。
- 经实际 SQLite 代理验证积压通知立即到期；11:22 重启 parse server 后，原任务
  `368ebbe7-37bf-4e13-86d4-9cc654b83678` 自动更新为 SUCCESS，finished queue 清空。
- 随后重启 parse worker；两项 ready 检查通过，原文 chunks 接口返回 200、1 个根节点。
  原文件没有重新上传或重新解析，也未手动修改数据库任务状态。
