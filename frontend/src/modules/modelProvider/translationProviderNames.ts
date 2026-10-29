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

export function translationProviderDisplayName(name: string, language: string): string {
  if (!language.toLowerCase().startsWith("zh")) return name;
  return chineseTranslationProviderNames[name.trim().toLowerCase()] || name;
}
