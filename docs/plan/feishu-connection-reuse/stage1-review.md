# 阶段一测试审查

2026-09-18，用户确认“普通断开保留加密凭据，凭据有效时重连免授权；解除绑定清除凭据”后，已更新 spec/tasks/checklist，并新增本批测试。尚未实现生产功能，未修改既有测试、真实连接数据或桌面包。

## 文件和结果

| 范围 | 新增场景 | 通过 | 预期失败 |
| --- | ---: | ---: | ---: |
| 后端：文件 SQLite | 29 | 6 | 23 |
| 后端：PostgreSQL 独立测试 Schema | 同一批 29 | 6 | 23 |
| 前端：飞书连接界面 | 5 | 1 | 4 |

新增文件：

- `tests/backend/channel-gateway/test_feishu_connection_reuse.py`
- `frontend/src/modules/notifications/FeishuReuse.test.tsx`

既有回归：`test_notification_accounts.py` 的 13 项 SQLite 测试通过；`Connections.test.tsx` 的 2 项前端测试通过。新增 Python 文件 flake8（120 字符上限）、前端 ESLint、git diff --check 通过。

后端预期失败：尚无 pause/resume 路由，返回 405；默认连接仍进入 preparing 注册流程；create_new 尚未被契约接受；注册适配器尚无新增/重新授权参数。前端预期失败：断开仍调用旧清密钥接口；重连没有直接调用恢复；独立解除绑定/新增机器人入口缺失。

初次前端测试有测试翻译 Mock 引起的重复渲染超时，已在本批尚未审查的测试中改为稳定的翻译函数；最终失败均为上述目标行为断言，未保留超时或环境失败。没有修改已 Review 的旧测试。

## 行为映射

- 复用：默认单个已有机器人、多个明确选择、同名机器人不混淆、选择原 account_id，及已结束连接会话的读取。
- 暂停：密文原样保留，状态 disconnected，停止已领取消息的租约与后续投递，收发历史不删除。
- 恢复：不调用注册，不新增账号，不重发暂停前已终止消息；重复与并发恢复只触发一次运行启动。
- 解除绑定：保持旧 DELETE 清密钥行为；缺失/损坏密钥时返回 FEISHU_REAUTHORIZATION_REQUIRED；解除绑定与恢复竞态后不得留下无密钥却 connected 的账号。
- 安全：所有权检查、其它渠道拒绝新生命周期操作、凭据不出现在响应、不同意图不可复用同一幂等键、严格布尔新增意图。
- 创建/授权：首次创建与显式新增的并发注册去重；SDK 适配器将原应用重新授权与强制新建区分。真实飞书网络与扫码未执行。

## 可复现命令

仓库根目录，使用已安装的测试环境：

```bash
/tmp/lazymind-notification-review-fixes-venv/bin/python -m pytest tests/backend/channel-gateway/test_feishu_connection_reuse.py -q --tb=short
/tmp/lazymind-notification-review-fixes-venv/bin/python -m pytest tests/backend/channel-gateway/test_notification_accounts.py -q --tb=short
/tmp/lazymind-notification-review-fixes-venv/bin/python -m flake8 tests/backend/channel-gateway/test_feishu_connection_reuse.py --max-line-length=120
```

PostgreSQL 使用 `CHANNEL_GATEWAY_TEST_DRIVER=postgres` 和 `CHANNEL_GATEWAY_TEST_POSTGRES_DSN` 执行同一新增测试文件。本次复用已存在的专用测试容器 `lazymind-notification-review-pg`；连接凭据只放子进程环境，不写入源码/日志，Fixture 仅创建并清理本次随机测试 Schema，未清理业务数据库。

前端目录，使用 Node 24：

```bash
node node_modules/vitest/vitest.mjs run src/modules/notifications/FeishuReuse.test.tsx src/modules/notifications/Connections.test.tsx
node node_modules/eslint/bin/eslint.js src/modules/notifications/FeishuReuse.test.tsx
```

日志：`/tmp/lazymind-feishu-reuse-sqlite-stage1.log`、`/tmp/lazymind-feishu-reuse-postgres-stage1.log`、`/tmp/lazymind-feishu-reuse-frontend-stage1.log`、`/tmp/lazymind-feishu-reuse-baseline.log`。

## 边界及下一阶段

测试使用真实文件 SQLite/真实 PostgreSQL，复用实际网关组件；仅替换外部注册线程启动和运行启停回调，保证测试不会联系真实飞书。运行线程实际停止、飞书凭据远端撤销后的反馈、扫码重新授权和机器人 app_id 不变，以及桌面重建后的系统联调仍需阶段二验收。原规则引用保留主要由账号 ID 不变和消息历史保留断言覆盖，尚未跨 Core 创建真实通知规则联调。取消/过期及中断恢复沿用现有流程，阶段二如改动该路径需补充相应测试并审查，不把本批视为这些路径已完整覆盖。

请审查本批 34 个新增场景及 spec 的接口细化。批准后进入生产实现；任何需要修改已批准测试、数据库 Schema 或扩大范围的发现仍按仓库规则处理。
