# Codex Workflow queue 接入

控制路径：Panel → Core HostAction → 共享 dispatcher → 桌面应用自带的 `codex queue` → 已绑定的原会话。
执行路径：Agent → LazyMind MCP → Core。集成不启动 App Server，不需要桌面应用开放 Socket。

## 能力

- Continue、Resume、Retry、Rewind 和内部执行完成均沿用 `continue` HostAction，以 queue 唤醒。
- `workflow.start` 在 MCP 内完成 Controller 绑定，返回绑定后的最新状态；不扫描会话，不额外发送首次绑定唤醒。
- 不支持中断当前 Codex turn。Panel Stop 仍立即在 Core 停止 Workflow、撤销授权；cancel 通知结算为 failed，并明确说明宿主不支持中断。
- 人工审核依赖 MCP 指令、状态返回的 `agent_instruction` 和唤醒 prompt：遇到 `awaiting_user` 结束当前 turn，不代审、不继续下一步、不轮询等待。DSH 仍使用既有钩子。
- 启动前缺少页数等必填参数时，Codex 异步提问后保持当前 turn，等待真实回答；`accepted` 仅代表问题已展示。此时尚未进入 Workflow 的 `awaiting_user`，不能套用结束 turn 的规则。提问工具失败或选项窗消失时，完整列出问题和选项，允许用户通过文字回答。

## 一键安装

普通使用：在 LazyMind「设置 → 外部 Agent 集成 → Codex」点击连接。已使用旧 MCP 接入的用户重新连接一次，即可升级为 **LazyMind Workflow** 插件。

当前源码开发环境只需在仓库根目录执行：

```bash
make codex-workflow-install
```

安装会构建集成包与 LazyMind CLI、启动 Assistant Bridge、定位桌面 Codex 与 Node、完成配对、注册个人插件市场条目并安装插件。成功后迁移旧的 LazyMind MCP 配置，其他 MCP 和插件设置保持原样。无需另外安装 Codex CLI。

然后在 Codex 新建一个任务，要求启动一个包含人工审核的 Workflow。到审核点应结束当前 turn，点击 Panel 的继续后应在同一任务恢复。

需要已登录 LazyMind、已初始化的桌面 Codex，以及 Node.js >= 22.19。安装器自动查找 Node，找不到或版本过旧会明确报告。发布版用户不需要 pnpm；dispatcher 已随 LazyMind CLI 内嵌。

## 插件生命周期

- 插件源安装在 `~/plugins/lazymind-workflow`，市场条目加入 `~/.agents/plugins/marketplace.json`，保留其他条目和顺序。
- 插件提供 Workflow skill 和 LazyMind MCP；MCP 使用 `lazymind mcp codex-workflow`，自动启动 queue dispatcher。所有 dispatcher 输出写到 `LAZYMIND_HOME/logs/codex-workflow.log`，不污染 MCP stdout。
- 同一个 profile 的多个 MCP 进程等待同一 dispatcher 锁；当前持有者退出后，其他进程接替。MCP 断开或父进程死亡时，其 worker 退出；异常退出会自动重启。
- 配对、CLI 路径、profile、环境变量由安装器生成。配对密钥不进入插件文件或安装输出。
- 从 LazyMind 断开会卸载插件并禁用配对。重新连接会恢复同一配对身份；源码变化会自动更新插件缓存版本。
- 若插件目录或同名市场条目属于其他安装，安装器拒绝覆盖并报告冲突。

## 开发调试

通常使用上面的一键安装入口。仅调试独立 dispatcher 时，可继续使用 `src/main.ts` 的 `--codex-bin`、`--codex-home`、`--pairing-file` 参数；配对命令为 `lazymind internal codex-workflow-pair --codex-home <profile>`。
`pnpm --dir integrations/codex-workflow build` 同时更新 Go CLI 内嵌的 dispatcher；不要只修改生成的 `assets/dispatcher.mjs`。

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
- 使用参数数组调用 `queue --thread <UUID> --message <prompt>`，不经 shell。`action_id` 放入消息仅用于关联，不宣称 CLI 提供原生去重。
- queue 成功退出记 accepted、native_event_seq=0；这只表示输入受理，不表示 Agent 已运行。
- 可确定进程未启动（如二进制缺失或无执行权限）记 failed；非零退出、超时、启动后中止或成功回执丢失均保守记 unknown。
- unknown 和租约过期后的 dispatching 不自动重发，不编造接收证据或原生事件序号。需要用户检查 Panel 后再明确 Continue。
- 排队消息可能延后处理，无法撤回或在宿主执行前拦截。prompt 要求先读 Core 最新状态；所有步骤仍需 MCP 授权。

## 验证

```bash
pnpm --dir integrations/codex-workflow typecheck
pnpm --dir integrations/codex-workflow test
pnpm --dir integrations/codex-workflow build
```

自动测试覆盖 queue 参数、原会话 UUID、取消不支持、过期通知、重复领取、超时、回执丢失与 dispatcher 重启。
真实桌面验收应在专门的测试 Workflow 中确认启动绑定 → 审核结束 turn → Panel Continue 唤醒同一会话；自动测试不替代这项端到端验收。
启动前询问也需手动验收：省略 PPT 页数等必填参数，待选项出现后至少等 15 秒，确认 agent 未发最终回复且选项仍可提交；回答后才调用 `workflow.start`。这项规则属于插件对 agent 的指引，不能替代 Codex 原生输入 UI 的生命周期保障。
