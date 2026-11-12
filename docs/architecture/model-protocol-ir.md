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
- `ResponseIR`：Responses 输出转换为统一消息、`ModelFinish` 和用量。底层模型调用器继续通过 `ModelCallError` 表达截断、拒绝、失败等非正常结束。

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
普通文本和工具参数仍取当前历史值，不能因保留原响应而恢复已压缩的旧正文。
这些私有属性不会作为 Completions 消息字段发送，也不进入正文 token 估算。

缺失、空值或内容块形式的 `content` 经 `MessageIR` 转为稳定的文本访问接口，工具循环不再直接假定供应商必然提供字符串字段。
Responses 截断和拒绝状态优先于工具调用存在性；截断的工具参数不得触发工具执行。
供应商返回空 Responses 输出时使用非成功终止状态，而不是假装完成。

本地 `ContextCapacityError` 和 `UnsupportedModelInput` 穿过高级请求包装层时保留原类别。
供应商异常仍按现有规则去除敏感正文和凭据。

## 新协议适配

本次接入 Completions 和 Responses；不新增 Gemini 或 Anthropic 的产品配置入口。
新增原生协议应在供应商边界编码 IR，并将输出转换到消息、工具调用、统一终止状态和用量接口。
不要在工作流、token 估算器或每个工具中添加协议字段判断。

适配器必须处理系统指令位置、工具结果关联、媒体 MIME/引用、流式与非流式对等，以及供应商私有续接信息。
未知协议不得被猜测成已支持协议；`RequestIR.to_payload` 对未实现协议显式报错。
跨协议切换时不可把签名、加密推理等不透明数据发送给另一个协议。

## 生图能力

图像生成使用独立的 Images API 和既有编码文件结果，不经聊天 Responses 路由。
OpenAI 原生 Base64 图片由适配器校验解码；硅基原生 `images` URL 被下载为本地图片，并兼容已有 `data` 响应。
硅基调用使用选中的 API 根地址，传递 `image_size`/`batch_size`，框架的 `priority`、`stream_output` 不进入供应商请求。
生成文件由工具层迁移到上传目录，返回静态资源链接；下载图片时不携带供应商 Authorization。

协议参考：[OpenAI Responses 迁移](https://developers.openai.com/api/docs/guides/migrate-to-responses)、
[OpenAI Images API](https://developers.openai.com/api/reference/resources/images/methods/generate)、
[SiliconFlow Images API](https://api-docs.siliconflow.cn/docs/api/images-generations-post)。
