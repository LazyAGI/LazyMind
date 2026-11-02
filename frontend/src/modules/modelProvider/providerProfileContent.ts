import type { InformationText } from "./providerInformationContent";

export interface ProviderProfile {
  introduction: string;
  serviceSummary: string;
  websiteUrl: string;
  highlights: string[];
}

interface EditorialProfile {
  introduction: InformationText;
  serviceSummary: InformationText;
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
      "Anthropic 研发 Claude 系列模型，通过开发者平台为应用和工具型智能体提供语言、推理与编程服务。",
      "Anthropic develops Claude models and provides language, reasoning, and coding services for applications and tool-using agents through its developer platform.",
    ],
    serviceSummary: ["自研模型 API，供应用与工具型智能体接入", "Claude model APIs for applications and tool-using agents"],
    websiteUrl: "https://www.anthropic.com/",
    highlights: [
      ["可靠性、可解释性与可控性研究", "Research on reliability, interpretability, and steerability"],
      ["开发者工作空间管理 API 凭据与权限", "API credentials and permissions managed by developer workspace"],
    ],
  },
  // https://www.deepseek.com/; https://api-docs.deepseek.com/
  DeepSeek: {
    introduction: [
      "深度求索研发语言与推理模型，产品覆盖网页对话、应用和开发者 API，可接入文本处理、编程及智能体应用。",
      "DeepSeek develops language and reasoning models, offering web chat, apps, and developer APIs for text processing, coding, and agent applications.",
    ],
    serviceSummary: ["官方语言与推理模型，供应用开发者通过 API 调用", "Official language and reasoning model APIs for application developers"],
    websiteUrl: "https://www.deepseek.com/",
    highlights: [
      ["持续发布模型研究成果", "Ongoing publication of model research"],
      ["网页对话与 API 凭据分开管理", "Web chat and API credentials are managed separately"],
    ],
  },
  // https://docs.volcengine.com/docs/ark?lang=zh
  Doubao: {
    introduction: [
      "火山方舟是火山引擎的大模型服务平台，汇集豆包及其他主流模型，以标准 API 为应用开发提供模型服务。",
      "Volcengine Ark is a model-service platform offering Doubao and other major model families through a standard API for application development.",
    ],
    serviceSummary: ["应用模型 API，接入豆包及方舟目录中的其他模型", "Standard model APIs for applications using Doubao and the Ark catalog"],
    websiteUrl: "https://www.volcengine.com/product/ark",
    highlights: [
      ["提供模型评测与精调", "Model evaluation and fine-tuning services"],
      ["按项目开通模型并管理 API Key", "Model activation and API keys managed by project"],
    ],
  },
  // https://www.sensetime.com/cn/faq; https://www.sensenova.cn/en/models
  SenseNova: {
    introduction: [
      "商汤日日新是商汤的模型与原生应用体系，覆盖多模态理解、内容生成和任务执行；经典 API 由大装置提供。",
      "SenseNova is SenseTime's model and native-app family for multimodal understanding, generation, and task execution. Its Classic API runs on SenseCore.",
    ],
    serviceSummary: ["大装置经典模型 API，与新平台订阅分开管理", "Classic SenseCore model APIs, managed separately from newer-platform subscriptions"],
    websiteUrl: "https://www.sensenova.cn/",
    highlights: [
      ["与新平台 Token Plan 分平台管理", "Managed separately from Token Plan on the newer platform"],
      ["沿用经典接口的认证与模型标识", "Uses classic-interface authentication and model identifiers"],
    ],
  },
  // https://help.aliyun.com/zh/model-studio/what-is-model-studio
  Qwen: {
    introduction: [
      "阿里云百炼汇集千问与第三方模型，提供云端模型推理和应用开发服务，面向开发者与企业的应用接入需求。",
      "Alibaba Cloud Model Studio brings Qwen and third-party models into a cloud platform for model inference and application development by developers and businesses.",
    ],
    serviceSummary: ["应用模型 API，按地域与工作空间接入百炼模型", "Standard model APIs for applications, scoped by region and workspace"],
    websiteUrl: "https://www.aliyun.com/product/bailian",
    highlights: [
      ["模型调优、评测与部署", "Model fine-tuning, evaluation, and deployment"],
      ["API Key 随地域与工作空间管理", "API keys scoped to a region and workspace"],
    ],
  },
  // https://docs.bigmodel.cn/cn/guide/start/model-overview
  GLM: {
    introduction: [
      "智谱研发 GLM 系列模型，通过开放平台向开发者和企业提供模型服务，以标准 API 支持应用开发和模型接入。",
      "Zhipu develops the GLM model family and serves developers and businesses through BigModel, with standard model APIs for application development.",
    ],
    serviceSummary: ["应用模型 API，提供智谱自研 GLM 模型服务", "Standard APIs for applications using Zhipu's GLM models"],
    websiteUrl: "https://www.bigmodel.cn/",
    highlights: [
      ["覆盖文本推理、视觉与语音等模型", "Models for text reasoning, vision, speech, and more"],
      ["标准 API 与个人、团队套餐 Key 分离", "Separate keys for Standard API, personal plans, and team plans"],
    ],
  },
  // https://www.moonshot.cn/; https://platform.kimi.com/docs/models
  Kimi: {
    introduction: [
      "月之暗面研发 Kimi 系列模型与 AI 产品，面向知识工作、推理和编程；开放平台向开发者提供模型 API 服务。",
      "Moonshot AI develops Kimi models and products for knowledge work, reasoning, and coding, with model APIs for developers through Kimi Open Platform.",
    ],
    serviceSummary: ["开放平台模型 API，供知识处理与智能体应用接入", "Open Platform model APIs for knowledge-processing and agent applications"],
    websiteUrl: "https://www.moonshot.cn/",
    highlights: [
      ["与网页助手、Kimi Code 分别运营", "Operates separately from the web assistant and Kimi Code"],
      ["使用开放平台 Key 与模型标识", "Uses Open Platform keys and model identifiers"],
    ],
  },
  // https://www.minimax.io/about
  Minimax: {
    introduction: [
      "MiniMax 研发文本、语音、图像、视频和音乐等多模态模型，通过开放平台向开发者与企业提供模型 API 服务。",
      "MiniMax develops multimodal models across text, speech, images, video, and music, serving developers and businesses through its Open API Platform.",
    ],
    serviceSummary: ["多模态模型 API，供应用开发者按实际用量调用", "Usage-based multimodal model APIs for application developers"],
    websiteUrl: "https://www.minimax.cn/",
    highlights: [
      ["另有编程、设计与音频等 AI 产品", "Also offers AI products for coding, design, and audio"],
      ["标准 API Key 与 M Plan 订阅 Key 分离", "Separate Standard API keys and M Plan Subscription Keys"],
    ],
  },
  // https://openai.com/about/; https://openai.com/api/
  OpenAI: {
    introduction: [
      "OpenAI 研发 ChatGPT 及基础模型，通过开发者平台为应用和智能体提供语言、推理、编程、视觉与音频 API。",
      "OpenAI develops ChatGPT and foundation models, offering developer APIs for language, reasoning, coding, vision, and audio applications and agents.",
    ],
    serviceSummary: ["项目级模型 API，供应用与智能体开发者接入", "Project-scoped model APIs for application and agent developers"],
    websiteUrl: "https://openai.com/",
    highlights: [
      ["项目级 API Key 管理访问权限", "Project API keys manage model access"],
      ["API 凭据与用量独立于 ChatGPT 订阅", "API credentials and usage are separate from ChatGPT subscriptions"],
    ],
  },
  // https://openrouter.ai/; https://openrouter.ai/docs/quickstart
  OpenRouter: {
    introduction: [
      "OpenRouter 汇集不同开发者与上游供应商的模型，通过统一目录和 API 提供模型访问服务，并以路由机制分配请求。",
      "OpenRouter brings models from different developers and upstream providers into one catalog and API, with routing to direct model requests.",
    ],
    serviceSummary: ["多模型聚合 API，供开发者统一访问与路由模型", "Aggregated model APIs for developers managing model access and routing"],
    websiteUrl: "https://openrouter.ai/",
    highlights: [
      ["可选择上游供应商并配置故障回退", "Upstream providers can be selected with fallback routing"],
      ["完整目录 ID，参数与可用性随上游而异", "Full catalog IDs; parameters and availability vary by upstream provider"],
    ],
  },
  // https://www.siliconflow.cn/
  SiliconFlow: {
    introduction: [
      "硅基流动是托管模型 API 与 AI 云服务平台，汇集语言、语音、图像及视频模型，面向开发者提供按量模型服务。",
      "SiliconFlow is a hosted model API and AI cloud platform, offering usage-based access to language, speech, image, and video models for developers.",
    ],
    serviceSummary: ["托管模型 API，供开发者接入多模态模型目录", "Hosted model APIs for developers accessing a multimodal catalog"],
    websiteUrl: "https://www.siliconflow.cn/",
    highlights: [
      ["提供推理加速与企业部署服务", "Inference acceleration and enterprise deployment services"],
      ["完整模型标识包含所需前缀与版本", "Full model identifiers include required prefixes and versions"],
    ],
  },
  // https://vllm.ai/
  vLLM: {
    introduction: [
      "vLLM 是开源大语言模型推理与服务引擎，可将自行部署的模型提供为在线服务，供团队管理模型和计算资源。",
      "vLLM is an open-source inference and serving engine that makes self-deployed language models available online, with teams managing their models and compute.",
    ],
    serviceSummary: ["自部署推理服务，连接团队已运行的模型与计算资源", "Self-hosted inference for teams connecting their deployed models and compute"],
    websiteUrl: "https://vllm.ai/",
    highlights: [
      ["PagedAttention 显存管理", "PagedAttention memory management"],
      ["连续批处理与模型服务调度", "Continuous batching and model-serving scheduling"],
    ],
  },
  // https://lmdeploy.readthedocs.io/en/latest/; https://github.com/InternLM/lmdeploy
  LMDeploy: {
    introduction: [
      "LMDeploy 是开源模型压缩、部署与服务工具集，支持语言和视觉语言模型，供开发者自行管理模型部署与服务。",
      "LMDeploy is an open-source toolkit for compressing, deploying, and serving language and vision-language models, for developers managing their own deployments.",
    ],
    serviceSummary: ["自部署模型服务，连接已运行的 API Server", "Self-hosted model serving through an already running API Server"],
    websiteUrl: "https://lmdeploy.readthedocs.io/en/latest/",
    highlights: [
      ["模型量化与推理引擎", "Model quantization and inference engines"],
      ["API Server 需从 LazyMind 后端可达", "API Server must be reachable from the LazyMind backend"],
    ],
  },
};

/** Child products keep their platform's homepage and describe their own offering. */
const productProfiles: Readonly<Record<string, Omit<EditorialProfile, "websiteUrl">>> = {
  // https://docs.volcengine.com/docs/ark/coding-plan-personal-plan-overview?lang=zh
  "Doubao:coding": {
    introduction: [
      "方舟 Coding Plan 是个人开发者的 AI 编程订阅，在支持的工具中提供模型，用于个人项目、学习与日常开发。",
      "Volcengine Ark Coding Plan is an AI coding subscription for individual developers using supported tools for personal projects, learning, and everyday development.",
    ],
    serviceSummary: ["个人编程订阅，为支持的编程工具提供多种模型", "Personal coding subscription with multiple models for supported coding tools"],
    highlights: [
      ["一个套餐提供多种模型家族", "Multiple model families in one plan"],
      ["使用个人版套餐的 Key 与连接配置", "Uses the personal plan's key and connection settings"],
    ],
  },
  // https://docs.volcengine.com/docs/ark/agent-plan-personal-plan-overview?lang=zh
  "Doubao:agent": {
    introduction: [
      "方舟 Agent Plan 是面向个人用户的智能体模型订阅，提供语言与多模态模型，用于 AI 工具中的智能体任务。",
      "Volcengine Ark Agent Plan is a personal model subscription offering language and multimodal models for agent tasks in AI tools.",
    ],
    serviceSummary: ["个人智能体订阅，提供模型与 Harness 工具能力", "Personal agent subscription combining models and Harness capabilities"],
    highlights: [
      ["包含搜索等 Harness 能力", "Includes Harness capabilities such as search"],
      ["专属 Key，普通方舟或 Coding Plan Key 不可替代", "Dedicated key; ordinary Ark or Coding Plan keys cannot replace it"],
    ],
  },
  // https://www.sensenova.cn/en/models; https://www.sensenova.cn/token-plan
  "SenseNova:token": {
    introduction: [
      "日日新 Token Plan 是新平台的智能体模型订阅，以多模态理解与任务执行支持数据分析、研究和办公内容制作。",
      "SenseNova Token Plan is an agent-model subscription on the newer platform, using multimodal understanding and task execution for analysis, research, and office content.",
    ],
    serviceSummary: ["新平台智能体订阅，提供多模态理解与任务执行", "New-platform agent subscription for multimodal understanding and task execution"],
    highlights: [
      ["新平台 Key 与大装置经典认证分离", "New-platform keys are separate from classic SenseCore authentication"],
      ["LazyMind 当前不支持 Anthropic 请求", "LazyMind currently does not support Anthropic requests for this plan"],
    ],
  },
  // https://help.aliyun.com/zh/model-studio/coding-plan
  "Qwen:coding": {
    introduction: [
      "百炼 Coding Plan 面向 AI 编程工具提供模型订阅权益，使用专属套餐凭据，与百炼标准 API 分开管理。",
      "Alibaba Cloud Model Studio Coding Plan provides model subscription access for AI coding tools, with dedicated credentials separate from Standard API.",
    ],
    serviceSummary: ["编程工具订阅，整合千问及多家厂商的编程模型", "Coding-tool subscription combining Qwen and other coding-model families"],
    highlights: [
      ["整合千问、GLM、Kimi 和 MiniMax 模型", "Includes Qwen, GLM, Kimi, and MiniMax models"],
      ["仅限 AI 编程工具与 OpenClaw 类 Agent，工作流不适用", "Limited to AI coding tools and OpenClaw-type agents; workflows are excluded"],
    ],
  },
  // https://help.aliyun.com/zh/model-studio/token-plan-personal-overview
  "Qwen:token": {
    introduction: [
      "百炼 Token Plan 个人版是个人开发者的模型订阅，将文本、多模态模型与内置工具用于编程和智能体任务。",
      "Alibaba Cloud Model Studio Token Plan Personal is a subscription for individual developers, combining text and multimodal models with built-in tools for coding and agent tasks.",
    ],
    serviceSummary: ["个人工具订阅，以 Credits 计量模型和内置工具", "Personal tool subscription with Credits metering for models and built-in tools"],
    highlights: [
      ["Credits 统一计量，使用个人版专属 Key", "Unified Credits metering with a dedicated personal-edition key"],
      ["仅限 AI 编程工具与 OpenClaw 类 Agent，工作流不适用", "Limited to AI coding tools and OpenClaw-type agents; workflows are excluded"],
    ],
  },
  // https://help.aliyun.com/zh/model-studio/token-plan-overview
  "Qwen:token_team": {
    introduction: [
      "百炼 Token Plan 团队版面向企业与团队，在编程和智能体工具中提供多模态模型及内置工具，使用团队订阅权益。",
      "Alibaba Cloud Model Studio Token Plan Team is a subscription for teams and businesses, providing multimodal models and built-in tools in coding and agent tools.",
    ],
    serviceSummary: ["团队工具订阅，提供多席位管理与套餐用量分析", "Team tool subscription with multiple seats and usage analysis"],
    highlights: [
      ["多席位与用量分析，使用团队版专属 Key", "Multiple seats and usage analysis with dedicated team-edition keys"],
      ["仅限 AI 编程工具与 OpenClaw 类 Agent，工作流不适用", "Limited to AI coding tools and OpenClaw-type agents; workflows are excluded"],
    ],
  },
  // https://docs.bigmodel.cn/cn/coding-plan/quick-start
  "GLM:coding": {
    introduction: [
      "智谱编程套餐以 GLM 模型提供编程订阅，支持代码生成、调试和开发任务，面向个人与团队，供指定工具使用。",
      "GLM Coding Plan is Zhipu's subscription for individuals and teams, using GLM models for code generation, debugging, and development in designated tools.",
    ],
    serviceSummary: ["个人与团队编程订阅，供官方指定工具使用 GLM", "Personal and team coding subscriptions using GLM in designated tools"],
    highlights: [
      ["个人、团队与标准 API 的 Key 互不通用", "Personal, team, and Standard API keys are not interchangeable"],
      ["部分 Agent 工具尽力交付、调度优先级较低", "Some agent tools receive best-effort service with lower scheduling priority"],
    ],
  },
  // https://www.kimi.com/code
  "Kimi:code": {
    introduction: [
      "Kimi Code 是月之暗面的 AI 编程产品与订阅，面向软件开发，提供代码编写、理解、修改和任务执行能力。",
      "Kimi Code is Moonshot AI's coding product and subscription for software development, helping users write, understand, and modify code and carry out tasks.",
    ],
    serviceSummary: ["个人交互式编程订阅，供支持的开发工具使用模型", "Personal interactive-coding subscription for supported development tools"],
    highlights: [
      ["桌面、命令行与 IDE", "Desktop, command-line, and IDE environments"],
      ["限个人交互式使用，不适用无人值守批处理", "Personal interactive use only; unattended batch processing is excluded"],
    ],
  },
  // https://platform.minimax.io/docs/m-plan/faq
  "Minimax:m_plan": {
    introduction: [
      "M Plan 是 MiniMax 面向个人交互式开发的模型订阅，涵盖编程与多模态用途，在支持的 AI 工具中使用。",
      "MiniMax M Plan is a model subscription for individual interactive development, covering coding and multimodal use in supported AI tools.",
    ],
    serviceSummary: ["个人开发订阅，为兼容 AI 工具提供编程与多模态模型", "Personal development subscription for coding and multimodal use in compatible tools"],
    highlights: [
      ["Subscription Key 与标准 API Key 分离", "Subscription Keys are separate from Standard API keys"],
      ["官方指南含 Dify；生产环境建议按量付费", "Official guidance includes Dify; pay-as-you-go is recommended for production"],
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
    serviceSummary: text(product.serviceSummary),
    websiteUrl: brand.websiteUrl,
    highlights: product.highlights.map(text),
  };
}
