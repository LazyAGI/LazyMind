# 桌面通知响应契约修复：阶段一

## 现场证据

2026-09-17，用户再次点击立即执行后，任务 `tc_fc32c0027aac4c74b12fa19b38d2f2e8` 已 succeeded，通知为 pending 且无投递回执。已打包的 main.js、preload.js、native-notifications.js 与工作区源文件一致，所以不是桌面包未更新。

通过当前验收环境已有登录凭据进行只读查询，代理端口 5024 和前端端口 8090 的 `/api/authservice/auth/me` 均返回 `{code,message,data}`；身份与 active 状态位于 data。接收器直接读取顶层 user_id/status，因而执行 clearSession，没有进入通知轮询。Core 桌面通知接口正常返回当前用户的 7 条 pending 记录。相同的响应层级问题还会令点击通知时无法取得 conversation_id，退回任务列表。

通知详情另有独立错误：前端 getAttempts 默认发送 `cursor=`，而网关 notification_history 声明整数 cursor，默认 0，实际请求返回 HTTP 422。该问题不能通过放宽后端校验解决。

## 本轮只新增测试

- `desktop/scripts/native-notifications-envelope.test.mjs`：4 项场景。代理封装身份能接收并只回执一次；停用身份拒绝；有效身份仍拒绝其他用户通知；点击通知从 Core 封装响应定位会话。
- `frontend/src/modules/notifications/NotificationHistoryContract.test.ts`：2 项场景。首次分页省略 cursor 或发送 0；后续最大范围整数游标不因转 Number 丢失精度。

运行命令（仓库根目录，使用 Node 24；前端命令在 frontend 目录）：

```bash
node --test desktop/scripts/native-notifications-envelope.test.mjs
node node_modules/vitest/vitest.mjs run src/modules/notifications/NotificationHistoryContract.test.ts src/modules/notifications/api.test.ts
node --test desktop/scripts/native-notifications.test.mjs desktop/scripts/native-notifications-http.test.mjs desktop/scripts/native-notifications-login.test.mjs desktop/scripts/native-notifications-session.test.mjs desktop/scripts/native-notifications-wiring.test.mjs
```

结果：新增 6 项中 2 项通过、4 项预期失败。桌面 3 项失败分别复现未投递、未进入已验证用户的 feed、点击未定位会话；前端 1 项失败复现空游标。原桌面回归 80 项通过，原前端 API 回归 6 项通过。无环境阻塞；未修改既有测试或生产实现。

## 待批准的最小实现

复用现有请求和身份校验流程，在身份及点击会话读取处正确处理响应 data，保留已有裸响应兼容；不降低 active 状态、当前用户、可信来源、过期会话和去重检查。前端首次历史查询省略空游标，后续游标保持原字符串。

批准测试后修改上述两个生产文件，执行本批与相关回归，重建并签名桌面包，再启动当前验收环境验证真实 macOS 投递回执及点击导航。用户原有任务、密钥、通知偏好保持不变。尚未实施生产修复、未重建，不宣称系统通知恢复。本改动无数据库/迁移/生产依赖变化。

## 用户审批

用户随后明确回复“批准”。阶段二已开始，最终结果见 `delivery.md`；上述失败结果保留为阶段一历史记录。
