import { getSupplierBrandKey, resolveSupplierService, type SupplierServiceIdentity } from "./supplierServices";

/** Editorial API formats, deliberately unrelated to runtime adapter identifiers. */
export type ProviderProtocolId = "openai-chat" | "openai-responses" | "anthropic" | "gemini" | "native";
type Text = readonly [zh: string, en: string];
interface ProtocolDeclaration {
  readonly protocols: readonly ProviderProtocolId[];
  readonly nativeLabel?: Text;
  readonly note: Text;
  readonly sources: readonly (readonly [label: Text, url: string])[];
}

export interface ProviderProtocols {
  status: "documented" | "unknown";
  protocols: { id: ProviderProtocolId; label: string }[];
  note: string;
  sources: { label: string; url: string }[];
  verifiedAt?: string;
}

const labels: Record<ProviderProtocolId, Text> = {
  "openai-chat": ["OpenAI 兼容 · Chat Completions", "OpenAI compatible · Chat Completions"],
  "openai-responses": ["OpenAI 兼容 · Responses", "OpenAI compatible · Responses"],
  anthropic: ["Anthropic Messages", "Anthropic Messages"],
  gemini: ["Gemini API", "Gemini API"],
  native: ["供应商原生 API", "Provider native API"],
};

/**
 * Every catalog service has a handwritten declaration and official evidence.
 * A documented format does not imply every model, parameter, or account supports
 * it, nor that LazyMind implements that route. Never feed this map into runtime
 * routing, model-type validation, or request-configuration availability.
 */
export const providerProtocolDeclarations: Readonly<Record<string, ProtocolDeclaration>> = {
  "Doubao:standard": {
    protocols: ["openai-chat", "openai-responses", "anthropic"],
    note: ["方舟标准 API 提供三种接口格式；具体模型与参数支持以对应接口文档为准。", "Ark Standard API documents three API formats; model and parameter support varies by endpoint."],
    sources: [
      [["方舟第三方工具协议", "Ark third-party API protocols"], "https://docs.volcengine.com/docs/ark/integrate-third-party-tools?lang=zh"],
      [["方舟 Responses 示例", "Ark Responses examples"], "https://docs.volcengine.com/docs/ark/quick-start?lang=en"],
    ],
  },
  "Doubao:coding": {
    protocols: ["openai-chat", "anthropic"],
    note: ["使用 Coding Plan 专属协议地址；不从标准 API 的接口能力推断套餐能力。", "Use the Coding Plan-specific endpoints; Standard API formats are not inherited by this plan."],
    sources: [[["Coding Plan 快速开始", "Coding Plan quick start"], "https://docs.volcengine.com/docs/ark/coding-plan-personal-get-started?lang=zh"]],
  },
  "Doubao:agent": {
    protocols: ["openai-chat", "anthropic"],
    note: ["Agent Plan 的 OpenAI 与 Anthropic 接口使用各自的套餐路径及专属 Key。", "Agent Plan's OpenAI and Anthropic interfaces use their respective plan paths and dedicated key."],
    sources: [
      [["Agent Plan 工具接入", "Agent Plan tool integration"], "https://docs.volcengine.com/docs/ark/agent-plan-personal-other-tools?lang=zh"],
      [["方舟协议对照", "Ark protocol comparison"], "https://docs.volcengine.com/docs/ark/integrate-third-party-tools?lang=zh"],
    ],
  },
  "SenseNova:standard": {
    protocols: ["openai-chat", "native"], nativeLabel: ["SenseNova 经典原生 API", "SenseNova Classic native API"],
    note: ["经典服务提供原生接口与 OpenAI Chat Completions 兼容模式；与 Token Plan 的接口声明分开维护。", "The Classic service has native APIs and OpenAI Chat Completions compatibility, maintained separately from Token Plan."],
    sources: [
      [["经典 API 兼容模式", "Classic API compatibility"], "https://console.sensecore.cn/micro/help/docs/model-as-a-service/nova/overview/compatible-mode/"],
      [["经典 API 列表", "Classic API reference"], "https://console.sensecore.cn/micro/help/docs/model-as-a-service/nova/chat/GetStarted/APIList/"],
    ],
  },
  "SenseNova:token": {
    protocols: ["openai-chat", "openai-responses", "anthropic"],
    note: ["新平台文档列出 Chat Completions、Responses 与 Messages；Messages 使用 Bearer 鉴权，Responses 有模型与无状态能力限制。", "Platform docs list Chat Completions, Responses and Messages. Messages uses Bearer authentication; Responses has model and stateless-operation limits."],
    sources: [
      [["Token Plan API 文档", "Token Plan API documentation"], "https://platform.sensenova.cn/docs"],
      [["Anthropic 兼容接口", "Anthropic compatibility"], "https://platform.sensenova.cn/docs#api-compatibility"],
      [["Responses 兼容接口", "Responses compatibility"], "https://platform.sensenova.cn/docs#api-responses"],
    ],
  },
  "Qwen:standard": {
    protocols: ["openai-chat", "anthropic", "native"], nativeLabel: ["DashScope 原生 API", "DashScope native API"],
    note: ["百炼按量付费服务提供 OpenAI、Anthropic 兼容路径及 DashScope 原生 API；模型、地域和功能需分别核对。", "Pay-as-you-go Model Studio offers OpenAI and Anthropic compatibility plus native DashScope APIs; check model, region and feature support separately."],
    sources: [[["百炼 Base URL 与协议", "Model Studio Base URLs and protocols"], "https://help.aliyun.com/zh/model-studio/base-url"]],
  },
  "Qwen:coding": {
    protocols: ["openai-chat", "anthropic"],
    note: ["Coding Plan 明确支持 Chat Completions 和 Messages，不支持 Responses；使用套餐专属地址与 Key。", "Coding Plan explicitly supports Chat Completions and Messages, but not Responses. Use its dedicated endpoints and key."],
    sources: [[["Coding Plan 协议说明", "Coding Plan protocol FAQ"], "https://help.aliyun.com/zh/model-studio/coding-plan-faq"]],
  },
  "Qwen:token": {
    protocols: ["openai-chat", "anthropic"],
    note: ["Token Plan 个人版列出 OpenAI 与 Anthropic 兼容地址；个人版、团队版及其他产品的凭据不可混用。", "Token Plan Personal documents OpenAI and Anthropic-compatible endpoints; keys are isolated from Team and other products."],
    sources: [[["Token Plan 个人版协议配置", "Token Plan Personal protocol setup"], "https://help.aliyun.com/zh/model-studio/token-plan-personal-quick-start"]],
  },
  "Qwen:token_team": {
    protocols: ["openai-chat", "anthropic"],
    note: ["Token Plan 团队版列出 OpenAI 与 Anthropic 兼容地址；需要团队席位及团队版专属 Key。", "Token Plan Team documents OpenAI and Anthropic-compatible endpoints and requires a team seat and Team-specific key."],
    sources: [[["Token Plan 团队版协议配置", "Token Plan Team protocol setup"], "https://help.aliyun.com/zh/model-studio/token-plan-team-quickstart"]],
  },
  "GLM:standard": {
    protocols: ["openai-chat", "native"], nativeLabel: ["智谱原生 API", "BigModel native API"],
    note: ["标准服务提供智谱 SDK/API 与 OpenAI 兼容调用；Coding Plan 的专属协议端点不自动适用于标准服务。", "Standard service offers BigModel SDK/APIs and OpenAI-compatible calls; Coding Plan-specific endpoints do not automatically apply."],
    sources: [
      [["OpenAI API 兼容", "OpenAI API compatibility"], "https://docs.bigmodel.cn/cn/guide/develop/openai/introduction"],
      [["智谱原生 API 入门", "BigModel native API quick start"], "https://docs.bigmodel.cn/cn/guide/start/quick-start"],
    ],
  },
  "GLM:coding": {
    protocols: ["openai-chat", "openai-responses", "anthropic"],
    note: ["官方工具接入表分别列出 Chat Completion、Response 和 Anthropic Message 的 Base URL。", "The official integration table lists separate Base URLs for Chat Completion, Response and Anthropic Message."],
    sources: [[["GLM Coding Plan 编程端点", "GLM Coding Plan endpoints"], "https://docs.bigmodel.cn/cn/coding-plan/tool/others"]],
  },
  "Kimi:standard": {
    protocols: ["openai-chat", "openai-responses", "anthropic"],
    note: ["Kimi 开放平台当前入门文档列出 Chat Completions、Responses 与 Anthropic Messages。", "Kimi Open Platform's current quick start lists Chat Completions, Responses and Anthropic Messages."],
    sources: [[["Kimi API 调用方式", "Kimi API formats"], "https://platform.kimi.com/docs/get-api-key"]],
  },
  "Kimi:code": {
    protocols: ["openai-chat", "openai-responses", "anthropic"],
    note: ["Kimi Code 模型配置列出 OpenAI 两种格式和 Anthropic 兼容地址；订阅使用范围仍需按官方规范核对。", "Kimi Code model configuration lists both OpenAI formats and Anthropic compatibility; subscription usage scope follows the official guidelines."],
    sources: [[["Kimi Code 模型与协议配置", "Kimi Code models and protocols"], "https://www.kimi.com/code/docs/en/kimi-code/models"]],
  },
  "Minimax:standard": {
    protocols: ["openai-chat", "openai-responses", "anthropic", "native"], nativeLabel: ["MiniMax 原生 API（旧版）", "MiniMax native API (legacy)"],
    note: ["标准文档提供 OpenAI 与 Anthropic 接口；原生 chatcompletion_v2 已标记弃用，具体支持模型见接口页。", "Standard docs provide OpenAI and Anthropic interfaces; native chatcompletion_v2 is deprecated. Check each API's supported models."],
    sources: [
      [["OpenAI SDK", "OpenAI SDK"], "https://platform.minimax.io/docs/api-reference/text-openai-api"],
      [["Responses API", "Responses API"], "https://platform.minimax.io/docs/api-reference/responses-create"],
      [["Anthropic SDK", "Anthropic SDK"], "https://platform.minimax.io/docs/api-reference/text-anthropic-api"],
      [["旧版原生文本接口", "Legacy native text API"], "https://platform.minimax.io/docs/api-reference/text-post"],
    ],
  },
  "Minimax:m_plan": {
    protocols: ["openai-chat", "anthropic"],
    note: ["M Plan 工具接入明确列出 OpenAI 与 Anthropic 两种协议，使用 Subscription Key；不继承标准 API 的其他协议。", "M Plan tool setup explicitly lists OpenAI and Anthropic protocols with a Subscription Key; other Standard API formats are not inherited."],
    sources: [[["M Plan 工具协议配置", "M Plan tool protocol setup"], "https://platform.minimax.io/docs/m-plan/other-tools"]],
  },
  "Claude:standard": {
    protocols: ["anthropic", "openai-chat"],
    note: ["Anthropic Messages 为原生接口；OpenAI SDK 兼容层存在参数及能力差异。", "Anthropic Messages is the native interface; the OpenAI SDK compatibility layer has parameter and feature differences."],
    sources: [
      [["Claude API 概览", "Claude API overview"], "https://platform.claude.com/docs/en/api/overview"],
      [["OpenAI SDK 兼容性", "OpenAI SDK compatibility"], "https://platform.claude.com/docs/en/api/openai-sdk"],
    ],
  },
  "DeepSeek:standard": {
    protocols: ["openai-chat", "openai-responses", "anthropic"],
    note: ["DeepSeek 分别提供 OpenAI Chat Completions、Responses 与 Anthropic 格式说明；模型和字段限制以各页为准。", "DeepSeek documents OpenAI Chat Completions, Responses and Anthropic formats separately; model and field limits follow each API guide."],
    sources: [
      [["DeepSeek API 入门", "DeepSeek API introduction"], "https://api-docs.deepseek.com/"],
      [["Responses API", "Responses API"], "https://api-docs.deepseek.com/guides/responses_api/"],
      [["Anthropic API", "Anthropic API"], "https://api-docs.deepseek.com/guides/anthropic_api/"],
    ],
  },
  "OpenAI:standard": {
    protocols: ["openai-chat", "openai-responses"],
    note: ["OpenAI 原生提供 Chat Completions 与 Responses；其他模态和实时接口需查看对应 API 文档。", "OpenAI natively provides Chat Completions and Responses; see the relevant API docs for other modalities and realtime interfaces."],
    sources: [[["OpenAI API 参考", "OpenAI API reference"], "https://developers.openai.com/api/reference/overview"]],
  },
  "OpenRouter:standard": {
    protocols: ["openai-chat", "openai-responses", "anthropic"],
    note: ["OpenRouter 提供统一接口及格式转换；提供 Gemini 模型不等于提供 Gemini 原生协议。", "OpenRouter provides normalized interfaces and format conversion; hosting Gemini models does not establish native Gemini protocol support."],
    sources: [
      [["OpenRouter API 参考", "OpenRouter API reference"], "https://openrouter.ai/docs/api/reference/overview"],
      [["OpenRouter 可用接口格式", "OpenRouter supported API formats"], "https://openrouter.ai/tool/web-search/"],
    ],
  },
  "SiliconFlow:standard": {
    protocols: ["openai-chat", "anthropic"],
    note: ["官方平台简介明确列出 OpenAI 和 Anthropic 对话协议兼容；具体模型能力需另行核对。", "The official platform introduction documents OpenAI and Anthropic conversation compatibility; model features require separate checks."],
    sources: [[["硅基流动平台协议简介", "SiliconFlow protocol introduction"], "https://docs.siliconflow.cn/docs/userguide/introduction"]],
  },
  "vLLM:standard": {
    protocols: ["openai-chat", "openai-responses", "anthropic", "native"], nativeLabel: ["vLLM 自有接口", "vLLM-specific APIs"],
    note: ["当前服务文档列出 OpenAI、Anthropic 及自有接口；可用性取决于实际部署版本、模型、模板与解析器。", "Current serving docs list OpenAI, Anthropic and custom APIs; availability depends on the deployed version, model, template and parsers."],
    sources: [[["vLLM 在线服务接口", "vLLM online serving APIs"], "https://docs.vllm.ai/en/stable/serving/online_serving/"]],
  },
  "LMDeploy:standard": {
    protocols: ["openai-chat", "anthropic"],
    note: ["当前文档列出 OpenAI 兼容服务及轻量 Anthropic Messages 接口；工具与推理输出依赖部署版本和解析器配置。", "Current docs list an OpenAI-compatible server and a lightweight Anthropic Messages surface; tools and reasoning depend on deployment version and parser configuration."],
    sources: [
      [["LMDeploy OpenAI 接口", "LMDeploy OpenAI APIs"], "https://lmdeploy.readthedocs.io/en/latest/llm/api_server.html"],
      [["LMDeploy Anthropic 接口", "LMDeploy Anthropic APIs"], "https://lmdeploy.readthedocs.io/en/latest/llm/api_server_anthropic.html"],
    ],
  },
};

const singleServiceBrands = new Set(["Claude", "DeepSeek", "OpenAI", "OpenRouter", "SiliconFlow", "vLLM", "LMDeploy"]);

/** No name, adapter, host, or source guessing for custom/unknown services. */
export function getProviderProtocols(provider: SupplierServiceIdentity, language = "en-US"): ProviderProtocols {
  const text = (value: Text) => value[/^zh(?:[-_]|$)/i.test(language) ? 0 : 1];
  const unknown = (): ProviderProtocols => ({
    status: "unknown", protocols: [], sources: [],
    note: text(["未声明协议；请根据实际服务的官方文档核对。", "No protocol declaration; check the actual service's official documentation."]),
  });
  if (provider.isCustom === true || provider.is_custom === true || !provider.name.trim()) return unknown();
  const service = resolveSupplierService(provider);
  const brand = getSupplierBrandKey(provider);
  const explicitService = (provider.serviceKey || provider.service_key || "").trim();
  const identity = service ? `${service.brandKey}:${service.serviceKey}`
    : brand && singleServiceBrands.has(brand) && (!explicitService || explicitService === "standard") ? `${brand}:standard` : undefined;
  if (!identity || !Object.prototype.hasOwnProperty.call(providerProtocolDeclarations, identity)) return unknown();
  const declaration = providerProtocolDeclarations[identity];
  return {
    status: "documented",
    protocols: declaration.protocols.map((id) => ({ id, label: text(id === "native" && declaration.nativeLabel ? declaration.nativeLabel : labels[id]) })),
    note: text(declaration.note),
    sources: declaration.sources.map(([label, url]) => ({ label: text(label), url })),
    verifiedAt: "2026-10-26",
  };
}
