# 桌面通知响应契约修复

用户批准新增测试后，生产改动仅限两个文件：

- `desktop/electron/src/native-notifications.js`：身份及点击任务响应从 data 读取，保留裸响应兼容；继续校验 active 用户、通知所有者、当前会话及可信来源。
- `frontend/src/modules/notifications/api.ts`：首次投递历史请求省略空 cursor；后续游标保留字符串精度，不改变网关整数参数契约。

原有测试未修改。全部桌面通知测试 84 项通过，前端通知目录测试 43 项通过；受影响前端文件 ESLint、git diff --check 通过。前端测试有 jsdom 不支持伪元素 getComputedStyle 的既有提示，不影响测试结果。

日志：`/tmp/lazymind-notification-response-desktop-tests.log`、`/tmp/lazymind-notification-response-frontend-tests.log`。

## 构建及真实验收结果

macOS arm64 桌面包完整构建成功，使用既有验收 Go 包装脚本、RELEASE_BUILD=false 和 adhoc 签名。深度签名校验通过，app.asar 内 native-notifications.js 与修复源码逐字节一致。产物为 `desktop/dist/mac-arm64/LazyMind.app` 和 `desktop/dist/LazyMind-darwin-arm64.zip`；Local 前端已恢复为 local 模式并构建通过，未同时启动第二套后台。

启动新包后 runtime=ready，原有 7 条 pending 通知均进入本地 acked 状态。2026-09-17 17:55:02 新执行任务 `tc_2ed215ba1a864663803e81fb9322029d` succeeded，对应通知 `e4c5998b8d93bd2fc3ae8d34a20b587900588cf47874813617e949c06a31f386` 为 sent，desktop_notification_receipts 为 delivered。未手工写入回执或模拟系统事件，回执来自 Electron 原生通知 show 事件。

去掉空 cursor 后，真实网关历史接口 HTTP 200，返回 items/next_cursor；当前任务详情接口返回正确 conversation_id。点击定位和单次回执由自动化回归覆盖；本轮没有捕获系统横幅截图，也没有在真实 macOS 通知上完成点击导航，因此不将系统接收回执等同于用户亲眼看到横幅。用户正在操作应用，未继续抢占界面或重复执行任务。

日志：`/tmp/lazymind-notification-response-build.log`、`/tmp/lazymind-notification-response-local-build.log`。当前桌面版保持打开；没有提交或推送代码。此次仅修复客户端响应解析与分页参数，不涉及数据库、迁移、生产依赖或后端 API 变更。
