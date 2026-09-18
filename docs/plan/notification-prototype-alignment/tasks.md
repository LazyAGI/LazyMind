# 任务

1. 真实路由测试原子创建/更新、回滚、清除与历史保留：backend/core/notification_draft_test.go。
2. 复用通知校验/CAS存储：taskcenter 与 scheduler；SQLite/PostgreSQL均验证。
3. 复用规则抽屉支持任务草稿；创建/编辑一次提交。
4. 修正设置/任务布局、概览和入口；前端组件测试。
5. 连接后带回账号，保留草稿，运行中才确认；组件测试。
6. 同步契约与生成客户端，构建、lint、通知回归和Local页面走查。
