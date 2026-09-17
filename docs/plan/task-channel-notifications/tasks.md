# 定时任务多渠道通知：任务与验证计划

状态：2026-09-15 已进入用户批准的阶段二。T5～T11 后端实现已完成，T12 的本需求双库主流程、回归、权限/契约及配置检查已执行。首批测试历史保留在 [test-review.md](test-review.md)，当前结果和未验收范围见 [implementation-report.md](implementation-report.md)。既有工作区审批测试冲突已按用户明确选择修正，SQLite/PostgreSQL 完整回归均通过。

## 执行顺序与人工门禁

1. 完成 T1 并批准三份计划。
2. 仅执行 T2～T4：新增测试、Fixture、Fake、可控时钟和编译必需的无逻辑声明；不新增生产行为、业务迁移或生产依赖。
3. 提交测试阶段报告并等待用户 Review。预期失败应来自功能缺失，环境失败单列，禁止以跳过代替验收。
4. 用户明确批准本批测试后执行 T5～T12；已批准测试需要改动时重新说明并申请批准。

## 任务清单

| ID | 任务与产出 | 影响文件/目录 | 依赖 | 参考位置 | 验证 |
| --- | --- | --- | --- | --- | --- |
| T1 | 确认渠道协议、任务范围、升级默认值和企微 SDK；定稿契约、阈值、设备及重试语义 | 本目录三份文档 | 无 | 飞书需求正文 3.1～5、`bootstrap.py`、官方企微 SDK | 每项未决问题有用户决定，三个文档一致 |
| T2 | 将 R1～R6 转成 Core 行为、契约及安全测试；覆盖所有创建/执行入口 | `backend/core/userprefs/`、`settings/`、`scheduler/`、`taskcenter/` 下测试及必要 `tests/backend/` Fixture | T1 获批 | `userprefs/ui_preferences_test.go`、`scheduler/automation_test.go`、`taskcenter/workflow_task_test.go`、`routes_test.go` | 默认继承、CAS 冲突、快照、关闭确认、事件时机、设备归属、异常输入有稳定失败 |
| T3 | 补网关账号、适配与 Outbox 测试；建立协议 Fake，覆盖三渠道同一公共路径 | 新建 `tests/backend/channel-gateway/` 聚焦测试；必要的网关就近测试 | T1 获批 | `common/application/providers.py`、`workers.py`、`wechat/delivery.py`、`feishu/delivery.py`、`test_security.py` | 软断开/同身份重连、所有权、目标绑定、SDK 错误、上下文隔离、去重/重试/未知结果 |
| T4 | 新增双库迁移、事务并发、跨服务故障与后端端到端测试；形成测试阶段报告 | `backend/core/migrate/` 测试、`tests/backend/channel-gateway/`、必要 `tests/backend/` 集成 Fixture | T2、T3 | `migrate/repository_postgres_test.go`、`repository_sqlite_test.go`、网关两种 Store | 真 PostgreSQL + 文件 SQLite；入队确认丢失、并发 worker、关闭竞态和重启恢复；报告预期/异常失败后等 Review |
| T5 | 实现规则、快照及事件/桌面记录的最小持久化；保留旧账号历史 | `backend/core/common/orm/`、`backend/core/migrations/dev_mode/v0_3/` 新 up/down、已有 `version_mode/v0_3/` 聚合、网关 `common/infrastructure/postgres.py` 和 `sqlite.py` | T4 测试获批 | `taskcenter_models.go`、`ui_preferences_models.go`、迁移目录 `AGENTS.md`、现有初始化路径 | 两库建库、升级、回退、数据保留；聚合/dev 等价；并发唯一约束 |
| T6 | 实现用户默认配置、任务配置和确认/版本控制；所有创建入口共用默认初始化 | `backend/core/settings/`、`userprefs/`、`scheduler/`、`routes.go`、必要 OpenAPI 文件 | T5 | `controls.go`、`ui_preferences.go`、`CreateSchedule`、批量/工具创建入口 | R1/R2、不覆盖已有任务、关闭保留目标、无效配置、恢复默认、CAS |
| T7 | 生成运行快照和持久事件，接入真实成功/失败/等待路径及实时总开关 | `backend/core/scheduler/`、`taskcenter/`、必要 `workflow/` 状态写入位置与 `main.go` 装配 | T5、T6 | `sendScheduledChatRequest`、`finalizeTaskOutput`、`dependency_runtime.go`、任务状态更新路径 | 不依赖打开任务详情；成功结果已落库；等待不发半成品；改规则只影响下一次执行；关闭期间不补发 |
| T8 | 完成账号软断开、同身份重连、目标和引用视图，补微信通知上下文 | 网关 `app.py`、`common/application/providers.py`、`common/domain/channel.py`、`common/ports/`、`wechat/`、`feishu/` 必要入口及 Core 引用查询 | T5、T6 | `delete_account`、`account_view`、`connect_referenced_account`、现有账号服务 | 断开无级联丢失、已排队阻断、新账号不改绑定、身份错配拒绝、微信上下文加密且按目标隔离 |
| T9 | 用获批 SDK 增加薄企业微信适配器，装配到现有 Provider 注册 | 新 `backend/channel-gateway/channel_gateway/wecom/` 必需文件、`bootstrap.py`、获批的 `requirements.txt` 及必要打包配置 | T3/T4 获批、D2/依赖获批、T8 | 官方 SDK、飞书/微信适配端口、`ProviderRegistry`、`AccountRuntimeSupervisor` | 鉴权、接收文本、公共路由、主动通知、自动重连/显式断开、超时、SDK 日志脱敏；多账号隔离 |
| T10 | 持久事件幂等交接、公共 Outbox 通知、状态查询和人工重试 | Core 任务通知接口、网关 `common/infrastructure/lazymind.py`、`common/application/workers.py`、现有 Store 与 Delivery 适配器 | T7～T9 | 原 `channel_outbox`、确定性分段发送 ID、任务监控用途过滤 | 同事件不重复入队；失败隔离；重试不重跑任务；失败原记录保留；平台未知结果不盲重发 |
| T11 | 实现桌面后端查询/回执与接口契约、安全错误和权限同步 | `backend/core/taskcenter/`、路由/OpenAPI、`api/backend/` 后端契约文档、`i18n/errors/`、`backend/scripts/` 生成的必要权限产物 | T6、T7、T10 | 现有任务视图、公共认证/错误、OpenAPI 生成/权限提取测试 | 用户/设备隔离、游标稳定、重复回执幂等、无授权不标成功；列明桌面端后续接入 |
| T12 | 完成主流程接入、三种部署验证、后端端到端和回归，更新交付文档 | 前述受影响后端模块；按需要 `local/local-runtime-manager/`、Compose/后端打包源配置、后端接入文档 | T5～T11 | `local/local-runtime-manager/channel_gateway_service.go`、既有 Compose/Kong 路由 | 见下方端到端验收；逐项说明平台真机/系统弹窗验证状态，不提交、不推送 |

## 2026-09-16 追加的非前端工作

用户已要求继续除前端外的剩余交付。N1/N4 继续执行已有后端阶段二范围；2026-09-16 用户确认 R7 后，以“进入下一阶段开发”批准 N2 测试，N3 原生桥接已实现，原 45 项测试全部通过。补充真实 HTTP、实际主进程 IPC 并发和普通登录同步回归后共 54 项通知测试通过；未修改已批准断言。阶段一历史见 [native-test-review.md](native-test-review.md)，当前交付及系统验收边界见 [native-implementation-report.md](native-implementation-report.md)。

| ID | 工作 | 文件与复用位置 | 验证与依赖 |
| --- | --- | --- | --- |
| N1 | 补齐渠道失败隔离、断开/分段竞态、企微多账号和旧扫码结果回归 | `tests/backend/channel-gateway/`，复用现有真实 Store/Worker 与网络边界 Fake | 文件 SQLite、PostgreSQL 分别执行；发现安全缺陷先复现并按规则 Review |
| N2 | 原生通知测试阶段 | `desktop/scripts/` 新增行为测试，必要的无业务声明；复用现有 Node 测试风格 | R7 方案批准后，覆盖登录隔离、分页/退避、系统事件、重复/重启、回执失败、点击和退出；报告预期失败，等待本批测试 Review |
| N3 | 原生通知实现与接入 | `desktop/electron/src/` 最小主进程模块及 main.js 生命周期/现有会话 IPC；必要桌面打包源配置 | N2 测试批准后实现，Node 回归、实际 macOS 系统验收；不修改 frontend |
| N4 | 独立打包及平台联调 | 现有 desktop/scripts、后端部署脚本；本目录交付报告 | 先复用已有构建验证；隔离输出和运行状态；macOS/Windows 分别报告，真实渠道等待指定收件对象 |

## 原后端阶段一测试矩阵

| 需求 | 正常与边界 | 故障、安全、并发 | 验证层级 |
| --- | --- | --- | --- |
| R1 默认设置 | 初始值、部分更新、默认事件至少一个、全部创建入口 | 读写失败无部分提交，跨用户，未知字段/值和请求大小，默认配置并发 | 单元、HTTP 契约、双库 |
| R2 单任务规则 | 无渠道可保存、关闭保留目标/内容、恢复默认、未配置与失效区分 | 旧版本冲突、目标归属/平台不符、账号状态变化 | HTTP、双库事务 |
| R3 快照/门禁 | 定时/手动/依赖路径、输出就绪、成功/失败/人工等待 | 总开关与出队竞争、运行中修改、关后开不补发、重复事件及进程恢复 | 状态集成、双库、可控时钟 |
| R4 账号/目标 | 多账号追加、断开、同 ID 重连、引用和接收对象列表 | 跨用户身份占用、错账号重连、旧登录结果、失效凭据、消息上下文串号 | API、协议 Fake、双库 |
| R5 投递 | 摘要/全文、分段、每渠道状态、手动重试关联 | 超时/限流/认证失败、成功回执丢失、分段恢复、租约失效、并发重试、敏感信息 | Worker 集成、协议 Fake、双库、获授权后的平台实测 |
| R6 桌面 | 按用户截图提供任务名称/实际结果摘要/定位数据、游标分页、客户端回执 | 设备/用户隔离、未授权、重复查询/回执、总开关抑制 | HTTP 契约、双库；macOS/Windows 原生通知中心、LazyMind 标识、勿扰模式与点击定位另行客户端验收 |
| 数据与部署 | 新库、旧库升级、up/down、两种运行配置 | 数据保留、约束索引一致、SQL 方言、SQLite 文件锁、依赖打包失败 | 迁移与部署集成 |

明确不适用：不测试新密码算法、不新增支付/OAuth 体系、不为本期做外部 Agent 任务桥接。前端交互、系统弹窗和平台实际可见性不以单元测试代替。

## 后端端到端验收

在隔离数据库和可控平台端点完成：用户保存默认规则 → 连接两个同平台账号及其目标 → 新建并绑定定时任务 → 触发真实任务执行链 → 固定快照 → 任务结束且输出可查询 → 事件持久化 → 网关幂等入队 → 三外部渠道/桌面分别产生投递记录。

在同一链路中注入：一渠道明确失败、发送响应丢失、运行中更新配置、关闭总开关、断开账号、Core/网关重启。验证其他渠道正常、任务结果不变、重试只投递、历史和绑定保留、下一次执行使用新配置，关闭期间通知不补发。

端到端测试不能直接调用新通知内部方法跳过真实任务状态与输出路径。平台 Fake 只替代网络边界，不替代 Core/Store/Worker 实际行为。真实平台联调另需用户明确测试收件人及发送授权，不要求用户把凭据发到对话中。

## 计划命令与报告要求

所有命令在实现阶段按实际影响选择；这里仅为计划，不表示已运行。PostgreSQL 使用专用测试实例和现有测试 DSN 机制，禁止连接业务库做迁移验收。

```bash
cd backend/core && go test ./userprefs ./settings ./scheduler ./taskcenter ./migrate
cd backend/core && go test ./...
PYTHONPATH=backend/channel-gateway python3 -m pytest tests/backend/channel-gateway backend/channel-gateway/test_security.py -v --tb=short
python3 -m flake8 backend/channel-gateway tests/backend/channel-gateway
cd local/local-runtime-manager && go test ./...
```

Go 文件实现后运行 gofmt；HTTP 契约改动增加 Core OpenAPI、网关 OpenAPI 和 API 权限提取检查；若影响运行配置，增加相应配置/打包验证。双库集成测试分别列出实际数据库种类和未运行原因，不能用总计绿色掩盖 Skip。

测试阶段报告须包含 R1～R6 映射、实际测试文件、完整命令、通过/预期失败/异常失败/跳过数量及环境阻塞，并明确“尚未实现生产功能，等待 Review”。实现报告须列出复用位置、新增文件/依赖、安全与兼容检查以及未验收的客户端/真实平台范围。
