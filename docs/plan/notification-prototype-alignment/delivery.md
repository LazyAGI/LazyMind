# 原型对齐交付记录（2026-09-17）

## 已完成

- 新建定时任务顶部和表单均可打开通知抽屉；显示渠道数量、时机和内容摘要。通知先进入任务草稿，取消不写入；编辑任务也使用同一流程。
- 任务内容、依赖、通知规则一次提交并在同一事务保存。省略通知字段保持旧客户端行为；冲突不会部分保存。复用既有通知验证、严格请求解析、目标校验、归属条件和 revision CAS；无新增数据库迁移、服务或生产依赖。
- 独立通知和任务编辑支持显式 clear:true，恢复为未配置；保留执行快照及历史。取消通知抽屉后再次打开会恢复已保存草稿。
- 任务卡片直达配置；详情补充渠道数量、通知时机/内容摘要与清除入口。关闭渠道/事件前检查相关运行及规则快照，只在有影响时确认；查询失败不当作没有运行。
- 设置页调整为总开关、渠道、时机；增加终端连接入口、渠道状态/说明和事件图标。任务内账号/接收对象内嵌选择，仍要求显式选择对象，后端再次验证可用性。
- 连接成功后可以返回并使用该账号；现有账号也可返回使用，保持父级草稿。补充连接总数、折叠账号摘要，复用 HTML 内嵌的微信/企业微信平台图标。
- 保留 BotID＋Secret、iLink、既有时间控件和多依赖能力，以及浏览器/Desktop 通知接收逻辑。暂停提示可进入全局设置并返回原草稿。

## 验证

- SQLite / PostgreSQL：`cd backend/core && go test -race . ./taskcenter ./scheduler -run '^TestNotification' -count=1`，均通过。新增草稿测试另外验证原子创建、冲突回滚、依赖失败回滚、跨用户拒绝、清除保留历史及并发竞争只成功一次，两个数据库分别执行。
- 既有调度创建：`go test ./scheduler -run '^Test(Create|Notification)' -count=1` 通过。
- 前端：通知、任务中心、终端连接入口、桌面桥接等 13 个文件、67 项测试通过。新增草稿测试验证只更新草稿、取消恢复、空闲不确认/运行中确认；连接返回测试验证用户显式选择后才带回账号。
- Electron 原生通知：80 项测试通过。
- 受影响文件 ESLint、OpenAPI 四组 fresh、`git diff --check` 通过；Local 模式生产构建通过。
- 定向 TypeScript 语义检查仍报告 ScheduleList 既有的 27 条隐式 any，报错对应源码均已存在于 HEAD；此次新增的通知实现没有出现在该次诊断中。全仓类型检查此前受 message.test.ts 语法错误阻塞，未修复无关基线问题，不声明全仓类型检查通过。
- Local 实际页面验证了新建入口、通知抽屉层级、草稿保存后摘要更新，以及取消新建后任务数量保持 1；设置页实际展示先渠道后时机。未创建验收残留任务或修改已有规则。

日志：`/tmp/lazymind-prototype-{sqlite-final,postgres-final,draft-race,draft-pg,final-tests,scheduler,electron,lint-final,build-final,types}.log`。

## 当前可验收环境与边界

Local 前端已重新构建，确认没有活动任务后更新并重启 Core，总体状态 ready。访问 http://localhost:8090/task-center?tab=schedules 并刷新页面即可检查新建任务。

2026-09-17 按用户要求重建 macOS arm64 Desktop：使用现有 Go 包装脚本、冻结 Skill 锁文件、RELEASE_BUILD=false 和 adhoc 签名，构建成功。产物为 `desktop/dist/mac-arm64/LazyMind.app` 和 `desktop/dist/LazyMind-darwin-arm64.zip`。深度严格签名校验通过，包内 Core 和前端入口与本次构建暂存文件一致，前端入口引用资源完整。构建后恢复了 Local 模式前端，确认 Local 状态 ready、HTTP 200，服务返回内容与恢复后的构建一致。此次未启动桌面应用，保持已有 Local 运行及数据不变。

重建日志：`/tmp/lazymind-desktop-prototype-rebuild.log`；Local 恢复日志：`/tmp/lazymind-local-after-desktop-rebuild.log`。

### 卡片操作栏截断修复

用户验收发现新增通知按钮后，卡片底部的更多按钮超出右侧边界。仅调整 `frontend/src/modules/taskCenter/index.scss`：操作栏改为最小高度并允许换行，按钮组限制为容器宽度、允许换行，列表模式同步移除固定高度。两个任务界面测试文件共 4 项通过，Local 和 Desktop 构建成功，桌面签名校验通过。已用新包实际检查窄卡片三个按钮完整可见、更多菜单可展开，以及列表模式布局；恢复卡片模式并保持桌面运行。日志：`/tmp/lazymind-card-layout-tests.log`、`/tmp/lazymind-desktop-card-layout-build.log`、`/tmp/lazymind-local-card-layout-build.log`。

本轮未提交/推送 Git，没有修改无关模块；保留本轮开始前的浏览器通知改动。真实外部账号的连接和消息投递未执行；连接返回流程使用组件测试与现有接入组件验证。HTML 的演示数据与静态授权状态不用于替代真实服务状态。
