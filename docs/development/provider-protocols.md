# 供应商 API 协议声明

核对日期：2026-10-26。前端 `providerProtocols.ts` 和后端 `provider_protocols.go` 显式维护 13 品牌、22 个内置服务的官方 API 格式；每项保留官方来源。协议属于具体服务，不从品牌、模型名称、当前适配器或地址后缀推断。前后端矩阵有一致性测试，目录新增服务会使覆盖测试失败。

这里只记录文档已说明的格式，不保证所有模型、参数、账号或部署版本都支持。没有记录某协议意味着未声明，不能当作厂商明确拒绝支持。提供 Gemini 模型也不等于提供 Gemini 原生 API；当前 22 项没有足够官方证据声明 Gemini 协议。

| 服务 | 官方已声明格式 | 主要官方依据 |
| --- | --- | --- |
| Doubao 标准 | Chat Completions、Responses、Anthropic Messages | [三方工具](https://docs.volcengine.com/docs/ark/integrate-third-party-tools?lang=zh)、[Responses 示例](https://docs.volcengine.com/docs/ark/quick-start?lang=en) |
| Doubao Coding Plan | Chat Completions、Anthropic Messages | [套餐入门](https://docs.volcengine.com/docs/ark/coding-plan-personal-get-started?lang=zh) |
| Doubao Agent Plan | Chat Completions、Anthropic Messages | [三方工具与套餐协议对照](https://docs.volcengine.com/docs/ark/integrate-third-party-tools?lang=zh) |
| SenseNova 经典 API | Chat Completions、经典原生 API | [兼容模式](https://console.sensecore.cn/micro/help/docs/model-as-a-service/nova/overview/compatible-mode/)、[经典接口](https://console.sensecore.cn/micro/help/docs/model-as-a-service/nova/chat/GetStarted/APIList/) |
| SenseNova Token Plan | Chat Completions、Responses、Anthropic Messages | [新平台文档](https://platform.sensenova.cn/docs) |
| Qwen 标准 | Chat Completions、Anthropic Messages、DashScope 原生 API | [按量付费 Base URL](https://help.aliyun.com/zh/model-studio/base-url) |
| Qwen Coding Plan | Chat Completions、Anthropic Messages | [套餐 FAQ，明确不支持 Responses](https://help.aliyun.com/zh/model-studio/coding-plan-faq) |
| Qwen Token Plan 个人版 | Chat Completions、Anthropic Messages | [个人版快速开始](https://help.aliyun.com/zh/model-studio/token-plan-personal-quick-start) |
| Qwen Token Plan 团队版 | Chat Completions、Anthropic Messages | [团队版快速开始](https://help.aliyun.com/zh/model-studio/token-plan-team-quickstart) |
| GLM 标准 | Chat Completions、智谱原生 API | [OpenAI 兼容](https://docs.bigmodel.cn/cn/guide/develop/openai/introduction)、[原生入门](https://docs.bigmodel.cn/cn/guide/start/quick-start) |
| GLM Coding Plan | Chat Completions、Responses、Anthropic Messages | [编程端点三行对照](https://docs.bigmodel.cn/cn/coding-plan/tool/others) |
| Kimi 开放平台 | Chat Completions、Responses、Anthropic Messages | [调用方式](https://platform.kimi.com/docs/get-api-key) |
| Kimi Code | Chat Completions、Responses、Anthropic Messages | [模型配置](https://www.kimi.com/code/docs/en/kimi-code/models) |
| MiniMax 标准 | Chat Completions、Responses、Anthropic Messages、旧版原生接口 | [OpenAI](https://platform.minimax.io/docs/api-reference/text-openai-api)、[Responses](https://platform.minimax.io/docs/api-reference/responses-create)、[Anthropic](https://platform.minimax.io/docs/api-reference/text-anthropic-api)、[旧接口，已弃用](https://platform.minimax.io/docs/api-reference/text-post) |
| MiniMax M Plan | Chat Completions、Anthropic Messages | [套餐工具接入](https://platform.minimax.io/docs/m-plan/other-tools) |
| Claude | Anthropic Messages、Chat Completions 兼容层 | [原生 API](https://platform.claude.com/docs/en/api/overview)、[兼容层及字段差异](https://platform.claude.com/docs/en/api/openai-sdk) |
| DeepSeek | Chat Completions、Responses、Anthropic Messages | [入门](https://api-docs.deepseek.com/)、[Responses](https://api-docs.deepseek.com/guides/responses_api/)、[Anthropic](https://api-docs.deepseek.com/guides/anthropic_api/) |
| OpenAI | 原生 Chat Completions、Responses | [API 参考](https://developers.openai.com/api/reference/overview) |
| OpenRouter | Chat Completions、Responses、Anthropic Messages | [API 格式](https://openrouter.ai/docs/api/reference/overview)、[三种可用接口](https://openrouter.ai/tool/web-search/) |
| SiliconFlow | Chat Completions、Anthropic Messages | [平台简介](https://docs.siliconflow.cn/docs/userguide/introduction) |
| vLLM | Chat Completions、Responses、Anthropic Messages、自有接口 | [在线服务清单](https://docs.vllm.ai/en/stable/serving/online_serving/) |
| LMDeploy | Chat Completions、Anthropic Messages | [OpenAI 服务](https://lmdeploy.readthedocs.io/en/latest/llm/api_server.html)、[Anthropic 服务](https://lmdeploy.readthedocs.io/en/latest/llm/api_server_anthropic.html) |

SenseNova 新平台使用客户端渲染。此次核对读取 `/docs` 返回的公开脚本中的中英文文档正文：`api-compatibility` 声明 `/v1/messages` 及 `Authorization: Bearer`；`api-responses` 声明 `/v1/responses`、适用模型及无状态限制。不能因当前 Claude 适配器使用 `x-api-key` 而把该供应商的 Anthropic 官方声明删除。

## 声明与运行时的边界

前端 `getProviderProtocols(identity, language)` 只生成详情展示：`status`、`protocols`、`note`、`sources`、`verifiedAt`。自定义服务即使撞名内置服务，也返回 `unknown` 和空协议、空来源。未知服务/未知子服务也不继承品牌或当前适配器的声明。

后端分别计算：

- `providerDeclaresProtocol`：纯官方声明查表，不读取 `ProtocolName` 或 `BaseURL`；自定义服务不继承声明。
- `runtimeSupportsRequestProtocol`：当前 OpenAI/Anthropic 适配能力；SenseNova Token Plan 的 Messages Bearer 鉴权缺口独立保留。
- `serviceSupportsRequestProtocol`：内置服务需同时满足官方声明与运行时；自定义服务由用户配置，但仍受运行时限制。未知内置服务不能默认获得所有协议。

请求配置响应保留 `supported` 作为可配置交集，并添加 `provider_supported?: boolean`（自定义省略）及 `runtime_supported: boolean`。`provider_supported=false` 是未声明，不能写成供应商确认不支持。Responses、Gemini、自有接口仍没有因此增加任何实际路由，模型类型校验保持现状。

创建自定义供应商现有下拉选择的是内置模板 `source_provider_id`，用于决定现有适配器/模型配置，并非官网协议能力选择。这次保留此流程。官网声明只能用于介绍及限制内置服务已实现协议的可选性，不能据此启用未实现适配器。

## 验证

前端测试核对 YAML 目录完整覆盖、22 项精确矩阵、13 品牌、自定义隔离、未知身份、重命名及适配器改变不影响声明。后端测试比对目录和前端声明，分别覆盖已声明但未实现、未声明但适配器存在、自定义兼容和未知内置隔离。
