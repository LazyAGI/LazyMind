/** Immutable catalog identity; it is never derived from a request adapter. */
export interface SupplierServiceIdentity {
  name: string;
  isCustom?: boolean;
  is_custom?: boolean;
  brandKey?: string;
  brand_key?: string;
  serviceKey?: string;
  service_key?: string;
  protocolName?: string;
  protocol_name?: string;
  source?: string;
}

export interface SupplierService {
  brandKey: string;
  serviceKey: string;
  order: number;
}

interface ServiceDefinition extends SupplierService {
  catalogName: string;
  zh: string;
  en: string;
}

const services: readonly ServiceDefinition[] = [
  { brandKey: "Doubao", serviceKey: "standard", order: 0, catalogName: "Doubao", zh: "标准API", en: "Standard API" },
  { brandKey: "Doubao", serviceKey: "coding", order: 1, catalogName: "DoubaoCodingPlan", zh: "Coding Plan", en: "Coding Plan" },
  { brandKey: "Doubao", serviceKey: "agent", order: 2, catalogName: "DoubaoAgentPlan", zh: "Agent Plan", en: "Agent Plan" },
  { brandKey: "SenseNova", serviceKey: "standard", order: 0, catalogName: "SenseNova", zh: "经典API", en: "Classic API" },
  { brandKey: "SenseNova", serviceKey: "token", order: 1, catalogName: "SenseNovaTokenPlan", zh: "Token Plan", en: "Token Plan" },
  { brandKey: "Qwen", serviceKey: "standard", order: 0, catalogName: "Qwen", zh: "标准API", en: "Standard API" },
  { brandKey: "Qwen", serviceKey: "coding", order: 1, catalogName: "QwenCodingPlan", zh: "Coding Plan", en: "Coding Plan" },
  { brandKey: "Qwen", serviceKey: "token", order: 2, catalogName: "QwenTokenPlan", zh: "Token Plan", en: "Token Plan" },
  { brandKey: "Qwen", serviceKey: "token_team", order: 3, catalogName: "QwenTokenPlanTeam", zh: "Token Plan团队版", en: "Token Plan Team" },
  { brandKey: "GLM", serviceKey: "standard", order: 0, catalogName: "GLM", zh: "标准API", en: "Standard API" },
  { brandKey: "GLM", serviceKey: "coding", order: 1, catalogName: "GLMCodingPlan", zh: "Coding Plan", en: "Coding Plan" },
  { brandKey: "Kimi", serviceKey: "standard", order: 0, catalogName: "Kimi", zh: "Kimi开放平台", en: "Kimi Open Platform" },
  { brandKey: "Kimi", serviceKey: "code", order: 1, catalogName: "KimiCode", zh: "Kimi Code", en: "Kimi Code" },
  { brandKey: "Minimax", serviceKey: "standard", order: 0, catalogName: "Minimax", zh: "标准API", en: "Standard API" },
  { brandKey: "Minimax", serviceKey: "m_plan", order: 1, catalogName: "MiniMaxMPlan", zh: "M Plan", en: "M Plan" },
];

const servicesByName = new Map(services.map((service) => [service.catalogName.toLowerCase(), service]));
const servicesByKey = new Map(services.map((service) => [`${service.brandKey}:${service.serviceKey}`, service]));
const brandAliases: ReadonlyArray<readonly [string, readonly string[]]> = [
  ["Claude", ["claude", "anthropic"]],
  ["DeepSeek", ["deepseek", "深度求索"]],
  ["Doubao", ["doubao", "豆包", "volcengine", "火山方舟", "火山引擎"]],
  ["GLM", ["glm", "zhipu", "bigmodel", "智谱", "智谱ai"]],
  ["Kimi", ["kimi", "moonshot", "moonshotai", "月之暗面"]],
  ["Minimax", ["minimax"]],
  ["OpenAI", ["openai"]],
  ["OpenRouter", ["openrouter"]],
  ["Qwen", ["qwen", "tongyi", "dashscope", "通义", "通义千问", "阿里云百炼"]],
  ["SenseNova", ["sensenova", "商汤日日新", "日日新"]],
  ["SiliconFlow", ["siliconflow", "硅基流动"]],
  ["vLLM", ["vllm"]],
  ["LMDeploy", ["lmdeploy"]],
];
const brandsByAlias = new Map(brandAliases.flatMap(([brand, aliases]) => aliases.map((alias) => [alias, brand] as const)));
const custom = (supplier: SupplierServiceIdentity) => supplier.isCustom === true || supplier.is_custom === true;
const explicitBrand = (supplier: SupplierServiceIdentity) => (supplier.brandKey || supplier.brand_key || "").trim();

export function getSupplierBrandKey(supplier: SupplierServiceIdentity): string | undefined {
  if (custom(supplier)) return undefined;
  const explicit = explicitBrand(supplier);
  if (explicit) return brandsByAlias.get(explicit.toLowerCase()) || explicit;
  const name = supplier.name.trim().toLowerCase();
  if (name) return servicesByName.get(name)?.brandKey || brandsByAlias.get(name);
  // Preserve the historical brand-only empty-name fallback. It never creates
  // a service identity and never overrides a saved/custom supplier name.
  for (const value of [supplier.protocolName, supplier.protocol_name, supplier.source]) {
    const brand = brandsByAlias.get(value?.trim().toLowerCase() || "");
    if (brand) return brand;
  }
  return undefined;
}

export function resolveSupplierService(supplier: SupplierServiceIdentity): SupplierService | undefined {
  if (custom(supplier)) return undefined;
  const legacy = servicesByName.get(supplier.name.trim().toLowerCase());
  const brand = explicitBrand(supplier);
  const service = (supplier.serviceKey || supplier.service_key || "").trim();
  const brandKey = brand ? getSupplierBrandKey(supplier) : legacy?.brandKey;
  const serviceKey = service || (legacy && legacy.brandKey === brandKey ? legacy.serviceKey : undefined);
  const match = brandKey && serviceKey ? servicesByKey.get(`${brandKey}:${serviceKey}`) : undefined;
  return match ? { brandKey: match.brandKey, serviceKey: match.serviceKey, order: match.order } : undefined;
}

export function getSupplierServiceLabel(supplier: SupplierServiceIdentity, language = "en-US"): string {
  const identity = resolveSupplierService(supplier);
  const service = identity ? servicesByKey.get(`${identity.brandKey}:${identity.serviceKey}`) : undefined;
  return service ? /^zh(?:[-_]|$)/i.test(language) ? service.zh : service.en : "";
}
