# 上下文压缩：入口、职责和保护范围

本文对应 `perf/compact` 合入主干 Skill Retrieval、Tool Retrieval 与工作区授权后的实现。

## 一次模型调用的顺序

1. `chat_service.py` / `subagent/runner.py` 构建 `AgentRunPlan`：提示词、历史、工具范围、工作区授权快照和模型配置。
2. `AgentExecutor.create_agent()` 选择压缩器、配置工具检索并安装工具执行中间件。
3. LazyLLM `FunctionCall` 构造 prior/current 消息，提供模型前缀、完整工具轮次与运行时通知预算。
4. LazyMind 压缩器返回 `(projected_prior, projected_current)`，只调整模型视图。
5. `FunctionCall` 附加本轮运行时通知，通过 ChatPrompter 发给模型。
6. 模型调用工具时，中间件依次处理失败策略、调用额度和工作区授权，再执行工具并返回原结果。工具额度不替代授权，授权拒绝不消耗实际执行额度。

## 三处不同的内容缩减

| 入口 | 触发条件 | 处理内容 |
| --- | --- | --- |
| 普通聊天 / 普通 SubAgent：`pruner.make_history_compactor` | 大型新工具结果，或达到配置的压力阈值 | 工具专用缩减、落盘，以及启用时的旧历史摘要；维护稳定投影以减少重复改写 |
| Workflow Step：`workflow_compactor.make_workflow_history_compactor` | 估算的下一次请求超过有效输入预算 | 依次落盘旧结果、移除最早完整轮次、最后落盘当前轮结果；不调用 LLM 总结执行历史 |
| SubAgent 输出事件：`runner._truncate_tool_result` | 普通结果超过 64 KiB 后，估算达到 32K tokens | 缩减写入步骤记录的工具结果事件，正文落盘；Skill / artifact 结果沿用专用返回策略 |

第三处不是 FunctionCall 的历史压缩器。三个入口不能用同一个“全文/摘要开关”解释，也不意味着所有工具都返回全文。

`context_compression_enabled=False` 时，Executor 不安装历史压缩器。普通路径默认压力阈值为有效预算的 0.9、目标为 0.45；Workflow 只看是否超出有效预算，不套用这两个提前压缩比例。

## Workflow 的确定性规则

- 预算包含前缀、工具定义、Skill 目录提示、当前输入、prior/current 历史和运行时通知预留；有效输入预算扣除输出预留。
- 预算内不修改历史，不生成落盘文件。
- 超预算后，从旧到新替换未保护轮次的结果正文，保留调用与结果消息；仍超限再删除旧的完整轮次。
- 最近配置数量的完整旧轮次受到保护。当前轮永不整轮删除，但最后可以将其结果正文换成文件引用。
- `ToolCallTurn` 的 `[start, stop)` 与 `result_indexes` 索引合并后的 `prior + current`；`current` 标记整轮保护，调用和结果可以跨两边分布。
- 配对识别由 LazyLLM `describe_tool_turns` 负责；缺失、重复 ID、插入其他消息或未完成的轮次不作为可删除单元。
- 文件提示使用绝对路径，避免新 Host `read` 工具按另一个工作目录解析相对路径。
- 受保护内容过大、没有工作区或落盘失败时，压缩器可能仍返回超预算历史。当前实现不承诺任何请求都能装进窗口。

## 普通摘要中的固定预算

原始用户任务等受保护前缀、最近消息尾部和非历史上下文均不能由本次摘要缩小。摘要校验将三者一起计入固定预算；如果配置目标低于该固定预算，回收比例按可达到的下限计算，避免拒绝已经有效缩小的摘要。必需章节、原文约束、调用配对与摘要压缩质量检查继续生效。

## Skill：加载、压缩和恢复

主干 `SkillManager` 负责目录发现、检索、加载与已声明资源访问；LazyMind 不再从历史拼接旧的候选 Skill 列表来替代主干范围管理。

- `get_skill` 返回正文及来源信息；`@Skill` 初次加载通过 `append_loaded_skill_invocations` 形成配对工具历史。
- 模型使用 `read_skill_resource` 读取已加载 Skill 声明的资源；历史 `read_reference` 名称继续兼容压缩。
- 结果不再被中间件立即替换成 locator，也不写入隐藏的 `pinned_skill_prompt`。
- 需要缩减旧结果时，专用压缩器保留 Skill 标识、路径、哈希及准确重载参数。优先用完整 `skill_key` 避免同名歧义。
- 资源读取结果保留 `name` / `rel_path`；加载和资源声明检查仍由 SkillManager 执行，压缩不会绕过它们。
- 任务目标、约束和产物定位沿用各自的运行时／持久化逻辑；它们不是 Skill 正文 pin。

## 代码位置与边界

- `algorithm/lazymind/chat/engine/agent_runtime/executor.py`：选择压缩器、组装执行器。
- `algorithm/lazymind/chat/engine/agent_runtime/pruner.py`：普通历史投影与摘要决策。
- `algorithm/lazymind/chat/engine/agent_runtime/workflow_compactor.py`：Workflow 超限策略。
- `algorithm/lazymind/chat/engine/agent_runtime/compactors.py`：工具分类、重载提示、落盘。
- `algorithm/lazymind/chat/engine/agent_runtime/tool_call_guard.py`：额度、失败策略和授权，不改写 Skill 正文。
- `algorithm/lazyllm/lazyllm/tools/agent/history.py`：通用完整轮次契约。
- `algorithm/lazyllm/lazyllm/tools/agent/skill_manager.py`：Skill 加载与资源访问。

仍使用模型无关 token 估算，运行时通知仍在压缩后注入并预留预算；这里尚未实现统一的最终 Model Request 计量接口。不能把“去掉了未发送的动态 pin”理解为“估算与供应商实际 token 数完全一致”。
