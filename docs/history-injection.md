# LazyMind 历史记录注入规范

本规范描述历史注入协议和发布流程。正式只读历史数据包托管在 ModelScope，Git 仓库只保存 `desktop/history-injection-package.json` 中的 URL、大小、SHA-256 和对话 ID，不提交任何注入 ZIP。本地审查素材统一放在仓库同级的 `../history-injection/`；Docker 准备脚本位于 `scripts/prepare_history_injection.py`。Core 在数据库迁移和内置 Workflow 初始化完成后执行注入，因此 Windows 安装器 warmup、macOS 首次启动 warmup、普通 Desktop 首次启动和 Docker Core 启动共用同一套导入逻辑。

## 目录约定

```text
../history-injection/
└── <workflow-category>/
    ├── README.md
    ├── <bundle-id>/          # 本地审查/打包目录；被 .gitignore 忽略
    │   ├── manifest.json
    │   ├── data.sql
    │   └── payload/
    │       ├── uploads/
    │       └── subagent/
    └── <bundle-id>.zip       # 本地生成物；被 .gitignore 忽略
```

ZIP 根目录必须直接包含 `manifest.json`，不能额外套一层目录。ZIP 与同名展开目录同时存在时按 `bundle_id` 去重，并优先使用 ZIP，确保 warmup 的解压路径也被测试。

## manifest.json

- `schema_version`：当前固定为 `1`。
- `bundle_id`：全局稳定且带版本的标识，例如 `ppt-league-of-legends-v1`。
- `category`：Workflow 分类，例如 `ppt`、`image`。
- `conversation_id` / `session_ids`：导出源的稳定 ID。
- `source_owner_id`：仅用于把权限列替换成新安装用户；不会把源用户注入到目标端。
- `workflow_ref` / `workflow_revision`：保留历史运行时使用的编译图。导入时 revision 会挂到目标端已安装的同一 Workflow resource 上。
- `sql_file`：PostgreSQL / SQLite 通用的导入 SQL。
- `files`：每个文件的目标虚拟根、相对路径、大小、权限和 SHA-256。

只允许两个目标根：

- `uploads` → `LAZYMIND_UPLOAD_ROOT`
- `subagent` → `LAZYMIND_SUBAGENT_WORKSPACE`

包内数据库值和 PPT 元数据统一保留以下可移植路径，不得写开发机、同事电脑或 Desktop 用户目录的绝对路径：

- uploads 使用 `/var/lib/lazymind/uploads/...`
- SubAgent 使用 `/data/subagent/...`

Docker 直接使用这两个路径；Desktop/Python Workflow 在读取时根据
`LAZYMIND_UPLOAD_ROOT` 和 `LAZYMIND_SUBAGENT_WORKSPACE` 映射到本机目录。
PPT 的 `lazymind-ppt-source` 元数据必须记录当前源 HTML 的 SHA-256；如果人工修改
页面或标题，必须同步重新发布产物或更新该哈希后再打包。

禁止绝对目标路径、`..`、软链接、未登记文件和含密钥/Token 的内容。

## SQL 规范

`data.sql` 必须同时兼容 PostgreSQL 16+ 与 SQLite 3.24+：

- 显式列名；一条 `INSERT` 一个分号。
- 使用 `TRUE` / `FALSE`、标准单引号转义和 `ON CONFLICT DO NOTHING`。
- 不使用 `COPY`、`::jsonb`、`RETURNING`、PostgreSQL sequence 或 SQLite 专属 pragma。
- 自动增长列（当前为 `workflow_events.id`）不导出。
- 用户列写成 `{{OWNER_USER_ID}}`，用户名写成 `{{OWNER_USER_NAME}}`。
- `plugin_sessions.plugin_revision_no` 写成 `{{WORKFLOW_REVISION_NO}}`。
- 面向首次启动展示的样例对话必须在导入结束前执行
  `UPDATE conversations SET is_task_conv = FALSE WHERE id = '<conversation_id>';`，
  以保证用户打开“快速问答”时可以直接看到；`TRUE` 只用于“新建任务”历史。
- SQL 不写 `BEGIN/COMMIT`；注入器统一开启事务。
- SQLite 导入完成后，注入器会把声明为 JSON 的公共列，以及迁移中为兼容 SQLite 而声明为 TEXT 的 `json.RawMessage` 列，规范为 BLOB 存储；SQL 文件本身仍保持 PostgreSQL / SQLite 通用语法。

## 同事交付与发布

同事应交付一个完整的 `<bundle-id>.zip`，以及在注入器协议发生变化时才需要交付代码。收到后：

1. 将 ZIP 复制到本地 `../history-injection/<category>/`。
2. 不修改 ZIP 内 ID、SQL 或 payload 路径。
3. 运行 Core 单测和该 bundle 的空库注入测试。
4. 将所有正式 bundle 组成外层 ZIP；外层解压后必须得到
   `history-injection/<category>/<bundle-id>.zip`。
5. 上传外层 ZIP 到 ModelScope，并更新
   `desktop/history-injection-package.json` 的 URL、文件名、大小、SHA-256 和
   `conversationIds`。该 ID 列表用于 Docker 在下载前查询 PostgreSQL。
6. 不要用 `git add -f` 提交任何内层或外层 ZIP。

Desktop 默认不再把案例 ZIP 放入安装包。Windows/macOS 构建脚本只复制发布描述文件，
将 URL、大小和 SHA-256 写入运行时清单；构建阶段不下载案例。现有案例打包、上传 ModelScope、
更新 `desktop/history-injection-package.json` 的流程保持不变，内置 Workflow 定义/执行脚本和 Skill 不变。

安装后的 warmup（或首次普通启动）在 Python 准备之后、Core 启动之前下载并校验案例，
缓存到 `<runtimeRoot>/cache/history-injection/<sha256>.zip`，将外层 ZIP 的 `history-injection/`
解压到用户数据目录，再启动 Core 导入；不修改已签名应用目录。匹配的解压标记存在时即使 ZIP 缓存被清理，
也无需联网；只缺解压目录时复用校验后的缓存。失败清理临时文件，保留之前的案例，允许基础服务继续启动，
下次启动重试；用户取消整个启动则正常取消。单次下载最多等待 3 分钟，不提供断点续传。

Actions 的 `defer_history` 和本地 `LAZYMIND_DESKTOP_DEFER_HISTORY` 默认开启；设为 false 恢复原有
“构建时下载校验、安装包内置 ZIP、warmup 本地解压”方式，用于离线发行和体积对照。
切换模式时构建脚本会清除相反模式的案例载荷，防止 `resume` 把旧 ZIP 带回精简包。
`LAZYMIND_HISTORY_INJECTION_ENABLED=false` 仍用于关闭 Core 的案例注入；该开关不改变安装包构建方式。

Docker 不在 Core 镜像构建阶段下载数据。`history-injection-init` 是复用 Chat/algorithm
镜像的一次性容器：它先查询持久化 PostgreSQL；五条对话全部存在时直接退出，缺少时才把外层 ZIP 下载并缓存到
`data/core/uploads/.history-injection/cache/`，然后校验、解压到相邻的 `bundles/`。
Core 等该容器成功结束后启动。已校验的下载和解压缓存都会复用，因此重新构建 Core、重启
Core 或再次执行 `make up` 不会重复下载。

## 启动顺序

```text
GitHub Action 只写案例下载元数据 → Desktop warmup 准备 Python → 下载/复用缓存、校验并解压到用户数据目录
→ Core 数据库迁移 → 内置 Workflow seed → 发现/解压内层 bundle ZIP → 登录本地 admin 获取 user_id
→ 校验 manifest/SHA-256 → 原子复制文件 → 单事务导入 SQL → Core 对外 healthy

Docker: history-injection-init 查询 PostgreSQL → 已存在则跳过；缺少则复用缓存或下载、校验、解压
→ Core 数据库迁移 → 内置 Workflow seed → 注入内层 bundle → Core 对外 healthy
```

注入按 conversation ID 幂等：同一用户已有该对话时仅补齐/校验文件；若相同 ID 已属于其他用户则拒绝覆盖。warmup 失败时安装器本身仍可完成，但 Core 日志会明确报告 bundle；普通首次启动也会再次执行同一流程，因此不是只能依赖安装器 warmup。


## 案例后置安装回归（2026-09-20）

- 精简构建：runtime 只有 `history-injection-package.json` 和 manifest 下载描述，不包含 `history-injection.zip`；已有源案例目录/ZIP 的排除规则继续有效。
- 联网首次 warmup：下载发布的 74,408,904 字节（约 70.96 MiB）案例包，通过 SHA-256 后解压，Core 中出现原有五个案例及产物。
- 再次启动：不重复下载或导入重复会话；移除下载缓存后离线启动仍可使用已装案例。
- 首次断网/404/错误大小/坏 SHA：应用能启动，日志提示案例暂不可用；恢复网络重启后补齐，不激活半成品。
- 升级案例版本：校验成功后替换案例缓存目录，原会话数据不由下载器删除；失败保留旧案例。
- `defer_history=false`：恢复随安装包携带案例，断网 warmup 仍可注入。
- Windows 安装器和 macOS 首次启动分别验证；案例内容、Workflow 执行能力和 Skill 内容回归保持原有流程。
