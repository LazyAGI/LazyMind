import {
  DATA_SOURCE_FILE_TYPE_OPTIONS,
  DEFAULT_DATA_SOURCE_FILE_TYPES,
} from "../constants/options";
import type { DataSourceFileType, SourceFormValues } from "../constants/types";
import type { ScanV2Binding } from "./scanAccessors";

const LEGACY_DATA_SOURCE_FILE_TYPE_MAP: Record<string, DataSourceFileType[]> = {
  word: ["doc", "docx"],
  excel: ["xls", "xlsx", "csv"],
  powerpoint: ["ppt", "pptx", "pptm"],
  image: ["jpg", "jpeg", "png", "gif", "bmp", "webp", "tiff", "tif"],
  notebook: ["ipynb"],
  ebook: ["epub"],
  markdown: ["md"],
  mailbox: ["mbox"],
  audio: ["mp3"],
  video: ["mp4"],
  text: ["txt"],
};

export function normalizeDataSourceFileTypes(value?: SourceFormValues["fileTypes"]) {
  const allowedTypes = new Set(DATA_SOURCE_FILE_TYPE_OPTIONS.map((item) => item.value));
  const values = Array.isArray(value) ? value : [];
  const normalizedValues = Array.from(
    new Set(
      values
        .flatMap((item) => {
          const normalizedItem = `${item || ""}`.trim().toLowerCase();
          return LEGACY_DATA_SOURCE_FILE_TYPE_MAP[normalizedItem] || normalizedItem;
        })
        .map((item) => item as DataSourceFileType)
        .filter((item) => allowedTypes.has(item)),
    ),
  );
  return normalizedValues.length > 0 ? normalizedValues : DEFAULT_DATA_SOURCE_FILE_TYPES;
}

export function getDataSourceFileTypeExtensions(value?: SourceFormValues["fileTypes"]) {
  const selectedTypes = new Set(normalizeDataSourceFileTypes(value));
  return DATA_SOURCE_FILE_TYPE_OPTIONS.filter((item) => selectedTypes.has(item.value)).flatMap(
    (item) => item.extensions,
  );
}

export function getDataSourceFileTypeIncludePatterns(value?: SourceFormValues["fileTypes"]) {
  return getDataSourceFileTypeExtensions(value).map((extension) => `**/*.${extension}`);
}

export function getExtensionsFromIncludePatterns(value: unknown) {
  const patterns = Array.isArray(value) ? value : [];
  return patterns
    .map((pattern) => `${pattern || ""}`.trim().toLowerCase())
    .map((pattern) => pattern.match(/\.([a-z0-9]+)$/)?.[1] || "")
    .filter(Boolean);
}

export function getBindingFileTypes(
  binding?: ScanV2Binding | null,
  fallbackTypes?: DataSourceFileType[],
) {
  const providerOptions = (binding?.provider_options || {}) as Record<string, unknown>;
  const rawExtensions = [
    ...((binding?.include_extensions || []) as string[]),
    ...((providerOptions.include_extensions || []) as string[]),
    ...getExtensionsFromIncludePatterns(providerOptions.include_patterns),
  ];
  const extensionSet = new Set(
    rawExtensions.map((extension) => `${extension || ""}`.replace(/^\./, "").toLowerCase()),
  );

  if (extensionSet.size === 0) {
    return fallbackTypes || DEFAULT_DATA_SOURCE_FILE_TYPES;
  }

  const fileTypes = DATA_SOURCE_FILE_TYPE_OPTIONS.filter((item) =>
    item.extensions.some((extension) => extensionSet.has(extension)),
  ).map((item) => item.value);
  return fileTypes.length > 0 ? fileTypes : fallbackTypes || DEFAULT_DATA_SOURCE_FILE_TYPES;
}
