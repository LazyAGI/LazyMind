# 外部 Agent 能力开放：支持范围与 PRD 差距

更新时间：2026-09-15。审查对象：`cst/open_tools`，HEAD `b9f19fdc` 加当前恢复的未提交外部能力代码。

**最新产品规则变更（用户确认）：模型与工具均默认开放。** 授权无记录时按允许处理，有显式 enabled=false 时拒绝；所有可用性、归属、启用状态和执行上下文检查仍保留。配置连接及 Key、验证通过的兼容模型自动列出并开启；新增可用能力自动遵循该默认值，不覆盖已有关闭设置。本文后续关于“全部能力默认关闭”的原始 PRD/验收记录属于变更前基线。

## 2026-09-15 后续实现及实测更新

以下更新覆盖本文原始审查中“内置仅文生图”的结论；后面的审查内容保留作为当时基线，不代表这些新增工作尚未完成。

- 外部内置工具清单现在自动读取算法侧 `DEFAULT_TOOLS`，当前 23 个工具组；工具 schema 和执行复用 `ToolManager`。无需在 Go 和 Python 两侧分别维护逐工具调用白名单。
- 现有页面支持按名称/说明筛选、展示工具说明与不可用原因。新出现的工具不自动授权，仍由用户给指定 Agent 勾选。
- 无会话依赖的注册工具可统一执行；涉及工作区、文件授权、账号上下文或交互确认的工具仍列出，但在上下文未接通前禁用。不是“23 个工具全部可执行”。Skill 继续使用已有独立读取入口。
- Core 每次加载当前用户已验证的搜索配置及禁用列表，算法侧使用独立请求会话，避免跨用户凭证串用。目录和执行接口新增内部 Token 校验。
- 通过用户 Chrome 的授权页面开启 `web_search`；使用 Codex 已保存的 LazyMind MCP 配置建立全新 stdio MCP 测试连接，`TavilySearch_search` 返回 3 条结果，`image_generator` 成功生成 1024×1024 图片。没有直接请求供应商 API。
- 在 Chrome 取消搜索授权后，新的 MCP 调用返回 `PERMISSION_DENIED`；测试后已恢复授权。成功与拒绝调用均可在外部调用记录检查。
- 旧 Codex 对话的原生 MCP 连接在 local-down 后返回 `Transport closed`，本次成功实测来自新建测试连接，不能宣称旧连接已恢复。
- 新增 Python 回归测试 6 项通过，外部能力/能力网关 Go 测试通过，助理页 25 项测试通过。服务以 `LAZYMIND_DYNAMIC_PROMPT_MODULES=false make -o skills-materialize local-up` 启动（Skill 已生成，跳过重复 Docker 打包），地址 `http://127.0.0.1:8090/`。

浏览器端验收另外修复了生成图片预览路径：`/static-files/` 转为 `/api/core/static-files/`，避免前端 fallback 返回 HTML。增加路径回归测试后，助理页共 26 项通过。

主要新增代码：`algorithm/lazymind/chat/service/external_tools.py`；网关修改位于 `backend/core/externalcapability/service.go`。原始审查列出的可信 Agent 身份绑定、可靠审计写入等其他缺口，不能因这次工具注册改造而视为解决。

本文按当前工作区实现进行静态核对，不代表线上已发布，也不代表六款客户端均已端到端验收。本次只补充开发文档，不修改业务代码、不暂存、不提交。本文放在项目现有开发文档目录 `docs/development`。

## 1. 结论

目前实现的是：外部 Agent 通过 MCP 调用 LazyMind 的能力网关，由 LazyMind 代理执行聊天模型、用户配置的 MCP 工具，以及内置生图工具。授权和调用结果展示已有基础实现，但尚不能判定完全满足 PRD。

需要区分两个方向：

- **外部 Agent → LazyMind**：本文范围。比如在 Codex 对话里调用 `model.chat`，或者用 `tool.call` 让 LazyMind 生图。
- **LazyMind → 外部 Agent 执行任务**：助理页的执行器能力，是另外一条链路，不等同于本 PRD 的模型与工具开放。

目前的 `model.chat` 是一个 MCP 工具，不是给 Codex 等客户端提供完整的模型供应商协议。它不会自动替换客户端自身的主模型。

## 2. 支持哪些外部 Agent

| 产品 | 授权标识 | 当前代码接入范围 |
| --- | --- | --- |
| Codex | `codex` | 助理页入口、连接适配及 MCP 桥接 |
| Cursor | `cursor` | 助理页入口、MCP 客户端配置适配 |
| WorkBuddy | `workbuddy` | 助理页入口、MCP 客户端配置适配 |
| 小浣熊 / Raccoon | `raccoon` | 助理页入口、MCP 客户端配置适配 |
| TRAE Work | `traework` | 助理页入口、MCP 客户端配置适配 |
| DeepSeek Harness | `deepseek-harness` | 助理页入口、MCP 适配；检查 web profile 与 MCP client 依赖 |

六个名称都在服务端白名单中，也有客户端接入代码；这只证明实现覆盖，不证明用户当前安装的版本均已实测兼容。当前不能把任意新的 Agent 名称直接作为授权对象，需要扩展白名单及对应接入逻辑。

依据：`backend/core/externalcapability/service.go` 的 `supportedAgents`、`NormalizeAgent`；`frontend/src/modules/agentIntegration/AgentIntegrationPage.tsx`；`local/lazymind-cli/internal/adapters/`。

## 3. 到底能调用什么

### 3.1 新增的统一授权执行入口

| MCP 方法 | 调用内容 | 当前边界 |
| --- | --- | --- |
| `model.list` | 返回当前用户对该 Agent 已授权且可用的模型 ID、名称、类型等 | 不返回原始 Key 或供应商地址；仅开放 `llm` / `vlm` 类型 |
| `model.chat` | 使用 `model_id`、`messages` 调用模型，返回文本、结束原因及 token 用量 | 上游固定走 OpenAI 风格 `/chat/completions`，非流式；可传 temperature、max_tokens |
| `tool.list` | 返回已授权可用工具的 ID、名称、说明及输入 schema | 来自用户 MCP 工具及已接入的内置工具适配器 |
| `tool.call` | 用 `tool_id` 和 `arguments` 服务端执行工具并返回 JSON 结果 | 每次重新检查授权、归属和数据库可用状态；结果上限 2 MiB |

模型输入的 `content` 是字符串。即使 `vlm` 类型能进入清单，该入口目前也没有图片内容数组，因此不能视为完整的视觉模型调用支持。也未实现此入口下的 embedding、rerank、语音、视频、流式输出、Responses API 或模型原生 tool-calling 参数透传。上述扩展不应全部自动认定为本期必做，但“所有已配置模型均支持”不符合当前实现。

依据：`backend/core/capability/types.go` 的 `ExternalModelMessage`、`InvokeExternalModelInput`；`backend/core/externalcapability/service.go` 的 `InvokeExternalModel`、`modelAvailability`。

### 3.2 MCP 工具：能加的不止图片

服务端代理用户已经接入 LazyMind 的 MCP 服务。工具名称不写死；只要该服务能被当前 MCP 客户端发现、调用，并满足以下条件，就可以作为授权候选：

1. 属于当前用户，服务和工具未删除。
2. 服务已验证、已启用。
3. 工具在该服务的允许工具列表中。
4. 用户另外给对应外部 Agent 开启了该工具授权。

因此，搜索、网页读取、数据库查询、代码仓库、文件操作、企业业务系统等都可以通过对应 MCP 服务提供。这里描述的是可接入类别，**不是 LazyMind 已预装这些服务或当前账号已配置它们**。实际能调用哪些，取决于用户的 MCP 服务配置和 `tool.list` 的返回。

执行端通过 HTTP JSON-RPC，并有 SSE endpoint 解析分支。不要把外部 Agent 到本地桥接器的 stdio 通道，误认为这里已经支持直接启动任意上游 stdio MCP 程序。

依据：`backend/core/externalcapability/service.go` 的 `tools`、`toolAvailability`；`backend/core/mcp/client.go` 的 `CallAuthorizedTool`。

### 3.3 内置工具：当前只有生图适配

`externalBuiltinTools` 当前只登记 `image_generator`，外部 ID 为 `builtin:image_generator`。参数为 `prompt`、`image_size`、`batch_size`（1–4），通过 LazyMind 后端调用算法侧执行。

需要用户选择相应模型、连接验证通过，且未禁用该工具，再给外部 Agent 授权。仅配置了模型，不等于自动对外开放。

没有发现把全部内部聊天工具自动转换为外部工具的注册层。浏览器点击、终端、文件编辑等内部能力，不能仅因 LazyMind 内部可用，就认为它们已经出现在这个外部授权清单中；需要单独适配，或由用户接入的 MCP 服务提供。

### 3.4 已有 Skill、知识库等接口是另一组能力

当前 Core MCP 服务还注册了：

| 方法 | 含义 |
| --- | --- |
| `skill.list`、`skill.get` | 枚举已启用、已提交的 Skill，并读取 Skill 内容（可包含 SKILL.md） |
| `knowledge.list` | 枚举用户可访问的知识库 |
| `knowledge.document.list`、`knowledge.document.get` | 枚举、读取可访问文档 |
| `knowledge.search` | 返回知识检索命中，不直接生成回答 |
| `cloud_document.list`、`cloud_document.get`、`cloud_document.search` | 列举已连接飞书账号、在线浏览文件/文件夹、搜索标题；不是飞书文档写入接口 |
| `vocabulary.wordbook.list`、`vocabulary.word.list` | 词本及词条读取 |

Skill 是可读取的指令内容，不是 `skill.execute`。外部 Agent 读取后如何执行、是否具备所需工具，是另外的问题。

这些已有接口沿用原本的用户身份、权限及资源访问控制，不走新增的 `external_capability_grants` 逐项授权，也不进入新增的模型/工具调用记录。不能把新授权页宣传为整个 MCP 服务所有能力的统一开关。词汇方法已在 Core 注册；CLI 桥接的必需方法列表没有将其列为必需项，应区分服务端注册与客户端探测口径。

依据：`backend/core/capability/mcp/server.go`、`backend/core/capability/service.go`、`local/lazymind-cli/internal/mcpbridge/bridge.go`。

## 4. 使用方式与结果展示

1. 在模型供应商或 MCP 设置中配置、验证并开启目标能力；MCP 还需发现工具并设置允许工具列表。
2. 在「设置 → 助理」选择目标外部 Agent，逐项开启外部模型/工具授权。连接 Agent 与授权能力是两个步骤。
3. 按该 Agent 的连接入口完成 MCP 配置，确保桥接使用已登录的 LazyMind 用户身份及正确 Agent 标识。
4. 在外部 Agent 中先调用 `model.list` / `tool.list`，使用返回的 ID 发起调用，不用供应商 Key，也不要猜 ID。
5. 回到 LazyMind 的外部能力调用记录查看状态、次数、用量与结果预览。

示例是 MCP 方法的参数，不是公开免认证 HTTP 接口：

```json
{"model_id":"<model.list 返回的 ID>","messages":[{"role":"user","content":"请简要介绍自己"}]}
```

生图先确认 `tool.list` 返回 `builtin:image_generator`，再调用 `tool.call`：

```json
{"tool_id":"builtin:image_generator","arguments":{"prompt":"一只在草地上玩耍的小狗","image_size":"1024x1024","batch_size":1}}
```

审计保存用户 ID、Agent 标识、调用 ID、能力 ID/名称、开始/完成时间、状态、错误码及用量。支持按 Agent/能力汇总次数；模型用量来自上游 token usage，工具目前仅统计结果字节数，不能解释成实际费用或生图 token 成本。

结果预览上限 16 KiB，并限制字符串、数组和对象大小；会脱敏常见敏感字段及部分字符串凭证模式，不保存请求正文。页面可显示 JSON/文本，以及符合本地静态文件路径规则的 `image_url`；不是任意工具结果都能可视化，也不是完整结果永久归档。历史明细最多返回最近 100 条（默认 50），汇总基于全部匹配记录；尚无明细游标分页。

依据：`backend/core/externalcapability/audit_result.go`、`service.go` 的 `InvocationHistory`；`frontend/src/modules/agentIntegration/ExternalCapabilityAccess.tsx`。

## 5. 逐项对照 PRD

“已实现”指当前代码存在相应路径，不替代真实客户端与故障场景验收。

| PRD 条目 | 判断 | 现状与差距 |
| --- | --- | --- |
| 1. 仅开放配置、验证、启用的能力 | 部分实现 | MCP 校验启用、验证、允许列表；模型校验归属、删除、分组验证和地址，但未独立校验模型启用状态，验证也是分组级，不是逐模型在线健康检查；内置仅生图 |
| 2. 统一发现与调用入口 | 已有基础实现，待端测 | 四个 MCP 方法及六类 Agent 适配；并非完整模型供应商协议，六款客户端仍需逐一验收 |
| 3. 用户选择、默认关闭、未授权禁止 | 部分实现 | 模型/工具按用户＋Agent 名称＋能力显式授权；Agent 名称来自请求头，未绑定认证凭证；已有 Skill 等不纳入这套授权 |
| 4. 原始凭证由服务端管理 | 部分实现 | 模型 Key/MCP headers 由服务端注入，不在能力清单返回；审计有脱敏，但成功结果返回给 Agent 前没有同等脱敏，任意工具回显凭证的场景未闭环 |
| 5. 执行及明确失败原因/建议 | 部分实现 | 有权限、连接、大小等错误及设置指引；MCP 业务错误 `isError` 未转为失败，提示较笼统 |
| 6. 停用、删除、失效、撤权立即影响新调用 | 部分实现 | 每次查询数据库状态与授权，已有撤权/禁用路径；实际断线不会维护独立失效状态，后续仍会接受并尝试请求 |
| 7. 身份、能力、时间、状态、用量审计 | 部分实现 | 字段、汇总及预览存在；身份可信度、审计丢失、业务错误误计成功和工具用量口径仍有缺口 |
| 8. 不导出 Key、不全量自动共享、不改连接 | 新增执行入口基本遵守 | 新模型/工具授权默认关闭，代理失败不改原始连接；但仍需覆盖工具结果泄密测试，且不能把已有接口也视为默认关闭 |

六条验收标准的对应结论：①授权调用有实现，待六客户端端测；②不可用拦截部分实现；③页面/接口/日志全链路无凭证暴露尚不能保证；④数据库撤权会阻止后续权限检查通过的调用；⑤审计基础实现存在但可靠性不足；⑥代理失败不改原始配置有实现，错误分类还需完善。

撤权不取消已经通过校验、正在执行的请求，符合 PRD 只要求停止接受新调用的基本范围；不应承诺在途请求立即中止。

## 6. 优先待修复项（本次仅记录）

### P1：Agent 身份缺少可信绑定

`backend/core/capability/mcp/server.go:53` 从 `X-LazyMind-Agent-Provider` 获取 Agent；认证 Token 提供用户与权限，但此处未把 Agent 声明绑定至 Token。持有同一用户有效 MCP 访问凭证的调用方，可以改变请求头，声明为该用户已授权的另一 Agent。

这不等于匿名用户能绕过登录，却意味着“未授权 Agent 不可调用”和审计来源不可仅依赖这个头。建议引入绑定用户、Agent、连接实例、有效期的专用访问凭证，并测试修改请求头不能扩大权限。

### P1：成功输出与审计的凭证保护不一致

`InvokeExternalTool` 把上游 JSON 直接作为结果返回；`InvokeExternalModel` 直接返回内容。`auditResultPreview` 只负责审计预览。若上游工具回显 headers、Token 或含密钥的 URL，可能在返回路径暴露；审计的模式匹配也不能保证识别任意无标签秘密。

需要统一明确成功输出的敏感信息策略，增加嵌套字段、文本、URL、工具错误内容等回归测试。现有“配置字段没有导出”不能直接推导出“任何接口及日志绝不泄密”。

### P1：MCP 业务失败可能被记为成功

`backend/core/mcp/client.go` 的 `doRPC` 检查 JSON-RPC 顶层 error，但 `CallAuthorizedTool` 没有检查工具结果中的 `isError: true`。外部执行器只要 JSON 可解析就正常返回，`finishAudit` 因没有 Go error 而记录 succeeded。

应把 MCP 工具业务错误映射为明确失败，并统一调用结果、页面状态、失败次数和处理建议。

### P1：审计写失败会静默放行

`startAudit` 创建记录失败时返回 nil，调用仍继续；`finishAudit` 忽略更新错误。可能发生有调用无记录，或记录一直 running。建议补充可靠写入/补偿、告警与明确失败策略，测试数据库不可用及进程中断。

### P2：连接失效与模型状态需要补齐语义

可用性依赖数据库已有验证标记；网络断开或凭证过期后仅当前请求失败，没有独立健康状态使后续请求快速拒绝。分组验证成功也不保证其中每个模型都可调用。

应将连接健康状态与原始连接配置分离，在不破坏用户配置的前提下处理失效、重新验证和恢复。还需核对产品中的模型启用/停用含义，并接入该判断。

内置生图的候选查询仅关联模型及分组，未像普通模型清单那样显式关联供应商删除状态；应补供应商删除场景测试，不能只靠普通模型测试推定生图一致。

### P2：能力范围与验收口径需明确

当前只有生图内置适配、文本模型代理，不能称为全部内置模型/工具开放。Skill 等已有读取接口也不受新增逐项授权和调用统计覆盖。

建议明确本期清单；若 PRD 的“工具”包含这些已有 MCP 方法，需统一授权和审计；若不包含，应在产品界面和文档说明边界。多模态、更多内置工具和完整模型供应商协议属于待确认范围，不能擅自扩展本期要求。

## 7. 建议验收清单

- 六款 Agent 各做一次：连接、发现、模型成功调用、MCP 工具成功调用、撤权后失败，并核对 LazyMind 记录。
- 默认零授权、跨用户能力 ID、伪造 Agent 请求头、禁用/删除/未验证的负向测试。
- 验证后断网、凭证过期、模型不存在、重新验证恢复；确认新请求行为。
- HTTP 失败、JSON-RPC error、MCP `isError`、超时、超大响应、无效 JSON，核对错误提示与成功/失败统计。
- 模型删除、分组删除、供应商删除、切换生图模型、禁用生图工具，分别检查清单和执行路径。
- 成功/失败结果中嵌入模拟凭证，检查外部响应、LazyMind 页面、数据库预览及服务日志；勿使用真实 Key 做泄漏测试。
- 审计创建/更新失败、进程中断、缺失 token usage，确认日志可靠性及用量展示不误导。
- Skill/知识库/云文档等旧入口与新增授权的关系，按最终确认范围补测。

现有 `backend/core/externalcapability/service_test.go` 包含模型授权代理、MCP 状态检查、生图代理、失败不改连接及审计脱敏/大小限制测试。它们是已有单测覆盖，不等于上述场景全部通过；本次文档审查未启动服务或重新执行端到端测试。

## 8. CC Switch 与文档关系

按已确认范围，本期不强制使用 CC Switch。当前实现走项目自身能力服务和 MCP 桥接，不把“未复用 CC Switch”列为 PRD 阻塞项；未来若引入其代码，再核查许可证和引用要求。

已有使用说明见 [外部能力使用文档](../external-agent-capabilities.CN.md)，架构说明见 [能力网关设计](../architecture/external-agent-capability-gateway.md)。若其概括性表述与实际范围有差异，应以本文列出的代码边界为准，后续再统一修订使用说明。
