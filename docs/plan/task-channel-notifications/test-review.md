# 首批测试 Review：定时任务多渠道通知

> 历史阶段一报告。用户随后明确批准“进入阶段二”；当前生产实现及验证结果见 [implementation-report.md](implementation-report.md)。以下保留当时失败和门禁记录。

日期：2026-09-14。用户已授权开始开发。本批仅新增测试、Fixture、测试依赖清单和计划文档；没有实现生产功能，没有改 Schema、安装企业微信生产 SDK、改 Electron/前端、提交或推送代码。

这是一批可运行的 Core/网关契约与数据库回归测试，不代表三渠道接入或完整端到端已经完成。新增测试没有 `xfail`、跳过标记、替代处理器或测试专用生产分支。缺失功能的失败保持可见。

## 文件与需求映射

| 文件 | 需求 | 本批已写入的断言 |
| --- | --- | --- |
| `backend/core/notification_api_test.go` | R1/R2/R3 | 默认值、旧任务未配置、新任务继承但不覆盖旧配置、事件/目标校验、禁用保留选项、恢复默认、真实 DB 并发版本冲突、用户归属、请求大小/格式、运行快照、关闭确认、聊天后台任务不受总开关影响 |
| `backend/core/notification_desktop_test.go` | R3/R6 | 任务标题和实际结果正文、200 字符预览、稳定 ID/游标、跨用户/设备拒绝、幂等回执、权限拒绝不得报已送达、关闭期间不补发、确认时复核运行任务集合 |
| `backend/core/scheduler/notification_lifecycle_test.go` | R3/R5 | 真实调度请求与输出整理路径、重复完成去重、安全失败原因、输出写入失败不得标成功、普通依赖等待不通知；只用本地 HTTP 服务替代聊天服务网络边界 |
| `backend/core/migrate/notification_migration_test.go` | 数据与兼容 | 两种数据库的 dev/aggregate/旧库升级与 up/down/up 测试代码；旧任务保持 NULL 配置、版本 0、回退保留原任务 |
| `tests/backend/channel-gateway/conftest.py` | 通用 Fixture | 原 `build_components`、真实 Store、加密器和 API；文件 SQLite 或临时 PostgreSQL Schema，隔离清理；不启动真实平台接收器 |
| `tests/backend/channel-gateway/test_notification_accounts.py` | R4/R5 | 断开保留账号和 Inbox/Outbox、在途租约失效、同身份重连保留 ID、跨用户绑定拒绝、账号视图不暴露凭据、三平台公共注册、重新装配后保留队列 |
| `tests/backend/channel-gateway/test_notification_delivery.py` | R4/R5 | 已有聊天回复回归、并发领取唯一性、旧租约不能完成、通知复用 Outbox、微信账号/收件人最新上下文隔离且加密、重复交接只入队一次、不可用目标安全拒绝、未知结果停止自动重试、错误脱敏、断开不补发、人工重试关联/确认/幂等 |
| `tests/backend/channel-gateway/test_wecom_contract.py` | R4/R5 | BotID/Secret 入参校验、企微通知内容清洗、各平台目标归属与上下文不泄漏；尚不包含企微成功鉴权及长连接会话测试 |
| `tests/backend/channel-gateway/requirements-test.txt` | 可复现环境 | 复用现有生产依赖，增加固定版本 pytest/flake8，仅供测试环境使用 |

## 运行结果

Go 数量按叶子用例统计，避免同时计算父测试和子测试。每种数据库新增 **78 个用例**：Core 41 个、网关 37 个。

| 运行 | 通过 | 需求尚未实现/行为待修改而失败 | 已有代码缺陷而失败 | 跳过/环境失败 |
| --- | ---: | ---: | ---: | ---: |
| Core：文件 SQLite | 0 | 40 | 1 | 0 |
| Core：PostgreSQL 16 | 0 | 40 | 1 | 0 |
| 网关：文件 SQLite | 8 | 27 | 2 | 0 |
| 网关：PostgreSQL 16 | 10 | 27 | 0 | 0 |
| 原有 Core 设置/偏好/调度/任务中心/迁移回归 | 143 | 0 | 0 | 0 |
| 原有网关 `test_security.py` | 4 | 0 | 0 | 0 |

新增 Go 测试可编译；gofmt 检查及新增 Python 测试的 flake8 均通过。原有 Core 回归的业务测试使用文件 SQLite；其中仓库已有 PostgreSQL 迁移验证使用专用 PostgreSQL 数据库执行，不能据此宣称所有原有业务测试都跑过 PostgreSQL。

失败分类：

1. 新通知 API 当前返回 404；企微 Provider 尚未注册，凭据模式尚未接入；通知表及迁移文件尚不存在。账号断开仍硬删除，重连产生新 ID。这些均是本次预期需要补齐或改变的行为。
2. **输出持久化失败仍被标为成功**：删除测试隔离库中的输出表模拟写入失败后，现有 `finalizeTaskOutput` 忽略写入错误并标记 `succeeded`。两库均稳定复现；这是“成功通知必须对应可查询结果”的必要修复项。
3. **SQLite 发件租约续租 SQL 语法错误**：`renew_outbound_lease` 使用的 PostgreSQL 时间表达式未被 SQLite 适配正确转换，报 `near ">": syntax error`。两个断开/在途租约用例在 SQLite 复现，PostgreSQL 对应用例通过；需修复后才能可靠支持 Desktop/Local。

多数新增用例在入口 404 或缺失表处就停止，因此后续断言虽已写入，**还没有执行到，更没有验证通过**。迁移三条路径目前停在缺失新迁移文件检查；现有仓库迁移的基线通过不代表本功能新迁移已通过。HTTP 客户端有一条上游弃用提示，不影响测试执行。

## 本批拟固化的具体契约

这些是实现前供本批 Review 的具体选择，不是已存在的能力。

- Core 延续 `{code,message,data}`，`code` 保持数字。通知错误用 `data.detail.reason` 表达稳定原因，`data.detail.request_id` 返回请求标识。本文测试没有要求把旧接口数字错误码改成字符串。
- 偏好读取包含 `enabled/revision/defaults`；任务配置包含 `configured/revision/config`。保存使用 `revision` 比较更新，返回完整最新视图。关闭总开关的确认字段为 `confirm_running_task_ids`，服务器重新比较当前受影响集合。
- 事件配置为 `events.{succeeded,failed,waiting}.{enabled,content}`，渠道配置为 `channels.{desktop,wechat,wecom,feishu}`；外部启用渠道携带稳定 `account_id/recipient_id`。关闭渠道保留原目标。
- 桌面查询为 `GET /task-center/desktop-notifications`，`device_id=local` 对应 Local/Desktop 的本地运行实例；不能用任意设备 ID 扩大范围。回执为 `POST /task-center/desktop-notifications/{notification_id}:ack`，区分 `delivered` 与 `permission_denied`。服务认证及实际本地设备绑定仍须后续集成验证，不能仅靠调用者自报参数建立信任。
- 网关接收接口候选为 `POST /api/channel-gateway/v1/task-notifications`：稳定 `event_id`、执行与规则标识、配置版本、事件、标题、正文、内容模式、渠道和目标；返回 `notification_id/outbox_id/status`。相同事件与目标重复提交只能生成一个 Outbox。相比方案中的“网关受控获取”，本批先细化接收侧；实际后台交接方式仍须与可信服务认证一起完成测试，不能将该接口直接暴露成任意用户/正文的转发入口。
- 网关查询/重试为 `GET /api/channel-gateway/v1/task-notifications/{notification_id}` 和 `POST /api/channel-gateway/v1/task-notifications/{notification_id}:retry`。重试用 `idempotency_key`；未知结果的人工重试需 `confirm_duplicate_risk`，新记录包含 `retry_of`。
- 目标列表为 `GET /api/channel-gateway/v1/channel-accounts/{account_id}/notification-targets`；企微沿用连接会话入口，增加写入型 `credentials.{bot_id,secret}`，不可回显。
- 存储候选：在 `user_schedules`、`task_center_tasks` 上增加 `notification_config/notification_revision`，分别保存规则与快照；必要新表为 `user_notification_preferences`、`task_notifications`、`desktop_notification_receipts`。外部投递仍只有原网关 Outbox。新增 dev migration 名称后缀为 `add_task_notifications`，实际 UTC 时间戳在实现时生成，不修改既有 dev migration。

## 实际命令与环境

Go 1.25、Python 3.11 临时虚拟环境，测试依赖使用上述清单。PostgreSQL 使用本机已有专用测试容器的回环端口：网关每例创建随机隔离 Schema；Core 复用已有隔离 Schema/临时数据库辅助方法。只清理本次测试创建的对象，没有删除容器、卷或业务数据；凭据从进程环境传递，未写入仓库或报告。

在已配置专用测试 DSN 的环境下，实际执行的测试命令为：

```bash
# 从仓库根目录运行，python 指向上述 Python 3.11 测试环境。
CHANNEL_GATEWAY_TEST_DRIVER=sqlite python -m pytest tests/backend/channel-gateway -q --tb=short
# 需 CHANNEL_GATEWAY_TEST_POSTGRES_DSN。
CHANNEL_GATEWAY_TEST_DRIVER=postgres python -m pytest tests/backend/channel-gateway -q --tb=short
PYTHONPATH=backend/channel-gateway python -m pytest backend/channel-gateway/test_security.py -q --tb=short
python -m flake8 tests/backend/channel-gateway

cd backend/core
TEST_DB_DRIVER=sqlite go test -json . ./scheduler ./migrate -run '^TestNotification' -count=1
# 需 TEST_DB_DSN 和 MIGRATION_TEST_POSTGRES_DSN。
TEST_DB_DRIVER=postgres go test -json . ./scheduler ./migrate -run '^TestNotification' -count=1
TEST_DB_DRIVER=sqlite go test -json ./settings ./userprefs ./scheduler ./taskcenter ./migrate -skip '^TestNotification' -count=1
gofmt -l notification_api_test.go notification_desktop_test.go scheduler/notification_lifecycle_test.go migrate/notification_migration_test.go
```

## 尚需补齐的验证

下列项目尚未覆盖或未执行成功，不能将本批测试 Review 等同于整项需求验收：

- 企微 SDK 成功鉴权、接收文本进入公共路由、心跳/断线恢复、多账号连接隔离、SDK 原始日志脱敏的网络边界 Fake 测试。
- 真实定时触发、手动立即执行、批量/对话工具创建、依赖恢复等全部入口；本批通过共享 `CreateSchedule/CreateTask` 和真实调度完成路径覆盖核心行为，不能替代全部入口集成。
- 人工待确认事项的具体状态来源、重复事项去重、新事项再次提醒。
- Core 到网关自动交接与重启恢复、集中权限/服务身份、关闭总开关与外部发送许可竞争，以及完整三渠道跨服务端到端链路；本批没有用手工 HTTP 入队冒充自动端到端。
- 分段中断恢复、明确可重试错误的 5 次上限/退避、过期租约和并发人工重试、取消/旧扫码结果覆盖防护、账号引用分页与断开确认、不同身份重连拒绝。
- 新迁移实际 up/down/路径一致性、OpenAPI/权限提取及 Desktop/Local/Compose 后端打包验证。
- 原生 macOS/Windows 通知中心、系统权限/勿扰和点击唤起；真实微信/飞书/企微投递。后者未发送任何真实消息，桌面客户端接入仍在本次后端修改范围之外。

## Review 门禁

依据根目录 [AGENTS.md](../../../AGENTS.md) 第 5 节：**“只有用户明确批准本批测试后，才进入阶段二”**。本批停在这里，尚未实现生产功能。批准本批后可实现已覆盖行为；其余场景仍需按计划补齐测试和相应 Review，不据此省略完整需求、权限、跨部署和端到端验证。
