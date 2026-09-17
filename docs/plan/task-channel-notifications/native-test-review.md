# 原生桌面通知：阶段一测试 Review

日期：2026-09-16。用户在 R7 方案确认点回复“继续”，据此进入 N2 测试阶段。本批只增加测试和文档，**尚未实现原生通知生产功能**，没有修改 frontend 或现有生产代码，等待用户 Review 后进入 N3。

## 范围及需求映射

| R7 要求 | 本批用例覆盖 | 验证边界 |
| --- | --- | --- |
| 原生展示及如实回执 | 使用任务标题/实际摘要，收到原生 show 后才 ack；重复回调去重；不支持、failed、同步抛错、没有回调均不伪造 delivered/permission_denied | Fake 仅替换 Electron 系统边界；未调用本机通知中心 |
| 轮询与资源限制 | 5 秒周期、单请求 10 秒超时、串行无重叠、故障退避上限 60 秒、每页不超过 100、游标重复终止、下一轮从第一页开始 | 注入可控 Clock，不等待实际 5/10/60 秒 |
| 当前用户及本地实例 | 认证服务 me 确认身份；仅受信本地源；禁跨源重定向；不自行注入 X-User-Id；错误用户/设备/渠道/状态和畸形标识拒绝 | HTTP 边界 Fake 使用现有 auth 与 Core 响应结构 |
| 登录、退出与会话变化 | 401 暂停，现有会话同步提供新 token 后恢复；退出中止请求；晚到响应/原生事件/点击无效；换号关闭原用户通知 | 不复用管理员 bootstrap 代替当前用户；不新增刷新协议 |
| 重启与重复风险 | 接受展示但回执失败后重启只补交回执；提交与回调之间崩溃保留未知、不自动重弹；用户和实例隔离 | 使用真实临时状态文件和重新创建的桥接实例；模拟崩溃时恢复原始持久化字节 |
| 最小本地存储 | 文件权限 0600（非 Windows）、不存 token/正文/标题、损坏或不可写状态先于系统副作用安全失败 | 真实文件系统；Windows ACL 与系统运行需另行验收 |
| 点击定位 | 查询所属任务后打开已存在的结果会话路由，标识 URL 编码；无会话到任务列表；拒绝通知自带外链；查询期间退出则不跳转；无权限任务不打开 | 不新增前端路由；当前进程内 click 回调，进程重启后的操作系统历史通知唤起尚未验证 |
| 主流程接入 | 登录/退出 IPC 仅信任当前本地主窗口主 frame；主进程接入真实 Electron Notification；显式退出 stop；后台模式常驻 | 6 项调用来源行为测试、3 项主进程源码契约；安装后真实主流程仍须在 N3 验证 |

## 新增文件与拟定模块边界

- `desktop/scripts/native-notifications.test.mjs`：42 项行为/安全测试。注入系统通知类、HTTP、可控时钟和现有运行状态；使用真实临时状态文件。只使用 Node 内建依赖。
- `desktop/scripts/native-notifications-wiring.test.mjs`：3 项主进程接入契约，避免独立模块通过却遗漏现有登录、后台、退出入口。
- 拟实现的同目录主进程模块为 `desktop/electron/src/native-notifications.js`。工厂 `createDesktopNotifications` 暴露 `setSession`、`clearSession`、`stop`；依赖通过参数传入，复用现有主进程生命周期，不新增服务或架构层。`setSession` 等待首轮可完成的身份/通知处理；网络可取消。独立 `isTrustedNotificationSender` 用于既有会话 IPC 的调用来源检查。
- `getRuntime` 来自主进程受信运行状态，提供当前实例标识、本地 API/页面源和就绪状态；不是 renderer 自报身份。当前会话取自已有同步入口，用户身份取自认证服务 `/api/authservice/auth/me`。Core 通过现有受保护代理路径访问。

测试中保留的合成 token 仅为非真实 Fixture，网络不访问外部账号。没有添加最小生产声明或测试专用生产分支，生产模块文件当前不存在。

## 实际命令及结果

```bash
node --check desktop/scripts/native-notifications.test.mjs
node --check desktop/scripts/native-notifications-wiring.test.mjs
node --test desktop/scripts/native-notifications.test.mjs desktop/scripts/native-notifications-wiring.test.mjs
node --test desktop/scripts/desktop-build.test.mjs desktop/scripts/runtime-smoke.test.mjs desktop/scripts/packaged-app-smoke.test.mjs desktop/scripts/preload-bridge.test.mjs desktop/scripts/external-navigation.test.mjs desktop/scripts/installer-warmup.test.mjs
git diff --check
```

- 语法检查与 diff 空白检查通过。
- 新增测试：45 项，0 通过、45 预期失败、0 跳过、0 取消。42 项因 `native-notifications.js` 尚不存在（MODULE_NOT_FOUND）失败；3 项因主进程尚无原生桥接接入（ERR_ASSERTION）失败。**行为断言尚未运行到实现，因此这不是功能验证成功。** 未发现环境依赖缺失或额外异常失败。
- 相关既有桌面测试：72 项全部通过、0 跳过。未修改或弱化原有测试。
- 日志目录沿用 `/var/folders/r8/6qz6g7g50tsfv19lysj0j4t110tzws/T/lazymind-notification-tests-lwcvsyrk/`：`native-notifications-stage1.log`、`native-notifications-stage1-baseline.log`。

## 兼容性与未验证项目

本批没有 SQL、迁移、Schema 或 Core/Gateway 生产改动，不重复执行已通过的双库矩阵；桥接只面向 Desktop/Local 本地设备，Compose 后端继续使用原有不可用设备边界。N3 必须检查真实接口消费并复跑受影响契约。

API 依据为仓库已安装 Electron 31 类型声明及[官方 Notification 文档](https://www.electronjs.org/docs/latest/api/notification)。最新文档有高版本专用能力，不能直接用作 Electron 31 的保证；本批只依赖现有版本的 isSupported/show/click/close/failed，不调用新增的 getHistory/handleActivation，不升级依赖。系统支持检测不代表权限；没有可靠回调时保留未知，不能据此推断用户已阅读或权限已拒绝。

本批未验证真实 macOS/Windows 横幅、通知中心、图标、声音、勿扰、授权和打包后的点击唤起。Windows 需要对应环境；真实微信/企微/飞书联调仍等待用户指定测试环境和收件对象，不因批准本批测试而发送外部消息。

根据根目录 AGENTS.md 第 5 节“只有用户明确批准本批测试后，才进入阶段二”，本批在此停止，等待 Review。批准后实现 N3 并运行上述行为与接入测试，不以修改断言代替实现。
