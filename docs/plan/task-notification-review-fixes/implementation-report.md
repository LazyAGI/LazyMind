> 历史记录：本文描述隔离前的来源分支及旧安装包，不能作为官方基线迁移后的验证结果。当前状态见 [隔离验收清单](../notification-official-isolation/checklist.md)。

# 定时任务通知审查修复报告

日期：2026-09-16。基线：`146e4bae475e2df886da133f8366100e23536bdc`。

用户在方案整理后明确要求“直接进行代码生产修改”。本批因此直接实现并补充回归，没有执行原计划中的测试先行人工停止点；不把本次结果描述为经过了该阶段审批。

## 已修改的行为

| 审查项 | 修复及关键依据 |
| --- | --- |
| P1-01 通知故障污染任务结果 | Core 为通知持久化故障保留独立内部错误分类，回滚收尾而不转入业务失败。聊天完成和恢复扫描可观察错误；恢复只使用已提交历史，不重跑模型。真实数据库触发器拒绝通知插入的测试覆盖 PostgreSQL 和文件 SQLite，恢复后任务成功、输出与事件各一份。 |
| P2-01 迟到会话更新覆盖终态 | 会话同步在实际 UPDATE 中检查完整终态集合，仅合法迁移后生成通知。使用真实并发连接与查询屏障验证 succeeded/failed/canceled/skipped，分别覆盖启用/未配置通知，以及迟到 failed/waiting。SQLite 单独验证 WAL 快照冲突后的事务重试。 |
| P2-02 刷新破坏原生通知 | 以独立请求版本取消旧网络操作，保留通知对象生命周期。认证确认同用户同实例后继续回执和点击；验证期间 show 落盘为待回执状态。主进程 IPC 使用暂停替代无条件清理，相同有效会话重复同步不取消正在进行的点击。换号、无效身份、退出、实例变化仍隔离旧对象。 |
| P2-03 祖先记录绕过重试确认 | 在既有账号及 Outbox 事务锁内检查整条事件/目标尝试链，活动或已成功尝试阻止新建，最新 unknown 或未知分支要求确认。同 key 返回原回执，不同 key 并发只创建一条活动尝试；从有效尝试继承分段进度，保留点击来源及检查点来源，不修改历史。 |
| 旧未配置任务恢复风险 | 恢复扫描覆盖 NULL 配置任务，不回填配置或通知。生产聊天流先结束，再经生产工作流完成入口结束异步工作流，直接验证任务表和输出表。单条坏记录不阻塞后续任务或下一页。 |

Core 的主要修改位于 `backend/core/taskcenter/`；Gateway 修改现有通知 Service 和共享 Store；Desktop 修改 `main.js`、`native-notifications.js`、preload 和会话交接模块；CLI 增加原用户续期与凭证快照入口。API 契约已同步，无前端页面、算法、Schema、迁移或生产依赖变更。

## 验证结果

下表为本轮实际执行，不引用历史交付报告的通过数字。

| 验证 | 文件 SQLite | PostgreSQL |
| --- | --- | --- |
| Core 根包、chat、workflow、taskcenter、scheduler 全部测试 | 5 包通过 | 5 包通过 |
| 新增聊天收尾、异步工作流和终态并发回归 | 通过 | 通过 |
| Gateway 全套 + 原安全 + 权限提取 | 92 通过，0 跳过 | 92 通过，0 跳过 |
| 新增 Core 回归 race 检查 | chat/taskcenter 通过 | chat/taskcenter 通过 |

桌面 `desktop/scripts/*.test.mjs`：后续续期补齐后为 200 项通过，0 失败、0 跳过，包含原 174 项、上一批 10 项刷新回归和本批 16 项续期/凭证交接回归。主进程联合用例执行实际 main.js 的会话处理器及实际通知模块；操作系统 Notification、认证网络及 connector 边界仍为测试替身。原会话测试仅将清理次数断言改成“暂停后退出立即清理”的行为要求，待处理登录不能在退出后重启通知等原有断言保留。

Python 修改文件的 flake8、Go 格式、diff 空白检查已通过。上述均为最终代码验证结果。

按 Go JSON 的叶子用例计数，sqlite 为 1014 个叶子用例通过、7 个已有环境条件跳过、0 失败；postgres 为 1017 个叶子用例通过、6 个已有环境条件跳过、0 失败。

Core 相关包包含已有的环境条件跳过（真实模型、浏览器、Redis、工作区外部服务）；逐项记录于测试 JSON。通知跨服务测试已设置 `LAZYMIND_NOTIFICATION_TEST_PYTHON` 并实际执行，不把环境跳过计入通知通过。

## 真实入口及边界

- 新增 `backend/core/chat/notification_review_test.go` 从生产聊天流完成入口落库，不通过假聊天接口直接插入最终历史来证明 P1。使用数据库原生触发器制造成功通知写入故障，覆盖失败通知开/关、持续失败、恢复幂等及模型调用次数不增加。
- 同一测试通过生产 `RunNowHandler` 发起下游任务，实际完成依赖选取和持久输入快照，检查发往聊天服务的请求含恢复后的正文与源任务 ID。下游聊天传输有意返回 503，以结束后台执行；这证明依赖消费，不宣称下游模型业务也成功完成。
- 原有 Core→Gateway 跨服务测试继续执行，覆盖实际 HTTP、数据库、Outbox、Worker 和重启恢复，其聊天边界仍采用原有替身。新聊天故障回归和跨服务成功测试共同提供分层证据，尚未新增一条将所有故障、真实聊天、真实平台统一串联的系统测试。
- 新增 `backend/core/taskcenter/notification_review_test.go` 使用屏障、独立数据库执行路径和 101 条恢复候选验证终态保护及扫描公平性。
- 新增 `tests/backend/channel-gateway/test_notification_retry_chain.py` 通过真实 HTTP 入口验证整链 unknown、活动/成功/跳过、并发 key、回放、跨用户、分段进度以及旧数据分叉。

## 可复现命令

先按 `tests/backend/channel-gateway/requirements-test.txt` 准备测试虚拟环境。PostgreSQL 使用现有专用测试容器的随机隔离 Schema；凭据仅通过环境传入，不写源码或报告。SQLite 通过仓库 fixture 使用临时文件、WAL 和实际事务设置。

```sh
# backend/core；分别设置 TEST_DB_DRIVER=sqlite / postgres。
# PostgreSQL 需 TEST_DB_DSN；根包关联测试另设置 MIGRATION_TEST_POSTGRES_DSN。
# 两库均设置 LAZYMIND_NOTIFICATION_TEST_PYTHON 为测试环境 Python 绝对路径。
go test -json . ./chat ./workflow ./taskcenter ./scheduler -p 2 -count=1 -timeout=600s
go test -race ./chat ./taskcenter -run '^TestNotificationReview' -count=1 -timeout=180s

# 仓库根目录；分别设置 CHANNEL_GATEWAY_TEST_DRIVER=sqlite / postgres。
# PostgreSQL 另需 CHANNEL_GATEWAY_TEST_POSTGRES_DSN。
python -m pytest tests/backend/channel-gateway backend/channel-gateway/test_security.py tests/backend/scripts/test_extract_api_permissions.py -q --tb=short
python -m flake8 backend/channel-gateway/channel_gateway/common/application/notifications.py backend/channel-gateway/channel_gateway/common/infrastructure/postgres.py tests/backend/channel-gateway/test_notification_retry_chain.py
node --test desktop/scripts/*.test.mjs
git diff --check
```

本机完整日志位于 `/tmp/notification-review-core-full-{sqlite,postgres}.json`、`/tmp/notification-review-race-{sqlite,postgres}.log`、`/tmp/notification-review-gateway-full-{sqlite,postgres}.log` 和 `/tmp/notification-review-desktop-all.log`。首次复用旧 Python 环境因缺少 pytest 无法启动；随后新建独立测试环境安装仓库列明的测试依赖，完成上述测试，没有改动生产依赖。

## 未纳入与残余限制

- 合并发送阶段 Core 回查属于可选优化，本批保留 verify 和 claim 的现有安全校验，不删除来源或内容检查。
- 无窗口独立续期已实现，范围为常驻 Electron；完全退出进程后不运行。刷新服务已经轮换令牌但响应丢失、或身份待核验时进程退出，会要求重新登录；不假造刷新成功。
- 未执行 Windows 真机、macOS 真实点击/勿扰/通知中心、崩溃后历史通知冷启动、真实三渠道投递或完整 Compose 栈验收；没有发送真实外部消息。
- 没有新增 Schema 或迁移，不宣称本轮做了升级/回退专项验收。没有打包、部署、提交或推送。
- 既有未提交的 `docs/plan/pr-710-review-fixes/` 保持原样。代码修复和上述自动验证与正式发布验收分别看待。

## F6 后续补齐：无窗口独立续期

用户继续指出遗漏后，直接补齐生产实现，无需再次授权。关闭窗口后，轮询 401 进入主进程续期；复用现有 refresh/auth-me 端点、跨进程文件锁和凭证原子写入，新令牌必须属于原 active 用户。不会调用本地管理员自动登录，不跟随重定向。窗口存在时仍由已有页面刷新，避免同一刷新令牌被同时消费。

临时失败按既有指数退避重试。如果新令牌已收到、仅身份查询暂时不可用，候选凭证只留在主进程内存，重试身份校验而不再次消耗旧 refresh token；确认身份后才写成有效凭证。明确失效、身份不符、退出、换号和实例变化停止旧会话；待展示、回执与点击生命周期继续保留，迟到旧令牌响应失效。

重开窗口先等待当前凭证操作，preload 在页面执行前经受信主 frame 的同步内存查询换入新凭证。既有凭证文件增加可选 `desktop_handoff` SHA-256 原会话摘要，冷启动通过内部 CLI 快照加载；必须匹配页面持有的完整原令牌对及同 origin 才能交接。没有新增旧明文令牌副本、SQL、Schema、迁移、公开后端接口或生产依赖，未修改前端页面。

本批验证：

- `node --test desktop/scripts/*.test.mjs`：200 通过、0 失败、0 跳过。实际主进程续期回调、会话 IPC、窗口重建函数、preload 与通知模块都纳入行为测试；网络和 OS Notification 在这些 Node 用例中仍是替身。
- 在 `local/lazymind-cli` 执行 `go test -race ./...`：全模块通过。新增 Store 和 CLI 测试使用真实本地 HTTP 与临时凭证文件，覆盖一次性并发刷新、身份核验临时故障、撤销、403、不同用户、非 active、重定向、网络故障、登出、来源变化、快照交接和脱敏错误码。
- 原生通知测试使用可控时钟推进过期/退避，未等待真实过期时间。原窗口源码断言更新为等待凭证操作后重建窗口，并新增执行实际窗口函数的行为测试，未弱化后台常驻要求。
- Go 格式及 `git diff --check` 通过。本批没有再修改 Core/Gateway，前文两库结果属于上一批已完成回归；独立续期没有新的数据库方言路径。

本批日志为 `/tmp/notification-renewal-desktop-all.log`、`/tmp/notification-renewal-cli.log`。尚未重打包或做 macOS/Windows 真机跨真实令牌过期的长时间验收；旧安装包不自动包含本次源码变更。没有提交或推送。
