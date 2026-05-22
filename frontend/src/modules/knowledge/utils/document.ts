import FileUtils from "./file";

export const DETAIL_UNSUPPORTED_FILE_TYPES = [
  "jpg",
  "jpeg",
  "png",
  "gif",
  "bmp",
  "webp",
  "tiff",
  "tif",
  "mp3",
  "mp4",
];

export function isDocumentDetailUnsupported(fileName?: string) {
  const suffix = FileUtils.getSuffix(fileName || "");
  return DETAIL_UNSUPPORTED_FILE_TYPES.includes(suffix);
}
