# 按需加载工具

用户设置 `enable_tool_retrieval` 默认关闭，通过 `/user/chat-settings` GET/PATCH 管理。
设置页位于任务设置的对话页；修改从下一次请求生效。普通 SubAgent 与 Workflow
步骤继承发起请求的快照，第三方执行器保持原有行为。关闭后继续使用原 Toolkit 展开机制。

开启后，`search_tools` 通过已安装的 bm25s 检索当前允许、尚未加载的原子工具。
搜索只返回描述，不加载 schema。飞书、Notion、Google Drive 合并为各自配套组；
CloudFileToolkit 本身不能作为整组加载。`load_tools` 在同一笔事务中先卸载再加载，
未知名称、权限限制、必需工具保护或持久化错误都会阻止提交。

工具软预算为现有有效输入预算的 10%。卸载后未超过阈值即可完整加入本批工具，
超过后只能卸载或幂等加载。Host 必需工具和已读取 Skill 的允许依赖绕过软预算，
仍计入每轮完整上下文的硬限制。新加载定义只在下一轮模型请求生效。

状态位于 `agentic_workspace/tool-retrieval-state/`，按用户、会话和 Agent 标识隔离。
文件锁与原子替换保证加载成功前状态已落盘；上下文预览只读。恢复时重新验证当前目录
和 Skill 依赖，历史工具调用不重新激活工具。必需性消失后解除保护但不自动卸载。
跨 Host 同步范围与现有运行时目录一致。

## 验证

基于 LazyMind `83322b4` 和 LazyLLM `8cf9e610` 实施。最新 LazyLLM 移除了旧
`list_dir`/`write_file` 导出，因此主仓库文件包装器同时适配了新接口，保留原覆盖审批
和递归目录契约。

容器内验证包括：

- LazyLLM 检索、分组、预算原子性、下一轮曝光、线程状态传播及旧模式执行回归。
- AgentExecutor 搜索/加载/执行完整链、Skill 文件依赖、状态恢复/隔离/写失败、只读预览和硬预算。
- 用户设置保存、隔离、请求快照、SubAgent/Workflow 继承与隔离；文件及上下文压缩回归。
- PostgreSQL 迁移路径一致性、PostgreSQL/SQLite 新字段默认值与 up/down/up。
- 前端开关加载、保存、失败恢复和原任务入口设置回归。

前端全量 TypeScript 检查受已有 `src/modules/chat/utils/message.test.ts:49` 语法错误阻断。
未修改该无关文件。

## 本地对照与后续线上验收

2026-09-17，在无外部工具凭据、默认注册目录和内置文件工具下，以脚本模型驱动
真实 AgentExecutor 和 calculator，分别执行 `1+2`、`6*7`。两种模式均得到正确结果。
这验证执行链和工具定义占用，**不代表真实模型成功率或成本评估**。

| 指标（每个任务） | 关闭 | 开启 |
| --- | ---: | ---: |
| 模型请求轮次 | 2 | 2 |
| 峰值工具定义 token 估算 | 7826 | 2401 |
| 暴露但未使用的工具定义数 | 34 | 13 |
| 搜索/加载调用数 | 0 | 0 |

独立集成测试覆盖需要搜索和加载的路径。实际模型总 token、费用、缓存命中以及
飞书/Notion/Google Drive、知识库、邮件恢复的外部服务端到端效果尚未实测。
上线试用应对同批业务任务记录成功率及这些指标，再判断收益。

复用现有 `agent_lab_event_path`/`context_compression_event_path` telemetry：
`tools_ready` 记录每轮工具名称和 schema token 估算，结合既有工具调用事件统计未使用
schema、搜索/加载次数；模型 usage 中可用的成本和缓存数据沿用现有统计。未新增看板。
