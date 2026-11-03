import { getSupplierBrandKey, getSupplierServiceLabel, type SupplierServiceIdentity } from "./supplierServices";

/** A display-only identity: never replace the stored/API supplier name with it. */
export interface ProviderDisplayIdentity extends SupplierServiceIdentity {}

interface ProviderBrandName {
  key: string;
  en: string;
  zh: string;
  aliases: readonly string[];
}

// These are supplier brands, not a translation of request protocols or model IDs.
// MiniMax's Chinese site also uses its English brand; custom names bypass this
// registry entirely, including names identical to one of these catalog entries.
const providerNames: readonly ProviderBrandName[] = [
  { key: "Claude", en: "Claude", zh: "Claude", aliases: ["claude", "anthropic"] },
  { key: "DeepSeek", en: "DeepSeek", zh: "深度求索", aliases: ["deepseek", "深度求索"] },
  {
    key: "Doubao", en: "Volcengine",
    zh: "火山引擎",
    aliases: ["doubao", "豆包", "volcengine", "火山方舟", "火山引擎"],
  },
  { key: "GLM", en: "GLM", zh: "智谱", aliases: ["glm", "zhipu", "bigmodel", "智谱", "智谱ai"] },
  { key: "Kimi", en: "Kimi", zh: "月之暗面", aliases: ["kimi", "moonshot", "moonshotai", "月之暗面"] },
  { key: "Minimax", en: "MiniMax", zh: "MiniMax", aliases: ["minimax"] },
  { key: "OpenAI", en: "OpenAI", zh: "OpenAI", aliases: ["openai"] },
  { key: "OpenRouter", en: "OpenRouter", zh: "OpenRouter", aliases: ["openrouter"] },
  {
    key: "Qwen", en: "Alibaba Cloud Model Studio",
    zh: "阿里云百炼",
    aliases: ["qwen", "tongyi", "dashscope", "通义", "通义千问", "阿里云百炼"],
  },
  {
    key: "SenseNova", en: "SenseNova",
    zh: "商汤日日新",
    aliases: ["sensenova", "商汤日日新", "日日新"],
  },
  { key: "SiliconFlow", en: "SiliconFlow", zh: "硅基流动", aliases: ["siliconflow", "硅基流动"] },
  { key: "vLLM", en: "vLLM", zh: "vLLM", aliases: ["vllm"] },
  { key: "LMDeploy", en: "LMDeploy", zh: "LMDeploy", aliases: ["lmdeploy"] },
];

const brandByKey = new Map(providerNames.map((brand) => [brand.key, brand]));

function isCustom(provider: ProviderDisplayIdentity): boolean {
  return provider.isCustom === true || provider.is_custom === true;
}

function findBrand(provider: ProviderDisplayIdentity): ProviderBrandName | undefined {
  if (isCustom(provider)) return undefined;
  return brandByKey.get(getSupplierBrandKey(provider) || "");
}

export function getProviderDisplayName(
  provider: ProviderDisplayIdentity,
  language = "en-US",
): string {
  const brand = getProviderBrandDisplayName(provider, language);
  const service = getProviderServiceDisplayName(provider, language);
  return service ? `${brand} · ${service}` : brand;
}

export function getProviderBrandDisplayName(provider: ProviderDisplayIdentity, language = "en-US"): string {
  const brand = findBrand(provider);
  if (!brand) return provider.name;
  return /^zh(?:[-_]|$)/i.test(language) ? brand.zh : brand.en;
}

export function getProviderServiceDisplayName(provider: ProviderDisplayIdentity, language = "en-US"): string {
  return getSupplierServiceLabel(provider, language);
}

/** Brand matches reveal its services; a service label must not match the parent. */
export function getProviderBrandSearchText(provider: ProviderDisplayIdentity, language = "en-US"): string {
  const brand = findBrand(provider);
  return [...new Set([
    getProviderBrandDisplayName(provider, language),
    ...(brand ? [brand.en, brand.zh, ...brand.aliases] : []),
  ])].join(" ").trim().toLowerCase();
}

/** Search both languages and catalog aliases without changing saved identity. */
export function getProviderSearchText(
  provider: ProviderDisplayIdentity,
  language = "en-US",
): string {
  const brand = findBrand(provider);
  return [...new Set([
    provider.name,
    getProviderDisplayName(provider, language),
    getProviderServiceDisplayName(provider, "zh-CN"),
    getProviderServiceDisplayName(provider, "en-US"),
    ...(brand ? [brand.en, brand.zh, ...brand.aliases] : []),
  ])].join(" ").trim().toLowerCase();
}
