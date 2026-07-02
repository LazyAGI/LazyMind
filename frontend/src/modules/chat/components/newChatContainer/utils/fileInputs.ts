import { RcFile } from "antd/es/upload";
import type { ChatImage } from "../../ChatImages";

export function getFileUrls(
  files: (RcFile & { uri: string })[] | undefined,
  images?: ChatImage[],
) {
  if (!files) {
    return [];
  }

  return files.map((file) => ({
    uri: file.uri,
    base64: images ? images.find((image) => image.uid === file.uid)?.base64 : "",
  }));
}
