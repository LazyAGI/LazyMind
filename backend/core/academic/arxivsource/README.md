# arXiv source

独立的 arXiv 标识符解析和官方元数据客户端。该包不写数据库、不创建文献、不下载 PDF，也不依赖浏览器开发代理。文献与附件入库由 `academic/arxiv_import.go` 负责，PDF 下载由 `academic/acquirer.go` 负责。

```go
identifier, err := arxivsource.Parse("arXiv:2404.06654v3")
paper, err := arxivsource.NewClient().Lookup(ctx, identifier.ID())
```

## 支持的输入

- 新编号：`2404.06654`、`arXiv: 2404.06654v3`，以及合法的 `[cs.CL]` 等学科后缀。
- 旧编号：`hep-th/9901001v2`、`math.CA/0611800v2`；旧编号的冗余学科分类规范化为 `math/0611800v2`。
- 官方 `arxiv.org`、`www.arxiv.org`、`export.arxiv.org` 的 `abs`、`pdf`、`html`、`format`、`src`、`e-print` 链接。
- HTTP/HTTPS、有或无协议、PDF 的 `.pdf` 后缀、查询参数、片段、尾随斜杠、Markdown 链接和尖括号自动链接。
- `10.48550/arXiv.2404.06654v3`、`doi: ...`、`https://doi.org/10.48550/arXiv.2404.06654v3`。

解析消费完整输入，不从任意网站 URL 或一段文字里猜测编号。月份、序号和版本必须有效；`0704–1412` 使用四位序号，`1501` 以后使用五位。版本格式为 `vN`，`N` 是从 1 开始的整数。

## 官方请求策略与版本

`NewClient()` 固定向 `https://export.arxiv.org/api/query` 发起 `id_list` 查询，绝不访问用户粘贴的地址。生产客户端在进程内共享请求闸门：请求至少间隔 3 秒，且前一个响应读取结束前不发起下一个请求。30 秒上下文超时包含排队等待，调用者取消请求也立即生效。跨进程或多实例部署需在上层协调同一供应商的总体请求限额。

明确指定的版本必须与返回版本一致。未指定版本时，查询最新版本并锁定官方响应中的实际版本。成功结果的 `version` 始终非空，`abs_url` 与 `pdf_url` 根据经过校验的实际编号生成，例如 `https://arxiv.org/pdf/2404.06654v3`。不采信响应中的任意下载链接，不跟随 API 重定向，不降级到第三方元数据或占位标题。

返回 `Metadata` 使用 snake_case JSON：`title`、`authors`、`year`、`abstract`、可选的 `doi`、`venue`、`base_id`、`version`、`abs_url`、`pdf_url`。`venue` 为文献来源 `arXiv`；`year` 来自实际 `published` 日期。缺少 DOI 时不生成 DOI。

`NewClientWithOptions` 仅用于可信代码的依赖注入和本地 HTTP 测试；不能把请求中的地址传给 `APIBaseURL`。正常业务使用 `NewClient()`。

## 错误语义

所有失败均返回 `*LookupError`，调用方应保留 `code`、`message` 和有效的 `status_code`，不得转换成成功条目。

| code | 含义 |
| --- | --- |
| `invalid_input` | 编号、版本、链接格式或主机不被接受 |
| `http_error` | 官方服务返回非 200 状态，包括限流和重定向 |
| `network_error` | 网络、读取、取消或超时失败；`Unwrap` 保留原因 |
| `invalid_response` | HTML、无效 Atom/XML、错误条目、缺失元数据或版本、超大响应等 |
| `not_found` | 有效 Atom feed 中没有论文条目 |
| `identifier_mismatch` | 响应的论文编号或显式指定的版本不匹配 |

## 应用接口

- `POST /api/core/academic/arxiv:lookup`：`{ "input": "..." }`，只查询真实题录。
- `POST /api/core/academic/arxiv:import`：`{ "input": "...", "target_dataset_id": "..." }`，保存文献父条目和实际 PDF 附件。
- `GET /api/core/academic/arxiv/library?target_dataset_id=...`：恢复已保存附件及真实处理状态。

接口使用现有登录身份和知识库权限。导入按 base ID 关联父条目、按知识库和版本去重 PDF。`processing_status` 区分 `ready`、`parsing`、`failed`；文件已保存但后续处理失败仍返回错误，文档 ID 保留用于恢复。前端只走这些 Core 接口，不需要 `/arxiv` 开发代理。

### 多实例导入

PDF 存在性检查、下载、文件入库和关联保存全程由数据库连接持有互斥锁，按规范化的 arXiv base ID 串行化，因此也覆盖同一知识库、同一版本的重复请求，以及跨知识库或版本共享的父条目。PostgreSQL 使用独立物理连接上的 session advisory lock，支持共享数据库和附件存储的多个 Core 实例；等待锁不会占用应用连接池。

PostgreSQL 锁连接单独禁用 `idle_session_timeout`，避免下载期间空闲超时释放锁；应用连接池仍保留原有超时策略。导入会定期检查锁连接，并在下载完成、创建文档前再次检查；检测到会话丢失后取消下载和后续数据库操作，返回明确的锁丢失错误。

SQLite 使用主数据库旁的 `<数据库文件>.academic-import-locks/` 目录，按编号散列到最多 128 个按需创建的协调数据库，在其中持有 `BEGIN IMMEDIATE`。这样不会锁住文档写入所用的主数据库。多个 Core 进程必须访问同一主库和协调目录；桌面本地 SQLite proxy 通过 `PRAGMA database_list` 获取该路径。远程 proxy、内存数据库或不可访问的数据库路径会明确失败，不会退回进程内锁。运行期间不要删除协调目录。两种数据库的锁都随连接关闭或进程退出释放，无需过期租约或数据库迁移；已经保存的文档继续通过恢复标记或可信导入任务恢复。

数据库锁语义参考：[PostgreSQL advisory locks](https://www.postgresql.org/docs/current/explicit-locking.html#ADVISORY-LOCKS)、[SQLite transactions](https://www.sqlite.org/lang_transaction.html)。

## 验证

在 `backend/core` 运行 `go test -race ./academic/arxivsource`。测试使用本地 HTTP 服务，覆盖格式解析、版本锁定、错误传播、恶意或错误响应、请求串行化、间隔和取消，不依赖真实网络。下载 URL 的版本保留与冲突测试位于 `academic/acquirer_arxiv_test.go`。

`go test ./academic -run '^(TestArxivImport|TestSQLiteArxivImport)'` 验证独立进程的并发去重、进程退出后恢复、取消等待和本地 SQLite proxy 锁共享。设置 `TEST_DB_DRIVER=postgres` 与测试用 `TEST_DB_DSN` 可对 PostgreSQL 执行相同的导入和进程测试。

依据：[标识符规范](https://info.arxiv.org/help/arxiv_identifier.html)、[旧编号规范化](https://info.arxiv.org/help/arxiv_identifier_for_services.html)、[API 和版本语义](https://info.arxiv.org/help/api/user-manual.html)、[API 请求限制](https://info.arxiv.org/help/api/tou.html)。

可选真实网络集成验证：在 `backend/core` 执行 `LAZYMIND_TEST_ARXIV_LIVE=1 go test ./academic -run '^TestArxivLiveImport$' -count=1 -v`。使用 RULER v3 官方题录及真实 PDF，验证生产下载器、文件入库、服务重建后恢复、同版本去重；数据库和附件均位于临时测试空间，不写用户文献库。官方限流或网络失败会直接使测试失败，默认测试不会访问外网。
