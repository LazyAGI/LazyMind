# 本机资源移动预算校准（待 8091 live 核对）

日期：2026-10-20。工作树：`/Users/dengyuang/.codex/worktrees/knowledge-management-mcp/LazyMind_dev`。

本次只验证本机直接连接磁盘 SQLite 的资源事务、原件复制与元数据查询，不等同于 8091 sqliteproxy 部署表现。未启停任何应用服务，未修改运行环境配置，未开启正式跨库入口。

## 可复现测试

新增 `backend/core/doc/resource_calibration_test.go`，默认跳过，显式运行：

```sh
LAZYMIND_RESOURCE_BUDGET_CALIBRATION=1 go -C backend/core test ./doc -run '^TestResourceBudgetCalibration$' -count=1 -v
```

测试连续运行三次，每次使用独立磁盘 SQLite 与独立原件目录。临时根目录创建在工作树的 `backend/core/doc` 下，并将测试数据库及原件约束到该目录，确保与工作树同卷；结束后关闭连接并删除本次临时目录。已检查没有残留的 `.resource-budget-calibration-*` 目录。

机器为 Apple M4 Pro，Darwin arm64，Go 1.26.1。SQLite 为 WAL、synchronous=NORMAL（PRAGMA 值 1），安装了实际资源迁移中的 SQLite 版本触发器。所有数据均为新生成的测试文件，无用户数据。

每次形状固定：一个根目录、20 个空子目录、100 个 TXT 原件；共 121 个资源，原件合计 33,554,432 bytes（32 MiB）。文件通过真实 `OriginalService.AcquireOriginal` 和 `ResourceService.Upload` 写入，再执行真实跨库 `ResourceService.Move`。每次移动后读取目标的 100 份原件，逐一校验 SHA-256，并检查资源映射、目录计数与查询结果。

源库和目标库都使用 `stored`。使用实际 stored 提交流程，没有用伪造的解析成功响应替代解析；本次没有执行解析、切分、索引、embedding 或搜索服务。Scan 权限服务由进程内测试响应提供，不包含真实账号、Scan HTTP、sqliteproxy RPC 延迟。原件刚由测试写入，操作系统缓存可能较热；未清空系统缓存、未模拟冷盘或并发负载。

## 三次实测

下表单位均为毫秒。准备阶段包含作用域／权限预检、源树读取、真实目标原件复制、哈希与文件验证。根事务从实际 BeginTx 到 Commit 返回计时；后续清理和回执更新不会覆盖这个计时。总耗时包含提交后真实本地源原件清理与回执更新。

| 次数 | 准备及复制 | 根事务含提交 | 提交后处理 | 完整移动 | 有效准备吞吐 MiB/s |
| --- | ---: | ---: | ---: | ---: | ---: |
| 1 | 711.499 | 44.961 | 112.293 | 868.753 | 44.975 |
| 2 | 529.700 | 39.522 | 32.197 | 601.420 | 60.412 |
| 3 | 496.139 | 36.733 | 33.704 | 566.576 | 64.498 |
| 中位数 | 529.700 | 39.522 | 33.704 | 601.420 | 60.412 |
| 最大耗时 | 711.499 | 44.961 | 112.293 | 868.753 | — |

三次样本不足以估计可靠的生产 p95，因此保留每次结果、中位数和最大值。

移动后，同一目标库的元数据查询实测如下。Catalog 包含 100 个文档及其逻辑路径；metadata 为真实 merged metadata 加载；aggregate 按文件类型、处理阶段、标签三维聚合；stats 校验 100 文件和 32 MiB 总量。

| 次数 | Catalog 100 条 | Metadata 100 条 | 三维 aggregate | 库 metadata stats |
| --- | ---: | ---: | ---: | ---: |
| 1 | 4.538 | 1.759 | 2.624 | 0.579 |
| 2 | 3.598 | 1.651 | 2.879 | 0.622 |
| 3 | 3.366 | 1.572 | 2.698 | 0.536 |

这只覆盖 100 文件规模，不证明 10,000 文档聚合预算、深层目录、大量既有目标资源、真实外部索引关联或并发查询的性能。

## 提供给 8091 live 校准的保守候选

以下 JSON 是供后续 8091 验证的候选，本次未写入运行环境。当前入口仍应保持未开放，直到 sqliteproxy 和完整端到端验收通过。

```json
{
  "max_resources": 128,
  "max_bytes": 33554432,
  "prepare_seconds": 15,
  "copy_bytes_per_second": 4194304
}
```

- `max_resources=128` 仅略高于已测的 121 资源形状；`max_bytes=32 MiB` 不超出本次实际复制量。
- `copy_bytes_per_second=4 MiB/s` 约为最慢实测有效准备吞吐的 1/11。32 MiB 的预算估算为 8 秒，给真实 HTTP/RPC、调度和磁盘差异保留余量。
- `prepare_seconds=15` 限制准备阶段；请求总截止时间还要保留已经实现的 3 秒提交／回执窗口。若实际剩余时间不足，服务会在复制／移动前返回结构化超限结果。
- 三次根事务均小于 45 ms，但这不构成 sqliteproxy 的事务延迟或锁竞争保证。需要用同规模在 8091 验证，记录实际准备／事务／完整移动时间及并发读响应，必要时降低资源／字节上限或吞吐估计。

原始输出：`/tmp/resource-budget-calibration.log`。默认未设置 opt-in 的测试已验证为 SKIP，避免普通测试意外产生大批文件。本次不修改服务默认预算，也不将本机结果标记为完整部署校准完成。

## 8091 live follow-up

The candidate was enabled only for the parent's 8091 acceptance run. Three sequential real public MCP moves through the actual sqliteproxy deployment, each copying 100 originals / 32 MiB and preserving 20 empty folders, completed in 820.960 / 791.301 / 806.783 ms. Resource-count and byte-count overages were rejected with zero migration. Public catalog/metadata/aggregate statistics also matched. This is an actual deployed API measurement, distinct from the direct SQLite phase timings above. It remains a local, largely warm-cache, stored-processing sample rather than a production p95 or parser/index benchmark. Full details and retained fixtures: resource-live-acceptance.md.
