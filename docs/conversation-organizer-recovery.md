# 会话整理失败后的恢复策略

后端 `conversationgroup/recovery.go` 统一判定 DTO 的 `can_retry`、`can_restart` 和重试接口权限。前端按能力展示按钮，不根据 `failed` 状态自行推断。

| 类别 | 错误码 | 行为 |
| --- | --- | --- |
| 网络与超时 | `connection_error`、`connection_timeout`、`response_timeout`、`first_response_timeout`、`stream_idle_timeout`、`request_timeout`、`transport_error` | 重试原任务，保留已提交进度 |
| 暂时性模型服务错误 | `rate_limited`、`concurrency_limited`、`provider_overloaded`、`service_unavailable`、`provider_internal_error` | 按算法的 retryable 判定重试；明确不可重试时不覆盖算法判定 |
| 调度与配置读取 | `lease_lost`、`lock_expired`、`update_failed`、`model_config` | 当前配置可读取且与快照一致时重试；配置变化则重新整理 |
| 冻结数据失效 | `model_config_changed`、`invalid_snapshot` | 重新整理，创建新任务，重新冻结配置与会话范围 |
| 模型配置、权限、容量 | `model_configuration`、`authentication_failed`、`permission_denied`、`not_found`、`invalid_request`、`token_limit`、`input_too_large`、`output_too_large` | 不重试旧任务；提示先处理原因，再重新整理。输入/输出过大已先尝试缩小批次 |
| 额度与过滤 | `usage_limit_exceeded`、`quota_exhausted`、`balance_exhausted`、`organization_spend_limit_exceeded`、`project_spend_limit_exceeded`、`input_filtered`、`output_filtered` | 不重试旧任务；处理原因后重新整理 |
| 旧调用未停止 | `cancellation_unconfirmed`，或持久化执行记录尚未 settled | 只允许原任务重试，先确认旧调用退出；禁止直接新开任务绕过停止确认 |
| 内部或未细分错误 | `invalid_payload`、`run_not_found`、`handler_not_found`、`handler_failed`、`preparation_failed`、`incremental_step_failed`、`apply_failed`、`step_limit`、`invalid_output`、`model_failed`、`worker_failed`、`worker_exited`、`protocol_error`、`provider_rejected`、`conflict`、`unprocessable_entity`、未知/空错误码 | 首次失败不提供盲目重试，提示排查原因。阶段错误可能包含数据、协议或程序错误，不能统一视为暂时故障 |

## 手动重试再次失败

每次手动重试创建新的 `async_jobs` 记录；自动重试只增加同一 Job 的 attempt。通过同一整理任务对应的 Job 数量判断是否发生过手动重试，刷新后仍可恢复。

- 手动重试再次失败，且旧调用已退出：提供“重新整理”。若当前错误仍可重试，同时保留“重试本次整理”。
- “重新整理”调用新建接口，不复用旧任务的配置、快照或检查点。
- 未确认退出、执行记录损坏时不提供重新整理；新建接口也会执行停止确认。
- 已取消且调用退出：重新整理。

算法的具体错误码和 retryable 会保留，不再统一丢失为 `incremental_step_failed`。自动重试仅用于明确可恢复的调用错误；未知错误保守终止。普通摘要条目的失败仍按原流程记录为跳过，不属于整个整理任务的失败。

复用边界：恢复策略属于会话整理领域（依赖冻结模型、快照、流停止确认），抽成公共函数供 DTO 和接口共用；不放入通用 AsyncJob 基类。
