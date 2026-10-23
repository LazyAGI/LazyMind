/** A display-only identity: never replace the stored/API supplier name with it. */
export interface ProviderDisplayIdentity {
  name: string;
  isCustom?: boolean;
  is_custom?: boolean;
  protocolName?: string;
  protocol_name?: string;
  source?: string;
}

interface ProviderBrandName {
  en: string;
  zh: string;
  aliases: readonly string[];
}

// These are supplier brands, not a translation of request protocols or model IDs.
// MiniMax's Chinese site also uses its English brand; custom names bypass this
// registry entirely, including names identical to one of these catalog entries.
const providerNames: readonly ProviderBrandName[] = [
  { en: "Claude", zh: "Claude", aliases: ["claude", "anthropic"] },
  { en: "DeepSeek", zh: "深度求索", aliases: ["deepseek", "深度求索"] },
  {
    en: "Volcengine",
    zh: "火山引擎",
    aliases: ["doubao", "豆包", "volcengine", "火山方舟", "火山引擎"],
  },
  { en: "GLM", zh: "智谱", aliases: ["glm", "zhipu", "bigmodel", "智谱", "智谱ai"] },
  { en: "Kimi", zh: "月之暗面", aliases: ["kimi", "moonshot", "moonshotai", "月之暗面"] },
  { en: "MiniMax", zh: "MiniMax", aliases: ["minimax"] },
  { en: "OpenAI", zh: "OpenAI", aliases: ["openai"] },
  { en: "OpenRouter", zh: "OpenRouter", aliases: ["openrouter"] },
  {
    en: "Qwen",
    zh: "通义千问",
    aliases: ["qwen", "tongyi", "dashscope", "通义", "通义千问", "阿里云百炼"],
  },
  {
    en: "SenseNova",
    zh: "商汤日日新",
    aliases: ["sensenova", "商汤日日新", "日日新"],
  },
  { en: "SiliconFlow", zh: "硅基流动", aliases: ["siliconflow", "硅基流动"] },
];

const brandByAlias = new Map(
  providerNames.flatMap((brand) => brand.aliases.map((alias) => [alias, brand] as const)),
);

function isCustom(provider: ProviderDisplayIdentity): boolean {
  return provider.isCustom === true || provider.is_custom === true;
}

function findBrand(provider: ProviderDisplayIdentity): ProviderBrandName | undefined {
  if (isCustom(provider)) return undefined;
  const name = provider.name.trim().toLowerCase();
  // A compatible request protocol is not evidence of supplier identity. This
  // also preserves unknown or historical custom names whose flag is absent.
  if (name) return brandByAlias.get(name);
  for (const identity of [provider.protocolName, provider.protocol_name, provider.source]) {
    const brand = brandByAlias.get(identity?.trim().toLowerCase() || "");
    if (brand) return brand;
  }
  return undefined;
}

export function getProviderDisplayName(
  provider: ProviderDisplayIdentity,
  language = "en-US",
): string {
  const brand = findBrand(provider);
  if (!brand) return provider.name;
  return /^zh(?:[-_]|$)/i.test(language) ? brand.zh : brand.en;
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
    ...(brand ? [brand.en, brand.zh, ...brand.aliases] : []),
  ])].join(" ").trim().toLowerCase();
}
