import i18n from "@/i18n";

export const tr = (key: string, values?: Record<string, unknown>): string =>
  String(i18n.t(`sourceImports.${key}`, values));
