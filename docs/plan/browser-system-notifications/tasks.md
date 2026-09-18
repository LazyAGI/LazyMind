# 实施任务

1. 扩展现有 Core feed/ack 的固定 browser 接收类别，保留 local 限制和所有权检查；同步 OpenAPI。
2. 在现有前端应用生命周期挂载浏览器消费者，复用 auth/request 和现有任务路由。
3. 增加安全上下文检测、显式权限按钮及中英文状态；复用 RuleEditor 渠道设置。
4. Web Locks + 有界持久日志协调标签页；处理不确定展示、回执重交、退出/换号、页面销毁。
5. 补充前端和双库公开 API 回归，运行 Electron 回归；用独立 Chromium 测试配置验证真实锁/标签页接管。
6. 同步接口与验收说明，检查构建、生成客户端和修改范围，披露真机未验证项。

执行入口：`frontend/src/modules/notifications/`、`frontend/src/App.tsx`、现有 auth 退出事件、`backend/core/taskcenter/notification_handlers.go`；不改网关/算法/数据库迁移。
