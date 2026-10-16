# Codex Desktop 共享后台运行组件

功能对比、整体架构与验证范围见 [实现说明](../../../docs/development/codex-shared-server.md)。本文保留运行组件的配置和恢复操作。

共享后台运行组件已内嵌到 LazyMind CLI，并接入“连接/断开 Codex”流程。macOS 已有桌面实测；Windows 已补平台实现，仍需实机验收。当前限定内置 CLI `0.162.0-alpha.17.2`。已接通 Workflow 原生 Continue/Stop 的入口与绑定能力；连接时自动准备并启用，断开时恢复默认启动配置。2026-10-15 本机部署后，用户反馈 Continue/Stop 测试正常；不代表长期稳定性或跨版本兼容已验证。

```text
Desktop ──stdio── 适配器 ──WebSocket / Unix socket── 独立 App Server
                    │                                  │
                    └─ 原装 MCP 插件 ←─桥接 socket─ 工具代理
                             │                         ↑ stdio
                             └─原生 socket─→ Desktop   └─后台启动
```

原图标启动 Desktop 后，Desktop 通过 `CODEX_CLI_PATH` 启动适配器。适配器在内核锁下检查后台归属、缺席启动并等待就绪；已有后台直接复用。退出 Desktop 只清理本次适配器及插件，不停止共享后台。重开后工具代理连接新的桌面桥接，保留真实聊天元数据与原生审批。

## 一键连接、升级与停用

- **连接**：在现有 Codex 接入入口点击连接；检查版本与依赖、准备运行组件、安装插件，然后启用共享模式。首次配置后正常退出并重开 Codex。
- **升级**：按运行组件内容和当前 App/插件配置识别版本；相同版本重复连接复用已有目录。升级使用新目录，先准备再切换，启用失败尝试恢复旧配置。若 Codex 正在运行，提示退出后重试，不自动中断聊天。
- **停用**：正常退出 Codex 后点击断开，先恢复 GUI 环境和自有生成配置，再卸载插件。后台及旧目录保留，以免破坏仍在运行的任务。
- **安装位置**：`~/Library/Application Support/LazyMind/codex-desktop/`。CLI 内嵌运行文件，部署时不依赖 pnpm；安装操作使用文件锁串行化。
- **平台**：macOS 使用 LaunchAgent 设置 GUI 环境；Windows 使用当前用户的环境变量并通知桌面 Shell。Windows 配置后若 Shell 仍保留旧环境，需要注销并重新登录一次，不替换原图标。

## 手动构建与准备（开发调试）

开发机需要 pnpm；运行时使用 App 自带的 Node/CLI，以及安装目录中的 LazyMind 原生 helper；不再依赖 Python。准备过程会检查依赖，不自动安装任何软件。

```bash
pnpm --dir integrations/codex-workflow build:desktop
LAZYMIND_RUNTIME_HELPER="/absolute/path/to/freshly-built/lazymind" \
node integrations/codex-workflow/dist/desktop/prepare.mjs \
  --output "$HOME/Library/Application Support/LazyMind/codex-desktop"
```

`prepare` 复制构建产物，生成 `runtime.json`、`launch-adapter`。**只准备文件，不启用共享模式、不启动 Desktop 或后台。** 输出目录必须尚不存在；升级另选目录，并先停用旧安装，避免覆盖运行中版本。可传 `--app`、`--codex-home`；默认根据当前 App 内的插件清单选择对应缓存版本，不猜测最新版本；缺失时提示先打开 Codex 完成初始化。开发调试可用 `--desktop-plugin`、`--browser-plugin` 显式指定已解析的 `.mcp.json`。

`runtime.json` 保存实际 App 路径、原插件配置、版本和运行目录；不保存 Desktop 动态校验身份。构建包不依赖实验目录或开发机 `node_modules`。安装目录应使用持久位置；下面的 `INSTALL` 仅是示例变量。

```bash
INSTALL="$HOME/Library/Application Support/LazyMind/codex-desktop"
node "$INSTALL/runtime/manage.mjs" plan --config "$INSTALL/runtime.json"
node "$INSTALL/runtime/manage.mjs" enable --config "$INSTALL/runtime.json"
```

`plan` 只展示变更。`enable` 保存原 GUI 环境，然后执行以下配置，不退出或打开 App：

| 修改项 | 作用 |
| --- | --- |
| `CODEX_CLI_PATH=<安装目录>/launch-adapter` | Desktop 启动外部适配器 |
| `CODEX_APP_SERVER_FORCE_CLI=1` | Desktop 使用 CLI stdio 通道 |
| 清除 `CODEX_APP_SERVER_WS_URL`、`CODEX_APP_SERVER_USE_LOCAL_DAEMON` | 避免跳过适配器直连后台 |
| `~/Library/LaunchAgents/com.lazymind.codex-desktop.plist` | 登录时设置上述环境，不预启动后台；`KeepAlive=false` |
| `<安装目录>/activation.json` | 保存原环境，供停用恢复 |
| `<Codex profile>/lazymind-shared-server.json` | 告知 Workflow dispatcher 和 MCP 绑定使用同一共享后台；停用时移除本安装登记 |

随后正常退出 Desktop，再点击原图标即可。无需替换图标、修改应用包、签名或安全校验。运行记录位于 `runtime.json` 的 `runtimeDir`，默认是当前用户专属的短 `/private/tmp/lm-codex-<uid>/<安装标识>` 路径，以满足 macOS socket 路径长度限制。

## 状态与恢复默认

```bash
node "$INSTALL/runtime/manage.mjs" status --config "$INSTALL/runtime.json"
# 先正常退出 Desktop，再执行：
node "$INSTALL/runtime/manage.mjs" disable --config "$INSTALL/runtime.json"
```

`status` 读取环境与后台身份记录，不会启动后台；记录存在不代表进程仍存活。`disable` 卸载本组件的登录服务、移除本组件的 Workflow 登记、还原原 GUI 环境，并将 Desktop 生成配置中属于本安装的 `CODEX_CLI_PATH` 恢复到原 CLI；其他编辑保留。配置被外部改动时明确报错，不覆盖未知配置。之后可从原图标打开默认模式。

停用不会终止后台任务，也不会删除聊天；后台可能仍存活，不应立即删除安装目录。正式卸载时的空闲确认和后台停止留待产品安装流程集成。此版本管理命令需串行调用。

## 实现边界

- 复用的是本安装记录并核验的后台，不自动接管其他 daemon。`backend.json` 中的 PID、启动时间、可执行文件以及监听端点归属用于校验。
- 每个适配器独立登记、监听和清理桥接。同一 Desktop 上下文组可共享；多个不同 Desktop 上下文同时存活时拒绝任意选路。
- 辅助 CLI 调用直接交回原 CLI；CUA 自用的辅助 App Server 不属于重复聊天后台。
- App Server 断线会结束适配器，不做聊天请求自动重放。工具代理可以在后续请求重连；断线时的在途调用返回结果未知。CUA 内存变量不保证恢复。
- 后台读取原 Codex profile，再使用命令行覆盖两项工具的传输配置；保留原插件工具列表和审批策略。Desktop 的其他临时 CLI 覆盖没有全部迁移，不能据此保证所有 App 专属功能。
- 日志记录进程和连接事件，不主动记录聊天正文、原生身份或工具返回值；`daemon.log` 是原 CLI 自身的输出。原生插件仍执行正常身份和审批检查。
- CLI 版本在准备时限定；App/插件升级后需重新准备并做一次正常流程确认。登录服务已实际启用并核对环境；最终部署的停用及重新登录行为尚未专项复验。

## 本阶段验证

```bash
pnpm --dir integrations/codex-workflow build:desktop
LAZYMIND_TEST_HELPER="/absolute/path/to/freshly-built/lazymind" pnpm --dir integrations/codex-workflow test:desktop
pnpm --dir integrations/codex-workflow test:platform
pnpm --dir integrations/codex-workflow test:install
pnpm --dir integrations/codex-workflow typecheck
pnpm --dir integrations/codex-workflow test
```

针对性回归使用隔离 socket 与模拟后台，检查双向消息和审批转发、工具断线不重发、3 个并发启动只创建一份后台、辅助 CLI 转交、未知端点保护及路径恢复。它们不冒充原装 Desktop 验收。

实验版已完成冷启动 2 次、正常重开复用 1 次，均有桌面工具和 IAB 实际调用证据。工程版最终只需一次人工正常流程确认：启用 → 原图标冷启动 → 工具/IAB → 正常退出 → 原图标重开 → 原聊天工具/IAB → 停用恢复。异常恢复和压力测试后续按需要补充，不再作为当前集成门槛。

## 平台实现边界

共享部分：安装事务与回滚、后台启动协调、聊天协议转发、MCP 路由与重连、Workflow Continue/Stop。系统操作通过 `platform.mjs` 和 Go `codexruntime` 平台文件调用；这些调用是短命 CLI 命令，不新增常驻服务。

| 系统能力 | macOS | Windows |
| --- | --- | --- |
| 启动入口 | shell `exec` 原装 Node | 原生 `.exe` 入口启动原装 Node |
| 启动配置 | GUI 环境 + LaunchAgent | HKCU 用户环境 + 环境变更通知 |
| 后台独立运行 | 新进程会话 | Detached + Job breakaway；系统拒绝时明确失败 |
| 后台通信 | 私有 Unix socket | 127.0.0.1 WebSocket + capability token；连接前检查监听 PID |
| 工具桥接 | 每适配器私有 Unix socket | 每适配器随机本机端口 + 独立令牌；认证后才启动插件 |
| 私有文件 | UID、文件类型、权限 | 用户 SID、文件类型、DACL |
| 启动锁 | 文件锁 | Windows 文件锁 |

Windows 不假定 Codex 支持命名管道监听，不使用未认证 TCP。令牌存在私有文件中，不放入 URL、命令行或日志。安装只接受可识别的原版 Desktop/CLI/Node/插件布局；未知布局和不具备认证能力的版本在修改启动配置前报错。

Windows 的原生入口会增加一层进程：`Desktop → 入口 exe → Node 适配器 → 原装插件`。仍保留原生工具校验；该进程链是否被具体 Windows Desktop 版本认可、原图标是否继承新环境、Job 是否允许后台独立存活，必须在 Windows 上实际确认。交叉编译和模拟协议测试不代表这些桌面行为已通过。

最小实机验收：一键连接 → 原图标启动 → Continue/Stop、桌面工具、IAB → 正常退出重开并确认同一后台 → 断开恢复。此轮开发不修改正在使用的 Desktop 启动环境，也不重启真实 App。
