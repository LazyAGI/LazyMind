const chineseTranslationProviderNames: Record<string, string> = {
  "google translate web": "谷歌翻译（网页）",
  "bing translate web": "必应翻译（网页）",
  "sogou translate web": "搜狗翻译（网页）",
  "deepl web": "DeepL 网页翻译",
  "tencent transmart web": "腾讯翻译君（网页）",
  "baidu translation": "百度翻译",
  "aliyun translation": "阿里云机器翻译",
  niutrans: "小牛翻译",
};

const preferredTranslationProviders = [
  "tencent transmart web",
  "deepl web",
  "bing translate web",
  "google translate web",
  "sogou translate web",
  "baidu translation",
];
const preferredTranslationProviderOrder = new Map(preferredTranslationProviders.map((name, index) => [name, index]));

export function sortTranslationProviders<T>(providers: readonly T[], providerName: (provider: T) => string): T[] {
  return [...providers].sort((first, second) => {
    const firstOrder = preferredTranslationProviderOrder.get(providerName(first).trim().toLowerCase()) ?? preferredTranslationProviders.length;
    const secondOrder = preferredTranslationProviderOrder.get(providerName(second).trim().toLowerCase()) ?? preferredTranslationProviders.length;
    return firstOrder - secondOrder;
  });
}

export function translationProviderDisplayName(name: string, language: string): string {
  if (!language.toLowerCase().startsWith("zh")) return name;
  return chineseTranslationProviderNames[name.trim().toLowerCase()] || name;
}
