> 历史记录：本文描述隔离前的来源分支及旧安装包，不能作为官方基线迁移后的验证结果。当前状态见 [隔离验收清单](../notification-official-isolation/checklist.md)。

# 原生桌面通知实现报告

日期：2026-09-16。用户以“进入下一阶段开发”批准本批测试，已完成 N3 代码与本机可执行验证。未修改 frontend，未增加生产依赖，未修改已 Review 的 45 项测试。

## 交付行为

- 主进程 `native-notifications.js` 复用现有受保护的身份、桌面通知、回执、任务查询接口，使用 Electron Notification。标题是任务名称，正文是已生成的结果摘要；系统 show 回调后才确认 delivered。
- `main.js` 复用既有运行状态、会话 IPC、窗口恢复、应用图标和退出入口。会话写入串行执行；换号/退出立即取消旧请求，已过期的异步登录不能恢复通知。IPC 只接受当前本地主窗口的主 frame。
- 接入检查发现原会话同步只由“应用接入”设置触发，普通登录不会主动调用。因此 `preload.js` 增加对既有 `lazymind:user` 存储和 `lazymind:user-change` 事件的监听，继续调用原有会话 IPC，不增加前端页面或公开 API。先以 4 项新增测试复现缺失，再补实现；真实 Electron sandbox/contextIsolation 环境已验证普通登录、令牌刷新、退出三条路径。
- 窗口关闭进入后台仍接收通知；显式退出清理请求和通知。点击重新查询所属任务，打开现有结果会话；无会话进入任务列表。旧用户点击、外链和无权限任务不打开。
- 本地状态只存作用域哈希、通知 ID、提交阶段；原子替换、Unix 0600。提交前写入用于防止崩溃重复弹出；有 show 但回执失败可重启补交，没有可靠回调则保持未知。
- 默认 5 秒串行轮询、10 秒网络/状态查询超时、查询故障退避上限 60 秒；分页 100 条，每轮最多 20 页。响应上限 1 MiB；状态文件读取上限 2 MiB、最多 5000 条，优先淘汰已确认记录。不能安全持久化时停止投递。

## 自动验证

| 验证 | 结果 |
| --- | --- |
| 已批准原生行为/调用来源/主流程接入测试 | 45/45 通过，断言未修改 |
| 新增真实 HTTP 消费及请求取消 | 2/2 通过 |
| 新增实际主进程 IPC 代码的登录/退出并发、调用来源、退出竞态 | 3/3 通过 |
| 新增普通登录/刷新/退出/后台保留的预加载同步 | 4/4 通过 |
| 上述通知 54 项 + 相关既有桌面 72 项 | **126/126 通过，0 跳过** |
| 桌面全量（后续工作区修复后） | **174/174 通过，0 失败、0 跳过** |
| 生产 JS 语法、git diff 空白检查 | 通过 |

主要命令：

```bash
node --test desktop/scripts/native-notifications*.test.mjs desktop/scripts/desktop-build.test.mjs desktop/scripts/runtime-smoke.test.mjs desktop/scripts/packaged-app-smoke.test.mjs desktop/scripts/preload-bridge.test.mjs desktop/scripts/external-navigation.test.mjs desktop/scripts/installer-warmup.test.mjs
node --test desktop/scripts/*.test.mjs
node --check desktop/electron/src/native-notifications.js
node --check desktop/electron/src/main.js
node --check desktop/electron/src/preload.js
git diff --check
```

原全量唯一失败是 `local-workspace-contract.test.mjs` 的 `Desktop reauthorization requires the user to choose the stored directory again`；HEAD 独立副本同样复现。后续新增真实目录行为测试，确认正确目录也因未定义的 canonicalPath 报错。用户批准修复后，补全变量并保留路径和目录身份校验；原有断言未修改。最新桌面全量 174 项全部通过，详见 旧目录中的工作区修复记录（已排除于本分支）。

## 实际运行与打包

在独立临时目录复用已有运行资源，覆盖本批 Core/运行管理器二进制、网关源代码、迁移及获批 SDK，使用仓库原 electron-builder 配置构建 macOS arm64 `.app` 和 DMG；保留原 desktop/build、desktop/dist。这是复用已有前端/算法资源的本地验证包，不是重新构建并验证全部组件的正式安装发行版。没有生成 Windows 安装包。

采用 ad-hoc 签名，打包的签名验证通过；没有使用 Developer ID 凭据、公证或上传。最新 DMG 已包含工作区修复；镜像完整性、只读挂载后的应用深度签名验证通过，包内 main.js、preload.js、native-notifications.js 与当前源码逐字节一致。包内工作区 10 项测试通过。包内 Python/Gateway 从其实际资源路径启动，文件 SQLite 的 healthz/readyz 均 200，正常退出和依赖导入通过。测试首次生成的 Python 缓存已恢复为打包前状态，重新以不写入字节码方式验证网关并重新生成最终 DMG；有签名失败的中间镜像未交付。之前 Linux 镜像的 SQLite/PostgreSQL 启动验证仍有效，本轮未修改后端 SQL。

独立签名的 LazyMind 通知测试包使用生产桥接模块和真实 Electron Notification，HTTP 仅为本地合成账号/任务 Fixture，不访问用户账号。2026-09-16 的实际日志：

- `03:13:26.293Z native-show`
- `03:13:26.301Z backend-receipt`
- `03:14:26.195Z finished`（测试进程正常退出，清理本次通知）

此结果证明 macOS 原生 show 回调与桥接回执链路实际运行；不等于用户已看到横幅。尝试读取系统通知中心时仅获得日历组件，系统菜单 UI 查询超时，未能完成横幅外观与实际点击验收。未改变系统通知权限或勿扰设置。

另以隐藏的真实 Electron 窗口运行生产 preload，`contextIsolation: true`、`sandbox: true`，主世界写入既有登录存储并触发登录/刷新/退出事件，三个对应 IPC 均经过真实主 frame 来源校验。未读取用户现有登录资料，测试 profile 独立。

## 证据与限制

证据目录：`/var/folders/r8/6qz6g7g50tsfv19lysj0j4t110tzws/T/lazymind-notification-package-btshjk9a/`。

- `dist-complete/mac-arm64/LazyMind.app`：包含工作区修复的应用包。
- `dist-install-final/LazyMind-macos-arm64.dmg`：通过最终验收的镜像。
- `package-install-final.log`、`dmg-final-result.json`：最终镜像构建、完整性、挂载应用签名及源码核对。
- 交付副本与 SHA256、测试日志：`/Users/zouyu/Downloads/LazyMind-notifications-20260916/`，详见 [交付说明](delivery-report.md)。
- `packaged-gateway-sqlite.log`：包内网关启动验证。
- `native-smoke-state/events.jsonl`：真实系统通知回调与回执。
- `preload-real-smoke-result.json`：真实 Electron 登录同步结果。
- 测试日志：`/tmp/lazymind-native-regression.log`、`/tmp/lazymind-desktop-all-tests.log`、`/tmp/lazymind-desktop-baseline-test.log`。

仍需：macOS 横幅/通知中心外观、真实点击与勿扰组合；Windows 真机；微信/企微/飞书真实环境及用户指定收件对象；完整 Compose 栈和正式安装包验收。当前点击只覆盖进程内 Notification 回调，未验证崩溃后系统历史通知的冷启动激活。

认证失效时按已批准测试暂停，已有登录/令牌刷新同步后恢复；当前桥接不独立更新 refresh token，不在 renderer 已关闭时另建自动登录/刷新流程，也不回退为管理员。若后台常驻超过现有 token 有效期，可能需要重新打开应用恢复会话，不能声称全天候离线身份续期已实现。

没有向真实外部平台发消息，没有提交、推送、增加生产依赖或修改用户原有生成产物。获批原生桥接代码与上述自动/实际回调验证已完成，不能将尚未验收的平台和完整系统交互列为完成。
