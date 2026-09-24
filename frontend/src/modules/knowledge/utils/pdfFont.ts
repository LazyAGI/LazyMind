import { axiosInstance, BASE_URL } from "@/components/request";

let fontPromise: Promise<string> | undefined;

export function getCjkFont(): Promise<string> {
  if (!fontPromise) {
    fontPromise = (async () => {
      let buffer: ArrayBuffer;
      if (import.meta.env.VITE_LAZYMIND_MODE === "desktop") {
        const response = await axiosInstance.get<ArrayBuffer>(`${BASE_URL}/api/core/system-dependencies/pdf-font`, {
          responseType: "arraybuffer", timeout: 190000,
        });
        buffer = response.data;
      } else {
        const response = await fetch("/fonts/NotoSansSC-wght.ttf");
        if (!response.ok) throw new Error("无法加载 PDF 中文字体");
        buffer = await response.arrayBuffer();
      }
      const bytes = new Uint8Array(buffer);
      let binary = "";
      for (let start = 0; start < bytes.length; start += 0x8000) {
        binary += String.fromCharCode(...bytes.subarray(start, start + 0x8000));
      }
      return binary;
    })().catch(() => {
      fontPromise = undefined;
      throw new Error("PDF 中文字体准备失败，请检查网络后重试；首次导出需要联网下载字体。");
    });
  }
  return fontPromise;
}
