import FileUtils from "./file";

export const IMAGE_DOCUMENT_FILE_TYPES = [
  "jpg",
  "jpeg",
  "png",
  "gif",
  "bmp",
  "webp",
  "tiff",
  "tif",
];

export const DETAIL_UNSUPPORTED_FILE_TYPES: string[] = [];

export function isDocumentDetailUnsupported(fileName?: string) {
  const suffix = FileUtils.getSuffix(fileName || "");
  return DETAIL_UNSUPPORTED_FILE_TYPES.includes(suffix);
}

export function isImageDocument(fileName?: string) {
  const suffix = FileUtils.getSuffix(fileName || "");
  return IMAGE_DOCUMENT_FILE_TYPES.includes(suffix);
}
