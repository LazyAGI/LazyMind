const providerLogoAssets: Array<[RegExp, string]> = [
  [/sensenova|sensecore|商汤|日日新/, "sensenova.svg"],
  [/claude|anthropic/, "anthropic.svg"],
  [/deepseek/, "deepseek.svg"],
  [/doubao|volc|ark/, "doubao.svg"],
  [/glm|bigmodel|zhipu/, "glm.svg"],
  [/kimi|moonshot/, "kimi.svg"],
  [/minimax/, "minimax.svg"],
  [/openrouter/, "openrouter.svg"],
  [/openai/, "openai.svg"],
  [/qwen|tongyi|通义/, "qwen.svg"],
  [/siliconflow/, "siliconflow.svg"],
];

export function getProviderLogoUrl(name: string) {
  const normalized = name.trim().toLowerCase();
  const match = providerLogoAssets.find(([pattern]) => pattern.test(normalized));
  return match ? `/provider-icons/${match[1]}` : undefined;
}
