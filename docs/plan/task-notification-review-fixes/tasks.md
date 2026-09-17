# 定时任务通知审查修复任务

状态：用户已明确要求直接修改生产代码，本批改为先实现、后补充及运行回归，不再等待原方案/测试审批。下列任务保留范围和验证映射；执行结果见 implementation-report.md。行为依据为 [spec.md](spec.md)，验收依据为 [checklist.md](checklist.md)。

## 阶段一：方案批准后编写并运行测试

| 任务 | 范围与参考位置 | 验证与依赖 |
| --- | --- | --- |
| T1 真实聊天收尾故障 | 在 `backend/core/chat/` 增加回归，参考 `conversation_logic.go` 的最终历史持久化与通知收尾；复用 `backend/core/common/orm/testdb.go` | F1；只在通知写入处注入可控故障；真实历史、事务与输出；检查业务状态、错误传播、重复收尾、取消/关闭门禁；依赖方案批准 |
| T2 会话终态并发 | 在 `backend/core/taskcenter/` 增加测试，参考 `taskcenter.go`、`taskcenter_test.go`、`notifications.go` | F2；PostgreSQL 独立连接加查询/更新屏障，SQLite 验证实际写锁与最终条件；成功/失败/取消/skipped、有无通知；依赖方案批准 |
| T3 重试链 HTTP/存储 | 在 `tests/backend/channel-gateway/` 增加测试，参考 `conftest.py`、`test_notification_delivery.py`、`test_notification_recovery.py`；实际 `app.py`、Service、Store 和 Worker | F4；失败祖先/unknown 子记录、成功子记录、不同及相同 key 并发、多分支、分段进度、关闭/断开/跨用户；两库执行；依赖方案批准 |
| T4 桌面刷新与 IPC | 在 `desktop/scripts/` 新增回归，复用 `native-notifications*.test.mjs`；最小调整 spec 中明确列出的 IPC 清理次数断言 | F3；show 前/后/认证等待中刷新，点击、重复同步、新 token 为另一用户、校验失败、退出/换号/实例变化、旧响应和乱序 IPC；真实通知模块与主进程处理器联合测试；依赖方案批准 |
| T5 旧任务异步完成及依赖 | 在 Core 的 `chat/`、`workflow/`、`scheduler/` 和必要跨模块测试中补主流程；参考 `workflow/eventloop.go`、`scheduler/dependency_runtime.go`、`notification_recovery_test.go`、`notification_e2e_test.go` | F1/F5；聊天先完成、工作流后完成；配置 NULL；检查状态表/输出表/通知表及真实依赖输入，统计任务/模型执行次数；两库分别执行；依赖 T1 fixture |
| T6 测试阶段报告 | 本目录新增 `test-review.md`，必要测试专用 fixture；不改生产业务 | 按 F1—F5 列出文件、命令、通过、预期失败、异常失败、跳过和环境阻塞；断言不得用固定成功替身代替真实持久化；依赖 T1—T5 |

用户后续指令覆盖原 T6 人工停止点；本批统一在实现报告中记录测试结果与限制，不再生成无生产实现的测试阶段报告。

## 阶段二：本批测试获批后实现

| 任务 | 影响文件/目录与实施边界 | 验证与依赖 |
| --- | --- | --- |
| T7 Core 收尾与恢复 | `backend/core/taskcenter/scheduled_output.go`、`notifications.go`、`notification_gateway.go`、`taskcenter.go`；必要的 `chat/conversation_logic.go`、`scheduler/dependency_runtime.go` 或工作流接线 | F1/F2/F5：区分通知故障、保持事务一致性、传播安全错误、原子终态条件、旧任务有界恢复；不重跑任务、不补旧通知；依赖测试批准及 T1/T2/T5 |
| T8 Gateway 重试约束 | `backend/channel-gateway/channel_gateway/common/application/notifications.py`、`common/infrastructure/postgres.py`、必要的 `sqlite.py` 与 `app.py` | F4：现有事务内统一查重、链状态判断、有效尝试检查点和插入；复用现有错误码，不改变旧聊天 Outbox；依赖测试批准及 T3 |
| T9 桌面会话刷新 | `desktop/electron/src/native-notifications.js`、`main.js`；`preload.js` 仅在现有同步接线确有需要时修改 | F3：安全验证新身份、暂停旧网络、保留可恢复系统回调、同用户通知和点击；完整退出/换号隔离；依赖测试批准及 T4 |
| T10 契约与交付同步 | `api/backend/task-notifications.md`、本目录实现报告；原 `docs/plan/task-channel-notifications/delivery-report.md` 追加当前修复状态，保留历史证据 | 明确重试链错误、幂等、刷新语义、后台续期和真机门槛；无新错误码则不改 `i18n/errors/`；依赖 T7—T9 |
| T11 主流程与最终回归 | Core、Gateway 和 Desktop 受影响测试；复用跨服务 fixture，补足真实聊天/工作流完成入口；不执行真实外部投递 | 双库分别运行“完成→通知写入故障→恢复→依赖消费→Gateway 重试”；桌面“会话同步→show→刷新→点击/回执”；gofmt、pytest/flake8、Node、diff 检查；披露未验证项；依赖 T7—T10 |

## 验证执行约定

- Core 使用 `TEST_DB_DRIVER=sqlite` 与 `TEST_DB_DRIVER=postgres` 分别运行；后者必须提供专用 `TEST_DB_DSN`，凭据只从环境传递，不输出、不写文档。
- Gateway 使用 `CHANNEL_GATEWAY_TEST_DRIVER=sqlite/postgres`；后者必须提供 `CHANNEL_GATEWAY_TEST_POSTGRES_DSN`。复用 `tests/backend/channel-gateway/requirements-test.txt`，不增加生产依赖。
- 跨服务测试必须设置 `LAZYMIND_NOTIFICATION_TEST_PYTHON`，否则其跳过不能计为通过。进程内真实 HTTP/DB 贯通与完整 Compose 分开报告。
- Node 先运行通知相关测试，再按影响运行桌面全套。使用虚拟时钟和系统通知边界替身；明确这不等于真实操作系统验收。
- Go 先定向 `./chat ./workflow ./taskcenter ./scheduler` 和根包通知测试，再按实际影响运行模块回归；所有修改的 Go 文件执行 gofmt。并发场景增加相关 race 检查。
- Python 运行通知、原有安全和权限提取相关 pytest，并对修改文件执行 flake8；新增接口字段才扩大对应契约检查。
- 两库隔离资源或运行时不可用时如实报告环境阻塞。不能以另一种数据库、源码搜索或模拟 Store 代替。
- 不修改已批准测试的行为预期；如实现发现新的契约冲突，先给出具体差异。T4 中已在方案列明的断言调整于阶段一展示并 Review。

## 后续续期实现

| 任务 | 影响文件 | 验证 |
| --- | --- | --- |
| T11 原用户独立续期 | `local/lazymind-cli/internal/credentials/desktop.go`、既有凭证 Store、CLI session 入口 | 真实本地 HTTP、文件凭证、并发刷新、撤销/不同用户/重定向/错误脱敏，无管理员回退 |
| T12 后台轮询和窗口交接 | Electron main/native-notifications/notification-session/preload | 可控时钟、实际主进程处理器、关闭/重开/冷启动、退出和换号竞态、回执与点击保留 |
| T13 集成回归和文档 | Desktop 全套、CLI 全模块、契约与报告 | Node 全套及 Go race，披露真机与单次刷新响应丢失的边界 |
