# Codex 桌面 Workflow 通信方案

当前方案使用桌面应用自带的 `codex queue` 向原会话投递唤醒输入。配置与运行方式见
[Codex Workflow 接入](../../integrations/codex-workflow/README.md)，语义以
[外部 Agent 协议](../architecture/external-agent-workflow-protocol-draft.md)及其 Codex 降级条款为准。

## 安装入口

在 LazyMind 的外部 Agent 设置中连接 Codex，即安装 LazyMind Workflow 插件。源码环境运行 `make codex-workflow-install`。
插件负责 MCP 配置、稳定配对和 dispatcher 生命周期；用户无需手动配置变量或启动单独终端。安装后在 Codex 新任务中加载工具。

## 通信流程

1. Agent 在当前桌面会话调用 `workflow.start`。MCP 从宿主元数据取得 thread UUID；缺失时由 Agent 传入当前宿主环境提供的真实 UUID。
2. MCP 创建 run 并用已配对的接入身份绑定该 thread，随后返回最新状态。启动不产生 HostAction。
3. Agent 通过 MCP 申请执行步骤。进入人工审核时，提示词要求告知用户并结束当前 turn。
4. 用户在 Panel 审核并继续，Core 先更新权威状态，再按协议创建 continue HostAction。
5. dispatcher 领取、复核，调用 `codex queue --thread <UUID> --message <prompt>`。
6. Agent 处理消息时重新读取状态。带 execution_id 的通知走 step.claim；普通推进按最新授权走 step.begin。

## 暂不支持的能力

- 不中断正在执行的 Codex turn，不发送 turn/interrupt 或 turn/steer。
- Stop 只保证 Core 停止和撤销执行授权，不能撤回已经发出的工具操作。
- 无 DSH 的执行前钩子；人工审核时结束 turn 依赖 Agent 遵循提示，后续步骤授权仍由 Core 强制校验。
- 无可靠的队列接收证据查询时，unknown 不自动重发。

## App Server 与当前实现的关系

`codex app-server` 可启动一个后端，供客户端通过协议控制其管理的会话；它本身不是桌面 UI。
本接入不启动额外后端，也不依赖现有桌面 App Server 对外监听端点。Socket/RPC、会话扫描、原生 turn 归属追踪和控制探针已从本接入删除。

桌面版本是否支持 queue、MCP 是否携带 thread 元数据，以及实际唤醒时序，均应以目标环境验收为准。
本方案不要求安装另一份 Codex CLI。
