import type { InformationText } from "./providerInformationContent";

export interface ProviderProfile {
  introduction: string;
  websiteUrl: string;
  highlights: string[];
}

interface EditorialProfile {
  introduction: InformationText;
  websiteUrl: string;
  highlights: readonly InformationText[];
}

/**
 * Product/company descriptions checked against official sources on 2026-10-23.
 * Websites are editorial homepage links, never connection defaults or API URLs.
 */
const brandProfiles: Readonly<Record<string, EditorialProfile>> = {
  // https://www.anthropic.com/company; https://claude.com/platform/api
  Claude: {
    introduction: [
      "Anthropic 是研发 Claude 系列模型的人工智能研究与产品公司，重视模型的可靠性、可解释性与可控性。Claude 开发者平台提供语言、推理、编程及工具型智能体所需的模型服务。",
      "Anthropic is an AI research and product company behind the Claude model family, with a focus on reliable, interpretable, and steerable systems. Its Claude platform serves models for language, reasoning, coding, and tool-using agents.",
    ],
    websiteUrl: "https://www.anthropic.com/",
    highlights: [
      ["Claude 模型家族", "Claude model family"],
      ["编程、推理与智能体场景", "Coding, reasoning, and agent use cases"],
      ["AI 安全与可解释性研究", "AI safety and interpretability research"],
    ],
  },
  // https://www.deepseek.com/; https://api-docs.deepseek.com/
  DeepSeek: {
    introduction: [
      "DeepSeek（深度求索）研发语言与推理模型，并持续发布模型研究成果。其产品覆盖网页对话、应用和开发者 API 平台，可用于文本处理、编程及智能体应用。",
      "DeepSeek develops language and reasoning models and publishes model research. Its products include web chat, apps, and a developer API platform for text processing, coding, and agent applications.",
    ],
    websiteUrl: "https://www.deepseek.com/",
    highlights: [
      ["自研语言与推理模型", "In-house language and reasoning models"],
      ["对话、编程与智能体应用", "Chat, coding, and agent applications"],
      ["模型研究与开放平台", "Model research and developer platform"],
    ],
  },
  // https://docs.volcengine.com/docs/ark?lang=zh
  Doubao: {
    introduction: [
      "火山方舟是火山引擎提供的大模型服务平台，汇集豆包及其他主流模型。平台覆盖模型推理、评测和精调，标准 API 面向应用开发提供模型服务。",
      "Volcengine Ark is a model-service platform offering Doubao and other major model families. It covers model inference, evaluation, and fine-tuning, with a standard API for application development.",
    ],
    websiteUrl: "https://www.volcengine.com/product/ark",
    highlights: [
      ["豆包与多家主流模型", "Doubao and other major model families"],
      ["推理、评测与精调", "Inference, evaluation, and fine-tuning"],
      ["面向应用开发的标准 API", "Standard API for application development"],
    ],
  },
  // https://www.sensetime.com/cn/faq; https://www.sensenova.cn/en/models
  SenseNova: {
    introduction: [
      "商汤日日新 SenseNova 是商汤的大模型与原生应用体系，覆盖多模态理解、内容生成和复杂任务执行。经典 API 通过商汤大装置提供模型服务，与新 SenseNova 平台的 Token Plan 分开管理。",
      "SenseNova is SenseTime's family of foundation models and native AI applications for multimodal understanding, content creation, and complex tasks. Its Classic API provides model services through SenseCore, separately from Token Plan on the newer SenseNova platform.",
    ],
    websiteUrl: "https://www.sensenova.cn/",
    highlights: [
      ["商汤自研模型与应用体系", "SenseTime models and native applications"],
      ["多模态理解与内容生成", "Multimodal understanding and generation"],
      ["大装置经典 API 服务", "Classic API services through SenseCore"],
    ],
  },
  // https://help.aliyun.com/zh/model-studio/what-is-model-studio
  Qwen: {
    introduction: [
      "阿里云百炼是以千问及其他模型构建 AI 应用的大模型服务平台。它在云端提供模型推理、评测、调优与应用开发服务，覆盖开发者和企业的模型应用需求。",
      "Alibaba Cloud Model Studio is a platform for building AI applications with Qwen and other models. It brings model inference, evaluation, fine-tuning, and application development into one cloud service.",
    ],
    websiteUrl: "https://www.aliyun.com/product/bailian",
    highlights: [
      ["千问与第三方模型目录", "Qwen and third-party model catalog"],
      ["模型调优、评测与部署", "Model fine-tuning, evaluation, and deployment"],
      ["智能体与应用开发平台", "Agent and application development platform"],
    ],
  },
  // https://docs.bigmodel.cn/cn/guide/start/model-overview
  GLM: {
    introduction: [
      "智谱是 GLM 系列模型的研发方，通过 BigModel 开放平台向开发者和企业提供模型服务。平台覆盖文本推理、编程、多模态理解与内容生成，标准 API 用于应用开发。",
      "Zhipu develops the GLM model family and provides model services to developers and businesses through its BigModel platform. The platform covers text reasoning, coding, multimodal understanding, and content generation, with a standard API for applications.",
    ],
    websiteUrl: "https://www.bigmodel.cn/",
    highlights: [
      ["GLM 模型家族", "GLM model family"],
      ["文本、视觉与语音等模型", "Text, vision, speech, and other models"],
      ["开发者与企业开放平台", "Developer and enterprise model platform"],
    ],
  },
  // https://www.moonshot.cn/; https://platform.kimi.com/docs/models
  Kimi: {
    introduction: [
      "月之暗面 Moonshot AI 研发 Kimi 系列模型，并提供面向知识工作、推理与编程的 AI 产品。Kimi 开放平台向开发者提供模型 API，与面向用户的 Kimi 助手和 Kimi Code 分别运营。",
      "Moonshot AI develops the Kimi model family and AI products for knowledge work, reasoning, and coding. Kimi Open Platform provides model APIs for developers, alongside the Kimi assistant and Kimi Code products.",
    ],
    websiteUrl: "https://www.moonshot.cn/",
    highlights: [
      ["Kimi 模型与智能体产品", "Kimi models and agent products"],
      ["知识工作、推理与编程", "Knowledge work, reasoning, and coding"],
      ["独立的开发者开放平台", "Dedicated developer API platform"],
    ],
  },
  // https://www.minimax.io/about
  Minimax: {
    introduction: [
      "MiniMax 是研发多模态基础模型的人工智能公司，模型覆盖文本、语音、图像、视频和音乐等领域。其开放平台面向开发者与企业提供模型 API，同时推出编程、设计和音频等 AI 产品。",
      "MiniMax is an AI foundation-model company developing models across text, speech, images, video, and music. Its Open API Platform serves developers and businesses alongside AI products for coding, design, and audio.",
    ],
    websiteUrl: "https://www.minimax.cn/",
    highlights: [
      ["多模态基础模型", "Multimodal foundation models"],
      ["编程、音视频与内容创作", "Coding, audiovisual, and creative applications"],
      ["企业与开发者开放平台", "Enterprise and developer API platform"],
    ],
  },
  // https://openai.com/about/; https://openai.com/api/
  OpenAI: {
    introduction: [
      "OpenAI 是研发和部署人工智能的公司，推出了 ChatGPT 及多种基础模型。其开发者平台通过 API 提供语言、推理、编程、视觉和音频等模型能力，服务于应用与智能体开发。",
      "OpenAI is an AI research and deployment company that develops ChatGPT and foundation models. Its developer platform offers model APIs for language, reasoning, coding, vision, and audio applications.",
    ],
    websiteUrl: "https://openai.com/",
    highlights: [
      ["基础模型研究与产品", "Foundation-model research and products"],
      ["语言、视觉与音频能力", "Language, vision, and audio capabilities"],
      ["应用与智能体开发 API", "APIs for application and agent development"],
    ],
  },
  // https://openrouter.ai/; https://openrouter.ai/docs/quickstart
  OpenRouter: {
    introduction: [
      "OpenRouter 是聚合不同模型及上游供应商的模型访问与路由平台。它以统一目录和接口提供模型选择，并支持供应商选择及故障回退等路由功能。",
      "OpenRouter is a model-access and routing platform that brings together models from different developers and upstream providers. Its unified catalog and API support model selection, provider preferences, and fallback routing.",
    ],
    websiteUrl: "https://openrouter.ai/",
    highlights: [
      ["聚合多家供应商与模型", "Models from multiple developers and providers"],
      ["模型路由与供应商选择", "Model routing and provider selection"],
      ["统一目录与故障回退", "Unified catalog and fallback routing"],
    ],
  },
  // https://www.siliconflow.cn/
  SiliconFlow: {
    introduction: [
      "硅基流动 SiliconFlow 是提供托管模型 API 与 AI 云服务的平台，汇集语言、语音、图像和视频等模型。除按量模型服务外，平台还提供推理加速及企业部署服务。",
      "SiliconFlow is a hosted model API and AI cloud platform with language, speech, image, and video models. Alongside usage-based model services, it offers inference acceleration and enterprise deployment services.",
    ],
    websiteUrl: "https://www.siliconflow.cn/",
    highlights: [
      ["托管多模态模型目录", "Hosted multimodal model catalog"],
      ["按量模型 API 服务", "Usage-based model API services"],
      ["推理加速与企业部署", "Inference acceleration and enterprise deployment"],
    ],
  },
  // https://vllm.ai/
  vLLM: {
    introduction: [
      "vLLM 是用于大语言模型推理与服务部署的开源引擎，重点关注显存管理与批量调度。它可将自行部署的模型提供为在线服务，适合需要管理模型和计算资源的团队。",
      "vLLM is an open-source inference and serving engine for large language models, with a focus on memory management and batch scheduling. It serves models on infrastructure managed by users and teams.",
    ],
    websiteUrl: "https://vllm.ai/",
    highlights: [
      ["开源自部署推理引擎", "Open-source, self-hosted inference engine"],
      ["PagedAttention 显存管理", "PagedAttention memory management"],
      ["连续批处理与模型服务", "Continuous batching and model serving"],
    ],
  },
  // https://lmdeploy.readthedocs.io/en/latest/; https://github.com/InternLM/lmdeploy
  LMDeploy: {
    introduction: [
      "LMDeploy 是用于压缩、部署和服务大语言模型的开源工具集，也支持视觉语言模型。它提供推理引擎、模型量化与 API Server 等能力，面向自行管理模型部署的开发者和团队。",
      "LMDeploy is an open-source toolkit for compressing, deploying, and serving large language and vision-language models. It combines inference engines, model quantization, and API serving for users managing their own deployments.",
    ],
    websiteUrl: "https://lmdeploy.readthedocs.io/en/latest/",
    highlights: [
      ["开源自部署模型工具集", "Open-source, self-hosted deployment toolkit"],
      ["模型量化与推理引擎", "Model quantization and inference engines"],
      ["语言与视觉语言模型服务", "Language and vision-language model serving"],
    ],
  },
};

/** Child products keep their platform's homepage and describe their own offering. */
const productProfiles: Readonly<Record<string, Omit<EditorialProfile, "websiteUrl">>> = {
  // https://docs.volcengine.com/docs/ark/coding-plan-personal-plan-overview?lang=zh
  "Doubao:coding": {
    introduction: [
      "火山方舟 Coding Plan 是火山引擎面向个人开发者推出的 AI 编程订阅服务。它在支持的编程工具中提供多种模型，服务于个人项目、学习实践和日常开发。",
      "Volcengine Ark Coding Plan is an AI coding subscription for individual developers. It provides multiple model families in supported coding tools for personal projects, learning, and everyday development.",
    ],
    highlights: [
      ["个人开发者订阅", "Individual developer subscription"],
      ["多模型编程服务", "Multi-model coding services"],
      ["面向 AI 编程工具", "Designed for AI coding tools"],
    ],
  },
  // https://docs.volcengine.com/docs/ark/agent-plan-personal-plan-overview?lang=zh
  "Doubao:agent": {
    introduction: [
      "火山方舟 Agent Plan 是面向个人用户的智能体模型订阅服务。它围绕 Agent 任务提供语言及多模态模型，并包含搜索等 Harness 能力，与 Coding Plan 的编程定位有所区分。",
      "Volcengine Ark Agent Plan is a model subscription for individual users working with AI agents. It combines language and multimodal models with Harness capabilities such as search, extending beyond Coding Plan's coding focus.",
    ],
    highlights: [
      ["个人智能体订阅", "Personal agent subscription"],
      ["语言与多模态模型", "Language and multimodal models"],
      ["模型与 Harness 能力", "Model and Harness capabilities"],
    ],
  },
  // https://www.sensenova.cn/en/models; https://www.sensenova.cn/token-plan
  "SenseNova:token": {
    introduction: [
      "商汤日日新 Token Plan 是新 SenseNova 平台面向智能体场景的模型订阅产品。它以多模态理解与任务执行为重点，覆盖数据分析、研究和办公内容制作等用途。",
      "SenseNova Token Plan is a model subscription on the newer SenseNova platform for agent use cases. It focuses on multimodal understanding and task execution for data analysis, research, and office content creation.",
    ],
    highlights: [
      ["新 SenseNova 平台订阅", "Subscription on the newer SenseNova platform"],
      ["多模态智能体任务", "Multimodal agent tasks"],
      ["分析、研究与办公内容", "Analysis, research, and office content"],
    ],
  },
  // https://help.aliyun.com/zh/model-studio/coding-plan
  "Qwen:coding": {
    introduction: [
      "阿里云百炼 Coding Plan 是面向 AI 编程工具的模型订阅服务。它整合千问、GLM、Kimi 和 MiniMax 等模型，以编程套餐提供模型使用权益，与百炼标准 API 分开管理。",
      "Alibaba Cloud Model Studio Coding Plan is a model subscription for AI coding tools. It brings together Qwen, GLM, Kimi, and MiniMax models in a coding plan managed separately from the standard Model Studio API.",
    ],
    highlights: [
      ["面向 AI 编程工具的订阅", "Subscription for AI coding tools"],
      ["整合多家编程模型", "Multiple coding-model families"],
      ["独立的 Coding Plan 产品", "Dedicated Coding Plan product"],
    ],
  },
  // https://help.aliyun.com/zh/model-studio/token-plan-personal-overview
  "Qwen:token": {
    introduction: [
      "阿里云百炼 Token Plan 个人版是面向个人开发者的模型订阅服务。它以 Credits 统一计量文本、多模态模型及内置工具的使用，适用于官方支持的编程和智能体工具。",
      "Alibaba Cloud Model Studio Token Plan Personal is a model subscription for individual developers. It uses Credits to measure text, multimodal model, and built-in tool usage across officially supported coding and agent tools.",
    ],
    highlights: [
      ["个人开发者订阅", "Individual developer subscription"],
      ["Credits 统一计量", "Unified Credits metering"],
      ["多模态模型与内置工具", "Multimodal models and built-in tools"],
    ],
  },
  // https://help.aliyun.com/zh/model-studio/token-plan-overview
  "Qwen:token_team": {
    introduction: [
      "阿里云百炼 Token Plan 团队版是面向企业与团队的模型订阅服务。它在编程和智能体工具中提供多模态模型及内置工具，并通过多席位管理和用量分析支持团队使用。",
      "Alibaba Cloud Model Studio Token Plan Team is a model subscription for teams and businesses. It combines multimodal models and built-in tools for coding and agent tools with multi-seat management and usage analysis.",
    ],
    highlights: [
      ["企业与团队订阅", "Team and business subscription"],
      ["多席位管理与用量分析", "Multi-seat management and usage analysis"],
      ["多模态模型与内置工具", "Multimodal models and built-in tools"],
    ],
  },
  // https://docs.bigmodel.cn/cn/coding-plan/quick-start
  "GLM:coding": {
    introduction: [
      "GLM Coding Plan 是智谱面向编程及指定智能体工具提供的订阅产品，支持个人和团队套餐。它以 GLM 模型服务于代码生成、调试和开发任务，与标准模型 API 分开管理。",
      "GLM Coding Plan is Zhipu's subscription product for coding and designated agent tools, with personal and team plans. It provides GLM models for code generation, debugging, and development tasks separately from the standard model API.",
    ],
    highlights: [
      ["个人与团队编程订阅", "Personal and team coding subscriptions"],
      ["GLM 编程与智能体模型", "GLM coding and agent models"],
      ["面向官方指定工具", "Designed for officially designated tools"],
    ],
  },
  // https://www.kimi.com/code
  "Kimi:code": {
    introduction: [
      "Kimi Code 是月之暗面面向交互式软件开发推出的 AI 编程产品与订阅服务。它覆盖桌面、命令行和 IDE 等开发环境，提供代码编写、理解、修改与任务执行能力。",
      "Kimi Code is Moonshot AI's coding product and subscription service for interactive software development. It spans desktop, command-line, and IDE environments, helping users write, understand, and modify code and carry out development tasks.",
    ],
    highlights: [
      ["交互式编程订阅", "Interactive coding subscription"],
      ["桌面、命令行与 IDE", "Desktop, command-line, and IDE environments"],
      ["代码理解、修改与任务执行", "Code understanding, editing, and task execution"],
    ],
  },
  // https://platform.minimax.io/docs/m-plan/faq
  "Minimax:m_plan": {
    introduction: [
      "MiniMax M Plan 是面向个人交互式开发的模型订阅产品，涵盖编程与多模态用途。它将订阅模型权益用于 MiniMax Code 及其他支持的 AI 工具，与标准 API 按量服务分开管理。",
      "MiniMax M Plan is a model subscription for individual, interactive development across coding and multimodal use cases. It provides subscription benefits in MiniMax Code and other supported AI tools, separately from standard usage-based API services.",
    ],
    highlights: [
      ["个人交互式开发订阅", "Individual interactive-development subscription"],
      ["编程与多模态用途", "Coding and multimodal use cases"],
      ["MiniMax Code 与支持的 AI 工具", "MiniMax Code and supported AI tools"],
    ],
  },
};

export function getProviderProfileContent(
  brandKey: string,
  serviceKey: string | undefined,
  language = "en-US",
): ProviderProfile | undefined {
  if (!Object.prototype.hasOwnProperty.call(brandProfiles, brandKey)) return undefined;
  const brand = brandProfiles[brandKey];
  const productKey = `${brandKey}:${serviceKey}`;
  const product = serviceKey === undefined || serviceKey === "standard"
    ? brand
    : Object.prototype.hasOwnProperty.call(productProfiles, productKey) ? productProfiles[productKey] : undefined;
  if (!product) return undefined;
  const text = (value: InformationText) => value[/^zh(?:[-_]|$)/i.test(language) ? 0 : 1];
  return {
    introduction: text(product.introduction),
    websiteUrl: brand.websiteUrl,
    highlights: product.highlights.map(text),
  };
}
