export interface ModelIdentity {
  vendor: string;
  logoUrl: string;
}

const families: Array<{ pattern: RegExp; vendor: string; asset: string }> = [
  { pattern: /deepseek/i, vendor: "DeepSeek", asset: "deepseek" },
  {
    pattern: /(?:claude|(?:^|[-_: .])(?:sonnet|haiku|opus)(?:$|[-_ .\d]))/i,
    vendor: "Anthropic",
    asset: "anthropic",
  },
  {
    pattern:
      /(?:^|[-_: .])(?:gpt|chatgpt)(?:$|[-_ .\d])|(?:^|[-_: .])o[134](?:$|[-_ .])/i,
    vendor: "OpenAI",
    asset: "openai",
  },
  {
    pattern:
      /(?:qwen|(?:^|[-_: .])qw[qv](?:$|[-_ .\d])|(?:^|[-_: .])qvq(?:$|[-_ .\d]))/i,
    vendor: "Qwen",
    asset: "qwen",
  },
  {
    pattern: /(?:^|[-_: .])glm(?:$|[-_ .\d])|chatglm/i,
    vendor: "GLM",
    asset: "glm",
  },
  {
    pattern:
      /(?:doubao|bytedance|(?:^|[-_: .])seed(?:ream|ance|edit|oss)?(?:$|[-_ .\d]))/i,
    vendor: "ByteDance",
    asset: "bytedance",
  },
  { pattern: /(?:^|[-_: .])bge(?:$|[-_ .\d])/i, vendor: "BAAI", asset: "baai" },
  {
    pattern: /hunyuan|混元|(?:^|[-_: .])hy\d+(?:$|[-_ .])/i,
    vendor: "Hunyuan",
    asset: "hunyuan",
  },
  { pattern: /llama/i, vendor: "Meta", asset: "meta" },
  { pattern: /mistral|mixtral/i, vendor: "Mistral", asset: "mistral" },
  { pattern: /gemini/i, vendor: "Google", asset: "gemini" },
  { pattern: /gemma/i, vendor: "Google", asset: "google" },
  {
    pattern: /(?:^|[-_: .])phi(?:$|[-_ .\d])/i,
    vendor: "Microsoft",
    asset: "microsoft",
  },
  { pattern: /kimi|moonshot/i, vendor: "Kimi", asset: "kimi" },
  { pattern: /minimax/i, vendor: "Minimax", asset: "minimax" },
  {
    pattern: /sensenova|sensechat|日日新/i,
    vendor: "SenseNova",
    asset: "sensenova",
  },
];

// A gateway's routing prefix does not establish who created the model.
// In particular, openai/private-model still receives a neutral model icon.
export function getModelIdentity(
  modelName: string,
  fallbackVendor?: string,
): ModelIdentity {
  const path = modelName.trim().split(/[\\/]/).filter(Boolean);
  const modelId = path[path.length - 1] || "";
  const family = families.find(({ pattern }) => pattern.test(modelId));
  return family
    ? { vendor: family.vendor, logoUrl: `/provider-icons/${family.asset}.svg` }
    : {
        vendor: fallbackVendor?.trim() || "Custom",
        logoUrl: "/provider-icons/custom.svg",
      };
}
