# 对话标题与初始意图摘要

原生对话的开场元数据保存在 `conversation_opening_metadata`，标题仍使用 `conversations.display_name`。摘要不返回前端、不注入聊天上下文，也不创建 Task 会话。侧聊和分叉在保留为正式对话后参与生成，外部助手、归档、回收站、临时对话不参与。

## 输入和模型调用

读取前三条有效用户输入，跳过纯问候，补充必要的助手澄清、已保存的附件描述。侧聊和分叉只有出现指代时才补充已保存的来源上下文。思考、工具日志和 Base64 附件不进入提示词；已保存的 `vision_extractor` 描述可以作为附件依据。

按 2026-09-07 最后确认的要求，**不设置应用层输入、输出 Token 上限**：长文本完整发送，调用不传 `max_tokens` 或 `max_completion_tokens`，保留模型默认的思考行为。已配置模型上下文容量时，按现有估算器与安全余量进行容量检查；容量未知时由供应商判定，不套用聊天压缩器的默认容量。`usage_json` 记录估算来源、余量、已知容量以及 `truncated=false`，输出预算记为 `null`。

优先使用独立的 `conversation_metadata` 模型选择；未配置或上下文不足时回退用户默认 `llm`。一般故障不切换供应商，两者均无法容纳时记录 `input_too_large`。配置仅用于这一次调用，不改会话主模型。

复用 `/api/chat/llm-task:run` 的 `conversation.describe_opening` 处理器，`mode=llm`，不加载工具或 Skills。标题和摘要的字段长度规范与模型 Token 预算分开：标题最多 255 字，摘要最多 256 字，JSON 字段、类型和语义状态严格校验。输出因模型或供应商上限终止时记为 `output_too_large`，不通过 JSON 修复接受残缺结果。

Core 环境变量 `LAZYMIND_OPENING_TIMEOUT_SECONDS` 默认 60 秒，可配置。每个依据版本最多三次技术尝试；供应商调用关闭叠加重试。容量预检查不计作实际模型调用，超时、限流和暂时性服务故障由 `asyncjob` 统一重试，确定性错误直接失败。

## 状态与并发保护

- `empty`：没有实质任务，标题和摘要为空，保留默认标题。
- `provisional`：已有任务，但关键对象或指代未明确，可等待新的开场信息或附件描述。
- `ready`：主要任务已明确，窗口关闭，不等待执行参数齐备。

一次初始生成，最多两次语义补全。仅当开场输入或附件描述变化时补全；历史回填只生成一次。开场被编辑、删除或替换时重新建立依据版本。用户期间改名会增加标题版本并标记 `user` 来源，旧任务不能覆盖标题，但有效摘要仍可保存。

模型调用在事务外运行，写回检查会话归属、状态、依据版本和任务租约。元数据及标题写入使用独立更新时间或 `UpdateColumns`，不改变会话、父对话的活跃时间和置顶状态。

公共能力留在 `asyncjob`：任务类型过滤、资源互斥、不可重试错误、过期租约恢复和旧执行者写入保护。开场语义规则留在 `chat`，模型传输复用现有 LLM Task 和 AutoModel。

## 历史回填接口

以下路径经网关加 `/api/core` 前缀：

- `POST /conversations/metadata-backfill`：`{"action":"start"}`、`pause`、`resume`、`retry`。
- `GET /conversations/metadata-backfill`：批次状态、完成、失败、跳过、未处理、待执行数量和元数据版本。
- `PATCH /conversations/{name}/title`：`display_name`、`title_revision`，保护并发改名。

首次登录进入布局时幂等启动，每用户一个批次，按 `(updated_at, id)` 从近到远每批扫描 50 条。实时与回填各有一个执行槽，实时待办优先，同会话任务互斥；暂停不会中断正在运行的调用。重新进入页面不重置失败预算。修改模型或超时配置后，可显式 `retry` 技术失败记录；相同配置的重复重试不重新入队。失败的扫描任务也需显式重试。

前端监听普通对话活跃事件及列表刷新事件。有待办时每 5 秒查询，版本改变后刷新列表；页面隐藏时暂停，处理完成后停止。摘要不进入前端状态。

## 迁移与验收

新增增量迁移 `20260907081757_add_conversation_opening`，同时更新 v0.3 聚合迁移。PostgreSQL、SQLite 均支持 up/down；回退保留会话及其当前标题和现有索引。

全部验收在容器内运行：

```sh
# Core
go test ./chat ./asyncjob ./modelprovider ./modelconfig ./algo
MIGRATION_TEST_POSTGRES_DSN=... go test ./migrate

# Python
python -m pytest algorithm/tests/chat/service/test_conversation_opening.py -q

# 真实模型：地址、名称来自验收环境，不进入业务代码
OPENING_MODEL_URL=... OPENING_MODEL_NAME=... \
  python algorithm/tests/chat/acceptance/conversation_opening.py
OPENING_MODEL_URL=... OPENING_MODEL_NAME=... \
  go test ./chat -run TestOpeningRealModelCapacityFallback -v

# Frontend
vitest run src/modules/chat/hooks/useConversationOpening.test.tsx \
  src/modules/chat/components/RecordList/index.test.tsx \
  src/modules/modelProvider/components/DefaultModelConfigPanel.visibility.test.tsx
vite build
```

真实模型用例覆盖十种开场语义及三种长输入要求位置。故障测试覆盖输出截断、超时、限流、认证失败，Core 测试覆盖实际调用计数、容量回退、有限补全、人工标题保护、旧结果拒绝、归档、去重、暂停继续、用户隔离及服务重启后的租约恢复。

2026-09-07 容器验收：Qwen 默认思考模式下 13 个真实模型用例全部通过；实际请求确认没有输出 Token 参数，约 90 万字符的超容量输入由供应商拒绝，归类为 `input_too_large` 且只调用一次。Core 到 Chat 的真实容量回退测试通过。历史 6 条全部回填完成，人工标题、活跃时间及置顶状态保持不变；浏览器新对话自动刷新标题，列表未出现摘要或额外 Task 会话。

Core 相关包与 PostgreSQL、SQLite 完整迁移测试通过，Python 元数据测试 12 项、LazyLLM 响应测试 62 项、前端相关测试 12 项通过，Vite 构建通过。全量 TypeScript 类型检查仍有现有模块及生成代码的错误，不作为已通过项。
