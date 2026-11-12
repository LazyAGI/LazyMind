# 工作流模型协议边界

工作流的上下文预算和工具循环依赖语义消息，不依赖供应商 HTTP 请求的字段名称。
协议相关代码位于 LazyLLM 子模块的 `module/llms/onlinemodule`；LazyMind 使用其语义表示进行上下文估算。

## 数据流

```text
历史消息、当前输入、工具定义
    → RequestIR（序列化前快照）
    → 供应商适配器 → HTTP 原生请求
    → 协议响应解析、统一终止状态 → MessageIR
    → 工作流工具循环、历史管理、最终文本
```

`base/protocol_ir.py` 定义以下结构：

- `ContentBlock`：区分文本、图片、文件、拒绝和不透明内容，媒体数据不会按正文计数。
- `ToolCallIR`：保留调用 ID、工具名称、参数，以及兼容层需要的附加属性。
- `MessageIR`：角色、内容块、工具调用及工具结果对应的调用 ID；`text` 始终返回字符串。
- `RequestIR`：语义消息与生成参数快照。`ModelRequest.payload` 仍是实际待发送请求，`ModelRequest.ir` 是独立的语义快照。
- `ResponseIR`：Completions 与 Responses 输出转换为统一消息、`ModelFinish` 和用量。底层模型调用器继续通过 `ModelCallError` 表达截断、拒绝、失败等非正常结束。

协议选项（工具定义、结构化输出、采样参数、输出 token 上限）的编码仍由供应商适配器负责。
现有消息字典通过边界转换兼容，不要求修改已保存的工作流和会话数据。

## 预算与媒体

模型调用在协议编码之前生成 `RequestIR`，请求准入只估算该语义快照。因此 Responses 把 `messages` 改为 `input`，或未来协议使用其他布局，都不改变预算入口。
直接构造的旧版 `ModelRequest` 仍可从 Completions 或 Responses 请求解析 IR。

媒体采用每块 1536 token 的近似预算，沿用现有图片预算策略；它不是供应商计费公式。
Base64、文件编码数据、加密续接信息均不按正文字符估算。
模型返回的真实用量独立记录，不能用本地估算替代。

## 多轮状态和终止

Responses 的不透明输出项通过注明协议来源的 `_lazyllm_provider_items` 保留在历史中，供同一协议的适配器回放。
完整原始输出保留消息 ID、引用、拒绝内容及顺序。角色、文本、媒体或工具参数变化时，语义摘要使原始快照失效，改用当前历史值，不能恢复已压缩的旧正文。
这些私有属性不会作为 Completions 消息字段发送，也不进入正文 token 估算。

主会话在已有 `ChatHistory.Ext` 中保存 canonical 消息序列，关联 run ID，并参与模型输出的 checkpoint/commit/rollback；失败尝试不会覆盖已提交历史。子代理工具步骤保存完整 assistant 消息，旧版扁平工具记录恢复时转换为标准调用格式。内部恢复使用这些数据，公开 SSE、任务历史与会话 DTO 不返回私有续接内容，无需数据库迁移。

缺失、空值或内容块形式的 `content` 经 `MessageIR` 转为稳定的文本访问接口，工具循环不再直接假定供应商必然提供字符串字段。
Responses 截断和拒绝状态优先于工具调用存在性；截断的工具参数不得触发工具执行。
供应商返回空 Responses 输出时使用非成功终止状态，而不是假装完成。

默认文本 formatter 在流式和非流式中返回相同的文本类型；工具 agent 使用结构化 formatter。Responses 的函数参数、推理和拒绝增量会保留为部分输出，只有完整终态可触发工具执行。坏 JSON/事件结构归为协议错误；失败响应保留已产生的输出和实际用量。取消和截止时间也闭合模型调用生命周期。

适配器显式拒绝无法等价映射的参数（例如 `n>1`、`stop`、频率惩罚），并转换旧版函数定义；不支持的内置工具必须在请求前拒绝。本适配器当前支持函数工具，生图走独立 Images API。

本地 `ContextCapacityError` 和 `UnsupportedModelInput` 穿过高级请求包装层时保留原类别。
供应商异常仍按现有规则去除敏感正文和凭据。

## 新协议适配

本次接入 Completions 和 Responses；不新增 Gemini 或 Anthropic 的产品配置入口。
新增原生协议应在供应商边界编码 IR，并将输出转换到消息、工具调用、统一终止状态和用量接口。
不要在工作流、token 估算器或每个工具中添加协议字段判断。

适配器必须处理系统指令位置、工具结果关联、媒体 MIME/引用、流式与非流式对等，以及供应商私有续接信息。
未知协议不得被猜测成已支持协议；`RequestIR.to_payload` 对未实现协议显式报错。
跨协议切换时不可把签名、加密推理等不透明数据发送给另一个协议。
未来 Gemini 的 thought signatures 与 Anthropic 的签名 thinking 必须由各自适配器声明与验证兼容范围；当前保留不透明项的能力不等于已实现这两种原生协议。

## 生图能力

图像生成使用独立的 Images API 和既有编码文件结果，不经聊天 Responses 路由。
OpenAI 原生 Base64 图片由适配器校验解码；硅基原生 `images` URL 被下载为本地图片，并兼容已有 `data` 响应。
硅基调用使用选中的 API 根地址，传递 `image_size`/`batch_size`，框架的 `priority`、`stream_output` 不进入供应商请求。
生成文件由工具层迁移到上传目录，返回静态资源链接；下载图片时不携带供应商 Authorization，且每次重定向都重新验证目标。图片编辑根据实际文件类型声明 MIME；生成数量只接受正整数。

协议参考：[OpenAI Responses 迁移](https://developers.openai.com/api/docs/guides/migrate-to-responses)、
[OpenAI Images API](https://developers.openai.com/api/reference/resources/images/methods/generate)、
[SiliconFlow Images API](https://api-docs.siliconflow.cn/docs/api/images-generations-post)。

## 协议验收清单

下表区分确定性的协议回归与真实供应商验收。HTTP mock 只能证明请求编码、响应处理及本地工具循环，不能证明凭据、配额或供应商支持情况。

| 场景 | 代码与回归入口 | 验收要求 |
| --- | --- | --- |
| 动态模型路由 | `model_config.py`、`test_responses_transport.py` | 官方 GPT-6 路由 Responses；代理端点保持配置边界；嵌入模型不进入聊天路由 |
| 角色、文本、空内容、内容块 | `test_protocol_ir.py`、子模块 `test_protocol_ir_fidelity.py` | 保留语义与角色，缺失 content 不使工作流收尾失败 |
| JPEG 附件与请求准入 | `test_protocol_ir.py` | 554 KB 图片不作为正文估算；两种协议均到达 HTTP 边界 |
| 工具调用与结果关联 | `test_protocol_ir.py` | 流式/非流式、两协议均执行真实本地工具并回传匹配 call ID |
| PPT 工作流 | `test_ppt_protocol_finalization.py` | AutoModel 与 runner 完成页面发布；必需产物缺失仍失败 |
| 多轮与恢复 | `test_engine_protocol_projection.py`、Core canonical history 测试 | 私有续接状态保存、压缩失效、回滚和公开字段过滤 |
| 流式增量与结束状态 | 子模块 `test_openai_responses.py` | 参数/推理/拒绝增量；截断工具不可执行；失败保留部分输出及用量；取消只产生一次终态 |
| 错误分类 | `test_protocol_ir.py`、`test_provider_request_advanced.py` | 本地容量/能力错误不包装为通用供应商失败；协议错误独立于网络和凭据错误 |
| Skill | `skill_listing.append_loaded_skill_invocations`、`test_skill_usage_context.py` | get_skill 历史在协议边界保留调用和正文；两协议与两种流式模式已补充 HTTP 传输回归；现有 qa-org-e Skill 真实调用返回三条摘要 |
| 图像生成 | `test_image_protocol_contract.py` | OpenAI Base64、硅基 URL、图片编辑 MIME、下载安全和资源落盘；真实供应商生成需单独验收 |

真实 OpenAI gpt-6.1-sol 已验证首轮回复、多轮代号记忆和计算工具回传（137×149=20413）。554 KB JPEG 实际上传后返回蓝色；一页 PPT HTML 已发布且工作流正常完成。生图真实环境验收和 PR 必需 CI 仍待完成，不以服务健康或上述 mock 测试代替。
Gemini/Anthropic 原生端点尚未接入，当前仅验收 IR 扩展边界；Responses 后台任务和内置工具不支持，验收目标是明确拒绝，不能标记为已支持。

验收环境说明：2026-11-12 生图尝试期间，原 PostgreSQL 进程退出并进入恢复状态，多进程处于 I/O 等待，页面断连；该尝试不能用于判定供应商或协议成功。未初始化、切换或清理数据库。
