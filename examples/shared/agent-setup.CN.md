# 连接现有 Agent

[English](agent-setup.md) · [案例首页](../README.CN.md)

## 一次性准备

1. 按[快速开始](../../docs/quick_start.CN.md)安装、启动并登录 LazyMind。接入与运行全程使用同一实例。
2. 配置并验证案例需要的模型和工具。PPT 与知识库检索所需能力不同，HTTP 健康检查不能代替能力验证。
3. 在 LazyMind 设置的外部 Agent 集成入口连接 **Codex** 或 **DeepSeek Harness**。使用连接器安装流程，不在聊天里复制 Token 或手填内部 ID。
4. 在已连接的 Agent 中新建任务，让它先列出该案例需要的 LazyMind 能力。若已有同名连接属于另一实例，先处理归属冲突，不为演示直接覆盖或断开它。

| 宿主 | 接入依据 | 审核方式 |
| --- | --- | --- |
| Codex Desktop | [Codex Workflow 插件](../../integrations/codex-workflow/README.md) | 集成要求宿主打开返回的 Workflow 页面；无法打开时明确提供链接 |
| DeepSeek Harness | [DSH Workflow 插件](../../integrations/dsh-workflow/README.md) | 插件展示与当前会话绑定的 Workflow 面板 |

当前 Codex 源码集成要求已初始化桌面 Codex、Node.js >= 22.19；DSH 集成文档标明 SDK 基线为 0.1.6-alpha.1。实际运行要记录安装版本，不能把源码要求当成已验证的发行版组合。

## 检查所需能力

- **PPT：**能发现已发布的 PPT Workflow，输入资料可读取，生成与导出依赖可用。列出工具不代表运行已通过。
- **知识库：**能使用 `knowledge.list`、`knowledge.search` 和 `knowledge.document.get`。按名称选择目标库，同名时确认，不由用户猜 ID。检索返回证据，外部 Agent 负责综合回答。

审核由用户在面板里完成，不让 Agent 代审。Codex 和 DSH 的宿主控制能力不同，尤其 Codex 的 Stop 不保证中断已经执行的原生工具。返回链接或通知已排队，都不证明面板已打开或 Agent 已恢复。

## 分开记录首次准备与日常使用

首次安装、模型配置和资料入库，与后续发任务、审核、传文件、修改分别记录。包括失败、重试、等待与人工修正；说明究竟省掉了哪些操作，再谈使用收益。

对照时允许同一 Agent 正常读取同一批本地资料、使用已有技能。尽量保持模型与任务一致；不同则说明差异。不刻意削弱对照组。

## 源码环境验证

使用隔离测试实例，记录真正部署的构建版本，不能只记录当前 Git HEAD。更新仓库不会自动更新正在运行的二进制。重启前遵循所在 checkout 的部署约定，保留本地配置与业务数据。验收记录不包含凭据、私有文件路径或未经脱敏的宿主配置。
