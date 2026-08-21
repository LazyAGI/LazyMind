# ch/feishu 合并后架构与功能审查

## 1. 审查快照与范围

- 分支：`ch/feishu`
- 功能提交：`c0351075`、`eb2e7282`
- 合并目标：`main@077d6dcd`
- 功能分支相对共同基线：101 个文件，约 5155 行新增、505 行删除。
- 分区：Channel Gateway 28、Core 39、Scan Control Plane 8、LazyMind CLI 14、Frontend 6、测试/文档/i18n 6。
- 机械清单审查覆盖 MCP、External Agent、Channel Gateway 相关 522 个文件和 7057 个函数、类、接口及类型声明。

本审查只处理 `ch/feishu` 与 main 的合并交叉点，以及该功能链路本身；main 新增的 PPT、Writer、知识广场和回收站实现只验证兼容性，不做相邻重构。

## 2. 当前架构

### 2.1 Channel Gateway

Channel Gateway 采用组合根 + 端口适配器结构。只有 `bootstrap.py` 认识 Feishu、WeChat、PostgreSQL/SQLite 和 Core HTTP 客户端等具体实现；应用层通过 Provider Registry 取得连接、账号、投递、流式回复和能力画像。[组合根与 Provider Registry](../backend/channel-gateway/channel_gateway/bootstrap.py#L92) [运行时装配](../backend/channel-gateway/channel_gateway/bootstrap.py#L171)

```text
Feishu WebSocket / WeChat Runtime
  -> durable inbox / route / checkpoint
  -> MessageWorker
  -> ChannelMessageService -> Router -> ChannelActionExecutor
  -> LazyMindClient -> Core
  -> durable outbox -> DeliveryWorker -> provider delivery
```

Feishu Runtime 只负责平台事件、账号路由、卡片刷新和平台生命周期；通用命令、会话、能力和回复投影位于 `common/application`，没有反向依赖具体平台。[Feishu 消息入口](../backend/channel-gateway/channel_gateway/feishu/runtime.py#L434) [通用消息工作器](../backend/channel-gateway/channel_gateway/common/application/workers.py#L97) [通用动作执行器](../backend/channel-gateway/channel_gateway/common/application/actions.py#L50)

运行时持久化支持 PostgreSQL 和 SQLite，敏感凭证与 inbox payload 使用不同 purpose 的密钥派生；这使本地/桌面和 Docker 共用同一应用模型。[存储与密钥装配](../backend/channel-gateway/channel_gateway/bootstrap.py#L184)

### 2.2 Feishu 云文档只读链路

云文档能力没有另建一套 Feishu 数据模型，也不会触发扫描或同步：

```text
Feishu 卡片/命令
  -> CloudDocumentActions
  -> LazyMindClient
  -> Core channel-capabilities HTTP API / MCP
  -> capability.Service
  -> CloudDocumentReader
     -> Auth Service：当前用户已授权且 chat-enabled 的 Feishu 连接
     -> Scan Control Plane：在线 tree children/search
  -> 元数据投影返回 Feishu
```

Channel Gateway 的 list/get/search 共用 `CloudDocumentClient` 端口并只产生展示投影。[云文档应用服务](../backend/channel-gateway/channel_gateway/common/application/cloud_documents.py#L11) Core 对 Channel Gateway 暴露三条受权限保护的内部 API。[Core 路由](../backend/core/main.go#L211) Scan 适配器明确只复用既有在线 connector tree，不创建 source 或扫描任务。[Scan 云文档适配器](../backend/core/capability/internal/scanadapter/cloud_documents.go#L1)

### 2.3 外部 Agent 会话与调用账本

LazyMind CLI 的 MCP middleware 在真实工具调用前写入 Invocation Ledger，调用完成后补齐证据；只有不在显式 LazyMind 托管会话中的调用才从客户端 metadata 推导 Codex/Cursor/WorkBuddy 等 provider-native thread。[调用中间件](../local/lazymind-cli/internal/mcpbridge/invocation.go#L32) [来源推导](../local/lazymind-cli/internal/mcpbridge/invocation.go#L97)

Core `externalcontext.Service` 是 provider-native thread 到 LazyMind conversation 的唯一绑定入口，负责：

- 校验 provider/thread/turn 身份；
- 按 `(provider, provider_thread_id)` 解析线程；
- 按 `(conversation_id, provider)` 限制一个会话每个 provider 只有一个绑定；
- 创建仅含用户活动投影的 conversation/history/run；
- 在调用结束后完成 observed turn，并同步最终回答。

[External Context 服务](../backend/core/externalcontext/service.go#L62) [绑定模型与唯一索引](../backend/core/common/orm/external_chat_run.go#L9)

`managed_by_lazymind` 表示线程起源，不能因某一轮从 LazyMind 继续执行而改变。普通会话列表使用未托管绑定区分“LazyMind 助理归属”和“执行引擎”，并与 main 的 `deleted_at IS NULL AND archived_at IS NULL` 条件组合。

### 2.4 前端职责

前端不复制 External Agent 领域模型：助理设置页只维护 provider 目录，聊天历史适配层负责隐藏 provider-native 的 user-only 占位记录并展示统一 execution projection。[助理目录](../frontend/src/modules/agentIntegration/AgentIntegrationPage.tsx#L45) [历史投影](../frontend/src/modules/chat/utils/message.ts#L43) [user-only 过滤](../frontend/src/modules/chat/utils/message.ts#L235)

## 3. 合并冲突结论

本次 9 个文本冲突均按组合语义解决：

1. 会话设置更新保留 main 的 owner/deleted 条件。
2. 会话列表同时保留 main 的归档过滤和 `ch/feishu` 的 assistant/provider 过滤。
3. ORM DDL 同时注册 `ExternalAgentBinding` 与 `ConversationArchiveFolder`。
4. v0.3 migration catalog 合并为 33 个 dev migration，并验证 aggregate 同时包含外部 Agent、归档和运行终态字段。
5. OpenAPI 同时保留归档/回收接口和 conversations 的 `assistant` 参数。
6. 聊天历史类型同时保留 `external_user_only` 与 main 的 run terminal 字段。
7. 错误码冲突已消除：main 保留 `2002098/2002099`，External Agent 两项迁移到 `2002292/2002293`，并从 `i18n/errors/core.json` 重新生成前端目录。

额外修复一处类型交叉：生成的 Core `execution` 字段与聊天层的强类型 projection 不再同时参与交叉类型，避免合并后 `ConversationHistoryItem[]` 无法传入历史构建器。

## 4. 依赖方向与冗余检查

- Channel Gateway Python 内部导入边：177；循环依赖：0；向内依赖规则违规：0。
- Provider-specific 模块没有 Feishu/WeChat 横向依赖。
- Core 的 capability domain/ports 不依赖 HTTP、Scan 或 Channel Gateway；具体适配器从外向内实现端口。
- 外部会话绑定只有 `externalcontext.Service` 一个写入口；会话列表与 chat execution 读取同一 ORM 权威模型，没有第二套前端归属模型。
- 云文档账号权威数据仍在 Auth Service，在线目录权威数据仍在 Scan；Channel Gateway 不缓存第二份业务目录。
- 全前端当前存在 7 个既有 import cycle；唯一触及宽泛审查范围的是 main 的 PPT export/raster cache 环，不在 `ch/feishu` 文件集合内，本次不修改。

当前主要测试债务：`backend/channel-gateway` 没有提交 Python 单元测试。其语法、镜像启动、PostgreSQL 运行、三个真实 Feishu WebSocket 和端到端云文档链路已验证，但 provider SDK 异常分支仍主要依赖真实服务验收。

## 5. 验证记录（2026-08-21）

- `go test ./...`：Core 全部通过。
- `go test ./...`：Scan Control Plane 全部通过，含 Feishu connector/tree。
- `go test ./...`：LazyMind CLI 全部通过。
- `pnpm typecheck`：MCP/Channel/Agent Integration 范围通过。
- `pnpm exec vitest run ...message.test.ts ...TerminalConnectionQuickPanel.test.tsx`：42/42 通过。
- `pnpm build`：生产构建通过，四份 OpenAPI 均为 fresh。
- Channel Gateway Python 3.11 compileall：通过。
- 真实 Docker 栈：Core、Scan Control Plane、Channel Gateway 均 healthy；三条 Feishu WebSocket 已连接。
- 持续运行观察：宿主/容器网络短暂中断超过 120 秒时，Feishu 与 WeChat 的数据库租约按 fencing 设计失效；网络恢复后 3 个 Feishu 和 1 个 WeChat 账号均自动重连并回到 `connected/running`，未出现僵尸 owner 或双写。
- PostgreSQL：`scope_external_agent_binding_by_provider` migration 已记录，两个组合唯一索引存在；main 的 conversation archive 字段存在。
- 聚焦真实服务：`TestRealConnectorCloudDocuments` 通过，真实执行 `cloud_document.list/get/search`，耗时约 7 秒。[真实服务测试](../local/lazymind-cli/real_service_test.go#L130)

全量前端测试为 220 通过、4 失败。失败均位于 main 新增的 Writer Markdown、TaskCenter 和 Skill Management 测试/测试环境，不涉及 `ch/feishu` 改动文件；生产构建和本次相关定向测试均通过。

完整 connector E2E 首次运行在创建合成知识库文档时被 `2000725` 阻断；日志确认原因为真实栈 Milvus 不可用，发生在云文档步骤之前。为隔离该无关依赖，新增了只验证 Feishu 云文档三工具的真实测试入口，并已通过。

## 6. 后续建议

1. 为 Channel Gateway 增加纯内存 fake port 测试，优先覆盖 inbox/outbox lease、Feishu action 幂等、CardKit retry 和 workspace stale-card 分支。
2. 在 CI 中把 `TestRealConnectorCloudDocuments` 作为可选真实环境 job，避免综合 E2E 被 Milvus、模型或 Workflow 任一无关依赖阻断。
3. 另开 main 修复任务处理当前 4 个前端测试失败；不要把这些修复混入 `ch/feishu`。
