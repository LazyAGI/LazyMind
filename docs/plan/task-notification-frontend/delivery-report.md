> 历史记录：本文描述隔离前的来源分支及旧安装包，不能作为官方基线迁移后的验证结果。当前状态见 [隔离验收清单](../notification-official-isolation/checklist.md)。

# 通知前端交付记录

日期：2026-09-17。

## 范围与实现

本次根据用户提供的四份 HTML 补充前端；四份文件的主体一致，仅初始页面和是否自动打开任务通知抽屉不同。保留仓库原有未提交的后端、桌面与启动修复，本次没有修改这些文件、生产依赖、数据库或服务配置，也没有提交或推送。

- 设置新增“消息通知”：总开关、成功/失败/人工处理事件、摘要/全文、桌面/飞书/企业微信/微信渠道。默认规则即时保存，只影响之后创建的任务。
- 复用一套 RuleEditor、账号选择器和既有认证请求实例，供默认规则和单任务规则使用。账号、接收对象显式选择；已关闭渠道保留目标；关闭全部渠道不影响任务执行。
- 总开关关闭使用后端返回的精确运行 ID 和原 revision 二次确认。配置冲突保留草稿并要求重新加载；失败不假报成功。
- 终端连接改为三平台导航、左侧账号展开管理、右侧连接面板。微信/飞书复用原扫码及轮询；企业微信按此前确认的 BotID＋Secret 接入。重连携带原 account_id，Secret 只在输入状态和请求中使用，提交后清空，不持久保存。
- 账号详情读取真实接收对象、运行状态和引用信息。断开前重新读取引用，读取失败禁止断开；引用和接收对象支持分页。
- 设置中的定时任务、任务卡片菜单、定时任务详情和编辑表单增加通知配置入口。旧任务打开详情/取消编辑不会初始化配置；已配置渠道显示名称、账号/对象和可用性。
- 定时任务详情可选择执行记录；执行任务详情显示固定规则快照和通知历史。外部渠道保留独立重试历史，未知结果需确认重复风险，响应丢失重试复用操作键。网关读取失败不会隐藏已经取得的桌面记录。
- 从规则配置进入连接时使用嵌入式连接面板，父级草稿保持挂载，返回后刷新账号列表。
- 新增中英文文案；保留原设置布局、任务中心、飞书接入和系统原生通知桥接。通知发送失败与任务执行失败使用不同文案。

## 与原型的接口边界

1. 企业微信采用已确认的智能机器人凭据方式，原型模拟的通用二维码不用于企业微信。
2. 内容遵循现有后端的“摘要/全文”契约，没有把演示中的卡片、图片、附件等选项伪装为可配置能力。
3. 现有创建任务接口只会复制当时默认规则，不接收独立通知配置。本次保留该行为，新任务保存后再配置独立通知；编辑表单里的通知保存与任务编辑独立，页面已明确说明。未添加可能产生重复任务或首轮规则竞争的两次提交式创建流程。
4. 单任务通知接口不接受 config=null；通过关闭渠道停止通知，不伪造“恢复为从未配置”。旧任务原本 configured=false 的空状态仍保留。
5. 使用原生桌面应用接收系统通知。普通网页提示使用桌面应用，不申请浏览器 Notification 权限，不新增桌面轮询器。默认桌面选项仍保留后端保存值。
6. 平台品牌复用现有飞书资源和 Ant Design 平台/团队图标，优先使用账号 HTTPS 头像，头像失败回退。未引入新的图标依赖。

## 验证

使用本机已有 Node 24.19.0；系统 Node 20.20.2 不满足现有 jsdom 30/undici 的运行要求。未为此修改依赖版本。

```bash
cd /Users/zouyu/Downloads/LazyMind-main/frontend
export LAZYMIND_FRONTEND_NODE=/Users/zouyu/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node

"$LAZYMIND_FRONTEND_NODE" node_modules/vitest/vitest.mjs run \
  src/modules/notifications \
  src/modules/taskCenter/SettingsScheduleList.test.tsx \
  src/modules/taskCenter/TaskDetail.test.tsx \
  src/modules/taskCenter/ScheduleList.i18n.test.tsx \
  src/modules/channelGateway/components/TerminalConnectionQuickPanel.test.tsx \
  src/modules/settings/SettingsPage.developer.test.tsx

"$LAZYMIND_FRONTEND_NODE" node_modules/eslint/bin/eslint.js \
  src/modules/notifications \
  src/modules/channelGateway/api.ts \
  src/modules/channelGateway/hooks/useChannelConnection.ts \
  src/modules/channelGateway/pages/ChannelConnectionPage.tsx \
  src/modules/settings/index.tsx \
  src/modules/taskCenter/ScheduleList.tsx \
  src/modules/taskCenter/SettingsScheduleList.tsx \
  src/modules/taskCenter/TaskDetail.tsx \
  src/i18n/locales/notifications-*.ts

"$LAZYMIND_FRONTEND_NODE" scripts/openapi/check-stale.mjs
"$LAZYMIND_FRONTEND_NODE" node_modules/vite/bin/vite.js build \
  --outDir /tmp/lazymind-notification-frontend-build
```

结果：8 个测试文件、26 项测试通过；本次文件 lint、四个 OpenAPI 客户端一致性检查通过；生产构建通过。构建保留既有大包和静态/动态导入提示；测试环境保留既有 Sass/组件兼容提示，没有未处理的测试错误。

全量 `tsc --noEmit` 未通过：修改前独立副本与本次各有 502 条类型错误；按文件和错误内容去除行号后比较，没有新增错误。本次没有用降低类型检查标准或修改无关模块解决基线问题。

真实桌面后端只读检查：

- GET `/api/core/user/notification-preferences` → 200。
- GET `/api/channel-gateway/v1/channel-accounts?provider=wecom` → 200。

浏览器使用仓库真实组件和临时隔离数据检查了通知设置、三平台连接、企业微信凭据表单、任务已配置渠道、任务通知抽屉及保存返回流程；该检查没有连接外部平台或发送消息，不能代替真实账号联调。

## 桌面包重建与启动验证

2026-09-17 按用户要求完成 `make desktop-darwin-arm64`，退出码 0。沿用已有 `acceptance-build-go.sh` 选择 Go 工具链和冻结 Skill 锁文件，使用 Node 24、`LAZYMIND_RELEASE_BUILD=false` 与 adhoc 签名；未修改生产依赖或构建源码。

- 新应用：`/Users/zouyu/Downloads/LazyMind-desktop-20260917-frontend/LazyMind.app`。
- 启动入口：同目录 `启动验收版.command`，沿用 `/Users/zouyu/Downloads/LazyMind-manual-05uNt5` 的验收数据和配置。
- 完整压缩包：`desktop/dist/LazyMind-darwin-arm64.zip`；构建日志：`/tmp/lazymind-desktop-rebuild-20260917/build.log`。
- 旧服务正常停止后，备份 profile、credentials、runtime/data 至验收目录的 `backups/before-frontend-rebuild-20260917/`；旧修复版应用保留。
- 新包源码与前端资源一致，包含已批准的 Python 启动修复；新 Python 运行时的 3 项启动回归测试通过。应用启动后再次执行 `codesign --verify --deep --strict`，通过。
- 新应用实际启动，15 个后台服务均为 running，整体 ready。原有 admin 登录与 DeepSeek 模型配置保留。
- 在真实桌面窗口验证消息通知设置、三平台终端连接入口及企业微信 BotID/Secret 表单，最终停留于消息通知页面；未修改规则、连接真实外部账号或发送消息。
- 新包运行期间通知偏好与企业微信账号列表的两项只读 API 均返回 200。`git diff --check` 通过，构建没有改动锁文件。

## 仍需人工验收

- 用指定测试账号完成飞书/微信扫码和企业微信连接，验证真实账号头像、目标发现、断开/重连及消息送达。
- 在重建后的 macOS/Windows 桌面应用检查系统横幅、勿扰设置、通知点击及系统权限。
- 若要求与原型完全一致的新建任务草稿通知及清除为未配置状态，需要后续扩展后端公开契约及测试。本次未扩大该接口范围。
