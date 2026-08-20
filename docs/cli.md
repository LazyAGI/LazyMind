# LazyMind CLI 操作手册

`lazymind` 是 LazyMind 的命令行入口，面向算法同学和 code agent，覆盖认证、知识库管理、目录导入、任务检查、文档查看、切块检查、检索验证、上传恢复与回滚。

如果你的目标是”尽快把一批文件导入知识库，然后确认解析和检索结果是否正常”，优先看”首次使用”和”常见场景”两节。

`upload` 是状态感知的命令：每次运行都会在 `~/.lazymind/runs/<run_id>/` 留下 manifest/state/result 三份文件，支持去重跳过、中断恢复、仅重跑失败项，以及通过 `run-undo` 一键回滚。

## 1. 使用前准备

- LazyMind 服务栈已启动，默认通过 Kong 网关访问：`http://localhost:8000`
- 如果要使用默认 `retrieve` 本地模式，需要本地 `lazyllm-algo` 容器处于运行状态
- Python 3.9+

## 2. 首次使用

下面这组命令是最短闭环：注册、建库、上传、检查、检索。

```bash
# 1. 注册并自动登录
./lazymind register -u alice -p mypassword

# 2. 创建知识库，并自动设为当前默认 dataset
./lazymind kb-create --name 'project-docs' --dataset-id project-docs

# 3. 查看当前上下文
./lazymind status

# 4. 上传目录并等待解析完成
./lazymind upload --dir ./my-docs --extensions pdf,docx,txt --wait

# 5. 查看任务和文档
./lazymind task-list
./lazymind doc-list

# 6. 查看某个文档的切块结果
./lazymind chunk <document_id> --json

# 7. 做一次检索验证
./lazymind config set algo_dataset general_algo
./lazymind retrieve '介绍一下解析链路' --json
```

如果这个知识库创建时使用了自定义 `--algo-id`，这里的 `algo_dataset` 也要设置成同一个值；`general_algo` 只适用于默认算法。

如果你已经有 dataset，也可以先切换默认上下文：

```bash
./lazymind use project-docs
```

之后大多数带 `--dataset` 的命令都可以省略这个参数。

## 3. 常见使用场景

### 场景一：新建知识库并导入一批文件

```bash
./lazymind register -u algo_demo -p 'Passw0rd!'
./lazymind kb-create --name 'Parser Smoke' --dataset-id parser-smoke
./lazymind upload --dir ./docs --extensions pdf,md,txt --wait
./lazymind task-list --json
./lazymind doc-list --json
```

适用于第一次跑通解析链路，确认文件是否成功入库。

### 场景二：绑定默认知识库，后续命令不再重复传参

```bash
./lazymind use parser-smoke
./lazymind status
./lazymind upload --dir ./more-docs --wait
./lazymind task-get <task_id>
./lazymind doc-list
```

适合长期盯一个 dataset 做反复调试。

### 场景三：检查解析结果是否符合预期

```bash
./lazymind doc-list --json
./lazymind chunk <document_id> --page-size 5 --json
```

常见检查点：

- 文档名是否正确
- 文档是否进入 `SUCCESS`
- 切块内容是否完整
- 切块粒度是否符合预期

### 场景四：做一次检索 smoke test

```bash
# 设置默认 algo dataset ID，后续 retrieve 可省略 --algo-dataset
./lazymind config set algo_dataset general_algo

# 默认模式：本地优先进入 lazyllm-algo 容器执行检索
# 检索时会自动携带当前 dataset 作为 kb_id 过滤条件
./lazymind retrieve '介绍一下解析链路'

# 指定 runtime_models 配置文件执行检索
./lazymind retrieve '介绍一下解析链路' \
  --config ./algorithm/chat/runtime_models.yaml \
  --json
```

适合在修改解析、embedding 或 retriever 配置后做快速回归。

### 场景五：清理数据

```bash
./lazymind doc-delete <document_id> -y
./lazymind kb-delete -y
```

如果 `kb-delete` 不传 `--dataset`，默认删除当前 `use` 选中的 dataset。

### 场景六：传错目录想整批干掉

```bash
# 找到那次 run
./lazymind run-list

# 回滚这次 run 上传的所有 document
./lazymind run-undo <run_id> -y
```

`run-undo` 会删除该 run 创建的所有 document，同时清理本地 uploaded 索引。

### 场景七：漏了几个文件想补上

```bash
# 把新文件放进目录，直接再跑一次即可
./lazymind upload --dir ./docs --wait
```

去重基于 `relative_path + size + mtime`，已存在且未变化的文件会自动跳过（identical existing 永远 skipped）。

### 场景八：改了几个文件想重传

```bash
./lazymind upload --dir ./docs --replace-changed --wait
```

CLI 把 mtime/size 变化的文件归为 `changed`，`--replace-changed` 会先删除远端旧 document 再上传新版本。

### 场景九：上传过程中断，想继续

```bash
# 上次被 Ctrl-C 中断，终端提示了 run_id
./lazymind upload --resume <run_id>
```

恢复会跳过已成功的文件，只处理剩余的 pending 项。

### 场景十：只重跑上次失败的

```bash
./lazymind upload --retry-failed --wait
```

会自动找到该 dataset 最近一次 run 里的 `failed` 文件，单独组成新 run 重试。

## 4. 认证与登录

CLI 通过 Kong 网关访问服务，需要先登录。凭证默认保存在 `~/.lazymind/credentials.json`，文件权限为 `0600`。

`access_token` 过期后，CLI 会自动使用 `refresh_token` 刷新；如果刷新也失败，需要重新登录。

### 注册

```bash
./lazymind register -u <username> -p <password> [--email user@example.com] [--no-login]
```

默认注册后自动登录；如果只想创建账号、不登录，加 `--no-login`。

### 登录

```bash
./lazymind login -u <username> -p <password>
```

如果不传 `-u` 或 `-p`，CLI 会进入交互式输入，密码输入不回显。

### 登出

```bash
./lazymind logout
```

会尽力调用服务端登出接口，并删除本地保存的凭证。

### 查看当前用户

```bash
./lazymind whoami [--json]
```

## 5. 上下文与配置

### use

```bash
./lazymind use <dataset_id>
```

将某个 dataset 设为当前默认值，后续 `upload / task-* / doc-* / chunk` 都可以省略 `--dataset`。

### status

```bash
./lazymind status [--json]
```

会输出当前 CLI 上下文，包括：

- 当前 server
- 是否已登录
- 当前 username / role
- 当前默认 dataset
- 当前 `algo_url`
- 当前 `algo_dataset`

### config

```bash
./lazymind config list [--json]
./lazymind config get <key>
./lazymind config set <key> <value>
./lazymind config unset <key>
```

常用配置项：

- `dataset`
- `algo_url`
- `algo_dataset`

示例：

```bash
./lazymind config set algo_dataset general_algo
./lazymind config set algo_url http://localhost:8001
./lazymind config list
```

## 6. 知识库管理

在 LazyMind 里，CLI 对外叫“知识库”，对应 core service API 里的 `dataset`。

### 新建知识库

```bash
./lazymind kb-create --name 'My KB' [--desc 'description'] [--algo-id my_algo] [--dataset-id custom-id]
```

说明：

- `--name` 必填，知识库展示名
- `--dataset-id` 可选，显式指定 dataset ID；不传则自动生成
- `--algo-id` 可选，关联算法 ID；默认取本地 `algo_dataset` 配置或环境变量，通常是 `general_algo`

### 列出知识库

```bash
./lazymind kb-list [--page-size 20] [--page 2] [--json]
```

### 删除知识库

```bash
./lazymind kb-delete [--dataset <dataset_id>] -y [--json]
```

## 7. 目录上传与任务查看

`upload` 是状态感知的批量导入命令，支持去重、中断恢复、失败重试。每次运行都会在 `~/.lazymind/runs/<run_id>/` 留下完整记录。

### 三种模式

`upload` 必须指定一种模式（`--dir` / `--resume` / `--retry-failed` 三选一）：

```bash
# 模式一：扫一个新目录
./lazymind upload --dir ./docs --wait

# 模式二：恢复上次中断的 run
./lazymind upload --resume <run_id>
./lazymind upload --resume /abs/path/to/manifest.json

# 模式三：只重跑该 dataset 最近一次 run 的失败项
./lazymind upload --retry-failed --wait
```

### 参数速查

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--dir` / `--directory` | 无 | 要上传的本地目录 |
| `--resume` | 无 | 恢复指定 run（`run_id` 或 manifest 绝对路径） |
| `--retry-failed` | 无 | 仅重跑最近一次 run 的失败项 |
| `--extensions` | 全部后缀 | 逗号分隔，例如 `pdf,docx,txt` |
| `--limit` | 不限制 | 最多上传多少个文件 |
| `--recursive` / `--no-recursive` | 递归扫描 | 是否扫描子目录 |
| `--include-hidden` | `false` | 是否包含隐藏文件和隐藏目录 |
| `--replace-changed` | `false` | 对 `changed` 文件先删除旧 doc 再上传 |
| `--wait` | `false` | 阻塞等待所有解析任务结束 |
| `--wait-interval` | `3.0s` | `--wait` 模式下的轮询间隔 |
| `--wait-timeout` | `0` | 最长等待秒数，`0` 表示一直等 |
| `--timeout` | `300s` | 单个文件上传请求超时 |
| `--report-json <path>` | 无 | 额外输出机器可读报告到文件 |
| `--json` | `false` | stdout 输出结构化结果 |

### 去重分类规则

`upload` 在正式上传前会比对三处信号：

- 本地扫描结果（`relative_path + size + mtime`）
- 远端 `doc-list`（`relative_path + document_size`）
- 本地 uploaded 索引 `~/.lazymind/datasets/<dataset_id>/uploaded.json`（`size + mtime`）

分类规则：

- `new`：远端无该路径，或远端 doc 状态是 `FAILED` → 直接上传
- `changed`：远端有该路径但 size 不一致，或本地索引中的 size/mtime 与当前不一致 → 默认**不上传**并打印警告，除非传 `--replace-changed`
- `existing`：远端健康且 size 一致 → **总是 skipped**（identical 文件没有重传的理由）

跨 run 索引 (`~/.lazymind/datasets/<ds>/uploaded.json`) 只会在任务真正 SUCCESS 后才写入。解析失败的 document 不会污染索引，下次运行会正确地重新上传。

### 示例

```bash
# 第一次上传（尚未登录就要先 login）
./lazymind upload --dir ./documents --extensions pdf,docx --wait

# 追加新文件（identical existing 自动 skipped）
./lazymind upload --dir ./documents --wait

# 覆盖被修改的文件
./lazymind upload --dir ./documents --replace-changed --wait

# 只重跑失败的
./lazymind upload --retry-failed --wait

# 恢复中断的 run（KeyboardInterrupt 后终端会显示 run_id）
./lazymind upload --resume 20260414T163045-ds_abc123

# 额外写机器可读报告
./lazymind upload --dir ./documents --wait --report-json /tmp/report.json --json
```

### 本地 run 目录结构

```text
~/.lazymind/
├── datasets/<dataset_id>/
│   └── uploaded.json          # 跨 run 的 uploaded 索引
└── runs/<run_id>/             # 每次 upload 一个目录
    ├── manifest.json          # 扫目录快照
    ├── state.json             # 实时进度（uploaded/skipped/failed/...）
    └── result.json            # 最终总结
```

每上传一个文件都会原子地更新 `state.json`，即使 Ctrl-C 也能安全 `--resume`。

### 目录层级处理说明

CLI 会把文件的相对路径传给服务端，但当前服务端只会根据 `relative_path` 的第一层路径创建一个顶层文件夹。

这意味着：

- `reports/q1/summary.pdf` 会进入顶层文件夹 `reports`
- 不会在服务端重建成 `reports/q1/summary.pdf` 这样的完整嵌套目录树

如果你依赖完整目录层级，请不要把当前行为当成已支持能力。

### 查看任务列表

```bash
./lazymind task-list [--dataset <dataset_id>] [--page-size 20] [--json]
```

### 查看单个任务详情

```bash
./lazymind task-get [--dataset <dataset_id>] <task_id>
```

### 取消运行中的任务

```bash
./lazymind task-cancel <task_id> [--dataset <dataset_id>] [--json]
```

对应后端 `:suspend` 端点，实际会把任务状态转为 `CANCELED`。

### 恢复被挂起的任务

```bash
./lazymind task-resume <task_id> [--dataset <dataset_id>] [--json]
```

仅对 `SUSPENDED` 态任务有效；对 `CANCELED` 任务的行为由后端决定。

## 8. 上传 run 管理

每次 `upload` 都是一个 run，本地状态存放在 `~/.lazymind/runs/<run_id>/`。通过 `run-list` / `run-undo` 查看历史和回滚。

### 列出历史 run

```bash
./lazymind run-list                    # 当前默认 dataset 的 run
./lazymind run-list --dataset <id>     # 指定 dataset
./lazymind run-list --all              # 所有 dataset
./lazymind run-list --json
```

每行会显示 `run_id / status / uploaded / failed / created_at / dataset`。

### 回滚一次 run

```bash
./lazymind run-undo <run_id> [-y] [--json]
```

`run-undo` 会：

1. 读 `~/.lazymind/runs/<run_id>/state.json`
2. 对每个已上传的 `document_id` 调用 `DELETE /datasets/{ds}/documents/{doc_id}`
3. 从本地 `uploaded.json` 索引中移除对应条目
4. 把该 run 的 `status` 标为 `undone`

单个删除失败时会继续处理剩余，最后汇总打印错误项。

## 9. 文档与切块检查

### 查看文档列表

```bash
./lazymind doc-list [--dataset <dataset_id>] [--page-size 20] [--json]
```

### 修改文档元信息

```bash
./lazymind doc-update [--dataset <dataset_id>] <document_id> \
  --name 'new-name.txt' \
  --meta '{"source":"manual-check"}'
```

### 删除文档

```bash
./lazymind doc-delete [--dataset <dataset_id>] <document_id> -y
```

### 查看切块

```bash
./lazymind chunk [--dataset <dataset_id>] <document_id> [--page-size 20] [--page 2] [--json]
```

适合用于确认解析后的 `segments / total_size / 内容片段` 是否符合预期。

## 10. 检索验证

### 最简单的用法

```bash
./lazymind retrieve '介绍一下解析链路'
```

默认行为是：

- 如果显式传了 `--url`，直接访问指定 algo service
- 如果本地配置了 `algo_url`，优先使用该地址
- 否则尝试自动找到本地运行中的 `lazyllm-algo` 容器，并在容器内执行检索
- 远程 `Document` 默认使用 `algo_dataset` 配置，通常是 `general_algo`
- 当前知识库通过 `kb_id=<dataset_id>` 作为过滤条件传给 retriever
- 如果知识库创建时指定了自定义 `algo_id`，需要把 `algo_dataset` 配置成相同值

### 常用参数

```bash
./lazymind retrieve '介绍一下解析链路' \
  --dataset project-docs \
  --algo-dataset general_algo \
  --group-name block \
  --topk 6 \
  --similarity cosine \
  --embed-keys embed_1 \
  --json
```

### 使用 runtime_models 配置文件

```bash
./lazymind retrieve '介绍一下解析链路' \
  --config ./algorithm/chat/runtime_models.yaml \
  --json
```

这个模式适合验证某份 `runtime_models.yaml` 中定义的检索配置是否真的能跑通。

## 11. 环境配置

### Server 地址

CLI 默认连接 `http://localhost:8000`。覆盖方式如下：

- 任意命令显式传 `--server URL`
- 设置环境变量 `LAZYMIND_SERVER_URL`
- 使用登录后保存在本地凭证里的 `server_url`

优先级：

`--server` > 本地凭证中的 `server_url` > `LAZYMIND_SERVER_URL` > 默认值

### 凭证目录

如果不想使用默认的 `~/.lazymind/`，可以设置 `LAZYMIND_HOME`：

```bash
export LAZYMIND_HOME=/custom/path
./lazymind login -u alice -p pass
```

此时凭证会写入 `/custom/path/credentials.json`。

## 12. 命令速查

### 认证

```bash
./lazymind register -u <username> -p <password>
./lazymind login -u <username> -p <password>
./lazymind logout
./lazymind whoami
```

### 上下文

```bash
./lazymind use <dataset_id>
./lazymind status
./lazymind config list
./lazymind config get <key>
./lazymind config set <key> <value>
./lazymind config unset <key>
```

### 知识库

```bash
./lazymind kb-create --name 'My KB'
./lazymind kb-list
./lazymind kb-delete -y
```

### 上传与任务

```bash
./lazymind upload --dir ./docs --wait
./lazymind upload --dir ./docs --replace-changed --wait
./lazymind upload --resume <run_id>
./lazymind upload --retry-failed --wait
./lazymind task-list
./lazymind task-get <task_id>
./lazymind task-cancel <task_id>
./lazymind task-resume <task_id>
```

### 上传 run 管理

```bash
./lazymind run-list
./lazymind run-list --all
./lazymind run-undo <run_id> -y
```

### 文档与切块

```bash
./lazymind doc-list
./lazymind doc-update <document_id> --name 'new-name.txt'
./lazymind doc-delete <document_id> -y
./lazymind chunk <document_id> --json
```

### 检索

```bash
./lazymind retrieve '介绍一下解析链路'
./lazymind retrieve '介绍一下解析链路' --topk 10 --group-name block --json
./lazymind retrieve '介绍一下解析链路' --url http://algo:8000 --algo-dataset general_algo
./lazymind retrieve '介绍一下解析链路' --config /path/to/runtime_models.yaml
```

## 13. 系统链路说明

```text
CLI (lazymind)
  |
  v
Kong API Gateway (:8000)
  |-- /api/authservice/*  --> auth-service (FastAPI)
  |-- /api/core/*         --> core service (Go)
                                 |
                                 v
                          doc-server / parse-server / parse-worker
```

CLI 基于 Python 标准库 `urllib` 实现，没有引入额外依赖；所有请求都通过 Kong 网关，鉴权依赖 JWT。

## 14. 代码目录

```text
cli/
├── __init__.py
├── __main__.py
├── main.py
├── config.py
├── credentials.py
├── context.py
├── upload_state.py        # run 目录 + uploaded 索引 + dedup 分类
├── client.py
└── commands/
    ├── __init__.py
    ├── auth.py
    ├── chunk.py
    ├── context.py
    ├── dataset.py
    ├── doc.py
    ├── retrieve.py
    ├── run.py             # run-list, run-undo
    ├── task.py            # task-cancel, task-resume
    └── upload.py
lazymind
tests/test_cli.py
```

## 15. 已知边界

- 上传目录时，服务端只会按第一层路径创建顶层文件夹，不支持完整嵌套目录重建
- 默认 `retrieve` 本地模式更适合本地 compose 或开发环境；远程部署场景建议显式传 `--url`
- 当前测试仍以单测为主，完整端到端行为仍需要结合真实运行栈做集成验证
- 上传失败项不会自动重试，需要用 `lazymind upload --retry-failed` 或 `--resume` 手动触发
- 本地 run 目录不会自动清理，`~/.lazymind/runs/` 需要用户自行清理（没有内置 `runs prune` 命令）
- 后端 `task-cancel` 实际对应 `:suspend` 端点，转移到 `CANCELED` 态，不是真正的暂停；`task-resume` 只对 `SUSPENDED` 态任务有效
- dedup 信号源：远端 `doc-list` 只能提供 `relative_path + document_size`，精到 `mtime` 的判断依赖本地 `~/.lazymind/datasets/<ds>/uploaded.json` 索引。首次在一台新机器上对一个已有 dataset 运行 `upload`，所有文件会被保守地归为 `existing`（除非远端 doc 状态是 FAILED）
- 跨 run 索引只会在任务真正 SUCCESS 之后才写入：非 `--wait` 模式下 `uploaded.json` 不会更新，下次运行 dedup 退化为"所有文件都没有本地索引"的保守态
- 当前 CLI 是单机串行上传，没有 `--jobs` 并发（规划中）

## 16. 让外部 Agent 使用 LazyMind

外部 Agent 统一通过宿主机 `lazymind mcp proxy` 使用 LazyMind。Desktop 和标准 Docker 启动命令都会自动托管这个 connector：

- LazyMind Desktop 继续使用内置进程托管和 IPC。
- `make up` / `make up-build` 会在 Docker 服务启动后自动构建并启动回环地址上的「助理桥接器」，`make down` 会停止它。
- 用户打开“设置 → 助理”，点击对应卡片的“连接”即可；不需要执行 `mcp add`、复制 JSON 或单独启动 Agent Host。
- Docker 用户没有安装 Go 时，`make up` 会用 Docker 中的 Go 工具链交叉编译当前宿主机的 connector，不新增宿主机开发依赖。

桥接器只监听 `127.0.0.1`，只接受本机 LazyMind 页面来源。它使用现有本地会话引导凭证，并且写入 Agent 配置的内容只有 connector 绝对路径、`mcp proxy` 参数以及可选的 `LAZYMIND_HOME`；不会写入 access token 或 refresh token。

直接执行裸 `docker compose up` 无法启动宿主机进程，因此标准用户入口是 `make up`。源码调试仍可使用 `lazymind agent guide <agent>` 查看备用配置，但它不再是正常安装流程。

### 16.1 Codex CLI

“设置 → 助理 → Codex CLI → 连接”会复用现有 Codex Adapter：先验证 LazyMind 的 20 个工具，再调用 Codex 原生 `codex mcp add`。重新连接和断开分别更新或移除同一个受管条目，不直接编辑 Codex 私有配置。

如果已经存在一个不属于当前 LazyMind 安装的同名 `lazymind` 服务，桥接器会拒绝覆盖或删除。连接成功后新建 Codex 任务即可加载工具。

### 16.2 Desktop 与 Docker 使用同一入口

Desktop 通过 Electron IPC 调用 connector；Docker 网页通过本机回环 Assistant Bridge 调用相同 adapter。两种入口的探测、归属校验、工具预检和最终配置完全一致，没有 Plugin、Marketplace 或 Codex Skill 分支。

### 16.3 Cursor

桥接器直接在 `~/.cursor/mcp.json#mcpServers` 中原子合并受管的 `lazymind` 条目，并保留其他服务和顶层设置。第一次修改前会创建 `.lazymind-backup`；断开时只删除归属于当前 connector 的条目。Cursor 中已经存在的外来同名条目不会被覆盖。连接后新建 Cursor Agent 会话即可。

### 16.4 WorkBuddy

桥接器检测 CodeBuddy CN / WorkBuddy，并原子合并 `~/.codebuddy/mcp.json#mcpServers`。备份、归属判断和精确移除规则与 Cursor 相同，不再要求用户进入 MCP 页面粘贴 JSON。连接后在 WorkBuddy 中新建会话即可。

### 16.5 TRAE Work

桥接器写入 TRAE Work Agent 实际读取的用户级 `mcp.json#mcpServers`，不会调用会写入错误 `servers` 字段的 `--add-mcp`。它保留其他配置并只管理当前 LazyMind 条目。连接后重启 TRAE Work 或新建会话。TRAE Work 本期仍只作为 MCP 客户端，不出现在 LazyMind 的对话执行器列表中。

### 16.6 DeepSeek Harness

桥接器在 `~/.dsh/profiles/web/cordis.patch.yml` 顶层列表中合并一个 ID 为 `mcp-lazymind` 的官方 `@deepseek-ai/dsh-mcp-client` 条目。YAML 中的其他 patch 和注释会保留，首次修改前同样创建备份；断开只移除该 insert。Harness 会热加载这个 profile，MCP 调用仍进入统一 Invocation Ledger。DeepSeek Harness 本期同样不是 LazyMind 对话执行器。

### 16.7 能力和安全边界

五个 Agent 得到完全相同的 20 个 Tool。其中六个是只读 Capability：

- `knowledge.list`
- `knowledge.document.list`
- `knowledge.document.get`
- `knowledge.search`
- `skill.list`
- `skill.get`

另外 14 个是通用 Workflow Driver：

- `workflow.list`
- `workflow.get`
- `workflow.input.import`
- `workflow.input.get`
- `workflow.start`
- `workflow.state`
- `workflow.session.list`
- `workflow.session.stop`
- `workflow.session.resume`
- `workflow.step.begin`
- `workflow.step.resume`
- `workflow.step.submit`
- `workflow.artifact.list`
- `workflow.artifact.get`

使用 Workflow 时，外部 Agent 先通过 `workflow.start` 建立持久会话，再按 `state → step.begin → 原生执行 → step.submit` 循环运行。Agent 丢失 Session ID 时用 `workflow.session.list` 找回；需要中止或继续整个会话时用 `workflow.session.stop` / `workflow.session.resume`；只有连接器中断但步骤仍在执行时才用 `workflow.step.resume` 恢复同一个 execution。LazyMind 固定 Workflow revision，并负责步骤状态、输入见证、产物内容、列表顺序和版本；外部 Agent 只执行 `step_contract`，不会成为 Core 内的 Executor 实现。

每个 `tools/call` 都会先建立持久调用记录，结束后写入成功、失败或中断状态，并关联 Workflow、Session、Step、Attempt、Artifact 等安全 ID。记录只保存参数哈希和白名单摘要，不保存 prompt、知识库查询正文、文件内容、绝对路径、Base64 或访问令牌。当前版本已经收集这些数据，LazyMind 前端展示留到后续版本。

`knowledge.search` 始终只返回检索结果，不进入 chat 或回答生成链路。LazyMind 不共享或接管外部 Agent 会话；新配置通常在外部 Agent 新建会话后生效。

### 16.8 用外部 Agent 执行 LazyMind 对话

“外部 Agent 使用 LazyMind MCP”和“外部 Agent 替代 LazyMind ChatAgent”是两个独立方向。完成前面的 MCP 配置后，Cursor、WorkBuddy、TRAE Work 和 DeepSeek Harness 可以在自己的界面调用 LazyMind；要让外部 Agent 在 LazyMind 对话界面内生成回复，还必须具备可靠的非交互运行、事件和会话恢复接口。当前只接入以下三个官方 CLI：

- Codex 使用 `codex exec --json`；
- Cursor 使用独立的 `cursor-agent`，安装后执行 `cursor-agent login`；
- WorkBuddy 使用独立的 CodeBuddy Code CLI（`codebuddy` 或 `cbc`），启动交互会话后执行 `/login`。

Cursor IDE 和 CodeBuddy CN 已登录，不代表这两个独立 CLI 已登录。LazyMind 不读取、复制或保存外部 Agent 的登录凭证。

LazyMind Desktop 和 Docker Assistant Bridge 都会自动托管本机已经安装的三个 CLI；用户不需要另起 `agent host` 进程。缺失的 CLI 只会把自身注册为不可用，不影响其他 provider。然后可在 LazyMind 的对话配置中选择 Codex、Cursor 或 WorkBuddy；若对应 Host 尚未连接，界面会拒绝切换并给出提示。`lazymind agent host ...` 仅保留为开发诊断入口。

TRAE Work 没有满足该边界的无头结果/事件/恢复接口；DeepSeek Harness 当前的外部执行协议也不能恢复既有会话，且与运行时 MCP 注入存在约束。因此二者不会注册 `ExternalChatRun` Host，不会出现在 `chat_executor` 中。这不影响它们在各自界面调用 LazyMind MCP。

所有 provider 共用同一条数据链：LazyMind 创建和持久化对话轮次，本机 Host 调用外部 CLI，CLI 通过既有 LazyMind MCP 使用 Knowledge、Skill 和 Workflow，结果仍回到 LazyMind 的 SSE、历史、刷新续流和停止链。每个 LazyMind 会话只续接该 provider 自己的 session/thread ID；切换 provider 不会把一家产品的私有会话 ID 交给另一家。外部 CLI 的文件操作被放在 `~/.lazymind/agent-workspaces/<conversation_id>` 的独立目录中，Workflow 产物和版本仍以 LazyMind Runtime 中的记录为准。
