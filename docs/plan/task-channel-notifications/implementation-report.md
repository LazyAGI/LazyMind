> 历史记录：本文描述隔离前的来源分支及旧安装包，不能作为官方基线迁移后的验证结果。当前状态见 [隔离验收清单](../notification-official-isolation/checklist.md)。

# 阶段二实现与验证报告

日期：2026-09-15。用户已明确批准“进入阶段二”；本批实现了批准的定时任务通知后端。通知相关自动验证通过。PostgreSQL 全量回归发现的既有审批测试冲突已按用户明确选择保留幂等、按实际副作用修正；定向连续 3 次通过，随后 SQLite/PostgreSQL 完整回归均通过。该项的具体复现和获准调整见 [approval-regression-review.md](approval-regression-review.md)。

## 业务结果与复用

- 微信沿用 ClawBot/iLink；飞书沿用原 Provider。新增企业微信薄适配器使用获批的 `wecom-aibot-python-sdk==1.0.1`，共用注册、账号运行管理、Inbox、消息路由、Outbox、分段和租约，不另起通知服务。
- 用户默认规则、任务独立 revision、运行快照和关闭确认已接入。新建任务复制默认配置；旧未配置任务保持未配置。只覆盖 scheduled，聊天后台任务提醒保留原行为。
- 复用并提取现有调度结果整理逻辑，主聊天持久化与调度完成走同一入口；成功通知必须对应已落库最终结果，失败和人工等待各有安全内容。普通依赖等待不发人工处理通知。增加已提交历史的后台恢复，不依赖打开任务详情。
- Core 事件与任务状态/输出事务提交；网关只接收与 Core 持久事件完全匹配的内容和目标，每一分段发送前核对总开关、目标和内部服务许可。入队确认丢失可重放。关闭标记不能被迟到的状态查询覆盖，重新开启不补发。
- 账号改为软断开，保留身份及历史；重连身份必须匹配。微信上下文按账号/收件人加密、发送前取最新值。新增账号详情、目标、引用与投递历史分页；多个收件人时不猜测默认目标。
- 明确失败和未知结果分别记录。可确定发送前失败按已有退避最多 5 次；unknown 不自动重发。人工重试生成关联原记录的新记录并保留分段进度，不重跑任务；刷新页面后可按 task_id 查询全部历史。
- 桌面后端返回 LazyMind 标识、任务标题、实际结果 200 字符预览、稳定 ID、任务定位和幂等回执。仅本地实例设备 local 可消费；系统拒绝权限不会被标成已展示。未修改前端、算法或桌面界面。

## 主要文件

| 位置 | 变更 |
| --- | --- |
| `backend/core/taskcenter/notification_*.go`、`notifications.go`、`scheduled_output.go` | 配置、所有权、快照、事件、后台交接、恢复、桌面契约与统一最终输出 |
| `backend/core/{scheduler,chat,main.go,routes.go}` | 复用创建/运行/最终结果主流程及统一权限注册 |
| `backend/core/common/orm/` | 两个现有表的通知配置字段、偏好/事件/桌面回执模型 |
| `backend/core/migrations/dev_mode/v0_3/20260914145559_add_task_notifications.{up,down}.sql` | 新增迁移；未修改旧 dev 文件 |
| `backend/core/migrations/version_mode/v0_3/20260805000000_workflow_runtime_release.{up,down}.sql` | 同步现有聚合路径 |
| `backend/channel-gateway/channel_gateway/common/` | 复用存储/公共 Worker/核心客户端，新增薄 NotificationService |
| `backend/channel-gateway/channel_gateway/wecom/` | 官方 SDK 鉴权、长连接、文本收发与安全日志适配 |
| `backend/channel-gateway/channel_gateway/{wechat,feishu}/` | 软断开、同身份重连、通知渲染复用和明确拒绝分类 |
| `backend/core/openapi_notifications.go`、[后端接口文档](../../../api/backend/task-notifications.md) | OpenAPI、参数/错误/原生客户端接入约定 |
| `local/local-runtime-manager/`、`docker-compose.yml` | 复用内部服务 Token，注入 Core/Gateway 回环地址 |

既有测试 Fixture 仅补齐实际需要的模型；迁移目录计数/版本随新迁移更新。首批通知断言未弱化。获准的安全补充只增加 Core 网络边界事件登记和拒绝伪造/篡改场景。未修改工作区审批生产代码。用户另行明确批准修正既有集成测试：检查同一操作/决定、相反决定冲突并保留文件只执行一次、跨用户拒绝和回执重放断言。

## 验证结果

两库使用真实隔离存储：文件 SQLite（非仅内存）及专用 PostgreSQL 16 随机 Schema。只创建和清理本次测试的临时资源，未清理业务库/卷。跨服务测试只替换外部网络边界，真实 Core 调度、HTTP、FastAPI、Store、Worker 和官方企微 SDK 均执行。

| 验证 | 结果 |
| --- | --- |
| Core HTTP/安全/桌面/恢复/跨服务测试 | SQLite 与 PostgreSQL 各 39 个通知叶子用例通过，0 个通知 Skip |
| 调度生命周期与真实 SQL 迁移 | 两库各 7 个叶子用例通过：4 个生命周期、3 条迁移路径 |
| Gateway、原有安全与权限提取测试 | SQLite 与 PostgreSQL 各 69 项完整通过，0 个跳过 |
| Core `go test ./...` | SQLite 与 PostgreSQL 最终完整回归均通过，各 87 个有测试的包、6 个无测试包；包含获准修正的审批集成测试 |
| PostgreSQL 失败基线核对 | 修正前工作区连续 3 次失败；未修改 HEAD 副本连续 3 次失败，确认原有测试冲突；获准修正后通过 |
| Local Runtime Manager `go test ./...` | 全部通过，包括新增双方地址/内部身份匹配测试 |
| Core 构建、gofmt、diff whitespace | 通过 |
| Python flake8 与 `uv pip check` | 通过；49 个已安装依赖兼容 |
| OpenAPI 与集中权限 | Core 新 schema 引用可解析，桌面字段/内部令牌声明齐全；网关 OpenAPI 与 read/write 权限匹配 |
| Gateway 实际进程启动 | SQLite 与 PostgreSQL 均实际启动 uvicorn、GET healthz/readyz 200、正常执行完整 Worker 退出；使用空测试账号库，没有发送消息 |
| Compose 与打包路径 | `docker compose config --quiet` 通过；双方内部 Token 相同，Gateway 无发布端口；既有 Dockerfile 和 Desktop 脚本包含适配器及 SDK requirements |

真实跨服务成功链路：保存规则 → 实际 RunNow HTTP 入口 → 调度调用聊天网络端点并落真实 ChatHistory → 公共最终输出与事务事件 → Gateway HTTP 入队 → 丢失首个入队回执 → 重新装配 Gateway → 同一记录恢复 → 飞书/微信/企微各发送一次 → Core 状态回收 → 桌面拉取与回执。SQLite、PostgreSQL 均通过。

失败、人工等待、总开关竞态、跨用户/伪造、分段未知结果、断开/重连、5 次重试和租约失效另由生命周期、API、Worker、真实 Store 集成用例覆盖；没有把所有故障的排列组合都扩展成完整跨服务场景。

### 可复现命令

先按 `tests/backend/channel-gateway/requirements-test.txt` 建立临时 Python 环境。为 PostgreSQL 设置专用 `TEST_DB_DSN`、`MIGRATION_TEST_POSTGRES_DSN`、`CHANNEL_GATEWAY_TEST_POSTGRES_DSN`；凭据不得写入文档或日志。

```bash
# 在 backend/core；两种 driver 分别执行
TEST_DB_DRIVER=sqlite go test ./... -timeout=180s
TEST_DB_DRIVER=postgres go test ./... -p 2 -timeout=600s
# 显式启用 Python 网络边界进程，避免跨服务测试被默认环境条件跳过
LAZYMIND_NOTIFICATION_TEST_PYTHON=/absolute/path/to/venv/bin/python \
  TEST_DB_DRIVER=sqlite go test . -run TestNotification -count=1 -v
LAZYMIND_NOTIFICATION_TEST_PYTHON=/absolute/path/to/venv/bin/python \
  TEST_DB_DRIVER=postgres go test . -run TestNotification -count=1 -v
TEST_DB_DRIVER=sqlite go test ./scheduler ./migrate -run TestNotification -count=1 -v
TEST_DB_DRIVER=postgres go test ./scheduler ./migrate -run TestNotification -count=1 -v

# 仓库根目录，两种 gateway driver 分别执行；本地密码测试库使用 PGGSSENCMODE=disable
CHANNEL_GATEWAY_TEST_DRIVER=sqlite python -m pytest tests/backend/channel-gateway \
  backend/channel-gateway/test_security.py tests/backend/scripts/test_extract_api_permissions.py -q --tb=short
CHANNEL_GATEWAY_TEST_DRIVER=postgres python -m pytest tests/backend/channel-gateway \
  backend/channel-gateway/test_security.py tests/backend/scripts/test_extract_api_permissions.py -q --tb=short
python -m flake8 backend/channel-gateway tests/backend/channel-gateway
# 在 local/local-runtime-manager
 go test ./... -timeout=180s
```

本次 Go 完整验证和定向验证均由临时运行包装设置 `LAZYMIND_NOTIFICATION_TEST_PYTHON`；跨服务测试实际通过，未因缺少该变量跳过。完整 Core 仓库仍有与当前部署不适用的既有条件测试，不能把这些算作本次双库覆盖。

临时证据目录：`/var/folders/r8/6qz6g7g50tsfv19lysj0j4t110tzws/T/lazymind-notification-tests-lwcvsyrk/`。主要日志：`phase2-core-final.log`、`phase2-core-pg-final.log`、`phase2-core-contract-{pg-final,final}.log`、`phase2-notification-lifecycle-{sqlite,postgres}.log`、`phase2-gateway-{verified,pg-verified}.log`、`phase2-baseline-chat-concurrency.log`。该目录为临时证据，可由上述命令重建，不是生产依赖。

## 限制与尚待验收

1. 既有工作区审批测试冲突已获准修正，详见独立 Review 说明；完整回归均已通过。
2. 没有发送真实微信、企微或飞书消息；平台可见性、真实账号长时间重连、平台具体限流码等仍需获授权后的真实收件人联调。
3. 没有实现或验收 macOS/Windows 系统通知桥接、图标、声音、勿扰模式与点击唤起。后端已提供契约；这部分需要桌面客户端范围的实现。
4. 已检查运行配置和已有依赖打包路径，但没有构建完整安装包或启动 Docker 全栈。SDK/Starlette 有 3 条上游弃用提示，测试未隐藏这些提示。临时就绪探测最初将数据库响应限时设为 1 秒，在并行迁移负载下超时；分离健康与就绪探测，并使用 10 秒数据库探测后通过，未修改生产超时。
5. 外部平台与本地数据库不能原子提交；发送成功但回执丢失采用 unknown 和用户确认重发，不能承诺平台端绝对 exactly-once。桌面客户端也须按稳定通知 ID 去重。

未提交、推送或修改 Git 配置；未修改无关的 `docs/plan/pr-710-review-fixes/`。

## 环境排查记录

- PostgreSQL 网关某轮全量运行卡在 libpq 的 GSS/Kerberos 自动认证探测。原生采样确认后停止该测试进程，仅在采用独立账号密码的本地测试环境设置 `PGGSSENCMODE=disable`；重跑全部 69 项通过（7.86 秒），没有修改生产认证配置。
- PostgreSQL Core 某轮聊天测试包达到 300 秒整包预算，未出现断言失败；改为错开数据库密集批次，以 `-p 2 -timeout=600s` 重跑后全部通过，聊天包耗时 229.563 秒。不改变业务超时、用例断言或数据库持久性设置。

## 最终结论

- 最新完整日志：`phase2-all-core-sqlite-verified.log`、`phase2-core-postgres-final.log`、`phase2-gateway-complete-sqlite.log`、`phase2-gateway-postgres-final.log`。两库 Core 各 87 个有测试的 package、Gateway/安全/权限各 69 个用例均通过。
- 本需求独立矩阵每库 46 个 Go 叶子用例通过（39 个 HTTP/桌面/恢复/跨服务，4 个生命周期，3 条 SQL 迁移路径）；本需求没有跳过项。最终 OpenAPI 错误字段补充后，独立契约测试再次通过。
- 本地运行管理器完整回归、真实 Gateway 进程双库启动/就绪/退出、Core 构建、Python lint/依赖检查、权限与 Compose 检查均通过。
- 批准的后端开发与自动验证已完成。仍需客户端实现原生弹窗并进行真实平台/系统验收；这些明确属于尚未实施的客户端或外部联调范围，不能以自动测试通过代替。

## 2026-09-16 非前端追加工作

用户已要求继续除前端外的剩余交付。本批新增 11 项后端故障回归，没有修改原已审测试断言或生产行为；原生桥接的新增方案写入 spec.md R7，已请求确认，尚未编写该批测试或生产功能。不得将此前“后端完成”扩展解释为原生弹窗已交付。

新增测试文件：

- `tests/backend/channel-gateway/test_notification_boundaries.py`：三渠道失败隔离；发送中关闭/断开后阻止后续分段；可控 30 秒 tick 驱动真实 120 秒租约续租、失效和旧 worker 防护；微信/飞书取消、过期、刷新后旧扫码结果不得覆盖账号。
- `tests/backend/channel-gateway/test_wecom_isolation.py`：真实官方 SDK 两账号同消息 ID 隔离；一账号断开不影响另一账号；真实连接 API 同身份重连、旧消息去重、新消息接收及新连接发送。仅替换 WebSocket 和飞书/微信网络发送边界，Store、公共 Worker、账号服务和 SDK 实际执行。

验证：`python -m pytest tests/backend/channel-gateway backend/channel-gateway/test_security.py tests/backend/scripts/test_extract_api_permissions.py -q`，文件 SQLite 和 PostgreSQL 各 **80 passed、0 skipped**；Python flake8 与 `git diff --check` 通过。日志为原临时证据目录中的 `phase2-gateway-expanded-{sqlite,postgres}.log`。

企微新增用例首轮错误使用只用于创建新账号的 Fixture 重连，返回空值；改为真实连接 API 后通过，未修改生产重连逻辑或既有断言。新用例未经过此前 Review，因此此项为本轮测试修正。

现有桌面打包、运行冒烟和安装后冒烟脚本的 Node 测试 **56 passed、0 skipped**（`node --test desktop/scripts/desktop-build.test.mjs desktop/scripts/runtime-smoke.test.mjs desktop/scripts/packaged-app-smoke.test.mjs`）。Windows x64 的 Core 与本地运行管理器按既有 `CGO_ENABLED=0` 配置交叉编译成功；macOS arm64 管理器按其原构建脚本的默认 CGO 配置编译成功，产物均在临时目录。最初给 macOS 套用 Windows 的无 CGO 配置时缺少原生进程扫描实现；按实际平台脚本重跑通过，未改动生产配置。交叉编译不等于 Windows 安装和运行验收。

网关按当前 Dockerfile 和 requirements.txt 构建为本地测试镜像 `lazymind-channel-gateway:notification-review-20260916`（Linux arm64）；只给此次构建设置下载超时 60 秒、重试 2 次以控制测试时长，不改生产默认值。构建命令为 `docker build --build-arg PIP_DEFAULT_TIMEOUT=60 --build-arg PIP_RETRIES=2 -t lazymind-channel-gateway:notification-review-20260916 backend/channel-gateway`。新镜像分别使用容器内文件 SQLite、专用 PostgreSQL 临时 schema，实际启动默认 uvicorn 入口，healthz/readyz 均 200，容器内 pip check 通过，停止日志包含 Application shutdown complete。未发布主机端口、未配置真实账号，临时容器及本次创建的 schema 已清理，未触碰业务数据。证据：`phase2-gateway-image-build.log`、`phase2-image-startup-{sqlite,postgres}.log`；镜像构建及运行通过不等于完整 Compose 栈验收。

真实平台测试仍等待用户指定环境和收件对象；未发送外部消息，未修改 frontend、desktop/build、desktop/dist、Git 配置或无关改动。

### 原生桌面通知进入测试 Review

2026-09-16 用户在新增方案确认点回复“继续”，R7 方案确认，N2 测试已编写并执行。新增 42 项行为/安全测试及 3 项主进程接入契约，共 45 项因原生模块和接入尚未实现而预期失败；相关既有桌面 72 项测试全部通过，语法与 diff 检查通过。没有新增或修改生产功能，当前等待本批测试 Review；完整范围、命令、失败分类及系统验收边界见 [native-test-review.md](native-test-review.md)。

### 原生桌面通知阶段二完成情况

后续用户明确“进入下一阶段开发”。现已实现主进程桥接及现有普通登录/刷新/退出事件同步，未改前端页面，已审测试断言未变。通知和相关回归 **126 项全部通过**。原有工作区失败随后经测试 Review 和用户“修复”授权处理，最新桌面全量 **174 项全部通过，无失败或跳过**。macOS 独立应用打包、签名校验、包内网关启动、真实系统 show/回执，以及真实 Electron 隔离沙箱中的登录同步均已验证；最新 DMG 已包含工作区修复，镜像完整性、只读挂载后的应用签名及当前源码一致性均通过，详见 [delivery-report.md](delivery-report.md)。完整说明见 [native-implementation-report.md](native-implementation-report.md)；此前“未实现原生桥接”的段落为当时历史状态，最新状态以该报告为准。
