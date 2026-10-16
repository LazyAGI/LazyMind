# Codex Workflow 接入

功能对比、整体架构与验证范围见 [Codex 共享后台与 Workflow 原生控制](../../docs/development/codex-shared-server.md)。本文保留安装和开发操作说明。

控制路径：Panel → Core HostAction → dispatcher → 已绑定的原会话。启用共享模式后使用 App Server 原生 RPC；未启用时保留 `codex queue`。
执行路径：Agent → LazyMind MCP → Core。

[desktop-runtime](desktop-runtime/README.md) 提供共享后台的准备、启用和恢复入口。启用时在 Codex profile 写入私有的 `lazymind-shared-server.json`；dispatcher 与 MCP 绑定读取同一登记，分别选择原生控制和声明 `cancellation=session`。登记无效、后台不可用或断线时明确失败，不自动回退 queue。macOS 已接入现有“连接/断开 Codex”入口；Windows 已有平台实现，尚待 Desktop 实机验收。

## 能力

- Continue、Resume、Retry、Rewind 和内部执行完成均沿用 `continue` HostAction。共享模式以 `turn/start` 在原聊天提交一轮，并保存 action/run/thread/turn/client ID；聊天忙时不追加到用户正在执行的轮次。
- `workflow.start` 在 MCP 内完成 Controller 绑定，返回绑定后的最新状态；不扫描会话，不额外发送首次绑定唤醒。
- Stop 先在 Core 撤销 Workflow 授权。共享模式核对当前 turn 的原生 MCP 执行证据或本机投递回执，仅对属于该 Workflow 的 turn 调用 `turn/interrupt` 并确认结果；普通用户轮次不发送中断。queue 模式仍不支持原生中断。
- 人工审核依赖 MCP 指令、状态返回的 `agent_instruction` 和唤醒 prompt：遇到 `awaiting_user` 结束当前 turn，不代审、不继续下一步、不轮询等待。DSH 仍使用既有钩子。
- 启动前缺少页数等必填参数时，Codex 异步提问后保持当前 turn，等待真实回答；`accepted` 仅代表问题已展示。此时尚未进入 Workflow 的 `awaiting_user`，不能套用结束 turn 的规则。提问工具失败或选项窗消失时，完整列出问题和选项，允许用户通过文字回答。

## 一键安装

普通使用：在 LazyMind「设置 → 外部 Agent 集成 → Codex」点击连接。受支持的桌面安装会同时安装/更新 **LazyMind Workflow** 插件和共享运行组件，完成后正常退出并重开 Codex。已有共享模式升级时，先退出 Codex 再连接；安装器不会主动结束聊天。

当前源码开发环境只需在仓库根目录执行：

```bash
make codex-workflow-install
```

安装会构建集成包与 LazyMind CLI、启动 Assistant Bridge、定位桌面 Codex 与 Node、完成配对、注册个人插件市场条目并安装插件。成功后迁移旧的 LazyMind MCP 配置；自动配置对应平台的用户级启动环境与共享后台登记。其他 MCP 和插件设置保持原样，无需另外安装 Codex CLI。

然后在 Codex 新建一个任务，要求启动一个包含人工审核的 Workflow。到审核点应结束当前 turn，点击 Panel 的继续后应在同一任务恢复。

需要已登录 LazyMind、已初始化的桌面 Codex，以及 Node.js >= 22.19。安装器自动查找 Node，找不到或版本过旧会明确报告。共享运行组件使用内嵌的 Go helper，不再依赖 Python。发布版用户不需要 pnpm；dispatcher 和共享运行组件均随 LazyMind CLI 内嵌。安装不会改变已有 CLI 功能开关；自动 Continue/Stop 仍需开启该开关。

## 插件生命周期

- 插件源安装在 `~/plugins/lazymind-workflow`，市场条目加入 `~/.agents/plugins/marketplace.json`，保留其他条目和顺序。
- 插件提供 Workflow skill 和 LazyMind MCP；MCP 使用 `lazymind mcp codex-workflow`，在 Codex CLI 功能启用时自动启动 dispatcher。所有 dispatcher 输出写到 `LAZYMIND_HOME/logs/codex-workflow.log`，不污染 MCP stdout。
- 同一个 profile 的多个 MCP 进程等待同一 dispatcher 锁；当前持有者退出后，其他进程接替。MCP 断开、父进程死亡或 CLI 功能关闭时，其 worker 退出；重新启用后恢复。worker 的退出不停止共享 App Server。
- 配对、CLI 路径、profile、环境变量由安装器生成。配对密钥不进入插件文件或安装输出。
- 断开前需正常退出 Codex；程序先恢复原启动环境，再卸载插件并禁用配对。共享后台及其安装目录会保留，避免终止未完成任务。重新连接会恢复同一配对身份；源码变化会自动更新插件缓存版本。
- 若插件目录或同名市场条目属于其他安装，安装器拒绝覆盖并报告冲突。

## 开发调试

通常使用上面的一键安装入口。仅调试独立 dispatcher 时，可继续使用 `src/main.ts` 的 `--codex-bin`、`--codex-home`、`--pairing-file` 参数；配对命令为 `lazymind internal codex-workflow-pair --codex-home <profile>`。
`pnpm --dir integrations/codex-workflow build` 同时更新 Go CLI 内嵌的 dispatcher；不要只修改生成的 `assets/dispatcher.mjs`。调试可显式传 `--app-server-url`；正常使用以 profile 登记为准，不使用 Desktop 的 `CODEX_APP_SERVER_WS_URL` 覆盖。

部署新代码需要更新 LazyMind CLI 和界面，随后使用“连接 Codex”更新插件及共享运行组件。源码构建不会替换正在运行的 CLI。共享模式下新建的 Workflow 才会绑定原生中断能力；已按 queue 绑定的旧 Workflow 不自动迁移。

## 会话绑定

启动调用优先读取请求 `_meta.thread_id` / `threadId`，或 `_meta["x-codex-turn-metadata"]` 中相同字段。
这些是兼容读取入口，不表示所有桌面版本都保证发送这些字段；MCP 传输会话 ID 不能代替 Codex thread ID。

元数据缺失时，Agent 从当前宿主执行环境读取 `CODEX_THREAD_ID`，将真实 UUID 作为 `workflow.start.driver_session_id` 传入。
该环境变量若也不可用，应明确报错，不能猜测 ID、使用会话标题或其他会话。
宿主元数据优先于 Agent 参数；格式错误或相互冲突的宿主元数据直接拒绝。

Agent 参数回退属于协议 §8 明确允许的 Codex 降级：UUID 校验不能证明当前会话归属，集成依赖 Agent 正确转交宿主身份。
调用启动时建议提供稳定的 `idempotency_key`。如果 run 已创建而绑定失败，错误会返回原 run ID 和重试 key；按原参数重试，不创建另一个 run。

## 投递与异常

- Core 原子创建 HostAction、claim 限制并发投递、发送前复核绑定版本与最新控制状态。每个 profile 配对仅允许一个协作 dispatcher。
- 共享模式在发送前再次核对 Core 控制状态，先持久化投递意图，再发送；以返回的 turn ID 和 client ID 核对归属。回执只保存标识，不保存聊天正文。
- queue 模式使用参数数组调用 `queue --thread <UUID> --message <prompt>`，不经 shell。不宣称 CLI 提供原生去重。
- 两种模式均不伪造原生事件序号，`native_event_seq=0`。原生 Continue 的 accepted 表示已接受并核对新 turn；Stop 的 accepted 表示目标已 interrupted，或本来已空闲。
- 可确定进程未启动（如二进制缺失或无执行权限）记 failed；非零退出、超时、启动后中止或成功回执丢失均保守记 unknown。
- unknown 和租约过期后的 dispatching 不自动重发，不编造接收证据或原生事件序号。需要用户检查 Panel 后再明确 Continue。
- 控制通知可能延后处理。prompt 要求先读 Core 最新状态；所有步骤仍需 MCP 授权。原生审批请求只提示等待真实用户，不自动批准；外部发起轮次的实际 Desktop 审批路由仍需业务验收。

## 验证

```bash
pnpm --dir integrations/codex-workflow typecheck
pnpm --dir integrations/codex-workflow test
pnpm --dir integrations/codex-workflow build
pnpm --dir integrations/codex-workflow test:control
pnpm --dir integrations/codex-workflow test:install
```

自动测试覆盖 queue 兼容、原生 turn 归属、过期控制、重复领取、回执丢失与 worker 启停。`test:control` 使用构建后的 dispatcher 和真实本地 HTTP/Unix WebSocket 传输，模拟 Core/App Server 验证 Continue → Stop → 回执结算；不调用真实模型或用户 Workflow。
2026-10-15 本机部署后，用户反馈新 Workflow 的 Continue/Stop 测试正常。后续版本的真实桌面验收应在专门的测试 Workflow 中确认启动绑定 → 审核结束 turn → Panel Continue 唤醒同一会话 → Stop 中断该 Workflow 的执行轮次；自动测试不替代这项端到端验收。
启动前询问也需手动验收：省略 PPT 页数等必填参数，待选项出现后至少等 15 秒，确认 agent 未发最终回复且选项仍可提交；回答后才调用 `workflow.start`。这项规则属于插件对 agent 的指引，不能替代 Codex 原生输入 UI 的生命周期保障。
