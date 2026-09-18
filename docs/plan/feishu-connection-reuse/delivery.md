# 飞书机器人复用交付

## 已实现

用户在阶段一报告之后明确要求“直接实现，最后再测试”；本次按该指令连续完成实现和统一验证。原 34 个场景的断言保留，修正前端取消会话 Mock 缺少 Promise 返回值的问题，并增加 20 个边界场景（后端 18、前端 2）。

- 普通断开暂停收发，保留加密凭据。恢复同一 account_id，不扫码、不创建应用，不补发已终止队列。
- 明确解除绑定仍使用原 DELETE 清除凭据，保留通知规则引用、账号和历史。
- 默认连接复用唯一已有机器人，多机器人要求明确选择；独立“新增机器人”传 create_new=true。
- 缺失/不可解密的凭据要求重新授权原机器人；远端撤销凭据可通过独立重新授权入口处理。reauthorize=true 必须指定原账号，不能同时新增。
- SDK 重新授权关闭 create_only，有可解密的 app_id 时指向原应用；最终校验 app_id + open_id。新建流程也不能覆盖已绑定机器人。
- 重新授权成功通过事务替换原凭据、完成会话，保留原身份和历史；失败/取消/过期不删除原账号。暂停/解绑取消该账号进行中的授权，防止迟到回调恢复连接。

## 复用和边界

复用现有账号服务、注册 SDK、会话幂等键、分布式运行租约、所有权校验、凭据加密和队列终止逻辑。没有新服务、生产依赖或数据库 Schema。恢复使用凭据版本条件更新，只有改变状态的请求启动运行实例；既有运行管理器继续协调实际连接。

接口使用 qa.write，权限提取器已识别新增 pause/resume 路由。API 文档、手工 OpenAPI 源和生成客户端已同步；前端只修改渠道连接入口及必要文案。原来的微信、企业微信连接流程保留。

resume 返回本地期望状态，不能证明飞书远端已接受密钥。实际运行状态在账号详情查看；凭据被远端撤销时使用“重新授权原机器人”。原凭据已被旧版本/解除绑定清空时，不能从身份哈希反推 app_id，需在飞书授权页选择原应用，后端严格核对最终身份。

## 自动化验证

| 范围 | 结果 |
| --- | --- |
| 文件 SQLite：全部渠道网关测试 + 既有密钥安全测试 | 138 通过 |
| PostgreSQL：全部渠道网关测试 | 134 通过 |
| 前端通知和渠道模块 | 10 个文件、51 项通过 |
| 前端 ESLint | 通过 |
| 全仓 TypeScript 检查 | 被既有 src/modules/chat/utils/message.test.ts:49 语法错误阻塞；未改动该无关文件 |
| 本次生产文件的定向 TypeScript 检查 | 本次文件无错误；依赖的既有 authservice-client/api.ts 有 6 个未使用导入错误，保留未改 |
| Python 改动及新增测试 lint | 新增问题 0；共享存储文件有一处原有 121 字符行长提示，未修改无关行 |
| 权限提取 | pause/resume 均为 POST、qa.write |
| 前端 Local 构建和生成契约检查 | 通过 |
| git diff --check | 通过 |

PostgreSQL 使用既有测试容器及每次独立测试 schema，SQLite 使用临时文件数据库；没有清理业务数据库。测试替代外部飞书注册和运行启动回调，未调用真实二维码或发送外部测试消息。依赖原有 6 条弃用告警不属于失败。

主要验证命令（仓库根目录执行；前端命令先进入 frontend）：

```bash
/tmp/lazymind-notification-review-fixes-venv/bin/python -m pytest tests/backend/channel-gateway backend/channel-gateway/test_security.py -q --tb=short
# PostgreSQL 使用同样测试目录，由 CHANNEL_GATEWAY_TEST_DRIVER=postgres 和受保护 DSN 指向独立测试库。
# 不在命令记录、文档或日志中保存数据库密码。
cd frontend
/Users/zouyu/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node node_modules/vitest/vitest.mjs run src/modules/notifications src/modules/channelGateway
```

本次日志：/tmp/lazymind-feishu-reuse-{sqlite,postgres,frontend}-final.log、/tmp/lazymind-feishu-reuse-lint-final.log、/tmp/lazymind-feishu-reuse-eslint.log、/tmp/lazymind-feishu-reuse-build.log。

## 桌面和人工验收

桌面包已重建，macOS ad-hoc 签名通过。打包后的 8 个后端修改文件与源代码一致，前端包包含新增控件。已用原验收配置启动，运行管理器 overall=ready，15 个服务均 running。经桌面真实访问链路调用 pause/resume（不存在的测试账号，未更改真实连接），均返回 404 / ACCOUNT_NOT_FOUND。Local 前端构建已恢复。

应用：desktop/dist/mac-arm64/LazyMind.app；分发包：desktop/dist/LazyMind-darwin-arm64.zip。构建日志：/tmp/lazymind-feishu-reuse-desktop-build.log。

启动原验收配置（不要用另一个默认配置启动第二个实例）：

```bash
cd /Users/zouyu/Downloads/LazyMind-notifications-official
./docs/plan/notification-official-isolation/start-acceptance.command
```

真实飞书以下项目仍需人工确认，自动测试不能代替：

1. 打开设置 → 连接，查看原飞书机器人及账号 ID，确认没有自动生成二维码或新增应用。
2. 点击普通“断开”，再点“重新连接”：不应要求扫码，账号 ID 不变，运行状态恢复后在原机器人中继续对话。
3. 原定时任务仍引用同一账号；运行一项已配置飞书通知的测试任务，确认通知送达原机器人。
4. 多个机器人时按名称和账号 ID 选择，不能自动合并同名机器人。
5. 点击“重新授权原机器人”，扫码选择原应用，确认最终账号 ID 不变；取消或选错身份后原账号和历史仍保留。
6. 如要验证解除绑定，确认框会说明清除凭据；解绑后重连需重新授权原机器人，不能免授权恢复。
7. 只有明确需要新增应用时才点击“新增机器人”；扫码后应出现独立账号。未自动创建或删除飞书应用。

未提交或推送；既有工作区改动保留。
