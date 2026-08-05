import type { TFunction } from "i18next";

export const SHOWCASE_ALL_CATEGORY = "全部";

const CATEGORY_KEYS: Record<string, string> = {
  [SHOWCASE_ALL_CATEGORY]: "all",
  "调研分析": "research",
  "数据分析": "dataAnalysis",
  "PPT 制作": "ppt",
  "文档写作": "document",
  "内容创作": "content",
  "图片设计": "design",
  "网页制作": "web",
  "办公效率": "productivity",
};

export function translateShowcaseCategory(t: TFunction, category: string) {
  const key = CATEGORY_KEYS[category];
  return key
    ? t(`showcase.categories.${key}`, { defaultValue: category })
    : category;
}
