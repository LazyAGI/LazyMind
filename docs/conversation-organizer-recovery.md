# 会话整理失败后的恢复策略

Core 的 `conversationgroup/recovery.go` 分别判断自动重试与用户恢复动作。DTO、重试接口、新建接口使用同一恢复策略；前端只展示后端允许的按钮。

## 恢复分类

| 类别 | 错误码 | 自动重试 | 用户恢复 |
| --- | --- | --- | --- |
| 网络与超时 | connection_error、connection_timeout、response_timeout、first_response_timeout、stream_idle_timeout、request_timeout、transport_error | 有限退避 | 保留进度重试 |
| 暂时性模型服务错误 | rate_limited、concurrency_limited、provider_overloaded、service_unavailable、provider_internal_error | 有限退避 | 保留进度重试 |
| 调度与明确可重试的数据库失败 | lease_lost、lock_expired、database_unavailable | 有限退避 | 保留进度重试 |
| 模型配置读取 | model_config | 不自动循环 | 配置恢复可读且一致后重试 |
| 权限与额度 | authentication_failed、permission_denied、usage_limit_exceeded、quota_exhausted、balance_exhausted、organization_spend_limit_exceeded、project_spend_limit_exceeded | 不自动重试 | 提示处理原因后保留进度重试；有效模型配置变化则重新整理 |
| 输出校验 | invalid_output | Algorithm 本地最多三轮；Core 本批方案最多三轮，不额外自动重启 job | 重置修复预算，保留已提交进度；已有待审核方案和审核页进度不丢弃 |
| 范围无法收敛 | scope_audit_unresolved | 达到修复及保守模式上限后停止 | 保留已提交目录与拒绝证据，清除未提交方案及其新组映射，从保守模式重试本批 |
| 配置或快照变化 | model_config_changed、invalid_snapshot | 否 | 新建任务，使用当前配置与会话范围 |
| 容量 | input_too_large、output_too_large | batch 和 audit 都先减半；最小一条仍失败后停止 | 提示处理容量；更换有效配置后重新整理 |
| 配置、请求、过滤与不明确供应商错误 | model_configuration、invalid_task_config、not_found、invalid_request、token_limit、input_filtered、output_filtered、protocol_error、provider_rejected、conflict、unprocessable_entity | 否 | 暂不可恢复，处理具体原因；有效配置变化后可重新整理 |
| 未细分内部错误 | invalid_payload、run_not_found、handler_not_found、handler_failed、preparation_failed、incremental_step_failed、apply_failed、update_failed、step_limit、model_failed、worker_failed、worker_exited、未知或空码 | 否 | 不盲目重放旧状态，也不默认提供重新整理；先检查任务 |
| 旧调用未退出 | cancellation_unconfirmed | 不自动循环 | 手动重试先确认旧调用退出，未确认时返回 409 且不创建 job |
| 用户取消 | canceled 状态 | 否 | 重新整理；仍需先确认旧调用退出 |

模型失败优先按已识别的语义分类：HTTP 200 中的限流仍可重试，HTTP 429 中的余额/额度不足不自动重试。未知供应商拒绝仅在明确暂时性 HTTP 状态下转换。`context_length_exceeded` 明确映射为输入过大；其他 token_limit 不猜测是额度还是容量。

## 恢复入口与状态

- 自动重试沿用同一 job，最多三个 attempt；前两次失败后等待 10、30 秒。手动重试创建新 job。
- `retryable` 仅用于自动重试，不否定手动恢复能力。
- 续跑要求模型配置一致、快照及 checkpoint 基本结构有效。无法读取配置时暂不恢复，不视为配置变化。
- 重试接口在创建 job 前确认旧执行退出。新建接口同样确认退出，并校验是否允许重新整理，不能绕过恢复策略。
- 新建与重试都会检查已有活动任务和未处理的成功结果。恢复按钮互斥。
- 执行记录 JSON 损坏时不提供恢复入口。历史通用 incremental_step_failed 不批量放开；仅既有的范围修复耗尽消息兼容为 scope_audit_unresolved。
- API 保留原失败码，页面显示安全的本地化原因、已保存进度、错误码与任务 ID，不展示原始堆栈。
- 普通摘要条目的失败仍记录为跳过；重试整理不自动补算这些已处理摘要。

## 分组与应用安全

先在内存中依序完成全部候选组调整，保存旧成员审核证据；使用完整目录解析本批归属，按最终范围审核旧成员，全部通过后在一个事务中提交目录、归属和 checkpoint。未知引用与循环引用不能跳过。

范围审核保留旧成员覆盖与共同业务场景检查。审核拒绝时保存原因与证据；无法形成共同场景或多次失败后，将已有候选组设为本批只读，仅允许复用、创建或自由会话归属。手动重试耗尽任务不会重新放开已有候选修改。

应用失败仅对 PostgreSQL 明确可重试状态（序列化失败、死锁、连接数暂满、启动中）进行恢复；网络断开导致提交结果不确定时保持 apply_failed，不直接重放。再次执行任务会先读取终态，已成功的结果不重复应用。

策略属于会话整理领域，不修改通用 AsyncJob 框架，不新增数据库表。新增 Core HTTP 错误须同步错误目录与中英文翻译。

## 撤销后的再次整理

新建整理只检查用户最新任务的恢复限制；后续成功结果已确认或撤销时，更早的失败记录不能阻止新建。成员表决定实际归属，状态表保留版本与撤销保护，不能用历史状态中的旧组作为整理前归属。撤销时仍保留版本、来源和成员关系检查；存在跳过项时前端明确提示未恢复数量。
